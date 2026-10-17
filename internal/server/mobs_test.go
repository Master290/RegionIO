package server

import (
	"math"
	"math/rand"
	"testing"

	"regionio/internal/world"
)

func mustSpawnTestMob(t *testing.T, srv *Server, typeName string, x, y, z float64) int32 {
	t.Helper()
	id, err := srv.SpawnMob(x, y, z, typeName)
	if err != nil {
		t.Fatalf("SpawnMob(%q, %v, %v, %v): %v", typeName, x, y, z, err)
	}
	return id
}

func TestEntityTypeResolutionAndClassification(t *testing.T) {
	wantIDs := map[string]int{
		MobTypeZombie:   EntityTypeZombieID,
		MobTypeSkeleton: EntityTypeSkeletonID,
		MobTypeCreeper:  EntityTypeCreeperID,
		MobTypeSpider:   EntityTypeSpiderID,
		MobTypePig:      EntityTypePigID,
		MobTypeCow:      EntityTypeCowID,
		MobTypeSheep:    EntityTypeSheepID,
		MobTypeChicken:  EntityTypeChickenID,
		MobTypeVillager: int(EntityTypeVillagerID),
	}
	for name, wantID := range wantIDs {
		if got := ResolveEntityTypeID(name); got != wantID {
			t.Errorf("ResolveEntityTypeID(%q) = %d, want %d", name, got, wantID)
		}
		if maxHP := ResolveMobMaxHealth(name); maxHP <= 0 {
			t.Errorf("ResolveMobMaxHealth(%q) = %v, want > 0", name, maxHP)
		}
	}

	for _, h := range AllHostileMobTypes() {
		if !IsHostileMob(h) {
			t.Errorf("IsHostileMob(%q) = false, want true", h)
		}
		if IsPassiveMob(h) {
			t.Errorf("IsPassiveMob(%q) = true, want false", h)
		}
	}
	for _, p := range AllPassiveAnimalTypes() {
		if !IsPassiveMob(p) || !IsPassiveAnimal(p) {
			t.Errorf("IsPassiveMob/IsPassiveAnimal(%q) = false, want true", p)
		}
		if IsHostileMob(p) {
			t.Errorf("IsHostileMob(%q) = true, want false", p)
		}
	}
}

func TestSkeletonChasesAndShootsArrowAtPlayer(t *testing.T) {
	srv := newSurvivalTestServer(t)
	col := &survivalPacketCollector{}
	player, err := srv.RegisterPlayer(Profile{Name: "ArcherTarget", UUID: OfflineUUID("ArcherTarget")}, col.send)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	surfaceY := float64(world.FlatSurfaceY + 1)
	srv.SetPlayerTransform(player, 8.5, surfaceY, 26.5, 0, 0, true)
	if err := srv.SetPlayerGameMode(player, GameModeSurvival); err != nil {
		t.Fatalf("SetPlayerGameMode: %v", err)
	}
	col.reset()

	skelID := mustSpawnTestMob(t, srv, MobTypeSkeleton, 8.5, surfaceY, 8.5)
	rng := rand.New(rand.NewSource(7))

	// Tick until the skeleton closes inside bow range (15 blocks) and fires its first arrow.
	shotTick := -1
	for tick := 0; tick < 80; tick++ {
		srv.TickEntities(rng)
		if player.Health() < world.DefaultPlayerHealth {
			shotTick = tick
			break
		}
	}
	if shotTick < 0 {
		sk := srv.Entities().Get(skelID)
		t.Fatalf("skeleton at (%v, %v, %v) never shot player at (8.5, %v, 26.5)", sk.X, sk.Y, sk.Z, surfaceY)
	}

	sk := srv.Entities().Get(skelID)
	dist := math.Hypot(8.5-sk.X, 26.5-sk.Z)
	if dist > SkeletonBowRange {
		t.Fatalf("skeleton distance at first arrow shot = %v, want <= %v", dist, SkeletonBowRange)
	}

	hpAfterFirstShot := player.Health()
	if hpAfterFirstShot >= world.DefaultPlayerHealth {
		t.Fatalf("expected player health < %v after skeleton arrow, got %v", world.DefaultPlayerHealth, hpAfterFirstShot)
	}
	if col.count(PlaySoundPacketID) == 0 {
		t.Fatal("expected skeleton bow shoot sound packet")
	}

	// During the next (SkeletonShootCooldownTicks - 1) ticks, cooldown prevents another arrow.
	for i := 0; i < SkeletonShootCooldownTicks-1; i++ {
		srv.TickEntities(rng)
		if got := player.Health(); got != hpAfterFirstShot {
			t.Fatalf("player took arrow damage during cooldown at tick +%d: health = %v, want %v", i+1, got, hpAfterFirstShot)
		}
	}

	// On the cooldown expiration tick, the skeleton fires a second arrow.
	srv.TickEntities(rng)
	if got := player.Health(); got >= hpAfterFirstShot {
		t.Fatalf("expected second arrow hit on cooldown expiry: health = %v, want < %v", got, hpAfterFirstShot)
	}
}

