package server

import (
	"math/rand"
	"testing"

	"regionio/internal/world"
)

func TestVillagerScheduleBedAndBellResonance(t *testing.T) {
	s, _ := newTestPlayServer(t)
	dim := world.DimensionOverworld

	// 1. Daily schedule checks for employed villager, nitwit, and baby villager.
	if got := VillagerScheduleActivity(VillagerProfessionLibrarian, false, 3000); got != VillagerActivityWork {
		t.Fatalf("expected librarian at 3000 to be in work activity, got %q", got)
	}
	if got := VillagerScheduleActivity(VillagerProfessionLibrarian, false, 9500); got != VillagerActivityMeet {
		t.Fatalf("expected librarian at 9500 to be in meet activity, got %q", got)
	}
	if got := VillagerScheduleActivity(VillagerProfessionLibrarian, false, 13000); got != VillagerActivityRest {
		t.Fatalf("expected librarian at 13000 to be in rest activity, got %q", got)
	}
	// Nitwit stays awake until 14000 and sleeps until 2000, never works.
	if got := VillagerScheduleActivity(VillagerProfessionNitwit, false, 3000); got != VillagerActivityIdle {
		t.Fatalf("expected nitwit at 3000 to be idle, got %q", got)
	}
	if got := VillagerScheduleActivity(VillagerProfessionNitwit, false, 13000); got != VillagerActivityIdle {
		t.Fatalf("expected nitwit at 13000 to still be idle, got %q", got)
	}
	if got := VillagerScheduleActivity(VillagerProfessionNitwit, false, 15000); got != VillagerActivityRest {
		t.Fatalf("expected nitwit at 15000 to be resting, got %q", got)
	}
	if got := VillagerScheduleActivity(VillagerProfessionNitwit, false, 1000); got != VillagerActivityRest {
		t.Fatalf("expected nitwit at 1000 to still be resting, got %q", got)
	}

	// 2. Bed claiming & sleeping at night, plus dawn wake-up heal (+2 HP).
	bedState, ok := world.BlockStateByName("minecraft:red_bed", map[string]string{
		"facing":   "north",
		"occupied": "false",
		"part":     "foot",
	})
	if !ok {
		t.Fatalf("missing minecraft:red_bed block state")
	}
	s.chunks.SetBlock(10, 65, 10, bedState)

	v1, err := s.SpawnVillagerInDimension(dim, 10.5, 65.0, 10.5, VillagerTypePlains, VillagerProfessionFarmer, 1)
	if err != nil {
		t.Fatalf("SpawnVillagerInDimension(v1): %v", err)
	}
	v2, err := s.SpawnVillagerInDimension(dim, 11.5, 65.0, 10.5, VillagerTypePlains, VillagerProfessionLibrarian, 1)
	if err != nil {
		t.Fatalf("SpawnVillagerInDimension(v2): %v", err)
	}
	s.SetMobHealth(v1, 14.0)

	// Nighttime tick -> v1 claims bed and sleeps.
	s.SetDayTime(13000)
	s.TickVillagers()
	if !s.IsVillagerSleeping(v1) {
		t.Fatalf("expected villager %d to claim bed and sleep at night", v1)
	}

	// Bell rung during night wakes sleeping villager, sets panic, and makes nearby raiders glow.
	pillagerID, err := s.SpawnMobInDimension(dim, 20.5, 65.0, 20.5, MobTypePillager)
	if err != nil {
		t.Fatalf("SpawnMobInDimension(pillager): %v", err)
	}
	alerted, glowing := s.OnVillageBellRung(dim, 10, 65, 10)
	if alerted < 2 || glowing < 1 {
		t.Fatalf("OnVillageBellRung alerted=%d glowing=%d, want >=2 and >=1", alerted, glowing)
	}
	if s.IsVillagerSleeping(v1) {
		t.Fatalf("expected villager %d to wake up when village bell is rung", v1)
	}
	if got := s.MobHealth(v1); got != 16.0 {
		t.Fatalf("expected waking villager %d to heal +2.0 HP to 16.0, got %.1f", v1, got)
	}
	if act := s.VillagerCurrentActivity(v1); act != VillagerActivityPanic {
		t.Fatalf("expected villager %d to be in panic activity after bell ring, got %q", v1, act)
	}
	hasGlow := false
	for _, eff := range s.EntityEffects(pillagerID) {
		if eff.ID == EffectGlowing {
			hasGlow = true
			break
		}
	}
	if !hasGlow {
		t.Fatalf("expected raider %d within 48 blocks of rung bell to receive Glowing effect", pillagerID)
	}

	// 3. Meeting hours (9500) -> villagers share gossips (minor_positive transfers with decay 5; major_positive stays personal).
	var playerUUID [16]byte
	playerUUID[0] = 0x42
	s.AddVillagerGossip(v1, playerUUID, GossipMinorPositive, 25)
	s.SetDayTime(9500)
	s.TickVillagers()
	if got := s.VillagerGossipValue(v2, playerUUID, GossipMinorPositive); got != 20 {
		t.Fatalf("expected villager %d to receive shared minor_positive gossip (20) during meeting hours, got %d", v2, got)
	}
}

