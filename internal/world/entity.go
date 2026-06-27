package world

import (
	"crypto/rand"
	"sync"
	"sync/atomic"
)

// Entity represents an in-game movable entity (mob, animal, etc).
type Entity struct {
	ID       int32
	UUID     [16]byte
	TypeID   int   // Network ID from the minecraft:entity_type registry
	TypeName string

	X, Y, Z       float64
	Pitch, Yaw    float32
	HeadYaw       float32
	VelocityX     int16
	VelocityY     int16
	VelocityZ     int16
}

// EntityManager tracks active entities in the server and manages thread-safe access.
type EntityManager struct {
	mu       sync.RWMutex
	entities map[int32]*Entity
	nextID   int32
}

// NewEntityManager returns a fresh manager starting at a high ID.
func NewEntityManager() *EntityManager {
	return &EntityManager{
		entities: make(map[int32]*Entity),
		nextID:   1000, // keep IDs above early players
	}
}

// Add assigns an ID and UUID (if missing) and tracks the entity.
func (em *EntityManager) Add(e *Entity) int32 {
	em.mu.Lock()
	defer em.mu.Unlock()
	e.ID = atomic.AddInt32(&em.nextID, 1)
	if e.UUID == [16]byte{} {
		rand.Read(e.UUID[:])
		// Version 4 UUID
		e.UUID[6] = (e.UUID[6] & 0x0f) | 0x40
		e.UUID[8] = (e.UUID[8] & 0x3f) | 0x80
	}
	em.entities[e.ID] = e
	return e.ID
}

// Remove drops the entity by its ID.
func (em *EntityManager) Remove(id int32) {
	em.mu.Lock()
	defer em.mu.Unlock()
	delete(em.entities, id)
}

// Get retrieves an entity by ID, or nil if not found.
func (em *EntityManager) Get(id int32) *Entity {
	em.mu.RLock()
	defer em.mu.RUnlock()
	return em.entities[id]
}

// All returns a snapshot slice of all active entities.
func (em *EntityManager) All() []*Entity {
	em.mu.RLock()
	defer em.mu.RUnlock()
	list := make([]*Entity, 0, len(em.entities))
	for _, e := range em.entities {
		list = append(list, e)
	}
	return list
}
