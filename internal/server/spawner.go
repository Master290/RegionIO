package server

import (
	"context"
	"encoding/binary"
	"log/slog"
	"math"
	"math/rand"
	"sync"
	"time"

	"regionio/internal/protocol"
	"regionio/internal/registry"
	"regionio/internal/world"
)

const (
	// maxEntityTrackingChunks caps entity tracking radius in chunks (4 chunks = 64 blocks,
	// matching vanilla's default mob tracking range).
	maxEntityTrackingChunks = 4
	fixedPointScale         = 4096.0
)

var serverTrackers sync.Map // map[*Server]*EntityTracker

func (s *Server) entityTracker() *EntityTracker {
	if tr, ok := serverTrackers.Load(s); ok {
		return tr.(*EntityTracker)
	}
	actual, _ := serverTrackers.LoadOrStore(s, NewEntityTracker())
	return actual.(*EntityTracker)
}

// StartSpawning begins the entity tick and spawn loops. log matches
// Cache.StartAutosave's convention: these are background loops that have to be
// able to report a failure nobody is waiting on.
func (s *Server) StartSpawning(ctx context.Context, log *slog.Logger) {
	go s.entityTickLoop(ctx, log)
	go s.mobSpawnLoop(ctx)
}

func (s *Server) entityTickLoop(ctx context.Context, log *slog.Logger) {
	ticker := time.NewTicker(50 * time.Millisecond) // 20 TPS
	defer ticker.Stop()
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	tracker := s.entityTracker()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// The world clock rides this loop because it is the only one
			// already running at 20 TPS. It belongs on the single authoritative
			// tick the engine still needs.
			if gameTime, dayTime := s.advanceWorldTime(); gameTime%timeSyncTicks == 0 {
				s.Broadcast(protocol.PlaySetTime, encodeSetTime(gameTime, dayTime))
				if gameTime%timePersistTicks == 0 {
					if err := s.SaveWorldTime(); err != nil && log != nil {
						log.Warn("saving world clock", "err", err)
					}
				}
			}
			tracker.TickAndBroadcast(s, rng)
		}
	}
}

// TickEntities advances entity physics/AI by one tick and broadcasts visibility
// and delta-movement updates to connected players.
func (s *Server) TickEntities(rng *rand.Rand) {
	s.entityTracker().TickAndBroadcast(s, rng)
}

// BroadcastEntities synchronizes entity spawns, delta movements, teleports, and
// batched despawns to all players within each entity's tracking range.
func (s *Server) BroadcastEntities() {
	s.entityTracker().Sync(s)
}

type trackedEntity struct {
	id                  int32
	baseX, baseY, baseZ int64
	yaw, pitch          byte
	onGround            bool
	seenTick            uint64
}

type viewerEntityState struct {
	tracked  []trackedEntity
	index    map[int32]int
	seenTick uint64
}

type viewerTarget struct {
	uuid    [16]byte
	x, y, z float64
	cx, cz  int32
	radius  int32
	send    PacketSender
}

// EntityTracker maintains per-viewer entity visibility and fixed-point base
// coordinates so movement updates use compact delta packets, despawns are
// batched, and steady-state ticks avoid heap allocations.
type EntityTracker struct {
	mu             sync.Mutex
	viewers        map[[16]byte]*viewerEntityState
	viewerBuf      []viewerTarget
	allEntitiesBuf []world.Entity
	visibleBuf     []world.Entity
	removeBuf      []int32
	pktBuf         []byte
	tickID         uint64
}

// NewEntityTracker constructs an EntityTracker with preallocated scratch buffers.
func NewEntityTracker() *EntityTracker {
	return &EntityTracker{
		viewers:        make(map[[16]byte]*viewerEntityState),
		viewerBuf:      make([]viewerTarget, 0, 16),
		allEntitiesBuf: make([]world.Entity, 0, 64),
		visibleBuf:     make([]world.Entity, 0, 64),
		removeBuf:      make([]int32, 0, 32),
		pktBuf:         make([]byte, 0, 128),
	}
}

// TickAndBroadcast steps all entities in s and broadcasts resulting visibility
// and delta-movement packets.
func (t *EntityTracker) TickAndBroadcast(s *Server, rng *rand.Rand) {
	t.mu.Lock()
	t.stepEntitiesLocked(s, rng)
	t.syncLocked(s)
	t.mu.Unlock()
}

// Sync performs one visibility and movement broadcast pass across all players.
func (t *EntityTracker) Sync(s *Server) {
	t.mu.Lock()
	t.syncLocked(s)
	t.mu.Unlock()
}

