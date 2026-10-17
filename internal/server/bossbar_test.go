package server

import (
	"math"
	"strings"
	"testing"

	"regionio/internal/protocol"
)

func TestBossEventPacketEncodingAndDecoding(t *testing.T) {
	uuid := DeterministicBossBarUUID("minecraft:test_bar")
	name := ChatComponent{Text: "Wither", Color: "dark_purple"}
	flags := PackBossBarFlags(true, true, false)

	// 1. ADD (op=0)
	addRaw := EncodeBossEventAddPacket(uuid, name, 0.75, BossBarColorPurple, BossBarOverlayNotched10, flags)
	addDec, err := DecodeBossEventPacket(protocol.NewReader(addRaw))
	if err != nil {
		t.Fatalf("DecodeBossEventPacket(ADD): %v", err)
	}
	if addDec.UUID != uuid || addDec.Operation != BossEventOpAdd || addDec.Name.PlainText() != "Wither" ||
		math.Abs(float64(addDec.Progress-0.75)) > 1e-5 || addDec.Color != BossBarColorPurple ||
		addDec.Overlay != BossBarOverlayNotched10 || addDec.Flags != flags {
		t.Fatalf("unexpected ADD decode: %+v", addDec)
	}
	darken, music, fog := UnpackBossBarFlags(addDec.Flags)
	if !darken || !music || fog {
		t.Fatalf("unexpected unpacked flags: darken=%v music=%v fog=%v", darken, music, fog)
	}

	// 2. REMOVE (op=1)
	remRaw := EncodeBossEventRemovePacket(uuid)
	remDec, err := DecodeBossEventPacket(protocol.NewReader(remRaw))
	if err != nil {
		t.Fatalf("DecodeBossEventPacket(REMOVE): %v", err)
	}
	if remDec.UUID != uuid || remDec.Operation != BossEventOpRemove {
		t.Fatalf("unexpected REMOVE decode: %+v", remDec)
	}

	// 3. UPDATE_PROGRESS (op=2)
	progRaw := EncodeBossEventUpdateProgressPacket(uuid, 0.42)
	progDec, err := DecodeBossEventPacket(protocol.NewReader(progRaw))
	if err != nil {
		t.Fatalf("DecodeBossEventPacket(UPDATE_PROGRESS): %v", err)
	}
	if progDec.UUID != uuid || progDec.Operation != BossEventOpUpdateProgress || math.Abs(float64(progDec.Progress-0.42)) > 1e-5 {
		t.Fatalf("unexpected UPDATE_PROGRESS decode: %+v", progDec)
	}

	// 4. UPDATE_NAME (op=3)
	newName := TextComponent("Enraged Wither")
	nameRaw := EncodeBossEventUpdateNamePacket(uuid, newName)
	nameDec, err := DecodeBossEventPacket(protocol.NewReader(nameRaw))
	if err != nil {
		t.Fatalf("DecodeBossEventPacket(UPDATE_NAME): %v", err)
	}
	if nameDec.UUID != uuid || nameDec.Operation != BossEventOpUpdateName || nameDec.Name.PlainText() != "Enraged Wither" {
		t.Fatalf("unexpected UPDATE_NAME decode: %+v", nameDec)
	}

	// 5. UPDATE_STYLE (op=4)
	styleRaw := EncodeBossEventUpdateStylePacket(uuid, BossBarColorRed, BossBarOverlayNotched20)
	styleDec, err := DecodeBossEventPacket(protocol.NewReader(styleRaw))
	if err != nil {
		t.Fatalf("DecodeBossEventPacket(UPDATE_STYLE): %v", err)
	}
	if styleDec.UUID != uuid || styleDec.Operation != BossEventOpUpdateStyle ||
		styleDec.Color != BossBarColorRed || styleDec.Overlay != BossBarOverlayNotched20 {
		t.Fatalf("unexpected UPDATE_STYLE decode: %+v", styleDec)
	}

	// 6. UPDATE_PROPERTIES (op=5)
	propRaw := EncodeBossEventUpdatePropertiesPacket(uuid, PackBossBarFlags(false, true, true))
	propDec, err := DecodeBossEventPacket(protocol.NewReader(propRaw))
	if err != nil {
		t.Fatalf("DecodeBossEventPacket(UPDATE_PROPERTIES): %v", err)
	}
	if propDec.UUID != uuid || propDec.Operation != BossEventOpUpdateProperties || propDec.Flags != (BossBarFlagPlayBossMusic|BossBarFlagCreateWorldFog) {
		t.Fatalf("unexpected UPDATE_PROPERTIES decode: %+v", propDec)
	}
}

