package server

import (
	"math"
	"math/rand"
	"strings"

	"regionio/internal/world"
)

// Exact Piglin, PiglinBrute, Hoglin, and Zoglin constants verified against
// Minecraft 26.1.2 (`AbstractPiglin`, `Piglin`, `PiglinAi`, `PiglinBrute`,
// `PiglinBruteAi`, `PiglinSpecificSensor`, `HoglinBase`, `Hoglin`, `HoglinAi`,
// `HoglinSpecificSensor`, `Zoglin`, and `BehaviorUtils` in `26.1.2.jar`).
const (
	PiglinEyeHeightAdult2612             = 1.79
	PiglinEyeHeightBaby2612              = 0.78
	PiglinBarterThrowYOffsetBelowEye2612 = 0.30
	PiglinBarterThrowSpeed2612           = 0.30
	PiglinDefaultPickupDelayTicks2612    = 10

	PiglinAdmiringDisabledTicks2612 = 400
	PiglinBabyFleeHurtTicks2612     = 100

	PiglinRepellentRangeH2612    = 8
	PiglinRepellentRangeV2612    = 4
	PiglinZombifiedAvoidDist2612 = 6.0

	PiglinCrossbowHoldMinTicks2612  = 20
	PiglinCrossbowHoldRandTicks2612 = 20 // 20 + nextInt(20) -> 20..39 ticks
	PiglinCrossbowArrowVelocity2612 = 1.6
	PiglinCrossbowBackupDistSq2612  = 25.0 // BackUpIfTooClose.create(5, 0.75F)

	HoglinRepellentRangeH2612     = 8
	HoglinRepellentRangeV2612     = 4
	HoglinAdultMeleeCooldown2612  = 40 // MeleeAttack.create(40)
	HoglinBabyMeleeCooldown2612   = 15 // MeleeAttack.create(15)
	HoglinAttackAnimDuration2612  = 10 // attackAnimationRemainingTicks = 10
	HoglinBaseAttackKnockback2612 = 1.0

	EntityEventHoglinZoglinAttack2612 byte = 4
)

// IsDimensionPiglinSafe2612 reports whether `dimension` does NOT zombify Piglins,
// Piglin Brutes, or Hoglins (`EnvironmentAttributes.PIGLINS_ZOMBIFY == false`,
// which in vanilla 26.1.2 holds only for `minecraft:the_nether`).
func IsDimensionPiglinSafe2612(dimension string) bool {
	return world.NormalizeDimension(dimension) == world.DimensionNether
}

// IsPiglinGuardedContainer2612 reports whether `blockName` is a container block
// in `#minecraft:guarded_by_piglins` whose opening triggers
// `PiglinAi.angerNearbyPiglins(level, player, /* requireVisibility = */ true)`.
func IsPiglinGuardedContainer2612(blockName string) bool {
	norm := canonicalResourceName(blockName)
	if strings.HasSuffix(norm, "shulker_box") || strings.Contains(norm, "copper_chest") {
		return true
	}
	switch norm {
	case "minecraft:chest", "minecraft:trapped_chest", "minecraft:ender_chest", "minecraft:barrel":
		return true
	default:
		return false
	}
}

// IsValidPiglinRepellentState2612 implements `PiglinSpecificSensor.isValidRepellent`
// (`piglin_hoglin_sensors_2612.txt:276-296`):
//   - Block must belong to `#minecraft:piglin_repellents`;
//   - If the block is `minecraft:soul_campfire`, it MUST be lit (`lit=true`,
//     `CampfireBlock.isLitCampfire`).
func IsValidPiglinRepellentState2612(state uint16) bool {
	name := canonicalResourceName(world.BlockName(state))
	if !IsPiglinRepellentBlock(name) {
		return false
	}
	if name == "minecraft:soul_campfire" {
		props := world.BlockProperties(state)
		if props == nil {
			return true
		}
		return props["lit"] == "true"
	}
	return true
}