func (t *EntityTracker) stepEntitiesLocked(s *Server, rng *rand.Rand) {
	t.viewerBuf = s.appendViewerTargets(t.viewerBuf[:0])
	t.allEntitiesBuf = s.entities.AppendAll(t.allEntitiesBuf[:0])

	for i := range t.allEntitiesBuf {
		snapshot := t.allEntitiesBuf[i]
		yBelow := int(math.Floor(snapshot.Y - 0.1))
		blockBelow := s.chunks.GetBlock(int(math.Floor(snapshot.X)), yBelow, int(math.Floor(snapshot.Z)))
		nearestX, nearestZ, hasPlayer := nearestViewerXZ(t.viewerBuf, snapshot.X, snapshot.Y, snapshot.Z)

		s.entities.Update(snapshot.ID, func(e *world.Entity) {
			if blockBelow == world.StateAir || blockBelow == world.StateWater {
				e.OnGround = false
				e.VelocityY -= 80
				if e.VelocityY < -3000 {
					e.VelocityY = -3000
				}
			} else {
				e.OnGround = true
				e.VelocityY = 0
				e.Y = float64(yBelow + 1)

				if hasPlayer && e.TypeName == "minecraft:zombie" {
					dx := nearestX - e.X
					dz := nearestZ - e.Z
					dist := math.Sqrt(dx*dx + dz*dz)
					if dist > 1.0 && dist < 32.0 {
						e.X += (dx / dist) * 0.15
						e.Z += (dz / dist) * 0.15
						e.Yaw = float32(math.Atan2(-dx, dz) * (180 / math.Pi))
						e.HeadYaw = e.Yaw
					}
				} else if rng != nil {
					e.X += (rng.Float64() - 0.5) * 0.2
					e.Z += (rng.Float64() - 0.5) * 0.2
					e.Yaw += float32((rng.Float64() - 0.5) * 10.0)
					e.HeadYaw = e.Yaw
				}
			}

			if e.VelocityY != 0 {
				e.Y += float64(e.VelocityY) / 8000.0
			}
		})
	}
}

func nearestViewerXZ(viewers []viewerTarget, x, y, z float64) (nx, nz float64, ok bool) {
	minDist := -1.0
	for i := range viewers {
		v := &viewers[i]
		dx := v.x - x
		dy := v.y - y
		dz := v.z - z
		dist := dx*dx + dy*dy + dz*dz
		if minDist < 0 || dist < minDist {
			minDist = dist
			nx = v.x
			nz = v.z
			ok = true
		}
	}
	return nx, nz, ok
}

func (s *Server) appendViewerTargets(dst []viewerTarget) []viewerTarget {
	maxView := s.cfg.MaxViewDistance
	if maxView < 2 {
		maxView = 2
	}
	s.playersMu.RLock()
	for _, session := range s.players {
		session.mu.RLock()
		pos := session.position
		vd := session.viewDist
		send := session.send
		uuid := session.Profile.UUID
		session.mu.RUnlock()

		radius := vd
		if radius < 2 {
			radius = 2
		}
		if radius > maxView {
			radius = maxView
		}
		if radius > maxEntityTrackingChunks {
			radius = maxEntityTrackingChunks
		}
		dst = append(dst, viewerTarget{
			uuid:   uuid,
			x:      pos[0],
			y:      pos[1],
			z:      pos[2],
			cx:     int32(int64(math.Floor(pos[0])) >> 4),
			cz:     int32(int64(math.Floor(pos[2])) >> 4),
			radius: int32(radius),
			send:   send,
		})
	}
	s.playersMu.RUnlock()
	return dst
}

