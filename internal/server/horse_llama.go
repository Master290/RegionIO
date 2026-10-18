package server

import (
	"encoding/json"
	"errors"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

// Equine (Horse, Donkey, Mule, Skeleton Horse, Zombie Horse), Llama, Trader Llama,
// Llama Spit, and Wandering Trader constants for Minecraft 26.1.2 (protocol 775).
const (
	MobTypeSkeletonHorse   = "minecraft:skeleton_horse"
	MobTypeZombieHorse     = "minecraft:zombie_horse"
	MobTypeWanderingTrader = "minecraft:wandering_trader"
	EntityTypeLlamaSpit    = "minecraft:llama_spit"
	EntityTypeLightning    = "minecraft:lightning_bolt"

	// BuiltInRegistries.ENTITY_TYPE numeric network IDs in Minecraft 26.1.2 (protocol 775).
	EntityTypeLightningBoltID   int = 77
	EntityTypeLlamaSpitID       int = 79
	EntityTypeSkeletonHorseID   int = 116
	EntityTypeWanderingTraderID int = 141
	EntityTypeZombieHorseID     int = 151

	// Equine & Llama randomized attribute ranges (vanilla 26.1.2 AbstractHorse).
	MinEquineMaxHealth     float32 = 15.0
	MaxEquineMaxHealth     float32 = 30.0
	MinHorseMovementSpeed  float64 = 0.1125
	MaxHorseMovementSpeed  float64 = 0.3375
	DonkeyMovementSpeed    float64 = 0.175
	MinHorseJumpStrength   float64 = 0.4
	MaxHorseJumpStrength   float64 = 1.0
	DonkeyJumpStrength     float64 = 0.5
	SkeletonHorseMaxHealth float32 = 15.0
	SkeletonHorseSpeed     float64 = 0.2
	ZombieHorseMaxHealth   float32 = 15.0
	ZombieHorseSpeed       float64 = 0.2
	WanderingTraderMaxHP   float32 = 20.0
	LlamaMovementSpeed     float64 = 0.175
	LlamaJumpStrength      float64 = 0.5

	// Equine inventory & equipment constants.
	EquineChestSlotCount = 15 // Donkey / Mule chest slots (3x5); Llama has 3 * Strength (3..15)
	MinLlamaStrength     = 1
	MaxLlamaStrength     = 5
	EquipmentSlotBody    = byte(6) // EquipmentSlot.BODY in 26.1.2 ClientboundSetEquipmentPacket

	// Horse coat variants (0..6) and markings (0..4) packed as Variant | (Markings << 8).
	HorseVariantWhite     int32 = 0
	HorseVariantCreamy    int32 = 1
	HorseVariantChestnut  int32 = 2
	HorseVariantBrown     int32 = 3
	HorseVariantBlack     int32 = 4
	HorseVariantGray      int32 = 5
	HorseVariantDarkBrown int32 = 6
	HorseVariantCount     int32 = 7

	HorseMarkingNone       int32 = 0
	HorseMarkingWhite      int32 = 1
	HorseMarkingWhiteField int32 = 2
	HorseMarkingWhiteDots  int32 = 3
	HorseMarkingBlackDots  int32 = 4
	HorseMarkingCount      int32 = 5

	// Llama variants (0..3).
	LlamaVariantCreamy int32 = 0
	LlamaVariantWhite  int32 = 1
	LlamaVariantBrown  int32 = 2
	LlamaVariantGray   int32 = 3
	LlamaVariantCount  int32 = 4

	// SynchedEntityData indices in Minecraft 26.1.2 (protocol 775).
	EntityDataHorseTypeVariantID byte = 18 // Horse.DATA_ID_TYPE_VARIANT (INT serializer 1)
	EntityDataChestedHorseID     byte = 18 // AbstractChestedHorse.DATA_ID_CHEST (BOOLEAN serializer 8)
	EntityDataLlamaStrengthID    byte = 19 // Llama.DATA_STRENGTH_ID (INT serializer 1, 1..5)
	EntityDataLlamaVariantID     byte = 20 // Llama.DATA_VARIANT_ID (INT serializer 1, 0..3)

	// AbstractHorse flags (index 17 byte).
	AbstractHorseFlagBred      byte = 0x08
	AbstractHorseFlagEating    byte = 0x10
	AbstractHorseFlagOpenMouth byte = 0x40

	// Skeleton trap constants.
	SkeletonTrapMaxAgeTicks     int     = 18000 // 15 minutes (18000 ticks) before an untriggered trap despawns
	SkeletonTrapTriggerDistance float64 = 10.0  // Triggers when a player approaches within 10 blocks
	SkeletonTrapExtraHorsemen   int     = 3     // Spawns 3 additional skeleton horsemen (4 total)

	// Wandering Trader constants.
	WanderingTraderDefaultDespawnTicks int = 48000 // 40 minutes (2 MC days)

	// Llama spit projectile constants.
	LlamaSpitDamage       float32 = 1.0
	LlamaSpitSpeed        float64 = 1.1
	LlamaSpitGravity      float64 = 0.06
	LlamaSpitDrag         float64 = 0.99
	LlamaSpitMaxAgeTicks  int     = 100
	LlamaSpitHitboxRadius float64 = 0.65
	LlamaSpitCooldown     int     = 40

	// Canonical sound effects for Horses, Donkeys, Mules, Llamas, Skeleton/Zombie Horses, and Wandering Traders.
	SoundHorseAmbient         = "minecraft:entity.horse.ambient"
	SoundHorseAngry           = "minecraft:entity.horse.angry"
	SoundHorseArmor           = "minecraft:entity.horse.armor"
	SoundHorseDeath           = "minecraft:entity.horse.death"
	SoundHorseEat             = "minecraft:entity.horse.eat"
	SoundHorseGallop          = "minecraft:entity.horse.gallop"
	SoundHorseHurt            = "minecraft:entity.horse.hurt"
	SoundHorseJump            = "minecraft:entity.horse.jump"
	SoundHorseSaddle          = "minecraft:entity.horse.saddle"
	SoundDonkeyAmbient        = "minecraft:entity.donkey.ambient"
	SoundDonkeyAngry          = "minecraft:entity.donkey.angry"
	SoundDonkeyChest          = "minecraft:entity.donkey.chest"
	SoundDonkeyDeath          = "minecraft:entity.donkey.death"
	SoundDonkeyEat            = "minecraft:entity.donkey.eat"
	SoundDonkeyHurt           = "minecraft:entity.donkey.hurt"
	SoundMuleAmbient          = "minecraft:entity.mule.ambient"
	SoundMuleAngry            = "minecraft:entity.mule.angry"
	SoundMuleChest            = "minecraft:entity.mule.chest"
	SoundMuleDeath            = "minecraft:entity.mule.death"
	SoundMuleEat              = "minecraft:entity.mule.eat"
	SoundMuleHurt             = "minecraft:entity.mule.hurt"
	SoundLlamaAmbient         = "minecraft:entity.llama.ambient"
	SoundLlamaAngry           = "minecraft:entity.llama.angry"
	SoundLlamaChest           = "minecraft:entity.llama.chest"
	SoundLlamaDeath           = "minecraft:entity.llama.death"
	SoundLlamaEat             = "minecraft:entity.llama.eat"
	SoundLlamaHurt            = "minecraft:entity.llama.hurt"
	SoundLlamaSpit            = "minecraft:entity.llama.spit"
	SoundLlamaSwag            = "minecraft:entity.llama.swag"
	SoundSkeletonHorseAmbient = "minecraft:entity.skeleton_horse.ambient"
	SoundSkeletonHorseDeath   = "minecraft:entity.skeleton_horse.death"
	SoundSkeletonHorseHurt    = "minecraft:entity.skeleton_horse.hurt"
	SoundZombieHorseAmbient   = "minecraft:entity.zombie_horse.ambient"
	SoundZombieHorseDeath     = "minecraft:entity.zombie_horse.death"
	SoundZombieHorseHurt      = "minecraft:entity.zombie_horse.hurt"
	SoundWanderingTraderDrinkMilk   = "minecraft:entity.wandering_trader.drink_milk"
	SoundWanderingTraderDrinkPotion = "minecraft:entity.wandering_trader.drink_potion"
	SoundWanderingTraderNo          = "minecraft:entity.wandering_trader.no"
	SoundWanderingTraderTrade       = "minecraft:entity.wandering_trader.trade"
	SoundWanderingTraderYes         = "minecraft:entity.wandering_trader.yes"
	SoundWanderingTraderHurt        = "minecraft:entity.wandering_trader.hurt"
	SoundWanderingTraderDeath       = "minecraft:entity.wandering_trader.death"
)

// EquineAndLlamaState holds extended attributes, variant/markings, armor/carpet,
// chest inventory, riding/jump state, and skeleton-trap state for a horse,
// donkey, mule, skeleton_horse, zombie_horse, llama, or trader_llama.
type EquineAndLlamaState struct {
	EntityID         int32                            `json:"entity_id"`
	TypeName         string                           `json:"type_name"`
	MaxHealth        float32                          `json:"max_health"`
	MovementSpeed    float64                          `json:"movement_speed"`
	JumpStrength     float64                          `json:"jump_strength"`
	HorseVariant     int32                            `json:"horse_variant"`
	HorseMarkings    int32                            `json:"horse_markings"`
	LlamaVariant     int32                            `json:"llama_variant"`
	LlamaStrength    int32                            `json:"llama_strength"`
	CarpetColor      int32                            `json:"carpet_color"` // -1 when no carpet equipped
	ArmorItem        world.ItemStack                  `json:"armor_item"`
	HasChest         bool                             `json:"has_chest"`
	ChestItems       [EquineChestSlotCount]world.ItemStack `json:"chest_items"`
	RiderEntityID    int32                            `json:"rider_entity_id"`
	JumpPower        int32                            `json:"jump_power"`
	RearingTicks     int                              `json:"rearing_ticks"`
	SkeletonTrap     bool                             `json:"skeleton_trap"`
	SkeletonTrapTime int                              `json:"skeleton_trap_time"`
	TraderOwnerID    int32                            `json:"trader_owner_id"`
	SpitCooldown     int                              `json:"spit_cooldown"`
}

// LlamaSpitState tracks an active `minecraft:llama_spit` projectile entity.
type LlamaSpitState struct {
	EntityID        int32
	ShooterEntityID int32
	AgeTicks        int
}

// WanderingTraderState tracks an active `minecraft:wandering_trader` entity and
// its despawn timer, night invisibility state, and linked trader llamas.
type WanderingTraderState struct {
	EntityID       int32   `json:"entity_id"`
	DespawnDelay   int     `json:"despawn_delay"`
	InvisibleNight bool    `json:"invisible_night"`
	LlamaIDs       []int32 `json:"llama_ids"`
}

type serverEquineManager struct {
	mu      sync.RWMutex
	equines map[int32]*EquineAndLlamaState
	spits   map[int32]*LlamaSpitState
	traders map[int32]*WanderingTraderState
}

var equineManagers sync.Map // map[*Server]*serverEquineManager

func (s *Server) equineManager() *serverEquineManager {
	if s == nil {
		return &serverEquineManager{
			equines: make(map[int32]*EquineAndLlamaState),
			spits:   make(map[int32]*LlamaSpitState),
			traders: make(map[int32]*WanderingTraderState),
		}
	}
	if v, ok := equineManagers.Load(s); ok {
		return v.(*serverEquineManager)
	}
	mgr := &serverEquineManager{
		equines: make(map[int32]*EquineAndLlamaState),
		spits:   make(map[int32]*LlamaSpitState),
		traders: make(map[int32]*WanderingTraderState),
	}
	actual, _ := equineManagers.LoadOrStore(s, mgr)
	return actual.(*serverEquineManager)
}

// --- Vehicle Hooks Integration (Agent B 943fdd5) ---

// IsVehicleEntity reports whether entityID is an active minecart or boat/raft entity.
func (s *Server) IsVehicleEntity(entityID int32) bool {
	if s == nil || s.entities == nil || entityID <= 0 {
		return false
	}
	ent := s.entities.Get(entityID)
	return ent != nil && IsVehicleEntity(ent.TypeName)
}

// MountEntityInVehicle mounts passengerEntityID into vehicleEntityID (boat or
// rideable minecart), returning true if mounted.
func (s *Server) MountEntityInVehicle(passengerEntityID, vehicleEntityID int32) bool {
	if s == nil {
		return false
	}
	_, ok := s.MountEntityOnVehicle(passengerEntityID, vehicleEntityID)
	return ok
}

// EntityMountedVehicle returns the vehicle entity ID currently carrying
// passengerEntityID, or 0 if not riding a vehicle.
func (s *Server) EntityMountedVehicle(passengerEntityID int32) int32 {
	if s == nil || passengerEntityID <= 0 {
		return 0
	}
	mgr := s.vehicleManager()
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	return mgr.byPassengerEntity[passengerEntityID]
}

// DismountEntityFromVehicle dismounts passengerEntityID from any boat or
// minecart it is currently riding, placing it at a safe dismount position and
// broadcasting ClientboundSetPassengersPacket (0x6b).
func (s *Server) DismountEntityFromVehicle(passengerEntityID int32) bool {
	if s == nil || passengerEntityID <= 0 {
		return false
	}
	if sess := s.playerByEntityID(passengerEntityID); sess != nil {
		return s.DismountPlayerVehicle(sess)
	}
	mgr := s.vehicleManager()
	mgr.mu.Lock()
	vehicleID := mgr.byPassengerEntity[passengerEntityID]
	if vehicleID <= 0 {
		mgr.mu.Unlock()
		return false
	}
	delete(mgr.byPassengerEntity, passengerEntityID)
	var passengersCopy []int32
	if st := mgr.vehicles[vehicleID]; st != nil {
		for i, pid := range st.Passengers {
			if pid == passengerEntityID {
				st.Passengers = append(st.Passengers[:i], st.Passengers[i+1:]...)
				break
			}
		}
		passengersCopy = make([]int32, len(st.Passengers))
		copy(passengersCopy, st.Passengers)
	}
	mgr.mu.Unlock()

	if s.entities != nil {
		if vehEnt := s.entities.Get(vehicleID); vehEnt != nil {
			dim := vehEnt.EffectiveDimension()
			cp := vehEnt.ChunkPos()
			dx, dy, dz := s.resolveSafeDismountPos(dim, vehEnt.X, vehEnt.Y, vehEnt.Z)
			s.entities.Update(passengerEntityID, func(e *world.Entity) {
				e.X = dx
				e.Y = dy
				e.Z = dz
				e.OnGround = true
			})
			s.broadcastVehiclePassengers(dim, cp.X, cp.Z, vehicleID, passengersCopy)
		}
	}
	return true
}

// --- Equine & Llama Classification, Attributes & Inheritance ---

// IsHorseLikeMob reports whether typeName is any horse-family mob (horse,
// donkey, mule, skeleton_horse, zombie_horse, llama, or trader_llama).
func IsHorseLikeMob(typeName string) bool {
	switch canonicalResourceName(typeName) {
	case MobTypeHorse, MobTypeDonkey, MobTypeMule,
		MobTypeSkeletonHorse, MobTypeZombieHorse,
		MobTypeLlama, MobTypeTraderLlama:
		return true
	default:
		return false
	}
}

// IsChestedEquineMob reports whether typeName can equip a chest (donkey, mule,
// llama, or trader_llama).
func IsChestedEquineMob(typeName string) bool {
	switch canonicalResourceName(typeName) {
	case MobTypeDonkey, MobTypeMule, MobTypeLlama, MobTypeTraderLlama:
		return true
	default:
		return false
	}
}

// PackHorseTypeVariant packs coat variant (0..6) and markings (0..4) into
// Horse.DATA_ID_TYPE_VARIANT: (variant & 0xFF) | ((markings & 0xFF) << 8).
func PackHorseTypeVariant(variant, markings int32) int32 {
	if variant < 0 {
		variant = 0
	}
	if markings < 0 {
		markings = 0
	}
	return (variant & 0xFF) | ((markings & 0xFF) << 8)
}

// UnpackHorseTypeVariant unpacks coat variant (0..6) and markings (0..4) from
// Horse.DATA_ID_TYPE_VARIANT.
func UnpackHorseTypeVariant(packed int32) (variant, markings int32) {
	return packed & 0xFF, (packed >> 8) & 0xFF
}

// RollEquineMaxHealth generates a vanilla 26.1.2 AbstractHorse max health in
// [15.0, 30.0] using 15 + rng.Intn(8) + rng.Intn(9).
func RollEquineMaxHealth(rng *rand.Rand) float32 {
	if rng == nil {
		return DefaultHorseMaxHealth
	}
	return float32(15 + rng.Intn(8) + rng.Intn(9))
}

// RollHorseMovementSpeed generates a vanilla 26.1.2 Horse movement speed in
// [0.1125, 0.3375]: (0.45 + 0.3*f1 + 0.3*f2 + 0.3*f3) * 0.25.
func RollHorseMovementSpeed(rng *rand.Rand) float64 {
	if rng == nil {
		return 0.225
	}
	return (0.45 + rng.Float64()*0.3 + rng.Float64()*0.3 + rng.Float64()*0.3) * 0.25
}

// RollHorseJumpStrength generates a vanilla 26.1.2 Horse jump strength in
// [0.4, 1.0]: 0.4 + 0.2*f1 + 0.2*f2 + 0.2*f3.
func RollHorseJumpStrength(rng *rand.Rand) float64 {
	if rng == nil {
		return 0.7
	}
	return 0.4 + rng.Float64()*0.2 + rng.Float64()*0.2 + rng.Float64()*0.2
}

// RollLlamaStrength rolls a wild Llama/Trader Llama strength in 1..5 using
// vanilla's distribution (1..3 base, with 1/3 chance to add up to +2).
func RollLlamaStrength(rng *rand.Rand) int32 {
	if rng == nil {
		return 3
	}
	str := int32(1 + rng.Intn(3))
	if rng.Intn(3) == 0 {
		str += int32(1 + rng.Intn(2))
	}
	if str > MaxLlamaStrength {
		str = MaxLlamaStrength
	}
	return str
}

// InheritEquineAttributes computes a bred foal/mule's MaxHealth, MovementSpeed,
// and JumpStrength from its two parents using vanilla 26.1.2 AbstractHorse
// inheritance (average of parentA, parentB, and a newly rolled random value,
// clamped to the valid equine bounds).
func InheritEquineAttributes(
	hpA, hpB float32,
	speedA, speedB float64,
	jumpA, jumpB float64,
	rng *rand.Rand,
) (childHP float32, childSpeed, childJump float64) {
	randHP := RollEquineMaxHealth(rng)
	childHP = (hpA + hpB + randHP) / 3.0
	if childHP < MinEquineMaxHealth {
		childHP = MinEquineMaxHealth
	}
	if childHP > MaxEquineMaxHealth {
		childHP = MaxEquineMaxHealth
	}

	randSpeed := RollHorseMovementSpeed(rng)
	childSpeed = (speedA + speedB + randSpeed) / 3.0
	if childSpeed < MinHorseMovementSpeed {
		childSpeed = MinHorseMovementSpeed
	}
	if childSpeed > MaxHorseMovementSpeed {
		childSpeed = MaxHorseMovementSpeed
	}

	randJump := RollHorseJumpStrength(rng)
	childJump = (jumpA + jumpB + randJump) / 3.0
	if childJump < MinHorseJumpStrength {
		childJump = MinHorseJumpStrength
	}
	if childJump > MaxHorseJumpStrength {
		childJump = MaxHorseJumpStrength
	}
	return childHP, childSpeed, childJump
}

// InheritHorseCoatAndMarkings selects a foal's coat color (0..6) and markings
// (0..4) from its two parents using vanilla 26.1.2 Horse.getOffspring rules:
//   - Coat: roll 0..8 -> 0..3 parentA, 4..7 parentB, 8 random (0..6).
//   - Markings: roll 0..4 -> 0..1 parentA, 2..3 parentB, 4 random (0..4).
func InheritHorseCoatAndMarkings(coatA, markA, coatB, markB int32, rng *rand.Rand) (coat, markings int32) {
	if rng == nil {
		return coatA, markA
	}
	cRoll := rng.Intn(9)
	switch {
	case cRoll < 4:
		coat = coatA
	case cRoll < 8:
		coat = coatB
	default:
		coat = int32(rng.Intn(int(HorseVariantCount)))
	}
	mRoll := rng.Intn(5)
	switch {
	case mRoll < 2:
		markings = markA
	case mRoll < 4:
		markings = markB
	default:
		markings = int32(rng.Intn(int(HorseMarkingCount)))
	}
	return coat, markings
}

// InheritLlamaStrength computes a baby llama's Strength (1..5) from its two
// parents according to vanilla 26.1.2 Llama.setOffspringAttributes.
func InheritLlamaStrength(strengthA, strengthB int32, rng *rand.Rand) int32 {
	maxParent := strengthA
	if strengthB > maxParent {
		maxParent = strengthB
	}
	if maxParent < MinLlamaStrength {
		maxParent = MinLlamaStrength
	}
	str := int32(1)
	if rng != nil {
		str = int32(1 + rng.Intn(int(maxParent)))
		if rng.Float32() < 0.03 {
			str++
		}
	} else {
		str = maxParent
	}
	if str < MinLlamaStrength {
		str = MinLlamaStrength
	}
	if str > MaxLlamaStrength {
		str = MaxLlamaStrength
	}
	return str
}

// HorseArmorDefense returns the vanilla 26.1.2 armor defense points for a horse
// armor item (leather=3, copper=4, iron=5, golden=7, diamond=11), or (0, false)
// if itemName is not valid horse armor.
func HorseArmorDefense(itemName string) (defense float32, ok bool) {
	switch canonicalResourceName(itemName) {
	case "minecraft:leather_horse_armor":
		return 3.0, true
	case "minecraft:copper_horse_armor":
		return 4.0, true
	case "minecraft:iron_horse_armor":
		return 5.0, true
	case "minecraft:golden_horse_armor":
		return 7.0, true
	case "minecraft:diamond_horse_armor":
		return 11.0, true
	default:
		return 0, false
	}
}

// IsHorseArmorItem reports whether itemName is a horse armor item.
func IsHorseArmorItem(itemName string) bool {
	_, ok := HorseArmorDefense(itemName)
	return ok
}

// CarpetColorByItemName maps a `minecraft:<color>_carpet` item to its 0..15
// DyeColor index, or (-1, false) if itemName is not a wool carpet.
func CarpetColorByItemName(itemName string) (int32, bool) {
	norm := strings.TrimPrefix(canonicalResourceName(itemName), "minecraft:")
	if !strings.HasSuffix(norm, "_carpet") || norm == "moss_carpet" || norm == "pale_moss_carpet" {
		return -1, false
	}
	colorName := strings.TrimSuffix(norm, "_carpet")
	return DyeColorIndexByItemName("minecraft:" + colorName + "_dye")
}

// CarpetItemNameByColor returns the canonical `minecraft:<color>_carpet` item
// identifier for a 0..15 DyeColor index.
func CarpetItemNameByColor(color int32) string {
	colors := [16]string{
		"white", "orange", "magenta", "light_blue",
		"yellow", "lime", "pink", "gray",
		"light_gray", "cyan", "purple", "blue",
		"brown", "green", "red", "black",
	}
	if color < 0 || int(color) >= len(colors) {
		return ""
	}
	return "minecraft:" + colors[color] + "_carpet"
}

// EncodeBodyEquipmentPacket encodes a ClientboundSetEquipmentPacket (0x66) for
// EquipmentSlot.BODY (slot 6), used to render horse armor and llama carpets.
func EncodeBodyEquipmentPacket(entityID int32, stack world.ItemStack) []byte {
	w := protocol.NewWriter(24)
	w.VarInt(entityID)
	w.Byte(EquipmentSlotBody)
	itemID := stack.ResolvedItemID()
	if stack.IsEmpty() || itemID <= 0 {
		w.VarInt(0)
	} else {
		w.VarInt(stack.Count)
		w.VarInt(itemID)
		w.VarInt(0)
		w.VarInt(0)
	}
	return w.Bytes()
}

// EncodeHorseVariantDataPacket encodes a ClientboundSetEntityDataPacket (0x63)
// for a `minecraft:horse` entity carrying AbstractHorse flags (index 17) and
// packed coat/marking variant (index 18, INT).
func EncodeHorseVariantDataPacket(entityID int32, tamed, saddled, standing bool, variant, markings int32) []byte {
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
	w := protocol.NewWriter(20)
	w.VarInt(entityID)
	w.Byte(EntityDataAbstractHorseFlags)
	w.VarInt(EntityDataSerializerByte)
	w.Byte(flags)
	w.Byte(EntityDataHorseTypeVariantID)
	w.VarInt(EntityDataSerializerInt)
	w.VarInt(PackHorseTypeVariant(variant, markings))
	w.Byte(0xFF)
	return w.Bytes()
}

// EncodeChestedHorseDataPacket encodes a ClientboundSetEntityDataPacket (0x63)
// for a donkey or mule carrying AbstractHorse flags (index 17) and DATA_ID_CHEST
// (index 18, BOOLEAN).
func EncodeChestedHorseDataPacket(entityID int32, tamed, saddled, standing, hasChest bool) []byte {
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
	w := protocol.NewWriter(20)
	w.VarInt(entityID)
	w.Byte(EntityDataAbstractHorseFlags)
	w.VarInt(EntityDataSerializerByte)
	w.Byte(flags)
	w.Byte(EntityDataChestedHorseID)
	w.VarInt(EntityDataSerializerBool)
	w.Bool(hasChest)
	w.Byte(0xFF)
	return w.Bytes()
}

// EncodeLlamaDataPacket encodes a ClientboundSetEntityDataPacket (0x63) for a
// `minecraft:llama` or `minecraft:trader_llama` carrying AbstractHorse flags
// (17), DATA_ID_CHEST (18), DATA_STRENGTH_ID (19), and DATA_VARIANT_ID (20).
func EncodeLlamaDataPacket(entityID int32, tamed, hasChest bool, strength, variant int32) []byte {
	var flags byte
	if tamed {
		flags |= AbstractHorseFlagTamed
	}
	if strength < MinLlamaStrength {
		strength = MinLlamaStrength
	}
	if strength > MaxLlamaStrength {
		strength = MaxLlamaStrength
	}
	w := protocol.NewWriter(28)
	w.VarInt(entityID)
	w.Byte(EntityDataAbstractHorseFlags)
	w.VarInt(EntityDataSerializerByte)
	w.Byte(flags)
	w.Byte(EntityDataChestedHorseID)
	w.VarInt(EntityDataSerializerBool)
	w.Bool(hasChest)
	w.Byte(EntityDataLlamaStrengthID)
	w.VarInt(EntityDataSerializerInt)
	w.VarInt(strength)
	w.Byte(EntityDataLlamaVariantID)
	w.VarInt(EntityDataSerializerInt)
	w.VarInt(variant)
	w.Byte(0xFF)
	return w.Bytes()
}

// DecodeEquineOrLlamaDataPacket decodes a ClientboundSetEntityDataPacket (0x63)
// produced by EncodeHorseVariantDataPacket, EncodeChestedHorseDataPacket, or
// EncodeLlamaDataPacket.
func DecodeEquineOrLlamaDataPacket(r *protocol.Reader) (entityID int32, flags byte, hasChest bool, variantOrPacked, strength, llamaVariant int32, err error) {
	entityID, err = r.VarInt()
	if err != nil {
		return
	}
	for {
		idx, rErr := r.ReadByte()
		if rErr != nil {
			err = rErr
			return
		}
		if idx == 0xFF {
			break
		}
		ser, sErr := r.VarInt()
		if sErr != nil {
			err = sErr
			return
		}
		switch idx {
		case EntityDataAbstractHorseFlags:
			if ser != EntityDataSerializerByte {
				err = errors.New("equine: unexpected flags serializer")
				return
			}
			flags, err = r.ReadByte()
		case EntityDataHorseTypeVariantID: // also EntityDataChestedHorseID (index 18)
			if ser == EntityDataSerializerInt {
				variantOrPacked, err = r.VarInt()
			} else if ser == EntityDataSerializerBool {
				hasChest, err = r.Bool()
			} else {
				err = errors.New("equine: unexpected index 18 serializer")
				return
			}
		case EntityDataLlamaStrengthID:
			if ser != EntityDataSerializerInt {
				err = errors.New("equine: unexpected strength serializer")
				return
			}
			strength, err = r.VarInt()
		case EntityDataLlamaVariantID:
			if ser != EntityDataSerializerInt {
				err = errors.New("equine: unexpected llama variant serializer")
				return
			}
			llamaVariant, err = r.VarInt()
		default:
			err = errors.New("equine: unexpected metadata index")
			return
		}
		if err != nil {
			return
		}
	}
	return
}

func (s *Server) ensureEquineState(entityID int32, typeName string, rng *rand.Rand) *EquineAndLlamaState {
	if s == nil || entityID <= 0 {
		return nil
	}
	norm := canonicalResourceName(typeName)
	mgr := s.equineManager()
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	st, ok := mgr.equines[entityID]
	if ok && st != nil {
		return st
	}
	st = &EquineAndLlamaState{
		EntityID:    entityID,
		TypeName:    norm,
		CarpetColor: -1,
	}
	switch norm {
	case MobTypeHorse:
		st.MaxHealth = RollEquineMaxHealth(rng)
		st.MovementSpeed = RollHorseMovementSpeed(rng)
		st.JumpStrength = RollHorseJumpStrength(rng)
		if rng != nil {
			st.HorseVariant = int32(rng.Intn(int(HorseVariantCount)))
			st.HorseMarkings = int32(rng.Intn(int(HorseMarkingCount)))
		}
	case MobTypeDonkey:
		st.MaxHealth = RollEquineMaxHealth(rng)
		st.MovementSpeed = DonkeyMovementSpeed
		st.JumpStrength = DonkeyJumpStrength
	case MobTypeMule:
		st.MaxHealth = RollEquineMaxHealth(rng)
		st.MovementSpeed = RollHorseMovementSpeed(rng)
		st.JumpStrength = RollHorseJumpStrength(rng)
	case MobTypeSkeletonHorse:
		st.MaxHealth = SkeletonHorseMaxHealth
		st.MovementSpeed = SkeletonHorseSpeed
		st.JumpStrength = RollHorseJumpStrength(rng)
	case MobTypeZombieHorse:
		st.MaxHealth = ZombieHorseMaxHealth
		st.MovementSpeed = ZombieHorseSpeed
		st.JumpStrength = RollHorseJumpStrength(rng)
	case MobTypeLlama, MobTypeTraderLlama:
		st.MaxHealth = RollEquineMaxHealth(rng)
		st.MovementSpeed = LlamaMovementSpeed
		st.JumpStrength = LlamaJumpStrength
		st.LlamaStrength = RollLlamaStrength(rng)
		if rng != nil {
			st.LlamaVariant = int32(rng.Intn(int(LlamaVariantCount)))
		}
	default:
		st.MaxHealth = DefaultHorseMaxHealth
		st.MovementSpeed = 0.225
		st.JumpStrength = 0.7
	}
	mgr.equines[entityID] = st
	return st
}

// GetEquineState returns a snapshot of the EquineAndLlamaState for entityID.
func (s *Server) GetEquineState(entityID int32) (EquineAndLlamaState, bool) {
	if s == nil || entityID <= 0 {
		return EquineAndLlamaState{}, false
	}
	mgr := s.equineManager()
	mgr.mu.RLock()
	st, ok := mgr.equines[entityID]
	var out EquineAndLlamaState
	if ok && st != nil {
		out = *st
	}
	mgr.mu.RUnlock()
	return out, ok && st != nil
}

// SetHorseVariantAndMarkings sets the coat variant (0..6) and markings (0..4)
// on a `minecraft:horse` entity and broadcasts updated SynchedEntityData.
func (s *Server) SetHorseVariantAndMarkings(entityID, variant, markings int32) bool {
	if s == nil || s.entities == nil || entityID <= 0 {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeHorse {
		return false
	}
	if variant < 0 || variant >= HorseVariantCount {
		variant = 0
	}
	if markings < 0 || markings >= HorseMarkingCount {
		markings = 0
	}
	st := s.ensureEquineState(entityID, MobTypeHorse, nil)
	mgr := s.equineManager()
	mgr.mu.Lock()
	st.HorseVariant = variant
	st.HorseMarkings = markings
	mgr.mu.Unlock()
	if tst, ok := s.GetTamedMobState(entityID); ok {
		s.broadcastTamedMobMetadata(ent, tst)
	}
	return true
}

// SetLlamaStrengthAndVariant sets the strength (1..5) and variant (0..3) on a
// `minecraft:llama` or `minecraft:trader_llama` and broadcasts updated metadata.
func (s *Server) SetLlamaStrengthAndVariant(entityID, strength, variant int32) bool {
	if s == nil || s.entities == nil || entityID <= 0 {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil {
		return false
	}
	norm := canonicalResourceName(ent.TypeName)
	if norm != MobTypeLlama && norm != MobTypeTraderLlama {
		return false
	}
	if strength < MinLlamaStrength {
		strength = MinLlamaStrength
	}
	if strength > MaxLlamaStrength {
		strength = MaxLlamaStrength
	}
	if variant < 0 || variant >= LlamaVariantCount {
		variant = 0
	}
	st := s.ensureEquineState(entityID, norm, nil)
	mgr := s.equineManager()
	mgr.mu.Lock()
	st.LlamaStrength = strength
	st.LlamaVariant = variant
	mgr.mu.Unlock()
	if tst, ok := s.GetTamedMobState(entityID); ok {
		s.broadcastTamedMobMetadata(ent, tst)
	}
	return true
}

// EquipHorseArmor equips an armorStack (`leather_horse_armor`, `copper_horse_armor`,
// `iron_horse_armor`, `golden_horse_armor`, or `diamond_horse_armor`) onto a
// tamed adult `minecraft:horse`, playing `entity.horse.armor` and broadcasting
// `ClientboundSetEquipmentPacket` for `EquipmentSlot.BODY` (slot 6).
func (s *Server) EquipHorseArmor(entityID int32, armorStack world.ItemStack) bool {
	if s == nil || s.entities == nil || entityID <= 0 || armorStack.IsEmpty() {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeHorse || s.IsMobBaby(entityID) {
		return false
	}
	itemID := canonicalResourceName(armorStack.ResolvedID())
	if !IsHorseArmorItem(itemID) {
		return false
	}
	st := s.ensureEquineState(entityID, MobTypeHorse, nil)
	mgr := s.equineManager()
	mgr.mu.Lock()
	if !st.ArmorItem.IsEmpty() {
		mgr.mu.Unlock()
		return false
	}
	equipped := world.NewNamedItemStack(itemID, 1)
	st.ArmorItem = equipped
	mgr.mu.Unlock()

	cp := ent.ChunkPos()
	dim := ent.EffectiveDimension()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlaySetEquipment,
		EncodeBodyEquipmentPacket(entityID, equipped))
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundHorseArmor, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 0.5, 1.0, 0))
	return true
}

// RemoveHorseArmor unequips and returns any worn horse armor from entityID.
func (s *Server) RemoveHorseArmor(entityID int32) (world.ItemStack, bool) {
	if s == nil || s.entities == nil || entityID <= 0 {
		return world.ItemStack{}, false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeHorse {
		return world.ItemStack{}, false
	}
	st := s.ensureEquineState(entityID, MobTypeHorse, nil)
	mgr := s.equineManager()
	mgr.mu.Lock()
	if st.ArmorItem.IsEmpty() {
		mgr.mu.Unlock()
		return world.ItemStack{}, false
	}
	removed := st.ArmorItem
	st.ArmorItem = world.ItemStack{}
	mgr.mu.Unlock()

	cp := ent.ChunkPos()
	dim := ent.EffectiveDimension()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlaySetEquipment,
		EncodeBodyEquipmentPacket(entityID, world.ItemStack{}))
	return removed, true
}

// EquipLlamaCarpet equips a wool carpet of carpetColor (0..15) onto a tamed
// adult llama or trader_llama, playing `entity.llama.swag` and broadcasting
// `ClientboundSetEquipmentPacket` for `EquipmentSlot.BODY` (slot 6).
func (s *Server) EquipLlamaCarpet(entityID int32, carpetColor int32) bool {
	if s == nil || s.entities == nil || entityID <= 0 || carpetColor < 0 || carpetColor > 15 {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || s.IsMobBaby(entityID) {
		return false
	}
	norm := canonicalResourceName(ent.TypeName)
	if norm != MobTypeLlama && norm != MobTypeTraderLlama {
		return false
	}
	carpetName := CarpetItemNameByColor(carpetColor)
	if carpetName == "" {
		return false
	}
	st := s.ensureEquineState(entityID, norm, nil)
	mgr := s.equineManager()
	mgr.mu.Lock()
	if st.CarpetColor == carpetColor {
		mgr.mu.Unlock()
		return false
	}
	st.CarpetColor = carpetColor
	mgr.mu.Unlock()

	carpetStack := world.NewNamedItemStack(carpetName, 1)
	cp := ent.ChunkPos()
	dim := ent.EffectiveDimension()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlaySetEquipment,
		EncodeBodyEquipmentPacket(entityID, carpetStack))
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundLlamaSwag, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
	return true
}

// ShearLlamaCarpet removes an equipped carpet from a llama/trader_llama (when
// sheared with `minecraft:shears`), dropping the carpet item and clearing
// `EquipmentSlot.BODY`.
func (s *Server) ShearLlamaCarpet(entityID int32) (world.ItemStack, bool) {
	if s == nil || s.entities == nil || entityID <= 0 {
		return world.ItemStack{}, false
	}
	ent := s.entities.Get(entityID)
	if ent == nil {
		return world.ItemStack{}, false
	}
	norm := canonicalResourceName(ent.TypeName)
	if norm != MobTypeLlama && norm != MobTypeTraderLlama {
		return world.ItemStack{}, false
	}
	st := s.ensureEquineState(entityID, norm, nil)
	mgr := s.equineManager()
	mgr.mu.Lock()
	if st.CarpetColor < 0 {
		mgr.mu.Unlock()
		return world.ItemStack{}, false
	}
	color := st.CarpetColor
	st.CarpetColor = -1
	mgr.mu.Unlock()

	carpetName := CarpetItemNameByColor(color)
	dropped := world.NewNamedItemStack(carpetName, 1)
	dim := ent.EffectiveDimension()
	cp := ent.ChunkPos()
	if !dropped.IsEmpty() {
		s.SpawnItemEntityInDimension(dim, ent.X, ent.Y+0.5, ent.Z, dropped)
	}
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlaySetEquipment,
		EncodeBodyEquipmentPacket(entityID, world.ItemStack{}))
	return dropped, true
}

