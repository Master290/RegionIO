package network

import (
	"testing"

	"regionio/internal/protocol"
)

func TestSendDefaultSpawnPositionLayout(t *testing.T) {
	recorder := &recordingConn{}
	h := &handler{conn: NewConn(recorder)}

	if err := h.sendDefaultSpawnPosition(); err != nil {
		t.Fatal(err)
	}
	packets := recorder.take(t)
	if len(packets) != 1 {
		t.Fatalf("packet count = %d, want 1", len(packets))
	}
	if packets[0].ID != protocol.PlayDefaultSpawnPos {
		t.Fatalf("packet ID = %#x, want %#x", packets[0].ID, protocol.PlayDefaultSpawnPos)
	}

	r := protocol.NewReader(packets[0].Data)
	if dimension, err := r.String(); err != nil || dimension != "minecraft:overworld" {
		t.Fatalf("dimension = %q, %v; want minecraft:overworld", dimension, err)
	}
	if x, y, z, err := r.Position(); err != nil || x != 8 || y != 100 || z != 8 {
		t.Fatalf("position = (%d, %d, %d), %v; want (8, 100, 8)", x, y, z, err)
	}
	if yaw, err := r.Float32(); err != nil || yaw != 0 {
		t.Fatalf("yaw = %v, %v; want 0", yaw, err)
	}
	if pitch, err := r.Float32(); err != nil || pitch != 0 {
		t.Fatalf("pitch = %v, %v; want 0", pitch, err)
	}
	if remaining := r.Remaining(); remaining != 0 {
		t.Fatalf("remaining bytes = %d, want 0", remaining)
	}
}
