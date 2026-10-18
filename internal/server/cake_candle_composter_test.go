package server

import (
	"testing"

	"regionio/internal/world"
)

func TestCakeCandleAndCandleCakeInteractionsAndComparators(t *testing.T) {
	if got := len(world.AllCandleBlockNames()); got != 17 {
		t.Fatalf("AllCandleBlockNames count = %d, want 17", got)
	}
	if got := len(world.AllCandleCakeBlockNames()); got != 17 {
		t.Fatalf("AllCandleCakeBlockNames count = %d, want 17", got)
	}

	dim := world.DimensionOverworld
	s, cache := newBeaconTestServer(t)
	session := newBeaconTestPlayer(s, "CakeTester", 16.5, 64.0, 16.5)

	x, y, z := 16, 64, 16
	cache.SetBlock(x, y-1, z, world.StateStone)
	cache.SetBlock(x+1, y-1, z, world.StateStone)

	// Place a comparator at (x+1, y, z) facing "west" (input at x, y, z).
	compState, ok := world.BlockStateIDByProperties("minecraft:comparator", map[string]string{
		"facing":  "west",
		"mode":    "compare",
		"powered": "false",
	})
	if !ok {
		t.Fatalf("missing comparator state")
	}
	cache.ApplyBlockWithUpdates(x+1, y, z, compState)

	// 1. Place uneaten cake[bites=0] at (x, y, z) -> comparator signal = 14.
	cake0 := world.MakeCakeBlockState(0)
	cache.ApplyBlockWithUpdates(x, y, z, cake0)
	cache.FlushBlockUpdates(16)
	if sig := cache.ComparatorOutputSignalAt(x+1, y, z); sig != 14 {
		t.Fatalf("comparator signal for cake[bites=0] = %d, want 14", sig)
	}

	// 2. Place red_candle on cake[bites=0] -> red_candle_cake[lit=false], comparator signal = 14.
	slot := world.HotbarContainerOffset
	session.SetInventorySlot(slot, world.NewNamedItemStack("minecraft:red_candle", 2))
	act, handled := s.InteractWithCakeCandleOrComposter(session, dim, x, y, z, 0)
	if !handled || act != "place_candle" {
		t.Fatalf("place candle on cake: handled=%v act=%q, want true/\"place_candle\"", handled, act)
	}
	if got := world.BlockName(cache.GetBlock(x, y, z)); got != "minecraft:red_candle_cake" {
		t.Fatalf("block after placing red_candle = %s, want minecraft:red_candle_cake", got)
	}
	if gotCount := session.InventorySlot(slot).Count; gotCount != 1 {
		t.Fatalf("held red_candle count = %d, want 1", gotCount)
	}
	cache.FlushBlockUpdates(16)
	if sig := cache.ComparatorOutputSignalAt(x+1, y, z); sig != 14 {
		t.Fatalf("comparator signal for red_candle_cake = %d, want 14", sig)
	}

	// 3. Light red_candle_cake with flint_and_steel -> lit=true, light emission = 3.
	session.SetInventorySlot(slot, world.NewNamedItemStack("minecraft:flint_and_steel", 1))
	act, handled = s.InteractWithCakeCandleOrComposter(session, dim, x, y, z, 0)
	if !handled || act != "light" {
		t.Fatalf("light candle_cake: handled=%v act=%q, want true/\"light\"", handled, act)
	}
	if !world.IsCandleOrCandleCakeLit(cache.GetBlock(x, y, z)) || world.CandleLightEmission(cache.GetBlock(x, y, z)) != 3 {
		t.Fatalf("expected lit red_candle_cake with light emission 3")
	}

	// 4. Extinguish lit red_candle_cake with empty hand -> lit=false.
	session.SetInventorySlot(slot, world.ItemStack{})
	act, handled = s.InteractWithCakeCandleOrComposter(session, dim, x, y, z, 0)
	if !handled || act != "extinguish" || world.IsCandleOrCandleCakeLit(cache.GetBlock(x, y, z)) {
		t.Fatalf("extinguish candle_cake: handled=%v act=%q", handled, act)
	}

	// 5. Make player hungry (food=6, sat=0) and eat the red_candle_cake:
	// converts to cake[bites=1], restores +2 food / +0.4 sat, drops red_candle, comparator = 12.
	session.mu.Lock()
	session.foodLevel = 6
	session.foodSaturation = 0
	session.mu.Unlock()

	act, handled = s.InteractWithCakeCandleOrComposter(session, dim, x, y, z, 0)
	if !handled || act != "eat" {
		t.Fatalf("eat candle_cake: handled=%v act=%q, want true/\"eat\"", handled, act)
	}
	if bites, ok := world.CakeBites(cache.GetBlock(x, y, z)); !ok || bites != 1 {
		t.Fatalf("after eating candle_cake, bites=%d ok=%v, want 1/true", bites, ok)
	}
	if gotFood := session.FoodLevel(); gotFood != 8 {
		t.Fatalf("player food after eating 1 slice = %d, want 8", gotFood)
	}
	cache.FlushBlockUpdates(16)
	if sig := cache.ComparatorOutputSignalAt(x+1, y, z); sig != 12 {
		t.Fatalf("comparator signal for cake[bites=1] = %d, want 12", sig)
	}

	// 6. Eat the remaining 6 slices (bites 1 -> 2 -> 3 -> 4 -> 5 -> 6 -> StateAir).
	for b := 2; b <= 7; b++ {
		session.mu.Lock()
		session.foodLevel = 6
		session.mu.Unlock()
		act, handled = s.InteractWithCakeCandleOrComposter(session, dim, x, y, z, 0)
		if !handled || act != "eat" {
			t.Fatalf("eat slice %d failed: handled=%v act=%q", b, handled, act)
		}
		cache.FlushBlockUpdates(16)
		if b <= 6 {
			wantSig := (7 - b) * 2
			if sig := cache.ComparatorOutputSignalAt(x+1, y, z); sig != wantSig {
				t.Fatalf("comparator signal for cake[bites=%d] = %d, want %d", b, sig, wantSig)
			}
		} else {
			if got := cache.GetBlock(x, y, z); got != world.StateAir {
				t.Fatalf("after 7th slice eaten, block = %s, want minecraft:air", world.BlockName(got))
			}
		}
	}
}

