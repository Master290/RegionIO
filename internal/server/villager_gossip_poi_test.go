package server

import (
	"math/rand"
	"testing"

	"regionio/internal/world"
)

func TestVillagerPOIWorkstationAcquisitionLossAndLockedProfession(t *testing.T) {
	srv := newSurvivalTestServer(t)

	// 1. Verify all 13 trading professions have workstation block mappings.
	for _, prof := range TradingVillagerProfessions() {
		blocks := WorkstationBlocksForProfession(prof)
		if len(blocks) == 0 {
			t.Fatalf("expected workstation block mapping for profession %q", prof)
		}
		gotProf, ok := ProfessionForWorkstationBlock(blocks[0])
		if !ok || gotProf != prof {
			t.Fatalf("ProfessionForWorkstationBlock(%q) = (%q, %v), want (%q, true)", blocks[0], gotProf, ok, prof)
		}
	}

	// 2. Spawn two unemployed villagers ("minecraft:none") at (10.5, 65, 10.5).
	v1, err := srv.SpawnVillager(10.5, 65.0, 10.5, VillagerProfessionNone, 1)
	if err != nil {
		t.Fatalf("SpawnVillager(v1): %v", err)
	}
	v2, err := srv.SpawnVillager(11.5, 65.0, 10.5, VillagerProfessionNone, 1)
	if err != nil {
		t.Fatalf("SpawnVillager(v2): %v", err)
	}

	// Place a single lectern at (12, 65, 10).
	lecternState, ok := world.BlockStateIDByProperties("minecraft:lectern", nil)
	if !ok || lecternState == world.StateAir {
		t.Fatal("expected minecraft:lectern block state to resolve")
	}
	srv.chunks.SetBlock(12, 65, 10, lecternState)

	// v1 acquires the lectern and becomes a Novice Librarian with rolled offers.
	site, acquired := srv.TryAcquireVillagerWorkstation(v1)
	if !acquired || site.Profession != VillagerProfessionLibrarian {
		t.Fatalf("v1 TryAcquireVillagerWorkstation = (%+v, %v), want librarian", site, acquired)
	}
	vs1, _ := srv.VillagerState(v1)
	if vs1.Profession != VillagerProfessionLibrarian || len(vs1.Offers) == 0 {
		t.Fatalf("v1 state after claiming lectern = (prof=%q, offers=%d), want librarian with offers", vs1.Profession, len(vs1.Offers))
	}

	// v2 cannot claim the same lectern because v1 owns it.
	if _, ok := srv.TryAcquireVillagerWorkstation(v2); ok {
		t.Fatal("expected v2 not to claim already-occupied lectern")
	}

	// 3. Break the lectern while v1 has XP == 0 -> v1 loses profession and reverts to "minecraft:none".
	srv.chunks.SetBlock(12, 65, 10, world.StateAir)
	affectedID, lostProf := srv.OnWorkstationBlockBroken(world.DimensionOverworld, 12, 65, 10)
	if affectedID != v1 || !lostProf {
		t.Fatalf("OnWorkstationBlockBroken(XP=0) = (id=%d, lost=%v), want (%d, true)", affectedID, lostProf, v1)
	}
	vs1, _ = srv.VillagerState(v1)
	if vs1.Profession != VillagerProfessionNone || len(vs1.Offers) != 0 {
		t.Fatalf("v1 after losing workstation at XP=0 = (prof=%q, offers=%d), want (minecraft:none, 0)", vs1.Profession, len(vs1.Offers))
	}

	// 4. Re-place lectern, let v1 claim it, grant v1 trade XP (> 0), then break lectern -> profession stays locked!
	srv.chunks.SetBlock(12, 65, 10, lecternState)
	srv.TickVillagers()
	vs1, _ = srv.VillagerState(v1)
	if vs1.Profession != VillagerProfessionLibrarian {
		t.Fatalf("expected TickVillagers to re-acquire lectern for v1, got %q", vs1.Profession)
	}
	srv.AddVillagerXP(v1, 5)

	srv.chunks.SetBlock(12, 65, 10, world.StateAir)
	_, lostProfLocked := srv.OnWorkstationBlockBroken(world.DimensionOverworld, 12, 65, 10)
	if lostProfLocked {
		t.Fatal("expected villager with XP > 0 to keep locked profession when workstation breaks")
	}
	vs1, _ = srv.VillagerState(v1)
	if vs1.Profession != VillagerProfessionLibrarian || vs1.XP != 5 || len(vs1.Offers) == 0 {
		t.Fatalf("expected locked librarian to retain profession/XP/offers, got %+v", vs1)
	}
	if !srv.IsVillagerJobSiteLost(v1) {
		t.Fatal("expected IsVillagerJobSiteLost(v1) to be true after workstation broke")
	}
}

