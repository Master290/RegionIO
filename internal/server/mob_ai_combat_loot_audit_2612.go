package server

import (
	"math"
	"math/rand"
	"strings"

	"regionio/internal/world"
)

// --- 1. Goals, Priorities, Threat Avoidance & Light/Time/Weather Reactions (26.1.2.jar) ---

// MobGoalEntry2612 describes a registered AI goal or target goal and its
// priority in vanilla 26.1.2 (`registerGoals()` in `<Mob>.class`).
type MobGoalEntry2612 struct {
	Priority     int
	GoalName     string
	IsTargetGoal bool
	SpeedMod     float64
	MaxDist      float64
}

// ThreatAvoidanceRule2612 describes an `AvoidEntityGoal` rule in 26.1.2
// (e.g. Creeper avoiding Ocelot/Cat within 6.0 blocks, Skeleton avoiding Wolf within 6.0 blocks).
type ThreatAvoidanceRule2612 struct {
	ThreatMobType string
	MaxDist       float64
	WalkSpeedMod  float64
	SprintSpeedMod float64
}

// VanillaMobGoalTable2612 returns the verified `registerGoals()` priority table
// from `26.1.2.jar` for key mob families (`creeper`, `skeleton`/`stray`/`bogged`/`parched`,
// `zombie`/`husk`/`drowned`/`zombie_villager`, `spider`/`cave_spider`, `wolf`, `villager`, `cow`/`sheep`/`pig`).
func VanillaMobGoalTable2612(mobType string) []MobGoalEntry2612 {
	norm := canonicalResourceName(mobType)
	switch norm {
	case MobTypeCreeper:
		return []MobGoalEntry2612{
			{Priority: 1, GoalName: "FloatGoal"},
			{Priority: 2, GoalName: "SwellGoal", MaxDist: CreeperIgniteRange},
			{Priority: 3, GoalName: "AvoidEntityGoal(ocelot,cat)", SpeedMod: 1.2, MaxDist: 6.0},
			{Priority: 4, GoalName: "MeleeAttackGoal", SpeedMod: 1.0},
			{Priority: 5, GoalName: "WaterAvoidingRandomStrollGoal", SpeedMod: 0.8},
			{Priority: 6, GoalName: "LookAtPlayerGoal", MaxDist: 8.0},
			{Priority: 6, GoalName: "RandomLookAroundGoal"},
			{Priority: 1, GoalName: "NearestAttackableTargetGoal(player)", IsTargetGoal: true, MaxDist: 16.0},
			{Priority: 2, GoalName: "HurtByTargetGoal", IsTargetGoal: true},
		}
	case MobTypeSkeleton, MobTypeStray, MobTypeBogged, MobTypeParched:
		return []MobGoalEntry2612{
			{Priority: 2, GoalName: "RestrictSunGoal"},
			{Priority: 3, GoalName: "FleeSunGoal", SpeedMod: 1.0},
			{Priority: 3, GoalName: "AvoidEntityGoal(wolf)", SpeedMod: 1.2, MaxDist: 6.0},
			{Priority: 4, GoalName: "RangedBowAttackGoal", SpeedMod: 1.0, MaxDist: 15.0},
			{Priority: 5, GoalName: "WaterAvoidingRandomStrollGoal", SpeedMod: 1.0},
			{Priority: 6, GoalName: "LookAtPlayerGoal", MaxDist: 8.0},
			{Priority: 6, GoalName: "RandomLookAroundGoal"},
			{Priority: 1, GoalName: "HurtByTargetGoal", IsTargetGoal: true},
			{Priority: 2, GoalName: "NearestAttackableTargetGoal(player)", IsTargetGoal: true, MaxDist: 16.0},
			{Priority: 3, GoalName: "NearestAttackableTargetGoal(iron_golem)", IsTargetGoal: true, MaxDist: 16.0},
			{Priority: 3, GoalName: "NearestAttackableTargetGoal(turtle_baby)", IsTargetGoal: true, MaxDist: 16.0},
		}
	case MobTypeZombie, MobTypeHusk, MobTypeDrowned, MobTypeZombieVillager:
		return []MobGoalEntry2612{
			{Priority: 2, GoalName: "ZombieAttackGoal", SpeedMod: 1.0},
			{Priority: 4, GoalName: "ZombieAttackTurtleEggGoal", SpeedMod: 1.0, MaxDist: 3.0},
			{Priority: 6, GoalName: "MoveThroughVillageGoal", SpeedMod: 1.0},
			{Priority: 7, GoalName: "WaterAvoidingRandomStrollGoal", SpeedMod: 1.0},
			{Priority: 8, GoalName: "LookAtPlayerGoal", MaxDist: 8.0},
			{Priority: 8, GoalName: "RandomLookAroundGoal"},
			{Priority: 1, GoalName: "HurtByTargetGoal(alert:zombified_piglin)", IsTargetGoal: true},
			{Priority: 2, GoalName: "NearestAttackableTargetGoal(player)", IsTargetGoal: true, MaxDist: 35.0},
			{Priority: 3, GoalName: "NearestAttackableTargetGoal(villager)", IsTargetGoal: true, MaxDist: 35.0},
			{Priority: 3, GoalName: "NearestAttackableTargetGoal(iron_golem)", IsTargetGoal: true, MaxDist: 35.0},
			{Priority: 5, GoalName: "NearestAttackableTargetGoal(turtle_baby)", IsTargetGoal: true, MaxDist: 35.0},
		}
	case MobTypeSpider, MobTypeCaveSpider:
		return []MobGoalEntry2612{
			{Priority: 1, GoalName: "FloatGoal"},
			{Priority: 2, GoalName: "AvoidEntityGoal(armadillo)", SpeedMod: 1.2, MaxDist: 6.0},
			{Priority: 3, GoalName: "LeapAtTargetGoal", SpeedMod: 0.4},
			{Priority: 4, GoalName: "SpiderAttackGoal", SpeedMod: 1.0},
			{Priority: 5, GoalName: "WaterAvoidingRandomStrollGoal", SpeedMod: 0.8},
			{Priority: 6, GoalName: "LookAtPlayerGoal", MaxDist: 8.0},
			{Priority: 6, GoalName: "RandomLookAroundGoal"},
			{Priority: 1, GoalName: "HurtByTargetGoal", IsTargetGoal: true},
			{Priority: 2, GoalName: "SpiderTargetGoal(player)", IsTargetGoal: true, MaxDist: 16.0},
			{Priority: 3, GoalName: "SpiderTargetGoal(iron_golem)", IsTargetGoal: true, MaxDist: 16.0},
		}
	case MobTypeWolf:
		return []MobGoalEntry2612{
			{Priority: 1, GoalName: "FloatGoal"},
			{Priority: 2, GoalName: "SitWhenOrderedToGoal"},
			{Priority: 3, GoalName: "WolfAvoidEntityGoal(llama)", SpeedMod: 1.5, MaxDist: 24.0},
			{Priority: 4, GoalName: "LeapAtTargetGoal", SpeedMod: 0.4},
			{Priority: 5, GoalName: "MeleeAttackGoal", SpeedMod: 1.0},
			{Priority: 6, GoalName: "FollowOwnerGoal", SpeedMod: 1.0, MaxDist: 10.0},
			{Priority: 7, GoalName: "BreedGoal", SpeedMod: 1.0},
			{Priority: 8, GoalName: "WaterAvoidingRandomStrollGoal", SpeedMod: 1.0},
			{Priority: 9, GoalName: "BegGoal", MaxDist: 8.0},
			{Priority: 10, GoalName: "LookAtPlayerGoal", MaxDist: 8.0},
			{Priority: 1, GoalName: "OwnerHurtByTargetGoal", IsTargetGoal: true},
			{Priority: 2, GoalName: "OwnerHurtTargetGoal", IsTargetGoal: true},
			{Priority: 3, GoalName: "HurtByTargetGoal(alertOthers)", IsTargetGoal: true},
			{Priority: 4, GoalName: "NearestAttackableTargetGoal(abstract_skeleton)", IsTargetGoal: true, MaxDist: 16.0},
		}
	default:
		return []MobGoalEntry2612{
			{Priority: 0, GoalName: "FloatGoal"},
			{Priority: 1, GoalName: "PanicGoal", SpeedMod: 1.25},
			{Priority: 2, GoalName: "BreedGoal", SpeedMod: 1.0},
			{Priority: 3, GoalName: "TemptGoal", SpeedMod: 1.1, MaxDist: 10.0},
			{Priority: 4, GoalName: "FollowParentGoal", SpeedMod: 1.1},
			{Priority: 5, GoalName: "WaterAvoidingRandomStrollGoal", SpeedMod: 1.0},
			{Priority: 6, GoalName: "LookAtPlayerGoal", MaxDist: 6.0},
			{Priority: 7, GoalName: "RandomLookAroundGoal"},
		}
	}
}

