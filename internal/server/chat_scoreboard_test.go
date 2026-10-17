package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"regionio/internal/protocol"
	"regionio/internal/registry"
	"regionio/internal/world"
)

func TestChatTypesAndComponentRoundTrip(t *testing.T) {
	all := AllChatTypes()
	if len(all) != 7 {
		t.Fatalf("AllChatTypes count = %d, want 7", len(all))
	}
	for i := int32(0); i < 7; i++ {
		def, ok := ChatTypeByIndex(i)
		if !ok || def.RegistryIndex != i {
			t.Fatalf("ChatTypeByIndex(%d) = (%+v, %v)", i, def, ok)
		}
		if regIdx := registry.Index("minecraft:chat_type", def.ID); regIdx != int(i) {
			t.Fatalf("registry.Index(chat_type, %q) = %d, want %d", def.ID, regIdx, i)
		}
		byID, ok := ChatTypeByID(def.ID)
		if !ok || byID.RegistryIndex != i {
			t.Fatalf("ChatTypeByID(%q) = (%+v, %v)", def.ID, byID, ok)
		}
	}

	// JSON -> ChatComponent -> NBT -> ChatComponent round-trip
	rawJSON := `{"text":"Hello ","color":"gold","bold":true,"extra":[{"text":"World","color":"#00FFAA","italic":true}]}`
	comp, err := ParseChatComponentJSON(rawJSON)
	if err != nil {
		t.Fatalf("ParseChatComponentJSON: %v", err)
	}
	if got := comp.PlainText(); got != "Hello World" {
		t.Fatalf("PlainText = %q, want %q", got, "Hello World")
	}

	sysRaw := EncodeSystemChatPacket(comp, true)
	decodedComp, overlay, err := DecodeSystemChatPacket(protocol.NewReader(sysRaw))
	if err != nil {
		t.Fatalf("DecodeSystemChatPacket: %v", err)
	}
	if !overlay || decodedComp.PlainText() != "Hello World" || decodedComp.Color != "gold" || decodedComp.Bold == nil || !*decodedComp.Bold {
		t.Fatalf("unexpected decoded system chat component: overlay=%v comp=%+v", overlay, decodedComp)
	}

	// DisguisedChatPacket round-trip
	senderComp := ColoredTextComponent("Alice", "aqua")
	targetComp := ColoredTextComponent("Bob", "yellow")
	disgRaw := EncodeDisguisedChatPacket(TextComponent("secret"), ChatTypeMsgCommandIncoming, senderComp, &targetComp)
	msgOut, ctIdx, senderOut, targetOut, err := DecodeDisguisedChatPacket(protocol.NewReader(disgRaw))
	if err != nil {
		t.Fatalf("DecodeDisguisedChatPacket: %v", err)
	}
	if msgOut.PlainText() != "secret" || ctIdx != ChatTypeMsgCommandIncoming || senderOut.PlainText() != "Alice" || targetOut == nil || targetOut.PlainText() != "Bob" {
		t.Fatalf("unexpected DisguisedChatPacket decode: msg=%+v ct=%d sender=%+v target=%+v", msgOut, ctIdx, senderOut, targetOut)
	}
}

