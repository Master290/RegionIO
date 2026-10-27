package server

import (
	"testing"

	"regionio/internal/world"
)

func TestServerWetSpongeNetherDryingAndIceSilkTouch(t *testing.T) {
	s := newDispenserTestServer(t)
	y := 64

	// 1. Placing wet_sponge in the Nether immediately dries it into minecraft:sponge.
	finalSt, dried := s.PlaceWetSpongeAt(world.DimensionNether, 0, y, 0)
	if !dried || !world.IsSpongeBlock(finalSt) {
		t.Fatalf("expected wet_sponge placed in Nether to dry into sponge, got dried=%v block=%s",
			dried, world.BlockName(finalSt))
	}

	// 2. Breaking ice with Silk Touch drops ice and does not create water; without Silk Touch creates water.
	cache := s.DimensionChunks(world.DimensionOverworld)
	ice, _ := world.BlockStateByName("minecraft:ice", nil)
	cache.SetBlock(5, y-1, 5, world.StateStone)
	cache.SetBlock(5, y, 5, ice)

	pick := world.NewNamedItemStack("minecraft:diamond_pickaxe", 1)
	silkMeta := ItemMeta{Enchantments: map[string]int{"minecraft:silk_touch": 1}}
	dropped, water := s.BreakIceBlock(world.DimensionOverworld, 5, y, 5, pick, silkMeta, false)
	if !dropped || water || cache.GetBlock(5, y, 5) != world.StateAir {
		t.Fatalf("expected Silk Touch on ice to drop ice and leave air, got dropped=%v water=%v block=%s",
			dropped, water, world.BlockName(cache.GetBlock(5, y, 5)))
	}

	cache.SetBlock(6, y-1, 6, world.StateStone)
	cache.SetBlock(6, y, 6, ice)
	dropped, water = s.BreakIceBlock(world.DimensionOverworld, 6, y, 6, pick, ItemMeta{}, false)
	if dropped || !water || cache.GetBlock(6, y, 6) != world.StateWater {
		t.Fatalf("expected breaking ice without Silk Touch over stone to turn into water, got dropped=%v water=%v block=%s",
			dropped, water, world.BlockName(cache.GetBlock(6, y, 6)))
	}
}

func TestServerFrostWalkerBootsAndSnowGolemTrail(t *testing.T) {
	srv, cache := newTestEnchantServer(t)
	sess, err := srv.RegisterPlayer(Profile{
		UUID: OfflineUUID("FrostWalkerPlayer"),
		Name: "FrostWalkerPlayer",
	}, nil)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	_ = srv.SetPlayerGameMode(sess, GameModeSurvival)
	y := world.FlatSurfaceY

	// 1. Equip boots with frost_walker: 1 and step near exposed source water.
	cache.SetBlock(10, y, 10, world.StateWater)
	cache.SetBlock(10, y+1, 10, world.StateAir)
	sess.SetInventorySlot(ArmorSlotFeet, world.NewNamedItemStack("minecraft:diamond_boots", 1))
	var bootsMeta ItemMeta
	bootsMeta.SetEnchantment("minecraft:frost_walker", 1)
	SetSlotItemMeta(sess, ArmorSlotFeet, bootsMeta)
	srv.SetPlayerTransform(sess, 9.5, float64(y+1), 10.5, 0, 0, true)

	placed, _ := srv.ApplyPlayerFrostWalkerStep(sess)
	if placed < 1 || !world.IsFrostedIceBlock(cache.GetBlock(10, y, 10)) {
		t.Fatalf("expected ApplyPlayerFrostWalkerStep to freeze (10,y,10) into frosted_ice, placed=%d", placed)
	}

	// 2. StepSnowGolemTrailAndHeat places snow in cold biome and damages in desert.
	cache.SetBlock(14, y, 14, world.StateGrass)
	cache.SetBlock(14, y+1, 14, world.StateAir)
	golemID, _ := srv.SpawnMobInDimension(world.DimensionOverworld, 14.5, float64(y+1), 14.5, "minecraft:snow_golem")
	if golemID == 0 {
		t.Fatal("expected SpawnMobInDimension(snow_golem) to succeed")
	}
	placedSnow, dmg := srv.StepSnowGolemTrailAndHeat(golemID, "minecraft:snowy_plains")
	if placedSnow < 1 || dmg != 0 || !world.IsSnowLayerBlock(cache.GetBlock(14, y+1, 14)) {
		t.Fatalf("StepSnowGolemTrailAndHeat(snowy_plains) = (placed=%d, dmg=%v)", placedSnow, dmg)
	}
	_, hotDmg := srv.StepSnowGolemTrailAndHeat(golemID, "minecraft:desert")
	if hotDmg != 1.0 {
		t.Fatalf("StepSnowGolemTrailAndHeat(desert) heatDamage = %v, want 1.0", hotDmg)
	}
}