// LookupMobThreatAvoidanceRules2612 returns the `AvoidEntityGoal` threat rules
// for `mobType` in vanilla 26.1.2:
//   - `creeper` flees `cat` and `ocelot` (`6.0` blocks, walk `1.0`, sprint `1.2`).
//   - `skeleton`/`stray`/`bogged`/`parched`/`wither_skeleton` flee `wolf` (`6.0` blocks, walk `1.0`, sprint `1.2`).
//   - `spider`/`cave_spider` flee unrolled `armadillo` (`6.0` blocks, walk `1.0`, sprint `1.2`).
//   - `wolf` flees `llama`/`trader_llama` (`24.0` blocks, walk `1.5`, sprint `1.5`).
//   - `villager`/`wandering_trader` flee zombies (`8.0` blocks) and illagers (`12.0` blocks).
func LookupMobThreatAvoidanceRules2612(mobType string) []ThreatAvoidanceRule2612 {
	switch canonicalResourceName(mobType) {
	case MobTypeCreeper:
		return []ThreatAvoidanceRule2612{
			{ThreatMobType: MobTypeCat, MaxDist: 6.0, WalkSpeedMod: 1.0, SprintSpeedMod: 1.2},
			{ThreatMobType: MobTypeOcelot, MaxDist: 6.0, WalkSpeedMod: 1.0, SprintSpeedMod: 1.2},
		}
	case MobTypeSkeleton, MobTypeStray, MobTypeBogged, MobTypeParched, MobTypeWitherSkeleton:
		return []ThreatAvoidanceRule2612{
			{ThreatMobType: MobTypeWolf, MaxDist: 6.0, WalkSpeedMod: 1.0, SprintSpeedMod: 1.2},
		}
	case MobTypeSpider, MobTypeCaveSpider:
		return []ThreatAvoidanceRule2612{
			{ThreatMobType: MobTypeArmadillo, MaxDist: 6.0, WalkSpeedMod: 1.0, SprintSpeedMod: 1.2},
		}
	case MobTypeWolf:
		return []ThreatAvoidanceRule2612{
			{ThreatMobType: MobTypeLlama, MaxDist: 24.0, WalkSpeedMod: 1.5, SprintSpeedMod: 1.5},
			{ThreatMobType: MobTypeTraderLlama, MaxDist: 24.0, WalkSpeedMod: 1.5, SprintSpeedMod: 1.5},
		}
	case MobTypeVillager, MobTypeWanderingTrader:
		return []ThreatAvoidanceRule2612{
			{ThreatMobType: MobTypeZombie, MaxDist: 8.0, WalkSpeedMod: 0.5, SprintSpeedMod: 0.5},
			{ThreatMobType: MobTypeHusk, MaxDist: 8.0, WalkSpeedMod: 0.5, SprintSpeedMod: 0.5},
			{ThreatMobType: MobTypeDrowned, MaxDist: 8.0, WalkSpeedMod: 0.5, SprintSpeedMod: 0.5},
			{ThreatMobType: MobTypePillager, MaxDist: 15.0, WalkSpeedMod: 0.5, SprintSpeedMod: 0.5},
			{ThreatMobType: MobTypeVindicator, MaxDist: 12.0, WalkSpeedMod: 0.5, SprintSpeedMod: 0.5},
			{ThreatMobType: MobTypeEvoker, MaxDist: 12.0, WalkSpeedMod: 0.5, SprintSpeedMod: 0.5},
			{ThreatMobType: MobTypeVex, MaxDist: 8.0, WalkSpeedMod: 0.5, SprintSpeedMod: 0.5},
		}
	}
	return nil
}

