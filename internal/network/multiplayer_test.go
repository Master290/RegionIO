package network

import (
	"bufio"
	"bytes"
	"io"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"

	"regionio/internal/protocol"
	"regionio/internal/server"
	"regionio/internal/world"
)

type recordingConn struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (c *recordingConn) Read([]byte) (int, error)         { return 0, io.EOF }
func (c *recordingConn) Close() error                     { return nil }
func (c *recordingConn) LocalAddr() net.Addr              { return testAddr("local") }
func (c *recordingConn) RemoteAddr() net.Addr             { return testAddr("remote") }
func (c *recordingConn) SetDeadline(time.Time) error      { return nil }
func (c *recordingConn) SetReadDeadline(time.Time) error  { return nil }
func (c *recordingConn) SetWriteDeadline(time.Time) error { return nil }
func (c *recordingConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.Write(p)
}

func (c *recordingConn) take(t *testing.T) []protocol.Packet {
	t.Helper()
	c.mu.Lock()
	raw := append([]byte(nil), c.buf.Bytes()...)
	c.buf.Reset()
	c.mu.Unlock()

	reader := bytes.NewReader(raw)
	br := bufio.NewReader(reader)
	var packets []protocol.Packet
	for br.Buffered() > 0 || reader.Len() > 0 {
		pkt, err := protocol.ReadPacket(br, -1)
		if err != nil {
			t.Fatalf("decode recorded packet: %v", err)
		}
		packets = append(packets, pkt)
	}
	return packets
}

type testAddr string

func (a testAddr) Network() string { return "test" }
func (a testAddr) String() string  { return string(a) }

func TestIntegrationTwoPlayersShareBlockAndChatUpdates(t *testing.T) {
	cfg := server.DefaultConfig()
	cfg.WorldDir = ""
	cfg.MaxPlayers = 4
	cache := world.NewCache(-1, world.GenerateFlat)
	srv, err := server.NewWithCache(cfg, cache)
	if err != nil {
		t.Fatal(err)
	}

	raw1, raw2 := &recordingConn{}, &recordingConn{}
	h1 := &handler{conn: NewConn(raw1), srv: srv, log: slog.Default()}
	h2 := &handler{conn: NewConn(raw2), srv: srv, log: slog.Default()}
	h1.conn.Profile = server.Profile{Name: "Alice", UUID: server.OfflineUUID("Alice")}
	h2.conn.Profile = server.Profile{Name: "Bob", UUID: server.OfflineUUID("Bob")}
	h1.session, err = srv.RegisterPlayer(h1.conn.Profile, h1.conn.Send)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.UnregisterPlayer(h1.session)
	h2.session, err = srv.RegisterPlayer(h2.conn.Profile, h2.conn.Send)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.UnregisterPlayer(h2.session)

	action := protocol.NewWriter(24)
	action.VarInt(0).Position(2, world.FlatSurfaceY, 3).Byte(1).VarInt(17)
	if err := h1.handlePlayerAction(protocol.Packet{ID: protocol.PlayPlayerAction, Data: action.Bytes()}); err != nil {
		t.Fatal(err)
	}
	if got := cache.GetBlock(2, world.FlatSurfaceY, 3); got != world.StateAir {
		t.Fatalf("shared block state = %d, want air", got)
	}
	assertPacketIDs(t, raw1.take(t), protocol.PlayBlockUpdate, protocol.PlayLightUpdate, protocol.PlayBlockChangedAck)
	assertPacketIDs(t, raw2.take(t), protocol.PlayBlockUpdate, protocol.PlayLightUpdate)

	chat := protocol.NewWriter(16).String("hello")
	if err := h1.handleChat(protocol.Packet{ID: protocol.PlayChatMessage, Data: chat.Bytes()}); err != nil {
		t.Fatal(err)
	}
	assertPacketIDs(t, raw1.take(t), protocol.PlaySystemChat)
	assertPacketIDs(t, raw2.take(t), protocol.PlaySystemChat)
}

