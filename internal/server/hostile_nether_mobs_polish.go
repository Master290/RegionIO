package server

import (
	"math"
	"math/rand"
	"strings"

	"regionio/internal/world"
)

// --- 1. Hitboxes, EyeHeights & Base Attributes from 26.1.2.jar (EntityType.class & <Mob>.createAttributes) ---

// MobHitboxAndAttributes2612 holds the exact hitbox dimensions (`Width`, `Height`,
// `EyeHeight`) and base attributes (`MaxHealth`, `MovementSpeed`, `AttackDamage`,
// `FollowRange`, `Armor`, `KnockbackResistance`, `StepHeight`, `XPReward`) extracted
// from `EntityType.class` and `<Mob>.createAttributes()` in `26.1.2.jar`.
type MobHitboxAndAttributes2612 struct {
	TypeName            string
	Width               float32
	Height              float32
	EyeHeight           float32
	MaxHealth           float32
	MovementSpeed       float64
	AttackDamage        float32
	FollowRange         float64
	Armor               float32
	KnockbackResistance float64
	StepHeight          float32
	XPReward            int32
}

var hostileNetherMobSpecs2612 = map[string]MobHitboxAndAttributes2612{
	MobTypeEnderman: {
		TypeName:      MobTypeEnderman,
		Width:         0.6,
		Height:        2.9,
		EyeHeight:     2.55,
		MaxHealth:     40.0,
		MovementSpeed: 0.3,
		AttackDamage:  7.0,
		FollowRange:   64.0,
		StepHeight:    1.0,
		XPReward:      5,
	},
	MobTypeEndermite: {
		TypeName:      MobTypeEndermite,
		Width:         0.4,
		Height:        0.3,
		EyeHeight:     0.13,
		MaxHealth:     8.0,
		MovementSpeed: 0.25,
		AttackDamage:  2.0,
		FollowRange:   16.0,
		StepHeight:    0.6,
		XPReward:      3,
	},
	MobTypeSilverfish: {
		TypeName:      MobTypeSilverfish,
		Width:         0.4,
		Height:        0.3,
		EyeHeight:     0.13,
		MaxHealth:     8.0,
		MovementSpeed: 0.25,
		AttackDamage:  1.0,
		FollowRange:   16.0,
		StepHeight:    0.6,
		XPReward:      5,
	},
	MobTypeShulker: {
		TypeName:            MobTypeShulker,
		Width:               1.0,
		Height:              1.0,
		EyeHeight:           0.5,
		MaxHealth:           30.0,
		MovementSpeed:       0.0,
		AttackDamage:        4.0,
		FollowRange:         16.0,
		Armor:               20.0, // COVERED_ARMOR_MODIFIER when shell is closed (peek == 0)
		KnockbackResistance: 1.0,
		StepHeight:          0.6,
		XPReward:            5,
	},
	EntityTypeShulkerBullet: {
		TypeName:  EntityTypeShulkerBullet,
		Width:     0.3125,
		Height:    0.3125,
		EyeHeight: 0.3125 * 0.85,
	},
	MobTypePhantom: {
		TypeName:      MobTypePhantom,
		Width:         0.9,
		Height:        0.5,
		EyeHeight:     0.175,
		MaxHealth:     20.0,
		MovementSpeed: 0.28,
		AttackDamage:  6.0,
		FollowRange:   64.0,
		StepHeight:    0.6,
		XPReward:      5,
	},
	MobTypePiglin: {
		TypeName:      MobTypePiglin,
		Width:         0.6,
		Height:        1.95,
		EyeHeight:     1.79,
		MaxHealth:     16.0,
		MovementSpeed: 0.35,
		AttackDamage:  5.0,
		FollowRange:   16.0,
		StepHeight:    0.6,
		XPReward:      5,
	},
	MobTypePiglinBrute: {
		TypeName:      MobTypePiglinBrute,
		Width:         0.6,
		Height:        1.95,
		EyeHeight:     1.79,
		MaxHealth:     50.0,
		MovementSpeed: 0.35,
		AttackDamage:  7.0,
		FollowRange:   12.0,
		StepHeight:    0.6,
		XPReward:      20,
	},
	MobTypeZombifiedPiglin: {
		TypeName:      MobTypeZombifiedPiglin,
		Width:         0.6,
		Height:        1.95,
		EyeHeight:     1.79,
		MaxHealth:     20.0,
		MovementSpeed: 0.23,
		AttackDamage:  5.0,
		FollowRange:   35.0,
		Armor:         2.0,
		StepHeight:    0.6,
		XPReward:      5,
	},
	MobTypeHoglin: {
		TypeName:            MobTypeHoglin,
		Width:               1.3964844,
		Height:              1.4,
		EyeHeight:           1.4 * 0.85,
		MaxHealth:           40.0,
		MovementSpeed:       0.3,
		AttackDamage:        6.0,
		FollowRange:         16.0,
		KnockbackResistance: 0.6,
		StepHeight:          0.6,
		XPReward:            5,
	},
	MobTypeZoglin: {
		TypeName:            MobTypeZoglin,
		Width:               1.3964844,
		Height:              1.4,
		EyeHeight:           1.4 * 0.85,
		MaxHealth:           40.0,
		MovementSpeed:       0.3,
		AttackDamage:        6.0,
		FollowRange:         16.0,
		KnockbackResistance: 0.6,
		StepHeight:          0.6,
		XPReward:            5,
	},
	MobTypeStrider: {
		TypeName:      MobTypeStrider,
		Width:         0.9,
		Height:        1.7,
		EyeHeight:     1.7 * 0.85,
		MaxHealth:     20.0,
		MovementSpeed: 0.175,
		FollowRange:   16.0,
		StepHeight:    0.6,
		XPReward:      2,
	},
	MobTypeRavager: {
		TypeName:            MobTypeRavager,
		Width:               1.95,
		Height:              2.2,
		EyeHeight:           2.2 * 0.85,
		MaxHealth:           100.0,
		MovementSpeed:       0.3,
		AttackDamage:        12.0,
		FollowRange:         32.0,
		KnockbackResistance: 0.75,
		StepHeight:          1.0,
		XPReward:            20,
	},
	MobTypeEvoker: {
		TypeName:      MobTypeEvoker,
		Width:         0.6,
		Height:        1.95,
		EyeHeight:     1.95 * 0.85,
		MaxHealth:     24.0,
		MovementSpeed: 0.5,
		FollowRange:   12.0,
		StepHeight:    0.6,
		XPReward:      10,
	},
	EntityTypeEvokerFangs: {
		TypeName:  EntityTypeEvokerFangs,
		Width:     0.5,
		Height:    0.8,
		EyeHeight: 0.8 * 0.85,
	},
	MobTypeVex: {
		TypeName:      MobTypeVex,
		Width:         0.4,
		Height:        0.8,
		EyeHeight:     0.51875,
		MaxHealth:     14.0,
		MovementSpeed: 0.22,
		AttackDamage:  4.0,
		FollowRange:   16.0,
		StepHeight:    0.6,
		XPReward:      5,
	},
	MobTypeWitch: {
		TypeName:      MobTypeWitch,
		Width:         0.6,
		Height:        1.95,
		EyeHeight:     1.62,
		MaxHealth:     26.0,
		MovementSpeed: 0.25,
		FollowRange:   16.0,
		StepHeight:    0.6,
		XPReward:      5,
	},
	MobTypeCreeper: {
		TypeName:      MobTypeCreeper,
		Width:         0.6,
		Height:        1.7,
		EyeHeight:     1.7 * 0.85,
		MaxHealth:     20.0,
		MovementSpeed: 0.25,
		FollowRange:   16.0,
		StepHeight:    0.6,
		XPReward:      5,
	},
	MobTypeSkeleton: {
		TypeName:      MobTypeSkeleton,
		Width:         0.6,
		Height:        1.99,
		EyeHeight:     1.74,
		MaxHealth:     20.0,
		MovementSpeed: 0.25,
		AttackDamage:  2.0,
		FollowRange:   16.0,
		StepHeight:    0.6,
		XPReward:      5,
	},
	MobTypeStray: {
		TypeName:      MobTypeStray,
		Width:         0.6,
		Height:        1.99,
		EyeHeight:     1.74,
		MaxHealth:     20.0,
		MovementSpeed: 0.25,
		AttackDamage:  2.0,
		FollowRange:   16.0,
		StepHeight:    0.6,
		XPReward:      5,
	},
	MobTypeWitherSkeleton: {
		TypeName:      MobTypeWitherSkeleton,
		Width:         0.7,
		Height:        2.4,
		EyeHeight:     2.1,
		MaxHealth:     20.0,
		MovementSpeed: 0.25,
		AttackDamage:  4.0,
		FollowRange:   16.0,
		StepHeight:    0.6,
		XPReward:      5,
	},
	MobTypeSpider: {
		TypeName:      MobTypeSpider,
		Width:         1.4,
		Height:        0.9,
		EyeHeight:     0.65,
		MaxHealth:     16.0,
		MovementSpeed: 0.3,
		AttackDamage:  2.0,
		FollowRange:   16.0,
		StepHeight:    0.6,
		XPReward:      5,
	},
	MobTypeCaveSpider: {
		TypeName:      MobTypeCaveSpider,
		Width:         0.7,
		Height:        0.5,
		EyeHeight:     0.45,
		MaxHealth:     12.0,
		MovementSpeed: 0.3,
		AttackDamage:  2.0,
		FollowRange:   16.0,
		StepHeight:    0.6,
		XPReward:      5,
	},
	MobTypeBlaze: {
		TypeName:      MobTypeBlaze,
		Width:         0.6,
		Height:        1.8,
		EyeHeight:     1.8 * 0.85,
		MaxHealth:     20.0,
		MovementSpeed: 0.23,
		AttackDamage:  6.0,
		FollowRange:   48.0,
		StepHeight:    0.6,
		XPReward:      10,
	},
	MobTypeGhast: {
		TypeName:      MobTypeGhast,
		Width:         4.0,
		Height:        4.0,
		EyeHeight:     2.6,
		MaxHealth:     10.0,
		MovementSpeed: 0.06,
		FollowRange:   100.0,
		StepHeight:    0.6,
		XPReward:      5,
	},
	MobTypeMagmaCube: {
		TypeName:      MobTypeMagmaCube,
		Width:         0.52,
		Height:        0.52,
		EyeHeight:     0.325,
		MaxHealth:     16.0, // Size 4 default (size^2)
		MovementSpeed: 0.2,
		AttackDamage:  6.0,  // size + 2
		FollowRange:   16.0,
		Armor:         12.0, // 3 * size
		StepHeight:    0.6,
		XPReward:      4,
	},
	MobTypeSlime: {
		TypeName:      MobTypeSlime,
		Width:         0.52,
		Height:        0.52,
		EyeHeight:     0.325,
		MaxHealth:     16.0,
		MovementSpeed: 0.2,
		AttackDamage:  4.0,
		FollowRange:   16.0,
		StepHeight:    0.6,
		XPReward:      4,
	},
	MobTypeGuardian: {
		TypeName:      MobTypeGuardian,
		Width:         0.85,
		Height:        0.85,
		EyeHeight:     0.425,
		MaxHealth:     30.0,
		MovementSpeed: 0.5,
		AttackDamage:  6.0,
		FollowRange:   16.0,
		StepHeight:    0.6,
		XPReward:      10,
	},
	MobTypeElderGuardian: {
		TypeName:      MobTypeElderGuardian,
		Width:         1.9975,
		Height:        1.9975,
		EyeHeight:     0.99875,
		MaxHealth:     80.0,
		MovementSpeed: 0.3,
		AttackDamage:  8.0,
		FollowRange:   16.0,
		StepHeight:    0.6,
		XPReward:      10,
	},
	MobTypeIronGolem: {
		TypeName:            MobTypeIronGolem,
		Width:               1.4,
		Height:              2.7,
		EyeHeight:           2.7 * 0.85,
		MaxHealth:           100.0,
		MovementSpeed:       0.25,
		AttackDamage:        15.0,
		FollowRange:         16.0,
		KnockbackResistance: 1.0,
		StepHeight:          1.0,
		XPReward:            0,
	},
	MobTypeSnowGolem: {
		TypeName:      MobTypeSnowGolem,
		Width:         0.7,
		Height:        1.9,
		EyeHeight:     1.7,
		MaxHealth:     4.0,
		MovementSpeed: 0.2,
		FollowRange:   16.0,
		StepHeight:    0.6,
		XPReward:      0,
	},
	MobTypeCopperGolem: {
		TypeName:      MobTypeCopperGolem,
		Width:         0.49,
		Height:        0.98,
		EyeHeight:     0.8125,
		MaxHealth:     12.0,
		MovementSpeed: 0.2,
		FollowRange:   16.0,
		StepHeight:    1.0,
		XPReward:      0,
	},
	MobTypeSkeletonHorse: {
		TypeName:      MobTypeSkeletonHorse,
		Width:         1.3964844,
		Height:        1.6,
		EyeHeight:     1.52,
		MaxHealth:     15.0,
		MovementSpeed: 0.2,
		FollowRange:   16.0,
		StepHeight:    1.0,
		XPReward:      2,
	},
	MobTypeZombieHorse: {
		TypeName:      MobTypeZombieHorse,
		Width:         1.3964844,
		Height:        1.6,
		EyeHeight:     1.52,
		MaxHealth:     25.0,
		MovementSpeed: 0.2,
		FollowRange:   16.0,
		StepHeight:    1.0,
		XPReward:      2,
	},
}

