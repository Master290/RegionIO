package server

import (
	"math/rand"
	"testing"

	"regionio/internal/world"
)

func TestEndDragonFightMainHooksGatewaysAndSpawnPlatform(t *testing.T) {
	srv := newTestMapServer(t)
	srv.SetDifficulty(DifficultyNormal)

	player, err := srv.RegisterPlayer(Profile{
		UUID: [16]byte{21, 22, 23, 24},
		Name: "EndExplorer",
	}, func(id int32, body []byte) error {
		return nil
	})
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	player.SetDimension(world.DimensionEnd)
	srv.SetPlayerGameMode(player, GameModeSurvival)
	srv.SetPlayerPosition(player, 0.5, 66.0, 2.5)

	// 1. Verify next gateway slot and coordinates match world.EndRingGatewayOrder(seed) and world.EndRingGatewayPos(slot).
	order := world.EndRingGatewayOrder(srv.Config().WorldSeed)
	expectedPos := world.EndRingGatewayPos(order[0])
	slot, nextPos, ok := srv.NextEndGatewaySlotAndPos()
	if !ok || slot != order[0] || nextPos.X != expectedPos[0] || nextPos.Y != expectedPos[1] || nextPos.Z != expectedPos[2] {
		t.Fatalf("NextEndGatewaySlotAndPos = (slot=%d, pos=%+v, ok=%v), want (slot=%d, pos=%v, true)",
			slot, nextPos, ok, order[0], expectedPos)
	}

	// 2. Start fight and kill the dragon -> spawns 1st gateway at expectedPos with StructureBlockEntity.
	dragonID, err := srv.StartEndDragonFight()
	if err != nil {
		t.Fatalf("StartEndDragonFight: %v", err)
	}
	_, killed := srv.DamageEnderDragon(dragonID, 500.0, player, false)
	if !killed {
		t.Fatalf("expected Ender Dragon kill to succeed")
	}

	endCache := srv.DimensionChunks(world.DimensionEnd)
	if got := world.BlockName(endCache.GetBlock(expectedPos[0], expectedPos[1], expectedPos[2])); got != EndGatewayBlockID {
		t.Fatalf("expected %s at seed-shuffled ring gateway %v, got %s", EndGatewayBlockID, expectedPos, got)
	}

	// 3. Resolve & teleport player through the central ring gateway -> teleports to outer island and creates paired return gateway!
	exitPos, retGatewayPos, exact, resolved := srv.ResolveEndGatewayExitAt(expectedPos[0], expectedPos[1], expectedPos[2])
	if !resolved || exact {
		t.Fatalf("ResolveEndGatewayExitAt(ring) = (exit=%v, ret=%v, exact=%v, ok=%v), want non-exact outer island target",
			exitPos, retGatewayPos, exact, resolved)
	}
	if got := world.BlockName(endCache.GetBlock(retGatewayPos[0], retGatewayPos[1], retGatewayPos[2])); got != EndGatewayBlockID {
		t.Fatalf("expected paired return %s at %v, got %s", EndGatewayBlockID, retGatewayPos, got)
	}

	dest, teleOK := srv.TeleportPlayerThroughEndGateway(player, expectedPos[0], expectedPos[1], expectedPos[2])
	if !teleOK || dest != exitPos {
		t.Fatalf("TeleportPlayerThroughEndGateway(ring) = (%v, %v), want (%v, true)", dest, teleOK, exitPos)
	}

	// 4. Teleport back through the outer-island return gateway -> returns to EndSpawnPointPos (100, 50, 0) and generates 5x5 obsidian platform at y=48!
	retDest, retOK := srv.TeleportPlayerThroughEndGateway(player, retGatewayPos[0], retGatewayPos[1], retGatewayPos[2])
	if !retOK || retDest != world.EndSpawnPointPos {
		t.Fatalf("TeleportPlayerThroughEndGateway(return) = (%v, %v), want (%v, true)", retDest, retOK, world.EndSpawnPointPos)
	}
	if got := world.BlockName(endCache.GetBlock(100, 48, 0)); got != "minecraft:obsidian" {
		t.Fatalf("expected 5x5 obsidian platform at (100,48,0), got %s", got)
	}
}

