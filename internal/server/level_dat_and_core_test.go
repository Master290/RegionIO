package server

import (
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"regionio/internal/nbt"
	"regionio/internal/world"
)

func TestIssue36AnvilChunkRoundTripVanillaWorldImportAndCorruptedChunkRecovery(t *testing.T) {
	worldDir := t.TempDir()
	cfg := DefaultConfig()
	cfg.WorldDir = worldDir
	cfg.WorldSeed = 424242

	srv, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() {
		if srv.Store() != nil {
			_ = srv.Store().Close()
		}
	}()

	// 1. Modify a block in chunk (0, 0), save, and verify Anvil persistence.
	diamondBlock, ok := world.BlockStateByName("minecraft:diamond_block", nil)
	if !ok {
		t.Fatal("missing minecraft:diamond_block state")
	}
	srv.Chunks().SetBlock(4, 70, 4, diamondBlock)
	if err := srv.SaveAll(); err != nil {
		t.Fatalf("SaveAll: %v", err)
	}

	// 2. Build a synthetic pre-1.18 vanilla region file (DataVersion 2730, nested under "Level" and "Sections")
	// plus one intentionally corrupted chunk slot at (2, 0), and import it via ImportVanillaRegionFile.
	srcRegionDir := filepath.Join(t.TempDir(), "vanilla_region")
	if err := os.MkdirAll(srcRegionDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	srcRF, err := world.OpenRegion(srcRegionDir, 0, 0)
	if err != nil {
		t.Fatalf("OpenRegion: %v", err)
	}
	legacySection := nbt.NewCompound().
		Set("Y", nbt.Byte(4)).
		Set("Palette", nbt.List{
			ElemID: nbt.TagCompound,
			Elems: []nbt.Tag{
				nbt.NewCompound().Set("Name", nbt.String("minecraft:gold_block")),
			},
		})
	legacyLevel := nbt.NewCompound().
		Set("xPos", nbt.Int(1)).
		Set("zPos", nbt.Int(0)).
		Set("Status", nbt.String("full")).
		Set("Sections", nbt.List{
			ElemID: nbt.TagCompound,
			Elems:  []nbt.Tag{legacySection},
		})
	legacyRoot := nbt.NewCompound().
		Set("DataVersion", nbt.Int(2730)).
		Set("Level", legacyLevel)
	if err := srcRF.WriteChunk(1, 0, nbt.MarshalNamed("", legacyRoot)); err != nil {
		t.Fatalf("WriteChunk legacy (1,0): %v", err)
	}
	// Write a corrupted non-NBT byte slice into slot (2, 0).
	if err := srcRF.WriteChunk(2, 0, []byte{0xde, 0xad, 0xbe, 0xef}); err != nil {
		t.Fatalf("WriteChunk corrupted (2,0): %v", err)
	}
	_ = srcRF.Close()

	// Close srv's open region handles before importing into worldDir/region.
	if srv.Store() != nil {
		_ = srv.Store().Close()
	}

	imported, recovered, err := srv.ImportVanillaRegionFile(srcRegionDir, 0, 0)
	if err != nil {
		t.Fatalf("ImportVanillaRegionFile: %v", err)
	}
	if imported != 1 || recovered != 1 {
		t.Fatalf("ImportVanillaRegionFile = (imported=%d, recovered=%d), want (1, 1)", imported, recovered)
	}

	// Reopen server on worldDir and verify both (0, 0) and migrated (1, 0) load cleanly.
	srv2, err := New(cfg)
	if err != nil {
		t.Fatalf("New(srv2): %v", err)
	}
	defer func() {
		if srv2.Store() != nil {
			_ = srv2.Store().Close()
		}
	}()
	if got := srv2.Chunks().GetBlock(4, 70, 4); got != diamondBlock {
		t.Fatalf("reloaded block at (4,70,4) = %d, want diamond_block (%d)", got, diamondBlock)
	}
	goldBlock, _ := world.BlockStateByName("minecraft:gold_block", nil)
	if got := srv2.Chunks().GetBlock(16+2, 64+2, 2); got != goldBlock {
		t.Fatalf("migrated legacy chunk (1,0) block at (18,66,2) = %d (%s), want gold_block (%d)", got, world.BlockName(got), goldBlock)
	}

	// 3. Test SafeRunChunkCallback panic recovery and SafeLoadOrRecoverChunk on corrupted chunk data.
	panicked, recErr := srv2.SafeRunChunkCallback(world.DimensionOverworld, 5, 5, func() {
		panic("simulated corrupted block entity tick")
	})
	if !panicked || recErr == nil {
		t.Fatalf("SafeRunChunkCallback = (panicked=%v, err=%v), want (true, non-nil)", panicked, recErr)
	}
	if srv2.CorruptedChunksRecovered() < 1 {
		t.Fatalf("CorruptedChunksRecovered = %d, want >= 1", srv2.CorruptedChunksRecovered())
	}

	ch, _, err := srv2.SafeLoadOrRecoverChunk(world.DimensionOverworld, 1, 0)
	if err != nil || ch == nil {
		t.Fatalf("SafeLoadOrRecoverChunk(1,0) = (%v, %v), want non-nil chunk", ch, err)
	}
}

func TestIssue36LevelDatRoundTripFallbackAndWorldPersistence(t *testing.T) {
	worldDir := t.TempDir()
	cfg := DefaultConfig()
	cfg.WorldDir = worldDir
	cfg.WorldSeed = -9876543210

	srv, err := NewWithCache(cfg, world.NewCache(-1, world.GenerateFlat))
	if err != nil {
		t.Fatalf("NewWithCache: %v", err)
	}

	srv.SetWorldTime(14500, 18250)
	srv.SetWeather(WeatherThunder, 4800)
	srv.SetWorldSpawn(128, 72, -64, 90.0)
	srv.SetDefaultGameMode(GameModeAdventure)
	srv.SetDifficulty(DifficultyHard)
	srv.SetDifficultyLocked(true)
	srv.SetKeepInventory(true)
	srv.SetRandomTickSpeed(7)
	srv.WorldBorder().SetCenter(50.0, -50.0)
	srv.WorldBorder().SetSize(2500.0)

	// 1. SaveLevelDat creates level.dat; second SaveLevelDat rotates previous to level.dat_old.
	if err := srv.SaveLevelDat(); err != nil {
		t.Fatalf("first SaveLevelDat: %v", err)
	}
	srv.SetWorldTime(14600, 18350)
	if err := srv.SaveLevelDat(); err != nil {
		t.Fatalf("second SaveLevelDat: %v", err)
	}

	levelDatPath := filepath.Join(worldDir, "level.dat")
	levelDatOldPath := filepath.Join(worldDir, "level.dat_old")
	if _, err := os.Stat(levelDatPath); err != nil {
		t.Fatalf("level.dat missing: %v", err)
	}
	if _, err := os.Stat(levelDatOldPath); err != nil {
		t.Fatalf("level.dat_old missing after rotation: %v", err)
	}

	// 2. Verify level.dat loads into a fresh Server instance with all fields preserved.
	srvReload, err := NewWithCache(cfg, world.NewCache(-1, world.GenerateFlat))
	if err != nil {
		t.Fatalf("NewWithCache(srvReload): %v", err)
	}
	gt, dt := srvReload.WorldTime()
	if gt != 14600 || dt != 18350 {
		t.Fatalf("reloaded WorldTime = (%d, %d), want (14600, 18350)", gt, dt)
	}
	if !srvReload.IsThundering() || !srvReload.IsRaining() {
		t.Fatalf("reloaded weather = %+v, want thundering", srvReload.Weather())
	}
	sx, sy, sz, syaw := srvReload.WorldSpawn()
	if sx != 128 || sy != 72 || sz != -64 || syaw != 90.0 {
		t.Fatalf("reloaded WorldSpawn = (%d,%d,%d, %.1f), want (128,72,-64, 90.0)", sx, sy, sz, syaw)
	}
	if srvReload.DefaultGameMode() != GameModeAdventure {
		t.Fatalf("reloaded DefaultGameMode = %d, want %d", srvReload.DefaultGameMode(), GameModeAdventure)
	}
	if srvReload.Difficulty() != DifficultyHard || !srvReload.DifficultyLocked() {
		t.Fatalf("reloaded Difficulty = (%v, locked=%v), want (Hard, true)", srvReload.Difficulty(), srvReload.DifficultyLocked())
	}
	if !srvReload.KeepInventory() || srvReload.GameRuleInt("randomTickSpeed") != 7 {
		t.Fatalf("reloaded GameRules: keepInventory=%v, randomTickSpeed=%d", srvReload.KeepInventory(), srvReload.GameRuleInt("randomTickSpeed"))
	}
	if srvReload.WorldBorder().Size() != 2500.0 || srvReload.WorldBorder().CenterX() != 50.0 || srvReload.WorldBorder().CenterZ() != -50.0 {
		t.Fatalf("reloaded WorldBorder = (size=%.1f, center=(%.1f, %.1f)), want (2500.0, (50.0, -50.0))",
			srvReload.WorldBorder().Size(), srvReload.WorldBorder().CenterX(), srvReload.WorldBorder().CenterZ())
	}

	// 3. Corrupt primary level.dat and verify LoadLevelDat falls back to level.dat_old (which has Time=14500).
	if err := os.WriteFile(levelDatPath, []byte("corrupted-gzip-stream"), 0o644); err != nil {
		t.Fatalf("WriteFile corrupt level.dat: %v", err)
	}
	srvFallback, err := NewWithCache(cfg, world.NewCache(-1, world.GenerateFlat))
	if err != nil {
		t.Fatalf("NewWithCache(srvFallback): %v", err)
	}
	gtFB, dtFB := srvFallback.WorldTime()
	if gtFB != 14500 || dtFB != 18250 {
		t.Fatalf("fallback WorldTime from level.dat_old = (%d, %d), want (14500, 18250)", gtFB, dtFB)
	}
}

func TestIssue36ChunkTicketsSimulationDistanceAndForceLoadedChunksDat(t *testing.T) {
	// 1. Test PackChunkPosLong / UnpackChunkPosLong with positive and negative coordinates.
	coords := [][2]int32{{0, 0}, {12, -34}, {-100, 250}, {-1, -1}, {32767, -32768}}
	for _, c := range coords {
		packed := PackChunkPosLong(c[0], c[1])
		ux, uz := UnpackChunkPosLong(packed)
		if ux != c[0] || uz != c[1] {
			t.Fatalf("UnpackChunkPosLong(PackChunkPosLong(%d, %d)) = (%d, %d)", c[0], c[1], ux, uz)
		}
	}

	worldDir := t.TempDir()
	cfg := DefaultConfig()
	cfg.WorldDir = worldDir
	cfg.SimulationDistance = 6

	srv, err := NewWithCache(cfg, world.NewCache(-1, world.GenerateFlat))
	if err != nil {
		t.Fatalf("NewWithCache: %v", err)
	}
	if srv.SimulationDistance() != 6 {
		t.Fatalf("SimulationDistance = %d, want 6", srv.SimulationDistance())
	}

	// 2. Add timed tickets (post_teleport TTL=5, portal TTL=300) and persistent tickets (forced).
	srv.AddChunkTicket(world.DimensionOverworld, 3, 4, TicketTypePostTeleport, 0, 0)
	srv.AddChunkTicket(world.DimensionOverworld, 5, 6, TicketTypePortal, 0, 0)
	srv.SetChunkForceLoaded(world.DimensionOverworld, -2, 7, true)

	if lvl := srv.EffectiveChunkTicketLevel(world.DimensionOverworld, 3, 4); lvl != TicketLevelPostTeleport {
		t.Fatalf("EffectiveChunkTicketLevel(3,4) = %d, want %d", lvl, TicketLevelPostTeleport)
	}
	if lvl := srv.EffectiveChunkTicketLevel(world.DimensionOverworld, 5, 6); lvl != TicketLevelPortal {
		t.Fatalf("EffectiveChunkTicketLevel(5,6) = %d, want %d", lvl, TicketLevelPortal)
	}
	if !srv.IsChunkEntityTicking(world.DimensionOverworld, 5, 6) {
		t.Fatal("expected portal-ticketed chunk (5,6) at level 30 to be entity-ticking")
	}
	if !srv.IsChunkEntityTicking(world.DimensionOverworld, -2, 7) {
		t.Fatal("expected force-loaded chunk (-2,7) to be entity-ticking")
	}

	// Advance 5 ticks -> post_teleport expires, portal still has 295 ticks remaining.
	for i := 0; i < 5; i++ {
		srv.TickChunkTickets()
	}
	if tickets := srv.ChunkTicketsAt(world.DimensionOverworld, 3, 4); len(tickets) != 0 {
		t.Fatalf("expected post_teleport ticket to expire after 5 ticks, got %+v", tickets)
	}
	if tickets := srv.ChunkTicketsAt(world.DimensionOverworld, 5, 6); len(tickets) != 1 || tickets[0].RemainingTicks != 295 {
		t.Fatalf("portal ticket after 5 ticks = %+v, want RemainingTicks=295", tickets)
	}

	// Advance remaining 295 ticks -> portal ticket expires, forced ticket remains.
	for i := 0; i < 295; i++ {
		srv.TickChunkTickets()
	}
	if tickets := srv.ChunkTicketsAt(world.DimensionOverworld, 5, 6); len(tickets) != 0 {
		t.Fatalf("expected portal ticket to expire after 300 ticks, got %+v", tickets)
	}
	if !srv.IsChunkForceLoaded(world.DimensionOverworld, -2, 7) {
		t.Fatal("expected force-loaded chunk (-2,7) to remain force-loaded")
	}

	// 3. Verify <WorldDir>/data/chunks.dat was persisted and reloads into a new Server.
	chunksDatPath := filepath.Join(worldDir, "data", "chunks.dat")
	if _, err := os.Stat(chunksDatPath); err != nil {
		t.Fatalf("chunks.dat missing at %s: %v", chunksDatPath, err)
	}
	srv2, err := NewWithCache(cfg, world.NewCache(-1, world.GenerateFlat))
	if err != nil {
		t.Fatalf("NewWithCache(srv2): %v", err)
	}
	if !srv2.IsChunkForceLoaded(world.DimensionOverworld, -2, 7) {
		t.Fatal("expected force-loaded chunk (-2,7) to be restored from chunks.dat on startup")
	}
}

func TestIssue36TickLoopPhaseOrderOverloadMetricsWeatherSleepAndLightning(t *testing.T) {
	srvCfg := DefaultConfig()
	srvCfg.WorldDir = ""
	srv, err := NewWithCache(srvCfg, world.NewCache(-1, world.GenerateFlat))
	if err != nil {
		t.Fatalf("NewWithCache: %v", err)
	}
	rng := rand.New(rand.NewSource(12345))

	// 1. Step 10 server ticks and verify deterministic phase order and metrics.
	var snap TickMetricsSnapshot
	for i := 0; i < 10; i++ {
		snap = srv.StepServerTick(rng)
	}
	if snap.TotalTicks != 10 {
		t.Fatalf("TotalTicks = %d, want 10", snap.TotalTicks)
	}
	if !reflect.DeepEqual(snap.LastPhaseOrder, TickPhaseOrder) {
		t.Fatalf("LastPhaseOrder = %v, want %v", snap.LastPhaseOrder, TickPhaseOrder)
	}
	if snap.TPS <= 0 || snap.TPS > 20.0 {
		t.Fatalf("TPS = %f, want in (0, 20]", snap.TPS)
	}

	// 2. Overload handling: < 2000ms does not warn; >= 2000ms warns and skips behind/50ms ticks.
	if warned, skipped := srv.RecordTickOverload(1500 * time.Millisecond); warned || skipped != 0 {
		t.Fatalf("RecordTickOverload(1500ms) = (%v, %d), want (false, 0)", warned, skipped)
	}
	if warned, skipped := srv.RecordTickOverload(2500 * time.Millisecond); !warned || skipped != 50 {
		t.Fatalf("RecordTickOverload(2500ms) = (%v, %d), want (true, 50)", warned, skipped)
	}
	metrics := srv.TickMetrics()
	if metrics.OverloadWarnings != 1 || metrics.SkippedTicks != 50 {
		t.Fatalf("TickMetrics overload = (warnings=%d, skipped=%d), want (1, 50)", metrics.OverloadWarnings, metrics.SkippedTicks)
	}

	// 3. Sleep during night + thunderstorm advances dayTime to morning (24000) and clears weather.
	srv.SetWorldTime(1000, 14000) // Night
	srv.SetWeather(WeatherThunder, 6000)
	alice, err := srv.RegisterPlayer(Profile{UUID: OfflineUUID("Alice"), Name: "Alice"}, func(int32, []byte) error { return nil })
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	_ = srv.SetPlayerGameMode(alice, GameModeSurvival)
	if skipped := srv.SetPlayerSleeping(alice, true); !skipped {
		t.Fatal("expected single online survival player sleeping at night to skip to morning")
	}
	_, dayTime := srv.WorldTime()
	if dayTime != 24000 {
		t.Fatalf("dayTime after sleep skip = %d, want 24000", dayTime)
	}
	if srv.IsRaining() || srv.IsThundering() {
		t.Fatalf("expected weather to clear after sleep, got %+v", srv.Weather())
	}

	// 4. Thunderstorm chunk lightning strike & lightning rod redirection.
	srv.SetWeather(WeatherThunder, 6000)
	rodState, ok := world.BlockStateByName("minecraft:lightning_rod", map[string]string{
		"facing":      "up",
		"powered":     "false",
		"waterlogged": "false",
	})
	if !ok {
		t.Fatal("missing minecraft:lightning_rod block state")
	}
	srv.Chunks().SetBlock(12, 65, 12, rodState)
	boltID, strikePos, struck := srv.TickThunderstormChunkLightning(0, 0, nil)
	if !struck || boltID == 0 {
		t.Fatalf("TickThunderstormChunkLightning = (boltID=%d, pos=%v, struck=%v), want struck", boltID, strikePos, struck)
	}
	if int(strikePos[0]) != 12 || int(strikePos[2]) != 12 {
		t.Fatalf("lightning strikePos = %v, want redirected to lightning rod at (12.5, _, 12.5)", strikePos)
	}
}

func TestIssue36ServerPropertiesACLAndBans(t *testing.T) {
	dir := t.TempDir()
	propsPath := filepath.Join(dir, "server.properties")

	cfg := DefaultConfig()
	cfg.WorldDir = filepath.Join(dir, "world")
	cfg.SimulationDistance = 12
	cfg.Gamemode = "adventure"
	cfg.ForceGamemode = true
	cfg.PvP = false
	cfg.AllowFlight = true
	cfg.SpawnProtection = 8
	cfg.MaxWorldSize = 1000000
	cfg.MaxTickTime = 45000
	cfg.RateLimit = 250
	cfg.EnableRCON = true
	cfg.RCONPort = 25580
	cfg.RCONPassword = "secret-rcon"
	cfg.BroadcastRCONToOps = false
	cfg.EnableQuery = true
	cfg.QueryPort = 25581
	cfg.EnableStatus = true
	cfg.SyncChunkWrites = false
	cfg.GenerateStructures = false
	cfg.LevelType = "minecraft:flat"
	cfg.OpPermissionLevel = 3
	cfg.FunctionPermissionLevel = 3
	cfg.PlayerIdleTimeout = 15
	cfg.PreventProxyConnections = true

	if err := SavePropertiesFile(propsPath, cfg); err != nil {
		t.Fatalf("SavePropertiesFile: %v", err)
	}
	loadedCfg, err := LoadPropertiesFile(propsPath, DefaultConfig())
	if err != nil {
		t.Fatalf("LoadPropertiesFile: %v", err)
	}
	if loadedCfg != cfg {
		t.Fatalf("loaded server.properties mismatch:\ngot:  %+v\nwant: %+v", loadedCfg, cfg)
	}

	loadedCfg.OpsFile = filepath.Join(dir, "ops.json")
	loadedCfg.WhitelistFile = filepath.Join(dir, "whitelist.json")
	loadedCfg.BannedPlayersFile = filepath.Join(dir, "banned-players.json")
	loadedCfg.BannedIPsFile = filepath.Join(dir, "banned-ips.json")
	srv, err := NewWithCache(loadedCfg, world.NewCache(-1, world.GenerateFlat))
	if err != nil {
		t.Fatalf("NewWithCache: %v", err)
	}
	srv.SetWhitelistEnabled(true)

	// Whitelist & Ops persistence
	aliceProf := Profile{UUID: OfflineUUID("Alice"), Name: "Alice"}
	bobProf := Profile{UUID: OfflineUUID("Bob"), Name: "Bob"}
	if err := srv.AddWhitelist(aliceProf); err != nil {
		t.Fatalf("AddWhitelist: %v", err)
	}
	if _, err := srv.CheckLoginAccess(bobProf, "10.0.0.2"); err != ErrNotWhitelisted {
		t.Fatalf("CheckLoginAccess(unwhitelisted Bob) = %v, want ErrNotWhitelisted", err)
	}
	if err := srv.GrantOp(bobProf); err != nil {
		t.Fatalf("GrantOp(Bob): %v", err)
	}
	if _, err := srv.CheckLoginAccess(bobProf, "10.0.0.2"); err != nil {
		t.Fatalf("CheckLoginAccess(Op Bob) = %v, want nil (ops bypass whitelist)", err)
	}

	// Player ban & IP ban persistence
	if !srv.BanPlayer("Alice", "Griefing") {
		t.Fatal("BanPlayer returned false")
	}
	if _, err := srv.CheckLoginAccess(aliceProf, "10.0.0.1"); err != ErrPlayerBanned {
		t.Fatalf("CheckLoginAccess(banned Alice) = %v, want ErrPlayerBanned", err)
	}
	if !srv.BanIP("10.0.0.99", "Botnet") {
		t.Fatal("BanIP returned false")
	}
	if _, err := srv.CheckLoginAccess(bobProf, "10.0.0.99"); err != ErrIPBanned {
		t.Fatalf("CheckLoginAccess(banned IP) = %v, want ErrIPBanned", err)
	}
}
