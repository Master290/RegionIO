package server

import (
	"errors"
	"math"
	"math/rand"
	"sync"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

// ============================================================================
// UNDEAD MOBS (ZOMBIE, HUSK, DROWNED, STRAY, WITHER SKELETON) & SILVERFISH
// Vanilla Minecraft 26.1.2 (Protocol 775)
// ============================================================================

const (
	MobTypeHusk       = "minecraft:husk"
	MobTypeDrowned    = "minecraft:drowned"
	MobTypeStray      = "minecraft:stray"
	MobTypeSilverfish = "minecraft:silverfish"

	// Vanilla 26.1.2 BuiltInRegistries.ENTITY_TYPE numeric network IDs (protocol 775).
	EntityTypeDrownedID    int = 38
	EntityTypeHuskID       int = 67
	EntityTypeSilverfishID int = 114
	EntityTypeStrayID      int = 128

	// Default Max Health values (vanilla 26.1.2).
	DefaultHuskMaxHealth       float32 = 20.0
	DefaultDrownedMaxHealth    float32 = 20.0
	DefaultStrayMaxHealth      float32 = 20.0
	DefaultSilverfishMaxHealth float32 = 8.0

	// Water drowning conversion timers for Husk -> Zombie -> Drowned (vanilla 26.1.2).
	ZombieInWaterBeforeConversionTicks = 600 // 30s submerged in water before conversion starts
	ZombieDrownedConversionTicks       = 300 // 15s shaking conversion into Zombie / Drowned

	// Powder snow freezing conversion timers for Skeleton -> Stray (vanilla 26.1.2).
	SkeletonPowderSnowFreezeTicks = 140 // 7s in powder_snow before conversion starts
	SkeletonStrayConversionTicks  = 300 // 15s shaking conversion into Stray

	// Combat, movement, and status effect parameters.
	HuskHungerBaseDurationTicks    int32   = 140 // 7s base Hunger duration per difficulty tier
	DrownedSwimSpeed               float64 = 0.18
	DrownedLandSpeed               float64 = 0.13
	DrownedTridentRange            float64 = 20.0
	DrownedTridentMinRange         float64 = 2.5
	DrownedTridentCooldownTicks    int     = 40 // 2.0s between trident throws
	DrownedTridentBaseDamage       float32 = 8.0
	StraySlownessDurationTicks     int32   = 600 // 30s Slowness I from Stray arrows
	WitherSkeletonSwordDamage      float32 = 8.0 // 4.0 base + stone_sword
	WitherSkeletonMeleeWitherTicks int32   = 200 // 10s Wither I on melee hit
	SilverfishChaseSpeed           float64 = 0.16
	SilverfishWanderSpeed          float64 = 0.09
	SilverfishAttackDamage         float32 = 1.0
	SilverfishAttackReach          float64 = 1.4
	SilverfishCallForHelpTicks     int     = 20 // 1.0s delay before waking nearby infested blocks
	SilverfishWakeHorizontalRadius int     = 10 // 21x11x21 search box around hurt silverfish
	SilverfishWakeVerticalRadius   int     = 5

	// Zombie reinforcement parameters (vanilla 26.1.2).
	DefaultZombieReinforcementChance float32 = 0.08
	ZombieReinforcementPenalty       float32 = 0.05
	ZombieReinforcementMinDist       int     = 7
	ZombieReinforcementMaxDist       int     = 40

	// SynchedEntityData indices for Zombie and AbstractSkeleton in 26.1.2 (protocol 775).
	EntityDataZombieBabyID              byte = 16 // Zombie.DATA_BABY_ID (BOOLEAN serializer = 8)
	EntityDataZombieDrownedConvertingID byte = 18 // Zombie.DATA_DROWNED_CONVERSION_ID (BOOLEAN serializer = 8)
	EntityDataSkeletonFreezeConvertingID byte = 16 // Skeleton.DATA_STRAY_CONVERSION_ID (BOOLEAN serializer = 8)

	// Equipment slot indices for ClientboundSetEquipmentPacket (0x66).
	UndeadEquipSlotMainHand byte = 0
	UndeadEquipSlotOffHand  byte = 1
	UndeadEquipSlotFeet     byte = 2
	UndeadEquipSlotLegs     byte = 3
	UndeadEquipSlotChest    byte = 4
	UndeadEquipSlotHead     byte = 5

	// Canonical item IDs used by Undead mobs.
	ItemTrident             = "minecraft:trident"
	ItemNautilusShell       = "minecraft:nautilus_shell"
	ItemCopperIngot         = "minecraft:copper_ingot"
	ItemStoneSword          = "minecraft:stone_sword"
	ItemIronSword           = "minecraft:iron_sword"
	ItemIronShovel          = "minecraft:iron_shovel"
	ItemTippedArrow         = "minecraft:tipped_arrow"
	ItemCoal                = "minecraft:coal"
	ItemWitherSkeletonSkull = "minecraft:wither_skeleton_skull"
	ItemWitherRose          = "minecraft:wither_rose"

	// Sound events (vanilla 26.1.2).
	SoundHuskAmbient            = "minecraft:entity.husk.ambient"
	SoundHuskHurt               = "minecraft:entity.husk.hurt"
	SoundHuskDeath              = "minecraft:entity.husk.death"
	SoundHuskConvertToZombie    = "minecraft:entity.husk.convert_to_zombie"
	SoundZombieConvertToDrowned = "minecraft:entity.zombie.converted_to_drowned"
	SoundDrownedAmbient         = "minecraft:entity.drowned.ambient"
	SoundDrownedHurt            = "minecraft:entity.drowned.hurt"
	SoundDrownedDeath           = "minecraft:entity.drowned.death"
	SoundDrownedShoot           = "minecraft:entity.drowned.shoot"
	SoundDrownedSwim            = "minecraft:entity.drowned.swim"
	SoundStrayAmbient           = "minecraft:entity.stray.ambient"
	SoundStrayHurt              = "minecraft:entity.stray.hurt"
	SoundStrayDeath             = "minecraft:entity.stray.death"
	SoundSkeletonConvertToStray = "minecraft:entity.skeleton.converted_to_stray"
	SoundWitherSkeletonHurt     = "minecraft:entity.wither_skeleton.hurt"
	SoundWitherSkeletonDeath    = "minecraft:entity.wither_skeleton.death"
	SoundWitherSkeletonStep     = "minecraft:entity.wither_skeleton.step"
	SoundSilverfishAmbient      = "minecraft:entity.silverfish.ambient"
	SoundSilverfishHurt         = "minecraft:entity.silverfish.hurt"
	SoundSilverfishDeath        = "minecraft:entity.silverfish.death"
	SoundSilverfishStep         = "minecraft:entity.silverfish.step"
)

// UndeadGearState tracks worn armor, held weapons, conversion timers, and
// reinforcement chance for a Zombie, Husk, Drowned, Zombie Villager, Skeleton,
// Stray, or Wither Skeleton.
type UndeadGearState struct {
	EntityID            int32
	TypeName            string
	MainHand            world.ItemStack
	OffHand             world.ItemStack
	Head                world.ItemStack
	Chest               world.ItemStack
	Legs                world.ItemStack
	Feet                world.ItemStack
	HelmetDamage        int
	HelmetMaxDamage     int
	InWaterTicks        int
	InPowderSnowTicks   int
	Converting          bool
	ConversionTicks     int
	ReinforcementChance float32
	CanBreakDoors       bool
	AttackCooldown      int
	SunBurnTickCounter  int
}

// SilverfishState tracks the AI and infested-block wake-up timer for a
// `minecraft:silverfish`.
type SilverfishState struct {
	EntityID         int32
	Dimension        string
	CallForHelpTicks int
	AttackCooldown   int
	MergeCooldown    int
}

type serverUndeadManager struct {
	mu         sync.RWMutex
	undead     map[int32]*UndeadGearState
	silverfish map[int32]*SilverfishState
}

var undeadManagers sync.Map // map[*Server]*serverUndeadManager

func (s *Server) undeadManager() *serverUndeadManager {
	if s == nil {
		return &serverUndeadManager{
			undead:     make(map[int32]*UndeadGearState),
			silverfish: make(map[int32]*SilverfishState),
		}
	}
	if v, ok := undeadManagers.Load(s); ok {
		return v.(*serverUndeadManager)
	}
	mgr := &serverUndeadManager{
		undead:     make(map[int32]*UndeadGearState),
		silverfish: make(map[int32]*SilverfishState),
	}
	actual, _ := undeadManagers.LoadOrStore(s, mgr)
	return actual.(*serverUndeadManager)
}

// IsZombieFamilyMob reports whether typeName is a Zombie-family mob (`zombie`,
// `zombie_villager`, `husk`, `drowned`, or `zombified_piglin`).
func IsZombieFamilyMob(typeName string) bool {
	switch canonicalResourceName(typeName) {
	case MobTypeZombie, MobTypeZombieVillager, MobTypeHusk, MobTypeDrowned, MobTypeZombifiedPiglin:
		return true
	default:
		return false
	}
}

// IsSkeletonFamilyMob reports whether typeName is a Skeleton-family mob
// (`skeleton`, `stray`, `bogged`, or `wither_skeleton`).
func IsSkeletonFamilyMob(typeName string) bool {
	switch canonicalResourceName(typeName) {
	case MobTypeSkeleton, MobTypeStray, MobTypeBogged, MobTypeWitherSkeleton:
		return true
	default:
		return false
	}
}

// IsUndeadMobType reports whether typeName is an undead mob in vanilla 26.1.2
// (affected by Smite, healed by Harming, hurt by Healing, ignored by Wither).
func IsUndeadMobType(typeName string) bool {
	norm := canonicalResourceName(typeName)
	if IsZombieFamilyMob(norm) || IsSkeletonFamilyMob(norm) {
		return true
	}
	switch norm {
	case MobTypePhantom, MobTypeWither, MobTypeZoglin, MobTypeSkeletonHorse, MobTypeZombieHorse:
		return true
	default:
		return false
	}
}

// DoesUndeadBurnInDaylight reports whether a mob of `typeName` naturally burns
// under direct sunlight in vanilla 26.1.2.
// Note: `minecraft:husk`, `minecraft:wither_skeleton`, `minecraft:zombified_piglin`,
// `minecraft:zoglin`, and `minecraft:wither` NEVER burn in sunlight!
func DoesUndeadBurnInDaylight(typeName string) bool {
	switch canonicalResourceName(typeName) {
	case MobTypeZombie, MobTypeZombieVillager, MobTypeDrowned,
		MobTypeSkeleton, MobTypeStray, MobTypeBogged, MobTypePhantom:
		return true
	default:
		return false
	}
}

func (s *Server) ensureUndeadState(entityID int32, typeName string, rng *rand.Rand) *UndeadGearState {
	norm := canonicalResourceName(typeName)
	mgr := s.undeadManager()
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	if st, ok := mgr.undead[entityID]; ok && st != nil {
		st.TypeName = norm
		return st
	}
	st := &UndeadGearState{
		EntityID:            entityID,
		TypeName:            norm,
		ReinforcementChance: DefaultZombieReinforcementChance,
	}
	switch norm {
	case MobTypeSkeleton, MobTypeStray:
		st.MainHand = world.NewNamedItemStack("minecraft:bow", 1)
	case MobTypeWitherSkeleton:
		st.MainHand = world.NewNamedItemStack(ItemStoneSword, 1)
	case MobTypeDrowned:
		if rng != nil {
			if rng.Float32() < 0.15 {
				st.MainHand = world.NewNamedItemStack(ItemTrident, 1)
			}
			if rng.Float32() < 0.03 {
				st.OffHand = world.NewNamedItemStack(ItemNautilusShell, 1)
			}
		}
	}
	mgr.undead[entityID] = st
	return st
}

// GetUndeadState returns a copy of the UndeadGearState for `entityID`.
func (s *Server) GetUndeadState(entityID int32) (UndeadGearState, bool) {
	if s == nil {
		return UndeadGearState{}, false
	}
	mgr := s.undeadManager()
	mgr.mu.RLock()
	st, ok := mgr.undead[entityID]
	var out UndeadGearState
	if ok && st != nil {
		out = *st
	}
	mgr.mu.RUnlock()
	return out, ok && st != nil
}

// ============================================================================
// SYNCHED ENTITY DATA & EQUIPMENT PACKETS FOR UNDEAD MOBS
// ============================================================================

// EncodeZombieConversionDataPacket encodes a ClientboundSetEntityDataPacket (0x63)
// updating `Zombie.DATA_BABY_ID` (index 16, BOOLEAN) and
// `Zombie.DATA_DROWNED_CONVERSION_ID` (index 18, BOOLEAN).
func EncodeZombieConversionDataPacket(entityID int32, baby, drownedConverting bool) []byte {
	w := protocol.NewWriter(16)
	w.VarInt(entityID)
	w.Byte(EntityDataZombieBabyID)
	w.VarInt(EntityDataSerializerBool)
	w.Bool(baby)
	w.Byte(EntityDataZombieDrownedConvertingID)
	w.VarInt(EntityDataSerializerBool)
	w.Bool(drownedConverting)
	w.Byte(0xFF)
	return w.Bytes()
}

// DecodeZombieConversionDataPacket decodes a ClientboundSetEntityDataPacket
// produced by EncodeZombieConversionDataPacket.
func DecodeZombieConversionDataPacket(r *protocol.Reader) (entityID int32, baby, drownedConverting bool, err error) {
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
			return 0, false, false, errors.New("undead: unexpected zombie boolean serializer")
		}
		val, err := r.Bool()
		if err != nil {
			return 0, false, false, err
		}
		switch idx {
		case EntityDataZombieBabyID:
			baby = val
		case EntityDataZombieDrownedConvertingID:
			drownedConverting = val
		}
	}
	return entityID, baby, drownedConverting, nil
}

