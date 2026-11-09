package server

import (
	"math"
	"math/rand"
	"testing"

	"regionio/internal/world"
)

func TestEquine2612GeneticsFoodAndZombieHorsePolish(t *testing.T) {
	srv := newSurvivalTestServer(t)
	player := newBeeTestPlayer(t, srv, "EquineRider", 0, 64, 0)
	rng := rand.New(rand.NewSource(2612))

	// 1. Verify AbstractHorse 26.1.2 slot & movement constants and breeding cross factor (0.15) with reflection clamping.
	if AbstractHorseChestSlotOffset != 499 || AbstractHorseInventorySlotOffset != 500 || AbstractHorseInventoryRows != 3 {
		t.Fatalf("unexpected AbstractHorse slot constants: chest=%d inv=%d rows=%d",
			AbstractHorseChestSlotOffset, AbstractHorseInventorySlotOffset, AbstractHorseInventoryRows)
	}
	for i := 0; i < 200; i++ {
		hp, spd, jmp := InheritEquineAttributes2612(15.0, 30.0, MinHorseMovementSpeed, MaxHorseMovementSpeed, MinHorseJumpStrength, MaxHorseJumpStrength, rng)
		if hp < MinEquineMaxHealth || hp > MaxEquineMaxHealth {
			t.Fatalf("offspring MaxHealth %v out of [%v, %v]", hp, MinEquineMaxHealth, MaxEquineMaxHealth)
		}
		if spd < MinHorseMovementSpeed || spd > MaxHorseMovementSpeed {
			t.Fatalf("offspring MovementSpeed %v out of [%v, %v]", spd, MinHorseMovementSpeed, MaxHorseMovementSpeed)
		}
		if jmp < MinHorseJumpStrength || jmp > MaxHorseJumpStrength {
			t.Fatalf("offspring JumpStrength %v out of [%v, %v]", jmp, MinHorseJumpStrength, MaxHorseJumpStrength)
		}
	}

	// 2. Verify #minecraft:horse_food (including carrot) and #minecraft:zombie_horse_food (red_mushroom).
	for _, food := range []string{
		"minecraft:wheat", "minecraft:sugar", "minecraft:hay_block",
		"minecraft:apple", "minecraft:carrot", "minecraft:golden_carrot",
		"minecraft:golden_apple", "minecraft:enchanted_golden_apple",
	} {
		if !IsHorseFoodItem2612(food) {
			t.Fatalf("expected %s in #minecraft:horse_food", food)
		}
		if _, _, _, _, ok := EquineFoodDetails2612(MobTypeHorse, food); !ok {
			t.Fatalf("expected EquineFoodDetails2612(horse, %s) to succeed", food)
		}
	}
	if !IsZombieHorseFoodItem("minecraft:red_mushroom") {
		t.Fatal("expected minecraft:red_mushroom in #minecraft:zombie_horse_food")
	}
	zHeal, zAge, zTemper, zLove, zOK := EquineFoodDetails2612(MobTypeZombieHorse, "minecraft:red_mushroom")
	if !zOK || zHeal != 3.0 || zAge != 0 || zTemper != 3 || zLove {
		t.Fatalf("EquineFoodDetails2612(zombie_horse, red_mushroom) = (%v, %d, %d, %v, %v)", zHeal, zAge, zTemper, zLove, zOK)
	}

	// 3. Verify 26.1.2 ZombieHorse: 25.0 MaxHealth, jump [0.5, 0.7], speed [0.21347, 0.28463],
	//    red_mushroom feeding, horse armor sun protection, and natural zombie spear jockey spawn.
	zhID, zRiderID, err := srv.SpawnNaturalZombieHorseJockey(world.DimensionOverworld, 2, 64, 0, rng)
	if err != nil || zhID <= 0 || zRiderID <= 0 {
		t.Fatalf("SpawnNaturalZombieHorseJockey failed: horse=%d rider=%d err=%v", zhID, zRiderID, err)
	}
	if gotHP := srv.MobHealth(zhID); gotHP != ZombieHorseMaxHealth2612 {
		t.Fatalf("zombie_horse health = %v, want %v", gotHP, ZombieHorseMaxHealth2612)
	}
	est, ok := srv.GetEquineState(zhID)
	if !ok || est.JumpStrength < 0.5 || est.JumpStrength > 0.7 {
		t.Fatalf("zombie_horse JumpStrength = %v, want in [0.5, 0.7]", est.JumpStrength)
	}
	if est.MovementSpeed < 9.0/42.16 || est.MovementSpeed > 12.0/42.16 {
		t.Fatalf("zombie_horse MovementSpeed = %v, want in [9/42.16, 12/42.16]", est.MovementSpeed)
	}
	if est.RiderEntityID != zRiderID {
		t.Fatalf("expected zombie rider %d on zombie_horse, got %d", zRiderID, est.RiderEntityID)
	}

	// Tame zombie_horse, injure it to 20.0 HP, feed red_mushroom (+3.0 HP), and equip copper_horse_armor for sun protection.
	srv.TameMob(zhID, player)
	srv.SetMobHealth(zhID, 20.0)
	player.SetInventorySlot(36, world.NewNamedItemStack("minecraft:red_mushroom", 2))
	if handled, _ := srv.InteractWithTameableMob(player, zhID, rng); !handled || srv.MobHealth(zhID) != 23.0 {
		t.Fatalf("expected feeding red_mushroom to heal zombie_horse from 20 to 23 HP, got %v (handled=%v)", srv.MobHealth(zhID), handled)
	}
	if srv.IsZombieHorseProtectedFromSun(zhID) {
		t.Fatal("expected unarmored zombie_horse not to have sun protection")
	}
	player.SetInventorySlot(36, world.NewNamedItemStack("minecraft:copper_horse_armor", 1))
	if handled, _ := srv.InteractWithTameableMob(player, zhID, rng); !handled || !srv.IsZombieHorseProtectedFromSun(zhID) {
		t.Fatal("expected equipping copper_horse_armor on zombie_horse to grant sun protection")
	}

	// Verify ZombieHorse death loot (2..3 rotten_flesh).
	zhDrops := RollEquineDeathLoot2612(MobTypeZombieHorse, rng, 0)
	if len(zhDrops) != 1 || zhDrops[0].ResolvedID() != "minecraft:rotten_flesh" || zhDrops[0].Count < 2 || zhDrops[0].Count > 3 {
		t.Fatalf("RollEquineDeathLoot2612(zombie_horse) = %v, want 2..3 rotten_flesh", zhDrops)
	}
}