func TestVillagerGossipTypesDecayCapsSharingAndReputationPricing(t *testing.T) {
	srv := newSurvivalTestServer(t)
	col := &survivalPacketCollector{}
	player, err := srv.RegisterPlayer(Profile{Name: "Gossiper", UUID: OfflineUUID("Gossiper")}, col.send)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	_ = srv.SetPlayerGameMode(player, GameModeSurvival)
	srv.SetPlayerHealth(player, 20.0, 20, 5.0)
	snap := player.Snapshot()

	v1, err := srv.SpawnVillager(snap.X+1.5, snap.Y, snap.Z, VillagerProfessionFarmer, 1)
	if err != nil {
		t.Fatalf("SpawnVillager(v1): %v", err)
	}
	v2, err := srv.SpawnVillager(snap.X+2.5, snap.Y, snap.Z, VillagerProfessionFarmer, 1)
	if err != nil {
		t.Fatalf("SpawnVillager(v2): %v", err)
	}

	uuid := player.Profile.UUID

	// 1. Verify Gossip caps & weights.
	if got := srv.AddVillagerGossip(v1, uuid, GossipMajorPositive, 50); got != 20 {
		t.Fatalf("major_positive cap = %d, want 20", got)
	}
	if got := srv.AddVillagerGossip(v1, uuid, GossipMinorPositive, 50); got != 25 {
		t.Fatalf("minor_positive cap = %d, want 25", got)
	}
	if got := srv.AddVillagerGossip(v1, uuid, GossipTrading, 50); got != 25 {
		t.Fatalf("trading cap = %d, want 25", got)
	}
	// Total reputation = 20*5 + 25*1 + 25*1 = 150.
	if rep := srv.VillagerPlayerReputation(v1, uuid); rep != 150 {
		t.Fatalf("VillagerPlayerReputation = %d, want 150", rep)
	}

	// 2. Verify daily gossip decay: major_positive (0 decay) stays 20, minor_positive (-1) -> 24, trading (-2) -> 23.
	srv.DecayVillagerGossips(v1)
	if got := srv.VillagerGossipValue(v1, uuid, GossipMajorPositive); got != 20 {
		t.Fatalf("major_positive after decay = %d, want 20 (permanent)", got)
	}
	if got := srv.VillagerGossipValue(v1, uuid, GossipMinorPositive); got != 24 {
		t.Fatalf("minor_positive after decay = %d, want 24", got)
	}
	if got := srv.VillagerGossipValue(v1, uuid, GossipTrading); got != 23 {
		t.Fatalf("trading after decay = %d, want 23", got)
	}

	// 3. Verify gossip sharing between v1 and v2:
	// major_positive (20 - 100 <= 0) is NOT transferred; minor_positive (24 - 5 = 19) and trading (23 - 20 = 3) ARE transferred.
	srv.ShareVillagerGossip(v1, v2)
	if got := srv.VillagerGossipValue(v2, uuid, GossipMajorPositive); got != 0 {
		t.Fatalf("v2 major_positive after share = %d, want 0", got)
	}
	if got := srv.VillagerGossipValue(v2, uuid, GossipMinorPositive); got != 19 {
		t.Fatalf("v2 minor_positive after share = %d, want 19", got)
	}
	if got := srv.VillagerGossipValue(v2, uuid, GossipTrading); got != 3 {
		t.Fatalf("v2 trading after share = %d, want 3", got)
	}

	// 4. Verify reputation-based prices in trading UI and negative reputation when attacking a villager.
	srv.SetVillagerState(v2, VillagerState{
		EntityID:     v2,
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
				PriceMultiplier: 0.2,
			},
		},
	})
	// Currently v2 has reputation = 19 + 3 = +22 -> SpecialPriceDiff = -floor(22 * 0.2) = -4 -> AdjustedCostA = 16 wheat.
	srv.UpdateVillagerSpecialPricesForPlayer(player, v2)
	vs2, _ := srv.VillagerState(v2)
	if got := vs2.Offers[0].AdjustedCostA().Count; got != 16 {
		t.Fatalf("discounted cost with +22 rep = %d, want 16", got)
	}

	// Player attacks v1 -> v1 gets +25 minor_negative and witnessing v2 (within 16 blocks) gets +15 minor_negative,
	// and killing v1 gives witnessing v2 +25 major_negative (-125 rep).
	srv.OnVillagerHurtByPlayer(v1, player)
	if got := srv.VillagerGossipValue(v1, uuid, GossipMinorNegative); got != 25 {
		t.Fatalf("v1 minor_negative after hurt = %d, want 25", got)
	}
	if got := srv.VillagerGossipValue(v2, uuid, GossipMinorNegative); got != 15 {
		t.Fatalf("witness v2 minor_negative after hurt = %d, want 15", got)
	}
	srv.OnVillagerKilledByPlayer(v1, world.DimensionOverworld, snap.X+1.5, snap.Y, snap.Z, player)
	if got := srv.VillagerGossipValue(v2, uuid, GossipMajorNegative); got != 25 {
		t.Fatalf("witness v2 major_negative after kill = %d, want 25", got)
	}

	// Now v2 reputation = +22 - 15 - (25*5) = -118 -> SpecialPriceDiff = -floor(-118 * 0.2) = +24 -> AdjustedCostA = 44 wheat!
	srv.UpdateVillagerSpecialPricesForPlayer(player, v2)
	vs2, _ = srv.VillagerState(v2)
	if got := vs2.Offers[0].AdjustedCostA().Count; got != 44 {
		t.Fatalf("surcharged cost with -118 rep = %d, want 44", got)
	}
}

