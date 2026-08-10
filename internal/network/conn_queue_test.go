package network

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"regionio/internal/protocol"
)

type blockedConn struct {
	mu      sync.Mutex
	closed  bool
	release chan struct{}
	once    sync.Once
}

func newBlockedConn() *blockedConn { return &blockedConn{release: make(chan struct{})} }

func (c *blockedConn) Read([]byte) (int, error) { return 0, io.EOF }
func (c *blockedConn) Write(p []byte) (int, error) {
	<-c.release
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return 0, net.ErrClosed
	}
	return len(p), nil
}
func (c *blockedConn) Close() error {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	c.once.Do(func() { close(c.release) })
	return nil
}
func (c *blockedConn) LocalAddr() net.Addr              { return testAddr("local") }
func (c *blockedConn) RemoteAddr() net.Addr             { return testAddr("remote") }
func (c *blockedConn) SetDeadline(time.Time) error      { return nil }
func (c *blockedConn) SetReadDeadline(time.Time) error  { return nil }
func (c *blockedConn) SetWriteDeadline(time.Time) error { return nil }

func TestConnWriterPreservesPacketOrder(t *testing.T) {
	raw := &recordingConn{}
	conn := NewConn(raw)
	defer conn.Close()
	for id := int32(1); id <= 3; id++ {
		if err := conn.Send(id, []byte{byte(id)}); err != nil {
			t.Fatal(err)
		}
	}
	raw.mu.Lock()
	data := append([]byte(nil), raw.buf.Bytes()...)
	raw.mu.Unlock()
	reader := bufio.NewReader(bytes.NewReader(data))
	for want := int32(1); want <= 3; want++ {
		packet, err := protocol.ReadPacket(reader, -1)
		if err != nil {
			t.Fatal(err)
		}
		if packet.ID != want || len(packet.Data) != 1 || packet.Data[0] != byte(want) {
			t.Fatalf("packet %d = id %d data %v", want, packet.ID, packet.Data)
		}
	}
}

func TestConnEnqueueDisconnectsAtByteBudget(t *testing.T) {
	raw := newBlockedConn()
	conn := NewConn(raw)
	body := make([]byte, protocol.MaxPacketSize)
	var err error
	for i := 0; i < 8; i++ {
		err = conn.Enqueue(1, body)
		if err != nil {
			break
		}
	}
	if !errors.Is(err, ErrOutboundFull) {
		t.Fatalf("enqueue error = %v, want ErrOutboundFull", err)
	}
	select {
	case <-conn.done:
	case <-time.After(time.Second):
		t.Fatal("queue overflow did not close connection")
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestConnCloseUnblocksWaitingSend(t *testing.T) {
	raw := newBlockedConn()
	conn := NewConn(raw)
	result := make(chan error, 1)
	go func() { result <- conn.Send(1, []byte("blocked")) }()
	time.Sleep(10 * time.Millisecond)
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("blocked send succeeded after close")
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not unblock Send")
	}
}
