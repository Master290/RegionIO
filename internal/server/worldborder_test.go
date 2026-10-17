package server

import (
	"math"
	"strings"
	"sync"
	"testing"

	"regionio/internal/protocol"
)

func TestWorldBorderDefaultsAndBounds(t *testing.T) {
	wb := NewWorldBorder()
	if wb.CenterX() != 0 || wb.CenterZ() != 0 {
		t.Fatalf("default center = (%v, %v), want (0, 0)", wb.CenterX(), wb.CenterZ())
	}
	if wb.Size() != DefaultBorderSize {
		t.Fatalf("default size = %v, want %v", wb.Size(), DefaultBorderSize)
	}
	if wb.DamagePerBlock() != DefaultBorderDamagePerBlock {
		t.Fatalf("default damagePerBlock = %v, want %v", wb.DamagePerBlock(), DefaultBorderDamagePerBlock)
	}
	if wb.SafeZone() != DefaultBorderSafeZone {
		t.Fatalf("default safeZone = %v, want %v", wb.SafeZone(), DefaultBorderSafeZone)
	}
	if wb.WarningBlocks() != DefaultBorderWarningBlocks {
		t.Fatalf("default warningBlocks = %d, want %d", wb.WarningBlocks(), DefaultBorderWarningBlocks)
	}
	if wb.WarningTime() != DefaultBorderWarningTime {
		t.Fatalf("default warningTime = %d, want %d", wb.WarningTime(), DefaultBorderWarningTime)
	}
	if wb.Status() != BorderStatusStationary {
		t.Fatalf("default status = %v, want stationary", wb.Status())
	}

	wb.SetCenter(10, -20)
	wb.SetSize(100)
	if wb.MinX() != -40 || wb.MaxX() != 60 || wb.MinZ() != -70 || wb.MaxZ() != 30 {
		t.Fatalf("bounds = [%v..%v, %v..%v], want [-40..60, -70..30]", wb.MinX(), wb.MaxX(), wb.MinZ(), wb.MaxZ())
	}
	if !wb.IsWithinBounds(10, -20) {
		t.Fatal("center should be within bounds")
	}
	if wb.IsWithinBounds(60, -20) {
		t.Fatal("maxX edge should be outside half-open bounds")
	}
	if !wb.IsBlockWithinBounds(59, 29) || wb.IsBlockWithinBounds(60, 30) {
		t.Fatal("unexpected IsBlockWithinBounds result")
	}
	if dist := wb.DistanceToBorder(10, -20); math.Abs(dist-50) > 1e-6 {
		t.Fatalf("DistanceToBorder at center = %v, want 50", dist)
	}
	if dist := wb.DistanceToBorder(70, -20); math.Abs(dist-(-10)) > 1e-6 {
		t.Fatalf("DistanceToBorder outside = %v, want -10", dist)
	}

	cx, cy, cz := wb.ClampToBounds(100, 64, -100)
	if cx >= 60 || cx < 59.999 || cy != 64 || cz != -70 {
		t.Fatalf("ClampToBounds(100, 64, -100) = (%v, %v, %v)", cx, cy, cz)
	}
}

