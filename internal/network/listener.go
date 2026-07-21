package network

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"sync"

	"regionio/internal/server"
)

// Listener accepts TCP connections and serves each in its own goroutine.
type Listener struct {
	srv   *server.Server
	log   *slog.Logger
	mu    sync.Mutex
	conns map[net.Conn]struct{}
	wg    sync.WaitGroup
}

// NewListener constructs a Listener bound to srv.
func NewListener(srv *server.Server, log *slog.Logger) *Listener {
	return &Listener{srv: srv, log: log, conns: make(map[net.Conn]struct{})}
}

// ListenAndServe binds the configured address and accepts connections until
// ctx is cancelled.
func (l *Listener) ListenAndServe(ctx context.Context) error {
	cfg := l.srv.Config()
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)

	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", addr, err)
	}
	defer func() {
		_ = ln.Close()
		l.closeConnections()
		l.wg.Wait()
	}()
	l.log.Info("RegionIO listening", "addr", addr, "version", "26.1.2")

	// Close the listener when the context is cancelled to unblock Accept.
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()

	for {
		raw, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil // graceful shutdown
			}
			l.log.Warn("accept failed", "err", err)
			continue
		}
		l.mu.Lock()
		l.conns[raw] = struct{}{}
		l.wg.Add(1)
		l.mu.Unlock()
		go l.serveConn(raw)
	}
}

func (l *Listener) closeConnections() {
	l.mu.Lock()
	conns := make([]net.Conn, 0, len(l.conns))
	for conn := range l.conns {
		conns = append(conns, conn)
	}
	l.mu.Unlock()
	for _, conn := range conns {
		_ = conn.Close()
	}
}

// serveConn wraps a raw connection and runs its state-machine handler.
func (l *Listener) serveConn(raw net.Conn) {
	defer func() {
		l.mu.Lock()
		delete(l.conns, raw)
		l.mu.Unlock()
		l.wg.Done()
	}()
	conn := NewConn(raw)
	h := &handler{
		conn: conn,
		srv:  l.srv,
		log:  l.log.With("peer", raw.RemoteAddr().String()),
	}
	h.serve()
}
