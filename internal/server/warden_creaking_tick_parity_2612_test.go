package server

import (
	"math"
	"math/rand"
	"testing"

	"regionio/internal/nbt"
	"regionio/internal/protocol"
	"regionio/internal/world"
)

func TestWardenHeartbeatFormulaProjectileWindowTouchCooldownAndCalmDig2612(t *testing.T) {
	srv := newSurvivalTestServer(t)
	dim := world.DimensionOverworld
	feetY := float64(world.FlatSurfaceY + 1)
	rng := rand.New(rand.NewSource(2612))

	// 1. Vanilla 26.1.2 Warden.getHeartBeatDelay():
	//    40 - floor(clamp(clientAnger / 80.0, 0.0, 1.0) * 30.0)
	wantHeartbeats := [][2]int{
		{0, 40},
		{20, 33},
		{40, 25},
		{55, 20},
		{60, 18},
		{80, 10},
		{150, 10},
	}
	for _, tc := range wantHeartbeats {
		if got := WardenHeartbeatIntervalTicks(tc[0]); got != tc[1] {
			t.Fatalf("WardenHeartbeatIntervalTicks(%d) = %d, want %d (vanilla getHeartBeatDelay)", tc[0], got, tc[1])
		}
	}

	// 2. Projectile vibration window (RECENT_PROJECTILE = 100 ticks), 30-block shooter distance cap,
	//    and disturbance location (1st projectile -> landing pos; 2nd within 100t -> shooter blockPosition!).
	archer, err := srv.RegisterPlayer(Profile{
		UUID: [16]byte{69, 1, 1, 1},
		Name: "DeepArcher",
	}, nil)
	if err != nil {
		t.Fatalf("RegisterPlayer(archer): %v", err)
	}
	_ = srv.SetPlayerGameMode(archer, GameModeSurvival)
	srv.SetPlayerTransform(archer, 18.5, feetY, 10.5, 0, 0, true)

	wardenID, err := srv.SpawnWarden(dim, 10.5, feetY, 10.5, false)
	if err != nil {
		t.Fatalf("SpawnWarden: %v", err)
	}

	// Far shooter (> 30 blocks from Warden) landing an arrow 4 blocks from Warden adds 0 anger!
	sniper, err := srv.RegisterPlayer(Profile{
		UUID: [16]byte{69, 1, 1, 2},
		Name: "FarSniper",
	}, nil)
	if err != nil {
		t.Fatalf("RegisterPlayer(sniper): %v", err)
	}
	_ = srv.SetPlayerGameMode(sniper, GameModeSurvival)
	srv.SetPlayerTransform(sniper, 45.5, feetY, 10.5, 0, 0, true) // 35 blocks from Warden (> 30.0)
	srv.EmitVibration(VibrationEvent{
		Dimension:          dim,
		Type:               GameEventProjectileLand,
		X:                  14.5,
		Y:                  feetY,
		Z:                  10.5,
		Player:             sniper,
		SourceEntityID:     sniper.EntityID,
		ProjectileEntityID: 7001,
	})
	if got := srv.WardenSuspectAnger(wardenID, sniper.EntityID); got != 0 {
		t.Fatalf("shooter > 30 blocks away got %d anger, want 0 (PROJECTILE_ANGER_DISTANCE=30)", got)
	}

	// Wait out 40-tick VIBRATION_COOLDOWN and 100-tick RECENT_PROJECTILE from sniper's arrow.
	for i := 0; i < 105; i++ {
		srv.TickWardens(rng)
	}

	// Archer (8 blocks away, < 30): 1st projectile lands at (12.5, feetY, 13.5) -> +10 anger, disturbance at landing pos.
	srv.EmitVibration(VibrationEvent{
		Dimension:          dim,
		Type:               GameEventProjectileLand,
		X:                  12.5,
		Y:                  feetY,
		Z:                  13.5,
		Player:             archer,
		SourceEntityID:     archer.EntityID,
		ProjectileEntityID: 7002,
	})
	if got := srv.WardenSuspectAnger(wardenID, archer.EntityID); got != 10 {
		t.Fatalf("1st projectile anger = %d, want 10", got)
	}
	st1, _ := srv.GetWardenState(wardenID)
	if math.Abs(st1.DisturbanceX-12.5) > 1.0 || math.Abs(st1.DisturbanceZ-13.5) > 1.0 {
		t.Fatalf("1st projectile disturbance = (%v, %v), want near landing (12.5, 13.5)", st1.DisturbanceX, st1.DisturbanceZ)
	}

	// Tick 45 ticks (> 40 VIBRATION_COOLDOWN, <= 100 RECENT_PROJECTILE; 2 decay ticks -> 10 - 2 = 8 anger).
	// Move archer far enough from Warden's walk path (22.5, feetY, 10.5) so sniff/touch won't interfere.
	srv.SetPlayerTransform(archer, 22.5, feetY, 10.5, 0, 0, true)
	for i := 0; i < 45; i++ {
		srv.TickWardens(rng)
	}
	angerBefore2nd := srv.WardenSuspectAnger(wardenID, archer.EntityID)

	// 2nd projectile within 100 ticks lands at (11.5, feetY, 14.5) -> +35 anger AND disturbance moves to shooter blockPos (22, 10)!
	srv.EmitVibration(VibrationEvent{
		Dimension:          dim,
		Type:               GameEventProjectileLand,
		X:                  11.5,
		Y:                  feetY,
		Z:                  14.5,
		Player:             archer,
		SourceEntityID:     archer.EntityID,
		ProjectileEntityID: 7003,
	})
	if got := srv.WardenSuspectAnger(wardenID, archer.EntityID); got != angerBefore2nd+35 {
		t.Fatalf("2nd projectile within 100t anger = %d, want %d (+35)", got, angerBefore2nd+35)
	}
	st2, _ := srv.GetWardenState(wardenID)
	if math.Abs(st2.DisturbanceX-22.0) > 1.0 || math.Abs(st2.DisturbanceZ-10.0) > 1.0 {
		t.Fatalf("2nd projectile disturbance = (%v, %v), want shooter blockPos ~(22, 10)", st2.DisturbanceX, st2.DisturbanceZ)
	}

	// Wait 105 ticks so RECENT_PROJECTILE (100 ticks) expires, then fire a projectile -> must add +10 again (not +35)!
	for i := 0; i < 105; i++ {
		srv.TickWardens(rng)
	}
	angerAfterExpiry := srv.WardenSuspectAnger(wardenID, archer.EntityID)
	srv.EmitVibration(VibrationEvent{
		Dimension:          dim,
		Type:               GameEventProjectileLand,
		X:                  12.5,
		Y:                  feetY,
		Z:                  12.5,
		Player:             archer,
		SourceEntityID:     archer.EntityID,
		ProjectileEntityID: 7004,
	})
	if got := srv.WardenSuspectAnger(wardenID, archer.EntityID); got != angerAfterExpiry+10 {
		t.Fatalf("projectile after 105t (>100t window) anger = %d, want %d (+10)", got, angerAfterExpiry+10)
	}

	// 3. Touch collision cooldown (TOUCH_COOLDOWN = 20 ticks): 5 consecutive ticks of body contact
	//    must add +35 ONLY ONCE, not +35 every tick!
	wardenTouchID, _ := srv.SpawnWarden(dim, -20.5, feetY, -20.5, false)
	srv.SetPlayerTransform(archer, -20.0, feetY, -20.5, 0, 0, true)
	for i := 0; i < 5; i++ {
		srv.TickWardens(rng)
	}
	if got := srv.WardenSuspectAnger(wardenTouchID, archer.EntityID); got != 35 {
		t.Fatalf("anger after 5 ticks of continuous touch = %d, want 35 (TOUCH_COOLDOWN=20)", got)
	}

	// 4. DIG_COOLDOWN = 1200 ticks: if Warden gets a single +35 anger (< 80) on a suspect that
	//    then moves out of 24-block smell range, and hears nothing for 1200 ticks, it MUST start
	//    Digging at exact tick 1200 (not wait 700 + 1200 = 1900 ticks)!
	srv.SetPlayerTransform(archer, 200.5, feetY, 200.5, 0, 0, true) // move players out of smell/touch range
	srv.SetPlayerTransform(sniper, 200.5, feetY, 200.5, 0, 0, true)
	wardenDigID, _ := srv.SpawnWarden(dim, 2.5, feetY, 2.5, false)
	pigID, _ := srv.SpawnMobInDimension(dim, 200.5, feetY, 2.5, MobTypePig) // far outside 24.0 smell range
	srv.AddWardenAnger(wardenDigID, pigID, nil, 200.5, feetY, 2.5, 35, true)
	mgr := srv.wardenManager()
	mgr.mu.Lock()
	mgr.wardens[wardenDigID].HasDisturbancePos = false
	mgr.mu.Unlock()

	for i := 0; i < WardenCalmDigTimeoutTicks; i++ {
		srv.TickWardens(rng)
	}
	stDig, ok := srv.GetWardenState(wardenDigID)
	if !ok || stDig.Phase != WardenPhaseDigging || stDig.Pose != PoseDigging {
		t.Fatalf("after 1200 calm ticks with initial 35-anger suspect, Warden phase=%v pose=%d, want digging/%d",
			stDig.Phase, stDig.Pose, PoseDigging)
	}
}

