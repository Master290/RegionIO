package server

import (
	"math"
	"math/rand"
	"testing"

	"regionio/internal/world"
)

func TestHostileNetherMobsHitboxesAttributesAndSounds2612(t *testing.T) {
	cases := []struct {
		mobType string
		w, h    float32
		eye     float32
		hp      float32
		dmg     float32
		xp      int32
	}{
		{MobTypeEnderman, 0.6, 2.9, 2.55, 40.0, 7.0, 5},
		{MobTypeEndermite, 0.4, 0.3, 0.13, 8.0, 2.0, 3},
		{MobTypeSilverfish, 0.4, 0.3, 0.13, 8.0, 1.0, 5},
		{MobTypeShulker, 1.0, 1.0, 0.5, 30.0, 4.0, 5},
		{MobTypePhantom, 0.9, 0.5, 0.175, 20.0, 6.0, 5},
		{MobTypePiglin, 0.6, 1.95, 1.79, 16.0, 5.0, 5},
		{MobTypePiglinBrute, 0.6, 1.95, 1.79, 50.0, 7.0, 20},
		{MobTypeZombifiedPiglin, 0.6, 1.95, 1.79, 20.0, 5.0, 5},
		{MobTypeHoglin, 1.3964844, 1.4, 1.19, 40.0, 6.0, 5},
		{MobTypeZoglin, 1.3964844, 1.4, 1.19, 40.0, 6.0, 5},
		{MobTypeStrider, 0.9, 1.7, 1.445, 20.0, 0.0, 2},
		{MobTypeRavager, 1.95, 2.2, 1.87, 100.0, 12.0, 20},
		{MobTypeEvoker, 0.6, 1.95, 1.6575, 24.0, 0.0, 10},
		{MobTypeVex, 0.4, 0.8, 0.51875, 14.0, 4.0, 5},
		{MobTypeWitch, 0.6, 1.95, 1.62, 26.0, 0.0, 5},
		{MobTypeCreeper, 0.6, 1.7, 1.445, 20.0, 0.0, 5},
		{MobTypeSkeleton, 0.6, 1.99, 1.74, 20.0, 2.0, 5},
		{MobTypeStray, 0.6, 1.99, 1.74, 20.0, 2.0, 5},
		{MobTypeWitherSkeleton, 0.7, 2.4, 2.1, 20.0, 4.0, 5},
		{MobTypeSpider, 1.4, 0.9, 0.65, 16.0, 2.0, 5},
		{MobTypeCaveSpider, 0.7, 0.5, 0.45, 12.0, 2.0, 5},
		{MobTypeBlaze, 0.6, 1.8, 1.53, 20.0, 6.0, 10},
		{MobTypeGhast, 4.0, 4.0, 2.6, 10.0, 0.0, 5},
		{MobTypeMagmaCube, 0.52, 0.52, 0.325, 16.0, 6.0, 4},
		{MobTypeGuardian, 0.85, 0.85, 0.425, 30.0, 6.0, 10},
		{MobTypeElderGuardian, 1.9975, 1.9975, 0.99875, 80.0, 8.0, 10},
		{MobTypeIronGolem, 1.4, 2.7, 2.295, 100.0, 15.0, 0},
		{MobTypeSnowGolem, 0.7, 1.9, 1.7, 4.0, 0.0, 0},
		{MobTypeCopperGolem, 0.49, 0.98, 0.8125, 12.0, 0.0, 0},
		{MobTypeSkeletonHorse, 1.3964844, 1.6, 1.52, 15.0, 0.0, 2},
		{MobTypeZombieHorse, 1.3964844, 1.6, 1.52, 25.0, 0.0, 2},
	}

	for _, tc := range cases {
		spec, ok := ResolveMobHitboxAndAttributes2612(tc.mobType)
		if !ok {
			t.Fatalf("missing hitbox/attribute spec for %s", tc.mobType)
		}
		if math.Abs(float64(spec.Width-tc.w)) > 1e-4 || math.Abs(float64(spec.Height-tc.h)) > 1e-4 || math.Abs(float64(spec.EyeHeight-tc.eye)) > 1e-4 {
			t.Fatalf("%s hitbox = (%v, %v, eye=%v), want (%v, %v, eye=%v)",
				tc.mobType, spec.Width, spec.Height, spec.EyeHeight, tc.w, tc.h, tc.eye)
		}
		if spec.MaxHealth != tc.hp || spec.AttackDamage != tc.dmg || spec.XPReward != tc.xp {
			t.Fatalf("%s stats = (hp=%v, dmg=%v, xp=%d), want (hp=%v, dmg=%v, xp=%d)",
				tc.mobType, spec.MaxHealth, spec.AttackDamage, spec.XPReward, tc.hp, tc.dmg, tc.xp)
		}
		snd, sndOK := ResolveMobSoundSet2612(tc.mobType)
		if !sndOK || snd.Hurt == "" || snd.Death == "" {
			t.Fatalf("missing or incomplete sound set for %s: %+v", tc.mobType, snd)
		}
	}

	// Verify MagmaCube & Phantom size scaling.
	mw, mh, meye, mhp, mdmg, marmor := ScaleSlimeOrMagmaCubeDimensions2612(MobTypeMagmaCube, 4)
	if math.Abs(float64(mw-2.08)) > 1e-4 || math.Abs(float64(mh-2.08)) > 1e-4 || math.Abs(float64(meye-1.3)) > 1e-4 || mhp != 16.0 || mdmg != 6.0 || marmor != 12.0 {
		t.Fatalf("MagmaCube size 4 scaling = (%v, %v, %v, hp=%v, dmg=%v, armor=%v)", mw, mh, meye, mhp, mdmg, marmor)
	}
	pw, ph, pdmg := ScalePhantomDimensions2612(2)
	if math.Abs(float64(pw-1.3)) > 1e-4 || math.Abs(float64(ph-0.7)) > 1e-4 || pdmg != 8.0 {
		t.Fatalf("Phantom size 2 scaling = (%v, %v, dmg=%v), want (1.3, 0.7, 8.0)", pw, ph, pdmg)
	}
}