func TestEndSpikeIronBarsCrystalChainExplosionAndStagedRespawn(t *testing.T) {
	srv := newTestMapServer(t)
	srv.SetDifficulty(DifficultyNormal)

	dragonID, err := srv.StartEndDragonFight()
	if err != nil {
		t.Fatalf("StartEndDragonFight: %v", err)
	}

	// 1. Verify guarded spikes have minecraft:iron_bars cages placed around the crystal.
	endCache := srv.DimensionChunks(world.DimensionEnd)
	spikes := world.EndSpikesForSeed(srv.Config().WorldSeed)
	foundGuarded := false
	for _, sp := range spikes {
		if sp.Guarded {
			foundGuarded = true
			topBar := world.BlockName(endCache.GetBlock(sp.CenterX, sp.Height+3, sp.CenterZ))
			sideBar := world.BlockName(endCache.GetBlock(sp.CenterX+2, sp.Height+1, sp.CenterZ))
			if topBar != "minecraft:iron_bars" || sideBar != "minecraft:iron_bars" {
				t.Fatalf("expected guarded spike at (%d,%d,%d) to have minecraft:iron_bars cage, got top=%s side=%s",
					sp.CenterX, sp.Height, sp.CenterZ, topBar, sideBar)
			}
		}
	}
	if !foundGuarded {
		t.Fatalf("expected at least 1 guarded spike in world.EndSpikesForSeed")
	}

	// 2. DamageEndCrystal ignores damage from ender_dragon, and chain-explodes nearby crystals on valid hit.
	obsidian, _ := world.BlockStateByName("minecraft:obsidian", nil)
	endCache.SetBlock(20, 64, 20, obsidian)
	endCache.SetBlock(20, 65, 20, world.StateAir)
	endCache.SetBlock(20, 66, 20, world.StateAir)
	endCache.SetBlock(22, 64, 20, obsidian)
	endCache.SetBlock(22, 65, 20, world.StateAir)
	endCache.SetBlock(22, 66, 20, world.StateAir)
	c1, ok1 := srv.PlaceEndCrystalItem(nil, world.DimensionEnd, 20, 64, 20)
	c2, ok2 := srv.PlaceEndCrystalItem(nil, world.DimensionEnd, 22, 64, 20)
	if !ok1 || !ok2 {
		t.Fatalf("failed to place test End Crystals: ok1=%v ok2=%v", ok1, ok2)
	}
	if srv.DamageEndCrystal(c1, 10.0, MobTypeEnderDragon, nil) {
		t.Fatalf("expected End Crystal to ignore damage from minecraft:ender_dragon")
	}
	if !srv.DamageEndCrystal(c1, 1.0, "arrow", nil) {
		t.Fatalf("expected DamageEndCrystal(arrow) to destroy crystal c1")
	}
	if _, stillAlive := srv.GetEndCrystalState(c2); stillAlive {
		t.Fatalf("expected adjacent End Crystal c2 within 6 blocks to chain-explode")
	}

	// 3. Kill the dragon, place 4 portal crystals manually, and run the 100-tick staged respawn ceremony.
	_, _ = srv.DamageEnderDragon(dragonID, 500.0, nil, false)
	portalY := srv.GetEnderDragonFightState().PortalY
	endCache.SetBlock(0, portalY+4, 0, world.StateAir)

	cardinalOffsets := [4][2]int{{0, -3}, {0, 3}, {-3, 0}, {3, 0}}
	for _, off := range cardinalOffsets {
		srv.spawnEndCrystalInternal(world.DimensionEnd, float64(off[0])+0.5, float64(portalY+1), float64(off[1])+0.5, false, -1)
	}

	portalCrystals, started := srv.StartEnderDragonRespawnSequence()
	if !started || len(portalCrystals) != 4 {
		t.Fatalf("StartEnderDragonRespawnSequence = (%v, %v), want 4 crystals", portalCrystals, started)
	}
	// Verify portal crystals initially point their beams skyward to (0, 128, 0).
	if cr0, ok := srv.GetEndCrystalState(portalCrystals[0]); !ok || !cr0.HasBeamTarget || cr0.BeamY != EnderDragonRespawnSkyBeamY {
		t.Fatalf("expected portal crystal beam targeting (0,%d,0), got %+v", EnderDragonRespawnSkyBeamY, cr0)
	}

	var respawnedID int32
	for tick := 0; tick < EnderDragonRespawnDurationTicks; tick++ {
		id, done := srv.StepEnderDragonRespawnSequence()
		if done {
			respawnedID = id
			break
		}
	}
	if respawnedID <= 0 || !srv.GetEnderDragonFightState().DragonAlive {
		t.Fatalf("expected staged respawn sequence to spawn new Ender Dragon at tick 100, got id=%d", respawnedID)
	}
}

