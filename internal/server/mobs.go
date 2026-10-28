package server

import (
	"errors"
	"math"
	"math/rand"
	"strings"
	"sync"
	"sync/atomic"

	"regionio/internal/protocol"
	"regionio/internal/registry"
	"regionio/internal/world"
)

// Mob entity type identifiers and vanilla 26.1.2 BuiltInRegistries.ENTITY_TYPE IDs.
const (
	MobTypeSkeleton = "minecraft:skeleton"
	MobTypeCreeper  = "minecraft:creeper"
	MobTypeSpider   = "minecraft:spider"
	MobTypeCow      = "minecraft:cow"
	MobTypeSheep    = "minecraft:sheep"
	MobTypeChicken          = "minecraft:chicken"
	MobTypeArrow            = "minecraft:arrow"
	EntityTypeFishingBobber = "minecraft:fishing_bobber"

	// Vanilla 26.1.2 BuiltInRegistries.ENTITY_TYPE numeric network IDs (protocol 775).
	EntityTypeArrowID         int = 6
	EntityTypeChickenID       int = 26
	EntityTypeCowID           int = 30
	EntityTypeCreeperID       int = 32
	EntityTypePigID           int = 100
	EntityTypeSheepID         int = 111
	EntityTypeSkeletonID      int = 115
	EntityTypeSpiderID        int = 124
	EntityTypeZombieID        int = 150
	EntityTypeFishingBobberID int = 156

	// Default Mob Max Health values (vanilla 26.1.2).
	DefaultSkeletonMaxHealth float32 = 20.0
	DefaultCreeperMaxHealth  float32 = 20.0
	DefaultSpiderMaxHealth   float32 = 16.0
	DefaultCowMaxHealth      float32 = 10.0
	DefaultSheepMaxHealth    float32 = 8.0
	DefaultChickenMaxHealth  float32 = 4.0

	// Hostile Skeleton parameters (ranged bow combat + movement).
	SkeletonChaseSpeed         = 0.14
	SkeletonWanderSpeed        = 0.08
	SkeletonAggroRange         = 32.0
	SkeletonBowRange           = 15.0
	SkeletonIdealStopDist      = 8.0
	SkeletonShootCooldownTicks = 40
	SkeletonArrowBaseDamage    = float32(3.0)

	// Hostile Creeper parameters (swell / fuse / explosion).
	CreeperChaseSpeed        = 0.13
	CreeperWanderSpeed       = 0.08
	CreeperAggroRange        = 32.0
	CreeperIgniteRange       = 3.0
	CreeperCancelRange       = 7.0
	CreeperFuseDurationTicks      = 30 // 1.5s at 20 TPS
	CreeperExplosionRadius        = 3.0
	ChargedCreeperExplosionRadius = 6.0
	GhastFireballExplosionRadius  = 1.0
	CreeperMaxBaseDamage          = float32(43.0)

	// Hostile/Neutral Spider parameters (light-dependent aggro + leap + bite).
	SpiderChaseSpeed          = 0.18
	SpiderWanderSpeed         = 0.10
	SpiderAggroRange          = 16.0
	SpiderHostileMaxLight     = byte(11)
	SpiderAttackReach         = 1.9
	SpiderAttackVerticalReach = 1.6
	SpiderAttackStopDist      = 1.0
	SpiderAttackDamage        = float32(2.0)
	SpiderAttackCooldownTicks = 20
	SpiderLeapMinDist         = 2.0
	SpiderLeapMaxDist         = 4.0
	SpiderLeapSpeed           = 0.38
	SpiderLeapCooldownTicks   = 25

	// Passive Animal parameters (Cow, Sheep, Chicken, Pig).
	CowWanderSpeed           = 0.08
	SheepWanderSpeed         = 0.08
	ChickenWanderSpeed       = 0.09
	AnimalPanicSpeedMult     = 1.5
	AnimalPanicDurationTicks = 60
	AnimalTemptRange         = 10.0
	AnimalTemptStopDist      = 1.75
	AnimalBreedRange         = 8.0
	AnimalLoveDurationTicks  = 600  // 30s in love
	AnimalBreedCooldownTicks = 6000 // 5m breeding cooldown
	ChickenEggMinTicks       = 6000 // 5m..10m egg timer
	ChickenEggMaxTicks       = 12000
	ChickenMaxFallVelocity   = int16(-400) // slow flapping fall
	SheepWoolRegrowTicks     = 1200

	// SynchedEntityData constants for Creeper and Sheep in Minecraft 26.1.2 (protocol 775).
	EntityDataCreeperSwellDirID byte  = 17 // Creeper.DATA_SWELL_DIR (INT serializer = 1)
	EntityDataCreeperPoweredID  byte  = 18 // Creeper.DATA_IS_POWERED (BOOLEAN serializer = 8)
	EntityDataCreeperIgnitedID  byte  = 19 // Creeper.DATA_IS_IGNITED (BOOLEAN serializer = 8)
	EntityDataSheepWoolID       byte  = 18 // Sheep.DATA_WOOL_ID (BYTE serializer = 0: color | 0x10 sheared)
	EntityDataSerializerByte    int32 = 0
	EntityDataSerializerInt     int32 = 1
	EntityDataSerializerBool    int32 = 8

	// ClientboundEntityEventPacket (0x22) event ID for breeding hearts.
	EntityEventLoveHearts byte = 18
)

var (
	hostileMobTypes = [4]string{
		MobTypeZombie,
		MobTypeSkeleton,
		MobTypeCreeper,
		MobTypeSpider,
	}
	passiveAnimalTypes = [4]string{
		MobTypePig,
		MobTypeCow,
		MobTypeSheep,
		MobTypeChicken,
	}
	fallbackEntityTypeIDs = map[string]int{
		MobTypeArrow:              EntityTypeArrowID,
		MobTypeCat:                EntityTypeCatID,
		MobTypeChicken:            EntityTypeChickenID,
		MobTypeCow:                EntityTypeCowID,
		MobTypeCreeper:            EntityTypeCreeperID,
		MobTypeDonkey:             EntityTypeDonkeyID,
		MobTypeHorse:              EntityTypeHorseID,
		MobTypeIronGolem:          EntityTypeIronGolemID,
		MobTypeLlama:              EntityTypeLlamaID,
		MobTypeMule:               EntityTypeMuleID,
		MobTypeParrot:             EntityTypeParrotID,
		MobTypePhantom:            EntityTypePhantomID,
		MobTypePig:                EntityTypePigID,
		MobTypeSheep:              EntityTypeSheepID,
		MobTypeSkeleton:           EntityTypeSkeletonID,
		MobTypeSnowGolem:          EntityTypeSnowGolemID,
		MobTypeSpider:             EntityTypeSpiderID,
		MobTypeTraderLlama:        EntityTypeTraderLlamaID,
		MobTypeVillager:           int(EntityTypeVillagerID),
		MobTypeWolf:               EntityTypeWolfID,
		MobTypeZombie:             EntityTypeZombieID,
		MobTypeZombieVillager:     EntityTypeZombieVillagerID,
		EntityTypeFishingBobber:   EntityTypeFishingBobberID,
		EntityTypeArmorStand:      EntityTypeArmorStandID,
		EntityTypeGlowItemFrame:   EntityTypeGlowItemFrameID,
		EntityTypeItemFrame:       EntityTypeItemFrameID,
		EntityTypePainting:        EntityTypePaintingID,
		EntityTypeAreaEffectCloud: EntityTypeAreaEffectCloudID,
		EntityTypeDragonFireball:  EntityTypeDragonFireballID,
		MobTypeEnderDragon:        EntityTypeEnderDragonID,
		EntityTypeEndCrystal:      EntityTypeEndCrystalID,
		MobTypeEvoker:             EntityTypeEvokerID,
		MobTypePillager:           EntityTypePillagerID,
		MobTypeRavager:            EntityTypeRavagerID,
		MobTypeVindicator:         EntityTypeVindicatorID,
		MobTypeWarden:             EntityTypeWardenID,
		MobTypeWitch:              EntityTypeWitchID,
		MobTypeWither:             EntityTypeWitherID,
		MobTypeWitherSkeleton:     EntityTypeWitherSkeletonID,
		EntityTypeWitherSkull:      EntityTypeWitherSkullID,
		EntityTypeFireworkRocket:   EntityTypeFireworkRocketID,
		MobTypeArmadillo:           EntityTypeArmadilloID,
		MobTypeBogged:              EntityTypeBoggedID,
		MobTypeBreeze:              EntityTypeBreezeID,
		BreezeWindChargeEntityType: EntityTypeBreezeWindChargeID,
		MobTypeCamel:               EntityTypeCamelID,
		MobTypeSniffer:             EntityTypeSnifferID,
		WindChargeEntityType:       EntityTypeWindChargeID,
		MobTypeAllay:               EntityTypeAllayID,
		MobTypeAxolotl:             EntityTypeAxolotlID,
		MobTypeFrog:                EntityTypeFrogID,
		MobTypeGoat:                EntityTypeGoatID,
		MobTypeMagmaCube:           EntityTypeMagmaCubeID,
		MobTypeSlime:               EntityTypeSlimeID,
		MobTypeTadpole:             EntityTypeTadpoleID,
		MobTypeSkeletonHorse:       EntityTypeSkeletonHorseID,
		MobTypeZombieHorse:         EntityTypeZombieHorseID,
		MobTypeWanderingTrader:     EntityTypeWanderingTraderID,
		EntityTypeLlamaSpit:        EntityTypeLlamaSpitID,
		MobTypeBee:                 EntityTypeBeeID,
		MobTypeCopperGolem:         EntityTypeCopperGolemID,
		MobTypeFox:                 EntityTypeFoxID,
		MobTypePanda:               EntityTypePandaID,
		MobTypeOcelot:              EntityTypeOcelotID,
		MobTypePiglin:              EntityTypePiglinID,
		MobTypePiglinBrute:         EntityTypePiglinBruteID,
		MobTypeZombifiedPiglin:     EntityTypeZombifiedPiglinID,
		MobTypeHoglin:              EntityTypeHoglinID,
		MobTypeZoglin:              EntityTypeZoglinID,
		MobTypeStrider:             EntityTypeStriderID,
		MobTypeDolphin:             EntityTypeDolphinID,
		MobTypeTurtle:              EntityTypeTurtleID,
		MobTypeSquid:               EntityTypeSquidID,
		MobTypeGlowSquid:           EntityTypeGlowSquidID,
		MobTypeCod:                 EntityTypeCodID,
		MobTypeSalmon:              EntityTypeSalmonID,
		MobTypeTropicalFish:        EntityTypeTropicalFishID,
		MobTypePufferfish:          EntityTypePufferfishID,
		MobTypeGuardian:            EntityTypeGuardianID,
		MobTypeElderGuardian:       EntityTypeElderGuardianID,
		MobTypeVex:                 EntityTypeVexID,
		EntityTypeEvokerFangs:      EntityTypeEvokerFangsID,
		MobTypeIllusioner:          EntityTypeIllusionerID,
		MobTypeHusk:                EntityTypeHuskID,
		MobTypeDrowned:             EntityTypeDrownedID,
		MobTypeStray:               EntityTypeStrayID,
		MobTypeSilverfish:          EntityTypeSilverfishID,
		MobTypeEnderman:            EntityTypeEndermanID,
		MobTypeEndermite:           EntityTypeEndermiteID,
		MobTypeBlaze:               EntityTypeBlazeID,
		MobTypeGhast:               EntityTypeGhastID,
		EntityTypeFireball:         EntityTypeFireballID,
		EntityTypeSmallFireball:    EntityTypeSmallFireballID,
	}
	creeperGriefingDisabled atomic.Bool
)