// EquipEquineChest attaches a `minecraft:chest` onto a tamed adult donkey,
// mule, llama, or trader_llama, unlocking its inventory (15 slots for
// donkey/mule, 3*Strength slots for llama/trader_llama).
func (s *Server) EquipEquineChest(entityID int32) bool {
	if s == nil || s.entities == nil || entityID <= 0 || s.IsMobBaby(entityID) {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || !IsChestedEquineMob(ent.TypeName) {
		return false
	}
	norm := canonicalResourceName(ent.TypeName)
	st := s.ensureEquineState(entityID, norm, nil)
	mgr := s.equineManager()
	mgr.mu.Lock()
	if st.HasChest {
		mgr.mu.Unlock()
		return false
	}
	st.HasChest = true
	mgr.mu.Unlock()

	snd := SoundDonkeyChest
	if norm == MobTypeMule {
		snd = SoundMuleChest
	} else if norm == MobTypeLlama || norm == MobTypeTraderLlama {
		snd = SoundLlamaChest
	}
	cp := ent.ChunkPos()
	dim := ent.EffectiveDimension()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(snd, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
	if tst, ok := s.GetTamedMobState(entityID); ok {
		s.broadcastTamedMobMetadata(ent, tst)
	}
	return true
}

// EquineChestSlotCountFor returns the active chest slot capacity of entityID:
//   - 0 if the mob does not have a chest attached
//   - 15 for a chested Donkey or Mule
//   - 3 * Strength (3..15) for a chested Llama or Trader Llama
func (s *Server) EquineChestSlotCountFor(entityID int32) int {
	st, ok := s.GetEquineState(entityID)
	if !ok || !st.HasChest {
		return 0
	}
	if st.TypeName == MobTypeLlama || st.TypeName == MobTypeTraderLlama {
		str := int(st.LlamaStrength)
		if str < MinLlamaStrength {
			str = MinLlamaStrength
		}
		if str > MaxLlamaStrength {
			str = MaxLlamaStrength
		}
		return 3 * str
	}
	return EquineChestSlotCount
}

// EquineInventorySlot returns the item in chest slot (0..capacity-1) of entityID.
func (s *Server) EquineInventorySlot(entityID int32, slot int) world.ItemStack {
	capSlots := s.EquineChestSlotCountFor(entityID)
	if slot < 0 || slot >= capSlots {
		return world.ItemStack{}
	}
	st, ok := s.GetEquineState(entityID)
	if !ok {
		return world.ItemStack{}
	}
	return st.ChestItems[slot]
}

// SetEquineInventorySlot sets the item in chest slot (0..capacity-1) of entityID.
func (s *Server) SetEquineInventorySlot(entityID int32, slot int, stack world.ItemStack) bool {
	capSlots := s.EquineChestSlotCountFor(entityID)
	if slot < 0 || slot >= capSlots {
		return false
	}
	mgr := s.equineManager()
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	st := mgr.equines[entityID]
	if st == nil || !st.HasChest {
		return false
	}
	st.ChestItems[slot] = stack
	return true
}

// AddItemToEquineChest merges stack into a chested donkey/mule/llama's chest
// inventory and returns the number of items added.
func (s *Server) AddItemToEquineChest(entityID int32, stack world.ItemStack) int32 {
	if s == nil || stack.IsEmpty() || stack.Count <= 0 {
		return 0
	}
	capSlots := s.EquineChestSlotCountFor(entityID)
	if capSlots <= 0 {
		return 0
	}
	itemID := stack.ResolvedItemID()
	if itemID <= 0 {
		return 0
	}
	maxStack := MaxItemStackSize
	if IsDamageableItem(stack.ResolvedID()) {
		maxStack = 1
	}
	mgr := s.equineManager()
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	st := mgr.equines[entityID]
	if st == nil || !st.HasChest {
		return 0
	}
	rem := stack.Count
	var added int32
	for i := 0; i < capSlots && rem > 0; i++ {
		cur := st.ChestItems[i]
		if !cur.IsEmpty() && cur.ResolvedItemID() == itemID && cur.Count < maxStack {
			take := maxStack - cur.Count
			if take > rem {
				take = rem
			}
			st.ChestItems[i].Count += take
			rem -= take
			added += take
		}
	}
	for i := 0; i < capSlots && rem > 0; i++ {
		if st.ChestItems[i].IsEmpty() {
			take := rem
			if take > maxStack {
				take = maxStack
			}
			st.ChestItems[i] = world.NewItemStack(itemID, take)
			rem -= take
			added += take
		}
	}
	return added
}

// EquineBodyEquipmentPacket returns a ClientboundSetEquipmentPacket (0x66) if
// entityID has horse armor or a llama carpet equipped in EquipmentSlot.BODY.
func (s *Server) EquineBodyEquipmentPacket(entityID int32) []byte {
	st, ok := s.GetEquineState(entityID)
	if !ok {
		return nil
	}
	if !st.ArmorItem.IsEmpty() {
		return EncodeBodyEquipmentPacket(entityID, st.ArmorItem)
	}
	if st.CarpetColor >= 0 {
		if name := CarpetItemNameByColor(st.CarpetColor); name != "" {
			return EncodeBodyEquipmentPacket(entityID, world.NewNamedItemStack(name, 1))
		}
	}
	return nil
}

// ReduceHorseDamageByArmor reduces incoming physical damage to a horse by its
// equipped horse armor defense points using CombatRules.getDamageAfterAbsorb.
func (s *Server) ReduceHorseDamageByArmor(entityID int32, rawDamage float32) float32 {
	if rawDamage <= 0 {
		return 0
	}
	st, ok := s.GetEquineState(entityID)
	if !ok || st.ArmorItem.IsEmpty() {
		return rawDamage
	}
	def, valid := HorseArmorDefense(st.ArmorItem.ResolvedID())
	if !valid || def <= 0 {
		return rawDamage
	}
	return DamageAfterArmor(rawDamage, def, 0)
}

// --- Charge Jump, Riding & Fall Damage ---

// ComputeEquineJumpScale converts a player's horse jump bar charge (0..100) into
// the vanilla PlayerRideableJumping jump scale in [0.0, 1.0]:
//   - jumpPower >= 90 -> 1.0
//   - jumpPower < 90  -> 0.4 + 0.4 * jumpPower / 90.0
func ComputeEquineJumpScale(jumpPower int32) float64 {
	if jumpPower <= 0 {
		return 0
	}
	if jumpPower >= 90 {
		return 1.0
	}
	return 0.4 + 0.4*float64(jumpPower)/90.0
}

// ComputeEquineJumpImpulse computes the vertical (vy) and forward (vx, vz)
// velocity impulse when a saddled equine executes a charged jump with
// jumpPower (0..100).
func ComputeEquineJumpImpulse(jumpStrength, movementSpeed float64, yaw float32, jumpPower int32, sprinting bool) (vx, vy, vz float64) {
	scale := ComputeEquineJumpScale(jumpPower)
	if scale <= 0 {
		return 0, 0, 0
	}
	vy = jumpStrength * scale
	rad := float64(yaw) * (math.Pi / 180.0)
	forward := movementSpeed * scale
	if sprinting {
		forward += 0.2 * scale
	}
	vx = -math.Sin(rad) * forward
	vz = math.Cos(rad) * forward
	return vx, vy, vz
}

// ComputeEquineFallDamage computes the fall damage for an equine/llama with
// jumpStrength falling fallDistance blocks:
//
//	ceil((fallDistance * 0.5 - 3.0) * (1.0 - (jumpStrength - 0.4) * 0.5))
//
// In vanilla AbstractHorse.calculateFallDamage, horses take half normal fall
// damage and high-jump horses have a higher safe fall threshold.
func ComputeEquineFallDamage(fallDistance, jumpStrength float64) float32 {
	safeBonus := 0.0
	if jumpStrength > 0.4 {
		safeBonus = (jumpStrength - 0.4) * 2.5
	}
	raw := math.Ceil(fallDistance*0.5 - 3.0 - safeBonus)
	if raw <= 0 {
		return 0
	}
	return float32(raw)
}

// ExecuteEquineJump performs a charged jump on a tamed, saddled horse/donkey/
// mule/skeleton_horse/zombie_horse with jumpPower in 1..100, setting the
// rearing/standing flag briefly, applying vertical + forward velocity, and
// playing `entity.horse.jump`.
func (s *Server) ExecuteEquineJump(entityID, jumpPower int32) (vx, vy, vz float64, ok bool) {
	if s == nil || s.entities == nil || entityID <= 0 || jumpPower <= 0 {
		return 0, 0, 0, false
	}
	if jumpPower > 100 {
		jumpPower = 100
	}
	ent := s.entities.Get(entityID)
	if ent == nil {
		return 0, 0, 0, false
	}
	norm := canonicalResourceName(ent.TypeName)
	switch norm {
	case MobTypeHorse, MobTypeDonkey, MobTypeMule, MobTypeSkeletonHorse, MobTypeZombieHorse:
	default:
		return 0, 0, 0, false
	}
	tst, hasTamed := s.GetTamedMobState(entityID)
	if !hasTamed || !tst.Tamed || (!tst.Saddled && norm != MobTypeSkeletonHorse && norm != MobTypeZombieHorse) {
		return 0, 0, 0, false
	}
	est := s.ensureEquineState(entityID, norm, nil)
	mgr := s.equineManager()
	mgr.mu.Lock()
	est.JumpPower = jumpPower
	est.RearingTicks = 10
	jumpStr := est.JumpStrength
	moveSpd := est.MovementSpeed
	mgr.mu.Unlock()

	vx, vy, vz = ComputeEquineJumpImpulse(jumpStr, moveSpd, ent.Yaw, jumpPower, jumpPower >= 90)
	s.entities.Update(entityID, func(e *world.Entity) {
		e.OnGround = false
		e.VelocityX = clampVelocityShort(vx)
		e.VelocityY = clampVelocityShort(vy)
		e.VelocityZ = clampVelocityShort(vz)
	})
	cp := ent.ChunkPos()
	dim := ent.EffectiveDimension()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlaySetEntityMotion,
		EncodeSetEntityMotionPacket(entityID, vx, vy, vz))
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundHorseJump, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 0.4, 1.0, 0))
	s.broadcastTamedMobMetadata(ent, tst)
	return vx, vy, vz, true
}