func TestCustomBossBarAndCommandLifecycle(t *testing.T) {
	srv := newTestMapServer(t)
	alice, alicePkts, aliceMu := newTestMapPlayer(srv, "Alice", 0, 70, 0, 0)
	bob, bobPkts, bobMu := newTestMapPlayer(srv, "Bob", 5, 70, 0, 0)

	// 1. /bossbar list when empty
	reply, handled := srv.HandleSocialAndScoreboardCommand(alice, "bossbar", []string{"list"})
	if !handled || !strings.Contains(reply, "no custom bossbars") {
		t.Fatalf("expected empty bossbar list, got handled=%v reply=%q", handled, reply)
	}

	// 2. /bossbar add dragon_bar {"text":"Ender Dragon","color":"light_purple"}
	reply = srv.HandleBossBarCommandLine(alice, `add dragon_bar {"text":"Ender Dragon","color":"light_purple"}`)
	if !strings.Contains(reply, "Created custom bossbar [Ender Dragon]") {
		t.Fatalf("unexpected /bossbar add reply: %q", reply)
	}
	bar, ok := srv.GetCustomBossBar("dragon_bar")
	if !ok || bar.ID != "minecraft:dragon_bar" || bar.Color != BossBarColorWhite ||
		bar.Overlay != BossBarOverlayProgress || bar.Max != 100 || bar.Value != 0 || !bar.Visible {
		t.Fatalf("unexpected initial CustomBossBar state: %+v (ok=%v)", bar, ok)
	}

	// 3. /bossbar get max|value|visible|players
	if r := srv.HandleBossBarCommandLine(alice, "get dragon_bar max"); !strings.Contains(r, "maximum of 100") {
		t.Fatalf("unexpected get max: %q", r)
	}
	if r := srv.HandleBossBarCommandLine(alice, "get dragon_bar value"); !strings.Contains(r, "value of 0") {
		t.Fatalf("unexpected get value: %q", r)
	}
	if r := srv.HandleBossBarCommandLine(alice, "get dragon_bar visible"); !strings.Contains(r, "currently shown") {
		t.Fatalf("unexpected get visible: %q", r)
	}
	if r := srv.HandleBossBarCommandLine(alice, "get dragon_bar players"); !strings.Contains(r, "no players") {
		t.Fatalf("unexpected get players: %q", r)
	}

	// 4. /bossbar set dragon_bar players @a -> sends ADD (op=0) to Alice and Bob
	aliceMu.Lock()
	*alicePkts = nil
	aliceMu.Unlock()
	bobMu.Lock()
	*bobPkts = nil
	bobMu.Unlock()

	reply = srv.HandleBossBarCommandLine(alice, "set dragon_bar players @a")
	if !strings.Contains(reply, "2 player(s)") {
		t.Fatalf("unexpected set players reply: %q", reply)
	}

	for name, pktsPtr := range map[string]*[]mapTestCapturedPacket{"Alice": alicePkts, "Bob": bobPkts} {
		if len(*pktsPtr) != 1 || (*pktsPtr)[0].id != PlayBossEventCB {
			t.Fatalf("expected 1 BossEvent ADD packet for %s, got %d", name, len(*pktsPtr))
		}
		dec, err := DecodeBossEventPacket(protocol.NewReader((*pktsPtr)[0].body))
		if err != nil || dec.Operation != BossEventOpAdd || dec.Name.PlainText() != "Ender Dragon" {
			t.Fatalf("unexpected BossEvent ADD for %s: dec=%+v err=%v", name, dec, err)
		}
	}

	// 5. /bossbar set dragon_bar max 200 & value 150 -> sends UPDATE_PROGRESS (progress=0.75)
	aliceMu.Lock()
	*alicePkts = nil
	aliceMu.Unlock()
	srv.HandleBossBarCommandLine(alice, "set dragon_bar max 200")
	srv.HandleBossBarCommandLine(alice, "set dragon_bar value 150")

	aliceMu.Lock()
	if len(*alicePkts) == 0 {
		aliceMu.Unlock()
		t.Fatalf("expected UPDATE_PROGRESS packet after max/value change")
	}
	lastPkt := (*alicePkts)[len(*alicePkts)-1]
	aliceMu.Unlock()

	decProg, err := DecodeBossEventPacket(protocol.NewReader(lastPkt.body))
	if err != nil || decProg.Operation != BossEventOpUpdateProgress || math.Abs(float64(decProg.Progress-0.75)) > 1e-5 {
		t.Fatalf("expected UPDATE_PROGRESS=0.75, got dec=%+v err=%v", decProg, err)
	}

	// 6. /bossbar set dragon_bar color purple & style notched_10 -> sends UPDATE_STYLE
	aliceMu.Lock()
	*alicePkts = nil
	aliceMu.Unlock()
	srv.HandleBossBarCommandLine(alice, "set dragon_bar color purple")
	srv.HandleBossBarCommandLine(alice, "set dragon_bar style notched_10")

	aliceMu.Lock()
	if len(*alicePkts) != 2 {
		aliceMu.Unlock()
		t.Fatalf("expected 2 UPDATE_STYLE packets, got %d", len(*alicePkts))
	}
	stylePkt := (*alicePkts)[1]
	aliceMu.Unlock()

	decStyle, err := DecodeBossEventPacket(protocol.NewReader(stylePkt.body))
	if err != nil || decStyle.Operation != BossEventOpUpdateStyle || decStyle.Color != BossBarColorPurple || decStyle.Overlay != BossBarOverlayNotched10 {
		t.Fatalf("unexpected UPDATE_STYLE decode: %+v err=%v", decStyle, err)
	}

	// 7. /bossbar set dragon_bar name The Void Dragon -> sends UPDATE_NAME
	aliceMu.Lock()
	*alicePkts = nil
	aliceMu.Unlock()
	srv.HandleBossBarCommandLine(alice, "set dragon_bar name The Void Dragon")

	aliceMu.Lock()
	if len(*alicePkts) != 1 {
		aliceMu.Unlock()
		t.Fatalf("expected 1 UPDATE_NAME packet, got %d", len(*alicePkts))
	}
	namePkt := (*alicePkts)[0]
	aliceMu.Unlock()

	decName, err := DecodeBossEventPacket(protocol.NewReader(namePkt.body))
	if err != nil || decName.Operation != BossEventOpUpdateName || decName.Name.PlainText() != "The Void Dragon" {
		t.Fatalf("unexpected UPDATE_NAME decode: %+v err=%v", decName, err)
	}

	// 8. /bossbar set dragon_bar visible false -> sends REMOVE; visible true -> sends ADD
	aliceMu.Lock()
	*alicePkts = nil
	aliceMu.Unlock()
	srv.HandleBossBarCommandLine(alice, "set dragon_bar visible false")
	srv.HandleBossBarCommandLine(alice, "set dragon_bar visible true")

	aliceMu.Lock()
	if len(*alicePkts) != 2 {
		aliceMu.Unlock()
		t.Fatalf("expected REMOVE then ADD packets on visible toggle, got %d", len(*alicePkts))
	}
	remOp, _ := DecodeBossEventPacket(protocol.NewReader((*alicePkts)[0].body))
	addOp, _ := DecodeBossEventPacket(protocol.NewReader((*alicePkts)[1].body))
	aliceMu.Unlock()
	if remOp.Operation != BossEventOpRemove || addOp.Operation != BossEventOpAdd {
		t.Fatalf("expected REMOVE then ADD, got %d then %d", remOp.Operation, addOp.Operation)
	}

	// 9. /bossbar set dragon_bar players Alice -> removes Bob (sends REMOVE to Bob)
	bobMu.Lock()
	*bobPkts = nil
	bobMu.Unlock()
	srv.HandleBossBarCommandLine(alice, "set dragon_bar players Alice")
	bobMu.Lock()
	if len(*bobPkts) != 1 {
		bobMu.Unlock()
		t.Fatalf("expected 1 REMOVE packet for Bob, got %d", len(*bobPkts))
	}
	bobRem, _ := DecodeBossEventPacket(protocol.NewReader((*bobPkts)[0].body))
	bobMu.Unlock()
	if bobRem.Operation != BossEventOpRemove {
		t.Fatalf("expected REMOVE for Bob, got op=%d", bobRem.Operation)
	}
	_ = bob

	// 10. /bossbar remove dragon_bar -> sends REMOVE to Alice
	aliceMu.Lock()
	*alicePkts = nil
	aliceMu.Unlock()
	srv.HandleBossBarCommandLine(alice, "remove dragon_bar")
	aliceMu.Lock()
	if len(*alicePkts) != 1 {
		aliceMu.Unlock()
		t.Fatalf("expected 1 REMOVE packet for Alice on /bossbar remove, got %d", len(*alicePkts))
	}
	aliceRem, _ := DecodeBossEventPacket(protocol.NewReader((*alicePkts)[0].body))
	aliceMu.Unlock()
	if aliceRem.Operation != BossEventOpRemove {
		t.Fatalf("expected REMOVE for Alice, got op=%d", aliceRem.Operation)
	}
	if len(srv.ListCustomBossBars()) != 0 {
		t.Fatalf("expected 0 custom bossbars after remove")
	}
}

