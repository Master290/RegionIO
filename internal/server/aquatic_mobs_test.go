package server

import (
	"math/rand"
	"testing"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

func placeWaterColumnForAquaticTest(s *Server, x, yMin, yMax, z int) {
	cache := s.DimensionChunks(world.DimensionOverworld)
	if cache == nil {
		return
	}
	for y := yMin; y <= yMax; y++ {
		cache.SetBlock(x, y, z, world.StateWater)
	}
}

func TestDolphinTrustTreasureEscortBreathingAndItemPlay(t *testing.T) {
	s := newSurvivalTestServer(t)
	s.SetDifficulty(DifficultyNormal)
	for x := 0; x <= 20; x++ {
		placeWaterColumnForAquaticTest(s, x, 60, 63, 0)
	}

	dolphinID, err := s.SpawnDolphin(world.DimensionOverworld, 2.5, 62.2, 0.5)
	if err != nil || dolphinID == 0 {
		t.Fatalf("SpawnDolphin failed: id=%d err=%v", dolphinID, err)
	}
	if hp := s.MobHealth(dolphinID); hp != DefaultDolphinMaxHealth {
		t.Fatalf("dolphin health = %v, want %v", hp, DefaultDolphinMaxHealth)
	}

	s.SetDolphinTreasureTarget(dolphinID, 15, 62, 0)

	p := newBeeTestPlayer(t, s, "Diver", 3.0, 62.2, 0.5)
	p.SetInventorySlot(world.HotbarContainerOffset, world.NewNamedItemStack("minecraft:cod", 2))

	if !s.InteractWithDolphin(p, dolphinID) {
		t.Fatalf("expected feeding dolphin raw cod to succeed")
	}
	st, ok := s.GetDolphinState(dolphinID)
	if !ok || !st.GotFish || !st.CanFindTreasure {
		t.Fatalf("expected GotFish and CanFindTreasure after feeding, got %+v", st)
	}
	if !p.HasPlayerEffect(EffectDolphinsGrace) {
		t.Fatalf("expected Dolphin's Grace effect on player after feeding dolphin")
	}

	startEnt := s.entities.Get(dolphinID)
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 10; i++ {
		s.TickAquaticMobs(rng)
	}
	movedEnt := s.entities.Get(dolphinID)
	if movedEnt == nil || movedEnt.X <= startEnt.X {
		t.Fatalf("expected dolphin to escort toward treasure X=15, startX=%v movedX=%v", startEnt.X, movedEnt.X)
	}

	// Test playing with floating item in water when not escorting.
	s.aquaticManager().mu.Lock()
	s.aquaticManager().dolphins[dolphinID].GotFish = false
	s.aquaticManager().dolphins[dolphinID].CanFindTreasure = false
	s.aquaticManager().dolphins[dolphinID].PlayItemCooldown = 0
	s.aquaticManager().mu.Unlock()

	itemID, _ := s.SpawnItemEntityInDimension(world.DimensionOverworld, movedEnt.X+0.5, movedEnt.Y, movedEnt.Z,
		world.NewNamedItemStack("minecraft:kelp", 1))
	if itemID == 0 {
		t.Fatalf("SpawnItemEntityInDimension returned 0")
	}
	playedID, played := s.TryDolphinPlayWithItem(dolphinID, rng)
	if !played || playedID != itemID {
		t.Fatalf("expected dolphin to play with item %d, got playedID=%d played=%v", itemID, playedID, played)
	}

	// Test moistness depletion on dry land (FluidHeightAt == 0) causing dry-out damage.
	dryDolphinID, err := s.SpawnDolphin(world.DimensionOverworld, -20.5, 70.0, -20.5)
	if err != nil {
		t.Fatalf("SpawnDolphin on land failed: %v", err)
	}
	s.SetDolphinAirAndMoistness(dryDolphinID, DolphinMaxAirSupplyTicks, 1)
	hpBefore := s.MobHealth(dryDolphinID)
	for i := 0; i < 21; i++ {
		s.TickAquaticMobs(rng)
	}
	if hpAfter := s.MobHealth(dryDolphinID); hpAfter >= hpBefore {
		t.Fatalf("expected beached dolphin to take dry-out damage, before=%v after=%v", hpBefore, hpAfter)
	}
}

func TestTurtleHomeBeachEggLayingIncubationSilkTouchAndBabyScute(t *testing.T) {
	s := newSurvivalTestServer(t)
	s.SetDifficulty(DifficultyNormal)
	cache := s.DimensionChunks(world.DimensionOverworld)
	sandState, _ := world.BlockStateIDByProperties("minecraft:sand", nil)
	for x := 8; x <= 14; x++ {
		for z := 8; z <= 14; z++ {
			cache.SetBlock(x, 63, z, sandState)
		}
	}

	t1, err := s.SpawnTurtle(world.DimensionOverworld, 10.5, 64.0, 10.5, false)
	if err != nil {
		t.Fatalf("SpawnTurtle 1 failed: %v", err)
	}
	t2, err := s.SpawnTurtle(world.DimensionOverworld, 11.2, 64.0, 10.5, false)
	if err != nil {
		t.Fatalf("SpawnTurtle 2 failed: %v", err)
	}
	st1, ok := s.GetTurtleState(t1)
	if !ok || st1.HomeX != 10 || st1.HomeY != 64 || st1.HomeZ != 10 {
		t.Fatalf("unexpected initial turtle HomePos: %+v", st1)
	}

	p := newBeeTestPlayer(t, s, "Breeder", 10.5, 64.0, 10.5)
	p.SetInventorySlot(world.HotbarContainerOffset, world.NewNamedItemStack(TurtleBreedingFoodItemID, 4))

	rng := rand.New(rand.NewSource(7))
	if h, _ := s.InteractWithTurtle(p, t1, rng); !h {
		t.Fatalf("expected feeding seagrass to turtle 1 to succeed")
	}
	h2, hasEgg := s.InteractWithTurtle(p, t2, rng)
	if !h2 || !hasEgg {
		t.Fatalf("expected feeding seagrass to turtle 2 to complete mating and set HasEgg, h2=%v hasEgg=%v", h2, hasEgg)
	}

	var laidEggs int
	var laidX, laidY, laidZ int
	s.SetTurtleEggPlaceHook(func(dim string, x, y, z int, eggCount int) bool {
		laidEggs = eggCount
		laidX, laidY, laidZ = x, y, z
		return true
	})

	for i := 0; i < TurtleLayEggDigTicks+5; i++ {
		s.TickAquaticMobs(rng)
	}
	if laidEggs < 1 || laidEggs > 4 {
		t.Fatalf("expected pregnant turtle to lay 1..4 eggs after digging on home sand beach, got %d", laidEggs)
	}

	// Test turtle egg incubation (hatch 0 -> 1 -> 2 -> hatch baby turtles).
	hStage, hatchedIDs, hatched := s.StepTurtleEggIncubationAt(world.DimensionOverworld, laidX, laidY, laidZ, 0, 2, true, rng)
	if hatched || hStage != 1 || len(hatchedIDs) != 0 {
		t.Fatalf("expected hatch stage 0 -> 1, got stage=%d hatched=%v", hStage, hatched)
	}
	hStage, hatchedIDs, hatched = s.StepTurtleEggIncubationAt(world.DimensionOverworld, laidX, laidY, laidZ, hStage, 2, true, rng)
	if hatched || hStage != 2 {
		t.Fatalf("expected hatch stage 1 -> 2, got stage=%d hatched=%v", hStage, hatched)
	}
	_, hatchedIDs, hatched = s.StepTurtleEggIncubationAt(world.DimensionOverworld, laidX, laidY, laidZ, hStage, 2, true, rng)
	if !hatched || len(hatchedIDs) != 2 {
		t.Fatalf("expected 2 baby turtles to hatch at stage 2, got hatched=%v ids=%v", hatched, hatchedIDs)
	}

	babyID := hatchedIDs[0]
	if !s.IsMobBaby(babyID) {
		t.Fatalf("expected hatched turtle %d to be a baby", babyID)
	}

	// Advance baby turtle to adulthood (-1 -> 0) and verify scute drop!
	s.SetMobAge(babyID, -1)
	s.TickBabyMobs()
	if s.IsMobBaby(babyID) {
		t.Fatalf("expected baby turtle %d to grow into adult after TickBabyMobs", babyID)
	}
	foundScute := false
	for _, ent := range s.entities.AppendAll(nil) {
		if ent.IsItem() && (ent.Item.ResolvedID() == TurtleScuteItemID || ent.Item.ResolvedID() == LegacyScuteItemID) {
			foundScute = true
			break
		}
	}
	if !foundScute {
		t.Fatalf("expected baby turtle maturing into adult to drop %s", TurtleScuteItemID)
	}

	// Verify Silk Touch requirement on harvesting turtle_egg.
	remNoSilk, dropNoSilk := s.HarvestTurtleEggBlock(world.DimensionOverworld, 10, 64, 10, 2, ItemMeta{})
	if remNoSilk != 1 || !dropNoSilk.IsEmpty() {
		t.Fatalf("breaking turtle egg without Silk Touch should not drop item, rem=%d drop=%+v", remNoSilk, dropNoSilk)
	}
	var silkMeta ItemMeta
	silkMeta.SetEnchantment(EnchantSilkTouch, 1)
	remSilk, dropSilk := s.HarvestTurtleEggBlock(world.DimensionOverworld, 10, 64, 10, 1, silkMeta)
	if remSilk != 0 || dropSilk.IsEmpty() || dropSilk.ResolvedID() != TurtleEggItemID {
		t.Fatalf("breaking turtle egg with Silk Touch should drop %s, rem=%d drop=%+v", TurtleEggItemID, remSilk, dropSilk)
	}
}

func TestSquidAndGlowSquidInkCloudAndDarkTicks(t *testing.T) {
	s := newSurvivalTestServer(t)
	s.SetDifficulty(DifficultyNormal)
	placeWaterColumnForAquaticTest(s, 0, 60, 64, 0)

	squidID, err := s.SpawnSquid(world.DimensionOverworld, 0.5, 62.0, 0.5)
	if err != nil {
		t.Fatalf("SpawnSquid failed: %v", err)
	}
	glowID, err := s.SpawnGlowSquid(world.DimensionOverworld, 0.5, 62.0, 0.5)
	if err != nil {
		t.Fatalf("SpawnGlowSquid failed: %v", err)
	}

	attacker := newBeeTestPlayer(t, s, "Hunter", 1.5, 62.0, 0.5)

	s.OnSquidDamaged(squidID, attacker)
	if !attacker.HasPlayerEffect(EffectBlindness) {
		t.Fatalf("expected squid ink cloud to apply Blindness to nearby attacker")
	}

	s.OnSquidDamaged(glowID, attacker)
	gst, ok := s.GetSquidState(glowID)
	if !ok || gst.DarkTicksRemaining != GlowSquidDarkDurationTicks {
		t.Fatalf("expected glow squid DarkTicksRemaining = %d after damage, got %+v", GlowSquidDarkDurationTicks, gst)
	}

	// Verify GlowSquid SynchedEntityData packet roundtrip.
	pkt := EncodeGlowSquidDataPacket(glowID, GlowSquidDarkDurationTicks)
	decID, decDark, err := DecodeGlowSquidDataPacket(protocol.NewReader(pkt))
	if err != nil || decID != glowID || decDark != GlowSquidDarkDurationTicks {
		t.Fatalf("DecodeGlowSquidDataPacket mismatch: id=%d dark=%d err=%v", decID, decDark, err)
	}

	// Verify loot drops for squid and glow_squid.
	rng := rand.New(rand.NewSource(11))
	squidLoot := MobLootDrops(MobTypeSquid, rng)
	if len(squidLoot) != 1 || squidLoot[0].ResolvedID() != InkSacItemID {
		t.Fatalf("unexpected squid loot: %+v", squidLoot)
	}
	glowLoot := MobLootDrops(MobTypeGlowSquid, rng)
	if len(glowLoot) != 1 || glowLoot[0].ResolvedID() != GlowInkSacItemID {
		t.Fatalf("unexpected glow_squid loot: %+v", glowLoot)
	}
}

func TestFishSchoolingLandFlopPufferfishPoisonAndBuckets(t *testing.T) {
	s := newSurvivalTestServer(t)
	s.SetDifficulty(DifficultyNormal)
	for x := 0; x <= 10; x++ {
		placeWaterColumnForAquaticTest(s, x, 60, 64, 0)
	}
	rng := rand.New(rand.NewSource(99))

	leaderID, err := s.SpawnFish(world.DimensionOverworld, 8.5, 62.0, 0.5, MobTypeCod, false, rng)
	if err != nil {
		t.Fatalf("SpawnFish leader failed: %v", err)
	}
	followerID, err := s.SpawnFish(world.DimensionOverworld, 2.5, 62.0, 0.5, MobTypeCod, false, rng)
	if err != nil {
		t.Fatalf("SpawnFish follower failed: %v", err)
	}
	if !s.LinkFishToSchoolLeader(followerID, leaderID) {
		t.Fatalf("expected LinkFishToSchoolLeader to succeed for two cod")
	}

	fBefore := s.entities.Get(followerID)
	for i := 0; i < 5; i++ {
		s.TickAquaticMobs(rng)
	}
	fAfter := s.entities.Get(followerID)
	if fAfter == nil || fAfter.X <= fBefore.X {
		t.Fatalf("expected schooling follower cod to swim toward leader at X=8.5, before=%v after=%v", fBefore.X, fAfter.X)
	}

	// Land flopping on dry block.
	landSalmonID, err := s.SpawnFish(world.DimensionOverworld, -15.5, 70.0, -15.5, MobTypeSalmon, false, rng)
	if err != nil {
		t.Fatalf("SpawnFish salmon failed: %v", err)
	}
	s.TickAquaticMobs(rng)
	salmonEnt := s.entities.Get(landSalmonID)
	if salmonEnt == nil || salmonEnt.VelocityY <= 0 {
		t.Fatalf("expected salmon on dry land to flop with positive VelocityY, got %+v", salmonEnt)
	}

	// Pufferfish inflation and poison sting when player approaches.
	puffID, err := s.SpawnFish(world.DimensionOverworld, 5.5, 62.0, 0.5, MobTypePufferfish, false, rng)
	if err != nil {
		t.Fatalf("SpawnFish pufferfish failed: %v", err)
	}
	swimmer := newBeeTestPlayer(t, s, "Swimmer", 6.0, 62.0, 0.5)

	s.TickAquaticMobs(rng)
	pst, ok := s.GetFishState(puffID)
	if !ok || pst.PuffState < PufferfishHalfPuffed {
		t.Fatalf("expected pufferfish to inflate when survival player is within 2.5 blocks, got %+v", pst)
	}
	if !swimmer.HasPlayerEffect(EffectPoison) {
		t.Fatalf("expected puffed pufferfish to sting and poison nearby player")
	}

	// Pufferfish SynchedEntityData packet roundtrip.
	ppkt := EncodePufferfishDataPacket(puffID, true, PufferfishFullyPuffed)
	dID, dBucket, dPuff, err := DecodePufferfishDataPacket(protocol.NewReader(ppkt))
	if err != nil || dID != puffID || !dBucket || dPuff != PufferfishFullyPuffed {
		t.Fatalf("DecodePufferfishDataPacket mismatch: id=%d bucket=%v puff=%d err=%v", dID, dBucket, dPuff, err)
	}

	// Catching fish in water_bucket and releasing it back.
	swimmer.SetInventorySlot(world.HotbarContainerOffset, world.NewNamedItemStack(WaterBucketItemID, 1))
	bucketItem, caught := s.CatchAquaticMobInBucket(swimmer, puffID)
	if !caught || bucketItem != PufferfishBucketItemID {
		t.Fatalf("expected catching pufferfish in water_bucket to yield %s, got %q caught=%v", PufferfishBucketItemID, bucketItem, caught)
	}
	placedID, placed := s.PlaceAquaticMobFromBucket(swimmer, world.DimensionOverworld, 5.5, 62.0, 0.5)
	if !placed || placedID == 0 {
		t.Fatalf("expected placing pufferfish from bucket to succeed")
	}
	placedSt, ok := s.GetFishState(placedID)
	if !ok || !placedSt.FromBucket {
		t.Fatalf("expected bucket-placed fish to have FromBucket=true, got %+v", placedSt)
	}
}

func TestGuardianLaserThornsAndElderGuardianMiningFatigue(t *testing.T) {
	s := newSurvivalTestServer(t)
	s.SetDifficulty(DifficultyNormal)
	placeWaterColumnForAquaticTest(s, 0, 60, 64, 0)

	gID, err := s.SpawnGuardian(world.DimensionOverworld, 0.5, 62.0, 0.5)
	if err != nil {
		t.Fatalf("SpawnGuardian failed: %v", err)
	}
	elderID, err := s.SpawnElderGuardian(world.DimensionOverworld, 2.5, 62.0, 0.5)
	if err != nil {
		t.Fatalf("SpawnElderGuardian failed: %v", err)
	}

	// Verify Guardian SynchedEntityData packet roundtrip.
	gpkt := EncodeGuardianDataPacket(gID, true, 77)
	decID, decMoving, decTarget, err := DecodeGuardianDataPacket(protocol.NewReader(gpkt))
	if err != nil || decID != gID || !decMoving || decTarget != 77 {
		t.Fatalf("DecodeGuardianDataPacket mismatch: id=%d moving=%v target=%d err=%v", decID, decMoving, decTarget, err)
	}

	raider := newBeeTestPlayer(t, s, "Raider", 4.5, 62.0, 0.5)

	// Stationary guardian has spikes extended -> melee attack triggers 2.0 HP Thorns retaliation.
	hpBeforeThorns := raider.Snapshot().Health
	retaliated, thornsDmg := s.OnGuardianThornsRetaliate(gID, raider)
	if !retaliated || thornsDmg <= 0 || raider.Snapshot().Health >= hpBeforeThorns {
		t.Fatalf("expected stationary guardian spikes to retaliate on attacker, retaliated=%v dmg=%v", retaliated, thornsDmg)
	}

	// Elder Guardian applies Mining Fatigue III aura (Amplifier=2, Duration=6000) within 50 blocks.
	affected := s.ApplyElderGuardianMiningFatigueAura(elderID)
	if affected != 1 {
		t.Fatalf("expected Elder Guardian aura to affect 1 player, got %d", affected)
	}
	eff, hasEff := raider.PlayerEffect(EffectMiningFatigue)
	if !hasEff || eff.Amplifier != ElderGuardianFatigueAmplifier || eff.Duration != ElderGuardianFatigueDurationTicks {
		t.Fatalf("unexpected Mining Fatigue effect from Elder Guardian: %+v has=%v", eff, hasEff)
	}

	// Laser beam charge (60 ticks for Elder Guardian, 80 ticks for Guardian) deals laser damage.
	rng := rand.New(rand.NewSource(123))
	hpBeforeLaser := raider.Snapshot().Health
	for i := 0; i < ElderGuardianLaserChargeTicks+2; i++ {
		s.TickAquaticMobs(rng)
	}
	if raider.Snapshot().Health >= hpBeforeLaser {
		t.Fatalf("expected guardian laser beam to damage target player after charge completes, before=%v after=%v",
			hpBeforeLaser, raider.Snapshot().Health)
	}
}
