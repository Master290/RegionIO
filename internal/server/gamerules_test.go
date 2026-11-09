package server

import (
	"strings"
	"testing"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

func TestDifficultyParseAndPackets(t *testing.T) {
	cases := []struct {
		in   string
		want Difficulty
		ok   bool
	}{
		{"peaceful", DifficultyPeaceful, true},
		{"p", DifficultyPeaceful, true},
		{"0", DifficultyPeaceful, true},
		{"easy", DifficultyEasy, true},
		{"E", DifficultyEasy, true},
		{"1", DifficultyEasy, true},
		{"normal", DifficultyNormal, true},
		{"n", DifficultyNormal, true},
		{"2", DifficultyNormal, true},
		{"hard", DifficultyHard, true},
		{"H", DifficultyHard, true},
		{"3", DifficultyHard, true},
		{"impossible", DifficultyNormal, false},
	}
	for _, tc := range cases {
		got, ok := ParseDifficulty(tc.in)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Errorf("ParseDifficulty(%q) = (%v, %v), want (%v, %v)", tc.in, got, ok, tc.want, tc.ok)
		}
	}

	if DifficultyHard.String() != "hard" || DifficultyHard.DisplayName() != "Hard" {
		t.Fatalf("unexpected String/DisplayName for Hard: %q / %q", DifficultyHard.String(), DifficultyHard.DisplayName())
	}
	if DifficultyPeaceful.TranslationKey() != "options.difficulty.peaceful" {
		t.Fatalf("unexpected TranslationKey: %q", DifficultyPeaceful.TranslationKey())
	}

	cb := EncodeChangeDifficultyPacket(DifficultyHard, true)
	d, locked, err := DecodeChangeDifficultyPacket(protocol.NewReader(cb))
	if err != nil || d != DifficultyHard || !locked {
		t.Fatalf("DecodeChangeDifficultyPacket = (%v, %v, %v), want (Hard, true, nil)", d, locked, err)
	}

	sb := EncodeChangeDifficultySBPacket(DifficultyEasy)
	sbDiff, err := DecodeChangeDifficultySBPacket(protocol.NewReader(sb))
	if err != nil || sbDiff != DifficultyEasy {
		t.Fatalf("DecodeChangeDifficultySBPacket = (%v, %v), want (Easy, nil)", sbDiff, err)
	}

	lockSB := EncodeLockDifficultySBPacket(true)
	sbLocked, err := DecodeLockDifficultySBPacket(protocol.NewReader(lockSB))
	if err != nil || !sbLocked {
		t.Fatalf("DecodeLockDifficultySBPacket = (%v, %v), want (true, nil)", sbLocked, err)
	}
}

func newGameRulesTestServer(cfg Config) *Server {
	return newServerState(cfg, nil, nil, world.NewEntityManager())
}

