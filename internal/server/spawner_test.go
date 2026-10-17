package server

import (
	"math"
	"math/rand"
	"testing"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

type capturedPacket struct {
	id   int32
	body []byte
}

type packetRecorder struct {
	packets []capturedPacket
}

func (r *packetRecorder) send(id int32, body []byte) error {
	cp := make([]byte, len(body))
	copy(cp, body)
	r.packets = append(r.packets, capturedPacket{id: id, body: cp})
	return nil
}

func (r *packetRecorder) take() []capturedPacket {
	out := r.packets
	r.packets = nil
	return out
}

func TestSpawnMobNearPlayerUsesSurface(t *testing.T) {
	cfg := DefaultConfig()
	cfg.WorldDir = ""
	srv, err := NewWithCache(cfg, world.NewCache(-1, world.GenerateFlat))
	if err != nil {
		t.Fatal(err)
	}
	session, err := srv.RegisterPlayer(Profile{Name: "Alice", UUID: OfflineUUID("Alice")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	srv.SetPlayerTransform(session, 8.5, 80, 8.5, 0, 0, true)

	if !srv.spawnMobNearPlayer(rand.New(rand.NewSource(1)), 10, 20) {
		t.Fatal("spawnMobNearPlayer returned false")
	}
	entities := srv.Entities().All()
	if len(entities) != 1 {
		t.Fatalf("entities = %d, want 1", len(entities))
	}
	entity := entities[0]
	if entity.Y != world.FlatSurfaceY+1 {
		t.Fatalf("mob Y = %v, want surface Y %d", entity.Y, world.FlatSurfaceY+1)
	}
	distance := math.Hypot(entity.X-8.5, entity.Z-8.5)
	if distance < 15 || distance > 33 {
		t.Fatalf("mob distance = %v, want near player", distance)
	}
}

func TestEntityManagerSpatialChunkIndex(t *testing.T) {
	em := world.NewEntityManager()
	id1 := em.Add(&world.Entity{TypeID: 1, X: 4, Y: 70, Z: 4})     // chunk (0, 0)
	id2 := em.Add(&world.Entity{TypeID: 1, X: 20, Y: 70, Z: 4})    // chunk (1, 0)
	id3 := em.Add(&world.Entity{TypeID: 1, X: 160, Y: 70, Z: 160}) // chunk (10, 10)

	if got := em.ChunkCount(0, 0); got != 1 {
		t.Fatalf("ChunkCount(0,0) = %d, want 1", got)
	}
	if got := em.ChunkCount(1, 0); got != 1 {
		t.Fatalf("ChunkCount(1,0) = %d, want 1", got)
	}

	near := em.AppendInChunkRange(nil, 0, 0, 1)
	if len(near) != 2 {
		t.Fatalf("AppendInChunkRange(0,0,r=1) count = %d, want 2", len(near))
	}

	// Move id1 from chunk (0, 0) to chunk (5, 5).
	em.Update(id1, func(e *world.Entity) {
		e.X = 84
		e.Z = 84
	})
	if got := em.ChunkCount(0, 0); got != 0 {
		t.Fatalf("ChunkCount(0,0) after cross-chunk move = %d, want 0", got)
	}
	if got := em.ChunkCount(5, 5); got != 1 {
		t.Fatalf("ChunkCount(5,5) after cross-chunk move = %d, want 1", got)
	}

	near = em.AppendInChunkRange(near[:0], 0, 0, 1)
	if len(near) != 1 || near[0].ID != id2 {
		t.Fatalf("AppendInChunkRange after move = %+v, want only id %d", near, id2)
	}

	em.Remove(id2)
	em.Remove(id3)
	if got := em.Count(); got != 1 {
		t.Fatalf("Count after removals = %d, want 1", got)
	}
}

func TestEntityBroadcastDeltaMoveRotateAndTeleport(t *testing.T) {
	cfg := DefaultConfig()
	cfg.WorldDir = ""
	cfg.MaxViewDistance = 4
	srv, err := NewWithCache(cfg, world.NewCache(-1, world.GenerateFlat))
	if err != nil {
		t.Fatal(err)
	}

	var nearRec, farRec packetRecorder
	nearSess, err := srv.RegisterPlayer(Profile{Name: "Alice", UUID: OfflineUUID("Alice")}, nearRec.send)
	if err != nil {
		t.Fatal(err)
	}
	farSess, err := srv.RegisterPlayer(Profile{Name: "Bob", UUID: OfflineUUID("Bob")}, farRec.send)
	if err != nil {
		t.Fatal(err)
	}
	srv.SetPlayerTransform(nearSess, 8, 80, 8, 0, 0, true)    // chunk (0, 0)
	srv.SetPlayerViewDistance(nearSess, 4)                    // radius 4 chunks
	srv.SetPlayerTransform(farSess, 200, 80, 200, 0, 0, true) // chunk (12, 12)
	srv.SetPlayerViewDistance(farSess, 4)

	entID := srv.Entities().Add(&world.Entity{
		TypeID:   77,
		TypeName: "minecraft:pig",
		X:        10.0,
		Y:        65.0,
		Z:        12.0,
		OnGround: true,
	})

	// 1. Initial sync: Alice receives PlayAddEntity; far player Bob receives nothing.
	srv.BroadcastEntities()
	pkts := nearRec.take()
	if len(pkts) != 1 || pkts[0].id != protocol.PlayAddEntity {
		t.Fatalf("initial packets = %v, want single PlayAddEntity", pkts)
	}
	if farPkts := farRec.take(); len(farPkts) != 0 {
		t.Fatalf("far player received %d packets, want 0", len(farPkts))
	}

	// 2. Stationary entity: 0 packets sent.
	srv.BroadcastEntities()
	if pkts := nearRec.take(); len(pkts) != 0 {
		t.Fatalf("stationary entity sent %d packets, want 0", len(pkts))
	}

	// 3. Small position-only move (+0.5, 0, -0.25) -> PlayMoveEntityPos (0x35).
	srv.Entities().Update(entID, func(e *world.Entity) {
		e.X = 10.5
		e.Z = 11.75
	})
	srv.BroadcastEntities()
	pkts = nearRec.take()
	if len(pkts) != 1 || pkts[0].id != protocol.PlayMoveEntityPos {
		t.Fatalf("position move packets = %v, want single PlayMoveEntityPos", pkts)
	}
	r := protocol.NewReader(pkts[0].body)
	if id, err := r.VarInt(); err != nil || id != entID {
		t.Fatalf("move entity id = %d (%v), want %d", id, err, entID)
	}
	dx, _ := r.Uint16()
	dy, _ := r.Uint16()
	dz, _ := r.Uint16()
	onGround, _ := r.Bool()
	if int16(dx) != 2048 || int16(dy) != 0 || int16(dz) != -1024 || !onGround {
		t.Fatalf("delta pos = (%d, %d, %d, onGround=%v), want (2048, 0, -1024, true)",
			int16(dx), int16(dy), int16(dz), onGround)
	}

	// 4. Rotation-only change (Yaw=90, Pitch=45) -> PlayMoveEntityRot (0x38).
	srv.Entities().Update(entID, func(e *world.Entity) {
		e.Yaw = 90
		e.Pitch = 45
	})
	srv.BroadcastEntities()
	pkts = nearRec.take()
	if len(pkts) != 1 || pkts[0].id != protocol.PlayMoveEntityRot {
		t.Fatalf("rotation packets = %v, want single PlayMoveEntityRot", pkts)
	}
	r = protocol.NewReader(pkts[0].body)
	_, _ = r.VarInt()
	yawByte, _ := r.ReadByte()
	pitchByte, _ := r.ReadByte()
	if yawByte != 64 || pitchByte != 32 {
		t.Fatalf("rot bytes = (%d, %d), want (64, 32)", yawByte, pitchByte)
	}

	// 5. Combined position + rotation change -> PlayMoveEntityPosRot (0x36).
	srv.Entities().Update(entID, func(e *world.Entity) {
		e.X = 10.75
		e.Y = 65.125
		e.Yaw = 180
		e.Pitch = 0
	})
	srv.BroadcastEntities()
	pkts = nearRec.take()
	if len(pkts) != 1 || pkts[0].id != protocol.PlayMoveEntityPosRot {
		t.Fatalf("pos+rot packets = %v, want single PlayMoveEntityPosRot", pkts)
	}
	r = protocol.NewReader(pkts[0].body)
	_, _ = r.VarInt()
	dx, _ = r.Uint16()
	dy, _ = r.Uint16()
	dz, _ = r.Uint16()
	yawByte, _ = r.ReadByte()
	pitchByte, _ = r.ReadByte()
	if int16(dx) != 1024 || int16(dy) != 512 || int16(dz) != 0 || yawByte != 128 || pitchByte != 0 {
		t.Fatalf("pos+rot = (%d, %d, %d, yaw=%d, pitch=%d)", int16(dx), int16(dy), int16(dz), yawByte, pitchByte)
	}

	// 6. Large displacement (>= 8 blocks, still within view distance) -> PlayTeleportEntity (0x7d).
	srv.Entities().Update(entID, func(e *world.Entity) {
		e.X = 24.0 // +13.25 blocks (> 8-block int16 delta limit), chunk (1, 0)
		e.Z = 20.0 // chunk (1, 1)
	})
	srv.BroadcastEntities()
	pkts = nearRec.take()
	if len(pkts) != 1 || pkts[0].id != protocol.PlayTeleportEntity {
		t.Fatalf("large displacement packets = %v, want single PlayTeleportEntity", pkts)
	}
	r = protocol.NewReader(pkts[0].body)
	if id, _ := r.VarInt(); id != entID {
		t.Fatalf("teleport id = %d, want %d", id, entID)
	}
	gotX, _ := r.Float64()
	gotY, _ := r.Float64()
	gotZ, _ := r.Float64()
	if gotX != 24.0 || gotY != 65.125 || gotZ != 20.0 {
		t.Fatalf("teleport coords = (%v, %v, %v), want (24, 65.125, 20)", gotX, gotY, gotZ)
	}
}

func TestEntityBroadcastSpawnDespawnOnChunkCrossingsAndBatchedRemove(t *testing.T) {
	cfg := DefaultConfig()
	cfg.WorldDir = ""
	cfg.MaxViewDistance = 2
	srv, err := NewWithCache(cfg, world.NewCache(-1, world.GenerateFlat))
	if err != nil {
		t.Fatal(err)
	}

	var rec packetRecorder
	sess, err := srv.RegisterPlayer(Profile{Name: "Alice", UUID: OfflineUUID("Alice")}, rec.send)
	if err != nil {
		t.Fatal(err)
	}
	srv.SetPlayerTransform(sess, 8, 80, 8, 0, 0, true) // chunk (0, 0)
	srv.SetPlayerViewDistance(sess, 2)                 // radius 2 chunks: [-2..2]

	idA := srv.Entities().Add(&world.Entity{TypeID: 1, X: 8, Y: 65, Z: 8, OnGround: true})    // chunk (0, 0)
	idB := srv.Entities().Add(&world.Entity{TypeID: 1, X: 24, Y: 65, Z: 8, OnGround: true})   // chunk (1, 0)
	idFar := srv.Entities().Add(&world.Entity{TypeID: 1, X: 88, Y: 65, Z: 8, OnGround: true}) // chunk (5, 0)

	// Initial sync: idA and idB spawn; idFar is out of range.
	srv.BroadcastEntities()
	pkts := rec.take()
	if len(pkts) != 2 || pkts[0].id != protocol.PlayAddEntity || pkts[1].id != protocol.PlayAddEntity {
		t.Fatalf("initial spawn count = %d, want 2 PlayAddEntity packets", len(pkts))
	}

	// Both idA and idB move outside chunk radius 2 on the same tick -> single batched PlayRemoveEntities!
	srv.Entities().Update(idA, func(e *world.Entity) { e.X = 70 }) // chunk (4, 0)
	srv.Entities().Update(idB, func(e *world.Entity) { e.X = 72 }) // chunk (4, 0)
	srv.BroadcastEntities()
	pkts = rec.take()
	if len(pkts) != 1 || pkts[0].id != protocol.PlayRemoveEntities {
		t.Fatalf("despawn packets = %v, want 1 batched PlayRemoveEntities", pkts)
	}
	r := protocol.NewReader(pkts[0].body)
	count, err := r.VarInt()
	if err != nil || count != 2 {
		t.Fatalf("batched remove count = %d (%v), want 2", count, err)
	}
	rem1, _ := r.VarInt()
	rem2, _ := r.VarInt()
	if (rem1 != idA || rem2 != idB) && (rem1 != idB || rem2 != idA) {
		t.Fatalf("removed IDs = [%d, %d], want [%d, %d]", rem1, rem2, idA, idB)
	}

	// Now Alice moves to chunk (4, 0) -> idA (chunk 4, 0), idB (chunk 4, 0), and idFar (chunk 5, 0) are all within radius 2!
	srv.SetPlayerTransform(sess, 68, 80, 8, 0, 0, true) // chunk (4, 0)
	srv.BroadcastEntities()
	pkts = rec.take()
	if len(pkts) != 3 {
		t.Fatalf("spawn packets after player chunk move = %d, want 3 (idA, idB, idFar)", len(pkts))
	}
	for i, p := range pkts {
		if p.id != protocol.PlayAddEntity {
			t.Fatalf("pkt[%d].id = %#x, want PlayAddEntity", i, p.id)
		}
	}

	// Remove idFar from world -> Alice gets PlayRemoveEntities for idFar only; idA and idB stay tracked.
	srv.Entities().Remove(idFar)
	srv.BroadcastEntities()
	pkts = rec.take()
	if len(pkts) != 1 || pkts[0].id != protocol.PlayRemoveEntities {
		t.Fatalf("packets after entity removal = %v, want 1 PlayRemoveEntities", pkts)
	}
	r = protocol.NewReader(pkts[0].body)
	if c, _ := r.VarInt(); c != 1 {
		t.Fatalf("remove count = %d, want 1", c)
	}
	if gotID, _ := r.VarInt(); gotID != idFar {
		t.Fatalf("removed ID = %d, want %d", gotID, idFar)
	}
}

func BenchmarkEntityTrackerSync(b *testing.B) {
	cfg := DefaultConfig()
	cfg.WorldDir = ""
	cfg.MaxPlayers = 16
	cfg.MaxViewDistance = 4
	srv, err := NewWithCache(cfg, world.NewCache(-1, world.GenerateFlat))
	if err != nil {
		b.Fatal(err)
	}

	noopSend := func(int32, []byte) error { return nil }
	for i := 0; i < 8; i++ {
		name := string(rune('A'+i)) + "Player"
		sess, err := srv.RegisterPlayer(Profile{Name: name, UUID: OfflineUUID(name)}, noopSend)
		if err != nil {
			b.Fatal(err)
		}
		srv.SetPlayerTransform(sess, float64(i*16)+8, 80, 8, 0, 0, true)
		srv.SetPlayerViewDistance(sess, 4)
	}

	ids := make([]int32, 100)
	for i := range ids {
		ids[i] = srv.Entities().Add(&world.Entity{
			TypeID:   77,
			TypeName: "minecraft:pig",
			X:        float64((i%20)*8) + 4,
			Y:        65,
			Z:        float64((i/20)*8) + 4,
			OnGround: true,
		})
	}

	tracker := NewEntityTracker()
	tracker.Sync(srv) // warm up initial spawns and buffers

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		step := float64(i&1)*0.125 - 0.0625
		yaw := float32((i * 7) & 0xff)
		for _, id := range ids[:16] {
			srv.Entities().Update(id, func(e *world.Entity) {
				e.X += step
				e.Yaw = yaw
			})
		}
		tracker.Sync(srv)
	}
}