func (t *EntityTracker) syncLocked(s *Server) {
	t.tickID++
	tickID := t.tickID
	t.viewerBuf = s.appendViewerTargets(t.viewerBuf[:0])

	for i := range t.viewerBuf {
		viewer := &t.viewerBuf[i]
		state := t.viewers[viewer.uuid]
		if state == nil {
			state = &viewerEntityState{
				tracked: make([]trackedEntity, 0, 16),
				index:   make(map[int32]int, 16),
			}
			t.viewers[viewer.uuid] = state
		}
		state.seenTick = tickID

		// Query only the chunks within the viewer's tracking radius.
		t.visibleBuf = s.entities.AppendInChunkRange(t.visibleBuf[:0], viewer.cx, viewer.cz, viewer.radius)

		for j := range t.visibleBuf {
			ent := &t.visibleBuf[j]
			curX := encodeFixedCoord(ent.X)
			curY := encodeFixedCoord(ent.Y)
			curZ := encodeFixedCoord(ent.Z)
			curYaw := encodeAngleByte(ent.Yaw)
			curPitch := encodeAngleByte(ent.Pitch)

			idx, exists := state.index[ent.ID]
			if !exists {
				state.index[ent.ID] = len(state.tracked)
				state.tracked = append(state.tracked, trackedEntity{
					id:       ent.ID,
					baseX:    curX,
					baseY:    curY,
					baseZ:    curZ,
					yaw:      curYaw,
					pitch:    curPitch,
					onGround: ent.OnGround,
					seenTick: tickID,
				})
				if viewer.send != nil {
					t.pktBuf = encodeAddEntityPacket(t.pktBuf[:0], ent)
					_ = viewer.send(protocol.PlayAddEntity, t.pktBuf)
				}
				continue
			}

			tr := &state.tracked[idx]
			tr.seenTick = tickID
			dx := curX - tr.baseX
			dy := curY - tr.baseY
			dz := curZ - tr.baseZ
			moved := dx != 0 || dy != 0 || dz != 0
			rotated := curYaw != tr.yaw || curPitch != tr.pitch
			groundChanged := ent.OnGround != tr.onGround

			if !moved && !rotated && !groundChanged {
				continue
			}

			if dx < math.MinInt16 || dx > math.MaxInt16 ||
				dy < math.MinInt16 || dy > math.MaxInt16 ||
				dz < math.MinInt16 || dz > math.MaxInt16 {
				tr.baseX = curX
				tr.baseY = curY
				tr.baseZ = curZ
				tr.yaw = curYaw
				tr.pitch = curPitch
				tr.onGround = ent.OnGround
				if viewer.send != nil {
					t.pktBuf = encodeTeleportEntityPacket(t.pktBuf[:0], ent)
					_ = viewer.send(protocol.PlayTeleportEntity, t.pktBuf)
				}
				continue
			}

			if moved && rotated {
				tr.baseX = curX
				tr.baseY = curY
				tr.baseZ = curZ
				tr.yaw = curYaw
				tr.pitch = curPitch
				tr.onGround = ent.OnGround
				if viewer.send != nil {
					t.pktBuf = encodeMovePosRotPacket(t.pktBuf[:0], ent.ID, int16(dx), int16(dy), int16(dz), curYaw, curPitch, ent.OnGround)
					_ = viewer.send(protocol.PlayMoveEntityPosRot, t.pktBuf)
				}
			} else if moved {
				tr.baseX = curX
				tr.baseY = curY
				tr.baseZ = curZ
				tr.onGround = ent.OnGround
				if viewer.send != nil {
					t.pktBuf = encodeMovePosPacket(t.pktBuf[:0], ent.ID, int16(dx), int16(dy), int16(dz), ent.OnGround)
					_ = viewer.send(protocol.PlayMoveEntityPos, t.pktBuf)
				}
			} else {
				tr.yaw = curYaw
				tr.pitch = curPitch
				tr.onGround = ent.OnGround
				if viewer.send != nil {
					t.pktBuf = encodeMoveRotPacket(t.pktBuf[:0], ent.ID, curYaw, curPitch, ent.OnGround)
					_ = viewer.send(protocol.PlayMoveEntityRot, t.pktBuf)
				}
			}
		}

		// Collect all entities that left this viewer's range or were despawned,
		// and send a single batched PlayRemoveEntities packet.
		t.removeBuf = t.removeBuf[:0]
		for k := 0; k < len(state.tracked); {
			if state.tracked[k].seenTick == tickID {
				k++
				continue
			}
			removedID := state.tracked[k].id
			t.removeBuf = append(t.removeBuf, removedID)
			delete(state.index, removedID)
			last := len(state.tracked) - 1
			if k != last {
				movedEntry := state.tracked[last]
				state.tracked[k] = movedEntry
				state.index[movedEntry.id] = k
			}
			state.tracked = state.tracked[:last]
		}
		if len(t.removeBuf) > 0 && viewer.send != nil {
			t.pktBuf = encodeRemoveEntitiesPacket(t.pktBuf[:0], t.removeBuf)
			_ = viewer.send(protocol.PlayRemoveEntities, t.pktBuf)
		}
	}

	// Prune disconnected viewers so tracking state does not leak.
	for uuid, state := range t.viewers {
		if state.seenTick != tickID {
			delete(t.viewers, uuid)
		}
	}
}

func encodeFixedCoord(v float64) int64 {
	return int64(math.Round(v * fixedPointScale))
}

func encodeAngleByte(deg float32) byte {
	return byte(deg * 256.0 / 360.0)
}

func appendBoolByte(dst []byte, v bool) []byte {
	if v {
		return append(dst, 1)
	}
	return append(dst, 0)
}

