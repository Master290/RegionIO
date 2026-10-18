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

func TestIntegrationHooksHungerFurnaceAndPressurePlate(t *testing.T) {
	srv := newSurvivalTestServer(t)
	col := &survivalPacketCollector{}
	session, err := srv.RegisterPlayer(Profile{Name: "HookTester", UUID: OfflineUUID("HookTester")}, col.send)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	if err := srv.SetPlayerGameMode(session, GameModeSurvival); err != nil {
		t.Fatalf("SetPlayerGameMode: %v", err)
	}

	// 1. Breaking a block in survival adds mining exhaustion.
	beforeEx := session.FoodExhaustion()
	if broken, _ := srv.BreakBlock(session, 2, world.FlatSurfaceY, 2, PlayerActionStopDestroyBlock); !broken {
		t.Fatal("expected block at (2, FlatSurfaceY, 2) to break in survival")
	}
	if got := session.FoodExhaustion(); got <= beforeEx {
		t.Fatalf("FoodExhaustion after BreakBlock = %v, want > %v", got, beforeEx)
	}

	// 2. PlayerData round-trips FoodExhaustion and FoodTickTimer.
	session.SetFoodExhaustion(2.75)
	session.SetFoodTickTimer(42)
	comp := world.PlayerDataToCompound(session.PlayerData())
	decoded, err := world.PlayerDataFromCompound(comp)
	if err != nil {
		t.Fatalf("PlayerDataFromCompound: %v", err)
	}
	if decoded.FoodExhaustion != 2.75 || decoded.FoodTickTimer != 42 {
		t.Fatalf("decoded exhaustion/timer = (%v, %d), want (2.75, 42)", decoded.FoodExhaustion, decoded.FoodTickTimer)
	}

	// 3. RespawnPlayer resets exhaustion, tickTimer, and cancels active eating.
	session.SetInventorySlot(world.HotbarContainerOffset, world.NewNamedItemStack("minecraft:Golden_apple", 2))
	srv.StartEating(session, HandMain)
	if !session.IsEating() {
		t.Fatal("expected session.IsEating() = true after StartEating")
	}
	if err := srv.RespawnPlayer(session); err != nil {
		t.Fatalf("RespawnPlayer: %v", err)
	}
	if session.FoodExhaustion() != 0 || session.FoodTickTimer() != 0 || session.IsEating() {
		t.Fatalf("after RespawnPlayer: exhaustion=%v, timer=%d, eating=%v; want 0, 0, false",
			session.FoodExhaustion(), session.FoodTickTimer(), session.IsEating())
	}

	// 4. Pressure plate activation via StepPressurePlateAt and automatic release via SetPressurePlateEntityCounter.
	if itemID, ok := world.ItemNameToID("minecraft:stone_pressure_plate"); ok {
		if plateState, ok := world.ItemToBlock(itemID); ok {
			px, py, pz := 6, world.FlatSurfaceY+1, 6
			srv.Chunks().SetBlock(px, py, pz, plateState)
			srv.SetPlayerTransform(session, float64(px)+0.5, float64(py), float64(pz)+0.5, 0, 0, true)
			srv.StepPressurePlateAt(world.DimensionOverworld, px, py, pz)
			if srv.Chunks().RedstonePowerAt(px, py, pz) != world.MaxRedstonePower {
				t.Fatalf("expected pressure plate power = 15 when player stands on it, got %d", srv.Chunks().RedstonePowerAt(px, py, pz))
			}
			// Move player off the plate and advance 20 ticks; counter returns 0 so plate releases.
			srv.SetPlayerTransform(session, float64(px)+5.5, float64(py), float64(pz)+5.5, 0, 0, true)
			for i := 0; i < world.PressurePlateTickDelay; i++ {
				srv.Chunks().TickBlockUpdates()
			}
			if srv.Chunks().RedstonePowerAt(px, py, pz) != 0 {
				t.Fatalf("expected pressure plate power = 0 after player steps off and 20 ticks elapse, got %d", srv.Chunks().RedstonePowerAt(px, py, pz))
			}
		}
	}
}