func TestEndermanShulkerEndermiteSilverfishAndPhantom2612(t *testing.T) {
	// Enderman holdable block tag & Silverfish infested block mapping.
	if !IsEndermanHoldableBlockName("minecraft:grass_block") || IsEndermanHoldableBlockName("minecraft:bedrock") {
		t.Fatalf("IsEndermanHoldableBlockName mismatch")
	}
	if inf, ok := InfestedBlockForHostBlock2612("minecraft:stone_bricks"); !ok || inf != "minecraft:infested_stone_bricks" {
		t.Fatalf("InfestedBlockForHostBlock2612(stone_bricks) = (%q, %v)", inf, ok)
	}
	if _, ok := InfestedBlockForHostBlock2612("minecraft:obsidian"); ok {
		t.Fatalf("expected obsidian not to be infestable by silverfish")
	}

	// Shulker closed vs open armor.
	shulkerSpec, _ := ResolveMobHitboxAndAttributes2612(MobTypeShulker)
	if shulkerSpec.Armor != 20.0 || shulkerSpec.KnockbackResistance != 1.0 {
		t.Fatalf("expected closed Shulker to have 20 armor and 1.0 knockback resistance, got (%v, %v)",
			shulkerSpec.Armor, shulkerSpec.KnockbackResistance)
	}

	// Endermite lifetime & pearl spawn chance.
	if EndermiteMaxLifetimeTicks != 2400 || EndermiteEnderPearlSpawnChance != 0.05 {
		t.Fatalf("unexpected Endermite constants: lifetime=%d chance=%v", EndermiteMaxLifetimeTicks, EndermiteEnderPearlSpawnChance)
	}

	// Phantom insomnia threshold & size metadata.
	if PhantomInsomniaMinTicks != 72000 {
		t.Fatalf("PhantomInsomniaMinTicks = %d, want 72000", PhantomInsomniaMinTicks)
	}
}

func TestPiglinHoglinZoglinStriderAndRepellents2612(t *testing.T) {
	srv := newSurvivalTestServer(t)
	cache := srv.DimensionChunks(world.DimensionNether)
	for x := -4; x <= 4; x++ {
		for z := -4; z <= 4; z++ {
			cache.SetBlock(x, 63, z, world.StateStone)
			cache.SetBlock(x, 64, z, world.StateAir)
		}
	}
	soulFire, ok := world.BlockStateByName("minecraft:soul_fire", nil)
	if !ok {
		t.Fatalf("missing minecraft:soul_fire state")
	}
	warpedFungus, ok := world.BlockStateByName("minecraft:warped_fungus", nil)
	if !ok {
		t.Fatalf("missing minecraft:warped_fungus state")
	}
	cache.SetBlock(2, 64, -1, soulFire)
	cache.SetBlock(-3, 64, 2, warpedFungus)

	if !IsPiglinRepellentBlock2612(soulFire) || IsPiglinRepellentBlock2612(warpedFungus) {
		t.Fatalf("IsPiglinRepellentBlock2612 mismatch")
	}
	if !IsHoglinRepellentBlock2612(warpedFungus) || IsHoglinRepellentBlock2612(soulFire) {
		t.Fatalf("IsHoglinRepellentBlock2612 mismatch")
	}

	rx, ry, rz, found := FindNearestRepellentBlock2612(cache, 0, 64, 0, 8, 4, IsPiglinRepellentBlock2612)
	if !found || rx != 2 || ry != 64 || rz != -1 {
		t.Fatalf("expected nearest piglin repellent at (2,64,-1), got (%d,%d,%d) found=%v", rx, ry, rz, found)
	}
	hx, hy, hz, hFound := FindNearestRepellentBlock2612(cache, 0, 64, 0, 8, 4, IsHoglinRepellentBlock2612)
	if !hFound || hx != -3 || hy != 64 || hz != 2 {
		t.Fatalf("expected nearest hoglin repellent at (-3,64,2), got (%d,%d,%d) found=%v", hx, hy, hz, hFound)
	}
}