func encodeAddEntityPacket(dst []byte, e *world.Entity) []byte {
	dst = protocol.AppendVarInt(dst, e.ID)
	dst = append(dst, e.UUID[:]...)
	dst = protocol.AppendVarInt(dst, int32(e.TypeID))
	dst = binary.BigEndian.AppendUint64(dst, math.Float64bits(e.X))
	dst = binary.BigEndian.AppendUint64(dst, math.Float64bits(e.Y))
	dst = binary.BigEndian.AppendUint64(dst, math.Float64bits(e.Z))
	if e.VelocityX == 0 && e.VelocityY == 0 && e.VelocityZ == 0 {
		dst = append(dst, 0)
	} else {
		var w protocol.Writer
		w.LPVec3(
			float64(e.VelocityX)/8000.0,
			float64(e.VelocityY)/8000.0,
			float64(e.VelocityZ)/8000.0,
		)
		dst = append(dst, w.Bytes()...)
	}
	dst = append(dst,
		encodeAngleByte(e.Pitch),
		encodeAngleByte(e.Yaw),
		encodeAngleByte(e.HeadYaw),
	)
	dst = protocol.AppendVarInt(dst, 0)
	return dst
}

func encodeMovePosPacket(dst []byte, id int32, dx, dy, dz int16, onGround bool) []byte {
	dst = protocol.AppendVarInt(dst, id)
	dst = binary.BigEndian.AppendUint16(dst, uint16(dx))
	dst = binary.BigEndian.AppendUint16(dst, uint16(dy))
	dst = binary.BigEndian.AppendUint16(dst, uint16(dz))
	return appendBoolByte(dst, onGround)
}

func encodeMovePosRotPacket(dst []byte, id int32, dx, dy, dz int16, yaw, pitch byte, onGround bool) []byte {
	dst = protocol.AppendVarInt(dst, id)
	dst = binary.BigEndian.AppendUint16(dst, uint16(dx))
	dst = binary.BigEndian.AppendUint16(dst, uint16(dy))
	dst = binary.BigEndian.AppendUint16(dst, uint16(dz))
	dst = append(dst, yaw, pitch)
	return appendBoolByte(dst, onGround)
}

func encodeMoveRotPacket(dst []byte, id int32, yaw, pitch byte, onGround bool) []byte {
	dst = protocol.AppendVarInt(dst, id)
	dst = append(dst, yaw, pitch)
	return appendBoolByte(dst, onGround)
}

func encodeTeleportEntityPacket(dst []byte, e *world.Entity) []byte {
	dst = protocol.AppendVarInt(dst, e.ID)
	dst = binary.BigEndian.AppendUint64(dst, math.Float64bits(e.X))
	dst = binary.BigEndian.AppendUint64(dst, math.Float64bits(e.Y))
	dst = binary.BigEndian.AppendUint64(dst, math.Float64bits(e.Z))
	dst = binary.BigEndian.AppendUint64(dst, math.Float64bits(float64(e.VelocityX)/8000.0))
	dst = binary.BigEndian.AppendUint64(dst, math.Float64bits(float64(e.VelocityY)/8000.0))
	dst = binary.BigEndian.AppendUint64(dst, math.Float64bits(float64(e.VelocityZ)/8000.0))
	dst = binary.BigEndian.AppendUint32(dst, math.Float32bits(e.Yaw))
	dst = binary.BigEndian.AppendUint32(dst, math.Float32bits(e.Pitch))
	dst = binary.BigEndian.AppendUint32(dst, 0)
	return appendBoolByte(dst, e.OnGround)
}

func encodeRemoveEntitiesPacket(dst []byte, ids []int32) []byte {
	dst = protocol.AppendVarInt(dst, int32(len(ids)))
	for _, id := range ids {
		dst = protocol.AppendVarInt(dst, id)
	}
	return dst
}

func (s *Server) mobSpawnLoop(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	pigType := registry.EntityTypeIndex("minecraft:pig")
	zombieType := registry.EntityTypeIndex("minecraft:zombie")
	if pigType < 0 || zombieType < 0 {
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if s.PlayerCount() == 0 || s.entities.Count() >= 20 {
				continue
			}
			s.spawnMobNearPlayer(rng, pigType, zombieType)
		}
	}
}

func (s *Server) spawnMobNearPlayer(rng *rand.Rand, pigType, zombieType int) bool {
	players := s.PlayerSnapshots()
	if len(players) == 0 {
		return false
	}
	player := players[rng.Intn(len(players))]
	angle := rng.Float64() * 2 * math.Pi
	distance := 16.0 + rng.Float64()*16.0
	x := int(math.Floor(player.X + math.Cos(angle)*distance))
	z := int(math.Floor(player.Z + math.Sin(angle)*distance))
	y, ok := s.chunks.SafeSpawnY(x, z)
	if !ok {
		return false
	}

	typeID := pigType
	typeName := "minecraft:pig"
	if rng.Float32() < 0.5 {
		typeID = zombieType
		typeName = "minecraft:zombie"
	}
	s.entities.Add(&world.Entity{
		TypeID:   typeID,
		TypeName: typeName,
		X:        float64(x) + 0.5,
		Y:        float64(y),
		Z:        float64(z) + 0.5,
		OnGround: true,
	})
	return true
}