// AllHostileMobTypes returns a copy of the 4 hostile mob type identifiers
// (zombie, skeleton, creeper, spider).
func AllHostileMobTypes() []string {
	out := make([]string, len(hostileMobTypes))
	copy(out, hostileMobTypes[:])
	return out
}

// AllPassiveAnimalTypes returns a copy of the 4 passive farm animal type
// identifiers (pig, cow, sheep, chicken).
func AllPassiveAnimalTypes() []string {
	out := make([]string, len(passiveAnimalTypes))
	copy(out, passiveAnimalTypes[:])
	return out
}

// IsPassiveAnimal reports whether typeName is a breedable passive farm/wild animal
// (pig, cow, sheep, chicken, bee, fox, panda, ocelot, or strider).
func IsPassiveAnimal(typeName string) bool {
	switch canonicalResourceName(typeName) {
	case MobTypePig, MobTypeCow, MobTypeSheep, MobTypeChicken, MobTypeBee, MobTypeFox, MobTypePanda, MobTypeOcelot, MobTypeStrider:
		return true
	default:
		return false
	}
}

// ResolveEntityTypeID returns the protocol 775 BuiltInRegistries.ENTITY_TYPE
// numeric ID for typeName, consulting registry.EntityTypeIndex first and
// falling back to the verified 26.1.2 built-in table.
func ResolveEntityTypeID(typeName string) int {
	norm := canonicalResourceName(typeName)
	if idx := registry.EntityTypeIndex(norm); idx >= 0 {
		return idx
	}
	if idx, ok := fallbackEntityTypeIDs[norm]; ok {
		return idx
	}
	if id, ok := EntityTypeRegistryID(norm); ok {
		return int(id)
	}
	return -1
}

// CreeperBlockGriefing reports whether creeper explosions destroy nearby
// non-indestructible blocks (true by default).
func (s *Server) CreeperBlockGriefing() bool {
	return !creeperGriefingDisabled.Load()
}

// SetCreeperBlockGriefing enables or disables block destruction by creeper explosions.
func (s *Server) SetCreeperBlockGriefing(enabled bool) {
	creeperGriefingDisabled.Store(!enabled)
}

// ResolveMobMaxHealth returns the vanilla 26.1.2 maximum health for a mob type.
func ResolveMobMaxHealth(typeName string) float32 {
	switch canonicalResourceName(typeName) {
	case MobTypeZombie:
		return DefaultZombieMaxHealth
	case MobTypeZombieVillager:
		return DefaultZombieVillagerMaxHealth
	case MobTypeSkeleton:
		return DefaultSkeletonMaxHealth
	case MobTypeBogged:
		return DefaultBoggedMaxHealth
	case MobTypeCreeper:
		return DefaultCreeperMaxHealth
	case MobTypeSpider:
		return DefaultSpiderMaxHealth
	case MobTypeBreeze:
		return DefaultBreezeMaxHealth
	case MobTypePig:
		return DefaultPigMaxHealth
	case MobTypeCow:
		return DefaultCowMaxHealth
	case MobTypeSheep:
		return DefaultSheepMaxHealth
	case MobTypeChicken:
		return DefaultChickenMaxHealth
	case MobTypeSniffer:
		return DefaultSnifferMaxHealth
	case MobTypeArmadillo:
		return DefaultArmadilloMaxHealth
	case MobTypeCamel:
		return DefaultCamelMaxHealth
	case MobTypeAllay:
		return DefaultAllayMaxHealth
	case MobTypeFrog:
		return DefaultFrogMaxHealth
	case MobTypeTadpole:
		return DefaultTadpoleMaxHealth
	case MobTypeAxolotl:
		return DefaultAxolotlMaxHealth
	case MobTypeGoat:
		return DefaultGoatMaxHealth
	case MobTypeVillager:
		return DefaultVillagerMaxHealth
	case MobTypeWolf:
		return DefaultWolfWildMaxHealth
	case MobTypeCat:
		return DefaultCatMaxHealth
	case MobTypeParrot:
		return DefaultParrotMaxHealth
	case MobTypeHorse:
		return DefaultHorseMaxHealth
	case MobTypeDonkey:
		return DefaultDonkeyMaxHealth
	case MobTypeMule:
		return DefaultMuleMaxHealth
	case MobTypeSkeletonHorse:
		return DefaultSkeletonHorseMaxHealth
	case MobTypeZombieHorse:
		return DefaultZombieHorseMaxHealth
	case MobTypeLlama, MobTypeTraderLlama:
		return DefaultLlamaMaxHealth
	case MobTypeWanderingTrader:
		return DefaultWanderingTraderMaxHealth
	case MobTypeIronGolem:
		return DefaultIronGolemMaxHealth
	case MobTypeSnowGolem:
		return DefaultSnowGolemMaxHealth
	case MobTypeCopperGolem:
		return DefaultCopperGolemMaxHealth
	case MobTypePhantom:
		return DefaultPhantomMaxHealth
	case MobTypePillager:
		return DefaultPillagerMaxHealth
	case MobTypeVindicator:
		return DefaultVindicatorMaxHealth
	case MobTypeEvoker:
		return DefaultEvokerMaxHealth
	case MobTypeRavager:
		return DefaultRavagerMaxHealth
	case MobTypeWarden:
		return DefaultWardenMaxHealth
	case MobTypeWitch:
		return DefaultWitchMaxHealth
	case MobTypeWither:
		return DefaultWitherMaxHealth
	case MobTypeWitherSkeleton:
		return DefaultWitherSkeletonMaxHealth
	case MobTypeEnderDragon:
		return DefaultEnderDragonMaxHealth
	case MobTypeBee:
		return DefaultBeeMaxHealth
	case MobTypeFox:
		return DefaultFoxMaxHealth
	case MobTypePanda:
		return DefaultPandaMaxHealth
	case MobTypeOcelot:
		return DefaultOcelotMaxHealth
	case MobTypePiglin:
		return DefaultPiglinMaxHealth
	case MobTypePiglinBrute:
		return DefaultPiglinBruteMaxHealth
	case MobTypeZombifiedPiglin:
		return DefaultZombifiedPiglinMaxHealth
	case MobTypeHoglin:
		return DefaultHoglinMaxHealth
	case MobTypeZoglin:
		return DefaultZoglinMaxHealth
	case MobTypeStrider:
		return DefaultStriderMaxHealth
	case MobTypeDolphin:
		return DefaultDolphinMaxHealth
	case MobTypeTurtle:
		return DefaultTurtleMaxHealth
	case MobTypeSquid:
		return DefaultSquidMaxHealth
	case MobTypeGlowSquid:
		return DefaultGlowSquidMaxHealth
	case MobTypeCod:
		return DefaultCodMaxHealth
	case MobTypeSalmon:
		return DefaultSalmonMaxHealth
	case MobTypeTropicalFish:
		return DefaultTropicalFishMaxHealth
	case MobTypePufferfish:
		return DefaultPufferfishMaxHealth
	case MobTypeGuardian:
		return DefaultGuardianMaxHealth
	case MobTypeElderGuardian:
		return DefaultElderGuardianMaxHealth
	case MobTypeVex:
		return DefaultVexMaxHealth
	case MobTypeIllusioner:
		return DefaultIllusionerMaxHealth
	case MobTypeHusk:
		return DefaultHuskMaxHealth
	case MobTypeDrowned:
		return DefaultDrownedMaxHealth
	case MobTypeStray:
		return DefaultStrayMaxHealth
	case MobTypeSilverfish:
		return DefaultSilverfishMaxHealth
	case MobTypeEnderman:
		return DefaultEndermanMaxHealth
	case MobTypeEndermite:
		return DefaultEndermiteMaxHealth
	case MobTypeBlaze:
		return DefaultBlazeMaxHealth
	case MobTypeGhast:
		return DefaultGhastMaxHealth
	case MobTypeMagmaCube, MobTypeSlime:
		return float32(MagmaCubeSizeLarge * MagmaCubeSizeLarge)
	default:
		return DefaultMobMaxHealth
	}
}

