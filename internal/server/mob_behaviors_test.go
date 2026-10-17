package server

import (
	"math/rand"
	"testing"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

func newTestMobBehaviorServer(t *testing.T) *Server {
	t.Helper()
	cfg := DefaultConfig()
	cfg.WorldDir = ""
	cache := world.NewCache(256, func(cx, cz int32) *world.Chunk {
		ch := &world.Chunk{X: cx, Z: cz}
		for x := 0; x < 16; x++ {
			for z := 0; z < 16; z++ {
				ch.SetBlock(x, 63, z, world.StateStone)
				ch.SetBlock(x, 64, z, world.StateGrass)
			}
		}
		return ch
	})
	srv, err := NewWithCache(cfg, cache)
	if err != nil {
		t.Fatalf("NewWithCache: %v", err)
	}
	return srv
}

func newTestMobBehaviorPlayer(t *testing.T, s *Server, name string, uuidByte byte, x, y, z float64) *PlayerSession {
	t.Helper()
	sess, err := s.RegisterPlayer(Profile{
		UUID: [16]byte{uuidByte, 2, 3, 4},
		Name: name,
	}, func(int32, []byte) error { return nil })
	if err != nil {
		t.Fatalf("RegisterPlayer(%s): %v", name, err)
	}
	s.SetPlayerGameMode(sess, GameModeSurvival)
	s.SetPlayerPosition(sess, x, y, z)
	return sess
}

func TestBreedingFoodsAndBabyGrowth(t *testing.T) {
	if !IsBreedingFoodForMob(MobTypeCow, "minecraft:wheat") || !IsBreedingFoodForMob(MobTypeSheep, "minecraft:wheat") {
		t.Fatalf("expected wheat to be breeding food for cow and sheep")
	}
	if !IsBreedingFoodForMob(MobTypePig, "minecraft:carrot") || !IsBreedingFoodForMob(MobTypePig, "minecraft:potato") || !IsBreedingFoodForMob(MobTypePig, "minecraft:beetroot") {
		t.Fatalf("expected carrot, potato, and beetroot to be breeding foods for pig")
	}
	if !IsBreedingFoodForMob(MobTypeChicken, "minecraft:wheat_seeds") || !IsBreedingFoodForMob(MobTypeChicken, "minecraft:pitcher_pod") {
		t.Fatalf("expected seeds and pitcher_pod to be breeding foods for chicken")
	}
	if !IsBreedingFoodForMob(MobTypeWolf, "minecraft:cooked_beef") || !IsBreedingFoodForMob(MobTypeWolf, "minecraft:rotten_flesh") {
		t.Fatalf("expected meat to be breeding food for wolf")
	}
	if !IsBreedingFoodForMob(MobTypeCat, "minecraft:cod") || !IsBreedingFoodForMob(MobTypeCat, "minecraft:salmon") {
		t.Fatalf("expected cod and salmon to be breeding foods for cat")
	}
	if !IsBreedingFoodForMob(MobTypeHorse, "minecraft:golden_carrot") || !IsBreedingFoodForMob(MobTypeDonkey, "minecraft:golden_apple") {
		t.Fatalf("expected golden carrot and golden apple to be breeding foods for horse/donkey")
	}
	if !IsBreedingFoodForMob(MobTypeLlama, "minecraft:hay_block") {
		t.Fatalf("expected hay_block to be breeding food for llama")
	}
	if babyType, ok := CanMobsMateWithEachOther(MobTypeHorse, MobTypeDonkey); !ok || babyType != MobTypeMule {
		t.Fatalf("expected horse + donkey -> mule, got (%q, %v)", babyType, ok)
	}

	// Verify AgeableMob packet roundtrip.
	pkt := EncodeAgeableMobDataPacket(42, true)
	id, isBaby, err := DecodeAgeableMobDataPacket(protocol.NewReader(pkt))
	if err != nil || id != 42 || !isBaby {
		t.Fatalf("DecodeAgeableMobDataPacket failed: id=%d isBaby=%v err=%v", id, isBaby, err)
	}

	s := newTestMobBehaviorServer(t)
	p := newTestMobBehaviorPlayer(t, s, "Breeder", 1, 8.5, 65, 8.5)

	cow1, err := s.SpawnMob(9.0, 65, 8.5, MobTypeCow)
	if err != nil {
		t.Fatalf("SpawnMob cow1: %v", err)
	}
	cow2, err := s.SpawnMob(10.0, 65, 8.5, MobTypeCow)
	if err != nil {
		t.Fatalf("SpawnMob cow2: %v", err)
	}

	slotIdx := world.HotbarContainerOffset + int(p.SelectedSlot())
	p.SetInventorySlot(slotIdx, world.NewNamedItemStack("minecraft:wheat", 10))

	handled1, off1 := s.InteractWithAnimal(p, cow1)
	if !handled1 || off1 != 0 || s.MobInLoveTicks(cow1) != AnimalLoveDurationTicks {
		t.Fatalf("expected cow1 to enter love mode: handled=%v off=%d love=%d", handled1, off1, s.MobInLoveTicks(cow1))
	}

	handled2, babyID := s.InteractWithAnimal(p, cow2)
	if !handled2 || babyID <= 0 {
		t.Fatalf("expected cow2 interaction to spawn baby calf: handled=%v babyID=%d", handled2, babyID)
	}
	if !s.IsMobBaby(babyID) || s.MobAge(babyID) != BabyMobGrowthTicks {
		t.Fatalf("expected calf to be baby with age %d, got isBaby=%v age=%d", BabyMobGrowthTicks, s.IsMobBaby(babyID), s.MobAge(babyID))
	}
	if s.MobBreedCooldown(cow1) != AnimalBreedCooldownTicks || s.MobBreedCooldown(cow2) != AnimalBreedCooldownTicks {
		t.Fatalf("expected parents to have breed cooldown %d, got %d / %d", AnimalBreedCooldownTicks, s.MobBreedCooldown(cow1), s.MobBreedCooldown(cow2))
	}

	// Feeding baby calf wheat accelerates growth by 10% (-24000 -> -21600).
	fed, _ := s.InteractWithAnimal(p, babyID)
	if !fed || s.MobAge(babyID) != -21600 {
		t.Fatalf("expected feeding baby calf to accelerate age to -21600, got fed=%v age=%d", fed, s.MobAge(babyID))
	}

	// Set age to -1 and tick once -> grows into adult.
	s.SetMobAge(babyID, -1)
	s.TickBabyMobs()
	if s.IsMobBaby(babyID) || s.MobAge(babyID) != 0 {
		t.Fatalf("expected calf to reach adult age 0 after tick, got age=%d", s.MobAge(babyID))
	}
}

func TestTamingWolfCatParrotAndEquine(t *testing.T) {
	s := newTestMobBehaviorServer(t)
	owner := newTestMobBehaviorPlayer(t, s, "Tamer", 2, 8.5, 65, 8.5)
	slotIdx := world.HotbarContainerOffset + int(owner.SelectedSlot())
	rng := rand.New(rand.NewSource(1))

	// 1. Wolf taming, health boost (8 -> 40), collar dyeing, sitting toggle, and teleporting >= 12 blocks.
	wolfID, err := s.SpawnMob(9.5, 65, 8.5, MobTypeWolf)
	if err != nil {
		t.Fatalf("SpawnMob wolf: %v", err)
	}
	if hp := s.MobHealth(wolfID); hp != DefaultWolfWildMaxHealth {
		t.Fatalf("expected wild wolf health %.1f, got %.1f", DefaultWolfWildMaxHealth, hp)
	}

	owner.SetInventorySlot(slotIdx, world.NewNamedItemStack("minecraft:bone", 5))
	// Pass nil rng for deterministic immediate taming roll.
	handled, _ := s.InteractWithTameableMob(owner, wolfID, nil)
	if !handled || !s.IsMobTamed(wolfID) || !s.IsMobSitting(wolfID) {
		t.Fatalf("expected wolf to be tamed and sitting, got handled=%v tamed=%v sitting=%v", handled, s.IsMobTamed(wolfID), s.IsMobSitting(wolfID))
	}
	if hp := s.MobHealth(wolfID); hp != DefaultWolfTamedMaxHealth {
		t.Fatalf("expected tamed wolf health %.1f, got %.1f", DefaultWolfTamedMaxHealth, hp)
	}

	// Dye wolf collar blue.
	owner.SetInventorySlot(slotIdx, world.NewNamedItemStack("minecraft:blue_dye", 2))
	if ok, _ := s.InteractWithTameableMob(owner, wolfID, rng); !ok {
		t.Fatalf("expected blue_dye to recolor wolf collar")
	}
	st, _ := s.GetTamedMobState(wolfID)
	blueID, _ := DyeColorIndexByItemName("minecraft:blue_dye")
	if st.CollarColor != blueID {
		t.Fatalf("expected wolf collar color %d, got %d", blueID, st.CollarColor)
	}

	// Empty-hand right-click toggles wolf from sitting to standing/following.
	owner.SetInventorySlot(slotIdx, world.ItemStack{})
	if ok, _ := s.InteractWithTameableMob(owner, wolfID, rng); !ok || s.IsMobSitting(wolfID) {
		t.Fatalf("expected wolf to stand up on owner right-click")
	}

	// Move owner 20 blocks away (>= 12 blocks) -> wolf teleports near owner on TickTamedMobs.
	s.SetPlayerPosition(owner, 30.5, 65, 8.5)
	s.TickTamedMobs(rng)
	wEnt := s.entities.Get(wolfID)
	if wEnt == nil || wEnt.X < 25.0 {
		t.Fatalf("expected standing tamed wolf to teleport near owner at X=30.5, got X=%.2f", wEnt.X)
	}

	// 2. Wolf defends owner when owner attacks a zombie.
	zombieID, _ := s.SpawnMob(32.0, 65, 8.5, MobTypeZombie)
	alerted := s.NotifyOwnerAttackedTarget(owner, zombieID)
	if alerted != 1 {
		t.Fatalf("expected 1 tamed wolf to aggro onto zombie, got %d", alerted)
	}
	hpBefore := s.MobHealth(zombieID)
	for i := 0; i < 5; i++ {
		s.TickTamedMobs(rng)
	}
	if s.MobHealth(zombieID) >= hpBefore {
		t.Fatalf("expected tamed wolf to chase and bite zombie (hpBefore=%.1f, hpNow=%.1f)", hpBefore, s.MobHealth(zombieID))
	}

	// 3. Parrot taming with seeds, shoulder perching, and lethal cookie poison.
	parrotID, _ := s.SpawnMob(31.0, 65, 8.5, MobTypeParrot)
	owner.SetInventorySlot(slotIdx, world.NewNamedItemStack("minecraft:wheat_seeds", 5))
	if ok, _ := s.InteractWithTameableMob(owner, parrotID, nil); !ok || !s.IsMobTamed(parrotID) {
		t.Fatalf("expected parrot to be tamed with wheat_seeds")
	}
	if !s.TryPerchParrotOnShoulder(owner, parrotID) {
		t.Fatalf("expected tamed parrot to perch on owner's shoulder")
	}
	dismounted := s.DismountShoulderParrots(owner)
	if len(dismounted) != 1 {
		t.Fatalf("expected 1 dismounted shoulder parrot, got %d", len(dismounted))
	}
	owner.SetInventorySlot(slotIdx, world.NewNamedItemStack("minecraft:cookie", 1))
	if ok, _ := s.InteractWithTameableMob(owner, dismounted[0], rng); !ok {
		t.Fatalf("expected cookie interaction on parrot to be handled")
	}
	if s.entities.Get(dismounted[0]) != nil {
		t.Fatalf("expected cookie to fatally poison and remove parrot")
	}

	// 4. Horse feeding + taming and Llama caravan linking.
	horseID, _ := s.SpawnMob(31.5, 65, 9.5, MobTypeHorse)
	owner.SetInventorySlot(slotIdx, world.NewNamedItemStack("minecraft:golden_apple", 2))
	if ok, _ := s.InteractWithTameableMob(owner, horseID, rng); !ok {
		t.Fatalf("expected feeding horse golden_apple to succeed")
	}
	owner.SetInventorySlot(slotIdx, world.ItemStack{})
	if ok, _ := s.InteractWithTameableMob(owner, horseID, nil); !ok || !s.IsMobTamed(horseID) {
		t.Fatalf("expected horse taming to succeed")
	}

	llama1, _ := s.SpawnMob(30.0, 65, 10.0, MobTypeLlama)
	llama2, _ := s.SpawnMob(34.0, 65, 10.0, MobTypeLlama)
	if !s.LinkLlamaCaravan(llama2, llama1) {
		t.Fatalf("expected LinkLlamaCaravan to succeed")
	}
	s.TickTamedMobs(rng)
	l2Ent := s.entities.Get(llama2)
	if l2Ent == nil || l2Ent.X >= 34.0 {
		t.Fatalf("expected caravan follower llama2 to step toward head llama1, got X=%.2f", l2Ent.X)
	}
}

func TestIronGolemPatternVillagerSpawnAndCombat(t *testing.T) {
	s := newTestMobBehaviorServer(t)
	player := newTestMobBehaviorPlayer(t, s, "Builder", 3, 2.5, 65, 2.5)
	slotIdx := world.HotbarContainerOffset + int(player.SelectedSlot())
	rng := rand.New(rand.NewSource(42))

	ironBlock, ok := world.BlockStateIDByProperties("minecraft:iron_block", nil)
	if !ok {
		t.Fatalf("missing minecraft:iron_block state")
	}
	pumpkin, ok := world.BlockStateIDByProperties("minecraft:carved_pumpkin", map[string]string{"facing": "north"})
	if !ok {
		t.Fatalf("missing minecraft:carved_pumpkin state")
	}

	// 1. Build Iron Golem T-shape at (4, 65..67, 4) with arms along X.
	s.chunks.SetBlock(4, 65, 4, ironBlock)
	s.chunks.SetBlock(3, 66, 4, ironBlock)
	s.chunks.SetBlock(4, 66, 4, ironBlock)
	s.chunks.SetBlock(5, 66, 4, ironBlock)
	s.chunks.SetBlock(4, 67, 4, pumpkin)

	golemID, summoned := s.TrySummonIronGolemAt(world.DimensionOverworld, 4, 67, 4, player)
	if !summoned || golemID <= 0 {
		t.Fatalf("expected Iron Golem T-shape to summon golem: summoned=%v id=%d", summoned, golemID)
	}
	if !s.IsIronGolemPlayerCreated(golemID) {
		t.Fatalf("expected block-built Iron Golem to be marked PlayerCreated")
	}
	if s.chunks.GetBlock(4, 67, 4) != world.StateAir || s.chunks.GetBlock(4, 65, 4) != world.StateAir {
		t.Fatalf("expected Iron Golem pattern blocks to be cleared to air")
	}

	// 2. Iron Golem targets and attacks nearby hostile zombie.
	zombieID, _ := s.SpawnMob(5.5, 65, 4.5, MobTypeZombie)
	zHPBefore := s.MobHealth(zombieID)
	s.TickIronGolems(rng)
	if s.MobHealth(zombieID) >= zHPBefore {
		t.Fatalf("expected Iron Golem to damage nearby zombie (before=%.1f, after=%.1f)", zHPBefore, s.MobHealth(zombieID))
	}

	// 3. Repair damaged Iron Golem with iron_ingot (+25 HP).
	s.SetMobHealth(golemID, 60.0)
	player.SetInventorySlot(slotIdx, world.NewNamedItemStack("minecraft:iron_ingot", 2))
	if !s.InteractWithIronGolem(player, golemID) || s.MobHealth(golemID) != 85.0 {
		t.Fatalf("expected iron_ingot to heal Iron Golem from 60 to 85 HP, got %.1f", s.MobHealth(golemID))
	}

	// 4. Villager panic spawns a village Iron Golem when 3 villagers are near a zombie.
	_, _ = s.SpawnVillager(40.5, 65, 40.5, VillagerProfessionFarmer, 1)
	_, _ = s.SpawnVillager(41.5, 65, 40.5, VillagerProfessionLibrarian, 1)
	_, _ = s.SpawnVillager(42.5, 65, 40.5, VillagerProfessionCleric, 1)
	_, _ = s.SpawnMob(43.5, 65, 40.5, MobTypeZombie)

	villageGolemID, vSpawned := s.TickVillagerGolemSpawning(rng)
	if !vSpawned || villageGolemID <= 0 {
		t.Fatalf("expected 3 panicked villagers near a zombie to spawn a village Iron Golem")
	}
	if s.IsIronGolemPlayerCreated(villageGolemID) {
		t.Fatalf("expected village-spawned Iron Golem to have PlayerCreated=false")
	}
}

func TestPhantomInsomniaSpawningSwoopAndDaylightBurn(t *testing.T) {
	s := newTestMobBehaviorServer(t)
	player := newTestMobBehaviorPlayer(t, s, "Insomniac", 4, 8.5, 65, 8.5)

	// Set world time to midnight (18000 = night) and Normal difficulty.
	s.SetDayTime(18000)

	// Below 72000 ticks of TIME_SINCE_REST -> not eligible.
	s.SetPlayerStat(player, StatTypeCustom, StatTimeSinceRest, 36000)
	if _, ok := s.CanSpawnPhantomsForPlayer(player); ok {
		t.Fatalf("expected player with time_since_rest=36000 not to spawn phantoms")
	}

	// At >= 72000 ticks -> eligible and spawns phantoms 20..34 blocks above player.
	s.SetPlayerStat(player, StatTypeCustom, StatTimeSinceRest, 80000)
	ids := s.TrySpawnPhantomsForPlayer(player, nil)
	if len(ids) == 0 {
		t.Fatalf("expected TrySpawnPhantomsForPlayer to spawn at least 1 phantom")
	}
	phantomID := ids[0]
	pEnt := s.entities.Get(phantomID)
	if pEnt == nil || pEnt.Y < 85.0 {
		t.Fatalf("expected phantom to spawn >= 20 blocks above player, got Y=%.2f", pEnt.Y)
	}

	// Put phantom in Swooping phase right above player and tick -> bites player.
	s.SetPhantomPhase(phantomID, PhantomPhaseSwooping)
	s.entities.Update(phantomID, func(e *world.Entity) {
		e.X = 8.5
		e.Y = 66.0
		e.Z = 8.5
	})
	hpBefore := player.Snapshot().Health
	s.TickPhantoms(nil)
	if player.Snapshot().Health >= hpBefore {
		t.Fatalf("expected swooping phantom to bite player (hpBefore=%.1f, hpNow=%.1f)", hpBefore, player.Snapshot().Health)
	}

	// Switch to daytime (6000 = noon) -> phantom catches fire and burns under open sky.
	s.SetDayTime(6000)
	if !s.IsPhantomInDaylight(world.DimensionOverworld, 8.5, 85.0, 8.5) {
		t.Fatalf("expected phantom at Y=85 at noon to be in daylight")
	}
	s.TickPhantoms(nil)
	if !s.IsEntityOnFire(phantomID) {
		t.Fatalf("expected phantom to catch fire in daytime sunlight")
	}
}

func TestMobCauldronHookExtinguishesBurningMob(t *testing.T) {
	s := newTestMobBehaviorServer(t)

	waterCauldronLvl3 := CauldronWaterBlockState(3)
	if waterCauldronLvl3 == world.StateAir {
		t.Fatalf("expected valid water_cauldron level 3 state")
	}
	s.chunks.SetBlock(12, 65, 12, waterCauldronLvl3)

	pigID, err := s.SpawnMob(12.5, 65.2, 12.5, MobTypePig)
	if err != nil {
		t.Fatalf("SpawnMob pig: %v", err)
	}
	s.SetEntityFireTicks(pigID, 100)
	if !s.IsEntityOnFire(pigID) {
		t.Fatalf("expected pig to be on fire before cauldron tick")
	}

	s.TickMobsInCauldrons()

	if s.IsEntityOnFire(pigID) {
		t.Fatalf("expected TickMobsInCauldrons to extinguish burning pig inside water_cauldron")
	}
	_, newLvl, isCauld := ParseCauldronState(s.chunks.GetBlock(12, 65, 12))
	if !isCauld || newLvl != 2 {
		t.Fatalf("expected water_cauldron level to drop from 3 to 2 after extinguishing mob, got lvl=%d", newLvl)
	}
}
