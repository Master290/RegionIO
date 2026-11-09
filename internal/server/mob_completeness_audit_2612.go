package server

import (
	"math"
	"math/rand"
	"strings"

	"regionio/internal/nbt"
	"regionio/internal/world"
)

// Additional entity type constants for complete 26.1.2 coverage.
const (
	MobTypePolarBear    = "minecraft:polar_bear"
	MobTypeGiant        = "minecraft:giant"
	EntityTypeMannequin = "minecraft:mannequin"

	EntityTypeGiantID     int = 59
	EntityTypeMannequinID int = 83
	EntityTypePolarBearID int = 104

	DefaultPolarBearMaxHealth float32 = 30.0
	DefaultGiantMaxHealth     float32 = 100.0
	DefaultMannequinMaxHealth float32 = 20.0

	// Air supply & powder snow freezing constants (`Entity.java` in 26.1.2.jar).
	DefaultMobMaxAirTicks         int = 300
	DolphinMaxAirTicks            int = 4800
	AxolotlMaxAirTicks            int = 6000
	PowderSnowRequiredFreezeTicks int = 140
)

// allEntityTypesOrdered2612 is the verbatim 0..156 registration order of
// `BuiltInRegistries.ENTITY_TYPE` extracted from `net/minecraft/world/entity/EntityType.class`
// in `26.1.2.jar` (protocol 775).
var allEntityTypesOrdered2612 = [157]string{
	"minecraft:acacia_boat",
	"minecraft:acacia_chest_boat",
	"minecraft:allay",
	"minecraft:area_effect_cloud",
	"minecraft:armadillo",
	"minecraft:armor_stand",
	"minecraft:arrow",
	"minecraft:axolotl",
	"minecraft:bamboo_chest_raft",
	"minecraft:bamboo_raft",
	"minecraft:bat",
	"minecraft:bee",
	"minecraft:birch_boat",
	"minecraft:birch_chest_boat",
	"minecraft:blaze",
	"minecraft:block_display",
	"minecraft:bogged",
	"minecraft:breeze",
	"minecraft:breeze_wind_charge",
	"minecraft:camel",
	"minecraft:camel_husk",
	"minecraft:cat",
	"minecraft:cave_spider",
	"minecraft:cherry_boat",
	"minecraft:cherry_chest_boat",
	"minecraft:chest_minecart",
	"minecraft:chicken",
	"minecraft:cod",
	"minecraft:copper_golem",
	"minecraft:command_block_minecart",
	"minecraft:cow",
	"minecraft:creaking",
	"minecraft:creeper",
	"minecraft:dark_oak_boat",
	"minecraft:dark_oak_chest_boat",
	"minecraft:dolphin",
	"minecraft:donkey",
	"minecraft:dragon_fireball",
	"minecraft:drowned",
	"minecraft:egg",
	"minecraft:elder_guardian",
	"minecraft:enderman",
	"minecraft:endermite",
	"minecraft:ender_dragon",
	"minecraft:ender_pearl",
	"minecraft:end_crystal",
	"minecraft:evoker",
	"minecraft:evoker_fangs",
	"minecraft:experience_bottle",
	"minecraft:experience_orb",
	"minecraft:eye_of_ender",
	"minecraft:falling_block",
	"minecraft:fireball",
	"minecraft:firework_rocket",
	"minecraft:fox",
	"minecraft:frog",
	"minecraft:furnace_minecart",
	"minecraft:ghast",
	"minecraft:happy_ghast",
	"minecraft:giant",
	"minecraft:glow_item_frame",
	"minecraft:glow_squid",
	"minecraft:goat",
	"minecraft:guardian",
	"minecraft:hoglin",
	"minecraft:hopper_minecart",
	"minecraft:horse",
	"minecraft:husk",
	"minecraft:illusioner",
	"minecraft:interaction",
	"minecraft:iron_golem",
	"minecraft:item",
	"minecraft:item_display",
	"minecraft:item_frame",
	"minecraft:jungle_boat",
	"minecraft:jungle_chest_boat",
	"minecraft:leash_knot",
	"minecraft:lightning_bolt",
	"minecraft:llama",
	"minecraft:llama_spit",
	"minecraft:magma_cube",
	"minecraft:mangrove_boat",
	"minecraft:mangrove_chest_boat",
	"minecraft:mannequin",
	"minecraft:marker",
	"minecraft:minecart",
	"minecraft:mooshroom",
	"minecraft:mule",
	"minecraft:nautilus",
	"minecraft:oak_boat",
	"minecraft:oak_chest_boat",
	"minecraft:ocelot",
	"minecraft:ominous_item_spawner",
	"minecraft:painting",
	"minecraft:pale_oak_boat",
	"minecraft:pale_oak_chest_boat",
	"minecraft:panda",
	"minecraft:parched",
	"minecraft:parrot",
	"minecraft:phantom",
	"minecraft:pig",
	"minecraft:piglin",
	"minecraft:piglin_brute",
	"minecraft:pillager",
	"minecraft:polar_bear",
	"minecraft:splash_potion",
	"minecraft:lingering_potion",
	"minecraft:pufferfish",
	"minecraft:rabbit",
	"minecraft:ravager",
	"minecraft:salmon",
	"minecraft:sheep",
	"minecraft:shulker",
	"minecraft:shulker_bullet",
	"minecraft:silverfish",
	"minecraft:skeleton",
	"minecraft:skeleton_horse",
	"minecraft:slime",
	"minecraft:small_fireball",
	"minecraft:sniffer",
	"minecraft:snowball",
	"minecraft:snow_golem",
	"minecraft:spawner_minecart",
	"minecraft:spectral_arrow",
	"minecraft:spider",
	"minecraft:spruce_boat",
	"minecraft:spruce_chest_boat",
	"minecraft:squid",
	"minecraft:stray",
	"minecraft:strider",
	"minecraft:tadpole",
	"minecraft:text_display",
	"minecraft:tnt",
	"minecraft:tnt_minecart",
	"minecraft:trader_llama",
	"minecraft:trident",
	"minecraft:tropical_fish",
	"minecraft:turtle",
	"minecraft:vex",
	"minecraft:villager",
	"minecraft:vindicator",
	"minecraft:wandering_trader",
	"minecraft:warden",
	"minecraft:wind_charge",
	"minecraft:witch",
	"minecraft:wither",
	"minecraft:wither_skeleton",
	"minecraft:wither_skull",
	"minecraft:wolf",
	"minecraft:zoglin",
	"minecraft:zombie",
	"minecraft:zombie_horse",
	"minecraft:zombie_nautilus",
	"minecraft:zombie_villager",
	"minecraft:zombified_piglin",
	"minecraft:player",
	"minecraft:fishing_bobber",
}

var entityTypeIDByNameMap2612 = func() map[string]int {
	m := make(map[string]int, len(allEntityTypesOrdered2612))
	for id, name := range allEntityTypesOrdered2612 {
		m[name] = id
	}
	return m
}()

// AllEntityTypesOrdered2612 returns a copy of all 157 protocol 775 `EntityType`
// identifiers in exact numeric ID order (`0..156`).
func AllEntityTypesOrdered2612() []string {
	out := make([]string, len(allEntityTypesOrdered2612))
	copy(out, allEntityTypesOrdered2612[:])
	return out
}

