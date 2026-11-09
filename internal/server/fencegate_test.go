package server

import (
	"testing"

	"regionio/internal/world"
)

func TestServerFenceGateDoubleDoorAndProjectileMechanisms(t *testing.T) {
	s := newDispenserTestServer(t)
	cache := s.DimensionChunks(world.DimensionOverworld)
	y := world.FlatSurfaceY + 5

	// 1. Fence gate interaction opens away from player.
	cache.SetBlock(0, y, 0, world.FenceGateBlockState("minecraft:spruce_fence_gate", "north", false, false, false))
	opened, toggled := s.InteractFenceGate(nil, world.DimensionOverworld, 0, y, 0, "south")
	if !toggled || !opened || world.BlockProperties(cache.GetBlock(0, y, 0))["facing"] != "south" {
		t.Fatalf("expected InteractFenceGate to open gate facing south, got opened=%v toggled=%v props=%+v",
			opened, toggled, world.BlockProperties(cache.GetBlock(0, y, 0)))
	}

	// 2. Double door synchronized toggle opens both left and right doors together.
	cache.SetBlock(5, y-1, 5, world.StateStone)
	cache.SetBlock(6, y-1, 5, world.StateStone)
	leftLower, _ := world.BlockStateByName("minecraft:oak_door", map[string]string{
		"facing": "north", "half": "lower", "hinge": "left", "open": "false", "powered": "false",
	})
	leftUpper, _ := world.BlockStateByName("minecraft:oak_door", map[string]string{
		"facing": "north", "half": "upper", "hinge": "left", "open": "false", "powered": "false",
	})
	rightLower, _ := world.BlockStateByName("minecraft:oak_door", map[string]string{
		"facing": "north", "half": "lower", "hinge": "right", "open": "false", "powered": "false",
	})
	rightUpper, _ := world.BlockStateByName("minecraft:oak_door", map[string]string{
		"facing": "north", "half": "upper", "hinge": "right", "open": "false", "powered": "false",
	})
	cache.SetBlock(5, y, 5, leftLower)
	cache.SetBlock(5, y+1, 5, leftUpper)
	cache.SetBlock(6, y, 5, rightLower)
	cache.SetBlock(6, y+1, 5, rightUpper)

	opened, toggled = s.InteractDoorWithPartner(nil, world.DimensionOverworld, 5, y, 5)
	if !toggled || !opened {
		t.Fatalf("expected InteractDoorWithPartner to open left door")
	}
	if world.BlockProperties(cache.GetBlock(6, y, 5))["open"] != "true" {
		t.Fatalf("expected right partner door at (6, y, 5) to open simultaneously")
	}

	// 3. Wind charge toggles lever via HitBlockWithProjectileMechanism.
	cache.SetBlock(10, y-1, 10, world.StateStone)
	lever, _ := world.BlockStateByName("minecraft:lever", map[string]string{
		"face": "floor", "facing": "north", "powered": "false",
	})
	cache.SetBlock(10, y, 10, lever)
	if !s.HitBlockWithProjectileMechanism(world.DimensionOverworld, 10, y, 10, "minecraft:wind_charge", 99) {
		t.Fatalf("expected wind_charge to toggle lever")
	}
	if world.BlockProperties(cache.GetBlock(10, y, 10))["powered"] != "true" {
		t.Fatalf("expected lever powered=true after wind_charge hit")
	}

	// 4. InteractTrapdoor toggles trapdoor and InteractWithLightBlock cycles light level.
	trapSt, _ := world.BlockStateByName("minecraft:cherry_trapdoor", map[string]string{
		"facing": "north", "half": "bottom", "open": "false", "powered": "false", "waterlogged": "false",
	})
	cache.SetBlock(12, y, 12, trapSt)
	if opened, toggled := s.InteractTrapdoor(nil, world.DimensionOverworld, 12, y, 12); !toggled || !opened {
		t.Fatalf("expected InteractTrapdoor to open cherry_trapdoor, got opened=%v toggled=%v", opened, toggled)
	}

	cache.SetBlock(14, y, 14, world.MakeLightBlockState(7, false))
	if newLvl, ok := s.InteractWithLightBlock(nil, world.DimensionOverworld, 14, y, 14); !ok || newLvl != 8 {
		t.Fatalf("expected InteractWithLightBlock to cycle 7 -> 8, got newLvl=%d ok=%v", newLvl, ok)
	}
}
