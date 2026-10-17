package server

import (
	"os"
	"path/filepath"
	"testing"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

func TestStatRegistries26_1_2(t *testing.T) {
	// 1. Block registry IDs (0-based BuiltInRegistries.BLOCK index, distinct from block state ID).
	airID, ok := world.BlockRegistryID("minecraft:air")
	if !ok || airID != 0 {
		t.Fatalf("BlockRegistryID(minecraft:air) = (%d, %v), want (0, true)", airID, ok)
	}
	stoneID, ok := world.BlockRegistryID("minecraft:stone")
	if !ok || stoneID != 1 {
		t.Fatalf("BlockRegistryID(minecraft:stone) = (%d, %v), want (1, true)", stoneID, ok)
	}
	if name, ok := world.BlockRegistryName(stoneID); !ok || name != "minecraft:stone" {
		t.Fatalf("BlockRegistryName(%d) = (%q, %v), want (minecraft:stone, true)", stoneID, name, ok)
	}

	// 2. Custom stats registry (77 entries in 26.1.2).
	if len(vanillaCustomStats) != 77 {
		t.Fatalf("len(vanillaCustomStats) = %d, want 77", len(vanillaCustomStats))
	}
	wantCustomIDs := map[string]int32{
		StatLeaveGame:                    0,
		StatPlayTime:                     1,
		StatTotalWorldTime:               2,
		StatTimeSinceDeath:               3,
		StatTimeSinceRest:                4,
		StatSneakTime:                    5,
		StatWalkOneCm:                    6,
		StatCrouchOneCm:                  7,
		StatSprintOneCm:                  8,
		StatFallOneCm:                    10,
		StatHappyGhastOneCm:              17,
		StatNautilusOneCm:                22,
		StatJump:                         23,
		StatDrop:                         24,
		StatDamageDealt:                  25,
		StatDamageTaken:                  28,
		StatDeaths:                       32,
		StatMobKills:                     33,
		StatPlayerKills:                  35,
		StatFishCaught:                   36,
		StatEnchantItem:                  55,
		StatSleepInBed:                   60,
		StatOpenBarrel:                   62,
		StatInteractWithBlastFurnace:     63,
		StatInteractWithSmoker:           64,
		StatInteractWithLectern:          65,
		StatInteractWithCampfire:         66,
		StatInteractWithCartographyTable: 67,
		StatInteractWithLoom:             68,
		StatInteractWithStonecutter:      69,
		StatInteractWithSmithingTable:    76,
	}
	for name, wantID := range wantCustomIDs {
		gotID, ok := CustomStatRegistryID(name)
		if !ok || gotID != wantID {
			t.Errorf("CustomStatRegistryID(%q) = (%d, %v), want (%d, true)", name, gotID, ok, wantID)
		}
		gotName, ok := CustomStatRegistryName(gotID)
		if !ok || gotName != name {
			t.Errorf("CustomStatRegistryName(%d) = (%q, %v), want (%q, true)", gotID, gotName, ok, name)
		}
	}

	// 3. Entity type registry (157 entries in 26.1.2).
	if len(vanillaEntityTypes) != 157 {
		t.Fatalf("len(vanillaEntityTypes) = %d, want 157", len(vanillaEntityTypes))
	}
	zombieID, ok := EntityTypeRegistryID("minecraft:zombie")
	if !ok || zombieID <= 0 {
		t.Fatalf("EntityTypeRegistryID(minecraft:zombie) = (%d, %v), want (>0, true)", zombieID, ok)
	}
	if name, ok := EntityTypeRegistryName(zombieID); !ok || name != "minecraft:zombie" {
		t.Fatalf("EntityTypeRegistryName(%d) = (%q, %v), want (minecraft:zombie, true)", zombieID, name, ok)
	}
	playerID, ok := EntityTypeRegistryID("minecraft:player")
	if !ok || playerID != 155 {
		t.Fatalf("EntityTypeRegistryID(minecraft:player) = (%d, %v), want (155, true)", playerID, ok)
	}
}

func TestAwardStatsPacketWireRoundTrip(t *testing.T) {
	entries := []AwardStatEntry{
		{CategoryID: StatTypeMined, StatID: 1, Value: 42},
		{CategoryID: StatTypeCrafted, StatID: 50, Value: 16},
		{CategoryID: StatTypeCustom, StatID: 23, Value: 100},
		{CategoryID: StatTypeKilled, StatID: 150, Value: 3},
	}
	raw := EncodeAwardStatsPacket(entries)
	decoded, err := DecodeAwardStatsPacket(protocol.NewReader(raw))
	if err != nil {
		t.Fatalf("DecodeAwardStatsPacket: %v", err)
	}
	if len(decoded) != len(entries) {
		t.Fatalf("len(decoded) = %d, want %d", len(decoded), len(entries))
	}
	for i := range entries {
		if decoded[i] != entries[i] {
			t.Errorf("decoded[%d] = %+v, want %+v", i, decoded[i], entries[i])
		}
	}
}

func TestPlayerStatsGameplayHooksAndRequestPacket(t *testing.T) {
	srv := newSurvivalTestServer(t)
	cache := srv.Chunks()
	collector := &survivalPacketCollector{}
	sess, err := srv.RegisterPlayer(Profile{
		UUID: [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
		Name: "StatTester",
	}, collector.send)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	srv.SetPlayerGameMode(sess, GameModeSurvival)
	srv.SetPlayerTransform(sess, 0.5, 64.0, 0.5, 0, 0, true)

	// 1. Mining a block with a wooden pickaxe that is 1 durability away from breaking.
	stoneState, ok := world.BlockStateByName("minecraft:stone", nil)
	if !ok {
		t.Fatalf("missing minecraft:stone state")
	}
	cache.SetBlock(1, 64, 0, stoneState)
	pickID, ok := world.ItemNameToID("minecraft:wooden_pickaxe")
	if !ok {
		t.Fatalf("missing minecraft:wooden_pickaxe item ID")
	}
	sess.SetSelectedSlot(0)
	sess.SetInventorySlot(world.HotbarContainerOffset, world.NewItemStack(pickID, 1))
	maxDur := MaxItemDurability("minecraft:wooden_pickaxe")
	SetSlotItemMeta(sess, world.HotbarContainerOffset, ItemMeta{Damage: maxDur - 1})

	broken, _ := srv.BreakBlock(sess, 1, 64, 0, PlayerActionStopDestroyBlock)
	if !broken {
		t.Fatalf("BreakBlock returned false")
	}
	if got := srv.GetPlayerStat(sess, StatTypeMined, "minecraft:stone"); got != 1 {
		t.Errorf("mined minecraft:stone = %d, want 1", got)
	}
	if got := srv.GetPlayerStat(sess, StatTypeUsed, "minecraft:wooden_pickaxe"); got != 1 {
		t.Errorf("used minecraft:wooden_pickaxe = %d, want 1", got)
	}
	if got := srv.GetPlayerStat(sess, StatTypeBroken, "minecraft:wooden_pickaxe"); got != 1 {
		t.Errorf("broken minecraft:wooden_pickaxe = %d, want 1", got)
	}

	// 2. Picking up dropped items (1 cobblestone from mined stone + 5 spawned = 6).
	cobbleID, _ := world.ItemNameToID("minecraft:cobblestone")
	srv.SpawnItemEntityInDimension(world.DimensionOverworld, 0.5, 64.0, 0.5, world.NewItemStack(cobbleID, 5))
	picked := srv.TryPickupItems(sess)
	if picked != 6 {
		t.Fatalf("TryPickupItems = %d, want 6", picked)
	}
	if got := srv.GetPlayerStat(sess, StatTypePickedUp, "minecraft:cobblestone"); got != 6 {
		t.Errorf("picked_up minecraft:cobblestone = %d, want 6", got)
	}

	// 3. Dropping items from cursor outside window.
	sess.SetCarriedItem(world.NewItemStack(cobbleID, 3))
	if err := srv.HandleContainerClick(sess, 0, -999, 0, ContainerInputPickup); err != nil {
		t.Fatalf("HandleContainerClick drop: %v", err)
	}
	if got := srv.GetPlayerStat(sess, StatTypeDropped, "minecraft:cobblestone"); got != 3 {
		t.Errorf("dropped minecraft:cobblestone = %d, want 3", got)
	}
	if got := srv.GetPlayerStat(sess, StatTypeCustom, StatDrop); got != 3 {
		t.Errorf("custom minecraft:drop = %d, want 3", got)
	}

	// 4. Crafting planks in 2x2 inventory grid.
	oakLogID, _ := world.ItemNameToID("minecraft:oak_log")
	sess.SetInventorySlot(1, world.NewItemStack(oakLogID, 1))
	if err := srv.HandleContainerClick(sess, 0, 0, 0, ContainerInputPickup); err != nil {
		t.Fatalf("HandleContainerClick craft: %v", err)
	}
	if got := srv.GetPlayerStat(sess, StatTypeCrafted, "minecraft:oak_planks"); got != 4 {
		t.Errorf("crafted minecraft:oak_planks = %d, want 4", got)
	}

	// 5. Movement stats (walk, sprint, crouch, jump, fall).
	srv.RecordPlayerMovementStats(sess, 0.5, 64.0, 0.5, 2.5, 64.0, 0.5, true, true) // 2.0m = 200cm walk
	if got := srv.GetPlayerStat(sess, StatTypeCustom, StatWalkOneCm); got != 200 {
		t.Errorf("walk_one_cm = %d, want 200", got)
	}
	sess.SetSprinting(true)
	srv.RecordPlayerMovementStats(sess, 2.5, 64.0, 0.5, 4.0, 64.0, 0.5, true, true) // 1.5m = 150cm sprint
	sess.SetSprinting(false)
	if got := srv.GetPlayerStat(sess, StatTypeCustom, StatSprintOneCm); got != 150 {
		t.Errorf("sprint_one_cm = %d, want 150", got)
	}
	sess.SetSneaking(true)
	srv.RecordPlayerMovementStats(sess, 4.0, 64.0, 0.5, 5.0, 64.0, 0.5, true, true) // 1.0m = 100cm crouch
	sess.SetSneaking(false)
	if got := srv.GetPlayerStat(sess, StatTypeCustom, StatCrouchOneCm); got != 100 {
		t.Errorf("crouch_one_cm = %d, want 100", got)
	}
	// Jump upward from ground.
	srv.RecordPlayerMovementStats(sess, 5.0, 64.0, 0.5, 5.0, 65.2, 0.5, true, false)
	if got := srv.GetPlayerStat(sess, StatTypeCustom, StatJump); got != 1 {
		t.Errorf("jump = %d, want 1", got)
	}
	// Fall downward while airborne.
	srv.RecordPlayerMovementStats(sess, 5.0, 65.2, 0.5, 5.0, 64.0, 0.5, false, true)
	if got := srv.GetPlayerStat(sess, StatTypeCustom, StatFallOneCm); got != 120 {
		t.Errorf("fall_one_cm = %d, want 120", got)
	}

	// 6. Combat stats: killing a mob.
	zombieID, _ := srv.SpawnMob(1.5, 64.0, 0.5, MobTypeZombie)
	srv.SetMobHealth(zombieID, 1.0)
	dealt, killed := srv.AttackEntityWithEnchantments(sess, zombieID)
	if !killed || dealt <= 0 {
		t.Fatalf("AttackEntityWithEnchantments = (%v, %v), want (>0, true)", dealt, killed)
	}
	if got := srv.GetPlayerStat(sess, StatTypeKilled, MobTypeZombie); got != 1 {
		t.Errorf("killed zombie = %d, want 1", got)
	}
	if got := srv.GetPlayerStat(sess, StatTypeCustom, StatMobKills); got != 1 {
		t.Errorf("mob_kills = %d, want 1", got)
	}
	if got := srv.GetPlayerStat(sess, StatTypeCustom, StatDamageDealt); got <= 0 {
		t.Errorf("damage_dealt = %d, want >0", got)
	}

	// 7. Tick stats & damage/death stats.
	srv.TickAllPlayerStats()
	if got := srv.GetPlayerStat(sess, StatTypeCustom, StatPlayTime); got != 1 {
		t.Errorf("play_time = %d, want 1", got)
	}
	if got := srv.GetPlayerStat(sess, StatTypeCustom, StatTimeSinceDeath); got != 1 {
		t.Errorf("time_since_death = %d, want 1", got)
	}
	srv.DamagePlayer(sess, 5.0, "mob")
	if got := srv.GetPlayerStat(sess, StatTypeCustom, StatDamageTaken); got != 50 {
		t.Errorf("damage_taken = %d, want 50", got)
	}
	srv.DamagePlayer(sess, 25.0, "mob")
	if got := srv.GetPlayerStat(sess, StatTypeCustom, StatDeaths); got != 1 {
		t.Errorf("deaths = %d, want 1", got)
	}
	if got := srv.GetPlayerStat(sess, StatTypeCustom, StatTimeSinceDeath); got != 0 {
		t.Errorf("time_since_death after death = %d, want 0", got)
	}

	// 8. ClientCommandRequestStats -> PlayAwardStats (0x03).
	collector.reset()
	if err := srv.HandleRequestStats(sess); err != nil {
		t.Fatalf("HandleRequestStats: %v", err)
	}
	awardPayload, ok := collector.last(protocol.PlayAwardStats)
	if !ok {
		t.Fatalf("expected PlayAwardStats (0x%02x) packet", protocol.PlayAwardStats)
	}
	entries, err := DecodeAwardStatsPacket(protocol.NewReader(awardPayload))
	if err != nil {
		t.Fatalf("DecodeAwardStatsPacket: %v", err)
	}
	if len(entries) == 0 {
		t.Fatalf("expected non-empty AwardStats entries")
	}
}

func TestPlayerStatsJSONPersistenceRoundTrip(t *testing.T) {
	worldDir := t.TempDir()
	srv := newSurvivalTestServer(t)
	srv.cfg.WorldDir = worldDir

	uuid := [16]byte{0x10, 0x20, 0x30, 0x40, 0x50, 0x60, 0x70, 0x80, 0x90, 0xa0, 0xb0, 0xc0, 0xd0, 0xe0, 0xf0, 0x01}
	sess1 := &PlayerSession{
		EntityID: 1,
		Profile:  Profile{UUID: uuid, Name: "PersistPlayer"},
	}
	srv.SetPlayerStat(sess1, StatTypeMined, "minecraft:stone", 128)
	srv.SetPlayerStat(sess1, StatTypeCrafted, "minecraft:crafting_table", 2)
	srv.SetPlayerStat(sess1, StatTypeCustom, StatJump, 55)
	srv.SetPlayerStat(sess1, StatTypeKilled, "minecraft:zombie", 9)

	if err := srv.SavePlayerStatsFile(sess1); err != nil {
		t.Fatalf("SavePlayerStatsFile: %v", err)
	}

	expectedPath := filepath.Join(worldDir, "stats", FormatUUID(uuid)+".json")
	if _, err := os.Stat(expectedPath); err != nil {
		t.Fatalf("expected stats file at %s: %v", expectedPath, err)
	}

	sess2 := &PlayerSession{
		EntityID: 2,
		Profile:  Profile{UUID: uuid, Name: "PersistPlayer"},
	}
	if err := srv.LoadPlayerStatsFile(sess2); err != nil {
		t.Fatalf("LoadPlayerStatsFile: %v", err)
	}
	if got := srv.GetPlayerStat(sess2, StatTypeMined, "minecraft:stone"); got != 128 {
		t.Errorf("loaded mined stone = %d, want 128", got)
	}
	if got := srv.GetPlayerStat(sess2, StatTypeCrafted, "minecraft:crafting_table"); got != 2 {
		t.Errorf("loaded crafted crafting_table = %d, want 2", got)
	}
	if got := srv.GetPlayerStat(sess2, StatTypeCustom, StatJump); got != 55 {
		t.Errorf("loaded custom jump = %d, want 55", got)
	}
	if got := srv.GetPlayerStat(sess2, StatTypeKilled, "minecraft:zombie"); got != 9 {
		t.Errorf("loaded killed zombie = %d, want 9", got)
	}
}
