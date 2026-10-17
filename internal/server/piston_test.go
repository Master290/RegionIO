package server

import (
	"testing"

	"regionio/internal/world"
)

func TestServerPistonShattersDecoratedPotAndBouncesEntityWithSlime(t *testing.T) {
	cfg := DefaultConfig()
	cfg.WorldDir = t.TempDir()
	cache := world.NewCache(-1, world.GenerateFlat)
	srv, err := NewWithCache(cfg, cache)
	if err != nil {
		t.Fatalf("NewWithCache: %v", err)
	}
	dim := world.DimensionOverworld
	srv.BindPistonAndRedstoneHooks(dim)
	y := world.FlatSurfaceY + 1

	// 1. Place a piston at (10, y, 10) facing east and a decorated_pot at (11, y, 10)
	//    holding 4 custom sherds and 3 emeralds.
	pistonSt, ok := world.BlockStateIDByProperties("minecraft:piston", map[string]string{
		"facing":   "east",
		"extended": "false",
	})
	if !ok {
		t.Fatal("failed to resolve piston state")
	}
	potSt := DecoratedPotBlockState("north", false, false)
	leverSt, _ := world.BlockStateIDByProperties("minecraft:lever", map[string]string{
		"face":    "floor",
		"facing":  "north",
		"powered": "false",
	})

	cache.ApplyBlockWithUpdates(10, y, 10, pistonSt)
	cache.ApplyBlockWithUpdates(11, y, 10, potSt)
	srv.SetDecoratedPotSherds(dim, 11, y, 10, [4]string{
		"minecraft:angler_pottery_sherd",
		"minecraft:archer_pottery_sherd",
		"minecraft:brewer_pottery_sherd",
		"minecraft:prize_pottery_sherd",
	})
	srv.SetDecoratedPotItem(dim, 11, y, 10, world.NewNamedItemStack("minecraft:emerald", 3), ItemMeta{})
	cache.ApplyBlockWithUpdates(10, y, 9, leverSt)

	// Extend piston: decorated_pot is destroyed on push and shatters into its 4 sherds + 3 emeralds!
	cache.ToggleLever(10, y, 9)
	cache.StepBlockUpdates(world.PistonTickDelay)

	if _, exists := srv.GetDecoratedPotAt(dim, 11, y, 10); exists {
		t.Fatal("expected decorated pot state to be removed when shattered by piston")
	}
	allEntities := srv.entities.AppendAll(nil)
	sherdCount := 0
	emeraldCount := 0
	for _, e := range allEntities {
		if !e.IsItem() {
			continue
		}
		id := e.Item.ResolvedID()
		if IsPotterySherd(id) {
			sherdCount += int(e.Item.Count)
		} else if id == "minecraft:emerald" {
			emeraldCount += int(e.Item.Count)
		}
	}
	if sherdCount != 4 || emeraldCount != 3 {
		t.Fatalf("shattered decorated pot dropped %d sherds and %d emeralds, want 4 sherds and 3 emeralds", sherdCount, emeraldCount)
	}

	// 2. Sticky piston at (20, y2, 20) facing east, slime block at (21, y2, 20),
	//    and an armor stand entity standing at (22.5, y2, 20.5):
	//    extending the piston pushes the slime block into (22, y2, 20), displacing the entity
	//    to x=23.5 and recording a (1, 0, 0) slime bounce velocity!
	y2 := world.FlatSurfaceY + 2
	stickySt, _ := world.BlockStateIDByProperties("minecraft:sticky_piston", map[string]string{
		"facing":   "east",
		"extended": "false",
	})
	slimeSt, _ := world.BlockStateIDByProperties("minecraft:slime_block", nil)
	wallLeverSt, _ := world.BlockStateIDByProperties("minecraft:lever", map[string]string{
		"face":    "wall",
		"facing":  "north",
		"powered": "false",
	})
	cache.ApplyBlockWithUpdates(20, y2, 20, stickySt)
	cache.ApplyBlockWithUpdates(21, y2, 20, slimeSt)
	cache.ApplyBlockWithUpdates(20, y2, 19, wallLeverSt)

	standID, err := srv.SpawnArmorStand(dim, 22.5, float64(y2), 20.5, 0)
	if err != nil {
		t.Fatalf("SpawnArmorStand: %v", err)
	}
	cache.ToggleLever(20, y2, 19)
	cache.StepBlockUpdates(world.PistonTickDelay)

	movedStand := srv.entities.Get(standID)
	if movedStand == nil {
		t.Fatal("expected armor stand entity to exist")
	}
	if movedStand.X < 23.4 {
		t.Fatalf("armor stand X after slime piston push = %.2f, want >= 23.5", movedStand.X)
	}
	vel, ok := srv.LastPistonSlimeVelocity(standID)
	if !ok || vel != [3]float64{1, 0, 0} {
		t.Fatalf("LastPistonSlimeVelocity = (%v, %v), want ([1 0 0], true)", vel, ok)
	}
}

