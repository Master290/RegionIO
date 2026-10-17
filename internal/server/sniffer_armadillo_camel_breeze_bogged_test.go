package server

import (
	"math"
	"math/rand"
	"testing"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

func newSurvivalTestSession(srv *Server, entityID int32, name string) *PlayerSession {
	col := &survivalPacketCollector{}
	sess, err := srv.RegisterPlayer(Profile{Name: name, UUID: OfflineUUID(name)}, col.send)
	if err != nil {
		return &PlayerSession{
			EntityID: entityID,
			Profile:  Profile{Name: name, UUID: OfflineUUID(name)},
			health:   20.0,
			send:     col.send,
		}
	}
	_ = srv.SetPlayerGameMode(sess, GameModeSurvival)
	return sess
}

func TestSnifferScentingDiggingEggHatchingMossBoostAndSniffletBreeding(t *testing.T) {
	srv := newSurvivalTestServer(t)
	rng := rand.New(rand.NewSource(42))

	if gotID := ResolveEntityTypeID(MobTypeSniffer); gotID != EntityTypeSnifferID {
		t.Fatalf("ResolveEntityTypeID(sniffer) = %d, want %d", gotID, EntityTypeSnifferID)
	}
	if gotHP := ResolveMobMaxHealth(MobTypeSniffer); gotHP != DefaultSnifferMaxHealth {
		t.Fatalf("ResolveMobMaxHealth(sniffer) = %v, want %v", gotHP, DefaultSnifferMaxHealth)
	}

	// Verify SynchedEntityData packet round-trip for Sniffer.
	pkt := EncodeSnifferEntityDataPacket(77, SnifferStateDigging, 120)
	gotEntID, gotState, gotDropTick, err := DecodeSnifferEntityDataPacket(protocol.NewReader(pkt))
	if err != nil {
		t.Fatalf("DecodeSnifferEntityDataPacket failed: %v", err)
	}
	if gotEntID != 77 || gotState != SnifferStateDigging || gotDropTick != 120 {
		t.Fatalf("decoded sniffer data = (%d, %d, %d), want (77, %d, 120)", gotEntID, gotState, gotDropTick, SnifferStateDigging)
	}

	// Place diggable grass_block and moss_block floor around (0, 63, 0).
	grassState, ok := world.BlockStateByName("minecraft:grass_block", nil)
	if !ok {
		t.Fatal("expected minecraft:grass_block state")
	}
	for dx := -3; dx <= 3; dx++ {
		for dz := -3; dz <= 3; dz++ {
			srv.chunks.SetBlock(dx, 63, dz, grassState)
			srv.chunks.SetBlock(dx, 64, dz, world.StateAir)
			srv.chunks.SetBlock(dx, 65, dz, world.StateAir)
		}
	}

	snifferID, err := srv.SpawnSniffer(world.DimensionOverworld, 0.5, 64.0, 0.5, false)
	if err != nil || snifferID == 0 {
		t.Fatalf("SpawnSniffer failed: id=%d, err=%v", snifferID, err)
	}

	// 1. Scenting -> Searching -> Digging -> Seed drop (torchflower_seeds / pitcher_pod).
	srv.SetSnifferDigCooldown(snifferID, 0)
	if !srv.TriggerSnifferScenting(snifferID, rng) {
		t.Fatal("expected TriggerSnifferScenting to succeed on grass_block floor")
	}
	st, ok := srv.GetSnifferState(snifferID)
	if !ok || st.State != SnifferStateScenting || !st.HasTargetDigPos {
		t.Fatalf("expected Sniffer in SCENTING state with target pos, got %+v", st)
	}
	firstDigPos := st.TargetDigPos

	// Advance through scenting, searching, and digging until seed drops.
	for tick := 0; tick < SnifferScentingDurationTicks+SnifferDiggingDurationTicks+20; tick++ {
		srv.TickSniffers(rng)
	}
	st, _ = srv.GetSnifferState(snifferID)
	if !st.SeedDropped || (st.LastDroppedSeed != TorchflowerSeedsItemID && st.LastDroppedSeed != PitcherPodItemID) {
		t.Fatalf("expected Sniffer to drop torchflower_seeds or pitcher_pod, got SeedDropped=%v LastDroppedSeed=%q", st.SeedDropped, st.LastDroppedSeed)
	}
	if len(st.ExploredPositions) == 0 || st.ExploredPositions[0] != firstDigPos {
		t.Fatalf("expected dug position %v to be recorded in ExploredPositions, got %v", firstDigPos, st.ExploredPositions)
	}

	// 2. Breeding two adult Sniffers with torchflower_seeds drops a sniffer_egg item entity.
	sniffer2ID, err := srv.SpawnSniffer(world.DimensionOverworld, 1.5, 64.0, 0.5, false)
	if err != nil {
		t.Fatalf("SpawnSniffer #2 failed: %v", err)
	}
	player := newSurvivalTestSession(srv, 1001, "SnifferBreeder")
	srv.SetPlayerTransform(player, 0.5, 64.0, 1.5, 0, 0, true)
	hotbarSlot := world.HotbarContainerOffset + int(player.Snapshot().SelectedSlot)
	player.SetInventorySlot(hotbarSlot, world.NewNamedItemStack(TorchflowerSeedsItemID, 4))

	handled1, egg1 := srv.InteractWithSniffer(player, snifferID)
	if !handled1 || egg1 != 0 {
		t.Fatalf("first Sniffer feed = (%v, %d), want (true, 0)", handled1, egg1)
	}
	handled2, eggEntityID := srv.InteractWithSniffer(player, sniffer2ID)
	if !handled2 || eggEntityID == 0 {
		t.Fatalf("second Sniffer feed = (%v, %d), want (true, >0 sniffer_egg item entity)", handled2, eggEntityID)
	}
	eggEnt := srv.entities.Get(eggEntityID)
	if eggEnt == nil || !eggEnt.IsItem() || eggEnt.Item.ResolvedID() != SnifferEggItemID {
		t.Fatalf("expected dropped minecraft:sniffer_egg item entity, got %+v", eggEnt)
	}

	// 3. Sniffer Egg hatching: normal block (8000 ticks/stage) vs moss_block (4000 ticks/stage, 2x faster).
	mossState, ok := world.BlockStateByName(MossBlockName, nil)
	if !ok {
		t.Fatal("expected minecraft:moss_block state")
	}
	srv.chunks.SetBlock(10, 63, 10, world.StateStone)
	srv.chunks.SetBlock(12, 63, 10, mossState)

	if !srv.PlaceSnifferEgg(world.DimensionOverworld, 10, 64, 10) {
		t.Fatal("PlaceSnifferEgg on stone failed")
	}
	if !srv.PlaceSnifferEgg(world.DimensionOverworld, 12, 64, 10) {
		t.Fatal("PlaceSnifferEgg on moss_block failed")
	}
	if srv.IsSnifferEggOnMossAt(world.DimensionOverworld, 10, 64, 10) {
		t.Fatal("expected egg on stone to report onMoss=false")
	}
	if !srv.IsSnifferEggOnMossAt(world.DimensionOverworld, 12, 64, 10) {
		t.Fatal("expected egg on moss_block to report onMoss=true")
	}

	// Fast-forward 3999 ticks into stage 0 for both eggs, then tick once:
	// the moss_block egg advances to stage 1 at 4000 ticks, while the stone egg stays at stage 0!
	srv.SetSnifferEggTicksInStage(world.DimensionOverworld, 10, 64, 10, SnifferEggMossBoostedStageTicks-1)
	srv.SetSnifferEggTicksInStage(world.DimensionOverworld, 12, 64, 10, SnifferEggMossBoostedStageTicks-1)
	srv.TickSnifferEggs(rng)

	stoneEggSt, _ := srv.SnifferEggStateAt(world.DimensionOverworld, 10, 64, 10)
	mossEggSt, _ := srv.SnifferEggStateAt(world.DimensionOverworld, 12, 64, 10)
	if stoneEggSt.HatchStage != 0 {
		t.Fatalf("stone egg stage after 4000 ticks = %d, want 0", stoneEggSt.HatchStage)
	}
	if mossEggSt.HatchStage != 1 {
		t.Fatalf("moss egg stage after 4000 ticks = %d, want 1", mossEggSt.HatchStage)
	}

	// Complete stage 1 -> 2 and stage 2 -> hatch on moss_block and verify Snifflet spawns!
	srv.SetSnifferEggTicksInStage(world.DimensionOverworld, 12, 64, 10, SnifferEggMossBoostedStageTicks-1)
	srv.TickSnifferEggs(rng) // stage 1 -> 2
	srv.SetSnifferEggTicksInStage(world.DimensionOverworld, 12, 64, 10, SnifferEggMossBoostedStageTicks-1)
	srv.TickSnifferEggs(rng) // stage 2 -> hatch!

	if world.BlockName(srv.chunks.GetBlock(12, 64, 10)) != "minecraft:air" {
		t.Fatalf("expected hatched sniffer_egg block to be replaced with air")
	}
	var sniffletID int32
	for _, ent := range srv.entities.AppendAll(nil) {
		if ent.TypeName == MobTypeSniffer && srv.IsMobBaby(ent.ID) {
			sniffletID = ent.ID
			break
		}
	}
	if sniffletID == 0 {
		t.Fatal("expected hatched sniffer_egg to spawn a baby Snifflet")
	}
	if gotAge := srv.MobAge(sniffletID); gotAge != SniffletGrowthTicks {
		t.Fatalf("Snifflet initial age = %d, want %d", gotAge, SniffletGrowthTicks)
	}
	// Feeding Snifflet torchflower_seeds accelerates its growth by 10%.
	fed, newAge := srv.FeedBabyMob(player, sniffletID, TorchflowerSeedsItemID)
	if !fed || newAge <= SniffletGrowthTicks {
		t.Fatalf("FeedBabyMob on Snifflet = (%v, %d), expected accelerated age > %d", fed, newAge, SniffletGrowthTicks)
	}
}

func TestArmadilloThreatRollUpDamageReductionScuteAndBreeding(t *testing.T) {
	srv := newSurvivalTestServer(t)
	rng := rand.New(rand.NewSource(99))

	if gotID := ResolveEntityTypeID(MobTypeArmadillo); gotID != EntityTypeArmadilloID {
		t.Fatalf("ResolveEntityTypeID(armadillo) = %d, want %d", gotID, EntityTypeArmadilloID)
	}

	armID, err := srv.SpawnArmadillo(world.DimensionOverworld, 0.5, 64.0, 0.5, false)
	if err != nil {
		t.Fatalf("SpawnArmadillo failed: %v", err)
	}
	if srv.IsArmadilloRolledUp(armID) {
		t.Fatal("expected newly spawned armadillo to be IDLE (not rolled up)")
	}
	if !srv.IsArmadilloScaringSpiders(armID) {
		t.Fatal("expected uncurled armadillo to scare spiders")
	}

	// 1. Spawn an undead threat (zombie) within 7 blocks -> armadillo rolls up into SCARED state.
	zombieID, err := srv.SpawnMob(3.5, 64.0, 0.5, MobTypeZombie)
	if err != nil {
		t.Fatalf("SpawnMob(zombie) failed: %v", err)
	}
	srv.TickArmadillos(rng)
	if !srv.IsArmadilloRolledUp(armID) {
		t.Fatal("expected armadillo to curl up when undead zombie is within 7 blocks")
	}
	st, _ := srv.GetArmadilloState(armID)
	if st.State != ArmadilloStateScared {
		t.Fatalf("armadillo state = %d, want %d (SCARED)", st.State, ArmadilloStateScared)
	}

	// 2. Verify rolled-up shell damage reduction: damage = (damage - 1.0) / 2.0.
	if got := ReduceArmadilloRolledDamage(7.0); math.Abs(float64(got-3.0)) > 1e-4 {
		t.Fatalf("ReduceArmadilloRolledDamage(7.0) = %v, want 3.0", got)
	}
	if got := ReduceArmadilloRolledDamage(1.0); got != 0 {
		t.Fatalf("ReduceArmadilloRolledDamage(1.0) = %v, want 0", got)
	}

	player := newSurvivalTestSession(srv, 1002, "ArmadilloTester")
	srv.SetPlayerTransform(player, 0.5, 64.0, 1.5, 0, 0, true)
	hotbarSlot := world.HotbarContainerOffset + int(player.Snapshot().SelectedSlot)
	// Iron sword deals 6.0 damage -> reduced to (6.0 - 1.0) / 2.0 = 2.5 when armadillo is rolled up!
	player.SetInventorySlot(hotbarSlot, world.NewNamedItemStack("minecraft:iron_sword", 1))
	dealt, killed := srv.AttackEntity(player, armID)
	if killed || math.Abs(float64(dealt-2.5)) > 1e-4 {
		t.Fatalf("AttackEntity on rolled armadillo with iron sword (6.0) dealt=%v killed=%v, want dealt=2.5", dealt, killed)
	}
	if gotHP := srv.MobHealth(armID); math.Abs(float64(gotHP-(DefaultArmadilloMaxHealth-2.5))) > 1e-4 {
		t.Fatalf("armadillo HP after rolled hit = %v, want %v", gotHP, DefaultArmadilloMaxHealth-2.5)
	}

	// Remove the zombie threat and tick until the armadillo unrolls back to IDLE.
	srv.entities.Remove(zombieID)
	for i := 0; i < ArmadilloScaredMinDurationTicks+ArmadilloUnrollingDurationTicks+5; i++ {
		srv.TickArmadillos(rng)
	}
	if srv.IsArmadilloRolledUp(armID) {
		t.Fatal("expected armadillo to unroll to IDLE after threat is gone")
	}

	// 3. Brushing armadillo with minecraft:brush drops armadillo_scute and wears 16 durability.
	player.SetInventorySlot(hotbarSlot, world.NewNamedItemStack(BrushItemID, 1))
	SetSlotItemMeta(player, hotbarSlot, ItemMeta{})
	handled, scuteEntID := srv.InteractWithArmadillo(player, armID)
	if !handled || scuteEntID == 0 {
		t.Fatalf("InteractWithArmadillo(brush) = (%v, %d), want (true, >0)", handled, scuteEntID)
	}
	scuteEnt := srv.entities.Get(scuteEntID)
	if scuteEnt == nil || !scuteEnt.IsItem() || scuteEnt.Item.ResolvedID() != ArmadilloScuteItemID {
		t.Fatalf("expected brushed item to be minecraft:armadillo_scute, got %+v", scuteEnt)
	}
	if gotBrushDmg := GetSlotItemMeta(player, hotbarSlot).Damage; gotBrushDmg != ArmadilloBrushWearCost {
		t.Fatalf("brush durability damage = %d, want %d", gotBrushDmg, ArmadilloBrushWearCost)
	}

	// 4. Periodic scute shedding when timer expires.
	srv.SetArmadilloScuteTimer(armID, 1)
	srv.TickArmadillos(rng)
	stAfterShed, _ := srv.GetArmadilloState(armID)
	if stAfterShed.ScuteShedTimer < ArmadilloScuteShedMinTicks {
		t.Fatalf("expected scute timer to reset to [%d..%d], got %d",
			ArmadilloScuteShedMinTicks, ArmadilloScuteShedMaxTicks, stAfterShed.ScuteShedTimer)
	}

	// 5. Breeding two uncurled armadillos with minecraft:spider_eye spawns a baby armadillo.
	arm2ID, err := srv.SpawnArmadillo(world.DimensionOverworld, 1.5, 64.0, 0.5, false)
	if err != nil {
		t.Fatalf("SpawnArmadillo #2 failed: %v", err)
	}
	player.SetInventorySlot(hotbarSlot, world.NewNamedItemStack(SpiderEyeItemID, 4))
	if ok1, _ := srv.InteractWithArmadillo(player, armID); !ok1 {
		t.Fatal("expected first armadillo spider_eye feed to succeed")
	}
	ok2, babyArmID := srv.InteractWithArmadillo(player, arm2ID)
	if !ok2 || babyArmID == 0 || !srv.IsMobBaby(babyArmID) {
		t.Fatalf("expected second armadillo spider_eye feed to spawn baby, got ok=%v babyID=%d", ok2, babyArmID)
	}
}

func TestCamelSittingDashTwoRidersCactusBreedingAndStepHeight(t *testing.T) {
	srv := newSurvivalTestServer(t)
	rng := rand.New(rand.NewSource(7))

	if gotID := ResolveEntityTypeID(MobTypeCamel); gotID != EntityTypeCamelID {
		t.Fatalf("ResolveEntityTypeID(camel) = %d, want %d", gotID, EntityTypeCamelID)
	}

	camelID, err := srv.SpawnCamel(world.DimensionOverworld, 0.5, 64.0, 0.5, false)
	if err != nil {
		t.Fatalf("SpawnCamel failed: %v", err)
	}
	if !srv.IsMobTamed(camelID) {
		t.Fatal("expected spawned Camel to be tame by default")
	}

	// 1. Sitting and standing state transitions + metadata packet round-trip.
	if !srv.SitDownCamel(camelID) || !srv.IsCamelSitting(camelID) {
		t.Fatal("expected SitDownCamel to put Camel in sitting pose")
	}
	cst, _ := srv.GetCamelState(camelID)
	pkt := EncodeCamelEntityDataPacket(camelID, true, cst.Sitting, cst.Dashing, cst.LastPoseChangeTick)
	decID, decSaddled, decSitting, decDashing, decTick, err := DecodeCamelEntityDataPacket(protocol.NewReader(pkt))
	if err != nil || decID != camelID || !decSaddled || !decSitting || decDashing || decTick >= 0 {
		t.Fatalf("DecodeCamelEntityDataPacket mismatch: id=%d saddled=%v sitting=%v dashing=%v tick=%d err=%v",
			decID, decSaddled, decSitting, decDashing, decTick, err)
	}
	// Taking damage causes a sitting Camel to stand up.
	srv.OnCamelDamaged(camelID)
	if srv.IsCamelSitting(camelID) {
		t.Fatal("expected sitting Camel to stand up when damaged")
	}

	// 2. Equip saddle and perform Camel Dash (55-tick cooldown).
	rider1 := newSurvivalTestSession(srv, 1003, "CamelDriver")
	srv.SetPlayerTransform(rider1, 0.5, 64.0, 1.0, 0, 0, true)
	hotbar1 := world.HotbarContainerOffset + int(rider1.Snapshot().SelectedSlot)
	rider1.SetInventorySlot(hotbar1, world.NewNamedItemStack(SaddleItemID, 1))
	if ok, _ := srv.InteractWithCamel(rider1, camelID); !ok || !srv.IsCamelSaddled(camelID) {
		t.Fatal("expected InteractWithCamel with saddle to equip saddle")
	}

	vx, vy, vz, dashed := srv.ExecuteCamelDash(camelID, 100)
	if !dashed || vy <= 0 || math.Hypot(vx, vz) < CamelDashMinHorizImpulse {
		t.Fatalf("ExecuteCamelDash(100) = (%v, %v, %v, %v), expected valid dash impulse", vx, vy, vz, dashed)
	}
	cst, _ = srv.GetCamelState(camelID)
	if cst.DashCooldownTicks != CamelDashCooldownTicks {
		t.Fatalf("DashCooldownTicks = %d, want %d", cst.DashCooldownTicks, CamelDashCooldownTicks)
	}
	// Cannot dash again while on 55-tick cooldown.
	if _, _, _, dashedAgain := srv.ExecuteCamelDash(camelID, 100); dashedAgain {
		t.Fatal("expected ExecuteCamelDash to fail while DashCooldownTicks > 0")
	}
	for i := 0; i < CamelDashCooldownTicks; i++ {
		srv.TickCamels(rng)
	}
	cst, _ = srv.GetCamelState(camelID)
	if cst.DashCooldownTicks != 0 {
		t.Fatalf("DashCooldownTicks after 55 ticks = %d, want 0", cst.DashCooldownTicks)
	}

	// 3. Two riders (Seat 0 driver + Seat 1 back passenger) and SetPassengers packet.
	rider1.SetInventorySlot(hotbar1, world.ItemStack{})
	seat0, ok0 := srv.MountCamel(rider1, camelID)
	if !ok0 || seat0 != 0 {
		t.Fatalf("first rider MountCamel = (%d, %v), want (0, true)", seat0, ok0)
	}
	rider2 := newSurvivalTestSession(srv, 1004, "CamelPassenger")
	srv.SetPlayerTransform(rider2, 0.5, 64.0, 1.0, 0, 0, true)
	seat1, ok1 := srv.MountCamel(rider2, camelID)
	if !ok1 || seat1 != 1 {
		t.Fatalf("second rider MountCamel = (%d, %v), want (1, true)", seat1, ok1)
	}
	rider3 := newSurvivalTestSession(srv, 1005, "ThirdRider")
	if _, ok3 := srv.MountCamel(rider3, camelID); ok3 {
		t.Fatal("expected 3rd rider to be rejected when Camel already has 2 riders")
	}
	// Dismounting Seat 0 promotes Seat 1 to driver (Seat 0).
	if !srv.DismountCamel(rider1, camelID) {
		t.Fatal("expected DismountCamel(rider1) to succeed")
	}
	if srv.CamelDriver(camelID) != rider2.EntityID {
		t.Fatalf("CamelDriver after rider1 dismount = %d, want %d", srv.CamelDriver(camelID), rider2.EntityID)
	}
	srv.DismountCamel(rider2, camelID)

	// 4. 1.5-block step height over fences and tall rider melee protection.
	if !CanCamelStepOverObstacle(1.5) || CanCamelStepOverObstacle(2.0) {
		t.Fatal("expected Camel to step over 1.5-block obstacles but not 2.0-block obstacles")
	}
	fenceSt, ok := world.BlockStateByName("minecraft:oak_fence", nil)
	if !ok {
		t.Fatal("expected minecraft:oak_fence state")
	}
	srv.chunks.SetBlock(5, 63, 5, world.StateStone)
	srv.chunks.SetBlock(6, 63, 5, world.StateStone)
	srv.chunks.SetBlock(6, 64, 5, fenceSt)
	srv.chunks.SetBlock(6, 65, 5, world.StateAir)
	srv.chunks.SetBlock(6, 66, 5, world.StateAir)
	if ny, stepOK := ResolveCamelStepMove(srv.chunks, 5, 64, 5, 6, 5); !stepOK || ny != 65 {
		t.Fatalf("ResolveCamelStepMove over oak_fence = (%d, %v), want (65, true)", ny, stepOK)
	}
	if CanGroundMobMeleeReachCamelRider(MobTypeZombie, 64.0, 64.0, false) {
		t.Fatal("expected standing Camel rider to be out of zombie ground melee reach")
	}

	// 5. Cactus healing and breeding.
	srv.SetMobHealth(camelID, DefaultCamelMaxHealth-2.0)
	rider1.SetInventorySlot(hotbar1, world.NewNamedItemStack(CactusItemID, 4))
	camel2ID, _ := srv.SpawnCamel(world.DimensionOverworld, 1.5, 64.0, 0.5, false)
	if ok, _ := srv.InteractWithCamel(rider1, camelID); !ok {
		t.Fatal("expected cactus interaction on camel #1 to heal and enter love mode")
	}
	if gotHP := srv.MobHealth(camelID); gotHP != DefaultCamelMaxHealth {
		t.Fatalf("camel #1 HP after cactus = %v, want %v", gotHP, DefaultCamelMaxHealth)
	}
	okBreed, babyCamelID := srv.InteractWithCamel(rider1, camel2ID)
	if !okBreed || babyCamelID == 0 || !srv.IsMobBaby(babyCamelID) {
		t.Fatalf("expected cactus on camel #2 to spawn baby camel, got ok=%v id=%d", okBreed, babyCamelID)
	}
}

func TestBreezeAIWindChargesBlockInteractionsAndProjectileDeflection(t *testing.T) {
	srv := newSurvivalTestServer(t)
	rng := rand.New(rand.NewSource(123))

	if gotID := ResolveEntityTypeID(MobTypeBreeze); gotID != EntityTypeBreezeID {
		t.Fatalf("ResolveEntityTypeID(breeze) = %d, want %d", gotID, EntityTypeBreezeID)
	}
	if gotID := ResolveEntityTypeID(WindChargeEntityType); gotID != EntityTypeWindChargeID {
		t.Fatalf("ResolveEntityTypeID(wind_charge) = %d, want %d", gotID, EntityTypeWindChargeID)
	}
	if gotID := ResolveEntityTypeID(BreezeWindChargeEntityType); gotID != EntityTypeBreezeWindChargeID {
		t.Fatalf("ResolveEntityTypeID(breeze_wind_charge) = %d, want %d", gotID, EntityTypeBreezeWindChargeID)
	}

	breezeID, err := srv.SpawnBreeze(world.DimensionOverworld, 0.5, 64.0, 0.5)
	if err != nil {
		t.Fatalf("SpawnBreeze failed: %v", err)
	}

	// 1. Breeze deflects arrows/tridents/snowballs, but NOT wind charges.
	if _, _, _, deflected := srv.TryDeflectProjectileByBreeze(breezeID, MobTypeArrow, 1.0, 0.0, 0.0); !deflected {
		t.Fatal("expected Breeze to deflect minecraft:arrow")
	}
	if _, _, _, deflected := srv.TryDeflectProjectileByBreeze(breezeID, "minecraft:trident", 0.0, -0.2, 1.0); !deflected {
		t.Fatal("expected Breeze to deflect minecraft:trident")
	}
	if _, _, _, deflected := srv.TryDeflectProjectileByBreeze(breezeID, WindChargeEntityType, 1.0, 0.0, 0.0); deflected {
		t.Fatal("expected Breeze NOT to deflect minecraft:wind_charge")
	}

	// 2. Breeze rod loot table drops 1..2 base rods (up to 8 with Looting III).
	rods := RollBreezeRodDrops(rng, 0)
	if rods.ResolvedID() != BreezeRodItemID || rods.Count < 1 || rods.Count > 2 {
		t.Fatalf("RollBreezeRodDrops(looting=0) = %+v, want 1..2 breeze_rod", rods)
	}

	// 3. Breeze AI shoots a breeze_wind_charge at a nearby survival player.
	player := newSurvivalTestSession(srv, 1006, "WindTester")
	srv.SetPlayerTransform(player, 6.5, 64.0, 0.5, 0, 0, true)
	for tick := 0; tick < 40; tick++ {
		srv.TickBreezes(rng)
	}
	bst, _ := srv.GetBreezeState(breezeID)
	if bst.LastShotChargeID == 0 {
		t.Fatal("expected Breeze AI to inhale and shoot a breeze_wind_charge at nearby player")
	}

	// 4. Player UseWindChargeItem with 10-tick cooldown + mid-air deflection + block interactions.
	hotbar := world.HotbarContainerOffset + int(player.Snapshot().SelectedSlot)
	player.SetInventorySlot(hotbar, world.NewNamedItemStack(WindChargeItemID, 3))
	chargeID, used := srv.UseWindChargeItem(player, 0)
	if !used || chargeID == 0 {
		t.Fatalf("UseWindChargeItem = (%d, %v), want (>0, true)", chargeID, used)
	}
	// Immediate second throw within 10 ticks is blocked by cooldown.
	if _, usedAgain := srv.UseWindChargeItem(player, 0); usedAgain {
		t.Fatal("expected second UseWindChargeItem on the same tick to be blocked by 10-tick cooldown")
	}

	// Place an oak_door (closed), iron_door (closed), oak_trapdoor (closed), lever (unpowered), and stone_button (unpowered) near (20, 64, 20).
	oakDoorClosed, _ := world.BlockStateByName("minecraft:oak_door", map[string]string{"facing": "north", "half": "lower", "hinge": "left", "open": "false", "powered": "false"})
	ironDoorClosed, _ := world.BlockStateByName("minecraft:iron_door", map[string]string{"facing": "north", "half": "lower", "hinge": "left", "open": "false", "powered": "false"})
	trapdoorClosed, _ := world.BlockStateByName("minecraft:oak_trapdoor", map[string]string{"facing": "north", "half": "bottom", "open": "false", "powered": "false", "waterlogged": "false"})
	leverOff, _ := world.BlockStateByName("minecraft:lever", map[string]string{"face": "floor", "facing": "north", "powered": "false"})
	buttonOff, _ := world.BlockStateByName("minecraft:stone_button", map[string]string{"face": "floor", "facing": "north", "powered": "false"})

	srv.chunks.SetBlock(20, 64, 20, oakDoorClosed)
	srv.chunks.SetBlock(21, 64, 20, ironDoorClosed)
	srv.chunks.SetBlock(20, 64, 21, trapdoorClosed)
	srv.chunks.SetBlock(21, 64, 21, leverOff)
	srv.chunks.SetBlock(19, 64, 20, buttonOff)

	var hookedBlocks []string
	srv.SetWindChargeRedstoneHook(func(dim string, x, y, z int, blockName string, oldState, newState uint16) {
		hookedBlocks = append(hookedBlocks, blockName)
	})

	srv.entities.Update(chargeID, func(e *world.Entity) {
		e.X, e.Y, e.Z = 20.5, 64.5, 20.5
	})
	_, toggled := srv.DetonateWindCharge(chargeID, 0)
	if len(toggled) < 4 || len(hookedBlocks) < 4 {
		t.Fatalf("expected wind charge burst to toggle oak_door, oak_trapdoor, lever, and stone_button; toggled=%v hooked=%v", toggled, hookedBlocks)
	}
	if _, props, ok := world.BlockStateProperties(srv.chunks.GetBlock(20, 64, 20)); !ok || props["open"] != "true" {
		t.Fatalf("expected oak_door to be opened by wind charge, got props=%v", props)
	}
	if _, props, ok := world.BlockStateProperties(srv.chunks.GetBlock(21, 64, 20)); !ok || props["open"] != "false" {
		t.Fatalf("expected iron_door NOT to be opened by wind charge, got props=%v", props)
	}
	if _, props, ok := world.BlockStateProperties(srv.chunks.GetBlock(20, 64, 21)); !ok || props["open"] != "true" {
		t.Fatalf("expected oak_trapdoor to be opened by wind charge, got props=%v", props)
	}
	if _, props, ok := world.BlockStateProperties(srv.chunks.GetBlock(21, 64, 21)); !ok || props["powered"] != "true" {
		t.Fatalf("expected lever to be flipped to powered=true by wind charge, got props=%v", props)
	}
	if _, props, ok := world.BlockStateProperties(srv.chunks.GetBlock(19, 64, 20)); !ok || props["powered"] != "true" {
		t.Fatalf("expected stone_button to be pressed (powered=true) by wind charge, got props=%v", props)
	}
}

func TestBoggedPoisonArrowsShearingMushroomsAndLoot(t *testing.T) {
	srv := newSurvivalTestServer(t)
	rng := rand.New(rand.NewSource(55))

	if gotID := ResolveEntityTypeID(MobTypeBogged); gotID != EntityTypeBoggedID {
		t.Fatalf("ResolveEntityTypeID(bogged) = %d, want %d", gotID, EntityTypeBoggedID)
	}
	if gotHP := ResolveMobMaxHealth(MobTypeBogged); gotHP != DefaultBoggedMaxHealth {
		t.Fatalf("ResolveMobMaxHealth(bogged) = %v, want %v (16.0)", gotHP, DefaultBoggedMaxHealth)
	}

	boggedID, err := srv.SpawnBogged(world.DimensionOverworld, 0.5, 64.0, 0.5)
	if err != nil {
		t.Fatalf("SpawnBogged failed: %v", err)
	}
	if srv.MobHealth(boggedID) != DefaultBoggedMaxHealth {
		t.Fatalf("Bogged initial health = %v, want %v", srv.MobHealth(boggedID), DefaultBoggedMaxHealth)
	}

	// 1. Bogged shoots poison arrows that inflict Poison I for 80 ticks (4.0s).
	target := newSurvivalTestSession(srv, 1007, "BoggedTarget")
	srv.SetPlayerTransform(target, 5.5, 64.0, 0.5, 0, 0, true)
	dealt := srv.ShootBoggedArrowAtPlayer(boggedID, 0.5, 64.0, 0.5, target)
	if dealt <= 0 {
		t.Fatalf("expected ShootBoggedArrowAtPlayer to deal damage, got %v", dealt)
	}
	poisonInst, hasPoison := target.PlayerEffect(EffectPoison)
	if !hasPoison || poisonInst.Duration != BoggedPoisonDurationTicks {
		t.Fatalf("expected target to receive Poison I for %d ticks, got has=%v inst=%+v",
			BoggedPoisonDurationTicks, hasPoison, poisonInst)
	}

	// 2. Shearing Bogged with shears sets DATA_SHEARED=true, drops 2 mushrooms, and wears 1 durability.
	srv.SetPlayerTransform(target, 0.5, 64.0, 1.5, 0, 0, true)
	hotbar := world.HotbarContainerOffset + int(target.Snapshot().SelectedSlot)
	target.SetInventorySlot(hotbar, world.NewNamedItemStack(ShearsItemID, 1))
	SetSlotItemMeta(target, hotbar, ItemMeta{})

	if srv.IsBoggedSheared(boggedID) {
		t.Fatal("expected Bogged to start unsheared")
	}
	dropped, sheared := srv.ShearBogged(target, boggedID, rng)
	if !sheared || !srv.IsBoggedSheared(boggedID) {
		t.Fatal("expected ShearBogged to succeed and mark Bogged as sheared")
	}
	var totalMushrooms int32
	for _, st := range dropped {
		id := st.ResolvedID()
		if id != RedMushroomItemID && id != BrownMushroomItemID {
			t.Fatalf("unexpected Bogged shear drop %q", id)
		}
		totalMushrooms += st.Count
	}
	if totalMushrooms != BoggedShearMushroomCount {
		t.Fatalf("total mushrooms dropped from Bogged shear = %d, want %d", totalMushrooms, BoggedShearMushroomCount)
	}
	if gotDmg := GetSlotItemMeta(target, hotbar).Damage; gotDmg != 1 {
		t.Fatalf("shears damage after shearing Bogged = %d, want 1", gotDmg)
	}
	// Second shear attempt on an already-sheared Bogged fails.
	if _, shearedAgain := srv.ShearBogged(target, boggedID, rng); shearedAgain {
		t.Fatal("expected second ShearBogged on already-sheared Bogged to return false")
	}

	// Verify Bogged sheared metadata packet round-trip.
	pkt := EncodeBoggedShearedDataPacket(boggedID, true)
	decID, decSheared, err := DecodeBoggedShearedDataPacket(protocol.NewReader(pkt))
	if err != nil || decID != boggedID || !decSheared {
		t.Fatalf("DecodeBoggedShearedDataPacket = (%d, %v, %v), want (%d, true, nil)", decID, decSheared, err, boggedID)
	}

	// 3. Death loot drops bones, arrows, and tipped_arrow.
	loot := RollBoggedDeathLoot(nil, true)
	if len(loot) < 3 {
		t.Fatalf("expected RollBoggedDeathLoot(killedByPlayer=true) to include bone, arrow, and tipped_arrow, got %+v", loot)
	}
}
