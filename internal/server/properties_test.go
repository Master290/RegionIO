package server

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"regionio/internal/world"
)

func TestParsePropertiesAndRoundTrip(t *testing.T) {
	input := `
# Minecraft server properties
! another comment
server-port=25570
level-seed=987654321
view-distance=6
max-players=42
motd=Hello RegionIO\nLine 2
online-mode=true
white-list=true
`
	cfg, err := ParseProperties(strings.NewReader(input), DefaultConfig())
	if err != nil {
		t.Fatalf("ParseProperties failed: %v", err)
	}
	if cfg.Port != 25570 {
		t.Fatalf("Port = %d, want 25570", cfg.Port)
	}
	if cfg.WorldSeed != 987654321 {
		t.Fatalf("WorldSeed = %d, want 987654321", cfg.WorldSeed)
	}
	if cfg.MaxViewDistance != 6 {
		t.Fatalf("MaxViewDistance = %d, want 6", cfg.MaxViewDistance)
	}
	if cfg.MaxPlayers != 42 {
		t.Fatalf("MaxPlayers = %d, want 42", cfg.MaxPlayers)
	}
	if cfg.MOTD != "Hello RegionIO\nLine 2" {
		t.Fatalf("MOTD = %q, want %q", cfg.MOTD, "Hello RegionIO\nLine 2")
	}
	if !cfg.OnlineMode {
		t.Fatalf("OnlineMode = false, want true")
	}
	if !cfg.EnforceWhitelist {
		t.Fatalf("EnforceWhitelist = false, want true")
	}

	// Short key aliases (port, seed) and hex seed.
	aliasCfg, err := ParseProperties(strings.NewReader("port: 25580\nseed: 0x10\n"), DefaultConfig())
	if err != nil {
		t.Fatalf("ParseProperties aliases failed: %v", err)
	}
	if aliasCfg.Port != 25580 || aliasCfg.WorldSeed != 16 {
		t.Fatalf("alias config = (%d, %d), want (25580, 16)", aliasCfg.Port, aliasCfg.WorldSeed)
	}

	// Round-trip via file.
	dir := t.TempDir()
	propPath := filepath.Join(dir, "server.properties")
	if err := SavePropertiesFile(propPath, cfg); err != nil {
		t.Fatalf("SavePropertiesFile: %v", err)
	}
	loaded, err := LoadPropertiesFile(propPath, DefaultConfig())
	if err != nil {
		t.Fatalf("LoadPropertiesFile: %v", err)
	}
	if loaded.Port != cfg.Port || loaded.WorldSeed != cfg.WorldSeed ||
		loaded.MaxViewDistance != cfg.MaxViewDistance || loaded.MaxPlayers != cfg.MaxPlayers ||
		loaded.MOTD != cfg.MOTD || loaded.OnlineMode != cfg.OnlineMode ||
		loaded.EnforceWhitelist != cfg.EnforceWhitelist {
		t.Fatalf("round-trip mismatch: got %+v, want %+v", loaded, cfg)
	}

	// Missing file returns base without error.
	def := DefaultConfig()
	missing, err := LoadPropertiesFile(filepath.Join(dir, "nonexistent.properties"), def)
	if err != nil || missing != def {
		t.Fatalf("missing file returned (%+v, %v), want default", missing, err)
	}

	// Invalid values return error.
	for _, bad := range []string{
		"port=99999",
		"view-distance=1",
		"max-players=0",
		"online-mode=notabool",
		"malformed_line_without_equals",
	} {
		if _, err := ParseProperties(strings.NewReader(bad), DefaultConfig()); err == nil {
			t.Fatalf("expected error for %q", bad)
		}
	}
}

