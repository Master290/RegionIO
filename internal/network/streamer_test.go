package network

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

// TestSpiralOrderCenterFirst confirms the centre coordinate is returned first
// and the ring order expands outward (Chebyshev distance non-decreasing).
func TestSpiralOrderCenterFirst(t *testing.T) {
	order := spiralOrder(0, 0, 2)
	if order[0] != [2]int32{0, 0} {
		t.Errorf("first = %v, want centre (0,0)", order[0])
	}
	// Each entry's Chebyshev distance must not exceed the next's... actually it
	// only needs to be non-decreasing within ring blocks. Verify the max
	// distance of the first k entries grows as expected by checking the full
	// set contains exactly the (2*2+1)² = 49 distinct coords.
	want := (2*2 + 1) * (2*2 + 1)
	if len(order) != want {
		t.Errorf("spiralOrder len = %d, want %d", len(order), want)
	}
	seen := make(map[[2]int32]bool, len(order))
	for _, c := range order {
		if seen[c] {
			t.Errorf("duplicate %v in spiral order", c)
		}
		seen[c] = true
	}
}

// TestSpiralOrderRingStructure checks that all distance-0 coords come before
// distance-1, which come before distance-2 (centre-outward ordering).
func TestSpiralOrderRingStructure(t *testing.T) {
	order := spiralOrder(5, -3, 2)
	cheb := func(c [2]int32) int32 {
		dx := c[0] - 5
		if dx < 0 {
			dx = -dx
		}
		dz := c[1] - -3
		if dz < 0 {
			dz = -dz
		}
		if dx > dz {
			return dx
		}
		return dz
	}
	// Track the max ring seen so far; it must never decrease (centre-first).
	var maxRing int32
	for _, c := range order {
		r := cheb(c)
		if r < maxRing {
			t.Errorf("ring %d appeared after ring %d — not centre-first", r, maxRing)
		}
		if r > maxRing {
			maxRing = r
		}
	}
	if maxRing != 2 {
		t.Errorf("max ring = %d, want 2", maxRing)
	}
}

func TestSpiralOrderWalksEachRingContiguously(t *testing.T) {
	order := spiralOrder(0, 0, 4)
	for i := 1; i < len(order); i++ {
		previousRing := chunkDistanceFrom(0, 0, order[i-1])
		currentRing := chunkDistanceFrom(0, 0, order[i])
		if previousRing != currentRing {
			continue
		}
		dx := order[i][0] - order[i-1][0]
		if dx < 0 {
			dx = -dx
		}
		dz := order[i][1] - order[i-1][1]
		if dz < 0 {
			dz = -dz
		}
		if dx+dz != 1 {
			t.Fatalf("ring %d jumps from %v to %v", currentRing, order[i-1], order[i])
		}
	}
}

// TestRequestRecenterNonBlocking confirms requestRecenter never blocks the
// caller even when many requests are pushed rapidly (the streamer drains stale
// ones). This is the property the read loop relies on to stay responsive.
func TestRequestRecenterNonBlocking(t *testing.T) {
	s := newStreamer(nil, nil, nil, 4)
	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			s.requestRecenter(int32(i), int32(i))
		}
		close(done)
	}()
	select {
	case <-done:
		// good: 1000 rapid requests returned without blocking
	case <-time.After(2 * time.Second):
		t.Fatal("requestRecenter blocked for 2s")
	}
}

func TestStreamerSendsStrictlyNearFirst(t *testing.T) {
	cache := world.NewCache(-1, func(cx, cz int32) *world.Chunk {
		return world.NewChunk(cx, cz, world.BiomePlains)
	})
	recorder := &recordingConn{}
	s := newStreamer(cache, NewConn(recorder), nil, 2)
	s.genRadius = s.viewRadius
	s.poolSize = 4
	if _, superseded := s.processRecenter(context.Background(), 7, -3); superseded {
		t.Fatal("unexpected recenter supersession")
	}
	defer s.tickets.Close()

	lastDistance := int32(-1)
	chunks := 0
	wantOrder := spiralOrder(7, -3, 2)
	for _, packet := range recorder.take(t) {
		if packet.ID != protocol.PlayLevelChunk {
			continue
		}
		r := packet.Body()
		x, err := r.Int32()
		if err != nil {
			t.Fatal(err)
		}
		z, err := r.Int32()
		if err != nil {
			t.Fatal(err)
		}
		distance := chunkDistanceFrom(7, -3, [2]int32{x, z})
		if distance < lastDistance {
			t.Fatalf("chunk (%d,%d) at distance %d arrived after distance %d", x, z, distance, lastDistance)
		}
		lastDistance = distance
		if want := wantOrder[chunks]; x != want[0] || z != want[1] {
			t.Fatalf("chunk[%d] = (%d,%d), want %v", chunks, x, z, want)
		}
		chunks++
	}
	if chunks != 25 {
		t.Fatalf("level chunks sent = %d, want 25", chunks)
	}
	if got := cache.Stats().Tickets; got != 25 {
		t.Fatalf("tickets = %d, want 25", got)
	}
}