func TestWitherMobTargetingWitherRoseAndCustomNameBossBar(t *testing.T) {
	srv := newTestMapServer(t)
	srv.SetDifficulty(DifficultyNormal)

	player, err := srv.RegisterPlayer(Profile{
		UUID: [16]byte{31, 32, 33, 34},
		Name: "RoseTester",
	}, func(id int32, body []byte) error {
		return nil
	})
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	srv.SetPlayerGameMode(player, GameModeSurvival)
	srv.SetPlayerHealth(player, 20.0, 20, 5.0)

	// 1. Wither target filter: targets living non-undead mobs, ignores undead & ghasts.
	if !IsValidWitherTargetMob(MobTypePig) || !IsValidWitherTargetMob(MobTypeVillager) || !IsValidWitherTargetMob(MobTypeIronGolem) {
		t.Fatalf("expected pig, villager, and iron_golem to be valid Wither targets")
	}
	if IsValidWitherTargetMob(MobTypeZombie) || IsValidWitherTargetMob(MobTypeSkeleton) || IsValidWitherTargetMob(MobTypeWitherSkeleton) || IsValidWitherTargetMob("minecraft:ghast") {
		t.Fatalf("expected undead mobs and ghasts to NOT be targeted by Wither")
	}

	// 2. Wither skull killing a pig on soul_sand heals the Wither +5 HP and plants minecraft:wither_rose!
	ovCache := srv.DimensionChunks(world.DimensionOverworld)
	soulSand, _ := world.BlockStateByName(SoulSandBlockID, nil)
	for x := 8; x <= 12; x++ {
		for z := 5; z <= 12; z++ {
			ovCache.SetBlock(x, 64, z, soulSand)
			for y := 65; y <= 70; y++ {
				ovCache.SetBlock(x, y, z, world.StateAir)
			}
		}
	}

	witherID, err := srv.SpawnWither(world.DimensionOverworld, 10.5, 65.0, 6.5, false)
	if err != nil {
		t.Fatalf("SpawnWither: %v", err)
	}
	srv.DamageWither(witherID, 20.0, nil, false) // 280 HP

	pigID, err := srv.SpawnMob(10.5, 65.0, 10.5, MobTypePig)
	if err != nil {
		t.Fatalf("SpawnMob(pig): %v", err)
	}
	srv.SetMobHealth(pigID, 4.0)

	_, shot := srv.ShootWitherSkull(witherID, 0, 10.5, 65.5, 10.5, false)
	if !shot {
		t.Fatalf("expected ShootWitherSkull to succeed")
	}
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 15; i++ {
		srv.TickWithers(rng)
	}
	if got := world.BlockName(ovCache.GetBlock(10, 65, 10)); got != WitherRoseBlockID {
		t.Fatalf("expected killed pig on soul_sand to plant %s at (10,65,10), got %s", WitherRoseBlockID, got)
	}
	if wst, ok := srv.GetWitherState(witherID); !ok || wst.Health <= 280.0 {
		t.Fatalf("expected Wither to heal +5 HP on skull hit against mob, got %.1f", wst.Health)
	}

	// 3. When ground cannot support wither_rose (e.g. stone), OnMobKilledByWither drops 1x minecraft:wither_rose item.
	ovCache.SetBlock(20, 64, 20, world.StateStone)
	ovCache.SetBlock(20, 65, 20, world.StateAir)
	placed, itemID := srv.OnMobKilledByWither(world.DimensionOverworld, 20.5, 65.0, 20.5)
	if placed || itemID <= 0 {
		t.Fatalf("expected OnMobKilledByWither on stone to drop wither_rose item, got placed=%v itemID=%d", placed, itemID)
	}

	// 4. ApplyWitherRoseContact applies Wither I + 1.0 damage to survival player.
	if !srv.ApplyWitherRoseContact(player, 0) || !player.HasPlayerEffect(EffectWither) {
		t.Fatalf("expected ApplyWitherRoseContact to apply EffectWither to player")
	}

	// 5. Custom name updates Wither boss bar title, and bosses are immune to status effects.
	if !srv.SetWitherCustomName(witherID, "Named Wither") {
		t.Fatalf("expected SetWitherCustomName to succeed")
	}
	bar, hasBar := srv.GetEntityBossBar(witherID)
	if !hasBar || bar.Name.Text != "Named Wither" {
		t.Fatalf("expected Wither boss bar title 'Named Wither', got %+v", bar)
	}
	if !IsBossImmuneToStatusEffect(MobTypeWither, EffectWither) || !IsBossImmuneToStatusEffect(MobTypeEnderDragon, EffectPoison) {
		t.Fatalf("expected Wither and Ender Dragon to be immune to status effects")
	}
}

