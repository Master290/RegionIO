package server

import (
	"errors"
	"math"
	"strings"
)

// --- 1. Passengers, Mounts, Vehicles & Attachment Offsets (26.1.2.jar) ---

// MountSeatSpec2612 describes the passenger capacity, control requirements,
// underwater behavior, and vertical attachment offset for a mount or vehicle in 26.1.2.
type MountSeatSpec2612 struct {
	MaxPassengers        int
	RequiresSaddle       bool
	RequiresHarness      bool
	SteeringItemID       string
	SteeringSpeedBoost   float64
	DismountsUnderwater  bool
	CanFloatWhileRidden  bool
	PassengerYOffset     float64
	FrontSeatForwardOff  float64
	SecondSeatForwardOff float64
}

// LookupMountSeatSpec2612 returns the verified 26.1.2 mount/vehicle passenger
// rules and attachment offsets for `entityType`:
//   - `#minecraft:dismounts_underwater`: `camel`, `chicken`, `donkey`, `happy_ghast`,
//     `horse`, `llama`, `mule`, `pig`, `ravager`, `spider`, `strider`, `trader_llama`, `zombie_horse`.
//   - `#minecraft:can_float_while_ridden`: `horse`, `zombie_horse`, `mule`, `donkey`, `camel`, `camel_husk`.
//   - `skeleton_horse`, `camel_husk`, `nautilus`, `zombie_nautilus` can be ridden underwater without dismounting.
func LookupMountSeatSpec2612(entityType string) (MountSeatSpec2612, bool) {
	norm := canonicalResourceName(entityType)
	if strings.Contains(norm, "chest_boat") || strings.Contains(norm, "chest_raft") {
		return MountSeatSpec2612{
			MaxPassengers:       1,
			DismountsUnderwater: true,
			PassengerYOffset:    0.36,
		}, true
	}
	if strings.HasSuffix(norm, "_boat") || strings.HasSuffix(norm, "_raft") {
		return MountSeatSpec2612{
			MaxPassengers:        2,
			DismountsUnderwater:  true,
			PassengerYOffset:     0.36,
			FrontSeatForwardOff:  0.2,
			SecondSeatForwardOff: -0.6,
		}, true
	}
	if strings.HasSuffix(norm, "minecart") {
		if norm == "minecraft:minecart" {
			return MountSeatSpec2612{
				MaxPassengers:    1,
				PassengerYOffset: 0.35,
			}, true
		}
		return MountSeatSpec2612{}, false
	}

	switch norm {
	case MobTypeHorse, MobTypeDonkey, MobTypeMule, MobTypeZombieHorse:
		return MountSeatSpec2612{
			MaxPassengers:       1,
			RequiresSaddle:      true,
			DismountsUnderwater: true,
			CanFloatWhileRidden: true,
			PassengerYOffset:    1.27,
		}, true
	case MobTypeSkeletonHorse:
		return MountSeatSpec2612{
			MaxPassengers:       1,
			RequiresSaddle:      true,
			DismountsUnderwater: false,
			CanFloatWhileRidden: false,
			PassengerYOffset:    1.27,
		}, true
	case MobTypeCamel:
		return MountSeatSpec2612{
			MaxPassengers:        2,
			RequiresSaddle:       true,
			DismountsUnderwater:  true,
			CanFloatWhileRidden:  true,
			PassengerYOffset:     1.90625,
			FrontSeatForwardOff:  CamelFrontSeatForwardOffset,
			SecondSeatForwardOff: CamelBackSeatForwardOffset,
		}, true
	case MobTypeCamelHusk:
		return MountSeatSpec2612{
			MaxPassengers:        2,
			RequiresSaddle:       true,
			DismountsUnderwater:  false,
			CanFloatWhileRidden:  true,
			PassengerYOffset:     1.90625,
			FrontSeatForwardOff:  CamelFrontSeatForwardOffset,
			SecondSeatForwardOff: CamelBackSeatForwardOffset,
		}, true
	case MobTypePig:
		return MountSeatSpec2612{
			MaxPassengers:       1,
			RequiresSaddle:      true,
			SteeringItemID:      "minecraft:carrot_on_a_stick",
			SteeringSpeedBoost:  1.15,
			DismountsUnderwater: true,
			PassengerYOffset:    0.675,
		}, true
	case MobTypeStrider:
		return MountSeatSpec2612{
			MaxPassengers:       1,
			RequiresSaddle:      true,
			SteeringItemID:      "minecraft:warped_fungus_on_a_stick",
			SteeringSpeedBoost:  1.50,
			DismountsUnderwater: true,
			PassengerYOffset:    1.51,
		}, true
	case MobTypeHappyGhast:
		return MountSeatSpec2612{
			MaxPassengers:       HappyGhastMaxPassengers,
			RequiresHarness:     true,
			DismountsUnderwater: true,
			PassengerYOffset:    HappyGhastAdultHeight,
		}, true
	case MobTypeNautilus, MobTypeZombieNautilus:
		return MountSeatSpec2612{
			MaxPassengers:       1,
			RequiresSaddle:      true,
			DismountsUnderwater: false,
			PassengerYOffset:    0.6,
		}, true
	case MobTypeLlama, MobTypeTraderLlama:
		return MountSeatSpec2612{
			MaxPassengers:       1,
			DismountsUnderwater: true,
			PassengerYOffset:    1.35,
		}, true
	case MobTypeChicken:
		return MountSeatSpec2612{
			MaxPassengers:       1,
			DismountsUnderwater: true,
			PassengerYOffset:    0.35,
		}, true
	case MobTypeSpider, MobTypeCaveSpider:
		return MountSeatSpec2612{
			MaxPassengers:       1,
			DismountsUnderwater: true,
			PassengerYOffset:    0.675,
		}, true
	case MobTypeRavager:
		return MountSeatSpec2612{
			MaxPassengers:       1,
			DismountsUnderwater: true,
			PassengerYOffset:    2.1,
		}, true
	}
	return MountSeatSpec2612{}, false
}