// MountEquine mounts riderEntityID onto an adult horse/donkey/mule/llama/
// skeleton_horse/zombie_horse and broadcasts ClientboundSetPassengersPacket.
func (s *Server) MountEquine(riderEntityID, equineEntityID int32) bool {
	if s == nil || s.entities == nil || riderEntityID <= 0 || equineEntityID <= 0 || s.IsMobBaby(equineEntityID) {
		return false
	}
	ent := s.entities.Get(equineEntityID)
	if ent == nil || !IsHorseLikeMob(ent.TypeName) {
		return false
	}
	est := s.ensureEquineState(equineEntityID, ent.TypeName, nil)
	mgr := s.equineManager()
	mgr.mu.Lock()
	est.RiderEntityID = riderEntityID
	mgr.mu.Unlock()

	cp := ent.ChunkPos()
	dim := ent.EffectiveDimension()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetPassengersCB,
		EncodeSetPassengersPacket(equineEntityID, []int32{riderEntityID}))
	return true
}

// DismountEquine dismounts any current rider from equineEntityID and broadcasts
// an empty ClientboundSetPassengersPacket.
func (s *Server) DismountEquine(equineEntityID int32) bool {
	if s == nil || s.entities == nil || equineEntityID <= 0 {
		return false
	}
	mgr := s.equineManager()
	mgr.mu.Lock()
	est := mgr.equines[equineEntityID]
	if est == nil || est.RiderEntityID == 0 {
		mgr.mu.Unlock()
		return false
	}
	est.RiderEntityID = 0
	mgr.mu.Unlock()

	if ent := s.entities.Get(equineEntityID); ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, PlaySetPassengersCB,
			EncodeSetPassengersPacket(equineEntityID, nil))
	}
	return true
}

