package network

import (
	"context"
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
			case ArgParserVec3, ArgParserGameMode:
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

	for _, want := range []string{"tp", "gamemode", "time", "query", "weather", "clear", "rain", "thunder", "seed", "save-all", "stop", "list", "help"} {
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