func TestWardenEmergingDiggingInvulnerabilitySniffRadiusAndDarknessInPoses2612(t *testing.T) {
	srv := newSurvivalTestServer(t)
	dim := world.DimensionOverworld
	feetY := float64(world.FlatSurfaceY + 1)
	rng := rand.New(rand.NewSource(775))

	player, err := srv.RegisterPlayer(Profile{
		UUID: [16]byte{69, 2, 2, 2},
		Name: "DeepExplorer",
	}, nil)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	_ = srv.SetPlayerGameMode(player, GameModeSurvival)
	srv.SetPlayerTransform(player, 10.5, feetY, 4.5, 0, 0, true)

	// 1. Emerging Warden (134 ticks) is invulnerable to player/mob attacks AND still pulses Darkness at 120 ticks!
	wardenID, err := srv.SpawnWarden(dim, 10.5, feetY, 10.5, true)
	if err != nil {
		t.Fatalf("SpawnWarden(emerging): %v", err)
	}
	dealtEmerge, killedEmerge := srv.DamageWarden(wardenID, 80.0, player)
	if dealtEmerge != 0 || killedEmerge {
		t.Fatalf("DamageWarden during PoseEmerging = (%v, %v), want (0, false)", dealtEmerge, killedEmerge)
	}
	stEmerge, _ := srv.GetWardenState(wardenID)
	if stEmerge.Health != DefaultWardenMaxHealth || srv.WardenSuspectAnger(wardenID, player.EntityID) != 0 {
		t.Fatalf("emerging Warden took damage or anger: hp=%v anger=%d", stEmerge.Health, srv.WardenSuspectAnger(wardenID, player.EntityID))
	}

	// Tick 125 ticks while still inside 134-tick Emerging -> Darkness (260t) MUST be pulsed within 120 ticks!
	for i := 0; i < 125; i++ {
		srv.TickWardens(rng)
	}
	if !player.HasPlayerEffect(EffectDarkness) {
		t.Fatal("expected Warden to pulse EffectDarkness (every 120 ticks) even while in PoseEmerging")
	}
	// Finish remaining 9 ticks of Emerging -> PoseStanding.
	for i := 125; i < WardenEmergingDurationTicks; i++ {
		srv.TickWardens(rng)
	}

	// 2. Sniffing: prioritizes Player over closer Mob, and ONLY adds +35 anger if closerThan(target, 6.0, 20.0)!
	zombieID, _ := srv.SpawnMobInDimension(dim, 10.5, feetY, 8.0, MobTypeZombie) // 2.5 blocks from Warden
	srv.SetPlayerTransform(player, 10.5, feetY, 5.5, 0, 0, true)                 // 5.0 blocks (< 6.0) from Warden
	smelledID, smelledAnger := srv.PerformWardenSniff(wardenID)
	if smelledID != player.EntityID || smelledAnger != 35 {
		t.Fatalf("PerformWardenSniff with zombie@2.5 and player@5.0 = (id=%d, anger=%d), want player (%d, 35) over zombie (%d)",
			smelledID, smelledAnger, player.EntityID, zombieID)
	}
	srv.Entities().Remove(zombieID)
	srv.ClearWardenSuspect(wardenID, player.EntityID)

	// Move player to 10.0 blocks horizontally (>= 6.0, < 24.0): Sniffing sets disturbance (if absent) and adds 0 anger!
	srv.SetPlayerTransform(player, 20.5, feetY, 10.5, 0, 0, true)
	mgr := srv.wardenManager()
	mgr.mu.Lock()
	mgr.wardens[wardenID].HasDisturbancePos = false
	mgr.mu.Unlock()
	_, farAnger := srv.PerformWardenSniff(wardenID)
	if farAnger != 0 || srv.WardenSuspectAnger(wardenID, player.EntityID) != 0 {
		t.Fatalf("PerformWardenSniff at 10.0 blocks (>= 6.0) added anger %d, want 0", farAnger)
	}

	// 3. Digging Warden (100 ticks) is invulnerable to non-bypass attacks and MUST NOT cancel digging!
	mgr.mu.Lock()
	mgr.wardens[wardenID].Phase = WardenPhaseDigging
	mgr.wardens[wardenID].Pose = PoseDigging
	mgr.wardens[wardenID].PhaseTicks = WardenDiggingDurationTicks
	mgr.mu.Unlock()
	dealtDig, _ := srv.DamageWarden(wardenID, 80.0, player)
	stAfterDigHit, _ := srv.GetWardenState(wardenID)
	if dealtDig != 0 || stAfterDigHit.Phase != WardenPhaseDigging || stAfterDigHit.Pose != PoseDigging {
		t.Fatalf("DamageWarden during PoseDigging dealt=%v phase=%v pose=%d, want 0/digging/%d",
			dealtDig, stAfterDigHit.Phase, stAfterDigHit.Pose, PoseDigging)
	}
}

