package network

import (
	"bufio"
	"bytes"
	"math"
	"net"
	"testing"

	"regionio/internal/protocol"
	"regionio/internal/server"
	"regionio/internal/world"
)

func readServerPacket(t *testing.T, c net.Conn) protocol.Packet {
	t.Helper()
	// net.Pipe has no buffering; read the frame from a goroutine-driven write.
	br := make([]byte, 4096)
	n, err := c.Read(br)
	if err != nil {
		t.Fatalf("reading packet: %v", err)
	}
	pkt, err := protocol.ReadPacket(bufio.NewReader(bytes.NewReader(br[:n])), -1)
	if err != nil {
		t.Fatalf("decoding packet: %v", err)
	}
	return pkt
}

func TestSendEntityTeleportUsesPositionMoveRotation(t *testing.T) {
	serverSide, clientSide := net.Pipe()
	defer serverSide.Close()
	defer clientSide.Close()

	h := &handler{conn: NewConn(serverSide)}
	ent := &world.Entity{
		ID:        42,
		X:         1.25,
		Y:         65.5,
		Z:         -3.75,
		Yaw:       90,
		Pitch:     15,
		VelocityX: 80,
		VelocityY: -160,
		VelocityZ: 240,
	}

	errc := make(chan error, 1)
	go func() { errc <- h.sendEntityTeleport(ent) }()

	pkt := readServerPacket(t, clientSide)
	if err := <-errc; err != nil {
		t.Fatalf("sendEntityTeleport: %v", err)
	}
	if pkt.ID != protocol.PlayTeleportEntity {
		t.Fatalf("packet id = %#x, want %#x", pkt.ID, protocol.PlayTeleportEntity)
	}

	r := pkt.Body()
	if id, err := r.VarInt(); err != nil || id != ent.ID {
		t.Fatalf("entity id = %d, %v; want %d", id, err, ent.ID)
	}
	coords := []struct {
		name string
		want float64
	}{
		{"x", ent.X},
		{"y", ent.Y},
		{"z", ent.Z},
	}
	for _, tc := range coords {
		got, err := r.Float64()
		if err != nil || got != tc.want {
			t.Fatalf("%s = %v, %v; want %v", tc.name, got, err, tc.want)
		}
	}
	velocities := []struct {
		name string
		want float64
	}{
		{"vx", float64(ent.VelocityX) / 8000.0},
		{"vy", float64(ent.VelocityY) / 8000.0},
		{"vz", float64(ent.VelocityZ) / 8000.0},
	}
	for _, tc := range velocities {
		got, err := r.Float64()
		if err != nil || got != tc.want {
			t.Fatalf("%s = %v, %v; want %v", tc.name, got, err, tc.want)
		}
	}
	if yaw, err := r.Float32(); err != nil || yaw != ent.Yaw {
		t.Fatalf("yaw = %v, %v; want %v", yaw, err, ent.Yaw)
	}
	if pitch, err := r.Float32(); err != nil || pitch != ent.Pitch {
		t.Fatalf("pitch = %v, %v; want %v", pitch, err, ent.Pitch)
	}
	if flags, err := r.Int32(); err != nil || flags != 0 {
		t.Fatalf("relative flags = %d, %v; want 0", flags, err)
	}
	if onGround, err := r.Bool(); err != nil || !onGround {
		t.Fatalf("onGround = %v, %v; want true", onGround, err)
	}
	if rem := r.Remaining(); rem != 0 {
		t.Fatalf("remaining bytes = %d, want 0", rem)
	}
}

func TestSendAddEntityLayout(t *testing.T) {
	serverSide, clientSide := net.Pipe()
	defer serverSide.Close()
	defer clientSide.Close()

	uuid := [16]byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10}
	h := &handler{conn: NewConn(serverSide)}
	ent := &world.Entity{
		ID:      43,
		UUID:    uuid,
		TypeID:  77,
		X:       10.5,
		Y:       66.25,
		Z:       -20.75,
		Pitch:   45,
		Yaw:     180,
		HeadYaw: 90,
	}

	errc := make(chan error, 1)
	go func() { errc <- h.sendAddEntity(ent) }()

	pkt := readServerPacket(t, clientSide)
	if err := <-errc; err != nil {
		t.Fatalf("sendAddEntity: %v", err)
	}
	if pkt.ID != protocol.PlayAddEntity {
		t.Fatalf("packet id = %#x, want %#x", pkt.ID, protocol.PlayAddEntity)
	}

	r := pkt.Body()
	if id, err := r.VarInt(); err != nil || id != ent.ID {
		t.Fatalf("entity id = %d, %v; want %d", id, err, ent.ID)
	}
	if got, err := r.UUID(); err != nil || got != uuid {
		t.Fatalf("uuid = %x, %v; want %x", got, err, uuid)
	}
	if typ, err := r.VarInt(); err != nil || typ != int32(ent.TypeID) {
		t.Fatalf("type id = %d, %v; want %d", typ, err, ent.TypeID)
	}
	coords := []struct {
		name string
		want float64
	}{
		{"x", ent.X},
		{"y", ent.Y},
		{"z", ent.Z},
	}
	for _, tc := range coords {
		got, err := r.Float64()
		if err != nil || got != tc.want {
			t.Fatalf("%s = %v, %v; want %v", tc.name, got, err, tc.want)
		}
	}
	vx, vy, vz, err := r.LPVec3()
	if err != nil {
		t.Fatalf("velocity: %v", err)
	}
	wantVelocity := []float64{
		float64(ent.VelocityX) / 8000.0,
		float64(ent.VelocityY) / 8000.0,
		float64(ent.VelocityZ) / 8000.0,
	}
	for i, got := range []float64{vx, vy, vz} {
		if math.Abs(got-wantVelocity[i]) > 1.0/16383.0 {
			t.Fatalf("velocity[%d] = %v, want %v", i, got, wantVelocity[i])
		}
	}
	angles := []struct {
		name string
		want byte
	}{
		{"pitch", byte(ent.Pitch * 256.0 / 360.0)},
		{"yaw", byte(ent.Yaw * 256.0 / 360.0)},
		{"headYaw", byte(ent.HeadYaw * 256.0 / 360.0)},
	}
	for _, tc := range angles {
		got, err := r.ReadByte()
		if err != nil || got != tc.want {
			t.Fatalf("%s = %d, %v; want %d", tc.name, got, err, tc.want)
		}
	}
	if data, err := r.VarInt(); err != nil || data != 0 {
		t.Fatalf("data = %d, %v; want 0", data, err)
	}
	if rem := r.Remaining(); rem != 0 {
		t.Fatalf("remaining bytes = %d, want 0", rem)
	}
}

