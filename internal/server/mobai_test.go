package server

import (
	"math"
	"math/rand"
	"testing"

	"regionio/internal/protocol"
	"regionio/internal/registry"
	"regionio/internal/world"
)

func TestZombieChasesAndAttacksSurvivalPlayer(t *testing.T) {
	srv := newSurvivalTestServer(t)
	col := &survivalPacketCollector{}
	player, err := srv.RegisterPlayer(Profile{Name: "Survivor", UUID: OfflineUUID("Survivor")}, col.send)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	surfaceY := float64(world.FlatSurfaceY + 1)
	srv.SetPlayerTransform(player, 8.5, surfaceY, 14.5, 0, 0, true)
	if err := srv.SetPlayerGameMode(player, GameModeSurvival); err != nil {
		t.Fatalf("SetPlayerGameMode: %v", err)
	}
	col.reset()

	zombieType := registry.EntityTypeIndex(MobTypeZombie)
	zombieID := srv.Entities().Add(&world.Entity{
		TypeID:   zombieType,
		TypeName: MobTypeZombie,
		X:        8.5,
		Y:        surfaceY,
		Z:        8.5,
		OnGround: true,
	})

	rng := rand.New(rand.NewSource(42))
	initialDist := math.Hypot(8.5-8.5, 14.5-8.5)

	// Tick until the zombie reaches attack range and lands its first melee hit.
	attackTick := -1
	for tick := 0; tick < 60; tick++ {
		srv.TickEntities(rng)
		if player.Health() < world.DefaultPlayerHealth {
			attackTick = tick
			break
		}
	}
	if attackTick < 0 {
		z := srv.Entities().Get(zombieID)
		t.Fatalf("zombie at (%v, %v, %v) never attacked player at (8.5, %v, 14.5)", z.X, z.Y, z.Z, surfaceY)
	}

	z := srv.Entities().Get(zombieID)
	curDist := math.Hypot(8.5-z.X, 14.5-z.Z)
	if curDist >= initialDist || curDist > ZombieAttackReach {
		t.Fatalf("zombie distance at first attack = %v, want <= %v", curDist, ZombieAttackReach)
	}

	wantHP := world.DefaultPlayerHealth - ZombieAttackDamage
	if got := player.Health(); got != wantHP {
		t.Fatalf("player health after first zombie hit = %v, want %v", got, wantHP)
	}
	if col.count(protocol.PlayHurtAnimation) == 0 || col.count(protocol.PlaySetHealth) == 0 {
		t.Fatalf("expected PlayHurtAnimation (%d) and PlaySetHealth (%d) on zombie hit",
			col.count(protocol.PlayHurtAnimation), col.count(protocol.PlaySetHealth))
	}

	// During the next (ZombieAttackCooldownTicks - 1) ticks, cooldown prevents another hit.
	for i := 0; i < ZombieAttackCooldownTicks-1; i++ {
		srv.TickEntities(rng)
		if got := player.Health(); got != wantHP {
			t.Fatalf("player took damage during cooldown at tick +%d: health = %v, want %v", i+1, got, wantHP)
		}
	}

	// On the cooldown expiration tick, the zombie lands a second attack.
	srv.TickEntities(rng)
	wantSecondHP := wantHP - ZombieAttackDamage
	if got := player.Health(); got != wantSecondHP {
		t.Fatalf("player health after second zombie hit = %v, want %v", got, wantSecondHP)
	}

	// Set player health to ZombieAttackDamage so the next hit after cooldown is fatal.
	srv.SetPlayerHealth(player, ZombieAttackDamage, world.DefaultPlayerFoodLevel, world.DefaultPlayerSaturation)
	col.reset()
	for i := 0; i < ZombieAttackCooldownTicks; i++ {
		srv.TickEntities(rng)
	}
	if !player.IsDead() || player.Health() != 0 {
		t.Fatalf("expected player to die from zombie attack (isDead=%v, health=%v)", player.IsDead(), player.Health())
	}
	if col.count(protocol.PlayPlayerCombatKill) != 1 {
		t.Fatalf("PlayPlayerCombatKill count = %d, want 1", col.count(protocol.PlayPlayerCombatKill))
	}
}

