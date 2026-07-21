package network

import (
	"context"
	"log/slog"
	"runtime"
	"sync"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

// streamer.go is the per-connection background chunk streamer. The read loop
// no longer generates or sends chunks inline; it pushes recenter requests here
// and stays free to handle the player's packets (movement, chat, keep-alive
// acks). The streamer holds cache tickets for the view and one predictive ring,
// admits work in distance-priority batches, and sends finished frames serially
// under the conn's write mutex.
//
// Ownership:
//   - read loop: calls requestRecenter (non-blocking), owns nothing else here.
//   - streamer goroutine: owns `loaded`, `centerX/Z`, the pool, and the sender.
//   - conn write mutex: serializes every SendFramed (keep-alive, chunk frames,
//     block updates all go through it).

// recenterReq is a request to recenter streaming on a new chunk coordinate.
type recenterReq struct{ cx, cz int32 }

// streamer streams chunks to one connection in the background.
type streamer struct {
	cache *world.Cache
	conn  *Conn
	log   *slog.Logger

	recenter chan recenterReq

	// The loaded-set and current center are owned solely by the streamer's run
	// goroutine — no other goroutine reads or writes them.
	loaded    map[[2]int32]bool
	centerX   int32
	centerZ   int32
	hasCenter bool

	viewRadius int // chunks within this Chebyshev radius are sent to the client
	genRadius  int // viewRadius + 1: pre-generated but not sent (predictive ring)
	poolSize   int // parallel generation workers
	tickets    *world.TicketSet
}

// defaultViewRadius is used when the client hasn't sent client_information or
// sent an implausible value. Matches the legacy chunkRadius.
const defaultViewRadius = 4

// newStreamer constructs a streamer for the given cache/conn. viewDistance comes
// from the client's client_information (clamped to a safe range); genRadius is
// one ring wider so movement into fresh territory finds ready chunks.
func newStreamer(cache *world.Cache, conn *Conn, log *slog.Logger, viewDistance int) *streamer {
	if viewDistance < 2 {
		viewDistance = defaultViewRadius
	}
	if viewDistance > 16 {
		viewDistance = 16
	}
	pool := runtime.NumCPU()
	if pool > 8 {
		pool = 8
	}
	if pool < 2 {
		pool = 2
	}
	s := &streamer{
		cache:      cache,
		conn:       conn,
		log:        log,
		recenter:   make(chan recenterReq, 4),
		loaded:     make(map[[2]int32]bool),
		viewRadius: viewDistance,
		genRadius:  viewDistance + 1,
		poolSize:   pool,
	}
	if cache != nil {
		s.tickets = cache.NewTicketSet()
	}
	return s
}

// requestRecenter asks the streamer to recenter on (cx, cz). Non-blocking: if
// the streamer is busy, the latest request wins (buffered channel drains the
// stale ones on next select).
func (s *streamer) requestRecenter(cx, cz int32) {
	for {
		select {
		case s.recenter <- recenterReq{cx, cz}:
			return
		default:
			// Channel full: a previous request is still queued. Drop it so the
			// newest recenter is what the streamer acts on next.
			select {
			case <-s.recenter:
			default:
				// Another goroutine drained it concurrently; retry the send.
				continue
			}
		}
	}
}

// run is the streamer's main loop. It blocks until ctx is cancelled (on
// connection close). On each recenter it generates+sends the newly-in-range
// chunks in spiral order (nearest first) and pre-generates the outer ring.
func (s *streamer) run(ctx context.Context) {
	if s.tickets != nil {
		defer s.tickets.Close()
	}
	for {
		var req recenterReq
		select {
		case <-ctx.Done():
			return
		case req = <-s.recenter:
		}
		for {
			next, superseded := s.processRecenter(ctx, req.cx, req.cz)
			if !superseded {
				break
			}
			req = next
		}
	}
}

// processRecenter generates and sends the chunks newly in range of (cx, cz),
// pre-generates the predictive ring, and drops chunks that left client view.
// It is the only place `loaded`/`centerX`/`centerZ` are mutated.
func (s *streamer) processRecenter(ctx context.Context, cx, cz int32) (recenterReq, bool) {
	s.centerX, s.centerZ, s.hasCenter = cx, cz, true

	s.sendChunkCacheCenter(cx, cz)

	// Build the desired set: everything within genRadius (the union of what we
	// send + the pre-gen ring). Sent = within viewRadius; pre-gen = the ring.
	order := spiralOrder(cx, cz, s.genRadius)
	view := make(map[[2]int32]bool, (2*s.viewRadius+1)*(2*s.viewRadius+1))

	// Split into "to send" (within viewRadius) and "pre-gen only" (the ring).
	var toSend [][2]int32
	var toPreGen [][2]int32
	var viewTickets []world.ChunkPos
	var prefetchTickets []world.ChunkPos
	for _, key := range order {
		if chunkDistanceFrom(cx, cz, key) <= int32(s.viewRadius) {
			view[key] = true
			toSend = append(toSend, key)
			viewTickets = append(viewTickets, world.ChunkPos{X: key[0], Z: key[1]})
		} else {
			toPreGen = append(toPreGen, key)
			prefetchTickets = append(prefetchTickets, world.ChunkPos{X: key[0], Z: key[1]})
		}
	}
	if s.tickets != nil {
		s.tickets.Replace(viewTickets, prefetchTickets)
	}

	// Client residency follows viewRadius exactly. The prefetch ring is retained
	// only server-side by tickets and never left loaded on the client.
	for key := range s.loaded {
		if !view[key] {
			s.sendForgetLevelChunk(key[0], key[1])
			delete(s.loaded, key)
		}
	}

	// Work is admitted in strict distance order. Each batch is at most poolSize,
	// so a new recenter only waits for currently-running frames, not a full ring.
	if next, superseded := s.streamPriority(ctx, cx, cz, toSend, true); superseded {
		return next, true
	}
	// Pre-generate the ring so the next recenter finds frames warm in the cache.
	if next, superseded := s.streamPriority(ctx, cx, cz, toPreGen, false); superseded {
		return next, true
	}
	return recenterReq{}, false
}

func chunkDistanceFrom(cx, cz int32, key [2]int32) int32 {
	dx := key[0] - cx
	if dx < 0 {
		dx = -dx
	}
	dz := key[1] - cz
	if dz < 0 {
		dz = -dz
	}
	if dz > dx {
		return dz
	}
	return dx
}

func (s *streamer) streamPriority(ctx context.Context, cx, cz int32, keys [][2]int32, send bool) (recenterReq, bool) {
	for start := 0; start < len(keys); {
		ring := chunkDistanceFrom(cx, cz, keys[start])
		end := start
		for end < len(keys) && end-start < s.poolSize && chunkDistanceFrom(cx, cz, keys[end]) == ring {
			end++
		}
		if send {
			s.parallelSend(ctx, keys[start:end])
		} else {
			s.parallelGenerate(ctx, keys[start:end])
		}
		if next, ok := s.latestRecenter(); ok {
			return next, true
		}
		select {
		case <-ctx.Done():
			return recenterReq{}, false
		default:
		}
		start = end
	}
	return recenterReq{}, false
}

func (s *streamer) latestRecenter() (recenterReq, bool) {
	var latest recenterReq
	found := false
	for {
		select {
		case latest = <-s.recenter:
			found = true
		default:
			return latest, found
		}
	}
}

func (s *streamer) sendChunkCacheCenter(cx, cz int32) {
	if s.conn == nil {
		return
	}
	w := protocol.NewWriter(8)
	w.VarInt(cx)
	w.VarInt(cz)
	_ = s.conn.SendWriter(protocol.PlayChunkCacheCenter, w)
}

func (s *streamer) sendForgetLevelChunk(cx, cz int32) {
	if s.conn == nil {
		return
	}
	w := protocol.NewWriter(8)
	w.Int32(cz)
	w.Int32(cx)
	_ = s.conn.SendWriter(protocol.PlayForgetLevelChunk, w)
}

// parallelSend generates the given chunks across the worker pool and sends each
// frame as soon as it is ready (order is best-effort; the client reassembles).
// Already-loaded chunks are skipped. Returns when all are sent or ctx cancels.
func (s *streamer) parallelSend(ctx context.Context, keys [][2]int32) {
	var pending []frameJob
	for _, k := range keys {
		if s.loaded[k] {
			continue
		}
		pending = append(pending, frameJob{k[0], k[1]})
	}
	if len(pending) == 0 {
		return
	}

	jobs := make(chan frameJob, len(pending))
	results := make(chan frameResult, len(pending))

	var wg sync.WaitGroup
	workers := s.poolSize
	if workers > len(pending) {
		workers = len(pending)
	}
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.generateWorker(ctx, jobs, results)
		}()
	}

	// Feed the jobs.
	go func() {
		for _, j := range pending {
			select {
			case <-ctx.Done():
				close(jobs)
				return
			case jobs <- j:
			}
		}
		close(jobs)
	}()

	// Sender: drain results serially so writes don't interleave.
	go func() {
		wg.Wait()
		close(results)
	}()
	for r := range results {
		if r.err != nil {
			// Send failed — the connection is likely closing. Bail out; the
			// serve loop will tear us down via ctx cancel.
			if s.log != nil {
				s.log.Debug("streamer frame failed", "cx", r.cx, "cz", r.cz, "err", r.err)
			}
			return
		}
		if s.conn != nil {
			if err := s.conn.SendFramed(r.frame); err != nil {
				if s.log != nil {
					s.log.Debug("streamer send failed", "cx", r.cx, "cz", r.cz, "err", err)
				}
				return
			}
		}
		s.loaded[[2]int32{r.cx, r.cz}] = true
	}
}

