package network

import (
	"errors"
	"math"
	"time"

	"regionio/internal/nbt"
	"regionio/internal/protocol"
	"regionio/internal/registry"
	"regionio/internal/server"
	"regionio/internal/world"
)

// Spawn column. The feet-level Y is resolved from the generated surface when
// the player enters the play phase.
const (
	spawnX      = 8.5
	spawnZ      = 8.5
	maxPlayerXZ = 30_000_000.0
	maxPlayerY  = 20_000_000.0
)

// beginPlay sends the join sequence once the client enters the Play phase and
// hands chunk streaming off to the background streamer. The streamer stops when
// h.ctx (the connection lifetime context) is cancelled.
func (h *handler) beginPlay() error {
	session, err := h.srv.RegisterPlayer(h.conn.Profile, h.conn.Enqueue)
	if err != nil {
		return err
	}
	h.session = session
	spawnY, ok := h.srv.Chunks().SafeSpawnY(int(math.Floor(spawnX)), int(math.Floor(spawnZ)))
	if !ok {
		spawnY = world.SeaLevel + 1
	}
	h.spawnY = float64(spawnY)
	h.srv.SetPlayerTransform(session, spawnX, h.spawnY, spawnZ, 0, 0, true)
	h.srv.SetPlayerViewDistance(session, h.visibilityRadius())
	for i := range h.hotbar {
		h.hotbar[i] = -1 // empty
	}
	if err := h.sendPlayLogin(); err != nil {
		return err
	}
	// "Start waiting for level chunks": tells the client to show the loading
	// screen until chunks arrive.
	if err := h.sendGameEvent(protocol.GameEventStartWaitingChunks, 0); err != nil {
		return err
	}
	if err := h.sendPlayerPosition(1); err != nil {
		return err
	}
	if err := h.sendChunkCacheCenter(0, 0); err != nil {
		return err
	}
	if err := h.sendDefaultSpawnPosition(); err != nil {
		return err
	}
	if err := h.sendPlayerAbilities(); err != nil {
		return err
	}
	// The client's sky stays where it started until it is told the time.
	if err := h.conn.Send(protocol.PlaySetTime, h.srv.SetTimePacket()); err != nil {
		return err
	}
	// Launch the background chunk streamer. It owns generation + sending so the
	// read loop stays free; requestRecenter is a non-blocking push.
	h.streamer = newStreamer(h.srv.Chunks(), h.conn, h.log, h.visibilityRadius())
	go h.streamer.run(h.ctx)
	h.streamer.requestRecenter(0, 0)
	go h.keepAliveLoop()
	go h.entitySyncLoop()
	return nil
}

func (h *handler) sendChunkCacheCenter(cx, cz int32) error {
	w := protocol.NewWriter(8)
	w.VarInt(cx)
	w.VarInt(cz)
	return h.conn.SendWriter(protocol.PlayChunkCacheCenter, w)
}

func (h *handler) sendDefaultSpawnPosition() error {
	// 26.1.2 wraps the spawn in LevelData.RespawnData: GlobalPos followed by
	// yaw and pitch. GlobalPos starts with the dimension resource key.
	w := protocol.NewWriter(40)
	w.String("minecraft:overworld")
	w.Position(8, int(math.Floor(h.spawnY)), 8)
	w.Float32(0.0)
	w.Float32(0.0)
	return h.conn.SendWriter(protocol.PlayDefaultSpawnPos, w)
}

func (h *handler) sendPlayerAbilities() error {
	w := protocol.NewWriter(9)
	w.Byte(0x0F)
	w.Float32(0.05)
	w.Float32(0.1)
	return h.conn.SendWriter(protocol.PlayAbilities, w)
}

// onPlayerMove recenters the streamer when the player crosses into a new chunk.
// It is a non-blocking push; the read loop never waits on generation.
func (h *handler) onPlayerMove(x, y, z float64, yaw, pitch float32, onGround bool) error {
	if math.IsNaN(x) || math.IsNaN(y) || math.IsNaN(z) ||
		math.IsInf(x, 0) || math.IsInf(y, 0) || math.IsInf(z, 0) ||
		math.IsNaN(float64(yaw)) || math.IsNaN(float64(pitch)) ||
		math.IsInf(float64(yaw), 0) || math.IsInf(float64(pitch), 0) {
		return errors.New("invalid player position")
	}
	if math.Abs(x) > maxPlayerXZ || math.Abs(z) > maxPlayerXZ || math.Abs(y) > maxPlayerY {
		return errors.New("player position outside world bounds")
	}
	h.srv.SetPlayerTransform(h.session, x, y, z, yaw, pitch, onGround)
	cx := int32(int64(math.Floor(x)) >> 4)
	cz := int32(int64(math.Floor(z)) >> 4)
	if h.streamer != nil {
		h.streamer.requestRecenter(cx, cz)
	}
	return nil
}