func TestWardenSonicBoom40TickCooldownMeleeResetAndKnockbackResistance2612(t *testing.T) {
	srv := newSurvivalTestServer(t)
	dim := world.DimensionOverworld
	cache := srv.DimensionChunks(dim)
	feetY := float64(world.FlatSurfaceY + 1)
	rng := rand.New(rand.NewSource(909))

	stone := mustBlockState(t, "minecraft:stone")
	for x := 0; x <= 14; x++ {
		for z := 0; z <= 10; z++ {
			cache.SetBlock(x, world.FlatSurfaceY, z, stone)
			for y := 1; y <= 4; y++ {
				cache.SetBlock(x, world.FlatSurfaceY+y, z, world.StateAir)
			}
		}
	}

	col := &survivalPacketCollector{}
	player, err := srv.RegisterPlayer(Profile{
		UUID: [16]byte{69, 3, 3, 3},
		Name: "NetheriteTank",
	}, col.send)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	_ = srv.SetPlayerGameMode(player, GameModeSurvival)
	player.SetInventorySlot(ArmorSlotHead, world.NewNamedItemStack("minecraft:netherite_helmet", 1))
	player.SetInventorySlot(ArmorSlotChest, world.NewNamedItemStack("minecraft:netherite_chestplate", 1))
	player.SetInventorySlot(ArmorSlotLegs, world.NewNamedItemStack("minecraft:netherite_leggings", 1))
	player.SetInventorySlot(ArmorSlotFeet, world.NewNamedItemStack("minecraft:netherite_boots", 1))
	player.SetInventorySlot(45, world.NewNamedItemStack("minecraft:shield", 1))

	wardenID, _ := srv.SpawnWarden(dim, 1.5, feetY, 2.5, false)

	// 1. Melee attack sets SonicBoomCooldown to 40 ticks (Warden.doHurtTarget -> SonicBoom.setCooldown(this, 40)).
	srv.SetPlayerTransform(player, 2.5, feetY, 2.5, 0, 0, true)
	srv.AttackFromWarden(wardenID, player.EntityID)
	stAfterMelee, _ := srv.GetWardenState(wardenID)
	if stAfterMelee.SonicBoomCooldown != 40 {
		t.Fatalf("SonicBoomCooldown after melee hit = %d, want 40", stAfterMelee.SonicBoomCooldown)
	}
	srv.HealPlayer(player, 20.0)
	mgr := srv.wardenManager()
	mgr.mu.Lock()
	delete(mgr.shieldLocks, player)
	mgr.mu.Unlock()
	_ = srv.UseShield(player, 1)

	// 2. Full Netherite armor (4 * 0.1 = 0.4 knockback resistance) + active shield:
	//    FireWardenSonicBoom must deal 10.0 damage (bypassing armor + shield), add 0 exhaustion,
	//    and scale horizontal knockback by (1 - 0.4) = 0.6 -> 2.5 * 0.6 = 1.5!
	srv.SetPlayerTransform(player, 9.5, feetY, 2.5, 180, 0, true) // 8 blocks +X from Warden
	exhaustBefore := player.FoodExhaustion()
	col.mu.Lock()
	col.pkts = nil
	col.mu.Unlock()
	dealt := srv.FireWardenSonicBoom(wardenID, player.EntityID)
	if math.Abs(float64(dealt-10.0)) > 1e-4 || math.Abs(float64(player.Health()-10.0)) > 1e-4 {
		t.Fatalf("SonicBoom against Netherite+Shield dealt=%v hp=%v, want 10.0 / 10.0", dealt, player.Health())
	}
	if exhaustAfter := player.FoodExhaustion(); math.Abs(float64(exhaustAfter-exhaustBefore)) > 1e-5 {
		t.Fatalf("SonicBoom added food exhaustion (%v -> %v), want 0.0 (sonic_boom.json exhaustion=0)", exhaustBefore, exhaustAfter)
	}
	// Check last PlaySetEntityMotion packet sent to player: vx should be ~1.5 (2.5 * (1 - 0.4)), NOT 2.5!
	var sawScaledMotion bool
	col.mu.Lock()
	pkts := append([]survivalTestPacket(nil), col.pkts...)
	col.mu.Unlock()
	for _, p := range pkts {
		if p.id == protocol.PlaySetEntityMotion {
			r := protocol.NewReader(p.body)
			eid, _ := r.VarInt()
			vx, _, _, err := r.LPVec3()
			if err == nil && eid == player.EntityID && math.Abs(vx-1.5) < 0.05 {
				sawScaledMotion = true
			}
		}
	}
	if !sawScaledMotion {
		t.Fatal("expected SonicBoom horizontal knockback against 4x Netherite armor (kbRes=0.4) to be scaled to ~1.5 (not 2.5)")
	}

	// Iron Golem (KNOCKBACK_RESISTANCE = 1.0) takes 10.0 SonicBoom damage with 0 knockback velocity.
	golemID, _ := srv.SpawnMobInDimension(dim, 6.5, feetY, 2.5, MobTypeIronGolem)
	srv.FireWardenSonicBoom(wardenID, golemID)
	if gEnt := srv.Entities().Get(golemID); gEnt != nil && (gEnt.VelocityX != 0 || gEnt.VelocityZ != 0) {
		t.Fatalf("Iron Golem (kbRes=1.0) got SonicBoom velocity (%d, %d), want (0, 0)", gEnt.VelocityX, gEnt.VelocityZ)
	}
	srv.Entities().Remove(golemID)

	// 3. Build solid wall at x=5, provoke Warden, finish Roar (84t), clear melee SonicBoomCooldown,
	//    and step through SonicBoom (34t charge -> 60t total): cooldown after stop() MUST be 40 ticks (not 200)!
	for y := 1; y <= 4; y++ {
		for z := 0; z <= 6; z++ {
			cache.SetBlock(5, world.FlatSurfaceY+y, z, stone)
		}
	}
	srv.HealPlayer(player, 20.0)
	srv.AddWardenAnger(wardenID, player.EntityID, player, 9.5, feetY, 2.5, 90, true)
	for i := 0; i < WardenRoarDurationTicks; i++ {
		srv.TickWardens(rng)
	}
	mgr.mu.Lock()
	mgr.wardens[wardenID].SonicBoomCooldown = 0
	mgr.mu.Unlock()

	// Tick 1 starts SonicBoom; ticks 2..61 run the 60-tick SonicBoom behavior (firing at tick 34, stopping at tick 60).
	srv.TickWardens(rng)
	stStart, _ := srv.GetWardenState(wardenID)
	if stStart.Phase != WardenPhaseSonicBoom || stStart.Pose != PoseShooting {
		t.Fatalf("expected Warden behind wall to start SonicBoom on tick 1, got phase=%v pose=%d", stStart.Phase, stStart.Pose)
	}
	for tick := 1; tick <= WardenSonicBoomTotalTicks; tick++ {
		hpBefore := player.Health()
		srv.TickWardens(rng)
		if tick == WardenSonicBoomChargeTicks && player.Health() >= hpBefore {
			t.Fatalf("expected SonicBoom beam to fire on exact tick %d, health remained %v", WardenSonicBoomChargeTicks, player.Health())
		}
	}
	stDone, _ := srv.GetWardenState(wardenID)
	if stDone.SonicBoomCooldown != 40 {
		t.Fatalf("SonicBoomCooldown after SonicBoom.stop() = %d, want 40 (vanilla SonicBoom.COOLDOWN=40)", stDone.SonicBoomCooldown)
	}
}