// ComputePassengerAttachmentPosition2612 computes the world `(px, py, pz)`
// position of passenger index `seatIndex` (`0..MaxPassengers-1`) riding `vehicleType`
// at `(vx, vy, vz)` with yaw `yawDeg`.
func ComputePassengerAttachmentPosition2612(vehicleType string, vx, vy, vz float64, yawDeg float32, seatIndex int) (px, py, pz float64) {
	spec, ok := LookupMountSeatSpec2612(vehicleType)
	if !ok {
		return vx, vy + 0.75, vz
	}
	py = vy + spec.PassengerYOffset
	var forward float64
	if spec.MaxPassengers >= 2 {
		if seatIndex == 0 {
			forward = spec.FrontSeatForwardOff
		} else {
			forward = spec.SecondSeatForwardOff
		}
	}
	if forward == 0 {
		return vx, py, vz
	}
	rad := float64(yawDeg) * (math.Pi / 180.0)
	px = vx - math.Sin(rad)*forward
	pz = vz + math.Cos(rad)*forward
	return px, py, pz
}

// --- 2. Leash / Lead Elastic Physics & Snap Rules (Leashable in 26.1.2.jar) ---

// StepLeashPhysics2612 evaluates the distance and elastic pull velocity between
// a leashed entity at `(ex, ey, ez)` and its leash holder/knot at `(hx, hy, hz)`:
//   - `dist > maxSnapDist` (`10.0` blocks default, `12.0` for Happy Ghast quad-leash):
//     the lead snaps (`snapped = true`) and drops `1x` `minecraft:lead`.
//   - `dist > LeashElasticPullDistance` (`6.0` blocks): applies elastic spring
//     velocity toward the holder (`0.4 * (dist - 6.0) / dist` along each axis).
func StepLeashPhysics2612(ex, ey, ez, hx, hy, hz, maxSnapDist float64) (dvx, dvy, dvz float64, snapped bool) {
	if maxSnapDist <= 0 {
		maxSnapDist = LeashSnapDistance
	}
	dx := hx - ex
	dy := hy - ey
	dz := hz - ez
	dist := math.Sqrt(dx*dx + dy*dy + dz*dz)
	if dist > maxSnapDist {
		return 0, 0, 0, true
	}
	if dist > LeashElasticPullDistance && dist > 1e-6 {
		pull := 0.4 * (dist - LeashElasticPullDistance) / dist
		return dx * pull, dy * pull, dz * pull, false
	}
	return 0, 0, 0, false
}

