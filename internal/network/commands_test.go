package network

import (
	"context"
	"math"
	"strings"
	"sync"
	"testing"

	"regionio/internal/protocol"
	"regionio/internal/server"
	"regionio/internal/world"
)

func TestDefaultCommandTreeWireEncoding(t *testing.T) {
	raw := EncodeCommandsPacket()
	r := protocol.NewReader(raw)

	count, err := r.VarInt()
	if err != nil {
		t.Fatalf("read node count: %v", err)
	}
	if count <= 0 {
		t.Fatalf("node count = %d, want > 0", count)
	}

	literals := make(map[string]bool)
	for i := int32(0); i < count; i++ {
		flags, err := r.ReadByte()
		if err != nil {
			t.Fatalf("node %d flags: %v", i, err)
		}
		childCount, err := r.VarInt()
		if err != nil {
			t.Fatalf("node %d childCount: %v", i, err)
		}
		for c := int32(0); c < childCount; c++ {
			childIdx, err := r.VarInt()
			if err != nil {
				t.Fatalf("node %d child %d: %v", i, c, err)
			}
			if childIdx < 0 || childIdx >= count {
				t.Fatalf("node %d child index %d out of range [0,%d)", i, childIdx, count)
			}
		}
		if flags&CommandFlagRedirect != 0 {
			if _, err := r.VarInt(); err != nil {
				t.Fatalf("node %d redirect: %v", i, err)
			}
		}
		switch flags & CommandNodeTypeMask {
		case CommandNodeRoot:
			if i != 0 {
				t.Fatalf("unexpected root node at index %d", i)
			}
		case CommandNodeLiteral:
			name, err := r.String()
			if err != nil {
				t.Fatalf("node %d literal name: %v", i, err)
			}
			literals[name] = true
		case CommandNodeArgument:
			if _, err := r.String(); err != nil {
				t.Fatalf("node %d arg name: %v", i, err)
			}
			parserID, err := r.VarInt()
			if err != nil {
				t.Fatalf("node %d parserID: %v", i, err)
			}
			switch parserID {
			case ArgParserEntity:
				if _, err := r.ReadByte(); err != nil {
					t.Fatalf("node %d entity flags: %v", i, err)
				}
			case ArgParserTime:
				if _, err := r.Int32(); err != nil {
					t.Fatalf("node %d time min: %v", i, err)
				}
			case ArgParserString:
				if _, err := r.VarInt(); err != nil {
					t.Fatalf("node %d string mode: %v", i, err)
				}
			case ArgParserVec3, ArgParserRotation, ArgParserGameMode:
				// singleton argument info: 0 property bytes
			default:
				t.Fatalf("node %d unexpected parserID %d", i, parserID)
			}
			if flags&CommandFlagCustomSuggestions != 0 {
				if _, err := r.String(); err != nil {
					t.Fatalf("node %d suggestion id: %v", i, err)
				}
			}
		}
	}

	rootIdx, err := r.VarInt()
	if err != nil {
		t.Fatalf("read rootIndex: %v", err)
	}
	if rootIdx != 0 {
		t.Fatalf("rootIndex = %d, want 0", rootIdx)
	}
	if r.Remaining() != 0 {
		t.Fatalf("trailing %d bytes in commands packet", r.Remaining())
	}

	for _, want := range []string{"tp", "teleport", "execute", "gamemode", "time", "query", "weather", "clear", "rain", "thunder", "seed", "save-all", "stop", "list", "help"} {
		if !literals[want] {
			t.Errorf("command tree missing literal %q", want)
		}
	}
}