func TestChatCommandsJoinLeaveDeathAndTeamVisibility(t *testing.T) {
	srv := newTestMapServer(t)
	alice, alicePkts, aliceMu := newTestMapPlayer(srv, "Alice", 0, 70, 0, 0)
	_, bobPkts, bobMu := newTestMapPlayer(srv, "Bob", 5, 70, 0, 0)
	charlie, charliePkts, charlieMu := newTestMapPlayer(srv, "Charlie", 10, 70, 0, 0)

	// 1) Join & leave translatable messages
	joinComp := srv.FormatJoinMessage(alice)
	if joinComp.Translate != "multiplayer.player.joined" || joinComp.Color != "yellow" || joinComp.PlainText() != "Alice joined the game" {
		t.Fatalf("unexpected join message: %+v (%q)", joinComp, joinComp.PlainText())
	}
	leaveComp := srv.FormatLeaveMessage(alice)
	if leaveComp.Translate != "multiplayer.player.left" || leaveComp.Color != "yellow" || leaveComp.PlainText() != "Alice left the game" {
		t.Fatalf("unexpected leave message: %+v (%q)", leaveComp, leaveComp.PlainText())
	}

	// 2) /msg (/tell) sends incoming whisper to Bob and outgoing whisper to Alice
	aliceMu.Lock()
	*alicePkts = nil
	aliceMu.Unlock()
	bobMu.Lock()
	*bobPkts = nil
	bobMu.Unlock()

	if reply := srv.HandleMsgCommand(alice, []string{"Bob", "meet", "at", "spawn"}); reply != "" {
		t.Fatalf("HandleMsgCommand reply = %q, want empty", reply)
	}
	bobMu.Lock()
	if len(*bobPkts) == 0 {
		t.Fatal("expected Bob to receive incoming whisper packet")
	}
	bobMsg, _, err := DecodeSystemChatPacket(protocol.NewReader((*bobPkts)[len(*bobPkts)-1].body))
	bobMu.Unlock()
	if err != nil || bobMsg.Translate != "commands.message.display.incoming" || bobMsg.Color != "gray" {
		t.Fatalf("unexpected incoming whisper to Bob: %+v err=%v", bobMsg, err)
	}

	aliceMu.Lock()
	if len(*alicePkts) == 0 {
		t.Fatal("expected Alice to receive outgoing whisper packet")
	}
	aliceMsg, _, err := DecodeSystemChatPacket(protocol.NewReader((*alicePkts)[len(*alicePkts)-1].body))
	aliceMu.Unlock()
	if err != nil || aliceMsg.Translate != "commands.message.display.outgoing" {
		t.Fatalf("unexpected outgoing whisper to Alice: %+v err=%v", aliceMsg, err)
	}

	// 3) /me and /say and /tellraw
	if reply := srv.HandleMeCommand(alice, []string{"waves"}); reply != "" {
		t.Fatalf("HandleMeCommand: %s", reply)
	}
	if reply := srv.HandleSayCommand(alice, []string{"Server", "restart", "soon"}); reply != "" {
		t.Fatalf("HandleSayCommand: %s", reply)
	}
	if reply := srv.HandleTellrawCommand(alice, []string{"@a", `[{"text":"Notice: ","color":"red","bold":true},{"text":"Welcome!","color":"green"}]`}); reply != "" {
		t.Fatalf("HandleTellrawCommand: %s", reply)
	}

	// 4) Team prefix/color/suffix in chat + deathMessageVisibility filtering
	_, _ = srv.CreateTeam("red_team", TextComponent("Red Team"))
	_, _ = srv.UpdateTeam("red_team", func(tm *ScoreboardTeam) error {
		tm.Color = ChatColorRed
		tm.Prefix = TextComponent("[RED] ")
		tm.Suffix = TextComponent(" ★")
		tm.DeathMessageVisibility = TeamVisibilityHideForOtherTeams
		return nil
	})
	_, _ = srv.JoinTeam("red_team", "Alice", "Bob")

	disp := srv.FormatPlayerDisplayName(alice)
	if disp.PlainText() != "[RED] Alice ★" || disp.Color != "red" {
		t.Fatalf("FormatPlayerDisplayName with team = %+v (%q), want '[RED] Alice ★' red", disp, disp.PlainText())
	}

	// Broadcast death of Alice: Bob (same team) should receive it, Charlie (other team) should NOT.
	bobMu.Lock()
	*bobPkts = nil
	bobMu.Unlock()
	charlieMu.Lock()
	*charliePkts = nil
	charlieMu.Unlock()

	srv.BroadcastDeathMessage(alice, "lava", "")

	bobMu.Lock()
	bobGotDeath := len(*bobPkts) > 0
	bobMu.Unlock()
	charlieMu.Lock()
	charlieGotDeath := len(*charliePkts) > 0
	charlieMu.Unlock()
	if !bobGotDeath || charlieGotDeath {
		t.Fatalf("deathMessageVisibility=hideForOtherTeams: bobGot=%v charlieGot=%v, want (true, false)",
			bobGotDeath, charlieGotDeath)
	}
	_ = charlie
}