func TestCreeperSwellsDefusesAndExplodes(t *testing.T) {
	srv := newSurvivalTestServer(t)
	col := &survivalPacketCollector{}
	player, err := srv.RegisterPlayer(Profile{Name: "Miner", UUID: OfflineUUID("Miner")}, col.send)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	surfaceY := float64(world.FlatSurfaceY + 1)
	srv.SetPlayerTransform(player, 8.5, surfaceY, 10.5, 0, 0, true)
	if err := srv.SetPlayerGameMode(player, GameModeSurvival); err != nil {
		t.Fatalf("SetPlayerGameMode: %v", err)
	}
	col.reset()

	creeperID := mustSpawnTestMob(t, srv, MobTypeCreeper, 8.5, surfaceY, 8.5)
	rng := rand.New(rand.NewSource(99))

	// Within 2 blocks (< 3.0 ignite distance), ticking once ignites the creeper fuse.
	srv.TickEntities(rng)
	fuse, swellDir := srv.CreeperFuseState(creeperID)
	if swellDir != 1 || fuse != 1 {
		t.Fatalf("expected creeper to start swelling (dir=1, fuse=1), got dir=%d fuse=%d", swellDir, fuse)
	}

	// Advance 10 more ticks while close.
	for i := 0; i < 10; i++ {
		srv.TickEntities(rng)
	}
	fuse, _ = srv.CreeperFuseState(creeperID)
	if fuse != 11 {
		t.Fatalf("creeper fuse = %d, want 11", fuse)
	}

	// Move player 12 blocks away (> 7.0 continue distance): creeper should defuse.
	srv.SetPlayerTransform(player, 8.5, surfaceY, 22.5, 0, 0, true)
	srv.TickEntities(rng)
	fuse, swellDir = srv.CreeperFuseState(creeperID)
	if swellDir != -1 || fuse != 10 {
		t.Fatalf("expected creeper to defuse (dir=-1, fuse=10), got dir=%d fuse=%d", swellDir, fuse)
	}

	// Move player right next to the creeper and tick until explosion (30 fuse ticks).
	cr := srv.Entities().Get(creeperID)
	srv.SetPlayerTransform(player, cr.X+1.0, surfaceY, cr.Z, 0, 0, true)
	exploded := false
	for tick := 0; tick < CreeperFuseDurationTicks+5; tick++ {
		srv.TickEntities(rng)
		if srv.Entities().Get(creeperID) == nil {
			exploded = true
			break
		}
	}
	if !exploded {
		t.Fatal("expected creeper to explode and despawn after reaching CreeperFuseDurationTicks")
	}
	if player.Health() >= world.DefaultPlayerHealth {
		t.Fatalf("expected player to take explosion damage, health = %v", player.Health())
	}
}

