package server

import (
	"math"
	"math/rand"
	"testing"

	"regionio/internal/world"
)

func TestPlayerBlockMiningSpeedAndEffects(t *testing.T) {
	srv, cache := newTestEnchantServer(t)
	sess, err := srv.RegisterPlayer(Profile{
		UUID: OfflineUUID("MinerPlayer"),
		Name: "MinerPlayer",
	}, func(id int32, body []byte) error { return nil })
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	_ = srv.SetPlayerGameMode(sess, GameModeSurvival)
	srv.SetPlayerTransform(sess, 10.5, 64.0, 10.5, 0, 0, true)

	// Clear blocks at player's head so eye is not in water.
	cache.SetBlock(10, 64, 10, world.StateAir)
	cache.SetBlock(10, 65, 10, world.StateAir)

	stoneState := world.StateStone
	obsidianState := mustHardnessTestBlockState(t, "minecraft:obsidian")

	// 1. Bare hand cannot harvest stone; takes 150 ticks (1.5 * 100 / 1.0 = 150).
	if srv.CanPlayerHarvestBlock(sess, stoneState) {
		t.Fatalf("expected bare hand not to harvest stone")
	}
	if got := srv.PlayerBlockTicksToBreak(sess, stoneState); got != 150 {
		t.Fatalf("bare hand ticks to break stone = %d, want 150", got)
	}

	// 2. Equip diamond_pickaxe in hotbar slot 36 (selectedSlot 0).
	sess.SetInventorySlot(36, world.NewNamedItemStack("minecraft:diamond_pickaxe", 1))
	if !srv.CanPlayerHarvestBlock(sess, stoneState) {
		t.Fatalf("expected diamond_pickaxe to harvest stone")
	}
	if !srv.CanPlayerHarvestBlock(sess, obsidianState) {
		t.Fatalf("expected diamond_pickaxe to harvest obsidian")
	}
	if got := srv.PlayerBlockDestroySpeed(sess, stoneState); math.Abs(got-8.0) > 1e-6 {
		t.Fatalf("diamond_pickaxe destroy speed on stone = %v, want 8.0", got)
	}

	// 3. Add Efficiency V (+26 -> 34.0) and Haste II (*1.4 -> 47.6 >= 45 instant-break stone threshold!).
	var pickMeta ItemMeta
	pickMeta.SetEnchantment("minecraft:efficiency", 5)
	SetSlotItemMeta(sess, 36, pickMeta)
	srv.ApplyPlayerEffect(sess, NewStatusEffectInstance(EffectHaste, 600, 1)) // Amplifier 1 = Haste II
	if got := srv.PlayerBlockTicksToBreak(sess, stoneState); got != 0 {
		t.Fatalf("Efficiency V + Haste II diamond_pickaxe ticks on stone = %d, want 0 (instant-mine)", got)
	}
	srv.RemovePlayerEffect(sess, EffectHaste)

	// 4. Mining Fatigue III (Amplifier 2 -> *0.027) drastically slows mining.
	SetSlotItemMeta(sess, 36, ItemMeta{})
	srv.ApplyPlayerEffect(sess, NewStatusEffectInstance(EffectMiningFatigue, 600, 2))
	if got := srv.PlayerBlockDestroySpeed(sess, stoneState); math.Abs(got-8.0*0.027) > 1e-6 {
		t.Fatalf("Mining Fatigue III speed = %v, want %v", got, 8.0*0.027)
	}
	srv.RemovePlayerEffect(sess, EffectMiningFatigue)

	// 5. Submerged eye in water divides speed by 5 unless wearing Aqua Affinity helmet.
	cache.SetBlock(10, 65, 10, world.StateWater)
	if !srv.IsPlayerEyeInWater(sess) {
		t.Fatalf("expected player eye at (10, 65, 10) to be in water")
	}
	if got := srv.PlayerBlockDestroySpeed(sess, stoneState); math.Abs(got-8.0*0.2) > 1e-6 {
		t.Fatalf("submerged without Aqua Affinity speed = %v, want 1.6", got)
	}
	sess.SetInventorySlot(ArmorSlotHead, world.NewNamedItemStack("minecraft:turtle_helmet", 1))
	var helmMeta ItemMeta
	helmMeta.SetEnchantment("minecraft:aqua_affinity", 1)
	SetSlotItemMeta(sess, ArmorSlotHead, helmMeta)
	if got := srv.PlayerBlockDestroySpeed(sess, stoneState); math.Abs(got-8.0) > 1e-6 {
		t.Fatalf("submerged with Aqua Affinity speed = %v, want 8.0", got)
	}
	cache.SetBlock(10, 65, 10, world.StateAir)

	// 6. Airborne (OnGround=false) divides speed by 5.
	srv.SetPlayerTransform(sess, 10.5, 64.0, 10.5, 0, 0, false)
	if got := srv.PlayerBlockDestroySpeed(sess, stoneState); math.Abs(got-8.0*0.2) > 1e-6 {
		t.Fatalf("airborne speed = %v, want 1.6", got)
	}
}