// ComputePiglinThrowVelocity2612 implements `PiglinAi.throwItems` +
// `PiglinAi.throwItemsTowardPos` + `BehaviorUtils.throwItem`
// (`piglin_2612.txt:1978-2045, 4103-4169`):
//   - Spawn position: `(piglinX, piglinY + eyeHeight - 0.3, piglinZ)`
//   - Aim point: if `hasTargetPos` (`NEAREST_VISIBLE_PLAYER`), `(targetX, targetY + 1.0, targetZ)`;
//     otherwise `getRandomNearbyPos(piglin)` (`±4` X/Z, `±2` Y) + `(0, 1.0, 0)`.
//   - Velocity: `normalize(aim - piglin.position()).multiply(0.3, 0.3, 0.3)`.
func ComputePiglinThrowVelocity2612(
	piglinX, piglinY, piglinZ float64,
	eyeHeight float64,
	hasTargetPos bool,
	targetX, targetY, targetZ float64,
	rng *rand.Rand,
) (spawnX, spawnY, spawnZ, vx, vy, vz float64) {
	spawnX = piglinX
	spawnY = piglinY + eyeHeight - PiglinBarterThrowYOffsetBelowEye2612
	spawnZ = piglinZ

	tx, ty, tz := targetX, targetY, targetZ
	if !hasTargetPos {
		if rng != nil {
			// LandRandomPos.getPos(piglin, 4, 2) samples within [-4..4] x [-2..2] x [-4..4].
			ox := float64(rng.Intn(9) - 4)
			oy := float64(rng.Intn(5) - 2)
			oz := float64(rng.Intn(9) - 4)
			tx = piglinX + ox
			ty = piglinY + oy
			tz = piglinZ + oz
		} else {
			tx, ty, tz = piglinX+1.0, piglinY, piglinZ
		}
	}

	dx := tx - piglinX
	dy := (ty + 1.0) - piglinY
	dz := tz - piglinZ
	mag := math.Sqrt(dx*dx + dy*dy + dz*dz)
	if mag < 1e-9 {
		return spawnX, spawnY, spawnZ, 0, PiglinBarterThrowSpeed2612, 0
	}
	vx = (dx / mag) * PiglinBarterThrowSpeed2612
	vy = (dy / mag) * PiglinBarterThrowSpeed2612
	vz = (dz / mag) * PiglinBarterThrowSpeed2612
	return spawnX, spawnY, spawnZ, vx, vy, vz
}

// RollHoglinBaseAttackDamage2612 implements the damage roll in
// `HoglinBase.hurtAndThrowTarget` (`hoglin_zoglin_2612.txt:10-48`):
//   - Baby (`baby == true`): flat `0.5` (`HoglinBabyAttackDmg`);
//   - Adult (`!baby` and `int(baseDamage) > 0`): `baseDamage/2.0 + float32(rng.Intn(int(baseDamage)))`
//     (`3.0 .. 8.0` for `baseDamage = 6.0`).
func RollHoglinBaseAttackDamage2612(baseDamage float32, baby bool, rng *rand.Rand) float32 {
	if baby {
		return HoglinBabyAttackDmg
	}
	intDmg := int(baseDamage)
	if intDmg <= 0 {
		return baseDamage
	}
	bonus := 0
	if rng != nil {
		bonus = rng.Intn(intDmg)
	} else {
		bonus = intDmg / 2
	}
	return baseDamage/2.0 + float32(bonus)
}