// ResolveMobHitboxAndAttributes2612 returns the verified 26.1.2 hitbox and
// base attribute specification for `typeName`.
func ResolveMobHitboxAndAttributes2612(typeName string) (MobHitboxAndAttributes2612, bool) {
	spec, ok := hostileNetherMobSpecs2612[canonicalResourceName(typeName)]
	return spec, ok
}

// ScaleSlimeOrMagmaCubeDimensions2612 returns the scaled `(width, height, eyeHeight, maxHealth, attackDamage, armor)`
// for a `minecraft:slime` or `minecraft:magma_cube` of `size` (`1`, `2`, or `4`).
func ScaleSlimeOrMagmaCubeDimensions2612(typeName string, size int) (width, height, eyeHeight, maxHealth, attackDamage, armor float32) {
	if size < 1 {
		size = 1
	}
	s := float32(size)
	width = 0.52 * s
	height = 0.52 * s
	eyeHeight = 0.325 * s
	maxHealth = s * s
	if canonicalResourceName(typeName) == MobTypeMagmaCube {
		attackDamage = s + 2.0
		armor = 3.0 * s
	} else {
		attackDamage = s
		armor = 0.0
	}
	return width, height, eyeHeight, maxHealth, attackDamage, armor
}

// ScalePhantomDimensions2612 returns the scaled `(width, height, attackDamage)`
// for a `minecraft:phantom` with `size` (`0..64`, matching `Phantom.updatePhantomSizeInfo`:
// `width = 0.9 + 0.2*size`, `height = 0.5 + 0.1*size`, `attackDamage = 6 + size`).
func ScalePhantomDimensions2612(size int) (width, height, attackDamage float32) {
	if size < 0 {
		size = 0
	}
	s := float32(size)
	return 0.9 + 0.2*s, 0.5 + 0.1*s, 6.0 + s
}