// EncodeSkeletonConversionDataPacket encodes a ClientboundSetEntityDataPacket (0x63)
// updating `Skeleton.DATA_STRAY_CONVERSION_ID` (index 16, BOOLEAN).
func EncodeSkeletonConversionDataPacket(entityID int32, strayConverting bool) []byte {
	w := protocol.NewWriter(12)
	w.VarInt(entityID)
	w.Byte(EntityDataSkeletonFreezeConvertingID)
	w.VarInt(EntityDataSerializerBool)
	w.Bool(strayConverting)
	w.Byte(0xFF)
	return w.Bytes()
}

// DecodeSkeletonConversionDataPacket decodes a ClientboundSetEntityDataPacket
// produced by EncodeSkeletonConversionDataPacket.
func DecodeSkeletonConversionDataPacket(r *protocol.Reader) (entityID int32, strayConverting bool, err error) {
	entityID, err = r.VarInt()
	if err != nil {
		return 0, false, err
	}
	for {
		idx, err := r.ReadByte()
		if err != nil {
			return 0, false, err
		}
		if idx == 0xFF {
			break
		}
		ser, err := r.VarInt()
		if err != nil {
			return 0, false, err
		}
		if ser != EntityDataSerializerBool {
			return 0, false, errors.New("undead: unexpected skeleton boolean serializer")
		}
		val, err := r.Bool()
		if err != nil {
			return 0, false, err
		}
		if idx == EntityDataSkeletonFreezeConvertingID {
			strayConverting = val
		}
	}
	return entityID, strayConverting, nil
}

// EncodeUndeadEquipmentPacket encodes a ClientboundSetEquipmentPacket (0x66)
// for all 6 equipment slots (MainHand, OffHand, Feet, Legs, Chest, Head) of an
// undead mob.
func EncodeUndeadEquipmentPacket(st UndeadGearState) []byte {
	w := protocol.NewWriter(48)
	w.VarInt(st.EntityID)
	slots := [6]struct {
		slot  byte
		stack world.ItemStack
	}{
		{UndeadEquipSlotMainHand, st.MainHand},
		{UndeadEquipSlotOffHand, st.OffHand},
		{UndeadEquipSlotFeet, st.Feet},
		{UndeadEquipSlotLegs, st.Legs},
		{UndeadEquipSlotChest, st.Chest},
		{UndeadEquipSlotHead, st.Head},
	}
	for i, entry := range slots {
		header := entry.slot
		if i < len(slots)-1 {
			header |= 0x80
		}
		w.Byte(header)
		writeEquipmentSlotStack(w, entry.stack)
	}
	return w.Bytes()
}

// SetUndeadEquipment updates the held weapon and helmet of an undead entity and
// broadcasts `EncodeUndeadEquipmentPacket`.
func (s *Server) SetUndeadEquipment(entityID int32, mainHand, offHand, head world.ItemStack) bool {
	if s == nil || s.entities == nil {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil {
		return false
	}
	s.ensureUndeadState(entityID, ent.TypeName, nil)
	mgr := s.undeadManager()
	mgr.mu.Lock()
	st := mgr.undead[entityID]
	st.MainHand = mainHand
	st.OffHand = offHand
	st.Head = head
	st.HelmetDamage = 0
	st.HelmetMaxDamage = defaultHelmetMaxDurability(head.ResolvedID())
	snap := *st
	mgr.mu.Unlock()

	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, protocol.PlaySetEquipment,
		EncodeUndeadEquipmentPacket(snap))
	return true
}

// SetMobBaby marks `entityID` as a baby (`ageTicks = BabyMobGrowthTicks`) or
// adult (`ageTicks = 0`).
func (s *Server) SetMobBaby(entityID int32, isBaby bool) {
	if isBaby {
		s.SetMobAge(entityID, BabyMobGrowthTicks)
	} else {
		s.SetMobAge(entityID, 0)
	}
}