func TestVillagerBreedingGiftsAndLightningWitch(t *testing.T) {
	s, player := newTestPlayServer(t)
	s.SetDifficulty(DifficultyNormal)
	dim := world.DimensionOverworld
	rng := rand.New(rand.NewSource(7))

	vA, err := s.SpawnVillagerInDimension(dim, 5.5, 65.0, 5.5, VillagerTypeDesert, VillagerProfessionFarmer, 2)
	if err != nil {
		t.Fatalf("SpawnVillagerInDimension(vA): %v", err)
	}
	vB, err := s.SpawnVillagerInDimension(dim, 6.5, 65.0, 5.5, VillagerTypeSnow, VillagerProfessionLibrarian, 2)
	if err != nil {
		t.Fatalf("SpawnVillagerInDimension(vB): %v", err)
	}

	// Feed vA 6 loaves of bread (6 * 4 = 24 food points) and share half with vB (12 / 12).
	pts, ok := s.AddVillagerFood(vA, "minecraft:bread", 6)
	if !ok || pts != 24 {
		t.Fatalf("AddVillagerFood(bread x6) = (%d, %v), want (24, true)", pts, ok)
	}
	if !s.ShareVillagerFood(vA, vB) {
		t.Fatalf("expected ShareVillagerFood from vA(24) to vB(0) to succeed")
	}
	if s.VillagerFoodPoints(vA) != 12 || s.VillagerFoodPoints(vB) != 12 {
		t.Fatalf("expected 12/12 food points after sharing, got %d/%d", s.VillagerFoodPoints(vA), s.VillagerFoodPoints(vB))
	}

	// Breed vA and vB -> spawns baby villager with 0 food remaining on parents.
	childID, bred := s.TryBreedVillagers(vA, vB, rng)
	if !bred || childID <= 0 {
		t.Fatalf("expected TryBreedVillagers to spawn baby villager: bred=%v childID=%d", bred, childID)
	}
	if !s.IsMobBaby(childID) {
		t.Fatalf("expected child villager %d to be a baby", childID)
	}
	if s.VillagerFoodPoints(vA) != 0 || s.VillagerFoodPoints(vB) != 0 {
		t.Fatalf("expected parents to consume 12 food points each on breeding, got %d/%d", s.VillagerFoodPoints(vA), s.VillagerFoodPoints(vB))
	}

	// Hero of the Village gift throw from employed adult villager.
	s.SetPlayerTransform(player, 6.0, 65.0, 5.5, 0, 0, true)
	s.ApplyPlayerEffect(player, NewStatusEffectInstance(EffectHeroOfTheVillage, 600, 0))
	gift, threw := s.TryThrowHeroOfTheVillageGift(vA, player, rng)
	if !threw || gift.IsEmpty() {
		t.Fatalf("expected farmer villager to throw Hero of the Village gift, got (%+v, %v)", gift, threw)
	}

	// Lightning strike on villager -> converts to witch on Normal difficulty (and not on Peaceful).
	s.SetDifficulty(DifficultyPeaceful)
	if _, conv := s.OnVillagerStruckByLightning(vB); conv {
		t.Fatalf("expected villager NOT to convert into witch on Peaceful difficulty")
	}
	s.SetDifficulty(DifficultyNormal)
	witchID, conv := s.OnVillagerStruckByLightning(vB)
	if !conv || witchID <= 0 {
		t.Fatalf("expected villager %d struck by lightning on Normal to convert into witch", vB)
	}
	if wEnt := s.entities.Get(witchID); wEnt == nil || wEnt.TypeName != MobTypeWitch {
		t.Fatalf("expected entity %d to be %s, got %+v", witchID, MobTypeWitch, wEnt)
	}
}