func TestRavagerEvokerVexWitchCreeperSkeletonSpiderBlazeGhastMagmaGuardianGolemAndHorseTrap2612(t *testing.T) {
	rng := rand.New(rand.NewSource(261225))

	// Cave spider difficulty-scaled poison duration.
	if CaveSpiderPoisonDurationTicks2612(DifficultyEasy) != 0 {
		t.Fatalf("Easy cave spider poison = %d, want 0", CaveSpiderPoisonDurationTicks2612(DifficultyEasy))
	}
	if CaveSpiderPoisonDurationTicks2612(DifficultyNormal) != 140 {
		t.Fatalf("Normal cave spider poison = %d, want 140", CaveSpiderPoisonDurationTicks2612(DifficultyNormal))
	}
	if CaveSpiderPoisonDurationTicks2612(DifficultyHard) != 300 {
		t.Fatalf("Hard cave spider poison = %d, want 300", CaveSpiderPoisonDurationTicks2612(DifficultyHard))
	}

	// Zombie horse attribute generators from ZombieHorse.class in 26.1.2.jar.
	for i := 0; i < 20; i++ {
		jump := GenerateZombieHorseJumpStrength2612(rng)
		if jump < 0.5 || jump > 0.700001 {
			t.Fatalf("GenerateZombieHorseJumpStrength2612 = %v, want in [0.5, 0.7]", jump)
		}
		spd := GenerateZombieHorseSpeed2612(rng)
		if spd < 9.0/42.16-1e-5 || spd > 12.0/42.16+1e-5 {
			t.Fatalf("GenerateZombieHorseSpeed2612 = %v, want in [0.2134, 0.2847]", spd)
		}
	}

	// Verify loot table drops across groups.
	evokerDrops := RollHostileNetherMobDrops2612(MobTypeEvoker, 3, false, true, false, false, rng)
	hasTotem := false
	for _, st := range evokerDrops {
		if st.ResolvedID() == "minecraft:totem_of_undying" && st.Count == 1 {
			hasTotem = true
		}
	}
	if !hasTotem {
		t.Fatalf("expected Evoker to always drop 1x totem_of_undying, got %+v", evokerDrops)
	}

	ravagerDrops := RollHostileNetherMobDrops2612(MobTypeRavager, 0, false, true, false, false, rng)
	if len(ravagerDrops) != 1 || ravagerDrops[0].ResolvedID() != "minecraft:saddle" {
		t.Fatalf("expected Ravager to drop 1x saddle, got %+v", ravagerDrops)
	}

	witherSkelChargedDrops := RollHostileNetherMobDrops2612(MobTypeWitherSkeleton, 0, false, true, false, true, rng)
	hasWitherSkull := false
	for _, st := range witherSkelChargedDrops {
		if st.ResolvedID() == "minecraft:wither_skeleton_skull" {
			hasWitherSkull = true
		}
	}
	if !hasWitherSkull {
		t.Fatalf("expected Wither Skeleton killed by Charged Creeper to drop wither_skeleton_skull, got %+v", witherSkelChargedDrops)
	}

	creeperDiscDrops := RollHostileNetherMobDrops2612(MobTypeCreeper, 0, false, false, true, true, rng)
	hasDisc := false
	hasCreeperHead := false
	for _, st := range creeperDiscDrops {
		if st.ResolvedID() == "minecraft:music_disc_cat" {
			hasDisc = true
		}
		if st.ResolvedID() == "minecraft:creeper_head" {
			hasCreeperHead = true
		}
	}
	if !hasDisc || !hasCreeperHead {
		t.Fatalf("expected Creeper killed by skeleton + charged creeper to drop disc and head, got %+v", creeperDiscDrops)
	}

	elderDrops := RollHostileNetherMobDrops2612(MobTypeElderGuardian, 0, false, true, false, false, rng)
	hasWetSponge := false
	for _, st := range elderDrops {
		if st.ResolvedID() == "minecraft:wet_sponge" {
			hasWetSponge = true
		}
	}
	if !hasWetSponge {
		t.Fatalf("expected Elder Guardian killed by player to drop wet_sponge, got %+v", elderDrops)
	}

	golemDrops := RollHostileNetherMobDrops2612(MobTypeIronGolem, 3, false, true, false, false, rng)
	hasIron := false
	for _, st := range golemDrops {
		if st.ResolvedID() == "minecraft:iron_ingot" && st.Count >= 3 && st.Count <= 5 {
			hasIron = true
		}
	}
	if !hasIron {
		t.Fatalf("expected Iron Golem to drop 3..5 iron_ingot, got %+v", golemDrops)
	}
}