// --- 2. Sound Event Registry for Hostile, Nether, Golem & Undead Horse Mobs ---

// MobSoundSet2612 holds the canonical sound event identifiers for a mob in 26.1.2.
type MobSoundSet2612 struct {
	Ambient string
	Hurt    string
	Death   string
	Special string
}

// ResolveMobSoundSet2612 returns the canonical 26.1.2 sound event IDs for `typeName`.
func ResolveMobSoundSet2612(typeName string) (MobSoundSet2612, bool) {
	switch canonicalResourceName(typeName) {
	case MobTypeEnderman:
		return MobSoundSet2612{Ambient: SoundEndermanAmbient, Hurt: SoundEndermanHurt, Death: SoundEndermanDeath, Special: SoundEndermanTeleport}, true
	case MobTypeEndermite:
		return MobSoundSet2612{Ambient: SoundEndermiteAmbient, Hurt: SoundEndermiteHurt, Death: SoundEndermiteDeath, Special: SoundEndermiteStep}, true
	case MobTypeSilverfish:
		return MobSoundSet2612{
			Ambient: "minecraft:entity.silverfish.ambient",
			Hurt:    "minecraft:entity.silverfish.hurt",
			Death:   "minecraft:entity.silverfish.death",
			Special: "minecraft:entity.silverfish.step",
		}, true
	case MobTypeShulker:
		return MobSoundSet2612{
			Ambient: "minecraft:entity.shulker.ambient",
			Hurt:    "minecraft:entity.shulker.hurt",
			Death:   "minecraft:entity.shulker.death",
			Special: "minecraft:entity.shulker.teleport",
		}, true
	case MobTypePhantom:
		return MobSoundSet2612{Ambient: SoundPhantomAmbient, Hurt: SoundPhantomHurt, Death: SoundPhantomDeath, Special: SoundPhantomSwoop}, true
	case MobTypePiglin:
		return MobSoundSet2612{
			Ambient: "minecraft:entity.piglin.ambient",
			Hurt:    "minecraft:entity.piglin.hurt",
			Death:   "minecraft:entity.piglin.death",
			Special: "minecraft:entity.piglin.admiring_item",
		}, true
	case MobTypePiglinBrute:
		return MobSoundSet2612{
			Ambient: "minecraft:entity.piglin_brute.ambient",
			Hurt:    "minecraft:entity.piglin_brute.hurt",
			Death:   "minecraft:entity.piglin_brute.death",
			Special: "minecraft:entity.piglin_brute.angry",
		}, true
	case MobTypeZombifiedPiglin:
		return MobSoundSet2612{
			Ambient: "minecraft:entity.zombified_piglin.ambient",
			Hurt:    "minecraft:entity.zombified_piglin.hurt",
			Death:   "minecraft:entity.zombified_piglin.death",
			Special: "minecraft:entity.zombified_piglin.angry",
		}, true
	case MobTypeHoglin:
		return MobSoundSet2612{
			Ambient: "minecraft:entity.hoglin.ambient",
			Hurt:    "minecraft:entity.hoglin.hurt",
			Death:   "minecraft:entity.hoglin.death",
			Special: "minecraft:entity.hoglin.attack",
		}, true
	case MobTypeZoglin:
		return MobSoundSet2612{
			Ambient: "minecraft:entity.zoglin.ambient",
			Hurt:    "minecraft:entity.zoglin.hurt",
			Death:   "minecraft:entity.zoglin.death",
			Special: "minecraft:entity.zoglin.attack",
		}, true
	case MobTypeStrider:
		return MobSoundSet2612{
			Ambient: "minecraft:entity.strider.ambient",
			Hurt:    "minecraft:entity.strider.hurt",
			Death:   "minecraft:entity.strider.death",
			Special: "minecraft:entity.strider.step_lava",
		}, true
	case MobTypeRavager:
		return MobSoundSet2612{
			Ambient: "minecraft:entity.ravager.ambient",
			Hurt:    "minecraft:entity.ravager.hurt",
			Death:   "minecraft:entity.ravager.death",
			Special: "minecraft:entity.ravager.roar",
		}, true
	case MobTypeEvoker:
		return MobSoundSet2612{
			Ambient: "minecraft:entity.evoker.ambient",
			Hurt:    "minecraft:entity.evoker.hurt",
			Death:   "minecraft:entity.evoker.death",
			Special: "minecraft:entity.evoker.prepare_attack",
		}, true
	case MobTypeVex:
		return MobSoundSet2612{
			Ambient: "minecraft:entity.vex.ambient",
			Hurt:    "minecraft:entity.vex.hurt",
			Death:   "minecraft:entity.vex.death",
			Special: "minecraft:entity.vex.charge",
		}, true
	case MobTypeWitch:
		return MobSoundSet2612{
			Ambient: "minecraft:entity.witch.ambient",
			Hurt:    "minecraft:entity.witch.hurt",
			Death:   "minecraft:entity.witch.death",
			Special: "minecraft:entity.witch.throw",
		}, true
	case MobTypeCreeper:
		return MobSoundSet2612{
			Ambient: "",
			Hurt:    "minecraft:entity.creeper.hurt",
			Death:   "minecraft:entity.creeper.death",
			Special: "minecraft:entity.creeper.primed",
		}, true
	case MobTypeSkeleton:
		return MobSoundSet2612{
			Ambient: "minecraft:entity.skeleton.ambient",
			Hurt:    "minecraft:entity.skeleton.hurt",
			Death:   "minecraft:entity.skeleton.death",
			Special: "minecraft:entity.skeleton.shoot",
		}, true
	case MobTypeStray:
		return MobSoundSet2612{
			Ambient: "minecraft:entity.stray.ambient",
			Hurt:    "minecraft:entity.stray.hurt",
			Death:   "minecraft:entity.stray.death",
			Special: "minecraft:entity.stray.step",
		}, true
	case MobTypeWitherSkeleton:
		return MobSoundSet2612{
			Ambient: "minecraft:entity.wither_skeleton.ambient",
			Hurt:    "minecraft:entity.wither_skeleton.hurt",
			Death:   "minecraft:entity.wither_skeleton.death",
			Special: "minecraft:entity.wither_skeleton.step",
		}, true
	case MobTypeSpider, MobTypeCaveSpider:
		return MobSoundSet2612{
			Ambient: "minecraft:entity.spider.ambient",
			Hurt:    "minecraft:entity.spider.hurt",
			Death:   "minecraft:entity.spider.death",
			Special: "minecraft:entity.spider.step",
		}, true
	case MobTypeBlaze:
		return MobSoundSet2612{
			Ambient: "minecraft:entity.blaze.ambient",
			Hurt:    "minecraft:entity.blaze.hurt",
			Death:   "minecraft:entity.blaze.death",
			Special: "minecraft:entity.blaze.shoot",
		}, true
	case MobTypeGhast:
		return MobSoundSet2612{
			Ambient: "minecraft:entity.ghast.ambient",
			Hurt:    "minecraft:entity.ghast.hurt",
			Death:   "minecraft:entity.ghast.death",
			Special: "minecraft:entity.ghast.warn",
		}, true
	case MobTypeMagmaCube:
		return MobSoundSet2612{
			Ambient: "",
			Hurt:    "minecraft:entity.magma_cube.hurt",
			Death:   "minecraft:entity.magma_cube.death",
			Special: "minecraft:entity.magma_cube.squish",
		}, true
	case MobTypeGuardian:
		return MobSoundSet2612{
			Ambient: "minecraft:entity.guardian.ambient",
			Hurt:    "minecraft:entity.guardian.hurt",
			Death:   "minecraft:entity.guardian.death",
			Special: "minecraft:entity.guardian.attack",
		}, true
	case MobTypeElderGuardian:
		return MobSoundSet2612{
			Ambient: "minecraft:entity.elder_guardian.ambient",
			Hurt:    "minecraft:entity.elder_guardian.hurt",
			Death:   "minecraft:entity.elder_guardian.death",
			Special: "minecraft:entity.elder_guardian.curse",
		}, true
	case MobTypeIronGolem:
		return MobSoundSet2612{
			Ambient: "",
			Hurt:    "minecraft:entity.iron_golem.hurt",
			Death:   "minecraft:entity.iron_golem.death",
			Special: "minecraft:entity.iron_golem.attack",
		}, true
	case MobTypeSnowGolem:
		return MobSoundSet2612{
			Ambient: "minecraft:entity.snow_golem.ambient",
			Hurt:    "minecraft:entity.snow_golem.hurt",
			Death:   "minecraft:entity.snow_golem.death",
			Special: "minecraft:entity.snow_golem.shoot",
		}, true
	case MobTypeCopperGolem:
		return MobSoundSet2612{
			Ambient: "minecraft:entity.copper_golem.ambient",
			Hurt:    SoundCopperGolemHurt,
			Death:   SoundCopperGolemDeath,
			Special: "minecraft:entity.copper_golem.spin",
		}, true
	case MobTypeSkeletonHorse:
		return MobSoundSet2612{
			Ambient: "minecraft:entity.skeleton_horse.ambient",
			Hurt:    "minecraft:entity.skeleton_horse.hurt",
			Death:   "minecraft:entity.skeleton_horse.death",
			Special: "minecraft:entity.skeleton_horse.swim",
		}, true
	case MobTypeZombieHorse:
		return MobSoundSet2612{
			Ambient: "minecraft:entity.zombie_horse.ambient",
			Hurt:    "minecraft:entity.zombie_horse.hurt",
			Death:   "minecraft:entity.zombie_horse.death",
			Special: "minecraft:entity.horse.gallop",
		}, true
	default:
		return MobSoundSet2612{}, false
	}
}