// EntityTypeRegistryIDByName2612 returns the protocol 775 numeric ID (`0..156`)
// for any of the 157 `EntityType` entries in `26.1.2.jar`.
func EntityTypeRegistryIDByName2612(typeName string) (int, bool) {
	id, ok := entityTypeIDByNameMap2612[canonicalResourceName(typeName)]
	return id, ok
}

// allLivingMobTypes2612 lists all 89 living mob entity types in `26.1.2.jar`
// (87 mobs with spawn eggs + `minecraft:giant` and `minecraft:illusioner`).
var allLivingMobTypes2612 = [89]string{
	MobTypeAllay,
	MobTypeArmadillo,
	MobTypeAxolotl,
	MobTypeBat,
	MobTypeBee,
	MobTypeBlaze,
	MobTypeBogged,
	MobTypeBreeze,
	MobTypeCamel,
	MobTypeCamelHusk,
	MobTypeCat,
	MobTypeCaveSpider,
	MobTypeChicken,
	MobTypeCod,
	MobTypeCopperGolem,
	MobTypeCow,
	MobTypeCreaking,
	MobTypeCreeper,
	MobTypeDolphin,
	MobTypeDonkey,
	MobTypeDrowned,
	MobTypeElderGuardian,
	MobTypeEnderDragon,
	MobTypeEnderman,
	MobTypeEndermite,
	MobTypeEvoker,
	MobTypeFox,
	MobTypeFrog,
	MobTypeGhast,
	MobTypeGiant,
	MobTypeGlowSquid,
	MobTypeGoat,
	MobTypeGuardian,
	MobTypeHappyGhast,
	MobTypeHoglin,
	MobTypeHorse,
	MobTypeHusk,
	MobTypeIllusioner,
	MobTypeIronGolem,
	MobTypeLlama,
	MobTypeMagmaCube,
	MobTypeMooshroom,
	MobTypeMule,
	MobTypeNautilus,
	MobTypeOcelot,
	MobTypePanda,
	MobTypeParched,
	MobTypeParrot,
	MobTypePhantom,
	MobTypePig,
	MobTypePiglin,
	MobTypePiglinBrute,
	MobTypePillager,
	MobTypePolarBear,
	MobTypePufferfish,
	MobTypeRabbit,
	MobTypeRavager,
	MobTypeSalmon,
	MobTypeSheep,
	MobTypeShulker,
	MobTypeSilverfish,
	MobTypeSkeleton,
	MobTypeSkeletonHorse,
	MobTypeSlime,
	MobTypeSniffer,
	MobTypeSnowGolem,
	MobTypeSpider,
	MobTypeSquid,
	MobTypeStray,
	MobTypeStrider,
	MobTypeTadpole,
	MobTypeTraderLlama,
	MobTypeTropicalFish,
	MobTypeTurtle,
	MobTypeVex,
	MobTypeVillager,
	MobTypeVindicator,
	MobTypeWanderingTrader,
	MobTypeWarden,
	MobTypeWitch,
	MobTypeWither,
	MobTypeWitherSkeleton,
	MobTypeWolf,
	MobTypeZoglin,
	MobTypeZombie,
	MobTypeZombieHorse,
	MobTypeZombieNautilus,
	MobTypeZombieVillager,
	MobTypeZombifiedPiglin,
}

// AllLivingMobTypes2612 returns a copy of all 89 living mob entity types in 26.1.2.
func AllLivingMobTypes2612() []string {
	out := make([]string, len(allLivingMobTypes2612))
	copy(out, allLivingMobTypes2612[:])
	return out
}

// AllSpawnEggItems2612 returns all 87 `minecraft:<mob>_spawn_egg` item IDs in
// `net/minecraft/world/item/Items.class` from `26.1.2.jar`.
func AllSpawnEggItems2612() []string {
	out := make([]string, 0, 87)
	for _, mob := range allLivingMobTypes2612 {
		if mob == MobTypeGiant || mob == MobTypeIllusioner {
			continue
		}
		short := strings.TrimPrefix(mob, "minecraft:")
		out = append(out, "minecraft:"+short+"_spawn_egg")
	}
	return out
}

// EntityTypeFromSpawnEgg2612 resolves a `minecraft:<mob>_spawn_egg` item ID to
// its corresponding `minecraft:<mob>` entity type in 26.1.2.
func EntityTypeFromSpawnEgg2612(itemID string) (string, bool) {
	norm := canonicalResourceName(itemID)
	if !strings.HasSuffix(norm, "_spawn_egg") {
		return "", false
	}
	mob := strings.TrimSuffix(norm, "_spawn_egg")
	if mob == MobTypeGiant || mob == MobTypeIllusioner {
		return "", false
	}
	for _, m := range allLivingMobTypes2612 {
		if m == mob {
			return mob, true
		}
	}
	return "", false
}

// MobCompletenessSpec2612 aggregates the complete vanilla 26.1.2 registration,
// attribute, hitbox, sound, loot table, spawn egg, and XP reward specification
// for a living mob entity.
type MobCompletenessSpec2612 struct {
	TypeName      string
	NetworkID     int
	Category      MobCategory2612
	MaxHealth     float32
	MovementSpeed float64
	AttackDamage  float32
	Armor         float32
	FollowRange   float64
	Width         float64
	Height        float64
	EyeHeight     float64
	AmbientSound  string
	HurtSound     string
	DeathSound    string
	StepSound     string
	LootTableID   string
	SpawnEggItem  string
	BaseXPMin     int
	BaseXPMax     int
}

// LookupMobCompletenessSpec2612 returns the complete `MobCompletenessSpec2612`
// for any of the 89 living mobs (plus `armor_stand` and `mannequin`) in 26.1.2.
func LookupMobCompletenessSpec2612(typeName string) (MobCompletenessSpec2612, bool) {
	norm := canonicalResourceName(typeName)
	netID, ok := EntityTypeRegistryIDByName2612(norm)
	if !ok {
		return MobCompletenessSpec2612{}, false
	}
	isLiving := false
	for _, m := range allLivingMobTypes2612 {
		if m == norm {
			isLiving = true
			break
		}
	}
	if !isLiving && norm != EntityTypeArmorStand && norm != EntityTypeMannequin {
		return MobCompletenessSpec2612{}, false
	}

	short := strings.TrimPrefix(norm, "minecraft:")
	hp := ResolveMobMaxHealth(norm)
	cat := MobCategoryForEntityType(norm)

	// Hitbox dimensions & attributes (using hostile_nether_mobs_polish.go table or passive/neutral defaults).
	attr, hasAttr := ResolveMobHitboxAndAttributes2612(norm)
	var width, height, eyeHeight float64
	var speed, follow float64
	var atk, armor float32
	var xpMin, xpMax int
	if hasAttr {
		width = float64(attr.Width)
		height = float64(attr.Height)
		eyeHeight = float64(attr.EyeHeight)
		speed = attr.MovementSpeed
		if speed <= 0 {
			speed = 0.2
		}
		atk = attr.AttackDamage
		armor = attr.Armor
		follow = attr.FollowRange
		if follow <= 0 {
			follow = 16.0
		}
		xpMin = int(attr.XPReward)
		xpMax = int(attr.XPReward)
	} else {
		width, height, eyeHeight = defaultPassiveOrNeutralHitbox2612(norm)
		speed, atk, armor, follow, xpMin, xpMax = defaultPassiveOrNeutralAttrs2612(norm)
	}

	// Sounds (using hostile_nether_mobs_polish.go table or canonical entity.<short>.*).
	snd, hasSnd := ResolveMobSoundSet2612(norm)
	stepSound := "minecraft:entity." + short + ".step"
	if !hasSnd {
		snd = MobSoundSet2612{
			Ambient: "minecraft:entity." + short + ".ambient",
			Hurt:    "minecraft:entity." + short + ".hurt",
			Death:   "minecraft:entity." + short + ".death",
			Special: stepSound,
		}
	} else if snd.Special != "" {
		stepSound = snd.Special
	}

	spawnEgg := "minecraft:" + short + "_spawn_egg"
	if norm == MobTypeGiant || norm == MobTypeIllusioner || norm == EntityTypeArmorStand || norm == EntityTypeMannequin {
		spawnEgg = ""
	}

	return MobCompletenessSpec2612{
		TypeName:      norm,
		NetworkID:     netID,
		Category:      cat,
		MaxHealth:     hp,
		MovementSpeed: speed,
		AttackDamage:  atk,
		Armor:         armor,
		FollowRange:   follow,
		Width:         width,
		Height:        height,
		EyeHeight:     eyeHeight,
		AmbientSound:  snd.Ambient,
		HurtSound:     snd.Hurt,
		DeathSound:    snd.Death,
		StepSound:     stepSound,
		LootTableID:   "minecraft:entities/" + short,
		SpawnEggItem:  spawnEgg,
		BaseXPMin:     xpMin,
		BaseXPMax:     xpMax,
	}, true
}

