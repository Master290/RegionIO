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