func TestTabListUpdateRemoveAndHeaderFooter(t *testing.T) {
	srv := newTestMapServer(t)
	alice, _, _ := newTestMapPlayer(srv, "Alice", 0, 70, 0, 0)
	bob, bobPkts, bobMu := newTestMapPlayer(srv, "Bob", 10, 70, 0, 0)

	customName := ColoredTextComponent("★ Alice ★", "aqua")
	srv.SetPlayerLatency(alice, 42)
	srv.SetPlayerTabListDisplayName(alice, &customName)
	srv.SetPlayerTabListListed(alice, false)
	srv.SetPlayerTabListOrder(alice, 5)

	entry := srv.BuildTabListEntry(alice)
	if entry.LatencyMs != 42 || entry.Listed || entry.ListOrder != 5 || entry.DisplayName == nil || entry.DisplayName.PlainText() != "★ Alice ★" {
		t.Fatalf("unexpected BuildTabListEntry: %+v", entry)
	}

	// Encode & decode full PlayerInfoUpdate packet
	raw := EncodePlayerInfoUpdatePacket(PlayerInfoActionAll, []TabListEntry{entry})
	gotActions, gotEntries, err := DecodePlayerInfoUpdatePacket(protocol.NewReader(raw))
	if err != nil {
		t.Fatalf("DecodePlayerInfoUpdatePacket: %v", err)
	}
	if gotActions != PlayerInfoActionAll || len(gotEntries) != 1 {
		t.Fatalf("decoded actions=%02x len=%d", gotActions, len(gotEntries))
	}
	ge := gotEntries[0]
	if ge.UUID != alice.Profile.UUID || ge.Name != "Alice" || ge.LatencyMs != 42 || ge.Listed || ge.ListOrder != 5 || ge.DisplayName == nil || ge.DisplayName.PlainText() != "★ Alice ★" {
		t.Fatalf("decoded TabListEntry mismatch: %+v", ge)
	}

	// Encode & decode PlayerInfoRemove packet
	remRaw := EncodePlayerInfoRemovePacket([][16]byte{alice.Profile.UUID})
	uuids, err := DecodePlayerInfoRemovePacket(protocol.NewReader(remRaw))
	if err != nil || len(uuids) != 1 || uuids[0] != alice.Profile.UUID {
		t.Fatalf("DecodePlayerInfoRemovePacket = (%v, %v)", uuids, err)
	}

	// Tab list header & footer
	header := ColoredTextComponent("Welcome to RegionIO", "gold")
	footer := ColoredTextComponent("20 TPS — Vanilla 26.1.2", "gray")
	bobMu.Lock()
	*bobPkts = nil
	bobMu.Unlock()

	srv.SetTabListHeaderFooter(header, footer)

	bobMu.Lock()
	var tabPktBody []byte
	for _, p := range *bobPkts {
		if p.id == PlayTabListCB {
			tabPktBody = p.body
		}
	}
	bobMu.Unlock()
	if tabPktBody == nil {
		t.Fatal("expected Bob to receive PlayTabListCB (0x7a)")
	}
	gotHeader, gotFooter, err := DecodeTabListHeaderFooterPacket(protocol.NewReader(tabPktBody))
	if err != nil || gotHeader.PlainText() != "Welcome to RegionIO" || gotFooter.PlainText() != "20 TPS — Vanilla 26.1.2" {
		t.Fatalf("DecodeTabListHeaderFooterPacket = (%+v, %+v, %v)", gotHeader, gotFooter, err)
	}
	_ = bob
}