func defaultPassiveOrNeutralHitbox2612(norm string) (width, height, eyeHeight float64) {
	switch norm {
	case MobTypePolarBear:
		return 1.4, 1.4, 1.19
	case MobTypeGiant:
		return 3.6, 12.0, 10.44
	case EntityTypeArmorStand:
		return 0.5, 1.975, 1.7775
	case EntityTypeMannequin, MobTypeVillager, MobTypeWanderingTrader:
		return 0.6, 1.95, 1.62
	case MobTypeCow, MobTypeMooshroom:
		return 0.9, 1.4, 1.3
	case MobTypeSheep, MobTypeGoat:
		return 0.9, 1.3, 1.235
	case MobTypePig:
		return 0.9, 0.9, 0.765
	case MobTypeChicken, MobTypeRabbit:
		return 0.4, 0.7, 0.644
	case MobTypeWolf:
		return 0.6, 0.85, 0.68
	case MobTypeCat, MobTypeOcelot, MobTypeFox:
		return 0.6, 0.7, 0.35
	case MobTypeParrot, MobTypeBat:
		return 0.5, 0.9, 0.54
	case MobTypeHorse, MobTypeSkeletonHorse, MobTypeZombieHorse, MobTypeMule, MobTypeDonkey:
		return 1.3965, 1.6, 1.52
	case MobTypeCamel, MobTypeCamelHusk:
		return 1.7, 2.375, 2.275
	case MobTypeSniffer:
		return 1.9, 1.75, 1.05
	case MobTypePanda:
		return 1.3, 1.25, 1.0625
	case MobTypeTurtle:
		return 1.2, 0.4, 0.32
	case MobTypeArmadillo, MobTypeBee:
		return 0.7, 0.65, 0.325
	case MobTypeFrog:
		return 0.5, 0.5, 0.375
	case MobTypeTadpole, MobTypeCod, MobTypeTropicalFish:
		return 0.5, 0.3, 0.195
	case MobTypeSalmon, MobTypePufferfish, MobTypeAxolotl:
		return 0.7, 0.4, 0.26
	case MobTypeDolphin:
		return 0.9, 0.6, 0.3
	case MobTypeSquid, MobTypeGlowSquid, MobTypeNautilus, MobTypeZombieNautilus:
		return 0.8, 0.8, 0.4
	case MobTypeHappyGhast:
		return 4.0, 4.0, 2.6
	case MobTypeWarden:
		return 0.9, 2.9, 2.4
	case MobTypeWither:
		return 0.9, 3.5, 2.975
	case MobTypeEnderDragon:
		return 16.0, 8.0, 6.8
	default:
		return 0.6, 1.8, 1.53
	}
}

func defaultPassiveOrNeutralAttrs2612(norm string) (speed float64, atk, armor float32, follow float64, xpMin, xpMax int) {
	switch norm {
	case MobTypePolarBear:
		return 0.25, 6.0, 0, 20.0, 1, 3
	case MobTypeGiant:
		return 0.5, 50.0, 0, 16.0, 5, 5
	case MobTypeWolf:
		return 0.3, 4.0, 0, 16.0, 1, 3
	case MobTypeCat, MobTypeOcelot, MobTypeFox:
		return 0.3, 2.0, 0, 16.0, 1, 3
	case MobTypePanda:
		return 0.15, 6.0, 0, 16.0, 1, 3
	case MobTypeBee:
		return 0.3, 2.0, 0, 48.0, 1, 3
	case MobTypeGoat:
		return 0.2, 2.0, 0, 16.0, 1, 3
	case MobTypeLlama, MobTypeTraderLlama:
		return 0.175, 1.0, 0, 40.0, 1, 3
	case MobTypeDolphin:
		return 1.2, 3.0, 0, 16.0, 1, 3
	case MobTypeWarden:
		return 0.3, 30.0, 0, 32.0, 5, 5
	case MobTypeWither:
		return 0.6, 8.0, 4.0, 40.0, 50, 50
	case MobTypeEnderDragon:
		return 0.7, 10.0, 0, 150.0, 500, 12000
	case MobTypeBat, MobTypeVillager, MobTypeWanderingTrader, MobTypeAllay, MobTypeTadpole, EntityTypeArmorStand, EntityTypeMannequin:
		return 0.2, 0, 0, 16.0, 0, 0
	default:
		return 0.25, 0, 0, 16.0, 1, 3
	}
}

// RollPolarBearDeathLoot2612 rolls `data/minecraft/loot_table/entities/polar_bear.json`
// from `26.1.2.jar`: 1 pool with 1 roll choosing `minecraft:cod` (`weight 3`) or
// `minecraft:salmon` (`weight 1`), count `0..2` + `0..lootingLevel`, smelted to
// `cooked_cod`/`cooked_salmon` when `onFireOrSmeltsLoot` is true.
func RollPolarBearDeathLoot2612(onFireOrSmeltsLoot bool, lootingLevel int, rng *rand.Rand) []world.ItemStack {
	if lootingLevel < 0 {
		lootingLevel = 0
	}
	intn := func(n int) int {
		if rng != nil {
			return rng.Intn(n)
		}
		return rand.Intn(n)
	}
	isCod := intn(4) < 3 // weight 3 cod, weight 1 salmon
	count := intn(3)
	if lootingLevel > 0 {
		count += intn(lootingLevel + 1)
	}
	if count <= 0 {
		return nil
	}
	itemID := "minecraft:salmon"
	if isCod {
		itemID = "minecraft:cod"
		if onFireOrSmeltsLoot {
			itemID = "minecraft:cooked_cod"
		}
	} else if onFireOrSmeltsLoot {
		itemID = "minecraft:cooked_salmon"
	}
	return []world.ItemStack{world.NewNamedItemStack(itemID, int32(count))}
}

// --- 2. Entity Type Tag Membership (data/minecraft/tags/entity_type/* in 26.1.2.jar) ---