func TestZombiePathfindsAroundWallAndClimbsOneBlockStep(t *testing.T) {
	srv := newSurvivalTestServer(t)
	feetY := world.FlatSurfaceY + 1

	// Build a 2-block-high wall at Z = 11 across X = 7..9.
	for x := 7; x <= 9; x++ {
		srv.Chunks().SetBlock(x, feetY, 11, world.StateStone)
		srv.Chunks().SetBlock(x, feetY+1, 11, world.StateStone)
	}

	path := FindPath(srv.Chunks(), 8, feetY, 9, 8, feetY, 13, MaxPathSearchNodes)
	if len(path) == 0 {
		t.Fatal("expected FindPath to find a route around the wall")
	}
	last := path[len(path)-1]
	if last.X != 8 || last.Z != 13 {
		t.Fatalf("path end = %+v, want (8, %d, 13)", last, feetY)
	}
	for _, node := range path {
		if node.Z == 11 && node.X >= 7 && node.X <= 9 {
			t.Fatalf("path passed through solid wall block at %+v", node)
		}
	}

	player, err := srv.RegisterPlayer(Profile{Name: "Target", UUID: OfflineUUID("Target")}, nil)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	if err := srv.SetPlayerGameMode(player, GameModeSurvival); err != nil {
		t.Fatalf("SetPlayerGameMode: %v", err)
	}
	srv.SetPlayerTransform(player, 8.5, float64(feetY), 13.5, 0, 0, true)

	zombieID := srv.Entities().Add(&world.Entity{
		TypeID:   registry.EntityTypeIndex(MobTypeZombie),
		TypeName: MobTypeZombie,
		X:        8.5,
		Y:        float64(feetY),
		Z:        9.5,
		OnGround: true,
	})

	rng := rand.New(rand.NewSource(7))
	for tick := 0; tick < 120; tick++ {
		srv.TickEntities(rng)
		z := srv.Entities().Get(zombieID)
		bx := int(math.Floor(z.X))
		bz := int(math.Floor(z.Z))
		if bz == 11 && bx >= 7 && bx <= 9 {
			t.Fatalf("tick %d: zombie clipped through wall at (%v, %v, %v)", tick, z.X, z.Y, z.Z)
		}
		if player.Health() < world.DefaultPlayerHealth {
			break
		}
	}
	if player.Health() >= world.DefaultPlayerHealth {
		z := srv.Entities().Get(zombieID)
		t.Fatalf("zombie failed to navigate around wall and attack player; final pos = (%v, %v, %v)", z.X, z.Y, z.Z)
	}

	// Now test climbing a 1-block step: build a raised platform at Z = 20..22, X = 7..9.
	for x := 7; x <= 9; x++ {
		for z := 20; z <= 22; z++ {
			srv.Chunks().SetBlock(x, feetY, z, world.StateStone)
		}
	}
	srv.SetPlayerTransform(player, 8.5, float64(feetY+1), 21.5, 0, 0, true)
	srv.Entities().Update(zombieID, func(e *world.Entity) {
		e.X = 8.5
		e.Y = float64(feetY)
		e.Z = 18.5
	})

	climbed := false
	for tick := 0; tick < 60; tick++ {
		srv.TickEntities(rng)
		z := srv.Entities().Get(zombieID)
		if int(math.Round(z.Y)) == feetY+1 && z.Z >= 20.0 {
			climbed = true
			break
		}
	}
	if !climbed {
		z := srv.Entities().Get(zombieID)
		t.Fatalf("expected zombie to climb 1-block step to Y=%d, got (%v, %v, %v)", feetY+1, z.X, z.Y, z.Z)
	}
}

