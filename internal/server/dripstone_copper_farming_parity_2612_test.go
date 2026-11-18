package server

import (
	"math"
	"math/rand"
	"testing"

	"regionio/internal/world"
)

func TestServerStalagmiteFallDamageFallingStalactiteAndCauldronDripParity2612(t *testing.T) {
	s, cache := newTestEnchantServer(t)
	dim := world.DimensionOverworld

	sess, err := s.RegisterPlayer(Profile{
		UUID: OfflineUUID("DripstoneTester"),
		Name: "DripstoneTester",
	}, nil)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	_ = s.SetPlayerGameMode(sess, GameModeSurvival)

	// 1. Player falling 2.0 blocks onto upward pointed_dripstone[up, tip] takes 3.0 stalagmite damage
	// (floor((2.0 + 2.5 + 1e-6 - 3.0) * 2.0) = 3), even though 2.0 <= normal 3.0 safe fall distance!
	sx, sy, sz := 10, world.FlatSurfaceY+1, 10
	cache.SetBlock(sx, sy-1, sz, world.StateStone)
	cache.SetBlock(sx, sy, sz, world.MakePointedDripstoneState("up", "tip", false))

	s.SetPlayerTransform(sess, float64(sx)+0.5, float64(sy)+2.6875, float64(sz)+0.5, 0, 0, false)
	s.HandlePlayerFall(sess, float64(sy)+2.6875, float64(sy)+1.6875, false, false)
	s.SetPlayerTransform(sess, float64(sx)+0.5, float64(sy)+0.6875, float64(sz)+0.5, 0, 0, true)
	appliedDmg := s.HandlePlayerFall(sess, float64(sy)+1.6875, float64(sy)+0.6875, false, true)
	if math.Abs(float64(appliedDmg-3.0)) > 1e-4 {
		t.Fatalf("player 2.0-block fall onto stalagmite tip dealt %v, want 3.0", appliedDmg)
	}

	// 2. Collapsing stalactite deals falling_stalactite damage to mob in landing cell after 2-tick delay.
	fx, fz := 16, 10
	for cy := world.FlatSurfaceY + 1; cy <= world.FlatSurfaceY+12; cy++ {
		cache.SetBlock(fx, cy, fz, world.StateAir)
	}
	cache.SetBlock(fx, world.FlatSurfaceY, fz, world.StateStone)
	cache.SetBlock(fx, world.FlatSurfaceY+6, fz, world.StateStone)
	cache.ApplyBlockWithUpdates(fx, world.FlatSurfaceY+5, fz, world.MakePointedDripstoneState("down", "tip", false))

	mobID, _ := s.SpawnMobInDimension(dim, float64(fx)+0.5, float64(world.FlatSurfaceY+1), float64(fz)+0.5, "minecraft:cow")
	beforeHP := s.MobHealth(mobID)

	cache.BreakBlockWithUpdates(fx, world.FlatSurfaceY+6, fz)
	s.TickDimensionBlockUpdates(dim) // tick 1: still suspended
	if s.MobHealth(mobID) != beforeHP {
		t.Fatalf("tick 1 mob took damage prematurely: hp=%v want=%v", s.MobHealth(mobID), beforeHP)
	}
	s.TickDimensionBlockUpdates(dim) // tick 2: falls from y+5 to y+1 (fallDistance=4 -> ceil(4-1)*6 = 18 damage!)
	if gotHP := s.MobHealth(mobID); gotHP >= beforeHP {
		t.Fatalf("tick 2 mob was not hurt by falling stalactite: hp=%v before=%v", gotHP, beforeHP)
	}

	// 3. FindDripstoneFluidAboveCauldron rejects 11-block stalactites (> 10), flowing water, and waterlogged tips.
	cx, cy, cz := 22, world.FlatSurfaceY+1, 10
	for y := cy; y <= cy+20; y++ {
		cache.SetBlock(cx, y, cz, world.StateAir)
	}
	cache.SetBlock(cx, cy, cz, EmptyCauldronBlockState())
	cache.SetBlock(cx, cy+3, cz, world.MakePointedDripstoneState("down", "tip", true)) // waterlogged tip!
	cache.SetBlock(cx, cy+4, cz, world.StateStone)
	cache.SetBlock(cx, cy+5, cz, world.StateWater)
	if got := FindDripstoneFluidAboveCauldron(cache.GetBlock, cx, cy, cz); got != "" {
		t.Fatalf("waterlogged stalactite tip should not drip into cauldron, got %q", got)
	}

	cache.SetBlock(cx, cy+3, cz, world.MakePointedDripstoneState("down", "tip", false))
	flowingWater, _ := world.BlockStateByName("minecraft:water", map[string]string{"level": "2"})
	cache.SetBlock(cx, cy+5, cz, flowingWater)
	if got := FindDripstoneFluidAboveCauldron(cache.GetBlock, cx, cy, cz); got != "" {
		t.Fatalf("flowing water (level=2) should not drip into cauldron, got %q", got)
	}
}

