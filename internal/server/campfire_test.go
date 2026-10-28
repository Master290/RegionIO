package server

import (
	"math"
	"testing"

	"regionio/internal/world"
)

func placeCampfireBlockForTest(t *testing.T, srv *Server, itemName string, x, y, z int) uint16 {
	t.Helper()
	itemID, ok := world.ItemNameToID(itemName)
	if !ok {
		t.Fatalf("ItemNameToID(%q) failed", itemName)
	}
	state, ok := world.ItemToBlock(itemID)
	if !ok {
		t.Fatalf("ItemToBlock(%q) failed", itemName)
	}
	srv.Chunks().ApplyBlockWithUpdates(x, y, z, state)
	return srv.Chunks().GetBlock(x, y, z)
}

func TestCampfireCookingRecipesAndFourSlotLifecycle(t *testing.T) {
	// 1. Verify all 9 vanilla 26.1.2 campfire_cooking recipes load with 600-tick cook time.
	wantRecipes := map[string]string{
		"minecraft:beef":     "minecraft:cooked_beef",
		"minecraft:porkchop": "minecraft:cooked_porkchop",
		"minecraft:chicken":  "minecraft:cooked_chicken",
		"minecraft:mutton":   "minecraft:cooked_mutton",
		"minecraft:rabbit":   "minecraft:cooked_rabbit",
		"minecraft:cod":      "minecraft:cooked_cod",
		"minecraft:salmon":   "minecraft:cooked_salmon",
		"minecraft:potato":   "minecraft:baked_potato",
		"minecraft:kelp":     "minecraft:dried_kelp",
	}
	for rawName, wantCooked := range wantRecipes {
		rec, ok := MatchCampfireRecipe(world.NewNamedItemStack(rawName, 1))
		if !ok || rec == nil {
			t.Fatalf("MatchCampfireRecipe(%q) returned false", rawName)
		}
		if rec.Result.ResolvedID() != wantCooked {
			t.Fatalf("MatchCampfireRecipe(%q) result = %q, want %q", rawName, rec.Result.ResolvedID(), wantCooked)
		}
		if rec.CookingTime != 600 {
			t.Fatalf("MatchCampfireRecipe(%q) CookingTime = %d, want 600", rawName, rec.CookingTime)
		}
	}
	if CanCookOnCampfire(world.NewNamedItemStack("minecraft:raw_iron", 1)) {
		t.Fatal("expected raw_iron not to be cookable on a campfire")
	}

	// 2. Place 4 raw food items onto a lit campfire via player interaction, verify 5th is rejected,
	//    and tick 600 times to pop 4 cooked items into the world.
	srv := newSurvivalTestServer(t)
	col := &survivalPacketCollector{}
	session, err := srv.RegisterPlayer(Profile{Name: "CampChef", UUID: OfflineUUID("CampChef")}, col.send)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	if err := srv.SetPlayerGameMode(session, GameModeSurvival); err != nil {
		t.Fatalf("SetPlayerGameMode: %v", err)
	}

	dim := world.DimensionOverworld
	cx, cy, cz := 15, world.FlatSurfaceY+1, 15
	placeCampfireBlockForTest(t, srv, "minecraft:campfire", cx, cy, cz)

	session.SetInventorySlot(world.HotbarContainerOffset, world.NewNamedItemStack("minecraft:beef", 5))
	for i := 0; i < CampfireSlotCount; i++ {
		act, ok := srv.InteractCampfire(session, cx, cy, cz)
		if !ok || act != CampfireActionPlacedFood {
			t.Fatalf("slot %d InteractCampfire = (%v, %v), want (PlacedFood, true)", i, act, ok)
		}
	}
	// 5th item should be rejected because all 4 slots are full.
	if act, ok := srv.InteractCampfire(session, cx, cy, cz); ok || act != CampfireActionNone {
		t.Fatalf("5th InteractCampfire on full campfire = (%v, %v), want (None, false)", act, ok)
	}
	if got := session.HeldItem().Count; got != 1 {
		t.Fatalf("held beef count after placing 4 = %d, want 1", got)
	}

	// Tick 599 times: items are still cooking on the campfire.
	for i := 0; i < 599; i++ {
		srv.TickCampfires()
	}
	slots := srv.CampfireSlots(dim, cx, cy, cz)
	for i := 0; i < CampfireSlotCount; i++ {
		if slots[i].ResolvedID() != "minecraft:beef" {
			t.Fatalf("slot %d after 599 ticks = %+v, want beef", i, slots[i])
		}
	}

	// 600th tick completes cooking all 4 slots and pops 4x cooked_beef into the world.
	cooked, changed := srv.TickCampfireAt(dim, cx, cy, cz)
	if !changed || len(cooked) != 4 {
		t.Fatalf("600th TickCampfireAt = (len=%d, changed=%v), want (4, true)", len(cooked), changed)
	}
	slots = srv.CampfireSlots(dim, cx, cy, cz)
	for i := 0; i < CampfireSlotCount; i++ {
		if !slots[i].IsEmpty() {
			t.Fatalf("slot %d after 600 ticks = %+v, want empty", i, slots[i])
		}
	}
	spawnedCooked := 0
	for _, ent := range srv.entities.All() {
		if ent.IsItem() && ent.Item.ResolvedID() == "minecraft:cooked_beef" &&
			math.Hypot(ent.X-(float64(cx)+0.5), ent.Z-(float64(cz)+0.5)) < 1.0 {
			spawnedCooked += int(ent.Item.Count)
		}
	}
	if spawnedCooked != 4 {
		t.Fatalf("spawned cooked_beef entity count = %d, want 4", spawnedCooked)
	}
}