// --- 3. Knockback Physics, Entity Pushing & Cramming (`maxEntityCramming`) ---

// ApplyMobKnockbackVelocity2612 implements `LivingEntity.knockback(strength, ratioX, ratioZ)`
// in `26.1.2.jar`:
//   - Scales `strength *= (1.0 - clamp(knockbackResistance, 0, 1))`.
//   - If `strength <= 0`, velocity is unchanged.
//   - Normalizes `(ratioX, ratioZ)` and computes `vx = vx/2 - nx*strength`,
//     `vz = vz/2 - nz*strength`, and `vy = min(0.4, vy/2 + strength)` when `onGround`.
func ApplyMobKnockbackVelocity2612(vx, vy, vz, strength, ratioX, ratioZ, knockbackResistance float64, onGround bool) (nvx, nvy, nvz float64) {
	if knockbackResistance > 1.0 {
		knockbackResistance = 1.0
	}
	if knockbackResistance < 0.0 {
		knockbackResistance = 0.0
	}
	strength *= 1.0 - knockbackResistance
	if strength <= 0 {
		return vx, vy, vz
	}
	mag := math.Hypot(ratioX, ratioZ)
	if mag < 1e-6 {
		return vx, vy, vz
	}
	nx := ratioX / mag
	nz := ratioZ / mag
	nvx = vx*0.5 - nx*strength
	nvz = vz*0.5 - nz*strength
	nvy = vy
	if onGround {
		nvy = math.Min(0.4, vy*0.5+strength)
	}
	return nvx, nvy, nvz
}

// EvaluateEntityCrammingAndPush2612 evaluates `LivingEntity.pushEntities()` in 26.1.2:
//   - If `maxEntityCramming > 0` and `overlappingPushableCount >= maxEntityCramming`,
//     deals `6.0` `cramming` damage.
//   - Computes the horizontal separation impulse `(pushVX, pushVZ)` away from a
//     colliding neighbor at offset `(dx, dz)`.
func EvaluateEntityCrammingAndPush2612(overlappingPushableCount, maxEntityCramming int, dx, dz float64) (crammingDamage float32, pushVX, pushVZ float64) {
	if maxEntityCramming > 0 && overlappingPushableCount >= maxEntityCramming {
		crammingDamage = 6.0
	}
	maxAbs := math.Max(math.Abs(dx), math.Abs(dz))
	if maxAbs >= 0.01 {
		scale := 0.05 / math.Sqrt(maxAbs)
		if maxAbs > 1.0 {
			scale /= maxAbs
		}
		pushVX = -dx * scale
		pushVZ = -dz * scale
	}
	return crammingDamage, pushVX, pushVZ
}

// --- 4. Block Surface & Inside-Block Movement Physics (Blocks.class in 26.1.2.jar) ---

// BlockMovementPhysics2612 holds the exact vanilla 26.1.2 friction, speed factor,
// jump factor, inside-block velocity multiplier, and contact damage for a block.
type BlockMovementPhysics2612 struct {
	Friction             float32
	SpeedFactor          float32
	JumpFactor           float32
	InsideStuckX         float64
	InsideStuckY         float64
	InsideStuckZ         float64
	ResetsFallDistance   bool
	FallDamageMultiplier float32
	BounceFactor         float64
	ContactDamagePerTick float32
	SetsOnFireTicks      int
}