func defaultHelmetMaxDurability(itemName string) int {
	switch canonicalResourceName(itemName) {
	case "minecraft:leather_helmet":
		return 55
	case "minecraft:golden_helmet":
		return 77
	case "minecraft:chainmail_helmet", "minecraft:iron_helmet":
		return 165
	case "minecraft:turtle_helmet":
		return 275
	case "minecraft:diamond_helmet":
		return 363
	case "minecraft:netherite_helmet":
		return 407
	default:
		return 55
	}
}

// EquipUndeadRandomGear rolls natural spawn armor and hand weapon for an undead
// mob (`zombie`, `husk`, `drowned`, `skeleton`, `stray`, `wither_skeleton`) and
// broadcasts `EncodeUndeadEquipmentPacket`.
func (s *Server) EquipUndeadRandomGear(entityID int32, rng *rand.Rand) (UndeadGearState, bool) {
	if s == nil || s.entities == nil {
		return UndeadGearState{}, false
	}
	ent := s.entities.Get(entityID)
	if ent == nil {
		return UndeadGearState{}, false
	}
	if rng == nil {
		rng = rand.New(rand.NewSource(int64(entityID) + 17))
	}
	norm := canonicalResourceName(ent.TypeName)
	s.ensureUndeadState(entityID, norm, rng)

	armorSets := [][4]string{
		{"minecraft:leather_helmet", "minecraft:leather_chestplate", "minecraft:leather_leggings", "minecraft:leather_boots"},
		{"minecraft:golden_helmet", "minecraft:golden_chestplate", "minecraft:golden_leggings", "minecraft:golden_boots"},
		{"minecraft:chainmail_helmet", "minecraft:chainmail_chestplate", "minecraft:chainmail_leggings", "minecraft:chainmail_boots"},
		{"minecraft:iron_helmet", "minecraft:iron_chestplate", "minecraft:iron_leggings", "minecraft:iron_boots"},
		{"minecraft:diamond_helmet", "minecraft:diamond_chestplate", "minecraft:diamond_leggings", "minecraft:diamond_boots"},
	}
	tier := rng.Intn(len(armorSets))
	chosen := armorSets[tier]

	mgr := s.undeadManager()
	mgr.mu.Lock()
	st := mgr.undead[entityID]
	st.Head = world.NewNamedItemStack(chosen[0], 1)
	st.HelmetDamage = 0
	st.HelmetMaxDamage = defaultHelmetMaxDurability(chosen[0])
	if rng.Float32() < 0.75 {
		st.Chest = world.NewNamedItemStack(chosen[1], 1)
	}
	if rng.Float32() < 0.75 {
		st.Legs = world.NewNamedItemStack(chosen[2], 1)
	}
	if rng.Float32() < 0.75 {
		st.Feet = world.NewNamedItemStack(chosen[3], 1)
	}

	switch norm {
	case MobTypeZombie, MobTypeHusk, MobTypeZombieVillager:
		if rng.Intn(3) == 0 {
			st.MainHand = world.NewNamedItemStack(ItemIronSword, 1)
		} else {
			st.MainHand = world.NewNamedItemStack(ItemIronShovel, 1)
		}
		if s.Difficulty() == DifficultyHard && rng.Float32() < 0.10 {
			st.CanBreakDoors = true
		}
	case MobTypeDrowned:
		st.MainHand = world.NewNamedItemStack(ItemTrident, 1)
	case MobTypeSkeleton, MobTypeStray:
		st.MainHand = world.NewNamedItemStack("minecraft:bow", 1)
	case MobTypeWitherSkeleton:
		st.MainHand = world.NewNamedItemStack(ItemStoneSword, 1)
	}
	snap := *st
	mgr.mu.Unlock()

	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, protocol.PlaySetEquipment,
		EncodeUndeadEquipmentPacket(snap))
	return snap, true
}

// ============================================================================
// 1. ZOMBIE, HUSK & DROWNED SPAWN, CONVERSION & COMBAT
// ============================================================================

// SpawnHusk spawns a `minecraft:husk` (`20.0 HP`, desert zombie variant that
// does not burn in sunlight and inflicts Hunger) at `(x, y, z)` in `dimension`.
func (s *Server) SpawnHusk(dimension string, x, y, z float64, isBaby bool) (int32, error) {
	if s == nil || s.entities == nil {
		return 0, errors.New("server: nil entity manager")
	}
	normDim := world.NormalizeDimension(dimension)
	typeID := ResolveEntityTypeID(MobTypeHusk)
	if typeID < 0 {
		typeID = EntityTypeHuskID
	}
	id := s.entities.Add(&world.Entity{
		TypeID:    typeID,
		TypeName:  MobTypeHusk,
		Dimension: normDim,
		X:         x,
		Y:         y,
		Z:         z,
		OnGround:  true,
	})
	s.SetMobHealth(id, DefaultHuskMaxHealth)
	s.ensureUndeadState(id, MobTypeHusk, nil)
	if isBaby {
		s.SetMobBaby(id, true)
	}
	if ent := s.entities.Get(id); ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, ent))
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, PlaySetEntityDataCB,
			EncodeZombieConversionDataPacket(id, isBaby, false))
	}
	return id, nil
}

// SpawnDrowned spawns a `minecraft:drowned` (`20.0 HP`, aquatic zombie variant)
// at `(x, y, z)` in `dimension`. If `withTrident` is true, equips it with a
// `minecraft:trident` in its main hand.
func (s *Server) SpawnDrowned(dimension string, x, y, z float64, isBaby, withTrident bool) (int32, error) {
	if s == nil || s.entities == nil {
		return 0, errors.New("server: nil entity manager")
	}
	normDim := world.NormalizeDimension(dimension)
	typeID := ResolveEntityTypeID(MobTypeDrowned)
	if typeID < 0 {
		typeID = EntityTypeDrownedID
	}
	id := s.entities.Add(&world.Entity{
		TypeID:    typeID,
		TypeName:  MobTypeDrowned,
		Dimension: normDim,
		X:         x,
		Y:         y,
		Z:         z,
		OnGround:  true,
	})
	s.SetMobHealth(id, DefaultDrownedMaxHealth)
	s.ensureUndeadState(id, MobTypeDrowned, nil)
	if isBaby {
		s.SetMobBaby(id, true)
	}
	if withTrident {
		s.SetUndeadEquipment(id, world.NewNamedItemStack(ItemTrident, 1), world.ItemStack{}, world.ItemStack{})
	}
	if ent := s.entities.Get(id); ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, ent))
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, PlaySetEntityDataCB,
			EncodeZombieConversionDataPacket(id, isBaby, false))
	}
	return id, nil
}