func (h *handler) visibilityRadius() int {
	distance := h.viewDistance
	if distance < 2 {
		distance = defaultViewRadius
	}
	if max := h.srv.Config().MaxViewDistance; distance > max {
		distance = max
	}
	return distance
}

// sendPlayLogin writes the clientbound play "login" packet. Field layout was
// confirmed against the 26.1.2 vanilla server capture.
func (h *handler) sendPlayLogin() error {
	dimTypeIdx := registry.Index("minecraft:dimension_type", "minecraft:overworld")
	if dimTypeIdx < 0 {
		dimTypeIdx = 0
	}

	w := protocol.NewWriter(128)
	entityID := int32(1)
	if h.session != nil {
		entityID = h.session.EntityID
	}
	w.Int32(entityID) // entity ID
	w.Bool(false)     // is hardcore

	// Dimension names: the worlds available on this server.
	dims := []string{"minecraft:overworld", "minecraft:the_end", "minecraft:the_nether"}
	w.VarInt(int32(len(dims)))
	for _, d := range dims {
		w.String(d)
	}

	w.VarInt(int32(h.srv.Config().MaxPlayers)) // max players (legacy)
	w.VarInt(int32(h.visibilityRadius()))      // view distance
	w.VarInt(int32(h.visibilityRadius()))      // simulation distance
	w.Bool(false)                              // reduced debug info
	w.Bool(true)                               // enable respawn screen
	w.Bool(false)                              // do limited crafting

	w.VarInt(int32(dimTypeIdx))     // dimension type (registry index)
	w.String("minecraft:overworld") // dimension name (this world)
	w.Int64(0)                      // hashed seed
	w.Byte(1)                       // game mode: creative (instant break, creative inventory)
	w.Byte(0xFF)                    // previous game mode: -1 (none)
	w.Bool(false)                   // is debug
	w.Bool(false)                   // is flat
	w.Bool(false)                   // has death location
	w.VarInt(0)                     // portal cooldown
	w.VarInt(63)                    // sea level (overworld)
	w.Bool(false)                   // enforces secure chat

	return h.conn.SendWriter(protocol.PlayLogin, w)
}

// sendGameEvent writes a game_event packet (event id + float value).
func (h *handler) sendGameEvent(event byte, value float32) error {
	w := protocol.NewWriter(5)
	w.Byte(event)
	w.Float32(value)
	return h.conn.SendWriter(protocol.PlayGameEvent, w)
}

// sendPlayerPosition teleports the player to spawn. The client must echo the
// teleport ID back via accept_teleportation.
func (h *handler) sendPlayerPosition(teleportID int32) error {
	w := protocol.NewWriter(64)
	w.VarInt(teleportID)
	w.Float64(spawnX).Float64(h.spawnY).Float64(spawnZ) // position
	w.Float64(0).Float64(0).Float64(0)                  // velocity
	w.Float32(0)                                        // yaw
	w.Float32(0)                                        // pitch
	w.Int32(0)                                          // relative flags
	return h.conn.SendWriter(protocol.PlayPlayerPosition, w)
}

// keepAliveLoop sends a keep-alive every 15 seconds. It exits as soon as a send
// fails, which happens when the connection closes.
func (h *handler) keepAliveLoop() {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-h.ctx.Done():
			return
		case <-ticker.C:
			h.keepAliveMu.Lock()
			if h.keepAlivePending {
				timedOut := time.Since(h.keepAliveSent) >= 30*time.Second
				h.keepAliveMu.Unlock()
				if timedOut {
					_ = h.conn.Close()
					return
				}
				continue
			}
			id := time.Now().UnixMilli()
			h.keepAlivePending = true
			h.keepAliveID = id
			h.keepAliveSent = time.Now()
			h.keepAliveMu.Unlock()
			w := protocol.NewWriter(8)
			w.Int64(id)
			if err := h.conn.SendWriter(protocol.PlayKeepAliveCB, w); err != nil {
				return
			}
		}
	}
}