func TestVillagerTwiceDailyRestockAndDemandAdjustment(t *testing.T) {
	srv := newSurvivalTestServer(t)
	vid, err := srv.SpawnVillager(0.5, 65.0, 0.5, VillagerProfessionFarmer, 1)
	if err != nil {
		t.Fatalf("SpawnVillager: %v", err)
	}

	srv.SetVillagerState(vid, VillagerState{
		EntityID:       vid,
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
				XP:              2,
				PriceMultiplier: 0.05,
			},
		},
	})

	// Restock 1 at day 0, tick 2500 (work hours 2000..9000):
	// Uses resets to 0, Demand becomes 0 + 16 - (16 - 16) = +16 -> price increases by floor(20 * 16 * 0.05) = +16 -> 36!
	srv.SetWorldTime(2500, 2500)
	srv.TickVillagers()
	vs, _ := srv.VillagerState(vid)
	if vs.Offers[0].Uses != 0 || vs.RestocksToday != 1 || vs.Offers[0].Demand != 16 {
		t.Fatalf("after 1st restock: uses=%d restocksToday=%d demand=%d, want 0, 1, 16",
			vs.Offers[0].Uses, vs.RestocksToday, vs.Offers[0].Demand)
	}
	if gotCost := vs.Offers[0].AdjustedCostA().Count; gotCost != 36 {
		t.Fatalf("AdjustedCostA after sold-out restock demand surge = %d, want 36", gotCost)
	}

	// Exhaust offer again and advance to tick 5500 (>= 2400 ticks later, still in work hours) -> Restock 2!
	vs.Offers[0].Uses = 16
	srv.SetVillagerState(vid, vs)
	srv.SetWorldTime(5500, 5500)
	srv.TickVillagers()
	vs, _ = srv.VillagerState(vid)
	if vs.Offers[0].Uses != 0 || vs.RestocksToday != 2 {
		t.Fatalf("after 2nd restock: uses=%d restocksToday=%d, want 0, 2", vs.Offers[0].Uses, vs.RestocksToday)
	}

	// Exhaust offer a 3rd time on the same day (tick 8500): must NOT restock (cap = 2 per day).
	vs.Offers[0].Uses = 16
	srv.SetVillagerState(vid, vs)
	srv.SetWorldTime(8500, 8500)
	srv.TickVillagers()
	vs, _ = srv.VillagerState(vid)
	if !vs.Offers[0].IsOutOfStock() || vs.RestocksToday != 2 {
		t.Fatalf("expected 3rd restock on same day to be blocked; uses=%d restocksToday=%d", vs.Offers[0].Uses, vs.RestocksToday)
	}

	// Advance to next day work hours (day 1, tick 24000+3000 = 27000) -> RestocksToday resets and Villager restocks!
	srv.SetWorldTime(27000, 27000)
	srv.TickVillagers()
	vs, _ = srv.VillagerState(vid)
	if vs.Offers[0].Uses != 0 || vs.RestocksToday != 1 {
		t.Fatalf("expected restock on new day; uses=%d restocksToday=%d", vs.Offers[0].Uses, vs.RestocksToday)
	}
}