// ShouldMobBurnInDaylight2612 implements `Mob.isSunBurnTick()` in 26.1.2:
//   - Only mobs in `#minecraft:burn_in_daylight` burn (`husk`, `parched`, `camel_husk`, `zombified_piglin` do NOT).
//   - Requires daytime (`isDay`), sky exposure (`canSeeSky`), not raining/thundering,
//     not in water/powder snow/bubble column, and no helmet equipped (or helmet absorbs burn via durability damage).
func ShouldMobBurnInDaylight2612(mobType string, isDay, canSeeSky, isRaining, inWaterOrPowderSnow, hasHelmet bool) (burnEntity bool, damageHelmet bool) {
	if !IsMobInEntityTypeTag2612(mobType, "minecraft:burn_in_daylight") {
		return false, false
	}
	if !isDay || !canSeeSky || isRaining || inWaterOrPowderSnow {
		return false, false
	}
	if hasHelmet {
		return false, true
	}
	return true, false
}

// --- 2. Group Behavior (Wolf Pack, Bees, Piglins, Zombie Reinforcements) ---

// GroupAlertSpec2612 describes `HurtByTargetGoal.setAlertOthers` group aggro behavior.
type GroupAlertSpec2612 struct {
	AlertsSameSpecies bool
	AlertRange        float64
	AngerMinTicks     int
	AngerMaxTicks     int
}