// visibleEntity is the comparable state retained by one client's visibility
// tracker. OnGround is separate because mobs currently always use true.
type visibleEntity struct {
	entity   world.Entity
	onGround bool
}

// entitySyncLoop maintains tab-list membership and chunk-scoped entity state.
func (h *handler) entitySyncLoop() {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-h.ctx.Done():
			return
		case <-ticker.C:
			if err := h.syncVisibleEntities(); err != nil {
				return
			}
		}
	}
}

// syncVisibleEntities performs one deterministic visibility pass. Keeping it
// separate from the ticker makes the four-client workflow integration-testable.
func (h *handler) syncVisibleEntities() error {
	if h.session == nil {
		return nil
	}
	if h.knownPlayers == nil {
		h.knownPlayers = make(map[[16]byte]bool)
	}
	if h.knownEntities == nil {
		h.knownEntities = make(map[int32]visibleEntity)
	}

	viewer := h.session.Snapshot()
	players := h.srv.PlayerSnapshots()
	currentPlayers := make(map[[16]byte]bool, len(players))
	for _, player := range players {
		currentPlayers[player.Profile.UUID] = true
		if !h.knownPlayers[player.Profile.UUID] {
			if err := h.sendPlayerInfoAdd(player.Profile); err != nil {
				return err
			}
			h.knownPlayers[player.Profile.UUID] = true
		}
	}

	currentEntities := make(map[int32]visibleEntity)
	for _, player := range players {
		if player.EntityID == viewer.EntityID || !playerVisible(viewer, player, h.visibilityRadius()) ||
			!h.entityChunkResident(player.X, player.Z) {
			continue
		}
		currentEntities[player.EntityID] = visibleEntity{
			entity: world.Entity{
				ID: player.EntityID, UUID: player.Profile.UUID,
				TypeID: registry.EntityTypeIndex("minecraft:player"), TypeName: "minecraft:player",
				X: player.X, Y: player.Y, Z: player.Z,
				Yaw: player.Yaw, Pitch: player.Pitch, HeadYaw: player.Yaw,
			},
			onGround: player.OnGround,
		}
	}
	for _, entity := range h.srv.Entities().All() {
		if entityVisible(viewer, entity, h.visibilityRadius()) && h.entityChunkResident(entity.X, entity.Z) {
			currentEntities[entity.ID] = visibleEntity{entity: entity, onGround: true}
		}
	}

	for id, current := range currentEntities {
		known, exists := h.knownEntities[id]
		if !exists {
			entity := current.entity
			if err := h.sendAddEntity(&entity); err != nil {
				return err
			}
		} else if known != current {
			entity := current.entity
			if err := h.sendEntityTeleportState(&entity, current.onGround); err != nil {
				return err
			}
		}
	}
	for id := range h.knownEntities {
		if _, visible := currentEntities[id]; !visible {
			if err := h.sendRemoveEntity(id); err != nil {
				return err
			}
		}
	}
	h.knownEntities = currentEntities

	for uuid := range h.knownPlayers {
		if !currentPlayers[uuid] {
			if err := h.sendPlayerInfoRemove(uuid); err != nil {
				return err
			}
			delete(h.knownPlayers, uuid)
		}
	}
	return nil
}

func (h *handler) entityChunkResident(x, z float64) bool {
	if h.streamer == nil {
		return true
	}
	cx := int32(int64(math.Floor(x)) >> 4)
	cz := int32(int64(math.Floor(z)) >> 4)
	return h.streamer.isResident(cx, cz)
}

func playerVisible(viewer, target server.PlayerSnapshot, radius int) bool {
	return chunksWithin(viewer.X, viewer.Z, target.X, target.Z, radius)
}

func entityVisible(viewer server.PlayerSnapshot, target world.Entity, radius int) bool {
	return chunksWithin(viewer.X, viewer.Z, target.X, target.Z, radius)
}

