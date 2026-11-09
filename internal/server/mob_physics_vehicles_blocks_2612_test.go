package server

import (
	"math"
	"testing"
)

func TestMountSeatsUnderwaterDismountAndPassengerAttachment2612(t *testing.T) {
	// 1. Boats (2 seats), ChestBoats (1 seat), Minecart (1 seat), Camel (2 seats), HappyGhast (4 seats).
	if spec, ok := LookupMountSeatSpec2612("minecraft:oak_boat"); !ok || spec.MaxPassengers != 2 || !spec.DismountsUnderwater {
		t.Fatalf("oak_boat seat spec = %+v, ok=%v", spec, ok)
	}
	if spec, ok := LookupMountSeatSpec2612("minecraft:oak_chest_boat"); !ok || spec.MaxPassengers != 1 {
		t.Fatalf("oak_chest_boat seat spec = %+v, ok=%v", spec, ok)
	}
	if spec, ok := LookupMountSeatSpec2612("minecraft:minecart"); !ok || spec.MaxPassengers != 1 {
		t.Fatalf("minecart seat spec = %+v, ok=%v", spec, ok)
	}
	if spec, ok := LookupMountSeatSpec2612(MobTypeCamel); !ok || spec.MaxPassengers != 2 || !spec.CanFloatWhileRidden || !spec.DismountsUnderwater {
		t.Fatalf("camel seat spec = %+v, ok=%v", spec, ok)
	}
	// CamelHusk, SkeletonHorse, Nautilus, ZombieNautilus do NOT dismount underwater!
	for _, m := range []string{MobTypeCamelHusk, MobTypeSkeletonHorse, MobTypeNautilus, MobTypeZombieNautilus} {
		spec, ok := LookupMountSeatSpec2612(m)
		if !ok || spec.DismountsUnderwater {
			t.Fatalf("%s seat spec = %+v, ok=%v; want DismountsUnderwater=false", m, spec, ok)
		}
	}
	if spec, ok := LookupMountSeatSpec2612(MobTypeHappyGhast); !ok || spec.MaxPassengers != 4 || !spec.RequiresHarness || spec.PassengerYOffset != 4.0 {
		t.Fatalf("happy_ghast seat spec = %+v, ok=%v", spec, ok)
	}
	if spec, ok := LookupMountSeatSpec2612(MobTypePig); !ok || spec.SteeringItemID != "minecraft:carrot_on_a_stick" {
		t.Fatalf("pig seat spec = %+v, ok=%v", spec, ok)
	}
	if spec, ok := LookupMountSeatSpec2612(MobTypeStrider); !ok || spec.SteeringItemID != "minecraft:warped_fungus_on_a_stick" || spec.SteeringSpeedBoost != 1.50 {
		t.Fatalf("strider seat spec = %+v, ok=%v", spec, ok)
	}

	// 2. Passenger attachment position for front and back seats on Camel.
	_, py0, pz0 := ComputePassengerAttachmentPosition2612(MobTypeCamel, 0, 64, 0, 0, 0)
	_, py1, pz1 := ComputePassengerAttachmentPosition2612(MobTypeCamel, 0, 64, 0, 0, 1)
	if math.Abs(py0-(64+1.90625)) > 1e-5 || py0 != py1 || pz0 <= pz1 {
		t.Fatalf("camel front/back passenger positions = (y=%v, z0=%v, z1=%v)", py0, pz0, pz1)
	}
}