func TestServerOpenCopperChestSkipsOxidationAndEntityPushUpParity2612(t *testing.T) {
	s, cache := newTestEnchantServer(t)
	dim := world.DimensionOverworld
	y := world.FlatSurfaceY + 1

	sess, err := s.RegisterPlayer(Profile{
		UUID: OfflineUUID("ChestViewer"),
		Name: "ChestViewer",
	}, nil)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	_ = s.SetPlayerGameMode(sess, GameModeSurvival)

	// 1. Open copper_chest must NOT oxidize on random tick while a player has it open!
	chestState, _ := world.BlockStateByName("minecraft:copper_chest", map[string]string{
		"facing": "north", "type": "single", "waterlogged": "false",
	})
	cache.SetBlock(10, y, 20, chestState)
	cid, err := s.OpenChest(sess, 10, y, 20)
	if err != nil || cid <= 0 {
		t.Fatalf("OpenChest failed: cid=%d err=%v", cid, err)
	}
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 500; i++ {
		cache.TryOxidizeCopperOnRandomTick(10, y, 20, rng)
	}
	if got := world.BlockName(cache.GetBlock(10, y, 20)); got != "minecraft:copper_chest" {
		t.Fatalf("open copper_chest oxidized while container was open: got %q", got)
	}
	_ = s.CloseContainer(sess, cid, false)
	for i := 0; i < 500; i++ {
		cache.TryOxidizeCopperOnRandomTick(10, y, 20, rng)
		if world.BlockName(cache.GetBlock(10, y, 20)) != "minecraft:copper_chest" {
			break
		}
	}
	if world.BlockName(cache.GetBlock(10, y, 20)) == "minecraft:copper_chest" {
		t.Fatal("closed copper_chest failed to oxidize after closing container")
	}

	// 2. Entity standing on farmland (15/16 = 0.9375) is pushed up to y + 1.0 when farmland turns to dirt!
	fx, fy, fz := 16, world.FlatSurfaceY, 20
	dryFarmland, _ := world.BlockStateByName("minecraft:farmland", map[string]string{"moisture": "0"})
	cache.SetBlock(fx, fy, fz, dryFarmland)
	cache.SetBlock(fx, fy+1, fz, world.StateAir)
	mobID, _ := s.SpawnMobInDimension(dim, float64(fx)+0.5, float64(fy)+0.9375, float64(fz)+0.5, "minecraft:pig")

	s.SetPlayerTransform(sess, float64(fx)+0.5, float64(fy)+0.9375, float64(fz)+0.5, 0, 0, true)
	trampled, _ := s.CheckPlayerFarmlandTrample(sess, 2.5, rand.New(rand.NewSource(1)))
	if !trampled {
		t.Fatal("expected CheckPlayerFarmlandTrample to trample farmland")
	}
	if ent := s.entities.Get(mobID); ent == nil || ent.Y < float64(fy)+1.0-1e-6 {
		t.Fatalf("mob on farmland was not pushed up to %.4f on turnToDirt: got %+v", float64(fy)+1.0, ent)
	}
	if snap := sess.Snapshot(); snap.Y < float64(fy)+1.0-1e-6 {
		t.Fatalf("player on farmland was not pushed up to %.4f on turnToDirt: got %.4f", float64(fy)+1.0, snap.Y)
	}
}
