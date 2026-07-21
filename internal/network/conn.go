// Package network owns the TCP listener and per-connection lifecycle: framing,
// the handshake state machine, and dispatch to per-state handlers.
package network

import (
	"bufio"
	"net"
	"sync"
	"time"

	"regionio/internal/protocol"
	"regionio/internal/server"
)

// Conn wraps a TCP connection with buffered reads and tracks protocol state.
type Conn struct {
	raw   net.Conn
	br    *bufio.Reader
	state protocol.State

	// compressionThreshold is -1 until Set Compression is negotiated.
	compressionThreshold int32

	// Profile is populated once the client identifies during login.
	Profile server.Profile

	// writeMu serializes writes so a background sender (e.g. keep-alive) and
	// the read-loop handler never interleave bytes on the wire.
	writeMu sync.Mutex
}

// NewConn wraps a raw TCP connection.
func NewConn(raw net.Conn) *Conn {
	return &Conn{
		raw:                  raw,
		br:                   bufio.NewReaderSize(raw, 4096),
		state:                protocol.StateHandshaking,
		compressionThreshold: -1,
	}
}

// State returns the current protocol state.
func (c *Conn) State() protocol.State { return c.state }

// SetState transitions to a new protocol state.
func (c *Conn) SetState(s protocol.State) { c.state = s }

// EnableCompression sets the compression threshold for all subsequent packets.
// The caller must send the Set Compression packet (uncompressed) first.
func (c *Conn) EnableCompression(threshold int32) { c.compressionThreshold = threshold }

// CompressionEnabled reports whether compression is active.
func (c *Conn) CompressionEnabled() bool { return c.compressionThreshold >= 0 }

// RemoteAddr returns the peer address for logging.
func (c *Conn) RemoteAddr() net.Addr { return c.raw.RemoteAddr() }

// ReadPacket reads the next frame using the current compression settings.
func (c *Conn) ReadPacket() (protocol.Packet, error) {
	if err := c.raw.SetReadDeadline(time.Now().Add(30 * time.Second)); err != nil {
		return protocol.Packet{}, err
	}
	pkt, err := protocol.ReadPacket(c.br, c.compressionThreshold)
	_ = c.raw.SetReadDeadline(time.Time{})
	return pkt, err
}

// Send writes a packet with the given ID and pre-encoded body. Safe for
// concurrent use.
func (c *Conn) Send(id int32, body []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return protocol.WritePacket(c.raw, c.compressionThreshold, id, body)
}

// SendWriter writes a packet whose body was built with a protocol.Writer.
func (c *Conn) SendWriter(id int32, w *protocol.Writer) error {
	return c.Send(id, w.Bytes())
}

// SendFramed writes an already-framed packet (e.g. a cached chunk) verbatim.
// The frame must have been built for this connection's compression threshold.
// Safe for concurrent use.
func (c *Conn) SendFramed(frame []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err := c.raw.Write(frame)
	return err
}

// CompressionThreshold returns the active threshold (-1 if disabled).
func (c *Conn) CompressionThreshold() int32 { return c.compressionThreshold }

// Close closes the underlying connection.
func (c *Conn) Close() error { return c.raw.Close() }