// IsMobInEntityTypeTag2612 checks whether `mobType` belongs to the given
// `data/minecraft/tags/entity_type/<tag>.json` tag in `26.1.2.jar`.
func IsMobInEntityTypeTag2612(mobType, tag string) bool {
	normMob := canonicalResourceName(mobType)
	normTag := strings.TrimPrefix(canonicalResourceName(tag), "#")

	switch normTag {
	case "minecraft:skeletons":
		switch normMob {
		case MobTypeSkeleton, MobTypeStray, MobTypeWitherSkeleton, MobTypeSkeletonHorse, MobTypeBogged, MobTypeParched:
			return true
		}
	case "minecraft:zombies":
		switch normMob {
		case MobTypeZombieHorse, MobTypeCamelHusk, MobTypeZombie, MobTypeZombieVillager,
			MobTypeZombifiedPiglin, MobTypeZoglin, MobTypeDrowned, MobTypeHusk, MobTypeZombieNautilus:
			return true
		}
	case "minecraft:undead", "minecraft:ignores_poison_and_regen", "minecraft:inverted_healing_and_harm", "minecraft:sensitive_to_smite":
		if IsMobInEntityTypeTag2612(normMob, "minecraft:skeletons") || IsMobInEntityTypeTag2612(normMob, "minecraft:zombies") {
			return true
		}
		return normMob == MobTypeWither || normMob == MobTypePhantom
	case "minecraft:arthropod", "minecraft:sensitive_to_bane_of_arthropods":
		switch normMob {
		case MobTypeBee, MobTypeEndermite, MobTypeSilverfish, MobTypeSpider, MobTypeCaveSpider:
			return true
		}
	case "minecraft:aquatic", "minecraft:sensitive_to_impaling":
		switch normMob {
		case MobTypeTurtle, MobTypeAxolotl, MobTypeGuardian, MobTypeElderGuardian,
			MobTypeCod, MobTypePufferfish, MobTypeSalmon, MobTypeTropicalFish,
			MobTypeDolphin, MobTypeSquid, MobTypeGlowSquid, MobTypeTadpole,
			MobTypeNautilus, MobTypeZombieNautilus:
			return true
		}
	case "minecraft:illager":
		switch normMob {
		case MobTypeEvoker, MobTypeIllusioner, MobTypePillager, MobTypeVindicator:
			return true
		}
	case "minecraft:raiders":
		switch normMob {
		case MobTypeEvoker, MobTypePillager, MobTypeRavager, MobTypeVindicator, MobTypeIllusioner, MobTypeWitch:
			return true
		}
	case "minecraft:burn_in_daylight":
		switch normMob {
		case MobTypeSkeleton, MobTypeStray, MobTypeWitherSkeleton, MobTypeBogged,
			MobTypeZombie, MobTypeZombieHorse, MobTypeZombieVillager, MobTypeDrowned,
			MobTypeZombieNautilus, MobTypePhantom:
			return true
		}
	case "minecraft:can_equip_saddle":
		switch normMob {
		case MobTypeHorse, MobTypeSkeletonHorse, MobTypeZombieHorse, MobTypeDonkey, MobTypeMule,
			MobTypePig, MobTypeStrider, MobTypeCamel, MobTypeCamelHusk, MobTypeNautilus, MobTypeZombieNautilus:
			return true
		}
	case "minecraft:can_equip_harness":
		return normMob == MobTypeHappyGhast
	case "minecraft:can_wear_horse_armor":
		return normMob == MobTypeHorse || normMob == MobTypeZombieHorse
	case "minecraft:can_wear_nautilus_armor":
		return normMob == MobTypeNautilus || normMob == MobTypeZombieNautilus
	case "minecraft:fall_damage_immune":
		switch normMob {
		case MobTypeCopperGolem, MobTypeIronGolem, MobTypeSnowGolem, MobTypeShulker,
			MobTypeAllay, MobTypeBat, MobTypeBee, MobTypeBlaze, MobTypeCat, MobTypeChicken,
			MobTypeGhast, MobTypeHappyGhast, MobTypePhantom, MobTypeMagmaCube, MobTypeOcelot,
			MobTypeParrot, MobTypeWither, MobTypeBreeze:
			return true
		}
	case "minecraft:freeze_immune_entity_types":
		switch normMob {
		case MobTypeStray, MobTypePolarBear, MobTypeSnowGolem, MobTypeWither:
			return true
		}
	case "minecraft:freeze_hurts_extra_types":
		switch normMob {
		case MobTypeStrider, MobTypeBlaze, MobTypeMagmaCube:
			return true
		}
	case "minecraft:can_breathe_under_water":
		if IsMobInEntityTypeTag2612(normMob, "minecraft:undead") {
			return true
		}
		switch normMob {
		case MobTypeAxolotl, MobTypeFrog, MobTypeGuardian, MobTypeElderGuardian,
			MobTypeTurtle, MobTypeGlowSquid, MobTypeCod, MobTypePufferfish,
			MobTypeSalmon, MobTypeSquid, MobTypeTropicalFish, MobTypeTadpole,
			EntityTypeArmorStand, MobTypeCopperGolem, MobTypeNautilus:
			return true
		}
	}
	return false
}

// --- 3. Breeding & Taming Food Tags and Rules (data/minecraft/tags/item/*_food.json) ---

// IsMobFoodItem2612 reports whether `itemID` is in `#minecraft:<mob>_food` in `26.1.2.jar`.
func IsMobFoodItem2612(mobType, itemID string) bool {
	normMob := canonicalResourceName(mobType)
	normItem := canonicalResourceName(itemID)

	switch normMob {
	case MobTypeWolf:
		return IsWolfMeatItem(normItem) || normItem == "minecraft:pufferfish" || normItem == "minecraft:rabbit_stew"
	case MobTypeCat, MobTypeOcelot:
		return IsCatFoodItem(normItem)
	case MobTypeParrot:
		return IsParrotTameSeedItem(normItem)
	case MobTypeHorse, MobTypeDonkey, MobTypeMule:
		switch normItem {
		case "minecraft:wheat", "minecraft:sugar", "minecraft:hay_block", "minecraft:apple",
			"minecraft:carrot", "minecraft:golden_carrot", "minecraft:golden_apple", "minecraft:enchanted_golden_apple":
			return true
		}
	case MobTypeCamel:
		return normItem == "minecraft:cactus"
	case MobTypeCamelHusk:
		return normItem == "minecraft:rabbit_foot"
	case MobTypeZombieHorse:
		return normItem == "minecraft:red_mushroom"
	case MobTypeNautilus, MobTypeZombieNautilus:
		switch normItem {
		case "minecraft:cod", "minecraft:cooked_cod", "minecraft:salmon", "minecraft:cooked_salmon",
			"minecraft:tropical_fish", "minecraft:pufferfish",
			"minecraft:cod_bucket", "minecraft:salmon_bucket", "minecraft:tropical_fish_bucket", "minecraft:pufferfish_bucket":
			return true
		}
	case MobTypeFox:
		return normItem == "minecraft:sweet_berries" || normItem == "minecraft:glow_berries"
	case MobTypePanda:
		return normItem == "minecraft:bamboo"
	case MobTypeSniffer:
		return normItem == "minecraft:torchflower_seeds"
	case MobTypeArmadillo:
		return normItem == "minecraft:spider_eye"
	case MobTypeFrog:
		return normItem == "minecraft:slime_ball"
	case MobTypeHoglin:
		return normItem == "minecraft:crimson_fungus"
	case MobTypeStrider:
		return normItem == "minecraft:warped_fungus"
	case MobTypeTurtle:
		return normItem == "minecraft:seagrass"
	case MobTypeAxolotl:
		return normItem == "minecraft:tropical_fish_bucket"
	case MobTypeHappyGhast:
		return normItem == "minecraft:snowball"
	case MobTypeCow, MobTypeMooshroom, MobTypeSheep, MobTypeGoat:
		return normItem == "minecraft:wheat"
	case MobTypePig:
		return normItem == "minecraft:carrot" || normItem == "minecraft:potato" || normItem == "minecraft:beetroot"
	case MobTypeChicken:
		return IsParrotTameSeedItem(normItem)
	case MobTypeRabbit:
		return normItem == "minecraft:carrot" || normItem == "minecraft:golden_carrot" || normItem == "minecraft:dandelion"
	case MobTypeLlama, MobTypeTraderLlama:
		return normItem == "minecraft:wheat" || normItem == "minecraft:hay_block"
	}
	return false
}