// EquineRider returns the entity ID currently riding equineEntityID, or 0 if unridden.
func (s *Server) EquineRider(equineEntityID int32) int32 {
	st, ok := s.GetEquineState(equineEntityID)
	if !ok {
		return 0
	}
	return st.RiderEntityID
}

// --- Llama Caravans & Llama Spit ---

// UnlinkLlamaCaravan detaches followerLlamaID from any caravan head and also
// detaches any follower directly behind followerLlamaID if breakBehind is true.
func (s *Server) UnlinkLlamaCaravan(followerLlamaID int32) bool {
	if s == nil || followerLlamaID <= 0 {
		return false
	}
	mgr := s.tamedManager()
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	st := mgr.mobs[followerLlamaID]
	if st == nil || st.CaravanHeadID <= 0 {
		return false
	}
	st.CaravanHeadID = 0
	return true
}

// LlamaCaravanChain returns the ordered list of llama entity IDs in the caravan
// starting from the frontmost llama down to the tail (up to MaxLlamaCaravanLength = 10).
func (s *Server) LlamaCaravanChain(headLlamaID int32) []int32 {
	if s == nil || headLlamaID <= 0 {
		return nil
	}
	mgr := s.tamedManager()
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()

	// Walk forward to the true caravan head first.
	head := headLlamaID
	visited := make(map[int32]bool, MaxLlamaCaravanLength)
	for !visited[head] {
		visited[head] = true
		st := mgr.mobs[head]
		if st == nil || st.CaravanHeadID <= 0 {
			break
		}
		head = st.CaravanHeadID
	}

	chain := []int32{head}
	seen := map[int32]bool{head: true}
	for len(chain) < MaxLlamaCaravanLength {
		last := chain[len(chain)-1]
		var nextID int32
		for id, st := range mgr.mobs {
			if st != nil && st.CaravanHeadID == last && !seen[id] {
				if nextID == 0 || id < nextID {
					nextID = id
				}
			}
		}
		if nextID == 0 {
			break
		}
		seen[nextID] = true
		chain = append(chain, nextID)
	}
	return chain
}

