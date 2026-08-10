// Package network owns the TCP listener and per-connection lifecycle: framing,
// the handshake state machine, and dispatch to per-state handlers.
package network

import (
	"bufio"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"regionio/internal/protocol"
	"regionio/internal/server"
)

const (
	networkWriteTimeout = 30 * time.Second
	outboundQueueSize   = 256
	maxOutboundBytes    = 8 * 1024 * 1024
)

var ErrOutboundFull = errors.New("network: outbound queue full")

type outboundPacket struct {
	frame []byte
	done  chan error
}

// Conn wraps a TCP connection with buffered reads and tracks protocol state.
type Conn struct {
	raw   net.Conn
	br    *bufio.Reader
	state protocol.State

	// compressionThreshold is -1 until Set Compression is negotiated.
	compressionThreshold int32

	// Profile is populated once the client identifies during login.
	Profile server.Profile

	outbound     chan outboundPacket
	done         chan struct{}
	writerDone   chan struct{}
	closeOnce    sync.Once
	outboundMu   sync.Mutex
	pendingBytes int
}

// NewConn wraps a raw TCP connection.
func NewConn(raw net.Conn) *Conn {
	c := &Conn{
		raw:                  raw,
		br:                   bufio.NewReaderSize(raw, 4096),
		state:                protocol.StateHandshaking,
		compressionThreshold: -1,
		outbound:             make(chan outboundPacket, outboundQueueSize),
		done:                 make(chan struct{}),
		writerDone:           make(chan struct{}),
	}
	go c.writeLoop()
	return c
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

// Send queues a packet and waits until the connection's sole writer has put it
// on the wire. Waiting preserves state-machine ordering while keeping all socket
// writes serialized through the bounded outbound queue.
func (c *Conn) Send(id int32, body []byte) error {
	return c.sendFrame(protocol.AppendPacket(nil, c.compressionThreshold, id, body), true)
}

// Enqueue adds a packet without waiting for the socket. Server broadcasts use
// it so one slow recipient cannot stall the world or another connection. A
// client that exceeds the bounded queue is disconnected.
func (c *Conn) Enqueue(id int32, body []byte) error {
	return c.sendFrame(protocol.AppendPacket(nil, c.compressionThreshold, id, body), false)
}

// SendWriter writes a packet whose body was built with a protocol.Writer.
func (c *Conn) SendWriter(id int32, w *protocol.Writer) error {
	return c.Send(id, w.Bytes())
}

// SendFramed writes an already-framed packet (e.g. a cached chunk) verbatim.
// The frame must have been built for this connection's compression threshold.
// Safe for concurrent use.
func (c *Conn) SendFramed(frame []byte) error {
	return c.sendFrame(frame, true)
}

func (c *Conn) sendFrame(frame []byte, wait bool) error {
	packet := outboundPacket{frame: frame}
	if wait {
		packet.done = make(chan error, 1)
	}
	c.outboundMu.Lock()
	if c.pendingBytes+len(frame) > maxOutboundBytes {
		c.outboundMu.Unlock()
		c.abort()
		return ErrOutboundFull
	}
	select {
	case <-c.done:
		c.outboundMu.Unlock()
		return net.ErrClosed
	case c.outbound <- packet:
		c.pendingBytes += len(frame)
		c.outboundMu.Unlock()
	default:
		c.outboundMu.Unlock()
		c.abort()
		return ErrOutboundFull
	}
	if packet.done == nil {
		return nil
	}
	select {
	case err := <-packet.done:
		return err
	case <-c.done:
		select {
		case err := <-packet.done:
			return err
		default:
			return net.ErrClosed
		}
	}
}

func (c *Conn) writeLoop() {
	defer close(c.writerDone)
	for {
		select {
		case packet := <-c.outbound:
			err := c.writeFrame(packet.frame)
			c.outboundMu.Lock()
			c.pendingBytes -= len(packet.frame)
			c.outboundMu.Unlock()
			if packet.done != nil {
				packet.done <- err
			}
			if err != nil {
				c.abort()
				c.failPending(err)
				return
			}
		case <-c.done:
			c.failPending(net.ErrClosed)
			return
		}
	}
}

func (c *Conn) writeFrame(frame []byte) error {
	if err := c.raw.SetWriteDeadline(time.Now().Add(networkWriteTimeout)); err != nil {
		return err
	}
	defer c.raw.SetWriteDeadline(time.Time{})
	for len(frame) > 0 {
		n, err := c.raw.Write(frame)
		if err != nil {
			return err
		}
		if n <= 0 || n > len(frame) {
			return io.ErrShortWrite
		}
		frame = frame[n:]
	}
	return nil
}

func (c *Conn) failPending(err error) {
	for {
		select {
		case packet := <-c.outbound:
			c.outboundMu.Lock()
			c.pendingBytes -= len(packet.frame)
			c.outboundMu.Unlock()
			if packet.done != nil {
				packet.done <- err
			}
		default:
			return
		}
	}
}

func (c *Conn) abort() {
	c.closeOnce.Do(func() {
		close(c.done)
		_ = c.raw.Close()
	})
}

// CompressionThreshold returns the active threshold (-1 if disabled).
func (c *Conn) CompressionThreshold() int32 { return c.compressionThreshold }

// Close stops the writer and closes the underlying connection.
func (c *Conn) Close() error {
	c.abort()
	<-c.writerDone
	return nil
}
