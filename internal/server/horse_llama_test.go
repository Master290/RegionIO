package server

import (
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

func newTestPlayServer(t *testing.T) (*Server, *PlayerSession) {
	t.Helper()
	srv := newSurvivalTestServer(t)
	col := &survivalPacketCollector{}
	player, err := srv.RegisterPlayer(Profile{Name: "Rider", UUID: OfflineUUID("Rider")}, col.send)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	srv.SetPlayerTransform(player, 1.5, 65.0, 1.5, 0, 0, true)
	_ = srv.SetPlayerGameMode(player, GameModeSurvival)
	return srv, player
}

func TestHorseMetadataAndAttributeInheritance(t *testing.T) {
	packed := PackHorseTypeVariant(HorseVariantBlack, HorseMarkingsWhiteField)
	coat, mark := UnpackHorseTypeVariant(packed)
	if coat != HorseVariantBlack || mark != HorseMarkingsWhiteField {
		t.Fatalf("UnpackHorseTypeVariant(%d) = (%d, %d), want (%d, %d)", packed, coat, mark, HorseVariantBlack, HorseMarkingsWhiteField)
	}

	pkt := EncodeHorseVariantDataPacket(42, true, true, false, HorseVariantChestnut, HorseMarkingsWhiteDots)
	id, flags, _, packedVar, _, _, err := DecodeEquineOrLlamaDataPacket(protocol.NewReader(pkt))
	if err != nil {
		t.Fatalf("DecodeEquineOrLlamaDataPacket failed: %v", err)
	}
	gotCoat, gotMark := UnpackHorseTypeVariant(packedVar)
	tamed := (flags & AbstractHorseFlagTamed) != 0
	saddled := (flags & AbstractHorseFlagSaddled) != 0
	standing := (flags & AbstractHorseFlagStanding) != 0
	if id != 42 || !tamed || !saddled || standing || gotCoat != HorseVariantChestnut || gotMark != HorseMarkingsWhiteDots {
		t.Fatalf("decoded horse metadata mismatch: id=%d tamed=%v saddled=%v standing=%v coat=%d mark=%d", id, tamed, saddled, standing, gotCoat, gotMark)
	}

	rng := rand.New(rand.NewSource(12345))
	for i := 0; i < 50; i++ {
		hp := RollEquineMaxHealth(rng)
		if hp < EquineMinMaxHealth || hp > EquineMaxMaxHealth {
			t.Fatalf("RollEquineMaxHealth out of range: %v", hp)
		}
		spd := RollHorseMovementSpeed(rng)
		if spd < HorseMinSpeed-1e-6 || spd > HorseMaxSpeed+1e-6 {
			t.Fatalf("RollHorseMovementSpeed out of range: %v", spd)
		}
		jmp := RollHorseJumpStrength(rng)
		if jmp < HorseMinJumpStrength-1e-6 || jmp > HorseMaxJumpStrength+1e-6 {
			t.Fatalf("RollHorseJumpStrength out of range: %v", jmp)
		}

		cHP, cSpd, cJmp := InheritEquineAttributes(20.0, 28.0, 0.20, 0.30, 0.5, 0.9, rng)
		if cHP < EquineMinMaxHealth || cHP > EquineMaxMaxHealth {
			t.Fatalf("InheritEquineAttributes HP out of bounds: %v", cHP)
		}
		if cSpd < HorseMinSpeed || cSpd > HorseMaxSpeed {
			t.Fatalf("InheritEquineAttributes Speed out of bounds: %v", cSpd)
		}
		if cJmp < HorseMinJumpStrength || cJmp > HorseMaxJumpStrength {
			t.Fatalf("InheritEquineAttributes Jump out of bounds: %v", cJmp)
		}
	}
}

func TestChestedHorseAndLlamaMetadataPackets(t *testing.T) {
	chPkt := EncodeChestedHorseDataPacket(77, true, false, false, true)
	id, flags, hasChest, _, _, _, err := DecodeEquineOrLlamaDataPacket(protocol.NewReader(chPkt))
	if err != nil {
		t.Fatalf("DecodeEquineOrLlamaDataPacket(chested) failed: %v", err)
	}
	tamed := (flags & AbstractHorseFlagTamed) != 0
	saddled := (flags & AbstractHorseFlagSaddled) != 0
	standing := (flags & AbstractHorseFlagStanding) != 0
	if id != 77 || !tamed || saddled || standing || !hasChest {
		t.Fatalf("decoded chested horse mismatch: id=%d tamed=%v saddled=%v standing=%v chest=%v", id, tamed, saddled, standing, hasChest)
	}

	llPkt := EncodeLlamaDataPacket(88, true, true, 4, LlamaVariantGray)
	lid, lflags, lchest, _, lstr, lvar, err := DecodeEquineOrLlamaDataPacket(protocol.NewReader(llPkt))
	if err != nil {
		t.Fatalf("DecodeEquineOrLlamaDataPacket(llama) failed: %v", err)
	}
	ltamed := (lflags & AbstractHorseFlagTamed) != 0
	lstand := (lflags & AbstractHorseFlagStanding) != 0
	if lid != 88 || !ltamed || lstand || !lchest || lstr != 4 || lvar != LlamaVariantGray {
		t.Fatalf("decoded llama metadata mismatch: id=%d tamed=%v stand=%v chest=%v str=%d var=%d", lid, ltamed, lstand, lchest, lstr, lvar)
	}
}

func TestHorseTamingSaddleArmorJumpAndBreedingToMule(t *testing.T) {
	s, sess := newTestPlayServer(t)
	rng := rand.New(rand.NewSource(99))

	horseID, err := s.SpawnMob(0.5, 65, 0.5, MobTypeHorse)
	if err != nil {
		t.Fatalf("SpawnMob(horse) failed: %v", err)
	}
	donkeyID, err := s.SpawnMob(1.5, 65, 0.5, MobTypeDonkey)
	if err != nil {
		t.Fatalf("SpawnMob(donkey) failed: %v", err)
	}

	// Feed sugar to wild horse to increase temper, then tame both.
	slot := world.HotbarContainerOffset + int(sess.SelectedSlot())
	sess.SetInventorySlot(slot, world.NewNamedItemStack("minecraft:sugar", 5))
	handled, _ := s.InteractWithTameableMob(sess, horseID, rng)
	if !handled {
		t.Fatalf("expected feeding sugar to wild horse to be handled")
	}
	st, ok := s.GetTamedMobState(horseID)
	if !ok || st.Temper != 3 {
		t.Fatalf("expected horse temper 3 after sugar, got %+v", st)
	}

	s.TameMob(horseID, sess)
	s.TameMob(donkeyID, sess)

	// Equip saddle and diamond horse armor on the tamed horse.
	sess.SetInventorySlot(slot, world.NewNamedItemStack("minecraft:saddle", 1))
	if h, _ := s.InteractWithTameableMob(sess, horseID, rng); !h || !s.IsEquineSaddled(horseID) {
		t.Fatalf("expected horse to be saddled")
	}
	sess.SetInventorySlot(slot, world.NewNamedItemStack("minecraft:diamond_horse_armor", 1))
	if h, _ := s.InteractWithTameableMob(sess, horseID, rng); !h || s.HorseArmorItem(horseID) != DiamondHorseArmorItemID {
		t.Fatalf("expected diamond_horse_armor equipped on horse, got %q", s.HorseArmorItem(horseID))
	}

	// Charge jump impulse & fall damage reduction.
	_, vy, _, ok := s.ExecuteEquineJump(horseID, 100)
	if !ok || vy <= 0.4 {
		t.Fatalf("expected positive charge jump velocity on saddled tamed horse, got vy=%v ok=%v", vy, ok)
	}
	if dmg := ComputeEquineFallDamage(4.0, 0.7); dmg != 0 {
		t.Fatalf("expected 0 fall damage for 4-block fall on horse, got %v", dmg)
	}
	if dmg := ComputeEquineFallDamage(10.0, 0.7); dmg <= 0 {
		t.Fatalf("expected positive fall damage for 10-block fall on horse, got %v", dmg)
	}

	// Breed tamed horse + tamed donkey with golden_carrot -> baby mule with inherited attributes.
	sess.SetInventorySlot(slot, world.NewNamedItemStack("minecraft:golden_carrot", 4))
	_, _ = s.InteractWithTameableMob(sess, horseID, rng)
	_, muleID := s.InteractWithTameableMob(sess, donkeyID, rng)
	if muleID <= 0 {
		t.Fatalf("expected horse + donkey breeding to spawn baby mule")
	}
	muleEnt := s.entities.Get(muleID)
	if muleEnt == nil || muleEnt.TypeName != MobTypeMule {
		t.Fatalf("expected offspring to be minecraft:mule, got %+v", muleEnt)
	}
	if !s.IsMobBaby(muleID) {
		t.Fatalf("expected newborn mule to be a baby")
	}
	muleState, ok := s.GetEquineState(muleID)
	if !ok || muleState.MaxHealth < EquineMinMaxHealth || muleState.MovementSpeed < HorseMinSpeed || muleState.JumpStrength < HorseMinJumpStrength {
		t.Fatalf("expected mule to inherit valid equine attributes, got %+v", muleState)
	}
}

func TestDonkeyAndLlamaChestsCarpetsCaravansAndSpit(t *testing.T) {
	s, sess := newTestPlayServer(t)
	rng := rand.New(rand.NewSource(7))
	slot := world.HotbarContainerOffset + int(sess.SelectedSlot())

	donkeyID, _ := s.SpawnMob(0.5, 65, 0.5, MobTypeDonkey)
	s.TameMob(donkeyID, sess)
	sess.SetInventorySlot(slot, world.NewNamedItemStack("minecraft:chest", 1))
	if h, _ := s.InteractWithTameableMob(sess, donkeyID, rng); !h || !s.HasEquineChest(donkeyID) {
		t.Fatalf("expected donkey chest to be attached")
	}
	if s.EquineChestSlotCountFor(donkeyID) != DonkeyMuleChestSlots {
		t.Fatalf("donkey chest slots = %d, want %d", s.EquineChestSlotCountFor(donkeyID), DonkeyMuleChestSlots)
	}
	apple := world.NewNamedItemStack("minecraft:apple", 5)
	if !s.SetEquineInventorySlot(donkeyID, 0, apple) {
		t.Fatalf("SetEquineInventorySlot failed")
	}
	if got := s.EquineInventorySlot(donkeyID, 0); got.Count != 5 {
		t.Fatalf("EquineInventorySlot(0) = %+v, want 5 apples", got)
	}

	// Llama strength, carpet, chest, shears removal, and caravan up to 10 llamas.
	llamaHead, _ := s.SpawnMob(1.5, 65, 0.5, MobTypeLlama)
	s.TameMob(llamaHead, sess)
	s.SetLlamaStrengthAndVariant(llamaHead, 4, LlamaVariantCreamy)
	sess.SetInventorySlot(slot, world.NewNamedItemStack("minecraft:chest", 1))
	_, _ = s.InteractWithTameableMob(sess, llamaHead, rng)
	if s.EquineChestSlotCountFor(llamaHead) != 12 {
		t.Fatalf("llama chest slots for strength 4 = %d, want 12", s.EquineChestSlotCountFor(llamaHead))
	}

	sess.SetInventorySlot(slot, world.NewNamedItemStack("minecraft:blue_carpet", 1))
	if h, _ := s.InteractWithTameableMob(sess, llamaHead, rng); !h || s.LlamaCarpetColor(llamaHead) != 11 {
		t.Fatalf("expected blue_carpet (color 11) on llama, got %d (%q)", s.LlamaCarpetColor(llamaHead), s.LlamaCarpetItem(llamaHead))
	}
	sess.SetInventorySlot(slot, world.NewNamedItemStack("minecraft:shears", 1))
	if h, _ := s.InteractWithTameableMob(sess, llamaHead, rng); !h || s.LlamaCarpetColor(llamaHead) != -1 {
		t.Fatalf("expected shears to remove llama carpet")
	}

	// Caravan linking up to MaxLlamaCaravanLength (10).
	prevID := llamaHead
	for i := 1; i < MaxLlamaCaravanLength; i++ {
		lid, _ := s.SpawnMob(float64(i)+1.5, 65, 0.5, MobTypeLlama)
		if !s.LinkLlamaCaravan(lid, prevID) {
			t.Fatalf("LinkLlamaCaravan failed at index %d", i)
		}
		prevID = lid
	}
	extraLlama, _ := s.SpawnMob(15.5, 65, 0.5, MobTypeLlama)
	if s.LinkLlamaCaravan(extraLlama, prevID) {
		t.Fatalf("expected caravan link beyond MaxLlamaCaravanLength (10) to be rejected")
	}
	chain := s.LlamaCaravanChain(llamaHead)
	if len(chain) != MaxLlamaCaravanLength {
		t.Fatalf("LlamaCaravanChain length = %d, want %d", len(chain), MaxLlamaCaravanLength)
	}

	// Llama spit when damaged + decorated pot shatter on projectile impact.
	potState, ok := world.BlockStateByName("minecraft:decorated_pot", nil)
	if ok {
		s.chunks.SetBlock(3, 70, 5, potState)
		spitID, err := s.SpawnLlamaSpit(world.DimensionOverworld, llamaHead, 2.2, 70.5, 5.5, 1.0, 0.0, 0.0)
		if err != nil || spitID <= 0 {
			t.Fatalf("SpawnLlamaSpit failed: %v", err)
		}
		s.TickLlamaSpits()
		if IsDecoratedPotBlock(s.chunks.GetBlock(3, 70, 5)) {
			t.Fatalf("expected llama_spit projectile to shatter decorated_pot at (3,70,5)")
		}
	}
}

func TestWanderingTraderWithTraderLlamasAndSkeletonTrap(t *testing.T) {
	s, sess := newTestPlayServer(t)
	rng := rand.New(rand.NewSource(42))

	traderID, llamaIDs, err := s.SpawnWanderingTrader(world.DimensionOverworld, 1.5, 65, 1.5, true, rng)
	if err != nil || traderID <= 0 || len(llamaIDs) != 2 {
		t.Fatalf("SpawnWanderingTrader failed: trader=%d llamas=%v err=%v", traderID, llamaIDs, err)
	}

	// Right-click wandering trader opens merchant UI with 6 trades (5 ordinary + 1 special).
	opened, cid, err := s.InteractWithEntity(sess, traderID)
	if err != nil || !opened || cid <= 0 {
		t.Fatalf("InteractWithEntity(wandering_trader) failed: opened=%v cid=%d err=%v", opened, cid, err)
	}
	vs, ok := s.VillagerState(traderID)
	if !ok || len(vs.Offers) != WanderingTraderOrdinaryOfferCount+WanderingTraderSpecialOfferCount {
		t.Fatalf("expected %d wandering trader offers, got %d", WanderingTraderOrdinaryOfferCount+WanderingTraderSpecialOfferCount, len(vs.Offers))
	}
	_ = s.CloseMerchantContainer(sess, cid, false)

	// Attacking the wandering trader causes its trader llamas to spit at the attacker.
	wolfID, _ := s.SpawnMob(2.5, 65, 1.5, MobTypeWolf)
	spitCount := s.OnWanderingTraderDamaged(traderID, nil, wolfID)
	if spitCount != 2 {
		t.Fatalf("expected 2 trader llamas to spit at attacker, got %d", spitCount)
	}

	// Skeleton trap horse triggered when a player is within 10 blocks -> 4 tamed skeleton horsemen.
	trapHorseID, err := s.SpawnSkeletonTrapHorse(world.DimensionOverworld, 2.5, 65, 0.5)
	if err != nil || !s.IsSkeletonTrapHorse(trapHorseID) {
		t.Fatalf("SpawnSkeletonTrapHorse failed: id=%d err=%v", trapHorseID, err)
	}
	horseIDs, skelIDs, triggered := s.TriggerSkeletonTrap(trapHorseID)
	if !triggered || len(horseIDs) != SkeletonTrapHorsemanCount || len(skelIDs) != SkeletonTrapHorsemanCount {
		t.Fatalf("TriggerSkeletonTrap spawned %d horses and %d skeletons, want %d", len(horseIDs), len(skelIDs), SkeletonTrapHorsemanCount)
	}
	for i := range horseIDs {
		if !s.IsMobTamed(horseIDs[i]) || s.IsSkeletonTrapHorse(horseIDs[i]) {
			t.Fatalf("expected skeleton trap horse %d to be tamed and untriggered", horseIDs[i])
		}
		if s.EquineRider(horseIDs[i]) != skelIDs[i] {
			t.Fatalf("expected skeleton rider %d mounted on skeleton horse %d, got %d", skelIDs[i], horseIDs[i], s.EquineRider(horseIDs[i]))
		}
	}

	// Vehicle hooks (IsVehicleEntity, MountEntityInVehicle, DismountEntityFromVehicle, EntityMountedVehicle).
	boatID, boatEnt := s.SpawnVehicle(world.DimensionOverworld, "minecraft:oak_boat", 4.5, 65, 4.5, 0)
	if boatEnt == nil || !s.IsVehicleEntity(boatID) {
		t.Fatalf("SpawnVehicle / IsVehicleEntity failed: id=%d", boatID)
	}
	if !s.MountEntityInVehicle(wolfID, boatID) || s.EntityMountedVehicle(wolfID) != boatID {
		t.Fatalf("expected wolf %d to be mounted in boat %d", wolfID, boatID)
	}
	if !s.DismountEntityFromVehicle(wolfID) || s.EntityMountedVehicle(wolfID) != 0 {
		t.Fatalf("expected wolf %d to be dismounted from boat %d", wolfID, boatID)
	}

	// Persistence round-trip.
	s.cfg.WorldDir = filepath.Join(t.TempDir(), "world")
	_ = os.MkdirAll(s.cfg.WorldDir, 0o755)
	if err := s.SaveEquinesAndLlamasFile(); err != nil {
		t.Fatalf("SaveEquinesAndLlamasFile failed: %v", err)
	}
	if err := s.LoadEquinesAndLlamasFile(); err != nil {
		t.Fatalf("LoadEquinesAndLlamasFile failed: %v", err)
	}
}

func TestRedstoneHooksProjectileTargetLightningRodAndMobTripwire(t *testing.T) {
	s, _ := newTestPlayServer(t)
	dim := world.DimensionOverworld

	// 1. Projectile hitting minecraft:target powers the target block (1..15).
	targetSt, ok := world.BlockStateByName("minecraft:target", map[string]string{"power": "0"})
	if !ok {
		t.Fatalf("missing minecraft:target block state")
	}
	s.chunks.SetBlock(10, 70, 10, targetSt)
	wcID, err := s.SpawnWindCharge(dim, 9.2, 70.5, 10.5, 1.0, 0.0, 0.0, 0, false)
	if err != nil || wcID <= 0 {
		t.Fatalf("SpawnWindCharge failed: %v", err)
	}
	s.TickWindCharges()
	props := world.BlockProperties(s.chunks.GetBlock(10, 70, 10))
	if props["power"] == "0" || props["power"] == "" {
		t.Fatalf("expected wind_charge hitting minecraft:target to set power > 0, got %q", props["power"])
	}

	// 2. Lightning strike redirects to minecraft:lightning_rod within 128 blocks.
	rodSt, ok := world.BlockStateByName("minecraft:lightning_rod", map[string]string{
		"facing":      "up",
		"powered":     "false",
		"waterlogged": "false",
	})
	if !ok {
		t.Fatalf("missing minecraft:lightning_rod block state")
	}
	s.chunks.SetBlock(20, 65, 20, rodSt)
	boltID, sx, sy, sz, redirected := s.SpawnLightningBolt(dim, 30.5, 65.0, 30.5, nil)
	if boltID <= 0 || !redirected || sx != 20.5 || sy != 65.0 || sz != 20.5 {
		t.Fatalf("SpawnLightningBolt = (id=%d, %.1f, %.1f, %.1f, redir=%v), want redirected to (20.5, 65.0, 20.5)", boltID, sx, sy, sz, redirected)
	}
	if world.BlockProperties(s.chunks.GetBlock(20, 65, 20))["powered"] != "true" {
		t.Fatalf("expected lightning_rod at (20,65,20) to be powered=true after lightning strike")
	}

	// 3. Mob stepping into minecraft:tripwire activates the tripwire.
	wireSt, ok := world.BlockStateByName("minecraft:tripwire", map[string]string{
		"attached": "false",
		"disarmed": "false",
		"east":     "false",
		"north":    "false",
		"powered":  "false",
		"south":    "false",
		"west":     "false",
	})
	if !ok {
		t.Fatalf("missing minecraft:tripwire block state")
	}
	s.chunks.SetBlock(15, 65, 15, wireSt)
	_, err = s.SpawnMob(15.5, 65.0, 15.5, MobTypePig)
	if err != nil {
		t.Fatalf("SpawnMob(pig) failed: %v", err)
	}
	s.TickMobsInCauldrons()
	if world.BlockProperties(s.chunks.GetBlock(15, 65, 15))["powered"] != "true" {
		t.Fatalf("expected mob inside minecraft:tripwire to set powered=true")
	}

	// 4. Non-fire-immune mob inside lit campfire / soul_campfire takes 1.0 / 2.0 contact damage.
	campSt, ok := world.BlockStateByName("minecraft:soul_campfire", map[string]string{
		"facing":      "north",
		"lit":         "true",
		"signal_fire": "false",
		"waterlogged": "false",
	})
	if !ok {
		t.Fatalf("missing minecraft:soul_campfire block state")
	}
	s.chunks.SetBlock(18, 65, 18, campSt)
	cowID, err := s.SpawnMob(18.5, 65.0, 18.5, MobTypeCow)
	if err != nil {
		t.Fatalf("SpawnMob(cow) failed: %v", err)
	}
	beforeHP := s.MobHealth(cowID)
	s.TickMobsInCauldrons()
	if got := s.MobHealth(cowID); got != beforeHP-2.0 {
		t.Fatalf("expected cow on lit soul_campfire to take 2.0 damage: before=%v got=%v", beforeHP, got)
	}
}

func TestCopperGolemAndLightningHooks(t *testing.T) {
	s := newSurvivalTestServer(t)

	// 1. Lightning bolt redirects to oxidized lightning rod within 128 blocks and cleans oxidation + powers rod.
	rodSt, ok := world.BlockStateByName("minecraft:oxidized_lightning_rod", map[string]string{
		"facing":      "up",
		"powered":     "false",
		"waterlogged": "false",
	})
	if !ok {
		t.Fatalf("missing minecraft:oxidized_lightning_rod")
	}
	s.chunks.SetBlock(12, 66, 12, rodSt)
	boltID, _, _, _, _ := s.SpawnLightningBolt(world.DimensionOverworld, 10.5, 65.0, 10.5, rand.New(rand.NewSource(1)))
	if boltID == 0 {
		t.Fatalf("expected non-zero lightning bolt ID")
	}
	afterRod := s.chunks.GetBlock(12, 66, 12)
	if got := world.BlockName(afterRod); got != "minecraft:lightning_rod" {
		t.Fatalf("expected lightning strike on rod to clean oxidation to minecraft:lightning_rod, got %q", got)
	}
	if world.BlockProperties(afterRod)["powered"] != "true" {
		t.Fatalf("expected lightning rod to be powered=true after strike")
	}

	// 2. Copper golem spawns, oxidizes into statue when Oxidized & unwaxed, and revives when scraped with axe on unaffected statue.
	golemID, err := s.SpawnCopperGolem(world.DimensionOverworld, 14.5, 65.0, 14.5, "south")
	if err != nil {
		t.Fatalf("SpawnCopperGolem failed: %v", err)
	}
	if _, ok := s.GetCopperGolemState(golemID); !ok {
		t.Fatalf("missing CopperGolemState for %d", golemID)
	}
	s.SetCopperGolemState(golemID, world.CopperStageOxidized, false, "sitting")

	statueBlock, ok := s.PetrifyCopperGolemIntoStatue(golemID, nil)
	if !ok {
		t.Fatalf("expected PetrifyCopperGolemIntoStatue to succeed")
	}
	if got := world.BlockName(statueBlock); got != "minecraft:oxidized_copper_golem_statue" {
		t.Fatalf("expected minecraft:oxidized_copper_golem_statue, got %q", got)
	}
	if _, ok := s.GetCopperGolemState(golemID); ok {
		t.Fatalf("expected copper golem entity %d to be despawned after turning into statue", golemID)
	}

	// Scrape statue down to unaffected, then scrape once more to revive copper golem via hook.
	unaffectedStatue, ok := world.BlockStateByName("minecraft:copper_golem_statue", map[string]string{
		"copper_golem_pose": "sitting",
		"facing":            "south",
		"waterlogged":       "false",
	})
	if !ok {
		t.Fatalf("missing minecraft:copper_golem_statue")
	}
	s.chunks.SetBlock(14, 65, 14, unaffectedStatue)
	axeStack := world.ItemStack{ID: "minecraft:iron_axe", Count: 1}
	_, _, act, revived := s.UseAxeOnCopperBlock(world.DimensionOverworld, 14, 65, 14, axeStack, ItemMeta{}, true)
	if !revived || act != world.CopperAxeActionScrape {
		t.Fatalf("expected UseAxeOnCopperBlock on unaffected copper_golem_statue to revive golem: revived=%v act=%v", revived, act)
	}
	if got := s.chunks.GetBlock(14, 65, 14); got != world.StateAir {
		t.Fatalf("expected statue block to break on revive, got %d (%s)", got, world.BlockName(got))
	}
}