func TestWorldBorderPacketRoundTrips(t *testing.T) {
	wb := NewWorldBorder()
	wb.SetCenter(12.5, -34.25)
	wb.LerpSizeBetween(200.0, 100.0, 400)
	wb.SetWarningBlocks(8)
	wb.SetWarningTime(25)

	initBytes := EncodeInitializeBorderPacket(wb)
	initPkt, err := DecodeInitializeBorderPacket(initBytes)
	if err != nil {
		t.Fatalf("DecodeInitializeBorderPacket: %v", err)
	}
	if initPkt.NewCenterX != 12.5 || initPkt.NewCenterZ != -34.25 ||
		initPkt.OldSize != 200.0 || initPkt.NewSize != 100.0 ||
		initPkt.LerpTime != 400 || initPkt.NewAbsoluteMaxSize != DefaultBorderAbsoluteMaxSize ||
		initPkt.WarningBlocks != 8 || initPkt.WarningTime != 25 {
		t.Fatalf("unexpected InitializeBorderPacket: %+v", initPkt)
	}

	centerBytes := EncodeSetBorderCenterPacket(42.0, -99.5)
	cx, cz, err := DecodeSetBorderCenterPacket(centerBytes)
	if err != nil || cx != 42.0 || cz != -99.5 {
		t.Fatalf("DecodeSetBorderCenterPacket = (%v, %v, %v)", cx, cz, err)
	}

	lerpBytes := EncodeSetBorderLerpSizePacket(150.0, 300.0, 200)
	oldS, newS, lerpT, err := DecodeSetBorderLerpSizePacket(lerpBytes)
	if err != nil || oldS != 150.0 || newS != 300.0 || lerpT != 200 {
		t.Fatalf("DecodeSetBorderLerpSizePacket = (%v, %v, %v, %v)", oldS, newS, lerpT, err)
	}

	sizeBytes := EncodeSetBorderSizePacket(64.5)
	sz, err := DecodeSetBorderSizePacket(sizeBytes)
	if err != nil || sz != 64.5 {
		t.Fatalf("DecodeSetBorderSizePacket = (%v, %v)", sz, err)
	}

	delayBytes := EncodeSetBorderWarningDelayPacket(30)
	delay, err := DecodeSetBorderWarningDelayPacket(delayBytes)
	if err != nil || delay != 30 {
		t.Fatalf("DecodeSetBorderWarningDelayPacket = (%v, %v)", delay, err)
	}

	distBytes := EncodeSetBorderWarningDistancePacket(12)
	dist, err := DecodeSetBorderWarningDistancePacket(distBytes)
	if err != nil || dist != 12 {
		t.Fatalf("DecodeSetBorderWarningDistancePacket = (%v, %v)", dist, err)
	}
}

func TestWorldBorderLerpTicking(t *testing.T) {
	wb := NewWorldBorder()
	wb.LerpSizeBetween(100.0, 200.0, 10)
	if wb.Status() != BorderStatusGrowing {
		t.Fatalf("status = %v, want growing", wb.Status())
	}
	if wb.Status().Color() != 0x40FF80 {
		t.Fatalf("growing color = 0x%x, want 0x40FF80", wb.Status().Color())
	}
	if wb.LerpTime() != 10 || wb.LerpTarget() != 200.0 {
		t.Fatalf("lerpTime=%d, lerpTarget=%v", wb.LerpTime(), wb.LerpTarget())
	}

	for i := 0; i < 5; i++ {
		wb.Tick()
	}
	if math.Abs(wb.Size()-150.0) > 1e-6 {
		t.Fatalf("size after 5/10 ticks = %v, want 150.0", wb.Size())
	}
	if wb.LerpTime() != 5 {
		t.Fatalf("remaining ticks = %d, want 5", wb.LerpTime())
	}

	for i := 0; i < 5; i++ {
		wb.Tick()
	}
	if wb.Size() != 200.0 || wb.LerpTime() != 0 || wb.Status() != BorderStatusStationary {
		t.Fatalf("after 10/10 ticks: size=%v, lerpTime=%d, status=%v", wb.Size(), wb.LerpTime(), wb.Status())
	}

	// Shrinking status
	wb.LerpSizeBetween(200.0, 50.0, 5)
	if wb.Status() != BorderStatusShrinking || wb.Status().Color() != 0xFF3030 {
		t.Fatalf("shrinking status=%v color=0x%x", wb.Status(), wb.Status().Color())
	}
}