func TestCreakingGazeActivation3PointConeGlassFreezeKnockbackAndSpawnerLoop2612(t *testing.T) {
	s := newSurvivalTestServer(t)
	s.SetDifficulty(DifficultyNormal)
	dim := world.DimensionOverworld
	cache := s.DimensionChunks(dim)
	rng := rand.New(rand.NewSource(1234))

	stone := mustBlockState(t, "minecraft:stone")
	glass := mustBlockState(t, "minecraft:glass")
	for x := 0; x <= 14; x++ {
		for z := 0; z <= 15; z++ {
			cache.SetBlock(x, 63, z, stone)
		}
	}

	// 1. Spawn standalone Creaking (IsActive=false, 1.0 HP) at (4.5, 64.0, 14.5) (inside chunk (0,0)!).
	//    Player at (4.5, 64.0, 1.5) is 13.0 blocks away (>= 12.0 activation radius) looking straight South (+Z):
	//    Creaking.checkCanMove MUST NOT activate or freeze it until a player looks within < 12.0 blocks!
	creakingID, err := s.SpawnCreaking(dim, 4.5, 64.0, 14.5)
	if err != nil {
		t.Fatalf("SpawnCreaking: %v", err)
	}
	player := newBeeTestPlayer(t, s, "PaleWatcher", 4.5, 64.0, 1.5)
	s.SetPlayerTransform(player, 4.5, 64.0, 1.5, 0.0, 0.0, true) // 13 blocks away (>= 12.0), looking +Z
	s.TickCreakingsAndHearts(rng)
	cstFar, _ := s.GetCreakingState(creakingID)
	if cstFar.IsActive || !cstFar.CanMove {
		t.Fatalf("inactive Creaking observed from 13.0 blocks (>= 12.0): IsActive=%v CanMove=%v, want false/true",
			cstFar.IsActive, cstFar.CanMove)
	}

	// 2. Move player to (4.5, 64.0, 4.5) (10.0 < 12.0 blocks) looking +Z -> activates (IsActive=true) AND freezes (CanMove=false)!
	s.SetPlayerTransform(player, 4.5, 64.0, 4.5, 0.0, 0.0, true)
	s.TickCreakingsAndHearts(rng)
	cstAct, _ := s.GetCreakingState(creakingID)
	if !cstAct.IsActive || cstAct.CanMove {
		t.Fatalf("Creaking observed within 10.0 blocks (< 12.0): IsActive=%v CanMove=%v, want true/false",
			cstAct.IsActive, cstAct.CanMove)
	}

	// 3. Once IsActive=true, moving back to 13.0 blocks (>= 12.0, < 32.0 FOLLOW_RANGE) STILL freezes the Creaking,
	//    AND running EntityTracker.TickAndBroadcast MUST NOT move or rotate the frozen Creaking!
	s.SetPlayerTransform(player, 4.5, 64.0, 1.5, 0.0, 0.0, true)
	tr := s.entityTracker()
	for i := 0; i < 5; i++ {
		tr.TickAndBroadcast(s, rng)
		s.TickCreakingsAndHearts(rng)
	}
	cstActiveFar, _ := s.GetCreakingState(creakingID)
	entFrozen := s.entities.Get(creakingID)
	if cstActiveFar.CanMove || math.Abs(entFrozen.Z-14.5) > 1e-4 {
		t.Fatalf("active Creaking observed at 13.0 blocks moved during TickAndBroadcast: CanMove=%v Z=%v, want false/14.5",
			cstActiveFar.CanMove, entFrozen.Z)
	}

	// 4. Glass wall (ClipContext.Block.VISUAL transparent) between player and Creaking keeps it frozen!
	for y := 64; y <= 67; y++ {
		cache.SetBlock(4, y, 8, glass)
	}
	s.TickCreakingsAndHearts(rng)
	if cstGlass, _ := s.GetCreakingState(creakingID); cstGlass.CanMove {
		t.Fatal("expected player looking through minecraft:glass (empty visual shape) to keep Creaking frozen")
	}
	for y := 64; y <= 67; y++ {
		cache.SetBlock(4, y, 8, world.StateAir)
	}

	// 5. 3-point Y gaze cone (eyeY=Y+2.3, Y+0.5, Y+1.15): player standing close (0.6 blocks away)
	//    pitching steeply up at Creaking's eyes (Pitch = -75 deg) has dot > 0.5 to Y+2.3 even though dot <= 0.5 to Y+1.35!
	s.SetPlayerTransform(player, 4.5, 64.0, 13.9, 0.0, -75.0, true)
	s.TickCreakingsAndHearts(rng)
	if cstCloseHead, _ := s.GetCreakingState(creakingID); cstCloseHead.CanMove {
		t.Fatal("expected player pitching up at Creaking eyeY (Y+2.3) to freeze Creaking via 3-point isLookingAtMe")
	}

	// 6. Player looks away (Yaw=180) -> Creaking unfreezes, attacks with 40-tick melee cooldown (not 20),
	//    and standalone Creaking (1.0 HP, not heart-bound) takes normal damage and dies directly!
	s.SetPlayerTransform(player, 4.5, 64.0, 13.5, 180.0, 0.0, true)
	s.TickCreakingsAndHearts(rng)
	cstAfterHit, _ := s.GetCreakingState(creakingID)
	if cstAfterHit.AttackCooldown != 40 {
		t.Fatalf("Creaking melee AttackCooldown = %d, want 40 (vanilla CreakingAi MeleeAttack=40)", cstAfterHit.AttackCooldown)
	}

	player.SetInventorySlot(world.HotbarContainerOffset, world.NewNamedItemStack("minecraft:wooden_sword", 1))
	player.SetSelectedSlot(0)
	player.SetAttackStrengthTicks(20)
	if _, killedStandalone := s.AttackEntity(player, creakingID); !killedStandalone || s.entities.Get(creakingID) != nil {
		t.Fatal("expected standalone (non-heart-bound, 1.0 HP) Creaking to take damage and die directly")
	}
}