func TestCamelAndCamelHusk2612Behavior(t *testing.T) {
	srv := newSurvivalTestServer(t)
	player1 := newBeeTestPlayer(t, srv, "CamelDriver", 0, 64, 0)
	player2 := newBeeTestPlayer(t, srv, "CamelPassenger", 1, 64, 0)
	rng := rand.New(rand.NewSource(99))

	// 1. Spawn CamelHusk (entity ID 20, 32.0 HP, never baby, cannot breed).
	chID, err := srv.SpawnCamelHusk(world.DimensionOverworld, 1, 64, 0)
	if err != nil || chID <= 0 {
		t.Fatalf("SpawnCamelHusk failed: %v", err)
	}
	if gotHP := srv.MobHealth(chID); gotHP != DefaultCamelMaxHealth {
		t.Fatalf("CamelHusk health = %v, want %v", gotHP, DefaultCamelMaxHealth)
	}
	if CanCamelHuskBreed() {
		t.Fatal("expected CanCamelHuskBreed() == false")
	}

	// 2. Mob-controlled CamelHusk cannot be leashed until unridden by mobs.
	huskID, _ := srv.SpawnMob(1, 64, 0, "minecraft:husk")
	if seat, ok := srv.MountMobOnCamel(chID, huskID); !ok || seat != 0 {
		t.Fatalf("MountMobOnCamel failed: seat=%d ok=%v", seat, ok)
	}
	if !srv.IsCamelHuskMobControlled(chID) || srv.CanCamelHuskBeLeashed(chID) {
		t.Fatal("expected mob-controlled CamelHusk to disallow leashing")
	}

	// Clear mob passenger, equip saddle, heal with rabbit_foot (#minecraft:camel_husk_food), mount 2 players, and dash!
	mgr := srv.camelManager()
	mgr.mu.Lock()
	mgr.camels[chID].Passengers = nil
	mgr.mu.Unlock()
	if !srv.CanCamelHuskBeLeashed(chID) {
		t.Fatal("expected unridden CamelHusk to allow leashing")
	}

	srv.SetMobHealth(chID, 28.0)
	player1.SetInventorySlot(36, world.NewNamedItemStack("minecraft:rabbit_foot", 2))
	if handled, babyID := srv.InteractWithCamel(player1, chID); !handled || babyID != 0 || srv.MobHealth(chID) != 30.0 {
		t.Fatalf("expected rabbit_foot to heal CamelHusk to 30.0 HP without breeding, got hp=%v handled=%v baby=%d", srv.MobHealth(chID), handled, babyID)
	}

	player1.SetInventorySlot(36, world.NewNamedItemStack("minecraft:saddle", 1))
	if handled, _ := srv.InteractWithCamel(player1, chID); !handled || !srv.IsCamelSaddled(chID) {
		t.Fatal("expected equipping saddle on CamelHusk to succeed")
	}
	if seat0, ok := srv.MountCamel(player1, chID); !ok || seat0 != 0 {
		t.Fatalf("expected player1 to mount seat 0 on CamelHusk, got seat=%d ok=%v", seat0, ok)
	}
	if seat1, ok := srv.MountCamel(player2, chID); !ok || seat1 != 1 {
		t.Fatalf("expected player2 to mount seat 1 on CamelHusk, got seat=%d ok=%v", seat1, ok)
	}
	if _, _, _, dashed := srv.ExecuteCamelDash(chID, 100); !dashed {
		t.Fatal("expected saddled CamelHusk dash to succeed")
	}
	cst, _ := srv.GetCamelState(chID)
	if cst.DashCooldownTicks != CamelDashCooldownTicks {
		t.Fatalf("CamelHusk DashCooldownTicks = %d, want %d", cst.DashCooldownTicks, CamelDashCooldownTicks)
	}

	// Sit down (40t transition) and stand up (52t transition), plus 1.5-block step height check.
	if !srv.SitDownCamel(chID) || !srv.IsCamelSitting(chID) {
		t.Fatal("expected SitDownCamel on CamelHusk to succeed")
	}
	if !srv.StandUpCamel(chID) || srv.IsCamelSitting(chID) {
		t.Fatal("expected StandUpCamel on CamelHusk to succeed")
	}
	if !CanCamelStepOverObstacle(1.5) || CanCamelStepOverObstacle(1.6) {
		t.Fatal("unexpected CanCamelStepOverObstacle result for 1.5-block step height")
	}

	// Verify CamelHusk death loot (2..3 rotten_flesh).
	drops := RollCamelHuskDeathLoot(rng, 0)
	if len(drops) != 1 || drops[0].ResolvedID() != "minecraft:rotten_flesh" || drops[0].Count < 2 || drops[0].Count > 3 {
		t.Fatalf("RollCamelHuskDeathLoot = %v, want 2..3 rotten_flesh", drops)
	}
}