func TestPlayerInfoPacketLayouts(t *testing.T) {
	serverSide, clientSide := net.Pipe()
	defer serverSide.Close()
	defer clientSide.Close()

	profile := server.Profile{Name: "Alice", UUID: server.OfflineUUID("Alice")}
	h := &handler{conn: NewConn(serverSide)}

	errCh := make(chan error, 1)
	go func() { errCh <- h.sendPlayerInfoAdd(profile) }()
	pkt := readServerPacket(t, clientSide)
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}
	if pkt.ID != protocol.PlayPlayerInfoUpdate {
		t.Fatalf("player info update id = %#x, want %#x", pkt.ID, protocol.PlayPlayerInfoUpdate)
	}
	r := pkt.Body()
	if actions, err := r.ReadByte(); err != nil || actions != 0xff {
		t.Fatalf("actions = %#x, %v; want 0xff", actions, err)
	}
	if count, err := r.VarInt(); err != nil || count != 1 {
		t.Fatalf("entry count = %d, %v; want 1", count, err)
	}
	if uuid, err := r.UUID(); err != nil || uuid != profile.UUID {
		t.Fatalf("profile UUID = %x, %v; want %x", uuid, err, profile.UUID)
	}
	if name, err := r.String(); err != nil || name != profile.Name {
		t.Fatalf("profile name = %q, %v; want %q", name, err, profile.Name)
	}
	if properties, err := r.VarInt(); err != nil || properties != 0 {
		t.Fatalf("properties = %d, %v; want 0", properties, err)
	}
	if chatSession, err := r.Bool(); err != nil || chatSession {
		t.Fatalf("chat session = %v, %v; want false", chatSession, err)
	}
	if gameMode, err := r.VarInt(); err != nil || gameMode != 1 {
		t.Fatalf("game mode = %d, %v; want 1", gameMode, err)
	}
	if listed, err := r.Bool(); err != nil || !listed {
		t.Fatalf("listed = %v, %v; want true", listed, err)
	}
	if latency, err := r.VarInt(); err != nil || latency != 0 {
		t.Fatalf("latency = %d, %v; want 0", latency, err)
	}
	if displayName, err := r.Bool(); err != nil || displayName {
		t.Fatalf("display name = %v, %v; want absent", displayName, err)
	}
	if order, err := r.VarInt(); err != nil || order != 0 {
		t.Fatalf("list order = %d, %v; want 0", order, err)
	}
	if showHat, err := r.Bool(); err != nil || !showHat {
		t.Fatalf("show hat = %v, %v; want true", showHat, err)
	}
	if r.Remaining() != 0 {
		t.Fatalf("player info update trailing bytes = %d", r.Remaining())
	}

	go func() { errCh <- h.sendPlayerInfoRemove(profile.UUID) }()
	pkt = readServerPacket(t, clientSide)
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}
	if pkt.ID != protocol.PlayPlayerInfoRemove {
		t.Fatalf("player info remove id = %#x, want %#x", pkt.ID, protocol.PlayPlayerInfoRemove)
	}
	r = pkt.Body()
	if count, err := r.VarInt(); err != nil || count != 1 {
		t.Fatalf("remove count = %d, %v; want 1", count, err)
	}
	if uuid, err := r.UUID(); err != nil || uuid != profile.UUID {
		t.Fatalf("removed UUID = %x, %v; want %x", uuid, err, profile.UUID)
	}
	if r.Remaining() != 0 {
		t.Fatalf("player info remove trailing bytes = %d", r.Remaining())
	}
}
