package server

import (
	"math"
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
			// Apply gravity
			yBelow := int(e.Y - 0.1) // slightly below the entity
			blockBelow := s.chunks.GetBlock(int(e.X), yBelow, int(e.Z))
			
			if blockBelow == world.StateAir || blockBelow == world.StateWater { // Air or Water
				e.VelocityY -= 80 // gravity acceleration
				if e.VelocityY < -3000 {
					e.VelocityY = -3000 // terminal velocity
				}
			} else {
				e.VelocityY = 0
				e.Y = float64(yBelow + 1)
				
				// Basic random wandering or player tracking when on ground
				pos, ok := s.NearestPlayer(e.X, e.Y, e.Z)
				
				if ok && e.TypeName == "minecraft:zombie" {
					// Zombies move towards the player
					dx := pos[0] - e.X
					dz := pos[2] - e.Z
					dist := math.Sqrt(dx*dx + dz*dz)
					if dist > 1.0 && dist < 32.0 {
						e.X += (dx / dist) * 0.15
						e.Z += (dz / dist) * 0.15
						// Simple yaw calculation
						e.Yaw = float32(math.Atan2(-dx, dz) * (180 / math.Pi))
					}
				} else {
					// Random wander
					e.X += (rand.Float64() - 0.5) * 0.2
					e.Z += (rand.Float64() - 0.5) * 0.2
					e.Yaw += float32((rand.Float64() - 0.5) * 10.0)
				}
			}
			
			if e.VelocityY != 0 {
				e.Y += float64(e.VelocityY) / 8000.0
			}
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
