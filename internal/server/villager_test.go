package server

import (
	"math"
	"math/rand"
	"testing"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

func TestVillagerProfessionsTypesAndEntityDataPacket(t *testing.T) {
	profs := AllVillagerProfessions()
	if len(profs) != 15 {
		t.Fatalf("len(AllVillagerProfessions()) = %d, want 15", len(profs))
	}
	for id, name := range profs {
		if got := VillagerProfessionID(name); got != int32(id) {
			t.Fatalf("VillagerProfessionID(%q) = %d, want %d", name, got, id)
		}
		if got := VillagerProfessionByID(int32(id)); got != name {
			t.Fatalf("VillagerProfessionByID(%d) = %q, want %q", id, got, name)
		}
	}
	if CanProfessionTrade(VillagerProfessionNone) || CanProfessionTrade(VillagerProfessionNitwit) {
		t.Fatal("expected none and nitwit professions not to trade")
	}
	for _, tp := range TradingVillagerProfessions() {
		if !CanProfessionTrade(tp) {
			t.Fatalf("expected %q to be a trading profession", tp)
		}
	}

	pkt := EncodeVillagerEntityDataPacket(42, VillagerTypeID(VillagerTypeSnow), VillagerProfessionID(VillagerProfessionLibrarian), 4)
	eid, vtype, vprof, vlevel, err := DecodeVillagerEntityDataPacket(protocol.NewReader(pkt))
	if err != nil {
		t.Fatalf("DecodeVillagerEntityDataPacket: %v", err)
	}
	if eid != 42 || vtype != VillagerTypeID(VillagerTypeSnow) || vprof != VillagerProfessionID(VillagerProfessionLibrarian) || vlevel != 4 {
		t.Fatalf("decoded villager data = (%d, %d, %d, %d), want (42, %d, %d, 4)",
			eid, vtype, vprof, vlevel, VillagerTypeID(VillagerTypeSnow), VillagerProfessionID(VillagerProfessionLibrarian))
	}
}

func TestVillagerXPLevelUpUnlocksNewTradesAndBroadcastsMetadata(t *testing.T) {
	srv := newSurvivalTestServer(t)
	col := &survivalPacketCollector{}
	session, err := srv.RegisterPlayer(Profile{Name: "Observer", UUID: OfflineUUID("Observer")}, col.send)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	snap := session.Snapshot()

	vid, err := srv.SpawnVillager(snap.X+2.0, snap.Y, snap.Z, VillagerProfessionArmorer, 1)
	if err != nil {
		t.Fatalf("SpawnVillager: %v", err)
	}
	vs, ok := srv.VillagerState(vid)
	if !ok || vs.Level != 1 || len(vs.Offers) != 2 {
		t.Fatalf("initial villager state = (level=%d, offers=%d), want (1, 2)", vs.Level, len(vs.Offers))
	}

	// Walk through each vanilla XP threshold: 10 (L2), 70 (L3), 150 (L4), 250 (L5).
	steps := []struct {
		addXP     int32
		wantLevel int
		minOffers int
	}{
		{addXP: 10, wantLevel: 2, minOffers: 4},
		{addXP: 60, wantLevel: 3, minOffers: 6},
		{addXP: 80, wantLevel: 4, minOffers: 8},
		{addXP: 100, wantLevel: 5, minOffers: 10},
	}

	for _, st := range steps {
		col.reset()
		newLvl, leveled := srv.AddVillagerXP(vid, st.addXP)
		if !leveled || newLvl != st.wantLevel {
			t.Fatalf("AddVillagerXP(+%d) = (level=%d, leveled=%v), want (%d, true)", st.addXP, newLvl, leveled, st.wantLevel)
		}
		vs, _ = srv.VillagerState(vid)
		if len(vs.Offers) < st.minOffers {
			t.Fatalf("level %d offers count = %d, want >= %d", newLvl, len(vs.Offers), st.minOffers)
		}
		if col.count(PlaySetEntityDataCB) == 0 || col.count(protocol.PlayEntityEvent) == 0 {
			t.Fatalf("expected PlaySetEntityDataCB and PlayEntityEvent on level-up to %d", newLvl)
		}
	}
}

func TestVillagerDailyRestockAndDemandPricing(t *testing.T) {
	srv := newSurvivalTestServer(t)
	vid, err := srv.SpawnVillager(8.5, float64(world.FlatSurfaceY+1), 8.5, VillagerProfessionFarmer, 1)
	if err != nil {
		t.Fatalf("SpawnVillager: %v", err)
	}

	srv.SetVillagerState(vid, VillagerState{
		EntityID:     vid,
		VillagerType: VillagerTypePlains,
		Profession:   VillagerProfessionFarmer,
		Level:        1,
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
		LastRestockDay: -1,
	})

	// 1. At night (14000), TickVillagers should NOT restock.
	srv.SetWorldTime(14000, 14000)
	srv.TickVillagers()
	vs, _ := srv.VillagerState(vid)
	if !vs.Offers[0].IsOutOfStock() {
		t.Fatal("expected villager not to restock at night (14000)")
	}

	// 2. During work hours on day 1 (gameTime=27000, dayTime=27000 -> tod=3000), TickVillagers restocks!
	srv.SetWorldTime(27000, 27000)
	srv.TickVillagers()
	vs, _ = srv.VillagerState(vid)
	if vs.Offers[0].Uses != 0 || vs.RestocksToday != 1 {
		t.Fatalf("after 1st work-hour restock: uses=%d restocksToday=%d, want 0, 1", vs.Offers[0].Uses, vs.RestocksToday)
	}
	if vs.Offers[0].Demand <= 0 || vs.Offers[0].AdjustedCostA().Count <= 20 {
		t.Fatalf("expected positive demand after selling out before restock; demand=%d adjustedCost=%d",
			vs.Offers[0].Demand, vs.Offers[0].AdjustedCostA().Count)
	}

	// 3. Exhaust again and advance past MinRestockIntervalTicks (2400) within work hours (tod=6000) -> 2nd restock.
	vs.Offers[0].Uses = 16
	srv.SetVillagerState(vid, vs)
	srv.SetWorldTime(30000, 30000)
	srv.TickVillagers()
	vs, _ = srv.VillagerState(vid)
	if vs.Offers[0].Uses != 0 || vs.RestocksToday != 2 {
		t.Fatalf("after 2nd work-hour restock: uses=%d restocksToday=%d, want 0, 2", vs.Offers[0].Uses, vs.RestocksToday)
	}

	// 4. Exhaust a 3rd time on the same day: capped at MaxVillagerRestocksPerDay (2).
	vs.Offers[0].Uses = 16
	srv.SetVillagerState(vid, vs)
	srv.SetWorldTime(32500, 32500) // tod=8500
	srv.TickVillagers()
	vs, _ = srv.VillagerState(vid)
	if !vs.Offers[0].IsOutOfStock() || vs.RestocksToday != 2 {
		t.Fatalf("expected 3rd restock on same day to be blocked; uses=%d restocksToday=%d", vs.Offers[0].Uses, vs.RestocksToday)
	}

	// 5. Next day during work hours (dayTime=51000 -> tod=3000): daily counter resets and villager restocks!
	srv.SetWorldTime(51000, 51000)
	srv.TickVillagers()
	vs, _ = srv.VillagerState(vid)
	if vs.Offers[0].Uses != 0 || vs.RestocksToday != 1 {
		t.Fatalf("after next-day restock: uses=%d restocksToday=%d, want 0, 1", vs.Offers[0].Uses, vs.RestocksToday)
	}
}

func TestVillagerWanderAIAndEntityTrackerMetadataSync(t *testing.T) {
	srv := newSurvivalTestServer(t)
	col := &survivalPacketCollector{}
	session, err := srv.RegisterPlayer(Profile{Name: "Viewer", UUID: OfflineUUID("Viewer")}, col.send)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	snap := session.Snapshot()

	startX, startY, startZ := snap.X+2.5, float64(world.FlatSurfaceY+1), snap.Z+2.5
	vid, err := srv.SpawnVillager(startX, startY, startZ, VillagerProfessionCartographer, 2)
	if err != nil {
		t.Fatalf("SpawnVillager: %v", err)
	}
	col.reset()

	rng := rand.New(rand.NewSource(99))
	moved := false
	for i := 0; i < 80; i++ {
		srv.TickEntities(rng)
		ent := srv.entities.Get(vid)
		if ent == nil {
			t.Fatal("villager entity disappeared")
		}
		if math.Hypot(ent.X-startX, ent.Z-startZ) > 0.05 {
			moved = true
		}
	}
	if !moved {
		t.Fatal("expected villager to wander using stepWanderAI")
	}
	if col.count(protocol.PlayAddEntity) == 0 || col.count(PlaySetEntityDataCB) == 0 {
		t.Fatalf("expected EntityTracker.Sync to send PlayAddEntity (%d) and PlaySetEntityDataCB (%d)",
			col.count(protocol.PlayAddEntity), col.count(PlaySetEntityDataCB))
	}
}

func TestVillagerRightClickViaInteractPacketAndNitwitRefusal(t *testing.T) {
	srv := newSurvivalTestServer(t)
	col := &survivalPacketCollector{}
	session, err := srv.RegisterPlayer(Profile{Name: "Player1", UUID: OfflineUUID("Player1")}, col.send)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	snap := session.Snapshot()

	// 1. Nitwit villager refuses trade on right-click.
	nitwitID, err := srv.SpawnVillager(snap.X+1.5, snap.Y, snap.Z, VillagerProfessionNitwit, 1)
	if err != nil {
		t.Fatalf("SpawnVillager(nitwit): %v", err)
	}
	col.reset()
	w := protocol.NewWriter(8)
	w.VarInt(nitwitID)
	w.VarInt(InteractActionInteract)
	w.VarInt(0) // main hand
	w.Bool(false)
	if err := srv.HandleInteractPacket(session, protocol.NewReader(w.Bytes())); err != nil {
		t.Fatalf("HandleInteractPacket(nitwit): %v", err)
	}
	if session.OpenContainer() != nil || col.count(protocol.PlayOpenScreen) != 0 {
		t.Fatal("expected nitwit villager not to open merchant screen")
	}
	if col.count(PlaySoundPacketID) == 0 {
		t.Fatal("expected nitwit villager to play entity.villager.no sound")
	}

	// 2. Librarian villager opens merchant screen on right-click via HandleInteractPacket.
	libID, err := srv.SpawnVillager(snap.X+1.5, snap.Y, snap.Z+1.0, VillagerProfessionLibrarian, 1)
	if err != nil {
		t.Fatalf("SpawnVillager(librarian): %v", err)
	}
	col.reset()
	w2 := protocol.NewWriter(8)
	w2.VarInt(libID)
	w2.VarInt(InteractActionInteract)
	w2.VarInt(0) // main hand
	w2.Bool(false)
	if err := srv.HandleInteractPacket(session, protocol.NewReader(w2.Bytes())); err != nil {
		t.Fatalf("HandleInteractPacket(librarian): %v", err)
	}
	open := session.OpenContainer()
	if open == nil || open.MenuType != MenuTypeMerchant {
		t.Fatalf("expected merchant menu to open on right-click, got %+v", open)
	}
}
