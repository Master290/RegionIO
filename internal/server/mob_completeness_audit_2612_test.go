package server

import (
	"math/rand"
	"testing"
)

func TestAll157EntityTypesAnd89LivingMobsCompletenessAudit2612(t *testing.T) {
	allTypes := AllEntityTypesOrdered2612()
	if len(allTypes) != 157 {
		t.Fatalf("AllEntityTypesOrdered2612 len = %d, want 157", len(allTypes))
	}
	for wantID, name := range allTypes {
		gotID, ok := EntityTypeRegistryIDByName2612(name)
		if !ok || gotID != wantID {
			t.Fatalf("EntityTypeRegistryIDByName2612(%q) = (%d, %v), want (%d, true)", name, gotID, ok, wantID)
		}
	}

	living := AllLivingMobTypes2612()
	if len(living) != 89 {
		t.Fatalf("AllLivingMobTypes2612 len = %d, want 89", len(living))
	}
	for _, mob := range living {
		if id := ResolveEntityTypeID(mob); id < 0 {
			t.Fatalf("ResolveEntityTypeID(%q) = %d, want >= 0", mob, id)
		}
		if hp := ResolveMobMaxHealth(mob); hp <= 0 {
			t.Fatalf("ResolveMobMaxHealth(%q) = %v, want > 0", mob, hp)
		}
		spec, ok := LookupMobCompletenessSpec2612(mob)
		if !ok {
			t.Fatalf("LookupMobCompletenessSpec2612(%q) returned false", mob)
		}
		if spec.MaxHealth <= 0 || spec.MovementSpeed <= 0 || spec.Width <= 0 || spec.Height <= 0 || spec.EyeHeight <= 0 {
			t.Fatalf("invalid spec dimensions/attributes for %q: %+v", mob, spec)
		}
		if spec.LootTableID == "" || spec.HurtSound == "" || spec.DeathSound == "" {
			t.Fatalf("missing loot table or sounds for %q: %+v", mob, spec)
		}
		if mob != MobTypeGiant && mob != MobTypeIllusioner {
			if spec.SpawnEggItem == "" {
				t.Fatalf("expected spawn egg for %q", mob)
			}
			resolvedMob, ok := EntityTypeFromSpawnEgg2612(spec.SpawnEggItem)
			if !ok || resolvedMob != mob {
				t.Fatalf("EntityTypeFromSpawnEgg2612(%q) = (%q, %v), want (%q, true)", spec.SpawnEggItem, resolvedMob, ok, mob)
			}
		}
	}

	eggs := AllSpawnEggItems2612()
	if len(eggs) != 87 {
		t.Fatalf("AllSpawnEggItems2612 len = %d, want 87", len(eggs))
	}

	// Verify Polar Bear (ID 104, 30 HP) and its loot table.
	pbSpec, ok := LookupMobCompletenessSpec2612(MobTypePolarBear)
	if !ok || pbSpec.NetworkID != 104 || pbSpec.MaxHealth != 30.0 || pbSpec.AttackDamage != 6.0 {
		t.Fatalf("PolarBear spec = %+v, ok=%v", pbSpec, ok)
	}
	rng := rand.New(rand.NewSource(7))
	smeltedDrops := RollPolarBearDeathLoot2612(true, 3, rng)
	for _, st := range smeltedDrops {
		id := st.ResolvedID()
		if id != "minecraft:cooked_cod" && id != "minecraft:cooked_salmon" {
			t.Fatalf("unexpected smelted polar bear drop: %q", id)
		}
	}
}