func TestCampfireSignalFireExtinguishRelightLightAndDamage(t *testing.T) {
	srv := newSurvivalTestServer(t)
	col := &survivalPacketCollector{}
	session, err := srv.RegisterPlayer(Profile{Name: "Scout", UUID: OfflineUUID("Scout")}, col.send)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	if err := srv.SetPlayerGameMode(session, GameModeSurvival); err != nil {
		t.Fatalf("SetPlayerGameMode: %v", err)
	}

	dim := world.DimensionOverworld
	cx, cy, cz := 25, world.FlatSurfaceY+2, 25

	// 1. Place hay_block at (cx, cy-1, cz), then place campfire at (cx, cy, cz) -> signal_fire="true" and light=15.
	hayID, _ := world.ItemNameToID("minecraft:hay_block")
	hayState, _ := world.ItemToBlock(hayID)
	srv.Chunks().ApplyBlockWithUpdates(cx, cy-1, cz, hayState)
	st := placeCampfireBlockForTest(t, srv, "minecraft:campfire", cx, cy, cz)
	if !CampfireStateLit(st) || !CampfireStateSignalFire(st) {
		t.Fatalf("campfire over hay_block props = %v, want lit=true, signal_fire=true", world.BlockProperties(st))
	}
	if _, blkLight := srv.LightAt(cx, cy, cz); blkLight != 15 {
		t.Fatalf("lit campfire block light = %d, want 15", blkLight)
	}

	// Remove hay_block below -> neighbor update sets signal_fire="false".
	srv.Chunks().ApplyBlockWithUpdates(cx, cy-1, cz, world.StateStone)
	if CampfireStateSignalFire(srv.Chunks().GetBlock(cx, cy, cz)) {
		t.Fatal("expected signal_fire=false after replacing hay_block below with stone")
	}

	// 2. Place raw salmon on campfire, then extinguish with iron_shovel -> sets lit=false, light=0, pops raw salmon.
	srv.PlaceFoodOnCampfire(dim, cx, cy, cz, world.NewNamedItemStack("minecraft:salmon", 1))
	session.SetInventorySlot(world.HotbarContainerOffset, world.NewNamedItemStack("minecraft:iron_shovel", 1))
	act, ok := srv.InteractCampfire(session, cx, cy, cz)
	if !ok || act != CampfireActionExtinguished {
		t.Fatalf("InteractCampfire(iron_shovel) = (%v, %v), want (Extinguished, true)", act, ok)
	}
	if CampfireStateLit(srv.Chunks().GetBlock(cx, cy, cz)) {
		t.Fatal("expected campfire to be lit=false after shovel extinguish")
	}
	if _, blkLight := srv.LightAt(cx, cy, cz); blkLight != 0 {
		t.Fatalf("extinguished campfire block light = %d, want 0", blkLight)
	}
	if !srv.CampfireSlots(dim, cx, cy, cz)[0].IsEmpty() {
		t.Fatal("expected campfire slot 0 to be emptied on dowse/extinguish")
	}

	// 3. Relight campfire with flint_and_steel -> lit=true, light=15.
	session.SetInventorySlot(world.HotbarContainerOffset, world.NewNamedItemStack("minecraft:flint_and_steel", 1))
	act, ok = srv.InteractCampfire(session, cx, cy, cz)
	if !ok || act != CampfireActionLit {
		t.Fatalf("InteractCampfire(flint_and_steel) = (%v, %v), want (Lit, true)", act, ok)
	}
	if !CampfireStateLit(srv.Chunks().GetBlock(cx, cy, cz)) {
		t.Fatal("expected campfire to be lit=true after flint_and_steel")
	}

	// 4. Waterlogging extinguishes campfire and prevents relighting while waterlogged.
	if !srv.ExtinguishCampfire(dim, cx, cy, cz, true) {
		t.Fatal("ExtinguishCampfire(waterlogged=true) returned false")
	}
	wlState := srv.Chunks().GetBlock(cx, cy, cz)
	if CampfireStateLit(wlState) || !CampfireStateWaterlogged(wlState) {
		t.Fatalf("waterlogged campfire props = %v, want lit=false, waterlogged=true", world.BlockProperties(wlState))
	}
	if srv.LightCampfire(dim, cx, cy, cz) {
		t.Fatal("expected waterlogged campfire to reject LightCampfire")
	}

	// 5. Soul campfire light emission (10) and contact damage (1.0 for campfire, 2.0 for soul_campfire).
	sx, sy, sz := 28, world.FlatSurfaceY+1, 25
	soulSt := placeCampfireBlockForTest(t, srv, "minecraft:soul_campfire", sx, sy, sz)
	if _, blkLight := srv.LightAt(sx, sy, sz); blkLight != 10 {
		t.Fatalf("lit soul_campfire block light = %d, want 10", blkLight)
	}
	if dmg, ok := CampfireContactDamageForState(soulSt); !ok || dmg != 2.0 {
		t.Fatalf("soul_campfire contact damage = (%v, %v), want (2.0, true)", dmg, ok)
	}
	srv.SetPlayerPosition(session, float64(sx)+0.5, float64(sy), float64(sz)+0.5)
	beforeHP := session.Snapshot().Health
	if dmg, hit := srv.ApplyCampfireContactDamageToPlayer(session); !hit || dmg != 2.0 {
		t.Fatalf("ApplyCampfireContactDamageToPlayer on soul_campfire = (%v, %v), want (2.0, true)", dmg, hit)
	}
	if gotHP := session.Snapshot().Health; gotHP != beforeHP-2.0 {
		t.Fatalf("player health after soul_campfire tick = %v, want %v", gotHP, beforeHP-2.0)
	}
}

