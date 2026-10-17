package server

import (
	"math"
	"math/rand"
	"testing"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

func setupWaterPool(chunks *world.Cache, centerX, surfaceY, centerZ, radius, depth int) {
	for dz := -radius; dz <= radius; dz++ {
		for dx := -radius; dx <= radius; dx++ {
			x := centerX + dx
			z := centerZ + dz
			for d := 0; d < depth; d++ {
				chunks.SetBlock(x, surfaceY-d, z, world.StateWater)
			}
			for a := 1; a <= 4; a++ {
				chunks.SetBlock(x, surfaceY+a, z, world.StateAir)
			}
		}
	}
}

func newFishingTestPlayer(t *testing.T, srv *Server, name string, x, y, z float64, send PacketSender) *PlayerSession {
	t.Helper()
	if send == nil {
		send = func(int32, []byte) error { return nil }
	}
	p, err := srv.RegisterPlayer(Profile{Name: name, UUID: OfflineUUID(name)}, send)
	if err != nil {
		t.Fatalf("RegisterPlayer(%s): %v", name, err)
	}
	_ = srv.SetPlayerGameMode(p, GameModeSurvival)
	srv.SetPlayerHealth(p, 20.0, 20, 5.0)
	srv.SetPlayerTransform(p, x, y, z, 0, 0, true)
	return p
}

func TestFishingCastBobberAndAddEntityOwnerID(t *testing.T) {
	srv := newSurvivalTestServer(t)
	var addedEntityPkt []byte
	var removedPkt []byte
	session := newFishingTestPlayer(t, srv, "Fisher", 0.5, 65.0, 0.5, func(pktID int32, payload []byte) error {
		switch pktID {
		case protocol.PlayAddEntity:
			addedEntityPkt = append([]byte(nil), payload...)
		case protocol.PlayRemoveEntities:
			removedPkt = append([]byte(nil), payload...)
		}
		return nil
	})

	rod := world.NewNamedItemStack("minecraft:fishing_rod", 1)
	if rod.IsEmpty() {
		t.Fatal("expected minecraft:fishing_rod item stack to resolve")
	}
	hotbarSlot := world.HotbarContainerOffset + int(session.Snapshot().SelectedSlot)
	session.SetInventorySlot(hotbarSlot, rod)

	// Cast via HandleUseItemPacket (serverbound use_item).
	w := protocol.NewWriter(16)
	w.VarInt(0) // main hand
	w.VarInt(1) // sequence
	w.Float32(0)
	w.Float32(0)
	if err := srv.HandleUseItemPacket(session, protocol.NewReader(w.Bytes())); err != nil {
		t.Fatalf("HandleUseItemPacket failed: %v", err)
	}

	bobber := srv.ActiveFishingBobber(session)
	if bobber == nil {
		t.Fatal("expected active fishing bobber after casting fishing rod")
	}
	ent := srv.entities.Get(bobber.EntityID)
	if ent == nil {
		t.Fatal("expected fishing bobber entity in EntityManager")
	}
	if ent.TypeName != EntityTypeFishingBobber || ent.TypeID != EntityTypeFishingBobberID {
		t.Fatalf("bobber entity type = (%q, %d), want (%q, %d)",
			ent.TypeName, ent.TypeID, EntityTypeFishingBobber, EntityTypeFishingBobberID)
	}

	// Verify PlayAddEntity trailing Data VarInt is the owner player's EntityID.
	if len(addedEntityPkt) == 0 {
		t.Fatal("expected PlayAddEntity packet when casting fishing bobber")
	}
	r := protocol.NewReader(addedEntityPkt)
	gotEntID, err := r.VarInt()
	if err != nil || gotEntID != bobber.EntityID {
		t.Fatalf("PlayAddEntity entityID = %d (err=%v), want %d", gotEntID, err, bobber.EntityID)
	}
	expectedPkt := srv.EncodeFishingBobberAddEntityPacket(nil, ent)
	if string(addedEntityPkt) != string(expectedPkt) {
		t.Fatalf("PlayAddEntity packet mismatch for fishing bobber")
	}

	// Second use_item reels in the bobber with 0 durability wear when in air/water without bite.
	cast, wear := srv.UseFishingRod(session, 0)
	if cast {
		t.Fatal("expected second UseFishingRod call to reel in, not cast")
	}
	if wear != FishingWearNone {
		t.Fatalf("reel wear in air without bite = %d, want %d", wear, FishingWearNone)
	}
	if srv.ActiveFishingBobber(session) != nil {
		t.Fatal("expected active fishing bobber to be cleared after reel in")
	}
	if len(removedPkt) == 0 {
		t.Fatal("expected PlayRemoveEntities packet on reel in")
	}
}

func TestOpenWaterDetectionAndLootTables(t *testing.T) {
	srv := newSurvivalTestServer(t)

	// 1. Build a valid 5x5x2 open water pool at (8, 63..64, 8) with air at 65..66.
	setupWaterPool(srv.chunks, 8, 64, 8, 2, 2)
	if !srv.IsOpenWaterFishingAt(8, 64, 8) {
		t.Fatal("expected 5x5x2 water pool with clear air above to be open water")
	}

	// Placing a solid stone block inside the 5x4x5 volume invalidates open water.
	srv.chunks.SetBlock(10, 64, 10, world.StateStone)
	if srv.IsOpenWaterFishingAt(8, 64, 8) {
		t.Fatal("expected solid stone obstruction in 5x4x5 volume to invalidate open water")
	}
	srv.chunks.SetBlock(10, 64, 10, world.StateWater)

	// 1-block-deep water (stone at y=73) is NOT open water.
	setupWaterPool(srv.chunks, 8, 74, 8, 2, 1)
	for dz := -2; dz <= 2; dz++ {
		for dx := -2; dx <= 2; dx++ {
			srv.chunks.SetBlock(8+dx, 73, 8+dz, world.StateStone)
		}
	}
	if srv.IsOpenWaterFishingAt(8, 74, 8) {
		t.Fatal("expected 1-block-deep water to not qualify as open water")
	}

	// 2. Verify category weight calculation with Luck of the Sea and open-water requirement.
	fishW0, junkW0, treasW0 := EffectiveFishingCategoryWeights(0, true)
	if fishW0 != 85 || junkW0 != 10 || treasW0 != 5 {
		t.Fatalf("weights(luck=0, open=true) = (%d, %d, %d), want (85, 10, 5)", fishW0, junkW0, treasW0)
	}
	_, _, treasShallow := EffectiveFishingCategoryWeights(3, false)
	if treasShallow != 0 {
		t.Fatalf("treasure weight in shallow water = %d, want 0", treasShallow)
	}
	fishW3, junkW3, treasW3 := EffectiveFishingCategoryWeights(3, true)
	if fishW3 != 82 || junkW3 != 4 || treasW3 != 11 {
		t.Fatalf("weights(luck=3, open=true) = (%d, %d, %d), want (82, 4, 11)", fishW3, junkW3, treasW3)
	}

	// 3. Roll shallow water loot 200 times with Luck of the Sea III: treasure must NEVER drop.
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 200; i++ {
		drop := RollFishingLoot(rng, 3, false, false)
		if drop.Category == FishingCategoryTreasure {
			t.Fatalf("unexpected treasure drop %q in shallow water", drop.Stack.ResolvedID())
		}
		if drop.Stack.IsEmpty() {
			t.Fatal("expected non-empty loot stack")
		}
	}

	// 4. Roll open water loot with Luck of the Sea III: verify fish, junk, and treasure all occur.
	counts := make(map[FishingLootCategory]int)
	for i := 0; i < 500; i++ {
		drop := RollFishingLoot(rng, 3, true, true)
		counts[drop.Category]++
		if drop.Stack.IsEmpty() {
			t.Fatalf("empty stack rolled for category %s", drop.Category)
		}
	}
	if counts[FishingCategoryFish] == 0 || counts[FishingCategoryJunk] == 0 || counts[FishingCategoryTreasure] == 0 {
		t.Fatalf("expected all 3 categories in open water rolls, got %v", counts)
	}
}

func TestFishingBiteCycleCatchXPAndDurability(t *testing.T) {
	srv := newSurvivalTestServer(t)
	setupWaterPool(srv.chunks, 8, 64, 8, 2, 2)

	var biteMetaReceived bool
	var biteEventReceived bool
	session := newFishingTestPlayer(t, srv, "Angler", 4.5, 65.0, 4.5, func(pktID int32, payload []byte) error {
		switch pktID {
		case PlaySetEntityDataCB:
			biteMetaReceived = true
		case protocol.PlayEntityEvent:
			if len(payload) == 5 && payload[4] == EntityEventFishingHookReel {
				biteEventReceived = true
			}
		}
		return nil
	})

	rod := world.NewNamedItemStack("minecraft:fishing_rod", 1)
	hotbarSlot := world.HotbarContainerOffset + int(session.Snapshot().SelectedSlot)
	session.SetInventorySlot(hotbarSlot, rod)

	// Enchant rod with Lure III and Luck of the Sea II.
	var rodMeta ItemMeta
	rodMeta.SetEnchantment("minecraft:lure", 3)
	rodMeta.SetEnchantment("minecraft:luck_of_the_sea", 2)
	SetSlotItemMeta(session, hotbarSlot, rodMeta)

	rng := rand.New(rand.NewSource(12345))
	cast, _ := srv.UseFishingRodWithRand(session, 0, rng)
	if !cast {
		t.Fatal("expected UseFishingRod to cast bobber")
	}

	bobber := srv.ActiveFishingBobber(session)
	if bobber == nil {
		t.Fatal("expected active bobber")
	}
	if bobber.LureLevel != 3 || bobber.LuckOfTheSea != 2 {
		t.Fatalf("bobber enchantments = (lure=%d, luck=%d), want (3, 2)", bobber.LureLevel, bobber.LuckOfTheSea)
	}
	// With Lure III (-300 ticks), initial wait must be <= 300 ticks.
	if bobber.TimeUntilLured < 1 || bobber.TimeUntilLured > 300 {
		t.Fatalf("TimeUntilLured with Lure III = %d, want in [1, 300]", bobber.TimeUntilLured)
	}

	// Place bobber directly in the open water pool and set timers near bite.
	srv.entities.Update(bobber.EntityID, func(e *world.Entity) {
		e.X = 8.5
		e.Y = 64.2
		e.Z = 8.5
	})
	srv.SetFishingBobberTimers(session, 1, 0, 0, true)

	// Tick 1: TimeUntilLured reaches 0 and transitions to TimeUntilHooked (20..80).
	srv.TickFishingBobbers(rng)
	bobber = srv.ActiveFishingBobber(session)
	if bobber == nil || bobber.TimeUntilHooked <= 0 {
		t.Fatalf("expected TimeUntilHooked > 0 after TimeUntilLured expired, got %+v", bobber)
	}

	// Advance until fish bites (NibbleTicks > 0).
	for i := 0; i < 100; i++ {
		srv.TickFishingBobbers(rng)
		bobber = srv.ActiveFishingBobber(session)
		if bobber != nil && bobber.Biting && bobber.NibbleTicks > 0 {
			break
		}
	}
	if bobber == nil || !bobber.Biting || bobber.NibbleTicks <= 0 {
		t.Fatalf("expected bobber to enter biting state, got %+v", bobber)
	}
	if !biteMetaReceived || !biteEventReceived {
		t.Fatalf("expected bite entity metadata (%v) and entity event 31 (%v)", biteMetaReceived, biteEventReceived)
	}

	// Reel in while fish is biting!
	cast, wear := srv.UseFishingRodWithRand(session, 0, rng)
	if cast {
		t.Fatal("expected reel-in on second UseFishingRod")
	}
	if wear != FishingWearCatch {
		t.Fatalf("wear on catch = %d, want %d", wear, FishingWearCatch)
	}
	afterMeta := GetSlotItemMeta(session, hotbarSlot)
	if afterMeta.Damage != FishingWearCatch {
		t.Fatalf("rod metadata damage = %d, want %d", afterMeta.Damage, FishingWearCatch)
	}

	// Verify a caught ItemEntity and an ExperienceOrb were spawned.
	var foundItem, foundXPOrb bool
	for _, ent := range srv.entities.AppendAll(nil) {
		if ent.IsItem() {
			foundItem = true
		}
		if ent.IsExperienceOrb() && srv.ExperienceOrbValue(ent.ID) >= 1 {
			foundXPOrb = true
		}
	}
	if !foundItem {
		t.Fatal("expected caught ItemEntity to be spawned in world")
	}
	if !foundXPOrb {
		t.Fatal("expected XP orb (1..6 XP) to be spawned on catch")
	}
}

func TestFishingHookedEntityPullAndDurabilityWear(t *testing.T) {
	srv := newSurvivalTestServer(t)
	session := newFishingTestPlayer(t, srv, "Hooker", 0.5, 65.0, 0.5, nil)

	rod := world.NewNamedItemStack("minecraft:fishing_rod", 1)
	hotbarSlot := world.HotbarContainerOffset + int(session.Snapshot().SelectedSlot)
	session.SetInventorySlot(hotbarSlot, rod)

	// 1. Hook a mob (zombie) -> reel in pulls mob toward player and wears 5 durability.
	mobID, err := srv.SpawnMob(0.5, 65.0, 10.5, MobTypeZombie)
	if err != nil {
		t.Fatalf("SpawnMob failed: %v", err)
	}
	rng := rand.New(rand.NewSource(99))
	if cast, _ := srv.UseFishingRodWithRand(session, 0, rng); !cast {
		t.Fatal("expected cast")
	}
	bobber := srv.ActiveFishingBobber(session)
	srv.entities.Update(bobber.EntityID, func(e *world.Entity) {
		e.X, e.Y, e.Z = 0.5, 65.5, 10.5
	})
	srv.SetFishingBobberHookedEntity(session, mobID)

	beforeMob := srv.entities.Get(mobID)
	beforeDist := math.Hypot(beforeMob.X-0.5, beforeMob.Z-0.5)

	_, wear := srv.UseFishingRodWithRand(session, 0, rng)
	if wear != FishingWearHookedEntity {
		t.Fatalf("wear when reeling hooked mob = %d, want %d", wear, FishingWearHookedEntity)
	}
	afterMob := srv.entities.Get(mobID)
	afterDist := math.Hypot(afterMob.X-0.5, afterMob.Z-0.5)
	if afterDist >= beforeDist {
		t.Fatalf("expected hooked mob to be pulled closer to player: beforeDist=%.2f, afterDist=%.2f", beforeDist, afterDist)
	}
	if gotDmg := GetSlotItemMeta(session, hotbarSlot).Damage; gotDmg != FishingWearHookedEntity {
		t.Fatalf("rod damage after mob pull = %d, want %d", gotDmg, FishingWearHookedEntity)
	}

	// 2. Hook a dropped item entity -> wears 3 durability.
	itemEntID, _ := srv.SpawnItemEntity(0.5, 65.0, 8.5, world.NewNamedItemStack("minecraft:cod", 1))
	if cast, _ := srv.UseFishingRodWithRand(session, 0, rng); !cast {
		t.Fatal("expected cast")
	}
	bobber = srv.ActiveFishingBobber(session)
	srv.entities.Update(bobber.EntityID, func(e *world.Entity) {
		e.X, e.Y, e.Z = 0.5, 65.0, 8.5
	})
	srv.SetFishingBobberHookedEntity(session, itemEntID)
	_, wear = srv.UseFishingRodWithRand(session, 0, rng)
	if wear != FishingWearHookedItem {
		t.Fatalf("wear when reeling hooked item entity = %d, want %d", wear, FishingWearHookedItem)
	}
	if gotDmg := GetSlotItemMeta(session, hotbarSlot).Damage; gotDmg != FishingWearHookedEntity+FishingWearHookedItem {
		t.Fatalf("cumulative rod damage = %d, want %d", gotDmg, FishingWearHookedEntity+FishingWearHookedItem)
	}

	// 3. Bobber on solid ground -> wears 2 durability.
	if cast, _ := srv.UseFishingRodWithRand(session, 0, rng); !cast {
		t.Fatal("expected cast")
	}
	bobber = srv.ActiveFishingBobber(session)
	srv.entities.Update(bobber.EntityID, func(e *world.Entity) {
		e.OnGround = true
	})
	_, wear = srv.UseFishingRodWithRand(session, 0, rng)
	if wear != FishingWearGround {
		t.Fatalf("wear when reeling bobber from ground = %d, want %d", wear, FishingWearGround)
	}

	// 4. Switching away from the fishing rod or moving > 32 blocks despawns the bobber on tick.
	if cast, _ := srv.UseFishingRodWithRand(session, 0, rng); !cast {
		t.Fatal("expected cast")
	}
	srv.SetPlayerTransform(session, 100.0, 65.0, 100.0, 0, 0, true)
	srv.TickFishingBobbers(rng)
	if srv.ActiveFishingBobber(session) != nil {
		t.Fatal("expected bobber to despawn when player moves > 32 blocks away")
	}
}

func TestFishingRainBiteSpeedupBobberPhysicsAndPlayerPull(t *testing.T) {
	srv := newSurvivalTestServer(t)
	setupWaterPool(srv.chunks, 8, 64, 8, 2, 3)

	// 1. Verify rain bite speed boost (~1.25 ticks/tick) vs clear (1.0) vs roofed (~0.5).
	rng := rand.New(rand.NewSource(777))
	var clearSteps, rainSteps, roofSteps int
	for i := 0; i < 1000; i++ {
		clearSteps += ComputeFishingBiteSpeed(false, true, rng)
		rainSteps += ComputeFishingBiteSpeed(true, true, rng)
		roofSteps += ComputeFishingBiteSpeed(false, false, rng)
	}
	if clearSteps != 1000 {
		t.Fatalf("clear open-sky steps = %d, want 1000", clearSteps)
	}
	if rainSteps <= clearSteps+150 {
		t.Fatalf("expected rain open-sky steps (%d) to exceed clear steps (%d) by ~25%%", rainSteps, clearSteps)
	}
	if roofSteps >= clearSteps-350 {
		t.Fatalf("expected obstructed-sky steps (%d) to be ~50%% slower than clear steps (%d)", roofSteps, clearSteps)
	}

	// Verify actual bobber countdown in rain ticks faster than 100 ticks for a 100-tick timer.
	session := newFishingTestPlayer(t, srv, "RainAngler", 4.5, 65.0, 4.5, nil)
	rod := world.NewNamedItemStack("minecraft:fishing_rod", 1)
	hotbarSlot := world.HotbarContainerOffset + int(session.Snapshot().SelectedSlot)
	session.SetInventorySlot(hotbarSlot, rod)

	srv.SetWeather(WeatherRain, 12000)
	if cast, _ := srv.UseFishingRodWithRand(session, 0, rng); !cast {
		t.Fatal("expected cast")
	}
	bobber := srv.ActiveFishingBobber(session)
	// Place bobber submerged at Y=62.5 (below surface Y=64) to test upward water buoyancy too.
	srv.entities.Update(bobber.EntityID, func(e *world.Entity) {
		e.X, e.Y, e.Z = 8.5, 62.5, 8.5
	})
	srv.SetFishingBobberTimers(session, 100, 0, 0, true)

	ticks := 0
	for ticks < 100 {
		srv.TickFishingBobbers(rng)
		ticks++
		b := srv.ActiveFishingBobber(session)
		if b != nil && b.TimeUntilHooked > 0 {
			break
		}
	}
	if ticks >= 95 {
		t.Fatalf("expected 100-tick lure timer to expire in < 95 ticks under rain, took %d ticks", ticks)
	}
	entAfterFloat := srv.entities.Get(bobber.EntityID)
	if entAfterFloat == nil || entAfterFloat.Y <= 62.5 {
		t.Fatalf("expected submerged bobber to float upward toward surface, got Y=%.2f", entAfterFloat.Y)
	}
	_, _ = srv.UseFishingRodWithRand(session, 0, rng) // reel in

	// 2. Verify PlayerFishingLuck combines Luck of the Sea + EffectLuck / EffectUnluck.
	srv.ApplyPlayerEffect(session, NewStatusEffectInstance(EffectLuck, 600, 1)) // Luck II (+2)
	if got := PlayerFishingLuck(session, 3); got != 5.0 {
		t.Fatalf("PlayerFishingLuck(LoTS=3, Luck II) = %.1f, want 5.0", got)
	}
	srv.ApplyPlayerEffect(session, NewStatusEffectInstance(EffectUnluck, 600, 0)) // Bad Luck I (-1)
	if got := PlayerFishingLuck(session, 3); got != 4.0 {
		t.Fatalf("PlayerFishingLuck(LoTS=3, Luck II, Unluck I) = %.1f, want 4.0", got)
	}

	// 3. Verify Jungle bamboo junk loot and Enchanted/Damaged treasure ItemMeta tracking.
	srv.SetFishingJungleAt(8, 8, true)
	foundBamboo := false
	for i := 0; i < 400; i++ {
		d := RollFishingLoot(rng, 0, false, srv.IsJungleFishingAt(8, 64, 8))
		if d.Stack.ResolvedID() == "minecraft:bamboo" {
			foundBamboo = true
			break
		}
	}
	if !foundBamboo {
		t.Fatal("expected bamboo to roll from junk table when fishing in jungle biome")
	}

	// 4. Verify hooking and pulling another online player wears 5 durability and moves the player closer.
	otherPlayer := newFishingTestPlayer(t, srv, "Swimmer", 4.5, 65.0, 12.5, nil)
	if cast, _ := srv.UseFishingRodWithRand(session, 0, rng); !cast {
		t.Fatal("expected cast")
	}
	srv.SetFishingBobberHookedEntity(session, otherPlayer.EntityID)
	beforeDist := math.Hypot(otherPlayer.Snapshot().X-4.5, otherPlayer.Snapshot().Z-4.5)
	_, wear := srv.UseFishingRodWithRand(session, 0, rng)
	if wear != FishingWearHookedEntity {
		t.Fatalf("wear when pulling hooked player = %d, want %d", wear, FishingWearHookedEntity)
	}
	afterDist := math.Hypot(otherPlayer.Snapshot().X-4.5, otherPlayer.Snapshot().Z-4.5)
	if afterDist >= beforeDist {
		t.Fatalf("expected hooked player to be pulled closer: before=%.2f after=%.2f", beforeDist, afterDist)
	}
}