// LookupMobGroupAlertSpec2612 returns the group-alert parameters when a mob is
// attacked in vanilla 26.1.2 (`wolf`, `bee`, `zombified_piglin`, `piglin`, `silverfish`).
func LookupMobGroupAlertSpec2612(mobType string) (GroupAlertSpec2612, bool) {
	switch canonicalResourceName(mobType) {
	case MobTypeWolf:
		return GroupAlertSpec2612{AlertsSameSpecies: true, AlertRange: 16.0, AngerMinTicks: 400, AngerMaxTicks: 780}, true
	case MobTypeBee:
		return GroupAlertSpec2612{AlertsSameSpecies: true, AlertRange: 48.0, AngerMinTicks: 400, AngerMaxTicks: 780}, true
	case MobTypeZombifiedPiglin:
		return GroupAlertSpec2612{AlertsSameSpecies: true, AlertRange: 35.0, AngerMinTicks: 400, AngerMaxTicks: 800}, true
	case MobTypePiglin, MobTypePiglinBrute:
		return GroupAlertSpec2612{AlertsSameSpecies: true, AlertRange: 16.0, AngerMinTicks: 600, AngerMaxTicks: 600}, true
	case MobTypeSilverfish:
		return GroupAlertSpec2612{AlertsSameSpecies: true, AlertRange: 20.0, AngerMinTicks: 200, AngerMaxTicks: 200}, true
	}
	return GroupAlertSpec2612{}, false
}

// RollZombieReinforcementSpawn2612 implements `Zombie.hurtServer` reinforcement
// spawning in vanilla 26.1.2:
//   - Only triggers on `DifficultyHard` when damaged by a living attacker.
//   - If `rng.Float64() < reinforcementChance`, reduces `reinforcementChance` by `0.05`
//     (clamped to `>= 0`) and signals a reinforcement zombie spawn (`7..40` blocks away).
func RollZombieReinforcementSpawn2612(difficulty Difficulty, reinforcementChance float64, rng *rand.Rand) (spawnReinforcement bool, nextReinforcementChance float64) {
	if difficulty != DifficultyHard || reinforcementChance <= 0 {
		return false, reinforcementChance
	}
	var r float64
	if rng != nil {
		r = rng.Float64()
	} else {
		r = rand.Float64()
	}
	if r < reinforcementChance {
		next := reinforcementChance - 0.05
		if next < 0 {
			next = 0
		}
		return true, next
	}
	return false, reinforcementChance
}

// --- 3. Villager Schedules, Sleep & Restocking (Schedule.java in 26.1.2.jar) ---

// VillagerScheduleActivityAtTick2612 returns the scheduled `ActivityType` for an
// adult or baby villager at `dayTime` (`0..23999` ticks) matching `Schedule.VILLAGER_DEFAULT`
// and `Schedule.VILLAGER_BABY` in `26.1.2.jar`:
//   - Adult (`hasJob`): `10..1999` `IDLE`, `2000..8999` `WORK`, `9000..10999` `MEET`,
//     `11000..11999` `IDLE`, `12000..23999` `REST`.
//   - Adult (`!hasJob` / Nitwit): `10..11999` `IDLE`, `12000..23999` `REST`.
//   - Baby: `10..2999` `IDLE`, `3000..5999` `PLAY`, `6000..9999` `IDLE`,
//     `10000..11999` `PLAY`, `12000..23999` `REST`.
func VillagerScheduleActivityAtTick2612(dayTime int64, isBaby, hasJob bool) ActivityType {
	t := dayTime % 24000
	if t < 0 {
		t += 24000
	}
	if isBaby {
		switch {
		case t >= 12000 || t < 10:
			return ActivityRest
		case (t >= 3000 && t < 6000) || (t >= 10000 && t < 12000):
			return ActivityPlay
		default:
			return ActivityIdle
		}
	}
	if t >= 12000 || t < 10 {
		return ActivityRest
	}
	if !hasJob {
		return ActivityIdle
	}
	switch {
	case t >= 2000 && t < 9000:
		return ActivityWork
	case t >= 9000 && t < 11000:
		return ActivityMeet
	default:
		return ActivityIdle
	}
}