func animalWanderSpeed(typeName string) float64 {
	switch canonicalResourceName(typeName) {
	case MobTypeCow:
		return CowWanderSpeed
	case MobTypeSheep:
		return SheepWanderSpeed
	case MobTypeChicken:
		return ChickenWanderSpeed
	case MobTypeSniffer:
		return SnifferWanderSpeed
	case MobTypeArmadillo:
		return ArmadilloWanderSpeed
	case MobTypeCamel:
		return CamelWalkSpeed
	case MobTypeAllay:
		return AllayFlySpeed
	case MobTypeFrog:
		return FrogWanderSpeed
	case MobTypeTadpole:
		return TadpoleSwimSpeed
	case MobTypeAxolotl:
		return AxolotlSwimSpeed
	case MobTypeGoat:
		return GoatWanderSpeed
	case MobTypeBee:
		return BeeFlySpeed
	case MobTypeFox:
		return FoxWanderSpeed
	case MobTypePanda:
		return PandaWanderSpeed
	case MobTypeOcelot:
		return OcelotWanderSpeed
	case MobTypePiglin:
		return PiglinWanderSpeed
	case MobTypePiglinBrute:
		return PiglinBruteSpeed
	case MobTypeHoglin:
		return HoglinWanderSpeed
	case MobTypeZoglin:
		return ZoglinChaseSpeed
	case MobTypeStrider:
		return StriderLandSpeed
	case MobTypeDolphin:
		return DolphinSwimSpeed
	case MobTypeTurtle:
		return TurtleLandSpeed
	case MobTypeSquid, MobTypeGlowSquid:
		return SquidJetSpeed
	case MobTypeCod, MobTypeSalmon, MobTypeTropicalFish, MobTypePufferfish:
		return FishSwimSpeed
	case MobTypeGuardian:
		return GuardianSwimSpeed
	case MobTypeElderGuardian:
		return ElderGuardianSwimSpeed
	case MobTypeVex:
		return VexFlySpeed
	case MobTypePillager, MobTypeVindicator, MobTypeEvoker, MobTypeIllusioner, MobTypeWitch, MobTypeRavager:
		return RaiderWanderSpeed
	case MobTypeHusk, MobTypeDrowned:
		return ZombieWanderSpeed
	case MobTypeStray:
		return SkeletonWanderSpeed
	case MobTypeSilverfish:
		return SilverfishWanderSpeed
	case MobTypeEnderman:
		return EndermanWanderSpeed
	case MobTypeEndermite:
		return EndermiteWanderSpeed
	case MobTypeVillager, MobTypeWanderingTrader:
		return VillagerWanderSpeed
	case MobTypeHorse, MobTypeDonkey, MobTypeMule, MobTypeSkeletonHorse, MobTypeZombieHorse, MobTypeLlama, MobTypeTraderLlama:
		return DonkeyDefaultSpeed
	default:
		return PigWanderSpeed
	}
}

// SpawnMob spawns a mob entity of typeName at (x, y, z) in the Overworld,
// initializes its AI and health state, and broadcasts PlayAddEntity to nearby viewers.
func (s *Server) SpawnMob(x, y, z float64, typeName string) (int32, error) {
	return s.SpawnMobInDimension(world.DimensionOverworld, x, y, z, typeName)
}

// SpawnMobInDimension spawns a mob entity of typeName at (x, y, z) in dimension,
// initializes its AI and health state, and broadcasts PlayAddEntity to nearby viewers.
func (s *Server) SpawnMobInDimension(dimension string, x, y, z float64, typeName string) (int32, error) {
	if s == nil || s.entities == nil {
		return 0, errors.New("server: nil entity manager")
	}
	s.EnsureWardenSummonHook()
	s.EnsureCopperGolemStatueReviveHook()
	normDim := world.NormalizeDimension(dimension)
	norm := canonicalResourceName(typeName)
	if norm == MobTypeVillager {
		return s.SpawnVillager(x, y, z, VillagerProfessionFarmer, MinVillagerLevel)
	}
	if norm == MobTypeWanderingTrader {
		traderID, _, err := s.SpawnWanderingTrader(normDim, x, y, z, false, nil)
		return traderID, err
	}
	if norm == MobTypeWither {
		return s.SpawnWither(normDim, x, y, z, true)
	}
	if norm == MobTypeEnderDragon {
		return s.SpawnEnderDragon(x, y, z)
	}
	if norm == MobTypeWarden {
		return s.SpawnWarden(normDim, x, y, z, false)
	}
	if norm == MobTypeIronGolem {
		return s.SpawnIronGolem(normDim, x, y, z, false)
	}
	if norm == MobTypeCopperGolem {
		return s.SpawnCopperGolem(normDim, x, y, z, "north")
	}
	if norm == MobTypePhantom {
		return s.SpawnPhantom(normDim, x, y, z, nil)
	}
	if norm == MobTypeSniffer {
		return s.SpawnSniffer(normDim, x, y, z, false)
	}
	if norm == MobTypeArmadillo {
		return s.SpawnArmadillo(normDim, x, y, z, false)
	}
	if norm == MobTypeCamel {
		return s.SpawnCamel(normDim, x, y, z, false)
	}
	if norm == MobTypeBreeze {
		return s.SpawnBreeze(normDim, x, y, z)
	}
	if norm == MobTypeBogged {
		return s.SpawnBogged(normDim, x, y, z)
	}
	if norm == MobTypeAllay {
		return s.SpawnAllay(normDim, x, y, z)
	}
	if norm == MobTypeFrog {
		return s.SpawnFrog(normDim, x, y, z, -1)
	}
	if norm == MobTypeTadpole {
		return s.SpawnTadpole(normDim, x, y, z)
	}
	if norm == MobTypeAxolotl {
		return s.SpawnAxolotl(normDim, x, y, z, -1, false)
	}
	if norm == MobTypeGoat {
		return s.SpawnGoat(normDim, x, y, z, false, false)
	}
	if norm == MobTypeBee {
		return s.SpawnBee(normDim, x, y, z, false)
	}
	if norm == MobTypeFox {
		return s.SpawnFox(normDim, x, y, z, false)
	}
	if norm == MobTypePanda {
		return s.SpawnPanda(normDim, x, y, z, false, PandaGeneNormal, PandaGeneNormal, nil)
	}
	if norm == MobTypeOcelot {
		return s.SpawnOcelot(normDim, x, y, z, false)
	}
	if norm == MobTypePiglin {
		return s.SpawnPiglin(normDim, x, y, z, false)
	}
	if norm == MobTypePiglinBrute {
		return s.SpawnPiglinBrute(normDim, x, y, z)
	}
	if norm == MobTypeZombifiedPiglin {
		return s.SpawnZombifiedPiglin(normDim, x, y, z, false)
	}
	if norm == MobTypeHoglin {
		return s.SpawnHoglin(normDim, x, y, z, false)
	}
	if norm == MobTypeZoglin {
		return s.SpawnZoglin(normDim, x, y, z, false)
	}
	if norm == MobTypeStrider {
		return s.SpawnStrider(normDim, x, y, z, false)
	}
	if norm == MobTypeDolphin {
		return s.SpawnDolphin(normDim, x, y, z)
	}
	if norm == MobTypeTurtle {
		return s.SpawnTurtle(normDim, x, y, z, false)
	}
	if norm == MobTypeSquid {
		return s.SpawnSquid(normDim, x, y, z)
	}
	if norm == MobTypeGlowSquid {
		return s.SpawnGlowSquid(normDim, x, y, z)
	}
	if IsFishMobType(norm) {
		return s.SpawnFish(normDim, x, y, z, norm, false, nil)
	}
	if norm == MobTypeGuardian {
		return s.SpawnGuardian(normDim, x, y, z)
	}
	if norm == MobTypeElderGuardian {
		return s.SpawnElderGuardian(normDim, x, y, z)
	}
	if norm == MobTypeVex {
		return s.SpawnVex(normDim, x, y, z, 0)
	}
	if norm == MobTypeIllusioner {
		return s.SpawnIllusioner(normDim, x, y, z)
	}
	if norm == MobTypeHusk {
		return s.SpawnHusk(normDim, x, y, z, false)
	}
	if norm == MobTypeDrowned {
		return s.SpawnDrowned(normDim, x, y, z, false, false)
	}
	if norm == MobTypeStray {
		return s.SpawnStray(normDim, x, y, z)
	}
	if norm == MobTypeSilverfish {
		return s.SpawnSilverfish(normDim, x, y, z)
	}
	if norm == MobTypeEnderman {
		return s.SpawnEnderman(normDim, x, y, z)
	}
	if norm == MobTypeEndermite {
		return s.SpawnEndermite(normDim, x, y, z, false)
	}
	if norm == MobTypeBlaze {
		return s.SpawnBlaze(normDim, x, y, z)
	}
	if norm == MobTypeGhast {
		return s.SpawnGhast(normDim, x, y, z)
	}
	if norm == MobTypeMagmaCube {
		return s.SpawnMagmaCube(normDim, x, y, z, MagmaCubeSizeSmall)
	}
	if norm == MobTypeSlime {
		return s.SpawnSlime(normDim, x, y, z, MagmaCubeSizeSmall)
	}
	typeID := ResolveEntityTypeID(norm)
	if typeID < 0 {
		return 0, errors.New("server: unknown mob entity type " + norm)
	}
	id := s.entities.Add(&world.Entity{
		TypeID:    typeID,
		TypeName:  norm,
		Dimension: normDim,
		X:         x,
		Y:         y,
		Z:         z,
		OnGround:  true,
	})
	if IsRaiderMobType(norm) || IsIllagerMob(norm) {
		s.ensureIllagerOrRaiderState(id, norm, false)
	}
	if IsZombieFamilyMob(norm) || IsSkeletonFamilyMob(norm) {
		s.ensureUndeadState(id, norm, nil)
	}
	if IsTameableMob(norm) {
		_ = s.ensureTamedMobState(id, norm)
		switch norm {
		case MobTypeWolf:
			_ = s.ensureWolfDetailState(id)
		case MobTypeCat:
			_ = s.ensureCatDetailState(id, nil)
		case MobTypeParrot:
			_ = s.ensureParrotDetailState(id, nil)
		}
	}
	if IsEquineOrLlamaMob(norm) {
		s.initEquineOrLlamaOnSpawn(id, norm, nil)
	}
	tr := s.entityTracker()
	tr.mu.Lock()
	mem := tr.mobStateFor(id, tr.aiTickID)
	mem.healthInit = true
	if IsEquineOrLlamaMob(norm) {
		mem.health = s.EquineMaxHealth(id)
	} else {
		mem.health = ResolveMobMaxHealth(norm)
	}
	mem.lastHealth = mem.health
	if norm == MobTypeChicken && mem.eggTimer <= 0 {
		mem.eggTimer = ChickenEggMinTicks
	}
	tr.mu.Unlock()

	if ent := s.entities.Get(id); ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, ent))
	}
	return id, nil
}