func TestEnchantingCombatDurabilityAndXPPersistenceHooks(t *testing.T) {
	srv := newSurvivalTestServer(t)
	col := &survivalPacketCollector{}
	session, err := srv.RegisterPlayer(Profile{Name: "EnchMiner", UUID: OfflineUUID("EnchMiner")}, col.send)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	if err := srv.SetPlayerGameMode(session, GameModeSurvival); err != nil {
		t.Fatalf("SetPlayerGameMode: %v", err)
	}
	feetY := float64(world.FlatSurfaceY + 1)
	srv.SetPlayerTransform(session, 8.5, feetY, 8.5, 0, 0, true)

	// 1. Breaking stone with a Silk Touch pickaxe drops minecraft:stone (not cobblestone) and wears 1 durability.
	heldSlot := world.HotbarContainerOffset
	session.SetSelectedSlot(0)
	session.SetInventorySlot(heldSlot, world.NewNamedItemStack("minecraft:diamond_pickaxe", 1))
	var pickMeta ItemMeta
	pickMeta.SetEnchantment("minecraft:silk_touch", 1)
	SetSlotItemMeta(session, heldSlot, pickMeta)

	srv.Chunks().SetBlock(8, world.FlatSurfaceY, 8, world.StateStone)
	broken, dropID := srv.BreakBlock(session, 8, world.FlatSurfaceY, 8, PlayerActionStopDestroyBlock)
	if !broken || dropID == 0 {
		t.Fatalf("BreakBlock with Silk Touch pickaxe = (%v, %d), want (true, >0)", broken, dropID)
	}
	dropEnt := srv.Entities().Get(dropID)
	if dropEnt == nil || dropEnt.Item.ResolvedID() != "minecraft:stone" {
		t.Fatalf("Silk Touch drop = %+v, want minecraft:stone", dropEnt)
	}
	if gotDmg := GetSlotItemMeta(session, heldSlot).Damage; gotDmg != 1 {
		t.Fatalf("pickaxe durability damage after BreakBlock = %d, want 1", gotDmg)
	}

	// 2. Equipping Feather Falling IV boots reduces fall damage in HandlePlayerFall / DamagePlayer and wears armor durability.
	session.SetInventorySlot(ArmorSlotFeet, world.NewNamedItemStack("minecraft:diamond_boots", 1))
	var bootsMeta ItemMeta
	bootsMeta.SetEnchantment("minecraft:feather_falling", 4) // EPF = 12 -> 48% reduction
	SetSlotItemMeta(session, ArmorSlotFeet, bootsMeta)

	srv.HandlePlayerFall(session, 75.0, 68.0, true, false)
	fallDmg := srv.HandlePlayerFall(session, 68.0, 62.0, false, true) // 13 blocks -> base 10.0, reduced by 48% -> 5.2
	if fallDmg >= 6.0 || fallDmg <= 5.0 {
		t.Fatalf("fall damage with Feather Falling IV = %v, want ~5.2", fallDmg)
	}
	if gotBootsDmg := GetSlotItemMeta(session, ArmorSlotFeet).Damage; gotBootsDmg != 1 {
		t.Fatalf("boots durability damage after fall = %d, want 1", gotBootsDmg)
	}

	// 3. Weapon enchantment attack bonus (Sharpness V = +3.0) and weapon durability wear in AttackEntityWithEnchantments.
	session.SetInventorySlot(heldSlot, world.NewNamedItemStack("minecraft:diamond_sword", 1)) // 7.0 base
	var swordMeta ItemMeta
	swordMeta.SetEnchantment("minecraft:sharpness", 5) // +3.0 -> 10.0 total, one-shots a 10 HP pig
	SetSlotItemMeta(session, heldSlot, swordMeta)
	session.SetAttackStrengthTicks(30)

	pigID := srv.Entities().Add(&world.Entity{
		TypeName: MobTypePig,
		X:        8.5,
		Y:        feetY,
		Z:        10.0,
		OnGround: true,
	})
	dealt, killed := srv.AttackEntityWithEnchantments(session, pigID)
	if !killed || dealt < 9.9 {
		t.Fatalf("AttackEntityWithEnchantments with Sharpness V diamond sword: dealt=%v killed=%v, want >=10.0 & true", dealt, killed)
	}
	if gotSwordDmg := GetSlotItemMeta(session, heldSlot).Damage; gotSwordDmg != 1 {
		t.Fatalf("sword durability damage after attack = %d, want 1", gotSwordDmg)
	}

	// 4. XP persistence round-trip in PlayerData and ApplyPlayerData.
	if err := srv.AddPlayerExperience(session, 200); err != nil {
		t.Fatalf("AddPlayerExperience: %v", err)
	}
	session.SetEnchantmentSeed(777888)
	savedComp := world.PlayerDataToCompound(session.PlayerData())
	restoredData, err := world.PlayerDataFromCompound(savedComp)
	if err != nil {
		t.Fatalf("PlayerDataFromCompound: %v", err)
	}
	if restoredData.XpTotal != session.TotalExperience() || restoredData.XpLevel != session.ExperienceLevel() || restoredData.XpSeed != 777888 {
		t.Fatalf("restored XP = (lvl=%d, tot=%d, seed=%d), want (%d, %d, 777888)",
			restoredData.XpLevel, restoredData.XpTotal, restoredData.XpSeed,
			session.ExperienceLevel(), session.TotalExperience())
	}

	session2, err := srv.RegisterPlayer(Profile{Name: "EnchMiner2", UUID: OfflineUUID("EnchMiner2")}, nil)
	if err != nil {
		t.Fatalf("RegisterPlayer(2): %v", err)
	}
	session2.ApplyPlayerData(restoredData)
	if session2.TotalExperience() != session.TotalExperience() || session2.ExperienceLevel() != session.ExperienceLevel() || session2.EnchantmentSeed() != 777888 {
		t.Fatalf("ApplyPlayerData XP = (lvl=%d, tot=%d, seed=%d), want (%d, %d, 777888)",
			session2.ExperienceLevel(), session2.TotalExperience(), session2.EnchantmentSeed(),
			session.ExperienceLevel(), session.TotalExperience())
	}
}

