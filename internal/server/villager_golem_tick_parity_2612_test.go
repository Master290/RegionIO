package server

import (
	"math"
	"math/rand"
	"testing"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

func TestVillagerSchedulePOISleepAndMultiDayParity2612(t *testing.T) {
	srv := newSurvivalTestServer(t)
	dim := world.DimensionOverworld
	feetY := float64(world.FlatSurfaceY + 1)

	// 1. Exact `data/minecraft/timeline/villager_schedule.json` (`period_ticks: 24000`):
	//    - Keyframe 12000 (`rest`) wraps into ticks 0..9 (`ticks < 10`) for both adults and babies.
	//    - `Schedule.VILLAGER_SIMPLE` was removed in 26.1.2: `Villager.registerBrainGoals` assigns
	//      `VILLAGER_ACTIVITY` to all non-babies (including `nitwit` and `none`), falling back from
	//      `work` (2000..8999) to `idle` when `JOB_SITE` is absent, and resting at 12000..23999 & 0..9.
	if got := VillagerScheduleActivity(VillagerProfessionFarmer, false, 5); got != VillagerActivityRest {
		t.Fatalf("adult at tod=5 = %q, want %q (wrap from keyframe 12000 before keyframe 10)", got, VillagerActivityRest)
	}
	if got := VillagerScheduleActivity(VillagerProfessionNone, true, 9); got != VillagerActivityRest {
		t.Fatalf("baby at tod=9 = %q, want %q (wrap from keyframe 12000 before keyframe 10)", got, VillagerActivityRest)
	}
	if got := VillagerScheduleActivity(VillagerProfessionNitwit, false, 1000); got != VillagerActivityIdle {
		t.Fatalf("nitwit at tod=1000 = %q, want %q (26.1.2 villager_schedule.json)", got, VillagerActivityIdle)
	}
	if got := VillagerScheduleActivity(VillagerProfessionNitwit, false, 13000); got != VillagerActivityRest {
		t.Fatalf("nitwit at tod=13000 = %q, want %q (26.1.2 villager_schedule.json)", got, VillagerActivityRest)
	}

	// 2. Unemployed villager on Day 1+ (`dayTime = 27000`, `tod = 3000`) MUST still acquire a nearby workstation!
	vUnemp, err := srv.SpawnVillagerInDimension(dim, 4.5, feetY, 4.5, VillagerTypePlains, VillagerProfessionNone, 1)
	if err != nil {
		t.Fatalf("SpawnVillagerInDimension: %v", err)
	}
	lecternSt, ok := world.BlockStateIDByProperties("minecraft:lectern", nil)
	if !ok {
		t.Fatal("missing minecraft:lectern")
	}
	srv.chunks.SetBlock(5, int(feetY), 4, lecternSt)
	srv.SetWorldTime(27000, 27000) // Day 1, tod = 3000
	srv.TickVillagers()
	vs, _ := srv.VillagerState(vUnemp)
	if vs.Profession != VillagerProfessionLibrarian {
		t.Fatalf("Day 1 (dayTime=27000) unemployed villager profession = %q, want %q", vs.Profession, VillagerProfessionLibrarian)
	}

	// 3. `Activity.MEET` (`9000..10999`) requires `MEETING_POINT` (`minecraft:bell`) in `Brain`:
	//    without a bell, `VillagerCurrentActivity` falls back to `idle`; with a bell, claims `MEETING_POINT` and enters `meet`.
	srv.SetWorldTime(33500, 33500) // tod = 9500
	srv.TickVillagers()
	if act := srv.VillagerCurrentActivity(vUnemp); act != VillagerActivityIdle {
		t.Fatalf("VillagerCurrentActivity at tod=9500 without bell = %q, want %q", act, VillagerActivityIdle)
	}
	bellSt, ok := world.BlockStateIDByProperties("minecraft:bell", nil)
	if !ok {
		t.Fatal("missing minecraft:bell")
	}
	srv.chunks.SetBlock(6, int(feetY), 4, bellSt)
	srv.TickVillagers()
	if act := srv.VillagerCurrentActivity(vUnemp); act != VillagerActivityMeet {
		t.Fatalf("VillagerCurrentActivity at tod=9500 with bell = %q, want %q", act, VillagerActivityMeet)
	}
	// Breaking the bell releases `MEETING_POINT` and falls back to `idle`.
	srv.chunks.SetBlock(6, int(feetY), 4, world.StateAir)
	srv.TickVillagers()
	if act := srv.VillagerCurrentActivity(vUnemp); act != VillagerActivityIdle {
		t.Fatalf("VillagerCurrentActivity after bell broken = %q, want %q", act, VillagerActivityIdle)
	}

	// 4. `OnVillagerStruckByLightning` converts villager to Witch and calls `releaseAllPois()` so
	//    its `job_site` lectern is freed for another unemployed villager!
	srv.SetDifficulty(DifficultyNormal)
	if _, conv := srv.OnVillagerStruckByLightning(vUnemp); !conv {
		t.Fatal("expected OnVillagerStruckByLightning to convert villager to witch")
	}
	v2, _ := srv.SpawnVillagerInDimension(dim, 4.5, feetY, 5.5, VillagerTypePlains, VillagerProfessionNone, 1)
	if _, claimed := srv.TryAcquireVillagerWorkstation(v2); !claimed {
		t.Fatal("expected lectern POI to be released when previous owner converted into Witch")
	}
}

func TestVillagerProfessionLockDelayedLevelUpAndRestockDemandParity2612(t *testing.T) {
	srv := newSurvivalTestServer(t)
	col := &survivalPacketCollector{}
	player, err := srv.RegisterPlayer(Profile{Name: "Trader", UUID: OfflineUUID("Trader")}, col.send)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	_ = srv.SetPlayerGameMode(player, GameModeSurvival)
	snap := player.Snapshot()
	feetY := int(math.Floor(snap.Y))

	// 1. `ResetProfession` (`ResetProfession.java:683..689`): `villagerXp == 0 && level <= 1`.
	//    A villager spawned at Level 2 with XP == 0 must NOT lose its profession when its workstation breaks.
	vLvl2, _ := srv.SpawnVillager(snap.X+1.5, snap.Y, snap.Z, VillagerProfessionNone, 1)
	lecternSt, _ := world.BlockStateIDByProperties("minecraft:lectern", nil)
	srv.chunks.SetBlock(int(snap.X)+2, feetY, int(snap.Z), lecternSt)
	srv.TryAcquireVillagerWorkstation(vLvl2)
	vs2, _ := srv.VillagerState(vLvl2)
	vs2.Level = 2
	vs2.XP = 0
	srv.SetVillagerState(vLvl2, vs2)
	srv.chunks.SetBlock(int(snap.X)+2, feetY, int(snap.Z), world.StateAir)
	if _, lost := srv.OnWorkstationBlockBroken(world.DimensionOverworld, int(snap.X)+2, feetY, int(snap.Z)); lost {
		t.Fatal("expected Level 2 villager (even with XP==0) to keep profession per ResetProfession (level <= 1)")
	}

	// 2. Locked villager (`XP > 0`) whose workstation broke (`IsVillagerJobSiteLost == true`)
	//    MUST re-bind to a newly placed matching workstation without losing its locked Offers/XP!
	vLocked, _ := srv.SpawnVillager(snap.X+40.5, snap.Y, snap.Z+40.0, VillagerProfessionNone, 1)
	srv.chunks.SetBlock(int(snap.X)+41, feetY, int(snap.Z)+40, lecternSt)
	srv.TryAcquireVillagerWorkstation(vLocked)
	srv.AddVillagerXP(vLocked, 5)
	lockedBefore, _ := srv.VillagerState(vLocked)
	firstOfferName := lockedBefore.Offers[0].Name

	srv.chunks.SetBlock(int(snap.X)+41, feetY, int(snap.Z)+40, world.StateAir)
	srv.OnWorkstationBlockBroken(world.DimensionOverworld, int(snap.X)+41, feetY, int(snap.Z)+40)
	if !srv.IsVillagerJobSiteLost(vLocked) {
		t.Fatal("expected IsVillagerJobSiteLost(vLocked) == true after lectern broken")
	}
	// Place a new lectern at an adjacent block and tick during work hours -> re-binds!
	srv.chunks.SetBlock(int(snap.X)+42, feetY, int(snap.Z)+40, lecternSt)
	srv.SetWorldTime(3000, 3000)
	srv.TickVillagers()
	if srv.IsVillagerJobSiteLost(vLocked) {
		t.Fatal("expected locked Librarian to bind newly placed lectern and clear jobSiteLost")
	}
	lockedAfter, _ := srv.VillagerState(vLocked)
	if lockedAfter.XP != 5 || len(lockedAfter.Offers) == 0 || lockedAfter.Offers[0].Name != firstOfferName {
		t.Fatalf("expected locked Librarian to preserve XP=5 and offers (%q), got XP=%d offers=%+v",
			firstOfferName, lockedAfter.XP, lockedAfter.Offers)
	}

	// 3. Delayed level-up (`updateMerchantTimer = 40` while `!isTrading()`) + `Regeneration I` (`200` ticks):
	vTrade, _ := srv.SpawnVillager(snap.X+1.0, snap.Y, snap.Z+1.0, VillagerProfessionFarmer, 1)
	srv.SetVillagerState(vTrade, VillagerState{
		EntityID:     vTrade,
		VillagerType: VillagerTypePlains,
		Profession:   VillagerProfessionFarmer,
		Level:        1,
		Offers: []MerchantOffer{
			{
				Name:            "minecraft:farmer/1/wheat_emerald",
				BaseCostA:       world.NewNamedItemStack("minecraft:wheat", 20),
				Result:          world.NewNamedItemStack("minecraft:emerald", 1),
				MaxUses:         16,
				XP:              10, // reaches Level 2 threshold (10) in 1 trade
				PriceMultiplier: 0.05,
			},
		},
	})
	player.SetInventorySlot(world.HotbarContainerOffset, world.NewNamedItemStack("minecraft:wheat", 20))
	opened, cid, err := srv.InteractWithVillager(player, vTrade)
	if err != nil || !opened {
		t.Fatalf("InteractWithVillager: opened=%v err=%v", opened, err)
	}
	_ = srv.HandleSelectTradePacket(player, protocol.NewReader(EncodeSelectTradePacket(0)))
	_ = srv.HandleMerchantContainerClick(player, cid, MerchantSlotResult, 0, ContainerInputPickup)

	// While trading UI is still open (`isTrading() == true`), villager must NOT have leveled up yet!
	for i := 0; i < 45; i++ {
		srv.TickVillagers()
	}
	vsDuring, _ := srv.VillagerState(vTrade)
	if vsDuring.Level != 1 {
		t.Fatalf("expected villager Level to remain 1 while player is still trading, got %d", vsDuring.Level)
	}
	_ = srv.CloseMerchantContainer(player, cid, true)

	// After closing UI, ticks 1..39 keep Level == 1; tick 40 upgrades to Level 2 + Regeneration I (200 ticks)!
	for i := 0; i < 39; i++ {
		srv.TickVillagers()
	}
	vs39, _ := srv.VillagerState(vTrade)
	if vs39.Level != 1 {
		t.Fatalf("expected villager Level to still be 1 at 39 ticks after stopTrading, got %d", vs39.Level)
	}
	srv.TickVillagers() // 40th tick
	vs40, _ := srv.VillagerState(vTrade)
	if vs40.Level != 2 || len(vs40.Offers) <= 1 {
		t.Fatalf("expected villager to level up to 2 on 40th tick with unlocked offers, got level=%d offers=%d", vs40.Level, len(vs40.Offers))
	}
	hasRegen := false
	for _, eff := range srv.EntityEffects(vTrade) {
		if eff.ID == EffectRegeneration && eff.Amplifier == 0 && eff.Duration == 200 {
			hasRegen = true
			break
		}
	}
	if !hasRegen {
		t.Fatalf("expected Regeneration I (200 ticks) on villager level-up, got effects=%+v", srv.EntityEffects(vTrade))
	}

	// 4. `MerchantOffer.updateDemand()` allows negative demand, `allowedToRestock` requires strictly `> 2400`,
	//    and `catchUpDemand` decays inflated demand on a new day even when `Uses == 0`!
	if got := UpdateMerchantOfferDemandOnRestock2612(0, 0, 16); got != -16 {
		t.Fatalf("UpdateMerchantOfferDemandOnRestock2612(0, 0, 16) = %d, want -16", got)
	}
	vRestock, _ := srv.SpawnVillager(snap.X+2.0, snap.Y, snap.Z-1.0, VillagerProfessionFarmer, 1)
	srv.SetVillagerState(vRestock, VillagerState{
		EntityID:       vRestock,
		VillagerType:   VillagerTypePlains,
		Profession:     VillagerProfessionFarmer,
		Level:          1,
		LastRestockDay: -1,
		Offers: []MerchantOffer{
			{
				Name:            "minecraft:farmer/1/wheat_emerald",
				BaseCostA:       world.NewNamedItemStack("minecraft:wheat", 20),
				Result:          world.NewNamedItemStack("minecraft:emerald", 1),
				Uses:            16,
				MaxUses:         16,
				PriceMultiplier: 0.05,
			},
		},
	})
	srv.SetWorldTime(2500, 2500)
	srv.TickVillagers() // Restock #1 at 2500 -> Demand = +16, RestocksToday = 1
	vr, _ := srv.VillagerState(vRestock)
	vr.Offers[0].Uses = 8
	srv.SetVillagerState(vRestock, vr)
	// Exactly +2400 ticks (`4900`) must NOT restock yet (`gameTime > lastRestockGameTime + 2400`).
	srv.SetWorldTime(4900, 4900)
	srv.TickVillagers()
	vr, _ = srv.VillagerState(vRestock)
	if vr.Offers[0].Uses != 8 {
		t.Fatalf("expected restock to wait until strictly > 2400 ticks (at 4900 uses=%d, want 8)", vr.Offers[0].Uses)
	}
	// Reset Uses to 0 (no further trades on Day 0, RestocksToday = 1, Demand = +16).
	// On Day 1 (`27000`), `catchUpDemand` runs `2 - 1 = 1` time -> Demand decays `16 + 0 - 16 = 0`!
	vr.Offers[0].Uses = 0
	srv.SetVillagerState(vRestock, vr)
	srv.SetWorldTime(27000, 27000)
	srv.TickVillagers()
	vr, _ = srv.VillagerState(vRestock)
	if vr.Offers[0].Demand != 0 {
		t.Fatalf("expected catchUpDemand on new day with Uses=0 to decay Demand from 16 to 0, got %d", vr.Offers[0].Demand)
	}
}

func TestVillagerGossipDiscardDecayAndHeroSpecialPriceParity2612(t *testing.T) {
	srv := newSurvivalTestServer(t)
	col := &survivalPacketCollector{}
	player, err := srv.RegisterPlayer(Profile{Name: "Hero", UUID: OfflineUUID("Hero")}, col.send)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	_ = srv.SetPlayerGameMode(player, GameModeSurvival)
	snap := player.Snapshot()

	// 1. `GossipType.MAJOR_POSITIVE`: `decayPerTransfer == 20` in `26.1.2.jar` (NOT 100).
	if spec, ok := LookupGossipTypeSpec(GossipMajorPositive); !ok || spec.DecayPerTransfer != 20 {
		t.Fatalf("GossipMajorPositive.DecayPerTransfer = %d, want 20", spec.DecayPerTransfer)
	}

	v1, _ := srv.SpawnVillager(snap.X+1.5, snap.Y, snap.Z, VillagerProfessionFarmer, 1)
	v2, _ := srv.SpawnVillager(snap.X+2.5, snap.Y, snap.Z, VillagerProfessionFarmer, 1)
	uuid := player.Profile.UUID

	// 2. `GossipContainer.DISCARD_THRESHOLD = 2`:
	//    - `minor_positive = 2` decays by 1 -> `1 < 2` -> removed (`0`)!
	//    - `trading = 21` shared (`21 - 20 = 1 < 2`) -> NOT transferred (`0`)!
	srv.AddVillagerGossip(v1, uuid, GossipMinorPositive, 2)
	srv.DecayVillagerGossips(v1)
	if got := srv.VillagerGossipValue(v1, uuid, GossipMinorPositive); got != 0 {
		t.Fatalf("minor_positive(2) after 1 decay = %d, want 0 (DISCARD_THRESHOLD=2)", got)
	}
	srv.AddVillagerGossip(v1, uuid, GossipTrading, 21)
	srv.ShareVillagerGossip(v1, v2)
	if got := srv.VillagerGossipValue(v2, uuid, GossipTrading); got != 0 {
		t.Fatalf("trading(21) transferred with decay 20 = %d, want 0 (1 < DISCARD_THRESHOLD=2)", got)
	}

	// 3. `Villager.maybeDecayGossip()` uses per-villager `gameTime` (`24000` ticks), NOT `dayTime / 24000` boundary!
	srv.SetWorldTime(100, 23999)
	vDecay, _ := srv.SpawnVillager(snap.X+3.5, snap.Y, snap.Z, VillagerProfessionFarmer, 1)
	srv.AddVillagerGossip(vDecay, uuid, GossipMinorPositive, 10)
	srv.TickVillagers()          // initializes lastGossipDecayTime = 100
	srv.SetWorldTime(101, 24000) // crosses midnight/day boundary after only 1 tick of gameTime!
	srv.TickVillagers()
	if got := srv.VillagerGossipValue(vDecay, uuid, GossipMinorPositive); got != 10 {
		t.Fatalf("gossip after 1 tick across dayTime=24000 = %d, want 10 (must wait 24000 gameTime ticks)", got)
	}
	srv.SetWorldTime(24100, 48000)
	srv.TickVillagers()
	if got := srv.VillagerGossipValue(vDecay, uuid, GossipMinorPositive); got != 9 {
		t.Fatalf("gossip after 24000 gameTime ticks = %d, want 9", got)
	}

	// 4. `Villager.updateSpecialPrices`: reputation (+20 * 0.05 -> -1) + `Hero of the Village I`
	//    (`floor(0.3 * 20) = 6` -> -6) => `20 - 1 - 6 = 13` wheat in real merchant trade, AND
	//    `CloseMerchantContainer` resets `SpecialPriceDiff` to 0!
	vHero, _ := srv.SpawnVillager(snap.X+1.0, snap.Y, snap.Z+1.0, VillagerProfessionFarmer, 1)
	srv.SetVillagerState(vHero, VillagerState{
		EntityID:     vHero,
		VillagerType: VillagerTypePlains,
		Profession:   VillagerProfessionFarmer,
		Level:        1,
		Offers: []MerchantOffer{
			{
				Name:            "minecraft:farmer/1/wheat_emerald",
				BaseCostA:       world.NewNamedItemStack("minecraft:wheat", 20),
				Result:          world.NewNamedItemStack("minecraft:emerald", 1),
				MaxUses:         16,
				XP:              2,
				PriceMultiplier: 0.05,
			},
		},
	})
	srv.AddVillagerGossip(vHero, uuid, GossipMinorPositive, 20)
	srv.ApplyPlayerEffect(player, NewStatusEffectInstance(EffectHeroOfTheVillage, 600, 0))
	player.SetInventorySlot(world.HotbarContainerOffset, world.NewNamedItemStack("minecraft:wheat", 13))

	opened, cid, err := srv.InteractWithVillager(player, vHero)
	if err != nil || !opened {
		t.Fatalf("InteractWithVillager(vHero): opened=%v err=%v", opened, err)
	}
	_ = srv.HandleSelectTradePacket(player, protocol.NewReader(EncodeSelectTradePacket(0)))
	_ = srv.HandleMerchantContainerClick(player, cid, MerchantSlotResult, 0, ContainerInputPickup)
	if player.CarriedItem().ResolvedID() != "minecraft:emerald" || player.CarriedItem().Count != 1 {
		t.Fatalf("expected Hero of the Village + rep trade to succeed with 13 wheat, carried=%+v", player.CarriedItem())
	}
	_ = srv.CloseMerchantContainer(player, cid, true)
	vhAfter, _ := srv.VillagerState(vHero)
	if vhAfter.Offers[0].SpecialPriceDiff != 0 {
		t.Fatalf("expected CloseMerchantContainer (stopTrading) to reset SpecialPriceDiff to 0, got %d", vhAfter.Offers[0].SpecialPriceDiff)
	}
}

func TestVillagerFarmingAndVacantBedBreedingParity2612(t *testing.T) {
	srv := newSurvivalTestServer(t)
	dim := world.DimensionOverworld
	feetY := float64(world.FlatSurfaceY + 1)
	rng := rand.New(rand.NewSource(77))

	// 1. `VillagerMakeLove`: two villagers with >= 12 food points CANNOT spawn a baby when no
	//    reachable unclaimed bed exists within 48 blocks!
	vA, _ := srv.SpawnVillagerInDimension(dim, 6.5, feetY, 6.5, VillagerTypePlains, VillagerProfessionFarmer, 1)
	vB, _ := srv.SpawnVillagerInDimension(dim, 7.5, feetY, 6.5, VillagerTypePlains, VillagerProfessionLibrarian, 1)
	srv.AddVillagerFood(vA, "minecraft:bread", 3) // 12 pts
	srv.AddVillagerFood(vB, "minecraft:bread", 3) // 12 pts

	if childID, bred := srv.TryBreedVillagers(vA, vB, rng); bred || childID != 0 {
		t.Fatalf("expected TryBreedVillagers WITHOUT vacant bed to fail, got (child=%d, bred=%v)", childID, bred)
	}

	// Place a reachable unclaimed bed -> now breeding succeeds and claims the bed for the newborn baby!
	bedSt, _ := world.BlockStateByName("minecraft:red_bed", map[string]string{
		"facing":   "north",
		"occupied": "false",
		"part":     "foot",
	})
	srv.chunks.SetBlock(8, int(feetY), 6, bedSt)
	srv.AddVillagerFood(vA, "minecraft:bread", 3)
	srv.AddVillagerFood(vB, "minecraft:bread", 3)
	childID, bred := srv.TryBreedVillagers(vA, vB, rng)
	if !bred || childID <= 0 {
		t.Fatalf("expected TryBreedVillagers WITH vacant bed to succeed, got (child=%d, bred=%v)", childID, bred)
	}
	// A second breed attempt with no second vacant bed must fail because the baby claimed the only bed!
	vC, _ := srv.SpawnVillagerInDimension(dim, 6.5, feetY, 8.5, VillagerTypePlains, VillagerProfessionCleric, 1)
	vD, _ := srv.SpawnVillagerInDimension(dim, 7.5, feetY, 8.5, VillagerTypePlains, VillagerProfessionMason, 1)
	srv.AddVillagerFood(vC, "minecraft:bread", 3)
	srv.AddVillagerFood(vD, "minecraft:bread", 3)
	if _, bred2 := srv.TryBreedVillagers(vC, vD, rng); bred2 {
		t.Fatal("expected second pair not to breed when the only bed was already given to the first baby")
	}

	// 2. Farmer autonomous crop pickup + `HarvestFarmland` harvest & replant during `WORK` (`tod = 3000`):
	farmlandSt, _ := world.BlockStateByName("minecraft:farmland", map[string]string{"moisture": "7"})
	matureWheat, _ := world.BlockStateByName("minecraft:wheat", map[string]string{"age": "7"})
	srv.chunks.SetBlock(10, int(feetY)-1, 10, farmlandSt)
	srv.chunks.SetBlock(10, int(feetY), 10, matureWheat)
	farmerID, _ := srv.SpawnVillagerInDimension(dim, 10.5, feetY, 10.5, VillagerTypePlains, VillagerProfessionFarmer, 1)
	composterSt, _ := world.BlockStateByName("minecraft:composter", map[string]string{"level": "0"})
	srv.chunks.SetBlock(11, int(feetY), 10, composterSt)
	srv.SpawnItemEntityInDimension(dim, 10.5, feetY, 10.5, world.NewNamedItemStack("minecraft:wheat_seeds", 4))

	srv.SetWorldTime(3000, 3000)
	for i := 0; i < 5; i++ {
		srv.TickVillagers()
		srv.TickEntities(rng)
	}
	afterCrop := srv.chunks.GetBlock(10, int(feetY), 10)
	youngWheat, _ := world.BlockStateByName("minecraft:wheat", map[string]string{"age": "0"})
	if afterCrop != youngWheat {
		t.Fatalf("expected Farmer villager (%d) to harvest mature wheat and replant age=0 wheat, got blockState=%d (want %d)",
			farmerID, afterCrop, youngWheat)
	}
}

func TestVillagerGolemSummonAndIronGolemCombatReputationParity2612(t *testing.T) {
	srv := newSurvivalTestServer(t)
	srv.SetDifficulty(DifficultyNormal)
	col := &survivalPacketCollector{}
	player, err := srv.RegisterPlayer(Profile{Name: "Villain", UUID: OfflineUUID("Villain")}, col.send)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	_ = srv.SetPlayerGameMode(player, GameModeSurvival)
	srv.SetPlayerHealth(player, 20.0, 20, 5.0)
	// Equip 2 pieces of Netherite armor (2 * 0.1 = 0.2 KNOCKBACK_RESISTANCE) so player survives & scales fling.
	player.SetInventorySlot(5, world.NewNamedItemStack("minecraft:netherite_helmet", 1))
	player.SetInventorySlot(6, world.NewNamedItemStack("minecraft:netherite_chestplate", 1))

	dim := world.DimensionOverworld
	feetY := float64(world.FlatSurfaceY + 1)
	rng := rand.New(rand.NewSource(1234))

	// 1. `wantsToSpawnGolem`: 3 panicked villagers who have NEVER slept (`LAST_SLEPT` absent) MUST NOT summon Iron Golem!
	v1, _ := srv.SpawnVillagerInDimension(dim, 4.5, feetY, 4.5, VillagerTypePlains, VillagerProfessionFarmer, 1)
	v2, _ := srv.SpawnVillagerInDimension(dim, 5.5, feetY, 4.5, VillagerTypePlains, VillagerProfessionLibrarian, 1)
	v3, _ := srv.SpawnVillagerInDimension(dim, 6.5, feetY, 4.5, VillagerTypePlains, VillagerProfessionCleric, 1)
	// Pillager at 14 blocks triggers `VillagerHostilesSensor` (`pillager = 15.0` in 26.1.2.jar)!
	pillagerID, _ := srv.SpawnMobInDimension(dim, 4.5, feetY, 18.5, MobTypePillager)
	_ = pillagerID

	srv.SetWorldTime(200, 200)
	if gid, spawned := srv.TickVillagerGolemSpawning(rng); spawned || gid != 0 {
		t.Fatalf("expected panicked villagers without LAST_SLEPT NOT to summon Iron Golem, got (id=%d, spawned=%v)", gid, spawned)
	}

	// Put all 3 villagers to sleep at night (`13000`), then check expiry at `13000 + 24000 = 37000`:
	bedSt, _ := world.BlockStateByName("minecraft:red_bed", map[string]string{
		"facing":   "north",
		"occupied": "false",
		"part":     "foot",
	})
	srv.chunks.SetBlock(4, int(feetY), 4, bedSt)
	srv.chunks.SetBlock(5, int(feetY), 4, bedSt)
	srv.chunks.SetBlock(6, int(feetY), 4, bedSt)
	srv.SetWorldTime(13000, 13000)
	srv.TickVillagers()
	if !srv.IsVillagerSleeping(v1) || !srv.IsVillagerSleeping(v2) || !srv.IsVillagerSleeping(v3) {
		t.Fatal("expected all 3 villagers to sleep at night")
	}
	// At `gameTime = 37000` (`37000 - 13000 == 24000`, NOT `< 24000`), `wantsToSpawnGolem` is false!
	srv.SetWorldTime(37000, 25000)
	srv.TickVillagers()
	if _, spawned := srv.TickVillagerGolemSpawning(rng); spawned {
		t.Fatal("expected LAST_SLEPT older than or equal to 24000 ticks NOT to summon Iron Golem")
	}
	// Sleep again at `38000`, then at `39000` (`< 24000` ticks since sleep) with pillager at 14 blocks -> summons golem!
	srv.SetWorldTime(38000, 38000) // tod = 14000 (night)
	srv.TickVillagers()
	srv.SetWorldTime(39000, 39000)
	villageGolemID, spawned := srv.TickVillagerGolemSpawning(rng)
	if !spawned || villageGolemID <= 0 {
		t.Fatal("expected 3 villagers with recent LAST_SLEPT + pillager within 15 blocks to summon Iron Golem")
	}

	// 2. Iron Golem `DefendVillageTargetGoal`:
	//    - Reputation `-20` (and `-99`) must NOT aggro (`threshold <= -100`, NOT `-15`!).
	//    - Reputation `-105` (`<= -100`) MUST aggro village golem, and NEVER aggro `playerCreated = true` golem!
	gEnt := srv.entities.Get(villageGolemID)
	srv.SetPlayerTransform(player, gEnt.X+1.0, feetY, gEnt.Z, 0, 0, true)
	srv.AddVillagerGossip(v1, player.Profile.UUID, GossipMinorNegative, 20) // rep = -20
	if srv.ShouldIronGolemAttackPlayer(villageGolemID, player) {
		t.Fatal("expected village Iron Golem NOT to attack player with reputation -20 (vanilla threshold <= -100)")
	}
	srv.AddVillagerGossip(v1, player.Profile.UUID, GossipMajorNegative, 20) // rep = -20 + (-100) = -120 <= -100
	if !srv.ShouldIronGolemAttackPlayer(villageGolemID, player) {
		t.Fatal("expected village Iron Golem to attack player with reputation -120 (<= -100)")
	}

	playerGolemID, _ := srv.SpawnIronGolem(dim, gEnt.X+4.0, feetY, gEnt.Z, true)
	srv.OnIronGolemDamaged(playerGolemID, 100.0, 90.0, player)
	if srv.ShouldIronGolemAttackPlayer(playerGolemID, player) {
		t.Fatal("expected playerCreated Iron Golem NEVER to attack player even after being hit and rep <= -100")
	}

	// 3. Iron Golem `doHurtTarget` vertical fling (`0.4 * max(0, 1 - KNOCKBACK_RESISTANCE)`):
	//    - Against player with 2 Netherite armor pieces (`kbRes = 0.2`), sends `PlaySetEntityMotion` with `vy > 0.25`!
	col.reset()
	srv.TickIronGolems(rng)
	if col.count(protocol.PlaySetEntityMotion) == 0 {
		t.Fatal("expected village Iron Golem attacking player to send PlaySetEntityMotion upward fling")
	}

	//    - Against Warden (`KNOCKBACK_RESISTANCE = 1.0`), vertical fling `VelocityY` MUST be 0!
	wardenGolemID, _ := srv.SpawnIronGolem(dim, 12.5, feetY, 12.5, true)
	wardenID, _ := srv.SpawnMobInDimension(dim, 13.5, feetY, 12.5, MobTypeWarden)
	srv.TickIronGolems(rng)
	wEnt := srv.entities.Get(wardenID)
	if wEnt != nil && wEnt.VelocityY != 0 {
		t.Fatalf("expected Warden (KNOCKBACK_RESISTANCE=1.0) hit by Iron Golem (%d) to have VelocityY=0, got %d",
			wardenGolemID, wEnt.VelocityY)
	}

	// 4. Iron Golem underwater air supply immunity (`IronGolem.decreaseAirSupply` returns air unchanged):
	nextAir, drownDmg := StepMobAirSupplyAndDrowning2612(MobTypeIronGolem, 300, true, false)
	if nextAir != 300 || drownDmg != 0 {
		t.Fatalf("StepMobAirSupplyAndDrowning2612(iron_golem) = (%d, %v), want (300, 0)", nextAir, drownDmg)
	}
}

func TestVillagerInventoryBoneMealGolemSpawnRulesPoppyAndNBTRoundTrip2612(t *testing.T) {
	srv := newSurvivalTestServer(t)
	srv.SetDifficulty(DifficultyNormal)
	dim := world.DimensionOverworld
	feetY := float64(world.FlatSurfaceY + 1)

	// 1. `WantsVillagerToPickUp2612`: Farmer picks up `minecraft:bone_meal`, Librarian does NOT!
	if !WantsVillagerToPickUp2612(VillagerProfessionFarmer, "minecraft:bone_meal") {
		t.Fatal("expected Farmer villager to pick up minecraft:bone_meal")
	}
	if WantsVillagerToPickUp2612(VillagerProfessionLibrarian, "minecraft:bone_meal") {
		t.Fatal("expected non-Farmer villager NOT to pick up minecraft:bone_meal")
	}

	// 2. Farmer `WorkAtComposter.makeBread` (`3x wheat -> 1x bread` in 8-slot inventory) & `TradeWithVillager` food sharing:
	composterSt, _ := world.BlockStateByName("minecraft:composter", map[string]string{"level": "0"})
	srv.chunks.SetBlock(20, int(feetY), 20, composterSt)
	farmerID, _ := srv.SpawnVillagerInDimension(dim, 20.5, feetY, 20.5, VillagerTypePlains, VillagerProfessionNone, 1)
	srv.ClaimVillagerWorkstation(farmerID, dim, 20, int(feetY), 20)
	srv.AddItemToVillagerInventory(farmerID, world.NewNamedItemStack("minecraft:wheat", 18)) // 6 bread = 24 food pts

	srv.SetWorldTime(3000, 3000)
	srv.TickVillagers()
	inv := srv.VillagerInventory(farmerID)
	var breadCount int32
	for _, st := range inv {
		if !st.IsEmpty() && st.ResolvedID() == "minecraft:bread" {
			breadCount += st.Count
		}
	}
	if breadCount != 6 {
		t.Fatalf("expected Farmer at composter to craft 18 wheat into 6 bread, got %d (inv=%+v)", breadCount, inv)
	}

	// Spawn a hungry villager next to the farmer and tick during idle/meet -> farmer shares half its bread (3 bread = 12 pts)!
	hungryID, _ := srv.SpawnVillagerInDimension(dim, 21.5, feetY, 20.5, VillagerTypePlains, VillagerProfessionLibrarian, 1)
	srv.SetWorldTime(9500, 9500)
	srv.TickVillagers()
	if !srv.CanVillagerBreed(hungryID) {
		t.Fatalf("expected hungry villager (%d) to receive shared bread (>=12 food pts) from farmer", hungryID)
	}

	// 3. `SpawnUtil.Strategy.LEGACY_IRON_GOLEM`: platform made strictly of `minecraft:glass` / `minecraft:oak_leaves` must REJECT village golem spawn!
	glassSt, _ := world.BlockStateIDByProperties("minecraft:glass", nil)
	leavesSt, _ := world.BlockStateIDByProperties("minecraft:oak_leaves", nil)
	for dx := -2; dx <= 2; dx++ {
		for dz := -2; dz <= 2; dz++ {
			srv.chunks.SetBlock(24+dx, 100, 24+dz, glassSt)
		}
	}
	if IsLegacyIronGolemSpawnSpot2612(srv.chunks, 24, 101, 24) {
		t.Fatal("expected IsLegacyIronGolemSpawnSpot2612 to reject minecraft:glass floor")
	}
	srv.chunks.SetBlock(24, 100, 24, leavesSt)
	if IsLegacyIronGolemSpawnSpot2612(srv.chunks, 24, 101, 24) {
		t.Fatal("expected IsLegacyIronGolemSpawnSpot2612 to reject minecraft:oak_leaves floor")
	}
	srv.chunks.SetBlock(24, 100, 24, glassSt)
	if badGolemID, ok := srv.TrySpawnVillageIronGolemNear(dim, 24.5, 101.0, 24.5, nil); ok || badGolemID != 0 {
		t.Fatalf("expected LEGACY_IRON_GOLEM to reject glass floor, got (id=%d, ok=%v)", badGolemID, ok)
	}

	// 4. `IronGolem` & `Villager` binary NBT round-trip (`PlayerCreated`, `anger_end_time`, `angry_at`, `VillagerData`, `Gossips`, `Inventory`, `last_slept`):
	srv.SetWorldTime(5000, 5000)
	col := &survivalPacketCollector{}
	p, _ := srv.RegisterPlayer(Profile{Name: "NBTTarget", UUID: OfflineUUID("NBTTarget")}, col.send)
	_ = srv.SetPlayerGameMode(p, GameModeSurvival)

	g1, _ := srv.SpawnIronGolem(dim, 30.5, feetY, 30.5, false)
	srv.SetMobHealth(g1, 62.5)
	srv.ProvokeVillageIronGolemsNear(dim, 30.5, feetY, 30.5, p)
	gNBT, ok := srv.SaveIronGolemNBT2612(g1)
	if !ok || len(gNBT) == 0 {
		t.Fatal("SaveIronGolemNBT2612 failed")
	}
	g2, _ := srv.SpawnIronGolem(dim, 32.5, feetY, 30.5, true) // starts playerCreated=true, 100 HP
	if err := srv.LoadIronGolemNBT2612(g2, gNBT); err != nil {
		t.Fatalf("LoadIronGolemNBT2612: %v", err)
	}
	if srv.IsIronGolemPlayerCreated(g2) || srv.MobHealth(g2) != 62.5 || !srv.ShouldIronGolemAttackPlayer(g2, p) {
		t.Fatalf("restored IronGolem mismatch: playerCreated=%v hp=%.1f attacks=%v",
			srv.IsIronGolemPlayerCreated(g2), srv.MobHealth(g2), srv.ShouldIronGolemAttackPlayer(g2, p))
	}

	srv.RecordVillagerSlept(farmerID, 4800)
	srv.AddVillagerGossip(farmerID, p.Profile.UUID, GossipMajorPositive, 20)
	vNBT, ok := srv.SaveVillagerNBT2612(farmerID)
	if !ok || len(vNBT) == 0 {
		t.Fatal("SaveVillagerNBT2612 failed")
	}
	restoredVil, _ := srv.SpawnVillagerInDimension(dim, 34.5, feetY, 30.5, VillagerTypeDesert, VillagerProfessionNone, 1)
	if err := srv.LoadVillagerNBT2612(restoredVil, vNBT); err != nil {
		t.Fatalf("LoadVillagerNBT2612: %v", err)
	}
	if !srv.WantsVillagerToSpawnGolem2612(restoredVil, 5000) {
		t.Fatal("expected restored villager to preserve Brain.memories.minecraft:last_slept")
	}
	if got := srv.VillagerGossipValue(restoredVil, p.Profile.UUID, GossipMajorPositive); got != 20 {
		t.Fatalf("restored villager major_positive gossip = %d, want 20", got)
	}
}