// CreeperFuseState returns the current fuse tick counter (0..30) and swell
// direction (-1, 0, or 1) for a creeper entity.
func (s *Server) CreeperFuseState(entityID int32) (fuseTicks int, swellDir int8) {
	if s == nil {
		return 0, 0
	}
	tr := s.entityTracker()
	tr.mu.Lock()
	defer tr.mu.Unlock()
	mem := tr.mobStates[entityID]
	if mem == nil {
		return 0, 0
	}
	return mem.creeperFuseTicks, mem.creeperSwellDir
}

// ProvokeSpider marks a spider entity as provoked so it chases and attacks
// players even in bright daylight.
func (s *Server) ProvokeSpider(entityID int32) {
	if s == nil {
		return
	}
	tr := s.entityTracker()
	tr.mu.Lock()
	mem := tr.mobStateFor(entityID, tr.aiTickID)
	mem.provoked = true
	tr.mu.Unlock()
}

// SetChickenEggTimer sets the remaining ticks until a chicken entity lays its
// next egg.
func (s *Server) SetChickenEggTimer(entityID int32, ticks int) {
	if s == nil {
		return
	}
	tr := s.entityTracker()
	tr.mu.Lock()
	mem := tr.mobStateFor(entityID, tr.aiTickID)
	mem.eggTimer = ticks
	tr.mu.Unlock()
}

// EncodeCreeperSwellDataPacket encodes a ClientboundSetEntityDataPacket (0x63)
// updating a creeper's DATA_SWELL_DIR (-1 defusing, 0 idle, 1 swelling) and
// DATA_IS_IGNITED flag.
func EncodeCreeperSwellDataPacket(entityID int32, swellDir int32, ignited bool) []byte {
	w := protocol.NewWriter(16)
	w.VarInt(entityID)
	w.Byte(EntityDataCreeperSwellDirID)
	w.VarInt(EntityDataSerializerInt)
	w.VarInt(swellDir)
	w.Byte(EntityDataCreeperIgnitedID)
	w.VarInt(EntityDataSerializerBool)
	w.Bool(ignited)
	w.Byte(0xFF)
	return w.Bytes()
}

// EncodeSheepWoolDataPacket encodes a ClientboundSetEntityDataPacket (0x63)
// updating a sheep's DATA_WOOL_ID byte (low 4 bits = color 0..15, 0x10 = sheared).
func EncodeSheepWoolDataPacket(entityID int32, color byte, sheared bool) []byte {
	flags := color & 0x0F
	if sheared {
		flags |= 0x10
	}
	w := protocol.NewWriter(12)
	w.VarInt(entityID)
	w.Byte(EntityDataSheepWoolID)
	w.VarInt(EntityDataSerializerByte)
	w.Byte(flags)
	w.Byte(0xFF)
	return w.Bytes()
}

// IsAnimalBreedingFood reports whether itemName is a valid temptation and
// breeding food for a passive animal of typeName in vanilla 26.1.2.
func IsAnimalBreedingFood(typeName, itemName string) bool {
	mob := canonicalResourceName(typeName)
	item := canonicalResourceName(itemName)
	switch mob {
	case MobTypeCow, MobTypeSheep:
		return item == "minecraft:wheat"
	case MobTypePig:
		return item == "minecraft:carrot" || item == "minecraft:potato" || item == "minecraft:beetroot"
	case MobTypeChicken:
		switch item {
		case "minecraft:wheat_seeds", "minecraft:beetroot_seeds",
			"minecraft:melon_seeds", "minecraft:pumpkin_seeds",
			"minecraft:torchflower_seeds", "minecraft:pitcher_pod":
			return true
		}
	case MobTypeSniffer:
		return item == TorchflowerSeedsItemID
	case MobTypeArmadillo:
		return item == SpiderEyeItemID
	case MobTypeCamel:
		return item == CactusItemID
	case MobTypeFrog:
		return item == SlimeBallItemID
	case MobTypeAxolotl:
		return item == TropicalFishBucketItemID
	case MobTypeGoat:
		return item == GoatBreedingFoodItemID
	case MobTypeBee:
		return IsBeeBreedItem(item)
	}
	return false
}

// IsSpiderHostile reports whether a spider at (x, feetY, z) is currently
// hostile toward players: true when provoked by an attack or when effective
// ambient light at its position is <= SpiderHostileMaxLight (11).
func (s *Server) IsSpiderHostile(x float64, feetY int, z float64, provoked bool) bool {
	if provoked {
		return true
	}
	if s == nil || s.chunks == nil {
		return true
	}
	bx := int(math.Floor(x))
	bz := int(math.Floor(z))
	_, _, internalLight := s.EffectiveLightAt(bx, feetY, bz)
	return internalLight <= SpiderHostileMaxLight
}

// selectTargetWithinRange finds the nearest valid player within aggroRange of (x, y, z).
// Survival and Adventure players take priority over Creative players; dead and
// Spectator players are ignored.
func selectTargetWithinRange(targets []playerTarget, x, y, z, aggroRange float64) (playerTarget, bool) {
	maxDistSq := aggroRange * aggroRange
	bestSurvivalIdx := -1
	bestSurvivalDistSq := maxDistSq
	bestFallbackIdx := -1
	bestFallbackDistSq := maxDistSq

	for i := range targets {
		pt := &targets[i]
		if pt.session == nil || pt.health <= 0 || pt.gameMode == GameModeSpectator || pt.session.IsDead() {
			continue
		}
		dx := pt.x - x
		dy := pt.y - y
		dz := pt.z - z
		distSq := dx*dx + dy*dy + dz*dz
		if distSq > maxDistSq {
			continue
		}
		if pt.gameMode == GameModeSurvival || pt.gameMode == GameModeAdventure {
			if bestSurvivalIdx < 0 || distSq < bestSurvivalDistSq {
				bestSurvivalIdx = i
				bestSurvivalDistSq = distSq
			}
		} else if bestFallbackIdx < 0 || distSq < bestFallbackDistSq {
			bestFallbackIdx = i
			bestFallbackDistSq = distSq
		}
	}
	if bestSurvivalIdx >= 0 {
		return targets[bestSurvivalIdx], true
	}
	if bestFallbackIdx >= 0 {
		return targets[bestFallbackIdx], true
	}
	return playerTarget{}, false
}

// stepHostileChaseToward advances a mob along a path or direct line toward
// target at chaseSpeed, stopping when horizontal distance <= stopDist.
func stepHostileChaseToward(
	chunks *world.Cache,
	mem *mobMemory,
	x float64,
	feetY int,
	z float64,
	yaw float32,
	targetX, targetY, targetZ float64,
	chaseSpeed, stopDist float64,
) (nextX, nextY, nextZ float64, nextYaw float32) {
	dx := targetX - x
	dz := targetZ - z
	horizDist := math.Hypot(dx, dz)

	nextYaw = yawToward(dx, dz)
	if math.IsNaN(float64(nextYaw)) {
		nextYaw = yaw
	}
	nextX = x
	nextY = float64(feetY)
	nextZ = z

	if horizDist <= stopDist {
		return nextX, nextY, nextZ, nextYaw
	}

	targetFeetY := int(math.Floor(targetY + 0.01))
	directOK := math.Abs(float64(targetFeetY-feetY)) <= 1 &&
		hasDirectWalkableLine(chunks, x, feetY, z, targetX, targetZ)

	var moveGoalX, moveGoalZ float64
	if directOK {
		mem.waypoints = mem.waypoints[:0]
		mem.waypointIdx = 0
		moveGoalX = targetX
		moveGoalZ = targetZ
	} else {
		needRepath := mem.repathCooldown <= 0 || mem.waypointIdx >= len(mem.waypoints)
		if !needRepath && len(mem.waypoints) > 0 {
			last := mem.waypoints[len(mem.waypoints)-1]
			if absInt(last.X-int(math.Floor(targetX))) > 2 || absInt(last.Z-int(math.Floor(targetZ))) > 2 {
				needRepath = true
			}
		}
		if needRepath {
			startX := int(math.Floor(x))
			startZ := int(math.Floor(z))
			goalBX := int(math.Floor(targetX))
			goalBZ := int(math.Floor(targetZ))
			mem.waypoints = FindPath(chunks, startX, feetY, startZ, goalBX, targetFeetY, goalBZ, MaxPathSearchNodes)
			mem.waypointIdx = 0
			mem.repathCooldown = PathRepathIntervalTicks
		}
		for mem.waypointIdx < len(mem.waypoints) {
			wp := mem.waypoints[mem.waypointIdx]
			wx := float64(wp.X) + 0.5
			wz := float64(wp.Z) + 0.5
			if math.Hypot(wx-x, wz-z) <= waypointAdvanceDist {
				mem.waypointIdx++
				continue
			}
			break
		}
		if mem.waypointIdx < len(mem.waypoints) {
			wp := mem.waypoints[mem.waypointIdx]
			moveGoalX = float64(wp.X) + 0.5
			moveGoalZ = float64(wp.Z) + 0.5
		} else {
			moveGoalX = targetX
			moveGoalZ = targetZ
		}
	}

	stepDX := moveGoalX - x
	stepDZ := moveGoalZ - z
	stepDist := math.Hypot(stepDX, stepDZ)
	if stepDist > 1e-5 {
		stepLen := chaseSpeed
		if stepLen > stepDist {
			stepLen = stepDist
		}
		if horizDist-stepLen < stopDist {
			stepLen = math.Max(0, horizDist-stopDist)
		}
		if stepLen > 0 {
			candX := x + (stepDX/stepDist)*stepLen
			candZ := z + (stepDZ/stepDist)*stepLen
			if nx, ny, nz, ok := tryApplyHorizontalStep(chunks, x, feetY, z, candX, candZ); ok {
				nextX, nextY, nextZ = nx, float64(ny), nz
			} else {
				mem.repathCooldown = 0
			}
		}
	}
	return nextX, nextY, nextZ, nextYaw
}