// --- 3. Zombie Horse 26.1.2 Attribute Generators & Piglin/Hoglin Repellents ---

// GenerateZombieHorseJumpStrength2612 implements `ZombieHorse.generateZombieHorseJumpStrength`
// from `26.1.2.jar`: `0.5 + (rng + rng + rng) * (0.2 / 3.0)` (range `[0.5, 0.7]`).
func GenerateZombieHorseJumpStrength2612(rng *rand.Rand) float64 {
	r := func() float64 {
		if rng != nil {
			return rng.Float64()
		}
		return rand.Float64()
	}
	const perRandom = 0.06666666666666667
	return 0.5 + r()*perRandom + r()*perRandom + r()*perRandom
}

// GenerateZombieHorseSpeed2612 implements `ZombieHorse.generateZombieHorseSpeed`
// from `26.1.2.jar`: `(9.0 + rng + rng + rng) / 42.15999984741211` (range `[0.21347, 0.28463]`).
func GenerateZombieHorseSpeed2612(rng *rand.Rand) float64 {
	r := func() float64 {
		if rng != nil {
			return rng.Float64()
		}
		return rand.Float64()
	}
	return (9.0 + r() + r() + r()) / 42.15999984741211
}

// IsPiglinRepellentBlock2612 reports whether `state` is in `#minecraft:piglin_repellents`
// in vanilla 26.1.2 (`soul_fire`, `soul_torch`, `soul_wall_torch`, `soul_lantern`,
// and lit `soul_campfire`).
func IsPiglinRepellentBlock2612(state uint16) bool {
	switch world.BlockName(state) {
	case "minecraft:soul_fire", "minecraft:soul_torch", "minecraft:soul_wall_torch", "minecraft:soul_lantern":
		return true
	case "minecraft:soul_campfire":
		return world.BlockProperties(state)["lit"] != "false"
	default:
		return false
	}
}

