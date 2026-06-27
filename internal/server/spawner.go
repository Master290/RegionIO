package server

import (
	"math/rand"
	"time"

	"regionio/internal/registry"
	"regionio/internal/world"
)

// StartSpawning begins the entity tick and spawn loops.
func (s *Server) StartSpawning() {
	go s.entityTickLoop()
	go s.mobSpawnLoop()
}

func (s *Server) entityTickLoop() {
	ticker := time.NewTicker(50 * time.Millisecond) // 20 TPS
	defer ticker.Stop()
	for range ticker.C {
		all := s.entities.All()
		for _, e := range all {
			// Basic random wandering
			e.X += (rand.Float64() - 0.5) * 0.2
			e.Z += (rand.Float64() - 0.5) * 0.2
			e.Yaw += float32((rand.Float64() - 0.5) * 10.0)
		}
	}
}

func (s *Server) mobSpawnLoop() {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	pigType := registry.Index("minecraft:entity_type", "minecraft:pig")
	zombieType := registry.Index("minecraft:entity_type", "minecraft:zombie")
	if pigType < 0 || zombieType < 0 {
		return
	}

	for range ticker.C {
		all := s.entities.All()
		if len(all) > 50 {
			continue // limit to 50 entities
		}

		// Spawn near the spawn point (8.5, 200, 8.5)
		x := (rand.Float64() - 0.5) * 30.0
		z := (rand.Float64() - 0.5) * 30.0
		
		t := pigType
		name := "minecraft:pig"
		if rand.Float32() < 0.5 {
			t = zombieType
			name = "minecraft:zombie"
		}

		s.entities.Add(&world.Entity{
			TypeID:   t,
			TypeName: name,
			X:        x + 8.5,
			Y:        200.0, // They float for now since there's no gravity
			Z:        z + 8.5,
		})
	}
}
