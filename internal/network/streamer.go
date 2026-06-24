package network

import (
	"context"
	"log/slog"
	"runtime"
	"sync"

	"regionio/internal/world"
)

// streamer.go is the per-connection background chunk streamer. The read loop
// no longer generates or sends chunks inline; it pushes recenter requests here
// and stays free to handle the player's packets (movement, chat, keep-alive
// acks). The streamer generates chunks in a worker pool (Cache.Frame is
// goroutine-safe), pre-generates a ring beyond the view distance so movement
// doesn't pop in, and sends finished frames serially under the conn's write
// mutex.
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
	return &streamer{
		cache:      cache,
		conn:       conn,
		log:        log,
		recenter:   make(chan recenterReq, 4),
		loaded:     make(map[[2]int32]bool),
		viewRadius: viewDistance,
		genRadius:  viewDistance + 1,
		poolSize:   pool,
	}
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
	var (
		// latest holds the most recent recenter request; processed when the
		// previous batch finishes or on arrival if idle.
		pending bool
		next    recenterReq
	)
	for {
		// If we have a pending recenter, process it; otherwise block waiting.
		if pending {
			select {
			case <-ctx.Done():
				return
			case req := <-s.recenter:
				next = req // newer request supersedes the pending one
			default:
				// No newer request; process the one we have.
				pending = false
				s.processRecenter(ctx, next.cx, next.cz)
			}
		} else {
			select {
			case <-ctx.Done():
				return
			case req := <-s.recenter:
				pending = true
				next = req
			}
		}
	}
}

// processRecenter generates and sends the chunks newly in range of (cx, cz),
// pre-generates the predictive ring, and drops chunks that left the gen radius.
// It is the only place `loaded`/`centerX`/`centerZ` are mutated.
func (s *streamer) processRecenter(ctx context.Context, cx, cz int32) {
	s.centerX, s.centerZ, s.hasCenter = cx, cz, true

	// Build the desired set: everything within genRadius (the union of what we
	// send + the pre-gen ring). Sent = within viewRadius; pre-gen = the ring.
	order := spiralOrder(cx, cz, s.genRadius)
	desired := make(map[[2]int32]bool, len(order))

	// Split into "to send" (within viewRadius) and "pre-gen only" (the ring).
	var toSend [][2]int32
	var toPreGen [][2]int32
	for _, key := range order {
		desired[key] = true
		dx := key[0] - cx
		if dx < 0 {
			dx = -dx
		}
		dz := key[1] - cz
		if dz < 0 {
			dz = -dz
		}
		if dx <= int32(s.viewRadius) && dz <= int32(s.viewRadius) {
			toSend = append(toSend, key)
		} else {
			toPreGen = append(toPreGen, key)
		}
	}

	// Generate the send set in parallel, sending each frame as it completes.
	// The pool guarantees `poolSize` concurrent cache.Frame calls; a single
	// sender drains results and writes to the conn (serialized by writeMu).
	s.parallelSend(ctx, toSend)
	// Pre-generate the ring so the next recenter finds frames warm in the cache.
	// Errors are irrelevant here (we don't send anything), so no sender.
	s.parallelGenerate(ctx, toPreGen)

	// Forget chunks that left the gen radius. The client drops them itself once
	// it gets the new chunk-cache-center, but trimming our set keeps memory
	// bounded and avoids re-sending.
	for key := range s.loaded {
		if !desired[key] {
			delete(s.loaded, key)
		}
	}
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
			s.log.Debug("streamer send failed", "cx", r.cx, "cz", r.cz, "err", r.err)
			return
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
				_ = s.cache.Frame(j.cx, j.cz) // warm the cache; discard the frame
			}
		}()
	}
	for _, j := range pending {
		select {
		case <-ctx.Done():
			break
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
		frame := s.cache.Frame(j.cx, j.cz)
		if err := s.conn.SendFramed(frame); err != nil {
			select {
			case results <- frameResult{cx: j.cx, cz: j.cz, err: err}:
			case <-ctx.Done():
			}
			return
		}
		select {
		case results <- frameResult{cx: j.cx, cz: j.cz}:
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