// IsEntitySubmergedInWaterForConversion reports whether `entityID` has its
// body/eye in water (`FluidWater` or `bubble_column`), matching vanilla
// `Zombie.isEyeInFluid(FluidTags.WATER)` for drowning conversion.
func (s *Server) IsEntitySubmergedInWaterForConversion(entityID int32) bool {
	if s == nil || s.entities == nil {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil {
		return false
	}
	dim := ent.EffectiveDimension()
	if _, fh, kind, sub := s.EntitySubmergedFluidDepth(dim, ent.X, ent.Y, ent.Z); sub && fh > 0 && kind == world.FluidWater {
		return true
	}
	cache := s.DimensionChunks(dim)
	if cache == nil {
		return false
	}
	bx := int(math.Floor(ent.X))
	by := int(math.Floor(ent.Y))
	bz := int(math.Floor(ent.Z))
	for _, checkY := range []int{by, int(math.Floor(ent.Y + 1.5))} {
		if checkY < world.MinY || checkY >= world.MinY+world.WorldHeight {
			continue
		}
		blk := cache.GetBlock(bx, checkY, bz)
		if blk == world.StateWater || world.BlockName(blk) == "minecraft:bubble_column" {
			return true
		}
		if kind, _, _, _, _, ok := world.ClassifyFluidState(blk); ok && kind == world.FluidWater {
			return true
		}
	}
	return false
}

// StepZombieWaterConversion advances the water drowning conversion timer for a
// `minecraft:husk` (`Husk -> Zombie`) or `minecraft:zombie` (`Zombie -> Drowned`)
// by `ticks` ticks (`1` per game tick):
//   - After `600` ticks (`30s`) in water, starts `300`-tick (`15s`) shaking conversion.
//   - When conversion completes, replaces the mob with `minecraft:zombie` (from Husk)
//     or `minecraft:drowned` (from Zombie), preserving health ratio, baby state,
//     and worn/held equipment!
func (s *Server) StepZombieWaterConversion(entityID int32, ticks int) (convertedID int32, newType string, converted bool) {
	if s == nil || s.entities == nil || ticks <= 0 {
		return 0, "", false
	}
	ent := s.entities.Get(entityID)
	if ent == nil {
		return 0, "", false
	}
	norm := canonicalResourceName(ent.TypeName)
	if norm != MobTypeHusk && norm != MobTypeZombie {
		return 0, "", false
	}
	s.ensureUndeadState(entityID, norm, nil)
	inWater := s.IsEntitySubmergedInWaterForConversion(entityID)
	isBaby := s.IsMobBaby(entityID)

	mgr := s.undeadManager()
	mgr.mu.Lock()
	st := mgr.undead[entityID]
	if st == nil {
		mgr.mu.Unlock()
		return 0, "", false
	}

	startedConversion := false
	finishConversion := false
	if st.Converting {
		st.ConversionTicks -= ticks
		if st.ConversionTicks <= 0 {
			finishConversion = true
		}
	} else if inWater {
		st.InWaterTicks += ticks
		if st.InWaterTicks >= ZombieInWaterBeforeConversionTicks {
			st.Converting = true
			st.ConversionTicks = ZombieDrownedConversionTicks
			startedConversion = true
		}
	} else if st.InWaterTicks > 0 {
		st.InWaterTicks -= ticks
		if st.InWaterTicks < 0 {
			st.InWaterTicks = 0
		}
	}
	gearCopy := *st
	mgr.mu.Unlock()

	dim := ent.EffectiveDimension()
	cp := ent.ChunkPos()
	if startedConversion {
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
			EncodeZombieConversionDataPacket(entityID, isBaby, true))
	}
	if !finishConversion {
		return 0, "", false
	}

	// Conversion complete: Husk -> Zombie, Zombie -> Drowned.
	targetType := MobTypeDrowned
	convSound := SoundZombieConvertToDrowned
	if norm == MobTypeHusk {
		targetType = MobTypeZombie
		convSound = SoundHuskConvertToZombie
	}
	oldHP := s.MobHealth(entityID)
	oldMax := ResolveMobMaxHealth(norm)
	if oldMax <= 0 {
		oldMax = 20.0
	}
	hpRatio := oldHP / oldMax
	if hpRatio <= 0 || hpRatio > 1.0 {
		hpRatio = 1.0
	}

	x, y, z, yaw, pitch := ent.X, ent.Y, ent.Z, ent.Yaw, ent.Pitch
	mgr.mu.Lock()
	delete(mgr.undead, entityID)
	mgr.mu.Unlock()
	s.entities.Remove(entityID)
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayRemoveEntities,
		encodeRemoveEntitiesPacket(nil, []int32{entityID}))

	var newID int32
	var err error
	if targetType == MobTypeDrowned {
		newID, err = s.SpawnDrowned(dim, x, y, z, isBaby, false)
	} else {
		newID, err = s.SpawnMobInDimension(dim, x, y, z, MobTypeZombie)
		if err == nil && isBaby {
			s.SetMobBaby(newID, true)
		}
	}
	if err != nil || newID <= 0 {
		return 0, "", false
	}
	s.entities.Update(newID, func(e *world.Entity) {
		e.Yaw = yaw
		e.HeadYaw = yaw
		e.Pitch = pitch
	})
	newMax := ResolveMobMaxHealth(targetType)
	s.SetMobHealth(newID, newMax*hpRatio)

	// Preserve worn armor and held items across conversion!
	s.ensureUndeadState(newID, targetType, nil)
	mgr.mu.Lock()
	if nst := mgr.undead[newID]; nst != nil {
		nst.MainHand = gearCopy.MainHand
		nst.OffHand = gearCopy.OffHand
		nst.Head = gearCopy.Head
		nst.Chest = gearCopy.Chest
		nst.Legs = gearCopy.Legs
		nst.Feet = gearCopy.Feet
		nst.HelmetDamage = gearCopy.HelmetDamage
		nst.HelmetMaxDamage = gearCopy.HelmetMaxDamage
		nst.ReinforcementChance = gearCopy.ReinforcementChance
		nst.CanBreakDoors = gearCopy.CanBreakDoors
	}
	mgr.mu.Unlock()

	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(convSound, SoundSourceHostile, x, y, z, 2.0, 1.0, 0))
	return newID, targetType, true
}

// ShouldUndeadBurnInSunlightNow reports whether `entityID` should burn in
// sunlight on the current tick:
//   - `DoesUndeadBurnInDaylight(typeName)` must be true (`Husk` and `WitherSkeleton` return false!)
//   - Daytime in Overworld (`!s.IsNight() && !s.IsRaining()`)
//   - Open sky above the mob's head (`s.HasOpenSkyAbove`)
//   - Not submerged in water or standing in powder snow
//   - Not wearing a helmet (or if wearing a helmet, damages the helmet instead of burning!)
func (s *Server) ShouldUndeadBurnInSunlightNow(entityID int32, damageHelmet bool) bool {
	if s == nil || s.entities == nil {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil {
		return false
	}
	norm := canonicalResourceName(ent.TypeName)
	if !DoesUndeadBurnInDaylight(norm) {
		return false
	}
	dim := ent.EffectiveDimension()
	if dim != world.DimensionOverworld || s.IsNight() || s.IsRaining() {
		return false
	}
	if s.IsEntitySubmergedInWaterForConversion(entityID) || s.IsEntityInPowderSnow(entityID) {
		return false
	}
	cache := s.DimensionChunks(dim)
	bx := int(math.Floor(ent.X))
	by := int(math.Floor(ent.Y))
	bz := int(math.Floor(ent.Z))
	if !s.HasOpenSkyAbove(cache, bx, by+1, bz) {
		return false
	}

	// Check if a worn helmet protects the undead mob from sunlight.
	s.ensureUndeadState(entityID, norm, nil)
	mgr := s.undeadManager()
	mgr.mu.Lock()
	st := mgr.undead[entityID]
	if st != nil && !st.Head.IsEmpty() {
		if damageHelmet {
			st.HelmetDamage++
			if st.HelmetMaxDamage <= 0 {
				st.HelmetMaxDamage = defaultHelmetMaxDurability(st.Head.ResolvedID())
			}
			if st.HelmetDamage >= st.HelmetMaxDamage {
				st.Head = world.ItemStack{}
				st.HelmetDamage = 0
				snap := *st
				mgr.mu.Unlock()
				cp := ent.ChunkPos()
				s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlaySetEquipment,
					EncodeUndeadEquipmentPacket(snap))
				return false
			}
		}
		mgr.mu.Unlock()
		return false
	}
	mgr.mu.Unlock()
	return true
}

// ApplyHuskMeleeHunger applies `EffectHunger` (`140` ticks * difficulty tier:
// `140` Easy, `280` Normal, `420` Hard) when a `minecraft:husk` hits `target`
// in melee.
func (s *Server) ApplyHuskMeleeHunger(huskID int32, target *PlayerSession) bool {
	if s == nil || target == nil || target.IsDead() {
		return false
	}
	diff := int32(s.Difficulty())
	if diff <= 0 {
		return false
	}
	dur := HuskHungerBaseDurationTicks * diff
	s.ApplyPlayerEffect(target, NewStatusEffectInstance(EffectHunger, dur, 0))
	return true
}

// ShootDrownedTridentAtPlayer fires a ranged `minecraft:trident` from Drowned
// `drownedID` at `target`, dealing `DrownedTridentBaseDamage` (`8.0` base,
// scaled by difficulty) and broadcasting `SoundDrownedShoot`.
func (s *Server) ShootDrownedTridentAtPlayer(drownedID int32, target *PlayerSession) (dealt float32, ok bool) {
	if s == nil || s.entities == nil || target == nil || target.IsDead() {
		return 0, false
	}
	ent := s.entities.Get(drownedID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeDrowned {
		return 0, false
	}
	st, hasSt := s.GetUndeadState(drownedID)
	if !hasSt || canonicalResourceName(st.MainHand.ResolvedID()) != ItemTrident {
		return 0, false
	}
	dim := ent.EffectiveDimension()
	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundDrownedShoot, SoundSourceHostile, ent.X, ent.Y+1.5, ent.Z, 1.0, 1.0, 0))
	dealt = s.AttackPlayerFromMob(drownedID, MobTypeDrowned, ent.X, ent.Z, target, DrownedTridentBaseDamage)
	return dealt, true
}