// stepHostileSkeletonAI advances a skeleton by one tick: chases players up to
// bow range (8..15 blocks) and fires ranged arrow attacks on a 40-tick cooldown.
func stepHostileSkeletonAI(
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

	target, found := selectTargetWithinRange(targets, x, float64(feetY), z, SkeletonAggroRange)
	if !found {
		if mem.state == mobStateChase {
			mem.state = mobStateIdle
			mem.stateTicks = 20
			mem.waypoints = mem.waypoints[:0]
			mem.waypointIdx = 0
		}
		nextX, nextY, nextZ, nextYaw = stepWanderAI(chunks, rng, mem, x, feetY, z, yaw, SkeletonWanderSpeed)
		return nextX, nextY, nextZ, nextYaw, nil
	}

	mem.state = mobStateChase
	nextX, nextY, nextZ, nextYaw = stepHostileChaseToward(
		chunks, mem, x, feetY, z, yaw,
		target.x, target.y, target.z,
		SkeletonChaseSpeed, SkeletonIdealStopDist,
	)

	dist3D := math.Sqrt((target.x-nextX)*(target.x-nextX) + (target.y-nextY)*(target.y-nextY) + (target.z-nextZ)*(target.z-nextZ))
	if dist3D <= SkeletonBowRange && mem.attackCooldown <= 0 {
		shootTarget = target.session
		mem.attackCooldown = SkeletonShootCooldownTicks
	}
	return nextX, nextY, nextZ, nextYaw, shootTarget
}

// ShootSkeletonArrowAtPlayer fires a skeleton arrow at target, broadcasting the
// bow shoot sound and applying ranged arrow damage and knockback.
func (s *Server) ShootSkeletonArrowAtPlayer(skeletonID int32, skelX, skelY, skelZ float64, target *PlayerSession) float32 {
	if s == nil || target == nil || target.IsDead() {
		return 0
	}
	cx := int32(int64(math.Floor(skelX)) >> 4)
	cz := int32(int64(math.Floor(skelZ)) >> 4)
	s.BroadcastChunk(cx, cz, PlaySoundPacketID,
		EncodeSoundPacket("minecraft:entity.skeleton.shoot", SoundSourceHostile, skelX, skelY, skelZ, 1.0, 1.0, 0))
	return s.AttackPlayerFromMob(skeletonID, MobTypeSkeleton, skelX, skelZ, target, SkeletonArrowBaseDamage)
}

// stepHostileCreeperAI advances a creeper by one tick: chases players within
// 32 blocks, starts its 30-tick fuse when within 3 blocks (stopping movement
// while swelling), defuses if the player moves past 7 blocks, and explodes when
// the fuse reaches 30 ticks.
func stepHostileCreeperAI(
	chunks *world.Cache,
	rng *rand.Rand,
	mem *mobMemory,
	targets []playerTarget,
	x float64,
	feetY int,
	z float64,
	yaw float32,
) (nextX, nextY, nextZ float64, nextYaw float32, swellChanged bool, exploded bool) {
	if mem.repathCooldown > 0 {
		mem.repathCooldown--
	}

	if mem.creeperIgnited {
		if mem.creeperSwellDir != 1 {
			mem.creeperSwellDir = 1
			swellChanged = true
		}
		mem.creeperFuseTicks++
		if mem.creeperFuseTicks >= CreeperFuseDurationTicks {
			return x, float64(feetY), z, yaw, swellChanged, true
		}
		return x, float64(feetY), z, yaw, swellChanged, false
	}

	target, found := selectTargetWithinRange(targets, x, float64(feetY), z, CreeperAggroRange)
	if !found {
		if mem.creeperFuseTicks > 0 {
			if mem.creeperSwellDir != -1 {
				mem.creeperSwellDir = -1
				swellChanged = true
			}
			mem.creeperFuseTicks--
			if mem.creeperFuseTicks <= 0 {
				mem.creeperFuseTicks = 0
				mem.creeperSwellDir = 0
				swellChanged = true
			}
		}
		if mem.state == mobStateChase {
			mem.state = mobStateIdle
			mem.stateTicks = 20
			mem.waypoints = mem.waypoints[:0]
			mem.waypointIdx = 0
		}
		nextX, nextY, nextZ, nextYaw = stepWanderAI(chunks, rng, mem, x, feetY, z, yaw, CreeperWanderSpeed)
		return nextX, nextY, nextZ, nextYaw, swellChanged, false
	}

	mem.state = mobStateChase
	dx := target.x - x
	dy := target.y - float64(feetY)
	dz := target.z - z
	dist := math.Sqrt(dx*dx + dy*dy + dz*dz)

	nextYaw = yawToward(dx, dz)
	if math.IsNaN(float64(nextYaw)) {
		nextYaw = yaw
	}

	// Determine swell direction: ignite at <= 3.0 blocks, continue swelling while <= 7.0 blocks.
	if dist <= CreeperIgniteRange || (mem.creeperSwellDir > 0 && dist <= CreeperCancelRange) {
		if mem.creeperSwellDir != 1 {
			mem.creeperSwellDir = 1
			swellChanged = true
		}
		mem.creeperFuseTicks++
		if mem.creeperFuseTicks >= CreeperFuseDurationTicks {
			return x, float64(feetY), z, nextYaw, swellChanged, true
		}
		// Creepers stand still while actively swelling.
		return x, float64(feetY), z, nextYaw, swellChanged, false
	}

	// Target is beyond ignite/cancel range: defuse if fuse was partially charged.
	if mem.creeperFuseTicks > 0 {
		if mem.creeperSwellDir != -1 {
			mem.creeperSwellDir = -1
			swellChanged = true
		}
		mem.creeperFuseTicks--
		if mem.creeperFuseTicks <= 0 {
			mem.creeperFuseTicks = 0
			mem.creeperSwellDir = 0
			swellChanged = true
		}
	}

	nextX, nextY, nextZ, nextYaw = stepHostileChaseToward(
		chunks, mem, x, feetY, z, yaw,
		target.x, target.y, target.z,
		CreeperChaseSpeed, 1.5,
	)
	return nextX, nextY, nextZ, nextYaw, swellChanged, false
}

// ComputeCreeperExplosionDamage calculates the base (Normal difficulty)
// explosion damage at distance dist from an explosion of radius, matching
// vanilla's linear falloff formula inside 2*radius.
func ComputeCreeperExplosionDamage(dist, radius float64) float32 {
	maxDiameter := radius * 2.0
	if maxDiameter <= 0 || dist >= maxDiameter {
		return 0
	}
	impact := 1.0 - (dist / maxDiameter)
	if impact <= 0 {
		return 0
	}
	raw := (impact*impact + impact) / 2.0 * 7.0 * maxDiameter + 1.0
	if raw > float64(CreeperMaxBaseDamage) {
		raw = float64(CreeperMaxBaseDamage)
	}
	return float32(raw)
}

var creeperChargedMap sync.Map // map[int32]bool

// SetCreeperCharged sets whether the creeper with entityID is charged (explosion radius 6.0 instead of 3.0)
// and broadcasts Creeper.DATA_IS_POWERED (index 18) to nearby viewers.
func (s *Server) SetCreeperCharged(entityID int32, charged bool) {
	if entityID <= 0 {
		return
	}
	if charged {
		creeperChargedMap.Store(entityID, true)
	} else {
		creeperChargedMap.Delete(entityID)
	}
	if s != nil && s.entities != nil {
		if ent := s.entities.Get(entityID); ent != nil && canonicalResourceName(ent.TypeName) == MobTypeCreeper {
			_, swellDir := s.CreeperFuseState(entityID)
			ignited := s.IsCreeperIgnited(entityID)
			cp := ent.ChunkPos()
			s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, protocol.PlaySetEntityData,
				EncodeCreeperEntityDataPacket(entityID, int32(swellDir), charged, ignited))
		}
	}
}

// IsCreeperCharged reports whether the creeper with entityID is charged.
func (s *Server) IsCreeperCharged(entityID int32) bool {
	if entityID <= 0 {
		return false
	}
	v, ok := creeperChargedMap.Load(entityID)
	return ok && v == true
}

