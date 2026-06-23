package network

import (
	"errors"
	"io"
	"log/slog"
	"net"

	"regionio/internal/protocol"
	"regionio/internal/server"
)

// handler drives one connection through its state machine until it closes.
// It is owned by a single goroutine (the read loop), so the play fields below
// need no synchronization.
type handler struct {
	conn *Conn
	srv  *server.Server
	log  *slog.Logger

	// Play-phase chunk streaming state.
	loaded    map[[2]int32]bool // chunks currently sent to the client
	centerX   int32
	centerZ   int32
	hasCenter bool

	// Creative inventory state for block placement.
	heldSlot int32     // selected hotbar index (0-8)
	hotbar   [9]int32  // item network IDs per hotbar slot (-1 = empty)
}

// serve runs the read/dispatch loop for a single connection.
func (h *handler) serve() {
	defer h.conn.Close()

	for {
		pkt, err := h.conn.ReadPacket()
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
				h.log.Debug("connection closed", "err", err)
			}
			return
		}

		if err := h.dispatch(pkt); err != nil {
			h.log.Debug("dispatch error", "state", h.conn.State(), "id", pkt.ID, "err", err)
			return
		}
	}
}

// dispatch routes a packet to the handler for the current state.
func (h *handler) dispatch(pkt protocol.Packet) error {
	switch h.conn.State() {
	case protocol.StateHandshaking:
		return h.handleHandshake(pkt)
	case protocol.StateStatus:
		return h.handleStatus(pkt)
	case protocol.StateLogin:
		return h.handleLogin(pkt)
	case protocol.StateConfiguration:
		return h.handleConfiguration(pkt)
	case protocol.StatePlay:
		return h.handlePlay(pkt)
	default:
		return errors.New("no handler for state " + h.conn.State().String())
	}
}

// handleHandshake reads the single handshake packet and transitions state.
func (h *handler) handleHandshake(pkt protocol.Packet) error {
	if pkt.ID != protocol.HandshakeID {
		return errors.New("unexpected packet in handshaking state")
	}
	r := pkt.Body()

	protoVer, err := r.VarInt()
	if err != nil {
		return err
	}
	addr, err := r.String()
	if err != nil {
		return err
	}
	port, err := r.Uint16()
	if err != nil {
		return err
	}
	next, err := r.VarInt()
	if err != nil {
		return err
	}

	h.log.Debug("handshake",
		"protocol", protoVer, "addr", addr, "port", port, "next", next)

	switch next {
	case protocol.NextStateStatus:
		h.conn.SetState(protocol.StateStatus)
	case protocol.NextStateLogin, protocol.NextStateTransfer:
		h.conn.SetState(protocol.StateLogin)
	default:
		return errors.New("invalid next state in handshake")
	}
	return nil
}

// handleStatus answers the server-list ping: status request and ping/pong.
func (h *handler) handleStatus(pkt protocol.Packet) error {
	switch pkt.ID {
	case protocol.StatusRequestID:
		jsonBytes, err := h.srv.StatusJSON(0)
		if err != nil {
			return err
		}
		w := protocol.NewWriter(len(jsonBytes) + 4)
		w.String(string(jsonBytes))
		return h.conn.SendWriter(protocol.StatusResponseID, w)

	case protocol.PingRequestID:
		// Echo the client's payload back verbatim for latency measurement.
		payload, err := pkt.Body().Int64()
		if err != nil {
			return err
		}
		w := protocol.NewWriter(8)
		w.Int64(payload)
		return h.conn.SendWriter(protocol.PongResponseID, w)

	default:
		return errors.New("unexpected packet in status state")
	}
}