// parallelGenerate warms the cache for the given chunks without sending them
// (used for the predictive ring). Errors are ignored.
func (s *streamer) parallelGenerate(ctx context.Context, keys [][2]int32) {
	var pending []frameJob
	for _, k := range keys {
		if s.loaded[k] {
			continue
		}
		pending = append(pending, frameJob{k[0], k[1]})
	}
	if len(pending) == 0 {
		return
	}

	jobs := make(chan frameJob, len(pending))
	var wg sync.WaitGroup
	workers := s.poolSize
	if workers > len(pending) {
		workers = len(pending)
	}
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				select {
				case <-ctx.Done():
					return
				default:
				}
				_, _ = s.cache.FrameErrContext(ctx, j.cx, j.cz) // warm cache; discard frame
			}
		}()
	}
Loop:
	for _, j := range pending {
		select {
		case <-ctx.Done():
			break Loop
		case jobs <- j:
		}
	}
	close(jobs)
	wg.Wait()
}

// frameJob is one chunk coordinate awaiting generation.
type frameJob struct{ cx, cz int32 }

// frameResult is a generated chunk frame plus any send error.
type frameResult struct {
	cx, cz int32
	frame  []byte
	err    error
}

// generateWorker reads jobs, generates+frames the chunk via the (thread-safe)
// cache, and sends the frame to the conn. It exits when jobs closes.
func (s *streamer) generateWorker(ctx context.Context, jobs <-chan frameJob, results chan<- frameResult) {
	for j := range jobs {
		select {
		case <-ctx.Done():
			return
		default:
		}
		frame, err := s.cache.FrameErrContext(ctx, j.cx, j.cz)
		if err != nil {
			select {
			case results <- frameResult{cx: j.cx, cz: j.cz, err: err}:
			case <-ctx.Done():
			}
			return
		}
		select {
		case results <- frameResult{cx: j.cx, cz: j.cz, frame: frame}:
		case <-ctx.Done():
			return
		}
	}
}

// spiralOrder returns chunk coordinates in a square of side (2*radius+1) around
// (cx, cz), ordered from the centre outward (Chebyshev rings). The centre is
// first, then ring 1, ring 2, … ring `radius`. Within a ring the order is
// deterministic but not otherwise constrained — nearest-first is what matters.
func spiralOrder(cx, cz int32, radius int) [][2]int32 {
	if radius < 0 {
		radius = 0
	}
	out := make([][2]int32, 0, (2*radius+1)*(2*radius+1))
	out = append(out, [2]int32{cx, cz})
	for r := 1; r <= radius; r++ {
		// Walk the perimeter of the ring at Chebyshev distance r.
		for d := -r; d <= r; d++ {
			out = append(out, [2]int32{cx + int32(d), cz - int32(r)}) // top edge
			out = append(out, [2]int32{cx + int32(d), cz + int32(r)}) // bottom edge
		}
		for d := -r + 1; d <= r-1; d++ {
			out = append(out, [2]int32{cx - int32(r), cz + int32(d)}) // left edge
			out = append(out, [2]int32{cx + int32(r), cz + int32(d)}) // right edge
		}
	}
	return out
}