func chunksWithin(ax, az, bx, bz float64, radius int) bool {
	acx := int32(int64(math.Floor(ax)) >> 4)
	acz := int32(int64(math.Floor(az)) >> 4)
	bcx := int32(int64(math.Floor(bx)) >> 4)
	bcz := int32(int64(math.Floor(bz)) >> 4)
	dx := acx - bcx
	if dx < 0 {
		dx = -dx
	}
	dz := acz - bcz
	if dz < 0 {
		dz = -dz
	}
	return dx <= int32(radius) && dz <= int32(radius)
}

func (h *handler) sendPlayerInfoAdd(profile server.Profile) error {
	w := protocol.NewWriter(64)
	w.Byte(0xff) // All eight initialization actions, fixed 8-bit EnumSet.
	w.VarInt(1)
	w.UUID(profile.UUID)
	w.String(profile.Name)
	w.VarInt(0)   // profile properties
	w.Bool(false) // no signed chat session
	w.VarInt(1)   // creative game mode
	w.Bool(true)  // listed
	w.VarInt(0)   // latency
	w.Bool(false) // no custom display name
	w.VarInt(0)   // list order
	w.Bool(true)  // show hat
	return h.conn.SendWriter(protocol.PlayPlayerInfoUpdate, w)
}

func (h *handler) sendPlayerInfoRemove(uuid [16]byte) error {
	w := protocol.NewWriter(20)
	w.VarInt(1)
	w.UUID(uuid)
	return h.conn.SendWriter(protocol.PlayPlayerInfoRemove, w)
}

// sendAddEntity sends the minecraft:add_entity packet.
func (h *handler) sendAddEntity(e *world.Entity) error {
	w := protocol.NewWriter(64)
	w.VarInt(e.ID)
	w.UUID(e.UUID)
	w.VarInt(int32(e.TypeID))
	w.Float64(e.X).Float64(e.Y).Float64(e.Z)
	w.LPVec3(
		float64(e.VelocityX)/8000.0,
		float64(e.VelocityY)/8000.0,
		float64(e.VelocityZ)/8000.0,
	)
	w.Byte(byte(e.Pitch * 256.0 / 360.0))
	w.Byte(byte(e.Yaw * 256.0 / 360.0))
	w.Byte(byte(e.HeadYaw * 256.0 / 360.0))
	w.VarInt(0) // Data
	return h.conn.SendWriter(protocol.PlayAddEntity, w)
}

func (h *handler) sendEntityTeleport(e *world.Entity) error {
	return h.sendEntityTeleportState(e, true)
}

func (h *handler) sendEntityTeleportState(e *world.Entity, onGround bool) error {
	w := protocol.NewWriter(64)
	w.VarInt(e.ID)
	// PositionMoveRotation: position, deltaMovement, yRot, xRot.
	w.Float64(e.X).Float64(e.Y).Float64(e.Z)
	w.Float64(float64(e.VelocityX) / 8000.0)
	w.Float64(float64(e.VelocityY) / 8000.0)
	w.Float64(float64(e.VelocityZ) / 8000.0)
	w.Float32(e.Yaw)
	w.Float32(e.Pitch)
	w.Int32(0) // Relative.SET_STREAM_CODEC uses ByteBufCodecs.INT; no relative flags.
	w.Bool(onGround)
	return h.conn.SendWriter(protocol.PlayTeleportEntity, w)
}

func (h *handler) sendRemoveEntity(id int32) error {
	w := protocol.NewWriter(16)
	w.VarInt(1) // count
	w.VarInt(id)
	return h.conn.SendWriter(protocol.PlayRemoveEntities, w)
}