func TestDifficultyCommandAndPeacefulDespawn(t *testing.T) {
	srv := newGameRulesTestServer(DefaultConfig())

	var gotDifficultyPkt []byte
	admin, err := srv.RegisterPlayer(Profile{UUID: [16]byte{1}, Name: "Admin"}, func(id int32, body []byte) error {
		if id == PlayChangeDifficultyCB {
			gotDifficultyPkt = append([]byte(nil), body...)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	_ = srv.GrantOp(admin.Profile)

	if reply := srv.HandleDifficultyCommand(admin, nil); reply != "The difficulty is Normal" {
		t.Fatalf("expected Normal difficulty, got %q", reply)
	}

	// Spawn a hostile mob (zombie) and a passive mob (pig).
	zombieID := srv.Entities().Add(&world.Entity{TypeName: "minecraft:zombie", X: 10, Y: 64, Z: 10})
	pigID := srv.Entities().Add(&world.Entity{TypeName: "minecraft:pig", X: 12, Y: 64, Z: 12})
	if zombieID == 0 || pigID == 0 {
		t.Fatalf("expected non-zero mob IDs, got zombie=%d pig=%d", zombieID, pigID)
	}

	reply := srv.HandleDifficultyCommand(admin, []string{"peaceful"})
	if reply != "The difficulty has been set to Peaceful" {
		t.Fatalf("unexpected reply: %q", reply)
	}
	if srv.Difficulty() != DifficultyPeaceful {
		t.Fatalf("Difficulty() = %v, want Peaceful", srv.Difficulty())
	}
	if len(gotDifficultyPkt) != 2 || gotDifficultyPkt[0] != byte(DifficultyPeaceful) {
		t.Fatalf("expected broadcast difficulty packet for Peaceful, got %v", gotDifficultyPkt)
	}
	if srv.Entities().Get(zombieID) != nil {
		t.Fatalf("expected hostile zombie to be despawned on Peaceful")
	}
	if srv.Entities().Get(pigID) == nil {
		t.Fatalf("expected passive pig to remain on Peaceful")
	}

	// Repeating the same difficulty reports unchanged.
	if reply := srv.HandleDifficultyCommand(admin, []string{"peaceful"}); !strings.Contains(reply, "did not change") {
		t.Fatalf("expected did not change message, got %q", reply)
	}
}

func TestDifficultyLockAndServerboundPackets(t *testing.T) {
	srv := newGameRulesTestServer(DefaultConfig())
	admin, _ := srv.RegisterPlayer(Profile{UUID: [16]byte{2}, Name: "OpUser"}, nil)
	_ = srv.GrantOp(admin.Profile)

	if err := srv.HandleChangeDifficultyPacket(admin, protocol.NewReader(EncodeChangeDifficultySBPacket(DifficultyHard))); err != nil {
		t.Fatalf("HandleChangeDifficultyPacket: %v", err)
	}
	if srv.Difficulty() != DifficultyHard {
		t.Fatalf("expected Hard difficulty, got %v", srv.Difficulty())
	}

	if err := srv.HandleLockDifficultyPacket(admin, protocol.NewReader(EncodeLockDifficultySBPacket(true))); err != nil {
		t.Fatalf("HandleLockDifficultyPacket: %v", err)
	}
	if !srv.DifficultyLocked() {
		t.Fatalf("expected DifficultyLocked() == true")
	}

	// While locked, ServerboundChangeDifficultyPacket should not change difficulty.
	_ = srv.HandleChangeDifficultyPacket(admin, protocol.NewReader(EncodeChangeDifficultySBPacket(DifficultyEasy)))
	if srv.Difficulty() != DifficultyHard {
		t.Fatalf("expected difficulty to remain Hard while locked, got %v", srv.Difficulty())
	}
}

func TestHardcoreModeDeathAndLoginFlags(t *testing.T) {
	srv := newGameRulesTestServer(DefaultConfig())
	srv.SetHardcore(true)

	if !srv.IsHardcore() || srv.Difficulty() != DifficultyHard || !srv.DifficultyLocked() {
		t.Fatalf("expected Hardcore to force Hard and lock difficulty: hardcore=%v diff=%v locked=%v",
			srv.IsHardcore(), srv.Difficulty(), srv.DifficultyLocked())
	}
	if reply := srv.HandleDifficultyCommand(nil, []string{"easy"}); !strings.Contains(reply, "Hardcore Mode") {
		t.Fatalf("expected Hardcore Mode rejection, got %q", reply)
	}

	p, _ := srv.RegisterPlayer(Profile{UUID: [16]byte{3}, Name: "HardcoreHero"}, func(int32, []byte) error { return nil })
	_ = srv.SetPlayerGameMode(p, GameModeSurvival)

	// Verify login packet encodes isHardcore = true.
	loginBytes := srv.EncodePlayerLoginPacket(p, 8)
	r := protocol.NewReader(loginBytes)
	if _, err := r.Int32(); err != nil {
		t.Fatalf("read entityID: %v", err)
	}
	hcFlag, err := r.Bool()
	if err != nil || !hcFlag {
		t.Fatalf("login packet isHardcore = %v (err=%v), want true", hcFlag, err)
	}

	// Fatal damage in Hardcore transitions the player to Spectator mode.
	applied := srv.DamagePlayer(p, 100.0, "out_of_world")
	if !applied {
		t.Fatalf("expected fatal damage to be applied")
	}
	if got := p.Snapshot().GameMode; got != GameModeSpectator {
		t.Fatalf("expected Spectator mode on Hardcore death, got %d", got)
	}
}

func TestGameRulesCatalogAndCommand(t *testing.T) {
	srv := newGameRulesTestServer(DefaultConfig())
	names := AllGameRuleNames()
	if len(names) < 50 {
		t.Fatalf("expected at least 50 vanilla game rules, got %d", len(names))
	}

	// Query and set boolean and integer rules (both camelCase and snake_case).
	if reply := srv.HandleGameRuleCommand(nil, []string{"keepInventory"}); reply != "Gamerule keepInventory is currently set to: false" {
		t.Fatalf("unexpected query reply: %q", reply)
	}
	if reply := srv.HandleGameRuleCommand(nil, []string{"keep_inventory", "true"}); reply != "Gamerule keepInventory is now set to: true" {
		t.Fatalf("unexpected set reply: %q", reply)
	}
	if !srv.KeepInventory() {
		t.Fatalf("expected KeepInventory() == true")
	}

	if reply := srv.HandleGameRuleCommand(nil, []string{"playersSleepingPercentage", "50"}); reply != "Gamerule playersSleepingPercentage is now set to: 50" {
		t.Fatalf("unexpected int set reply: %q", reply)
	}
	if srv.PlayersSleepingPercentage() != 50 {
		t.Fatalf("expected PlayersSleepingPercentage() == 50, got %d", srv.PlayersSleepingPercentage())
	}
	if !srv.CanSkipNightWithSleepingPlayers(1, 2) || srv.CanSkipNightWithSleepingPlayers(1, 3) {
		t.Fatalf("unexpected CanSkipNightWithSleepingPlayers results at 50%%")
	}
}

func TestGameRuleGameplayEffects(t *testing.T) {
	srv := newGameRulesTestServer(DefaultConfig())

	var gameEvents []byte
	var entityEvents []byte
	p, _ := srv.RegisterPlayer(Profile{UUID: [16]byte{4}, Name: "Tester"}, func(id int32, body []byte) error {
		switch id {
		case protocol.PlayGameEvent:
			gameEvents = append(gameEvents, body[0])
		case protocol.PlayEntityEvent:
			if len(body) >= 5 {
				entityEvents = append(entityEvents, body[4])
			}
		}
		return nil
	})
	_ = srv.SetPlayerGameMode(p, GameModeSurvival)
	gameEvents = nil

	// Toggle doImmediateRespawn, doLimitedCrafting, and reducedDebugInfo.
	srv.SetDoImmediateRespawn(true)
	srv.SetDoLimitedCrafting(true)
	srv.SetReducedDebugInfo(true)
	if len(gameEvents) != 2 || gameEvents[0] != protocol.GameEventEnableRespawnScreen || gameEvents[1] != protocol.GameEventLimitedCrafting {
		t.Fatalf("expected game events [11, 12], got %v", gameEvents)
	}
	if len(entityEvents) != 1 || entityEvents[0] != EntityEventReducedDebugInfo {
		t.Fatalf("expected entity event [22], got %v", entityEvents)
	}
	srv.SetDoImmediateRespawn(false)

	// Damage gamerules: fallDamage, fireDamage, drowningDamage, freezeDamage.
	srv.SetFallDamage(false)
	if srv.DamagePlayer(p, 5.0, "fall") {
		t.Fatalf("expected fall damage to be suppressed when fallDamage=false")
	}
	srv.SetFireDamage(false)
	if srv.DamagePlayer(p, 5.0, "lava") {
		t.Fatalf("expected lava damage to be suppressed when fireDamage=false")
	}
	srv.SetDrowningDamage(false)
	if srv.DamagePlayer(p, 5.0, "drowning") {
		t.Fatalf("expected drowning damage to be suppressed when drowningDamage=false")
	}
	srv.SetFreezeDamage(false)
	if srv.DamagePlayer(p, 5.0, "freeze") {
		t.Fatalf("expected freeze damage to be suppressed when freezeDamage=false")
	}

	// Death drops vs keepInventory and vanishing_curse.
	diamondSword := world.NewNamedItemStack("minecraft:diamond_sword", 1)
	cursedSword := world.NewNamedItemStack("minecraft:iron_sword", 1)
	p.SetInventorySlot(world.HotbarContainerOffset, diamondSword)
	p.SetInventorySlot(world.HotbarContainerOffset+1, cursedSword)
	SetSlotItemMeta(p, world.HotbarContainerOffset+1, ItemMeta{
		Enchantments: map[string]int{"minecraft:vanishing_curse": 1},
	})
	srv.SetPlayerExperience(p, 5, 100, 0.5)

	// With keepInventory=true, death preserves inventory and XP.
	srv.SetKeepInventory(true)
	srv.DamagePlayer(p, 100.0, "generic")
	if p.InventorySlot(world.HotbarContainerOffset).IsEmpty() {
		t.Fatalf("expected inventory preserved when keepInventory=true")
	}
	_ = srv.RespawnPlayer(p)

	// With keepInventory=false, death drops non-cursed items and clears cursed items.
	srv.SetKeepInventory(false)
	droppedIDs, droppedXP := srv.DropPlayerDeathInventoryAndXP(p)
	if len(droppedIDs) != 1 {
		t.Fatalf("expected 1 dropped item (vanishing_curse item should vanish), got %d", len(droppedIDs))
	}
	if droppedXP != 35 {
		t.Fatalf("expected 35 dropped XP (5*7), got %d", droppedXP)
	}
	if !p.InventorySlot(world.HotbarContainerOffset).IsEmpty() || !p.InventorySlot(world.HotbarContainerOffset+1).IsEmpty() {
		t.Fatalf("expected inventory slots cleared after death drop")
	}
}

func TestWorldSettingsPersistenceRoundTrip(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()
	cfg.WorldDir = dir
	srv1 := newGameRulesTestServer(cfg)
	srv1.ChangeDifficulty(DifficultyHard)
	srv1.SetDifficultyLocked(true)
	srv1.SetKeepInventory(true)
	srv1.SetPlayersSleepingPercentage(25)
	srv1.SetGameRuleInt("randomTickSpeed", 9)
	if err := srv1.SaveWorldSettingsFile(); err != nil {
		t.Fatalf("SaveWorldSettingsFile: %v", err)
	}

	srv2 := newGameRulesTestServer(cfg)
	if srv2.Difficulty() != DifficultyHard || !srv2.DifficultyLocked() {
		t.Fatalf("restored difficulty=(%v, locked=%v), want (Hard, true)", srv2.Difficulty(), srv2.DifficultyLocked())
	}
	if !srv2.KeepInventory() || srv2.PlayersSleepingPercentage() != 25 || srv2.GameRuleInt("randomTickSpeed") != 9 {
		t.Fatalf("restored gamerules mismatch: keepInv=%v sleepPct=%d tickSpeed=%d",
			srv2.KeepInventory(), srv2.PlayersSleepingPercentage(), srv2.GameRuleInt("randomTickSpeed"))
	}
}