func TestLlamaCaravanSpeedWolfRepulsionAndTraderLlamaDespawn(t *testing.T) {
	srv := newSurvivalTestServer(t)
	player := newBeeTestPlayer(t, srv, "CaravanLeader", 0, 64, 0)
	rng := rand.New(rand.NewSource(77))

	// 1. Verify #minecraft:llama_food & #minecraft:llama_tempt_items and caravan speed modifier decay (2.1 -> 1.0).
	if !IsLlamaFoodItem("minecraft:wheat") || !IsLlamaFoodItem("minecraft:hay_block") || !IsLlamaTemptItem("minecraft:hay_block") {
		t.Fatal("unexpected llama food / tempt item check")
	}
	mod := LlamaCaravanInitialSpeedModifier
	for i := 0; i < 10; i++ {
		mod = ComputeLlamaCaravanSpeedModifier(mod, 1.5)
	}
	if mod != LlamaCaravanMinSpeedModifier {
		t.Fatalf("ComputeLlamaCaravanSpeedModifier after close ticks = %v, want %v", mod, LlamaCaravanMinSpeedModifier)
	}

	// 2. Verify IsLlamaCaravanHeadLeashed across caravan hops and wolf fleeing when hit by llama_spit.
	llamaHead, _ := srv.SpawnMob(0, 64, 0, MobTypeLlama)
	llamaTail, _ := srv.SpawnMob(2, 64, 0, MobTypeLlama)
	player.SetInventorySlot(36, world.NewNamedItemStack("minecraft:lead", 1))
	if !srv.LeashMobToPlayer(player, llamaHead) {
		t.Fatal("expected LeashMobToPlayer(llamaHead) to succeed")
	}
	if !srv.LinkLlamaCaravan(llamaTail, llamaHead) {
		t.Fatal("expected LinkLlamaCaravan(llamaTail, llamaHead) to succeed")
	}
	if !srv.IsLlamaCaravanHeadLeashed(llamaTail) {
		t.Fatal("expected IsLlamaCaravanHeadLeashed(llamaTail) to be true when head is leashed")
	}

	wolfID, _ := srv.SpawnMob(3, 64, 0, MobTypeWolf)
	if !srv.OnWolfHitByLlamaSpit(wolfID, llamaHead) {
		t.Fatal("expected wild wolf hit by llama_spit to flee")
	}
	if wEnt := srv.entities.Get(wolfID); wEnt == nil || wEnt.X <= 3.0 {
		t.Fatalf("expected fleeing wolf to move away from llama at X=0, got X=%v", wEnt.X)
	}

	// 3. Verify TraderLlama inherits WanderingTrader despawn delay (48000 ticks).
	traderID, llamaIDs, err := srv.SpawnWanderingTrader(world.DimensionOverworld, 10, 64, 10, true, rng)
	if err != nil || traderID <= 0 || len(llamaIDs) != 2 {
		t.Fatalf("SpawnWanderingTrader failed: trader=%d llamas=%v err=%v", traderID, llamaIDs, err)
	}
	if delay := srv.SyncTraderLlamaDespawnDelay(llamaIDs[0]); delay != WanderingTraderDefaultDespawnTicks {
		t.Fatalf("SyncTraderLlamaDespawnDelay = %d, want %d", delay, WanderingTraderDefaultDespawnTicks)
	}
}

