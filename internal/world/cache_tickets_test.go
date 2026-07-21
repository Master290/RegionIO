package world

import (
	"context"
	"errors"
	"testing"
)

func TestTicketPinsChunkUntilRelease(t *testing.T) {
	cache := NewCacheWithLimit(-1, func(cx, cz int32) *Chunk {
		return NewChunk(cx, cz, BiomePlains)
	}, nil, 2)
	tickets := cache.NewTicketSet()
	tickets.Replace([]ChunkPos{{X: 0, Z: 0}}, nil)
	for x := int32(0); x < 4; x++ {
		if _, err := cache.FrameErr(x, 0); err != nil {
			t.Fatal(err)
		}
	}
	if !hasChunk(cache, 0, 0) {
		t.Fatal("ticketed chunk was evicted")
	}
	if got := cache.Stats().Tickets; got != 1 {
		t.Fatalf("ticket count = %d, want 1", got)
	}

	tickets.Close()
	if got := cache.Stats(); got.Tickets != 0 || got.Chunks > 2 {
		t.Fatalf("after release stats = %+v, want no tickets and <=2 chunks", got)
	}
}

func TestOverlappingTicketSetsRequireFinalRelease(t *testing.T) {
	cache := NewCacheWithLimit(-1, func(cx, cz int32) *Chunk {
		return NewChunk(cx, cz, BiomePlains)
	}, nil, 1)
	first, second := cache.NewTicketSet(), cache.NewTicketSet()
	pos := []ChunkPos{{X: 0, Z: 0}}
	first.Replace(pos, nil)
	second.Replace(nil, pos)
	if _, err := cache.FrameErr(0, 0); err != nil {
		t.Fatal(err)
	}
	first.Close()
	if got := cache.Stats().Tickets; got != 1 {
		t.Fatalf("tickets after first close = %d, want 1", got)
	}
	if _, err := cache.FrameErr(1, 0); err != nil {
		t.Fatal(err)
	}
	if !hasChunk(cache, 0, 0) {
		t.Fatal("chunk was evicted while second owner still held it")
	}

	second.Close()
	second.Close() // idempotent
	if got := cache.Stats(); got.Tickets != 0 || got.Chunks > 1 {
		t.Fatalf("after final close stats = %+v", got)
	}
}

func TestFrameAdmissionCanBeCancelled(t *testing.T) {
	cache := NewCache(-1, func(cx, cz int32) *Chunk {
		return NewChunk(cx, cz, BiomePlains)
	})
	for i := 0; i < cap(cache.frameSlots); i++ {
		cache.frameSlots <- struct{}{}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := cache.FrameErrContext(ctx, 0, 0)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("FrameErrContext error = %v, want context.Canceled", err)
	}
	for i := 0; i < cap(cache.frameSlots); i++ {
		<-cache.frameSlots
	}
}