func TestParseCommandHelpers(t *testing.T) {
	cmd, err := ParseCommandLine("  /tp Alice ~10 64 ~-5  ")
	if err != nil {
		t.Fatalf("ParseCommandLine: %v", err)
	}
	if cmd.Name != "tp" || len(cmd.Args) != 4 || cmd.Args[0] != "Alice" || cmd.Args[1] != "~10" {
		t.Fatalf("unexpected ParsedCommand: %+v", cmd)
	}

	c1, err := ParseCoordArg("125.5")
	if err != nil || c1.Relative || c1.Resolve(10) != 125.5 {
		t.Fatalf("ParseCoordArg(125.5) = %+v, %v", c1, err)
	}
	c2, err := ParseCoordArg("~-4.5")
	if err != nil || !c2.Relative || c2.Resolve(10) != 5.5 {
		t.Fatalf("ParseCoordArg(~-4.5) = %+v, %v", c2, err)
	}
	c3, err := ParseCoordArg("~")
	if err != nil || !c3.Relative || c3.Resolve(42) != 42 {
		t.Fatalf("ParseCoordArg(~) = %+v, %v", c3, err)
	}

	for raw, want := range map[string]uint8{
		"survival":  server.GameModeSurvival,
		"0":         server.GameModeSurvival,
		"creative":  server.GameModeCreative,
		"c":         server.GameModeCreative,
		"adventure": server.GameModeAdventure,
		"spectator": server.GameModeSpectator,
		"3":         server.GameModeSpectator,
	} {
		got, _, err := ParseGameMode(raw)
		if err != nil || got != want {
			t.Fatalf("ParseGameMode(%q) = (%d, %v), want %d", raw, got, err, want)
		}
	}

	for raw, want := range map[string]int64{
		"day":      1000,
		"noon":     6000,
		"night":    13000,
		"midnight": 18000,
		"500":      500,
		"10s":      200,
		"0.5d":     12000,
		"40t":      40,
	} {
		got, err := ParseTimeValue(raw, true)
		if err != nil || got != want {
			t.Fatalf("ParseTimeValue(%q) = (%d, %v), want %d", raw, got, err, want)
		}
	}
}