func TestFishVariantsSchoolingAndAquaticLootTables2612(t *testing.T) {
	rng := rand.New(rand.NewSource(12345))

	// 1. Verify MaxSchoolSizeForFish (Salmon = 5, Cod/TropicalFish = 8, Pufferfish = 1).
	if MaxSchoolSizeForFish(MobTypeSalmon) != 5 || MaxSchoolSizeForFish(MobTypeCod) != 8 || MaxSchoolSizeForFish(MobTypePufferfish) != 1 {
		t.Fatalf("unexpected MaxSchoolSizeForFish: salmon=%d cod=%d puffer=%d",
			MaxSchoolSizeForFish(MobTypeSalmon), MaxSchoolSizeForFish(MobTypeCod), MaxSchoolSizeForFish(MobTypePufferfish))
	}

	// 2. Verify Salmon variants (small=0.5, medium=1.0, large=1.5) and SynchedEntityData packet round-trip.
	seenSmall, seenMed, seenLarge := false, false, false
	for i := 0; i < 200; i++ {
		v := RollSalmonVariantWeighted2612(rng)
		switch v {
		case SalmonVariantSmallID:
			seenSmall = true
			if SalmonBoundingBoxScale(v) != 0.5 || SalmonVariantName(v) != "small" {
				t.Fatal("unexpected small salmon scale/name")
			}
		case SalmonVariantMediumID:
			seenMed = true
			if SalmonBoundingBoxScale(v) != 1.0 || SalmonVariantName(v) != "medium" {
				t.Fatal("unexpected medium salmon scale/name")
			}
		case SalmonVariantLargeID:
			seenLarge = true
			if SalmonBoundingBoxScale(v) != 1.5 || SalmonVariantName(v) != "large" {
				t.Fatal("unexpected large salmon scale/name")
			}
		}
	}
	if !seenSmall || !seenMed || !seenLarge {
		t.Fatalf("expected all 3 salmon variants to be rolled, got small=%v med=%v large=%v", seenSmall, seenMed, seenLarge)
	}
	spkt := EncodeSalmonDataPacket(44, true, SalmonVariantLargeID)
	sID, sBucket, sVar, err := DecodeSalmonDataPacket(spkt)
	if err != nil || sID != 44 || !sBucket || sVar != SalmonVariantLargeID {
		t.Fatalf("DecodeSalmonDataPacket = (%d, %v, %d, %v)", sID, sBucket, sVar, err)
	}

	// 3. Verify 22 Common Tropical Fish Variants and SynchedEntityData packet round-trip.
	if len(TropicalFishCommonVariants2612) != 22 {
		t.Fatalf("len(TropicalFishCommonVariants2612) = %d, want 22", len(TropicalFishCommonVariants2612))
	}
	tfVar := RollTropicalFishVariant2612(rng)
	tfpkt := EncodeTropicalFishDataPacket(45, false, tfVar)
	tfID, tfBucket, tfDecVar, err := DecodeTropicalFishDataPacket(tfpkt)
	if err != nil || tfID != 45 || tfBucket || tfDecVar != tfVar {
		t.Fatalf("DecodeTropicalFishDataPacket = (%d, %v, %d, %v)", tfID, tfBucket, tfDecVar, err)
	}

	// 4. Verify 26.1.2 death loot tables for Fish (bone_meal 5%), Squid/GlowSquid (1..3 ink_sac), Dolphin (cooked_cod on fire), and Turtle (bowl on lightning).
	fishLoot := RollFishDeathLoot2612(MobTypeSalmon, true, rng)
	if len(fishLoot) == 0 || fishLoot[0].ResolvedID() != "minecraft:cooked_salmon" {
		t.Fatalf("expected on-fire salmon to drop cooked_salmon, got %v", fishLoot)
	}
	glowLoot := RollSquidDeathLoot2612(true, rng, 0)
	if len(glowLoot) != 1 || glowLoot[0].ResolvedID() != "minecraft:glow_ink_sac" || glowLoot[0].Count < 1 || glowLoot[0].Count > 3 {
		t.Fatalf("RollSquidDeathLoot2612(glow) = %v, want 1..3 glow_ink_sac", glowLoot)
	}
	dolphinLoot := RollDolphinDeathLoot2612(true, rng, 2)
	if len(dolphinLoot) > 0 && dolphinLoot[0].ResolvedID() != "minecraft:cooked_cod" {
		t.Fatalf("expected on-fire dolphin loot to be cooked_cod, got %v", dolphinLoot)
	}
	turtleLightningLoot := RollTurtleDeathLoot2612(true, rng, 0)
	foundBowl := false
	for _, st := range turtleLightningLoot {
		if st.ResolvedID() == "minecraft:bowl" && st.Count == 1 {
			foundBowl = true
		}
	}
	if !foundBowl {
		t.Fatalf("expected turtle killed by lightning to drop 1 minecraft:bowl, got %v", turtleLightningLoot)
	}
}