// ExplodeMob executes a mob-caused explosion at (x, y, z) in dimension with the
// given radius, createFire flag, and mobGriefing flag, delegating to DetonateExplosion.
func (s *Server) ExplodeMob(dimension string, x, y, z, radius float64, createFire, mobGriefing bool, attackerEntityID int32) ExplosionResult {
	if s == nil || radius <= 0 {
		return ExplosionResult{}
	}
	normDim := world.NormalizeDimension(dimension)
	sourceType := ""
	if s.entities != nil && attackerEntityID > 0 {
		if ent := s.entities.Get(attackerEntityID); ent != nil {
			sourceType = ent.TypeName
		}
	}
	cause := "explosion"
	if sourceType != "" {
		cause = mobAttackCauseName(sourceType)
	}
	breakBlocks := mobGriefing
	return s.DetonateExplosion(ExplosionOptions{
		Dimension:      normDim,
		X:              x,
		Y:              y,
		Z:              z,
		Radius:         radius,
		Category:       ExplosionSourceMob,
		DamageCause:    cause,
		SourceEntityID: attackerEntityID,
		SourceMobType:  sourceType,
		CausesFire:     createFire,
		BreakBlocks:    &breakBlocks,
	})
}

// DetonateGhastFireball detonates a Ghast fireball at (x, y, z) in dimension
// with vanilla radius 1.0 and createFire=true via ExplodeMob.
func (s *Server) DetonateGhastFireball(dimension string, x, y, z float64, fireballEntityID int32) ExplosionResult {
	if s == nil {
		return ExplosionResult{}
	}
	return s.ExplodeMob(dimension, x, y, z, GhastFireballExplosionRadius, true, s.MobGriefing() && s.CreeperBlockGriefing(), fireballEntityID)
}

// TriggerCreeperExplosion detonates the creeper with entityID at (x, y, z):
// removes the creeper entity and delegates to s.ExplodeMob (radius 3.0 normal,
// 6.0 if charged) for raycast shielding, blast damage/knockback, and block drops.
func (s *Server) TriggerCreeperExplosion(entityID int32, x, y, z float64) (playersDamaged int) {
	if s == nil {
		return 0
	}
	cx := int32(int64(math.Floor(x)) >> 4)
	cz := int32(int64(math.Floor(z)) >> 4)

	radius := CreeperExplosionRadius
	wasCharged := s.IsCreeperCharged(entityID)
	if wasCharged {
		radius = ChargedCreeperExplosionRadius
	}
	s.SetCreeperCharged(entityID, false)

	dim := world.DimensionOverworld
	if s.entities != nil && entityID > 0 {
		if ent := s.entities.Get(entityID); ent != nil {
			dim = ent.EffectiveDimension()
		}
		s.entities.Remove(entityID)
		s.BroadcastChunk(cx, cz, protocol.PlayRemoveEntities, encodeRemoveEntitiesPacket(nil, []int32{entityID}))
	}

	type preMobSnap struct {
		id       int32
		typeName string
		x, y, z  float64
	}
	var preMobs []preMobSnap
	if s.entities != nil {
		maxDiam := radius * 2.0
		for _, m := range s.entities.All() {
			if m.ID == entityID || m.IsItem() || m.IsExperienceOrb() || m.EffectiveDimension() != dim {
				continue
			}
			if s.MobHealth(m.ID) <= 0 {
				continue
			}
			if math.Hypot(m.X-x, m.Z-z) <= maxDiam+2.0 {
				preMobs = append(preMobs, preMobSnap{id: m.ID, typeName: m.TypeName, x: m.X, y: m.Y, z: m.Z})
			}
		}
	}

	canGrief := s.MobGriefing() && s.CreeperBlockGriefing()
	res := s.DetonateExplosion(ExplosionOptions{
		Dimension:      dim,
		X:              x,
		Y:              y,
		Z:              z,
		Radius:         radius,
		Category:       ExplosionSourceMob,
		DamageCause:    "creeper",
		SourceEntityID: entityID,
		SourceMobType:  MobTypeCreeper,
		CausesFire:     false,
		BreakBlocks:    &canGrief,
	})

	if s.entities != nil {
		for _, pm := range preMobs {
			if s.MobHealth(pm.id) <= 0 {
				s.entities.Remove(pm.id)
				mcpX := int32(int64(math.Floor(pm.x)) >> 4)
				mcpZ := int32(int64(math.Floor(pm.z)) >> 4)
				s.BroadcastDimensionChunk(dim, mcpX, mcpZ, protocol.PlayRemoveEntities,
					encodeRemoveEntitiesPacket(nil, []int32{pm.id}))
				if wasCharged && s.DoMobLoot() {
					if skullID, ok := CreeperHeadDropForVictim(pm.typeName); ok && skullID != "" {
						if stack := world.NewNamedItemStack(skullID, 1); !stack.IsEmpty() {
							_, _ = s.SpawnItemEntityInDimension(dim, pm.x, pm.y+0.25, pm.z, stack)
						}
					}
				}
			}
		}
	}
	return res.PlayersDamaged
}

// stepHostileSpiderAI advances a spider by one tick: neutral in bright daylight
// unless provoked, hostile in darkness/night (light <= 11), leaps toward target
// at 2..4 blocks, and bites within melee reach.
func (s *Server) stepHostileSpiderAI(
	chunks *world.Cache,
	rng *rand.Rand,
	mem *mobMemory,
	targets []playerTarget,
	x float64,
	feetY int,
	z float64,
	yaw float32,
) (nextX, nextY, nextZ float64, nextYaw float32, attackTarget *PlayerSession, leaped bool) {
	if mem.attackCooldown > 0 {
		mem.attackCooldown--
	}
	if mem.leapCooldown > 0 {
		mem.leapCooldown--
	}
	if mem.repathCooldown > 0 {
		mem.repathCooldown--
	}

	if !s.IsSpiderHostile(x, feetY, z, mem.provoked) {
		if mem.state == mobStateChase {
			mem.state = mobStateIdle
			mem.stateTicks = 20
			mem.waypoints = mem.waypoints[:0]
			mem.waypointIdx = 0
		}
		nextX, nextY, nextZ, nextYaw = stepWanderAI(chunks, rng, mem, x, feetY, z, yaw, SpiderWanderSpeed)
		return nextX, nextY, nextZ, nextYaw, nil, false
	}

	target, found := selectTargetWithinRange(targets, x, float64(feetY), z, SpiderAggroRange)
	if !found {
		if mem.state == mobStateChase {
			mem.state = mobStateIdle
			mem.stateTicks = 20
			mem.waypoints = mem.waypoints[:0]
			mem.waypointIdx = 0
		}
		nextX, nextY, nextZ, nextYaw = stepWanderAI(chunks, rng, mem, x, feetY, z, yaw, SpiderWanderSpeed)
		return nextX, nextY, nextZ, nextYaw, nil, false
	}

	mem.state = mobStateChase
	dx := target.x - x
	dz := target.z - z
	horizDist := math.Hypot(dx, dz)

	// Spider leap/pounce when 2..4 blocks away and leapCooldown == 0.
	speed := SpiderChaseSpeed
	if horizDist >= SpiderLeapMinDist && horizDist <= SpiderLeapMaxDist && mem.leapCooldown <= 0 {
		speed = SpiderLeapSpeed
		mem.leapCooldown = SpiderLeapCooldownTicks
		leaped = true
	}

	nextX, nextY, nextZ, nextYaw = stepHostileChaseToward(
		chunks, mem, x, feetY, z, yaw,
		target.x, target.y, target.z,
		speed, SpiderAttackStopDist,
	)

	finalDX := target.x - nextX
	finalDY := math.Abs(target.y - nextY)
	finalDZ := target.z - nextZ
	finalHoriz := math.Hypot(finalDX, finalDZ)
	if finalHoriz <= SpiderAttackReach && finalDY <= SpiderAttackVerticalReach && mem.attackCooldown <= 0 {
		attackTarget = target.session
		mem.attackCooldown = SpiderAttackCooldownTicks
	}
	return nextX, nextY, nextZ, nextYaw, attackTarget, leaped
}

