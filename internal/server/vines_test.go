package server

import (
	"math/rand"
	"testing"

	"regionio/internal/world"
)

func TestServerVinesHarvestBoneMealAndShear(t *testing.T) {
	s := newDispenserTestServer(t)

	cache := s.DimensionChunks(world.DimensionOverworld)
	cache.SetBlock(0, 70, 0, world.StateStone)
	cache.ApplyBlockWithUpdates(0, 69, 0, world.CaveVinesBlockState(3, false))

	// 1. Bone meal grows berries and consumes 1 bone_meal in survival.
	bm := world.NewNamedItemStack("minecraft:bone_meal", 4)
	nextBM, grown := s.BoneMealHangingFloraAt(world.DimensionOverworld, 0, 69, 0, bm, false, rand.New(rand.NewSource(1)))
	if !grown || nextBM.Count != 3 {
		t.Fatalf("expected bone meal to grow berries and decrement count to 3, got grown=%v count=%d", grown, nextBM.Count)
	}
	if !world.CaveVinesHasBerries(cache.GetBlock(0, 69, 0)) {
		t.Fatalf("expected berries=true on cave_vines")
	}

	// 2. Harvest glow berries returns 1x glow_berries and clears berries=false.
	drop, harvested := s.HarvestCaveVinesBerries(nil, world.DimensionOverworld, 0, 69, 0, false)
	if !harvested || drop.ResolvedID() != "minecraft:glow_berries" || drop.Count != 1 {
		t.Fatalf("expected 1x glow_berries on harvest, got harvested=%v drop=%+v", harvested, drop)
	}
	if world.CaveVinesHasBerries(cache.GetBlock(0, 69, 0)) {
		t.Fatalf("expected berries=false after harvest")
	}

	// 3. Shearing tip sets age=25 and damages shears by 1.
	shears := world.NewNamedItemStack("minecraft:shears", 1)
	nextShears, nextMeta, sheared := s.ShearGrowingPlantTip(world.DimensionOverworld, 0, 69, 0, shears, ItemMeta{}, false)
	if !sheared || nextShears.IsEmpty() || nextMeta.Damage != 1 {
		t.Fatalf("expected shears to shear tip and take 1 damage, got sheared=%v meta=%+v", sheared, nextMeta)
	}
	if world.BlockProperties(cache.GetBlock(0, 69, 0))["age"] != "25" {
		t.Fatalf("expected cave_vines age=25 after shearing")
	}
}
