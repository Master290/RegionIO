package server

import (
	"context"
	"log/slog"
	"math"
	"math/rand"
	"time"

	"regionio/internal/protocol"
	"regionio/internal/registry"
	"regionio/internal/world"
)

// StartSpawning begins the entity tick and spawn loops. log matches
// Cache.StartAutosave's convention: these are background loops that have to be
// able to report a failure nobody is waiting on.
func (s *Server) StartSpawning(ctx context.Context, log *slog.Logger) {
	go s.entityTickLoop(ctx, log)
	go s.mobSpawnLoop(ctx)
}

func (s *Server) entityTickLoop(ctx context.Context, log *slog.Logger) {
	ticker := time.NewTicker(50 * time.Millisecond) // 20 TPS
	defer ticker.Stop()
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// The world clock rides this loop because it is the only one
			// already running at 20 TPS. It belongs on the single authoritative
			// tick the engine still needs.
			if gameTime, dayTime := s.advanceWorldTime(); gameTime%timeSyncTicks == 0 {
				s.Broadcast(protocol.PlaySetTime, encodeSetTime(gameTime, dayTime))
				if gameTime%timePersistTicks == 0 {
					if err := s.SaveWorldTime(); err != nil && log != nil {
						log.Warn("saving world clock", "err", err)
					}
				}
			}
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
							e.X += (rng.Float64() - 0.5) * 0.2
							e.Z += (rng.Float64() - 0.5) * 0.2
							e.Yaw += float32((rng.Float64() - 0.5) * 10.0)
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
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

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
			if s.PlayerCount() == 0 || s.entities.Count() >= 20 {
				continue
			}
			s.spawnMobNearPlayer(rng, pigType, zombieType)
		}
	}
}

func (s *Server) spawnMobNearPlayer(rng *rand.Rand, pigType, zombieType int) bool {
	players := s.PlayerSnapshots()
	if len(players) == 0 {
		return false
	}
	player := players[rng.Intn(len(players))]
	angle := rng.Float64() * 2 * math.Pi
	distance := 16.0 + rng.Float64()*16.0
	x := int(math.Floor(player.X + math.Cos(angle)*distance))
	z := int(math.Floor(player.Z + math.Sin(angle)*distance))
	y, ok := s.chunks.SafeSpawnY(x, z)
	if !ok {
		return false
	}

	typeID := pigType
	typeName := "minecraft:pig"
	if rng.Float32() < 0.5 {
		typeID = zombieType
		typeName = "minecraft:zombie"
	}
	s.entities.Add(&world.Entity{
		TypeID:   typeID,
		TypeName: typeName,
		X:        float64(x) + 0.5,
		Y:        float64(y),
		Z:        float64(z) + 0.5,
	})
	return true
}