// stepPassiveAnimalAI advances a passive animal (cow, sheep, chicken, pig) or
// villager by one tick: handles panic fleeing when hurt, food temptation toward
// nearby players, sheep wool regrowth, chicken egg laying, and idle wandering.
func (s *Server) stepPassiveAnimalAI(
	entityID int32,
	typeName string,
	chunks *world.Cache,
	rng *rand.Rand,
	mem *mobMemory,
	targets []playerTarget,
	x float64,
	feetY int,
	z float64,
	yaw float32,
) (nextX, nextY, nextZ float64, nextYaw float32) {
	if mem.repathCooldown > 0 {
		mem.repathCooldown--
	}
	if mem.breedCooldown > 0 {
		mem.breedCooldown--
	}
	if mem.inLoveTicks > 0 {
		mem.inLoveTicks--
	}

	norm := canonicalResourceName(typeName)
	baseSpeed := animalWanderSpeed(norm)

	// Chicken egg-laying timer.
	if norm == MobTypeChicken {
		if mem.eggTimer <= 0 {
			mem.eggTimer = ChickenEggMinTicks
			if rng != nil {
				mem.eggTimer += rng.Intn(ChickenEggMaxTicks - ChickenEggMinTicks + 1)
			}
		}
		mem.eggTimer--
		if mem.eggTimer <= 0 {
			if rng != nil {
				mem.eggTimer = ChickenEggMinTicks + rng.Intn(ChickenEggMaxTicks-ChickenEggMinTicks+1)
			} else {
				mem.eggTimer = ChickenEggMinTicks
			}
			if s != nil {
				eggStack := world.NewNamedItemStack("minecraft:egg", 1)
				if !eggStack.IsEmpty() {
					s.SpawnItemEntity(x, float64(feetY)+0.25, z, eggStack)
					cx := int32(int64(math.Floor(x)) >> 4)
					cz := int32(int64(math.Floor(z)) >> 4)
					s.BroadcastChunk(cx, cz, PlaySoundPacketID,
						EncodeSoundPacket("minecraft:entity.chicken.egg", SoundSourceNeutral, x, float64(feetY), z, 1.0, 1.0, 0))
				}
			}
		}
	}

	// Sheep wool regrowth timer when standing on grass.
	if norm == MobTypeSheep && mem.sheared {
		mem.woolRegrowTimer++
		if mem.woolRegrowTimer >= SheepWoolRegrowTicks {
			mem.sheared = false
			mem.woolRegrowTimer = 0
			if s != nil {
				cx := int32(int64(math.Floor(x)) >> 4)
				cz := int32(int64(math.Floor(z)) >> 4)
				s.BroadcastChunk(cx, cz, PlaySetEntityDataCB, EncodeSheepWoolDataPacket(entityID, 0, false))
			}
		}
	}

	// 1. Panic flee state when recently hurt.
	if mem.panicTicks > 0 {
		mem.panicTicks--
		return stepWanderAI(chunks, rng, mem, x, feetY, z, yaw, baseSpeed*AnimalPanicSpeedMult)
	}

	// 2. Temptation: follow nearest player within AnimalTemptRange holding breeding food.
	if IsPassiveAnimal(norm) {
		if temptTarget, ok := findTemptingPlayer(targets, norm, x, float64(feetY), z); ok {
			return stepHostileChaseToward(
				chunks, mem, x, feetY, z, yaw,
				temptTarget.x, temptTarget.y, temptTarget.z,
				baseSpeed*1.1, AnimalTemptStopDist,
			)
		}
	}

	// 3. Default passive wander.
	return stepWanderAI(chunks, rng, mem, x, feetY, z, yaw, baseSpeed)
}

func findTemptingPlayer(targets []playerTarget, animalType string, x, y, z float64) (playerTarget, bool) {
	maxDistSq := AnimalTemptRange * AnimalTemptRange
	bestIdx := -1
	bestDistSq := maxDistSq
	for i := range targets {
		pt := &targets[i]
		if pt.session == nil || pt.health <= 0 || pt.gameMode == GameModeSpectator || pt.session.IsDead() {
			continue
		}
		held := pt.session.HeldItem()
		if held.IsEmpty() || !IsAnimalBreedingFood(animalType, held.ResolvedID()) {
			continue
		}
		dx := pt.x - x
		dy := pt.y - y
		dz := pt.z - z
		distSq := dx*dx + dy*dy + dz*dz
		if distSq <= bestDistSq {
			bestIdx = i
			bestDistSq = distSq
		}
	}
	if bestIdx >= 0 {
		return targets[bestIdx], true
	}
	return playerTarget{}, false
}