func TestEntityTypeTagsCompleteness2612(t *testing.T) {
	living := AllLivingMobTypes2612()
	countInTag := func(tag string) int {
		n := 0
		for _, m := range living {
			if IsMobInEntityTypeTag2612(m, tag) {
				n++
			}
		}
		return n
	}

	if got := countInTag("minecraft:skeletons"); got != 6 {
		t.Fatalf("#minecraft:skeletons count = %d, want 6", got)
	}
	if got := countInTag("minecraft:zombies"); got != 9 {
		t.Fatalf("#minecraft:zombies count = %d, want 9", got)
	}
	if got := countInTag("minecraft:undead"); got != 17 {
		t.Fatalf("#minecraft:undead count = %d, want 17", got)
	}
	if got := countInTag("minecraft:arthropod"); got != 5 {
		t.Fatalf("#minecraft:arthropod count = %d, want 5", got)
	}
	if got := countInTag("minecraft:aquatic"); got != 14 {
		t.Fatalf("#minecraft:aquatic count = %d, want 14", got)
	}
	if got := countInTag("minecraft:illager"); got != 4 {
		t.Fatalf("#minecraft:illager count = %d, want 4", got)
	}
	if got := countInTag("minecraft:raiders"); got != 6 {
		t.Fatalf("#minecraft:raiders count = %d, want 6", got)
	}
	if got := countInTag("minecraft:burn_in_daylight"); got != 10 {
		t.Fatalf("#minecraft:burn_in_daylight count = %d, want 10", got)
	}
	if got := countInTag("minecraft:can_equip_saddle"); got != 11 {
		t.Fatalf("#minecraft:can_equip_saddle count = %d, want 11", got)
	}
	if got := countInTag("minecraft:fall_damage_immune"); got != 18 {
		t.Fatalf("#minecraft:fall_damage_immune count = %d, want 18", got)
	}
	if got := countInTag("minecraft:freeze_immune_entity_types"); got != 4 {
		t.Fatalf("#minecraft:freeze_immune_entity_types count = %d, want 4", got)
	}
	if got := countInTag("minecraft:freeze_hurts_extra_types"); got != 3 {
		t.Fatalf("#minecraft:freeze_hurts_extra_types count = %d, want 3", got)
	}
	if IsMobInEntityTypeTag2612(MobTypeParched, "minecraft:burn_in_daylight") || IsMobInEntityTypeTag2612(MobTypeCamelHusk, "minecraft:burn_in_daylight") {
		t.Fatalf("Parched and CamelHusk must not burn in daylight in 26.1.2")
	}
}

func TestMobBreedingTamingAndLeashVehicles2612(t *testing.T) {
	rng := rand.New(rand.NewSource(42))

	// 1. Food item tags in 26.1.2.
	if !IsMobFoodItem2612(MobTypeWolf, "minecraft:beef") || !IsMobFoodItem2612(MobTypeWolf, "minecraft:rabbit_stew") {
		t.Fatalf("wolf_food mismatch")
	}
	if !IsMobFoodItem2612(MobTypeCamel, "minecraft:cactus") || !IsMobFoodItem2612(MobTypeCamelHusk, "minecraft:rabbit_foot") {
		t.Fatalf("camel / camel_husk food mismatch")
	}
	if !IsMobFoodItem2612(MobTypeZombieHorse, "minecraft:red_mushroom") || !IsMobFoodItem2612(MobTypeNautilus, "minecraft:cod_bucket") {
		t.Fatalf("zombie_horse / nautilus food mismatch")
	}
	if !IsMobFoodItem2612(MobTypeSniffer, "minecraft:torchflower_seeds") || !IsMobFoodItem2612(MobTypeArmadillo, "minecraft:spider_eye") {
		t.Fatalf("sniffer / armadillo food mismatch")
	}

	// 2. Taming & breeding evaluation.
	parrotCookie := EvaluateMobTamingOrBreeding2612(MobTypeParrot, "minecraft:cookie", false, 0, rng)
	if !parrotCookie.PoisonedParrot {
		t.Fatalf("expected cookie to poison parrot")
	}
	pandaLowBamboo := EvaluateMobTamingOrBreeding2612(MobTypePanda, "minecraft:bamboo", false, 4, rng)
	if pandaLowBamboo.EnteredLoveMode {
		t.Fatalf("panda must not enter love mode with < 8 nearby bamboo blocks")
	}
	pandaEnoughBamboo := EvaluateMobTamingOrBreeding2612(MobTypePanda, "minecraft:bamboo", false, 8, rng)
	if !pandaEnoughBamboo.EnteredLoveMode {
		t.Fatalf("panda should enter love mode with >= 8 nearby bamboo blocks")
	}
	snifferBreed := EvaluateMobTamingOrBreeding2612(MobTypeSniffer, "minecraft:torchflower_seeds", false, 0, rng)
	if !snifferBreed.EnteredLoveMode || snifferBreed.SpawnedEggBlock != "minecraft:sniffer_egg" {
		t.Fatalf("sniffer breed = %+v", snifferBreed)
	}

	// 3. Leash & boat/minecart passenger rules.
	if !CanMobBeLeashed2612(MobTypeCow, false) || !CanMobBeLeashed2612(MobTypeCamel, false) {
		t.Fatalf("cow and camel should be leashable")
	}
	if CanMobBeLeashed2612(MobTypeWolf, true) || CanMobBeLeashed2612(MobTypeVillager, false) || CanMobBeLeashed2612(MobTypeWarden, false) {
		t.Fatalf("angry wolf, villager, and warden must not be leashable")
	}
	if VehicleMaxMobPassengers2612("minecraft:oak_boat") != 2 || VehicleMaxMobPassengers2612("minecraft:oak_chest_boat") != 1 || VehicleMaxMobPassengers2612("minecraft:minecart") != 1 {
		t.Fatalf("VehicleMaxMobPassengers2612 mismatch")
	}
	if !CanMobMountVehicle2612(MobTypeZombie, "minecraft:oak_boat", 1) || CanMobMountVehicle2612(MobTypeWarden, "minecraft:oak_boat", 0) {
		t.Fatalf("CanMobMountVehicle2612 mismatch")
	}
}