func TestCreakingHeartConnectedResinSpreadTearingDeath45TicksAndDaytimeDisintegrate2612(t *testing.T) {
	s := newSurvivalTestServer(t)
	s.SetDifficulty(DifficultyNormal)
	dim := world.DimensionOverworld
	cache := s.DimensionChunks(dim)
	rng := rand.New(rand.NewSource(4545))

	stone := mustBlockState(t, "minecraft:stone")
	for x := 0; x <= 14; x++ {
		for z := 0; z <= 14; z++ {
			cache.SetBlock(x, 63, z, stone)
		}
	}
	paleLogY, _ := world.BlockStateIDByProperties(PaleOakLogBlockID, map[string]string{"axis": "y"})
	heartAwake, _ := world.BlockStateIDByProperties(CreakingHeartBlockID, map[string]string{
		"axis":                 "y",
		"creaking_heart_state": CreakingHeartStateAwake,
		"natural":              "true",
	})
	heartDormant, _ := world.BlockStateIDByProperties(CreakingHeartBlockID, map[string]string{
		"axis":                 "y",
		"creaking_heart_state": CreakingHeartStateDormant,
		"natural":              "true",
	})

	// Place sandwiched heart at (4, 65, 4) with connected logs (4, 64, 4) and (4, 66, 4),
	// PLUS a disconnected Pale Oak log at (5, 67, 5) separated by air!
	cache.SetBlock(4, 64, 4, paleLogY)
	cache.SetBlock(4, 65, 4, heartDormant)
	cache.SetBlock(4, 66, 4, paleLogY)
	cache.SetBlock(5, 67, 5, paleLogY) // NOT connected to (4, 65, 4)!

	s.SetDayTime(14000)
	player := newBeeTestPlayer(t, s, "ResinHarvester", 4.5, 64.0, 6.5)

	// 1. If heart block state is DORMANT, hitting heart-bound Creaking is invulnerable and places 0 resin!
	creakingID, _ := s.SpawnCreakingLinkedToHeart(dim, 4.5, 64.0, 8.5, 4, 65, 4)
	invulnDormant, placedDormant := s.OnCreakingHit(creakingID, player, rng)
	if !invulnDormant || placedDormant != 0 {
		t.Fatalf("OnCreakingHit while heart state=dormant: invuln=%v placed=%d, want true/0", invulnDormant, placedDormant)
	}

	// Switch heart block to AWAKE, clear 100-tick cooldown, and hit multiple times -> resin ONLY on connected logs, NEVER adjacent to (5, 67, 5)!
	cache.SetBlock(4, 65, 4, heartAwake)
	for burst := 0; burst < 4; burst++ {
		for i := 0; i < CreakingResinCooldownTicks; i++ {
			// Advance cooldown via TickCreakingsAndHearts while night.
			cm := s.creakingRabbitMgr()
			cm.mu.Lock()
			if hs := cm.hearts[creakingBlockKey{dim: dim, x: 4, y: 65, z: 4}]; hs != nil && hs.ResinCooldownTicks > 0 {
				hs.ResinCooldownTicks--
			}
			cm.mu.Unlock()
		}
		_, placed := s.OnCreakingHit(creakingID, player, rng)
		if burst == 0 && (placed < 2 || placed > 3) {
			t.Fatalf("OnCreakingHit while awake placed %d resin_clump, want 2..3", placed)
		}
	}
	// Verify NONE of the 6 air cells around disconnected log (5, 67, 5) received resin_clump
	// (note: (4,67,5), (5,67,4) are dist > 1 from (4,66,4); check (6,67,5), (5,68,5), (5,67,6)).
	for _, off := range [][3]int{{6, 67, 5}, {5, 68, 5}, {5, 67, 6}} {
		if world.BlockName(cache.GetBlock(off[0], off[1], off[2])) == ResinClumpItemID {
			t.Fatalf("spreadResin placed resin_clump at %v on disconnected pale_oak_log (5,67,5)!", off)
		}
	}

	// 2. Daytime arrival (SetDayTime(1000)) MUST immediately tearDown and remove the heart-bound Creaking
	//    EVEN WHEN the player is staring straight at it!
	s.SetPlayerTransform(player, 4.5, 64.0, 6.5, 0.0, 0.0, true) // staring straight +Z at (4.5, 64, 8.5)!
	s.SetDayTime(1000)
	s.TickCreakingsAndHearts(rng)
	if s.entities.Get(creakingID) != nil {
		t.Fatal("expected daytime (!CREAKING_ACTIVE) to immediately tearDown heart-bound Creaking even while observed by player")
	}

	// 3. Re-awaken at night, spawn new protector, and break creaking_heart with destroyer=player:
	//    Creaking MUST enter IsTearingDown=true for 45 ticks (ticks 1..45 still present, removed on tick 46 when deathTime > 45)!
	s.SetDayTime(14000)
	_, creakingID2 := s.UpdateCreakingHeartAt(dim, 4, 65, 4, rng)
	if creakingID2 <= 0 {
		t.Fatal("expected awake heart at night to spawn protector #2")
	}
	cache.SetBlock(4, 65, 4, world.StateAir)
	destroyedID, _, _ := s.OnCreakingHeartBroken(dim, 4, 65, 4, player, false, 3, rng)
	if destroyedID != creakingID2 {
		t.Fatalf("OnCreakingHeartBroken destroyedID = %d, want %d", destroyedID, creakingID2)
	}
	cstTear, ok := s.GetCreakingState(creakingID2)
	if !ok || !cstTear.IsTearingDown || s.entities.Get(creakingID2) == nil {
		t.Fatalf("immediately after player broke creaking_heart: ok=%v IsTearingDown=%v entNil=%v, want true/true/false (45-tick tickDeath)",
			ok, cstTear.IsTearingDown, s.entities.Get(creakingID2) == nil)
	}
	for tick := 1; tick <= 45; tick++ {
		s.TickCreakingsAndHearts(rng)
		if s.entities.Get(creakingID2) == nil {
			t.Fatalf("tearing-down Creaking was removed prematurely at tick %d, want tick 46 (deathTime > 45)", tick)
		}
	}
	s.TickCreakingsAndHearts(rng) // tick 46 -> deathTime == 46 > 45 -> tearDown()!
	if s.entities.Get(creakingID2) != nil {
		t.Fatal("expected tearing-down Creaking to be removed on tick 46 (deathTime > 45)")
	}
}