// AutoFormLlamaCaravan links nearby unlinked llamas/trader_llamas within 6.5
// blocks behind leadLlamaID (up to MaxLlamaCaravanLength = 10), returning the
// number of llamas newly linked into the caravan.
func (s *Server) AutoFormLlamaCaravan(leadLlamaID int32) int {
	if s == nil || s.entities == nil || leadLlamaID <= 0 {
		return 0
	}
	leadEnt := s.entities.Get(leadLlamaID)
	if leadEnt == nil {
		return 0
	}
	leadType := canonicalResourceName(leadEnt.TypeName)
	if leadType != MobTypeLlama && leadType != MobTypeTraderLlama {
		return 0
	}
	dim := leadEnt.EffectiveDimension()
	chain := s.LlamaCaravanChain(leadLlamaID)
	if len(chain) >= MaxLlamaCaravanLength {
		return 0
	}
	tailID := chain[len(chain)-1]
	tailEnt := s.entities.Get(tailID)
	if tailEnt == nil {
		tailEnt = leadEnt
	}

	allEnts := s.entities.AppendAll(nil)
	linked := 0
	for i := range allEnts {
		if len(chain)+linked >= MaxLlamaCaravanLength {
			break
		}
		cand := &allEnts[i]
		if cand.ID == leadLlamaID || cand.ID == tailID || cand.EffectiveDimension() != dim {
			continue
		}
		cType := canonicalResourceName(cand.TypeName)
		if cType != MobTypeLlama && cType != MobTypeTraderLlama {
			continue
		}
		if st, ok := s.GetTamedMobState(cand.ID); ok && st.CaravanHeadID > 0 {
			continue
		}
		dx := cand.X - tailEnt.X
		dy := cand.Y - tailEnt.Y
		dz := cand.Z - tailEnt.Z
		if dx*dx+dy*dy+dz*dz <= 6.5*6.5 {
			if s.LinkLlamaCaravan(cand.ID, tailID) {
				tailID = cand.ID
				tailEnt = cand
				linked++
			}
		}
	}
	return linked
}

// SpawnLlamaSpit spawns a `minecraft:llama_spit` projectile (ID 79) from
// shooterID at (x, y, z) in dimension moving with velocity (vx, vy, vz), and
// broadcasts `entity.llama.spit` and `PlayAddEntity`.
func (s *Server) SpawnLlamaSpit(dimension string, shooterID int32, x, y, z, vx, vy, vz float64) (int32, error) {
	if s == nil || s.entities == nil {
		return 0, errors.New("server: nil entity manager")
	}
	normDim := world.NormalizeDimension(dimension)
	yaw := float32(math.Atan2(-vx, vz) * (180.0 / math.Pi))
	pitch := float32(math.Atan2(-vy, math.Hypot(vx, vz)) * (180.0 / math.Pi))

	ent := &world.Entity{
		TypeID:    ResolveEntityTypeID(EntityTypeLlamaSpit),
		TypeName:  EntityTypeLlamaSpit,
		Dimension: normDim,
		X:         x,
		Y:         y,
		Z:         z,
		Yaw:       yaw,
		Pitch:     pitch,
		HeadYaw:   yaw,
		VelocityX: clampVelocityShort(vx),
		VelocityY: clampVelocityShort(vy),
		VelocityZ: clampVelocityShort(vz),
		OnGround:  false,
	}
	id := s.entities.Add(ent)
	mgr := s.equineManager()
	mgr.mu.Lock()
	mgr.spits[id] = &LlamaSpitState{
		EntityID:        id,
		ShooterEntityID: shooterID,
	}
	mgr.mu.Unlock()

	if saved := s.entities.Get(id); saved != nil {
		cp := saved.ChunkPos()
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, saved))
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, PlaySoundPacketID,
			EncodeSoundPacket(SoundLlamaSpit, SoundSourceNeutral, x, y, z, 1.0, 1.0, 0))
	}
	return id, nil
}

// LlamaSpitAtTarget fires a `minecraft:llama_spit` projectile from llamaID
// toward target coordinates (targetX, targetY, targetZ).
func (s *Server) LlamaSpitAtTarget(llamaID int32, targetX, targetY, targetZ float64) (int32, bool) {
	if s == nil || s.entities == nil || llamaID <= 0 {
		return 0, false
	}
	ent := s.entities.Get(llamaID)
	if ent == nil {
		return 0, false
	}
	norm := canonicalResourceName(ent.TypeName)
	if norm != MobTypeLlama && norm != MobTypeTraderLlama {
		return 0, false
	}
	startX := ent.X
	startY := ent.Y + 1.2
	startZ := ent.Z
	dx := targetX - startX
	dy := (targetY + 0.8) - startY
	dz := targetZ - startZ
	horiz := math.Hypot(dx, dz)
	dy += horiz * 0.2
	dist := math.Sqrt(dx*dx + dy*dy + dz*dz)
	if dist < 1e-4 {
		dist = 1.0
		dz = 1.0
	}
	vx := (dx / dist) * LlamaSpitSpeed
	vy := (dy / dist) * LlamaSpitSpeed
	vz := (dz / dist) * LlamaSpitSpeed
	spitID, err := s.SpawnLlamaSpit(ent.EffectiveDimension(), llamaID, startX, startY, startZ, vx, vy, vz)
	return spitID, err == nil && spitID > 0
}

// OnLlamaDamaged triggers a defensive `minecraft:llama_spit` retaliation when a
// llama or trader_llama is attacked by a player or mob.
func (s *Server) OnLlamaDamaged(llamaID int32, attacker *PlayerSession, attackerMobID int32) int32 {
	if s == nil || s.entities == nil || llamaID <= 0 {
		return 0
	}
	ent := s.entities.Get(llamaID)
	if ent == nil {
		return 0
	}
	norm := canonicalResourceName(ent.TypeName)
	if norm != MobTypeLlama && norm != MobTypeTraderLlama {
		return 0
	}
	if attacker != nil && !attacker.IsDead() {
		ps := attacker.Snapshot()
		if spitID, ok := s.LlamaSpitAtTarget(llamaID, ps.X, ps.Y, ps.Z); ok {
			return spitID
		}
	}
	if attackerMobID > 0 {
		if mEnt := s.entities.Get(attackerMobID); mEnt != nil {
			if spitID, ok := s.LlamaSpitAtTarget(llamaID, mEnt.X, mEnt.Y, mEnt.Z); ok {
				return spitID
			}
		}
	}
	return 0
}

// TickLlamaSpits advances all active `minecraft:llama_spit` projectiles by one
// tick, applying 1.0 HP projectile damage on hit (or shattering decorated pots).
func (s *Server) TickLlamaSpits() {
	if s == nil || s.entities == nil {
		return
	}
	mgr := s.equineManager()
	mgr.mu.RLock()
	if len(mgr.spits) == 0 {
		mgr.mu.RUnlock()
		return
	}
	ids := make([]int32, 0, len(mgr.spits))
	for id := range mgr.spits {
		ids = append(ids, id)
	}
	mgr.mu.RUnlock()

	for _, id := range ids {
		s.tickSingleLlamaSpit(id)
	}
}

func (s *Server) tickSingleLlamaSpit(spitID int32) {
	ent := s.entities.Get(spitID)
	mgr := s.equineManager()
	mgr.mu.Lock()
	st, ok := mgr.spits[spitID]
	if !ok || st == nil || ent == nil {
		delete(mgr.spits, spitID)
		mgr.mu.Unlock()
		return
	}
	st.AgeTicks++
	age := st.AgeTicks
	shooterID := st.ShooterEntityID
	mgr.mu.Unlock()

	if age > LlamaSpitMaxAgeTicks {
		s.removeLlamaSpit(spitID, ent)
		return
	}

	dim := ent.EffectiveDimension()
	cache := s.DimensionChunks(dim)
	vx := float64(ent.VelocityX) / 8000.0
	vy := float64(ent.VelocityY) / 8000.0
	vz := float64(ent.VelocityZ) / 8000.0

	nextX := ent.X + vx
	nextY := ent.Y + vy
	nextZ := ent.Z + vz

	// Check player hit.
	for _, p := range s.onlinePlayersSnapshot() {
		if p == nil || p.IsDead() {
			continue
		}
		ps := p.Snapshot()
		if world.NormalizeDimension(ps.Dimension) != dim || ps.GameMode == GameModeSpectator || ps.GameMode == GameModeCreative {
			continue
		}
		dx := ps.X - nextX
		dy := (ps.Y + 0.9) - nextY
		dz := ps.Z - nextZ
		if dx*dx+dy*dy+dz*dz <= LlamaSpitHitboxRadius*LlamaSpitHitboxRadius*2.25 {
			s.DamagePlayerWithArmor(p, LlamaSpitDamage, "spit")
			s.removeLlamaSpit(spitID, ent)
			return
		}
	}

	// Check mob hit.
	allEnts := s.entities.AppendAll(nil)
	for i := range allEnts {
		other := &allEnts[i]
		if other.ID == spitID || other.ID == shooterID || other.IsItem() || other.IsExperienceOrb() ||
			other.TypeName == EntityTypeLlamaSpit || other.EffectiveDimension() != dim || IsDecorationEntity(other.TypeName) {
			continue
		}
		dx := other.X - nextX
		dy := (other.Y + 0.7) - nextY
		dz := other.Z - nextZ
		if dx*dx+dy*dy+dz*dz <= LlamaSpitHitboxRadius*LlamaSpitHitboxRadius*2.25 {
			s.applyDamageToMob(other, LlamaSpitDamage, nil, 0.25, false, false)
			s.removeLlamaSpit(spitID, ent)
			return
		}
	}

	// Check solid block collision (decorated pot shatter, target block hit, or solid wall).
	if cache != nil {
		bx, by, bz := int(math.Floor(nextX)), int(math.Floor(nextY)), int(math.Floor(nextZ))
		if by >= world.MinY && by < world.MinY+world.WorldHeight {
			bState := cache.GetBlock(bx, by, bz)
			if IsDecoratedPotBlock(bState) || world.BlockName(bState) == "minecraft:target" {
				_, _ = s.OnProjectileBlockImpact(dim, bx, by, bz, ent.X, ent.Y, ent.Z, nextX, nextY, nextZ, spitID, EntityTypeLlamaSpit)
				s.removeLlamaSpit(spitID, ent)
				return
			}
			if !isPassableBlock(bState) && bState != world.StateWater {
				s.removeLlamaSpit(spitID, ent)
				return
			}
		}
	}

	vy -= LlamaSpitGravity
	vx *= LlamaSpitDrag
	vy *= LlamaSpitDrag
	vz *= LlamaSpitDrag
	s.entities.Update(spitID, func(e *world.Entity) {
		e.X = nextX
		e.Y = nextY
		e.Z = nextZ
		e.VelocityX = clampVelocityShort(vx)
		e.VelocityY = clampVelocityShort(vy)
		e.VelocityZ = clampVelocityShort(vz)
	})
}

func (s *Server) removeLlamaSpit(spitID int32, ent *world.Entity) {
	mgr := s.equineManager()
	mgr.mu.Lock()
	delete(mgr.spits, spitID)
	mgr.mu.Unlock()
	if s.entities != nil {
		s.entities.Remove(spitID)
	}
	if ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, protocol.PlayRemoveEntities,
			encodeRemoveEntitiesPacket(nil, []int32{spitID}))
	}
}

// --- Wandering Trader & Trader Llamas ---

