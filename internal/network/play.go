package network

import (
	"math"
	"time"

	"regionio/internal/nbt"
	"regionio/internal/protocol"
	"regionio/internal/registry"
	"regionio/internal/world"
)

// Spawn coordinates. Y sits above the maximum terrain height so the player
// drops onto the generated surface rather than spawning inside it.
const (
	spawnX = 8.5
	spawnY = 200.0
	spawnZ = 8.5
)

// beginPlay sends the join sequence once the client enters the Play phase and
// hands chunk streaming off to the background streamer. The streamer stops when
// h.ctx (the connection lifetime context) is cancelled.
func (h *handler) beginPlay() error {
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
	// Launch the background chunk streamer. It owns generation + sending so the
	// read loop stays free; requestRecenter is a non-blocking push.
	h.streamer = newStreamer(h.srv.Chunks(), h.conn, h.log, h.viewDistance)
	go h.streamer.run(h.ctx)
	h.streamer.requestRecenter(0, 0)
	go h.keepAliveLoop()
	go h.entitySyncLoop()
	return nil
}

// onPlayerMove recenters the streamer when the player crosses into a new chunk.
// It is a non-blocking push; the read loop never waits on generation.
func (h *handler) onPlayerMove(x, z float64) error {
	cx := int32(int64(math.Floor(x)) >> 4)
	cz := int32(int64(math.Floor(z)) >> 4)
	if h.streamer != nil {
		h.streamer.requestRecenter(cx, cz)
	}
	return nil
}

// sendPlayLogin writes the clientbound play "login" packet. Field layout was
// confirmed against the 26.1.2 vanilla server capture.
func (h *handler) sendPlayLogin() error {
	dimTypeIdx := registry.Index("minecraft:dimension_type", "minecraft:overworld")
	if dimTypeIdx < 0 {
		dimTypeIdx = 0
	}

	w := protocol.NewWriter(128)
	w.Int32(1)    // entity ID
	w.Bool(false) // is hardcore

	// Dimension names: the worlds available on this server.
	dims := []string{"minecraft:overworld", "minecraft:the_end", "minecraft:the_nether"}
	w.VarInt(int32(len(dims)))
	for _, d := range dims {
		w.String(d)
	}

	w.VarInt(int32(h.srv.Config().MaxPlayers)) // max players (legacy)
	w.VarInt(10)                               // view distance
	w.VarInt(10)                               // simulation distance
	w.Bool(false)                              // reduced debug info
	w.Bool(true)                               // enable respawn screen
	w.Bool(false)                              // do limited crafting

	w.VarInt(int32(dimTypeIdx))   // dimension type (registry index)
	w.String("minecraft:overworld") // dimension name (this world)
	w.Int64(0)                    // hashed seed
	w.Byte(1)                     // game mode: creative (instant break, creative inventory)
	w.Byte(0xFF)                  // previous game mode: -1 (none)
	w.Bool(false)                 // is debug
	w.Bool(false)                 // is flat
	w.Bool(false)                 // has death location
	w.VarInt(0)                   // portal cooldown
	w.VarInt(63)                  // sea level (overworld)
	w.Bool(false)                 // enforces secure chat

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
	w.Float64(spawnX).Float64(spawnY).Float64(spawnZ) // position
	w.Float64(0).Float64(0).Float64(0)                // velocity
	w.Float32(0)                                      // yaw
	w.Float32(0)                                      // pitch
	w.Int32(0)                                        // relative flags
	return h.conn.SendWriter(protocol.PlayPlayerPosition, w)
}

// keepAliveLoop sends a keep-alive every 15 seconds. It exits as soon as a send
// fails, which happens when the connection closes.
func (h *handler) keepAliveLoop() {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		id := time.Now().UnixMilli()
		w := protocol.NewWriter(8)
		w.Int64(id)
		if err := h.conn.SendWriter(protocol.PlayKeepAliveCB, w); err != nil {
			return
		}
	}
}