// --- 4. Fluid / Vine / Powder Snow Movement (`powder_snow_walkable_mobs`) ---

// CanMobWalkOnPowderSnow2612 implements `PowderSnowBlock.canEntityWalkOnPowderSnow(Entity)`
// in `26.1.2.jar`: returns true if `mobType` is in `#minecraft:powder_snow_walkable_mobs`
// (`rabbit`, `endermite`, `silverfish`, `fox`) OR is wearing `minecraft:leather_boots`.
func CanMobWalkOnPowderSnow2612(mobType, feetArmorItemID string) bool {
	if canonicalResourceName(feetArmorItemID) == "minecraft:leather_boots" {
		return true
	}
	switch canonicalResourceName(mobType) {
	case MobTypeRabbit, MobTypeEndermite, MobTypeSilverfish, MobTypeFox:
		return true
	default:
		return false
	}
}

// ComputeMobFluidOrClimbVelocity2612 computes the adjusted vertical velocity `vy`
// and horizontal speed multiplier for a mob in water, lava, or on climbable blocks
// (`ladder`/`vine`/`scaffolding`):
//   - In water with `FloatGoal`: adds `+0.04` buoyancy to `vy`.
//   - `strider` on lava: walks on top (`vy >= 0`, speed multiplier `1.85` on lava).
//   - On climbable block while moving forward/colliding horizontally: `vy = max(vy, 0.2)`.
func ComputeMobFluidOrClimbVelocity2612(mobType string, vy float64, inWater, inLava, onClimbable, movingForward bool) (nextVY, speedMult float64) {
	norm := canonicalResourceName(mobType)
	nextVY = vy
	speedMult = 1.0

	if inLava && norm == MobTypeStrider {
		if nextVY < 0 {
			nextVY = 0
		}
		return nextVY, 1.85
	}
	if inWater {
		if !IsMobInEntityTypeTag2612(norm, "minecraft:sinks_in_water") && norm != MobTypeZombie && norm != MobTypeDrowned && norm != MobTypeHusk && norm != MobTypeIronGolem {
			nextVY += 0.04
		}
		speedMult = 0.8
	}
	if onClimbable {
		if nextVY < -0.15 {
			nextVY = -0.15
		}
		if movingForward && nextVY < 0.2 {
			nextVY = 0.2
		}
	}
	return nextVY, speedMult
}

// --- 5. Zombie Villager Curing & Lightning Conversions (26.1.2.jar) ---

// StepZombieVillagerCure2612 advances one tick of `ZombieVillager` curing
// (`getConversionProgress()` in `ZombieVillager.class`):
//   - Base progress is `1` tick per server tick.
//   - With `1%` chance per tick, scans up to `14` iron bars / bed blocks in the
//     `9x9x9` cube (`±4` blocks); each block has a `30%` chance to add `+1` progress.
//   - When `conversionTimeRemaining <= 0`, converts to `minecraft:villager` and
//     grants `VillagerCureDiscountGossip` (`major_positive +20`, `minor_positive +25`).
func StepZombieVillagerCure2612(conversionTimeRemaining, nearbyIronBarsAndBeds int, rng *rand.Rand) (nextRemaining int, cured bool) {
	if conversionTimeRemaining <= 0 {
		return 0, true
	}
	if nearbyIronBarsAndBeds > 14 {
		nearbyIronBarsAndBeds = 14
	}
	f32 := func() float32 {
		if rng != nil {
			return rng.Float32()
		}
		return rand.Float32()
	}
	step := 1
	if nearbyIronBarsAndBeds > 0 && f32() < 0.01 {
		for i := 0; i < nearbyIronBarsAndBeds; i++ {
			if f32() < 0.30 {
				step++
			}
		}
	}
	nextRemaining = conversionTimeRemaining - step
	if nextRemaining <= 0 {
		return 0, true
	}
	return nextRemaining, false
}

// LightningConversionResult2612 describes the effect of a lightning bolt
// (`thunderHit` in `26.1.2.jar`) striking a mob on non-Peaceful difficulty.
type LightningConversionResult2612 struct {
	ConvertedToMobType string
	MainHandItem       string
	ChargedCreeper     bool
	ToggledMooshroom   bool
	InstantTurtleKill  bool
	DroppedItemID      string
}