// handlePlay dispatches serverbound play packets. Most are tolerated for now;
// teleport and keep-alive are acknowledged/logged.
func (h *handler) handlePlay(pkt protocol.Packet) error {
	switch pkt.ID {
	case protocol.PlayAcceptTeleport:
		id, err := pkt.Body().VarInt()
		if err != nil {
			return err
		}
		h.log.Debug("teleport confirmed", "id", id)
		return nil

	case protocol.PlayKeepAliveServer:
		id, err := pkt.Body().Int64()
		if err != nil {
			return err
		}
		h.keepAliveMu.Lock()
		valid := h.keepAlivePending && id == h.keepAliveID
		if valid {
			h.keepAlivePending = false
		}
		h.keepAliveMu.Unlock()
		if !valid {
			return errors.New("unexpected keep-alive response")
		}
		h.log.Debug("keep-alive ack", "id", id)
		return nil

	case protocol.PlayPlayerLoaded:
		h.log.Info("player loaded into world", "name", h.conn.Profile.Name)
		return nil

	case protocol.PlayMovePos, protocol.PlayMovePosRot:
		r := pkt.Body()
		x, err := r.Float64()
		if err != nil {
			return err
		}
		y, err := r.Float64() // feet Y
		if err != nil {
			return err
		}
		z, err := r.Float64()
		if err != nil {
			return err
		}
		snapshot := h.session.Snapshot()
		yaw, pitch := snapshot.Yaw, snapshot.Pitch
		if pkt.ID == protocol.PlayMovePosRot {
			yaw, err = r.Float32()
			if err != nil {
				return err
			}
			pitch, err = r.Float32()
			if err != nil {
				return err
			}
		}
		flags, err := r.ReadByte()
		if err != nil {
			return err
		}
		return h.onPlayerMove(x, y, z, yaw, pitch, flags&1 != 0)

	case protocol.PlayMoveRot:
		r := pkt.Body()
		yaw, err := r.Float32()
		if err != nil {
			return err
		}
		pitch, err := r.Float32()
		if err != nil {
			return err
		}
		flags, err := r.ReadByte()
		if err != nil {
			return err
		}
		snapshot := h.session.Snapshot()
		return h.onPlayerMove(snapshot.X, snapshot.Y, snapshot.Z, yaw, pitch, flags&1 != 0)

	case protocol.PlayMoveStatusOnly:
		flags, err := pkt.Body().ReadByte()
		if err != nil {
			return err
		}
		snapshot := h.session.Snapshot()
		return h.onPlayerMove(snapshot.X, snapshot.Y, snapshot.Z, snapshot.Yaw, snapshot.Pitch, flags&1 != 0)

	case protocol.PlayPlayerAction:
		return h.handlePlayerAction(pkt)

	case protocol.PlayChatMessage:
		return h.handleChat(pkt)

	case protocol.PlayUseItemOn:
		return h.handleUseItemOn(pkt)

	case protocol.PlaySetCarriedItem:
		slot, err := pkt.Body().Uint16()
		if err != nil {
			return err
		}
		if slot < 9 {
			h.heldSlot = int32(slot)
		}
		return nil

	case protocol.PlaySetCreativeSlot:
		return h.handleCreativeSlot(pkt)

	default:
		h.log.Debug("play packet ignored", "id", pkt.ID)
		return nil
	}
}

// handleChat reads a chat message (only the leading text field is needed) and
// echoes it to the player as a system message prefixed with their name. Once a
// player registry exists this will broadcast to everyone.
func (h *handler) handleChat(pkt protocol.Packet) error {
	msg, err := pkt.Body().String()
	if err != nil {
		return err
	}
	line := "<" + h.conn.Profile.Name + "> " + msg
	h.log.Info("chat", "msg", line)
	return h.broadcastSystemChat(line)
}

func (h *handler) broadcastSystemChat(text string) error {
	w := protocol.NewWriter(len(text) + 8)
	w.Raw(nbt.Marshal(nbt.String(text)))
	w.Bool(false)
	h.srv.Broadcast(protocol.PlaySystemChat, w.Bytes())
	return nil
}

// handlePlayerAction processes digging. In creative the client sends
// START_DESTROY_BLOCK (status 0) for an instant break; survival also sends
// STOP/FINISH (status 2). Either way we clear the block, push a block_update,
// and acknowledge the sequence so the client keeps its predicted change.
func (h *handler) handlePlayerAction(pkt protocol.Packet) error {
	r := pkt.Body()
	status, err := r.VarInt()
	if err != nil {
		return err
	}
	x, y, z, err := r.Position()
	if err != nil {
		return err
	}
	if _, err := r.ReadByte(); err != nil { // face
		return err
	}
	seq, err := r.VarInt()
	if err != nil {
		return err
	}

	const startDig, finishDig = 0, 2
	if status == startDig || status == finishDig {
		if valid, lightChunks := h.srv.Chunks().SetBlockWithLight(x, y, z, world.StateAir); valid {
			h.broadcastBlockUpdate(x, y, z, world.StateAir, lightChunks)
			h.log.Debug("block broken", "x", x, "y", y, "z", z)
		}
	}
	return h.sendBlockChangedAck(seq)
}