// LookupBlockMovementPhysics2612 returns the verified `BlockBehaviour` movement
// and contact physics for `blockName` in `26.1.2.jar`.
func LookupBlockMovementPhysics2612(blockName, mobType string, isSneaking, hasSoulSpeed, hasWeavingEffect, isFireImmune bool) BlockMovementPhysics2612 {
	normBlock := canonicalResourceName(blockName)
	normMob := canonicalResourceName(mobType)

	p := BlockMovementPhysics2612{
		Friction:             0.6,
		SpeedFactor:          1.0,
		JumpFactor:           1.0,
		InsideStuckX:         1.0,
		InsideStuckY:         1.0,
		InsideStuckZ:         1.0,
		FallDamageMultiplier: 1.0,
	}

	switch normBlock {
	case "minecraft:ice", "minecraft:packed_ice", "minecraft:frosted_ice":
		p.Friction = 0.98
	case "minecraft:blue_ice":
		p.Friction = 0.989
	case "minecraft:slime_block":
		p.Friction = 0.8
		if !isSneaking {
			p.FallDamageMultiplier = 0.0
			p.BounceFactor = 1.0
		}
	case "minecraft:honey_block":
		p.Friction = 0.8
		p.SpeedFactor = 0.4
		p.JumpFactor = 0.5
		p.FallDamageMultiplier = 0.2
	case "minecraft:soul_sand":
		if !hasSoulSpeed {
			p.SpeedFactor = 0.4
		}
	case "minecraft:cobweb":
		p.ResetsFallDistance = true
		if hasWeavingEffect || normMob == MobTypeSpider || normMob == MobTypeCaveSpider {
			p.InsideStuckX = 0.5
			p.InsideStuckY = 0.25
			p.InsideStuckZ = 0.5
		} else {
			p.InsideStuckX = 0.25
			p.InsideStuckY = 0.05
			p.InsideStuckZ = 0.25
		}
	case "minecraft:sweet_berry_bush":
		if normMob != MobTypeFox && normMob != MobTypeBee {
			p.InsideStuckX = 0.8
			p.InsideStuckY = 0.75
			p.InsideStuckZ = 0.8
			p.ContactDamagePerTick = 1.0
		}
	case "minecraft:powder_snow":
		p.ResetsFallDistance = true
		p.InsideStuckX = 0.9
		p.InsideStuckY = 1.5
		p.InsideStuckZ = 0.9
	case "minecraft:cactus":
		p.ContactDamagePerTick = 1.0
	case "minecraft:magma_block":
		if !isSneaking && !isFireImmune {
			p.ContactDamagePerTick = 1.0
		}
	case "minecraft:campfire":
		if !isFireImmune {
			p.ContactDamagePerTick = 1.0
		}
	case "minecraft:soul_campfire":
		if !isFireImmune {
			p.ContactDamagePerTick = 2.0
		}
	case "minecraft:fire":
		if !isFireImmune {
			p.ContactDamagePerTick = 1.0
			p.SetsOnFireTicks = 160
		}
	case "minecraft:soul_fire":
		if !isFireImmune {
			p.ContactDamagePerTick = 2.0
			p.SetsOnFireTicks = 160
		}
	case "minecraft:lava":
		p.ResetsFallDistance = true
		p.InsideStuckX = 0.5
		p.InsideStuckY = 0.5
		p.InsideStuckZ = 0.5
		if !isFireImmune {
			p.ContactDamagePerTick = 4.0
			p.SetsOnFireTicks = 300
		}
	case "minecraft:water", "minecraft:bubble_column":
		p.ResetsFallDistance = true
	}
	return p
}