func TestScoreboardObjectivesScoresDisplaySlotsAndNumberFormats(t *testing.T) {
	srv := newTestMapServer(t)
	alice, pkts, mu := newTestMapPlayer(srv, "Alice", 0, 70, 0, 0)

	// 1) /scoreboard objectives add & setdisplay & modify
	if reply := srv.HandleScoreboardCommand(alice, []string{"objectives", "add", "coins", "dummy", `{"text":"Gold Coins","color":"gold"}`}); !strings.Contains(reply, "Gold Coins") {
		t.Fatalf("objectives add reply = %q", reply)
	}
	if reply := srv.HandleScoreboardCommand(alice, []string{"objectives", "setdisplay", "sidebar", "coins"}); !strings.Contains(reply, "sidebar") {
		t.Fatalf("objectives setdisplay sidebar reply = %q", reply)
	}
	if reply := srv.HandleScoreboardCommand(alice, []string{"objectives", "modify", "coins", "numberformat", "styled", `{"color":"yellow","bold":true}`}); !strings.Contains(reply, "Updated number format") {
		t.Fatalf("objectives modify numberformat reply = %q", reply)
	}

	// 2) /scoreboard players set / add / remove / operation / display
	_ = srv.HandleScoreboardCommand(alice, []string{"players", "set", "Alice", "coins", "100"})
	_ = srv.HandleScoreboardCommand(alice, []string{"players", "add", "Alice", "coins", "25"})
	_ = srv.HandleScoreboardCommand(alice, []string{"players", "remove", "Alice", "coins", "5"})
	if val, ok := srv.GetScore("Alice", "coins"); !ok || val != 120 {
		t.Fatalf("Alice coins score = (%d, %v), want (120, true)", val, ok)
	}

	_ = srv.HandleScoreboardCommand(alice, []string{"players", "set", "Bank", "coins", "30"})
	_ = srv.HandleScoreboardCommand(alice, []string{"players", "operation", "Alice", "coins", "+=", "Bank", "coins"})
	if val, ok := srv.GetScore("Alice", "coins"); !ok || val != 150 {
		t.Fatalf("Alice coins after += Bank = (%d, %v), want (150, true)", val, ok)
	}

	_ = srv.HandleScoreboardCommand(alice, []string{"players", "display", "name", "Alice", "coins", `{"text":"Rich Alice","color":"aqua"}`})
	_ = srv.HandleScoreboardCommand(alice, []string{"players", "display", "numberformat", "Alice", "coins", "fixed", `{"text":"150 GP","color":"gold"}`})

	scEntry, ok := srv.GetScoreEntry("Alice", "coins")
	if !ok || scEntry.Display == nil || scEntry.Display.PlainText() != "Rich Alice" || scEntry.NumberFormat == nil || scEntry.NumberFormat.Type != NumberFormatFixed {
		t.Fatalf("unexpected ScoreEntry after display commands: %+v", scEntry)
	}

	// Verify SetObjective, SetDisplayObjective, SetScore, and ResetScore packet round-trips.
	obj, _ := srv.Objective("coins")
	objRaw := EncodeSetObjectivePacket(ObjectiveMethodChange, obj)
	mode, decObj, err := DecodeSetObjectivePacket(protocol.NewReader(objRaw))
	if err != nil || mode != ObjectiveMethodChange || decObj.Name != "coins" || decObj.NumberFormat == nil || decObj.NumberFormat.Type != NumberFormatStyled {
		t.Fatalf("DecodeSetObjectivePacket: mode=%d obj=%+v err=%v", mode, decObj, err)
	}

	scRaw := EncodeSetScorePacket(*scEntry)
	decSc, err := DecodeSetScorePacket(protocol.NewReader(scRaw))
	if err != nil || decSc.Holder != "Alice" || decSc.Value != 150 || decSc.Display == nil || decSc.NumberFormat == nil || decSc.NumberFormat.Fixed.PlainText() != "150 GP" {
		t.Fatalf("DecodeSetScorePacket: %+v err=%v", decSc, err)
	}

	// Reset score and verify PlayResetScoreCB (0x4f)
	mu.Lock()
	*pkts = nil
	mu.Unlock()
	_ = srv.HandleScoreboardCommand(alice, []string{"players", "reset", "Bank", "coins"})
	if _, exists := srv.GetScore("Bank", "coins"); exists {
		t.Fatal("expected Bank score to be removed after reset")
	}
	mu.Lock()
	var resetBody []byte
	for _, p := range *pkts {
		if p.id == PlayResetScoreCB {
			resetBody = p.body
		}
	}
	mu.Unlock()
	if resetBody == nil {
		t.Fatal("expected PlayResetScoreCB (0x4f) packet")
	}
	rHolder, rObj, err := DecodeResetScorePacket(protocol.NewReader(resetBody))
	if err != nil || rHolder != "Bank" || rObj != "coins" {
		t.Fatalf("DecodeResetScorePacket = (%q, %q, %v)", rHolder, rObj, err)
	}
}