// MobTamingBreedingResult2612 describes the outcome of feeding `itemID` to a mob.
type MobTamingBreedingResult2612 struct {
	ConsumedItem     bool
	TameAttempted    bool
	TameSucceeded    bool
	NewMaxHealth     float32
	EnteredLoveMode  bool
	LoveTicks        int
	BreedCooldown    int
	SpawnedEggBlock  string
	PoisonedParrot   bool
	TrustingFoxBaby  bool
}

// EvaluateMobTamingOrBreeding2612 evaluates taming or breeding interaction when
// a player feeds `itemID` to `mobType` in 26.1.2.
func EvaluateMobTamingOrBreeding2612(mobType, itemID string, alreadyTamed bool, nearbyBambooCount int, rng *rand.Rand) MobTamingBreedingResult2612 {
	normMob := canonicalResourceName(mobType)
	normItem := canonicalResourceName(itemID)
	intn := func(n int) int {
		if rng != nil {
			return rng.Intn(n)
		}
		return rand.Intn(n)
	}

	// Parrot cookie poisoning (#minecraft:parrot_poisonous_food = minecraft:cookie).
	if normMob == MobTypeParrot && normItem == "minecraft:cookie" {
		return MobTamingBreedingResult2612{
			ConsumedItem:   true,
			PoisonedParrot: true,
		}
	}

	// Untamed Wolf tamed with bone (1/3 chance, MaxHealth 8.0 -> 40.0 in 26.1.2).
	if normMob == MobTypeWolf && !alreadyTamed && normItem == "minecraft:bone" {
		won := intn(3) == 0
		res := MobTamingBreedingResult2612{
			ConsumedItem:  true,
			TameAttempted: true,
			TameSucceeded: won,
		}
		if won {
			res.NewMaxHealth = DefaultWolfTamedMaxHealth
		}
		return res
	}

	// Untamed Cat tamed with cod/salmon (1/3 chance).
	if normMob == MobTypeCat && !alreadyTamed && IsCatFoodItem(normItem) {
		won := intn(3) == 0
		return MobTamingBreedingResult2612{
			ConsumedItem:  true,
			TameAttempted: true,
			TameSucceeded: won,
			NewMaxHealth:  DefaultCatMaxHealth,
		}
	}

	// Untamed Parrot tamed with seeds (1/10 chance; parrots cannot breed).
	if normMob == MobTypeParrot && !alreadyTamed && IsParrotTameSeedItem(normItem) {
		won := intn(10) == 0
		return MobTamingBreedingResult2612{
			ConsumedItem:  true,
			TameAttempted: true,
			TameSucceeded: won,
			NewMaxHealth:  DefaultParrotMaxHealth,
		}
	}

	if !IsMobFoodItem2612(normMob, normItem) {
		return MobTamingBreedingResult2612{}
	}

	// Horses/Donkeys require taming and golden_apple / golden_carrot to enter love mode.
	if normMob == MobTypeHorse || normMob == MobTypeDonkey {
		if !alreadyTamed || (normItem != "minecraft:golden_apple" && normItem != "minecraft:enchanted_golden_apple" && normItem != "minecraft:golden_carrot") {
			return MobTamingBreedingResult2612{ConsumedItem: true}
		}
	}
	if (normMob == MobTypeWolf || normMob == MobTypeCat) && !alreadyTamed {
		return MobTamingBreedingResult2612{}
	}
	// Panda breeding requires at least 8 bamboo blocks within 5 blocks.
	if normMob == MobTypePanda && nearbyBambooCount < 8 {
		return MobTamingBreedingResult2612{ConsumedItem: true}
	}

	res := MobTamingBreedingResult2612{
		ConsumedItem:    true,
		EnteredLoveMode: true,
		LoveTicks:       AnimalLoveDurationTicks,
		BreedCooldown:   AnimalBreedCooldownTicks,
	}
	if normMob == MobTypeSniffer {
		res.SpawnedEggBlock = "minecraft:sniffer_egg"
	}
	if normMob == MobTypeFox {
		res.TrustingFoxBaby = true
	}
	return res
}

// --- 4. Leash & Boat / Minecart Passenger Rules (26.1.2) ---

// CanMobBeLeashed2612 reports whether `mobType` can be attached to a `minecraft:lead`
// in vanilla 26.1.2 (`Leashable` / `Mob.canBeLeashed`).
func CanMobBeLeashed2612(mobType string, isAngryWolf bool) bool {
	norm := canonicalResourceName(mobType)
	if isAngryWolf && norm == MobTypeWolf {
		return false
	}
	switch norm {
	case MobTypeEnderDragon, MobTypeWither, MobTypeWarden,
		MobTypeVillager, MobTypeWanderingTrader,
		MobTypeBat, MobTypeShulker, MobTypeTurtle, MobTypePanda,
		MobTypeEnderman, MobTypeGhast, MobTypeVex,
		MobTypePillager, MobTypeVindicator, MobTypeEvoker, MobTypeIllusioner:
		return false
	default:
		_, ok := LookupMobCompletenessSpec2612(norm)
		return ok && norm != EntityTypeArmorStand && norm != EntityTypeMannequin
	}
}

// VehicleMaxMobPassengers2612 returns the maximum passenger capacity of a boat,
// chest boat, or minecart in 26.1.2 (`2` for standard boats/rafts, `1` for chest boats and minecarts).
func VehicleMaxMobPassengers2612(vehicleEntityType string) int {
	norm := canonicalResourceName(vehicleEntityType)
	if strings.Contains(norm, "chest_boat") || strings.Contains(norm, "chest_raft") {
		return 1
	}
	if strings.HasSuffix(norm, "_boat") || strings.HasSuffix(norm, "_raft") {
		return 2
	}
	if strings.HasSuffix(norm, "minecart") {
		return 1
	}
	return 0
}

// CanMobMountVehicle2612 reports whether `mobType` can auto-mount a boat or
// minecart in vanilla 26.1.2 (`#minecraft:cannot_be_pushed_onto_boats` excludes
// `player`, `elder_guardian`, `creaking`, `warden`, `wither`, `ender_dragon`, `ghast`, `happy_ghast`).
func CanMobMountVehicle2612(mobType, vehicleEntityType string, currentPassengers int) bool {
	maxPass := VehicleMaxMobPassengers2612(vehicleEntityType)
	if maxPass <= 0 || currentPassengers >= maxPass {
		return false
	}
	normMob := canonicalResourceName(mobType)
	switch normMob {
	case MobTypeElderGuardian, MobTypeCreaking, MobTypeWarden, MobTypeWither,
		MobTypeEnderDragon, MobTypeGhast, MobTypeHappyGhast, MobTypeShulker,
		EntityTypeArmorStand, EntityTypeMannequin:
		return false
	}
	return true
}

