package world

import (
	"crypto/rand"
	"math"
	"sync"
)

// Entity represents an in-game movable entity (mob, animal, etc).
type Entity struct {
	ID       int32
	UUID     [16]byte
	TypeID   int // Network ID from the built-in minecraft:entity_type registry
	TypeName string

	X, Y, Z    float64
	Pitch, Yaw float32
	HeadYaw    float32
	VelocityX  int16
	VelocityY  int16
	VelocityZ  int16
	OnGround   bool
}

// ChunkPos returns the chunk coordinates containing the entity's X/Z position.
func (e *Entity) ChunkPos() ChunkPos {
	return ChunkPos{
		X: int32(int64(math.Floor(e.X)) >> 4),
		Z: int32(int64(math.Floor(e.Z)) >> 4),
	}
}

// EntityManager tracks active entities in the server and maintains a chunk-indexed
// spatial table for visibility-scoped queries.
type EntityManager struct {
	mu       sync.RWMutex
	entities map[int32]*Entity
	byChunk  map[ChunkPos][]*Entity
	chunkOf  map[int32]ChunkPos
	nextID   int32
}

// NewEntityManager returns a fresh manager starting at a high ID.
func NewEntityManager() *EntityManager {
	return &EntityManager{
		entities: make(map[int32]*Entity),
		byChunk:  make(map[ChunkPos][]*Entity),
		chunkOf:  make(map[int32]ChunkPos),
		nextID:   1000, // keep IDs above early players
	}
}

// Add assigns an ID and UUID (if missing), indexes the entity by chunk, and tracks it.
func (em *EntityManager) Add(e *Entity) int32 {
	em.mu.Lock()
	defer em.mu.Unlock()
	em.nextID++
	e.ID = em.nextID
	if e.UUID == [16]byte{} {
		rand.Read(e.UUID[:])
		// Version 4 UUID
		e.UUID[6] = (e.UUID[6] & 0x0f) | 0x40
		e.UUID[8] = (e.UUID[8] & 0x3f) | 0x80
	}
	em.entities[e.ID] = e
	pos := e.ChunkPos()
	em.chunkOf[e.ID] = pos
	em.byChunk[pos] = append(em.byChunk[pos], e)
	return e.ID
}

// Remove drops the entity by its ID and removes it from the spatial index.
func (em *EntityManager) Remove(id int32) {
	em.mu.Lock()
	defer em.mu.Unlock()
	e, ok := em.entities[id]
	if !ok {
		return
	}
	delete(em.entities, id)
	if pos, hasPos := em.chunkOf[id]; hasPos {
		em.removeFromChunk(pos, e)
		delete(em.chunkOf, id)
	}
}

func (em *EntityManager) removeFromChunk(pos ChunkPos, target *Entity) {
	bucket := em.byChunk[pos]
	for i, e := range bucket {
		if e == target {
			last := len(bucket) - 1
			bucket[i] = bucket[last]
			bucket[last] = nil
			bucket = bucket[:last]
			if len(bucket) == 0 {
				delete(em.byChunk, pos)
			} else {
				em.byChunk[pos] = bucket
			}
			return
		}
	}
}

// Get retrieves a snapshot of an entity by ID, or nil if not found.
func (em *EntityManager) Get(id int32) *Entity {
	em.mu.RLock()
	defer em.mu.RUnlock()
	e := em.entities[id]
	if e == nil {
		return nil
	}
	copy := *e
	return &copy
}

// Update executes a function on an entity under a write lock and updates its
// chunk index if the entity crossed a chunk boundary.
func (em *EntityManager) Update(id int32, fn func(*Entity)) {
	em.mu.Lock()
	defer em.mu.Unlock()
	if e, ok := em.entities[id]; ok {
		oldPos := em.chunkOf[id]
		fn(e)
		newPos := e.ChunkPos()
		if newPos != oldPos {
			em.removeFromChunk(oldPos, e)
			em.chunkOf[id] = newPos
			em.byChunk[newPos] = append(em.byChunk[newPos], e)
		}
	}
}

// All returns a snapshot slice of all active entities.
func (em *EntityManager) All() []Entity {
	em.mu.RLock()
	defer em.mu.RUnlock()
	list := make([]Entity, 0, len(em.entities))
	for _, e := range em.entities {
		list = append(list, *e)
	}
	return list
}

// AppendAll appends snapshots of all active entities to dst without allocating
// when cap(dst) is sufficient.
func (em *EntityManager) AppendAll(dst []Entity) []Entity {
	em.mu.RLock()
	defer em.mu.RUnlock()
	for _, e := range em.entities {
		dst = append(dst, *e)
	}
	return dst
}

// AppendInChunkRange appends snapshots of all entities located in chunks within
// Chebyshev distance radius of (centerX, centerZ). Reuses dst's backing array.
func (em *EntityManager) AppendInChunkRange(dst []Entity, centerX, centerZ, radius int32) []Entity {
	em.mu.RLock()
	defer em.mu.RUnlock()
	for cx := centerX - radius; cx <= centerX+radius; cx++ {
		for cz := centerZ - radius; cz <= centerZ+radius; cz++ {
			for _, e := range em.byChunk[ChunkPos{X: cx, Z: cz}] {
				dst = append(dst, *e)
			}
		}
	}
	return dst
}

// ForEachInChunkRange calls fn with a snapshot of each entity within Chebyshev
// chunk distance radius of (centerX, centerZ) without allocating a slice.
func (em *EntityManager) ForEachInChunkRange(centerX, centerZ, radius int32, fn func(Entity)) {
	em.mu.RLock()
	defer em.mu.RUnlock()
	for cx := centerX - radius; cx <= centerX+radius; cx++ {
		for cz := centerZ - radius; cz <= centerZ+radius; cz++ {
			for _, e := range em.byChunk[ChunkPos{X: cx, Z: cz}] {
				fn(*e)
			}
		}
	}
}

// ChunkCount returns the number of entities currently indexed in chunk (cx, cz).
func (em *EntityManager) ChunkCount(cx, cz int32) int {
	em.mu.RLock()
	defer em.mu.RUnlock()
	return len(em.byChunk[ChunkPos{X: cx, Z: cz}])
}

// Count returns the number of active entities.
func (em *EntityManager) Count() int {
	em.mu.RLock()
	defer em.mu.RUnlock()
	return len(em.entities)
}