func TestTeamsFriendlyFireAndScoreboardPersistence(t *testing.T) {
	tmpDir := t.TempDir()
	srv := newTestMapServer(t)
	srv.cfg.WorldDir = tmpDir

	alice, _, _ := newTestMapPlayer(srv, "Alice", 0, 70, 0, 0)
	bob, _, _ := newTestMapPlayer(srv, "Bob", 1.5, 70, 0, 0)

	// Configure team via /team commands
	_ = srv.HandleTeamCommand(alice, []string{"add", "defenders", `{"text":"Defenders","color":"blue"}`})
	_ = srv.HandleTeamCommand(alice, []string{"modify", "defenders", "color", "blue"})
	_ = srv.HandleTeamCommand(alice, []string{"modify", "defenders", "prefix", `{"text":"[DEF] ","color":"aqua"}`})
	_ = srv.HandleTeamCommand(alice, []string{"modify", "defenders", "suffix", `{"text":" [🛡]","color":"gray"}`})
	_ = srv.HandleTeamCommand(alice, []string{"modify", "defenders", "friendlyFire", "false"})
	_ = srv.HandleTeamCommand(alice, []string{"modify", "defenders", "nametagVisibility", "hideForOtherTeams"})
	_ = srv.HandleTeamCommand(alice, []string{"modify", "defenders", "collisionRule", "pushOtherTeams"})
	_ = srv.HandleTeamCommand(alice, []string{"join", "defenders", "Alice"})
	_ = srv.HandleTeamCommand(alice, []string{"join", "defenders", "Bob"})

	// Verify SetPlayerTeamPacket (0x6d) wire round-trip
	tm, ok := srv.Team("defenders")
	if !ok || tm == nil {
		t.Fatal("expected team 'defenders' to exist")
	}
	pktBytes := EncodeSetPlayerTeamPacket(TeamMethodAdd, tm, tm.Members)
	method, decTeam, decPlayers, err := DecodeSetPlayerTeamPacket(protocol.NewReader(pktBytes))
	if err != nil {
		t.Fatalf("DecodeSetPlayerTeamPacket: %v", err)
	}
	if method != TeamMethodAdd || decTeam.Name != "defenders" || decTeam.AllowFriendlyFire ||
		decTeam.Color != ChatColorBlue || decTeam.NameTagVisibility != TeamVisibilityHideForOtherTeams ||
		decTeam.CollisionRule != TeamCollisionPushOtherTeams || len(decPlayers) != 2 {
		t.Fatalf("decoded team mismatch: method=%d team=%+v players=%v", method, decTeam, decPlayers)
	}

	// Friendly-fire check: Alice cannot damage teammate Bob when friendlyFire=false
	if srv.CanAttackPlayer(alice, bob) {
		t.Fatal("expected CanAttackPlayer(alice, bob) to be false when friendlyFire is disabled")
	}
	alice.SetInventorySlot(world.HotbarContainerOffset, world.NewNamedItemStack("minecraft:diamond_sword", 1))
	if dealt, killed := srv.AttackEntity(alice, bob.EntityID); dealt != 0 || killed {
		t.Fatalf("expected AttackEntity on teammate with friendlyFire=false to deal 0 damage, got (%v, %v)", dealt, killed)
	}

	// Re-enable friendlyFire -> Alice can now damage Bob
	_ = srv.HandleTeamCommand(alice, []string{"modify", "defenders", "friendlyFire", "true"})
	if !srv.CanAttackPlayer(alice, bob) {
		t.Fatal("expected CanAttackPlayer(alice, bob) to be true after enabling friendlyFire")
	}
	if dealt, _ := srv.AttackEntity(alice, bob.EntityID); dealt <= 0 {
		t.Fatalf("expected positive PvP damage with friendlyFire=true, got %v", dealt)
	}

	// Add an objective + score + display slot and persist to <WorldDir>/data/scoreboard.dat
	_, _ = srv.CreateObjective("kills", "totalKillCount", ColoredTextComponent("Total Kills", "red"))
	_ = srv.SetDisplayObjective(DisplaySlotBelowName, "kills")
	_, _ = srv.SetScore("Alice", "kills", 7)

	if err := srv.SaveScoreboardFile(); err != nil {
		t.Fatalf("SaveScoreboardFile: %v", err)
	}
	sbFile := filepath.Join(tmpDir, "data", "scoreboard.dat")
	if _, err := os.Stat(sbFile); err != nil {
		t.Fatalf("expected %s to exist: %v", sbFile, err)
	}

	// Load into a fresh server instance and verify objectives, scores, slots, and teams
	srv2 := newTestMapServer(t)
	srv2.cfg.WorldDir = tmpDir
	if err := srv2.LoadScoreboardFile(); err != nil {
		t.Fatalf("LoadScoreboardFile: %v", err)
	}
	if gotSlotObj := srv2.DisplayObjective(DisplaySlotBelowName); gotSlotObj != "kills" {
		t.Fatalf("loaded DisplayObjective(below_name) = %q, want 'kills'", gotSlotObj)
	}
	if val, ok := srv2.GetScore("Alice", "kills"); !ok || val != 7 {
		t.Fatalf("loaded Alice kills score = (%d, %v), want (7, true)", val, ok)
	}
	loadedTeam, ok := srv2.Team("defenders")
	if !ok || loadedTeam == nil || loadedTeam.Color != ChatColorBlue || len(loadedTeam.Members) != 2 || loadedTeam.Prefix.PlainText() != "[DEF] " {
		t.Fatalf("loaded team mismatch: %+v", loadedTeam)
	}
}