// --- 5. Environmental Damage (Fall, Fire, Drowning, Powder Snow) & Potion Effects ---

// ComputeMobFallDamage2612 calculates the fall damage taken by `mobType` in 26.1.2
// after falling `fallDistance` blocks:
//   - Mobs in `#minecraft:fall_damage_immune` or `ender_dragon` / `slime` take `0`.
//   - `goat` and `Info` (`frog`, `camel`) reduce fall damage (`-10` for goat, `-5` for frog, `-4` for horse/camel).
func ComputeMobFallDamage2612(mobType string, fallDistance float64) float32 {
	if fallDistance <= 3.0 {
		return 0
	}
	norm := canonicalResourceName(mobType)
	if IsMobInEntityTypeTag2612(norm, "minecraft:fall_damage_immune") || norm == MobTypeEnderDragon {
		return 0
	}
	reduction := 3.0
	switch norm {
	case MobTypeGoat:
		reduction = 13.0 // 3 base + 10 goat reduction
	case MobTypeFrog:
		reduction = 8.0 // 3 base + 5 frog reduction
	case MobTypeHorse, MobTypeDonkey, MobTypeMule, MobTypeSkeletonHorse, MobTypeZombieHorse, MobTypeCamel, MobTypeCamelHusk:
		reduction = 6.0
	case MobTypeFox, MobTypeRabbit:
		reduction = 5.0
	}
	dmg := math.Ceil(fallDistance - reduction)
	if dmg <= 0 {
		return 0
	}
	return float32(dmg)
}

// IsMobFireImmune2612 reports whether `mobType` is immune to fire and lava
// damage in vanilla 26.1.2 (`EntityType.fireImmune()` or active Fire Resistance).
func IsMobFireImmune2612(mobType string, hasFireResistance bool) bool {
	if hasFireResistance {
		return true
	}
	switch canonicalResourceName(mobType) {
	case MobTypeBlaze, MobTypeGhast, MobTypeMagmaCube, MobTypeStrider,
		MobTypeWitherSkeleton, MobTypeZombifiedPiglin, MobTypeZoglin,
		MobTypeWarden, MobTypeWither, MobTypeEnderDragon, MobTypeShulker:
		return true
	default:
		return false
	}
}

// StepMobAirSupplyAndDrowning2612 steps one tick of air supply and drowning for
// `mobType`:
//   - Mobs in `#minecraft:can_breathe_under_water` or with `Water Breathing`
//     never lose air underwater.
//   - Otherwise, air decreases by `1` per tick underwater; at `air == -20`,
//     resets to `0` and deals `2.0` drowning damage.
func StepMobAirSupplyAndDrowning2612(mobType string, currentAir int, inWater, hasWaterBreathing bool) (nextAir int, drownDamage float32) {
	norm := canonicalResourceName(mobType)
	maxAir := DefaultMobMaxAirTicks
	if norm == MobTypeDolphin {
		maxAir = DolphinMaxAirTicks
	} else if norm == MobTypeAxolotl {
		maxAir = AxolotlMaxAirTicks
	}

	if inWater {
		if IsMobInEntityTypeTag2612(norm, "minecraft:can_breathe_under_water") || hasWaterBreathing {
			return maxAir, 0
		}
		nextAir = currentAir - 1
		if nextAir <= -20 {
			return 0, 2.0
		}
		return nextAir, 0
	}

	// Out of water: recover +4 air per tick up to maxAir.
	nextAir = currentAir + 4
	if nextAir > maxAir {
		nextAir = maxAir
	}
	return nextAir, 0
}

// StepMobPowderSnowFreezing2612 steps one tick of powder snow freezing (`TicksFrozen`):
//   - Immune if in `#minecraft:freeze_immune_entity_types` or wearing any leather armor.
//   - In powder snow: `ticksFrozen = min(140, ticksFrozen + 1)`. At `140`, deals
//     `1.0` freeze damage (`5.0` for `#minecraft:freeze_hurts_extra_types`) every `40` ticks.
//   - Outside powder snow: `ticksFrozen = max(0, ticksFrozen - 2)`.
func StepMobPowderSnowFreezing2612(mobType string, ticksFrozen int, inPowderSnow, wearingLeatherArmor bool, tickCounter int) (nextTicksFrozen int, freezeDamage float32) {
	norm := canonicalResourceName(mobType)
	if IsMobInEntityTypeTag2612(norm, "minecraft:freeze_immune_entity_types") || wearingLeatherArmor {
		return 0, 0
	}
	if inPowderSnow {
		nextTicksFrozen = ticksFrozen + 1
		if nextTicksFrozen > PowderSnowRequiredFreezeTicks {
			nextTicksFrozen = PowderSnowRequiredFreezeTicks
		}
		if nextTicksFrozen >= PowderSnowRequiredFreezeTicks && tickCounter%40 == 0 {
			if IsMobInEntityTypeTag2612(norm, "minecraft:freeze_hurts_extra_types") {
				return nextTicksFrozen, 5.0
			}
			return nextTicksFrozen, 1.0
		}
		return nextTicksFrozen, 0
	}
	nextTicksFrozen = ticksFrozen - 2
	if nextTicksFrozen < 0 {
		nextTicksFrozen = 0
	}
	return nextTicksFrozen, 0
}

// CanMobBeAffectedByPotionEffect2612 reports whether `mobType` can receive the
// continuous status effect `effectName` in 26.1.2:
//   - Undead (`#minecraft:ignores_poison_and_regen`) ignore `poison` and `regeneration`.
//   - `spider` and `cave_spider` ignore `poison`.
//   - `wither` and `ender_dragon` ignore `wither` (Ender Dragon ignores all status effects).
//   - `parched` ignores `weakness`.
func CanMobBeAffectedByPotionEffect2612(mobType, effectName string) bool {
	normMob := canonicalResourceName(mobType)
	normEff := canonicalResourceName(effectName)
	if normMob == MobTypeEnderDragon {
		return false
	}
	if IsMobInEntityTypeTag2612(normMob, "minecraft:ignores_poison_and_regen") {
		if normEff == "minecraft:poison" || normEff == "minecraft:regeneration" {
			return false
		}
	}
	if (normMob == MobTypeSpider || normMob == MobTypeCaveSpider) && normEff == "minecraft:poison" {
		return false
	}
	if (normMob == MobTypeWither || normMob == MobTypeWitherSkeleton) && normEff == "minecraft:wither" {
		return false
	}
	if normMob == MobTypeParched && normEff == "minecraft:weakness" {
		return false
	}
	return true
}

// ResolveInstantHealthOrDamageOnMob2612 computes the health delta (`+` heal, `-` damage)
// when `minecraft:instant_health` or `minecraft:instant_damage` is applied to `mobType`,
// inverting healing and harm for `#minecraft:inverted_healing_and_harm` (all undead).
func ResolveInstantHealthOrDamageOnMob2612(mobType, effectName string, amplifier int) float32 {
	if amplifier < 0 {
		amplifier = 0
	}
	normMob := canonicalResourceName(mobType)
	normEff := canonicalResourceName(effectName)
	inverted := IsMobInEntityTypeTag2612(normMob, "minecraft:inverted_healing_and_harm")

	isHealEffect := normEff == "minecraft:instant_health"
	isHarmEffect := normEff == "minecraft:instant_damage"
	if !isHealEffect && !isHarmEffect {
		return 0
	}

	shouldHeal := (isHealEffect && !inverted) || (isHarmEffect && inverted)
	if shouldHeal {
		return float32(int(4) << amplifier)
	}
	return -float32(int(6) << amplifier)
}

