package network

import (
	"log/slog"
	"math"
	"testing"

	"regionio/internal/protocol"
	"regionio/internal/server"
	"regionio/internal/world"
)

func TestValidPlayerName(t *testing.T) {
	for _, name := range []string{"Steve", "player_123", "A"} {
		if !validPlayerName(name) {
			t.Errorf("validPlayerName(%q) = false", name)
		}
	}
	for _, name := range []string{"", "seventeen_chars_1", "player-name", "имя"} {
		if validPlayerName(name) {
			t.Errorf("validPlayerName(%q) = true", name)
		}
	}
}

func TestPlayerMoveRejectsCoordinatesOutsideWorld(t *testing.T) {
	cfg := server.DefaultConfig()
	cfg.WorldDir = ""
	srv, err := server.NewWithCache(cfg, world.NewCache(-1, func(cx, cz int32) *world.Chunk {
		return world.GenerateFlat(cx, cz)
	}))
	if err != nil {
		t.Fatal(err)
	}
	session, err := srv.RegisterPlayer(server.Profile{Name: "Steve"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	h := &handler{srv: srv, session: session, log: slog.Default()}
	for _, position := range [][3]float64{
		{maxPlayerXZ + 1, 0, 0},
		{0, maxPlayerY + 1, 0},
		{0, 0, -maxPlayerXZ - 1},
		{math.NaN(), 0, 0},
	} {
		if err := h.onPlayerMove(position[0], position[1], position[2], 0, 0, true); err == nil {
			t.Errorf("accepted position %v", position)
		}
	}
}

func TestKeepAliveRequiresPendingMatchingID(t *testing.T) {
	h := &handler{log: slog.Default()}
	w := protocol.NewWriter(8).Int64(42)
	pkt := protocol.Packet{ID: protocol.PlayKeepAliveServer, Data: w.Bytes()}
	if err := h.handlePlay(pkt); err == nil {
		t.Fatal("accepted keep-alive response without a pending challenge")
	}
	h.keepAlivePending = true
	h.keepAliveID = 42
	if err := h.handlePlay(pkt); err != nil {
		t.Fatalf("matching response: %v", err)
	}
	if h.keepAlivePending {
		t.Fatal("matching response did not clear pending challenge")
	}
}