func TestZombiePrioritizesSurvivalOverCreativeAndIgnoresSpectator(t *testing.T) {
	srv := newSurvivalTestServer(t)
	feetY := float64(world.FlatSurfaceY + 1)

	specPlayer, err := srv.RegisterPlayer(Profile{Name: "Ghost", UUID: OfflineUUID("Ghost")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = srv.SetPlayerGameMode(specPlayer, GameModeSpectator)
	srv.SetPlayerTransform(specPlayer, 8.5, feetY, 10.5, 0, 0, true) // 2 blocks +Z

	creativePlayer, err := srv.RegisterPlayer(Profile{Name: "Builder", UUID: OfflineUUID("Builder")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = srv.SetPlayerGameMode(creativePlayer, GameModeCreative)
	srv.SetPlayerTransform(creativePlayer, 8.5, feetY, 5.5, 0, 0, true) // 3 blocks -Z

	survPlayer, err := srv.RegisterPlayer(Profile{Name: "Runner", UUID: OfflineUUID("Runner")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = srv.SetPlayerGameMode(survPlayer, GameModeSurvival)
	srv.SetPlayerTransform(survPlayer, 18.5, feetY, 8.5, 0, 0, true) // 10 blocks +X

	zombieID := srv.Entities().Add(&world.Entity{
		TypeID:   registry.EntityTypeIndex(MobTypeZombie),
		TypeName: MobTypeZombie,
		X:        8.5,
		Y:        feetY,
		Z:        8.5,
		OnGround: true,
	})

	for i := 0; i < 10; i++ {
		srv.TickEntities(nil)
	}
	z := srv.Entities().Get(zombieID)
	if z.X <= 9.5 {
		t.Fatalf("expected zombie to chase Survival player along +X, got pos (%v, %v, %v)", z.X, z.Y, z.Z)
	}
}

func TestPassivePigWandersAvoidsHazardsAndItemsStayPut(t *testing.T) {
	srv := newSurvivalTestServer(t)
	feetY := world.FlatSurfaceY + 1

	// Place water around (8, 8) on the West, North, and South sides, leaving +X open.
	for x := 6; x <= 8; x++ {
		for z := 6; z <= 10; z++ {
			if x == 8 && z == 8 {
				continue
			}
			srv.Chunks().SetBlock(x, world.FlatSurfaceY, z, world.StateWater)
		}
	}

	pigID := srv.Entities().Add(&world.Entity{
		TypeID:   registry.EntityTypeIndex(MobTypePig),
		TypeName: MobTypePig,
		X:        8.5,
		Y:        float64(feetY),
		Z:        8.5,
		OnGround: true,
	})

	itemID, _ := srv.SpawnItemEntity(8.5, float64(feetY), 8.5, world.NewNamedItemStack("minecraft:dirt", 1))
	srv.Entities().Update(itemID, func(e *world.Entity) {
		e.PickupDelay = 5
	})

	rng := rand.New(rand.NewSource(99))
	moved := false
	for tick := 0; tick < 80; tick++ {
		srv.TickEntities(rng)
		pig := srv.Entities().Get(pigID)
		bx := int(math.Floor(pig.X))
		bz := int(math.Floor(pig.Z))
		if floor := srv.Chunks().GetBlock(bx, world.FlatSurfaceY, bz); floor == world.StateWater {
			t.Fatalf("tick %d: pig stepped into water column (%d, %d) at pos (%v, %v)", tick, bx, bz, pig.X, pig.Z)
		}
		if math.Hypot(pig.X-8.5, pig.Z-8.5) > 0.2 {
			moved = true
		}
	}
	if !moved {
		pig := srv.Entities().Get(pigID)
		t.Fatalf("expected passive pig to wander along walkable terrain, stayed at (%v, %v)", pig.X, pig.Z)
	}

	item := srv.Entities().Get(itemID)
	if item == nil {
		t.Fatal("expected item entity to remain in world")
	}
	if item.X != 8.5 || item.Z != 8.5 {
		t.Fatalf("item entity wandered to (%v, %v), want (8.5, 8.5)", item.X, item.Z)
	}
	if item.PickupDelay != 0 || item.Age != 80 {
		t.Fatalf("item PickupDelay=%d Age=%d, want PickupDelay=0 Age=80", item.PickupDelay, item.Age)
	}
}