func TestBossBarJoinQuitAndPersistence(t *testing.T) {
	srv1 := newTestMapServer(t)
	srv1.cfg.WorldDir = t.TempDir()
	alice1, _, _ := newTestMapPlayer(srv1, "Alice", 0, 70, 0, 0)

	bar, err := srv1.CreateCustomBossBar("custom:raid_wave", ChatComponent{Text: "Raid - Wave 3", Color: "red"})
	if err != nil {
		t.Fatalf("CreateCustomBossBar: %v", err)
	}
	_, _ = srv1.SetCustomBossBarColor(bar.ID, BossBarColorRed)
	_, _ = srv1.SetCustomBossBarOverlay(bar.ID, BossBarOverlayNotched12)
	_, _ = srv1.SetCustomBossBarMax(bar.ID, 50)
	_, _ = srv1.SetCustomBossBarValue(bar.ID, 35)
	_, _ = srv1.SetCustomBossBarFlags(bar.ID, true, false, true)
	_, _ = srv1.SetCustomBossBarPlayers(bar.ID, []*PlayerSession{alice1})

	if err := srv1.SaveBossBarsFile(); err != nil {
		t.Fatalf("SaveBossBarsFile: %v", err)
	}

	// Load into a second server sharing the same WorldDir.
	srv2 := newTestMapServer(t)
	srv2.cfg.WorldDir = srv1.cfg.WorldDir
	if err := srv2.LoadBossBarsFile(); err != nil {
		t.Fatalf("LoadBossBarsFile: %v", err)
	}

	loaded, ok := srv2.GetCustomBossBar("custom:raid_wave")
	if !ok {
		t.Fatalf("expected custom:raid_wave to be loaded from bossbars.dat")
	}
	if loaded.Name.PlainText() != "Raid - Wave 3" || loaded.Color != BossBarColorRed ||
		loaded.Overlay != BossBarOverlayNotched12 || loaded.Max != 50 || loaded.Value != 35 ||
		!loaded.Visible || !loaded.DarkenScreen || loaded.PlayBossMusic || !loaded.CreateWorldFog {
		t.Fatalf("unexpected loaded custom bossbar: %+v", loaded)
	}

	// Simulate Alice joining srv2 -> should receive ADD packet for custom:raid_wave.
	alice2, alice2Pkts, alice2Mu := newTestMapPlayer(srv2, "Alice", 0, 70, 0, 0)
	alice2Mu.Lock()
	*alice2Pkts = nil
	alice2Mu.Unlock()

	srv2.SendBossBarsOnPlayerJoin(alice2)

	alice2Mu.Lock()
	if len(*alice2Pkts) != 1 || (*alice2Pkts)[0].id != PlayBossEventCB {
		alice2Mu.Unlock()
		t.Fatalf("expected 1 BossEvent ADD on join, got %d", len(*alice2Pkts))
	}
	joinDec, err := DecodeBossEventPacket(protocol.NewReader((*alice2Pkts)[0].body))
	alice2Mu.Unlock()
	if err != nil || joinDec.Operation != BossEventOpAdd || math.Abs(float64(joinDec.Progress-0.70)) > 1e-5 ||
		joinDec.Color != BossBarColorRed || joinDec.Overlay != BossBarOverlayNotched12 ||
		joinDec.Flags != (BossBarFlagDarkenScreen|BossBarFlagCreateWorldFog) {
		t.Fatalf("unexpected join BossEvent packet: %+v err=%v", joinDec, err)
	}

	// Simulate Alice quitting -> receives REMOVE, but stays subscribed for next join.
	alice2Mu.Lock()
	*alice2Pkts = nil
	alice2Mu.Unlock()
	srv2.OnPlayerQuitBossBars(alice2)
	alice2Mu.Lock()
	if len(*alice2Pkts) != 1 {
		alice2Mu.Unlock()
		t.Fatalf("expected 1 BossEvent REMOVE on quit, got %d", len(*alice2Pkts))
	}
	quitDec, _ := DecodeBossEventPacket(protocol.NewReader((*alice2Pkts)[0].body))
	alice2Mu.Unlock()
	if quitDec.Operation != BossEventOpRemove {
		t.Fatalf("expected REMOVE on quit, got op=%d", quitDec.Operation)
	}
	if !loaded.HasPlayer(alice2) {
		t.Fatalf("expected custom bossbar to retain player UUID across disconnect")
	}
}

