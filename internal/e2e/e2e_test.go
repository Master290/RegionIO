package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"regionio/internal/protocol"
	"regionio/internal/registry"
	"regionio/internal/server"
	"regionio/internal/world"
)

func TestHeadlessClientFullLifecycle(t *testing.T) {
	worldDir := t.TempDir()
	hcfg := DefaultHarnessConfig(worldDir)
	// Use the fast deterministic flat generator backed by the real Anvil Store
	// in worldDir so the full E2E test runs quickly even under -race while
	// exercising real disk persistence (.mca regions, time, and playerdata).
	hcfg.UseFlatGenerator = true
	hcfg.WorldSeed = 424242

	harness, err := StartHarness(hcfg)
	if err != nil {
		t.Fatalf("StartHarness: %v", err)
	}
	defer func() {
		if err := harness.Close(); err != nil {
			t.Errorf("harness.Close: %v", err)
		}
	}()

	client, err := Dial(harness.Addr, 5*time.Second)
	if err != nil {
		t.Fatalf("Dial(%s): %v", harness.Addr, err)
	}
	defer client.Close()

	// 1. Handshake + Login (offline-mode, zlib compression negotiation).
	const username = "E2EBot"
	if err := client.Login("127.0.0.1", harness.Port, username, 5*time.Second); err != nil {
		t.Fatalf("Login: %v", err)
	}
	wantUUID := server.OfflineUUID(username)
	if client.Username != username {
		t.Fatalf("Username = %q, want %q", client.Username, username)
	}
	if client.UUID != wantUUID {
		t.Fatalf("UUID = %x, want %x", client.UUID, wantUUID)
	}

	// 2. Configuration phase (brand, enabled features, known packs, registries, tags).
	if err := client.CompleteConfiguration(2, 10*time.Second); err != nil {
		t.Fatalf("CompleteConfiguration: %v", err)
	}
	if client.Brand != "RegionIO" {
		t.Fatalf("server brand = %q, want %q", client.Brand, "RegionIO")
	}
	if wantRegs := len(registry.Synced()); client.SyncedRegistries != wantRegs {
		t.Fatalf("SyncedRegistries = %d, want %d", client.SyncedRegistries, wantRegs)
	}
	if client.UpdatedTagsBytes == 0 {
		t.Fatal("expected non-empty update_tags payload during configuration")
	}

	// 3. Play phase: receive spawn and level chunks.
	// With viewDistance=2, the 5x5 view around (0,0) has 25 chunks; wait for at least 9.
	if err := client.WaitForSpawnAndChunks(9, 15*time.Second); err != nil {
		t.Fatalf("WaitForSpawnAndChunks: %v", err)
	}
	if client.LoadedChunks[[2]int32{0, 0}] <= 0 {
		t.Fatalf("spawn chunk (0,0) not received; loaded=%d", len(client.LoadedChunks))
	}
	wantSpawnY := float64(world.FlatSurfaceY + 1)
	if client.X != 8.5 || client.Y != wantSpawnY || client.Z != 8.5 {
		t.Fatalf("spawn pos = (%.1f, %.1f, %.1f), want (8.5, %.1f, 8.5)",
			client.X, client.Y, client.Z, wantSpawnY)
	}

	// 4. Move the player (including across a chunk boundary into chunk (1,0))
	// and verify both server-side session state and chunk cache recentering.
	targetX, targetY, targetZ := 20.25, wantSpawnY, 12.75
	targetYaw, targetPitch := float32(45.0), float32(-15.0)
	if err := client.MovePosRot(targetX, targetY, targetZ, targetYaw, targetPitch, true); err != nil {
		t.Fatalf("MovePosRot: %v", err)
	}
	if err := client.WaitUntil(5*time.Second, func() bool {
		return client.CenterX == 1 && client.CenterZ == 0
	}); err != nil {
		t.Fatalf("waiting for chunk cache recenter to (1,0): %v (center=(%d,%d))",
			err, client.CenterX, client.CenterZ)
	}

	session := harness.Server.PlayerByName(username)
	if session == nil {
		t.Fatalf("PlayerByName(%q) returned nil", username)
	}
	if snap := session.Snapshot(); snap.X != targetX || snap.Y != targetY || snap.Z != targetZ || snap.Yaw != targetYaw {
		t.Fatalf("server player pos = (%.2f, %.2f, %.2f, yaw=%.1f), want (%.2f, %.2f, %.2f, yaw=%.1f)",
			snap.X, snap.Y, snap.Z, snap.Yaw, targetX, targetY, targetZ, targetYaw)
	}

	// Connect a second headless client (ObserverBot) to verify that block
	// breaking and placement are broadcast and visible to nearby players too.
	observer, err := Dial(harness.Addr, 5*time.Second)
	if err != nil {
		t.Fatalf("Dial observer: %v", err)
	}
	defer observer.Close()
	if err := observer.Login("127.0.0.1", harness.Port, "ObserverBot", 5*time.Second); err != nil {
		t.Fatalf("observer Login: %v", err)
	}
	if err := observer.CompleteConfiguration(2, 10*time.Second); err != nil {
		t.Fatalf("observer CompleteConfiguration: %v", err)
	}
	if err := observer.WaitForSpawnAndChunks(9, 15*time.Second); err != nil {
		t.Fatalf("observer WaitForSpawnAndChunks: %v", err)
	}

	// 5. Break a surface block at (9, FlatSurfaceY, 9) and verify visibility on
	// both clients and the server chunk cache.
	bx, by, bz := 9, world.FlatSurfaceY, 9
	if got := harness.Server.Chunks().GetBlock(bx, by, bz); got == world.StateAir {
		t.Fatalf("expected solid surface block at (%d,%d,%d) before break, got air", bx, by, bz)
	}
	if err := client.BreakBlock(bx, by, bz, server.PlayerActionStartDestroyBlock, 1, 101); err != nil {
		t.Fatalf("BreakBlock: %v", err)
	}
	if err := client.WaitUntil(5*time.Second, func() bool {
		st, ok := client.BlockStates[[3]int{bx, by, bz}]
		return ok && st == world.StateAir && client.LastAckedSeq == 101
	}); err != nil {
		t.Fatalf("waiting for break block_update to air on actor: %v", err)
	}
	if err := observer.WaitUntil(5*time.Second, func() bool {
		st, ok := observer.BlockStates[[3]int{bx, by, bz}]
		return ok && st == world.StateAir
	}); err != nil {
		t.Fatalf("waiting for break block_update to air on observer: %v", err)
	}
	if got := harness.Server.Chunks().GetBlock(bx, by, bz); got != world.StateAir {
		t.Fatalf("server chunk block at (%d,%d,%d) after break = %d, want StateAir (%d)",
			bx, by, bz, got, world.StateAir)
	}

	// 6. Place a block into the broken spot (clicking UP face of (bx, by-1, bz))
	// and verify both clients' packets and server chunk state.
	itemID, ok := world.ItemNameToID("minecraft:stone")
	if !ok {
		t.Fatal("world.ItemNameToID(minecraft:stone) not found")
	}
	placedState, ok := world.ItemToBlock(itemID)
	if !ok || placedState == world.StateAir {
		t.Fatalf("world.ItemToBlock(%d) = (%d, %v), want non-air state", itemID, placedState, ok)
	}

	if err := client.SetCarriedItem(0); err != nil {
		t.Fatalf("SetCarriedItem: %v", err)
	}
	// Hotbar slot 0 is container slot 36.
	if err := client.SetCreativeInventorySlot(world.HotbarContainerOffset, itemID, 64); err != nil {
		t.Fatalf("SetCreativeInventorySlot: %v", err)
	}
	// Click the UP face (face=1) of the dirt block at (bx, by-1, bz) to place at (bx, by, bz).
	if err := client.PlaceBlockAgainst(bx, by-1, bz, 1, 102); err != nil {
		t.Fatalf("PlaceBlockAgainst: %v", err)
	}
	if err := client.WaitUntil(5*time.Second, func() bool {
		st, exists := client.BlockStates[[3]int{bx, by, bz}]
		return exists && st == placedState && client.LastAckedSeq == 102
	}); err != nil {
		t.Fatalf("waiting for place block_update on actor (want state %d): %v", placedState, err)
	}
	if err := observer.WaitUntil(5*time.Second, func() bool {
		st, exists := observer.BlockStates[[3]int{bx, by, bz}]
		return exists && st == placedState
	}); err != nil {
		t.Fatalf("waiting for place block_update on observer (want state %d): %v", placedState, err)
	}
	if got := harness.Server.Chunks().GetBlock(bx, by, bz); got != placedState {
		t.Fatalf("server chunk block at (%d,%d,%d) after place = %d, want %d",
			bx, by, bz, got, placedState)
	}

	// 7. Execute commands (/time set 6000, /gamemode survival, /gamemode creative, /list, /save-all).
	if err := client.SendCommand("time set 6000"); err != nil {
		t.Fatalf("SendCommand(time set 6000): %v", err)
	}
	if err := client.WaitUntil(5*time.Second, func() bool {
		return client.HasTime && client.DayTime == 6000 && hasChatContaining(client.SystemChats, "Set the time to 6000")
	}); err != nil {
		t.Fatalf("waiting for /time set 6000 effect: %v (chats=%v, dayTime=%d)",
			err, client.SystemChats, client.DayTime)
	}
	if _, dt := harness.Server.WorldTime(); dt != 6000 {
		t.Fatalf("server WorldTime dayTime = %d, want 6000", dt)
	}

	if err := client.SendCommand("gamemode survival"); err != nil {
		t.Fatalf("SendCommand(gamemode survival): %v", err)
	}
	if err := client.WaitUntil(5*time.Second, func() bool {
		return client.GameMode == server.GameModeSurvival &&
			client.AbilityFlags == 0x00 &&
			hasChatContaining(client.SystemChats, "Survival Mode")
	}); err != nil {
		t.Fatalf("waiting for /gamemode survival effect: %v (mode=%d, chats=%v)",
			err, client.GameMode, client.SystemChats)
	}
	if snap := session.Snapshot(); snap.GameMode != server.GameModeSurvival {
		t.Fatalf("server session GameMode = %d, want Survival (%d)", snap.GameMode, server.GameModeSurvival)
	}

	if err := client.SendCommand("gamemode creative"); err != nil {
		t.Fatalf("SendCommand(gamemode creative): %v", err)
	}
	if err := client.WaitUntil(5*time.Second, func() bool {
		return client.GameMode == server.GameModeCreative &&
			client.AbilityFlags == 0x0F &&
			hasChatContaining(client.SystemChats, "Creative Mode")
	}); err != nil {
		t.Fatalf("waiting for /gamemode creative effect: %v", err)
	}

	if err := client.SendCommand("list"); err != nil {
		t.Fatalf("SendCommand(list): %v", err)
	}
	if err := client.WaitUntil(5*time.Second, func() bool {
		return hasChatContaining(client.SystemChats, "E2EBot") && hasChatContaining(client.SystemChats, "ObserverBot")
	}); err != nil {
		t.Fatalf("waiting for /list output: %v (chats=%v)", err, client.SystemChats)
	}

	if err := client.SendCommand("save-all"); err != nil {
		t.Fatalf("SendCommand(save-all): %v", err)
	}
	if err := client.WaitUntil(5*time.Second, func() bool {
		return hasChatContaining(client.SystemChats, "Saved the game")
	}); err != nil {
		t.Fatalf("waiting for /save-all confirmation: %v (chats=%v)", err, client.SystemChats)
	}

	// Verify that region and playerdata files were persisted in the temporary world directory.
	regionFile := filepath.Join(worldDir, "region", "r.0.0.mca")
	if info, err := os.Stat(regionFile); err != nil || info.Size() == 0 {
		t.Fatalf("expected persisted region file %s after /save-all, stat err=%v", regionFile, err)
	}
	playerDataDir := filepath.Join(worldDir, "playerdata")
	entries, err := os.ReadDir(playerDataDir)
	if err != nil || len(entries) == 0 {
		t.Fatalf("expected persisted playerdata in %s after /save-all, entries=%d, err=%v",
			playerDataDir, len(entries), err)
	}

	// 8. Cleanly disconnect both clients and verify the server unregisters all sessions.
	if err := observer.Close(); err != nil {
		t.Fatalf("observer.Close: %v", err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("client.Close: %v", err)
	}
	disconnectDeadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(disconnectDeadline) {
		if harness.Server.PlayerCount() == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := harness.Server.PlayerCount(); got != 0 {
		t.Fatalf("server PlayerCount after disconnect = %d, want 0", got)
	}

	// 9. Reconnect E2EBot and verify its persisted position was restored from disk.
	reconnected, err := Dial(harness.Addr, 5*time.Second)
	if err != nil {
		t.Fatalf("Dial reconnect: %v", err)
	}
	defer reconnected.Close()
	if err := reconnected.Login("127.0.0.1", harness.Port, username, 5*time.Second); err != nil {
		t.Fatalf("reconnect Login: %v", err)
	}
	if err := reconnected.CompleteConfiguration(2, 10*time.Second); err != nil {
		t.Fatalf("reconnect CompleteConfiguration: %v", err)
	}
	if err := reconnected.WaitForSpawnAndChunks(5, 15*time.Second); err != nil {
		t.Fatalf("reconnect WaitForSpawnAndChunks: %v", err)
	}
	if reconnected.X != targetX || reconnected.Y != targetY || reconnected.Z != targetZ {
		t.Fatalf("reconnected pos = (%.2f, %.2f, %.2f), want persisted (%.2f, %.2f, %.2f)",
			reconnected.X, reconnected.Y, reconnected.Z, targetX, targetY, targetZ)
	}
	if err := reconnected.Close(); err != nil {
		t.Fatalf("reconnected.Close: %v", err)
	}
	disconnectDeadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(disconnectDeadline) {
		if harness.Server.PlayerCount() == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := harness.Server.PlayerCount(); got != 0 {
		t.Fatalf("server PlayerCount after second disconnect = %d, want 0", got)
	}

	// Also verify GameEventStartWaitingChunks was received during Play join.
	sawWaitingChunks := false
	for _, ev := range client.GameEvents {
		if ev.Event == protocol.GameEventStartWaitingChunks {
			sawWaitingChunks = true
			break
		}
	}
	if !sawWaitingChunks {
		t.Fatal("expected GameEventStartWaitingChunks during initial Play join sequence")
	}
}

func hasChatContaining(chats []string, substr string) bool {
	for _, msg := range chats {
		if strings.Contains(msg, substr) {
			return true
		}
	}
	return false
}
