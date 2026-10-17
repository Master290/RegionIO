package server

import (
	"sync"
	"testing"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

type survivalTestPacket struct {
	id   int32
	body []byte
}

type survivalPacketCollector struct {
	mu   sync.Mutex
	pkts []survivalTestPacket
}

func (c *survivalPacketCollector) send(id int32, body []byte) error {
	cp := append([]byte(nil), body...)
	c.mu.Lock()
	c.pkts = append(c.pkts, survivalTestPacket{id: id, body: cp})
	c.mu.Unlock()
	return nil
}

func (c *survivalPacketCollector) count(id int32) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, p := range c.pkts {
		if p.id == id {
			n++
		}
	}
	return n
}

func (c *survivalPacketCollector) last(id int32) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := len(c.pkts) - 1; i >= 0; i-- {
		if c.pkts[i].id == id {
			return c.pkts[i].body, true
		}
	}
	return nil, false
}

func (c *survivalPacketCollector) reset() {
	c.mu.Lock()
	c.pkts = nil
	c.mu.Unlock()
}

func newSurvivalTestServer(t *testing.T) *Server {
	t.Helper()
	cfg := DefaultConfig()
	cfg.WorldDir = ""
	srv, err := NewWithCache(cfg, world.NewCache(-1, world.GenerateFlat))
	if err != nil {
		t.Fatalf("NewWithCache: %v", err)
	}
	return srv
}

func TestSurvivalBlockBreakSpawnsItemEntityAndCreativeDoesNot(t *testing.T) {
	srv := newSurvivalTestServer(t)
	col := &survivalPacketCollector{}
	session, err := srv.RegisterPlayer(Profile{Name: "Miner", UUID: OfflineUUID("Miner")}, col.send)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	srv.SetPlayerTransform(session, 8.5, float64(world.FlatSurfaceY+1), 8.5, 0, 0, true)
	if err := srv.SetPlayerGameMode(session, GameModeSurvival); err != nil {
		t.Fatalf("SetPlayerGameMode: %v", err)
	}
	col.reset()

	bx, by, bz := 8, world.FlatSurfaceY, 8
	if got := srv.Chunks().GetBlock(bx, by, bz); got != world.StateGrass {
		t.Fatalf("initial block at (%d,%d,%d) = %d, want StateGrassBlock (%d)", bx, by, bz, got, world.StateGrass)
	}

	// In survival, START_DESTROY_BLOCK (status 0) on a non-instant block does not break it yet.
	if broken, dropID := srv.BreakBlock(session, bx, by, bz, PlayerActionStartDestroyBlock); broken || dropID != 0 {
		t.Fatalf("BreakBlock(start) = (%v, %d), want (false, 0)", broken, dropID)
	}
	if got := srv.Chunks().GetBlock(bx, by, bz); got != world.StateGrass {
		t.Fatalf("block after START_DESTROY_BLOCK = %d, want still StateGrassBlock", got)
	}

	// STOP_DESTROY_BLOCK (status 2) breaks the block and spawns an item entity.
	broken, dropID := srv.BreakBlock(session, bx, by, bz, PlayerActionStopDestroyBlock)
	if !broken || dropID == 0 {
		t.Fatalf("BreakBlock(stop) = (%v, %d), want (true, >0)", broken, dropID)
	}
	if got := srv.Chunks().GetBlock(bx, by, bz); got != world.StateAir {
		t.Fatalf("block after STOP_DESTROY_BLOCK = %d, want StateAir", got)
	}

	dropEnt := srv.Entities().Get(dropID)
	if dropEnt == nil || !dropEnt.IsItem() {
		t.Fatalf("spawned entity = %+v, want item entity", dropEnt)
	}
	if dropEnt.Item.ResolvedID() != "minecraft:dirt" || dropEnt.Item.Count != 1 {
		t.Fatalf("dropped item = %+v, want 1x minecraft:dirt", dropEnt.Item)
	}
	if col.count(protocol.PlayBlockUpdate) == 0 {
		t.Fatal("expected PlayBlockUpdate to be broadcast")
	}
	if col.count(protocol.PlayAddEntity) == 0 || col.count(protocol.PlaySetEntityData) == 0 {
		t.Fatalf("expected PlayAddEntity (%d) and PlaySetEntityData (%d) for item drop",
			col.count(protocol.PlayAddEntity), col.count(protocol.PlaySetEntityData))
	}

	// Bedrock cannot be broken in survival mode.
	if broken, _ := srv.BreakBlock(session, 8, 0, 8, PlayerActionStopDestroyBlock); broken {
		t.Fatal("expected bedrock to be unbreakable in survival mode")
	}

	// In creative mode, START_DESTROY_BLOCK breaks immediately without spawning an item entity.
	if err := srv.SetPlayerGameMode(session, GameModeCreative); err != nil {
		t.Fatalf("SetPlayerGameMode(Creative): %v", err)
	}
	prevEntityCount := srv.Entities().Count()
	broken, creativeDropID := srv.BreakBlock(session, 9, world.FlatSurfaceY, 8, PlayerActionStartDestroyBlock)
	if !broken || creativeDropID != 0 {
		t.Fatalf("creative BreakBlock(start) = (%v, %d), want (true, 0)", broken, creativeDropID)
	}
	if srv.Entities().Count() != prevEntityCount {
		t.Fatalf("entity count after creative break = %d, want %d", srv.Entities().Count(), prevEntityCount)
	}
}