// IsHoglinRepellentBlock2612 reports whether `state` is in `#minecraft:hoglin_repellents`
// in vanilla 26.1.2 (`warped_fungus`, `potted_warped_fungus`, `nether_portal`, `respawn_anchor`).
func IsHoglinRepellentBlock2612(state uint16) bool {
	switch world.BlockName(state) {
	case "minecraft:warped_fungus", "minecraft:potted_warped_fungus", "minecraft:nether_portal", "minecraft:respawn_anchor":
		return true
	default:
		return false
	}
}

// FindNearestRepellentBlock2612 searches within `(radiusXZ, radiusY)` around
// `(cx, cy, cz)` for the closest repellent block matching `pred` (used by
// Piglin soul-fire avoidance and Hoglin warped-fungus/portal/respawn-anchor avoidance).
func FindNearestRepellentBlock2612(cache *world.Cache, cx, cy, cz, radiusXZ, radiusY int, pred func(uint16) bool) (rx, ry, rz int, found bool) {
	if cache == nil || pred == nil {
		return 0, 0, 0, false
	}
	bestDistSq := math.MaxInt
	for dy := -radiusY; dy <= radiusY; dy++ {
		by := cy + dy
		if by < world.MinY || by >= world.MinY+world.WorldHeight {
			continue
		}
		for dx := -radiusXZ; dx <= radiusXZ; dx++ {
			for dz := -radiusXZ; dz <= radiusXZ; dz++ {
				bx := cx + dx
				bz := cz + dz
				if !pred(cache.GetBlock(bx, by, bz)) {
					continue
				}
				d2 := dx*dx + dy*dy + dz*dz
				if d2 < bestDistSq {
					bestDistSq = d2
					rx, ry, rz = bx, by, bz
					found = true
				}
			}
		}
	}
	return rx, ry, rz, found
}