// EvaluateLightningStrikeConversion2612 evaluates `Entity.thunderHit` for `mobType`:
//   - `minecraft:pig` -> `minecraft:zombified_piglin` armed with `minecraft:golden_sword`.
//   - `minecraft:villager` -> `minecraft:witch`.
//   - `minecraft:creeper` -> Charged Creeper (`powered = true`).
//   - `minecraft:mooshroom` -> toggles red <-> brown variant.
//   - `minecraft:turtle` -> instant lightning death + drops `1x` `minecraft:bowl`.
func EvaluateLightningStrikeConversion2612(mobType string, difficulty Difficulty) LightningConversionResult2612 {
	switch canonicalResourceName(mobType) {
	case MobTypePig:
		if difficulty != DifficultyPeaceful {
			return LightningConversionResult2612{
				ConvertedToMobType: MobTypeZombifiedPiglin,
				MainHandItem:       "minecraft:golden_sword",
			}
		}
	case MobTypeVillager:
		if difficulty != DifficultyPeaceful {
			return LightningConversionResult2612{
				ConvertedToMobType: MobTypeWitch,
			}
		}
	case MobTypeCreeper:
		return LightningConversionResult2612{
			ChargedCreeper: true,
		}
	case MobTypeMooshroom:
		return LightningConversionResult2612{
			ToggledMooshroom: true,
		}
	case MobTypeTurtle:
		return LightningConversionResult2612{
			InstantTurtleKill: true,
			DroppedItemID:     "minecraft:bowl",
		}
	}
	return LightningConversionResult2612{}
}

// --- 6. Melee Damage, Knockback, Shield Disable & Ranged Attacks (26.1.2.jar) ---

// ComputeMobMeleeDamageAndKnockback2612 calculates the difficulty-scaled melee
// damage, effective horizontal knockback (`baseKnockback * (1 - knockbackResistance)`),
// and shield disable duration (`100` ticks = `5s` when attacker wields an axe or is a `vindicator`/`piglin_brute`/`ravager`).
func ComputeMobMeleeDamageAndKnockback2612(attackerMobType, attackerWeaponID string, baseDamage float32, difficulty Difficulty, targetKnockbackResistance float64, targetBlockingWithShield bool) (damage float32, knockback float64, shieldDisabledTicks int) {
	normAttacker := canonicalResourceName(attackerMobType)
	normWeapon := canonicalResourceName(attackerWeaponID)

	// Difficulty scaling for hostile melee damage (`Difficulty.EASY: d/2 + 1`, `HARD: d * 3/2`).
	scaled := baseDamage
	switch difficulty {
	case DifficultyEasy:
		scaled = float32(math.Min(float64(baseDamage/2.0+1.0), float64(baseDamage)))
	case DifficultyHard:
		scaled = baseDamage * 1.5
	}

	breaksShield := strings.HasSuffix(normWeapon, "_axe") ||
		normAttacker == MobTypeVindicator ||
		normAttacker == MobTypePiglinBrute ||
		normAttacker == MobTypeRavager ||
		normAttacker == MobTypeWarden

	if targetBlockingWithShield {
		if breaksShield {
			shieldDisabledTicks = 100
		}
		return 0, 0, shieldDisabledTicks
	}

	kbMult := 1.0 - targetKnockbackResistance
	if kbMult < 0 {
		kbMult = 0
	}
	return scaled, 0.4 * kbMult, 0
}

// MobRangedAttackSpec2612 describes the projectile, range, cooldown, speed,
// inaccuracy, and status effect of a ranged mob attack in 26.1.2.
type MobRangedAttackSpec2612 struct {
	ProjectileEntityType string
	MaxRange             float64
	CooldownTicks        int
	ProjectileSpeed      float64
	Inaccuracy           float32
	StatusEffectID       string
	StatusEffectTicks    int
}

