package server

import (
	"errors"
	"math"
	"math/rand"
	"sync"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

// Aquatic mobs constants, entity type IDs, SynchedEntityData indices, and sounds
// for Minecraft 26.1.2 (protocol 775):
//   - Dolphin (minecraft:dolphin, ID 35)
//   - Turtle (minecraft:turtle, ID 137)
//   - Squid (minecraft:squid, ID 127) & Glow Squid (minecraft:glow_squid, ID 61)
//   - Fish: Cod (minecraft:cod, ID 27), Salmon (minecraft:salmon, ID 110),
//     Tropical Fish (minecraft:tropical_fish, ID 136), Pufferfish (minecraft:pufferfish, ID 107)
//   - Guardian (minecraft:guardian, ID 63) & Elder Guardian (minecraft:elder_guardian, ID 40)
const (
	MobTypeDolphin       = "minecraft:dolphin"
	MobTypeTurtle        = "minecraft:turtle"
	MobTypeSquid         = "minecraft:squid"
	MobTypeGlowSquid     = "minecraft:glow_squid"
	MobTypeCod           = "minecraft:cod"
	MobTypeSalmon        = "minecraft:salmon"
	MobTypeTropicalFish  = "minecraft:tropical_fish"
	MobTypePufferfish    = "minecraft:pufferfish"
	MobTypeGuardian      = "minecraft:guardian"
	MobTypeElderGuardian = "minecraft:elder_guardian"

	// Protocol 775 (Minecraft 26.1.2) BuiltInRegistries.ENTITY_TYPE numeric IDs.
	EntityTypeCodID           = 27
	EntityTypeDolphinID       = 35
	EntityTypeElderGuardianID = 40
	EntityTypeGlowSquidID     = 61
	EntityTypeGuardianID      = 63
	EntityTypePufferfishID    = 107
	EntityTypeSalmonID        = 110
	EntityTypeSquidID         = 127
	EntityTypeTropicalFishID  = 136
	EntityTypeTurtleID        = 137

	// Default max healths in vanilla 26.1.2.
	DefaultDolphinMaxHealth       float32 = 10.0
	DefaultTurtleMaxHealth        float32 = 30.0
	DefaultSquidMaxHealth         float32 = 10.0
	DefaultGlowSquidMaxHealth     float32 = 10.0
	DefaultCodMaxHealth           float32 = 3.0
	DefaultSalmonMaxHealth        float32 = 3.0
	DefaultTropicalFishMaxHealth  float32 = 3.0
	DefaultPufferfishMaxHealth    float32 = 3.0
	DefaultGuardianMaxHealth      float32 = 30.0
	DefaultElderGuardianMaxHealth float32 = 80.0

	// Swim & land movement speeds.
	DolphinSwimSpeed       float64 = 0.22
	TurtleSwimSpeed        float64 = 0.14
	TurtleLandSpeed        float64 = 0.05
	SquidJetSpeed          float64 = 0.16
	FishSwimSpeed          float64 = 0.12
	GuardianSwimSpeed      float64 = 0.14
	ElderGuardianSwimSpeed float64 = 0.10

	// Dolphin constants (Dolphin.java).
	DolphinMaxAirSupplyTicks      int     = 4800 // 4 minutes (4800 ticks) max air before drowning underwater
	DolphinMaxMoistnessTicks      int     = 2400 // 2 minutes (2400 ticks) on land before drying out
	DolphinGraceRange             float64 = 15.0 // Dolphin's Grace aura radius around swimming player
	DolphinGraceDurationTicks     int32   = 100  // 5 seconds (100 ticks) refreshed while swimming near dolphin
	DolphinTreasureStopDist       float64 = 4.0  // Distance at which dolphin stops escorting near treasure
	DolphinPlayItemRange          float64 = 3.5  // Range to nudge/toss floating items in water
	DolphinPlayItemCooldownTicks  int     = 40   // 2 seconds between item tosses

	// Turtle constants (Turtle.java & TurtleEggBlock.java).
	TurtleBreedingFoodItemID   = "minecraft:seagrass"
	TurtleScuteItemID          = "minecraft:turtle_scute"
	LegacyScuteItemID          = "minecraft:scute"
	TurtleEggBlockName         = "minecraft:turtle_egg"
	TurtleEggItemID            = "minecraft:turtle_egg"
	TurtleLayEggDigTicks       int = 200 // 10 seconds digging on home sand beach before laying eggs
	TurtleMaxEggsPerBlock      int = 4
	TurtleEggMaxHatchStage     int = 2 // hatch=0 -> 1 -> 2 -> hatch baby turtles

	// Squid & Glow Squid constants (Squid.java & GlowSquid.java).
	SquidMaxOutOfWaterTicks     int     = 300   // 15 seconds (300 ticks) out of water before suffocation damage
	GlowSquidDarkDurationTicks  int32   = 100   // 5 seconds (100 ticks) dark after taking damage
	SquidInkBlindnessRadius     float64 = 3.5   // Radius within which attacker receives Blindness from ink cloud
	SquidInkBlindnessTicks      int32   = 80    // 4 seconds (80 ticks) Blindness on ink squirt
	InkSacItemID                string  = "minecraft:ink_sac"
	GlowInkSacItemID            string  = "minecraft:glow_ink_sac"

	// Fish & Pufferfish constants (AbstractSchoolingFish.java & Pufferfish.java).
	FishMaxSchoolSize           int     = 9   // 1 leader + up to 8 followers
	FishSchoolRange             float64 = 8.0 // Range to join/follow school leader
	FishMaxOutOfWaterTicks      int     = 300 // 15 seconds (300 ticks) flopping on land before suffocating
	FishFlopVelocityY           float64 = 0.2 // Upward bounce impulse when flopping on land
	PufferfishDeflated          int32   = 0
	PufferfishHalfPuffed        int32   = 1
	PufferfishFullyPuffed       int32   = 2
	PufferfishDetectRange       float64 = 2.5  // Proximity radius triggering inflation & poison sting
	PufferfishDeflateDelayTicks int     = 60   // Ticks before stepping down puff state when threat leaves
	PufferfishStingCooldown     int     = 20   // 1s cooldown between stings
	PufferfishPoisonBaseTicks   int32   = 60   // 3s Poison I (60 ticks) at half-puffed, 6s (120 ticks) at fully puffed

	// Fish bucket item IDs.
	CodBucketItemID          = "minecraft:cod_bucket"
	SalmonBucketItemID       = "minecraft:salmon_bucket"
	PufferfishBucketItemID   = "minecraft:pufferfish_bucket"
	// TropicalFishBucketItemID is already defined in axolotl.go ("minecraft:tropical_fish_bucket").

	// Salmon size variant names (Salmon.Variant in 26.1.2).
	SalmonVariantSmall  = "small"
	SalmonVariantMedium = "medium"
	SalmonVariantLarge  = "large"

	// Guardian & Elder Guardian constants (Guardian.java & ElderGuardian.java).
	GuardianLaserRange                  float64 = 15.0
	GuardianLaserChargeTicks            int     = 80   // 4 seconds (80 ticks) laser charge for Guardian
	ElderGuardianLaserChargeTicks       int     = 60   // 3 seconds (60 ticks) laser charge for Elder Guardian
	GuardianLaserDamage                 float32 = 6.0  // Base laser damage for Guardian
	ElderGuardianLaserDamage            float32 = 8.0  // Base laser damage for Elder Guardian
	GuardianThornsDamage                float32 = 2.0  // Spike retaliation damage when attacked while stationary
	ElderGuardianFatigueIntervalTicks   int     = 1200 // Every 60 seconds (1200 ticks)
	ElderGuardianFatigueRadius          float64 = 50.0 // 50-block aura radius
	ElderGuardianFatigueDurationTicks   int32   = 6000 // 5 minutes (6000 ticks) Mining Fatigue III
	ElderGuardianFatigueAmplifier       int32   = 2    // Level III (amplifier 2)
	PrismarineShardItemID               string  = "minecraft:prismarine_shard"
	PrismarineCrystalsItemID            string  = "minecraft:prismarine_crystals"
	WetSpongeItemID                     string  = "minecraft:wet_sponge"

	// SynchedEntityData indices for Aquatic Mobs in Minecraft 26.1.2 (protocol 775):
	// GlowSquid extends Squid -> AgeableWaterCreature -> PathfinderMob -> Mob -> LivingEntity -> Entity:
	EntityDataGlowSquidDarkTicksID byte = 16 // GlowSquid.DATA_DARK_TICKS_REMAINING (INT serializer = 1)
	// AbstractFish: DATA_FROM_BUCKET = 16 (BOOLEAN); Pufferfish.PUFF_STATE = 17 (INT); TropicalFish.DATA_ID_TYPE_VARIANT = 17 (INT).
	EntityDataFishFromBucketID      byte = 16 // AbstractFish.FROM_BUCKET (BOOLEAN serializer = 8)
	EntityDataPufferfishPuffStateID byte = 17 // Pufferfish.PUFF_STATE (INT serializer = 1)
	EntityDataTropicalFishVariantID byte = 17 // TropicalFish.DATA_ID_TYPE_VARIANT (INT serializer = 1)
	// Guardian extends Monster -> PathfinderMob -> Mob -> LivingEntity -> Entity:
	EntityDataGuardianMovingID       byte = 16 // Guardian.DATA_ID_MOVING (BOOLEAN serializer = 8)
	EntityDataGuardianAttackTargetID byte = 17 // Guardian.DATA_ID_ATTACK_TARGET (INT serializer = 1)

	// Canonical Aquatic Mob sound event identifiers.
	SoundDolphinAmbient      = "minecraft:entity.dolphin.ambient"
	SoundDolphinAmbientWater = "minecraft:entity.dolphin.ambient_water"
	SoundDolphinAttack       = "minecraft:entity.dolphin.attack"
	SoundDolphinDeath        = "minecraft:entity.dolphin.death"
	SoundDolphinEat          = "minecraft:entity.dolphin.eat"
	SoundDolphinHurt         = "minecraft:entity.dolphin.hurt"
	SoundDolphinJump         = "minecraft:entity.dolphin.jump"
	SoundDolphinPlay         = "minecraft:entity.dolphin.play"
	SoundDolphinSplash       = "minecraft:entity.dolphin.splash"
	SoundDolphinSwim         = "minecraft:entity.dolphin.swim"

	SoundTurtleAmbientLand  = "minecraft:entity.turtle.ambient_land"
	SoundTurtleDeath        = "minecraft:entity.turtle.death"
	SoundTurtleDeathBaby    = "minecraft:entity.turtle.death_baby"
	SoundTurtleEggBreak     = "minecraft:entity.turtle.egg_break"
	SoundTurtleEggCrack     = "minecraft:entity.turtle.egg_crack"
	SoundTurtleEggHatch     = "minecraft:entity.turtle.egg_hatch"
	SoundTurtleHurt         = "minecraft:entity.turtle.hurt"
	SoundTurtleHurtBaby     = "minecraft:entity.turtle.hurt_baby"
	SoundTurtleLayEgg       = "minecraft:entity.turtle.lay_egg"
	SoundTurtleShamble      = "minecraft:entity.turtle.shamble"
	SoundTurtleShambleBaby  = "minecraft:entity.turtle.shamble_baby"
	SoundTurtleSwim         = "minecraft:entity.turtle.swim"

	SoundSquidAmbient     = "minecraft:entity.squid.ambient"
	SoundSquidDeath       = "minecraft:entity.squid.death"
	SoundSquidHurt        = "minecraft:entity.squid.hurt"
	SoundSquidSquirt      = "minecraft:entity.squid.squirt"
	SoundGlowSquidAmbient = "minecraft:entity.glow_squid.ambient"
	SoundGlowSquidDeath   = "minecraft:entity.glow_squid.death"
	SoundGlowSquidHurt    = "minecraft:entity.glow_squid.hurt"
	SoundGlowSquidSquirt  = "minecraft:entity.glow_squid.squirt"

	SoundCodDeath              = "minecraft:entity.cod.death"
	SoundCodFlop               = "minecraft:entity.cod.flop"
	SoundCodHurt               = "minecraft:entity.cod.hurt"
	SoundSalmonDeath           = "minecraft:entity.salmon.death"
	SoundSalmonFlop            = "minecraft:entity.salmon.flop"
	SoundSalmonHurt            = "minecraft:entity.salmon.hurt"
	SoundTropicalFishDeath     = "minecraft:entity.tropical_fish.death"
	SoundTropicalFishFlop      = "minecraft:entity.tropical_fish.flop"
	SoundTropicalFishHurt      = "minecraft:entity.tropical_fish.hurt"
	SoundPufferfishBlowOut     = "minecraft:entity.puffer_fish.blow_out"
	SoundPufferfishBlowUp      = "minecraft:entity.puffer_fish.blow_up"
	SoundPufferfishDeath       = "minecraft:entity.puffer_fish.death"
	SoundPufferfishFlop        = "minecraft:entity.puffer_fish.flop"
	SoundPufferfishHurt        = "minecraft:entity.puffer_fish.hurt"
	SoundPufferfishSting       = "minecraft:entity.puffer_fish.sting"
	SoundBucketFillFish        = "minecraft:item.bucket.fill_fish"
	SoundBucketEmptyFish       = "minecraft:item.bucket.empty_fish"

	SoundGuardianAmbient      = "minecraft:entity.guardian.ambient"
	SoundGuardianAmbientLand  = "minecraft:entity.guardian.ambient_land"
	SoundGuardianAttack       = "minecraft:entity.guardian.attack"
	SoundGuardianDeath        = "minecraft:entity.guardian.death"
	SoundGuardianDeathLand    = "minecraft:entity.guardian.death_land"
	SoundGuardianFlop         = "minecraft:entity.guardian.flop"
	SoundGuardianHurt         = "minecraft:entity.guardian.hurt"
	SoundGuardianHurtLand     = "minecraft:entity.guardian.hurt_land"
	SoundElderGuardianAmbient = "minecraft:entity.elder_guardian.ambient"
	SoundElderGuardianCurse   = "minecraft:entity.elder_guardian.curse"
	SoundElderGuardianDeath   = "minecraft:entity.elder_guardian.death"
	SoundElderGuardianHurt    = "minecraft:entity.elder_guardian.hurt"
)

// DolphinBehaviorState holds the server-side state for a `minecraft:dolphin`.
type DolphinBehaviorState struct {
	EntityID         int32
	GotFish          bool
	CanFindTreasure  bool
	TreasureX        int
	TreasureY        int
	TreasureZ        int
	HasTreasurePos   bool
	AirSupplyTicks   int
	MoistnessTicks   int
	PlayItemCooldown int
}

// TurtleBehaviorState holds the server-side state for a `minecraft:turtle`.
type TurtleBehaviorState struct {
	EntityID       int32
	HomeX          int
	HomeY          int
	HomeZ          int
	HasEgg         bool
	TravellingHome bool
	LayingEgg      bool
	LayEggCounter  int
	IsBaby         bool
}

// SquidBehaviorState holds the server-side state for a `minecraft:squid` or
// `minecraft:glow_squid`.
type SquidBehaviorState struct {
	EntityID           int32
	IsGlowSquid        bool
	DarkTicksRemaining int32
	OutOfWaterTicks    int
	SwimTimer          int
	SwimDirX           float64
	SwimDirY           float64
	SwimDirZ           float64
}

// FishBehaviorState holds the server-side state for `minecraft:cod`,
// `minecraft:salmon`, `minecraft:tropical_fish`, and `minecraft:pufferfish`.
type FishBehaviorState struct {
	EntityID           int32
	FishType           string
	FromBucket         bool
	SchoolLeaderID     int32
	OutOfWaterTicks    int
	SalmonSize         string // "small", "medium", "large"
	TropicalVariant    int32  // Packed 32-bit variant (size | pattern<<8 | baseColor<<16 | patternColor<<24)
	PuffState          int32  // 0 = deflated, 1 = half-puffed, 2 = fully puffed
	DeflateTimer       int
	StingCooldownTicks int
}

// GuardianBehaviorState holds the server-side state for a `minecraft:guardian`
// or `minecraft:elder_guardian`.
type GuardianBehaviorState struct {
	EntityID            int32
	IsElder             bool
	Moving              bool
	AttackTargetID      int32
	LaserChargeTicks    int
	FatigueCooldown     int
	OutOfWaterTicks     int
	SwimTimer           int
	SwimDirX            float64
	SwimDirY            float64
	SwimDirZ            float64
	HasHome             bool
	HomeX               float64
	HomeY               float64
	HomeZ               float64
	HomeRadius          float64
	PersistenceRequired bool
}

// TurtleEggPlaceHook is an optional callback invoked when a pregnant turtle
// finishes digging on a sand beach and places 1..4 `minecraft:turtle_egg`
// blocks at (x, y, z) in dimension.
type TurtleEggPlaceHook func(dimension string, x, y, z int, eggCount int) bool

type bucketedFishInfo struct {
	FishType        string
	SalmonSize      string
	TropicalVariant int32
	Health          float32
}

type serverAquaticManager struct {
	mu                sync.RWMutex
	dolphins          map[int32]*DolphinBehaviorState
	turtles           map[int32]*TurtleBehaviorState
	squids            map[int32]*SquidBehaviorState
	fish              map[int32]*FishBehaviorState
	guardians         map[int32]*GuardianBehaviorState
	bucketedFish      map[*PlayerSession]bucketedFishInfo
	turtleEggHook     TurtleEggPlaceHook
}

var aquaticManagers sync.Map // map[*Server]*serverAquaticManager

func (s *Server) aquaticManager() *serverAquaticManager {
	if s == nil {
		return &serverAquaticManager{
			dolphins:     make(map[int32]*DolphinBehaviorState),
			turtles:      make(map[int32]*TurtleBehaviorState),
			squids:       make(map[int32]*SquidBehaviorState),
			fish:         make(map[int32]*FishBehaviorState),
			guardians:    make(map[int32]*GuardianBehaviorState),
			bucketedFish: make(map[*PlayerSession]bucketedFishInfo),
		}
	}
	if v, ok := aquaticManagers.Load(s); ok {
		return v.(*serverAquaticManager)
	}
	mgr := &serverAquaticManager{
		dolphins:     make(map[int32]*DolphinBehaviorState),
		turtles:      make(map[int32]*TurtleBehaviorState),
		squids:       make(map[int32]*SquidBehaviorState),
		fish:         make(map[int32]*FishBehaviorState),
		guardians:    make(map[int32]*GuardianBehaviorState),
		bucketedFish: make(map[*PlayerSession]bucketedFishInfo),
	}
	actual, _ := aquaticManagers.LoadOrStore(s, mgr)
	return actual.(*serverAquaticManager)
}

// IsAquaticMob reports whether typeName is one of the aquatic mob types in
// vanilla 26.1.2.
func IsAquaticMob(typeName string) bool {
	switch canonicalResourceName(typeName) {
	case MobTypeDolphin, MobTypeTurtle, MobTypeSquid, MobTypeGlowSquid,
		MobTypeCod, MobTypeSalmon, MobTypeTropicalFish, MobTypePufferfish,
		MobTypeGuardian, MobTypeElderGuardian, MobTypeAxolotl, MobTypeTadpole:
		return true
	default:
		return false
	}
}

// IsSchoolingFishType reports whether typeName is a schooling fish species
// (`minecraft:cod`, `minecraft:salmon`, or `minecraft:tropical_fish`).
func IsSchoolingFishType(typeName string) bool {
	switch canonicalResourceName(typeName) {
	case MobTypeCod, MobTypeSalmon, MobTypeTropicalFish:
		return true
	default:
		return false
	}
}

// IsFishMobType reports whether typeName is one of the 4 bucketable fish mobs
// (`cod`, `salmon`, `tropical_fish`, `pufferfish`).
func IsFishMobType(typeName string) bool {
	switch canonicalResourceName(typeName) {
	case MobTypeCod, MobTypeSalmon, MobTypeTropicalFish, MobTypePufferfish:
		return true
	default:
		return false
	}
}

// IsDolphinFoodItem reports whether itemName is a raw fish item that can feed
// a dolphin (`minecraft:cod` or `minecraft:salmon`) to gain its trust and
// trigger treasure escorting.
func IsDolphinFoodItem(itemName string) bool {
	switch canonicalResourceName(itemName) {
	case "minecraft:cod", "minecraft:salmon":
		return true
	default:
		return false
	}
}

// IsTurtleBreedingFoodItem reports whether itemName is `minecraft:seagrass`,
// used to breed adult turtles and accelerate baby turtle growth.
func IsTurtleBreedingFoodItem(itemName string) bool {
	return canonicalResourceName(itemName) == TurtleBreedingFoodItemID
}

// PackTropicalFishVariant packs a tropical fish's size (0=small, 1=large),
// pattern (0..5), base dye color (0..15), and pattern dye color (0..15) into
// the 32-bit integer format used by `TropicalFish.DATA_ID_TYPE_VARIANT`.
func PackTropicalFishVariant(size, pattern, baseColor, patternColor int) int32 {
	return int32((size & 0xFF) | ((pattern & 0xFF) << 8) | ((baseColor & 0xFF) << 16) | ((patternColor & 0xFF) << 24))
}

// UnpackTropicalFishVariant unpacks a 32-bit tropical fish variant integer into
// size (0..1), pattern (0..5), baseColor (0..15), and patternColor (0..15).
func UnpackTropicalFishVariant(variant int32) (size, pattern, baseColor, patternColor int) {
	u := uint32(variant)
	return int(u & 0xFF), int((u >> 8) & 0xFF), int((u >> 16) & 0xFF), int((u >> 24) & 0xFF)
}

// EncodeGlowSquidDataPacket encodes a ClientboundSetEntityDataPacket (0x63)
// updating `GlowSquid.DATA_DARK_TICKS_REMAINING` (index 16, INT serializer 1).
func EncodeGlowSquidDataPacket(entityID, darkTicks int32) []byte {
	w := protocol.NewWriter(16)
	w.VarInt(entityID)
	w.Byte(EntityDataGlowSquidDarkTicksID)
	w.VarInt(EntityDataSerializerInt)
	w.VarInt(darkTicks)
	w.Byte(0xFF)
	return w.Bytes()
}

// DecodeGlowSquidDataPacket decodes a ClientboundSetEntityDataPacket produced
// by EncodeGlowSquidDataPacket.
func DecodeGlowSquidDataPacket(r *protocol.Reader) (entityID, darkTicks int32, err error) {
	entityID, err = r.VarInt()
	if err != nil {
		return 0, 0, err
	}
	idx, err := r.ReadByte()
	if err != nil {
		return 0, 0, err
	}
	if idx != EntityDataGlowSquidDarkTicksID {
		return 0, 0, errors.New("aquatic: unexpected glow squid data index")
	}
	ser, err := r.VarInt()
	if err != nil {
		return 0, 0, err
	}
	if ser != EntityDataSerializerInt {
		return 0, 0, errors.New("aquatic: unexpected glow squid serializer")
	}
	darkTicks, err = r.VarInt()
	if err != nil {
		return 0, 0, err
	}
	end, err := r.ReadByte()
	if err != nil {
		return 0, 0, err
	}
	if end != 0xFF {
		return 0, 0, errors.New("aquatic: missing SynchedEntityData end byte")
	}
	return entityID, darkTicks, nil
}

// EncodePufferfishDataPacket encodes a ClientboundSetEntityDataPacket (0x63)
// updating `AbstractFish.FROM_BUCKET` (index 16, BOOLEAN) and
// `Pufferfish.PUFF_STATE` (index 17, INT: 0..2).
func EncodePufferfishDataPacket(entityID int32, fromBucket bool, puffState int32) []byte {
	w := protocol.NewWriter(20)
	w.VarInt(entityID)
	w.Byte(EntityDataFishFromBucketID)
	w.VarInt(EntityDataSerializerBool)
	w.Bool(fromBucket)
	w.Byte(EntityDataPufferfishPuffStateID)
	w.VarInt(EntityDataSerializerInt)
	w.VarInt(puffState)
	w.Byte(0xFF)
	return w.Bytes()
}

// DecodePufferfishDataPacket decodes a ClientboundSetEntityDataPacket produced
// by EncodePufferfishDataPacket.
func DecodePufferfishDataPacket(r *protocol.Reader) (entityID int32, fromBucket bool, puffState int32, err error) {
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
		case EntityDataFishFromBucketID:
			if ser != EntityDataSerializerBool {
				return 0, false, 0, errors.New("aquatic: unexpected fish from_bucket serializer")
			}
			fromBucket, err = r.Bool()
			if err != nil {
				return 0, false, 0, err
			}
		case EntityDataPufferfishPuffStateID:
			if ser != EntityDataSerializerInt {
				return 0, false, 0, errors.New("aquatic: unexpected pufferfish puff_state serializer")
			}
			puffState, err = r.VarInt()
			if err != nil {
				return 0, false, 0, err
			}
		default:
			return 0, false, 0, errors.New("aquatic: unexpected pufferfish data index")
		}
	}
	return entityID, fromBucket, puffState, nil
}