func TestServerRedstoneHooksTargetLightningRodDaylightAndTripwire(t *testing.T) {
	cfg := DefaultConfig()
	cfg.WorldDir = t.TempDir()
	cache := world.NewCache(-1, world.GenerateFlat)
	srv, err := NewWithCache(cfg, cache)
	if err != nil {
		t.Fatalf("NewWithCache: %v", err)
	}
	dim := world.DimensionOverworld
	srv.BindPistonAndRedstoneHooks(dim)
	y := world.FlatSurfaceY + 1

	// 1. Target block hit with arrow -> power 15.
	targetSt, _ := world.BlockStateIDByProperties("minecraft:target", map[string]string{"power": "0"})
	cache.ApplyBlockWithUpdates(40, y, 40, targetSt)
	power, hit := srv.HitTargetBlockWithProjectile(dim, 40, y, 40, "up", 0.5, 1.0, 0.5, "minecraft:arrow")
	if !hit || power != 15 {
		t.Fatalf("HitTargetBlockWithProjectile = (%d, %v), want (15, true)", power, hit)
	}

	// 2. Lightning rod redirect within 128 blocks.
	rodSt, _ := world.BlockStateIDByProperties("minecraft:lightning_rod", map[string]string{
		"facing":      "up",
		"powered":     "false",
		"waterlogged": "false",
	})
	cache.ApplyBlockWithUpdates(50, y, 50, world.StateStone)
	cache.ApplyBlockWithUpdates(50, y+1, 50, rodSt)
	rx, ry, rz, redirected := srv.RedirectLightningToRod(dim, 60, y+1, 60)
	if !redirected || rx != 50 || ry != y+1 || rz != 50 {
		t.Fatalf("RedirectLightningToRod = (%d,%d,%d, %v), want (50,%d,50, true)", rx, ry, rz, redirected, y+1)
	}
	if world.BlockProperties(cache.GetBlock(50, y+1, 50))["powered"] != "true" {
		t.Fatalf("lightning rod should be powered=true after RedirectLightningToRod")
	}

	// 3. Daylight detector interaction & update.
	detSt, _ := world.BlockStateIDByProperties("minecraft:daylight_detector", map[string]string{
		"inverted": "false",
		"power":    "0",
	})
	cache.ApplyBlockWithUpdates(70, y, 70, detSt)
	if got := srv.UpdateDaylightDetectorBlock(dim, 70, y, 70, 6000, false, false); got != 15 {
		t.Fatalf("UpdateDaylightDetectorBlock at noon = %d, want 15", got)
	}
	if toggled, _ := srv.InteractDaylightDetector(nil, 70, y, 70); !toggled {
		t.Fatal("expected InteractDaylightDetector to toggle inverted state")
	}
	if world.BlockProperties(cache.GetBlock(70, y, 70))["inverted"] != "true" {
		t.Fatal("expected daylight detector inverted=true")
	}

	// 4. Tripwire step & shears disarm break.
	hookEast, _ := world.BlockStateIDByProperties("minecraft:tripwire_hook", map[string]string{
		"facing":   "east",
		"attached": "false",
		"powered":  "false",
	})
	hookWest, _ := world.BlockStateIDByProperties("minecraft:tripwire_hook", map[string]string{
		"facing":   "west",
		"attached": "false",
		"powered":  "false",
	})
	twSt, _ := world.BlockStateIDByProperties("minecraft:tripwire", map[string]string{
		"attached": "false",
		"disarmed": "false",
		"powered":  "false",
		"north":    "false",
		"south":    "false",
		"east":     "false",
		"west":     "false",
	})
	cache.ApplyBlockWithUpdates(80, y, 80, world.StateStone)
	cache.ApplyBlockWithUpdates(84, y, 80, world.StateStone)
	cache.ApplyBlockWithUpdates(81, y, 80, hookEast)
	cache.ApplyBlockWithUpdates(83, y, 80, hookWest)
	cache.ApplyBlockWithUpdates(82, y, 80, twSt)

	if !srv.OnEntityStepTripwire(dim, 82, y, 80) {
		t.Fatal("expected OnEntityStepTripwire to power tripwire")
	}
	if world.BlockProperties(cache.GetBlock(81, y, 80))["powered"] != "true" {
		t.Fatal("expected hook at (81,y,80) to be powered=true")
	}
	cache.StepBlockUpdates(world.TripwireTickDelay)

	// Break with shears -> hooks detach silently without pulsing.
	if !srv.BreakTripwireBlock(dim, 82, y, 80, "minecraft:shears") {
		t.Fatal("expected BreakTripwireBlock to succeed")
	}
	hProps := world.BlockProperties(cache.GetBlock(81, y, 80))
	if hProps["attached"] != "false" || hProps["powered"] != "false" {
		t.Fatalf("hook after shears BreakTripwireBlock = %v, want attached=false, powered=false", hProps)
	}
}