// --- 6. Metadata Flags, Custom Name, Team Color & NBT Persistence ---

// BuildEntitySharedFlagsByte2612 packs the 7 boolean entity state flags into
// SynchedEntityData index 0 (`Entity.DATA_SHARED_FLAGS_ID`).
func BuildEntitySharedFlagsByte2612(onFire, crouching, sprinting, swimming, invisible, glowing, fallFlying bool) byte {
	var b byte
	if onFire {
		b |= EntitySharedFlagOnFire
	}
	if crouching {
		b |= EntitySharedFlagShiftKey
	}
	if sprinting {
		b |= EntitySharedFlagSprinting
	}
	if swimming {
		b |= EntitySharedFlagSwimming
	}
	if invisible {
		b |= EntitySharedFlagInvisible
	}
	if glowing {
		b |= EntitySharedFlagGlowing
	}
	if fallFlying {
		b |= EntitySharedFlagFallFlying
	}
	return b
}

// MobPersistentNBTData2612 represents the persistent vanilla 26.1.2 NBT compound
// fields for a saved mob entity.
type MobPersistentNBTData2612 struct {
	ID                  string
	Health              float32
	Air                 int16
	Fire                int16
	FallDistance        float64
	TicksFrozen         int32
	AbsorptionAmount    float32
	PersistenceRequired bool
	CustomName          string
	CustomNameVisible   bool
	NoAI                bool
	Silent              bool
	Glowing             bool
	Invisible           bool
	Invulnerable        bool
	Age                 int32
	InLove              int32
	OwnerUUID           string
	Sitting             bool
	Saddled             bool
	CollarColor         int32
	Team                string
}

// ApplyNameTagToMob2612 applies a `minecraft:name_tag` with `customName` to a
// mob's persistent state, setting `CustomName` and `PersistenceRequired = true`
// (except on `ender_dragon`).
func ApplyNameTagToMob2612(data *MobPersistentNBTData2612, customName string) bool {
	if data == nil || strings.TrimSpace(customName) == "" {
		return false
	}
	if canonicalResourceName(data.ID) == MobTypeEnderDragon {
		return false
	}
	data.CustomName = customName
	data.PersistenceRequired = true
	return true
}

// EncodeMobPersistentNBT2612 serializes `MobPersistentNBTData2612` to an NBT compound blob.
func EncodeMobPersistentNBT2612(d MobPersistentNBTData2612) ([]byte, error) {
	boolByte := func(v bool) nbt.Byte {
		if v {
			return 1
		}
		return 0
	}
	c := nbt.NewCompound().
		Set("id", nbt.String(canonicalResourceName(d.ID))).
		Set("Health", nbt.Float(d.Health)).
		Set("Air", nbt.Short(d.Air)).
		Set("Fire", nbt.Short(d.Fire)).
		Set("fall_distance", nbt.Double(d.FallDistance)).
		Set("TicksFrozen", nbt.Int(d.TicksFrozen)).
		Set("AbsorptionAmount", nbt.Float(d.AbsorptionAmount)).
		Set("PersistenceRequired", boolByte(d.PersistenceRequired)).
		Set("CustomNameVisible", boolByte(d.CustomNameVisible)).
		Set("NoAI", boolByte(d.NoAI)).
		Set("Silent", boolByte(d.Silent)).
		Set("Glowing", boolByte(d.Glowing)).
		Set("Invisible", boolByte(d.Invisible)).
		Set("Invulnerable", boolByte(d.Invulnerable)).
		Set("Age", nbt.Int(d.Age)).
		Set("InLove", nbt.Int(d.InLove)).
		Set("Sitting", boolByte(d.Sitting)).
		Set("Saddle", boolByte(d.Saddled)).
		Set("CollarColor", nbt.Byte(d.CollarColor))
	if d.CustomName != "" {
		c.Set("CustomName", nbt.String(d.CustomName))
	}
	if d.OwnerUUID != "" {
		c.Set("Owner", nbt.String(d.OwnerUUID))
	}
	if d.Team != "" {
		c.Set("Team", nbt.String(d.Team))
	}
	return nbt.MarshalNamed("", c), nil
}

// DecodeMobPersistentNBT2612 decodes a `MobPersistentNBTData2612` from an NBT compound blob.
func DecodeMobPersistentNBT2612(raw []byte) (MobPersistentNBTData2612, error) {
	_, tag, err := nbt.UnmarshalNamed(raw)
	if err != nil {
		return MobPersistentNBTData2612{}, err
	}
	c, ok := tag.(*nbt.Compound)
	if !ok || c == nil {
		return MobPersistentNBTData2612{}, nil
	}
	var d MobPersistentNBTData2612
	if t, ok := c.Get("id"); ok {
		if v, ok := t.(nbt.String); ok {
			d.ID = string(v)
		}
	}
	if t, ok := c.Get("Health"); ok {
		if v, ok := t.(nbt.Float); ok {
			d.Health = float32(v)
		}
	}
	if t, ok := c.Get("Air"); ok {
		if v, ok := t.(nbt.Short); ok {
			d.Air = int16(v)
		}
	}
	if t, ok := c.Get("Fire"); ok {
		if v, ok := t.(nbt.Short); ok {
			d.Fire = int16(v)
		}
	}
	if t, ok := c.Get("fall_distance"); ok {
		if v, ok := t.(nbt.Double); ok {
			d.FallDistance = float64(v)
		}
	}
	if t, ok := c.Get("TicksFrozen"); ok {
		if v, ok := t.(nbt.Int); ok {
			d.TicksFrozen = int32(v)
		}
	}
	if t, ok := c.Get("AbsorptionAmount"); ok {
		if v, ok := t.(nbt.Float); ok {
			d.AbsorptionAmount = float32(v)
		}
	}
	if t, ok := c.Get("PersistenceRequired"); ok {
		if v, ok := t.(nbt.Byte); ok {
			d.PersistenceRequired = v != 0
		}
	}
	if t, ok := c.Get("CustomName"); ok {
		if v, ok := t.(nbt.String); ok {
			d.CustomName = string(v)
		}
	}
	if t, ok := c.Get("CustomNameVisible"); ok {
		if v, ok := t.(nbt.Byte); ok {
			d.CustomNameVisible = v != 0
		}
	}
	if t, ok := c.Get("NoAI"); ok {
		if v, ok := t.(nbt.Byte); ok {
			d.NoAI = v != 0
		}
	}
	if t, ok := c.Get("Silent"); ok {
		if v, ok := t.(nbt.Byte); ok {
			d.Silent = v != 0
		}
	}
	if t, ok := c.Get("Glowing"); ok {
		if v, ok := t.(nbt.Byte); ok {
			d.Glowing = v != 0
		}
	}
	if t, ok := c.Get("Invisible"); ok {
		if v, ok := t.(nbt.Byte); ok {
			d.Invisible = v != 0
		}
	}
	if t, ok := c.Get("Invulnerable"); ok {
		if v, ok := t.(nbt.Byte); ok {
			d.Invulnerable = v != 0
		}
	}
	if t, ok := c.Get("Age"); ok {
		if v, ok := t.(nbt.Int); ok {
			d.Age = int32(v)
		}
	}
	if t, ok := c.Get("InLove"); ok {
		if v, ok := t.(nbt.Int); ok {
			d.InLove = int32(v)
		}
	}
	if t, ok := c.Get("Owner"); ok {
		if v, ok := t.(nbt.String); ok {
			d.OwnerUUID = string(v)
		}
	}
	if t, ok := c.Get("Sitting"); ok {
		if v, ok := t.(nbt.Byte); ok {
			d.Sitting = v != 0
		}
	}
	if t, ok := c.Get("Saddle"); ok {
		if v, ok := t.(nbt.Byte); ok {
			d.Saddled = v != 0
		}
	}
	if t, ok := c.Get("CollarColor"); ok {
		if v, ok := t.(nbt.Byte); ok {
			d.CollarColor = int32(v)
		}
	}
	if t, ok := c.Get("Team"); ok {
		if v, ok := t.(nbt.String); ok {
			d.Team = string(v)
		}
	}
	return d, nil
}