// EncodeGuardianDataPacket encodes a ClientboundSetEntityDataPacket (0x63)
// updating `Guardian.DATA_ID_MOVING` (index 16, BOOLEAN) and
// `Guardian.DATA_ID_ATTACK_TARGET` (index 17, INT).
func EncodeGuardianDataPacket(entityID int32, moving bool, attackTargetID int32) []byte {
	w := protocol.NewWriter(20)
	w.VarInt(entityID)
	w.Byte(EntityDataGuardianMovingID)
	w.VarInt(EntityDataSerializerBool)
	w.Bool(moving)
	w.Byte(EntityDataGuardianAttackTargetID)
	w.VarInt(EntityDataSerializerInt)
	w.VarInt(attackTargetID)
	w.Byte(0xFF)
	return w.Bytes()
}

// DecodeGuardianDataPacket decodes a ClientboundSetEntityDataPacket produced
// by EncodeGuardianDataPacket.
func DecodeGuardianDataPacket(r *protocol.Reader) (entityID int32, moving bool, attackTargetID int32, err error) {
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
		case EntityDataGuardianMovingID:
			if ser != EntityDataSerializerBool {
				return 0, false, 0, errors.New("aquatic: unexpected guardian moving serializer")
			}
			moving, err = r.Bool()
			if err != nil {
				return 0, false, 0, err
			}
		case EntityDataGuardianAttackTargetID:
			if ser != EntityDataSerializerInt {
				return 0, false, 0, errors.New("aquatic: unexpected guardian target serializer")
			}
			attackTargetID, err = r.VarInt()
			if err != nil {
				return 0, false, 0, err
			}
		default:
			return 0, false, 0, errors.New("aquatic: unexpected guardian data index")
		}
	}
	return entityID, moving, attackTargetID, nil
}