// TrySpawnZombieReinforcement implements vanilla `Zombie.hurtServer`
// reinforcement spawning: when a Zombie-family mob takes non-lethal damage on
// Normal or Hard difficulty, rolls `ReinforcementChance` (or forces spawn when
// `force` is true) to spawn a reinforcement `minecraft:zombie` `7..40` blocks away!
func (s *Server) TrySpawnZombieReinforcement(damagedZombieID int32, attacker *PlayerSession, rng *rand.Rand, force bool) (reinforcementID int32, spawned bool) {
	if s == nil || s.entities == nil {
		return 0, false
	}
	if !force && s.Difficulty() < DifficultyNormal {
		return 0, false
	}
	ent := s.entities.Get(damagedZombieID)
	if ent == nil || !IsZombieFamilyMob(ent.TypeName) {
		return 0, false
	}
	if s.MobHealth(damagedZombieID) <= 0 {
		return 0, false
	}
	s.ensureUndeadState(damagedZombieID, ent.TypeName, rng)
	if rng == nil {
		rng = rand.New(rand.NewSource(int64(damagedZombieID) + 99))
	}

	mgr := s.undeadManager()
	mgr.mu.Lock()
	st := mgr.undead[damagedZombieID]
	chance := st.ReinforcementChance
	if !force && (chance <= 0 || rng.Float32() >= chance) {
		mgr.mu.Unlock()
		return 0, false
	}
	st.ReinforcementChance -= ZombieReinforcementPenalty
	if st.ReinforcementChance < 0 {
		st.ReinforcementChance = 0
	}
	mgr.mu.Unlock()

	dim := ent.EffectiveDimension()
	cache := s.DimensionChunks(dim)
	bx0 := int(math.Floor(ent.X))
	by0 := int(math.Floor(ent.Y))
	bz0 := int(math.Floor(ent.Z))

	for attempt := 0; attempt < 50; attempt++ {
		distX := ZombieReinforcementMinDist + rng.Intn(ZombieReinforcementMaxDist-ZombieReinforcementMinDist+1)
		distZ := ZombieReinforcementMinDist + rng.Intn(ZombieReinforcementMaxDist-ZombieReinforcementMinDist+1)
		if rng.Intn(2) == 0 {
			distX = -distX
		}
		if rng.Intn(2) == 0 {
			distZ = -distZ
		}
		dy := rng.Intn(7) - 3
		cx := bx0 + distX
		cy := by0 + dy
		cz := bz0 + distZ
		if cy <= world.MinY+1 || cy >= world.MinY+world.WorldHeight-2 {
			continue
		}
		if cache != nil {
			if !isSolidGroundBlock(cache.GetBlock(cx, cy-1, cz)) || !isColumnPassableAt(cache, cx, cy, cz) {
				continue
			}
		}
		id, err := s.SpawnMobInDimension(dim, float64(cx)+0.5, float64(cy), float64(cz)+0.5, MobTypeZombie)
		if err == nil && id > 0 {
			return id, true
		}
	}
	// Fallback if chunk is ungenerated in unit test: spawn 8 blocks away on same Y.
	if force {
		id, err := s.SpawnMobInDimension(dim, ent.X+8.0, ent.Y, ent.Z, MobTypeZombie)
		if err == nil && id > 0 {
			return id, true
		}
	}
	return 0, false
}

// ============================================================================
// 2. SKELETON, STRAY & WITHER SKELETON (POWDER SNOW CONVERSION & COMBAT)
// ============================================================================

// SpawnStray spawns a `minecraft:stray` (`20.0 HP`, icy skeleton variant that
// fires Slowness arrows and is immune to powder snow freezing) at `(x, y, z)`.
func (s *Server) SpawnStray(dimension string, x, y, z float64) (int32, error) {
	if s == nil || s.entities == nil {
		return 0, errors.New("server: nil entity manager")
	}
	normDim := world.NormalizeDimension(dimension)
	typeID := ResolveEntityTypeID(MobTypeStray)
	if typeID < 0 {
		typeID = EntityTypeStrayID
	}
	id := s.entities.Add(&world.Entity{
		TypeID:    typeID,
		TypeName:  MobTypeStray,
		Dimension: normDim,
		X:         x,
		Y:         y,
		Z:         z,
		OnGround:  true,
	})
	s.SetMobHealth(id, DefaultStrayMaxHealth)
	s.ensureUndeadState(id, MobTypeStray, nil)
	if ent := s.entities.Get(id); ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, ent))
		if gst, ok := s.GetUndeadState(id); ok {
			s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlaySetEquipment,
				EncodeUndeadEquipmentPacket(gst))
		}
	}
	return id, nil
}

// IsEntityInPowderSnow reports whether `entityID` is currently inside a
// `minecraft:powder_snow` block.
func (s *Server) IsEntityInPowderSnow(entityID int32) bool {
	if s == nil || s.entities == nil {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil {
		return false
	}
	cache := s.DimensionChunks(ent.EffectiveDimension())
	if cache == nil {
		return false
	}
	bx := int(math.Floor(ent.X))
	by := int(math.Floor(ent.Y))
	bz := int(math.Floor(ent.Z))
	if by < world.MinY || by >= world.MinY+world.WorldHeight {
		return false
	}
	blk := cache.GetBlock(bx, by, bz)
	return blk == world.StatePowderSnow || world.BlockName(blk) == "minecraft:powder_snow"
}

// StepSkeletonPowderSnowConversion advances the powder snow freezing conversion
// timer for a `minecraft:skeleton` (`Skeleton -> Stray`) by `ticks` ticks:
//   - After `140` ticks (`7s`) in `minecraft:powder_snow`, starts `300`-tick (`15s`)
//     conversion and broadcasts `EncodeSkeletonConversionDataPacket(id, true)`.
//   - When conversion finishes, replaces the `Skeleton` with a `minecraft:stray`
//     (`SoundSkeletonConvertToStray`), preserving health ratio and equipment!
func (s *Server) StepSkeletonPowderSnowConversion(entityID int32, ticks int) (strayID int32, converted bool) {
	if s == nil || s.entities == nil || ticks <= 0 {
		return 0, false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeSkeleton {
		return 0, false
	}
	s.ensureUndeadState(entityID, MobTypeSkeleton, nil)
	inPowderSnow := s.IsEntityInPowderSnow(entityID)

	mgr := s.undeadManager()
	mgr.mu.Lock()
	st := mgr.undead[entityID]
	if st == nil {
		mgr.mu.Unlock()
		return 0, false
	}

	startedConversion := false
	finishConversion := false
	if st.Converting {
		st.ConversionTicks -= ticks
		if st.ConversionTicks <= 0 {
			finishConversion = true
		}
	} else if inPowderSnow {
		st.InPowderSnowTicks += ticks
		if st.InPowderSnowTicks >= SkeletonPowderSnowFreezeTicks {
			st.Converting = true
			st.ConversionTicks = SkeletonStrayConversionTicks
			startedConversion = true
		}
	} else if st.InPowderSnowTicks > 0 {
		st.InPowderSnowTicks -= ticks
		if st.InPowderSnowTicks < 0 {
			st.InPowderSnowTicks = 0
		}
	}
	gearCopy := *st
	mgr.mu.Unlock()

	dim := ent.EffectiveDimension()
	cp := ent.ChunkPos()
	if startedConversion {
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
			EncodeSkeletonConversionDataPacket(entityID, true))
	}
	if !finishConversion {
		return 0, false
	}

	oldHP := s.MobHealth(entityID)
	hpRatio := oldHP / DefaultSkeletonMaxHealth
	if hpRatio <= 0 || hpRatio > 1.0 {
		hpRatio = 1.0
	}
	x, y, z, yaw, pitch := ent.X, ent.Y, ent.Z, ent.Yaw, ent.Pitch

	mgr.mu.Lock()
	delete(mgr.undead, entityID)
	mgr.mu.Unlock()
	s.entities.Remove(entityID)
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayRemoveEntities,
		encodeRemoveEntitiesPacket(nil, []int32{entityID}))

	newID, err := s.SpawnStray(dim, x, y, z)
	if err != nil || newID <= 0 {
		return 0, false
	}
	s.entities.Update(newID, func(e *world.Entity) {
		e.Yaw = yaw
		e.HeadYaw = yaw
		e.Pitch = pitch
	})
	s.SetMobHealth(newID, DefaultStrayMaxHealth*hpRatio)

	mgr.mu.Lock()
	if nst := mgr.undead[newID]; nst != nil {
		nst.MainHand = gearCopy.MainHand
		nst.OffHand = gearCopy.OffHand
		nst.Head = gearCopy.Head
		nst.Chest = gearCopy.Chest
		nst.Legs = gearCopy.Legs
		nst.Feet = gearCopy.Feet
		nst.HelmetDamage = gearCopy.HelmetDamage
		nst.HelmetMaxDamage = gearCopy.HelmetMaxDamage
	}
	mgr.mu.Unlock()

	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundSkeletonConvertToStray, SoundSourceHostile, x, y, z, 2.0, 1.0, 0))
	return newID, true
}

// ShootStrayArrowAtPlayer fires a Slowness arrow from Stray `strayID` at
// `target`, dealing ranged arrow damage and applying `EffectSlowness` (`30s` =
// `600` ticks, amplifier `0`).
func (s *Server) ShootStrayArrowAtPlayer(strayID int32, strayX, strayY, strayZ float64, target *PlayerSession) float32 {
	if s == nil || target == nil || target.IsDead() {
		return 0
	}
	dealt := s.ShootSkeletonArrowAtPlayer(strayID, strayX, strayY, strayZ, target)
	if dealt > 0 && !target.IsDead() {
		s.ApplyPlayerEffect(target, NewStatusEffectInstance(EffectSlowness, StraySlownessDurationTicks, 0))
	}
	return dealt
}