func TestCandleClusterLightingAndWaterlogging(t *testing.T) {
	dim := world.DimensionOverworld
	s, cache := newBeaconTestServer(t)
	session := newBeaconTestPlayer(s, "CandleTester", 24.5, 64.0, 24.5)

	x, y, z := 24, 64, 24
	cache.SetBlock(x, y-1, z, world.StateStone)

	c1, ok := world.MakeCandleBlockState("minecraft:cyan_candle", 1, false, false)
	if !ok {
		t.Fatalf("missing minecraft:cyan_candle state")
	}
	cache.ApplyBlockWithUpdates(x, y, z, c1)

	slot := world.HotbarContainerOffset
	session.SetInventorySlot(slot, world.NewNamedItemStack("minecraft:cyan_candle", 3))
	for want := 2; want <= 4; want++ {
		act, handled := s.InteractWithCandleBlock(session, dim, x, y, z, 0)
		if !handled || act != "add_candle" {
			t.Fatalf("add candle to %d: handled=%v act=%q", want, handled, act)
		}
		if got := world.CandleCount(cache.GetBlock(x, y, z)); got != want {
			t.Fatalf("candle count = %d, want %d", got, want)
		}
	}

	// Light the 4-candle cluster -> light emission = 3 * 4 = 12.
	session.SetInventorySlot(slot, world.NewNamedItemStack("minecraft:flint_and_steel", 1))
	if act, handled := s.InteractWithCandleBlock(session, dim, x, y, z, 0); !handled || act != "light" {
		t.Fatalf("light 4-candle cluster failed: handled=%v act=%q", handled, act)
	}
	if em := world.CandleLightEmission(cache.GetBlock(x, y, z)); em != 12 {
		t.Fatalf("CandleLightEmission for 4 lit candles = %d, want 12", em)
	}

	// Waterlogging a lit candle automatically extinguishes it.
	wlCandle, _ := world.MakeCandleBlockState("minecraft:cyan_candle", 4, true, true)
	if world.IsCandleOrCandleCakeLit(wlCandle) {
		t.Fatalf("waterlogged candle should not remain lit")
	}
}