// isEntityInWater checks via `s.EntitySubmergedFluidDepth` (`cache.FluidHeightAt`)
// whether the entity at `(x, y, z)` in `dimension` has its feet submerged in water.
func (s *Server) isEntityInWater(dimension string, x, y, z float64) bool {
	if s == nil {
		return false
	}
	_, fh, kind, submerged := s.EntitySubmergedFluidDepth(dimension, x, y, z)
	return submerged && fh > 0 && kind == world.FluidWater
}

// isEntityEyesUnderWater checks whether the entity's eye level (`y + eyeOffset`)
// is submerged in water (`cache.FluidHeightAt`).
func (s *Server) isEntityEyesUnderWater(dimension string, x, y, z, eyeOffset float64) bool {
	return s.isEntityInWater(dimension, x, y+eyeOffset, z)
}

// ============================================================================
// 1. DOLPHINS (minecraft:dolphin)
// ============================================================================

// SpawnDolphin spawns a `minecraft:dolphin` at `(x, y, z)` in `dimension` with
// full air supply (4800 ticks) and moistness (2400 ticks).
func (s *Server) SpawnDolphin(dimension string, x, y, z float64) (int32, error) {
	if s == nil || s.entities == nil {
		return 0, errors.New("server: nil entity manager")
	}
	normDim := world.NormalizeDimension(dimension)
	id := s.entities.Add(&world.Entity{
		TypeID:    ResolveEntityTypeID(MobTypeDolphin),
		TypeName:  MobTypeDolphin,
		Dimension: normDim,
		X:         x,
		Y:         y,
		Z:         z,
		OnGround:  false,
	})
	s.SetMobHealth(id, DefaultDolphinMaxHealth)

	mgr := s.aquaticManager()
	mgr.mu.Lock()
	mgr.dolphins[id] = &DolphinBehaviorState{
		EntityID:       id,
		AirSupplyTicks: DolphinMaxAirSupplyTicks,
		MoistnessTicks: DolphinMaxMoistnessTicks,
	}
	mgr.mu.Unlock()

	ent := s.entities.Get(id)
	if ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, ent))
	}
	return id, nil
}

// GetDolphinState returns a snapshot of the DolphinBehaviorState for entityID.
func (s *Server) GetDolphinState(entityID int32) (DolphinBehaviorState, bool) {
	if s == nil {
		return DolphinBehaviorState{}, false
	}
	mgr := s.aquaticManager()
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()
	st, ok := mgr.dolphins[entityID]
	if !ok || st == nil {
		return DolphinBehaviorState{}, false
	}
	return *st, true
}

// SetDolphinTreasureTarget configures the target buried treasure / shipwreck /
// ocean ruin coordinates that a fed dolphin escorts players toward.
func (s *Server) SetDolphinTreasureTarget(entityID int32, tx, ty, tz int) bool {
	if s == nil {
		return false
	}
	mgr := s.aquaticManager()
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	st := mgr.dolphins[entityID]
	if st == nil {
		return false
	}
	st.TreasureX = tx
	st.TreasureY = ty
	st.TreasureZ = tz
	st.HasTreasurePos = true
	return true
}

// SetDolphinAirAndMoistness sets a dolphin's current air supply and moistness
// ticks (used by tests and NBT restore).
func (s *Server) SetDolphinAirAndMoistness(entityID int32, airTicks, moistnessTicks int) bool {
	if s == nil {
		return false
	}
	mgr := s.aquaticManager()
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	st := mgr.dolphins[entityID]
	if st == nil {
		return false
	}
	st.AirSupplyTicks = airTicks
	st.MoistnessTicks = moistnessTicks
	return true
}

// InteractWithDolphin handles feeding a `minecraft:dolphin` raw fish
// (`minecraft:cod` or `minecraft:salmon`), setting `GotFish = true` and
// `CanFindTreasure = true`, selecting a treasure/shipwreck escort target if not
// already set, and granting `EffectDolphinsGrace` to the player.
func (s *Server) InteractWithDolphin(session *PlayerSession, entityID int32) (fed bool) {
	if s == nil || s.entities == nil || session == nil || session.IsDead() {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeDolphin {
		return false
	}
	held := session.HeldItem()
	if held.IsEmpty() || !IsDolphinFoodItem(held.ResolvedID()) {
		return false
	}

	mgr := s.aquaticManager()
	mgr.mu.Lock()
	st := mgr.dolphins[entityID]
	if st == nil {
		st = &DolphinBehaviorState{
			EntityID:       entityID,
			AirSupplyTicks: DolphinMaxAirSupplyTicks,
			MoistnessTicks: DolphinMaxMoistnessTicks,
		}
		mgr.dolphins[entityID] = st
	}
	st.GotFish = true
	st.CanFindTreasure = true
	if !st.HasTreasurePos {
		// Default nearest shipwreck/buried treasure target along chunk grid if none set.
		bx := (int(math.Floor(ent.X))>>4)*16 + 9
		bz := (int(math.Floor(ent.Z))>>4)*16 + 64
		st.TreasureX = bx
		st.TreasureY = int(math.Floor(ent.Y))
		st.TreasureZ = bz
		st.HasTreasurePos = true
	}
	mgr.mu.Unlock()

	consumeOneHeldItemInSurvival(session)
	dim := ent.EffectiveDimension()
	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundDolphinEat, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayEntityEvent,
		EncodeEntityEventPacket(entityID, EntityEventVillagerHappy))

	// Grant Dolphin's Grace immediately to the feeding player.
	s.ApplyPlayerEffect(session, NewStatusEffectInstance(EffectDolphinsGrace, DolphinGraceDurationTicks, 0))
	return true
}

// TryDolphinPlayWithItem checks for floating/submerged item entities within
// `DolphinPlayItemRange` of dolphin `entityID` and nudges/tosses one with a
// velocity impulse (`SoundDolphinPlay`).
func (s *Server) TryDolphinPlayWithItem(entityID int32, rng *rand.Rand) (itemEntityID int32, played bool) {
	if s == nil || s.entities == nil {
		return 0, false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeDolphin {
		return 0, false
	}
	mgr := s.aquaticManager()
	mgr.mu.Lock()
	st := mgr.dolphins[entityID]
	if st == nil || st.PlayItemCooldown > 0 {
		mgr.mu.Unlock()
		return 0, false
	}
	mgr.mu.Unlock()

	dim := ent.EffectiveDimension()
	maxDistSq := DolphinPlayItemRange * DolphinPlayItemRange
	allEnts := s.entities.AppendAll(nil)
	for i := range allEnts {
		it := &allEnts[i]
		if !it.IsItem() || it.EffectiveDimension() != dim {
			continue
		}
		dx := it.X - ent.X
		dy := it.Y - ent.Y
		dz := it.Z - ent.Z
		if dx*dx+dy*dy+dz*dz > maxDistSq {
			continue
		}
		// Compute a playful toss velocity away from the dolphin and slightly upward.
		yawRad := float64(ent.Yaw) * (math.Pi / 180.0)
		vx := -math.Sin(yawRad) * 0.25
		vy := 0.22
		vz := math.Cos(yawRad) * 0.25
		if rng != nil {
			vx += (rng.Float64() - 0.5) * 0.1
			vz += (rng.Float64() - 0.5) * 0.1
		}
		s.entities.Update(it.ID, func(e *world.Entity) {
			e.VelocityX = clampVelocityShort(vx)
			e.VelocityY = clampVelocityShort(vy)
			e.VelocityZ = clampVelocityShort(vz)
			e.X += vx
			e.Y += vy
			e.Z += vz
			e.PickupDelay = 40
		})
		mgr.mu.Lock()
		if st := mgr.dolphins[entityID]; st != nil {
			st.PlayItemCooldown = DolphinPlayItemCooldownTicks
		}
		mgr.mu.Unlock()

		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
			EncodeSoundPacket(SoundDolphinPlay, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlaySetEntityMotion,
			EncodeSetEntityMotionPacket(it.ID, vx, vy, vz))
		return it.ID, true
	}
	return 0, false
}

// ============================================================================
// 2. TURTLES (minecraft:turtle) & TURTLE EGGS
// ============================================================================

// SpawnTurtle spawns a `minecraft:turtle` at `(x, y, z)` in `dimension` and
// records its initial `HomePos` (`floor(x), floor(y), floor(z)`).
func (s *Server) SpawnTurtle(dimension string, x, y, z float64, isBaby bool) (int32, error) {
	if s == nil || s.entities == nil {
		return 0, errors.New("server: nil entity manager")
	}
	normDim := world.NormalizeDimension(dimension)
	id := s.entities.Add(&world.Entity{
		TypeID:    ResolveEntityTypeID(MobTypeTurtle),
		TypeName:  MobTypeTurtle,
		Dimension: normDim,
		X:         x,
		Y:         y,
		Z:         z,
		OnGround:  true,
	})
	s.SetMobHealth(id, DefaultTurtleMaxHealth)

	hx := int(math.Floor(x))
	hy := int(math.Floor(y))
	hz := int(math.Floor(z))
	mgr := s.aquaticManager()
	mgr.mu.Lock()
	mgr.turtles[id] = &TurtleBehaviorState{
		EntityID: id,
		HomeX:    hx,
		HomeY:    hy,
		HomeZ:    hz,
		IsBaby:   isBaby,
	}
	mgr.mu.Unlock()

	if isBaby {
		s.SetMobAge(id, BabyMobGrowthTicks)
	}
	ent := s.entities.Get(id)
	if ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, ent))
	}
	return id, nil
}

// GetTurtleState returns a snapshot of the TurtleBehaviorState for entityID.
func (s *Server) GetTurtleState(entityID int32) (TurtleBehaviorState, bool) {
	if s == nil {
		return TurtleBehaviorState{}, false
	}
	mgr := s.aquaticManager()
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()
	st, ok := mgr.turtles[entityID]
	if !ok || st == nil {
		return TurtleBehaviorState{}, false
	}
	return *st, true
}

// SetTurtleHomePos updates the home beach block coordinates for turtle entityID.
func (s *Server) SetTurtleHomePos(entityID int32, hx, hy, hz int) bool {
	if s == nil {
		return false
	}
	mgr := s.aquaticManager()
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	st := mgr.turtles[entityID]
	if st == nil {
		return false
	}
	st.HomeX = hx
	st.HomeY = hy
	st.HomeZ = hz
	return true
}

// SetTurtleHasEgg marks turtle entityID as carrying fertilized eggs (`HasEgg`)
// so it travels back to its `HomePos` beach to dig and lay eggs.
func (s *Server) SetTurtleHasEgg(entityID int32, hasEgg bool) bool {
	if s == nil {
		return false
	}
	mgr := s.aquaticManager()
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	st := mgr.turtles[entityID]
	if st == nil {
		return false
	}
	st.HasEgg = hasEgg
	st.TravellingHome = hasEgg
	if !hasEgg {
		st.LayingEgg = false
		st.LayEggCounter = 0
	}
	return true
}