// ComputeHoglinThrowImpulse2612 implements `HoglinBase.throwTarget`
// (`hoglin_zoglin_2612.txt:62-144`):
//   - Babies never throw (`baby == true` -> `thrown = false`).
//   - `effective = attackKnockback - targetKnockbackResistance`; if `effective <= 0`, returns `thrown = false`.
//   - `f = float64(rng.Intn(21) - 10)` passed to `Vec3.yRot(float)` (`cos(f)`, `sin(f)`).
//   - `horizScale = effective * (rng.Float32()*0.5 + 0.2)`.
//   - `vy = effective * rng.Float32() * 0.5`.
func ComputeHoglinThrowImpulse2612(
	attackerX, attackerZ float64,
	targetX, targetZ float64,
	attackKnockback float64,
	targetKnockbackResistance float64,
	baby bool,
	rng *rand.Rand,
) (vx, vy, vz float64, thrown bool) {
	if baby {
		return 0, 0, 0, false
	}
	effective := attackKnockback - targetKnockbackResistance
	if effective <= 0.0 {
		return 0, 0, 0, false
	}
	dx := targetX - attackerX
	dz := targetZ - attackerZ
	dist := math.Hypot(dx, dz)
	var ux, uz float64
	if dist > 1e-6 {
		ux = dx / dist
		uz = dz / dist
	} else {
		ux = 1.0
	}

	var rotAngle float64
	horizRoll := 0.45
	vertRoll := 0.5
	if rng != nil {
		rotAngle = float64(rng.Intn(21) - 10)
		horizRoll = float64(rng.Float32()*0.5 + 0.2)
		vertRoll = float64(rng.Float32())
	}
	horizMag := effective * horizRoll
	sx := ux * horizMag
	sz := uz * horizMag

	cosA := math.Cos(rotAngle)
	sinA := math.Sin(rotAngle)
	vx = sx*cosA + sz*sinA
	vz = sz*cosA - sx*sinA
	vy = effective * vertRoll * 0.5
	return vx, vy, vz, true
}

// MobKnockbackResistance2612 returns the base `Attributes.KNOCKBACK_RESISTANCE`
// for a mob species in Minecraft 26.1.2.
func MobKnockbackResistance2612(typeName string) float64 {
	norm := canonicalResourceName(typeName)
	if spec, ok := hostileNetherMobSpecs2612[norm]; ok && spec.KnockbackResistance > 0 {
		return spec.KnockbackResistance
	}
	switch norm {
	case MobTypeIronGolem, MobTypeWarden, MobTypeShulker, MobTypeEnderDragon:
		return 1.0
	case MobTypeRavager:
		return 0.75
	case MobTypeHoglin, MobTypeZoglin:
		return 0.6
	default:
		return 0.0
	}
}

// IsValidZoglinAttackTargetEntity2612 implements `Zoglin.findNearestValidAttackTarget`
// (`hoglin_zoglin_2612.txt:1502-1515, 1891-1909`):
//   - Must be an attackable `LivingEntity` (excludes items, XP orbs, armor stands,
//     decorations, boats/minecarts, projectiles, crystals, TNT, clouds, fangs, etc.);
//   - Excludes `minecraft:zoglin` and `minecraft:creeper`.
func IsValidZoglinAttackTargetEntity2612(ent *world.Entity) bool {
	if ent == nil || ent.IsItem() || ent.IsExperienceOrb() {
		return false
	}
	norm := canonicalResourceName(ent.TypeName)
	if norm == "" || norm == MobTypeZoglin || norm == MobTypeCreeper || norm == "minecraft:player" {
		return false
	}
	if norm == EntityTypeArmorStand ||
		IsDecorationEntity(norm) ||
		IsStandardProjectileEntityType(norm) ||
		IsFireballEntityType(norm) ||
		IsWindChargeEntityType(norm) ||
		IsVehicleEntity(norm) {
		return false
	}
	if strings.HasSuffix(norm, "_boat") || strings.HasSuffix(norm, "_raft") || strings.Contains(norm, "minecart") {
		return false
	}
	switch norm {
	case EntityTypeFishingBobber, EntityTypeLlamaSpit, EntityTypeLightning, EntityTypeEvokerFangs,
		"minecraft:area_effect_cloud", "minecraft:end_crystal", "minecraft:tnt",
		"minecraft:falling_block", "minecraft:shulker_bullet", "minecraft:wither_skull",
		"minecraft:dragon_fireball", "minecraft:eye_of_ender", "minecraft:potion",
		"minecraft:splash_potion", "minecraft:lingering_potion", "minecraft:experience_bottle",
		"minecraft:firework_rocket", "minecraft:ominous_item_spawner", "minecraft:interaction",
		"minecraft:block_display", "minecraft:item_display", "minecraft:text_display", "minecraft:marker":
		return false
	}
	return true
}