var wanderingTraderOrdinaryTrades = [][3]any{
	{"minecraft:emerald", int32(1), "minecraft:fern"},
	{"minecraft:emerald", int32(1), "minecraft:sugar_cane"},
	{"minecraft:emerald", int32(1), "minecraft:pumpkin"},
	{"minecraft:emerald", int32(1), "minecraft:dandelion"},
	{"minecraft:emerald", int32(1), "minecraft:poppy"},
	{"minecraft:emerald", int32(1), "minecraft:wheat_seeds"},
	{"minecraft:emerald", int32(1), "minecraft:brown_mushroom"},
	{"minecraft:emerald", int32(1), "minecraft:red_mushroom"},
	{"minecraft:emerald", int32(1), "minecraft:vine"},
	{"minecraft:emerald", int32(2), "minecraft:glowstone"},
	{"minecraft:emerald", int32(3), "minecraft:kelp"},
	{"minecraft:emerald", int32(3), "minecraft:cactus"},
	{"minecraft:emerald", int32(3), "minecraft:slime_ball"},
	{"minecraft:emerald", int32(5), "minecraft:oak_sapling"},
	{"minecraft:emerald", int32(5), "minecraft:nautilus_shell"},
}

var wanderingTraderSpecialTrades = [][3]any{
	{"minecraft:emerald", int32(1), "minecraft:gunpowder"},
	{"minecraft:emerald", int32(3), "minecraft:packed_ice"},
	{"minecraft:emerald", int32(3), "minecraft:podzol"},
	{"minecraft:emerald", int32(5), "minecraft:tropical_fish_bucket"},
	{"minecraft:emerald", int32(5), "minecraft:pufferfish_bucket"},
	{"minecraft:emerald", int32(6), "minecraft:blue_ice"},
}

// RollWanderingTraderOffers generates 6 trade offers for a `minecraft:wandering_trader`
// (5 ordinary trades + 1 special trade) matching vanilla 26.1.2.
func RollWanderingTraderOffers(rng *rand.Rand) []MerchantOffer {
	indices := make([]int, len(wanderingTraderOrdinaryTrades))
	for i := range indices {
		indices[i] = i
	}
	if rng != nil && len(indices) > 1 {
		rng.Shuffle(len(indices), func(i, j int) {
			indices[i], indices[j] = indices[j], indices[i]
		})
	}
	offers := make([]MerchantOffer, 0, 6)
	for _, idx := range indices {
		if len(offers) >= 5 {
			break
		}
		row := wanderingTraderOrdinaryTrades[idx]
		cost := world.NewNamedItemStack(row[0].(string), row[1].(int32))
		res := world.NewNamedItemStack(row[2].(string), 1)
		if !cost.IsEmpty() && !res.IsEmpty() {
			offers = append(offers, MerchantOffer{
				BaseCostA:       cost,
				Result:          res,
				MaxUses:         12,
				XP:              0,
				PriceMultiplier: 0.05,
			})
		}
	}
	specIdx := 0
	if rng != nil && len(wanderingTraderSpecialTrades) > 0 {
		specIdx = rng.Intn(len(wanderingTraderSpecialTrades))
	}
	sRow := wanderingTraderSpecialTrades[specIdx]
	cost := world.NewNamedItemStack(sRow[0].(string), sRow[1].(int32))
	res := world.NewNamedItemStack(sRow[2].(string), 1)
	if !cost.IsEmpty() && !res.IsEmpty() {
		offers = append(offers, MerchantOffer{
			BaseCostA:       cost,
			Result:          res,
			MaxUses:         6,
			XP:              0,
			PriceMultiplier: 0.05,
		})
	}
	return offers
}

// SpawnWanderingTrader spawns a `minecraft:wandering_trader` (ID 141) at
// (x, y, z) in dimension with a 48000-tick (40-minute) despawn timer, 6 trade
// offers, and (when withLlamas is true) 2 bodyguard `minecraft:trader_llama`
// entities linked to the trader.
func (s *Server) SpawnWanderingTrader(dimension string, x, y, z float64, withLlamas bool, rng *rand.Rand) (traderID int32, llamaIDs []int32, err error) {
	if s == nil || s.entities == nil {
		return 0, nil, errors.New("server: nil entity manager")
	}
	normDim := world.NormalizeDimension(dimension)
	typeID := ResolveEntityTypeID(MobTypeWanderingTrader)
	traderID = s.entities.Add(&world.Entity{
		TypeID:    typeID,
		TypeName:  MobTypeWanderingTrader,
		Dimension: normDim,
		X:         x,
		Y:         y,
		Z:         z,
		OnGround:  true,
		Health:    WanderingTraderMaxHP,
		MaxHealth: WanderingTraderMaxHP,
	})
	s.SetMobHealth(traderID, WanderingTraderMaxHP)

	offers := RollWanderingTraderOffers(rng)
	vtr := s.villagerTracker()
	vtr.mu.Lock()
	vtr.villagers[traderID] = &VillagerState{
		EntityID:       traderID,
		VillagerType:   VillagerTypePlains,
		Profession:     MobTypeWanderingTrader,
		Level:          1,
		XP:             0,
		Offers:         offers,
		LastRestockDay: -1,
	}
	vtr.mu.Unlock()

	if ent := s.entities.Get(traderID); ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, ent))
	}

	if withLlamas {
		offsets := [2][2]float64{{-1.5, 0}, {1.5, 0}}
		for _, off := range offsets {
			lid, lErr := s.SpawnMobInDimension(normDim, x+off[0], y, z+off[1], MobTypeTraderLlama)
			if lErr != nil {
				continue
			}
			est := s.ensureEquineState(lid, MobTypeTraderLlama, rng)
			mgr := s.equineManager()
			mgr.mu.Lock()
			est.TraderOwnerID = traderID
			mgr.mu.Unlock()
			llamaIDs = append(llamaIDs, lid)
		}
	}

	mgr := s.equineManager()
	mgr.mu.Lock()
	mgr.traders[traderID] = &WanderingTraderState{
		EntityID:     traderID,
		DespawnDelay: WanderingTraderDefaultDespawnTicks,
		LlamaIDs:     llamaIDs,
	}
	mgr.mu.Unlock()
	return traderID, llamaIDs, nil
}

// GetWanderingTraderState returns a snapshot of the WanderingTraderState for entityID.
func (s *Server) GetWanderingTraderState(entityID int32) (WanderingTraderState, bool) {
	if s == nil || entityID <= 0 {
		return WanderingTraderState{}, false
	}
	mgr := s.equineManager()
	mgr.mu.RLock()
	st, ok := mgr.traders[entityID]
	var out WanderingTraderState
	if ok && st != nil {
		out = *st
		if len(st.LlamaIDs) > 0 {
			out.LlamaIDs = make([]int32, len(st.LlamaIDs))
			copy(out.LlamaIDs, st.LlamaIDs)
		}
	}
	mgr.mu.RUnlock()
	return out, ok && st != nil
}

// InteractWithWanderingTrader opens the merchant trading screen for a
// `minecraft:wandering_trader` when right-clicked by session.
func (s *Server) InteractWithWanderingTrader(session *PlayerSession, entityID int32) (opened bool, containerID int32, err error) {
	if s == nil || s.entities == nil || session == nil || session.IsDead() {
		return false, 0, nil
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator || snap.Health <= 0 {
		return false, 0, nil
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeWanderingTrader {
		return false, 0, nil
	}
	dx := ent.X - snap.X
	dy := ent.Y - snap.Y
	dz := ent.Z - snap.Z
	if snap.GameMode != GameModeCreative && (dx*dx+dy*dy+dz*dz) > MaxMeleeAttackReach*MaxMeleeAttackReach {
		return false, 0, nil
	}
	if s.IsMobBaby(entityID) {
		return false, 0, nil
	}
	cid, err := s.OpenMerchant(session, entityID)
	if err != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, PlaySoundPacketID,
			EncodeSoundPacket(SoundWanderingTraderNo, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
		return false, 0, err
	}
	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundWanderingTraderTrade, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
	return true, cid, nil
}

// OnWanderingTraderDamaged alerts any bodyguard `minecraft:trader_llama` owned
// by traderID to immediately spit at the attacker (player or mob).
func (s *Server) OnWanderingTraderDamaged(traderID int32, attacker *PlayerSession, attackerMobID int32) int {
	if s == nil || s.entities == nil || traderID <= 0 {
		return 0
	}
	wt, ok := s.GetWanderingTraderState(traderID)
	if !ok {
		return 0
	}
	spitCount := 0
	for _, lid := range wt.LlamaIDs {
		if s.OnLlamaDamaged(lid, attacker, attackerMobID) > 0 {
			spitCount++
		}
	}
	return spitCount
}

// --- Zombie Horse, Skeleton Horse & Skeleton Trap ---

// SpawnSkeletonHorse spawns a `minecraft:skeleton_horse` (ID 116) at (x, y, z)
// in dimension with 15.0 HP and speed 0.2.
func (s *Server) SpawnSkeletonHorse(dimension string, x, y, z float64, tamed bool) (int32, error) {
	id, err := s.SpawnMobInDimension(dimension, x, y, z, MobTypeSkeletonHorse)
	if err != nil {
		return 0, err
	}
	if tamed {
		st := s.ensureTamedMobState(id, MobTypeSkeletonHorse)
		mgr := s.tamedManager()
		mgr.mu.Lock()
		st.Tamed = true
		st.Saddled = true
		snap := *st
		mgr.mu.Unlock()
		if ent := s.entities.Get(id); ent != nil {
			s.broadcastTamedMobMetadata(ent, snap)
		}
	}
	return id, nil
}

// SpawnZombieHorse spawns a `minecraft:zombie_horse` (ID 151) at (x, y, z) in
// dimension with 15.0 HP and speed 0.2.
func (s *Server) SpawnZombieHorse(dimension string, x, y, z float64, tamed bool) (int32, error) {
	id, err := s.SpawnMobInDimension(dimension, x, y, z, MobTypeZombieHorse)
	if err != nil {
		return 0, err
	}
	if tamed {
		st := s.ensureTamedMobState(id, MobTypeZombieHorse)
		mgr := s.tamedManager()
		mgr.mu.Lock()
		st.Tamed = true
		st.Saddled = true
		snap := *st
		mgr.mu.Unlock()
		if ent := s.entities.Get(id); ent != nil {
			s.broadcastTamedMobMetadata(ent, snap)
		}
	}
	return id, nil
}

// SpawnSkeletonTrapHorse spawns a `minecraft:skeleton_horse` with
// `SkeletonTrap = true` (as spawned by a lightning strike during a thunderstorm).
func (s *Server) SpawnSkeletonTrapHorse(dimension string, x, y, z float64) (int32, error) {
	id, err := s.SpawnSkeletonHorse(dimension, x, y, z, false)
	if err != nil {
		return 0, err
	}
	est := s.ensureEquineState(id, MobTypeSkeletonHorse, nil)
	mgr := s.equineManager()
	mgr.mu.Lock()
	est.SkeletonTrap = true
	est.SkeletonTrapTime = 0
	mgr.mu.Unlock()
	return id, nil
}

// TrySpawnSkeletonTrapOnLightning rolls for spawning a Skeleton Trap horse when
// lightning strikes at (x, y, z) in dimension during a thunderstorm (matching
// vanilla ServerLevel.tickChunk: 0.75%..6.75% chance on Easy..Hard, or 100% if
// forceSpawn is true). If a `minecraft:lightning_rod` is within 128 blocks,
// the lightning bolt is redirected to the rod (`RedirectLightningToRod`) and no
// natural skeleton trap spawns.
func (s *Server) TrySpawnSkeletonTrapOnLightning(dimension string, x, y, z float64, forceSpawn bool, rng *rand.Rand) (int32, bool) {
	if s == nil || s.Difficulty() == DifficultyPeaceful {
		return 0, false
	}
	bx, by, bz := int(math.Floor(x)), int(math.Floor(y)), int(math.Floor(z))
	if _, _, _, redirected := s.RedirectLightningToCopperRod(dimension, bx, by, bz, 128); redirected && !forceSpawn {
		return 0, false
	}
	if _, _, _, redirected := s.RedirectLightningToRod(dimension, bx, by, bz); redirected && !forceSpawn {
		return 0, false
	}
	if !forceSpawn {
		chance := 0.03
		switch s.Difficulty() {
		case DifficultyEasy:
			chance = 0.015
		case DifficultyHard:
			chance = 0.0675
		}
		if rng != nil && rng.Float64() >= chance {
			return 0, false
		}
	}
	id, err := s.SpawnSkeletonTrapHorse(dimension, x, y, z)
	return id, err == nil && id > 0
}

// SpawnLightningBolt spawns a `minecraft:lightning_bolt` (ID 77) in dimension,
// first checking `RedirectLightningToCopperRod` (and `RedirectLightningToRod`)
// within 128 blocks of (x, y, z), and then invoking `OnLightningStrikeCopper`
// at the final strike block.
func (s *Server) SpawnLightningBolt(dimension string, x, y, z float64, rng *rand.Rand) (entityID int32, strikeX, strikeY, strikeZ float64, redirected bool) {
	if s == nil || s.entities == nil {
		return 0, x, y, z, false
	}
	normDim := world.NormalizeDimension(dimension)
	strikeX, strikeY, strikeZ = x, y, z
	bx, by, bz := int(math.Floor(x)), int(math.Floor(y)), int(math.Floor(z))
	if rx, ry, rz, redir := s.RedirectLightningToCopperRod(normDim, bx, by, bz, 128); redir {
		redirected = true
		strikeX = float64(rx) + 0.5
		strikeY = float64(ry)
		strikeZ = float64(rz) + 0.5
	} else if rx, ry, rz, redir := s.RedirectLightningToRod(normDim, bx, by, bz); redir {
		redirected = true
		strikeX = float64(rx) + 0.5
		strikeY = float64(ry)
		strikeZ = float64(rz) + 0.5
	}
	targetBX := int(math.Floor(strikeX))
	targetBY := int(math.Floor(strikeY))
	targetBZ := int(math.Floor(strikeZ))
	_, _ = s.OnLightningStrikeCopper(normDim, targetBX, targetBY, targetBZ)

	typeID := ResolveEntityTypeID(EntityTypeLightning)
	if typeID < 0 {
		typeID = EntityTypeLightningBoltID
	}
	entityID = s.entities.Add(&world.Entity{
		TypeID:    typeID,
		TypeName:  EntityTypeLightning,
		Dimension: normDim,
		X:         strikeX,
		Y:         strikeY,
		Z:         strikeZ,
		OnGround:  true,
	})
	if ent := s.entities.Get(entityID); ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, ent))
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, PlaySoundPacketID,
			EncodeSoundPacket("minecraft:entity.lightning_bolt.thunder", SoundSourceWeather, strikeX, strikeY, strikeZ, 10000.0, 0.8, 0))
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, PlaySoundPacketID,
			EncodeSoundPacket("minecraft:entity.lightning_bolt.impact", SoundSourceWeather, strikeX, strikeY, strikeZ, 2.0, 0.5, 0))
	}
	if !redirected {
		_, _ = s.TrySpawnSkeletonTrapOnLightning(normDim, strikeX, strikeY, strikeZ, false, rng)
	}
	return entityID, strikeX, strikeY, strikeZ, redirected
}

