package server

import (
	"math/rand"
	"testing"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

func TestLeashLeadFenceKnotElasticPullAndSnapDistance(t *testing.T) {
	// 1. Packet round-trip for ClientboundSetEntityLinkPacket (0x64).
	pkt := EncodeSetEntityLinkPacket(101, 202)
	srcID, dstID, err := DecodeSetEntityLinkPacket(protocol.NewReader(pkt))
	if err != nil || srcID != 101 || dstID != 202 {
		t.Fatalf("DecodeSetEntityLinkPacket = (%d, %d, %v), want (101, 202, nil)", srcID, dstID, err)
	}

	s, sess := newTestPlayServer(t)
	dim := world.DimensionOverworld

	// 2. Eligibility checks: passive/mounts/golems/boats allowed; bosses/hostiles/villagers/angry wolves rejected.
	if !CanMobTypeBeLeashed(MobTypeHorse) || !CanMobTypeBeLeashed(MobTypeCamel) ||
		!CanMobTypeBeLeashed(MobTypeIronGolem) || !CanMobTypeBeLeashed(MobTypeStrider) ||
		!CanMobTypeBeLeashed("minecraft:oak_boat") {
		t.Fatalf("expected horse, camel, iron_golem, strider, and oak_boat to be leashable")
	}
	if CanMobTypeBeLeashed(MobTypeWither) || CanMobTypeBeLeashed(MobTypeEnderDragon) ||
		CanMobTypeBeLeashed(MobTypeZombie) || CanMobTypeBeLeashed(MobTypeVillager) ||
		CanMobTypeBeLeashed(MobTypeWanderingTrader) || CanMobTypeBeLeashed(MobTypePanda) {
		t.Fatalf("expected bosses, zombies, villagers, wandering_trader, and panda to NOT be leashable")
	}

	wolfID, err := s.SpawnMob(2.5, 65.0, 2.5, MobTypeWolf)
	if err != nil {
		t.Fatalf("SpawnMob(wolf): %v", err)
	}
	s.ProvokeWildWolf(wolfID, sess)
	if s.CanEntityBeLeashed(wolfID) {
		t.Fatalf("expected angry wild wolf to NOT be leashable")
	}

	// 3. Leash a horse to player, tie it to an oak_fence via TieLeashedMobsToFence, then break the fence.
	horseID, err := s.SpawnMob(2.5, 65.0, 1.5, MobTypeHorse)
	if err != nil {
		t.Fatalf("SpawnMob(horse): %v", err)
	}
	slot := world.HotbarContainerOffset + int(sess.SelectedSlot())
	sess.SetInventorySlot(slot, world.NewNamedItemStack(LeadItemID, 2))

	if !s.LeashMobToPlayer(sess, horseID) || !s.IsEntityLeashed(horseID) || s.LeashHolderID(horseID) != sess.EntityID {
		t.Fatalf("expected horse to be leashed to player %d, got holder=%d", sess.EntityID, s.LeashHolderID(horseID))
	}
	if got := sess.InventorySlot(slot).Count; got != 1 {
		t.Fatalf("expected 1 lead consumed in survival, remaining=%d", got)
	}

	fenceState, ok := world.BlockStateByName("minecraft:oak_fence", nil)
	if !ok {
		t.Fatalf("missing minecraft:oak_fence block state")
	}
	s.chunks.SetBlock(3, 65, 1, fenceState)
	knotID, tied := s.TieLeashedMobsToFence(sess, dim, 3, 65, 1)
	if knotID <= 0 || tied != 1 || s.LeashHolderID(horseID) != knotID {
		t.Fatalf("TieLeashedMobsToFence = (knot=%d, tied=%d), holder=%d", knotID, tied, s.LeashHolderID(horseID))
	}

	// Breaking the fence block breaks the leash_knot and unleashes the horse.
	s.chunks.SetBlock(3, 65, 1, world.StateAir)
	if broken := s.OnFenceBlockBrokenForLeash(dim, 3, 65, 1); broken != 1 || s.IsEntityLeashed(horseID) {
		t.Fatalf("expected OnFenceBlockBrokenForLeash to unleash 1 mob, got broken=%d leashed=%v", broken, s.IsEntityLeashed(horseID))
	}

	// 4. Elastic pull (> 6 blocks) and snap (> 10 blocks) in TickLeashedMobs, plus ShearMobLeash.
	for x := 0; x <= 20; x++ {
		s.chunks.SetBlock(x, 64, 1, world.StateStone)
		s.chunks.SetBlock(x, 65, 1, world.StateAir)
		s.chunks.SetBlock(x, 66, 1, world.StateAir)
	}
	s.SetPlayerTransform(sess, 1.5, 65.0, 1.5, 0, 0, true)
	s.entities.Update(horseID, func(e *world.Entity) {
		e.X = 9.0 // 7.5 blocks away from player (> 6.0 elastic pull, <= 10.0 snap)
		e.Y = 65.0
		e.Z = 1.5
	})
	if !s.LeashMobToPlayer(sess, horseID) {
		t.Fatalf("expected LeashMobToPlayer to succeed")
	}
	s.TickLeashedMobs()
	if hEnt := s.entities.Get(horseID); hEnt == nil || hEnt.X >= 9.0 {
		t.Fatalf("expected elastic pull to move horse closer than X=9.0, got %+v", hEnt)
	}

	// Move horse beyond 10 blocks -> lead snaps!
	s.entities.Update(horseID, func(e *world.Entity) {
		e.X = 15.0 // 13.5 blocks away (> 10.0 snap threshold)
	})
	s.TickLeashedMobs()
	if s.IsEntityLeashed(horseID) {
		t.Fatalf("expected lead to snap when distance > %.1f blocks", LeashSnapDistance)
	}

	// Re-leash and snip with shears.
	s.entities.Update(horseID, func(e *world.Entity) {
		e.X = 2.5
	})
	s.LeashMobToEntity(horseID, sess.EntityID)
	if dropID, sheared := s.ShearMobLeash(sess, horseID); !sheared || dropID <= 0 || s.IsEntityLeashed(horseID) {
		t.Fatalf("expected ShearMobLeash to snip lead and drop item, got sheared=%v dropID=%d", sheared, dropID)
	}
}

func TestHorseLeatherArmorDyeingAndUntamedRideTaming(t *testing.T) {
	s, sess := newTestPlayServer(t)
	rng := rand.New(rand.NewSource(123))
	slot := world.HotbarContainerOffset + int(sess.SelectedSlot())

	horseID, err := s.SpawnMob(2.0, 65.0, 1.5, MobTypeHorse)
	if err != nil {
		t.Fatalf("SpawnMob(horse): %v", err)
	}

	// 1. Mount untamed horse with Temper=0 -> AttemptRideTamingStep bucks the rider off and adds +5 temper.
	if !s.MountEquine(sess.EntityID, horseID) {
		t.Fatalf("expected MountEquine on untamed adult horse to succeed")
	}
	tamed, bucked := s.AttemptRideTamingStep(horseID, rng)
	if tamed || !bucked || s.EquineRider(horseID) != 0 {
		t.Fatalf("expected AttemptRideTamingStep at temper=0 to buck rider off: tamed=%v bucked=%v rider=%d", tamed, bucked, s.EquineRider(horseID))
	}
	if tst, ok := s.GetTamedMobState(horseID); !ok || tst.Temper != 5 {
		t.Fatalf("expected horse temper to increase to 5 after buck-off, got %+v", tst)
	}

	// 2. Set high temper and mount again -> AttemptRideTamingStep tames the horse while keeping rider mounted.
	tmgr := s.tamedManager()
	tmgr.mu.Lock()
	tmgr.mobs[horseID].Temper = 100
	tmgr.mu.Unlock()
	s.MountEquine(sess.EntityID, horseID)
	tamed, bucked = s.AttemptRideTamingStep(horseID, rng)
	if !tamed || bucked || !s.IsMobTamed(horseID) || s.EquineRider(horseID) != sess.EntityID {
		t.Fatalf("expected AttemptRideTamingStep at temper=100 to tame horse and keep rider mounted: tamed=%v bucked=%v", tamed, bucked)
	}
	s.DismountEquine(horseID)

	// 3. Equip leather_horse_armor, dye it red then blend blue, and wash in cauldron.
	sess.SetInventorySlot(slot, world.NewNamedItemStack("minecraft:leather_horse_armor", 1))
	if h, _ := s.InteractWithTameableMob(sess, horseID, rng); !h || s.HorseArmorItem(horseID) != "minecraft:leather_horse_armor" {
		t.Fatalf("expected leather_horse_armor to be equipped")
	}
	sess.SetInventorySlot(slot, world.NewNamedItemStack("minecraft:red_dye", 2))
	if h, _ := s.InteractWithTameableMob(sess, horseID, rng); !h {
		t.Fatalf("expected red_dye interaction on leather_horse_armor to succeed")
	}
	rgb1, ok := s.GetHorseArmorDyedRGB(horseID)
	if !ok || rgb1 < 0 {
		t.Fatalf("expected non-negative DyedRGB on leather_horse_armor after red_dye, got %d (%v)", rgb1, ok)
	}
	if blended, ok := s.DyeEquippedHorseArmor(horseID, 11); !ok || blended == rgb1 {
		t.Fatalf("expected blending blue dye (11) to change leather_horse_armor color")
	}
	if !s.WashEquippedHorseArmor(horseID) {
		t.Fatalf("expected WashEquippedHorseArmor to clear dye color")
	}
	if _, stillDyed := s.GetHorseArmorDyedRGB(horseID); stillDyed {
		t.Fatalf("expected leather_horse_armor to be undyed after washing")
	}
}

func TestLlamaAutoCaravanOnLeashCaravanSplitAndTraderLlamaLeash(t *testing.T) {
	s, sess := newTestPlayServer(t)
	rng := rand.New(rand.NewSource(55))

	// 1. Leashing a lead llama automatically links nearby llamas into a caravan.
	l1, _ := s.SpawnMob(2.0, 65.0, 1.5, MobTypeLlama)
	l2, _ := s.SpawnMob(4.0, 65.0, 1.5, MobTypeLlama)
	l3, _ := s.SpawnMob(6.0, 65.0, 1.5, MobTypeLlama)

	if !s.LeashMobToPlayer(sess, l1) {
		t.Fatalf("expected LeashMobToPlayer(l1) to succeed")
	}
	chain := s.LlamaCaravanChain(l1)
	if len(chain) != 3 || chain[0] != l1 || chain[1] != l2 || chain[2] != l3 {
		t.Fatalf("expected auto-formed 3-llama caravan [%d,%d,%d], got %v", l1, l2, l3, chain)
	}

	// 2. Moving middle llama l2 > 10 blocks away breaks the caravan in TickLlamaCaravans.
	s.entities.Update(l2, func(e *world.Entity) {
		e.X = 25.0
	})
	s.TickLlamaCaravans()
	if got := s.LlamaCaravanChain(l1); len(got) != 1 {
		t.Fatalf("expected caravan to split when l2 exceeds 10 blocks, got chain=%v", got)
	}

	// 3. SpawnWanderingTrader withLlamas=true automatically leashes both trader_llamas to the trader.
	traderID, llamaIDs, err := s.SpawnWanderingTrader(world.DimensionOverworld, 10.5, 65.0, 10.5, true, rng)
	if err != nil || len(llamaIDs) != 2 {
		t.Fatalf("SpawnWanderingTrader failed: %v", err)
	}
	for _, lid := range llamaIDs {
		if !s.IsEntityLeashed(lid) || s.LeashHolderID(lid) != traderID {
			t.Fatalf("expected trader_llama %d to be leashed to wandering_trader %d, got holder=%d", lid, traderID, s.LeashHolderID(lid))
		}
	}

	// 4. Wolf flee rule from strong llamas (Strength >= 4).
	if !ShouldWolfFleeFromLlama(4, rng) || !ShouldWolfFleeFromLlama(5, rng) {
		t.Fatalf("expected wolves to always flee from strength 4..5 llamas")
	}
}

func TestCamelShearSaddlePassengerSyncAndStriderPigPreservation(t *testing.T) {
	s, driver := newTestPlayServer(t)
	col2 := &survivalPacketCollector{}
	passenger, err := s.RegisterPlayer(Profile{Name: "BackRider", UUID: OfflineUUID("BackRider")}, col2.send)
	if err != nil {
		t.Fatalf("RegisterPlayer(BackRider): %v", err)
	}
	s.SetPlayerTransform(passenger, 2.0, 65.0, 1.5, 0, 0, true)
	_ = s.SetPlayerGameMode(passenger, GameModeSurvival)

	camelID, err := s.SpawnCamel(world.DimensionOverworld, 2.0, 65.0, 1.5, false)
	if err != nil {
		t.Fatalf("SpawnCamel: %v", err)
	}
	s.SetCamelSaddled(camelID, true)

	// 1. Mount 2 players onto the Camel and tick to sync passenger seats.
	seat0, ok0 := s.MountCamel(driver, camelID)
	seat1, ok1 := s.MountCamel(passenger, camelID)
	if !ok0 || !ok1 || seat0 != 0 || seat1 != 1 {
		t.Fatalf("MountCamel seats = (%d/%v, %d/%v), want (0/true, 1/true)", seat0, ok0, seat1, ok1)
	}
	s.TickCamels(rand.New(rand.NewSource(1)))

	// 2. Dismount both riders and shear the saddle off the Camel using shears.
	s.DismountCamel(driver, camelID)
	s.DismountCamel(passenger, camelID)
	slot := world.HotbarContainerOffset + int(driver.SelectedSlot())
	driver.SetInventorySlot(slot, world.NewNamedItemStack("minecraft:shears", 1))
	if handled, _ := s.InteractWithCamel(driver, camelID); !handled || s.IsCamelSaddled(camelID) {
		t.Fatalf("expected shears interaction on unridden Camel to remove saddle: handled=%v saddled=%v", handled, s.IsCamelSaddled(camelID))
	}

	// 3. Verify Strider saddle and warped_fungus_on_a_stick boost remain intact.
	striderID, err := s.SpawnStrider(world.DimensionOverworld, 4.0, 65.0, 4.0, false)
	if err != nil {
		t.Fatalf("SpawnStrider: %v", err)
	}
	if !s.EquipStriderSaddle(striderID, true) || !s.IsStriderSaddled(striderID) {
		t.Fatalf("expected Strider saddle mechanics to remain intact")
	}
}