func TestSurvivalItemPickupAddsToInventoryAndRemovesEntity(t *testing.T) {
	srv := newSurvivalTestServer(t)
	col := &survivalPacketCollector{}
	session, err := srv.RegisterPlayer(Profile{Name: "Collector", UUID: OfflineUUID("Collector")}, col.send)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	if err := srv.SetPlayerGameMode(session, GameModeSurvival); err != nil {
		t.Fatalf("SetPlayerGameMode: %v", err)
	}
	srv.SetPlayerTransform(session, 10.5, 65.0, 10.5, 0, 0, true)

	cobble := world.NewNamedItemStack("minecraft:cobblestone", 5)
	id1, _ := srv.SpawnItemEntity(11.0, 65.0, 10.5, cobble)
	if id1 == 0 {
		t.Fatal("failed to spawn first cobblestone item entity")
	}
	col.reset()

	picked := srv.TryPickupItems(session)
	if picked != 5 {
		t.Fatalf("TryPickupItems = %d, want 5", picked)
	}
	if srv.Entities().Get(id1) != nil {
		t.Fatal("expected picked-up item entity to be removed from EntityManager")
	}
	slot0 := session.InventorySlot(world.HotbarContainerOffset)
	if slot0.ResolvedID() != "minecraft:cobblestone" || slot0.Count != 5 {
		t.Fatalf("hotbar slot 0 = %+v, want 5x minecraft:cobblestone", slot0)
	}
	if col.count(protocol.PlayTakeItemEntity) != 1 {
		t.Fatalf("PlayTakeItemEntity count = %d, want 1", col.count(protocol.PlayTakeItemEntity))
	}
	if col.count(protocol.PlayContainerSetSlot) != 1 {
		t.Fatalf("PlayContainerSetSlot count = %d, want 1", col.count(protocol.PlayContainerSetSlot))
	}
	if col.count(protocol.PlayRemoveEntities) != 1 {
		t.Fatalf("PlayRemoveEntities count = %d, want 1", col.count(protocol.PlayRemoveEntities))
	}

	// Picking up another stack of cobblestone merges into the existing hotbar stack.
	id2, _ := srv.SpawnItemEntity(10.2, 65.0, 10.2, world.NewNamedItemStack("minecraft:cobblestone", 7))
	if got := srv.TryPickupItems(session); got != 7 {
		t.Fatalf("second TryPickupItems = %d, want 7", got)
	}
	if srv.Entities().Get(id2) != nil {
		t.Fatal("expected second item entity to be removed")
	}
	slot0 = session.InventorySlot(world.HotbarContainerOffset)
	if slot0.Count != 12 {
		t.Fatalf("merged hotbar slot 0 count = %d, want 12", slot0.Count)
	}
}