func TestComposterPlayerInteractionAndMaturation(t *testing.T) {
	dim := world.DimensionOverworld
	s, cache := newBeaconTestServer(t)
	session := newBeaconTestPlayer(s, "CompostTester", 32.5, 64.0, 32.5)

	x, y, z := 32, 64, 32
	cache.SetBlock(x, y-1, z, world.StateStone)
	cache.ApplyBlockWithUpdates(x, y, z, world.MakeComposterBlockState(0))

	slot := world.HotbarContainerOffset
	// Pumpkin pie has 100% compost chance -> 7 uses fill composter from level 0 to level 7.
	session.SetInventorySlot(slot, world.NewNamedItemStack("minecraft:pumpkin_pie", 7))
	for wantLvl := 1; wantLvl <= 7; wantLvl++ {
		act, _, ok := s.InteractWithComposter(session, dim, x, y, z, 0)
		if !ok || act != "fill_success" {
			t.Fatalf("fill step %d: ok=%v act=%q", wantLvl, ok, act)
		}
		if gotLvl, _ := world.ComposterLevel(cache.GetBlock(x, y, z)); gotLvl != wantLvl {
			t.Fatalf("composter level = %d, want %d", gotLvl, wantLvl)
		}
	}

	// Step 20 world block-update ticks -> level=7 matures into level=8!
	cache.StepBlockUpdates(world.ComposterReadyTickDelay)
	if gotLvl, _ := world.ComposterLevel(cache.GetBlock(x, y, z)); gotLvl != 8 {
		t.Fatalf("after 20 ticks, composter level = %d, want 8", gotLvl)
	}

	// Right-click at level=8 harvests 1x bone_meal and resets to level=0.
	act, harvested, ok := s.InteractWithComposter(session, dim, x, y, z, 0)
	if !ok || act != "harvest" || harvested.ResolvedID() != "minecraft:bone_meal" || harvested.Count != 1 {
		t.Fatalf("harvest composter: ok=%v act=%q item=%+v", ok, act, harvested)
	}
	if gotLvl, _ := world.ComposterLevel(cache.GetBlock(x, y, z)); gotLvl != 0 {
		t.Fatalf("after harvest, composter level = %d, want 0", gotLvl)
	}
}

func TestSculkShriekerWardenPresentSuppressionAndPlayerPlacedState(t *testing.T) {
	dim := world.DimensionOverworld
	s, cache := newBeaconTestServer(t)
	session := newBeaconTestPlayer(s, "ShriekerTester", 40.5, 64.0, 40.5)

	x, y, z := 40, 64, 40
	// Player-placed shrieker has can_summon=false.
	placed := ResolvePlayerPlacedSculkShriekerState(false)
	canSummon, shrieking, _, ok := ParseSculkShriekerState(placed)
	if !ok || canSummon || shrieking {
		t.Fatalf("ResolvePlayerPlacedSculkShriekerState = canSummon:%v shrieking:%v ok:%v", canSummon, shrieking, ok)
	}

	// Worldgen shrieker with can_summon=true when a Warden is already within 48 blocks:
	// SetWardenPresentHook returns true -> suppresses warning level escalation.
	cache.SetBlock(x, y, z, SculkShriekerBlockState(true, false, false))
	session.SetPlayerWarningLevel(1)
	s.SetWardenPresentHook(func(d string, wx, wy, wz float64, radius float64) bool {
		return true
	})
	defer s.SetWardenPresentHook(nil)

	if !s.TriggerSculkShriekerAt(dim, x, y, z, session) {
		t.Fatalf("expected TriggerSculkShriekerAt to shriek")
	}
	if gotLvl := session.PlayerWarningLevel(); gotLvl != 1 {
		t.Fatalf("warning level escalated to %d despite Warden present within 48 blocks, want 1", gotLvl)
	}
	s.OnBreakSculkShriekerAt(dim, x, y, z, cache.GetBlock(x, y, z))
}