func TestMobEnvironmentalDamagePotionEffectsMetadataNBTPersistenceAndItemInteractions2612(t *testing.T) {
	// 1. Fall damage & fire immunity.
	if dmg := ComputeMobFallDamage2612(MobTypeCat, 20.0); dmg != 0 {
		t.Fatalf("cat fall damage = %v, want 0", dmg)
	}
	if dmg := ComputeMobFallDamage2612(MobTypeGoat, 12.0); dmg != 0 {
		t.Fatalf("goat 12-block fall damage = %v, want 0 (10-block reduction)", dmg)
	}
	if dmg := ComputeMobFallDamage2612(MobTypeZombie, 10.0); dmg != 7.0 {
		t.Fatalf("zombie 10-block fall damage = %v, want 7.0", dmg)
	}
	if !IsMobFireImmune2612(MobTypeBlaze, false) || !IsMobFireImmune2612(MobTypeZombifiedPiglin, false) || IsMobFireImmune2612(MobTypeZombie, false) {
		t.Fatalf("IsMobFireImmune2612 mismatch")
	}

	// 2. Air supply & drowning.
	air, drownDmg := StepMobAirSupplyAndDrowning2612(MobTypeCow, -19, true, false)
	if air != 0 || drownDmg != 2.0 {
		t.Fatalf("StepMobAirSupplyAndDrowning2612 at -19 = (%d, %v), want (0, 2.0)", air, drownDmg)
	}
	// Undead breathe underwater without losing air.
	uAir, uDmg := StepMobAirSupplyAndDrowning2612(MobTypeZombie, 300, true, false)
	if uAir != 300 || uDmg != 0 {
		t.Fatalf("undead underwater air = (%d, %v), want (300, 0)", uAir, uDmg)
	}

	// 3. Powder snow freezing.
	if tf, fd := StepMobPowderSnowFreezing2612(MobTypePolarBear, 140, true, false, 40); tf != 0 || fd != 0 {
		t.Fatalf("polar_bear must be immune to powder snow freezing")
	}
	if tf, fd := StepMobPowderSnowFreezing2612(MobTypeBlaze, 140, true, false, 40); tf != 140 || fd != 5.0 {
		t.Fatalf("blaze freeze damage = (%d, %v), want (140, 5.0)", tf, fd)
	}

	// 4. Potion effect immunity & undead healing/harm inversion.
	if CanMobBeAffectedByPotionEffect2612(MobTypeZombie, "minecraft:poison") || CanMobBeAffectedByPotionEffect2612(MobTypeSpider, "minecraft:poison") {
		t.Fatalf("zombie and spider must ignore poison")
	}
	if CanMobBeAffectedByPotionEffect2612(MobTypeParched, "minecraft:weakness") {
		t.Fatalf("parched must ignore weakness")
	}
	if delta := ResolveInstantHealthOrDamageOnMob2612(MobTypeZombie, "minecraft:instant_health", 0); delta >= 0 {
		t.Fatalf("instant_health on undead zombie = %v, want < 0 (damage)", delta)
	}
	if delta := ResolveInstantHealthOrDamageOnMob2612(MobTypeZombie, "minecraft:instant_damage", 0); delta <= 0 {
		t.Fatalf("instant_damage on undead zombie = %v, want > 0 (heal)", delta)
	}

	// 5. Shared entity flags, Name Tag & NBT persistence round-trip.
	flags := BuildEntitySharedFlagsByte2612(true, false, false, false, true, true, false)
	if flags != (EntitySharedFlagOnFire | EntitySharedFlagInvisible | EntitySharedFlagGlowing) {
		t.Fatalf("flags = 0x%02x", flags)
	}
	orig := MobPersistentNBTData2612{
		ID:           MobTypeWolf,
		Health:       40.0,
		Air:          300,
		Age:          0,
		OwnerUUID:    "00112233-4455-6677-8899-aabbccddeeff",
		Sitting:      true,
		CollarColor:  14,
		Glowing:      true,
		Invisible:    false,
		Team:         "red_team",
	}
	if !ApplyNameTagToMob2612(&orig, "Ghost") || !orig.PersistenceRequired || orig.CustomName != "Ghost" {
		t.Fatalf("ApplyNameTagToMob2612 failed: %+v", orig)
	}
	rawNBT, err := EncodeMobPersistentNBT2612(orig)
	if err != nil {
		t.Fatalf("EncodeMobPersistentNBT2612 error: %v", err)
	}
	decoded, err := DecodeMobPersistentNBT2612(rawNBT)
	if err != nil {
		t.Fatalf("DecodeMobPersistentNBT2612 error: %v", err)
	}
	if decoded.ID != orig.ID || decoded.CustomName != "Ghost" || !decoded.PersistenceRequired || !decoded.Glowing || decoded.Team != "red_team" || decoded.Health != 40.0 {
		t.Fatalf("decoded NBT mismatch: %+v vs %+v", decoded, orig)
	}

	// 6. Item interactions with mobs (saddle, shears, bucket, water_bucket, bowl, bottle, brush).
	rng := rand.New(rand.NewSource(99))
	if res := InteractItemWithMob2612(MobTypeCamelHusk, "minecraft:saddle", false, false, false, false, rng); !res.EquippedSaddle {
		t.Fatalf("expected camel_husk to accept saddle: %+v", res)
	}
	if res := InteractItemWithMob2612(MobTypePig, "minecraft:shears", false, true, false, false, rng); !res.RemovedSaddle {
		t.Fatalf("expected shears to remove saddle from saddled pig: %+v", res)
	}
	if res := InteractItemWithMob2612(MobTypeMooshroom, "minecraft:shears", false, false, false, false, rng); res.ConvertedToMobType != MobTypeCow || len(res.DroppedItems) != 1 || res.DroppedItems[0].Count != 5 {
		t.Fatalf("mooshroom shear result = %+v", res)
	}
	if res := InteractItemWithMob2612(MobTypeGoat, "minecraft:bucket", false, false, false, false, rng); res.ReplacementItemID != "minecraft:milk_bucket" {
		t.Fatalf("goat bucket result = %+v", res)
	}
	if res := InteractItemWithMob2612(MobTypeAxolotl, "minecraft:water_bucket", false, false, false, false, rng); res.ReplacementItemID != "minecraft:axolotl_bucket" || !res.ScoopedMobIntoItem || !res.PersistenceGranted {
		t.Fatalf("axolotl water_bucket result = %+v", res)
	}
	if res := InteractItemWithMob2612(MobTypeMooshroom, "minecraft:bowl", false, false, false, true, rng); res.ReplacementItemID != "minecraft:suspicious_stew" {
		t.Fatalf("brown mooshroom bowl with flower result = %+v", res)
	}
	if res := InteractItemWithMob2612(MobTypeArmadillo, "minecraft:brush", false, false, false, false, rng); res.ToolDurabilityCost != 16 || len(res.DroppedItems) != 1 || res.DroppedItems[0].ResolvedID() != "minecraft:armadillo_scute" {
		t.Fatalf("armadillo brush result = %+v", res)
	}
}