// SetTurtleEggPlaceHook registers an optional block-layer hook (for Agent C)
// called when a turtle finishes digging on sand and lays 1..4 turtle eggs.
func (s *Server) SetTurtleEggPlaceHook(hook TurtleEggPlaceHook) {
	if s == nil {
		return
	}
	mgr := s.aquaticManager()
	mgr.mu.Lock()
	mgr.turtleEggHook = hook
	mgr.mu.Unlock()
}

// InteractWithTurtle handles feeding `minecraft:seagrass` to a turtle:
//   - If baby (`IsMobBaby(entityID)`), accelerates remaining baby growth by 10%.
//   - If adult and not carrying eggs (`!HasEgg`), places the turtle into love mode
//     and mates with a nearby adult turtle in love (setting `HasEgg = true` on
//     one parent rather than spawning an immediate baby).
func (s *Server) InteractWithTurtle(session *PlayerSession, entityID int32, rng *rand.Rand) (handled bool, hasEggNow bool) {
	if s == nil || s.entities == nil || session == nil || session.IsDead() {
		return false, false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeTurtle {
		return false, false
	}
	held := session.HeldItem()
	if held.IsEmpty() || !IsTurtleBreedingFoodItem(held.ResolvedID()) {
		return false, false
	}
	if s.IsMobBaby(entityID) {
		fed, newAge := s.FeedBabyMob(session, entityID, held.ResolvedID())
		if fed && newAge >= 0 {
			s.OnBabyTurtleGrownUp(entityID)
		}
		return fed, false
	}

	mgr := s.aquaticManager()
	mgr.mu.RLock()
	st := mgr.turtles[entityID]
	alreadyHasEgg := st != nil && st.HasEgg
	mgr.mu.RUnlock()
	if alreadyHasEgg || s.MobInLoveTicks(entityID) > 0 || s.MobBreedCooldown(entityID) > 0 {
		return false, alreadyHasEgg
	}

	if !s.SetMobInLove(entityID, AnimalLoveDurationTicks, session) {
		return false, false
	}
	consumeOneHeldItemInSurvival(session)
	pregnantID, mated := s.TryMateTurtles(entityID, AnimalBreedRange, rng)
	return true, mated && pregnantID != 0
}

// TryMateTurtles checks if adult turtle `entityID` (in love) has a nearby mate
// in love within `maxDist`; if so, instead of spawning an immediate baby, sets
// `HasEgg = true` on one parent, spawns 1..7 XP orbs, and grants
// `minecraft:husbandry/breed_an_animal`.
func (s *Server) TryMateTurtles(entityID int32, maxDist float64, rng *rand.Rand) (pregnantTurtleID int32, mated bool) {
	if s == nil || s.entities == nil {
		return 0, false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeTurtle {
		return 0, false
	}
	dim := ent.EffectiveDimension()
	maxDistSq := maxDist * maxDist

	tr := s.entityTracker()
	tr.mu.Lock()
	mem := tr.mobStates[entityID]
	if mem == nil || mem.ageTicks < 0 || mem.inLoveTicks <= 0 || mem.breedCooldown > 0 {
		tr.mu.Unlock()
		return 0, false
	}

	var mateEnt *world.Entity
	var causePlayer *PlayerSession
	allEnts := s.entities.AppendAll(nil)
	for i := range allEnts {
		other := &allEnts[i]
		if other.ID == entityID || other.EffectiveDimension() != dim || canonicalResourceName(other.TypeName) != MobTypeTurtle {
			continue
		}
		dx := other.X - ent.X
		dy := other.Y - ent.Y
		dz := other.Z - ent.Z
		if dx*dx+dy*dy+dz*dz > maxDistSq {
			continue
		}
		otherMem := tr.mobStates[other.ID]
		if otherMem == nil || otherMem.ageTicks < 0 || otherMem.inLoveTicks <= 0 || otherMem.breedCooldown > 0 {
			continue
		}
		mateEnt = other
		causePlayer = mem.loveCause
		if causePlayer == nil {
			causePlayer = otherMem.loveCause
		}
		mem.inLoveTicks = 0
		mem.breedCooldown = AnimalBreedCooldownTicks
		mem.loveCause = nil
		otherMem.inLoveTicks = 0
		otherMem.breedCooldown = AnimalBreedCooldownTicks
		otherMem.loveCause = nil
		break
	}
	tr.mu.Unlock()

	if mateEnt == nil {
		return 0, false
	}

	s.SetTurtleHasEgg(entityID, true)
	xp := int32(BreedingMinXP)
	if rng != nil {
		xp += int32(rng.Intn(BreedingMaxXP - BreedingMinXP + 1))
	} else {
		xp = 4
	}
	s.SpawnExperienceOrbsInDimension(dim, ent.X, ent.Y+0.25, ent.Z, xp)
	if causePlayer != nil {
		s.GrantCriterion(causePlayer, "minecraft:husbandry/breed_an_animal", "bred")
	}
	return entityID, true
}

// isSandBeachBelow reports whether the block below `(bx, by, bz)` is sand
// (`minecraft:sand`, `minecraft:red_sand`, or `minecraft:suspicious_sand`).
func isSandBeachBlockName(blockName string) bool {
	switch canonicalResourceName(blockName) {
	case "minecraft:sand", "minecraft:red_sand", "minecraft:suspicious_sand":
		return true
	default:
		return false
	}
}

// TryLayTurtleEggsAt places `1..4` `minecraft:turtle_egg` blocks at `(bx, by, bz)`
// in `dimension` when a pregnant turtle finishes digging on its home sand beach.
func (s *Server) TryLayTurtleEggsAt(entityID int32, dimension string, bx, by, bz int, rng *rand.Rand) (eggCount int, laid bool) {
	if s == nil {
		return 0, false
	}
	eggCount = 1
	if rng != nil {
		eggCount = 1 + rng.Intn(TurtleMaxEggsPerBlock) // 1..4 eggs
	}
	normDim := world.NormalizeDimension(dimension)

	mgr := s.aquaticManager()
	mgr.mu.Lock()
	if st := mgr.turtles[entityID]; st != nil {
		st.HasEgg = false
		st.TravellingHome = false
		st.LayingEgg = false
		st.LayEggCounter = 0
	}
	hook := mgr.turtleEggHook
	mgr.mu.Unlock()

	if hook != nil {
		_ = hook(normDim, bx, by, bz, eggCount)
	} else if cache := s.DimensionChunks(normDim); cache != nil {
		props := map[string]string{
			"eggs":  itoaSmall(eggCount),
			"hatch": "0",
		}
		if st, ok := world.BlockStateByName(TurtleEggBlockName, props); ok {
			cache.SetBlock(bx, by, bz, st)
			s.broadcastMobBlockUpdate(normDim, bx, by, bz, st)
		} else if defSt, ok := world.BlockStateIDByProperties(TurtleEggBlockName, nil); ok && defSt != world.StateAir {
			cache.SetBlock(bx, by, bz, defSt)
			s.broadcastMobBlockUpdate(normDim, bx, by, bz, defSt)
		}
	}

	cx := int32(bx >> 4)
	cz := int32(bz >> 4)
	s.BroadcastDimensionChunk(normDim, cx, cz, PlaySoundPacketID,
		EncodeSoundPacket(SoundTurtleLayEgg, SoundSourceBlocks, float64(bx)+0.5, float64(by)+0.5, float64(bz)+0.5, 1.0, 1.0, 0))
	return eggCount, true
}

func itoaSmall(n int) string {
	switch n {
	case 0:
		return "0"
	case 1:
		return "1"
	case 2:
		return "2"
	case 3:
		return "3"
	case 4:
		return "4"
	default:
		return "1"
	}
}

// StepTurtleEggIncubationAt advances the incubation stage (`hatch = 0..2`) of a
// `minecraft:turtle_egg` block at `(x, y, z)` in `dimension` when sited on sand
// (`minecraft:sand` / `minecraft:red_sand`). When `hatch == 2` completes, it
// spawns `eggCount` (`1..4`) baby turtles with `HomePos = (x, y, z)` and returns
// `hatched = true`.
//
// In vanilla 26.1.2, turtle eggs only crack/hatch on sand, and hatch much faster
// during late night (`dayTime` in `[21600, 22550]`).
func (s *Server) StepTurtleEggIncubationAt(dimension string, x, y, z int, currentHatch, eggCount int, forceAdvance bool, rng *rand.Rand) (newHatch int, hatchedIDs []int32, hatched bool) {
	if s == nil {
		return currentHatch, nil, false
	}
	normDim := world.NormalizeDimension(dimension)
	cache := s.DimensionChunks(normDim)
	if cache != nil && !forceAdvance {
		belowName := world.BlockName(cache.GetBlock(x, y-1, z))
		if !isSandBeachBlockName(belowName) {
			return currentHatch, nil, false
		}
		inNightWindow := s.IsNight()
		if !inNightWindow && rng != nil && rng.Intn(500) != 0 {
			return currentHatch, nil, false
		}
	}

	if eggCount < 1 {
		eggCount = 1
	} else if eggCount > TurtleMaxEggsPerBlock {
		eggCount = TurtleMaxEggsPerBlock
	}

	cx := int32(x >> 4)
	cz := int32(z >> 4)
	if currentHatch < TurtleEggMaxHatchStage {
		newHatch = currentHatch + 1
		if cache != nil {
			props := map[string]string{
				"eggs":  itoaSmall(eggCount),
				"hatch": itoaSmall(newHatch),
			}
			if st, ok := world.BlockStateByName(TurtleEggBlockName, props); ok {
				cache.SetBlock(x, y, z, st)
				s.broadcastMobBlockUpdate(normDim, x, y, z, st)
			}
		}
		s.BroadcastDimensionChunk(normDim, cx, cz, PlaySoundPacketID,
			EncodeSoundPacket(SoundTurtleEggCrack, SoundSourceBlocks, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, 0.7, 0.9, 0))
		return newHatch, nil, false
	}

	// Hatch stage 2 -> hatch 1..4 baby turtles and clear the egg block!
	if cache != nil {
		cache.SetBlock(x, y, z, world.StateAir)
		s.broadcastMobBlockUpdate(normDim, x, y, z, world.StateAir)
	}
	s.BroadcastDimensionChunk(normDim, cx, cz, PlaySoundPacketID,
		EncodeSoundPacket(SoundTurtleEggHatch, SoundSourceBlocks, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, 0.7, 1.0, 0))

	for i := 0; i < eggCount; i++ {
		offsetX := float64(x) + 0.3 + float64(i)*0.15
		offsetZ := float64(z) + 0.5
		tid, err := s.SpawnTurtle(normDim, offsetX, float64(y), offsetZ, true)
		if err == nil && tid != 0 {
			s.SetTurtleHomePos(tid, x, y, z)
			hatchedIDs = append(hatchedIDs, tid)
		}
	}
	return TurtleEggMaxHatchStage, hatchedIDs, true
}

// CanSilkTouchHarvestTurtleEgg reports whether `toolMeta` has the
// `minecraft:silk_touch` enchantment required to drop a `minecraft:turtle_egg`
// item when broken.
func CanSilkTouchHarvestTurtleEgg(toolMeta ItemMeta) bool {
	return HasSilkTouch(toolMeta)
}

// HarvestTurtleEggBlock handles breaking one or all eggs in a
// `minecraft:turtle_egg` block with `toolMeta`:
//   - If `toolMeta` has `minecraft:silk_touch`, drops `1` `minecraft:turtle_egg` item.
//   - Otherwise plays `SoundTurtleEggBreak` and drops nothing (decrementing egg count by 1).
func (s *Server) HarvestTurtleEggBlock(dimension string, x, y, z int, eggCount int, toolMeta ItemMeta) (remainingEggs int, droppedItem world.ItemStack) {
	if eggCount <= 1 {
		remainingEggs = 0
	} else {
		remainingEggs = eggCount - 1
	}
	if CanSilkTouchHarvestTurtleEgg(toolMeta) {
		droppedItem = world.NewNamedItemStack(TurtleEggItemID, 1)
	}
	if s != nil {
		normDim := world.NormalizeDimension(dimension)
		if !droppedItem.IsEmpty() {
			_, _ = s.SpawnItemEntityInDimension(normDim, float64(x)+0.5, float64(y)+0.2, float64(z)+0.5, droppedItem)
		}
		cx := int32(x >> 4)
		cz := int32(z >> 4)
		s.BroadcastDimensionChunk(normDim, cx, cz, PlaySoundPacketID,
			EncodeSoundPacket(SoundTurtleEggBreak, SoundSourceBlocks, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, 0.7, 0.9, 0))
	}
	return remainingEggs, droppedItem
}

// OnBabyTurtleGrownUp is invoked when a baby `minecraft:turtle` reaches adult
// age (`ageTicks == 0`), dropping `1` `minecraft:turtle_scute` at its position
// (vanilla `Turtle.ageBoundaryReached`).
func (s *Server) OnBabyTurtleGrownUp(entityID int32) (droppedScute world.ItemStack, dropped bool) {
	if s == nil || s.entities == nil {
		return world.ItemStack{}, false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeTurtle {
		return world.ItemStack{}, false
	}
	mgr := s.aquaticManager()
	mgr.mu.Lock()
	if st := mgr.turtles[entityID]; st != nil {
		st.IsBaby = false
	}
	mgr.mu.Unlock()

	stack := world.NewNamedItemStack(TurtleScuteItemID, 1)
	if stack.IsEmpty() {
		stack = world.NewNamedItemStack(LegacyScuteItemID, 1)
	}
	if !stack.IsEmpty() {
		_, _ = s.SpawnItemEntityInDimension(ent.EffectiveDimension(), ent.X, ent.Y+0.25, ent.Z, stack)
	}
	return stack, !stack.IsEmpty()
}

// ============================================================================
// 3. SQUID (minecraft:squid) & GLOW SQUID (minecraft:glow_squid)
// ============================================================================

// SpawnSquid spawns a `minecraft:squid` at `(x, y, z)` in `dimension`.
func (s *Server) SpawnSquid(dimension string, x, y, z float64) (int32, error) {
	return s.spawnSquidInternal(dimension, x, y, z, false)
}

// SpawnGlowSquid spawns a `minecraft:glow_squid` at `(x, y, z)` in `dimension`.
func (s *Server) SpawnGlowSquid(dimension string, x, y, z float64) (int32, error) {
	return s.spawnSquidInternal(dimension, x, y, z, true)
}

func (s *Server) spawnSquidInternal(dimension string, x, y, z float64, glow bool) (int32, error) {
	if s == nil || s.entities == nil {
		return 0, errors.New("server: nil entity manager")
	}
	normDim := world.NormalizeDimension(dimension)
	typeName := MobTypeSquid
	maxHP := DefaultSquidMaxHealth
	if glow {
		typeName = MobTypeGlowSquid
		maxHP = DefaultGlowSquidMaxHealth
	}
	id := s.entities.Add(&world.Entity{
		TypeID:    ResolveEntityTypeID(typeName),
		TypeName:  typeName,
		Dimension: normDim,
		X:         x,
		Y:         y,
		Z:         z,
		OnGround:  false,
	})
	s.SetMobHealth(id, maxHP)

	mgr := s.aquaticManager()
	mgr.mu.Lock()
	mgr.squids[id] = &SquidBehaviorState{
		EntityID:    id,
		IsGlowSquid: glow,
	}
	mgr.mu.Unlock()

	ent := s.entities.Get(id)
	if ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, ent))
		if glow {
			s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, PlaySetEntityDataCB,
				EncodeGlowSquidDataPacket(id, 0))
		}
	}
	return id, nil
}

