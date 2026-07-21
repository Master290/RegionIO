package server

import (
	"context"
	"math"
	"math/rand"
	"time"

	"regionio/internal/registry"
	"regionio/internal/world"
)

// StartSpawning begins the entity tick and spawn loops.
func (s *Server) StartSpawning(ctx context.Context) {
	go s.entityTickLoop(ctx)
	go s.mobSpawnLoop(ctx)
}

func (s *Server) entityTickLoop(ctx context.Context) {
	ticker := time.NewTicker(50 * time.Millisecond) // 20 TPS
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			all := s.entities.All()
			for i := range all {
				snapshot := all[i]
				yBelow := int(math.Floor(snapshot.Y - 0.1))
				blockBelow := s.chunks.GetBlock(int(math.Floor(snapshot.X)), yBelow, int(math.Floor(snapshot.Z)))
				nearest, hasPlayer := s.NearestPlayer(snapshot.X, snapshot.Y, snapshot.Z)
				s.entities.Update(all[i].ID, func(e *world.Entity) {
					// Apply gravity
					if blockBelow == world.StateAir || blockBelow == world.StateWater { // Air or Water
						e.VelocityY -= 80 // gravity acceleration
						if e.VelocityY < -3000 {
							e.VelocityY = -3000 // terminal velocity
						}
					} else {
						e.VelocityY = 0
						e.Y = float64(yBelow + 1)

						// Basic random wandering or player tracking when on ground
						if hasPlayer && e.TypeName == "minecraft:zombie" {
							// Zombies move towards the player
							dx := nearest[0] - e.X
							dz := nearest[2] - e.Z
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
				})
			}
		}
	}
}

func (s *Server) mobSpawnLoop(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	pigType := registry.EntityTypeIndex("minecraft:pig")
	zombieType := registry.EntityTypeIndex("minecraft:zombie")
	if pigType < 0 || zombieType < 0 {
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if s.PlayerCount() == 0 || s.entities.Count() >= 50 {
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
				Y:        200.0,
				Z:        z + 8.5,
			})
		}
	}
}