// TriggerSkeletonTrap activates a skeleton trap horse (`SkeletonTrap == true`),
// striking lightning, taming the trap horse, mounting an armed `minecraft:skeleton`
// rider onto it, and spawning 3 additional tamed `minecraft:skeleton_horse` +
// `minecraft:skeleton` horsemen (4 skeleton horsemen total).
func (s *Server) TriggerSkeletonTrap(trapHorseID int32) (horseIDs, skeletonIDs []int32, ok bool) {
	if s == nil || s.entities == nil || trapHorseID <= 0 {
		return nil, nil, false
	}
	ent := s.entities.Get(trapHorseID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeSkeletonHorse {
		return nil, nil, false
	}
	mgr := s.equineManager()
	mgr.mu.Lock()
	est := mgr.equines[trapHorseID]
	if est == nil || !est.SkeletonTrap {
		mgr.mu.Unlock()
		return nil, nil, false
	}
	est.SkeletonTrap = false
	mgr.mu.Unlock()

	dim := ent.EffectiveDimension()
	cp := ent.ChunkPos()

	// Broadcast thunder sound for the trap lightning strike.
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket("minecraft:entity.lightning_bolt.thunder", SoundSourceWeather, ent.X, ent.Y, ent.Z, 10000.0, 0.8, 0))
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket("minecraft:entity.lightning_bolt.impact", SoundSourceWeather, ent.X, ent.Y, ent.Z, 2.0, 0.5, 0))

	// Tame the original trap horse and mount a skeleton rider.
	tst := s.ensureTamedMobState(trapHorseID, MobTypeSkeletonHorse)
	tmgr := s.tamedManager()
	tmgr.mu.Lock()
	tst.Tamed = true
	tst.Saddled = true
	tSnap := *tst
	tmgr.mu.Unlock()
	s.broadcastTamedMobMetadata(ent, tSnap)

	horseIDs = append(horseIDs, trapHorseID)
	if skelID, err := s.SpawnMobInDimension(dim, ent.X, ent.Y, ent.Z, MobTypeSkeleton); err == nil {
		s.MountEquine(skelID, trapHorseID)
		skeletonIDs = append(skeletonIDs, skelID)
	}

	// Spawn 3 additional skeleton horsemen (4 horsemen total).
	offsets := [3][2]float64{{1.5, 0}, {-1.5, 0}, {0, 1.5}}
	for i := 0; i < SkeletonTrapExtraHorsemen; i++ {
		hx := ent.X + offsets[i][0]
		hz := ent.Z + offsets[i][1]
		extraHorseID, err := s.SpawnSkeletonHorse(dim, hx, ent.Y, hz, true)
		if err != nil {
			continue
		}
		horseIDs = append(horseIDs, extraHorseID)
		if skelID, sErr := s.SpawnMobInDimension(dim, hx, ent.Y, hz, MobTypeSkeleton); sErr == nil {
			s.MountEquine(skelID, extraHorseID)
			skeletonIDs = append(skeletonIDs, skelID)
		}
	}
	return horseIDs, skeletonIDs, true
}

// --- Per-Tick Equine, Llama, Skeleton Trap & Wandering Trader Loop ---

// TickEquinesAndLlamas steps all active horses, donkeys, mules, llamas,
// trader_llamas, skeleton trap horses, llama spit projectiles, and wandering
// traders by one server tick.
func (s *Server) TickEquinesAndLlamas(rng *rand.Rand) {
	if s == nil || s.entities == nil {
		return
	}
	s.TickLlamaSpits()
	s.TickWanderingTraders(rng)

	mgr := s.equineManager()
	mgr.mu.RLock()
	if len(mgr.equines) == 0 {
		mgr.mu.RUnlock()
		return
	}
	ids := make([]int32, 0, len(mgr.equines))
	for id := range mgr.equines {
		ids = append(ids, id)
	}
	mgr.mu.RUnlock()

	for _, id := range ids {
		s.tickSingleEquineOrLlama(id, rng)
	}
}

func (s *Server) tickSingleEquineOrLlama(entityID int32, rng *rand.Rand) {
	ent := s.entities.Get(entityID)
	mgr := s.equineManager()
	mgr.mu.Lock()
	st, ok := mgr.equines[entityID]
	if !ok || st == nil || ent == nil {
		delete(mgr.equines, entityID)
		mgr.mu.Unlock()
		return
	}
	rearingDone := false
	if st.RearingTicks > 0 {
		st.RearingTicks--
		if st.RearingTicks == 0 {
			rearingDone = true
		}
	}
	if st.SpitCooldown > 0 {
		st.SpitCooldown--
	}
	isTrap := st.SkeletonTrap
	if isTrap {
		st.SkeletonTrapTime++
	}
	trapTime := st.SkeletonTrapTime
	riderID := st.RiderEntityID
	traderOwner := st.TraderOwnerID
	mgr.mu.Unlock()

	if rearingDone {
		if tst, ok := s.GetTamedMobState(entityID); ok {
			s.broadcastTamedMobMetadata(ent, tst)
		}
	}

	// Keep mounted rider entity synced with its mount.
	if riderID > 0 {
		if rEnt := s.entities.Get(riderID); rEnt != nil {
			s.entities.Update(riderID, func(e *world.Entity) {
				e.X = ent.X
				e.Y = ent.Y + 0.85
				e.Z = ent.Z
			})
		} else if sess := s.playerByEntityID(riderID); sess == nil {
			s.DismountEquine(entityID)
		}
	}

	// Trader Llama follows its Wandering Trader if within 32 blocks.
	if traderOwner > 0 && st.TypeName == MobTypeTraderLlama {
		if tEnt := s.entities.Get(traderOwner); tEnt != nil && tEnt.EffectiveDimension() == ent.EffectiveDimension() {
			dx := tEnt.X - ent.X
			dz := tEnt.Z - ent.Z
			if math.Hypot(dx, dz) > 2.5 {
				cache := s.DimensionChunks(ent.EffectiveDimension())
				feetY := int(math.Floor(ent.Y + 0.01))
				s.stepEntityTowardPoint(ent, cache, feetY, tEnt.X, tEnt.Y, tEnt.Z, LlamaMovementSpeed, 2.0)
			}
		}
	}

	// Skeleton Trap check: despawn at 18000 ticks (15m) or trigger when a player is within 10 blocks.
	if isTrap && canonicalResourceName(ent.TypeName) == MobTypeSkeletonHorse {
		if trapTime >= SkeletonTrapMaxAgeTicks {
			mgr.mu.Lock()
			delete(mgr.equines, entityID)
			mgr.mu.Unlock()
			cp := ent.ChunkPos()
			s.entities.Remove(entityID)
			s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, protocol.PlayRemoveEntities,
				encodeRemoveEntitiesPacket(nil, []int32{entityID}))
			return
		}
		dim := ent.EffectiveDimension()
		for _, p := range s.onlinePlayersSnapshot() {
			if p == nil || p.IsDead() {
				continue
			}
			ps := p.Snapshot()
			if world.NormalizeDimension(ps.Dimension) != dim || ps.GameMode == GameModeSpectator {
				continue
			}
			dx := ps.X - ent.X
			dy := ps.Y - ent.Y
			dz := ps.Z - ent.Z
			if dx*dx+dy*dy+dz*dz <= SkeletonTrapTriggerDistance*SkeletonTrapTriggerDistance {
				s.TriggerSkeletonTrap(entityID)
				return
			}
		}
	}
}

// TickWanderingTraders steps all active `minecraft:wandering_trader` entities:
//   - Decrements DespawnDelay (despawning at 0 along with its Trader Llamas)
//   - Drinks an Invisibility potion at dusk/night (`12500..23500` time of day)
//     and milk at dawn (`0..12500` time of day) to clear invisibility.
func (s *Server) TickWanderingTraders(rng *rand.Rand) {
	if s == nil || s.entities == nil {
		return
	}
	mgr := s.equineManager()
	mgr.mu.RLock()
	if len(mgr.traders) == 0 {
		mgr.mu.RUnlock()
		return
	}
	ids := make([]int32, 0, len(mgr.traders))
	for id := range mgr.traders {
		ids = append(ids, id)
	}
	mgr.mu.RUnlock()

	tod := s.TimeOfDay()
	isNight := tod >= 12500 && tod <= 23500

	for _, id := range ids {
		ent := s.entities.Get(id)
		mgr.mu.Lock()
		wt, ok := mgr.traders[id]
		if !ok || wt == nil || ent == nil {
			delete(mgr.traders, id)
			mgr.mu.Unlock()
			continue
		}
		if wt.DespawnDelay > 0 {
			wt.DespawnDelay--
			if wt.DespawnDelay == 0 {
				llamas := make([]int32, len(wt.LlamaIDs))
				copy(llamas, wt.LlamaIDs)
				delete(mgr.traders, id)
				mgr.mu.Unlock()
				s.despawnWanderingTraderAndLlamas(id, ent, llamas)
				continue
			}
		}
		wasInvis := wt.InvisibleNight
		if isNight != wasInvis {
			wt.InvisibleNight = isNight
		}
		mgr.mu.Unlock()

		if isNight != wasInvis {
			dim := ent.EffectiveDimension()
			cp := ent.ChunkPos()
			if isNight {
				s.ApplyEntityEffectScaled(id, NewStatusEffectInstance(EffectInvisibility, 12000, 0), 1.0)
				s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
					EncodeSoundPacket(SoundWanderingTraderDrinkPotion, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
			} else {
				s.ClearEntityEffects(id)
				s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
					EncodeSoundPacket(SoundWanderingTraderDrinkMilk, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
			}
		}
	}
}

func (s *Server) despawnWanderingTraderAndLlamas(traderID int32, ent *world.Entity, llamaIDs []int32) {
	dim := ent.EffectiveDimension()
	cp := ent.ChunkPos()
	s.closeMerchantScreensForVillager(traderID)
	s.entities.Remove(traderID)
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayRemoveEntities,
		encodeRemoveEntitiesPacket(nil, []int32{traderID}))
	for _, lid := range llamaIDs {
		if st, ok := s.GetTamedMobState(lid); ok && st.Tamed {
			continue
		}
		if lEnt := s.entities.Get(lid); lEnt != nil {
			lcp := lEnt.ChunkPos()
			s.entities.Remove(lid)
			s.BroadcastDimensionChunk(lEnt.EffectiveDimension(), lcp.X, lcp.Z, protocol.PlayRemoveEntities,
				encodeRemoveEntitiesPacket(nil, []int32{lid}))
		}
	}
}

// OnEquineOrLlamaKilled returns all extra equipment and chest inventory items
// (saddle, horse armor, llama carpet, attached chest, and stored chest items)
// that should drop when an equine or llama dies, and cleans up its state.
func (s *Server) OnEquineOrLlamaKilled(entityID int32) []world.ItemStack {
	if s == nil || entityID <= 0 {
		return nil
	}
	var drops []world.ItemStack
	if tst, ok := s.GetTamedMobState(entityID); ok && tst.Saddled {
		if saddle := world.NewNamedItemStack("minecraft:saddle", 1); !saddle.IsEmpty() {
			drops = append(drops, saddle)
		}
	}
	mgr := s.equineManager()
	mgr.mu.Lock()
	est := mgr.equines[entityID]
	delete(mgr.equines, entityID)
	delete(mgr.traders, entityID)
	mgr.mu.Unlock()
	if est == nil {
		return drops
	}
	if !est.ArmorItem.IsEmpty() {
		drops = append(drops, est.ArmorItem)
	}
	if est.CarpetColor >= 0 {
		if carpetName := CarpetItemNameByColor(est.CarpetColor); carpetName != "" {
			if st := world.NewNamedItemStack(carpetName, 1); !st.IsEmpty() {
				drops = append(drops, st)
			}
		}
	}
	if est.HasChest {
		if chest := world.NewNamedItemStack("minecraft:chest", 1); !chest.IsEmpty() {
			drops = append(drops, chest)
		}
		for i := range est.ChestItems {
			if !est.ChestItems[i].IsEmpty() && est.ChestItems[i].Count > 0 {
				drops = append(drops, est.ChestItems[i])
			}
		}
	}
	return drops
}

// --- Persistence (SaveEquinesAndLlamasFile / LoadEquinesAndLlamasFile) ---

type persistedEquinesFile struct {
	Equines []EquineAndLlamaState  `json:"equines"`
	Traders []WanderingTraderState `json:"traders"`
}

// SaveEquinesAndLlamasFile persists all active equine/llama states and wandering
// traders to `<WorldDir>/equines_and_llamas.json`.
func (s *Server) SaveEquinesAndLlamasFile() error {
	if s == nil || s.cfg.WorldDir == "" {
		return nil
	}
	mgr := s.equineManager()
	mgr.mu.RLock()
	out := persistedEquinesFile{
		Equines: make([]EquineAndLlamaState, 0, len(mgr.equines)),
		Traders: make([]WanderingTraderState, 0, len(mgr.traders)),
	}
	for _, st := range mgr.equines {
		if st != nil {
			out.Equines = append(out.Equines, *st)
		}
	}
	for _, wt := range mgr.traders {
		if wt != nil {
			out.Traders = append(out.Traders, *wt)
		}
	}
	mgr.mu.RUnlock()

	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.cfg.WorldDir, "equines_and_llamas.json"), data, 0o644)
}