func TestLeashPhysicsKnockbackAndEntityCramming2612(t *testing.T) {
	// 1. Leash elastic pull (6..10 blocks) and snap (> 10 blocks).
	dvx, _, _, snapped := StepLeashPhysics2612(0, 64, 0, 5, 64, 0, 10.0)
	if snapped || dvx != 0 {
		t.Fatalf("leash at 5 blocks = (dvx=%v, snapped=%v), want (0, false)", dvx, snapped)
	}
	dvx8, _, _, snapped8 := StepLeashPhysics2612(0, 64, 0, 8, 64, 0, 10.0)
	if snapped8 || dvx8 <= 0 {
		t.Fatalf("leash at 8 blocks = (dvx=%v, snapped=%v), want (>0, false)", dvx8, snapped8)
	}
	_, _, _, snapped11 := StepLeashPhysics2612(0, 64, 0, 11, 64, 0, 10.0)
	if !snapped11 {
		t.Fatalf("expected leash at 11 blocks to snap")
	}

	// 2. Knockback velocity & knockback resistance.
	nvx, nvy, _ := ApplyMobKnockbackVelocity2612(0, 0, 0, 0.4, 1.0, 0.0, 0.0, true)
	if math.Abs(nvx - -0.4) > 1e-6 || math.Abs(nvy-0.4) > 1e-6 {
		t.Fatalf("knockback(0.4, res=0) = (%v, %v), want (-0.4, 0.4)", nvx, nvy)
	}
	nvxImmune, nvyImmune, _ := ApplyMobKnockbackVelocity2612(0, 0, 0, 0.4, 1.0, 0.0, 1.0, true)
	if nvxImmune != 0 || nvyImmune != 0 {
		t.Fatalf("knockback(0.4, res=1.0) = (%v, %v), want (0, 0)", nvxImmune, nvyImmune)
	}

	// 3. Entity cramming (maxEntityCramming = 24 -> 6.0 cramming damage) & push separation.
	cramDmg, pushVX, _ := EvaluateEntityCrammingAndPush2612(24, 24, 0.25, 0)
	if cramDmg != 6.0 || pushVX >= 0 {
		t.Fatalf("cramming(24/24) = (dmg=%v, pushVX=%v), want (6.0, <0)", cramDmg, pushVX)
	}
	if cramDmg0, _, _ := EvaluateEntityCrammingAndPush2612(23, 24, 0.25, 0); cramDmg0 != 0 {
		t.Fatalf("cramming(23/24) = %v, want 0", cramDmg0)
	}
}

func TestBlockMovementPhysicsAndContactDamage2612(t *testing.T) {
	// 1. Ice, Blue Ice, Slime Block, Honey Block, Soul Sand.
	if p := LookupBlockMovementPhysics2612("minecraft:blue_ice", MobTypeZombie, false, false, false, false); math.Abs(float64(p.Friction-0.989)) > 1e-5 {
		t.Fatalf("blue_ice friction = %v, want 0.989", p.Friction)
	}
	slimeP := LookupBlockMovementPhysics2612("minecraft:slime_block", MobTypeZombie, false, false, false, false)
	if slimeP.FallDamageMultiplier != 0.0 || slimeP.BounceFactor != 1.0 {
		t.Fatalf("slime_block (unsneaked) = %+v", slimeP)
	}
	honeyP := LookupBlockMovementPhysics2612("minecraft:honey_block", MobTypeZombie, false, false, false, false)
	if math.Abs(float64(honeyP.SpeedFactor-0.4)) > 1e-5 || math.Abs(float64(honeyP.JumpFactor-0.5)) > 1e-5 || math.Abs(float64(honeyP.FallDamageMultiplier-0.2)) > 1e-5 {
		t.Fatalf("honey_block = %+v", honeyP)
	}
	soulSandSlow := LookupBlockMovementPhysics2612("minecraft:soul_sand", MobTypeZombie, false, false, false, false)
	soulSandFast := LookupBlockMovementPhysics2612("minecraft:soul_sand", MobTypeZombie, false, true, false, false)
	if math.Abs(float64(soulSandSlow.SpeedFactor-0.4)) > 1e-5 || soulSandFast.SpeedFactor != 1.0 {
		t.Fatalf("soul_sand speedFactor = (slow=%v, soulSpeed=%v)", soulSandSlow.SpeedFactor, soulSandFast.SpeedFactor)
	}

	// 2. Cobweb (normal vs Weaving / Spider), Sweet Berry Bush (Fox immune), Magma Block, Campfire, Lava.
	webNorm := LookupBlockMovementPhysics2612("minecraft:cobweb", MobTypeZombie, false, false, false, false)
	webWeaving := LookupBlockMovementPhysics2612("minecraft:cobweb", MobTypeZombie, false, false, true, false)
	if webNorm.InsideStuckX != 0.25 || webWeaving.InsideStuckX != 0.5 || !webNorm.ResetsFallDistance {
		t.Fatalf("cobweb = (norm=%+v, weaving=%+v)", webNorm, webWeaving)
	}
	berryZombie := LookupBlockMovementPhysics2612("minecraft:sweet_berry_bush", MobTypeZombie, false, false, false, false)
	berryFox := LookupBlockMovementPhysics2612("minecraft:sweet_berry_bush", MobTypeFox, false, false, false, false)
	if berryZombie.ContactDamagePerTick != 1.0 || berryFox.ContactDamagePerTick != 0 {
		t.Fatalf("sweet_berry_bush damage = (zombie=%v, fox=%v)", berryZombie.ContactDamagePerTick, berryFox.ContactDamagePerTick)
	}
	lavaNorm := LookupBlockMovementPhysics2612("minecraft:lava", MobTypeZombie, false, false, false, false)
	lavaImmune := LookupBlockMovementPhysics2612("minecraft:lava", MobTypeStrider, false, false, false, true)
	if lavaNorm.ContactDamagePerTick != 4.0 || lavaNorm.SetsOnFireTicks != 300 || lavaImmune.ContactDamagePerTick != 0 {
		t.Fatalf("lava = (norm=%+v, immune=%+v)", lavaNorm, lavaImmune)
	}
}