func TestSpiderNeutralInDaylightAndHostileAtNightOrWhenProvoked(t *testing.T) {
	srv := newSurvivalTestServer(t)
	col := &survivalPacketCollector{}
	player, err := srv.RegisterPlayer(Profile{Name: "Explorer", UUID: OfflineUUID("Explorer")}, col.send)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	surfaceY := float64(world.FlatSurfaceY + 1)
	srv.SetPlayerTransform(player, 8.5, surfaceY, 10.0, 0, 0, true)
	if err := srv.SetPlayerGameMode(player, GameModeSurvival); err != nil {
		t.Fatalf("SetPlayerGameMode: %v", err)
	}

	// Noon in clear weather -> sky light 15 (> SpiderHostileMaxLight 11).
	srv.SetWorldTime(6000, 6000)
	spiderID := mustSpawnTestMob(t, srv, MobTypeSpider, 8.5, surfaceY, 8.5)
	rng := rand.New(rand.NewSource(123))

	for tick := 0; tick < 20; tick++ {
		srv.TickEntities(rng)
	}
	if player.Health() != world.DefaultPlayerHealth {
		t.Fatalf("spider attacked player in bright daylight: health = %v, want %v", player.Health(), world.DefaultPlayerHealth)
	}

	// Provoke the spider by attacking it in daylight: it should become hostile and bite back.
	if dealt, _ := srv.AttackEntity(player, spiderID); dealt <= 0 {
		t.Fatal("AttackEntity on spider dealt 0 damage")
	}
	for tick := 0; tick < 30; tick++ {
		srv.TickEntities(rng)
		if player.Health() < world.DefaultPlayerHealth {
			break
		}
	}
	if player.Health() >= world.DefaultPlayerHealth {
		t.Fatalf("expected provoked spider to counter-attack in daylight, health = %v", player.Health())
	}

	// Now test unprovoked hostility at night (time 15000 -> SkyDarkening 11 -> effective light 4 <= 11).
	srv.SetPlayerHealth(player, world.DefaultPlayerHealth, world.DefaultPlayerFoodLevel, world.DefaultPlayerSaturation)
	srv.Entities().Remove(spiderID)
	srv.SetWorldTime(15000, 15000)

	nightSpiderID := mustSpawnTestMob(t, srv, MobTypeSpider, 8.5, surfaceY, 7.0)
	leaped := false
	for tick := 0; tick < 40; tick++ {
		srv.TickEntities(rng)
		sp := srv.Entities().Get(nightSpiderID)
		if sp != nil && sp.VelocityY > 0 {
			leaped = true
		}
		if player.Health() < world.DefaultPlayerHealth {
			break
		}
	}
	if !leaped {
		t.Fatal("expected spider at 3 blocks distance to leap toward player at night")
	}
	if player.Health() >= world.DefaultPlayerHealth {
		t.Fatalf("expected spider to attack player at night, health = %v", player.Health())
	}
}

func TestPassiveAnimalsWanderTemptPanicAndChickenSlowFall(t *testing.T) {
	srv := newSurvivalTestServer(t)
	col := &survivalPacketCollector{}
	player, err := srv.RegisterPlayer(Profile{Name: "Farmer", UUID: OfflineUUID("Farmer")}, col.send)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	surfaceY := float64(world.FlatSurfaceY + 1)
	srv.SetPlayerTransform(player, 8.5, surfaceY, 13.5, 0, 0, true)
	if err := srv.SetPlayerGameMode(player, GameModeSurvival); err != nil {
		t.Fatalf("SetPlayerGameMode: %v", err)
	}

	// Equip wheat in the player's main hand to tempt a cow.
	wheatStack := world.NewNamedItemStack("minecraft:wheat", 4)
	if wheatStack.IsEmpty() {
		t.Fatal("minecraft:wheat missing from item registry")
	}
	player.SetInventorySlot(world.HotbarContainerOffset, wheatStack)

	cowID := mustSpawnTestMob(t, srv, MobTypeCow, 8.5, surfaceY, 8.5)
	rng := rand.New(rand.NewSource(55))
	initialDist := math.Hypot(8.5-8.5, 13.5-8.5)

	for tick := 0; tick < 30; tick++ {
		srv.TickEntities(rng)
	}
	cow := srv.Entities().Get(cowID)
	newDist := math.Hypot(8.5-cow.X, 13.5-cow.Z)
	if newDist >= initialDist {
		t.Fatalf("expected cow tempted by wheat to move closer to player: dist = %v, initial = %v", newDist, initialDist)
	}

	// Clear held wheat and damage the cow: it should enter panic mode.
	player.SetInventorySlot(world.HotbarContainerOffset, world.ItemStack{})
	if dealt, _ := srv.AttackEntity(player, cowID); dealt <= 0 {
		t.Fatal("AttackEntity on cow dealt 0 damage")
	}
	srv.TickEntities(rng)
	mem := srv.entityTracker().mobStates[cowID]
	if mem == nil || mem.panicTicks <= 0 {
		t.Fatalf("expected cow to enter panic state after taking damage, got %+v", mem)
	}

	// Spawn a chicken high in the air and verify slow falling (VelocityY >= ChickenMaxFallVelocity)
	// plus egg laying when the egg timer reaches 1.
	chickenID := mustSpawnTestMob(t, srv, MobTypeChicken, 12.5, surfaceY+10, 12.5)
	for tick := 0; tick < 20; tick++ {
		srv.TickEntities(rng)
	}
	ch := srv.Entities().Get(chickenID)
	if ch.VelocityY < ChickenMaxFallVelocity {
		t.Fatalf("chicken fall VelocityY = %d, want >= %d (slow falling)", ch.VelocityY, ChickenMaxFallVelocity)
	}

	// Place chicken on the ground and set eggTimer to 1 so the next tick lays an egg item.
	srv.Entities().Update(chickenID, func(e *world.Entity) {
		e.Y = surfaceY
		e.OnGround = true
		e.VelocityY = 0
	})
	srv.SetChickenEggTimer(chickenID, 1)
	beforeCount := srv.Entities().Count()
	srv.TickEntities(rng)
	if srv.Entities().Count() <= beforeCount {
		t.Fatal("expected chicken to spawn an egg item entity when eggTimer reached 0")
	}
}