func TestCampfireBreakDropsAndNBTPersistence(t *testing.T) {
	srv := newSurvivalTestServer(t)
	if srv.cfg.WorldDir == "" {
		srv.cfg.WorldDir = t.TempDir()
	}
	dim := world.DimensionOverworld

	// 1. Regular campfire break without Silk Touch drops 2x charcoal + any uncooked items.
	cx, cy, cz := 35, world.FlatSurfaceY+1, 35
	placeCampfireBlockForTest(t, srv, "minecraft:campfire", cx, cy, cz)
	srv.PlaceFoodOnCampfire(dim, cx, cy, cz, world.NewNamedItemStack("minecraft:potato", 1))
	drops := srv.BreakCampfireAt(dim, cx, cy, cz, false)
	if len(drops) != 2 || drops[0].ResolvedID() != "minecraft:potato" ||
		drops[1].ResolvedID() != "minecraft:charcoal" || drops[1].Count != 2 {
		t.Fatalf("regular campfire non-silk drops = %+v, want [1x potato, 2x charcoal]", drops)
	}

	// 2. Soul campfire break without Silk Touch drops 1x soul_soil; with Silk Touch drops 1x soul_campfire.
	sx, sy, sz := 37, world.FlatSurfaceY+1, 35
	placeCampfireBlockForTest(t, srv, "minecraft:soul_campfire", sx, sy, sz)
	soulDrops := srv.BreakCampfireAt(dim, sx, sy, sz, false)
	if len(soulDrops) != 1 || soulDrops[0].ResolvedID() != "minecraft:soul_soil" || soulDrops[0].Count != 1 {
		t.Fatalf("soul_campfire non-silk drops = %+v, want [1x soul_soil]", soulDrops)
	}
	placeCampfireBlockForTest(t, srv, "minecraft:soul_campfire", sx, sy, sz)
	silkDrops := srv.BreakCampfireAt(dim, sx, sy, sz, true)
	if len(silkDrops) != 1 || silkDrops[0].ResolvedID() != "minecraft:soul_campfire" || silkDrops[0].Count != 1 {
		t.Fatalf("soul_campfire silk_touch drops = %+v, want [1x soul_campfire]", silkDrops)
	}

	// 3. NBT persistence via SaveCampfires / loadPersistedCampfires.
	px, py, pz := 40, world.FlatSurfaceY+1, 40
	placeCampfireBlockForTest(t, srv, "minecraft:campfire", px, py, pz)
	srv.SetCampfireState(CampfireBlockEntity{
		ID:        "minecraft:campfire",
		Dimension: dim,
		X:         px,
		Y:         py,
		Z:         pz,
		Items: [CampfireSlotCount]world.ItemStack{
			world.NewNamedItemStack("minecraft:porkchop", 1),
			world.NewNamedItemStack("minecraft:kelp", 1),
		},
		CookingProgress: [CampfireSlotCount]int16{120, 450, 0, 0},
		CookingTime:     [CampfireSlotCount]int16{600, 600, 0, 0},
	})
	if err := srv.SaveCampfires(); err != nil {
		t.Fatalf("SaveCampfires: %v", err)
	}

	loaded, err := srv.loadPersistedCampfires()
	if err != nil || len(loaded) != 1 {
		t.Fatalf("loadPersistedCampfires = (%+v, %v), want 1 entry", loaded, err)
	}
	got := loaded[0]
	if got.X != px || got.Y != py || got.Z != pz ||
		got.Items[0].ResolvedID() != "minecraft:porkchop" || got.Items[1].ResolvedID() != "minecraft:kelp" ||
		got.CookingProgress[0] != 120 || got.CookingProgress[1] != 450 ||
		got.CookingTime[0] != 600 || got.CookingTime[1] != 600 {
		t.Fatalf("reloaded CampfireBlockEntity = %+v", got)
	}
}