func TestWiredAdvancementHooksAndPlayerDataPersistence(t *testing.T) {
	srv := newSurvivalTestServer(t)
	session, err := srv.RegisterPlayer(Profile{Name: "AdvTester", UUID: OfflineUUID("AdvTester")}, nil)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	feetY := float64(world.FlatSurfaceY + 1)
	srv.SetPlayerTransform(session, 8.5, feetY, 8.5, 0, 0, true)
	_ = srv.SetPlayerGameMode(session, GameModeSurvival)

	// 1. Item pickup in TryPickupItems triggers mine_stone & mine_diamond.
	cobbleStack := world.NewNamedItemStack("minecraft:cobblestone", 1)
	_, _ = srv.SpawnItemEntity(8.5, feetY, 8.5, cobbleStack)
	if picked := srv.TryPickupItems(session); picked != 1 {
		t.Fatalf("TryPickupItems = %d, want 1", picked)
	}
	if !session.HasCompletedAdvancement("minecraft:story/mine_stone") {
		t.Fatal("expected minecraft:story/mine_stone after picking up cobblestone")
	}

	// 2. Crafting in HandleContainerClick triggers story/root (crafting_table).
	planks := world.NewNamedItemStack("minecraft:oak_planks", 1)
	for s := 1; s <= 4; s++ {
		session.SetInventorySlot(s, planks)
	}
	if err := srv.HandleContainerClick(session, 0, 0, 0, ContainerInputPickup); err != nil {
		t.Fatalf("HandleContainerClick(craft crafting_table): %v", err)
	}
	if !session.HasCompletedAdvancement("minecraft:story/root") {
		t.Fatal("expected minecraft:story/root after crafting crafting_table")
	}

	// 3. Furnace output take triggers smelt_iron.
	srv.SetFurnaceSlot(10, world.FlatSurfaceY, 10, world.FurnaceSlotOutput, world.NewNamedItemStack("minecraft:iron_ingot", 1))
	cid, err := srv.OpenFurnace(session, 10, world.FlatSurfaceY, 10)
	if err != nil {
		t.Fatalf("OpenFurnace: %v", err)
	}
	if err := srv.HandleContainerClick(session, cid, world.FurnaceSlotOutput, 0, ContainerInputQuickMove); err != nil {
		t.Fatalf("HandleContainerClick(furnace output): %v", err)
	}
	if !session.HasCompletedAdvancement("minecraft:story/smelt_iron") {
		t.Fatal("expected minecraft:story/smelt_iron after taking iron_ingot from furnace")
	}
	_ = srv.CloseContainer(session, cid, false)

	// 4. Enchanting table button click triggers enchant_item.
	_ = srv.AddPlayerExperienceLevels(session, 30)
	enchCID, err := srv.OpenEnchantingTable(session, 12, world.FlatSurfaceY, 12)
	if err != nil {
		t.Fatalf("OpenEnchantingTable: %v", err)
	}
	session.SetInventorySlot(9, world.NewNamedItemStack("minecraft:iron_sword", 1))
	session.SetInventorySlot(10, world.NewNamedItemStack("minecraft:lapis_lazuli", 10))
	_ = srv.HandleContainerClick(session, enchCID, 2, 0, ContainerInputQuickMove)
	_ = srv.HandleContainerClick(session, enchCID, 3, 0, ContainerInputQuickMove)
	if err := srv.HandleContainerButtonClick(session, enchCID, 0); err != nil {
		t.Fatalf("HandleContainerButtonClick: %v", err)
	}
	if !session.HasCompletedAdvancement("minecraft:story/enchant_item") {
		t.Fatal("expected minecraft:story/enchant_item after enchanting item")
	}
	_ = srv.CloseContainer(session, enchCID, false)

	// 5. Dimension transfer triggers enter_the_nether and enter_the_end.
	if err := srv.ChangePlayerDimension(session, world.DimensionNether, 0.5, 65, 0.5, 0, 0); err != nil {
		t.Fatalf("ChangePlayerDimension(nether): %v", err)
	}
	if !session.HasCompletedAdvancement("minecraft:story/enter_the_nether") || !session.HasCompletedAdvancement("minecraft:nether/root") {
		t.Fatal("expected enter_the_nether and nether/root after dimension transfer to Nether")
	}
	if err := srv.ChangePlayerDimension(session, world.DimensionEnd, 100.5, 49, 0.5, 0, 0); err != nil {
		t.Fatalf("ChangePlayerDimension(end): %v", err)
	}
	if !session.HasCompletedAdvancement("minecraft:story/enter_the_end") {
		t.Fatal("expected enter_the_end after dimension transfer to End")
	}

	// 6. PlayerData NBT round-trip preserves advancements.
	comp := world.PlayerDataToCompound(session.PlayerData())
	decoded, err := world.PlayerDataFromCompound(comp)
	if err != nil {
		t.Fatalf("PlayerDataFromCompound: %v", err)
	}
	sessionRestored, err := srv.RegisterPlayer(Profile{Name: "AdvRestored", UUID: OfflineUUID("AdvRestored")}, nil)
	if err != nil {
		t.Fatalf("RegisterPlayer(AdvRestored): %v", err)
	}
	sessionRestored.ApplyPlayerData(decoded)
	for _, advID := range []string{
		"minecraft:story/mine_stone",
		"minecraft:story/root",
		"minecraft:story/smelt_iron",
		"minecraft:story/enchant_item",
		"minecraft:story/enter_the_nether",
		"minecraft:story/enter_the_end",
	} {
		if !sessionRestored.HasCompletedAdvancement(advID) {
			t.Fatalf("restored session missing advancement %s", advID)
		}
	}
}