func TestStrider2612BoostCurveAndSpeedModifiers(t *testing.T) {
	rng := rand.New(rand.NewSource(314))

	if !IsStriderFoodItem("minecraft:warped_fungus") || !IsStriderTemptItem("minecraft:warped_fungus_on_a_stick") {
		t.Fatal("expected warped_fungus and warped_fungus_on_a_stick to match strider food/tempt tags")
	}

	for i := 0; i < 100; i++ {
		dur := RollStriderBoostDuration2612(rng)
		if dur < 140 || dur > 980 {
			t.Fatalf("RollStriderBoostDuration2612 = %d, want in [140, 980]", dur)
		}
	}

	// At mid-boost (boostTime = 100, total = 200), sin(pi/2) = 1.0 -> boostFactor = 2.15.
	midBoost := ComputeStriderBoostFactor2612(100, 200)
	if math.Abs(midBoost-2.15) > 1e-6 {
		t.Fatalf("ComputeStriderBoostFactor2612(100, 200) = %v, want 2.15", midBoost)
	}

	// Verify attribute speed on lava (0.175) vs suffocating on land (0.175 * 0.66 = 0.1155),
	// and ridden speed on lava vs land.
	lavaAttr := ComputeStriderAttributeSpeed2612(false)
	landAttr := ComputeStriderAttributeSpeed2612(true)
	if math.Abs(lavaAttr-0.175) > 1e-6 || math.Abs(landAttr-0.1155) > 1e-6 {
		t.Fatalf("strider attribute speeds: lava=%v (want 0.175), land=%v (want 0.1155)", lavaAttr, landAttr)
	}
	riddenLava := ComputeStriderRiddenSpeed2612(false, 0, 0)
	riddenLand := ComputeStriderRiddenSpeed2612(true, 0, 0)
	if riddenLava <= riddenLand {
		t.Fatalf("expected ridden strider speed on lava (%v) > suffocating on land (%v)", riddenLava, riddenLand)
	}
}