func TestWorldBorderCommandsAndBroadcasts(t *testing.T) {
	srv := newSurvivalTestServer(t)

	var (
		mu      sync.Mutex
		pktIDs  []int32
		lastPkt []byte
	)
	sender := PacketSender(func(id int32, body []byte) error {
		mu.Lock()
		pktIDs = append(pktIDs, id)
		cp := make([]byte, len(body))
		copy(cp, body)
		lastPkt = cp
		mu.Unlock()
		return nil
	})
	player, err := srv.RegisterPlayer(Profile{UUID: OfflineUUID("BorderAdmin"), Name: "BorderAdmin"}, sender)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	srv.SetPlayerTransform(player, 10, 64, -20, 0, 0, true)

	// /worldborder set 100
	reply := srv.HandleWorldBorderCommand(player, []string{"set", "100"})
	if !strings.Contains(reply, "100.0") {
		t.Fatalf("set reply = %q", reply)
	}
	mu.Lock()
	gotID := pktIDs[len(pktIDs)-1]
	mu.Unlock()
	if gotID != protocol.PlaySetBorderSize {
		t.Fatalf("expected PlaySetBorderSize (0x%x), got 0x%x", protocol.PlaySetBorderSize, gotID)
	}

	// /worldborder get
	reply = srv.HandleWorldBorderCommand(player, []string{"get"})
	if !strings.Contains(reply, "100") {
		t.Fatalf("get reply = %q", reply)
	}

	// /worldborder add 50 5 (5 seconds = 100 ticks)
	reply = srv.HandleWorldBorderCommand(player, []string{"add", "50", "5"})
	if !strings.Contains(reply, "Growing") || !strings.Contains(reply, "150.0") {
		t.Fatalf("add lerp reply = %q", reply)
	}
	mu.Lock()
	gotID = pktIDs[len(pktIDs)-1]
	oldS, newS, lerpT, decErr := DecodeSetBorderLerpSizePacket(lastPkt)
	mu.Unlock()
	if gotID != protocol.PlaySetBorderLerpSize || decErr != nil || oldS != 100.0 || newS != 150.0 || lerpT != 100 {
		t.Fatalf("lerp packet: id=0x%x err=%v old=%v new=%v lerp=%d", gotID, decErr, oldS, newS, lerpT)
	}

	// /worldborder center ~5 ~10
	reply = srv.HandleWorldBorderCommand(player, []string{"center", "~5", "~10"})
	if !strings.Contains(reply, "15.00") || !strings.Contains(reply, "-10.00") {
		t.Fatalf("center reply = %q", reply)
	}
	if srv.WorldBorder().CenterX() != 15 || srv.WorldBorder().CenterZ() != -10 {
		t.Fatalf("center = (%v, %v), want (15, -10)", srv.WorldBorder().CenterX(), srv.WorldBorder().CenterZ())
	}

	// /worldborder damage amount 0.5 & buffer 3.0
	reply = srv.HandleWorldBorderCommand(player, []string{"damage", "amount", "0.5"})
	if !strings.Contains(reply, "0.50") || srv.WorldBorder().DamagePerBlock() != 0.5 {
		t.Fatalf("damage amount reply = %q, val = %v", reply, srv.WorldBorder().DamagePerBlock())
	}
	reply = srv.HandleWorldBorderCommand(player, []string{"damage", "buffer", "3"})
	if !strings.Contains(reply, "3.00") || srv.WorldBorder().SafeZone() != 3.0 {
		t.Fatalf("damage buffer reply = %q, val = %v", reply, srv.WorldBorder().SafeZone())
	}

	// /worldborder warning distance 12 & time 20
	reply = srv.HandleWorldBorderCommand(player, []string{"warning", "distance", "12"})
	if !strings.Contains(reply, "12") || srv.WorldBorder().WarningBlocks() != 12 {
		t.Fatalf("warning distance reply = %q, val = %d", reply, srv.WorldBorder().WarningBlocks())
	}
	reply = srv.HandleWorldBorderCommand(player, []string{"warning", "time", "20"})
	if !strings.Contains(reply, "20") || srv.WorldBorder().WarningTime() != 20 {
		t.Fatalf("warning time reply = %q, val = %d", reply, srv.WorldBorder().WarningTime())
	}
}