func TestWiredBrewingPotionAndEffectHooks(t *testing.T) {
	srv := newSurvivalTestServer(t)
	session, err := srv.RegisterPlayer(Profile{Name: "Alchemist", UUID: OfflineUUID("Alchemist")}, nil)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	feetY := float64(world.FlatSurfaceY + 1)
	srv.SetPlayerTransform(session, 8.5, feetY, 8.5, 0, 0, true)
	_ = srv.SetPlayerGameMode(session, GameModeSurvival)

	// 1. InteractBlock opens BrewingStand, HandleContainerClick moves items, BreakBlock drops contents.
	bx, by, bz := 14, world.FlatSurfaceY+1, 14
	srv.Chunks().SetBlock(bx, by, bz, BrewingStandBlockState(false, false, false))
	opened, cid, err := srv.InteractBlock(session, bx, by, bz)
	if err != nil || !opened || cid <= 0 {
		t.Fatalf("InteractBlock(brewing_stand) = (%v, %d, %v), want (true, >0, nil)", opened, cid, err)
	}
	if oc := session.OpenContainer(); oc == nil || oc.MenuType != MenuTypeBrewingStand {
		t.Fatalf("OpenContainer = %+v, want MenuTypeBrewingStand (%d)", oc, MenuTypeBrewingStand)
	}

	// Put a water potion in player inventory slot 9 (menu slot 5) and shift-click into brewing stand slot 0.
	session.SetInventorySlot(9, world.NewNamedItemStack("minecraft:potion", 1))
	SetSlotPotionType(session, 9, PotionWater)
	if err := srv.HandleContainerClick(session, cid, BrewingStandSlotCount, 0, ContainerInputQuickMove); err != nil {
		t.Fatalf("HandleContainerClick(quick-move bottle into stand): %v", err)
	}
	stand, ok := srv.BrewingStandAt(bx, by, bz)
	if !ok || stand.Items[0].IsEmpty() || stand.PotionTypes[0] != PotionWater {
		t.Fatalf("BrewingStandAt after quick-move = %+v (ok=%v), want water bottle in slot 0", stand, ok)
	}

	// Pick up and place back with Pickup mode to verify re-entrant CarriedItem() doesn't deadlock.
	if err := srv.HandleContainerClick(session, cid, 0, 0, ContainerInputPickup); err != nil {
		t.Fatalf("HandleContainerClick(pickup bottle from stand): %v", err)
	}
	if GetCarriedPotionType(session) != PotionWater {
		t.Fatalf("GetCarriedPotionType = %q, want %q", GetCarriedPotionType(session), PotionWater)
	}
	if err := srv.HandleContainerClick(session, cid, 0, 0, ContainerInputPickup); err != nil {
		t.Fatalf("HandleContainerClick(place bottle into stand): %v", err)
	}

	// Break the brewing stand block in survival mode -> drops its contents and closes container.
	broken, _ := srv.BreakBlock(session, bx, by, bz, PlayerActionStopDestroyBlock)
	if !broken {
		t.Fatal("BreakBlock(brewing_stand) returned false")
	}
	if session.OpenContainer() != nil {
		t.Fatalf("expected brewing stand screen to close on BreakBlock, got %+v", session.OpenContainer())
	}
	foundPotionDrop := false
	for _, ent := range srv.Entities().All() {
		if ent.IsItem() && ent.Item.ResolvedID() == "minecraft:potion" {
			foundPotionDrop = true
			break
		}
	}
	if !foundPotionDrop {
		t.Fatal("expected breaking brewing stand to drop its contained potion item entity")
	}

	// 2. Fall damage respects Slow Falling and Jump Boost; RespawnPlayer clears active effects.
	srv.ApplyPlayerEffect(session, NewStatusEffectInstance(EffectSlowFalling, 200, 0))
	if dmg := srv.HandlePlayerFall(session, feetY+10, feetY, false, true); dmg != 0 {
		t.Fatalf("HandlePlayerFall with Slow Falling = %v, want 0", dmg)
	}
	srv.RemovePlayerEffect(session, EffectSlowFalling)
	srv.ApplyPlayerEffect(session, NewStatusEffectInstance(EffectJumpBoost, 200, 1)) // Jump Boost II (-2 fall damage)
	_ = srv.HandlePlayerFall(session, feetY+6, feetY+3, true, false)
	if dmg := srv.HandlePlayerFall(session, feetY+3, feetY, false, true); dmg != 1.0 {
		t.Fatalf("HandlePlayerFall(6 blocks) with Jump Boost II = %v, want 1.0", dmg)
	}

	// 3. PlayerData NBT round-trip preserves active status effects, absorption, and slot potion types.
	srv.ApplyPlayerEffect(session, NewStatusEffectInstance(EffectSpeed, 1200, 1))
	srv.ApplyPlayerEffect(session, NewStatusEffectInstance(EffectAbsorption, 1200, 0))
	hotbarSlot := world.HotbarContainerOffset
	session.SetInventorySlot(hotbarSlot, world.NewNamedItemStack("minecraft:potion", 1))
	SetSlotPotionType(session, hotbarSlot, PotionStrongHealing)

	comp := world.PlayerDataToCompound(session.PlayerData())
	decoded, err := world.PlayerDataFromCompound(comp)
	if err != nil {
		t.Fatalf("PlayerDataFromCompound: %v", err)
	}
	session2, err := srv.RegisterPlayer(Profile{Name: "Alchemist2", UUID: OfflineUUID("Alchemist2")}, nil)
	if err != nil {
		t.Fatalf("RegisterPlayer(Alchemist2): %v", err)
	}
	session2.ApplyPlayerData(decoded)
	if sp, ok := session2.PlayerEffect(EffectSpeed); !ok || sp.Amplifier != 1 || sp.Duration != 1200 {
		t.Fatalf("restored Speed effect = (%+v, %v), want Amplifier=1 Duration=1200", sp, ok)
	}
	if abs := session2.AbsorptionHealth(); abs != 4.0 {
		t.Fatalf("restored AbsorptionHealth = %v, want 4.0", abs)
	}
	if pot := GetSlotPotionType(session2, hotbarSlot); pot != PotionStrongHealing {
		t.Fatalf("restored slot potion type = %q, want %q", pot, PotionStrongHealing)
	}

	// RespawnPlayer clears all active effects on session2.
	if err := srv.RespawnPlayer(session2); err != nil {
		t.Fatalf("RespawnPlayer: %v", err)
	}
	if len(session2.PlayerEffects()) != 0 || session2.AbsorptionHealth() != 0 {
		t.Fatalf("expected RespawnPlayer to clear effects and absorption, got effects=%d abs=%v",
			len(session2.PlayerEffects()), session2.AbsorptionHealth())
	}
}