// entitySyncLoop periodically sends all entities in the world to the client.
// In a real server this would track which entities the player can see and send updates.
func (h *handler) entitySyncLoop() {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	known := make(map[int32]bool)

	for {
		select {
		case <-h.ctx.Done():
			return
		case <-ticker.C:
			all := h.srv.Entities().All()
			current := make(map[int32]bool)
			for _, e := range all {
				current[e.ID] = true
				if !known[e.ID] {
					h.sendAddEntity(e)
					known[e.ID] = true
				} else {
					h.sendEntityTeleport(e)
				}
			}
			// remove entities that disappeared
			for id := range known {
				if !current[id] {
					h.sendRemoveEntity(id)
					delete(known, id)
				}
			}
		}
	}
}

// sendAddEntity sends the minecraft:add_entity packet.
func (h *handler) sendAddEntity(e *world.Entity) error {
	w := protocol.NewWriter(64)
	w.VarInt(e.ID)
	w.UUID(e.UUID)
	w.VarInt(int32(e.TypeID))
	w.Float64(e.X).Float64(e.Y).Float64(e.Z)
	w.Byte(byte(e.Pitch * 256.0 / 360.0))
	w.Byte(byte(e.Yaw * 256.0 / 360.0))
	w.Byte(byte(e.HeadYaw * 256.0 / 360.0))
	w.VarInt(0) // Data
	w.Uint16(uint16(e.VelocityX))
	w.Uint16(uint16(e.VelocityY))
	w.Uint16(uint16(e.VelocityZ))
	return h.conn.SendWriter(protocol.PlayAddEntity, w)
}

func (h *handler) sendEntityTeleport(e *world.Entity) error {
	w := protocol.NewWriter(64)
	w.VarInt(e.ID)
	w.Float64(e.X).Float64(e.Y).Float64(e.Z)
	w.Byte(byte(e.Yaw * 256.0 / 360.0))
	w.Byte(byte(e.Pitch * 256.0 / 360.0))
	w.Bool(true) // On ground
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
		// A response to our keep-alive; presence is enough for liveness.
		h.log.Debug("keep-alive ack")
		return nil

	case protocol.PlayPlayerLoaded:
		h.log.Info("player loaded into world", "name", h.conn.Profile.Name)
		return nil

	case protocol.PlayMovePos, protocol.PlayMovePosRot:
		// Both packets begin with the absolute X, Y, Z position.
		r := pkt.Body()
		x, err := r.Float64()
		if err != nil {
			return err
		}
		if _, err := r.Float64(); err != nil { // feet Y, unused for streaming
			return err
		}
		z, err := r.Float64()
		if err != nil {
			return err
		}
		return h.onPlayerMove(x, z)

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
	return h.sendSystemChat(line)
}

// sendSystemChat sends a plain-text system chat message. The text component is
// network NBT; a bare string tag is the shorthand for {"text": ...}.
func (h *handler) sendSystemChat(text string) error {
	w := protocol.NewWriter(len(text) + 8)
	w.Raw(nbt.Marshal(nbt.String(text)))
	w.Bool(false) // not an action-bar overlay
	return h.conn.SendWriter(protocol.PlaySystemChat, w)
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
		if h.srv.Chunks().SetBlock(x, y, z, world.StateAir) {
			if err := h.sendBlockUpdate(x, y, z, world.StateAir); err != nil {
				return err
			}
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
			if h.srv.Chunks().SetBlock(px, py, pz, state) {
				if err := h.sendBlockUpdate(px, py, pz, state); err != nil {
					return err
				}
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

// sendBlockUpdate notifies the client of a single block change.
func (h *handler) sendBlockUpdate(x, y, z int, state uint16) error {
	w := protocol.NewWriter(12)
	w.Position(x, y, z)
	w.VarInt(int32(state))
	return h.conn.SendWriter(protocol.PlayBlockUpdate, w)
}

// sendBlockChangedAck confirms a block-action sequence so the client does not
// roll back its predicted change.
func (h *handler) sendBlockChangedAck(sequence int32) error {
	w := protocol.NewWriter(4)
	w.VarInt(sequence)
	return h.conn.SendWriter(protocol.PlayBlockChangedAck, w)
}