// hotbarInvStart is the inventory slot index of hotbar slot 0.
const hotbarInvStart = 36

// handleCreativeSlot records the item a creative player placed into a slot so we
// know what block to place. The packet is: Short slot, then an item stack
// (VarInt count; if non-empty, VarInt item id followed by components we ignore).
func (h *handler) handleCreativeSlot(pkt protocol.Packet) error {
	r := pkt.Body()
	slot, err := r.Uint16()
	if err != nil {
		return err
	}
	hotbarIdx := int(slot) - hotbarInvStart
	if hotbarIdx < 0 || hotbarIdx >= len(h.hotbar) {
		return nil // not a hotbar slot; ignored
	}

	count, err := r.VarInt()
	if err != nil {
		return err
	}
	if count <= 0 {
		h.hotbar[hotbarIdx] = -1 // emptied
		return nil
	}
	itemID, err := r.VarInt()
	if err != nil {
		return err
	}
	h.hotbar[hotbarIdx] = itemID // remaining component data is not needed
	return nil
}

// faceOffsets maps a Direction (block face) to the unit offset of the block
// placed against it: DOWN, UP, NORTH, SOUTH, WEST, EAST.
var faceOffsets = [6][3]int{
	{0, -1, 0}, {0, 1, 0}, {0, 0, -1}, {0, 0, 1}, {-1, 0, 0}, {1, 0, 0},
}

// handleUseItemOn places the held block against the clicked face. Layout
// (captured from the client): Hand, Position, Face, cursor XYZ floats,
// insideBlock bool, worldBorderHit bool, sequence.
func (h *handler) handleUseItemOn(pkt protocol.Packet) error {
	r := pkt.Body()
	if _, err := r.VarInt(); err != nil { // hand
		return err
	}
	x, y, z, err := r.Position()
	if err != nil {
		return err
	}
	face, err := r.VarInt()
	if err != nil {
		return err
	}
	// Skip cursor (3 floats) + insideBlock + worldBorderHit, then read sequence.
	for i := 0; i < 3; i++ {
		if _, err := r.Float32(); err != nil {
			return err
		}
	}
	if _, err := r.Bool(); err != nil {
		return err
	}
	if _, err := r.Bool(); err != nil {
		return err
	}
	seq, err := r.VarInt()
	if err != nil {
		return err
	}

	if face >= 0 && int(face) < len(faceOffsets) {
		if state, ok := h.heldBlock(); ok {
			off := faceOffsets[face]
			px, py, pz := x+off[0], y+off[1], z+off[2]
			if valid, lightChunks := h.srv.Chunks().SetBlockWithLight(px, py, pz, state); valid {
				h.broadcastBlockUpdate(px, py, pz, state, lightChunks)
				h.log.Debug("block placed", "x", px, "y", py, "z", pz, "state", state)
			}
		}
	}
	return h.sendBlockChangedAck(seq)
}

// heldBlock returns the block state for the currently held item, if it is a
// placeable block.
func (h *handler) heldBlock() (uint16, bool) {
	itemID := h.hotbar[h.heldSlot]
	if itemID < 0 {
		return 0, false
	}
	return world.ItemToBlock(itemID)
}

func (h *handler) broadcastBlockUpdate(x, y, z int, state uint16, lightChunks []world.ChunkPos) {
	w := protocol.NewWriter(12)
	w.Position(x, y, z)
	w.VarInt(int32(state))
	cx := int32(x >> 4)
	cz := int32(z >> 4)
	h.srv.BroadcastChunk(cx, cz, protocol.PlayBlockUpdate, w.Bytes())
	for _, chunk := range lightChunks {
		if light, err := h.srv.Chunks().LightUpdate(chunk.X, chunk.Z); err == nil {
			h.srv.BroadcastChunk(chunk.X, chunk.Z, protocol.PlayLightUpdate, light)
		}
	}
}

// sendBlockChangedAck confirms a block-action sequence so the client does not
// roll back its predicted change.
func (h *handler) sendBlockChangedAck(sequence int32) error {
	w := protocol.NewWriter(4)
	w.VarInt(sequence)
	return h.conn.SendWriter(protocol.PlayBlockChangedAck, w)
}