// WitherSkeletonMeleeAttackPlayer performs a melee attack from Wither Skeleton
// `witherSkelID` against `target`, dealing `WitherSkeletonSwordDamage` (`8.0`
// base scaled by difficulty) and inflicting `EffectWither` (`200` ticks = `10s`)!
func (s *Server) WitherSkeletonMeleeAttackPlayer(witherSkelID int32, target *PlayerSession) float32 {
	if s == nil || s.entities == nil || target == nil || target.IsDead() {
		return 0
	}
	ent := s.entities.Get(witherSkelID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeWitherSkeleton {
		return 0
	}
	dealt := s.AttackPlayerFromMob(witherSkelID, MobTypeWitherSkeleton, ent.X, ent.Z, target, WitherSkeletonSwordDamage)
	if dealt > 0 && !target.IsDead() {
		s.ApplyPlayerEffect(target, NewStatusEffectInstance(EffectWither, WitherSkeletonMeleeWitherTicks, 0))
	}
	return dealt
}

// TryPlaceOrDropWitherRoseOnMobKill implements vanilla `LivingEntity.createWitherRose`:
// when a mob is killed by a `minecraft:wither` or `minecraft:wither_skull`,
// places `minecraft:wither_rose` at `(bx, by, bz)` if `mobGriefing` is enabled
// and the supporting block is valid (`grass_block`, `dirt`, `netherrack`,
// `soul_sand`, `soul_soil`, `moss_block`, `mud`), or drops `1x minecraft:wither_rose`!
func (s *Server) TryPlaceOrDropWitherRoseOnMobKill(dimension string, x, y, z float64) (placedBlock bool, droppedItemID int32) {
	if s == nil {
		return false, 0
	}
	normDim := world.NormalizeDimension(dimension)
	cache := s.DimensionChunks(normDim)
	bx := int(math.Floor(x))
	by := int(math.Floor(y))
	bz := int(math.Floor(z))

	if cache != nil && s.MobGriefing() && s.CreeperBlockGriefing() && by > world.MinY && by < world.MinY+world.WorldHeight {
		here := cache.GetBlock(bx, by, bz)
		below := cache.GetBlock(bx, by-1, bz)
		if here == world.StateAir && canSupportWitherRose(below) {
			if roseState, ok := world.BlockStateByName(ItemWitherRose, nil); ok && roseState != world.StateAir {
				cache.SetBlock(bx, by, bz, roseState)
				s.broadcastMobBlockUpdate(normDim, bx, by, bz, roseState)
				return true, 0
			}
		}
	}
	roseStack := world.NewNamedItemStack(ItemWitherRose, 1)
	if !roseStack.IsEmpty() && s.entities != nil {
		id, _ := s.SpawnItemEntityInDimension(normDim, x, y+0.25, z, roseStack)
		return false, id
	}
	return false, 0
}

func canSupportWitherRose(belowState uint16) bool {
	if belowState == world.StateAir {
		return false
	}
	switch canonicalResourceName(world.BlockName(belowState)) {
	case "minecraft:grass_block", "minecraft:dirt", "minecraft:coarse_dirt",
		"minecraft:podzol", "minecraft:rooted_dirt", "minecraft:mycelium",
		"minecraft:moss_block", "minecraft:mud", "minecraft:Farmland", "minecraft:farmland",
		"minecraft:netherrack", "minecraft:soul_sand", "minecraft:soul_soil":
		return true
	default:
		return false
	}
}

// ============================================================================
// 3. UNDEAD DEATH LOOT TABLES (DROWNED, STRAY, WITHER SKELETON, HUSK)
// ============================================================================

// RollDrownedDeathLoot rolls the vanilla 26.1.2 death loot for `minecraft:drowned`:
//   - `0..2` `minecraft:rotten_flesh`
//   - `11% + 2% * lootingLevel` chance of `1x minecraft:copper_ingot` when killed by player
//   - Held `minecraft:nautilus_shell` (100% if in offhand) or `minecraft:trident` (8.5% if held)
func RollDrownedDeathLoot(rng *rand.Rand, lootingLevel int, killedByPlayer bool, gear UndeadGearState) []world.ItemStack {
	var out []world.ItemStack
	flesh := int32(1)
	if rng != nil {
		flesh = int32(1 + rng.Intn(2))
	}
	if st := world.NewNamedItemStack("minecraft:rotten_flesh", flesh); !st.IsEmpty() {
		out = append(out, st)
	}
	if killedByPlayer {
		copperChance := float32(0.11) + float32(lootingLevel)*0.02
		if rng == nil || rng.Float32() < copperChance {
			if st := world.NewNamedItemStack(ItemCopperIngot, 1); !st.IsEmpty() {
				out = append(out, st)
			}
		}
	}
	if canonicalResourceName(gear.OffHand.ResolvedID()) == ItemNautilusShell {
		if st := world.NewNamedItemStack(ItemNautilusShell, 1); !st.IsEmpty() {
			out = append(out, st)
		}
	}
	if canonicalResourceName(gear.MainHand.ResolvedID()) == ItemTrident {
		tridentChance := float32(0.085) + float32(lootingLevel)*0.01
		if rng != nil && rng.Float32() < tridentChance {
			if st := world.NewNamedItemStack(ItemTrident, 1); !st.IsEmpty() {
				out = append(out, st)
			}
		}
	}
	return out
}

// RollStrayDeathLoot rolls the vanilla 26.1.2 death loot for `minecraft:stray`:
//   - `1..2` `minecraft:bone`
//   - `1..2` `minecraft:arrow`
//   - `1x minecraft:tipped_arrow` (Arrow of Slowness, 50% base chance when killed by player)
func RollStrayDeathLoot(rng *rand.Rand, killedByPlayer bool) []world.ItemStack {
	var out []world.ItemStack
	bones := int32(1)
	arrows := int32(1)
	if rng != nil {
		bones = int32(1 + rng.Intn(2))
		arrows = int32(1 + rng.Intn(2))
	}
	if s1 := world.NewNamedItemStack("minecraft:bone", bones); !s1.IsEmpty() {
		out = append(out, s1)
	}
	if s2 := world.NewNamedItemStack("minecraft:arrow", arrows); !s2.IsEmpty() {
		out = append(out, s2)
	}
	if killedByPlayer && (rng == nil || rng.Float32() < 0.50) {
		if s3 := world.NewNamedItemStack(ItemTippedArrow, 1); !s3.IsEmpty() {
			out = append(out, s3)
		}
	}
	return out
}

// RollWitherSkeletonDeathLoot rolls the vanilla 26.1.2 death loot for
// `minecraft:wither_skeleton`:
//   - `1..2` `minecraft:bone`
//   - `0..1` `minecraft:coal`
//   - `1x minecraft:wither_skeleton_skull` (`2.5% + 1% * lootingLevel`, or `100%` if `killedByChargedCreeper`)
func RollWitherSkeletonDeathLoot(rng *rand.Rand, lootingLevel int, killedByChargedCreeper bool) []world.ItemStack {
	var out []world.ItemStack
	bones := int32(1)
	coal := int32(1)
	if rng != nil {
		bones = int32(1 + rng.Intn(2))
		coal = int32(rng.Intn(2))
	}
	if s1 := world.NewNamedItemStack("minecraft:bone", bones); !s1.IsEmpty() {
		out = append(out, s1)
	}
	if coal > 0 {
		if s2 := world.NewNamedItemStack(ItemCoal, coal); !s2.IsEmpty() {
			out = append(out, s2)
		}
	}
	skullDrop := killedByChargedCreeper
	if !skullDrop {
		chance := float32(0.025) + float32(lootingLevel)*0.01
		if rng != nil && rng.Float32() < chance {
			skullDrop = true
		}
	}
	if skullDrop {
		if s3 := world.NewNamedItemStack(ItemWitherSkeletonSkull, 1); !s3.IsEmpty() {
			out = append(out, s3)
		}
	}
	return out
}

// ============================================================================
// 4. SILVERFISH & INFESTED BLOCKS (minecraft:silverfish, ID 114, 8.0 HP)
// ============================================================================

func (s *Server) ensureSilverfishState(entityID int32, dim string) *SilverfishState {
	normDim := world.NormalizeDimension(dim)
	mgr := s.undeadManager()
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	if st, ok := mgr.silverfish[entityID]; ok && st != nil {
		return st
	}
	st := &SilverfishState{
		EntityID:  entityID,
		Dimension: normDim,
	}
	mgr.silverfish[entityID] = st
	return st
}

// GetSilverfishState returns a copy of the SilverfishState for `entityID`.
func (s *Server) GetSilverfishState(entityID int32) (SilverfishState, bool) {
	if s == nil {
		return SilverfishState{}, false
	}
	mgr := s.undeadManager()
	mgr.mu.RLock()
	st, ok := mgr.silverfish[entityID]
	var out SilverfishState
	if ok && st != nil {
		out = *st
	}
	mgr.mu.RUnlock()
	return out, ok && st != nil
}

// SpawnSilverfish spawns a `minecraft:silverfish` (`8.0 HP`) at `(x, y, z)` in `dimension`.
func (s *Server) SpawnSilverfish(dimension string, x, y, z float64) (int32, error) {
	if s == nil || s.entities == nil {
		return 0, errors.New("server: nil entity manager")
	}
	normDim := world.NormalizeDimension(dimension)
	typeID := ResolveEntityTypeID(MobTypeSilverfish)
	if typeID < 0 {
		typeID = EntityTypeSilverfishID
	}
	id := s.entities.Add(&world.Entity{
		TypeID:    typeID,
		TypeName:  MobTypeSilverfish,
		Dimension: normDim,
		X:         x,
		Y:         y,
		Z:         z,
		OnGround:  true,
	})
	s.SetMobHealth(id, DefaultSilverfishMaxHealth)
	s.ensureSilverfishState(id, normDim)
	if ent := s.entities.Get(id); ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, ent))
	}
	return id, nil
}

