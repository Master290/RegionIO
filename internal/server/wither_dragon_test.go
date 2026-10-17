package server

import (
	"math/rand"
	"testing"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

func TestWitherPatternAndSummon(t *testing.T) {
	srv := newTestMapServer(t)
	srv.SetDifficulty(DifficultyNormal)

	var gotPackets []int32
	player, err := srv.RegisterPlayer(Profile{
		UUID: [16]byte{1, 2, 3, 4},
		Name: "WitherSummoner",
	}, func(id int32, body []byte) error {
		gotPackets = append(gotPackets, id)
		return nil
	})
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	srv.SetPlayerGameMode(player, GameModeSurvival)
	srv.SetPlayerPosition(player, 10.5, 65.0, 10.5)

	cache := srv.DimensionChunks(world.DimensionOverworld)
	soulSand, ok := world.BlockStateByName(SoulSandBlockID, nil)
	if !ok {
		t.Fatalf("missing soul_sand block state")
	}
	skull, ok := world.BlockStateByName(WitherSkeletonSkullBlockID, nil)
	if !ok {
		t.Fatalf("missing wither_skeleton_skull block state")
	}

	// Build T-shape centered at (10, 65..67, 10) with arms along X (9..11).
	cache.SetBlock(10, 65, 10, soulSand)
	cache.SetBlock(9, 66, 10, soulSand)
	cache.SetBlock(10, 66, 10, soulSand)
	cache.SetBlock(11, 66, 10, soulSand)
	cache.SetBlock(9, 67, 10, skull)
	cache.SetBlock(10, 67, 10, skull)
	cache.SetBlock(11, 67, 10, skull)

	match, found := FindWitherPatternAt(cache, 11, 67, 10)
	if !found {
		t.Fatalf("expected FindWitherPatternAt to match T-shape at (11,67,10)")
	}
	if match.CenterX != 10 || match.TopY != 67 || match.CenterZ != 10 || !match.ArmsAlongX {
		t.Fatalf("unexpected pattern match: %+v", match)
	}

	witherID, summoned := srv.TrySummonWitherAt(world.DimensionOverworld, 11, 67, 10, player)
	if !summoned || witherID <= 0 {
		t.Fatalf("expected TrySummonWitherAt to summon Wither, got id=%d summoned=%v", witherID, summoned)
	}

	// All 7 pattern blocks should be cleared to air.
	for _, pos := range match.Blocks() {
		if st := cache.GetBlock(pos.X, pos.Y, pos.Z); st != world.StateAir {
			t.Fatalf("expected pattern block %+v to be cleared to air, got state %d (%s)", pos, st, world.BlockName(st))
		}
	}

	st, ok := srv.GetWitherState(witherID)
	if !ok {
		t.Fatalf("expected active WitherState for entity %d", witherID)
	}
	if st.Phase() != WitherPhaseCharging || st.InvulnerableTicks != WitherInvulnerableTicks {
		t.Fatalf("expected charging phase with %d invulnerable ticks, got phase=%v inv=%d",
			WitherInvulnerableTicks, st.Phase(), st.InvulnerableTicks)
	}
	if st.Health != WitherInitialSpawnHealth || st.MaxHealth != DefaultWitherMaxHealth {
		t.Fatalf("expected initial charging health %.1f/%.1f, got %.1f/%.1f",
			WitherInitialSpawnHealth, DefaultWitherMaxHealth, st.Health, st.MaxHealth)
	}

	// Verify purple Wither boss bar was created and bound to the Wither entity.
	bar, hasBar := srv.GetEntityBossBar(witherID)
	if !hasBar || bar == nil {
		t.Fatalf("expected runtime boss bar for Wither %d", witherID)
	}
	if bar.Color != BossBarColorPurple || !bar.DarkenScreen {
		t.Fatalf("expected purple boss bar with DarkenScreen=true, got color=%v darken=%v", bar.Color, bar.DarkenScreen)
	}
}

func TestWitherPhasesSkullsAndDeathDrop(t *testing.T) {
	srv := newTestMapServer(t)
	srv.SetDifficulty(DifficultyNormal)

	player, err := srv.RegisterPlayer(Profile{
		UUID: [16]byte{5, 6, 7, 8},
		Name: "WitherFighter",
	}, func(id int32, body []byte) error {
		return nil
	})
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	srv.SetPlayerGameMode(player, GameModeSurvival)
	srv.SetPlayerHealth(player, 20.0, 20, 5.0)
	srv.SetPlayerPosition(player, 0.5, 65.0, 4.5)

	witherID, err := srv.SpawnWither(world.DimensionOverworld, 0.5, 65.0, 0.5, true)
	if err != nil {
		t.Fatalf("SpawnWither: %v", err)
	}

	// 1. While charging (InvulnerableTicks > 0), Wither is immune to damage.
	dealt, killed := srv.DamageWither(witherID, 50.0, player, false)
	if dealt != 0 || killed {
		t.Fatalf("expected 0 damage during charging phase, got dealt=%v killed=%v", dealt, killed)
	}

	// 2. Step through the 220-tick charging phase; health should fill to 300.0, trigger the radius-7 spawn explosion, and transition to Phase 1 (Aerial).
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < int(WitherInvulnerableTicks); i++ {
		srv.TickWithers(rng)
	}
	st, ok := srv.GetWitherState(witherID)
	if !ok {
		t.Fatalf("expected WitherState after charging")
	}
	if st.InvulnerableTicks != 0 || st.Phase() != WitherPhaseAerial || st.Health != DefaultWitherMaxHealth {
		t.Fatalf("expected Phase 1 (Aerial) at 300 HP after 220 ticks, got inv=%d phase=%v hp=%.1f",
			st.InvulnerableTicks, st.Phase(), st.Health)
	}
	if player.Snapshot().Health >= 20.0 {
		t.Fatalf("expected Wither radius-7 spawn explosion to damage nearby player")
	}
	// Restore player health for the Phase 2 & Wither skull impact checks.
	srv.SetPlayerHealth(player, 20.0, 20, 5.0)

	// 3. Damage Wither down to 140 HP (Phase 2: Wither Armor <= 150 HP).
	dealt, killed = srv.DamageWither(witherID, 160.0, player, false)
	if dealt != 160.0 || killed {
		t.Fatalf("expected 160 damage dealt without killing, got dealt=%v killed=%v", dealt, killed)
	}
	st, _ = srv.GetWitherState(witherID)
	if st.Phase() != WitherPhaseArmored || !st.IsPowered() || st.Health != 140.0 {
		t.Fatalf("expected Phase 2 (WitherPhaseArmored) at 140 HP, got phase=%v powered=%v hp=%.1f",
			st.Phase(), st.IsPowered(), st.Health)
	}

	// 4. In Phase 2 (Wither Armor), projectiles are deflected and deal 0 damage!
	projDealt, projKilled := srv.DamageWither(witherID, 25.0, player, true)
	if projDealt != 0 || projKilled {
		t.Fatalf("expected projectile damage to be deflected in Phase 2, got dealt=%v killed=%v", projDealt, projKilled)
	}

	// 5. Shoot a dangerous (blue) Wither Skull at the player and verify impact damage, Wither II effect, and +5 HP Wither heal.
	skullID, shot := srv.ShootWitherSkull(witherID, 0, 0.5, 65.5, 4.5, true)
	if !shot || skullID <= 0 {
		t.Fatalf("expected ShootWitherSkull to spawn skull projectile")
	}
	meta := srv.WitherSkullEntityDataPacket(skullID)
	if len(meta) == 0 {
		t.Fatalf("expected non-empty WitherSkull SynchedEntityData packet")
	}
	for i := 0; i < 15; i++ {
		srv.TickWithers(rng)
	}
	if !player.HasPlayerEffect(EffectWither) {
		t.Fatalf("expected Wither skull hit to inflict EffectWither on player")
	}
	st, _ = srv.GetWitherState(witherID)
	if st.Health <= 140.0 {
		t.Fatalf("expected Wither to heal +5 HP from skull hit, got %.1f", st.Health)
	}

	// 6. Deal lethal melee damage to the Wither and verify Nether Star drop + boss bar removal.
	_, killed = srv.DamageWither(witherID, 200.0, player, false)
	if !killed {
		t.Fatalf("expected Wither to be killed by 200 melee damage")
	}
	if _, stillAlive := srv.GetWitherState(witherID); stillAlive {
		t.Fatalf("expected WitherState to be removed on death")
	}
	if _, hasBar := srv.GetEntityBossBar(witherID); hasBar {
		t.Fatalf("expected Wither boss bar to be removed on death")
	}

	foundStar := false
	for _, ent := range srv.Entities().All() {
		if ent.IsItem() && ent.Item.ResolvedID() == NetherStarItemID && ent.Item.Count == 1 {
			foundStar = true
			break
		}
	}
	if !foundStar {
		t.Fatalf("expected killed Wither to drop 1x %s", NetherStarItemID)
	}
}

func TestEnderDragonCrystalsAndHealingBeam(t *testing.T) {
	srv := newTestMapServer(t)
	srv.SetDifficulty(DifficultyNormal)

	dragonID, err := srv.StartEndDragonFight()
	if err != nil || dragonID <= 0 {
		t.Fatalf("StartEndDragonFight: id=%d err=%v", dragonID, err)
	}

	// Verify all 10 End Crystals were spawned at the exact world.EndSpikesForSeed coordinates.
	spikes := world.EndSpikesForSeed(srv.Config().WorldSeed)
	crystals := srv.EndCrystalsInDimension(world.DimensionEnd)
	if len(crystals) != len(spikes) {
		t.Fatalf("expected %d End Crystals on spikes, got %d", len(spikes), len(crystals))
	}

	// Verify pink Ender Dragon boss bar was created.
	bar, hasBar := srv.GetEntityBossBar(dragonID)
	if !hasBar || bar == nil {
		t.Fatalf("expected Ender Dragon boss bar")
	}
	if bar.Color != BossBarColorPink || !bar.PlayBossMusic || !bar.CreateWorldFog {
		t.Fatalf("expected pink dragon boss bar with music & fog, got color=%v music=%v fog=%v",
			bar.Color, bar.PlayBossMusic, bar.CreateWorldFog)
	}

	// Damage the dragon by 30 HP (to 170 HP), move it near crystal[0], and tick 10 times so crystal[0] links and heals it.
	srv.DamageEnderDragon(dragonID, 30.0, nil, false)
	targetCrystal := crystals[0]
	srv.Entities().Update(dragonID, func(e *world.Entity) {
		e.X = targetCrystal.X + 4.0
		e.Y = targetCrystal.Y + 2.0
		e.Z = targetCrystal.Z
	})

	rng := rand.New(rand.NewSource(99))
	for i := 0; i < 10; i++ {
		srv.TickEnderDragons(rng)
	}

	fight := srv.GetEnderDragonFightState()
	if fight.NearestCrystalID != targetCrystal.EntityID {
		t.Fatalf("expected dragon to link to nearest crystal %d, got %d", targetCrystal.EntityID, fight.NearestCrystalID)
	}
	if fight.Health <= 170.0 {
		t.Fatalf("expected linked End Crystal to heal Ender Dragon above 170 HP, got %.1f", fight.Health)
	}
	crState, ok := srv.GetEndCrystalState(targetCrystal.EntityID)
	if !ok || !crState.HasBeamTarget {
		t.Fatalf("expected linked End Crystal to have HasBeamTarget=true")
	}

	// Destroying the linked End Crystal deals 10.0 backlash damage to the Ender Dragon!
	healthBeforeDestroy := fight.Health
	if !srv.DestroyEndCrystal(targetCrystal.EntityID, nil) {
		t.Fatalf("expected DestroyEndCrystal to succeed")
	}
	fightAfter := srv.GetEnderDragonFightState()
	if fightAfter.Health != healthBeforeDestroy-EnderDragonCrystalDestructionDamage {
		t.Fatalf("expected destroying tethered crystal to deal %.1f damage (from %.1f to %.1f), got %.1f",
			EnderDragonCrystalDestructionDamage, healthBeforeDestroy, healthBeforeDestroy-EnderDragonCrystalDestructionDamage, fightAfter.Health)
	}
	if fightAfter.CrystalsAlive != 9 {
		t.Fatalf("expected 9 spike crystals remaining, got %d", fightAfter.CrystalsAlive)
	}
}

func TestEnderDragonPhasesDeathEggAndRespawn(t *testing.T) {
	srv := newTestMapServer(t)
	srv.SetDifficulty(DifficultyNormal)

	player, err := srv.RegisterPlayer(Profile{
		UUID: [16]byte{9, 10, 11, 12},
		Name: "DragonSlayer",
	}, func(id int32, body []byte) error {
		return nil
	})
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	player.SetDimension(world.DimensionEnd)
	srv.SetPlayerGameMode(player, GameModeSurvival)
	srv.SetPlayerPosition(player, 0.5, 66.0, 2.5)

	dragonID, err := srv.StartEndDragonFight()
	if err != nil {
		t.Fatalf("StartEndDragonFight: %v", err)
	}

	// 1. Switch dragon to perched SittingFlaming phase: arrows/projectiles must deal 0 damage!
	srv.SetEnderDragonPhase(DragonPhaseSittingFlaming)
	fight := srv.GetEnderDragonFightState()
	if !fight.Phase.IsSitting() {
		t.Fatalf("expected SittingFlaming to report IsSitting()=true")
	}
	dealt, killed := srv.DamageEnderDragon(dragonID, 25.0, player, true)
	if dealt != 0 || killed {
		t.Fatalf("expected perched Ender Dragon to deflect projectile damage, got dealt=%v killed=%v", dealt, killed)
	}

	// 2. Taking >= 50 melee damage while perched causes the dragon to take off (DragonPhaseTakeoff).
	srv.DamageEnderDragon(dragonID, 55.0, player, false)
	fight = srv.GetEnderDragonFightState()
	if fight.Phase != DragonPhaseTakeoff {
		t.Fatalf("expected dragon to transition to Takeoff after >=50 sitting melee damage, got %v", fight.Phase)
	}

	// 3. Spawn a Dragon's Breath cloud and collect dragon_breath with a glass_bottle.
	srv.SpawnDragonBreathCloud(world.DimensionEnd, 0.5, 66.0, 2.5, dragonID)
	player.SetInventorySlot(world.HotbarContainerOffset, world.NewNamedItemStack("minecraft:glass_bottle", 1))
	player.SetSelectedSlot(0)
	if !srv.TryCollectDragonBreath(player) {
		t.Fatalf("expected TryCollectDragonBreath to fill glass_bottle with dragon_breath")
	}
	if got := player.HeldItem().ResolvedID(); got != DragonBreathItemID {
		t.Fatalf("expected held item %s after collecting breath, got %s", DragonBreathItemID, got)
	}

	// 4. Kill the Ender Dragon (first kill): verify Exit Portal activation, Dragon Egg at (0, portalY+4, 0), and End Gateway!
	_, killed = srv.DamageEnderDragon(dragonID, 500.0, player, false)
	if !killed {
		t.Fatalf("expected Ender Dragon to be killed")
	}
	fight = srv.GetEnderDragonFightState()
	if fight.DragonAlive || !fight.DragonKilled || !fight.PreviouslyKilled {
		t.Fatalf("expected DragonAlive=false, DragonKilled=true, PreviouslyKilled=true; got %+v", fight)
	}
	if fight.GatewaysSpawned != 1 {
		t.Fatalf("expected 1 End Gateway spawned on kill, got %d", fight.GatewaysSpawned)
	}
	if _, hasBar := srv.GetEntityBossBar(dragonID); hasBar {
		t.Fatalf("expected Ender Dragon boss bar to be removed on death")
	}

	endCache := srv.DimensionChunks(world.DimensionEnd)
	portalY := fight.PortalY
	if gotPortal := world.BlockName(endCache.GetBlock(1, portalY, 0)); gotPortal != EndPortalBlockID {
		t.Fatalf("expected active %s at (1,%d,0), got %s", EndPortalBlockID, portalY, gotPortal)
	}
	if gotEgg := world.BlockName(endCache.GetBlock(0, portalY+4, 0)); gotEgg != DragonEggBlockID {
		t.Fatalf("expected %s at (0,%d,0) on first dragon kill, got %s", DragonEggBlockID, portalY+4, gotEgg)
	}

	// Remove the Dragon Egg block (as if a player collected it), then respawn the dragon with 4 End Crystals on the portal rim!
	endCache.SetBlock(0, portalY+4, 0, world.StateAir)

	cardinalSupports := [4][2]int{
		{0, -3},
		{0, 3},
		{-3, 0},
		{3, 0},
	}
	for i, off := range cardinalSupports {
		cid, placed := srv.PlaceEndCrystalItem(nil, world.DimensionEnd, off[0], portalY, off[1])
		if !placed || cid <= 0 {
			t.Fatalf("expected PlaceEndCrystalItem #%d at (%d,%d,%d) to succeed", i, off[0], portalY, off[1])
		}
	}

	// After placing the 4th crystal, the Ender Dragon should be respawned and all 10 spike crystals restored!
	respawnedState := srv.GetEnderDragonFightState()
	if !respawnedState.DragonAlive || respawnedState.DragonEntityID <= 0 || respawnedState.DragonEntityID == dragonID {
		t.Fatalf("expected new Ender Dragon to be respawned after 4 portal crystals, got %+v", respawnedState)
	}
	if respawnedState.CrystalsAlive != 10 {
		t.Fatalf("expected all 10 spike crystals regenerated on respawn, got %d", respawnedState.CrystalsAlive)
	}
	// Exit portal should be deactivated while the respawned dragon is alive.
	if gotPortal := endCache.GetBlock(1, portalY, 0); gotPortal != world.StateAir {
		t.Fatalf("expected exit portal to be deactivated during respawned fight, got %s", world.BlockName(gotPortal))
	}

	// 5. Kill the respawned dragon (second kill): Exit Portal activates again, 2nd gateway spawns, but NO second Dragon Egg is placed!
	_, killed2 := srv.DamageEnderDragon(respawnedState.DragonEntityID, 500.0, player, false)
	if !killed2 {
		t.Fatalf("expected second Ender Dragon kill to succeed")
	}
	finalState := srv.GetEnderDragonFightState()
	if finalState.GatewaysSpawned != 2 {
		t.Fatalf("expected 2 End Gateways spawned after second kill, got %d", finalState.GatewaysSpawned)
	}
	if gotPortal := world.BlockName(endCache.GetBlock(1, portalY, 0)); gotPortal != EndPortalBlockID {
		t.Fatalf("expected active %s after second kill, got %s", EndPortalBlockID, gotPortal)
	}
	if gotEgg := endCache.GetBlock(0, portalY+4, 0); gotEgg != world.StateAir {
		t.Fatalf("expected no second Dragon Egg on subsequent kill, got %s", world.BlockName(gotEgg))
	}

	// Also verify packet encoding helpers decode cleanly.
	wPkt := EncodeWitherEntityDataPacket(10, 250.0, 1, 2, 3, 100)
	r := protocol.NewReader(wPkt)
	if id, err := r.VarInt(); err != nil || id != 10 {
		t.Fatalf("unexpected entity ID in Wither metadata packet: %d err=%v", id, err)
	}
}