func TestSpawnBlockBreakDropsXPAndInfestedSilverfish(t *testing.T) {
	srv, _ := newTestEnchantServer(t)
	dim := world.DimensionOverworld
	rng := rand.New(rand.NewSource(1234))

	diamondOre := mustHardnessTestBlockState(t, "minecraft:diamond_ore")
	ironPick := world.NewNamedItemStack("minecraft:iron_pickaxe", 1)
	woodenPick := world.NewNamedItemStack("minecraft:wooden_pickaxe", 1)

	// 1. Iron pickaxe on diamond_ore spawns diamond drop + XP orbs (3..7).
	drops, xp, sfID := srv.SpawnBlockBreakDropsAndXPAt(dim, 20, 64, 20, diamondOre, ironPick, ItemMeta{}, rng)
	if len(drops) != 1 || xp < 3 || xp > 7 || sfID != 0 {
		t.Fatalf("diamond_ore with iron_pickaxe: drops=%v xp=%d sfID=%d, want 1 drop and xp in [3,7]", drops, xp, sfID)
	}

	// 2. Wooden pickaxe on diamond_ore spawns 0 drops and 0 XP.
	dropsWood, xpWood, _ := srv.SpawnBlockBreakDropsAndXPAt(dim, 22, 64, 20, diamondOre, woodenPick, ItemMeta{}, rng)
	if len(dropsWood) != 0 || xpWood != 0 {
		t.Fatalf("diamond_ore with wooden_pickaxe: drops=%v xp=%d, want empty and 0 XP", dropsWood, xpWood)
	}

	// 3. Infested stone broken without Silk Touch spawns silverfish (and invokes hook if registered).
	infestedStone := mustHardnessTestBlockState(t, "minecraft:infested_stone")
	_, _, sfDefault := srv.SpawnBlockBreakDropsAndXPAt(dim, 25, 64, 25, infestedStone, ironPick, ItemMeta{}, rng)
	if sfDefault <= 0 {
		t.Fatalf("expected infested_stone without Silk Touch to spawn a silverfish, got entityID=%d", sfDefault)
	}

	// Silk Touch suppresses silverfish spawn.
	var silkMeta ItemMeta
	silkMeta.SetEnchantment("minecraft:silk_touch", 1)
	_, _, sfSilk := srv.SpawnBlockBreakDropsAndXPAt(dim, 26, 64, 25, infestedStone, ironPick, silkMeta, rng)
	if sfSilk != 0 {
		t.Fatalf("expected Silk Touch on infested_stone not to spawn silverfish, got entityID=%d", sfSilk)
	}

	// Custom hook for Agent A.
	var hookCalled bool
	SetInfestedSilverfishSpawnHook(func(s *Server, d string, x, y, z float64, st uint16) int32 {
		hookCalled = true
		id, _ := s.SpawnMobInDimension(d, x, y, z, "minecraft:silverfish")
		return id
	})
	defer SetInfestedSilverfishSpawnHook(nil)

	if id, ok := srv.BreakInfestedBlockAt(dim, 27, 64, 25, infestedStone, false); !ok || id <= 0 || !hookCalled {
		t.Fatalf("expected custom InfestedSilverfishSpawnHook to be invoked: ok=%v id=%d hookCalled=%v", ok, id, hookCalled)
	}
}

func TestServerTurtleEggPlaceTrampleAndHatch(t *testing.T) {
	srv, cache := newTestEnchantServer(t)
	dim := world.DimensionOverworld
	bx, by, bz := 30, 64, 30

	sandState := mustHardnessTestBlockState(t, "minecraft:sand")
	cache.SetBlock(bx, by-1, bz, sandState)
	cache.SetBlock(bx, by, bz, world.StateAir)

	// Stack 3 turtle eggs.
	for want := 1; want <= 3; want++ {
		st, ok := srv.PlaceOrStackTurtleEggAt(dim, bx, by, bz)
		if !ok {
			t.Fatalf("PlaceOrStackTurtleEggAt(%d) failed", want)
		}
		eggs, _, _ := world.TurtleEggCountAndHatch(st)
		if eggs != want {
			t.Fatalf("after place %d: eggs=%d, want %d", want, eggs, want)
		}
	}

	// Sneaking does not trample; falling > 0.5 without sneaking decrements 3 -> 2.
	if _, trampled := srv.TrampleTurtleEggBlockAt(dim, bx, by, bz, 1.5, true); trampled {
		t.Fatalf("sneaking should not trample turtle egg")
	}
	rem, trampled := srv.TrampleTurtleEggBlockAt(dim, bx, by, bz, 1.5, false)
	if !trampled || rem != 2 {
		t.Fatalf("TrampleTurtleEggBlockAt: trampled=%v rem=%d, want true, 2", trampled, rem)
	}

	// Set hatch=2 with 2 eggs and trigger night-window hatch -> spawns 2 baby turtles and clears block.
	eggState2Hatch2 := world.TurtleEggState(2, 2)
	if eggState2Hatch2 == world.StateAir {
		t.Fatalf("TurtleEggState(2, 2) not found")
	}
	cache.SetBlock(bx, by, bz, eggState2Hatch2)
	_, hatched, turtleIDs := srv.RandomTickTurtleEggAndSpawnBabiesAt(dim, bx, by, bz, 22000, rand.New(rand.NewSource(1)))
	if !hatched || len(turtleIDs) != 2 {
		t.Fatalf("RandomTickTurtleEggAndSpawnBabiesAt: hatched=%v turtleIDs=%v, want hatched=true with 2 turtles", hatched, turtleIDs)
	}
	if got := cache.GetBlock(bx, by, bz); got != world.StateAir {
		t.Fatalf("after hatching, block = %q, want air", world.BlockName(got))
	}
}

func mustHardnessTestBlockState(t *testing.T, name string) uint16 {
	t.Helper()
	st, ok := world.BlockStateIDByProperties(name, nil)
	if !ok {
		t.Fatalf("BlockStateIDByProperties(%q) not found", name)
	}
	return st
}

