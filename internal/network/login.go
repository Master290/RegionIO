package network

import (
	"errors"

	"regionio/internal/protocol"
	"regionio/internal/server"
)

// handleLogin drives the offline-mode login phase:
//
//	C→S Login Start        → derive offline profile
//	S→C Set Compression    → (optional) enable zlib for later packets
//	S→C Login Success      → confirm the profile
//	C→S Login Acknowledged → switch to the Configuration phase
//
// Encryption (online mode) is intentionally not implemented here.
func (h *handler) handleLogin(pkt protocol.Packet) error {
	switch pkt.ID {
	case protocol.LoginStartID:
		return h.handleLoginStart(pkt)
	case protocol.LoginAcknowledgedID:
		// Client acknowledges Login Success; both sides enter Configuration.
		h.conn.SetState(protocol.StateConfiguration)
		h.log.Info("player logged in",
			"name", h.conn.Profile.Name,
			"uuid", uuidString(h.conn.Profile.UUID))
		return h.beginConfiguration()
	default:
		return errors.New("unexpected packet in login state")
	}
}

func (h *handler) handleLoginStart(pkt protocol.Packet) error {
	r := pkt.Body()
	name, err := r.String()
	if err != nil {
		return err
	}
	if name == "" || len(name) > 16 {
		return errors.New("invalid login name")
	}
	// The client also sends a UUID, but in offline mode we derive our own so it
	// is stable and independent of what the client claims.
	if _, err := r.UUID(); err != nil {
		return err
	}

	h.conn.Profile = server.Profile{
		UUID: server.OfflineUUID(name),
		Name: name,
	}

	// Negotiate compression before Login Success so that packet (and every
	// later one) is sent in the compressed format.
	if t := h.srv.Config().CompressionThreshold; t >= 0 {
		w := protocol.NewWriter(protocol.VarIntLen(int32(t)))
		w.VarInt(int32(t))
		if err := h.conn.SendWriter(protocol.SetCompressionID, w); err != nil {
			return err
		}
		h.conn.EnableCompression(int32(t))
	}

	return h.sendLoginSuccess()
}

// sendLoginSuccess writes the Login Success packet. For protocol 775 the body
// is: UUID, Username, then a VarInt-prefixed array of profile properties (none
// in offline mode).
func (h *handler) sendLoginSuccess() error {
	p := h.conn.Profile
	w := protocol.NewWriter(16 + len(p.Name) + 2)
	w.UUID(p.UUID)
	w.String(p.Name)
	w.VarInt(0) // property count
	return h.conn.SendWriter(protocol.LoginSuccessID, w)
}

// uuidString formats a UUID as the canonical 8-4-4-4-12 hex string.
func uuidString(u [16]byte) string {
	const hexdigits = "0123456789abcdef"
	var b [36]byte
	j := 0
	for i := 0; i < 16; i++ {
		if i == 4 || i == 6 || i == 8 || i == 10 {
			b[j] = '-'
			j++
		}
		b[j] = hexdigits[u[i]>>4]
		b[j+1] = hexdigits[u[i]&0x0f]
		j += 2
	}
	return string(b[:j])
}