func TestZombieVillagerDifficultyInfectionAndCuringEdgeCases(t *testing.T) {
	s, player := newTestPlayServer(t)
	dim := world.DimensionOverworld
	rng := rand.New(rand.NewSource(99))

	// 1. On Easy difficulty, zombie kill NEVER zombifies villager (0% chance).
	s.SetDifficulty(DifficultyEasy)
	vEasy, _ := s.SpawnVillagerInDimension(dim, 10.5, 65.0, 10.5, VillagerTypeTaiga, VillagerProfessionArmorer, 3)
	if _, conv := s.TryZombifyVillagerOnZombieKill(vEasy, MobTypeZombie, rng); conv {
		t.Fatalf("expected 0%% zombie infection on Easy difficulty")
	}

	// 2. On Hard difficulty, zombie kill ALWAYS zombifies villager (100% chance) and preserves baby state + XP/level.
	s.SetDifficulty(DifficultyHard)
	vHard, _ := s.SpawnVillagerInDimension(dim, 12.5, 65.0, 12.5, VillagerTypeSwamp, VillagerProfessionCleric, 2)
	s.SetMobBaby(vHard, true)
	s.AddVillagerXP(vHard, 15)

	zvID, conv := s.TryZombifyVillagerOnZombieKill(vHard, MobTypeHusk, rng)
	if !conv || zvID <= 0 {
		t.Fatalf("expected 100%% zombie infection on Hard difficulty, got (%d, %v)", zvID, conv)
	}
	if !s.IsMobBaby(zvID) {
		t.Fatalf("expected baby villager to convert into baby zombie_villager")
	}

	// 3. Place iron_bars and bed blocks near the converting zombie villager to verify catalyst counting & cure preservation.
	barsSt, ok := world.BlockStateByName("minecraft:iron_bars", map[string]string{
		"east":        "false",
		"north":       "false",
		"south":       "false",
		"waterlogged": "false",
		"west":        "false",
	})
	if ok {
		s.chunks.SetBlock(12, 65, 13, barsSt)
		s.chunks.SetBlock(13, 65, 13, barsSt)
	}
	if cats := s.CountZombieCureCatalystsAt(dim, 12.5, 65.0, 12.5); cats < 2 {
		t.Fatalf("expected at least 2 zombie cure catalysts near (12.5, 65, 12.5), got %d", cats)
	}

	s.SetPlayerTransform(player, 12.5, 65.0, 12.5, 0, 0, true)
	player.SetInventorySlot(36, world.NewNamedItemStack("minecraft:golden_apple", 1))
	s.SetZombieVillagerWeakened(zvID, true)
	if !s.InteractWithZombieVillager(player, zvID, rng) {
		t.Fatalf("expected InteractWithZombieVillager with golden_apple + Weakness to start curing")
	}
	curedID, cured := s.CompleteZombieVillagerCure(zvID)
	if !cured || curedID <= 0 {
		t.Fatalf("expected CompleteZombieVillagerCure to succeed")
	}
	if !s.IsMobBaby(curedID) {
		t.Fatalf("expected cured baby zombie_villager to remain a baby villager")
	}
	rep := s.VillagerPlayerReputation(curedID, player.Profile.UUID)
	if rep < 125 { // 20*5 (major_positive) + 25*1 (minor_positive) = 125
		t.Fatalf("expected cured villager reputation for curer >= 125, got %d", rep)
	}
}