func TestSurvivalDamageFallDamageAndDeath(t *testing.T) {
	srv := newSurvivalTestServer(t)
	col := &survivalPacketCollector{}
	session, err := srv.RegisterPlayer(Profile{Name: "Jumper", UUID: OfflineUUID("Jumper")}, col.send)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	srv.SetPlayerTransform(session, 8.5, 80.0, 8.5, 0, 0, true)

	// Creative players do not take fall damage.
	if dmg := srv.HandlePlayerFall(session, 80.0, 65.0, false, true); dmg != 0 {
		t.Fatalf("creative fall damage = %v, want 0", dmg)
	}
	if session.Health() != world.DefaultPlayerHealth {
		t.Fatalf("creative health = %v, want %v", session.Health(), world.DefaultPlayerHealth)
	}

	// Switch to survival mode.
	if err := srv.SetPlayerGameMode(session, GameModeSurvival); err != nil {
		t.Fatalf("SetPlayerGameMode: %v", err)
	}
	col.reset()

	// Falling 3.0 blocks or less causes no damage.
	srv.HandlePlayerFall(session, 80.0, 78.5, true, false)
	if dmg := srv.HandlePlayerFall(session, 78.5, 77.0, false, true); dmg != 0 {
		t.Fatalf("3-block fall damage = %v, want 0", dmg)
	}
	if session.Health() != 20.0 {
		t.Fatalf("health after 3-block fall = %v, want 20.0", session.Health())
	}

	// Falling 7.4 blocks deals ceil(7.4 - 3.0) = 5.0 damage.
	srv.HandlePlayerFall(session, 77.0, 73.0, true, false)
	dmg := srv.HandlePlayerFall(session, 73.0, 69.6, false, true)
	if dmg != 5.0 {
		t.Fatalf("7.4-block fall damage = %v, want 5.0", dmg)
	}
	if session.Health() != 15.0 {
		t.Fatalf("health after 5 damage = %v, want 15.0", session.Health())
	}
	if session.IsDead() {
		t.Fatal("player should not be dead at 15.0 HP")
	}
	if col.count(protocol.PlayHurtAnimation) == 0 || col.count(protocol.PlaySetHealth) == 0 {
		t.Fatalf("expected PlayHurtAnimation (%d) and PlaySetHealth (%d)",
			col.count(protocol.PlayHurtAnimation), col.count(protocol.PlaySetHealth))
	}

	// Fatal fall (25 blocks -> 22 damage) reduces health to 0 and triggers death packets.
	col.reset()
	fatalDmg := srv.HandlePlayerFall(session, 90.0, 65.0, false, true)
	if fatalDmg != 22.0 {
		t.Fatalf("25-block fall damage = %v, want 22.0", fatalDmg)
	}
	if session.Health() != 0 {
		t.Fatalf("health after fatal fall = %v, want 0", session.Health())
	}
	if !session.IsDead() {
		t.Fatal("expected session.IsDead() = true after fatal damage")
	}
	if col.count(protocol.PlayPlayerCombatKill) != 1 {
		t.Fatalf("PlayPlayerCombatKill count = %d, want 1", col.count(protocol.PlayPlayerCombatKill))
	}
	if col.count(protocol.PlaySystemChat) != 1 {
		t.Fatalf("death broadcast PlaySystemChat count = %d, want 1", col.count(protocol.PlaySystemChat))
	}

	// Dead player cannot break blocks or pick up items.
	if broken, _ := srv.BreakBlock(session, 8, world.FlatSurfaceY, 8, PlayerActionStopDestroyBlock); broken {
		t.Fatal("dead player should not be able to break blocks")
	}
}

