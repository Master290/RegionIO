package server

import (
	"math"
	"math/rand"
	"path/filepath"
	"testing"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

func TestWardenSummonFromShriekerAnd48BlockCheck(t *testing.T) {
	srv := newSurvivalTestServer(t)
	dim := world.DimensionOverworld
	cache := srv.DimensionChunks(dim)

	col := &survivalPacketCollector{}
	player, err := srv.RegisterPlayer(Profile{
		UUID: [16]byte{20, 1, 1, 1},
		Name: "DeepDarkRunner",
	}, col.send)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	_ = srv.SetPlayerGameMode(player, GameModeSurvival)
	feetY := float64(world.FlatSurfaceY + 1)
	srv.SetPlayerTransform(player, 20.5, feetY, 20.5, 0, 0, true)

	// Place a can_summon=true sculk_shrieker at (22, FlatSurfaceY+1, 20).
	shX, shY, shZ := 22, world.FlatSurfaceY+1, 20
	cache.SetBlock(shX, shY, shZ, SculkShriekerBlockState(true, false, false))

	// Ensure our Warden summon hook is registered.
	srv.RegisterWardenSummonHook()

	// Set player warning level to 3 with 0 cooldown so the next shriek reaches level 4 and summons a Warden.
	player.SetPlayerWardenTrackerState(3, 0, 0)
	if !srv.TriggerSculkShriekerAt(dim, shX, shY, shZ, player) {
		t.Fatal("expected TriggerSculkShriekerAt to succeed")
	}
	if got := player.PlayerWarningLevel(); got != 4 {
		t.Fatalf("PlayerWarningLevel = %d, want 4", got)
	}

	// Tick 90 times so the shrieker finishes its 90-tick shriek and invokes SummonWardenFromShrieker.
	for i := 0; i < SculkShriekerDurationTicks; i++ {
		srv.TickAllSculk()
	}

	wardens := srv.ActiveWardens()
	if len(wardens) != 1 {
		t.Fatalf("ActiveWardens count = %d, want 1", len(wardens))
	}
	w := wardens[0]
	if w.Phase != WardenPhaseEmerging || w.Pose != PoseEmerging || w.PhaseTicks != WardenEmergingDurationTicks {
		t.Fatalf("newly summoned Warden state = (phase=%v, pose=%d, ticks=%d), want (emerging, %d, %d)",
			w.Phase, w.Pose, w.PhaseTicks, PoseEmerging, WardenEmergingDurationTicks)
	}
	if w.Health != DefaultWardenMaxHealth || srv.MobHealth(w.EntityID) != DefaultWardenMaxHealth {
		t.Fatalf("Warden health = %v, want %v", w.Health, DefaultWardenMaxHealth)
	}

	// Verify EncodeWardenEntityDataPacket round-trip.
	encoded := EncodeWardenEntityDataPacket(w.EntityID, PoseEmerging, 0)
	decID, decPose, decAnger, err := DecodeWardenEntityDataPacket(protocol.NewReader(encoded))
	if err != nil {
		t.Fatalf("DecodeWardenEntityDataPacket: %v", err)
	}
	if decID != w.EntityID || decPose != PoseEmerging || decAnger != 0 {
		t.Fatalf("decoded (%d, %d, %d), want (%d, %d, 0)", decID, decPose, decAnger, w.EntityID, PoseEmerging)
	}

	// Verify 48-block radius check prevents a second Warden from being summoned nearby.
	if srv.SummonWardenFromShrieker(dim, 25.5, feetY, 25.5, shX, shY, shZ, player) {
		t.Fatal("expected SummonWardenFromShrieker to return false when an alive Warden is within 48 blocks")
	}
	if len(srv.ActiveWardens()) != 1 {
		t.Fatalf("ActiveWardens count after duplicate summon attempt = %d, want 1", len(srv.ActiveWardens()))
	}

	// Advance 134 ticks -> Warden finishes emerging and transitions to Calm / PoseStanding.
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < WardenEmergingDurationTicks; i++ {
		srv.TickWardens(rng)
	}
	stAfter, ok := srv.GetWardenState(w.EntityID)
	if !ok {
		t.Fatal("expected Warden to remain active after emerging")
	}
	if stAfter.Phase != WardenPhaseCalm || stAfter.Pose != PoseStanding {
		t.Fatalf("after 134 emerging ticks: phase=%v pose=%d, want calm/%d", stAfter.Phase, stAfter.Pose, PoseStanding)
	}
}

func TestWardenCalmTimeoutAndDiggingDespawn(t *testing.T) {
	srv := newSurvivalTestServer(t)
	dim := world.DimensionOverworld
	feetY := float64(world.FlatSurfaceY + 1)

	wardenID, err := srv.SpawnWarden(dim, 10.5, feetY, 10.5, false)
	if err != nil {
		t.Fatalf("SpawnWarden: %v", err)
	}

	rng := rand.New(rand.NewSource(7))
	// Fast-forward TicksWithoutAnger to 1199, then tick once -> enters Digging (100 ticks).
	mgr := srv.wardenManager()
	mgr.mu.Lock()
	mgr.wardens[wardenID].TicksWithoutAnger = WardenCalmDigTimeoutTicks - 1
	mgr.mu.Unlock()

	srv.TickWardens(rng)
	st, ok := srv.GetWardenState(wardenID)
	if !ok || st.Phase != WardenPhaseDigging || st.Pose != PoseDigging || st.PhaseTicks != WardenDiggingDurationTicks {
		t.Fatalf("expected Warden to enter digging after 1200 calm ticks, got ok=%v phase=%v pose=%d ticks=%d",
			ok, st.Phase, st.Pose, st.PhaseTicks)
	}

	// Tick 100 times -> Warden finishes digging and despawns from the world.
	for i := 0; i < WardenDiggingDurationTicks; i++ {
		srv.TickWardens(rng)
	}
	if _, stillAlive := srv.GetWardenState(wardenID); stillAlive {
		t.Fatal("expected Warden state to be removed after digging completes")
	}
	if srv.Entities().Get(wardenID) != nil {
		t.Fatal("expected Warden entity to be removed from world after digging completes")
	}
}

func TestWardenAngerManagementDecayPlayerPriorityAndRoar(t *testing.T) {
	srv := newSurvivalTestServer(t)
	dim := world.DimensionOverworld
	feetY := float64(world.FlatSurfaceY + 1)

	player, err := srv.RegisterPlayer(Profile{
		UUID: [16]byte{20, 2, 2, 2},
		Name: "PriorityTarget",
	}, nil)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	_ = srv.SetPlayerGameMode(player, GameModeSurvival)
	srv.SetPlayerTransform(player, 14.5, feetY, 10.5, 0, 0, true)

	zombieID, err := srv.SpawnMobInDimension(dim, 10.5, feetY, 14.5, MobTypeZombie)
	if err != nil {
		t.Fatalf("SpawnMobInDimension zombie: %v", err)
	}

	wardenID, err := srv.SpawnWarden(dim, 10.5, feetY, 10.5, false)
	if err != nil {
		t.Fatalf("SpawnWarden: %v", err)
	}

	// 1. Add 110 anger toward the zombie -> crosses 80 threshold -> roars (+20 bonus -> 130 anger), zombie is active target.
	zAnger, roaredZ := srv.AddWardenAnger(wardenID, zombieID, nil, 10.5, feetY, 14.5, 110, true)
	if !roaredZ || zAnger != 130 {
		t.Fatalf("zombie anger = %d (roared=%v), want 130 (roared=true)", zAnger, roaredZ)
	}
	st, _ := srv.GetWardenState(wardenID)
	if st.ActiveTargetID != zombieID || st.Phase != WardenPhaseRoaring || st.Pose != PoseRoaring {
		t.Fatalf("expected Warden roaring at zombie, got target=%d phase=%v pose=%d", st.ActiveTargetID, st.Phase, st.Pose)
	}

	// Finish the 84-tick roar.
	rng := rand.New(rand.NewSource(99))
	for i := 0; i < WardenRoarDurationTicks; i++ {
		srv.TickWardens(rng)
	}

	// 2. Add 85 anger toward the player (+20 roar boost -> 105 anger).
	// Even though zombie has higher anger (~126 > 105), player has >= 80 anger so player MUST take priority!
	pAnger, roaredP := srv.AddWardenAnger(wardenID, player.EntityID, player, 14.5, feetY, 10.5, 85, true)
	if !roaredP || pAnger != 105 {
		t.Fatalf("player anger = %d (roared=%v), want 105 (roared=true)", pAnger, roaredP)
	}
	st, _ = srv.GetWardenState(wardenID)
	if st.ActiveTargetID != player.EntityID || !st.ActiveTargetIsPlayer {
		t.Fatalf("expected player (%d) to take target priority over mob (%d), got ActiveTargetID=%d",
			player.EntityID, zombieID, st.ActiveTargetID)
	}
	if st.ClientAngerLevel != pAnger {
		t.Fatalf("ClientAngerLevel = %d, want %d", st.ClientAngerLevel, pAnger)
	}

	// 3. Verify heartbeat intervals across anger stages.
	if got := WardenHeartbeatIntervalTicks(20); got != 40 {
		t.Fatalf("calm heartbeat interval = %d, want 40", got)
	}
	if got := WardenHeartbeatIntervalTicks(55); got != 20 {
		t.Fatalf("agitated heartbeat interval = %d, want 20", got)
	}
	if got := WardenHeartbeatIntervalTicks(100); got != 10 {
		t.Fatalf("angry heartbeat interval = %d, want 10", got)
	}

	// 4. Advance 20 ticks (1 second) and verify anger decays by 1 for all suspects.
	beforePlayerAnger := srv.WardenSuspectAnger(wardenID, player.EntityID)
	for i := 0; i < WardenAngerDecayIntervalTicks; i++ {
		srv.TickWardens(rng)
	}
	afterPlayerAnger := srv.WardenSuspectAnger(wardenID, player.EntityID)
	if afterPlayerAnger != beforePlayerAnger-1 {
		t.Fatalf("player anger after 20 ticks = %d, want %d", afterPlayerAnger, beforePlayerAnger-1)
	}
}

func TestWardenBlindnessVibrationProjectileSniffingAndSelfSilence(t *testing.T) {
	srv := newSurvivalTestServer(t)
	dim := world.DimensionOverworld
	cache := srv.DimensionChunks(dim)
	feetY := float64(world.FlatSurfaceY + 1)

	player, err := srv.RegisterPlayer(Profile{
		UUID: [16]byte{20, 3, 3, 3},
		Name: "SneakyArcher",
	}, nil)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	_ = srv.SetPlayerGameMode(player, GameModeSurvival)
	srv.SetPlayerTransform(player, 16.5, feetY, 10.5, 0, 0, true)

	wardenID, err := srv.SpawnWarden(dim, 10.5, feetY, 10.5, false)
	if err != nil {
		t.Fatalf("SpawnWarden: %v", err)
	}

	// 1. Place a sculk_sensor adjacent to the Warden at (11, FlatSurfaceY+1, 10) and verify
	// that vibrations from the Warden itself NEVER trigger the sculk_sensor!
	cache.SetBlock(11, world.FlatSurfaceY+1, 10, SculkSensorBlockState(SculkSensorPhaseInactive, 0, false))
	if n := srv.EmitVibration(VibrationEvent{
		Dimension:      dim,
		Type:           GameEventStep,
		X:              10.5,
		Y:              feetY,
		Z:              10.5,
		SourceEntityID: wardenID,
	}); n != 0 {
		t.Fatalf("expected Warden step vibration to be suppressed, scheduled %d sensors", n)
	}
	if srv.CanEntityTriggerSculk(wardenID) {
		t.Fatal("expected CanEntityTriggerSculk(wardenID) to be false")
	}

	// 2. Projectile distraction: 1st projectile landing adds +10 anger; 2nd within 100 ticks adds +35 anger!
	srv.EmitVibration(VibrationEvent{
		Dimension:          dim,
		Type:               GameEventProjectileLand,
		X:                  13.5,
		Y:                  feetY,
		Z:                  10.5,
		Player:             player,
		SourceEntityID:     player.EntityID,
		ProjectileEntityID: 9991,
	})
	if got := srv.WardenSuspectAnger(wardenID, player.EntityID); got != WardenProjectileDistractAnger {
		t.Fatalf("anger after 1st projectile distraction = %d, want %d", got, WardenProjectileDistractAnger)
	}

	// Clear vibration listener cooldown and fire a 2nd projectile within 100 ticks -> +35 anger (total 45).
	mgr := srv.wardenManager()
	mgr.mu.Lock()
	mgr.wardens[wardenID].VibrationCooldown = 0
	mgr.wardens[wardenID].TickCounter = 30
	mgr.mu.Unlock()

	srv.EmitVibration(VibrationEvent{
		Dimension:          dim,
		Type:               GameEventProjectileLand,
		X:                  14.5,
		Y:                  feetY,
		Z:                  10.5,
		Player:             player,
		SourceEntityID:     player.EntityID,
		ProjectileEntityID: 9992,
	})
	wantAfterSecond := WardenProjectileDistractAnger + WardenProjectileRepeatAnger
	if got := srv.WardenSuspectAnger(wardenID, player.EntityID); got != wantAfterSecond {
		t.Fatalf("anger after 2nd projectile within 100 ticks = %d, want %d", got, wantAfterSecond)
	}

	// 3. Sniffing adds +35 anger to the nearest smelled player (45 + 35 = 80 -> +20 roar = 100!).
	smelledID, newAnger := srv.PerformWardenSniff(wardenID)
	if smelledID != player.EntityID || newAnger != 100 {
		t.Fatalf("PerformWardenSniff = (id=%d, anger=%d), want (id=%d, anger=100)", smelledID, newAnger, player.EntityID)
	}
}

func TestWardenMeleeShieldDisableSonicBoomAndDarknessPulse(t *testing.T) {
	srv := newSurvivalTestServer(t)
	dim := world.DimensionOverworld
	feetY := float64(world.FlatSurfaceY + 1)

	col := &survivalPacketCollector{}
	player, err := srv.RegisterPlayer(Profile{
		UUID: [16]byte{20, 4, 4, 4},
		Name: "ShieldTank",
	}, col.send)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	_ = srv.SetPlayerGameMode(player, GameModeSurvival)
	srv.SetPlayerTransform(player, 11.5, feetY, 10.5, 0, 0, true)

	// Equip full diamond armor (20 defense, 8 toughness) and a shield in offhand (slot 45).
	player.SetInventorySlot(ArmorSlotHead, world.NewNamedItemStack("minecraft:diamond_helmet", 1))
	player.SetInventorySlot(ArmorSlotChest, world.NewNamedItemStack("minecraft:diamond_chestplate", 1))
	player.SetInventorySlot(ArmorSlotLegs, world.NewNamedItemStack("minecraft:diamond_leggings", 1))
	player.SetInventorySlot(ArmorSlotFeet, world.NewNamedItemStack("minecraft:diamond_boots", 1))
	player.SetInventorySlot(45, world.NewNamedItemStack("minecraft:shield", 1))
	if !srv.UseShield(player, 1) || !player.IsBlockingWithShield() {
		t.Fatal("expected player to start blocking with offhand shield")
	}

	wardenID, err := srv.SpawnWarden(dim, 10.5, feetY, 10.5, false)
	if err != nil {
		t.Fatalf("SpawnWarden: %v", err)
	}

	// 1. Warden melee strike disables the player's shield for 100 ticks (5s) and deals melee damage!
	dealtMelee := srv.AttackFromWarden(wardenID, player.EntityID)
	if dealtMelee <= 0 {
		t.Fatalf("expected AttackFromWarden to deal > 0 damage, got %v", dealtMelee)
	}
	if player.IsBlockingWithShield() || !srv.IsPlayerShieldDisabled(player) {
		t.Fatal("expected Warden melee hit to disable player's shield")
	}
	if srv.UseShield(player, 1) {
		t.Fatal("expected UseShield to fail while shield is disabled by Warden")
	}

	// 2. Restore player health to 20.0, move player 8 blocks away (out of melee reach, inside 15-block sonic boom range),
	// and fire Sonic Boom: must deal 10.0 damage bypassing full diamond armor!
	srv.HealPlayer(player, 20.0)
	srv.SetPlayerTransform(player, 18.5, feetY, 10.5, 0, 0, true)
	dealtSonic := srv.FireWardenSonicBoom(wardenID, player.EntityID)
	if math.Abs(float64(dealtSonic-WardenSonicBoomDamage)) > 1e-4 {
		t.Fatalf("FireWardenSonicBoom dealt %v, want %v (armor-bypassing)", dealtSonic, WardenSonicBoomDamage)
	}
	if math.Abs(float64(player.Health()-10.0)) > 1e-4 {
		t.Fatalf("player health after Sonic Boom = %v, want 10.0 (bypassed 20 diamond armor)", player.Health())
	}

	// 3. Verify Darkness pulse applies minecraft:darkness (260 ticks) to survival players within 20 blocks.
	affected := srv.PulseWardenDarkness(dim, 10.5, feetY, 10.5)
	if affected != 1 || !player.HasPlayerEffect(EffectDarkness) {
		t.Fatalf("expected PulseWardenDarkness to apply darkness to player (affected=%d)", affected)
	}
}

func TestWardenDeathDropsSculkCatalystAndCombatSculkHooks(t *testing.T) {
	srv := newSurvivalTestServer(t)
	dim := world.DimensionOverworld
	cache := srv.DimensionChunks(dim)
	feetY := float64(world.FlatSurfaceY + 1)

	player, err := srv.RegisterPlayer(Profile{
		UUID: [16]byte{20, 5, 5, 5},
		Name: "WardenSlayer",
	}, nil)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	_ = srv.SetPlayerGameMode(player, GameModeSurvival)
	srv.SetPlayerTransform(player, 40.5, feetY, 40.5, 0, 0, true)

	// 1. Spawn a Warden, hit it once (verify +100 hurt anger and 0 knockback), then kill it and verify 1x sculk_catalyst drop.
	wardenID, err := srv.SpawnWarden(dim, 40.5, feetY, 42.0, false)
	if err != nil {
		t.Fatalf("SpawnWarden: %v", err)
	}
	srv.DamageWarden(wardenID, 50.0, player)
	if gotAnger := srv.WardenSuspectAnger(wardenID, player.EntityID); gotAnger < WardenHurtAngerBoost {
		t.Fatalf("Warden anger after being attacked = %d, want >= %d", gotAnger, WardenHurtAngerBoost)
	}
	wEnt := srv.Entities().Get(wardenID)
	if wEnt == nil || wEnt.VelocityX != 0 || wEnt.VelocityZ != 0 {
		t.Fatalf("expected Warden to have 100%% knockback resistance (0 velocity), got %+v", wEnt)
	}

	_, killed := srv.DamageWarden(wardenID, 500.0, player)
	if !killed {
		t.Fatal("expected Warden to die after 550 total damage")
	}
	var foundCatalystDrop bool
	for _, e := range srv.Entities().All() {
		if e.IsItem() && e.Item.ResolvedID() == SculkCatalystItemID && e.Item.Count == 1 {
			foundCatalystDrop = true
			break
		}
	}
	if !foundCatalystDrop {
		t.Fatal("expected dead Warden to drop 1x minecraft:sculk_catalyst")
	}

	// 2. Place a sculk_catalyst at (60, FlatSurfaceY, 60) and a sculk_sensor at (62, FlatSurfaceY+1, 60).
	// Kill a zombie at (60.5, feetY, 60.5) via AttackEntity -> catalyst blooms & consumes XP (no XP orbs spawned),
	// and sculk_sensor receives GameEventEntityDie (frequency 15)!
	stone := mustBlockState(t, "minecraft:stone")
	for dx := -3; dx <= 3; dx++ {
		for dz := -3; dz <= 3; dz++ {
			cache.SetBlock(60+dx, world.FlatSurfaceY, 60+dz, stone)
			cache.SetBlock(60+dx, world.FlatSurfaceY+1, 60+dz, world.StateAir)
		}
	}
	cache.SetBlock(60, world.FlatSurfaceY, 60, SculkCatalystBlockState(false))
	cache.SetBlock(62, world.FlatSurfaceY+1, 60, SculkSensorBlockState(SculkSensorPhaseInactive, 0, false))

	srv.SetPlayerTransform(player, 60.5, feetY, 59.0, 0, 0, true)
	player.SetInventorySlot(world.HotbarContainerOffset, world.NewNamedItemStack("minecraft:netherite_sword", 1))
	player.SetSelectedSlot(0)

	zombieID, err := srv.SpawnMobInDimension(dim, 60.5, feetY, 60.5, MobTypeZombie)
	if err != nil {
		t.Fatalf("SpawnMobInDimension: %v", err)
	}
	srv.SetMobHealth(zombieID, 4.0)
	player.SetAttackStrengthTicks(30)

	if _, zKilled := srv.AttackEntity(player, zombieID); !zKilled {
		t.Fatal("expected zombie to be killed by netherite sword")
	}

	bloom, ok := ParseSculkCatalystState(cache.GetBlock(60, world.FlatSurfaceY, 60))
	if !ok || !bloom {
		t.Fatal("expected sculk_catalyst to bloom on mob death via combat.go hook")
	}
	for _, e := range srv.Entities().All() {
		if e.IsExperienceOrb() && math.Hypot(e.X-60.5, e.Z-60.5) < 4.0 {
			t.Fatal("expected sculk_catalyst to consume mob XP so no XP orbs spawn at (60.5, 60.5)")
		}
	}

	for i := 0; i < 4; i++ {
		srv.TickAllSculk()
	}
	if freq := srv.SculkSensorLastFrequency(dim, 62, world.FlatSurfaceY+1, 60); freq != 15 {
		t.Fatalf("sculk_sensor frequency after mob death = %d, want 15 (entity_die)", freq)
	}
}

func TestWardenTickAndBroadcastSniffCycleAndSonicBoomStateMachine(t *testing.T) {
	srv := newSurvivalTestServer(t)
	dim := world.DimensionOverworld
	cache := srv.DimensionChunks(dim)
	rng := rand.New(rand.NewSource(99))
	feetY := float64(world.FlatSurfaceY + 1)

	stone := mustBlockState(t, "minecraft:stone")
	for dx := -15; dx <= 15; dx++ {
		for dz := -15; dz <= 15; dz++ {
			cache.SetBlock(dx, world.FlatSurfaceY, dz, stone)
			cache.SetBlock(dx, world.FlatSurfaceY+1, dz, world.StateAir)
			cache.SetBlock(dx, world.FlatSurfaceY+2, dz, world.StateAir)
		}
	}

	player, err := srv.RegisterPlayer(Profile{
		UUID: [16]byte{20, 6, 6, 6},
		Name: "DeepDarkExplorer",
	}, nil)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	_ = srv.SetPlayerGameMode(player, GameModeSurvival)
	srv.SetPlayerTransform(player, 5.0, feetY, 0.0, 0, 0, true)

	// 1. Spawn a Warden in Emerging state and verify EntityTracker.TickAndBroadcast advances it without corrupting PoseEmerging.
	wardenID, err := srv.SpawnWarden(dim, 0.5, feetY, 0.5, true)
	if err != nil {
		t.Fatalf("SpawnWarden: %v", err)
	}
	tr := srv.entityTracker()
	for i := 0; i < 4; i++ {
		tr.TickAndBroadcast(srv, rng)
	}
	midSt, ok := srv.GetWardenState(wardenID)
	if !ok || midSt.Phase != WardenPhaseEmerging || midSt.Pose != PoseEmerging || midSt.PhaseTicks != WardenEmergingDurationTicks-4 {
		t.Fatalf("after 4 TickAndBroadcast ticks, Warden state = (%s, pose=%d, phaseTicks=%d), want (emerging, EMERGING=%d, %d)",
			midSt.Phase, midSt.Pose, midSt.PhaseTicks, PoseEmerging, WardenEmergingDurationTicks-4)
	}
	for i := 4; i < WardenEmergingDurationTicks; i++ {
		srv.TickWardens(rng)
	}
	st, ok := srv.GetWardenState(wardenID)
	if !ok || st.Phase != WardenPhaseCalm || st.Pose != PoseStanding {
		t.Fatalf("after %d ticks, Warden state = (%s, pose=%d), want (calm, STANDING=0)", WardenEmergingDurationTicks, st.Phase, st.Pose)
	}

	// 2. Tick until SniffCooldown (100 ticks) expires -> Warden enters WardenPhaseSniffing (PoseSniffing=12) for 84 ticks,
	// then completes sniff and smells the nearby player (+35 anger).
	var sawSniffPose bool
	for i := 0; i < WardenSniffIntervalTicks+WardenSniffDurationTicks+5; i++ {
		srv.TickWardens(rng)
		cur, _ := srv.GetWardenState(wardenID)
		if cur.Phase == WardenPhaseSniffing && cur.Pose == PoseSniffing {
			sawSniffPose = true
		}
		if srv.WardenSuspectAnger(wardenID, player.EntityID) >= WardenSniffAngerBoost {
			break
		}
	}
	if !sawSniffPose {
		t.Fatal("expected Warden to enter WardenPhaseSniffing (PoseSniffing=12)")
	}
	if got := srv.WardenSuspectAnger(wardenID, player.EntityID); got < WardenSniffAngerBoost {
		t.Fatalf("player anger after Warden sniff cycle = %d, want >= %d", got, WardenSniffAngerBoost)
	}

	// 3. Place player at (8.5, feetY, 0.5) behind a solid stone wall and provoke Warden into Chasing.
	for y := world.FlatSurfaceY + 1; y <= world.FlatSurfaceY+4; y++ {
		for dz := -3; dz <= 3; dz++ {
			cache.SetBlock(4, y, dz, stone)
		}
	}
	srv.SetPlayerTransform(player, 8.5, feetY, 0.5, 0, 0, true)
	srv.HealPlayer(player, 20.0)
	srv.AddWardenAnger(wardenID, player.EntityID, player, 8.5, feetY, 0.5, 90, true)

	// Finish Roar (84 ticks).
	for i := 0; i < WardenRoarDurationTicks; i++ {
		srv.TickWardens(rng)
	}
	// Tick Chasing -> Warden is >4 blocks away from player, so it enters WardenPhaseSonicBoom (PoseShooting=16)
	// and fires Sonic Boom at tick 34 through the wall!
	var sawShootingPose bool
	for i := 0; i < WardenSonicBoomTotalTicks+10; i++ {
		srv.TickWardens(rng)
		cur, _ := srv.GetWardenState(wardenID)
		if cur.Phase == WardenPhaseSonicBoom && cur.Pose == PoseShooting {
			sawShootingPose = true
		}
	}
	if !sawShootingPose {
		t.Fatal("expected Warden to enter WardenPhaseSonicBoom (PoseShooting=16)")
	}
	if player.Health() >= 20.0 {
		t.Fatalf("expected Sonic Boom state machine to damage player through solid stone wall, health = %v", player.Health())
	}
	afterBoom, _ := srv.GetWardenState(wardenID)
	if afterBoom.SonicBoomCooldown <= 0 {
		t.Fatalf("expected SonicBoomCooldown > 0 after completing Sonic Boom, got %d", afterBoom.SonicBoomCooldown)
	}

	beamPts := ComputeWardenSonicBoomBeamPoints(0.5, feetY, 0.5, 8.5, feetY, 0.5)
	if len(beamPts) < 8 {
		t.Fatalf("ComputeWardenSonicBoomBeamPoints len = %d, want >= 8", len(beamPts))
	}
}

func TestWardenSneakAdvancementTouchMobCombatAndPersistence(t *testing.T) {
	srv := newSurvivalTestServer(t)
	dim := world.DimensionOverworld
	cache := srv.DimensionChunks(dim)
	feetY := float64(world.FlatSurfaceY + 1)

	player, err := srv.RegisterPlayer(Profile{
		UUID: [16]byte{20, 7, 7, 7},
		Name: "SneakyExplorer",
	}, nil)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	_ = srv.SetPlayerGameMode(player, GameModeSurvival)
	srv.SetPlayerTransform(player, 4.5, feetY, 0.5, 0, 0, true)

	wardenID, err := srv.SpawnWarden(dim, 0.5, feetY, 0.5, false)
	if err != nil {
		t.Fatalf("SpawnWarden: %v", err)
	}

	// 1. Sneaking step within 16 blocks of an alive Warden does not alert the Warden AND grants minecraft:adventure/avoid_vibration ("Sneak 100").
	srv.EmitVibration(VibrationEvent{
		Dimension:      dim,
		Type:           GameEventStep,
		X:              4.5,
		Y:              feetY,
		Z:              0.5,
		Player:         player,
		SourceEntityID: player.EntityID,
		Sneaking:       true,
	})
	if gotAnger := srv.WardenSuspectAnger(wardenID, player.EntityID); gotAnger != 0 {
		t.Fatalf("Warden anger after sneaking step = %d, want 0", gotAnger)
	}
	if !player.HasCompletedAdvancement("minecraft:adventure/avoid_vibration") {
		t.Fatal("expected sneaking near a Warden to grant advancement minecraft:adventure/avoid_vibration")
	}

	// 2. When a Warden is already within 48 blocks, triggering a can_summon sculk_shrieker (at 24 blocks away)
	// does not increment the player's warning level.
	cache.SetBlock(24, world.FlatSurfaceY+1, 0, SculkShriekerBlockState(true, false, false))
	warnBefore := player.PlayerWarningLevel()
	if !srv.StepOnSculkShrieker(player, 24, world.FlatSurfaceY+1, 0) {
		t.Fatal("expected StepOnSculkShrieker to activate shrieker")
	}
	if warnAfter := player.PlayerWarningLevel(); warnAfter != warnBefore {
		t.Fatalf("warning level changed from %d to %d despite alive Warden within 48 blocks", warnBefore, warnAfter)
	}

	// 3. Touch collision (+35 anger) and Mob-vs-Warden combat (DamageWardenFromEntity adds +100 hurt anger + 20 roar bonus = 120).
	touchAnger, _ := srv.OnEntityCollideWithWarden(wardenID, player.EntityID)
	if touchAnger != WardenTouchAngerBoost {
		t.Fatalf("OnEntityCollideWithWarden anger = %d, want %d", touchAnger, WardenTouchAngerBoost)
	}
	if stage := srv.WardenAngerStageFor(wardenID); stage != WardenAngerStageCalm {
		t.Fatalf("WardenAngerStageFor(35) = %s, want calm", stage)
	}

	golemID, err := srv.SpawnMobInDimension(dim, 2.5, feetY, 0.5, MobTypeIronGolem)
	if err != nil {
		t.Fatalf("SpawnMobInDimension(iron_golem): %v", err)
	}
	dealt, killed := srv.DamageWardenFromEntity(wardenID, 20.0, golemID)
	if dealt != 20.0 || killed {
		t.Fatalf("DamageWardenFromEntity = (%v, %v), want (20, false)", dealt, killed)
	}
	if golemAnger := srv.WardenSuspectAnger(wardenID, golemID); golemAnger != 120 {
		t.Fatalf("Warden anger toward attacking Iron Golem = %d, want 120 (100 hurt + 20 roar)", golemAnger)
	}
	topID, topAnger, topIsPlayer, ok := srv.WardenTopSuspect(wardenID)
	if !ok || topID != golemID || topAnger != 120 || topIsPlayer {
		t.Fatalf("WardenTopSuspect = (%d, %d, isPlayer=%v, ok=%v), want (%d, 120, false, true)", topID, topAnger, topIsPlayer, ok, golemID)
	}

	// 4. SaveWardensFile / LoadWardensFile round-trip persistence.
	savePath := filepath.Join(t.TempDir(), "wardens.json")
	if err := srv.SaveWardensFile(savePath); err != nil {
		t.Fatalf("SaveWardensFile: %v", err)
	}

	srv2 := newSurvivalTestServer(t)
	if err := srv2.LoadWardensFile(savePath); err != nil {
		t.Fatalf("LoadWardensFile: %v", err)
	}
	loadedState, ok := srv2.GetWardenState(wardenID)
	if !ok {
		t.Fatalf("expected Warden %d to be restored by LoadWardensFile", wardenID)
	}
	if loadedState.Health != DefaultWardenMaxHealth-20.0 {
		t.Fatalf("restored Warden health = %v, want %v", loadedState.Health, DefaultWardenMaxHealth-20.0)
	}
	if got := srv2.WardenSuspectAnger(wardenID, golemID); got != 120 {
		t.Fatalf("restored Warden anger toward golem = %d, want 120", got)
	}
	if !srv2.ClearWardenSuspect(wardenID, golemID) || srv2.WardenSuspectAnger(wardenID, golemID) != 0 {
		t.Fatal("expected ClearWardenSuspect to remove suspect entry")
	}
}