func TestBoundaryEditBroadcastsEveryChangedLightChunk(t *testing.T) {
	cfg := server.DefaultConfig()
	cfg.WorldDir = ""
	cache := world.NewCache(-1, func(cx, cz int32) *world.Chunk {
		return world.NewChunk(cx, cz, world.BiomePlains)
	})
	if _, err := cache.FrameErr(0, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.FrameErr(1, 0); err != nil {
		t.Fatal(err)
	}
	srv, err := server.NewWithCache(cfg, cache)
	if err != nil {
		t.Fatal(err)
	}
	recorder := &recordingConn{}
	h := &handler{conn: NewConn(recorder), srv: srv, log: slog.Default()}
	h.conn.Profile = server.Profile{Name: "Alice", UUID: server.OfflineUUID("Alice")}
	h.session, err = srv.RegisterPlayer(h.conn.Profile, h.conn.Send)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.UnregisterPlayer(h.session)
	srv.SetPlayerTransform(h.session, 15, 100, 8, 0, 0, true)
	srv.SetPlayerViewDistance(h.session, 2)

	valid, lightChunks := cache.SetBlockWithLight(15, 100, 8, world.StateGlowstone)
	if !valid || len(lightChunks) != 2 {
		t.Fatalf("boundary edit valid=%v light chunks=%v; want two", valid, lightChunks)
	}
	h.broadcastBlockUpdate(15, 100, 8, world.StateGlowstone, lightChunks)
	assertPacketIDs(t, recorder.take(t), protocol.PlayBlockUpdate, protocol.PlayLightUpdate, protocol.PlayLightUpdate)
}

func TestIntegrationFourClientsVisibilityMovementLeaveAndLight(t *testing.T) {
	cfg := server.DefaultConfig()
	cfg.WorldDir = ""
	cfg.MaxPlayers = 4
	cache := world.NewCache(-1, world.GenerateFlat)
	srv, err := server.NewWithCache(cfg, cache)
	if err != nil {
		t.Fatal(err)
	}

	names := []string{"Alice", "Bob", "Carol", "Dave"}
	positions := [][3]float64{{0, 80, 0}, {16, 80, 0}, {160, 80, 0}, {176, 80, 0}}
	handlers := make([]*handler, len(names))
	recorders := make([]*recordingConn, len(names))
	for i, name := range names {
		recorders[i] = &recordingConn{}
		h := &handler{conn: NewConn(recorders[i]), srv: srv, log: slog.Default(), viewDistance: 2}
		h.conn.Profile = server.Profile{Name: name, UUID: server.OfflineUUID(name)}
		h.session, err = srv.RegisterPlayer(h.conn.Profile, h.conn.Send)
		if err != nil {
			t.Fatalf("register %s: %v", name, err)
		}
		srv.SetPlayerTransform(h.session, positions[i][0], positions[i][1], positions[i][2], 0, 0, true)
		srv.SetPlayerViewDistance(h.session, h.viewDistance)
		handlers[i] = h
	}
	defer func() {
		for _, h := range handlers {
			srv.UnregisterPlayer(h.session)
		}
	}()

	mobID := srv.Entities().Add(&world.Entity{
		TypeID: 100, TypeName: "minecraft:pig", X: 8, Y: 80, Z: 8,
	})
	for _, h := range handlers {
		if err := h.syncVisibleEntities(); err != nil {
			t.Fatal(err)
		}
	}

	for i, recorder := range recorders {
		packets := recorder.take(t)
		if got := countPackets(packets, protocol.PlayPlayerInfoUpdate); got != 4 {
			t.Fatalf("client %s player-info count = %d, want 4", names[i], got)
		}
		wantAdds := 1
		if i < 2 {
			wantAdds = 2 // nearby player plus the pig
		}
		if got := countPackets(packets, protocol.PlayAddEntity); got != wantAdds {
			t.Fatalf("client %s add-entity count = %d, want %d", names[i], got, wantAdds)
		}
	}

	move := protocol.NewWriter(40)
	move.Float64(32).Float64(80).Float64(0).Float32(90).Float32(15).Byte(1)
	if err := handlers[1].handlePlay(protocol.Packet{ID: protocol.PlayMovePosRot, Data: move.Bytes()}); err != nil {
		t.Fatal(err)
	}
	if err := handlers[0].syncVisibleEntities(); err != nil {
		t.Fatal(err)
	}
	packets := recorders[0].take(t)
	if got := countPackets(packets, protocol.PlayTeleportEntity); got != 1 {
		t.Fatalf("nearby movement teleports = %d, want 1", got)
	}
	assertTeleportTransform(t, firstPacket(t, packets, protocol.PlayTeleportEntity), handlers[1].session.EntityID, 32, 80, 0, 90, 15, true)

	move = protocol.NewWriter(32)
	move.Float64(64).Float64(80).Float64(0).Byte(1)
	if err := handlers[1].handlePlay(protocol.Packet{ID: protocol.PlayMovePos, Data: move.Bytes()}); err != nil {
		t.Fatal(err)
	}
	if err := handlers[0].syncVisibleEntities(); err != nil {
		t.Fatal(err)
	}
	assertPacketIDs(t, recorders[0].take(t), protocol.PlayRemoveEntities)

	move = protocol.NewWriter(32)
	move.Float64(16).Float64(80).Float64(0).Byte(1)
	if err := handlers[1].handlePlay(protocol.Packet{ID: protocol.PlayMovePos, Data: move.Bytes()}); err != nil {
		t.Fatal(err)
	}
	if err := handlers[0].syncVisibleEntities(); err != nil {
		t.Fatal(err)
	}
	assertPacketIDs(t, recorders[0].take(t), protocol.PlayAddEntity)

	srv.UnregisterPlayer(handlers[1].session)
	if err := handlers[0].syncVisibleEntities(); err != nil {
		t.Fatal(err)
	}
	assertPacketIDs(t, recorders[0].take(t), protocol.PlayRemoveEntities, protocol.PlayPlayerInfoRemove)

	action := protocol.NewWriter(24)
	action.VarInt(0).Position(2, world.FlatSurfaceY, 3).Byte(1).VarInt(91)
	if err := handlers[0].handlePlayerAction(protocol.Packet{ID: protocol.PlayPlayerAction, Data: action.Bytes()}); err != nil {
		t.Fatal(err)
	}
	assertPacketIDs(t, recorders[0].take(t), protocol.PlayBlockUpdate, protocol.PlayLightUpdate, protocol.PlayBlockChangedAck)
	if packets := recorders[2].take(t); len(packets) != 0 {
		t.Fatalf("far client Carol received %d block/light packets", len(packets))
	}
	if packets := recorders[3].take(t); len(packets) != 0 {
		t.Fatalf("far client Dave received %d block/light packets", len(packets))
	}

	srv.Entities().Remove(mobID)
}

func countPackets(packets []protocol.Packet, id int32) int {
	count := 0
	for _, packet := range packets {
		if packet.ID == id {
			count++
		}
	}
	return count
}

func firstPacket(t *testing.T, packets []protocol.Packet, id int32) protocol.Packet {
	t.Helper()
	for _, packet := range packets {
		if packet.ID == id {
			return packet
		}
	}
	t.Fatalf("packet %#x not found", id)
	return protocol.Packet{}
}

func assertTeleportTransform(t *testing.T, packet protocol.Packet, entityID int32, x, y, z float64, yaw, pitch float32, onGround bool) {
	t.Helper()
	r := packet.Body()
	if got, err := r.VarInt(); err != nil || got != entityID {
		t.Fatalf("teleport entity = %d, %v; want %d", got, err, entityID)
	}
	for _, field := range []struct {
		name string
		want float64
	}{{"x", x}, {"y", y}, {"z", z}} {
		got, err := r.Float64()
		if err != nil || got != field.want {
			t.Fatalf("teleport %s = %v, %v; want %v", field.name, got, err, field.want)
		}
	}
	for i := 0; i < 3; i++ {
		if got, err := r.Float64(); err != nil || got != 0 {
			t.Fatalf("teleport velocity[%d] = %v, %v; want 0", i, got, err)
		}
	}
	if got, err := r.Float32(); err != nil || got != yaw {
		t.Fatalf("teleport yaw = %v, %v; want %v", got, err, yaw)
	}
	if got, err := r.Float32(); err != nil || got != pitch {
		t.Fatalf("teleport pitch = %v, %v; want %v", got, err, pitch)
	}
	if _, err := r.Int32(); err != nil {
		t.Fatal(err)
	}
	if got, err := r.Bool(); err != nil || got != onGround {
		t.Fatalf("teleport onGround = %v, %v; want %v", got, err, onGround)
	}
}

func assertPacketIDs(t *testing.T, packets []protocol.Packet, want ...int32) {
	t.Helper()
	if len(packets) != len(want) {
		ids := make([]int32, len(packets))
		for i := range packets {
			ids[i] = packets[i].ID
		}
		t.Fatalf("packet IDs = %v (count %d), want %v (count %d)", ids, len(packets), want, len(want))
	}
	for i := range want {
		if packets[i].ID != want[i] {
			t.Fatalf("packet[%d] id = %#x, want %#x", i, packets[i].ID, want[i])
		}
	}
}