// --- 5. Movement Attributes (`gravity`, `safe_fall_distance`, `step_height`, `fall_damage_multiplier`) ---

// MobMovementAttributeSet2612 holds the vanilla 26.1.2 movement and fall physics
// attributes for a mob entity.
type MobMovementAttributeSet2612 struct {
	MovementSpeed        float64
	StepHeight           float64
	Gravity              float64
	SafeFallDistance     float64
	FallDamageMultiplier float64
	JumpStrength         float64
}

// DefaultMobMovementAttributes2612 returns the verified 26.1.2 movement attribute
// defaults (`Attributes.GRAVITY = 0.08`, `SAFE_FALL_DISTANCE = 3.0`, `STEP_HEIGHT = 0.6`,
// `FALL_DAMAGE_MULTIPLIER = 1.0`, `JUMP_STRENGTH = 0.42`) customized per species.
func DefaultMobMovementAttributes2612(mobType string) MobMovementAttributeSet2612 {
	norm := canonicalResourceName(mobType)
	spec, _ := LookupMobCompletenessSpec2612(norm)
	speed := spec.MovementSpeed
	if speed <= 0 {
		speed = 0.25
	}
	attrs := MobMovementAttributeSet2612{
		MovementSpeed:        speed,
		StepHeight:           0.6,
		Gravity:              0.08,
		SafeFallDistance:     3.0,
		FallDamageMultiplier: 1.0,
		JumpStrength:         0.42,
	}
	if IsMobInEntityTypeTag2612(norm, "minecraft:fall_damage_immune") || norm == MobTypeEnderDragon {
		attrs.FallDamageMultiplier = 0.0
	}
	switch norm {
	case MobTypeHorse, MobTypeDonkey, MobTypeMule, MobTypeSkeletonHorse, MobTypeZombieHorse:
		attrs.StepHeight = 1.0
		attrs.SafeFallDistance = 6.0
		attrs.FallDamageMultiplier = 0.5
		attrs.JumpStrength = 0.7
	case MobTypeCamel, MobTypeCamelHusk:
		attrs.StepHeight = 1.5
		attrs.SafeFallDistance = 6.0
		attrs.FallDamageMultiplier = 0.5
	case MobTypeGoat:
		attrs.SafeFallDistance = 13.0
	case MobTypeFrog:
		attrs.SafeFallDistance = 8.0
	case MobTypeFox, MobTypeRabbit:
		attrs.SafeFallDistance = 5.0
	case MobTypeIronGolem, MobTypeEnderman, MobTypeRavager, MobTypeStrider, MobTypeCopperGolem:
		attrs.StepHeight = 1.0
	}
	return attrs
}

// ComputeAttributeFallDamage2612 computes fall damage using `SAFE_FALL_DISTANCE`,
// `FALL_DAMAGE_MULTIPLIER`, and block `blockFallDamageMult` (`0.0` on slime/water, `0.2` on honey/hay_block):
// `max(0, ceil((fallDistance - safeFallDistance) * fallDamageMultiplier * blockFallDamageMult))`.
func ComputeAttributeFallDamage2612(fallDistance float64, attrs MobMovementAttributeSet2612, blockFallDamageMult float32) float32 {
	if fallDistance <= attrs.SafeFallDistance || attrs.FallDamageMultiplier <= 0 || blockFallDamageMult <= 0 {
		return 0
	}
	raw := (fallDistance - attrs.SafeFallDistance) * attrs.FallDamageMultiplier * float64(blockFallDamageMult)
	if raw <= 0 {
		return 0
	}
	return float32(math.Ceil(raw - 1e-5))
}

// --- 6. Command-Driven Mob Control (`/ride`, `/tp`, `/damage`, `/attribute`) ---

// AttributeModifierOp2612 represents vanilla 26.1.2 `AttributeModifier.Operation`.
type AttributeModifierOp2612 string