// LookupMobRangedAttackSpec2612 returns the ranged attack specification for
// `mobType` at `difficulty` in vanilla 26.1.2:
//   - `skeleton`/`stray`/`bogged`/`parched` bow (`arrow`, range `15.0`, speed `1.6`, inaccuracy `14 - diff*4`).
//   - `pillager`/`piglin` crossbow (`arrow`, range `8.0`, speed `1.6`).
//   - `drowned` trident (`trident`, range `10.0`, cooldown `40`, speed `1.6`).
//   - `blaze` small fireball (`small_fireball`, range `48.0`, cooldown `100`).
//   - `ghast` large fireball (`fireball`, range `64.0`, cooldown `40`).
//   - `shulker` bullet (`shulker_bullet`, range `16.0`, cooldown `40`, `levitation` `200t`).
//   - `snow_golem` snowball (`snowball`, range `10.0`, cooldown `20`, speed `1.6`).
//   - `breeze` wind charge (`breeze_wind_charge`, range `16.0`, cooldown `30`).
func LookupMobRangedAttackSpec2612(mobType string, difficulty Difficulty) (MobRangedAttackSpec2612, bool) {
	inacc := float32(14 - int(difficulty)*4)
	switch canonicalResourceName(mobType) {
	case MobTypeSkeleton:
		cd := 40
		if difficulty == DifficultyHard {
			cd = 20
		}
		return MobRangedAttackSpec2612{
			ProjectileEntityType: MobTypeArrow,
			MaxRange:             15.0,
			CooldownTicks:        cd,
			ProjectileSpeed:      1.6,
			Inaccuracy:           inacc,
		}, true
	case MobTypeStray:
		cd := 40
		if difficulty == DifficultyHard {
			cd = 20
		}
		return MobRangedAttackSpec2612{
			ProjectileEntityType: MobTypeArrow,
			MaxRange:             15.0,
			CooldownTicks:        cd,
			ProjectileSpeed:      1.6,
			Inaccuracy:           inacc,
			StatusEffectID:       "minecraft:slowness",
			StatusEffectTicks:    600,
		}, true
	case MobTypeBogged:
		cd := 70
		if difficulty == DifficultyHard {
			cd = 50
		}
		return MobRangedAttackSpec2612{
			ProjectileEntityType: MobTypeArrow,
			MaxRange:             15.0,
			CooldownTicks:        cd,
			ProjectileSpeed:      1.6,
			Inaccuracy:           inacc,
			StatusEffectID:       "minecraft:poison",
			StatusEffectTicks:    100,
		}, true
	case MobTypeParched:
		cd := 70
		if difficulty == DifficultyHard {
			cd = 50
		}
		return MobRangedAttackSpec2612{
			ProjectileEntityType: MobTypeArrow,
			MaxRange:             15.0,
			CooldownTicks:        cd,
			ProjectileSpeed:      1.6,
			Inaccuracy:           inacc,
			StatusEffectID:       "minecraft:weakness",
			StatusEffectTicks:    600,
		}, true
	case MobTypePillager, MobTypePiglin:
		return MobRangedAttackSpec2612{
			ProjectileEntityType: MobTypeArrow,
			MaxRange:             8.0,
			CooldownTicks:        40,
			ProjectileSpeed:      1.6,
			Inaccuracy:           inacc,
		}, true
	case MobTypeDrowned:
		return MobRangedAttackSpec2612{
			ProjectileEntityType: "minecraft:trident",
			MaxRange:             10.0,
			CooldownTicks:        40,
			ProjectileSpeed:      1.6,
			Inaccuracy:           inacc,
		}, true
	case MobTypeBlaze:
		return MobRangedAttackSpec2612{
			ProjectileEntityType: EntityTypeSmallFireball,
			MaxRange:             48.0,
			CooldownTicks:        100,
			ProjectileSpeed:      0.9,
		}, true
	case MobTypeGhast:
		return MobRangedAttackSpec2612{
			ProjectileEntityType: EntityTypeFireball,
			MaxRange:             64.0,
			CooldownTicks:        40,
			ProjectileSpeed:      1.0,
		}, true
	case MobTypeShulker:
		return MobRangedAttackSpec2612{
			ProjectileEntityType: EntityTypeShulkerBullet,
			MaxRange:             16.0,
			CooldownTicks:        40,
			ProjectileSpeed:      0.5,
			StatusEffectID:       "minecraft:levitation",
			StatusEffectTicks:    200,
		}, true
	case MobTypeSnowGolem:
		return MobRangedAttackSpec2612{
			ProjectileEntityType: "minecraft:snowball",
			MaxRange:             10.0,
			CooldownTicks:        20,
			ProjectileSpeed:      1.6,
			Inaccuracy:           12.0,
		}, true
	case MobTypeBreeze:
		return MobRangedAttackSpec2612{
			ProjectileEntityType: BreezeWindChargeEntityType,
			MaxRange:             16.0,
			CooldownTicks:        30,
			ProjectileSpeed:      1.2,
		}, true
	}
	return MobRangedAttackSpec2612{}, false
}

// --- 7. Equipment Drop Chances, Loot Pickup & Special Killer Drops (26.1.2.jar) ---