// InteractWithAnimal handles right-clicking a passive farm animal (cow, sheep,
// pig, chicken):
//   - Cow + bucket -> fills milk_bucket
//   - Sheep + shears -> shears 1..3 white_wool
//   - Animal + breeding food -> enters love mode (600 ticks), and if another
//     same-species animal nearby is also in love, spawns an offspring!
func (s *Server) InteractWithAnimal(session *PlayerSession, entityID int32) (handled bool, offspringID int32) {
	if s == nil || s.entities == nil || session == nil || session.IsDead() {
		return false, 0
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator || snap.Health <= 0 {
		return false, 0
	}
	ent := s.entities.Get(entityID)
	if ent == nil || !IsPassiveAnimal(ent.TypeName) {
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
	heldID := canonicalResourceName(held.ResolvedID())
	mobType := canonicalResourceName(ent.TypeName)
	cp := ent.ChunkPos()

	// 1. Cow milking with an empty bucket.
	if mobType == MobTypeCow && heldID == "minecraft:bucket" {
		milk := world.NewNamedItemStack("minecraft:milk_bucket", 1)
		if !milk.IsEmpty() {
			slotIdx := world.HotbarContainerOffset + int(snap.SelectedSlot)
			if snap.GameMode != GameModeCreative {
				if held.Count <= 1 {
					session.SetInventorySlot(slotIdx, milk)
				} else {
					session.SetInventorySlot(slotIdx, world.NewItemStack(held.ResolvedItemID(), held.Count-1))
					_, _ = session.AddItemToInventory(milk)
				}
			} else {
				_, _ = session.AddItemToInventory(milk)
			}
			s.BroadcastChunk(cp.X, cp.Z, PlaySoundPacketID,
				EncodeSoundPacket("minecraft:entity.cow.milk", SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
			return true, 0
		}
	}

	// 2. Sheep shearing with shears.
	if mobType == MobTypeSheep && heldID == "minecraft:shears" {
		tr := s.entityTracker()
		tr.mu.Lock()
		mem := tr.mobStateFor(entityID, tr.aiTickID)
		alreadySheared := mem.sheared
		if !alreadySheared {
			mem.sheared = true
			mem.woolRegrowTimer = 0
		}
		tr.mu.Unlock()
		if !alreadySheared {
			wool := world.NewNamedItemStack("minecraft:white_wool", 2)
			if !wool.IsEmpty() {
				s.SpawnItemEntity(ent.X, ent.Y+0.5, ent.Z, wool)
			}
			s.BroadcastChunk(cp.X, cp.Z, PlaySetEntityDataCB, EncodeSheepWoolDataPacket(entityID, 0, true))
			s.BroadcastChunk(cp.X, cp.Z, PlaySoundPacketID,
				EncodeSoundPacket("minecraft:entity.sheep.shear", SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
			return true, 0
		}
		return false, 0
	}

	// 3. Breeding food -> feed baby to speed up growth by 10%, or enter love mode for adults.
	if !IsAnimalBreedingFood(mobType, heldID) {
		return false, 0
	}
	if s.IsMobBaby(entityID) {
		fed, _ := s.FeedBabyMob(session, entityID, heldID)
		return fed, 0
	}

	tr := s.entityTracker()
	tr.mu.Lock()
	mem := tr.mobStateFor(entityID, tr.aiTickID)
	if mem.ageTicks < 0 || mem.breedCooldown > 0 || mem.inLoveTicks > 0 {
		tr.mu.Unlock()
		return false, 0
	}
	mem.inLoveTicks = AnimalLoveDurationTicks
	mem.loveCause = session

	// Search for a nearby adult mate of the same species that is also in love.
	var mateID int32
	var mateX, mateY, mateZ float64
	allEnts := s.entities.AppendAll(nil)
	for i := range allEnts {
		other := &allEnts[i]
		if other.ID == entityID || canonicalResourceName(other.TypeName) != mobType {
			continue
		}
		odx := other.X - ent.X
		ody := other.Y - ent.Y
		odz := other.Z - ent.Z
		if odx*odx+ody*ody+odz*odz > AnimalBreedRange*AnimalBreedRange {
			continue
		}
		otherMem := tr.mobStates[other.ID]
		if otherMem != nil && otherMem.ageTicks >= 0 && otherMem.inLoveTicks > 0 && otherMem.breedCooldown <= 0 {
			mateID = other.ID
			mateX, mateY, mateZ = other.X, other.Y, other.Z
			otherMem.inLoveTicks = 0
			otherMem.breedCooldown = AnimalBreedCooldownTicks
			otherMem.loveCause = nil
			mem.inLoveTicks = 0
			mem.breedCooldown = AnimalBreedCooldownTicks
			mem.loveCause = nil
			break
		}
	}
	tr.mu.Unlock()

	if snap.GameMode != GameModeCreative {
		slotIdx := world.HotbarContainerOffset + int(snap.SelectedSlot)
		rem := held.Count - 1
		if rem <= 0 {
			session.SetInventorySlot(slotIdx, world.ItemStack{})
		} else {
			session.SetInventorySlot(slotIdx, world.NewItemStack(held.ResolvedItemID(), rem))
		}
	}

	dim := ent.EffectiveDimension()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayEntityEvent, EncodeEntityEventPacket(entityID, EntityEventLoveHearts))
	if mateID > 0 {
		midX := (ent.X + mateX) * 0.5
		midY := math.Max(ent.Y, mateY)
		midZ := (ent.Z + mateZ) * 0.5
		offspringID, _ = s.SpawnBabyMob(dim, midX, midY, midZ, mobType)
		s.GrantCriterion(session, "minecraft:husbandry/breed_an_animal", "bred")
	}
	return true, offspringID
}

// IsFireImmuneMob reports whether a mob type is naturally immune to fire and
// campfire contact damage in vanilla 26.1.2.
func IsFireImmuneMob(typeName string) bool {
	switch canonicalResourceName(typeName) {
	case MobTypeWither, MobTypeWitherSkeleton, MobTypeEnderDragon, MobTypeWarden,
		MobTypeMagmaCube, "minecraft:blaze", "minecraft:ghast", "minecraft:strider",
		"minecraft:zombified_piglin":
		return true
	default:
		return false
	}
}

// FireDamageAtPosition checks the block footprint of an entity AABB centered at
// (x, z) from y to y+height in dimension and returns the highest fire contact
// damage (1.0 for `minecraft:fire`, 2.0 for `minecraft:soul_fire`) when gamerule
// fireDamage is enabled.
func (s *Server) FireDamageAtPosition(dimension string, x, y, z, width, height float64) (float32, bool) {
	if s == nil || !s.FireDamage() {
		return 0, false
	}
	normDim := world.NormalizeDimension(dimension)
	cache := s.DimensionChunks(normDim)
	if cache == nil {
		return 0, false
	}
	if width <= 0 {
		width = 0.6
	}
	if height <= 0 {
		height = 1.8
	}
	half := width * 0.5
	minX := int(math.Floor(x - half + 1e-4))
	maxX := int(math.Floor(x + half - 1e-4))
	minY := int(math.Floor(y + 1e-4))
	maxY := int(math.Floor(y + height - 1e-4))
	minZ := int(math.Floor(z - half + 1e-4))
	maxZ := int(math.Floor(z + half - 1e-4))

	var best float32
	for by := minY; by <= maxY; by++ {
		if by < world.MinY || by >= world.MinY+world.WorldHeight {
			continue
		}
		for bx := minX; bx <= maxX; bx++ {
			for bz := minZ; bz <= maxZ; bz++ {
				if dmg := world.FireDamageForBlock(cache.GetBlock(bx, by, bz)); dmg > best {
					best = dmg
				}
			}
		}
	}
	return best, best > 0
}

// BoatBubbleColumnSinkTicks is the number of ticks (60 = 3 seconds) a boat or
// raft rocks/wobbles on the surface of a bubble column before sinking.
const BoatBubbleColumnSinkTicks = 60

var boatBubbleColumnTicks sync.Map // map[int32]int

func isBoatOrRaftEntityType(typeName string) bool {
	norm := canonicalResourceName(typeName)
	return strings.HasSuffix(norm, "_boat") || strings.HasSuffix(norm, "_raft")
}

// EntitySubmergedFluidDepth computes the fluid height (`cache.FluidHeightAt`)
// at `(x, y, z)` in `dimension` and returns the immersion depth (`surfaceY - y`),
// the block's fluid height (`0..1`), the `world.FluidKind`, and whether the
// entity's feet are submerged (`depth > 0`).
func (s *Server) EntitySubmergedFluidDepth(dimension string, x, y, z float64) (submergedDepth float64, fluidHeight float32, kind world.FluidKind, submerged bool) {
	if s == nil {
		return 0, 0, world.FluidNone, false
	}
	normDim := world.NormalizeDimension(dimension)
	cache := s.DimensionChunks(normDim)
	if cache == nil {
		return 0, 0, world.FluidNone, false
	}
	bx := int(math.Floor(x))
	by := int(math.Floor(y))
	bz := int(math.Floor(z))
	fh := cache.FluidHeightAt(bx, by, bz)
	if fh <= 0 {
		return 0, 0, world.FluidNone, false
	}
	k, _, _, _, _, ok := world.ClassifyFluidState(cache.GetBlock(bx, by, bz))
	if !ok {
		return 0, 0, world.FluidNone, false
	}
	surfaceY := float64(by) + float64(fh)
	depth := surfaceY - y
	if depth <= 0 {
		return 0, fh, k, false
	}
	return depth, fh, k, true
}

// StepEntityFluidAndBubbleColumn applies `cache.BubbleColumnVelocityAt` and
// `cache.FluidHeightAt` physics to a mob, item, or boat/raft entity:
//   - Mobs and items inside or directly above a `minecraft:bubble_column` receive
//     the vanilla upward/whirlpool vertical velocity from `BubbleColumnVelocityAt`.
//   - Boats/rafts on the surface of a `minecraft:bubble_column` wobble for
//     `BoatBubbleColumnSinkTicks` (60 ticks) and then sink (`boatSunk = true`).
func (s *Server) StepEntityFluidAndBubbleColumn(entityID int32) (vx, vy, vz float64, inBubbleColumn bool, boatSunk bool) {
	if s == nil || s.entities == nil || entityID <= 0 {
		return 0, 0, 0, false, false
	}
	ent := s.entities.Get(entityID)
	if ent == nil {
		return 0, 0, 0, false, false
	}
	dim := ent.EffectiveDimension()
	cache := s.DimensionChunks(dim)
	if cache == nil {
		return 0, 0, 0, false, false
	}
	bx := int(math.Floor(ent.X))
	by := int(math.Floor(ent.Y))
	bz := int(math.Floor(ent.Z))
	yBelow := int(math.Floor(ent.Y - 0.1))

	curVX := float64(ent.VelocityX) / 8000.0
	curVY := float64(ent.VelocityY) / 8000.0
	curVZ := float64(ent.VelocityZ) / 8000.0

	colY := by
	nvx, nvy, nvz, ok := cache.BubbleColumnVelocityAt(bx, by, bz, curVX, curVY, curVZ)
	if !ok && yBelow != by {
		nvx, nvy, nvz, ok = cache.BubbleColumnVelocityAt(bx, yBelow, bz, curVX, curVY, curVZ)
		if ok {
			colY = yBelow
		}
	}

	isBoat := isBoatOrRaftEntityType(ent.TypeName)
	if ok {
		if isBoat {
			onSurface := colY+1 >= world.MinY+world.WorldHeight || cache.GetBlock(bx, colY+1, bz) == world.StateAir
			if onSurface {
				ticks := 1
				if prev, loaded := boatBubbleColumnTicks.Load(entityID); loaded {
					ticks = prev.(int) + 1
				}
				boatBubbleColumnTicks.Store(entityID, ticks)
				if ticks < BoatBubbleColumnSinkTicks {
					wobblePitch := float32(math.Sin(float64(ticks)*0.5) * 6.0)
					s.entities.Update(entityID, func(e *world.Entity) {
						e.Pitch = wobblePitch
					})
					return curVX, 0, curVZ, true, false
				}
				// After 60 ticks of rocking on a bubble column, the boat sinks!
				boatBubbleColumnTicks.Delete(entityID)
				if nvy > -0.7 {
					nvy = -0.7
				}
				s.entities.Update(entityID, func(e *world.Entity) {
					e.Pitch = 0
					e.VelocityX = clampVelocityShort(nvx)
					e.VelocityY = clampVelocityShort(nvy)
					e.VelocityZ = clampVelocityShort(nvz)
					e.Y += nvy
					e.OnGround = false
				})
				cp := ent.ChunkPos()
				s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlaySetEntityMotion,
					EncodeSetEntityMotionPacket(entityID, nvx, nvy, nvz))
				return nvx, nvy, nvz, true, true
			}
		}

		s.entities.Update(entityID, func(e *world.Entity) {
			e.VelocityX = clampVelocityShort(nvx)
			e.VelocityY = clampVelocityShort(nvy)
			e.VelocityZ = clampVelocityShort(nvz)
			e.Y += nvy
			e.OnGround = false
		})
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlaySetEntityMotion,
			EncodeSetEntityMotionPacket(entityID, nvx, nvy, nvz))
		return nvx, nvy, nvz, true, false
	}

	if isBoat {
		boatBubbleColumnTicks.Delete(entityID)
	}
	return curVX, curVY, curVZ, false, false
}

// TickMobsInCauldrons checks all mob/item/boat entities each tick and invokes:
//   - s.StepEntityFluidAndBubbleColumn(mob.ID) for bubble_column velocity and boat wobble/sinking
//   - s.HandleEntityInsideCauldron(dim, mob.ID, bx, by, bz, s.IsEntityOnFire(mob.ID))
//   - s.OnEntityStepTripwire(dim, bx, by, bz)
//   - s.CampfireContactDamageAt(dim, bx, by, bz) and s.FireDamageAtPosition for living non-fire-immune mobs
func (s *Server) TickMobsInCauldrons() {
	if s == nil || s.entities == nil {
		return
	}
	allEnts := s.entities.AppendAll(nil)
	for i := range allEnts {
		mob := &allEnts[i]
		if mob.IsItem() || isBoatOrRaftEntityType(mob.TypeName) {
			s.StepEntityFluidAndBubbleColumn(mob.ID)
			continue
		}
		if mob.IsExperienceOrb() || mob.TypeName == EntityTypeFishingBobber || IsDecorationEntity(mob.TypeName) ||
			IsStandardProjectileEntityType(mob.TypeName) || IsWindChargeEntityType(mob.TypeName) ||
			mob.TypeName == EntityTypeLlamaSpit || mob.TypeName == EntityTypeLightning || IsVehicleEntity(mob.TypeName) {
			continue
		}
		s.StepEntityFluidAndBubbleColumn(mob.ID)
		dim := mob.EffectiveDimension()
		cache := s.DimensionChunks(dim)
		if cache == nil {
			continue
		}
		bx := int(math.Floor(mob.X))
		by := int(math.Floor(mob.Y))
		bz := int(math.Floor(mob.Z))
		if by < world.MinY || by >= world.MinY+world.WorldHeight {
			continue
		}
		blk := cache.GetBlock(bx, by, bz)
		if IsCauldronBlock(blk) {
			s.HandleEntityInsideCauldron(dim, mob.ID, bx, by, bz, s.IsEntityOnFire(mob.ID))
		}
		if world.BlockName(blk) == "minecraft:tripwire" {
			s.OnEntityStepTripwire(dim, bx, by, bz)
		}
		if !IsFireImmuneMob(mob.TypeName) && !s.HasEntityEffect(mob.ID, EffectFireResistance) {
			if dmg, ok := s.CampfireContactDamageAt(dim, bx, by, bz); ok && dmg > 0 {
				s.applyDamageToMob(mob, dmg, nil, 0, false, false)
			} else if fDmg, fOK := s.FireDamageAtPosition(dim, mob.X, mob.Y, mob.Z, 0.6, 1.8); fOK && fDmg > 0 {
				s.applyDamageToMob(mob, fDmg, nil, 0, false, false)
			}
		}
	}
}

func mobAttackCauseName(mobType string) string {
	short := strings.TrimPrefix(canonicalResourceName(mobType), "minecraft:")
	if short == "" {
		return "mob"
	}
	return short
}