const (
	AttrOpAddValue           AttributeModifierOp2612 = "add_value"
	AttrOpAddMultipliedBase  AttributeModifierOp2612 = "add_multiplied_base"
	AttrOpAddMultipliedTotal AttributeModifierOp2612 = "add_multiplied_total"
)

// AttributeModifier2612 represents a named modifier attached to an entity attribute.
type AttributeModifier2612 struct {
	ID        string
	Amount    float64
	Operation AttributeModifierOp2612
}

// EvaluateAttributeValue2612 computes `AttributeInstance.calculateValue()` in 26.1.2:
//  1. Sum all `add_value` modifiers onto `baseValue` -> `v0`.
//  2. Multiply `v0 * (1 + sum(add_multiplied_base))` -> `v1`.
//  3. For each `add_multiplied_total` modifier `m`, multiply `v1 *= (1 + m.Amount)`.
func EvaluateAttributeValue2612(baseValue float64, modifiers []AttributeModifier2612) float64 {
	v0 := baseValue
	for _, m := range modifiers {
		if m.Operation == AttrOpAddValue {
			v0 += m.Amount
		}
	}
	multBase := 0.0
	for _, m := range modifiers {
		if m.Operation == AttrOpAddMultipliedBase {
			multBase += m.Amount
		}
	}
	v1 := v0 * (1.0 + multBase)
	for _, m := range modifiers {
		if m.Operation == AttrOpAddMultipliedTotal {
			v1 *= 1.0 + m.Amount
		}
	}
	return v1
}

// ValidateRideCommandMount2612 validates `/ride <rider> mount <vehicle>` in 26.1.2:
//   - Rejects mounting `minecraft:player` (`commands.ride.mount.failure.cant_ride_players`).
//   - Rejects self-mounting or cycles in `vehicleAncestors`.
func ValidateRideCommandMount2612(riderEntityID, vehicleEntityID int32, vehicleEntityType string, vehicleAncestors []int32) error {
	if riderEntityID <= 0 || vehicleEntityID <= 0 {
		return errors.New("ride: invalid entity ID")
	}
	if riderEntityID == vehicleEntityID {
		return errors.New("commands.ride.mount.failure.past_loop")
	}
	if canonicalResourceName(vehicleEntityType) == "minecraft:player" {
		return errors.New("commands.ride.mount.failure.cant_ride_players")
	}
	for _, anc := range vehicleAncestors {
		if anc == riderEntityID {
			return errors.New("commands.ride.mount.failure.past_loop")
		}
	}
	return nil
}

// EvaluateDamageCommandOnMob2612 evaluates `/damage <target> <amount> <damageType>`
// on `mobType` in 26.1.2, checking invulnerability, fire immunity (`in_fire`, `on_fire`, `lava`, `hot_floor`),
// fall immunity (`fall`), and freeze immunity (`freeze`).
func EvaluateDamageCommandOnMob2612(mobType string, amount float32, damageType string, isInvulnerable, hasFireResistance bool) (appliedDamage float32, accepted bool) {
	if amount < 0 {
		return 0, false
	}
	normDmg := canonicalResourceName(damageType)
	if isInvulnerable && normDmg != "minecraft:fell_out_of_world" && normDmg != "minecraft:generic_kill" {
		return 0, false
	}
	switch normDmg {
	case "minecraft:in_fire", "minecraft:on_fire", "minecraft:lava", "minecraft:hot_floor", "minecraft:fireball":
		if IsMobFireImmune2612(mobType, hasFireResistance) {
			return 0, false
		}
	case "minecraft:fall":
		if IsMobInEntityTypeTag2612(mobType, "minecraft:fall_damage_immune") || canonicalResourceName(mobType) == MobTypeEnderDragon {
			return 0, false
		}
	case "minecraft:freeze":
		if IsMobInEntityTypeTag2612(mobType, "minecraft:freeze_immune_entity_types") {
			return 0, false
		}
	}
	return amount, true
}