func TestSurvivalRespawnRestoresHealthAndTeleportsToSpawnPoint(t *testing.T) {
	srv := newSurvivalTestServer(t)
	col := &survivalPacketCollector{}
	session, err := srv.RegisterPlayer(Profile{Name: "Phoenix", UUID: OfflineUUID("Phoenix")}, col.send)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	if err := srv.SetPlayerGameMode(session, GameModeSurvival); err != nil {
		t.Fatalf("SetPlayerGameMode: %v", err)
	}
	srv.SetPlayerSpawnPoint(session, 40, 72, -30, 90.0, 15.0, true)
	srv.SetPlayerTransform(session, 100.5, 65.0, 100.5, 0, 0, true)

	if died := srv.DamagePlayer(session, 25.0, "generic"); !died || !session.IsDead() {
		t.Fatalf("expected player to die from 25 damage (died=%v, isDead=%v)", died, session.IsDead())
	}
	col.reset()

	if err := srv.RespawnPlayer(session); err != nil {
		t.Fatalf("RespawnPlayer: %v", err)
	}
	if session.IsDead() {
		t.Fatal("expected session.IsDead() = false after RespawnPlayer")
	}
	snap := session.Snapshot()
	if snap.Health != world.DefaultPlayerHealth || snap.FoodLevel != world.DefaultPlayerFoodLevel {
		t.Fatalf("respawned health/food = (%v, %d), want (%v, %d)",
			snap.Health, snap.FoodLevel, world.DefaultPlayerHealth, world.DefaultPlayerFoodLevel)
	}
	if snap.X != 40.5 || snap.Y != 72.0 || snap.Z != -29.5 || snap.Yaw != 90.0 || snap.Pitch != 15.0 {
		t.Fatalf("respawned position = (%v, %v, %v, %v, %v), want (40.5, 72.0, -29.5, 90.0, 15.0)",
			snap.X, snap.Y, snap.Z, snap.Yaw, snap.Pitch)
	}
	if col.count(protocol.PlayRespawn) != 1 {
		t.Fatalf("PlayRespawn count = %d, want 1", col.count(protocol.PlayRespawn))
	}
	if col.count(protocol.PlayPlayerPosition) != 1 {
		t.Fatalf("PlayPlayerPosition count = %d, want 1", col.count(protocol.PlayPlayerPosition))
	}
	if col.count(protocol.PlaySetHealth) != 1 {
		t.Fatalf("PlaySetHealth count = %d, want 1", col.count(protocol.PlaySetHealth))
	}
}

func TestConsumeHeldItemInSurvival(t *testing.T) {
	srv := newSurvivalTestServer(t)
	col := &survivalPacketCollector{}
	session, err := srv.RegisterPlayer(Profile{Name: "Builder", UUID: OfflineUUID("Builder")}, col.send)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	if err := srv.SetPlayerGameMode(session, GameModeSurvival); err != nil {
		t.Fatalf("SetPlayerGameMode: %v", err)
	}
	session.SetSelectedSlot(2)
	session.SetInventorySlot(world.HotbarContainerOffset+2, world.NewNamedItemStack("minecraft:dirt", 2))
	col.reset()

	slot, updated, ok := srv.ConsumeHeldItem(session, 1)
	if !ok || slot != world.HotbarContainerOffset+2 || updated.Count != 1 {
		t.Fatalf("first ConsumeHeldItem = (%d, %+v, %v), want (%d, count=1, true)",
			slot, updated, ok, world.HotbarContainerOffset+2)
	}
	if col.count(protocol.PlayContainerSetSlot) != 1 {
		t.Fatalf("PlayContainerSetSlot count = %d, want 1", col.count(protocol.PlayContainerSetSlot))
	}

	_, updated, ok = srv.ConsumeHeldItem(session, 1)
	if !ok || !updated.IsEmpty() {
		t.Fatalf("second ConsumeHeldItem = (%+v, %v), want (empty, true)", updated, ok)
	}
	if _, _, ok := srv.ConsumeHeldItem(session, 1); ok {
		t.Fatal("expected ConsumeHeldItem on empty slot to return false")
	}
}