// --- 7. Item Interactions with Mobs (Saddle, Shears, Buckets, Bowls, Bottles, Brush) ---

// MobItemInteractionResult2612 describes the result of right-clicking a mob
// with a tool or container item in vanilla 26.1.2.
type MobItemInteractionResult2612 struct {
	Handled             bool
	ConsumedHeldItem    bool
	ReplacementItemID   string
	DroppedItems        []world.ItemStack
	ToolDurabilityCost  int
	EquippedSaddle      bool
	RemovedSaddle       bool
	ConvertedToMobType  string
	ScoopedMobIntoItem  bool
	PersistenceGranted  bool
}

// InteractItemWithMob2612 evaluates item interactions on `mobType` in 26.1.2:
//   - `minecraft:saddle`: equips saddle on `#minecraft:can_equip_saddle` mobs.
//   - `minecraft:shears`: shears `sheep` (`1..3` wool), `mooshroom` (`5` mushrooms + convert to `cow`),
//     `snow_golem` (`carved_pumpkin`), `bogged` (`2` mushrooms), or unsaddles a saddled mob.
//   - `minecraft:bucket`: milks `cow`, `mooshroom`, or `goat` -> `minecraft:milk_bucket`.
//   - `minecraft:water_bucket`: scoops `axolotl`, `cod`, `salmon`, `pufferfish`, `tropical_fish`, `tadpole`
//     -> `minecraft:<mob>_bucket`.
//   - `minecraft:bowl`: milks `mooshroom` -> `minecraft:mushroom_stew` (or `minecraft:suspicious_stew` if `hasStewFlower`).
//   - `minecraft:glass_bottle`: collects `minecraft:dragon_breath` from `minecraft:area_effect_cloud`.
//   - `minecraft:brush`: brushes `minecraft:armadillo` -> drops `1` `minecraft:armadillo_scute` (`16` durability cost).
func InteractItemWithMob2612(mobType, itemID string, isBaby, isSaddled, isSheared, hasStewFlower bool, rng *rand.Rand) MobItemInteractionResult2612 {
	normMob := canonicalResourceName(mobType)
	normItem := canonicalResourceName(itemID)
	intn := func(n int) int {
		if rng != nil {
			return rng.Intn(n)
		}
		return rand.Intn(n)
	}

	switch normItem {
	case "minecraft:saddle":
		if !isBaby && !isSaddled && IsMobInEntityTypeTag2612(normMob, "minecraft:can_equip_saddle") {
			return MobItemInteractionResult2612{
				Handled:          true,
				ConsumedHeldItem: true,
				EquippedSaddle:   true,
			}
		}

	case "minecraft:shears":
		if isSaddled && IsMobInEntityTypeTag2612(normMob, "minecraft:can_equip_saddle") {
			return MobItemInteractionResult2612{
				Handled:            true,
				RemovedSaddle:      true,
				ToolDurabilityCost: 1,
				DroppedItems:       []world.ItemStack{world.NewNamedItemStack("minecraft:saddle", 1)},
			}
		}
		if !isBaby && !isSheared {
			switch normMob {
			case MobTypeSheep:
				woolCount := 1 + intn(3)
				return MobItemInteractionResult2612{
					Handled:            true,
					ToolDurabilityCost: 1,
					DroppedItems:       []world.ItemStack{world.NewNamedItemStack("minecraft:white_wool", int32(woolCount))},
				}
			case MobTypeMooshroom:
				return MobItemInteractionResult2612{
					Handled:            true,
					ToolDurabilityCost: 1,
					ConvertedToMobType: MobTypeCow,
					DroppedItems:       []world.ItemStack{world.NewNamedItemStack("minecraft:red_mushroom", 5)},
				}
			case MobTypeSnowGolem:
				return MobItemInteractionResult2612{
					Handled:            true,
					ToolDurabilityCost: 1,
					DroppedItems:       []world.ItemStack{world.NewNamedItemStack("minecraft:carved_pumpkin", 1)},
				}
			case MobTypeBogged:
				return MobItemInteractionResult2612{
					Handled:            true,
					ToolDurabilityCost: 1,
					DroppedItems:       []world.ItemStack{world.NewNamedItemStack("minecraft:red_mushroom", 1), world.NewNamedItemStack("minecraft:brown_mushroom", 1)},
				}
			}
		}

	case "minecraft:bucket":
		if !isBaby && (normMob == MobTypeCow || normMob == MobTypeMooshroom || normMob == MobTypeGoat) {
			return MobItemInteractionResult2612{
				Handled:           true,
				ConsumedHeldItem:  true,
				ReplacementItemID: "minecraft:milk_bucket",
			}
		}

	case "minecraft:water_bucket":
		switch normMob {
		case MobTypeAxolotl, MobTypeCod, MobTypeSalmon, MobTypePufferfish, MobTypeTropicalFish, MobTypeTadpole:
			short := strings.TrimPrefix(normMob, "minecraft:")
			return MobItemInteractionResult2612{
				Handled:            true,
				ConsumedHeldItem:   true,
				ReplacementItemID:  "minecraft:" + short + "_bucket",
				ScoopedMobIntoItem: true,
				PersistenceGranted: true,
			}
		}

	case "minecraft:bowl":
		if !isBaby && normMob == MobTypeMooshroom {
			stew := "minecraft:mushroom_stew"
			if hasStewFlower {
				stew = "minecraft:suspicious_stew"
			}
			return MobItemInteractionResult2612{
				Handled:           true,
				ConsumedHeldItem:  true,
				ReplacementItemID: stew,
			}
		}

	case "minecraft:glass_bottle":
		if normMob == EntityTypeAreaEffectCloud {
			return MobItemInteractionResult2612{
				Handled:           true,
				ConsumedHeldItem:  true,
				ReplacementItemID: "minecraft:dragon_breath",
			}
		}

	case "minecraft:brush":
		if normMob == MobTypeArmadillo {
			return MobItemInteractionResult2612{
				Handled:            true,
				ToolDurabilityCost: 16,
				DroppedItems:       []world.ItemStack{world.NewNamedItemStack("minecraft:armadillo_scute", 1)},
			}
		}
	}
	return MobItemInteractionResult2612{}
}