// GetSquidState returns a snapshot of the SquidBehaviorState for entityID.
func (s *Server) GetSquidState(entityID int32) (SquidBehaviorState, bool) {
	if s == nil {
		return SquidBehaviorState{}, false
	}
	mgr := s.aquaticManager()
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()
	st, ok := mgr.squids[entityID]
	if !ok || st == nil {
		return SquidBehaviorState{}, false
	}
	return *st, true
}

// OnSquidDamaged handles a `minecraft:squid` or `minecraft:glow_squid` taking
// damage:
//   - Squirts an ink cloud (`SoundSquidSquirt` / `SoundGlowSquidSquirt`) and
//     propels the squid away from the attacker.
//   - Applies `EffectBlindness` (`80` ticks) to a survival/adventure attacker
//     within `SquidInkBlindnessRadius` (`3.5` blocks).
//   - If `minecraft:glow_squid`, extinguishes its luminescence for `100` ticks
//     (`5s`, `GlowSquid.DATA_DARK_TICKS_REMAINING = 100`) and broadcasts metadata.
func (s *Server) OnSquidDamaged(entityID int32, attacker *PlayerSession) {
	if s == nil || s.entities == nil {
		return
	}
	ent := s.entities.Get(entityID)
	if ent == nil {
		return
	}
	norm := canonicalResourceName(ent.TypeName)
	if norm != MobTypeSquid && norm != MobTypeGlowSquid {
		return
	}
	isGlow := norm == MobTypeGlowSquid

	fleeX, fleeY, fleeZ := 0.25, 0.1, 0.0
	if attacker != nil {
		snap := attacker.Snapshot()
		dx := ent.X - snap.X
		dz := ent.Z - snap.Z
		dist := math.Hypot(dx, dz)
		if dist > 1e-4 {
			fleeX = (dx / dist) * (SquidJetSpeed * 2.2)
			fleeZ = (dz / dist) * (SquidJetSpeed * 2.2)
		}
		if snap.GameMode != GameModeCreative && snap.GameMode != GameModeSpectator {
			dist3D := math.Sqrt(dx*dx + (ent.Y-snap.Y)*(ent.Y-snap.Y) + dz*dz)
			if dist3D <= SquidInkBlindnessRadius {
				s.ApplyPlayerEffect(attacker, NewStatusEffectInstance(EffectBlindness, SquidInkBlindnessTicks, 0))
			}
		}
	}

	mgr := s.aquaticManager()
	mgr.mu.Lock()
	st := mgr.squids[entityID]
	if st == nil {
		st = &SquidBehaviorState{
			EntityID:    entityID,
			IsGlowSquid: isGlow,
		}
		mgr.squids[entityID] = st
	}
	st.SwimDirX = fleeX
	st.SwimDirY = fleeY
	st.SwimDirZ = fleeZ
	st.SwimTimer = 20
	if isGlow {
		st.DarkTicksRemaining = GlowSquidDarkDurationTicks
	}
	mgr.mu.Unlock()

	s.entities.Update(entityID, func(e *world.Entity) {
		e.VelocityX = clampVelocityShort(fleeX)
		e.VelocityY = clampVelocityShort(fleeY)
		e.VelocityZ = clampVelocityShort(fleeZ)
		e.X += fleeX
		e.Y += fleeY
		e.Z += fleeZ
	})

	dim := ent.EffectiveDimension()
	cp := ent.ChunkPos()
	squirtSound := SoundSquidSquirt
	if isGlow {
		squirtSound = SoundGlowSquidSquirt
	}
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(squirtSound, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
	if isGlow {
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
			EncodeGlowSquidDataPacket(entityID, GlowSquidDarkDurationTicks))
	}
}

// ============================================================================
// 4. FISH (cod, salmon, tropical_fish, pufferfish) & BUCKET HOOKS
// ============================================================================

// SpawnFish spawns one of the 4 vanilla fish mobs (`minecraft:cod`,
// `minecraft:salmon`, `minecraft:tropical_fish`, or `minecraft:pufferfish`) at
// `(x, y, z)` in `dimension`.
func (s *Server) SpawnFish(dimension string, x, y, z float64, fishType string, fromBucket bool, rng *rand.Rand) (int32, error) {
	if s == nil || s.entities == nil {
		return 0, errors.New("server: nil entity manager")
	}
	norm := canonicalResourceName(fishType)
	if !IsFishMobType(norm) {
		return 0, errors.New("aquatic: unsupported fish type " + fishType)
	}
	normDim := world.NormalizeDimension(dimension)
	id := s.entities.Add(&world.Entity{
		TypeID:    ResolveEntityTypeID(norm),
		TypeName:  norm,
		Dimension: normDim,
		X:         x,
		Y:         y,
		Z:         z,
		OnGround:  false,
	})
	s.SetMobHealth(id, ResolveMobMaxHealth(norm))

	salmonSize := SalmonVariantMedium
	if norm == MobTypeSalmon && rng != nil {
		r := rng.Intn(3)
		if r == 0 {
			salmonSize = SalmonVariantSmall
		} else if r == 2 {
			salmonSize = SalmonVariantLarge
		}
	}
	var tropVariant int32
	if norm == MobTypeTropicalFish {
		if rng != nil {
			tropVariant = PackTropicalFishVariant(rng.Intn(2), rng.Intn(6), rng.Intn(16), rng.Intn(16))
		} else {
			tropVariant = PackTropicalFishVariant(0, 0, 1, 0) // Kob (orange/white)
		}
	}

	mgr := s.aquaticManager()
	mgr.mu.Lock()
	mgr.fish[id] = &FishBehaviorState{
		EntityID:        id,
		FishType:        norm,
		FromBucket:      fromBucket,
		SalmonSize:      salmonSize,
		TropicalVariant: tropVariant,
		PuffState:       PufferfishDeflated,
	}
	mgr.mu.Unlock()

	ent := s.entities.Get(id)
	if ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, ent))
		if norm == MobTypePufferfish {
			s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, PlaySetEntityDataCB,
				EncodePufferfishDataPacket(id, fromBucket, PufferfishDeflated))
		}
	}
	return id, nil
}

// GetFishState returns a snapshot of the FishBehaviorState for entityID.
func (s *Server) GetFishState(entityID int32) (FishBehaviorState, bool) {
	if s == nil {
		return FishBehaviorState{}, false
	}
	mgr := s.aquaticManager()
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()
	st, ok := mgr.fish[entityID]
	if !ok || st == nil {
		return FishBehaviorState{}, false
	}
	return *st, true
}

// LinkFishToSchoolLeader assigns schooling fish `followerID` to follow
// `leaderID` (must be the same species of `cod`, `salmon`, or `tropical_fish`
// and within `FishMaxSchoolSize`).
func (s *Server) LinkFishToSchoolLeader(followerID, leaderID int32) bool {
	if s == nil || followerID == leaderID || followerID <= 0 || leaderID <= 0 {
		return false
	}
	mgr := s.aquaticManager()
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	follower := mgr.fish[followerID]
	leader := mgr.fish[leaderID]
	if follower == nil || leader == nil || follower.FishType != leader.FishType {
		return false
	}
	if !IsSchoolingFishType(follower.FishType) || leader.SchoolLeaderID != 0 {
		return false
	}
	count := 1
	for _, f := range mgr.fish {
		if f != nil && f.SchoolLeaderID == leaderID {
			count++
		}
	}
	if count >= FishMaxSchoolSize {
		return false
	}
	follower.SchoolLeaderID = leaderID
	return true
}