func TestZombieVillagerCuringAndPermanentDiscounts(t *testing.T) {
	srv := newSurvivalTestServer(t)
	col := &survivalPacketCollector{}
	player, err := srv.RegisterPlayer(Profile{Name: "Healer", UUID: OfflineUUID("Healer")}, col.send)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	_ = srv.SetPlayerGameMode(player, GameModeSurvival)
	srv.SetPlayerHealth(player, 20.0, 20, 5.0)
	snap := player.Snapshot()

	// Spawn a villager and a nearby witness villager, then zombify the first villager.
	origID, err := srv.SpawnVillager(snap.X+1.5, snap.Y, snap.Z, VillagerProfessionLibrarian, 2)
	if err != nil {
		t.Fatalf("SpawnVillager: %v", err)
	}
	witnessID, err := srv.SpawnVillager(snap.X+3.0, snap.Y, snap.Z, VillagerProfessionFarmer, 1)
	if err != nil {
		t.Fatalf("SpawnVillager(witness): %v", err)
	}

	srv.SetVillagerState(origID, VillagerState{
		EntityID:     origID,
		VillagerType: VillagerTypePlains,
		Profession:   VillagerProfessionLibrarian,
		Level:        2,
		XP:           15,
		Offers: []MerchantOffer{
			{
				Name:            "minecraft:librarian/1/emerald_book",
				BaseCostA:       world.NewNamedItemStack("minecraft:emerald", 20),
				CostB:           world.NewNamedItemStack("minecraft:book", 1),
				Result:          world.NewNamedItemStack("minecraft:enchanted_book", 1),
				MaxUses:         12,
				XP:              5,
				PriceMultiplier: 0.2,
			},
		},
	})

	zvID, ok := srv.ZombifyVillager(origID)
	if !ok || zvID <= 0 {
		t.Fatal("expected ZombifyVillager to succeed")
	}

	// Give player 2 golden apples in hotbar slot 0.
	hotbar0 := world.HotbarContainerOffset + int(player.Snapshot().SelectedSlot)
	player.SetInventorySlot(hotbar0, world.NewNamedItemStack("minecraft:golden_apple", 2))

	// Without Weakness, right-clicking with golden apple does nothing.
	if handled, _, _ := srv.InteractWithEntity(player, zvID); handled {
		t.Fatal("expected InteractWithZombieVillager without Weakness to fail")
	}

	// Apply Weakness and right-click with golden apple -> starts curing conversion and consumes 1 golden apple.
	srv.SetZombieVillagerWeakened(zvID, true)
	handled, _, err := srv.InteractWithEntity(player, zvID)
	if err != nil || !handled {
		t.Fatalf("InteractWithEntity(zombie_villager with weakness + golden_apple) = (%v, %v), want (true, nil)", handled, err)
	}
	if gotApples := player.InventorySlot(hotbar0).Count; gotApples != 1 {
		t.Fatalf("golden apples remaining = %d, want 1", gotApples)
	}
	zvState, ok := srv.ZombieVillagerState(zvID)
	if !ok || !zvState.Converting || zvState.ConversionTicks < ZombieVillagerMinCureTicks {
		t.Fatalf("ZombieVillagerState after golden apple = %+v, expected Converting=true", zvState)
	}

	// Fast-forward conversion timer to 1 tick and tick once.
	srv.SetZombieVillagerConversionTicks(zvID, 1)
	srv.TickZombieVillagers(rand.New(rand.NewSource(1)))

	// Verify zombie_villager was replaced by a cured minecraft:villager preserving Level 2, XP 15, and Offers.
	if srv.entities.Get(zvID) != nil {
		t.Fatal("expected zombie_villager entity to be removed upon cure completion")
	}
	var curedID int32
	for _, ent := range srv.entities.AppendAll(nil) {
		if ent.TypeName == MobTypeVillager && ent.ID != witnessID {
			curedID = ent.ID
			break
		}
	}
	if curedID <= 0 {
		t.Fatal("expected cured villager entity to be spawned")
	}

	// Check curing gossip on cured villager (+20 major_positive, +25 minor_positive -> +125 reputation)
	// and witness villager (+20 minor_positive).
	uuid := player.Profile.UUID
	if got := srv.VillagerGossipValue(curedID, uuid, GossipMajorPositive); got != 20 {
		t.Fatalf("cured villager major_positive = %d, want 20", got)
	}
	if got := srv.VillagerGossipValue(curedID, uuid, GossipMinorPositive); got != 25 {
		t.Fatalf("cured villager minor_positive = %d, want 25", got)
	}
	if rep := srv.VillagerPlayerReputation(curedID, uuid); rep != 125 {
		t.Fatalf("cured villager reputation = %d, want 125", rep)
	}
	if gotWitness := srv.VillagerGossipValue(witnessID, uuid, GossipMinorPositive); gotWitness != 20 {
		t.Fatalf("witness villager minor_positive = %d, want 20", gotWitness)
	}

	// Open merchant UI with cured villager: 20 emeralds with PriceMultiplier=0.2 and rep=125
	// gets SpecialPriceDiff = -25 -> AdjustedCostA clamped to 1 emerald!
	opened, cid, err := srv.InteractWithVillager(player, curedID)
	if err != nil || !opened || cid <= 0 {
		t.Fatalf("InteractWithVillager(curedID) = (%v, %d, %v)", opened, cid, err)
	}
	curedState, _ := srv.VillagerState(curedID)
	if gotCost := curedState.Offers[0].AdjustedCostA().Count; gotCost != 1 {
		t.Fatalf("cured villager discounted emerald cost = %d, want 1", gotCost)
	}
	_ = srv.CloseMerchantContainer(player, cid, false)
}