func TestRuntimeBossBarAPIForBossMobsAndRaids(t *testing.T) {
	srv := newTestMapServer(t)
	nearPlayer, nearPkts, nearMu := newTestMapPlayer(srv, "NearHero", 10, 70, 10, 0)
	farPlayer, farPkts, farMu := newTestMapPlayer(srv, "FarExplorer", 200, 70, 200, 0)
	_ = nearPlayer
	_ = farPlayer

	nearMu.Lock()
	*nearPkts = nil
	nearMu.Unlock()
	farMu.Lock()
	*farPkts = nil
	farMu.Unlock()

	// 1. Create an entity boss bar for a Wither (entityID=777)
	witherBar := srv.CreateEntityBossBar(777, ChatComponent{Translate: "entity.minecraft.wither"}, BossBarColorPurple, BossBarOverlayProgress, true, false, true)
	if witherBar == nil {
		t.Fatalf("CreateEntityBossBar returned nil")
	}

	// 2. Sync viewers within 64 blocks of (0, 70, 0) -> subscribes NearHero, ignores FarExplorer
	srv.SyncRuntimeBossBarPlayersInRange(witherBar.UUID, "minecraft:overworld", 0, 70, 0, 64.0)

	nearMu.Lock()
	if len(*nearPkts) != 1 {
		nearMu.Unlock()
		t.Fatalf("expected NearHero to receive ADD packet, got %d packets", len(*nearPkts))
	}
	nearAdd, _ := DecodeBossEventPacket(protocol.NewReader((*nearPkts)[0].body))
	*nearPkts = nil
	nearMu.Unlock()
	if nearAdd.Operation != BossEventOpAdd || math.Abs(float64(nearAdd.Progress-1.0)) > 1e-5 {
		t.Fatalf("unexpected NearHero ADD packet: %+v", nearAdd)
	}

	farMu.Lock()
	if len(*farPkts) != 0 {
		farMu.Unlock()
		t.Fatalf("expected FarExplorer to receive 0 packets, got %d", len(*farPkts))
	}
	farMu.Unlock()

	// 3. Update Wither health to 180 / 300 (0.60)
	if !srv.UpdateEntityBossBarHealth(777, 180, 300) {
		t.Fatalf("expected UpdateEntityBossBarHealth to return true")
	}
	nearMu.Lock()
	if len(*nearPkts) != 1 {
		nearMu.Unlock()
		t.Fatalf("expected 1 UPDATE_PROGRESS packet, got %d", len(*nearPkts))
	}
	nearProg, _ := DecodeBossEventPacket(protocol.NewReader((*nearPkts)[0].body))
	*nearPkts = nil
	nearMu.Unlock()
	if nearProg.Operation != BossEventOpUpdateProgress || math.Abs(float64(nearProg.Progress-0.6)) > 1e-5 {
		t.Fatalf("expected progress=0.6, got %+v", nearProg)
	}

	// 4. Remove entity boss bar on Wither death -> sends REMOVE to NearHero
	if !srv.RemoveEntityBossBar(777) {
		t.Fatalf("expected RemoveEntityBossBar to return true")
	}
	nearMu.Lock()
	if len(*nearPkts) != 1 {
		nearMu.Unlock()
		t.Fatalf("expected 1 REMOVE packet on boss death, got %d", len(*nearPkts))
	}
	nearRem, _ := DecodeBossEventPacket(protocol.NewReader((*nearPkts)[0].body))
	nearMu.Unlock()
	if nearRem.Operation != BossEventOpRemove {
		t.Fatalf("expected REMOVE operation, got %d", nearRem.Operation)
	}
}