// --- 4. Looting-Aware Loot Drops for Hostile, Nether, Golem & Undead Horse Mobs ---

// RollHostileNetherMobDrops2612 rolls the vanilla 26.1.2 loot table drops for
// the mobs in Issue #25, accounting for `lootingLevel`, `onFire`, `killedByPlayer`,
// `skeletonKill` (Creeper music disc), and `chargedCreeperKill` (mob heads).
func RollHostileNetherMobDrops2612(
	typeName string,
	lootingLevel int,
	onFire bool,
	killedByPlayer bool,
	skeletonKill bool,
	chargedCreeperKill bool,
	rng *rand.Rand,
) []world.ItemStack {
	if lootingLevel < 0 {
		lootingLevel = 0
	}
	intn := func(n int) int {
		if n <= 1 {
			return 0
		}
		if rng != nil {
			return rng.Intn(n)
		}
		return rand.Intn(n)
	}
	float32r := func() float32 {
		if rng != nil {
			return rng.Float32()
		}
		return rand.Float32()
	}

	var drops []world.ItemStack
	addDrop := func(itemID string, count int) {
		if count <= 0 || itemID == "" {
			return
		}
		if st := world.NewNamedItemStack(itemID, int32(count)); !st.IsEmpty() {
			drops = append(drops, st)
		}
	}

	norm := canonicalResourceName(typeName)
	switch norm {
	case MobTypeEnderman:
		addDrop("minecraft:ender_pearl", intn(2+lootingLevel))

	case MobTypeShulker:
		chance := float32(0.5) + float32(lootingLevel)*0.0625
		if float32r() < chance {
			addDrop("minecraft:shulker_shell", 1)
		}

	case MobTypePhantom:
		if killedByPlayer {
			addDrop("minecraft:phantom_membrane", intn(2+lootingLevel))
		}

	case MobTypeBlaze:
		if killedByPlayer {
			addDrop("minecraft:blaze_rod", intn(2+lootingLevel))
		}

	case MobTypeGhast:
		addDrop("minecraft:ghast_tear", intn(2+lootingLevel))
		addDrop("minecraft:gunpowder", intn(3+lootingLevel))

	case MobTypeMagmaCube:
		cnt := intn(4) - 2
		if lootingLevel > 0 {
			cnt += intn(lootingLevel + 1)
		}
		if cnt > 0 {
			addDrop("minecraft:magma_cream", cnt)
		}

	case MobTypeGuardian:
		addDrop("minecraft:prismarine_shard", intn(3+lootingLevel))
		if intn(5) < 2 {
			if onFire {
				addDrop("minecraft:cooked_cod", 1)
			} else {
				addDrop("minecraft:cod", 1)
			}
		} else if intn(5) < 2 {
			addDrop("minecraft:prismarine_crystals", 1)
		}

	case MobTypeElderGuardian:
		addDrop("minecraft:prismarine_shard", intn(3+lootingLevel))
		if killedByPlayer {
			addDrop("minecraft:wet_sponge", 1)
		}
		if float32r() < 0.20 {
			addDrop(TideArmorTrimTemplateItemID, 1)
		}

	case MobTypeCreeper:
		addDrop("minecraft:gunpowder", intn(3+lootingLevel))
		if skeletonKill {
			addDrop("minecraft:music_disc_cat", 1)
		}
		if chargedCreeperKill {
			addDrop("minecraft:creeper_head", 1)
		}

	case MobTypeSkeleton:
		addDrop("minecraft:arrow", intn(3+lootingLevel))
		addDrop("minecraft:bone", intn(3+lootingLevel))
		if chargedCreeperKill {
			addDrop("minecraft:skeleton_skull", 1)
		}

	case MobTypeStray:
		addDrop("minecraft:arrow", intn(3+lootingLevel))
		addDrop("minecraft:bone", intn(3+lootingLevel))
		if killedByPlayer {
			chance := float32(2*lootingLevel+1) / float32(2*lootingLevel+2)
			if float32r() < chance {
				addDrop("minecraft:tipped_arrow", 1)
			}
		}

	case MobTypeWitherSkeleton:
		if intn(3) == 0 {
			addDrop("minecraft:coal", 1+intn(1+lootingLevel))
		}
		addDrop("minecraft:bone", intn(3+lootingLevel))
		if chargedCreeperKill {
			addDrop("minecraft:wither_skeleton_skull", 1)
		} else if killedByPlayer {
			skullChance := float32(0.025) + float32(lootingLevel)*0.01
			if float32r() < skullChance {
				addDrop("minecraft:wither_skeleton_skull", 1)
			}
		}

	case MobTypeSpider, MobTypeCaveSpider:
		addDrop("minecraft:string", intn(3+lootingLevel))
		if killedByPlayer && (intn(3) == 0 || intn(1+lootingLevel) > 0) {
			addDrop("minecraft:spider_eye", 1)
		}

	case MobTypeWitch:
		pool := [7]string{
			"minecraft:glowstone_dust",
			"minecraft:sugar",
			"minecraft:redstone",
			"minecraft:spider_eye",
			"minecraft:glass_bottle",
			"minecraft:gunpowder",
			"minecraft:stick",
		}
		rolls := 1 + intn(3)
		for i := 0; i < rolls; i++ {
			item := pool[intn(len(pool))]
			addDrop(item, 1+intn(2+lootingLevel))
		}

	case MobTypeRavager:
		addDrop("minecraft:saddle", 1)

	case MobTypeEvoker:
		addDrop("minecraft:totem_of_undying", 1)
		if killedByPlayer {
			addDrop("minecraft:emerald", intn(2+lootingLevel))
		}

	case MobTypeHoglin:
		pork := 2 + intn(3+lootingLevel)
		if onFire {
			addDrop("minecraft:cooked_porkchop", pork)
		} else {
			addDrop("minecraft:porkchop", pork)
		}
		addDrop("minecraft:leather", intn(2+lootingLevel))

	case MobTypeZoglin:
		addDrop("minecraft:rotten_flesh", 1+intn(3+lootingLevel))

	case MobTypePiglin:
		if chargedCreeperKill {
			addDrop("minecraft:piglin_head", 1)
		}

	case MobTypeZombifiedPiglin:
		addDrop("minecraft:rotten_flesh", intn(2+lootingLevel))
		addDrop("minecraft:gold_nugget", intn(2+lootingLevel))
		if killedByPlayer && float32r() < 0.025+float32(lootingLevel)*0.01 {
			addDrop("minecraft:gold_ingot", 1)
		}

	case MobTypeStrider:
		addDrop("minecraft:string", 2+intn(4+lootingLevel))

	case MobTypeIronGolem:
		// Iron Golem drops 3..5 iron ingots and 0..2 poppies (unaffected by Looting).
		addDrop("minecraft:iron_ingot", 3+intn(3))
		addDrop("minecraft:poppy", intn(3))

	case MobTypeSnowGolem:
		addDrop("minecraft:snowball", intn(16))

	case MobTypeSkeletonHorse:
		addDrop("minecraft:bone", intn(3+lootingLevel))

	case MobTypeZombieHorse:
		addDrop("minecraft:rotten_flesh", intn(3+lootingLevel))
	}

	return drops
}

