package server

import (
	"math"
	"math/rand"
	"testing"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

func TestHappyGhastDriedGhastHydrationSnowballGrowthHarness4PassengersStandingStillAndHealing(t *testing.T) {
	s := newSurvivalTestServer(t)
	rng := rand.New(rand.NewSource(261258))

	if got := ResolveEntityTypeID(MobTypeHappyGhast); got != EntityTypeHappyGhastID {
		t.Fatalf("ResolveEntityTypeID(%s) = %d, want %d", MobTypeHappyGhast, got, EntityTypeHappyGhastID)
	}
	if got := ResolveMobMaxHealth(MobTypeHappyGhast); got != DefaultHappyGhastMaxHealth {
		t.Fatalf("ResolveMobMaxHealth(%s) = %v, want %v", MobTypeHappyGhast, got, DefaultHappyGhastMaxHealth)
	}

	// 1. SynchedEntityData packet roundtrip (indices 16, 17, 18).
	pkt := EncodeHappyGhastEntityDataPacket(580, true, true, true)
	r := protocol.NewReader(pkt)
	eid, baby, isHolder, staysStill, err := DecodeHappyGhastEntityDataPacket(r)
	if err != nil {
		t.Fatalf("DecodeHappyGhastEntityDataPacket failed: %v", err)
	}
	if eid != 580 || !baby || !isHolder || !staysStill {
		t.Fatalf("decoded = (%d, %v, %v, %v), want (580, true, true, true)", eid, baby, isHolder, staysStill)
	}

	// 2. Tags, underwater breathing, love mode, and restriction radii.
	if !IsHappyGhastFood2612("minecraft:snowball") || IsHappyGhastFood2612("minecraft:apple") {
		t.Fatal("IsHappyGhastFood2612 mismatch")
	}
	harnesses := AllHarnessItemIDs2612()
	if len(harnesses) != 16 {
		t.Fatalf("AllHarnessItemIDs2612 len = %d, want 16", len(harnesses))
	}
	for _, h := range harnesses {
		if !IsHarnessItem2612(h) || !IsHappyGhastTemptItem2612(h) {
			t.Fatalf("expected %q to be a valid harness and tempt item", h)
		}
	}
	if !IsHappyGhastTemptItem2612("minecraft:snowball") {
		t.Fatal("expected snowball to be a Happy Ghast tempt item")
	}
	if !CanHappyGhastBreatheUnderwater(true) || CanHappyGhastBreatheUnderwater(false) {
		t.Fatal("CanHappyGhastBreatheUnderwater mismatch (only baby ghastling can breathe underwater)")
	}
	if CanHappyGhastFallInLove() {
		t.Fatal("expected CanHappyGhastFallInLove() == false")
	}
	if HappyGhastRestrictionRadius(true, false) != 32 || HappyGhastRestrictionRadius(false, true) != 32 || HappyGhastRestrictionRadius(false, false) != 64 {
		t.Fatal("HappyGhastRestrictionRadius mismatch")
	}

	// 3. Dried Ghast block hydration (0 -> 1 -> 2 -> 3 -> spawns baby ghastling).
	h1, gid, snd := s.TickDriedGhastHydration2612(world.DimensionOverworld, 10, 64, 10, true, 0, "east")
	if h1 != 1 || gid != 0 || snd != SoundDriedGhastTransition {
		t.Fatalf("hydration step 0->1 = (%d, %d, %q)", h1, gid, snd)
	}
	// Drying out when not waterlogged decrements hydration by 1.
	hDry, _, _ := s.TickDriedGhastHydration2612(world.DimensionOverworld, 10, 64, 10, false, h1, "east")
	if hDry != 0 {
		t.Fatalf("expected dry tick to decrement hydration to 0, got %d", hDry)
	}
	h2, _, _ := s.TickDriedGhastHydration2612(world.DimensionOverworld, 10, 64, 10, true, 1, "east")
	h3, _, _ := s.TickDriedGhastHydration2612(world.DimensionOverworld, 10, 64, 10, true, h2, "east")
	if h3 != DriedGhastMaxHydrationLevel {
		t.Fatalf("expected hydration level 3, got %d", h3)
	}
	_, ghastlingID, spawnSnd := s.TickDriedGhastHydration2612(world.DimensionOverworld, 10, 64, 10, true, h3, "east")
	if ghastlingID <= 0 || spawnSnd != SoundGhastlingSpawn {
		t.Fatalf("expected hydrated dried_ghast at level 3 to spawn baby ghastling: id=%d snd=%q", ghastlingID, spawnSnd)
	}
	st, ok := s.GetHappyGhastState(ghastlingID)
	if !ok || !st.Baby {
		t.Fatalf("expected spawned entity %d to be a baby ghastling: %+v", ghastlingID, st)
	}

	// 4. Feed snowballs to grow baby ghastling into an adult Happy Ghast.
	p1 := newBeeTestPlayer(t, s, "Pilot1", 10.5, 64, 10.5)
	p2 := newBeeTestPlayer(t, s, "Rider2", 10.5, 64, 10.5)
	p3 := newBeeTestPlayer(t, s, "Rider3", 10.5, 64, 10.5)
	p4 := newBeeTestPlayer(t, s, "Rider4", 10.5, 64, 10.5)
	p5 := newBeeTestPlayer(t, s, "Extra5", 10.5, 64, 10.5)

	p1Slot := world.HotbarContainerOffset + int(p1.Snapshot().SelectedSlot)
	p1.SetInventorySlot(p1Slot, world.NewNamedItemStack("minecraft:snowball", 64))
	grewUp := false
	for i := 0; i < 64; i++ {
		handled, action := s.InteractWithHappyGhast(p1, ghastlingID)
		if !handled {
			t.Fatalf("InteractWithHappyGhast(feed_baby) step %d returned false", i)
		}
		if action == "grow_up" {
			grewUp = true
			break
		}
	}
	if !grewUp {
		t.Fatal("expected baby ghastling to grow up after feeding snowballs")
	}

	// 5. Equip harness, mount 4 passengers, steer flight, dismount, and shear harness.
	p1.SetInventorySlot(p1Slot, world.NewNamedItemStack("minecraft:blue_harness", 1))
	handled, action := s.InteractWithHappyGhast(p1, ghastlingID)
	if !handled || action != "equip_harness" {
		t.Fatalf("expected equip_harness, got (%v, %q)", handled, action)
	}
	for idx, rider := range []*PlayerSession{p1, p2, p3, p4} {
		h, act := s.InteractWithHappyGhast(rider, ghastlingID)
		if !h || act != "mount" {
			t.Fatalf("rider %d mount failed: (%v, %q)", idx+1, h, act)
		}
	}
	if h, _ := s.InteractWithHappyGhast(p5, ghastlingID); h {
		t.Fatal("expected 5th passenger to be rejected when Happy Ghast already has 4 passengers")
	}
	vx, vy, vz, ok := s.SteerHappyGhastByDriver(ghastlingID, p1, 1.0, 0.0, 1.0)
	if !ok || math.Abs(vy-HappyGhastFlyingSpeed*HappyGhastRiddenSpeedMultiplier) > 1e-6 || (vx == 0 && vz == 0) {
		t.Fatalf("SteerHappyGhastByDriver = (%v, %v, %v, %v)", vx, vy, vz, ok)
	}

	for _, rider := range []*PlayerSession{p1, p2, p3, p4} {
		if !s.DismountFromHappyGhast(rider, ghastlingID) {
			t.Fatal("DismountFromHappyGhast returned false")
		}
	}
	p1.SetInventorySlot(p1Slot, world.NewNamedItemStack("minecraft:shears", 1))
	handled, action = s.InteractWithHappyGhast(p1, ghastlingID)
	if !handled || action != "shear_harness" {
		t.Fatalf("expected shear_harness when unmounted, got (%v, %q)", handled, action)
	}

	// 6. Standing on top of adult Happy Ghast sets StaysStill = true & ServerStillTimeout = 10.
	ent := s.Entities().Get(ghastlingID)
	s.SetPlayerTransform(p2, ent.X, ent.Y+HappyGhastAdultHeight+0.2, ent.Z, 0, 0, true)
	if !s.ScanPlayerAboveHappyGhast(ghastlingID) {
		t.Fatal("expected ScanPlayerAboveHappyGhast to detect player standing on top of Happy Ghast")
	}
	s.TickHappyGhasts(rng)
	st, _ = s.GetHappyGhastState(ghastlingID)
	if !st.StaysStill || st.ServerStillTimeout != HappyGhastMaxStillTimeout {
		t.Fatalf("expected StaysStill=true and ServerStillTimeout=10, got %+v", st)
	}

	// 7. Continuous fast healing (every 20 ticks in precipitation / clouds).
	s.SetMobHealth(ghastlingID, 15.0)
	s.SetHappyGhastPrecipitation(ghastlingID, true)
	for i := 0; i < HappyGhastFastHealingTicks; i++ {
		s.TickHappyGhasts(rng)
	}
	if hp := s.MobHealth(ghastlingID); hp != 16.0 {
		t.Fatalf("expected Happy Ghast health 16.0 after 20 ticks in precipitation, got %v", hp)
	}
}

func TestParchedCamelHuskAndDesertJockeyTrio2612(t *testing.T) {
	s := newSurvivalTestServer(t)
	rng := rand.New(rand.NewSource(261297))

	if got := ResolveEntityTypeID(MobTypeParched); got != EntityTypeParchedID {
		t.Fatalf("ResolveEntityTypeID(%s) = %d, want %d", MobTypeParched, got, EntityTypeParchedID)
	}
	if got := ResolveEntityTypeID(MobTypeCamelHusk); got != EntityTypeCamelHuskID {
		t.Fatalf("ResolveEntityTypeID(%s) = %d, want %d", MobTypeCamelHusk, got, EntityTypeCamelHuskID)
	}
	if !IsSkeletonFamilyMob(MobTypeParched) || !IsUndeadMobType(MobTypeParched) || !IsUndeadMobType(MobTypeCamelHusk) {
		t.Fatal("expected Parched and CamelHusk to be undead")
	}
	if DoesUndeadBurnInDaylight(MobTypeParched) || DoesUndeadBurnInDaylight(MobTypeCamelHusk) {
		t.Fatal("expected Parched and CamelHusk NOT to burn in daylight")
	}
	if CanParchedBeAffectedByEffect2612(EffectWeakness) || !CanParchedBeAffectedByEffect2612(EffectSlowness) {
		t.Fatal("CanParchedBeAffectedByEffect2612 mismatch (Parched is immune to Weakness)")
	}
	if ParchedAttackIntervalTicks2612(true) != 50 || ParchedAttackIntervalTicks2612(false) != 70 {
		t.Fatal("ParchedAttackIntervalTicks2612 mismatch")
	}

	// 1. Desert Husk Jockey Trio: CamelHusk + Husk (iron_spear) + Parched (bow).
	camelID, huskID, parchedID, spawned := s.TrySpawnHuskCamelJockeyTrio2612(world.DimensionOverworld, 30.5, 64, 30.5, rng, true)
	if !spawned || camelID <= 0 || huskID <= 0 || parchedID <= 0 {
		t.Fatalf("TrySpawnHuskCamelJockeyTrio2612 failed: camel=%d husk=%d parched=%d spawned=%v", camelID, huskID, parchedID, spawned)
	}
	if !s.IsCamelHuskMobControlled(camelID) || s.CanCamelHuskBeLeashed(camelID) {
		t.Fatal("expected mob-ridden CamelHusk to report IsCamelHuskMobControlled=true and CanCamelHuskBeLeashed=false")
	}

	// 2. Parched shoots weakness-tipped arrow (600 ticks Weakness) at player.
	target := newBeeTestPlayer(t, s, "DesertTraveler", 34.5, 64, 30.5)
	cd, applied := s.ShootParchedWeaknessArrow(parchedID, target, 0, true)
	if cd != ParchedHardAttackIntervalTicks || !applied {
		t.Fatalf("ShootParchedWeaknessArrow = (%d, %v), want (50, true)", cd, applied)
	}
	if !target.HasPlayerEffect(EffectWeakness) {
		t.Fatal("expected target player to have EffectWeakness after being hit by Parched arrow")
	}

	// 3. CamelHusk player interaction: sets PersistenceRequired and feeds rabbit_foot.
	soloCamelID, err := s.SpawnCamelHusk(world.DimensionOverworld, 40.5, 64, 40.5)
	if err != nil || soloCamelID <= 0 {
		t.Fatalf("SpawnCamelHusk failed: %v", err)
	}
	s.SetMobHealth(soloCamelID, 25.0)
	slotIdx := world.HotbarContainerOffset + int(target.Snapshot().SelectedSlot)
	target.SetInventorySlot(slotIdx, world.NewNamedItemStack(CamelHuskFoodItemID, 2))
	handled, action := s.InteractWithCamelHusk(target, soloCamelID)
	if !handled || action != "feed" {
		t.Fatalf("InteractWithCamelHusk(rabbit_foot) = (%v, %q), want (true, feed)", handled, action)
	}
	if hp := s.MobHealth(soloCamelID); hp != 27.0 {
		t.Fatalf("expected CamelHusk health 27.0 after feeding rabbit_foot, got %v", hp)
	}
	cst, _ := s.GetCamelHuskState(soloCamelID)
	if !cst.PersistenceRequired {
		t.Fatal("expected InteractWithCamelHusk to set PersistenceRequired = true")
	}

	// 4. Parched & CamelHusk loot tables.
	camelLoot := RollCamelHuskDeathLoot2612(rng, 0)
	if len(camelLoot) != 1 || camelLoot[0].ResolvedID() != "minecraft:rotten_flesh" || camelLoot[0].Count < 2 || camelLoot[0].Count > 3 {
		t.Fatalf("RollCamelHuskDeathLoot2612 = %+v, want 2..3 rotten_flesh", camelLoot)
	}
	_ = RollParchedDeathLoot2612(rng, 2, true)
}

func TestPillagerPatrolSpawnerAndWanderingTraderSpawner2612(t *testing.T) {
	s := newSurvivalTestServer(t)
	s.SetDifficulty(DifficultyNormal)
	s.SetWorldTime(6000, 6000) // Daytime
	rng := rand.New(rand.NewSource(261299))

	_ = newBeeTestPlayer(t, s, "PatrolTarget", 100.5, 64, 100.5)

	// 1. Direct SpawnPillagerPatrolAt spawns ceil(effectiveDifficulty)+1 pillagers with leader at index 0.
	leaderID, members, err := s.SpawnPillagerPatrolAt(world.DimensionOverworld, 130.5, 64, 130.5, 2.0, rng)
	if err != nil || leaderID <= 0 || len(members) != 3 {
		t.Fatalf("SpawnPillagerPatrolAt(diff=2.0) = (leader=%d, members=%v, err=%v), want 3 members", leaderID, members, err)
	}
	if isCap, _ := s.IsRaidCaptain(leaderID); !isCap {
		t.Fatalf("expected patrol member 0 (%d) to be marked as Raid/Patrol Captain", leaderID)
	}

	// 2. TickPillagerPatrolSpawner triggers when NextTick reaches 0 during daytime.
	patrolSpawned := false
	for attempt := 0; attempt < 25; attempt++ {
		s.SetPillagerPatrolNextTick(1)
		ids, ok := s.TickPillagerPatrolSpawner(rng)
		if ok && len(ids) > 0 {
			patrolSpawned = true
			break
		}
	}
	if !patrolSpawned {
		t.Fatal("expected TickPillagerPatrolSpawner to spawn a patrol across daytime attempts")
	}

	// 3. StepWanderingTraderSpawner2612 advances spawn chance 25 -> 50 -> 75 and spawns trader + 2 llamas.
	traderSpawned := false
	for step := 0; step < 200; step++ {
		tID, llamas, chance, ok := s.StepWanderingTraderSpawner2612(rng, WanderingTraderSpawnerTickDelay)
		if chance < WanderingTraderSpawnerMinChance || chance > WanderingTraderSpawnerMaxChance {
			t.Fatalf("unexpected WanderingTraderSpawner chance %d", chance)
		}
		if ok && tID > 0 && len(llamas) == 2 {
			traderSpawned = true
			break
		}
	}
	if !traderSpawned {
		t.Fatal("expected StepWanderingTraderSpawner2612 to spawn a wandering trader with 2 trader llamas")
	}
}
