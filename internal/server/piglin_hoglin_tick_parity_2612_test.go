package server

import (
	"math"
	"math/rand"
	"testing"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

func TestPiglinGoldArmorBarterGuardRepellentCrossbowZombifyParity2612(t *testing.T) {
	s := newSurvivalTestServer(t)
	netherDim := world.DimensionNether
	netherCache := s.DimensionChunks(netherDim)
	surfaceY := 128.0
	airY := 128
	rng := rand.New(rand.NewSource(261254))

	col := &survivalPacketCollector{}
	player, err := s.RegisterPlayer(Profile{Name: "PiglinTester", UUID: OfflineUUID("PiglinTester")}, col.send)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	_ = s.SetPlayerGameMode(player, GameModeSurvival)
	player.SetDimension(netherDim)
	s.SetPlayerTransform(player, 10.5, surfaceY, 10.5, 0, 0, true)

	// 1. `#minecraft:piglin_loved` (26.1.2): includes golden_nautilus_armor, golden_spear, golden_dandelion; excludes gold_nugget!
	for _, loved := range []string{
		"minecraft:golden_nautilus_armor",
		"minecraft:golden_spear",
		"minecraft:golden_dandelion",
		"minecraft:gold_ingot",
		"minecraft:gilded_blackstone",
	} {
		if !IsPiglinLovedGoldItem(loved) {
			t.Fatalf("expected %q to be in #minecraft:piglin_loved (26.1.2)", loved)
		}
	}
	if IsPiglinLovedGoldItem("minecraft:gold_nugget") {
		t.Fatalf("expected minecraft:gold_nugget NOT to be in #minecraft:piglin_loved (26.1.2)")
	}

	// 2. Exact `gameplay/piglin_bartering` loot table from 26.1.2.jar: 19 entries, total weight = 469, with dried_ghast (weight 10)!
	table := PiglinBarterLootTable()
	if len(table) != 19 || PiglinBarterTotalWeight != 469 {
		t.Fatalf("PiglinBarterLootTable: len=%d totalWeight=%d, want len=19 totalWeight=469 (26.1.2)",
			len(table), PiglinBarterTotalWeight)
	}
	foundDriedGhast := false
	for _, e := range table {
		if e.ItemID == "minecraft:dried_ghast" && e.Weight == 10 && e.MinCount == 1 && e.MaxCount == 1 {
			foundDriedGhast = true
			break
		}
	}
	if !foundDriedGhast {
		t.Fatalf("expected gameplay/piglin_bartering to contain minecraft:dried_ghast (weight=10, count=1)")
	}

	// 3. Golden armor (`piglin_safe_armor`): Piglin Brute attacks gold-armored player WITHOUT falsely provoking unprovoked nearby Piglin!
	player.SetInventorySlot(8, world.NewNamedItemStack("minecraft:golden_boots", 1)) // slot 8 = boots
	calmPiglinID, _ := s.SpawnPiglin(netherDim, 14.5, surfaceY, 10.5, false)
	bruteID, _ := s.SpawnPiglinBrute(netherDim, 11.5, surfaceY, 10.5)
	hpBefore := player.Snapshot().Health
	s.TickPiglinsHoglinsAndStriders(rng)
	if player.Snapshot().Health >= hpBefore {
		t.Fatalf("expected PiglinBrute to attack player despite golden_boots (hpBefore=%v, hpAfter=%v)",
			hpBefore, player.Snapshot().Health)
	}
	calmSt, _ := s.GetPiglinState(calmPiglinID)
	if calmSt.AngerTarget == player || calmSt.AngerTicks > 0 {
		t.Fatalf("unprovoked Piglin must NOT become angry at gold-armored player merely because Brute saw player: %+v", calmSt)
	}
	s.entities.Remove(bruteID)
	s.OnPiglinOrBruteKilled(bruteID)
	s.HealPlayer(player, 20.0)

	// 4. Barter 120-tick admire cycle, throw spawn Y (`eyeY - 0.3 = Y + 1.49`) & 0.3 velocity vector (toward player AND toward random pos when alone):
	player.SetInventorySlot(36, world.NewNamedItemStack("minecraft:gold_ingot", 2))
	if !s.InteractWithPiglin(player, calmPiglinID) {
		t.Fatalf("InteractWithPiglin with gold_ingot failed")
	}
	for tick := 1; tick <= 119; tick++ {
		s.TickPiglinsHoglinsAndStriders(rng)
		stMid, _ := s.GetPiglinState(calmPiglinID)
		if stMid.AdmireTicks != 120-tick || canonicalResourceName(stMid.OffHandItem.ResolvedID()) != "minecraft:gold_ingot" {
			t.Fatalf("barter tick %d: got AdmireTicks=%d OffHand=%v, want %d gold_ingot",
				tick, stMid.AdmireTicks, stMid.OffHandItem, 120-tick)
		}
	}
	pEntBefore := s.entities.Get(calmPiglinID)
	s.TickPiglinsHoglinsAndStriders(rng) // tick 120 -> throws barter loot!
	var thrownNear *world.Entity
	for _, ent := range s.entities.AppendAll(nil) {
		if ent.IsItem() && ent.EffectiveDimension() == netherDim {
			cp := ent
			thrownNear = &cp
			s.entities.Remove(ent.ID)
		}
	}
	if thrownNear == nil {
		t.Fatalf("expected bartered item entity at tick 120")
	}
	if math.Abs((thrownNear.Y-pEntBefore.Y)-1.49) > 0.05 {
		t.Fatalf("barter throw spawn Y offset = %.3f, want ~1.49 (getEyeY()-0.3F)", thrownNear.Y-pEntBefore.Y)
	}
	tvx := float64(thrownNear.VelocityX) / 8000.0
	tvy := float64(thrownNear.VelocityY) / 8000.0
	tvz := float64(thrownNear.VelocityZ) / 8000.0
	tSpeed := math.Sqrt(tvx*tvx + tvy*tvy + tvz*tvz)
	if math.Abs(tSpeed-0.30) > 0.03 || tvx >= 0 {
		t.Fatalf("barter throw velocity toward player (-X) = (%.3f, %.3f, %.3f) |v|=%.3f, want tvx<0 and |v|≈0.30",
			tvx, tvy, tvz, tSpeed)
	}

	// Barter when NO player is nearby -> must still throw with |v| ≈ 0.30 (`throwItemsTowardRandomPos`), NOT (0,0,0)!
	soloPiglinID, _ := s.SpawnPiglin(netherDim, 200.5, surfaceY, 200.5, false)
	s.StartPiglinBartering(soloPiglinID, nil, world.NewNamedItemStack("minecraft:gold_ingot", 1))
	_, _, soloDropID, soloOK := s.CompletePiglinBarter(soloPiglinID, rng)
	if !soloOK || soloDropID <= 0 {
		t.Fatalf("CompletePiglinBarter without nearby player failed")
	}
	soloDrop := s.entities.Get(soloDropID)
	svx := float64(soloDrop.VelocityX) / 8000.0
	svy := float64(soloDrop.VelocityY) / 8000.0
	svz := float64(soloDrop.VelocityZ) / 8000.0
	if math.Sqrt(svx*svx+svy*svy+svz*svz) < 0.25 {
		t.Fatalf("barter throw with no nearby player had zero/tiny velocity (%.3f, %.3f, %.3f), want ~0.30", svx, svy, svz)
	}
	s.entities.Remove(soloDropID)
	s.entities.Remove(soloPiglinID)
	s.OnPiglinOrBruteKilled(soloPiglinID)

	// 5. Baby Piglin picks up dropped `gold_ingot`, admires for 120 ticks, and keeps it in MainHand WITHOUT bartering!
	babyID, _ := s.SpawnPiglin(netherDim, 30.5, surfaceY, 10.5, true)
	ingotEntID, _ := s.SpawnItemEntityInDimension(netherDim, 30.5, surfaceY, 10.5, world.NewNamedItemStack("minecraft:gold_ingot", 1))
	s.entities.Update(ingotEntID, func(e *world.Entity) { e.PickupDelay = 0 })
	s.TickPiglinsHoglinsAndStriders(rng) // picks up gold_ingot into offhand!
	babySt1, _ := s.GetPiglinState(babyID)
	if canonicalResourceName(babySt1.OffHandItem.ResolvedID()) != "minecraft:gold_ingot" || babySt1.AdmireTicks <= 0 {
		t.Fatalf("expected baby Piglin to pick up dropped gold_ingot into offhand and admire it, got %+v", babySt1)
	}
	for i := 0; i < PiglinBarterAdmireTicks; i++ {
		s.TickPiglinsHoglinsAndStriders(rng)
	}
	babyStAfter, _ := s.GetPiglinState(babyID)
	if canonicalResourceName(babyStAfter.MainHandItem.ResolvedID()) != "minecraft:gold_ingot" || !babyStAfter.OffHandItem.IsEmpty() {
		t.Fatalf("expected baby Piglin to keep gold_ingot in MainHand without bartering after 120 ticks, got %+v", babyStAfter)
	}
	for _, ent := range s.entities.AppendAll(nil) {
		if ent.IsItem() && ent.EffectiveDimension() == netherDim {
			t.Fatalf("baby Piglin must NOT throw barter loot, found item %v", ent.Item)
		}
	}
	s.entities.Remove(babyID)
	s.OnPiglinOrBruteKilled(babyID)

	// 6. Taking damage while admiring cancels admiring, clears offhand, and disables admiring for 400 ticks!
	player.SetInventorySlot(36, world.NewNamedItemStack("minecraft:gold_ingot", 2))
	if !s.InteractWithPiglin(player, calmPiglinID) {
		t.Fatalf("InteractWithPiglin before damage test failed")
	}
	s.OnPiglinOrBruteDamaged(calmPiglinID, player, 0)
	hurtSt, _ := s.GetPiglinState(calmPiglinID)
	if hurtSt.AdmireTicks != 0 || hurtSt.Bartering || !hurtSt.OffHandItem.IsEmpty() {
		t.Fatalf("expected taking damage to cancel admiring and clear offhand, got %+v", hurtSt)
	}
	if s.InteractWithPiglin(player, calmPiglinID) {
		t.Fatalf("expected ADMIRING_DISABLED (400 ticks) after player hurt Piglin to reject InteractWithPiglin")
	}
	s.entities.Remove(calmPiglinID)
	s.OnPiglinOrBruteKilled(calmPiglinID)

	// 7. `guarded_by_piglins`: container open REQUIRES LOS (`requireVisibility=true`), whereas block break DOES NOT (`requireVisibility=false`)!
	s.SetPlayerTransform(player, 10.5, surfaceY, 30.5, 0, 0, true)
	for y := airY; y <= airY+3; y++ {
		for z := 29; z <= 32; z++ {
			netherCache.SetBlock(15, y, z, world.StateStone) // solid wall at X=15
		}
	}
	walledPiglinID, _ := s.SpawnPiglin(netherDim, 20.5, surfaceY, 30.5, false)
	visiblePiglinID, _ := s.SpawnPiglin(netherDim, 10.5, surfaceY, 35.5, false)
	if alertedOpen := s.NotifyPiglinsPlayerOpenedGuardedBlock(netherDim, 11, airY, 30, player); alertedOpen != 1 {
		t.Fatalf("NotifyPiglinsPlayerOpenedGuardedBlock alerted %d piglins, want 1 (only visible Piglin, NOT walled Piglin)", alertedOpen)
	}
	wStOpen, _ := s.GetPiglinState(walledPiglinID)
	vStOpen, _ := s.GetPiglinState(visiblePiglinID)
	if wStOpen.AngerTicks != 0 || vStOpen.AngerTicks != 600 {
		t.Fatalf("after container open: walled AngerTicks=%d (want 0), visible AngerTicks=%d (want 600)",
			wStOpen.AngerTicks, vStOpen.AngerTicks)
	}
	if alertedBreak := s.NotifyPiglinsPlayerMinedGoldBlock(netherDim, 11, airY, 30, player); alertedBreak != 2 {
		t.Fatalf("NotifyPiglinsPlayerMinedGoldBlock alerted %d piglins, want 2 (even through solid wall!)", alertedBreak)
	}
	wStBreak, _ := s.GetPiglinState(walledPiglinID)
	if wStBreak.AngerTicks != 600 {
		t.Fatalf("after mining guarded block: walled AngerTicks=%d, want 600", wStBreak.AngerTicks)
	}
	// Also verify live game-event dispatch (`EmitPlayerBlockVibration` for `GameEventContainerOpen` & `GameEventBlockDestroy`):
	nmG := s.netherMobs()
	nmG.mu.Lock()
	nmG.piglins[walledPiglinID].AngerTicks = 0
	nmG.piglins[walledPiglinID].AngerTarget = nil
	nmG.piglins[visiblePiglinID].AngerTicks = 0
	nmG.piglins[visiblePiglinID].AngerTarget = nil
	nmG.mu.Unlock()
	s.EmitPlayerBlockVibration(player, GameEventContainerOpen, netherDim, 11, airY, 30, "minecraft:copper_chest")
	wStEvOpen, _ := s.GetPiglinState(walledPiglinID)
	vStEvOpen, _ := s.GetPiglinState(visiblePiglinID)
	if wStEvOpen.AngerTicks != 0 || vStEvOpen.AngerTicks != 600 {
		t.Fatalf("GameEventContainerOpen(copper_chest): walled=%d (want 0), visible=%d (want 600)",
			wStEvOpen.AngerTicks, vStEvOpen.AngerTicks)
	}
	s.EmitPlayerBlockVibration(player, GameEventBlockDestroy, netherDim, 11, airY, 30, "minecraft:gilded_blackstone")
	wStEvBreak, _ := s.GetPiglinState(walledPiglinID)
	if wStEvBreak.AngerTicks != 600 {
		t.Fatalf("GameEventBlockDestroy(gilded_blackstone): walled=%d, want 600", wStEvBreak.AngerTicks)
	}
	for y := airY; y <= airY+3; y++ {
		for z := 29; z <= 32; z++ {
			netherCache.SetBlock(15, y, z, world.StateAir)
		}
	}
	s.entities.Remove(walledPiglinID)
	s.OnPiglinOrBruteKilled(walledPiglinID)
	s.entities.Remove(visiblePiglinID)
	s.OnPiglinOrBruteKilled(visiblePiglinID)

	// 8. `piglin_repellents`: unlit `soul_campfire[lit=false]` does NOT repel; lit `soul_campfire[lit=true]` at dy=+4 DOES repel!
	unlitCamp, okUnlit := world.BlockStateIDByProperties("minecraft:soul_campfire", map[string]string{
		"facing": "north", "lit": "false", "signal_fire": "false", "waterlogged": "false",
	})
	litCamp, okLit := world.BlockStateIDByProperties("minecraft:soul_campfire", map[string]string{
		"facing": "north", "lit": "true", "signal_fire": "false", "waterlogged": "false",
	})
	if !okUnlit || !okLit {
		t.Fatalf("missing soul_campfire states (unlit=%v lit=%v)", okUnlit, okLit)
	}
	netherCache.SetBlock(50, airY, 50, unlitCamp)
	if _, _, _, rep := s.HasPiglinRepellentNear(netherDim, 50.5, surfaceY, 52.5, PiglinRepellentRadius); rep {
		t.Fatalf("unlit soul_campfire[lit=false] must NOT repel Piglins (CampfireBlock.isLitCampfire == false)")
	}
	netherCache.SetBlock(50, airY, 50, world.StateAir)
	netherCache.SetBlock(50, airY+4, 50, litCamp) // dy = +4 (`findClosestMatch(..., 8, 4, ...)`)
	if _, _, _, rep := s.HasPiglinRepellentNear(netherDim, 50.5, surfaceY, 52.5, PiglinRepellentRadius); !rep {
		t.Fatalf("lit soul_campfire[lit=true] at dy=+4 must repel Piglins (vertical range = 4)")
	}
	netherCache.SetBlock(50, airY+4, 50, world.StateAir)

	// 9. Zombification outside piglinSafe (Overworld & The End): preserves armor, weapon, CustomName, Baby, and drops pocket inventory!
	convID, _ := s.SpawnPiglinWithWeapon(world.DimensionEnd, 80.5, surfaceY, 80.5, true, "minecraft:golden_spear")
	// Give baby piglin golden_spear in mainhand, golden_helmet in HEAD, custom name "Goldie", and a bell in pocket inventory:
	s.SetMobCustomName2612(convID, "Goldie")
	s.SetMobEquipment(convID, EquipmentSlotHead, world.NewNamedItemStack("minecraft:golden_helmet", 1))
	nm := s.netherMobs()
	nm.mu.Lock()
	nm.piglins[convID].MainHandItem = world.NewNamedItemStack("minecraft:golden_spear", 1)
	nm.piglins[convID].Inventory = []world.ItemStack{world.NewNamedItemStack("minecraft:bell", 1)}
	nm.mu.Unlock()

	for tick := 1; tick <= 299; tick++ {
		s.TickPiglinsHoglinsAndStriders(rng)
		if s.entities.Get(convID) == nil {
			t.Fatalf("Piglin converted early at tick %d (want 300)", tick)
		}
	}
	s.TickPiglinsHoglinsAndStriders(rng) // tick 300 -> converts in The End!
	if s.entities.Get(convID) != nil {
		t.Fatalf("expected Piglin in The End to zombify at tick 300")
	}
	var zID int32
	var droppedBell bool
	for _, ent := range s.entities.AppendAll(nil) {
		if ent.EffectiveDimension() == world.DimensionEnd {
			if canonicalResourceName(ent.TypeName) == MobTypeZombifiedPiglin {
				zID = ent.ID
			} else if ent.IsItem() && canonicalResourceName(ent.Item.ResolvedID()) == "minecraft:bell" {
				droppedBell = true
			}
		}
	}
	if zID == 0 {
		t.Fatalf("expected converted zombified_piglin in The End")
	}
	zpSt, _ := s.GetZombifiedPiglinState(zID)
	if !zpSt.Baby || canonicalResourceName(zpSt.MainHandItem.ResolvedID()) != "minecraft:golden_spear" {
		t.Fatalf("converted zombified_piglin lost Baby or MainHandItem: %+v", zpSt)
	}
	if gotHelm := canonicalResourceName(s.MobEquipment(zID, EquipmentSlotHead).ResolvedID()); gotHelm != "minecraft:golden_helmet" {
		t.Fatalf("converted zombified_piglin lost helmet armor: got %q, want minecraft:golden_helmet", gotHelm)
	}
	if gotName := s.GetMobCustomName2612(zID); gotName != "Goldie" {
		t.Fatalf("converted zombified_piglin lost CustomName: got %q, want %q", gotName, "Goldie")
	}
	if !droppedBell {
		t.Fatalf("expected Piglin pocket inventory (bell) to drop on the ground upon zombification")
	}

	// 10. Crossbow Piglin (`CrossbowAttack`): charges 25 ticks (`ChargingCrossbow=true`), holds charged for `20..39` ticks (`ChargingCrossbow=false`), then shoots!
	player.SetInventorySlot(8, world.ItemStack{}) // remove golden_boots so Piglin targets player
	s.SetPlayerTransform(player, 10.5, surfaceY, 70.5, 0, 0, true)
	xbowPigID, _ := s.SpawnPiglinWithWeapon(netherDim, 16.5, surfaceY, 70.5, false, "minecraft:crossbow")
	s.TickPiglinsHoglinsAndStriders(rng) // tick 1: UNCHARGED -> CHARGING (`ChargingCrossbow = true`)
	xb1, _ := s.GetPiglinState(xbowPigID)
	if !xb1.ChargingCrossbow {
		t.Fatalf("expected crossbow Piglin at tick 1 to start charging crossbow")
	}
	for tick := 2; tick <= 25; tick++ {
		s.TickPiglinsHoglinsAndStriders(rng)
	}
	// Tick 26: `ticksUsingItem >= 25` -> transitions to CHARGED (`ChargingCrossbow = false`, `attackDelay = 20..39`) WITHOUT shooting yet!
	hpPreHold := player.Snapshot().Health
	s.TickPiglinsHoglinsAndStriders(rng)
	xbCharged, _ := s.GetPiglinState(xbowPigID)
	if xbCharged.ChargingCrossbow || player.Snapshot().Health < hpPreHold {
		t.Fatalf("expected crossbow Piglin after 25-tick draw to hold CHARGED (`attackDelay = 20..39`) before firing (charging=%v hp=%v->%v)",
			xbCharged.ChargingCrossbow, hpPreHold, player.Snapshot().Health)
	}
	fired := false
	for holdTick := 1; holdTick <= 42; holdTick++ {
		s.TickPiglinsHoglinsAndStriders(rng)
		if player.Snapshot().Health < hpPreHold {
			if holdTick < 20 {
				t.Fatalf("crossbow Piglin fired too early during CHARGED hold at tick %d (want 20..40)", holdTick)
			}
			fired = true
			break
		}
	}
	if !fired {
		t.Fatalf("expected crossbow Piglin to fire arrow after 20..39 tick CHARGED delay")
	}
	s.entities.Remove(xbowPigID)
	s.OnPiglinOrBruteKilled(xbowPigID)
}

func TestHoglinZoglinFlingRepellentsBreedZombifyParity2612(t *testing.T) {
	s := newSurvivalTestServer(t)
	netherDim := world.DimensionNether
	netherCache := s.DimensionChunks(netherDim)
	surfaceY := 128.0
	airY := 128
	rng := rand.New(rand.NewSource(261299))

	col := &survivalPacketCollector{}
	player, err := s.RegisterPlayer(Profile{Name: "HoglinTester", UUID: OfflineUUID("HoglinTester")}, col.send)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	_ = s.SetPlayerGameMode(player, GameModeSurvival)
	player.SetDimension(netherDim)
	s.SetPlayerTransform(player, 10.5, surfaceY, 10.5, 0, 0, true)

	// 1. Adult Hoglin attack: cooldown = 40 ticks (not 20), broadcasts EntityEvent = 4, and sends horizontal + vertical fling velocity!
	hoglinID, _ := s.SpawnHoglin(netherDim, 11.8, surfaceY, 10.5, false)
	col.mu.Lock()
	col.pkts = nil
	col.mu.Unlock()
	s.TickPiglinsHoglinsAndStriders(rng)
	hst, _ := s.GetHoglinState(hoglinID)
	if hst.AttackCooldown != 40 {
		t.Fatalf("adult Hoglin AttackCooldown after hit = %d, want 40 (MeleeAttack.create(40))", hst.AttackCooldown)
	}
	col.mu.Lock()
	pkts := append([]survivalTestPacket(nil), col.pkts...)
	col.mu.Unlock()
	sawEntityEvent4 := false
	var lastMotionVX, lastMotionVY, lastMotionVZ float64
	sawMotion := false
	for _, p := range pkts {
		if p.id == protocol.PlayEntityEvent && len(p.body) == 5 && p.body[4] == 4 {
			sawEntityEvent4 = true
		}
		if p.id == protocol.PlaySetEntityMotion {
			r := protocol.NewReader(p.body)
			eid, err1 := r.VarInt()
			mvx, mvy, mvz, err2 := r.LPVec3()
			if err1 == nil && err2 == nil && eid == player.EntityID {
				lastMotionVX, lastMotionVY, lastMotionVZ = mvx, mvy, mvz
				sawMotion = true
			}
		}
	}
	if !sawEntityEvent4 {
		t.Fatalf("expected Hoglin.doHurtTarget to broadcast EntityEvent = 4")
	}
	if !sawMotion || math.Hypot(lastMotionVX, lastMotionVZ) < 0.05 || lastMotionVY <= 0 {
		t.Fatalf("expected HoglinBase.throwTarget to apply BOTH horizontal and vertical impulse, got (%.3f, %.3f, %.3f)",
			lastMotionVX, lastMotionVY, lastMotionVZ)
	}
	s.entities.Remove(hoglinID)
	s.OnHoglinOrZoglinKilled(hoglinID)
	s.HealPlayer(player, 20.0)

	// 2. Baby Hoglin attack: 0.5 damage, AttackCooldown = 15 ticks (not 20), and NO throw impulse!
	babyHogID, _ := s.SpawnHoglin(netherDim, 11.8, surfaceY, 10.5, true)
	s.TickPiglinsHoglinsAndStriders(rng)
	bhSt, _ := s.GetHoglinState(babyHogID)
	if bhSt.AttackCooldown != 15 {
		t.Fatalf("baby Hoglin AttackCooldown after hit = %d, want 15 (MeleeAttack.create(15))", bhSt.AttackCooldown)
	}
	s.entities.Remove(babyHogID)
	s.OnHoglinOrZoglinKilled(babyHogID)
	s.HealPlayer(player, 20.0)

	// 3. `#hoglin_repellents`: horizontal range 8, vertical range 4 (`findClosestMatch(..., 8, 4, ...)`), and armor_stand is NOT a repellent!
	armorStandID := s.entities.Add(&world.Entity{
		TypeID:    ResolveEntityTypeID(EntityTypeArmorStand),
		TypeName:  EntityTypeArmorStand,
		Dimension: netherDim,
		X:         60.5,
		Y:         surfaceY,
		Z:         60.5,
		OnGround:  true,
	})
	if _, _, _, rep := s.HasHoglinRepellentNear(netherDim, 61.5, surfaceY, 60.5, 0); rep {
		t.Fatalf("minecraft:armor_stand is NOT in #minecraft:hoglin_repellents in 26.1.2.jar")
	}
	s.entities.Remove(armorStandID)

	warpedPot, okPot := world.BlockStateIDByProperties("minecraft:potted_warped_fungus", nil)
	if !okPot {
		t.Fatalf("missing potted_warped_fungus state")
	}
	netherCache.SetBlock(68, airY+4, 60, warpedPot) // dx = +8, dy = +4 from (60.5, surfaceY, 60.5)!
	if _, _, _, rep := s.HasHoglinRepellentNear(netherDim, 60.5, surfaceY, 60.5, 0); !rep {
		t.Fatalf("expected potted_warped_fungus at (dx=8, dy=4) to repel Hoglins (HoglinSpecificSensor range 8x4)")
	}
	netherCache.SetBlock(68, airY+4, 60, world.StateAir)

	// 4. Zoglin target selection: ignores creeper, zoglin, AND non-living/non-attackable entities (armor_stand, boat), while flinging pig!
	s.SetPlayerTransform(player, 500.5, surfaceY, 500.5, 0, 0, true) // move player far away
	zoglinID, _ := s.SpawnZoglin(netherDim, 100.5, surfaceY, 100.5, false)
	creeperID, _ := s.SpawnMobInDimension(netherDim, 101.5, surfaceY, 100.5, MobTypeCreeper)
	otherZoglinID, _ := s.SpawnZoglin(netherDim, 100.5, surfaceY, 101.5, false)
	standID := s.entities.Add(&world.Entity{
		TypeID:    ResolveEntityTypeID(EntityTypeArmorStand),
		TypeName:  EntityTypeArmorStand,
		Dimension: netherDim,
		X:         99.5,
		Y:         surfaceY,
		Z:         100.5,
		OnGround:  true,
	})
	boatID := s.entities.Add(&world.Entity{
		TypeID:    ResolveEntityTypeID("minecraft:oak_boat"),
		TypeName:  "minecraft:oak_boat",
		Dimension: netherDim,
		X:         100.5,
		Y:         surfaceY,
		Z:         99.5,
		OnGround:  true,
	})
	// Tick with ONLY creeper, other zoglin, armor_stand, and oak_boat nearby -> Zoglin must NOT attack any of them!
	s.TickPiglinsHoglinsAndStriders(rng)
	zStIdle, _ := s.GetHoglinState(zoglinID)
	if zStIdle.AttackCooldown != 0 {
		t.Fatalf("Zoglin attacked a non-valid target (creeper/zoglin/armor_stand/boat): AttackCooldown=%d", zStIdle.AttackCooldown)
	}
	// Now spawn a Pig next to Zoglin -> Zoglin attacks Pig, sets AttackCooldown=40, and flings Pig ( unless KNOCKBACK_RESISTANCE >= 1.0 )!
	pigID, _ := s.SpawnMobInDimension(netherDim, 101.5, surfaceY, 101.0, MobTypePig)
	pigHPBefore := s.MobHealth(pigID)
	s.TickPiglinsHoglinsAndStriders(rng)
	zStHit, _ := s.GetHoglinState(zoglinID)
	if zStHit.AttackCooldown != 40 || s.MobHealth(pigID) >= pigHPBefore {
		t.Fatalf("expected Zoglin to attack Pig with 40-tick cooldown (cd=%d, hpBefore=%v, hpAfter=%v)",
			zStHit.AttackCooldown, pigHPBefore, s.MobHealth(pigID))
	}
	pigEnt := s.entities.Get(pigID)
	if pigEnt.VelocityY <= 0 {
		t.Fatalf("expected adult Zoglin to fling Pig upward (`VelocityY > 0`), got VelocityY=%d", pigEnt.VelocityY)
	}
	s.entities.Remove(pigID)

	// 5. `KNOCKBACK_RESISTANCE = 1.0` (`minecraft:iron_golem`): `effective = 1.0 - 1.0 = 0` -> NO throw impulse (`VelocityY == 0`)!
	nm := s.netherMobs()
	nm.mu.Lock()
	nm.hoglins[zoglinID].AttackCooldown = 0
	nm.mu.Unlock()
	golemID, _ := s.SpawnIronGolem(netherDim, 101.5, surfaceY, 101.0, false)
	s.TickPiglinsHoglinsAndStriders(rng)
	golemEnt := s.entities.Get(golemID)
	if golemEnt != nil && golemEnt.VelocityY != 0 {
		t.Fatalf("expected IronGolem (KNOCKBACK_RESISTANCE=1.0) NOT to be flung (`VelocityY=0`), got %d", golemEnt.VelocityY)
	}
	_ = creeperID
	_ = otherZoglinID
	_ = standID
	_ = boatID
}