func TestIronGolemCrackStagesReputationAndPlayerCreatedRules(t *testing.T) {
	s, player := newTestPlayServer(t)
	s.SetDifficulty(DifficultyNormal)
	dim := world.DimensionOverworld

	// 1. Crack stages & damage immunities.
	if st := IronGolemCrackLevelForHealth(100, DefaultIronGolemMaxHealth); st != IronGolemCrackNone {
		t.Fatalf("expected 100 HP crack stage None, got %v", st)
	}
	if st := IronGolemCrackLevelForHealth(70, DefaultIronGolemMaxHealth); st != IronGolemCrackLow {
		t.Fatalf("expected 70 HP crack stage Low, got %v", st)
	}
	if st := IronGolemCrackLevelForHealth(40, DefaultIronGolemMaxHealth); st != IronGolemCrackMedium {
		t.Fatalf("expected 40 HP crack stage Medium, got %v", st)
	}
	if st := IronGolemCrackLevelForHealth(20, DefaultIronGolemMaxHealth); st != IronGolemCrackHigh {
		t.Fatalf("expected 20 HP crack stage High, got %v", st)
	}
	if !IsIronGolemImmuneToDamageCause("fall") || !IsIronGolemImmuneToDamageCause("drowning") || !IsIronGolemImmuneToDamageCause("freeze") {
		t.Fatalf("expected Iron Golem to be immune to fall, drowning, and freeze damage")
	}

	// 2. Village-spawned vs Player-created Iron Golem hostility toward low-reputation / aggressive players.
	villageGolemID, err := s.SpawnIronGolem(dim, 10.5, 65.0, 10.5, false)
	if err != nil {
		t.Fatalf("SpawnIronGolem(village): %v", err)
	}
	playerGolemID, err := s.SpawnIronGolem(dim, 12.5, 65.0, 10.5, true)
	if err != nil {
		t.Fatalf("SpawnIronGolem(playerCreated): %v", err)
	}
	villagerID, err := s.SpawnVillagerInDimension(dim, 11.0, 65.0, 10.5, VillagerTypePlains, VillagerProfessionFarmer, 1)
	if err != nil {
		t.Fatalf("SpawnVillagerInDimension: %v", err)
	}

	// Crossing crack threshold downward triggers crackIncreased = true.
	if !s.OnIronGolemDamaged(villageGolemID, 80.0, 65.0, nil) {
		t.Fatalf("expected OnIronGolemDamaged(80 -> 65) to report crack stage increase")
	}

	// Give player reputation <= -15 (-20 via minor_negative 20) on nearby villager.
	s.SetPlayerTransform(player, 11.5, 65.0, 11.5, 0, 0, true)
	s.AddVillagerGossip(villagerID, player.Profile.UUID, GossipMinorNegative, 20)
	if !s.ShouldIronGolemAttackPlayer(villageGolemID, player) {
		t.Fatalf("expected village-spawned Iron Golem to attack player with reputation <= -15")
	}
	if s.ShouldIronGolemAttackPlayer(playerGolemID, player) {
		t.Fatalf("expected player-created Iron Golem to NEVER attack player even with reputation <= -15")
	}

	// Poppy offering lifecycle.
	if !s.StartIronGolemOfferPoppy(villageGolemID, villagerID) || !s.IsIronGolemOfferingPoppy(villageGolemID) {
		t.Fatalf("expected StartIronGolemOfferPoppy to set offering state")
	}
	s.StopIronGolemOfferPoppy(villageGolemID)
	if s.IsIronGolemOfferingPoppy(villageGolemID) {
		t.Fatalf("expected StopIronGolemOfferPoppy to clear offering state")
	}
}

func TestWanderingTrader2612TradesAndNightInvisibility(t *testing.T) {
	s, player := newTestPlayServer(t)
	s.SetPlayerTransform(player, 0.5, 65.0, 0.5, 0, 0, true)
	rng := rand.New(rand.NewSource(123))

	// 1. Buying pool (2 offers buying supplies for emeralds) + full 26.1.2 offer set.
	buyOffers := RollWanderingTraderBuyingOffers(rng)
	if len(buyOffers) != WanderingTraderBuyingOfferCount {
		t.Fatalf("RollWanderingTraderBuyingOffers returned %d offers, want %d", len(buyOffers), WanderingTraderBuyingOfferCount)
	}
	for i, off := range buyOffers {
		if off.Result.ResolvedID() != "minecraft:emerald" {
			t.Fatalf("buy offer %d result = %q, want minecraft:emerald", i, off.Result.ResolvedID())
		}
	}
	fullOffers := RollWanderingTraderFullOffers2612(rng)
	if len(fullOffers) != WanderingTraderBuyingOfferCount+WanderingTraderOrdinaryOfferCount+WanderingTraderSpecialOfferCount {
		t.Fatalf("RollWanderingTraderFullOffers2612 returned %d offers, want %d", len(fullOffers),
			WanderingTraderBuyingOfferCount+WanderingTraderOrdinaryOfferCount+WanderingTraderSpecialOfferCount)
	}

	// 2. Natural spawn creates 1 trader + 2 trader llamas, and blocks a second natural spawn while active.
	traderID, spawned := s.TrySpawnNaturalWanderingTrader(rng)
	if !spawned || traderID <= 0 {
		t.Fatalf("expected TrySpawnNaturalWanderingTrader to succeed: id=%d spawned=%v", traderID, spawned)
	}
	if _, second := s.TrySpawnNaturalWanderingTrader(rng); second {
		t.Fatalf("expected second TrySpawnNaturalWanderingTrader to be skipped while trader is active")
	}

	// 3. Nighttime invisibility potion & morning milk.
	s.SetDayTime(13500)
	s.TickWanderingTraders(rng)
	invisAtNight := false
	for _, eff := range s.EntityEffects(traderID) {
		if eff.ID == EffectInvisibility {
			invisAtNight = true
			break
		}
	}
	if !invisAtNight {
		t.Fatalf("expected wandering trader %d to drink invisibility potion at night", traderID)
	}

	s.SetDayTime(2000)
	s.TickWanderingTraders(rng)
	for _, eff := range s.EntityEffects(traderID) {
		if eff.ID == EffectInvisibility {
			t.Fatalf("expected wandering trader %d to drink milk and clear invisibility at dawn", traderID)
		}
	}
}