func TestExecuteCommandsAndConsole(t *testing.T) {
	cfg := server.DefaultConfig()
	cfg.WorldDir = ""
	cfg.WorldSeed = -778899
	cfg.MaxPlayers = 10
	srv, err := server.NewWithCache(cfg, world.NewCache(-1, world.GenerateFlat))
	if err != nil {
		t.Fatal(err)
	}

	type sentPkt struct {
		id   int32
		body []byte
	}
	var mu sync.Mutex
	var alicePkts []sentPkt
	alice, err := srv.RegisterPlayer(server.Profile{Name: "Alice", UUID: server.OfflineUUID("Alice")}, func(id int32, body []byte) error {
		mu.Lock()
		alicePkts = append(alicePkts, sentPkt{id: id, body: append([]byte(nil), body...)})
		mu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	bob, err := srv.RegisterPlayer(server.Profile{Name: "Bob", UUID: server.OfflineUUID("Bob")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	srv.SetPlayerTransform(alice, 10, 70, 20, 0, 0, true)
	srv.SetPlayerTransform(bob, 100, 80, -50, 90, 0, true)

	// /seed
	if got := ExecuteCommand(srv, alice, "/seed"); got != "Seed: [-778899]" {
		t.Fatalf("/seed = %q, want %q", got, "Seed: [-778899]")
	}

	// /list
	if got := ExecuteCommand(srv, alice, "/list"); !strings.Contains(got, "There are 2 of a max of 10 players online: Alice, Bob") {
		t.Fatalf("/list = %q", got)
	}

	// /help
	if got := ExecuteCommand(srv, alice, "/help"); !strings.Contains(got, "/tp") || !strings.Contains(got, "/gamemode") {
		t.Fatalf("/help = %q", got)
	}

	// /tp relative coordinates
	if got := ExecuteCommand(srv, alice, "/tp ~5 ~10 ~-5"); !strings.Contains(got, "15.00, 80.00, 15.00") {
		t.Fatalf("/tp relative = %q", got)
	}
	if snap := alice.Snapshot(); snap.X != 15 || snap.Y != 80 || snap.Z != 15 {
		t.Fatalf("alice position after relative tp = (%v, %v, %v), want (15, 80, 15)", snap.X, snap.Y, snap.Z)
	}

	// /tp to another player
	if got := ExecuteCommand(srv, alice, "/tp Bob"); !strings.Contains(got, "Teleported Alice to Bob") {
		t.Fatalf("/tp Bob = %q", got)
	}
	if snap := alice.Snapshot(); snap.X != 100 || snap.Y != 80 || snap.Z != -50 {
		t.Fatalf("alice position after tp Bob = (%v, %v, %v), want (100, 80, -50)", snap.X, snap.Y, snap.Z)
	}

	// /gamemode survival
	if got := ExecuteCommand(srv, alice, "/gamemode survival"); got != "Set own game mode to Survival Mode" {
		t.Fatalf("/gamemode survival = %q", got)
	}
	if snap := alice.Snapshot(); snap.GameMode != server.GameModeSurvival {
		t.Fatalf("alice GameMode = %d, want %d", snap.GameMode, server.GameModeSurvival)
	}

	// /time set noon and /time add 1000
	if got := ExecuteCommand(srv, alice, "/time set noon"); got != "Set the time to 6000" {
		t.Fatalf("/time set noon = %q", got)
	}
	if got := ExecuteCommand(srv, alice, "/time add 1000"); !strings.Contains(got, "7000") {
		t.Fatalf("/time add 1000 = %q", got)
	}
	if _, dt := srv.WorldTime(); dt != 7000 {
		t.Fatalf("world dayTime = %d, want 7000", dt)
	}
	if got := ExecuteCommand(srv, alice, "/time query daytime"); got != "The time is 7000" {
		t.Fatalf("/time query daytime = %q, want %q", got, "The time is 7000")
	}

	// /weather thunder, rain, clear
	if got := ExecuteCommand(srv, alice, "/weather thunder 10s"); got != "Set the weather to rain & thunder" {
		t.Fatalf("/weather thunder 10s = %q", got)
	}
	if !srv.IsRaining() || !srv.IsThundering() || srv.Weather().Kind() != server.WeatherThunder {
		t.Fatalf("weather after /weather thunder = %+v, want rain+thunder", srv.Weather())
	}
	if got := ExecuteCommand(srv, alice, "/weather clear"); got != "Set the weather to clear" {
		t.Fatalf("/weather clear = %q", got)
	}
	if srv.IsRaining() || srv.IsThundering() || srv.Weather().Kind() != server.WeatherClear {
		t.Fatalf("weather after /weather clear = %+v, want clear", srv.Weather())
	}

	// /gamerule doDaylightCycle & doWeatherCycle
	if got := ExecuteCommand(srv, alice, "/gamerule doDaylightCycle false"); got != "Gamerule doDaylightCycle is now set to: false" {
		t.Fatalf("/gamerule doDaylightCycle false = %q", got)
	}
	if srv.DaylightCycle() {
		t.Fatal("expected DaylightCycle() == false")
	}
	if got := ExecuteCommand(srv, alice, "/gamerule doDaylightCycle true"); got != "Gamerule doDaylightCycle is now set to: true" {
		t.Fatalf("/gamerule doDaylightCycle true = %q", got)
	}
	if !srv.DaylightCycle() {
		t.Fatal("expected DaylightCycle() == true")
	}
	if got := ExecuteCommand(srv, alice, "/gamerule doWeatherCycle false"); got != "Gamerule doWeatherCycle is now set to: false" {
		t.Fatalf("/gamerule doWeatherCycle false = %q", got)
	}
	if srv.WeatherCycle() {
		t.Fatal("expected WeatherCycle() == false")
	}
	if got := ExecuteCommand(srv, alice, "/gamerule doWeatherCycle true"); got != "Gamerule doWeatherCycle is now set to: true" {
		t.Fatalf("/gamerule doWeatherCycle true = %q", got)
	}

	// /spawnpoint & /setworldspawn
	if got := ExecuteCommand(srv, alice, "/spawnpoint ~ ~2 ~ 90"); !strings.Contains(got, "Set the spawn point for Alice to 100, 82, -50") {
		t.Fatalf("/spawnpoint = %q", got)
	}
	if snap := alice.Snapshot(); !snap.HasSpawn || !snap.SpawnForced || snap.SpawnX != 100 || snap.SpawnY != 82 || snap.SpawnZ != -50 {
		t.Fatalf("alice spawn point after /spawnpoint = %+v", snap)
	}
	if got := ExecuteCommand(srv, alice, "/setworldspawn 12 72 -8 45"); !strings.Contains(got, "Set the world spawn point to 12, 72, -8") {
		t.Fatalf("/setworldspawn = %q", got)
	}
	if wx, wy, wz, wAngle := srv.WorldSpawn(); wx != 12 || wy != 72 || wz != -8 || wAngle != 45 {
		t.Fatalf("world spawn after /setworldspawn = (%d, %d, %d, %v), want (12, 72, -8, 45)", wx, wy, wz, wAngle)
	}

	// /save-all
	if got := ExecuteCommand(srv, alice, "/save-all"); got != "Saved the game" {
		t.Fatalf("/save-all = %q", got)
	}

	// Console stdin execution via RunConsole (time set + stop).
	consoleIn := strings.NewReader("time set midnight\nstop\n")
	RunConsole(context.Background(), consoleIn, srv, nil)
	if _, dt := srv.WorldTime(); dt != 18000 {
		t.Fatalf("dayTime after console time set midnight = %d, want 18000", dt)
	}
	select {
	case <-srv.StopCh():
	default:
		t.Fatal("console stop did not close srv.StopCh()")
	}

	// Verify Alice received teleport, gamemode, time, and system chat packets.
	mu.Lock()
	defer mu.Unlock()
	seenIDs := make(map[int32]bool)
	for _, p := range alicePkts {
		seenIDs[p.id] = true
	}
	for _, wantID := range []int32{
		protocol.PlayPlayerPosition,
		protocol.PlayGameEvent,
		protocol.PlayAbilities,
		protocol.PlaySetTime,
		protocol.PlaySystemChat,
	} {
		if !seenIDs[wantID] {
			t.Errorf("Alice did not receive expected packet ID 0x%02x", wantID)
		}
	}
}

func TestTeleportPolishAndExecuteCommand(t *testing.T) {
	cfg := server.DefaultConfig()
	cfg.WorldDir = ""
	cfg.WorldSeed = 424242
	cfg.MaxPlayers = 10
	srv, err := server.NewWithCache(cfg, world.NewCache(-1, world.GenerateFlat))
	if err != nil {
		t.Fatal(err)
	}

	alice, err := srv.RegisterPlayer(server.Profile{Name: "Alice", UUID: server.OfflineUUID("Alice")}, func(id int32, body []byte) error {
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	bob, err := srv.RegisterPlayer(server.Profile{Name: "Bob", UUID: server.OfflineUUID("Bob")}, func(id int32, body []byte) error {
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	srv.SetPlayerTransform(alice, 0, 70, 0, 0, 0, true)   // facing south (+Z)
	srv.SetPlayerTransform(bob, 20, 70, 0, -90, 0, true) // at +X

	// 1. Local ^left ^up ^forwards coordinates: facing south (yaw=0, pitch=0),
	// ^0 ^2 ^10 moves +10 along +Z and +2 along +Y -> (0, 72, 10).
	if got := ExecuteCommand(srv, alice, "/teleport @s ^0 ^2 ^10"); !strings.Contains(got, "0.00, 72.00, 10.00") {
		t.Fatalf("/teleport local ^ coords = %q", got)
	}
	if snap := alice.Snapshot(); math.Abs(snap.X-0) > 1e-4 || math.Abs(snap.Y-72) > 1e-4 || math.Abs(snap.Z-10) > 1e-4 {
		t.Fatalf("alice pos after local ^ tp = (%v, %v, %v), want (0, 72, 10)", snap.X, snap.Y, snap.Z)
	}

	// 2. /tp with explicit rotation (<yaw> <pitch>) and relative rotation (~yaw ~pitch).
	if got := ExecuteCommand(srv, alice, "/tp @s 0 70 0 90 -15"); !strings.Contains(got, "0.00, 70.00, 0.00") {
		t.Fatalf("/tp with rotation = %q", got)
	}
	if snap := alice.Snapshot(); snap.Yaw != 90 || snap.Pitch != -15 {
		t.Fatalf("alice rot = (%v, %v), want (90, -15)", snap.Yaw, snap.Pitch)
	}
	if got := ExecuteCommand(srv, alice, "/tp @s ~ ~ ~ ~-45 ~15"); !strings.Contains(got, "0.00, 70.00, 0.00") {
		t.Fatalf("/tp with relative rotation = %q", got)
	}
	if snap := alice.Snapshot(); snap.Yaw != 45 || snap.Pitch != 0 {
		t.Fatalf("alice relative rot = (%v, %v), want (45, 0)", snap.Yaw, snap.Pitch)
	}

	// 3. /tp facing <x> <y> <z> and /tp facing entity <target> [eyes|feet].
	// Looking from (0, 70, 0) toward (10, 70, 0) (+X, east) gives yaw = -90, pitch = 0.
	if got := ExecuteCommand(srv, alice, "/tp @s 0 70 0 facing 10 70 0"); !strings.Contains(got, "Teleported Alice") {
		t.Fatalf("/tp facing coords = %q", got)
	}
	if snap := alice.Snapshot(); math.Abs(float64(snap.Yaw - -90)) > 1e-3 || math.Abs(float64(snap.Pitch)) > 1e-3 {
		t.Fatalf("alice facing +X rot = (%v, %v), want (-90, 0)", snap.Yaw, snap.Pitch)
	}
	// Looking from (0, 70, 0) toward Bob at (20, 70, 0) feet gives yaw = -90, pitch = 0.
	if got := ExecuteCommand(srv, alice, "/tp @s 0 70 0 facing entity Bob feet"); !strings.Contains(got, "Teleported Alice") {
		t.Fatalf("/tp facing entity Bob feet = %q", got)
	}
	if snap := alice.Snapshot(); math.Abs(float64(snap.Yaw - -90)) > 1e-3 || math.Abs(float64(snap.Pitch)) > 1e-3 {
		t.Fatalf("alice facing entity Bob feet rot = (%v, %v), want (-90, 0)", snap.Yaw, snap.Pitch)
	}

	// 4. Multi-target selector @a[distance=..30] and non-player entity @e[type=zombie].
	zombieID := srv.Entities().Add(&world.Entity{
		TypeName:  "minecraft:zombie",
		Dimension: world.DimensionOverworld,
		X:         5,
		Y:         70,
		Z:         5,
	})
	if got := ExecuteCommand(srv, alice, "/tp @e[type=zombie,distance=..20] 12 71 14"); !strings.Contains(got, "12.00, 71.00, 14.00") {
		t.Fatalf("/tp zombie = %q", got)
	}
	if z := srv.Entities().Get(zombieID); z == nil || z.X != 12 || z.Y != 71 || z.Z != 14 {
		t.Fatalf("zombie after /tp = %+v", z)
	}

	// 5. /execute as, at, positioned, rotated, align, anchored, in, if/unless, run.
	if got := ExecuteCommand(srv, alice, "/execute as Bob at @s positioned ~5 ~2 ~-3 align xyz run tp @s ~ ~ ~"); !strings.Contains(got, "25.00, 72.00, -3.00") {
		t.Fatalf("/execute as Bob at @s positioned align run tp = %q", got)
	}
	if snap := bob.Snapshot(); snap.X != 25 || snap.Y != 72 || snap.Z != -3 {
		t.Fatalf("bob pos after /execute = (%v, %v, %v), want (25, 72, -3)", snap.X, snap.Y, snap.Z)
	}

	// /execute if block & unless block
	stoneState := world.StateStone
	srv.Chunks().SetBlock(0, 69, 0, stoneState)
	if got := ExecuteCommand(srv, alice, "/execute if block 0 69 0 minecraft:stone"); !strings.Contains(got, "Test passed") {
		t.Fatalf("/execute if block stone = %q", got)
	}
	if got := ExecuteCommand(srv, alice, "/execute unless block 0 69 0 minecraft:stone run tp @s 99 99 99"); got != "" {
		t.Fatalf("/execute unless block stone should not run, got %q", got)
	}

	// /execute if blocks (region comparison)
	srv.Chunks().SetBlock(2, 69, 2, stoneState)
	if got := ExecuteCommand(srv, alice, "/execute if blocks 0 69 0 0 69 0 2 69 2 all"); !strings.Contains(got, "Test passed") {
		t.Fatalf("/execute if blocks = %q", got)
	}

	// /execute if entity & if dimension
	if got := ExecuteCommand(srv, alice, "/execute if entity @e[type=minecraft:zombie] if dimension minecraft:overworld"); !strings.Contains(got, "Test passed") {
		t.Fatalf("/execute if entity + dimension = %q", got)
	}

	// /execute if score (matches and operator comparison)
	ExecuteCommand(srv, alice, "/scoreboard objectives add pts dummy")
	_, _ = srv.SetScore("Alice", "pts", 15)
	_, _ = srv.SetScore("Bob", "pts", 10)
	if got := ExecuteCommand(srv, alice, "/execute if score Alice pts matches 10..20 if score Alice pts > Bob pts"); !strings.Contains(got, "Test passed") {
		t.Fatalf("/execute if score = %q", got)
	}
	if got := ExecuteCommand(srv, alice, "/execute if score Alice pts < Bob pts"); got != "Test failed" {
		t.Fatalf("/execute if score false = %q, want Test failed", got)
	}

	// /execute in minecraft:the_nether run tp @s 8 64 8 (cross-dimension teleport!)
	if got := ExecuteCommand(srv, alice, "/execute in minecraft:the_nether run tp @s 8 64 8"); !strings.Contains(got, "8.00, 64.00, 8.00") {
		t.Fatalf("/execute in the_nether run tp = %q", got)
	}
	if alice.Dimension() != world.DimensionNether {
		t.Fatalf("alice dimension = %q, want %q", alice.Dimension(), world.DimensionNether)
	}
	if snap := alice.Snapshot(); snap.X != 8 || snap.Y != 64 || snap.Z != 8 {
		t.Fatalf("alice nether pos = (%v, %v, %v), want (8, 64, 8)", snap.X, snap.Y, snap.Z)
	}
}

func TestWorldBlockAndEntityCommandsBatch1(t *testing.T) {
	srv, err := server.NewWithCache(server.DefaultConfig(), world.NewCache(256, world.GenerateFlat))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.RequestStop() }()

	alice, err := srv.RegisterPlayer(server.Profile{Name: "Alice", UUID: server.OfflineUUID("Alice")}, func(id int32, body []byte) error {
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	srv.SetPlayerTransform(alice, 0, 70, 0, 0, 0, true)

	// 1. /setblock (replace, keep, destroy)
	if got := ExecuteCommand(srv, alice, "/setblock 5 70 5 minecraft:obsidian"); !strings.Contains(got, "Changed the block at 5, 70, 5") {
		t.Fatalf("/setblock obsidian = %q", got)
	}
	if world.BlockName(srv.Chunks().GetBlock(5, 70, 5)) != "minecraft:obsidian" {
		t.Fatalf("block at (5,70,5) = %s, want minecraft:obsidian", world.BlockName(srv.Chunks().GetBlock(5, 70, 5)))
	}
	if got := ExecuteCommand(srv, alice, "/setblock 5 70 5 minecraft:stone keep"); got != "Could not set the block" {
		t.Fatalf("/setblock keep on non-air = %q, want 'Could not set the block'", got)
	}

	// 2. /fill (replace, hollow, outline, replace with filter)
	if got := ExecuteCommand(srv, alice, "/fill 10 70 10 12 72 12 minecraft:stone hollow"); !strings.Contains(got, "Successfully filled") {
		t.Fatalf("/fill hollow = %q", got)
	}
	if srv.Chunks().GetBlock(11, 71, 11) != world.StateAir {
		t.Fatalf("hollow interior should be air, got %d", srv.Chunks().GetBlock(11, 71, 11))
	}
	if world.BlockName(srv.Chunks().GetBlock(10, 70, 10)) != "minecraft:stone" {
		t.Fatalf("hollow shell should be stone, got %s", world.BlockName(srv.Chunks().GetBlock(10, 70, 10)))
	}
	if got := ExecuteCommand(srv, alice, "/fill 10 70 10 12 72 12 minecraft:obsidian replace minecraft:stone"); !strings.Contains(got, "26") {
		t.Fatalf("/fill replace filter = %q", got)
	}

	// 3. /clone (replace, masked, move)
	if got := ExecuteCommand(srv, alice, "/clone 10 70 10 12 72 12 20 70 20 masked move"); !strings.Contains(got, "26") {
		t.Fatalf("/clone masked move = %q", got)
	}
	if world.BlockName(srv.Chunks().GetBlock(20, 70, 20)) != "minecraft:obsidian" {
		t.Fatalf("cloned destination (20,70,20) = %s, want minecraft:obsidian", world.BlockName(srv.Chunks().GetBlock(20, 70, 20)))
	}
	if srv.Chunks().GetBlock(10, 70, 10) != world.StateAir {
		t.Fatalf("moved source (10,70,10) should be air, got %d", srv.Chunks().GetBlock(10, 70, 10))
	}

	// 4. /fillbiome
	if got := ExecuteCommand(srv, alice, "/fillbiome 0 64 0 7 67 7 minecraft:nether_wastes"); !strings.Contains(got, "biome cell(s) set") {
		t.Fatalf("/fillbiome = %q", got)
	}
	if b := srv.Chunks().GetBiome(0, 64, 0); b != world.BiomeNetherWastes {
		t.Fatalf("GetBiome(0,64,0) = %d, want %d", b, world.BiomeNetherWastes)
	}

	// 5. /xp and /experience (add, set, query)
	if got := ExecuteCommand(srv, alice, "/xp add @s 5 levels"); !strings.Contains(got, "5 experience levels") {
		t.Fatalf("/xp add levels = %q", got)
	}
	if got := ExecuteCommand(srv, alice, "/experience query @s levels"); !strings.Contains(got, "5 experience levels") {
		t.Fatalf("/experience query levels = %q", got)
	}
	if got := ExecuteCommand(srv, alice, "/xp set @s 20 points"); !strings.Contains(got, "20 experience points") {
		t.Fatalf("/xp set points = %q", got)
	}
	if alice.TotalExperience() != 20 {
		t.Fatalf("alice TotalExperience = %d, want 20", alice.TotalExperience())
	}

	// 6. /summon, /damage, and /kill
	if got := ExecuteCommand(srv, alice, "/summon minecraft:zombie 30 70 30"); !strings.Contains(got, "Summoned new minecraft:zombie") {
		t.Fatalf("/summon zombie = %q", got)
	}
	if got := ExecuteCommand(srv, alice, "/damage @e[type=minecraft:zombie,limit=1] 5 generic"); !strings.Contains(got, "Applied 5.0 damage") {
		t.Fatalf("/damage zombie = %q", got)
	}
	if got := ExecuteCommand(srv, alice, "/kill @e[type=minecraft:zombie]"); !strings.Contains(got, "Killed") {
		t.Fatalf("/kill zombie = %q", got)
	}
}