// CaveSpiderPoisonDurationTicks2612 returns the Poison I duration in ticks
// applied by `CaveSpider.doHurtTarget` in vanilla 26.1.2 (`0` on Easy, `140` = 7s
// on Normal, `300` = 15s on Hard).
func CaveSpiderPoisonDurationTicks2612(difficulty Difficulty) int32 {
	switch difficulty {
	case DifficultyNormal:
		return 140
	case DifficultyHard:
		return 300
	default:
		return 0
	}
}

// IsInfestedHostStoneBlock2612 reports whether `blockName` is one of the 6
// blocks Silverfish can infest (`stone`, `cobblestone`, `stone_bricks`,
// `mossy_stone_bricks`, `cracked_stone_bricks`, `chiseled_stone_bricks`, or
// `deepslate`) and returns the corresponding `minecraft:infested_*` block name.
func InfestedBlockForHostBlock2612(blockName string) (infestedName string, ok bool) {
	norm := strings.TrimPrefix(canonicalResourceName(blockName), "minecraft:")
	switch norm {
	case "stone", "cobblestone", "stone_bricks", "mossy_stone_bricks",
		"cracked_stone_bricks", "chiseled_stone_bricks", "deepslate":
		return "minecraft:infested_" + norm, true
	default:
		return "", false
	}
}