func TestPlayerMechanicsSweep(t *testing.T) {
	srv := newSurvivalTestServer(t)
	collector := &survivalPacketCollector{}
	session, err := srv.RegisterPlayer(Profile{
		Name: "SweepTester",
		UUID: OfflineUUID("SweepTester"),
	}, collector.send)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	srv.SetPlayerTransform(session, 0.5, 65.0, 0.5, 0, 0, true)
	_ = srv.SetPlayerGameMode(session, GameModeSurvival)

	// 1. DropSelectedItem (single & full stack) and SwapHeldWithOffhand (F key) with ItemMeta preservation.
	hotbar0 := world.HotbarContainerOffset
	session.SetSelectedSlot(0)
	session.SetInventorySlot(hotbar0, world.NewNamedItemStack("minecraft:diamond_sword", 1))
	SetSlotItemMeta(session, hotbar0, ItemMeta{
		Damage:     15,
		RepairCost: 1,
		CustomName: "SharpBlade",
		Enchantments: map[string]int{
			"minecraft:sharpness": 5,
		},
	})
	if !srv.SwapHeldWithOffhand(session) {
		t.Fatal("SwapHeldWithOffhand failed")
	}
	if session.InventorySlot(OffHandContainerSlot).ResolvedID() != "minecraft:diamond_sword" {
		t.Fatalf("offhand after swap = %+v, want diamond_sword", session.InventorySlot(OffHandContainerSlot))
	}
	if meta := GetSlotItemMeta(session, OffHandContainerSlot); meta.CustomName != "SharpBlade" || meta.Damage != 15 {
		t.Fatalf("offhand meta after swap = %+v, want SharpBlade/Damage=15", meta)
	}
	if !srv.SwapHeldWithOffhand(session) {
		t.Fatal("SwapHeldWithOffhand back failed")
	}
	if _, dropped := srv.DropSelectedItem(session, false); dropped.Count != 1 {
		t.Fatalf("DropSelectedItem(single) = %+v, want count 1", dropped)
	}
	if !session.InventorySlot(hotbar0).IsEmpty() || !GetSlotItemMeta(session, hotbar0).IsEmpty() {
		t.Fatalf("expected hotbar0 and its meta to be cleared after drop")
	}

	// Stack drop (Ctrl+Q)
	session.SetInventorySlot(hotbar0, world.NewNamedItemStack("minecraft:cobblestone", 32))
	if _, dropped := srv.DropSelectedItem(session, true); dropped.Count != 32 {
		t.Fatalf("DropSelectedItem(entireStack) = %+v, want count 32", dropped)
	}
	if !session.InventorySlot(hotbar0).IsEmpty() {
		t.Fatalf("expected hotbar0 empty after full stack drop")
	}

	// 2. Curse of Binding prevents removing armor from slots 5..8, and WearableArmorSlotForItem validates armor slots.
	if slot, ok := WearableArmorSlotForItem("minecraft:diamond_helmet"); !ok || slot != ArmorSlotHead {
		t.Fatalf("expected diamond_helmet slot %d, got (%d, %v)", ArmorSlotHead, slot, ok)
	}
	if slot, ok := WearableArmorSlotForItem("minecraft:elytra"); !ok || slot != ArmorSlotChest {
		t.Fatalf("expected elytra slot %d, got (%d, %v)", ArmorSlotChest, slot, ok)
	}
	session.SetInventorySlot(ArmorSlotHead, world.NewNamedItemStack("minecraft:iron_helmet", 1))
	SetSlotItemMeta(session, ArmorSlotHead, ItemMeta{
		Enchantments: map[string]int{
			EnchantBindingCurse: 1,
		},
	})
	if !HasBindingCurseOnSlot(session, ArmorSlotHead) {
		t.Fatal("expected HasBindingCurseOnSlot=true on cursed helmet")
	}
	// Attempting to click or shift-click cursed helmet in survival must not remove it.
	if err := srv.HandleContainerClick(session, 0, ArmorSlotHead, 0, ContainerInputPickup); err != nil {
		t.Fatalf("HandleContainerClick(cursed helmet): %v", err)
	}
	if session.InventorySlot(ArmorSlotHead).ResolvedID() != "minecraft:iron_helmet" || !session.CarriedItem().IsEmpty() {
		t.Fatalf("expected cursed helmet to remain equipped in survival, got slot=%+v carried=%+v",
			session.InventorySlot(ArmorSlotHead), session.CarriedItem())
	}

	// Shift-click diamond_chestplate from hotbar equips it into chest armor slot (6).
	session.SetInventorySlot(hotbar0, world.NewNamedItemStack("minecraft:diamond_chestplate", 1))
	SetSlotItemMeta(session, hotbar0, ItemMeta{Damage: 7, CustomName: "ChestGuard"})
	if err := srv.HandleContainerClick(session, 0, hotbar0, 0, ContainerInputQuickMove); err != nil {
		t.Fatalf("HandleContainerClick(quickMove chestplate): %v", err)
	}
	if session.InventorySlot(ArmorSlotChest).ResolvedID() != "minecraft:diamond_chestplate" {
		t.Fatalf("expected shift-click to equip diamond_chestplate into slot 6, got %+v", session.InventorySlot(ArmorSlotChest))
	}
	if meta := GetSlotItemMeta(session, ArmorSlotChest); meta.CustomName != "ChestGuard" || meta.Damage != 7 {
		t.Fatalf("expected shift-click to preserve armor ItemMeta, got %+v", meta)
	}

	// 3. PickItemFromBlock & PickItemFromEntity (middle-click pick item).
	session.SetInventorySlot(world.HotbarContainerOffset+3, world.NewNamedItemStack("minecraft:oak_planks", 16))
	if !srv.SelectOrPickItem(session, "minecraft:oak_planks") {
		t.Fatal("SelectOrPickItem(oak_planks) failed")
	}
	if session.SelectedSlot() != 3 {
		t.Fatalf("SelectedSlot after SelectOrPickItem = %d, want 3", session.SelectedSlot())
	}
	// Creative pick-block spawns item if not in inventory.
	_ = srv.SetPlayerGameMode(session, GameModeCreative)
	if !srv.SelectOrPickItem(session, "minecraft:lantern") {
		t.Fatal("Creative SelectOrPickItem(lantern) failed")
	}
	if session.HeldItem().ResolvedID() != "minecraft:lantern" {
		t.Fatalf("HeldItem after Creative pick = %+v, want minecraft:lantern", session.HeldItem())
	}

	// 4. PlayerAbilities: toggling flight in Creative and verifying flying suppresses fall damage.
	abW := protocol.NewWriter(1)
	abW.Byte(0x02) // flying = true
	if err := srv.HandlePlayerAbilitiesPacket(session, protocol.NewReader(abW.Bytes())); err != nil {
		t.Fatalf("HandlePlayerAbilitiesPacket: %v", err)
	}
	if !session.IsFlying() {
		t.Fatal("expected session.IsFlying() == true after client toggle in Creative")
	}
	// Verify fall damage is suppressed while flying in Creative/Spectator.
	_ = srv.HandlePlayerFall(session, 80.0, 70.0, true, false)
	if dmg := srv.HandlePlayerFall(session, 70.0, 65.0, false, true); dmg != 0 {
		t.Fatalf("expected 0 fall damage while flying, got %v", dmg)
	}

	// 5. Spectator camera attachment/detachment and TeleportToEntity.
	target, err := srv.RegisterPlayer(Profile{
		Name: "SpectateTarget",
		UUID: OfflineUUID("SpectateTarget"),
	}, nil)
	if err != nil {
		t.Fatalf("RegisterPlayer(SpectateTarget): %v", err)
	}
	srv.SetPlayerTransform(target, 120.5, 70.0, -45.5, 90.0, 15.0, true)

	_ = srv.SetPlayerGameMode(session, GameModeSpectator)
	srv.SetSpectatorCamera(session, target.EntityID)
	if session.CameraEntityID() != target.EntityID {
		t.Fatalf("CameraEntityID = %d, want %d", session.CameraEntityID(), target.EntityID)
	}
	// Pressing Shift (sneak) in Spectator resets camera back to self.
	inW := protocol.NewWriter(1)
	inW.Byte(PlayerInputFlagShift)
	if err := srv.HandlePlayerInputPacket(session, protocol.NewReader(inW.Bytes())); err != nil {
		t.Fatalf("HandlePlayerInputPacket: %v", err)
	}
	if session.CameraEntityID() != session.EntityID {
		t.Fatalf("CameraEntityID after Shift = %d, want self (%d)", session.CameraEntityID(), session.EntityID)
	}
	// TeleportToEntity packet teleports spectator to target's position.
	srv.SetPlayerTransform(session, 0.5, 65.0, 0.5, 0, 0, true)
	tpW := protocol.NewWriter(16)
	tpW.UUID(target.Profile.UUID)
	if err := srv.HandleTeleportToEntityPacket(session, protocol.NewReader(tpW.Bytes())); err != nil {
		t.Fatalf("HandleTeleportToEntityPacket: %v", err)
	}
	snap := session.Snapshot()
	if snap.X != 120.5 || snap.Y != 70.0 || snap.Z != -45.5 {
		t.Fatalf("Spectator pos after TeleportToEntity = (%v, %v, %v), want (120.5, 70.0, -45.5)", snap.X, snap.Y, snap.Z)
	}

	// 6. PlayerData round-trip preserves Abilities and SlotItemMetas (Damage, CustomName, Enchantments, ArmorTrim).
	_ = srv.SetPlayerGameMode(session, GameModeSurvival)
	SetSlotArmorTrim(session, ArmorSlotChest, ArmorTrim{
		Material: "minecraft:amethyst",
		Pattern:  "minecraft:silence",
	})
	pd := session.PlayerData()
	comp := world.PlayerDataToCompound(pd)
	decoded, err := world.PlayerDataFromCompound(comp)
	if err != nil {
		t.Fatalf("PlayerDataFromCompound: %v", err)
	}
	restored, err := srv.RegisterPlayer(Profile{
		Name: "RestoredPlayer",
		UUID: OfflineUUID("RestoredPlayer"),
	}, nil)
	if err != nil {
		t.Fatalf("RegisterPlayer(RestoredPlayer): %v", err)
	}
	restored.ApplyPlayerData(decoded)
	if meta := GetSlotItemMeta(restored, ArmorSlotChest); meta.CustomName != "ChestGuard" || meta.Damage != 7 {
		t.Fatalf("restored ChestGuard meta = %+v, want CustomName=ChestGuard Damage=7", meta)
	}
	if trim, ok := GetSlotArmorTrim(restored, ArmorSlotChest); !ok || trim.Pattern != "minecraft:silence" {
		t.Fatalf("restored ArmorTrim = (%+v, %v), want silence/amethyst", trim, ok)
	}
}