func TestWardenAngerNBTAndCreakingHomePosNBTRebindReload2612(t *testing.T) {
	srv := newSurvivalTestServer(t)
	dim := world.DimensionOverworld
	feetY := float64(world.FlatSurfaceY + 1)
	rng := rand.New(rand.NewSource(2026))

	// 1. Warden binary NBT ("anger": {"suspects": [{"uuid": IntArray[4], "anger": Int}]}):
	//    Save while player is angry (115), reload onto fresh server BEFORE player joins,
	//    tick 20 ticks (-1 decay in AngerByUUID -> 114), then connect player with same UUID -> auto-rebinds!
	pUUID := [16]byte{0x12, 0x34, 0x56, 0x78, 0x9a, 0xbc, 0xde, 0xf0, 1, 2, 3, 4, 5, 6, 7, 8}
	player, err := srv.RegisterPlayer(Profile{UUID: pUUID, Name: "SavedSuspect"}, nil)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	_ = srv.SetPlayerGameMode(player, GameModeSurvival)
	srv.SetPlayerTransform(player, 14.5, feetY, 10.5, 0, 0, true)

	w1, _ := srv.SpawnWarden(dim, 10.5, feetY, 10.5, false)
	srv.AddWardenAnger(w1, player.EntityID, player, 14.5, feetY, 10.5, 95, true) // 95 + 20 roar = 115
	wComp, ok := srv.SaveWardenEntityNBT(w1)
	if !ok || wComp == nil {
		t.Fatal("SaveWardenEntityNBT failed")
	}
	wBytes := nbt.MarshalNamed("", wComp)
	_, wDecodedTag, err := nbt.UnmarshalNamed(wBytes)
	if err != nil {
		t.Fatalf("nbt.UnmarshalNamed(warden): %v", err)
	}

	srv2 := newSurvivalTestServer(t)
	w2, _ := srv2.SpawnWarden(dim, 10.5, feetY, 10.5, false)
	if !srv2.LoadWardenEntityNBT(w2, wDecodedTag.(*nbt.Compound)) {
		t.Fatal("LoadWardenEntityNBT failed")
	}
	// Tick 20 ticks before player reconnects -> AngerByUUID decays by 1 (115 -> 114).
	for i := 0; i < 20; i++ {
		srv2.TickWardens(rng)
	}
	reconnected, _ := srv2.RegisterPlayer(Profile{UUID: pUUID, Name: "SavedSuspect"}, nil)
	_ = srv2.SetPlayerGameMode(reconnected, GameModeSurvival)
	srv2.SetPlayerTransform(reconnected, 14.5, feetY, 10.5, 0, 0, true)
	for i := 0; i < 20; i++ {
		srv2.TickWardens(rng)
	}
	// Converted on tick 40 and decayed 1 more -> 113 (>= 80 Angry target!).
	gotReboundAnger := srv2.WardenSuspectAnger(w2, reconnected.EntityID)
	stW2, _ := srv2.GetWardenState(w2)
	if gotReboundAnger != 113 || stW2.ActiveTargetID != reconnected.EntityID {
		t.Fatalf("rebound Warden anger=%d ActiveTargetID=%d, want 113 / %d",
			gotReboundAnger, stW2.ActiveTargetID, reconnected.EntityID)
	}

	// 2. Creaking ("home_pos": IntArray[3]) & CreakingHeart ("creaking": IntArray[4]) binary NBT roundtrip + 30-tick grace rebind!
	cache2 := srv2.DimensionChunks(dim)
	stone := mustBlockState(t, "minecraft:stone")
	paleLogY, _ := world.BlockStateIDByProperties(PaleOakLogBlockID, map[string]string{"axis": "y"})
	heartAwake, _ := world.BlockStateIDByProperties(CreakingHeartBlockID, map[string]string{
		"axis":                 "y",
		"creaking_heart_state": CreakingHeartStateAwake,
		"natural":              "true",
	})
	for x := 2; x <= 8; x++ {
		for z := 2; z <= 8; z++ {
			cache2.SetBlock(x, 63, z, stone)
		}
	}
	cache2.SetBlock(4, 64, 4, paleLogY)
	cache2.SetBlock(4, 65, 4, heartAwake)
	cache2.SetBlock(4, 66, 4, paleLogY)
	srv.SetDayTime(14000)
	srv2.SetDayTime(14000)

	standaloneID, _ := srv.SpawnCreaking(dim, 2.5, 64.0, 2.5)
	standaloneNBT, ok := srv.SaveCreakingEntityNBT(standaloneID)
	if !ok {
		t.Fatal("SaveCreakingEntityNBT(standalone) failed")
	}
	if _, hasHome := standaloneNBT.Get("home_pos"); hasHome {
		t.Fatal("standalone Creaking NBT must not contain home_pos")
	}

	boundID, _ := srv.SpawnCreakingLinkedToHeart(dim, 6.5, 64.0, 6.5, 4, 65, 4)
	boundNBT, ok := srv.SaveCreakingEntityNBT(boundID)
	if !ok {
		t.Fatal("SaveCreakingEntityNBT(bound) failed")
	}
	heartNBT, ok := srv.SaveCreakingHeartBlockEntityNBT(dim, 4, 65, 4)
	if !ok {
		t.Fatal("SaveCreakingHeartBlockEntityNBT failed")
	}

	cBytes := nbt.MarshalNamed("", boundNBT)
	hBytes := nbt.MarshalNamed("", heartNBT)
	_, decCreakingTag, _ := nbt.UnmarshalNamed(cBytes)
	_, decHeartTag, _ := nbt.UnmarshalNamed(hBytes)

	// Load heart BE first on srv2, tick 10 ticks (< 30-tick grace window: must NOT spawn a duplicate Creaking!),
	// then spawn & load the saved Creaking entity -> rebinds cleanly to (4, 65, 4)!
	srv2.SetPlayerTransform(reconnected, 4.5, 64.0, 8.5, 0, 0, true)
	if !srv2.LoadCreakingHeartBlockEntityNBT(dim, 4, 65, 4, decHeartTag.(*nbt.Compound)) {
		t.Fatal("LoadCreakingHeartBlockEntityNBT failed")
	}
	for i := 0; i < 10; i++ {
		srv2.TickCreakingsAndHearts(rng)
	}
	for _, e := range srv2.Entities().All() {
		if e.TypeName == MobTypeCreaking {
			t.Fatalf("heart spawned duplicate Creaking %d during 30-tick reload grace window!", e.ID)
		}
	}

	reloadedCreakingID, _ := srv2.SpawnCreaking(dim, 6.5, 64.0, 6.5)
	if !srv2.LoadCreakingEntityNBT(reloadedCreakingID, decCreakingTag.(*nbt.Compound)) {
		t.Fatal("LoadCreakingEntityNBT failed")
	}
	cstReloaded, ok := srv2.GetCreakingState(reloadedCreakingID)
	if !ok || !cstReloaded.IsHeartBound || cstReloaded.HeartX != 4 || cstReloaded.HeartY != 65 || cstReloaded.HeartZ != 4 {
		t.Fatalf("reloaded Creaking state = %+v, want heart-bound to (4,65,4)", cstReloaded)
	}
	invulnReload, placedReload := srv2.OnCreakingHit(reloadedCreakingID, reconnected, rng)
	if !invulnReload || placedReload < 2 {
		t.Fatalf("hitting rebound Creaking after NBT reload: invuln=%v placed=%d, want true / >=2", invulnReload, placedReload)
	}
}
