package server

import (
	"math/rand"
	"testing"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

func TestIllagerPatrolCaptainBannerAndPromotion(t *testing.T) {
	s := newSurvivalTestServer(t)
	s.SetDifficulty(DifficultyNormal)
	rng := rand.New(rand.NewSource(42))

	patrolID, captainID, members, err := s.SpawnIllagerPatrol(world.DimensionOverworld, 10.5, 65.0, 10.5, 3, 1, rng)
	if err != nil || patrolID == 0 || captainID == 0 || len(members) != 3 {
		t.Fatalf("SpawnIllagerPatrol failed: patrol=%d cap=%d members=%v err=%v", patrolID, captainID, members, err)
	}
	isCap, amp := s.IsRaidCaptain(captainID)
	if !isCap || amp != 1 {
		t.Fatalf("expected patrol leader %d to be Raid Captain with amp=1, got isCap=%v amp=%d", captainID, isCap, amp)
	}

	followerID := members[1]
	if isFollowerCap, _ := s.IsRaidCaptain(followerID); isFollowerCap {
		t.Fatalf("expected follower %d not to be captain initially", followerID)
	}

	// Move captain ahead and verify follower marches toward captain.
	s.entities.Update(captainID, func(e *world.Entity) {
		e.X = 20.5
		e.Z = 10.5
	})
	fBefore := s.entities.Get(followerID)
	for i := 0; i < 5; i++ {
		s.TickIllagersWitchesRavagersAndVexes(rng)
	}
	fAfter := s.entities.Get(followerID)
	if fAfter == nil || fAfter.X <= fBefore.X {
		t.Fatalf("expected patrol follower to march toward captain, before=%v after=%v", fBefore.X, fAfter.X)
	}

	// Drop an Ominous Banner (white_banner) next to the follower -> follower picks it up and becomes Captain!
	_, _ = s.SpawnItemEntityInDimension(world.DimensionOverworld, fAfter.X+0.5, fAfter.Y, fAfter.Z,
		world.NewNamedItemStack(ItemWhiteBanner, 1))
	s.TickIllagersWitchesRavagersAndVexes(rng)
	if promoted, _ := s.IsRaidCaptain(followerID); !promoted {
		t.Fatalf("expected patrol follower %d to pick up dropped Ominous Banner and become Captain", followerID)
	}
}

func TestPillagerCrossbowAndVindicatorJohnny(t *testing.T) {
	s := newSurvivalTestServer(t)
	s.SetDifficulty(DifficultyNormal)
	rng := rand.New(rand.NewSource(7))

	pillagerID, err := s.SpawnMobInDimension(world.DimensionOverworld, 0.5, 65.0, 0.5, MobTypePillager)
	if err != nil {
		t.Fatalf("SpawnMobInDimension(pillager) failed: %v", err)
	}
	if !s.SetPillagerChargingCrossbow(pillagerID, true) {
		t.Fatalf("SetPillagerChargingCrossbow failed")
	}
	pkt := EncodePillagerDataPacket(pillagerID, true, true)
	decID, decCeleb, decCharge, err := DecodePillagerDataPacket(protocol.NewReader(pkt))
	if err != nil || decID != pillagerID || !decCeleb || !decCharge {
		t.Fatalf("DecodePillagerDataPacket mismatch: id=%d celeb=%v charge=%v err=%v", decID, decCeleb, decCharge, err)
	}

	target := newBeeTestPlayer(t, s, "ArcherTarget", 5.5, 65.0, 0.5)
	hpBefore := target.Snapshot().Health
	arrowID, dealt := s.ShootPillagerCrossbowAtPlayer(pillagerID, target)
	if arrowID == 0 || dealt <= 0 || target.Snapshot().Health >= hpBefore {
		t.Fatalf("expected ShootPillagerCrossbowAtPlayer to spawn arrow and deal damage, arrowID=%d dealt=%v", arrowID, dealt)
	}

	// Vindicator named "Johnny" attacks nearby non-illager mobs!
	vindID, err := s.SpawnMobInDimension(world.DimensionOverworld, 30.5, 65.0, 30.5, MobTypeVindicator)
	if err != nil {
		t.Fatalf("SpawnMobInDimension(vindicator) failed: %v", err)
	}
	if !s.SetVindicatorCustomName(vindID, "Johnny") {
		t.Fatalf("expected SetVindicatorCustomName(\"Johnny\") to enable IsJohnny")
	}
	pigID, err := s.SpawnMobInDimension(world.DimensionOverworld, 31.5, 65.0, 30.5, MobTypePig)
	if err != nil {
		t.Fatalf("SpawnMobInDimension(pig) failed: %v", err)
	}
	pigHPBefore := s.MobHealth(pigID)
	s.TickIllagersWitchesRavagersAndVexes(rng)
	if s.MobHealth(pigID) >= pigHPBefore {
		t.Fatalf("expected Johnny vindicator to attack nearby pig, hpBefore=%v hpAfter=%v", pigHPBefore, s.MobHealth(pigID))
	}
}

func TestEvokerFangsVexSummonDecayAndWololoSheep(t *testing.T) {
	s := newSurvivalTestServer(t)
	s.SetDifficulty(DifficultyNormal)
	rng := rand.New(rand.NewSource(19))

	evokerID, err := s.SpawnMobInDimension(world.DimensionOverworld, 0.5, 65.0, 0.5, MobTypeEvoker)
	if err != nil {
		t.Fatalf("SpawnMobInDimension(evoker) failed: %v", err)
	}

	// 1. Cast Evoker Fangs in a line (distance > 3) and in a 2-ring circle (distance <= 3).
	lineFangs := s.CastEvokerFangsSpell(evokerID, 8.5, 65.0, 0.5)
	if len(lineFangs) != 8 {
		t.Fatalf("expected 8 fangs in line attack, got %d", len(lineFangs))
	}
	circleFangs := s.CastEvokerFangsSpell(evokerID, 1.5, 65.0, 0.5)
	if len(circleFangs) != 13 {
		t.Fatalf("expected 13 fangs (5 inner + 8 outer) in close circle attack, got %d", len(circleFangs))
	}

	victim := newBeeTestPlayer(t, s, "FangVictim", 2.0, 65.0, 0.5)
	hpBefore := victim.Snapshot().Health
	for i := 0; i < 6; i++ {
		s.TickIllagersWitchesRavagersAndVexes(rng)
	}
	if victim.Snapshot().Health >= hpBefore {
		t.Fatalf("expected Evoker Fangs to snap shut and deal magic damage to player, before=%v after=%v",
			hpBefore, victim.Snapshot().Health)
	}

	// 2. Summon Vexes and verify charging metadata & life decay when summoner dies.
	vexIDs := s.CastEvokerSummonVexSpell(evokerID)
	if len(vexIDs) != EvokerSummonVexCount {
		t.Fatalf("expected %d summoned vexes, got %d", EvokerSummonVexCount, len(vexIDs))
	}
	vID := vexIDs[0]
	vpkt := EncodeVexDataPacket(vID, true)
	decVID, decCharging, err := DecodeVexDataPacket(protocol.NewReader(vpkt))
	if err != nil || decVID != vID || !decCharging {
		t.Fatalf("DecodeVexDataPacket mismatch: id=%d charging=%v err=%v", decVID, decCharging, err)
	}

	// Exceed Vex MaxLifeTicks -> takes 1.0 decay damage on 20-tick boundary.
	s.illagerManager().mu.Lock()
	s.illagerManager().vexes[vID].LifeTicks = VexLifeDecayStartTicks + 19
	s.illagerManager().mu.Unlock()
	vexHPBefore := s.MobHealth(vID)
	s.TickIllagersWitchesRavagersAndVexes(rng)
	if s.MobHealth(vID) >= vexHPBefore {
		t.Fatalf("expected Vex past MaxLifeTicks to take decay damage, before=%v after=%v", vexHPBefore, s.MobHealth(vID))
	}

	// 3. "Wololo" converts a nearby Blue Sheep (11) into a Red Sheep (14).
	sheepID, err := s.SpawnMobInDimension(world.DimensionOverworld, 4.5, 65.0, 4.5, MobTypeSheep)
	if err != nil {
		t.Fatalf("SpawnMobInDimension(sheep) failed: %v", err)
	}
	s.SetSheepWoolColor(sheepID, 11) // Blue wool
	convID, converted := s.TryEvokerWololoSheep(evokerID)
	if !converted || convID != sheepID || s.SheepWoolColor(sheepID) != 14 {
		t.Fatalf("expected Evoker Wololo to convert blue sheep (11) to red (14), conv=%v color=%d",
			converted, s.SheepWoolColor(sheepID))
	}
}

func TestIllusionerBlindnessAndMirrorClones(t *testing.T) {
	s := newSurvivalTestServer(t)
	s.SetDifficulty(DifficultyNormal)
	rng := rand.New(rand.NewSource(33))

	illID, err := s.SpawnIllusioner(world.DimensionOverworld, 0.5, 65.0, 0.5)
	if err != nil || illID == 0 {
		t.Fatalf("SpawnIllusioner failed: id=%d err=%v", illID, err)
	}
	p := newBeeTestPlayer(t, s, "Target", 4.5, 65.0, 0.5)

	if !s.CastIllusionerBlindnessSpell(illID, p) || !p.HasPlayerEffect(EffectBlindness) {
		t.Fatalf("expected CastIllusionerBlindnessSpell to apply Blindness to target")
	}
	if !s.CastIllusionerMirrorSpell(illID, rng) || !s.HasEntityEffect(illID, EffectInvisibility) {
		t.Fatalf("expected CastIllusionerMirrorSpell to apply Invisibility to illusioner")
	}
	st, ok := s.GetIllagerState(illID)
	if !ok || !st.HasMirrorClones {
		t.Fatalf("expected Illusioner state to have 4 mirror clone offsets, got %+v", st)
	}
}

func TestWitchPotionsMagicResistanceAndRaidAllyHeal(t *testing.T) {
	s := newSurvivalTestServer(t)
	s.SetDifficulty(DifficultyNormal)

	if got := ReduceWitchMagicDamage(10.0); got != 1.5 {
		t.Fatalf("ReduceWitchMagicDamage(10.0) = %v, want 1.5 (85%% reduction)", got)
	}

	witchID, err := s.SpawnRaiderMob(0.5, 65.0, 0.5, MobTypeWitch, false, 0, 0)
	if err != nil {
		t.Fatalf("SpawnRaiderMob(witch) failed: %v", err)
	}

	// Hurt witch -> selects Healing potion, drinks for 32 ticks, and restores +4.0 HP.
	s.SetMobHealth(witchID, 15.0)
	if pot := s.SelectWitchSelfPotion(witchID, 5.0); pot != WitchPotionHealing {
		t.Fatalf("expected hurt witch to select %q, got %q", WitchPotionHealing, pot)
	}
	if !s.StartWitchDrinkPotion(witchID, WitchPotionHealing) {
		t.Fatalf("StartWitchDrinkPotion failed")
	}
	wpkt := EncodeWitchDataPacket(witchID, false, true)
	decID, _, decDrink, err := DecodeWitchDataPacket(protocol.NewReader(wpkt))
	if err != nil || decID != witchID || !decDrink {
		t.Fatalf("DecodeWitchDataPacket mismatch: id=%d drink=%v err=%v", decID, decDrink, err)
	}
	applied, ok := s.FinishWitchDrinkPotion(witchID)
	if !ok || applied != WitchPotionHealing || s.MobHealth(witchID) != 19.0 {
		t.Fatalf("expected FinishWitchDrinkPotion to heal witch to 19.0 HP, got %v (applied=%q)", s.MobHealth(witchID), applied)
	}

	// Offensive splash potions: Slowness at >= 8 blocks, Poison when healthy, Weakness at <= 3 blocks.
	farPlayer := newBeeTestPlayer(t, s, "FarRunner", 9.0, 65.0, 0.5)
	pot1, _ := s.WitchThrowSplashPotionAtPlayer(witchID, farPlayer)
	if pot1 != WitchPotionSlowness || !farPlayer.HasPlayerEffect(EffectSlowness) {
		t.Fatalf("expected Slowness splash at >= 8 blocks, got %q", pot1)
	}
	s.SetPlayerTransform(farPlayer, 2.5, 65.0, 0.5, 0, 0, true)
	pot2, _ := s.WitchThrowSplashPotionAtPlayer(witchID, farPlayer)
	if pot2 != WitchPotionPoison || !farPlayer.HasPlayerEffect(EffectPoison) {
		t.Fatalf("expected Poison splash on healthy player, got %q", pot2)
	}
	pot3, _ := s.WitchThrowSplashPotionAtPlayer(witchID, farPlayer)
	if pot3 != WitchPotionWeakness || !farPlayer.HasPlayerEffect(EffectWeakness) {
		t.Fatalf("expected Weakness splash at <= 3 blocks once poisoned, got %q", pot3)
	}

	// Raid support: Witch in an active raid heals injured fellow raider.
	raid := s.StartOrAbsorbRaid(world.DimensionOverworld, 0.5, 65.0, 0.5, 1, farPlayer)
	if raid == nil {
		t.Fatalf("StartOrAbsorbRaid failed")
	}
	raidWitchID, _ := s.SpawnRaiderMob(0.5, 65.0, 0.5, MobTypeWitch, false, raid.ID, 1)
	allyID, _ := s.SpawnRaiderMob(2.5, 65.0, 0.5, MobTypePillager, false, raid.ID, 1)
	s.SetMobHealth(allyID, 10.0)

	healedID, healed := s.TryWitchHealRaidAlly(raidWitchID)
	if !healed || healedID != allyID || s.MobHealth(allyID) <= 10.0 || !s.HasEntityEffect(allyID, EffectRegeneration) {
		t.Fatalf("expected raid witch to heal and regenerate injured raider %d, healed=%v hp=%v",
			allyID, healed, s.MobHealth(allyID))
	}
}

func TestRavagerShieldStunRoarAndBlockDestructionAndChorusProjectile(t *testing.T) {
	s := newSurvivalTestServer(t)
	s.SetDifficulty(DifficultyNormal)
	s.SetCreeperBlockGriefing(true)
	rng := rand.New(rand.NewSource(77))

	ravID, err := s.SpawnMobInDimension(world.DimensionOverworld, 10.5, 65.0, 10.5, MobTypeRavager)
	if err != nil {
		t.Fatalf("SpawnMobInDimension(ravager) failed: %v", err)
	}

	defender := newBeeTestPlayer(t, s, "ShieldBearer", 12.5, 65.0, 10.5)
	if !s.OnRavagerBlockedByShield(ravID) {
		t.Fatalf("OnRavagerBlockedByShield failed")
	}
	rst, ok := s.GetRavagerState(ravID)
	if !ok || rst.StunnedTicks != RavagerStunDurationTicks {
		t.Fatalf("expected Ravager StunnedTicks = %d, got %+v", RavagerStunDurationTicks, rst)
	}

	hpBeforeRoar := defender.Snapshot().Health
	for i := 0; i < RavagerStunDurationTicks+1; i++ {
		s.TickIllagersWitchesRavagersAndVexes(rng)
	}
	if defender.Snapshot().Health >= hpBeforeRoar {
		t.Fatalf("expected Ravager roar after 40-tick stun to deal AoE damage, before=%v after=%v",
			hpBeforeRoar, defender.Snapshot().Health)
	}

	// Place oak_leaves and wheat (on farmland) in the Ravager's bounding box -> Ravager tramples both!
	cache := s.DimensionChunks(world.DimensionOverworld)
	leavesState, _ := world.BlockStateByName("minecraft:oak_leaves", nil)
	farmlandState, _ := world.BlockStateByName("minecraft:farmland", nil)
	wheatState, _ := world.BlockStateByName("minecraft:wheat", map[string]string{"age": "3"})
	cache.SetBlock(10, 65, 10, leavesState)
	cache.SetBlock(11, 64, 10, farmlandState)
	cache.SetBlock(11, 65, 10, wheatState)

	broken := s.RavagerDestroyBlocksAround(ravID)
	if len(broken) < 2 || cache.GetBlock(10, 65, 10) != world.StateAir || cache.GetBlock(11, 65, 10) != world.StateAir {
		t.Fatalf("expected Ravager to destroy leaves and crops around it, broken=%v", broken)
	}

	// Test projectile hit on chorus_flower breaks the flower and drops 1x minecraft:chorus_flower.
	endStone, _ := world.BlockStateByName("minecraft:end_stone", nil)
	cache.SetBlock(25, 64, 25, endStone)
	cache.ApplyBlockWithUpdates(25, 65, 25, world.ChorusFlowerBlockState(0))

	_, _ = s.OnProjectileBlockImpact(world.DimensionOverworld, 25, 65, 25, 24.0, 65.5, 25.5, 25.5, 65.5, 25.5, 0, MobTypeArrow)
	if got := cache.GetBlock(25, 65, 25); got != world.StateAir {
		t.Fatalf("expected projectile impact on chorus_flower to break it to air, got state %d", got)
	}
	foundChorusDrop := false
	for _, ent := range s.entities.AppendAll(nil) {
		if ent.IsItem() && ent.Item.ResolvedID() == "minecraft:chorus_flower" && ent.Item.Count == 1 {
			foundChorusDrop = true
			break
		}
	}
	if !foundChorusDrop {
		t.Fatalf("expected projectile impact on chorus_flower to drop 1x minecraft:chorus_flower")
	}
}