func TestStreamerQueuedRecenterStopsOldOuterRings(t *testing.T) {
	cache := world.NewCache(-1, func(cx, cz int32) *world.Chunk {
		return world.NewChunk(cx, cz, world.BiomePlains)
	})
	s := newStreamer(cache, nil, nil, 2)
	s.genRadius = s.viewRadius
	s.poolSize = 4
	s.requestRecenter(100, 100)
	next, superseded := s.processRecenter(context.Background(), 0, 0)
	defer s.tickets.Close()
	if !superseded || next != (recenterReq{cx: 100, cz: 100}) {
		t.Fatalf("superseded=%v next=%+v, want latest (100,100)", superseded, next)
	}
	if len(s.loaded) != 1 || !s.loaded[[2]int32{0, 0}] {
		t.Fatalf("old loaded set = %v, want only old center", s.loaded)
	}
}

func TestStreamerRecenterForgetsViewAndReplacesPrefetchTickets(t *testing.T) {
	cache := world.NewCacheWithLimit(-1, func(cx, cz int32) *world.Chunk {
		return world.NewChunk(cx, cz, world.BiomePlains)
	}, nil, 9)
	recorder := &recordingConn{}
	s := newStreamer(cache, NewConn(recorder), nil, 2)
	s.viewRadius, s.genRadius, s.poolSize = 0, 1, 4
	defer s.tickets.Close()
	if _, superseded := s.processRecenter(context.Background(), 0, 0); superseded {
		t.Fatal("unexpected first recenter supersession")
	}
	if len(s.loaded) != 1 || cache.Stats().Tickets != 9 {
		t.Fatalf("first lifecycle loaded=%v stats=%+v", s.loaded, cache.Stats())
	}
	recorder.take(t)

	if _, superseded := s.processRecenter(context.Background(), 10, 10); superseded {
		t.Fatal("unexpected second recenter supersession")
	}
	if len(s.loaded) != 1 || !s.loaded[[2]int32{10, 10}] {
		t.Fatalf("second loaded set = %v, want only (10,10)", s.loaded)
	}
	stats := cache.Stats()
	if stats.Tickets != 9 || stats.Chunks > 9 || hasLevelChunkPacket(recorder.take(t), protocol.PlayForgetLevelChunk) != 1 {
		t.Fatalf("second lifecycle stats=%+v; want 9 tickets, <=9 chunks and one forget", stats)
	}
}

func hasLevelChunkPacket(packets []protocol.Packet, id int32) int {
	count := 0
	for _, packet := range packets {
		if packet.ID == id {
			count++
		}
	}
	return count
}

func TestLoadSixteenClientStreamersBoundedAndReleasesTickets(t *testing.T) {
	var active, peak atomic.Int32
	gen := func(cx, cz int32) *world.Chunk {
		now := active.Add(1)
		for {
			old := peak.Load()
			if now <= old || peak.CompareAndSwap(old, now) {
				break
			}
		}
		time.Sleep(200 * time.Microsecond)
		active.Add(-1)
		return world.NewChunk(cx, cz, world.BiomePlains)
	}
	cache := world.NewCacheWithLimit(-1, gen, nil, 32)
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	const clients = 16
	const groups = 4
	recorders := make([]*recordingConn, clients)
	for i := 0; i < clients; i++ {
		recorders[i] = &recordingConn{}
		s := newStreamer(cache, NewConn(recorders[i]), nil, 2)
		// A 3x3 view produces 144 ticket claims. Four spawn regions exercise
		// both shared tickets and concurrent independent generation.
		s.viewRadius, s.genRadius, s.poolSize = 1, 1, 4
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			group := int32(index % groups)
			s.requestRecenter(group*64, group*64)
			s.run(ctx)
		}(i)
	}

	deadline := time.Now().Add(120 * time.Second)
	for (cache.Stats().Frames < groups*9 || cache.Stats().Tickets < clients*9 || clientsWithPacket(recorders, protocol.PlayLevelChunk) < clients) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	stats := cache.Stats()
	if stats.Frames < groups*9 || stats.Tickets != clients*9 || clientsWithPacket(recorders, protocol.PlayLevelChunk) != clients {
		cancel()
		wg.Wait()
		t.Fatalf("loaded stats = %+v, want at least %d frames and %d tickets", stats, groups*9, clients*9)
	}
	if got := peak.Load(); got > 8 {
		cancel()
		wg.Wait()
		t.Fatalf("peak concurrent generators = %d, want <= shared limit 8", got)
	}

	cancel()
	wg.Wait()
	deadline = time.Now().Add(5 * time.Second)
	for cache.Stats().Tickets != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if stats = cache.Stats(); stats.Tickets != 0 || stats.Chunks > 32 {
		t.Fatalf("after disconnect stats = %+v, want zero tickets and <=32 chunks", stats)
	}
}

func clientsWithPacket(recorders []*recordingConn, id int32) int {
	count := 0
	for _, recorder := range recorders {
		if recorder.countPacketID(id) > 0 {
			count++
		}
	}
	return count
}