// SpawnSilverfishFromInfestedBlock spawns a `minecraft:silverfish` at `(x, y, z)`
// when an `infested_*` block is broken without Silk Touch (compatible with
// `SetInfestedSilverfishSpawnHook`).
func (s *Server) SpawnSilverfishFromInfestedBlock(dim string, x, y, z float64, blockState uint16) int32 {
	if s == nil || !world.IsInfestedBlock(blockState) {
		return 0
	}
	id, err := s.SpawnSilverfish(dim, x, y, z)
	if err != nil {
		return 0
	}
	return id
}

// OnInfestedBlockBroken checks `world.ShouldSpawnSilverfishOnBreak(blockState, silkTouch, s.DoTileDrops())`
// and spawns a `minecraft:silverfish` at `(x+0.5, y, z+0.5)` when an infested
// block (`infested_stone`, `infested_cobblestone`, `infested_stone_bricks`,
// `infested_mossy_stone_bricks`, `infested_cracked_stone_bricks`,
// `infested_chiseled_stone_bricks`, `infested_deepslate`) is broken without Silk Touch!
func (s *Server) OnInfestedBlockBroken(dimension string, x, y, z int, blockState uint16, silkTouch bool) (int32, bool) {
	if s == nil || !world.ShouldSpawnSilverfishOnBreak(blockState, silkTouch, s.DoTileDrops()) {
		return 0, false
	}
	id, err := s.SpawnSilverfish(dimension, float64(x)+0.5, float64(y), float64(z)+0.5)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// OnSilverfishDamaged arms `SilverfishWakeUpFriendsGoal` (`CallForHelpTicks = 20`)
// when a Silverfish takes non-lethal damage from a player or magic/poison.
func (s *Server) OnSilverfishDamaged(entityID int32) bool {
	if s == nil || s.entities == nil {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeSilverfish {
		return false
	}
	if s.MobHealth(entityID) <= 0 {
		return false
	}
	s.ensureSilverfishState(entityID, ent.EffectiveDimension())
	mgr := s.undeadManager()
	mgr.mu.Lock()
	if st := mgr.silverfish[entityID]; st != nil && st.CallForHelpTicks <= 0 {
		st.CallForHelpTicks = SilverfishCallForHelpTicks
	}
	mgr.mu.Unlock()
	return true
}

// WakeNearbyInfestedBlocks implements vanilla `SilverfishWakeUpFriendsGoal`:
// searches up to `21x11x21` (`dx, dz in -10..10`, `dy in -5..5`) around `(cx, cy, cz)`
// in `dimension` for `world.IsInfestedBlock`, breaks them to `StateAir`, and
// spawns reinforcement `minecraft:silverfish` at each woken infested block!
func (s *Server) WakeNearbyInfestedBlocks(dimension string, cx, cy, cz, maxToWake int) (spawnedIDs []int32) {
	if s == nil || !s.CreeperBlockGriefing() {
		return nil
	}
	normDim := world.NormalizeDimension(dimension)
	cache := s.DimensionChunks(normDim)
	if cache == nil {
		return nil
	}
	if maxToWake <= 0 {
		maxToWake = 8
	}
	for dy := -SilverfishWakeVerticalRadius; dy <= SilverfishWakeVerticalRadius; dy++ {
		by := cy + dy
		if by < world.MinY || by >= world.MinY+world.WorldHeight {
			continue
		}
		for dx := -SilverfishWakeHorizontalRadius; dx <= SilverfishWakeHorizontalRadius; dx++ {
			for dz := -SilverfishWakeHorizontalRadius; dz <= SilverfishWakeHorizontalRadius; dz++ {
				bx, bz := cx+dx, cz+dz
				blk := cache.GetBlock(bx, by, bz)
				if !world.IsInfestedBlock(blk) {
					continue
				}
				cache.SetBlock(bx, by, bz, world.StateAir)
				s.broadcastMobBlockUpdate(normDim, bx, by, bz, world.StateAir)
				if id, err := s.SpawnSilverfish(normDim, float64(bx)+0.5, float64(by), float64(bz)+0.5); err == nil && id > 0 {
					spawnedIDs = append(spawnedIDs, id)
					if len(spawnedIDs) >= maxToWake {
						return spawnedIDs
					}
				}
			}
		}
	}
	return spawnedIDs
}

// TrySilverfishInfestBlock implements vanilla `SilverfishMergeWithStoneGoal`:
// when an idle `minecraft:silverfish` merges into a host stone/brick/deepslate
// block at `(bx, by, bz)` (`world.InfestedBlockForHost`) and `mobGriefing` is
// enabled, replaces the host block with its `minecraft:infested_*` state and
// despawns the silverfish!
func (s *Server) TrySilverfishInfestBlock(silverfishID int32, bx, by, bz int) (infestedState uint16, merged bool) {
	if s == nil || s.entities == nil || !s.CreeperBlockGriefing() {
		return world.StateAir, false
	}
	ent := s.entities.Get(silverfishID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeSilverfish {
		return world.StateAir, false
	}
	dim := ent.EffectiveDimension()
	cache := s.DimensionChunks(dim)
	if cache == nil || by < world.MinY || by >= world.MinY+world.WorldHeight {
		return world.StateAir, false
	}
	hostState := cache.GetBlock(bx, by, bz)
	infState, ok := world.InfestedBlockForHost(hostState)
	if !ok || infState == world.StateAir {
		return world.StateAir, false
	}
	cache.SetBlock(bx, by, bz, infState)
	s.broadcastMobBlockUpdate(dim, bx, by, bz, infState)

	mgr := s.undeadManager()
	mgr.mu.Lock()
	delete(mgr.silverfish, silverfishID)
	mgr.mu.Unlock()
	cp := ent.ChunkPos()
	s.entities.Remove(silverfishID)
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayRemoveEntities,
		encodeRemoveEntitiesPacket(nil, []int32{silverfishID}))
	return infState, true
}

// ============================================================================
// 5. MAIN TICK LOOP FOR UNDEAD CONVERSIONS, SUNBURN & SILVERFISH AI
// ============================================================================

func init() {
	SetInfestedSilverfishSpawnHook((*Server).SpawnSilverfishFromInfestedBlock)
}

// TickUndeadAndSilverfish steps all active Husk/Zombie/Drowned/Skeleton/Stray/
// WitherSkeleton and Silverfish entities by 1 game tick (20 TPS).
func (s *Server) TickUndeadAndSilverfish(rng *rand.Rand) {
	if s == nil || s.entities == nil {
		return
	}
	if rng == nil {
		rng = rand.New(rand.NewSource(int64(s.entityTracker().aiTickID) + 409))
	}

	allEnts := s.entities.AppendAll(nil)
	for i := range allEnts {
		ent := &allEnts[i]
		norm := canonicalResourceName(ent.TypeName)
		switch norm {
		case MobTypeHusk, MobTypeZombie:
			if _, _, conv := s.StepZombieWaterConversion(ent.ID, 1); conv {
				continue
			}
			if norm == MobTypeZombie {
				s.tickUndeadSunburn(ent, rng)
			}
			if norm == MobTypeHusk {
				s.tickHostileUndeadCombat(ent, norm, rng)
			}
		case MobTypeDrowned:
			s.tickUndeadSunburn(ent, rng)
			s.tickHostileUndeadCombat(ent, norm, rng)
		case MobTypeSkeleton:
			if _, conv := s.StepSkeletonPowderSnowConversion(ent.ID, 1); conv {
				continue
			}
			s.tickUndeadSunburn(ent, rng)
		case MobTypeStray, MobTypeWitherSkeleton:
			if norm == MobTypeStray {
				s.tickUndeadSunburn(ent, rng)
			}
			s.tickHostileUndeadCombat(ent, norm, rng)
		case MobTypeSilverfish:
			s.tickSingleSilverfish(ent.ID, rng)
		}
	}
}

func (s *Server) tickUndeadSunburn(ent *world.Entity, rng *rand.Rand) {
	if ent == nil || !s.ShouldUndeadBurnInSunlightNow(ent.ID, false) {
		return
	}
	s.ensureUndeadState(ent.ID, ent.TypeName, rng)
	mgr := s.undeadManager()
	mgr.mu.Lock()
	st := mgr.undead[ent.ID]
	st.SunBurnTickCounter++
	tick := st.SunBurnTickCounter
	mgr.mu.Unlock()

	// Damage helmet or burn every 20 ticks (1s) in direct sunlight.
	if tick%20 == 0 && s.ShouldUndeadBurnInSunlightNow(ent.ID, true) {
		s.SetEntityFireTicks(ent.ID, 160)
		s.applyDamageToMob(ent, 1.0, nil, 0, false, false)
	}
}

func (s *Server) tickHostileUndeadCombat(ent *world.Entity, norm string, rng *rand.Rand) {
	if ent == nil || s.Difficulty() == DifficultyPeaceful {
		return
	}
	s.ensureUndeadState(ent.ID, norm, rng)
	mgr := s.undeadManager()
	mgr.mu.Lock()
	st := mgr.undead[ent.ID]
	if st.AttackCooldown > 0 {
		st.AttackCooldown--
	}
	cd := st.AttackCooldown
	hasTrident := canonicalResourceName(st.MainHand.ResolvedID()) == ItemTrident
	mgr.mu.Unlock()

	dim := ent.EffectiveDimension()
	cache := s.DimensionChunks(dim)
	target := s.findNearestWitherTarget(dim, ent.X, ent.Y, ent.Z, ZombieAggroRange)
	if target == nil {
		return
	}
	tSnap := target.Snapshot()
	dx := tSnap.X - ent.X
	dy := tSnap.Y - ent.Y
	dz := tSnap.Z - ent.Z
	horizDist := math.Hypot(dx, dz)
	dist3D := math.Sqrt(dx*dx + dy*dy + dz*dz)
	yaw := yawToward(dx, dz)

	switch norm {
	case MobTypeHusk:
		if horizDist > ZombieAttackStopDist {
			nx := ent.X + (dx/horizDist)*ZombieChaseSpeed
			nz := ent.Z + (dz/horizDist)*ZombieChaseSpeed
			feetY := int(math.Floor(ent.Y))
			if cache != nil {
				if stepX, stepY, stepZ, ok := tryApplyHorizontalStep(cache, ent.X, feetY, ent.Z, nx, nz); ok {
					nx, nz = stepX, stepZ
					ent.Y = float64(stepY)
				}
			}
			s.entities.Update(ent.ID, func(e *world.Entity) {
				e.X, e.Y, e.Z, e.Yaw, e.HeadYaw = nx, ent.Y, nz, yaw, yaw
			})
		}
		if horizDist <= ZombieAttackReach && math.Abs(dy) <= ZombieAttackVerticalReach && cd <= 0 {
			mgr.mu.Lock()
			if st := mgr.undead[ent.ID]; st != nil {
				st.AttackCooldown = ZombieAttackCooldownTicks
			}
			mgr.mu.Unlock()
			dealt := s.AttackPlayerFromMob(ent.ID, MobTypeHusk, ent.X, ent.Z, target, ZombieAttackDamage)
			if dealt > 0 {
				s.ApplyHuskMeleeHunger(ent.ID, target)
			}
		}

	case MobTypeDrowned:
		inWater := s.IsEntitySubmergedInWaterForConversion(ent.ID)
		speed := DrownedLandSpeed
		if inWater {
			speed = DrownedSwimSpeed
		}
		stopDist := ZombieAttackStopDist
		if hasTrident {
			stopDist = 8.0
		}
		if horizDist > stopDist {
			nx := ent.X + (dx/horizDist)*speed
			nz := ent.Z + (dz/horizDist)*speed
			s.entities.Update(ent.ID, func(e *world.Entity) {
				e.X, e.Z, e.Yaw, e.HeadYaw = nx, nz, yaw, yaw
			})
		}
		if hasTrident && dist3D >= DrownedTridentMinRange && dist3D <= DrownedTridentRange && cd <= 0 {
			mgr.mu.Lock()
			if st := mgr.undead[ent.ID]; st != nil {
				st.AttackCooldown = DrownedTridentCooldownTicks
			}
			mgr.mu.Unlock()
			_, _ = s.ShootDrownedTridentAtPlayer(ent.ID, target)
		} else if horizDist <= ZombieAttackReach && math.Abs(dy) <= ZombieAttackVerticalReach && cd <= 0 {
			mgr.mu.Lock()
			if st := mgr.undead[ent.ID]; st != nil {
				st.AttackCooldown = ZombieAttackCooldownTicks
			}
			mgr.mu.Unlock()
			s.AttackPlayerFromMob(ent.ID, MobTypeDrowned, ent.X, ent.Z, target, ZombieAttackDamage)
		}

	case MobTypeStray:
		if horizDist > SkeletonIdealStopDist {
			nx := ent.X + (dx/horizDist)*SkeletonChaseSpeed
			nz := ent.Z + (dz/horizDist)*SkeletonChaseSpeed
			s.entities.Update(ent.ID, func(e *world.Entity) {
				e.X, e.Z, e.Yaw, e.HeadYaw = nx, nz, yaw, yaw
			})
		}
		if dist3D <= SkeletonBowRange && cd <= 0 {
			mgr.mu.Lock()
			if st := mgr.undead[ent.ID]; st != nil {
				st.AttackCooldown = SkeletonShootCooldownTicks
			}
			mgr.mu.Unlock()
			s.ShootStrayArrowAtPlayer(ent.ID, ent.X, ent.Y, ent.Z, target)
		}

	case MobTypeWitherSkeleton:
		if horizDist > ZombieAttackStopDist {
			nx := ent.X + (dx/horizDist)*0.17
			nz := ent.Z + (dz/horizDist)*0.17
			s.entities.Update(ent.ID, func(e *world.Entity) {
				e.X, e.Z, e.Yaw, e.HeadYaw = nx, nz, yaw, yaw
			})
		}
		if horizDist <= 2.0 && math.Abs(dy) <= 2.4 && cd <= 0 {
			mgr.mu.Lock()
			if st := mgr.undead[ent.ID]; st != nil {
				st.AttackCooldown = ZombieAttackCooldownTicks
			}
			mgr.mu.Unlock()
			s.WitherSkeletonMeleeAttackPlayer(ent.ID, target)
		}
	}
}

func (s *Server) tickSingleSilverfish(silverfishID int32, rng *rand.Rand) {
	ent := s.entities.Get(silverfishID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeSilverfish {
		return
	}
	dim := ent.EffectiveDimension()
	s.ensureSilverfishState(silverfishID, dim)

	mgr := s.undeadManager()
	mgr.mu.Lock()
	st := mgr.silverfish[silverfishID]
	if st == nil {
		mgr.mu.Unlock()
		return
	}
	if st.AttackCooldown > 0 {
		st.AttackCooldown--
	}
	if st.MergeCooldown > 0 {
		st.MergeCooldown--
	}
	wakeNow := false
	if st.CallForHelpTicks > 0 {
		st.CallForHelpTicks--
		if st.CallForHelpTicks == 0 {
			wakeNow = true
		}
	}
	cd := st.AttackCooldown
	mgr.mu.Unlock()

	if wakeNow {
		s.WakeNearbyInfestedBlocks(dim, int(math.Floor(ent.X)), int(math.Floor(ent.Y)), int(math.Floor(ent.Z)), 8)
	}

	target := s.findNearestWitherTarget(dim, ent.X, ent.Y, ent.Z, 16.0)
	if target != nil {
		tSnap := target.Snapshot()
		dx := tSnap.X - ent.X
		dy := tSnap.Y - ent.Y
		dz := tSnap.Z - ent.Z
		horizDist := math.Hypot(dx, dz)
		yaw := yawToward(dx, dz)
		if horizDist > 0.8 {
			nx := ent.X + (dx/horizDist)*SilverfishChaseSpeed
			nz := ent.Z + (dz/horizDist)*SilverfishChaseSpeed
			s.entities.Update(silverfishID, func(e *world.Entity) {
				e.X, e.Z, e.Yaw, e.HeadYaw = nx, nz, yaw, yaw
			})
		}
		if horizDist <= SilverfishAttackReach && math.Abs(dy) <= 1.5 && cd <= 0 {
			mgr.mu.Lock()
			if st := mgr.silverfish[silverfishID]; st != nil {
				st.AttackCooldown = 20
			}
			mgr.mu.Unlock()
			s.AttackPlayerFromMob(silverfishID, MobTypeSilverfish, ent.X, ent.Z, target, SilverfishAttackDamage)
		}
	}
}