func TestServerPersistenceHooksAndFurnaceCustomName(t *testing.T) {
	dir := t.TempDir()
	store, err := world.NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	cache := world.NewCacheWithStore(-1, world.GenerateFlat, store)
	cfg := DefaultConfig()
	cfg.WorldDir = dir
	srv, err := NewWithCache(cfg, cache)
	if err != nil {
		t.Fatalf("NewWithCache: %v", err)
	}
	srv.store = store
	dim := world.DimensionOverworld

	// 1. Place a furnace with CustomName via SetFurnaceState and verify OpenFurnace uses CustomName.
	furnaceSt, ok := world.BlockStateByName("minecraft:furnace", map[string]string{
		"facing": "north",
		"lit":    "false",
	})
	if !ok {
		t.Fatalf("missing furnace block state")
	}
	cache.SetBlock(2, 64, 2, furnaceSt)
	srv.SetFurnaceState(world.FurnaceBlockEntity{
		ID:         "minecraft:furnace",
		X:          2,
		Y:          64,
		Z:          2,
		CustomName: "Master Smelter",
	})
	f, ok := srv.FurnaceAt(2, 64, 2)
	if !ok || f.CustomName != "Master Smelter" {
		t.Fatalf("FurnaceAt CustomName = %q, want 'Master Smelter'", f.CustomName)
	}

	// 2. Populate chunk (0, 0) with a hopper, campfire, copper golem statue, and scheduled redstone tick.
	hopperSt, _ := world.BlockStateByName("minecraft:hopper", map[string]string{
		"enabled": "true",
		"facing":  "down",
	})
	cache.SetBlock(3, 64, 3, hopperSt)
	srv.SetHopperCustomName(dim, 3, 64, 3, "Chunk Hopper")
	srv.SetCopperGolemStatueAt(&CopperGolemStatueBlockEntity{
		Dimension:  dim,
		X:          4,
		Y:          64,
		Z:          4,
		Pose:       "running",
		Stage:      world.CopperStageWeathered,
		CustomName: "Copper Guard",
	})
	cache.ScheduleBlockUpdate(2, 64, 2, world.UpdateRedstone, 10)

	// Non-destructive read via ChunkBlockEntitiesNBT.
	nonDestructive := srv.ChunkBlockEntitiesNBT(dim, 0, 0)
	if len(nonDestructive) < 3 {
		t.Fatalf("ChunkBlockEntitiesNBT count = %d, want >= 3", len(nonDestructive))
	}
	if _, ok := srv.HopperAt(dim, 3, 64, 3); !ok {
		t.Fatalf("expected hopper still in memory after ChunkBlockEntitiesNBT")
	}

	// Destructive chunk export/unload and re-import via ExportChunkPersistenceNBT / ImportChunkPersistenceNBT.
	chunkNBT := srv.ExportChunkPersistenceNBT(dim, 0, 0, true)
	if _, ok := srv.HopperAt(dim, 3, 64, 3); ok {
		t.Fatalf("expected hopper unloaded after ExportChunkPersistenceNBT(unload=true)")
	}
	if _, ok := srv.GetCopperGolemStatueAt(dim, 4, 64, 4); ok {
		t.Fatalf("expected copper statue unloaded after ExportChunkPersistenceNBT(unload=true)")
	}
	if cache.HasPendingBlockUpdate(2, 64, 2, world.UpdateRedstone) {
		t.Fatalf("expected scheduled redstone tick unloaded after ExportChunkPersistenceNBT(unload=true)")
	}

	srv.ImportChunkPersistenceNBT(dim, 0, 0, chunkNBT)
	if h, ok := srv.HopperAt(dim, 3, 64, 3); !ok || h.CustomName != "Chunk Hopper" {
		t.Fatalf("expected hopper restored after ImportChunkPersistenceNBT, got %+v (ok=%v)", h, ok)
	}
	if st, ok := srv.GetCopperGolemStatueAt(dim, 4, 64, 4); !ok || st.CustomName != "Copper Guard" {
		t.Fatalf("expected copper statue restored after ImportChunkPersistenceNBT, got %+v (ok=%v)", st, ok)
	}
	if !cache.HasPendingBlockUpdate(2, 64, 2, world.UpdateRedstone) {
		t.Fatalf("expected scheduled redstone tick restored after ImportChunkPersistenceNBT")
	}

	// 3. Verify SaveAll writes copper block entities and redstone runtime file, and startup reloads them.
	if err := srv.SaveAll(); err != nil {
		t.Fatalf("SaveAll: %v", err)
	}
	cache2 := world.NewCacheWithStore(-1, world.GenerateFlat, store)
	cache2.SetBlock(2, 64, 2, furnaceSt)
	cache2.SetBlock(3, 64, 3, hopperSt)
	srv2, err := NewWithCache(cfg, cache2)
	if err != nil {
		t.Fatalf("NewWithCache(srv2): %v", err)
	}
	if st, ok := srv2.GetCopperGolemStatueAt(dim, 4, 64, 4); !ok || st.CustomName != "Copper Guard" {
		t.Fatalf("expected copper statue loaded on startup, got %+v (ok=%v)", st, ok)
	}
	if !cache2.HasPendingBlockUpdate(2, 64, 2, world.UpdateRedstone) {
		t.Fatalf("expected scheduled redstone tick loaded from redstone_runtime.dat on startup")
	}
}