// SetPufferfishPuffState sets a pufferfish's inflation state (`0..2`) and
// broadcasts `EncodePufferfishDataPacket`.
func (s *Server) SetPufferfishPuffState(entityID, puffState int32) bool {
	if s == nil || s.entities == nil {
		return false
	}
	if puffState < PufferfishDeflated {
		puffState = PufferfishDeflated
	} else if puffState > PufferfishFullyPuffed {
		puffState = PufferfishFullyPuffed
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypePufferfish {
		return false
	}
	mgr := s.aquaticManager()
	mgr.mu.Lock()
	st := mgr.fish[entityID]
	if st == nil {
		st = &FishBehaviorState{
			EntityID: entityID,
			FishType: MobTypePufferfish,
		}
		mgr.fish[entityID] = st
	}
	prev := st.PuffState
	st.PuffState = puffState
	fromBucket := st.FromBucket
	mgr.mu.Unlock()

	if prev != puffState {
		dim := ent.EffectiveDimension()
		cp := ent.ChunkPos()
		sound := SoundPufferfishBlowUp
		if puffState < prev {
			sound = SoundPufferfishBlowOut
		}
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
			EncodeSoundPacket(sound, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
			EncodePufferfishDataPacket(entityID, fromBucket, puffState))
	}
	return true
}

// ApplyPufferfishFoodEffects applies the vanilla 26.1.2 status effects when a
// player eats raw `minecraft:pufferfish`:
//   - Poison II (amplifier 1) for 1200 ticks (60s)
//   - Hunger III (amplifier 2) for 300 ticks (15s)
//   - Nausea I (amplifier 0) for 300 ticks (15s)
func (s *Server) ApplyPufferfishFoodEffects(session *PlayerSession) {
	if s == nil || session == nil || session.IsDead() {
		return
	}
	s.ApplyPlayerEffect(session, NewStatusEffectInstance(EffectPoison, 1200, 1))
	s.ApplyPlayerEffect(session, NewStatusEffectInstance(EffectHunger, 300, 2))
	s.ApplyPlayerEffect(session, NewStatusEffectInstance(EffectNausea, 300, 0))
}

// FishBucketItemForMob returns the corresponding `<fish>_bucket` item ID for a
// bucketable fish entity type.
func FishBucketItemForMob(typeName string) (string, bool) {
	switch canonicalResourceName(typeName) {
	case MobTypeCod:
		return CodBucketItemID, true
	case MobTypeSalmon:
		return SalmonBucketItemID, true
	case MobTypeTropicalFish:
		return TropicalFishBucketItemID, true
	case MobTypePufferfish:
		return PufferfishBucketItemID, true
	default:
		return "", false
	}
}

// FishMobForBucketItem returns the fish entity type for a `<fish>_bucket` item.
func FishMobForBucketItem(itemName string) (string, bool) {
	switch canonicalResourceName(itemName) {
	case CodBucketItemID:
		return MobTypeCod, true
	case SalmonBucketItemID:
		return MobTypeSalmon, true
	case TropicalFishBucketItemID:
		return MobTypeTropicalFish, true
	case PufferfishBucketItemID:
		return MobTypePufferfish, true
	default:
		return "", false
	}
}

// CatchAquaticMobInBucket captures a `cod`, `salmon`, `tropical_fish`, or
// `pufferfish` with a `minecraft:water_bucket`, despawning the entity, giving
// the player the corresponding `<fish>_bucket`, and granting the
// `minecraft:husbandry/tactical_fishing` advancement.
func (s *Server) CatchAquaticMobInBucket(session *PlayerSession, entityID int32) (bucketItem string, caught bool) {
	if s == nil || s.entities == nil || session == nil || session.IsDead() {
		return "", false
	}
	held := session.HeldItem()
	if canonicalResourceName(held.ResolvedID()) != WaterBucketItemID {
		return "", false
	}
	ent := s.entities.Get(entityID)
	if ent == nil {
		return "", false
	}
	norm := canonicalResourceName(ent.TypeName)
	bItem, ok := FishBucketItemForMob(norm)
	if !ok {
		return "", false
	}

	hp := s.MobHealth(entityID)
	mgr := s.aquaticManager()
	mgr.mu.Lock()
	st := mgr.fish[entityID]
	info := bucketedFishInfo{
		FishType:   norm,
		SalmonSize: SalmonVariantMedium,
		Health:     hp,
	}
	if st != nil {
		info.SalmonSize = st.SalmonSize
		info.TropicalVariant = st.TropicalVariant
	}
	mgr.bucketedFish[session] = info
	delete(mgr.fish, entityID)
	mgr.mu.Unlock()

	dim := ent.EffectiveDimension()
	cp := ent.ChunkPos()
	s.entities.Remove(entityID)
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayRemoveEntities,
		encodeRemoveEntitiesPacket(nil, []int32{entityID}))
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundBucketFillFish, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))

	snap := session.Snapshot()
	if snap.GameMode != GameModeCreative {
		slotIdx := world.HotbarContainerOffset + int(snap.SelectedSlot)
		bStack := world.NewNamedItemStack(bItem, 1)
		if !bStack.IsEmpty() {
			session.SetInventorySlot(slotIdx, bStack)
		}
	}
	s.GrantCriterion(session, "minecraft:husbandry/tactical_fishing", norm)
	return bItem, true
}

// PlaceAquaticMobFromBucket spawns a fish from a player's held `<fish>_bucket`
// at `(x, y, z)` in `dimension` with `FromBucket = true` and replaces the bucket
// with `minecraft:water_bucket`.
func (s *Server) PlaceAquaticMobFromBucket(session *PlayerSession, dimension string, x, y, z float64) (entityID int32, placed bool) {
	if s == nil || session == nil || session.IsDead() {
		return 0, false
	}
	held := session.HeldItem()
	fishType, ok := FishMobForBucketItem(held.ResolvedID())
	if !ok {
		return 0, false
	}
	id, err := s.SpawnFish(dimension, x, y, z, fishType, true, nil)
	if err != nil {
		return 0, false
	}

	mgr := s.aquaticManager()
	mgr.mu.Lock()
	if info, hasInfo := mgr.bucketedFish[session]; hasInfo && info.FishType == fishType {
		if st := mgr.fish[id]; st != nil {
			st.SalmonSize = info.SalmonSize
			st.TropicalVariant = info.TropicalVariant
		}
		if info.Health > 0 {
			s.SetMobHealth(id, info.Health)
		}
		delete(mgr.bucketedFish, session)
	}
	mgr.mu.Unlock()

	snap := session.Snapshot()
	if snap.GameMode != GameModeCreative {
		slotIdx := world.HotbarContainerOffset + int(snap.SelectedSlot)
		wb := world.NewNamedItemStack(WaterBucketItemID, 1)
		if !wb.IsEmpty() {
			session.SetInventorySlot(slotIdx, wb)
		}
	}
	normDim := world.NormalizeDimension(dimension)
	cx := int32(int(math.Floor(x)) >> 4)
	cz := int32(int(math.Floor(z)) >> 4)
	s.BroadcastDimensionChunk(normDim, cx, cz, PlaySoundPacketID,
		EncodeSoundPacket(SoundBucketEmptyFish, SoundSourceNeutral, x, y, z, 1.0, 1.0, 0))
	return id, true
}

// ============================================================================
// 5. GUARDIAN (minecraft:guardian) & ELDER GUARDIAN (minecraft:elder_guardian)
// ============================================================================

// SpawnGuardian spawns a `minecraft:guardian` (`30.0 HP`) at `(x, y, z)` in `dimension`.
func (s *Server) SpawnGuardian(dimension string, x, y, z float64) (int32, error) {
	return s.spawnGuardianInternal(dimension, x, y, z, false)
}

// SpawnElderGuardian spawns a `minecraft:elder_guardian` (`80.0 HP`) at `(x, y, z)` in `dimension`.
func (s *Server) SpawnElderGuardian(dimension string, x, y, z float64) (int32, error) {
	return s.spawnGuardianInternal(dimension, x, y, z, true)
}

func (s *Server) spawnGuardianInternal(dimension string, x, y, z float64, elder bool) (int32, error) {
	if s == nil || s.entities == nil {
		return 0, errors.New("server: nil entity manager")
	}
	normDim := world.NormalizeDimension(dimension)
	typeName := MobTypeGuardian
	maxHP := DefaultGuardianMaxHealth
	if elder {
		typeName = MobTypeElderGuardian
		maxHP = DefaultElderGuardianMaxHealth
	}
	id := s.entities.Add(&world.Entity{
		TypeID:    ResolveEntityTypeID(typeName),
		TypeName:  typeName,
		Dimension: normDim,
		X:         x,
		Y:         y,
		Z:         z,
		OnGround:  false,
	})
	s.SetMobHealth(id, maxHP)

	mgr := s.aquaticManager()
	mgr.mu.Lock()
	mgr.guardians[id] = &GuardianBehaviorState{
		EntityID: id,
		IsElder:  elder,
		Moving:   false, // Stationary by default -> spikes extended!
	}
	mgr.mu.Unlock()

	ent := s.entities.Get(id)
	if ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, ent))
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, PlaySetEntityDataCB,
			EncodeGuardianDataPacket(id, false, 0))
	}
	return id, nil
}

// GetGuardianState returns a snapshot of the GuardianBehaviorState for entityID.
func (s *Server) GetGuardianState(entityID int32) (GuardianBehaviorState, bool) {
	if s == nil {
		return GuardianBehaviorState{}, false
	}
	mgr := s.aquaticManager()
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()
	st, ok := mgr.guardians[entityID]
	if !ok || st == nil {
		return GuardianBehaviorState{}, false
	}
	return *st, true
}

// SetGuardianMoving updates whether a guardian is swimming (`Moving = true` ->
// spikes retracted) or stationary (`Moving = false` -> spikes extended for
// Thorns retaliation) and broadcasts `EncodeGuardianDataPacket`.
func (s *Server) SetGuardianMoving(entityID int32, moving bool) bool {
	if s == nil || s.entities == nil {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil {
		return false
	}
	mgr := s.aquaticManager()
	mgr.mu.Lock()
	st := mgr.guardians[entityID]
	if st == nil {
		mgr.mu.Unlock()
		return false
	}
	prev := st.Moving
	st.Moving = moving
	targetID := st.AttackTargetID
	mgr.mu.Unlock()

	if prev != moving {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, PlaySetEntityDataCB,
			EncodeGuardianDataPacket(entityID, moving, targetID))
	}
	return true
}

// OnGuardianThornsRetaliate applies `2.0 HP` spike retaliation damage to a melee
// attacker when a `minecraft:guardian` or `minecraft:elder_guardian` is hit
// while its spikes are extended (`!Moving`).
func (s *Server) OnGuardianThornsRetaliate(entityID int32, attacker *PlayerSession) (retaliated bool, dealt float32) {
	if s == nil || attacker == nil || attacker.IsDead() {
		return false, 0
	}
	mgr := s.aquaticManager()
	mgr.mu.RLock()
	st := mgr.guardians[entityID]
	spikesExtended := st != nil && !st.Moving
	mgr.mu.RUnlock()
	if !spikesExtended {
		return false, 0
	}
	dealt = s.DamagePlayerWithArmor(attacker, GuardianThornsDamage, "thorns")
	return dealt > 0, dealt
}