// LoadEquinesAndLlamasFile loads persisted equine/llama and wandering trader
// states from `<WorldDir>/equines_and_llamas.json`.
func (s *Server) LoadEquinesAndLlamasFile() error {
	if s == nil || s.cfg.WorldDir == "" {
		return nil
	}
	raw, err := os.ReadFile(filepath.Join(s.cfg.WorldDir, "equines_and_llamas.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var parsed persistedEquinesFile
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return err
	}
	mgr := s.equineManager()
	mgr.mu.Lock()
	for i := range parsed.Equines {
		cp := parsed.Equines[i]
		mgr.equines[cp.EntityID] = &cp
	}
	for i := range parsed.Traders {
		cp := parsed.Traders[i]
		mgr.traders[cp.EntityID] = &cp
	}
	mgr.mu.Unlock()
	return nil
}

// --- Additional Helper Accessors & Compatibility Wrappers ---

const (
	DefaultSkeletonHorseMaxHealth   float32 = SkeletonHorseMaxHealth
	DefaultZombieHorseMaxHealth     float32 = ZombieHorseMaxHealth
	DefaultWanderingTraderMaxHealth float32 = WanderingTraderMaxHP
	DonkeyDefaultSpeed              float64 = DonkeyMovementSpeed
	EquineMinMaxHealth              float32 = MinEquineMaxHealth
	EquineMaxMaxHealth              float32 = MaxEquineMaxHealth
	HorseMinSpeed                   float64 = MinHorseMovementSpeed
	HorseMaxSpeed                   float64 = MaxHorseMovementSpeed
	HorseMinJumpStrength            float64 = MinHorseJumpStrength
	HorseMaxJumpStrength            float64 = MaxHorseJumpStrength
	DonkeyMuleChestSlots            int     = EquineChestSlotCount
	DiamondHorseArmorItemID                 = "minecraft:diamond_horse_armor"
	HorseMarkingsWhiteField         int32   = HorseMarkingWhiteField
	HorseMarkingsWhiteDots          int32   = HorseMarkingWhiteDots
	WanderingTraderOrdinaryOfferCount       = 5
	WanderingTraderSpecialOfferCount        = 1
	SkeletonTrapHorsemanCount               = 4
)

// ClearEntityEffects removes all active status effects on entityID.
func (s *Server) ClearEntityEffects(entityID int32) {
	if s == nil || entityID <= 0 {
		return
	}
	es := s.entityEffectState()
	es.mu.Lock()
	delete(es.effects, entityID)
	es.mu.Unlock()
}

func (s *Server) initEquineOrLlamaOnSpawn(entityID int32, typeName string, rng *rand.Rand) {
	if s == nil || entityID <= 0 {
		return
	}
	_ = s.ensureEquineState(entityID, typeName, rng)
}

// EquineMaxHealth returns the randomized or inherited MaxHealth of entityID,
// falling back to ResolveMobMaxHealth if not yet initialized.
func (s *Server) EquineMaxHealth(entityID int32) float32 {
	if st, ok := s.GetEquineState(entityID); ok && st.MaxHealth > 0 {
		return st.MaxHealth
	}
	if s != nil && s.entities != nil {
		if ent := s.entities.Get(entityID); ent != nil {
			return ResolveMobMaxHealth(ent.TypeName)
		}
	}
	return DefaultHorseMaxHealth
}

// EquineOrLlamaEntityDataPacket returns the full SynchedEntityData packet (0x63)
// for a horse, donkey, mule, skeleton_horse, zombie_horse, llama, or trader_llama.
func (s *Server) EquineOrLlamaEntityDataPacket(entityID int32, typeName string) []byte {
	if s == nil || entityID <= 0 {
		return nil
	}
	tst, ok := s.GetTamedMobState(entityID)
	norm := canonicalResourceName(typeName)
	est := s.ensureEquineState(entityID, norm, nil)
	mgr := s.equineManager()
	mgr.mu.RLock()
	eSnap := *est
	mgr.mu.RUnlock()

	standing := eSnap.RearingTicks > 0
	tamed := ok && tst.Tamed
	saddled := ok && tst.Saddled
	switch norm {
	case MobTypeHorse:
		return EncodeHorseVariantDataPacket(entityID, tamed, saddled, standing, eSnap.HorseVariant, eSnap.HorseMarkings)
	case MobTypeDonkey, MobTypeMule:
		return EncodeChestedHorseDataPacket(entityID, tamed, saddled, standing, eSnap.HasChest)
	case MobTypeLlama, MobTypeTraderLlama:
		return EncodeLlamaDataPacket(entityID, tamed, eSnap.HasChest, eSnap.LlamaStrength, eSnap.LlamaVariant)
	case MobTypeSkeletonHorse, MobTypeZombieHorse:
		return EncodeAbstractHorseDataPacket(entityID, tamed, saddled, standing)
	default:
		return nil
	}
}

// EquipEquineSaddle sets the saddled state on a tamed horse/donkey/mule/skeleton_horse/zombie_horse,
// plays entity.horse.saddle, and broadcasts updated metadata.
func (s *Server) EquipEquineSaddle(entityID int32, saddled bool) bool {
	if s == nil || s.entities == nil || entityID <= 0 || s.IsMobBaby(entityID) {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil {
		return false
	}
	norm := canonicalResourceName(ent.TypeName)
	switch norm {
	case MobTypeHorse, MobTypeDonkey, MobTypeMule, MobTypeSkeletonHorse, MobTypeZombieHorse:
	default:
		return false
	}
	st := s.ensureTamedMobState(entityID, norm)
	mgr := s.tamedManager()
	mgr.mu.Lock()
	st.Saddled = saddled
	snap := *st
	mgr.mu.Unlock()
	if saddled {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, PlaySoundPacketID,
			EncodeSoundPacket(SoundHorseSaddle, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 0.5, 1.0, 0))
	}
	s.broadcastTamedMobMetadata(ent, snap)
	return true
}

// IsEquineSaddled reports whether entityID currently has a saddle equipped.
func (s *Server) IsEquineSaddled(entityID int32) bool {
	st, ok := s.GetTamedMobState(entityID)
	return ok && st.Saddled
}

// HorseArmorItem returns the canonical item ID of any worn horse armor on entityID.
func (s *Server) HorseArmorItem(entityID int32) string {
	st, ok := s.GetEquineState(entityID)
	if !ok || st.ArmorItem.IsEmpty() {
		return ""
	}
	return canonicalResourceName(st.ArmorItem.ResolvedID())
}

// LlamaCarpetColor returns the DyeColor index (0..15) of any equipped carpet on
// entityID, or -1 if none.
func (s *Server) LlamaCarpetColor(entityID int32) int32 {
	st, ok := s.GetEquineState(entityID)
	if !ok {
		return -1
	}
	return st.CarpetColor
}

// LlamaCarpetItem returns the canonical carpet item ID equipped on entityID, or "".
func (s *Server) LlamaCarpetItem(entityID int32) string {
	color := s.LlamaCarpetColor(entityID)
	if color < 0 {
		return ""
	}
	return CarpetItemNameByColor(color)
}

// HasEquineChest reports whether entityID has an attached chest.
func (s *Server) HasEquineChest(entityID int32) bool {
	st, ok := s.GetEquineState(entityID)
	return ok && st.HasChest
}

// ShearEquineOrLlamaEquipment removes worn horse armor, llama carpet, or saddle
// when a player uses shears on an unmounted tamed equine/llama.
func (s *Server) ShearEquineOrLlamaEquipment(session *PlayerSession, entityID int32) (world.ItemStack, bool) {
	if s == nil || s.entities == nil || entityID <= 0 || s.EquineRider(entityID) > 0 {
		return world.ItemStack{}, false
	}
	ent := s.entities.Get(entityID)
	if ent == nil {
		return world.ItemStack{}, false
	}
	dim := ent.EffectiveDimension()
	norm := canonicalResourceName(ent.TypeName)
	if norm == MobTypeLlama || norm == MobTypeTraderLlama {
		return s.ShearLlamaCarpet(entityID)
	}
	if norm == MobTypeHorse {
		if dropped, ok := s.RemoveHorseArmor(entityID); ok {
			if !dropped.IsEmpty() {
				s.SpawnItemEntityInDimension(dim, ent.X, ent.Y+0.5, ent.Z, dropped)
			}
			return dropped, true
		}
	}
	if s.IsEquineSaddled(entityID) {
		s.EquipEquineSaddle(entityID, false)
		saddle := world.NewNamedItemStack("minecraft:saddle", 1)
		if !saddle.IsEmpty() {
			s.SpawnItemEntityInDimension(dim, ent.X, ent.Y+0.5, ent.Z, saddle)
		}
		return saddle, true
	}
	return world.ItemStack{}, false
}

func (s *Server) inheritEquineOrLlamaTraits(parentA, parentB, offspringID int32, offspringType string, rng *rand.Rand) {
	if s == nil || offspringID <= 0 {
		return
	}
	norm := canonicalResourceName(offspringType)
	stA, _ := s.GetEquineState(parentA)
	stB, _ := s.GetEquineState(parentB)
	if stA.MaxHealth <= 0 {
		stA.MaxHealth = DefaultHorseMaxHealth
		stA.MovementSpeed = 0.225
		stA.JumpStrength = 0.7
		stA.LlamaStrength = 3
	}
	if stB.MaxHealth <= 0 {
		stB.MaxHealth = DefaultHorseMaxHealth
		stB.MovementSpeed = 0.225
		stB.JumpStrength = 0.7
		stB.LlamaStrength = 3
	}
	child := s.ensureEquineState(offspringID, norm, rng)
	cHP, cSpd, cJmp := InheritEquineAttributes(
		stA.MaxHealth, stB.MaxHealth,
		stA.MovementSpeed, stB.MovementSpeed,
		stA.JumpStrength, stB.JumpStrength,
		rng,
	)
	mgr := s.equineManager()
	mgr.mu.Lock()
	child.MaxHealth = cHP
	child.MovementSpeed = cSpd
	child.JumpStrength = cJmp
	if norm == MobTypeHorse {
		child.HorseVariant, child.HorseMarkings = InheritHorseCoatAndMarkings(
			stA.HorseVariant, stA.HorseMarkings,
			stB.HorseVariant, stB.HorseMarkings,
			rng,
		)
	} else if norm == MobTypeLlama || norm == MobTypeTraderLlama {
		child.LlamaStrength = InheritLlamaStrength(stA.LlamaStrength, stB.LlamaStrength, rng)
		if rng != nil && rng.Intn(2) == 1 {
			child.LlamaVariant = stB.LlamaVariant
		} else {
			child.LlamaVariant = stA.LlamaVariant
		}
	}
	mgr.mu.Unlock()
	s.SetMobHealth(offspringID, cHP)
}

// IsSkeletonTrapHorse reports whether entityID is currently an untriggered skeleton trap horse.
func (s *Server) IsSkeletonTrapHorse(entityID int32) bool {
	st, ok := s.GetEquineState(entityID)
	return ok && st.SkeletonTrap
}