func TestCampfireWaterSplashWaterBucketDowseOffhandAndSmokeSignal(t *testing.T) {
	srv := newSurvivalTestServer(t)
	col := &survivalPacketCollector{}
	session, err := srv.RegisterPlayer(Profile{Name: "CampTester", UUID: OfflineUUID("CampTester")}, col.send)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	_ = srv.SetPlayerGameMode(session, GameModeSurvival)

	dim := world.DimensionOverworld
	cx, cy, cz := 20, world.FlatSurfaceY+2, 20

	// 1. Place hay_block at (cx, cy-1, cz) and campfire at (cx, cy, cz) -> verify smoke signal height 24.
	hayID, _ := world.ItemNameToID("minecraft:hay_block")
	hayState, _ := world.ItemToBlock(hayID)
	srv.Chunks().ApplyBlockWithUpdates(cx, cy-1, cz, hayState)
	placeCampfireBlockForTest(t, srv, "minecraft:campfire", cx, cy, cz)

	smokeP, flameP, _, maxH, reachH, sigFire, active := srv.CampfireSmokeSignalInfoAt(dim, cx, cy, cz)
	if !active || !sigFire || smokeP != "minecraft:campfire_signal_smoke" || flameP != "minecraft:flame" || maxH != 24 || reachH != 24 {
		t.Fatalf("unexpected CampfireSmokeSignalInfoAt over hay_block: smoke=%q flame=%q maxH=%d reachH=%d sig=%v active=%v",
			smokeP, flameP, maxH, reachH, sigFire, active)
	}

	// 2. Place raw cod from off-hand (hand=1, slot 45) -> places food into slot 0.
	const offhandSlot = 45
	session.SetInventorySlot(offhandSlot, world.NewNamedItemStack("minecraft:cod", 1))
	if act, ok := srv.InteractCampfire(session, cx, cy, cz); !ok || act != CampfireActionPlacedFood {
		t.Fatalf("expected InteractCampfire to place food from off-hand, got (%v, %v)", act, ok)
	}
	if !session.InventorySlot(offhandSlot).IsEmpty() {
		t.Fatal("expected raw cod consumed from off-hand")
	}

	// 3. Splash water bottle at (cx, cy, cz) extinguishes campfire and drops raw cod.
	if ext := srv.ExtinguishCampfireWithWaterSplash(dim, cx, cy, cz, PotionWater); ext != 1 {
		t.Fatalf("ExtinguishCampfireWithWaterSplash = %d, want 1", ext)
	}
	if CampfireStateLit(srv.Chunks().GetBlock(cx, cy, cz)) {
		t.Fatal("expected campfire to be unlit after water splash")
	}
	if !srv.CampfireSlots(dim, cx, cy, cz)[0].IsEmpty() {
		t.Fatal("expected cooking slot 0 to be emptied on splash water dowse")
	}

	// 4. Relight with fire_charge, place raw chicken, then waterlog with water_bucket.
	session.SetInventorySlot(world.HotbarContainerOffset, world.NewNamedItemStack("minecraft:fire_charge", 1))
	if act, ok := srv.InteractCampfire(session, cx, cy, cz); !ok || act != CampfireActionLit {
		t.Fatalf("expected fire_charge to relight campfire, got (%v, %v)", act, ok)
	}
	srv.PlaceFoodOnCampfire(dim, cx, cy, cz, world.NewNamedItemStack("minecraft:chicken", 1))

	session.SetInventorySlot(world.HotbarContainerOffset, world.NewNamedItemStack("minecraft:water_bucket", 1))
	if act, ok := srv.InteractCampfire(session, cx, cy, cz); !ok || act != CampfireActionExtinguished {
		t.Fatalf("expected water_bucket on campfire to waterlog and extinguish, got (%v, %v)", act, ok)
	}
	wlSt := srv.Chunks().GetBlock(cx, cy, cz)
	if CampfireStateLit(wlSt) || !CampfireStateWaterlogged(wlSt) {
		t.Fatalf("expected lit=false waterlogged=true after water_bucket, got %v", world.BlockProperties(wlSt))
	}
	if held := session.InventorySlot(world.HotbarContainerOffset); held.ResolvedID() != "minecraft:bucket" {
		t.Fatalf("expected minecraft:bucket after pouring water_bucket on campfire, got %q", held.ResolvedID())
	}
	if !srv.CampfireSlots(dim, cx, cy, cz)[0].IsEmpty() {
		t.Fatal("expected cooking slot 0 to be emptied when waterlogged by water_bucket")
	}
}