func TestEnderDragonBlockDestructionPartDamageAndDragonEggTeleport(t *testing.T) {
	srv := newTestMapServer(t)
	endCache := srv.DimensionChunks(world.DimensionEnd)

	// 1. DestroyDragonCollidingBlocks breaks non-immune blocks (oak_planks) and preserves #minecraft:dragon_immune (end_stone, obsidian).
	planks, _ := world.BlockStateByName("minecraft:oak_planks", nil)
	endStone, _ := world.BlockStateByName("minecraft:end_stone", nil)
	obsidian, _ := world.BlockStateByName("minecraft:obsidian", nil)
	endCache.SetBlock(30, 70, 30, planks)
	endCache.SetBlock(31, 70, 30, endStone)
	endCache.SetBlock(30, 70, 31, obsidian)

	broken := srv.DestroyDragonCollidingBlocks(30.5, 70.0, 30.5)
	if broken != 1 {
		t.Fatalf("expected 1 non-immune block broken by Ender Dragon, got %d", broken)
	}
	if endCache.GetBlock(30, 70, 30) != world.StateAir {
		t.Fatalf("expected oak_planks to be destroyed by Ender Dragon")
	}
	if world.BlockName(endCache.GetBlock(31, 70, 30)) != "minecraft:end_stone" ||
		world.BlockName(endCache.GetBlock(30, 70, 31)) != "minecraft:obsidian" {
		t.Fatalf("expected end_stone and obsidian to be preserved by #minecraft:dragon_immune")
	}

	// 2. Sub-hitbox damage scaling: head = 100%, body/wing = 25% + 1.0.
	if got := ScaleEnderDragonPartDamage(20.0, "head"); got != 20.0 {
		t.Fatalf("ScaleEnderDragonPartDamage(20, head) = %v, want 20.0", got)
	}
	if got := ScaleEnderDragonPartDamage(20.0, "body"); got != 6.0 {
		t.Fatalf("ScaleEnderDragonPartDamage(20, body) = %v, want 6.0", got)
	}

	// 3. Dragon Egg block teleports to a nearby air block when interacted with.
	eggState, _ := world.BlockStateByName(DragonEggBlockID, nil)
	endCache.SetBlock(0, 68, 0, eggState)
	newPos, ok := srv.TeleportDragonEggBlock(world.DimensionEnd, 0, 68, 0, rand.New(rand.NewSource(11)))
	if !ok || (newPos.X == 0 && newPos.Y == 68 && newPos.Z == 0) {
		t.Fatalf("expected TeleportDragonEggBlock to move dragon_egg to a new position, got (%+v, %v)", newPos, ok)
	}
	if endCache.GetBlock(0, 68, 0) != world.StateAir {
		t.Fatalf("expected old dragon_egg position (0,68,0) to be cleared to air")
	}
	if world.BlockName(endCache.GetBlock(newPos.X, newPos.Y, newPos.Z)) != DragonEggBlockID {
		t.Fatalf("expected %s at new position %+v", DragonEggBlockID, newPos)
	}
}