func TestTitlesAndTitleCommand(t *testing.T) {
	srv := newTestMapServer(t)
	alice, pkts, mu := newTestMapPlayer(srv, "Alice", 0, 70, 0, 0)

	// 1) /title Alice times 10 70 20
	if reply := srv.HandleTitleCommand(alice, []string{"Alice", "times", "10", "4s", "20t"}); !strings.Contains(reply, "Changed title display times") {
		t.Fatalf("title times reply = %q", reply)
	}
	// 2) /title Alice title {"text":"VICTORY","color":"gold","bold":true}
	if reply := srv.HandleTitleCommand(alice, []string{"Alice", "title", `{"text":"VICTORY","color":"gold","bold":true}`}); !strings.Contains(reply, "Showing new title") {
		t.Fatalf("title title reply = %q", reply)
	}
	// 3) /title Alice subtitle {"text":"Wave 10 Complete","color":"yellow"}
	if reply := srv.HandleTitleCommand(alice, []string{"Alice", "subtitle", `{"text":"Wave 10 Complete","color":"yellow"}`}); !strings.Contains(reply, "Showing new subtitle") {
		t.Fatalf("title subtitle reply = %q", reply)
	}
	// 4) /title Alice actionbar {"text":"HP: 20/20","color":"green"}
	if reply := srv.HandleTitleCommand(alice, []string{"Alice", "actionbar", `{"text":"HP: 20/20","color":"green"}`}); !strings.Contains(reply, "Showing new actionbar") {
		t.Fatalf("title actionbar reply = %q", reply)
	}
	// 5) /title Alice reset
	if reply := srv.HandleTitleCommand(alice, []string{"Alice", "reset"}); !strings.Contains(reply, "Reset title options") {
		t.Fatalf("title reset reply = %q", reply)
	}

	mu.Lock()
	var sawTimes, sawTitle, sawSubtitle, sawActionBar, sawClear bool
	for _, p := range *pkts {
		switch p.id {
		case PlaySetTitlesAnimationCB:
			fi, st, fo, err := DecodeSetTitlesAnimationPacket(protocol.NewReader(p.body))
			if err != nil || fi != 10 || st != 80 || fo != 20 {
				t.Fatalf("DecodeSetTitlesAnimationPacket = (%d, %d, %d, %v), want (10, 80, 20, nil)", fi, st, fo, err)
			}
			sawTimes = true
		case PlaySetTitleTextCB:
			c, err := DecodeSetTitleTextPacket(protocol.NewReader(p.body))
			if err != nil || c.PlainText() != "VICTORY" {
				t.Fatalf("DecodeSetTitleTextPacket = (%+v, %v)", c, err)
			}
			sawTitle = true
		case PlaySetSubtitleTextCB:
			c, err := DecodeSetSubtitleTextPacket(protocol.NewReader(p.body))
			if err != nil || c.PlainText() != "Wave 10 Complete" {
				t.Fatalf("DecodeSetSubtitleTextPacket = (%+v, %v)", c, err)
			}
			sawSubtitle = true
		case PlaySetActionBarTextCB:
			c, err := DecodeSetActionBarTextPacket(protocol.NewReader(p.body))
			if err != nil || c.PlainText() != "HP: 20/20" {
				t.Fatalf("DecodeSetActionBarTextPacket = (%+v, %v)", c, err)
			}
			sawActionBar = true
		case PlayClearTitlesCB:
			reset, err := DecodeClearTitlesPacket(protocol.NewReader(p.body))
			if err != nil || !reset {
				t.Fatalf("DecodeClearTitlesPacket = (%v, %v), want (true, nil)", reset, err)
			}
			sawClear = true
		}
	}
	mu.Unlock()
	if !sawTimes || !sawTitle || !sawSubtitle || !sawActionBar || !sawClear {
		t.Fatalf("missing title packets: times=%v title=%v subtitle=%v actionbar=%v clear=%v",
			sawTimes, sawTitle, sawSubtitle, sawActionBar, sawClear)
	}
}
