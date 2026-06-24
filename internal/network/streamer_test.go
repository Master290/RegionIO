package network

import (
	"testing"
	"time"
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
	want := (2*2 + 1) * (2 * 2 + 1)
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