// ApplyElderGuardianMiningFatigueAura checks all survival/adventure players
// within `ElderGuardianFatigueRadius` (`50` blocks) of Elder Guardian `entityID`
// and applies `EffectMiningFatigue` III (`Amplifier = 2`, `Duration = 6000` ticks
// = 5 minutes) + `SoundElderGuardianCurse` to any player who does not already
// have Mining Fatigue III with `> 1200` ticks remaining.
func (s *Server) ApplyElderGuardianMiningFatigueAura(entityID int32) (affectedPlayers int) {
	if s == nil || s.entities == nil || s.Difficulty() == DifficultyPeaceful {
		return 0
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeElderGuardian {
		return 0
	}
	dim := ent.EffectiveDimension()
	maxDistSq := ElderGuardianFatigueRadius * ElderGuardianFatigueRadius

	for _, p := range s.onlinePlayersSnapshot() {
		if p == nil || p.IsDead() {
			continue
		}
		snap := p.Snapshot()
		if world.NormalizeDimension(snap.Dimension) != dim {
			continue
		}
		if snap.GameMode == GameModeCreative || snap.GameMode == GameModeSpectator {
			continue
		}
		dx := snap.X - ent.X
		dy := snap.Y - ent.Y
		dz := snap.Z - ent.Z
		if dx*dx+dy*dy+dz*dz > maxDistSq {
			continue
		}
		if inst, has := p.PlayerEffect(EffectMiningFatigue); has && inst.Amplifier >= ElderGuardianFatigueAmplifier && inst.Duration > 1200 {
			continue
		}
		s.ApplyPlayerEffect(p, NewStatusEffectInstance(EffectMiningFatigue, ElderGuardianFatigueDurationTicks, ElderGuardianFatigueAmplifier))
		cx := int32(int(math.Floor(snap.X)) >> 4)
		cz := int32(int(math.Floor(snap.Z)) >> 4)
		s.BroadcastDimensionChunk(dim, cx, cz, PlaySoundPacketID,
			EncodeSoundPacket(SoundElderGuardianCurse, SoundSourceHostile, snap.X, snap.Y, snap.Z, 1.0, 1.0, 0))
		affectedPlayers++
	}
	return affectedPlayers
}

// InteractWithAquaticMob routes right-click player interactions on a dolphin,
// turtle, or fish entity to the appropriate handler.
func (s *Server) InteractWithAquaticMob(session *PlayerSession, entityID int32, rng *rand.Rand) (handled bool) {
	if s == nil || s.entities == nil || session == nil {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil {
		return false
	}
	switch canonicalResourceName(ent.TypeName) {
	case MobTypeDolphin:
		return s.InteractWithDolphin(session, entityID)
	case MobTypeTurtle:
		h, _ := s.InteractWithTurtle(session, entityID, rng)
		return h
	case MobTypeCod, MobTypeSalmon, MobTypeTropicalFish, MobTypePufferfish:
		_, caught := s.CatchAquaticMobInBucket(session, entityID)
		return caught
	default:
		return false
	}
}

// ============================================================================
// 6. MAIN AQUATIC MOBS TICK LOOP
// ============================================================================

// TickAquaticMobs steps all live Dolphins, Turtles, Squids, Glow Squids, Fish
// (`cod`, `salmon`, `tropical_fish`, `pufferfish`), Guardians, and Elder
// Guardians by 1 server tick (20 TPS), relying on `EntitySubmergedFluidDepth`
// (`cache.FluidHeightAt`) and `StepEntityFluidAndBubbleColumn` (`cache.BubbleColumnVelocityAt`).
func (s *Server) TickAquaticMobs(rng *rand.Rand) {
	if s == nil || s.entities == nil {
		return
	}
	s.tickDolphins(rng)
	s.tickTurtles(rng)
	s.tickSquids(rng)
	s.tickFish(rng)
	s.tickGuardians(rng)
}

func (s *Server) tickDolphins(rng *rand.Rand) {
	mgr := s.aquaticManager()
	mgr.mu.RLock()
	if len(mgr.dolphins) == 0 {
		mgr.mu.RUnlock()
		return
	}
	ids := make([]int32, 0, len(mgr.dolphins))
	for id := range mgr.dolphins {
		ids = append(ids, id)
	}
	mgr.mu.RUnlock()

	for _, id := range ids {
		ent := s.entities.Get(id)
		if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeDolphin {
			mgr.mu.Lock()
			delete(mgr.dolphins, id)
			mgr.mu.Unlock()
			continue
		}
		dim := ent.EffectiveDimension()
		inWater := s.isEntityInWater(dim, ent.X, ent.Y, ent.Z)
		eyesSubmerged := s.isEntityEyesUnderWater(dim, ent.X, ent.Y, ent.Z, 0.3)

		mgr.mu.Lock()
		st := mgr.dolphins[id]
		if st == nil {
			mgr.mu.Unlock()
			continue
		}
		if st.PlayItemCooldown > 0 {
			st.PlayItemCooldown--
		}
		// Moistness: replenished in water, decreases on land (dries out at <= 0).
		if inWater {
			st.MoistnessTicks = DolphinMaxMoistnessTicks
		} else {
			st.MoistnessTicks--
		}
		// AirSupply: decreases when eyes are submerged in water without air above, replenishes in air.
		if eyesSubmerged {
			st.AirSupplyTicks--
		} else {
			st.AirSupplyTicks = DolphinMaxAirSupplyTicks
		}
		moistness := st.MoistnessTicks
		airSupply := st.AirSupplyTicks
		canEscort := st.GotFish && st.CanFindTreasure && st.HasTreasurePos
		tx, ty, tz := st.TreasureX, st.TreasureY, st.TreasureZ
		canPlay := inWater && st.PlayItemCooldown == 0
		mgr.mu.Unlock()

		// Dry-out damage on land every 20 ticks once Moistness <= 0.
		if moistness <= 0 && (-moistness)%20 == 0 {
			s.applyDamageToMob(ent, 1.0, nil, 0, false, false)
			if s.entities.Get(id) == nil {
				continue
			}
		}
		// Drowning damage if stuck underwater with AirSupply <= -20.
		if airSupply <= -20 {
			mgr.mu.Lock()
			if st := mgr.dolphins[id]; st != nil {
				st.AirSupplyTicks = 0
			}
			mgr.mu.Unlock()
			s.applyDamageToMob(ent, 2.0, nil, 0, false, false)
			if s.entities.Get(id) == nil {
				continue
			}
		}

		if inWater {
			// Grant Dolphin's Grace to nearby sprinting/swimming players in water.
			graceRangeSq := DolphinGraceRange * DolphinGraceRange
			for _, p := range s.onlinePlayersSnapshot() {
				if p == nil || p.IsDead() {
					continue
				}
				snap := p.Snapshot()
				if world.NormalizeDimension(snap.Dimension) != dim {
					continue
				}
				if snap.GameMode == GameModeSpectator {
					continue
				}
				dx := snap.X - ent.X
				dy := snap.Y - ent.Y
				dz := snap.Z - ent.Z
				if dx*dx+dy*dy+dz*dz <= graceRangeSq && (p.IsSprinting() || s.isEntityInWater(dim, snap.X, snap.Y, snap.Z)) {
					s.ApplyPlayerEffect(p, NewStatusEffectInstance(EffectDolphinsGrace, DolphinGraceDurationTicks, 0))
				}
			}

			// Escort toward treasure if fed raw fish.
			if canEscort {
				dx := (float64(tx) + 0.5) - ent.X
				dz := (float64(tz) + 0.5) - ent.Z
				dist := math.Hypot(dx, dz)
				if dist > DolphinTreasureStopDist {
					stepX := (dx / dist) * DolphinSwimSpeed
					stepZ := (dz / dist) * DolphinSwimSpeed
					yaw := yawToward(dx, dz)
					s.entities.Update(id, func(e *world.Entity) {
						e.X += stepX
						e.Z += stepZ
						e.Yaw = yaw
						e.HeadYaw = yaw
						e.VelocityX = clampVelocityShort(stepX)
						e.VelocityZ = clampVelocityShort(stepZ)
					})
				} else {
					mgr.mu.Lock()
					if st := mgr.dolphins[id]; st != nil {
						st.GotFish = false
						st.CanFindTreasure = false
					}
					mgr.mu.Unlock()
					_ = ty
				}
			} else if canPlay {
				_, _ = s.TryDolphinPlayWithItem(id, rng)
			}
		}
	}
}

func (s *Server) tickTurtles(rng *rand.Rand) {
	mgr := s.aquaticManager()
	mgr.mu.RLock()
	if len(mgr.turtles) == 0 {
		mgr.mu.RUnlock()
		return
	}
	ids := make([]int32, 0, len(mgr.turtles))
	for id := range mgr.turtles {
		ids = append(ids, id)
	}
	mgr.mu.RUnlock()

	for _, id := range ids {
		ent := s.entities.Get(id)
		if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeTurtle {
			mgr.mu.Lock()
			delete(mgr.turtles, id)
			mgr.mu.Unlock()
			continue
		}
		if s.MobInLoveTicks(id) > 0 {
			_, _ = s.TryMateTurtles(id, AnimalBreedRange, rng)
		}

		mgr.mu.Lock()
		st := mgr.turtles[id]
		if st == nil || !st.HasEgg {
			mgr.mu.Unlock()
			continue
		}
		hx, hy, hz := st.HomeX, st.HomeY, st.HomeZ
		laying := st.LayingEgg
		counter := st.LayEggCounter
		mgr.mu.Unlock()

		dim := ent.EffectiveDimension()
		dx := (float64(hx) + 0.5) - ent.X
		dz := (float64(hz) + 0.5) - ent.Z
		dist := math.Hypot(dx, dz)

		if !laying && dist > 1.75 {
			inWater := s.isEntityInWater(dim, ent.X, ent.Y, ent.Z)
			speed := TurtleLandSpeed
			if inWater {
				speed = TurtleSwimSpeed
			}
			stepX := (dx / dist) * speed
			stepZ := (dz / dist) * speed
			yaw := yawToward(dx, dz)
			s.entities.Update(id, func(e *world.Entity) {
				e.X += stepX
				e.Z += stepZ
				e.Yaw = yaw
				e.HeadYaw = yaw
			})
			continue
		}

		// Close to HomePos: check if standing on sand (or if chunk cache is nil in unit tests) and dig!
		bx := int(math.Floor(ent.X))
		by := int(math.Floor(ent.Y))
		bz := int(math.Floor(ent.Z))
		canDig := true
		if cache := s.DimensionChunks(dim); cache != nil {
			belowName := world.BlockName(cache.GetBlock(bx, by-1, bz))
			if belowName != "minecraft:air" && !isSandBeachBlockName(belowName) {
				canDig = false
			}
		}
		if !canDig {
			continue
		}

		counter++
		mgr.mu.Lock()
		if st := mgr.turtles[id]; st != nil {
			st.LayingEgg = true
			st.LayEggCounter = counter
		}
		mgr.mu.Unlock()

		if counter >= TurtleLayEggDigTicks {
			_, _ = s.TryLayTurtleEggsAt(id, dim, bx, by, bz, rng)
		}
		_ = hy
	}
}

func (s *Server) tickSquids(rng *rand.Rand) {
	mgr := s.aquaticManager()
	mgr.mu.RLock()
	if len(mgr.squids) == 0 {
		mgr.mu.RUnlock()
		return
	}
	ids := make([]int32, 0, len(mgr.squids))
	for id := range mgr.squids {
		ids = append(ids, id)
	}
	mgr.mu.RUnlock()

	for _, id := range ids {
		ent := s.entities.Get(id)
		if ent == nil {
			mgr.mu.Lock()
			delete(mgr.squids, id)
			mgr.mu.Unlock()
			continue
		}
		dim := ent.EffectiveDimension()
		inWater := s.isEntityInWater(dim, ent.X, ent.Y, ent.Z)

		mgr.mu.Lock()
		st := mgr.squids[id]
		if st == nil {
			mgr.mu.Unlock()
			continue
		}
		var broadcastRestoredGlow bool
		if st.IsGlowSquid && st.DarkTicksRemaining > 0 {
			st.DarkTicksRemaining--
			if st.DarkTicksRemaining == 0 {
				broadcastRestoredGlow = true
			}
		}
		if inWater {
			st.OutOfWaterTicks = 0
			if st.SwimTimer <= 0 {
				angle := 0.0
				vy := 0.02
				if rng != nil {
					angle = rng.Float64() * 2.0 * math.Pi
					vy = (rng.Float64() - 0.45) * 0.08
				}
				st.SwimDirX = math.Cos(angle) * SquidJetSpeed
				st.SwimDirY = vy
				st.SwimDirZ = math.Sin(angle) * SquidJetSpeed
				st.SwimTimer = 40
			} else {
				st.SwimTimer--
			}
		} else {
			st.OutOfWaterTicks++
		}
		outTicks := st.OutOfWaterTicks
		vx, vy, vz := st.SwimDirX, st.SwimDirY, st.SwimDirZ
		mgr.mu.Unlock()

		if broadcastRestoredGlow {
			cp := ent.ChunkPos()
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
				EncodeGlowSquidDataPacket(id, 0))
		}
		if inWater {
			s.entities.Update(id, func(e *world.Entity) {
				e.X += vx * 0.25
				e.Y += vy * 0.25
				e.Z += vz * 0.25
				e.VelocityX = clampVelocityShort(vx)
				e.VelocityY = clampVelocityShort(vy)
				e.VelocityZ = clampVelocityShort(vz)
			})
		} else if outTicks > SquidMaxOutOfWaterTicks && (outTicks-SquidMaxOutOfWaterTicks)%20 == 0 {
			s.applyDamageToMob(ent, 2.0, nil, 0, false, false)
		}
	}
}

func (s *Server) tickFish(rng *rand.Rand) {
	mgr := s.aquaticManager()
	mgr.mu.RLock()
	if len(mgr.fish) == 0 {
		mgr.mu.RUnlock()
		return
	}
	ids := make([]int32, 0, len(mgr.fish))
	for id := range mgr.fish {
		ids = append(ids, id)
	}
	mgr.mu.RUnlock()

	for _, id := range ids {
		ent := s.entities.Get(id)
		if ent == nil || !IsFishMobType(ent.TypeName) {
			mgr.mu.Lock()
			delete(mgr.fish, id)
			mgr.mu.Unlock()
			continue
		}
		dim := ent.EffectiveDimension()
		inWater := s.isEntityInWater(dim, ent.X, ent.Y, ent.Z)

		mgr.mu.Lock()
		st := mgr.fish[id]
		if st == nil {
			mgr.mu.Unlock()
			continue
		}
		if st.StingCooldownTicks > 0 {
			st.StingCooldownTicks--
		}
		if inWater {
			st.OutOfWaterTicks = 0
		} else {
			st.OutOfWaterTicks++
		}
		fishType := st.FishType
		leaderID := st.SchoolLeaderID
		outTicks := st.OutOfWaterTicks
		mgr.mu.Unlock()

		if !inWater {
			// Flop on land every 10 ticks and suffocate after 300 ticks out of water!
			if outTicks%10 == 1 {
				flopSound := SoundCodFlop
				switch fishType {
				case MobTypeSalmon:
					flopSound = SoundSalmonFlop
				case MobTypeTropicalFish:
					flopSound = SoundTropicalFishFlop
				case MobTypePufferfish:
					flopSound = SoundPufferfishFlop
				}
				s.entities.Update(id, func(e *world.Entity) {
					e.VelocityY = clampVelocityShort(FishFlopVelocityY)
					e.OnGround = false
				})
				cp := ent.ChunkPos()
				s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
					EncodeSoundPacket(flopSound, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
			}
			if outTicks > FishMaxOutOfWaterTicks && (outTicks-FishMaxOutOfWaterTicks)%20 == 0 {
				s.applyDamageToMob(ent, 1.0, nil, 0, false, false)
				if s.entities.Get(id) == nil {
					continue
				}
			}
		} else if IsSchoolingFishType(fishType) && leaderID != 0 {
			// Follow school leader in water.
			if leaderEnt := s.entities.Get(leaderID); leaderEnt != nil && leaderEnt.EffectiveDimension() == dim {
				dx := leaderEnt.X - ent.X
				dy := leaderEnt.Y - ent.Y
				dz := leaderEnt.Z - ent.Z
				dist := math.Hypot(dx, dz)
				if dist > 1.25 && dist <= FishSchoolRange*2 {
					stepX := (dx / dist) * FishSwimSpeed
					stepZ := (dz / dist) * FishSwimSpeed
					stepY := math.Max(-0.05, math.Min(0.05, dy*0.2))
					yaw := yawToward(dx, dz)
					s.entities.Update(id, func(e *world.Entity) {
						e.X += stepX
						e.Y += stepY
						e.Z += stepZ
						e.Yaw = yaw
						e.HeadYaw = yaw
					})
				}
			} else {
				mgr.mu.Lock()
				if st := mgr.fish[id]; st != nil {
					st.SchoolLeaderID = 0
				}
				mgr.mu.Unlock()
			}
		}

		if fishType == MobTypePufferfish {
			s.stepPufferfishDefense(id, ent, dim)
		}
	}
}

func (s *Server) stepPufferfishDefense(entityID int32, ent *world.Entity, dim string) {
	if s.Difficulty() == DifficultyPeaceful {
		return
	}
	detectSq := PufferfishDetectRange * PufferfishDetectRange
	var nearbyPlayers []*PlayerSession
	for _, p := range s.onlinePlayersSnapshot() {
		if p == nil || p.IsDead() {
			continue
		}
		snap := p.Snapshot()
		if world.NormalizeDimension(snap.Dimension) != dim {
			continue
		}
		if snap.GameMode == GameModeCreative || snap.GameMode == GameModeSpectator {
			continue
		}
		dx := snap.X - ent.X
		dy := snap.Y - ent.Y
		dz := snap.Z - ent.Z
		if dx*dx+dy*dy+dz*dz <= detectSq {
			nearbyPlayers = append(nearbyPlayers, p)
		}
	}

	mgr := s.aquaticManager()
	mgr.mu.Lock()
	st := mgr.fish[entityID]
	if st == nil {
		mgr.mu.Unlock()
		return
	}
	curPuff := st.PuffState
	canSting := st.StingCooldownTicks == 0
	var nextPuff = curPuff
	if len(nearbyPlayers) > 0 {
		st.DeflateTimer = PufferfishDeflateDelayTicks
		if curPuff < PufferfishFullyPuffed {
			nextPuff = curPuff + 1
		}
	} else if curPuff > PufferfishDeflated {
		if st.DeflateTimer > 0 {
			st.DeflateTimer--
		} else {
			nextPuff = curPuff - 1
			st.DeflateTimer = PufferfishDeflateDelayTicks
		}
	}
	if len(nearbyPlayers) > 0 && nextPuff > PufferfishDeflated && canSting {
		st.StingCooldownTicks = PufferfishStingCooldown
	}
	mgr.mu.Unlock()

	if nextPuff != curPuff {
		s.SetPufferfishPuffState(entityID, nextPuff)
	}
	if len(nearbyPlayers) > 0 && nextPuff > PufferfishDeflated && canSting {
		stingDmg := float32(nextPuff + 1) // 2.0 at half-puffed, 3.0 at fully puffed
		poisonTicks := PufferfishPoisonBaseTicks * nextPuff
		cp := ent.ChunkPos()
		for _, p := range nearbyPlayers {
			s.DamagePlayerWithArmor(p, stingDmg, "mob")
			s.ApplyPlayerEffect(p, NewStatusEffectInstance(EffectPoison, poisonTicks, 0))
		}
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
			EncodeSoundPacket(SoundPufferfishSting, SoundSourceHostile, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
	}
}

func (s *Server) tickGuardians(rng *rand.Rand) {
	mgr := s.aquaticManager()
	mgr.mu.RLock()
	if len(mgr.guardians) == 0 {
		mgr.mu.RUnlock()
		return
	}
	ids := make([]int32, 0, len(mgr.guardians))
	for id := range mgr.guardians {
		ids = append(ids, id)
	}
	mgr.mu.RUnlock()

	peaceful := s.Difficulty() == DifficultyPeaceful
	for _, id := range ids {
		ent := s.entities.Get(id)
		if ent == nil {
			mgr.mu.Lock()
			delete(mgr.guardians, id)
			mgr.mu.Unlock()
			continue
		}
		if peaceful && canonicalResourceName(ent.TypeName) == MobTypeGuardian {
			continue
		}
		dim := ent.EffectiveDimension()
		mgr.mu.Lock()
		st := mgr.guardians[id]
		if st == nil {
			mgr.mu.Unlock()
			continue
		}
		isElder := st.IsElder
		if isElder {
			if st.FatigueCooldown <= 0 {
				st.FatigueCooldown = ElderGuardianFatigueIntervalTicks
				mgr.mu.Unlock()
				s.ApplyElderGuardianMiningFatigueAura(id)
				mgr.mu.Lock()
				st = mgr.guardians[id]
				if st == nil {
					mgr.mu.Unlock()
					continue
				}
			} else {
				st.FatigueCooldown--
			}
		}
		mgr.mu.Unlock()

		if peaceful {
			continue
		}

		// Find nearest valid target (survival/adventure player, squid/glow_squid, or axolotl)
		// within 15 blocks with unobstructed line-of-sight.
		cache := s.DimensionChunks(dim)
		maxDistSq := GuardianLaserRange * GuardianLaserRange
		var bestPlayer *PlayerSession
		var bestSquid *world.Entity
		bestDistSq := maxDistSq

		for _, p := range s.onlinePlayersSnapshot() {
			if p == nil || p.IsDead() {
				continue
			}
			snap := p.Snapshot()
			if world.NormalizeDimension(snap.Dimension) != dim {
				continue
			}
			if snap.GameMode == GameModeCreative || snap.GameMode == GameModeSpectator {
				continue
			}
			dx := snap.X - ent.X
			dy := snap.Y - ent.Y
			dz := snap.Z - ent.Z
			dSq := dx*dx + dy*dy + dz*dz
			if dSq <= bestDistSq && HasGuardianLaserLineOfSight(cache, ent.X, ent.Y+0.425, ent.Z, snap.X, snap.Y+0.9, snap.Z) {
				bestDistSq = dSq
				bestPlayer = p
			}
		}

		if bestPlayer == nil {
			allEnts := s.entities.AppendAll(nil)
			for i := range allEnts {
				other := &allEnts[i]
				if other.EffectiveDimension() != dim {
					continue
				}
				onorm := canonicalResourceName(other.TypeName)
				if onorm != MobTypeSquid && onorm != MobTypeGlowSquid && onorm != MobTypeAxolotl {
					continue
				}
				dx := other.X - ent.X
				dy := other.Y - ent.Y
				dz := other.Z - ent.Z
				dSq := dx*dx + dy*dy + dz*dz
				if dSq <= bestDistSq && HasGuardianLaserLineOfSight(cache, ent.X, ent.Y+0.425, ent.Z, other.X, other.Y+0.4, other.Z) {
					bestDistSq = dSq
					bestSquid = other
				}
			}
		}

		var targetID int32
		if bestPlayer != nil {
			targetID = bestPlayer.EntityID
		} else if bestSquid != nil {
			targetID = bestSquid.ID
		}

		reqTicks := GuardianLaserChargeTicks
		laserDmg := GuardianLaserDamage
		magicDmg := GuardianMagicLaserDamage
		swimSpeed := GuardianSwimSpeed
		if isElder {
			reqTicks = ElderGuardianLaserChargeTicks
			laserDmg = ElderGuardianLaserDamage
			magicDmg = ElderGuardianMagicLaserDamage
			swimSpeed = ElderGuardianSwimSpeed
		}

		inWater := s.isEntityInWater(dim, ent.X, ent.Y, ent.Z)

		mgr.mu.Lock()
		st = mgr.guardians[id]
		if st == nil {
			mgr.mu.Unlock()
			continue
		}
		prevTarget := st.AttackTargetID
		prevMoving := st.Moving
		var fireLaser bool
		var doFlop bool
		var swimDX, swimDY, swimDZ float64
		if targetID != 0 {
			if inWater {
				st.OutOfWaterTicks = 0
			} else {
				st.OutOfWaterTicks++
			}
			st.Moving = false // Extend spikes and lock laser beam onto target!
			if st.AttackTargetID != targetID {
				st.AttackTargetID = targetID
				st.LaserChargeTicks = 1
			} else {
				st.LaserChargeTicks++
				if st.LaserChargeTicks >= reqTicks {
					fireLaser = true
					st.LaserChargeTicks = 0
					st.AttackTargetID = 0
				}
			}
		} else {
			st.AttackTargetID = 0
			st.LaserChargeTicks = 0
			if inWater {
				st.OutOfWaterTicks = 0
				st.Moving = true // Retract spikes while cruising underwater
				if st.SwimTimer <= 0 {
					st.SwimTimer = 40
					if rng != nil {
						st.SwimTimer = 30 + rng.Intn(40)
					}
					if st.HasHome {
						hdx := st.HomeX - ent.X
						hdy := st.HomeY - ent.Y
						hdz := st.HomeZ - ent.Z
						hDist := math.Sqrt(hdx*hdx + hdy*hdy + hdz*hdz)
						rad := st.HomeRadius
						if rad <= 0 {
							rad = ElderGuardianHomeRadius
						}
						if hDist > rad && hDist > 1e-4 {
							st.SwimDirX = hdx / hDist
							st.SwimDirY = hdy / hDist
							st.SwimDirZ = hdz / hDist
						}
					}
					if st.SwimDirX == 0 && st.SwimDirY == 0 && st.SwimDirZ == 0 {
						angle := 0.0
						if rng != nil {
							angle = rng.Float64() * 2.0 * math.Pi
						}
						st.SwimDirX = math.Cos(angle)
						st.SwimDirY = 0
						st.SwimDirZ = math.Sin(angle)
					}
				} else {
					st.SwimTimer--
				}
				swimDX, swimDY, swimDZ = st.SwimDirX*swimSpeed, st.SwimDirY*swimSpeed, st.SwimDirZ*swimSpeed
			} else {
				st.OutOfWaterTicks++
				st.Moving = false // Spikes extended while flopping out of water on land
				if st.OutOfWaterTicks == 1 || st.OutOfWaterTicks%GuardianFlopIntervalTicks == 0 {
					doFlop = true
				}
			}
		}
		curTarget := st.AttackTargetID
		curMoving := st.Moving
		mgr.mu.Unlock()

		cp := ent.ChunkPos()
		if curTarget != prevTarget || curMoving != prevMoving {
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
				EncodeGuardianDataPacket(id, curMoving, curTarget))
		}
		if fireLaser {
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
				EncodeSoundPacket(SoundGuardianAttack, SoundSourceHostile, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
			if bestPlayer != nil {
				s.AttackPlayerFromMob(id, ent.TypeName, ent.X, ent.Z, bestPlayer, laserDmg)
				if !bestPlayer.IsDead() {
					s.DamagePlayerWithEffects(bestPlayer, magicDmg, "indirect_magic")
				}
			} else if bestSquid != nil {
				s.applyDamageToMob(bestSquid, laserDmg+magicDmg, nil, 0, false, false)
			}
		} else if curMoving && (swimDX != 0 || swimDY != 0 || swimDZ != 0) {
			nx, ny, nz := ent.X+swimDX, ent.Y+swimDY, ent.Z+swimDZ
			if cache == nil || s.isEntityInWater(dim, nx, ny, nz) {
				s.entities.Update(id, func(e *world.Entity) {
					e.X = nx
					e.Y = ny
					e.Z = nz
					e.VelocityX = clampVelocityShort(swimDX)
					e.VelocityY = clampVelocityShort(swimDY)
					e.VelocityZ = clampVelocityShort(swimDZ)
				})
			} else {
				mgr.mu.Lock()
				if gst := mgr.guardians[id]; gst != nil {
					gst.SwimDirX = -gst.SwimDirX
					gst.SwimDirZ = -gst.SwimDirZ
				}
				mgr.mu.Unlock()
			}
		} else if doFlop {
			flopSX, flopSZ := 0.0, 0.0
			if rng != nil {
				ang := rng.Float64() * 2.0 * math.Pi
				flopSX = math.Cos(ang) * 0.15
				flopSZ = math.Sin(ang) * 0.15
			}
			s.entities.Update(id, func(e *world.Entity) {
				e.VelocityX = clampVelocityShort(flopSX)
				e.VelocityY = clampVelocityShort(GuardianFlopVelocityY)
				e.VelocityZ = clampVelocityShort(flopSZ)
				e.OnGround = false
			})
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
				EncodeSoundPacket(SoundGuardianFlop, SoundSourceHostile, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
		}
	}
	_ = rng
}
