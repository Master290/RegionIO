package server

import (
	"math"
	"math/rand"
	"strings"
	"testing"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

func TestRaiderTypesAndWaveComposition(t *testing.T) {
	types := AllRaiderMobTypes()
	if len(types) != 5 {
		t.Fatalf("AllRaiderMobTypes len = %d, want 5", len(types))
	}
	for _, rt := range types {
		if !IsRaiderMobType(rt) {
			t.Fatalf("IsRaiderMobType(%q) = false, want true", rt)
		}
		if id := ResolveRaiderEntityTypeID(rt); id < 0 {
			t.Fatalf("ResolveRaiderEntityTypeID(%q) = %d, want >= 0", rt, id)
		}
		if hp := RaiderMaxHealth(rt); hp <= 0 {
			t.Fatalf("RaiderMaxHealth(%q) = %v, want > 0", rt, hp)
		}
		if dmg := RaiderBaseAttackDamage(rt); dmg <= 0 {
			t.Fatalf("RaiderBaseAttackDamage(%q) = %v, want > 0", rt, dmg)
		}
	}
	if RaiderMaxHealth(MobTypeRavager) != 100.0 {
		t.Fatalf("Ravager max health = %v, want 100.0", RaiderMaxHealth(MobTypeRavager))
	}

	// Wave counts by difficulty and Bad Omen level.
	if got := TotalRaidWaveCount(DifficultyPeaceful, 1); got != 0 {
		t.Fatalf("Peaceful waves = %d, want 0", got)
	}
	if got := TotalRaidWaveCount(DifficultyEasy, 1); got != 3 {
		t.Fatalf("Easy (Omen I) waves = %d, want 3", got)
	}
	if got := TotalRaidWaveCount(DifficultyEasy, 2); got != 4 {
		t.Fatalf("Easy (Omen II) waves = %d, want 4", got)
	}
	if got := TotalRaidWaveCount(DifficultyNormal, 1); got != 5 {
		t.Fatalf("Normal (Omen I) waves = %d, want 5", got)
	}
	if got := TotalRaidWaveCount(DifficultyNormal, 3); got != 6 {
		t.Fatalf("Normal (Omen III) waves = %d, want 6", got)
	}
	if got := TotalRaidWaveCount(DifficultyHard, 1); got != 7 {
		t.Fatalf("Hard (Omen I) waves = %d, want 7", got)
	}
	if got := TotalRaidWaveCount(DifficultyHard, 5); got != 8 {
		t.Fatalf("Hard (Omen V) waves = %d, want 8", got)
	}

	rng := rand.New(rand.NewSource(42))
	for wave := 1; wave <= 8; wave++ {
		members := RollRaidWaveMembers(wave, DifficultyHard, wave == 8, rng)
		if len(members) == 0 {
			t.Fatalf("wave %d rolled 0 members", wave)
		}
		captains := 0
		for _, m := range members {
			if !IsRaiderMobType(m.TypeName) {
				t.Fatalf("wave %d member has non-raider type %q", wave, m.TypeName)
			}
			if m.IsCaptain {
				captains++
			}
		}
		if captains != 1 {
			t.Fatalf("wave %d captains = %d, want 1", wave, captains)
		}
	}
}

func TestPatrolCaptainKillVoluntaryExileAndOminousBottle(t *testing.T) {
	srv, _ := newTestEnchantServer(t)
	srv.SetDifficulty(DifficultyNormal)

	sess, err := srv.RegisterPlayer(Profile{
		UUID: OfflineUUID("ExileHunter"),
		Name: "ExileHunter",
	}, nil)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	_ = srv.SetPlayerGameMode(sess, GameModeSurvival)

	captainID, err := srv.SpawnPatrolCaptain(10.5, 5.0, 10.5, MobTypePillager, 2)
	if err != nil || captainID <= 0 {
		t.Fatalf("SpawnPatrolCaptain: id=%d, err=%v", captainID, err)
	}
	isCap, amp := srv.IsRaidCaptain(captainID)
	if !isCap || amp != 2 {
		t.Fatalf("IsRaidCaptain = (%v, %d), want (true, 2)", isCap, amp)
	}

	if !srv.OnRaiderKilled(captainID, sess) {
		t.Fatal("expected OnRaiderKilled to return true for patrol captain")
	}
	if !sess.HasCompletedAdvancement("minecraft:adventure/voluntary_exile") {
		t.Fatal("expected Voluntary Exile advancement on killing raid captain")
	}

	// Verify ominous_bottle and white_banner item entities were dropped.
	var foundBottle, foundBanner bool
	for _, ent := range srv.entities.AppendAll(nil) {
		if !ent.IsItem() {
			continue
		}
		switch ent.Item.ResolvedID() {
		case ItemOminousBottle:
			foundBottle = true
		case ItemWhiteBanner:
			foundBanner = true
		}
	}
	if !foundBottle || !foundBanner {
		t.Fatalf("captain drops: foundBottle=%v, foundBanner=%v; want both true", foundBottle, foundBanner)
	}

	// Put an ominous_bottle (amplifier 2 = Bad Omen III) in hotbar slot 0 (container slot 36) and drink it via UsePotionOrMilkItem.
	slot36 := world.HotbarContainerOffset
	sess.SetInventorySlot(slot36, world.NewNamedItemStack(ItemOminousBottle, 1))
	SetSlotOminousBottleAmplifier(sess, slot36, 2)

	if !srv.UsePotionOrMilkItem(sess, 0) {
		t.Fatal("expected UsePotionOrMilkItem to consume ominous_bottle")
	}
	if !sess.InventorySlot(slot36).IsEmpty() {
		t.Fatalf("expected ominous_bottle slot to be empty after shattering, got %+v", sess.InventorySlot(slot36))
	}

	badOmen, ok := sess.PlayerEffect(EffectBadOmen)
	if !ok {
		t.Fatal("expected player to have minecraft:bad_omen after drinking ominous_bottle")
	}
	if badOmen.Amplifier != 2 || badOmen.Duration != OminousBottleDurationTicks {
		t.Fatalf("Bad Omen = (amp=%d, dur=%d), want (amp=2, dur=%d)", badOmen.Amplifier, badOmen.Duration, OminousBottleDurationTicks)
	}
}

func TestBadOmenToRaidOmenAndFullRaidVictoryWithBossBar(t *testing.T) {
	srv, _ := newTestEnchantServer(t)
	srv.SetDifficulty(DifficultyEasy) // 3 waves for base Bad Omen I

	var bossPackets [][]byte
	sess, err := srv.RegisterPlayer(Profile{
		UUID: OfflineUUID("VillageDefender"),
		Name: "VillageDefender",
	}, func(id int32, body []byte) error {
		if id == PlayBossEventCB {
			cp := append([]byte(nil), body...)
			bossPackets = append(bossPackets, cp)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	_ = srv.SetPlayerGameMode(sess, GameModeSurvival)
	sess.mu.Lock()
	sess.position = [3]float64{0.5, 5.0, 0.5}
	sess.mu.Unlock()

	// Spawn a villager at (4.5, 5.0, 4.5) to establish a village.
	villagerID, err := srv.SpawnVillager(4.5, 5.0, 4.5, VillagerProfessionFarmer, 1)
	if err != nil || villagerID <= 0 {
		t.Fatalf("SpawnVillager: id=%d, err=%v", villagerID, err)
	}

	// Give player Bad Omen I and tick raids: Bad Omen should convert to Raid Omen (600 ticks).
	srv.ApplyPlayerEffect(sess, NewStatusEffectInstance(EffectBadOmen, OminousBottleDurationTicks, 0))
	srv.TickRaids()

	if sess.HasPlayerEffect(EffectBadOmen) {
		t.Fatal("expected Bad Omen to be removed when converted to Raid Omen in a village")
	}
	raidOmen, ok := sess.PlayerEffect(EffectRaidOmen)
	if !ok || raidOmen.Duration != RaidOmenDurationTicks || raidOmen.Amplifier != 0 {
		t.Fatalf("Raid Omen after conversion = %+v (ok=%v), want dur=%d amp=0", raidOmen, ok, RaidOmenDurationTicks)
	}

	// Fast-forward Raid Omen to 1 tick remaining and tick raids to trigger the Raid.
	srv.RemovePlayerEffect(sess, EffectRaidOmen)
	srv.ApplyPlayerEffect(sess, NewStatusEffectInstance(EffectRaidOmen, 1, 0))
	srv.TickRaids()

	if sess.HasPlayerEffect(EffectRaidOmen) {
		t.Fatal("expected Raid Omen to be consumed when Raid starts")
	}
	raids := srv.ActiveRaids()
	if len(raids) != 1 {
		t.Fatalf("ActiveRaids count = %d, want 1", len(raids))
	}
	raid := raids[0]
	if raid.Status != RaidStatusOngoing || raid.BadOmenLevel != 1 || raid.TotalWaves != 3 {
		t.Fatalf("started raid = %+v, want Ongoing, BadOmenLevel=1, TotalWaves=3", raid)
	}
	if !raid.HasBossBar {
		t.Fatal("expected raid to have a runtime boss bar")
	}
	if len(bossPackets) == 0 {
		t.Fatal("expected player in village range to receive BossEvent ADD packet")
	}
	decodedAdd, err := DecodeBossEventPacket(protocol.NewReader(bossPackets[0]))
	if err != nil {
		t.Fatalf("DecodeBossEventPacket: %v", err)
	}
	if decodedAdd.Operation != BossEventOpAdd || decodedAdd.Color != BossBarColorRed || decodedAdd.Overlay != BossBarOverlayNotched10 {
		t.Fatalf("raid boss bar ADD packet = %+v, want Red + Notched10", decodedAdd)
	}

	// Clear all 3 Easy waves and verify wave progression + Victory rewards.
	for wave := 1; wave <= 3; wave++ {
		spawned, err := srv.SpawnNextRaidWave(raid.ID)
		if err != nil || len(spawned) == 0 {
			t.Fatalf("wave %d SpawnNextRaidWave: spawned=%v, err=%v", wave, spawned, err)
		}
		rSnap, _ := srv.RaidByID(raid.ID)
		if rSnap.GroupsSpawned != wave || rSnap.AliveRaiderCount() != len(spawned) {
			t.Fatalf("wave %d state: GroupsSpawned=%d Alive=%d, want %d and %d",
				wave, rSnap.GroupsSpawned, rSnap.AliveRaiderCount(), wave, len(spawned))
		}

		// Damage the first raider by half its health and verify bossbar progress is in (0, 1).
		firstID := spawned[0]
		halfHP := srv.MobHealth(firstID) * 0.5
		srv.SetMobHealth(firstID, halfHP)
		srv.syncRaidBossBar(raid.ID)
		bar, ok := srv.GetRuntimeBossBar(rSnap.BossBarUUID)
		if !ok || bar.Progress <= 0 || bar.Progress >= 1.0 {
			t.Fatalf("wave %d bossbar progress after damage = %v, want in (0, 1)", wave, bar.Progress)
		}

		// Kill all raiders in the wave.
		for _, eid := range spawned {
			srv.OnRaiderKilled(eid, sess)
		}
	}

	finalRaid, ok := srv.RaidByID(raid.ID)
	if !ok || finalRaid.Status != RaidStatusVictory {
		t.Fatalf("raid status after clearing all waves = %+v, want %q", finalRaid, RaidStatusVictory)
	}
	bar, ok := srv.GetRuntimeBossBar(finalRaid.BossBarUUID)
	if !ok || !strings.Contains(bar.Name.Text, "Victory") {
		t.Fatalf("bossbar title on victory = %q, want Raid - Victory", bar.Name.Text)
	}

	// Verify Hero of the Village status effect and advancement were granted!
	heroEff, ok := sess.PlayerEffect(EffectHeroOfTheVillage)
	if !ok || heroEff.Duration != HeroOfTheVillageDurationTicks || heroEff.Amplifier != 0 {
		t.Fatalf("Hero of the Village effect = %+v (ok=%v), want dur=%d amp=0",
			heroEff, ok, HeroOfTheVillageDurationTicks)
	}
	if !sess.HasCompletedAdvancement("minecraft:adventure/hero_of_the_village") {
		t.Fatal("expected minecraft:adventure/hero_of_the_village advancement to be completed")
	}

	// Verify Hero of the Village villager trade discount and gift helper.
	if !srv.ApplyHeroOfTheVillageToVillager(sess, villagerID) {
		t.Fatal("expected ApplyHeroOfTheVillageToVillager to succeed")
	}
	vs, _ := srv.VillagerState(villagerID)
	for _, off := range vs.Offers {
		if off.SpecialPriceDiff >= 0 {
			t.Fatalf("expected negative SpecialPriceDiff on villager offer %+v", off)
		}
	}
	if diff5 := HeroOfTheVillagePriceDiff(20, 4); diff5 != -11 {
		t.Fatalf("HeroOfTheVillagePriceDiff(20, amp=4 [55%%]) = %d, want -11", diff5)
	}
	gift := RollHeroOfTheVillageGift(VillagerProfessionFarmer, rand.New(rand.NewSource(1)))
	if gift.IsEmpty() {
		t.Fatal("expected non-empty Hero of the Village gift from farmer")
	}
}

func TestRaidDefeatWhenVillageLostAndNBTPersistence(t *testing.T) {
	cfg := DefaultConfig()
	cfg.WorldDir = t.TempDir()
	cache := world.NewCache(-1, world.GenerateFlat)
	srv, err := NewWithCache(cfg, cache)
	if err != nil {
		t.Fatalf("NewWithCache: %v", err)
	}
	srv.SetDifficulty(DifficultyNormal)

	sess, err := srv.RegisterPlayer(Profile{
		UUID: OfflineUUID("DefeatTester"),
		Name: "DefeatTester",
	}, nil)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	sess.mu.Lock()
	sess.position = [3]float64{20.5, 5.0, 20.5}
	sess.mu.Unlock()

	villagerID, err := srv.SpawnVillager(22.5, 5.0, 22.5, VillagerProfessionLibrarian, 1)
	if err != nil {
		t.Fatalf("SpawnVillager: %v", err)
	}

	// Start a raid with Bad Omen II (should have 5 + 1 = 6 waves on Normal).
	raid := srv.StartOrAbsorbRaid(world.DimensionOverworld, 22.5, 5.0, 22.5, 2, sess)
	if raid == nil || raid.TotalWaves != 6 || raid.BadOmenLevel != 2 {
		t.Fatalf("StartOrAbsorbRaid = %+v, want BadOmenLevel=2 TotalWaves=6", raid)
	}

	// Absorb another Bad Omen II into the same raid -> BadOmenLevel becomes 4.
	raid = srv.StartOrAbsorbRaid(world.DimensionOverworld, 24.5, 5.0, 24.5, 2, sess)
	if raid.BadOmenLevel != 4 {
		t.Fatalf("BadOmenLevel after merge = %d, want 4", raid.BadOmenLevel)
	}

	// Test NBT round-trip and disk persistence (<WorldDir>/region/raids.nbt).
	if err := srv.SaveRaids(); err != nil {
		t.Fatalf("SaveRaids: %v", err)
	}
	srv2, err := NewWithCache(cfg, cache)
	if err != nil {
		t.Fatalf("NewWithCache srv2: %v", err)
	}
	if err := srv2.LoadRaids(); err != nil {
		t.Fatalf("LoadRaids: %v", err)
	}
	loaded, ok := srv2.RaidByID(raid.ID)
	if !ok || loaded.BadOmenLevel != 4 || loaded.TotalWaves != 6 || math.Abs(loaded.CenterX-22.5) > 0.01 {
		t.Fatalf("loaded raid = %+v (ok=%v), want BadOmenLevel=4 TotalWaves=6 CenterX=22.5", loaded, ok)
	}
	if !loaded.Heroes[sess.Profile.UUID] {
		t.Fatal("expected hero UUID to survive raid NBT round-trip")
	}

	// Spawn wave 1 on srv, then remove the only villager and tick: raid should transition to Defeat.
	if _, err := srv.SpawnNextRaidWave(raid.ID); err != nil {
		t.Fatalf("SpawnNextRaidWave: %v", err)
	}
	srv.entities.Remove(villagerID)
	srv.TickRaids()

	defeated, ok := srv.RaidByID(raid.ID)
	if !ok || defeated.Status != RaidStatusDefeat {
		t.Fatalf("raid status after losing all villagers = %+v, want %q", defeated, RaidStatusDefeat)
	}
	bar, ok := srv.GetRuntimeBossBar(defeated.BossBarUUID)
	if !ok || !strings.Contains(bar.Name.Text, "Defeat") {
		t.Fatalf("bossbar title on defeat = %q, want Raid - Defeat", bar.Name.Text)
	}
}

func TestPillagerPatrolPlayPathOminousBottlePickupAndBannerPromotion(t *testing.T) {
	cfg := DefaultConfig()
	cfg.WorldDir = t.TempDir()
	cache := world.NewCache(-1, world.GenerateFlat)
	srv, err := NewWithCache(cfg, cache)
	if err != nil {
		t.Fatalf("NewWithCache: %v", err)
	}
	srv.SetDifficulty(DifficultyNormal)

	sess, err := srv.RegisterPlayer(Profile{
		UUID: OfflineUUID("PatrolHunter"),
		Name: "PatrolHunter",
	}, nil)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	sess.mu.Lock()
	sess.position = [3]float64{100.5, 5.0, 100.5}
	sess.mu.Unlock()

	// Disable doPatrolSpawning -> natural patrol should not spawn.
	srv.SetGameRuleBool("doPatrolSpawning", false)
	if _, _, ok := srv.TrySpawnNaturalIllagerPatrol(nil); ok {
		t.Fatal("expected TrySpawnNaturalIllagerPatrol to respect doPatrolSpawning=false")
	}

	// Enable doPatrolSpawning -> natural patrol spawns with captain + followers.
	srv.SetGameRuleBool("doPatrolSpawning", true)
	patrolID, members, ok := srv.TrySpawnNaturalIllagerPatrol(nil)
	if !ok || patrolID <= 0 || len(members) < 2 {
		t.Fatalf("TrySpawnNaturalIllagerPatrol = (%d, %v, %v), want patrol with >=2 members", patrolID, members, ok)
	}
	leaderID := members[0]
	followerID := members[1]

	isCap, _ := srv.IsRaidCaptain(leaderID)
	if !isCap || !srv.IsIllagerCaptain(leaderID) {
		t.Fatalf("expected patrol leader %d to be marked captain in both raid and illager trackers", leaderID)
	}
	if eqPkt := srv.RaiderEquipmentPacket(leaderID); len(eqPkt) == 0 {
		t.Fatal("expected non-empty RaiderEquipmentPacket (crossbow + Ominous Banner) for patrol captain")
	}
	if metaPkt := srv.RaiderEntityDataPacket(leaderID); len(metaPkt) == 0 {
		t.Fatal("expected non-empty RaiderEntityDataPacket for patrol captain")
	}

	// Set custom Bad Omen III (amplifier 2) on the captain, move captain near player, and kill with enchanted sword.
	srv.MarkRaidCaptain(leaderID, 2)
	srv.entities.Update(leaderID, func(e *world.Entity) {
		e.X = 101.5
		e.Y = 5.0
		e.Z = 100.5
	})
	// Position follower right where the banner will drop so StepIllagerPatrols promotes it via banner pickup.
	srv.entities.Update(followerID, func(e *world.Entity) {
		e.X = 101.5
		e.Y = 5.0
		e.Z = 100.5
	})

	sword := world.NewNamedItemStack("minecraft:netherite_sword", 1)
	sess.SetInventorySlot(36, sword)
	SetSlotItemMeta(sess, 36, ItemMeta{
		Enchantments: map[string]int{EnchantSharpness: 5},
	})
	srv.SetMobHealth(leaderID, 10.0)
	if _, killed := srv.AttackEntityWithEnchantments(sess, leaderID); !killed {
		t.Fatal("expected enchanted sword strike to kill patrol captain")
	}
	if !sess.HasCompletedAdvancement("minecraft:adventure/voluntary_exile") {
		t.Fatal("expected minecraft:adventure/voluntary_exile advancement after killing patrol captain with enchanted sword")
	}

	// StepIllagerPatrols should promote the surviving follower to captain after picking up the dropped banner.
	srv.StepIllagerPatrols()
	if isFolCap, _ := srv.IsRaidCaptain(followerID); !isFolCap {
		t.Fatal("expected surviving patrol follower to be promoted to captain after leader death")
	}

	// Move player onto the dropped ominous_bottle and pick it up: slot amplifier (2 = Bad Omen III) must be preserved.
	sess.SetInventorySlot(36, world.ItemStack{})
	sess.mu.Lock()
	sess.position = [3]float64{101.5, 5.0, 100.5}
	sess.mu.Unlock()
	if picked := srv.TryPickupItems(sess); picked <= 0 {
		t.Fatal("expected player to pick up dropped patrol captain loot")
	}

	bottleSlot := -1
	for slot := 0; slot < world.PlayerInventorySlots; slot++ {
		if IsOminousBottleItem(sess.InventorySlot(slot)) {
			bottleSlot = slot
			break
		}
	}
	if bottleSlot < 0 {
		t.Fatal("expected picked-up ominous_bottle in player inventory")
	}
	if gotAmp := GetSlotOminousBottleAmplifier(sess, bottleSlot); gotAmp != 2 {
		t.Fatalf("picked-up ominous_bottle amplifier = %d, want 2 (Bad Omen III)", gotAmp)
	}

	// Move ominous_bottle to hotbar slot 36, swap to offhand and back, drop and re-pickup, then drink.
	if bottleSlot != 36 {
		sess.SetInventorySlot(36, sess.InventorySlot(bottleSlot))
		SetSlotOminousBottleAmplifier(sess, 36, GetSlotOminousBottleAmplifier(sess, bottleSlot))
		sess.SetInventorySlot(bottleSlot, world.ItemStack{})
		clearSlotOminousBottleAmplifier(sess, bottleSlot)
	}
	if !srv.SwapHeldWithOffhand(sess) {
		t.Fatal("SwapHeldWithOffhand failed")
	}
	if gotAmp := GetSlotOminousBottleAmplifier(sess, OffHandContainerSlot); gotAmp != 2 {
		t.Fatalf("offhand ominous_bottle amplifier after swap = %d, want 2", gotAmp)
	}
	if !srv.DrinkOminousBottle(sess, HandOffHand) {
		t.Fatal("DrinkOminousBottle from offhand failed")
	}
	eff, hasBadOmen := sess.PlayerEffect(EffectBadOmen)
	if !hasBadOmen || eff.Amplifier != 2 {
		t.Fatalf("Bad Omen after drinking captain's ominous_bottle = %+v (ok=%v), want amplifier=2", eff, hasBadOmen)
	}
}

func TestHeroOfTheVillageTradeExecutionAndAutomaticVillagerGifts(t *testing.T) {
	cfg := DefaultConfig()
	cfg.WorldDir = t.TempDir()
	cache := world.NewCache(-1, world.GenerateFlat)
	srv, err := NewWithCache(cfg, cache)
	if err != nil {
		t.Fatalf("NewWithCache: %v", err)
	}
	srv.SetDifficulty(DifficultyEasy)

	var packets []int32
	sess, err := srv.RegisterPlayer(Profile{
		UUID: OfflineUUID("HeroTrader"),
		Name: "HeroTrader",
	}, func(pktID int32, payload []byte) error {
		packets = append(packets, pktID)
		return nil
	})
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	sess.mu.Lock()
	sess.position = [3]float64{50.5, 5.0, 50.5}
	sess.mu.Unlock()

	villagerID, err := srv.SpawnVillager(52.0, 5.0, 50.5, VillagerProfessionFarmer, 1)
	if err != nil {
		t.Fatalf("SpawnVillager: %v", err)
	}
	// Configure an offer costing 20 emeralds -> 1 bread so Hero of the Village V (55% discount -> -11) reduces cost to 9 emeralds.
	vs, _ := srv.VillagerState(villagerID)
	vs.Offers = []MerchantOffer{
		{
			Name:            "minecraft:farmer/bread_bundle",
			BaseCostA:       world.NewNamedItemStack("minecraft:emerald", 20),
			Result:          world.NewNamedItemStack("minecraft:bread", 4),
			MaxUses:         16,
			PriceMultiplier: 0.05,
			XP:              2,
		},
	}
	srv.SetVillagerState(villagerID, vs)

	// Grant Hero of the Village V (amplifier 4) to the player.
	srv.ApplyPlayerEffect(sess, NewStatusEffectInstance(EffectHeroOfTheVillage, HeroOfTheVillageDurationTicks, 4))

	// Give player 12 emeralds (enough for discounted cost 9, not enough for base cost 20).
	sess.SetInventorySlot(36, world.NewNamedItemStack("minecraft:emerald", 12))

	cid, err := srv.OpenMerchant(sess, villagerID)
	if err != nil {
		t.Fatalf("OpenMerchant: %v", err)
	}
	if err := srv.SelectMerchantTrade(sess, 0); err != nil {
		t.Fatalf("SelectMerchantTrade: %v", err)
	}
	if err := srv.HandleMerchantContainerClick(sess, cid, MerchantSlotResult, 0, ContainerInputPickup); err != nil {
		t.Fatalf("HandleMerchantContainerClick: %v", err)
	}
	if carried := sess.CarriedItem(); carried.IsEmpty() || carried.ResolvedID() != "minecraft:bread" || carried.Count != 4 {
		t.Fatalf("carried item after Hero of the Village trade = %+v, want 4x minecraft:bread", carried)
	}
	_ = srv.CloseMerchantContainer(sess, cid, false)

	// Remaining emeralds in player inventory should be 12 - 9 = 3.
	var remEmeralds int32
	for i := 0; i < world.PlayerInventorySlots; i++ {
		st := sess.InventorySlot(i)
		if st.ResolvedID() == "minecraft:emerald" {
			remEmeralds += st.Count
		}
	}
	if remEmeralds != 3 {
		t.Fatalf("remaining emeralds after Hero V discounted trade = %d, want 3 (20 - 11 = 9 cost)", remEmeralds)
	}

	// TickRaids should also trigger the nearby employed farmer to throw a Hero of the Village gift item entity.
	beforeEnts := len(srv.entities.All())
	srv.TickRaids()
	afterEnts := len(srv.entities.All())
	if afterEnts <= beforeEnts {
		t.Fatalf("expected nearby farmer villager to throw a Hero of the Village gift item during TickRaids (entities %d -> %d)", beforeEnts, afterEnts)
	}

	// Verify disableRaids gamerule stops an active raid.
	raid := srv.StartOrAbsorbRaid(world.DimensionOverworld, 52.0, 5.0, 50.5, 1, sess)
	if raid == nil {
		t.Fatal("expected raid to start when disableRaids=false")
	}
	srv.SetGameRuleBool("disableRaids", true)
	srv.TickRaids()
	if _, stillActive := srv.RaidByID(raid.ID); stillActive {
		t.Fatal("expected active raid to stop when disableRaids=true")
	}
}