func TestWorldBorderOutsideDamageAndClamping(t *testing.T) {
	srv := newSurvivalTestServer(t)
	var chatMsgs []string
	sender := PacketSender(func(id int32, body []byte) error {
		if id == protocol.PlayPlayerCombatKill {
			r := protocol.NewReader(body)
			_, _ = r.VarInt()
			chatMsgs = append(chatMsgs, string(body))
		}
		return nil
	})
	player, err := srv.RegisterPlayer(Profile{UUID: OfflineUUID("Runner"), Name: "Runner"}, sender)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	srv.SetPlayerGameMode(player, GameModeSurvival)

	wb := srv.WorldBorder()
	wb.SetCenter(0, 0)
	wb.SetSize(20) // [-10, 10]
	wb.SetSafeZone(5.0)
	wb.SetDamagePerBlock(0.2)

	// Inside border (x=5) -> 0 damage
	srv.SetPlayerTransform(player, 5, 64, 0, 0, 0, true)
	srv.TickWorldBorder()
	if player.Health() != 20 {
		t.Fatalf("health inside border = %v, want 20", player.Health())
	}

	// Outside border but inside safeZone (x=14 -> dist=-4, +safeZone=+1) -> 0 damage
	srv.SetPlayerTransform(player, 14, 64, 0, 0, 0, true)
	srv.TickWorldBorder()
	if player.Health() != 20 {
		t.Fatalf("health inside safeZone = %v, want 20", player.Health())
	}

	// Outside border beyond safeZone (x=25 -> dist=-15, +safeZone=-10, floor(10*0.2)=2.0 damage)
	srv.SetPlayerTransform(player, 25, 64, 0, 0, 0, true)
	srv.TickWorldBorder()
	if player.Health() != 18 {
		t.Fatalf("health after first outside tick = %v, want 18", player.Health())
	}

	// Ticks 1..19 should be within the 20-tick hurt immunity interval; tick 20 deals damage again.
	for i := 0; i < 19; i++ {
		srv.TickWorldBorder()
	}
	if player.Health() != 18 {
		t.Fatalf("health during hurt interval = %v, want 18", player.Health())
	}
	srv.TickWorldBorder()
	if player.Health() != 16 {
		t.Fatalf("health after 20 ticks = %v, want 16", player.Health())
	}

	// EnforceWorldBorderMovement clamps walking from inside (9, 0) to outside (12, 0).
	nx, nz, clamped := srv.EnforceWorldBorderMovement(player, 9, 0, 12, 64, 0)
	if !clamped || nx >= 10.0 || nz != 0 {
		t.Fatalf("EnforceWorldBorderMovement = (%v, %v, %v)", nx, nz, clamped)
	}

	// ClampPlayerToWorldBorder teleports an outside player back inside bounds.
	if !srv.ClampPlayerToWorldBorder(player) {
		t.Fatal("expected ClampPlayerToWorldBorder to return true")
	}
	snap := player.Snapshot()
	if !wb.IsWithinBounds(snap.X, snap.Z) {
		t.Fatalf("clamped position (%v, %v) is not within bounds", snap.X, snap.Z)
	}
}

func TestWorldBorderPersistenceRoundTrip(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()
	cfg.WorldDir = dir

	srv1, err := New(cfg)
	if err != nil {
		t.Fatalf("New srv1: %v", err)
	}
	wb1 := srv1.WorldBorder()
	wb1.SetCenter(128.5, -256.25)
	wb1.LerpSizeBetween(500.0, 250.0, 600)
	wb1.SetDamagePerBlock(0.75)
	wb1.SetSafeZone(8.5)
	wb1.SetWarningBlocks(15)
	wb1.SetWarningTime(30)

	if err := srv1.SaveWorldBorderFile(); err != nil {
		t.Fatalf("SaveWorldBorderFile: %v", err)
	}

	srv2, err := New(cfg)
	if err != nil {
		t.Fatalf("New srv2: %v", err)
	}

	wb2 := srv2.WorldBorder()
	if wb2.CenterX() != 128.5 || wb2.CenterZ() != -256.25 {
		t.Fatalf("loaded center = (%v, %v), want (128.5, -256.25)", wb2.CenterX(), wb2.CenterZ())
	}
	if wb2.Size() != 500.0 || wb2.LerpTarget() != 250.0 || wb2.LerpTime() != 600 {
		t.Fatalf("loaded size/lerp = (%v -> %v in %d), want (500 -> 250 in 600)", wb2.Size(), wb2.LerpTarget(), wb2.LerpTime())
	}
	if wb2.DamagePerBlock() != 0.75 || wb2.SafeZone() != 8.5 {
		t.Fatalf("loaded damage/safeZone = (%v, %v), want (0.75, 8.5)", wb2.DamagePerBlock(), wb2.SafeZone())
	}
	if wb2.WarningBlocks() != 15 || wb2.WarningTime() != 30 {
		t.Fatalf("loaded warning blocks/time = (%d, %d), want (15, 30)", wb2.WarningBlocks(), wb2.WarningTime())
	}
}