func TestOpsAndWhitelistJSONAndServerAccess(t *testing.T) {
	dir := t.TempDir()
	opsPath := filepath.Join(dir, "ops.json")
	wlPath := filepath.Join(dir, "whitelist.json")

	opsEntries, err := ParseOpsJSON([]byte(`[
		{"name": "Admin", "level": 4, "bypassesPlayerLimit": true},
		{"uuid": "00000000-0000-0000-0000-000000000001", "name": "Mod", "level": 3}
	]`))
	if err != nil {
		t.Fatalf("ParseOpsJSON: %v", err)
	}
	if len(opsEntries) != 2 || opsEntries[0].Name != "Admin" || !opsEntries[0].BypassesPlayerLimit {
		t.Fatalf("unexpected opsEntries: %+v", opsEntries)
	}
	if err := SaveOpsFile(opsPath, opsEntries); err != nil {
		t.Fatalf("SaveOpsFile: %v", err)
	}

	wlEntries, err := ParseWhitelistJSON([]byte(`["AllowedUser"]`))
	if err != nil {
		t.Fatalf("ParseWhitelistJSON string array: %v", err)
	}
	if err := SaveWhitelistFile(wlPath, wlEntries); err != nil {
		t.Fatalf("SaveWhitelistFile: %v", err)
	}

	cfg := DefaultConfig()
	cfg.WorldDir = ""
	cfg.MaxPlayers = 1
	cfg.EnforceWhitelist = true
	cfg.OpsFile = opsPath
	cfg.WhitelistFile = wlPath

	srv, err := NewWithCache(cfg, world.NewCache(-1, world.GenerateFlat))
	if err != nil {
		t.Fatal(err)
	}
	if !srv.IsOp("admin") || !srv.IsOp("MOD") || srv.IsOp("stranger") {
		t.Fatalf("unexpected IsOp results")
	}
	if !srv.IsWhitelisted("alloweduser") || !srv.IsWhitelisted("admin") || srv.IsWhitelisted("stranger") {
		t.Fatalf("unexpected IsWhitelisted results")
	}

	// Non-whitelisted player is rejected.
	if _, err := srv.RegisterPlayer(Profile{Name: "Stranger", UUID: OfflineUUID("Stranger")}, nil); !errors.Is(err, ErrNotWhitelisted) {
		t.Fatalf("RegisterPlayer(Stranger) = %v, want ErrNotWhitelisted", err)
	}

	// Whitelisted player joins (fills MaxPlayers = 1).
	if _, err := srv.RegisterPlayer(Profile{Name: "AllowedUser", UUID: OfflineUUID("AllowedUser")}, nil); err != nil {
		t.Fatalf("RegisterPlayer(AllowedUser): %v", err)
	}

	// Op without bypassesPlayerLimit is rejected when full, while Admin (bypassesPlayerLimit=true) succeeds.
	if _, err := srv.RegisterPlayer(Profile{Name: "Mod", UUID: OfflineUUID("Mod")}, nil); !errors.Is(err, ErrServerFull) {
		t.Fatalf("RegisterPlayer(Mod) when full = %v, want ErrServerFull", err)
	}
	if _, err := srv.RegisterPlayer(Profile{Name: "Admin", UUID: OfflineUUID("Admin")}, nil); err != nil {
		t.Fatalf("RegisterPlayer(Admin with bypass) failed: %v", err)
	}
}

func TestServerRequestStopSavesWorld(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()
	cfg.WorldDir = dir
	cfg.WorldSeed = 42
	srv, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if srv.Store() != nil {
			_ = srv.Store().Close()
		}
	}()

	srv.SetWorldTime(1200, 6000)
	stopped := false
	srv.SetStopFunc(func() {
		stopped = true
	})
	if err := srv.RequestStop(); err != nil {
		t.Fatalf("RequestStop: %v", err)
	}
	if !stopped {
		t.Fatal("SetStopFunc callback was not called")
	}
	select {
	case <-srv.StopCh():
	default:
		t.Fatal("StopCh was not closed")
	}
	gt, dt := srv.Store().WorldTime()
	if gt != 1200 || dt != 6000 {
		t.Fatalf("saved world time = (%d, %d), want (1200, 6000)", gt, dt)
	}
}