func TestAnimalInteractionsMilkingShearingAndBreeding(t *testing.T) {
	srv := newSurvivalTestServer(t)
	col := &survivalPacketCollector{}
	player, err := srv.RegisterPlayer(Profile{Name: "Rancher", UUID: OfflineUUID("Rancher")}, col.send)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	surfaceY := float64(world.FlatSurfaceY + 1)
	srv.SetPlayerTransform(player, 8.5, surfaceY, 8.5, 0, 0, true)
	if err := srv.SetPlayerGameMode(player, GameModeSurvival); err != nil {
		t.Fatalf("SetPlayerGameMode: %v", err)
	}

	// 1. Milk a cow with an empty bucket.
	cow1 := mustSpawnTestMob(t, srv, MobTypeCow, 9.5, surfaceY, 8.5)
	player.SetInventorySlot(world.HotbarContainerOffset, world.NewNamedItemStack("minecraft:bucket", 1))
	handled, _ := srv.InteractWithAnimal(player, cow1)
	if !handled {
		t.Fatal("InteractWithAnimal(cow, bucket) returned false")
	}
	held := player.InventorySlot(world.HotbarContainerOffset)
	if held.ResolvedID() != "minecraft:milk_bucket" || held.Count != 1 {
		t.Fatalf("expected milk_bucket in hand after milking cow, got %+v", held)
	}

	// 2. Shear a sheep with shears, verify wool drop, then reject re-shearing while sheared.
	sheepID := mustSpawnTestMob(t, srv, MobTypeSheep, 10.5, surfaceY, 8.5)
	player.SetInventorySlot(world.HotbarContainerOffset, world.NewNamedItemStack("minecraft:shears", 1))
	beforeEntities := srv.Entities().Count()
	if ok, _ := srv.InteractWithAnimal(player, sheepID); !ok {
		t.Fatal("InteractWithAnimal(sheep, shears) returned false")
	}
	if srv.Entities().Count() <= beforeEntities {
		t.Fatal("expected shearing sheep to drop white_wool item entity")
	}
	if ok, _ := srv.InteractWithAnimal(player, sheepID); ok {
		t.Fatal("expected already-sheared sheep to reject a second shearing before regrowing wool")
	}

	sheepMem := srv.entityTracker().mobStates[sheepID]
	if sheepMem == nil || !sheepMem.sheared {
		t.Fatalf("expected sheep memory to be marked sheared, got %+v", sheepMem)
	}

	// 3. Breed two cows with wheat and verify baby cow spawns when both are in love.
	cow2 := mustSpawnTestMob(t, srv, MobTypeCow, 10.0, surfaceY, 8.5)
	player.SetInventorySlot(world.HotbarContainerOffset, world.NewNamedItemStack("minecraft:wheat", 4))
	countBeforeBreed := srv.Entities().Count()
	if ok, _ := srv.InteractWithAnimal(player, cow1); !ok {
		t.Fatal("feeding cow1 wheat returned false")
	}
	ok, offspringID := srv.InteractWithAnimal(player, cow2)
	if !ok || offspringID <= 0 {
		t.Fatalf("feeding cow2 wheat returned (ok=%v, offspringID=%d), want (true, >0)", ok, offspringID)
	}
	if got := player.InventorySlot(world.HotbarContainerOffset).Count; got != 2 {
		t.Fatalf("wheat count after feeding 2 cows = %d, want 2", got)
	}
	if srv.Entities().Count() != countBeforeBreed+1 {
		t.Fatalf("entities count after breeding = %d, want %d (baby cow spawned)", srv.Entities().Count(), countBeforeBreed+1)
	}
}