func TestMovementAttributesAndRideDamageAttributeCommands2612(t *testing.T) {
	// 1. Movement attributes & attribute-based fall damage.
	camelAttrs := DefaultMobMovementAttributes2612(MobTypeCamel)
	if camelAttrs.StepHeight != 1.5 || camelAttrs.SafeFallDistance != 6.0 || camelAttrs.FallDamageMultiplier != 0.5 {
		t.Fatalf("camel movement attrs = %+v", camelAttrs)
	}
	// Camel falling 10 blocks on normal ground: ceil((10 - 6) * 0.5 * 1.0) = 2.0.
	if dmg := ComputeAttributeFallDamage2612(10.0, camelAttrs, 1.0); dmg != 2.0 {
		t.Fatalf("camel 10-block fall damage = %v, want 2.0", dmg)
	}
	// Falling on honey block (blockFallDamageMult = 0.2): ceil((13 - 3) * 1.0 * 0.2) = 2.0.
	zombieAttrs := DefaultMobMovementAttributes2612(MobTypeZombie)
	if dmg := ComputeAttributeFallDamage2612(13.0, zombieAttrs, 0.2); dmg != 2.0 {
		t.Fatalf("zombie 13-block fall onto honey_block = %v, want 2.0", dmg)
	}

	// 2. Attribute modifier calculation (`add_value`, `add_multiplied_base`, `add_multiplied_total`).
	mods := []AttributeModifier2612{
		{ID: "bonus_flat", Amount: 2.0, Operation: AttrOpAddValue},
		{ID: "bonus_base", Amount: 0.5, Operation: AttrOpAddMultipliedBase},
		{ID: "bonus_total", Amount: 0.2, Operation: AttrOpAddMultipliedTotal},
	}
	// (10 + 2) * (1 + 0.5) * (1 + 0.2) = 12 * 1.5 * 1.2 = 21.6
	if val := EvaluateAttributeValue2612(10.0, mods); math.Abs(val-21.6) > 1e-6 {
		t.Fatalf("EvaluateAttributeValue2612 = %v, want 21.6", val)
	}

	// 3. /ride and /damage command validation.
	if err := ValidateRideCommandMount2612(10, 10, MobTypeHorse, nil); err == nil {
		t.Fatalf("expected self-mount to fail")
	}
	if err := ValidateRideCommandMount2612(10, 20, "minecraft:player", nil); err == nil {
		t.Fatalf("expected mounting a player to fail")
	}
	if err := ValidateRideCommandMount2612(10, 20, MobTypeHorse, []int32{30, 10}); err == nil {
		t.Fatalf("expected cycle mount to fail")
	}
	if err := ValidateRideCommandMount2612(10, 20, MobTypeHorse, []int32{30}); err != nil {
		t.Fatalf("unexpected valid mount error: %v", err)
	}

	if _, ok := EvaluateDamageCommandOnMob2612(MobTypeBlaze, 5.0, "minecraft:lava", false, false); ok {
		t.Fatalf("blaze must reject lava damage in /damage")
	}
	if dmg, ok := EvaluateDamageCommandOnMob2612(MobTypeBlaze, 5.0, "minecraft:generic_kill", true, false); !ok || dmg != 5.0 {
		t.Fatalf("generic_kill must bypass invulnerability in /damage")
	}
}