// DefaultMobEquipmentDropChance2612 is `Mob.DEFAULT_EQUIPMENT_DROP_CHANCE = 0.085F` (8.5%).
const DefaultMobEquipmentDropChance2612 float32 = 0.085

var creeperDropMusicDiscs2612 = [12]string{
	"minecraft:music_disc_13",
	"minecraft:music_disc_cat",
	"minecraft:music_disc_blocks",
	"minecraft:music_disc_chirp",
	"minecraft:music_disc_far",
	"minecraft:music_disc_mall",
	"minecraft:music_disc_mellohi",
	"minecraft:music_disc_stal",
	"minecraft:music_disc_strad",
	"minecraft:music_disc_ward",
	"minecraft:music_disc_11",
	"minecraft:music_disc_wait",
}

// ComputeMobEquipmentDropChance2612 computes the effective drop probability for
// an equipped mob slot in vanilla 26.1.2 (`Mob.dropCustomDeathLoot`):
//   - Picked-up equipment (`pickedUpFromGround == true`, vanilla dropChance `2.0F`)
//     always drops (`1.0` probability) regardless of Looting.
//   - Natural spawn equipment (`baseDropChance = 0.085F`) increases by `+0.01F` per `lootingLevel`.
func ComputeMobEquipmentDropChance2612(pickedUpFromGround bool, lootingLevel int) float32 {
	if pickedUpFromGround {
		return 1.0
	}
	if lootingLevel < 0 {
		lootingLevel = 0
	}
	return DefaultMobEquipmentDropChance2612 + 0.01*float32(lootingLevel)
}

// CanMobPickUpLootChance2612 returns the probability that a newly spawned undead
// mob (`zombie`, `skeleton`, etc.) gets `canPickUpLoot = true` (`0.55 * specialDifficultyMultiplier`).
func CanMobPickUpLootChance2612(specialDifficultyMultiplier float32) float32 {
	if specialDifficultyMultiplier < 0 {
		return 0
	}
	if specialDifficultyMultiplier > 1 {
		specialDifficultyMultiplier = 1
	}
	return 0.55 * specialDifficultyMultiplier
}

// EvaluateSpecialKillerMobDrop2612 evaluates killer-specific bonus drops in 26.1.2:
//   - `Creeper` killed by any `#minecraft:skeletons` mob drops one of the 12
//     `#minecraft:creeper_drop_music_discs`.
//   - `Zombie`, `Skeleton`, `Creeper`, `WitherSkeleton`, or `Piglin` killed by a
//     Charged Creeper explosion drops its corresponding mob head/skull.
//   - Any living mob killed by `minecraft:wither` drops `1x` `minecraft:wither_rose`.
func EvaluateSpecialKillerMobDrop2612(victimMobType, killerMobType string, killerIsChargedCreeper bool, rng *rand.Rand) []world.ItemStack {
	normVictim := canonicalResourceName(victimMobType)
	normKiller := canonicalResourceName(killerMobType)

	// 1. Charged Creeper skull drop.
	if killerIsChargedCreeper && normKiller == MobTypeCreeper {
		switch normVictim {
		case MobTypeZombie:
			return []world.ItemStack{world.NewNamedItemStack("minecraft:zombie_head", 1)}
		case MobTypeSkeleton:
			return []world.ItemStack{world.NewNamedItemStack("minecraft:skeleton_skull", 1)}
		case MobTypeCreeper:
			return []world.ItemStack{world.NewNamedItemStack("minecraft:creeper_head", 1)}
		case MobTypeWitherSkeleton:
			return []world.ItemStack{world.NewNamedItemStack("minecraft:wither_skeleton_skull", 1)}
		case MobTypePiglin:
			return []world.ItemStack{world.NewNamedItemStack("minecraft:piglin_head", 1)}
		}
	}

	// 2. Creeper killed by a Skeleton family mob -> random music disc from #minecraft:creeper_drop_music_discs.
	if normVictim == MobTypeCreeper && IsMobInEntityTypeTag2612(normKiller, "minecraft:skeletons") {
		idx := 0
		if rng != nil {
			idx = rng.Intn(len(creeperDropMusicDiscs2612))
		} else {
			idx = rand.Intn(len(creeperDropMusicDiscs2612))
		}
		return []world.ItemStack{world.NewNamedItemStack(creeperDropMusicDiscs2612[idx], 1)}
	}

	// 3. Any mob killed by Wither -> wither_rose.
	if normKiller == MobTypeWither {
		return []world.ItemStack{world.NewNamedItemStack("minecraft:wither_rose", 1)}
	}

	return nil
}
