package server

import (
	"errors"
	"math"
	"math/rand"
	"sync"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

// Breeze (minecraft:breeze) constants, AI parameters, projectile deflection,
// loot drops, and sounds for Minecraft 26.1.2 (protocol 775).
const (
	MobTypeBreeze      = "minecraft:breeze"
	EntityTypeBreezeID = 17

	DefaultBreezeMaxHealth float32 = 30.0
	BreezeDeathXP          int     = 10

	BreezeRodItemID  = "minecraft:breeze_rod"
	BreezeMinRodDrop = 1
	BreezeMaxRodDrop = 2

	// Breeze AI movement, long-jump, and wind-charge shooting parameters.
	BreezeAggroRange            float64 = 24.0
	BreezeIdealMinDist          float64 = 4.0
	BreezeIdealMaxDist          float64 = 16.0
	BreezeSlideSpeed            float64 = 0.16
	BreezeLongJumpHorizSpeed    float64 = 0.85
	BreezeLongJumpVertSpeed     float64 = 0.52
	BreezeInhaleDurationTicks   int     = 12
	BreezeShootPoseTicks        int     = 15
	BreezeShootCooldownTicks    int     = 40 // 2.0s between wind-charge shots
	BreezeLongJumpCooldownTicks int     = 80 // 4.0s between long jumps

	// Canonical Breeze sounds.
	SoundBreezeIdleGround = "minecraft:entity.breeze.idle_ground"
	SoundBreezeIdleAir    = "minecraft:entity.breeze.idle_air"
	SoundBreezeInhale     = "minecraft:entity.breeze.inhale"
	SoundBreezeShoot      = "minecraft:entity.breeze.shoot"
	SoundBreezeJump       = "minecraft:entity.breeze.jump"
	SoundBreezeLand       = "minecraft:entity.breeze.land"
	SoundBreezeSlide      = "minecraft:entity.breeze.slide"
	SoundBreezeDeflect    = "minecraft:entity.breeze.deflect"
	SoundBreezeHurt       = "minecraft:entity.breeze.hurt"
	SoundBreezeDeath      = "minecraft:entity.breeze.death"
	SoundBreezeCharge     = "minecraft:entity.breeze.charge"
)

// BreezeBehaviorState holds the server-side AI state for a Breeze entity.
type BreezeBehaviorState struct {
	EntityID              int32
	Pose                  byte
	InhaleTicks           int
	ShootPoseTicks        int
	ShootCooldownTicks    int
	LongJumpCooldownTicks int
	TargetPlayerUUID      [16]byte
	TargetEntityID        int32
	LastShotChargeID      int32
	DeflectedCount        int
}

type serverBreezeManager struct {
	mu      sync.RWMutex
	breezes map[int32]*BreezeBehaviorState
}

var breezeManagers sync.Map // map[*Server]*serverBreezeManager

func (s *Server) breezeManager() *serverBreezeManager {
	if v, ok := breezeManagers.Load(s); ok {
		return v.(*serverBreezeManager)
	}
	actual, _ := breezeManagers.LoadOrStore(s, &serverBreezeManager{
		breezes: make(map[int32]*BreezeBehaviorState),
	})
	return actual.(*serverBreezeManager)
}

// EncodeBreezePoseDataPacket encodes a ClientboundSetEntityDataPacket (0x63)
// updating Entity.DATA_POSE (index 6) for a Breeze (STANDING, SLIDING, INHALING,
// SHOOTING, LONG_JUMPING).
func EncodeBreezePoseDataPacket(entityID int32, pose byte) []byte {
	w := protocol.NewWriter(12)
	w.VarInt(entityID)
	w.Byte(EntityDataPoseID)
	w.VarInt(EntityDataSerializerPose)
	w.VarInt(int32(pose))
	w.Byte(0xFF)
	return w.Bytes()
}

// RollBreezeRodDrops rolls the vanilla 26.1.2 entities/breeze loot table:
// 1..2 breeze_rod base + 0..(2*lootingLevel) with Looting (up to 8 at Looting III).
func RollBreezeRodDrops(rng *rand.Rand, lootingLevel int32) world.ItemStack {
	count := int32(BreezeMinRodDrop)
	if rng != nil {
		count += int32(rng.Intn(BreezeMaxRodDrop - BreezeMinRodDrop + 1))
		if lootingLevel > 0 {
			count += int32(rng.Intn(int(lootingLevel*2) + 1))
		}
	}
	if count > 8 {
		count = 8
	}
	return world.NewNamedItemStack(BreezeRodItemID, count)
}

// CanBreezeDeflectProjectile reports whether a Breeze deflects an incoming
// projectile of projectileType. In vanilla 26.1.2, the Breeze deflects all
// projectiles EXCEPT wind_charge and breeze_wind_charge.
func CanBreezeDeflectProjectile(projectileType string) bool {
	norm := canonicalResourceName(projectileType)
	if norm == "" {
		return false
	}
	if IsWindChargeEntityType(norm) {
		return false
	}
	return true
}

func (s *Server) ensureBreezeState(entityID int32) *BreezeBehaviorState {
	if s == nil || entityID <= 0 {
		return nil
	}
	mgr := s.breezeManager()
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	st, ok := mgr.breezes[entityID]
	if !ok || st == nil {
		st = &BreezeBehaviorState{
			EntityID:              entityID,
			Pose:                  PoseStanding,
			ShootCooldownTicks:    20,
			LongJumpCooldownTicks: 40,
		}
		mgr.breezes[entityID] = st
	}
	return st
}

// GetBreezeState returns a snapshot of the BreezeBehaviorState for entityID.
func (s *Server) GetBreezeState(entityID int32) (BreezeBehaviorState, bool) {
	if s == nil {
		return BreezeBehaviorState{}, false
	}
	mgr := s.breezeManager()
	mgr.mu.RLock()
	st, ok := mgr.breezes[entityID]
	var out BreezeBehaviorState
	if ok && st != nil {
		out = *st
	}
	mgr.mu.RUnlock()
	return out, ok && st != nil
}

// SpawnBreeze spawns a Breeze entity at (x, y, z) in dimension.
func (s *Server) SpawnBreeze(dimension string, x, y, z float64) (int32, error) {
	if s == nil || s.entities == nil {
		return 0, errors.New("server: nil entity manager")
	}
	normDim := world.NormalizeDimension(dimension)
	id := s.entities.Add(&world.Entity{
		TypeID:    ResolveEntityTypeID(MobTypeBreeze),
		TypeName:  MobTypeBreeze,
		Dimension: normDim,
		X:         x,
		Y:         y,
		Z:         z,
		OnGround:  true,
	})
	tr := s.entityTracker()
	tr.mu.Lock()
	mem := tr.mobStateFor(id, tr.aiTickID)
	mem.healthInit = true
	mem.health = DefaultBreezeMaxHealth
	mem.lastHealth = DefaultBreezeMaxHealth
	tr.mu.Unlock()

	_ = s.ensureBreezeState(id)
	if ent := s.entities.Get(id); ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, ent))
	}
	return id, nil
}

// TryDeflectProjectileByBreeze checks if targetEntityID is a Breeze and
// projectileType is deflectable; if so, reflects the velocity vector
// (-inVX, -inVY, -inVZ), increments the Breeze's deflection counter, plays
// entity.breeze.deflect, and returns deflected = true (preventing projectile damage).
func (s *Server) TryDeflectProjectileByBreeze(
	breezeEntityID int32,
	projectileType string,
	inVX, inVY, inVZ float64,
) (outVX, outVY, outVZ float64, deflected bool) {
	if s == nil || s.entities == nil || !CanBreezeDeflectProjectile(projectileType) {
		return inVX, inVY, inVZ, false
	}
	ent := s.entities.Get(breezeEntityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeBreeze {
		return inVX, inVY, inVZ, false
	}
	st := s.ensureBreezeState(breezeEntityID)
	if st != nil {
		mgr := s.breezeManager()
		mgr.mu.Lock()
		st.DeflectedCount++
		mgr.mu.Unlock()
	}

	outVX = -inVX * 0.85
	outVY = -inVY*0.5 + 0.15
	outVZ = -inVZ * 0.85
	if outVX == 0 && outVZ == 0 {
		outVX = 0.5
	}

	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundBreezeDeflect, SoundSourceHostile, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
	return outVX, outVY, outVZ, true
}

// ShootBreezeWindChargeAt fires a minecraft:breeze_wind_charge projectile from
// the Breeze with breezeID toward (targetX, targetY, targetZ).
func (s *Server) ShootBreezeWindChargeAt(breezeID int32, targetX, targetY, targetZ float64) (int32, error) {
	if s == nil || s.entities == nil {
		return 0, errors.New("server: nil entity manager")
	}
	ent := s.entities.Get(breezeID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeBreeze {
		return 0, errors.New("breeze: invalid shooter entity")
	}
	dim := ent.EffectiveDimension()
	spawnX := ent.X
	spawnY := ent.Y + 1.1
	spawnZ := ent.Z

	dx := targetX - spawnX
	dy := (targetY + 0.6) - spawnY
	dz := targetZ - spawnZ
	dist := math.Sqrt(dx*dx + dy*dy + dz*dz)
	if dist < 1e-4 {
		dx, dy, dz, dist = 0, 0, 1, 1
	}
	vx := (dx / dist) * BreezeWindChargeFlightSpeed
	vy := (dy / dist) * BreezeWindChargeFlightSpeed
	vz := (dz / dist) * BreezeWindChargeFlightSpeed

	chargeID, err := s.SpawnWindCharge(dim, spawnX, spawnY, spawnZ, vx, vy, vz, breezeID, true)
	if err != nil {
		return 0, err
	}

	st := s.ensureBreezeState(breezeID)
	if st != nil {
		mgr := s.breezeManager()
		mgr.mu.Lock()
		st.Pose = PoseShooting
		st.ShootPoseTicks = BreezeShootPoseTicks
		st.ShootCooldownTicks = BreezeShootCooldownTicks
		st.LastShotChargeID = chargeID
		mgr.mu.Unlock()
	}

	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB, EncodeBreezePoseDataPacket(breezeID, PoseShooting))
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundBreezeShoot, SoundSourceHostile, ent.X, ent.Y, ent.Z, 1.5, 1.0, 0))
	return chargeID, nil
}

// PerformBreezeLongJump leaps the Breeze toward (goalX, goalZ), setting
// PoseLongJumping and broadcasting motion and jump sound.
func (s *Server) PerformBreezeLongJump(breezeID int32, goalX, goalZ float64) bool {
	if s == nil || s.entities == nil {
		return false
	}
	ent := s.entities.Get(breezeID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeBreeze {
		return false
	}
	dim := ent.EffectiveDimension()
	cache := s.DimensionChunks(dim)
	dx := goalX - ent.X
	dz := goalZ - ent.Z
	dist := math.Hypot(dx, dz)
	if dist < 1e-4 {
		dx, dz, dist = 1, 0, 1
	}
	vx := (dx / dist) * BreezeLongJumpHorizSpeed
	vy := BreezeLongJumpVertSpeed
	vz := (dz / dist) * BreezeLongJumpHorizSpeed

	feetY := int(math.Floor(ent.Y + 0.01))
	nextX := ent.X + vx*2.0
	nextY := ent.Y
	nextZ := ent.Z + vz*2.0
	if cache != nil {
		if nx, ny, nz, ok := tryApplyHorizontalStep(cache, ent.X, feetY, ent.Z, nextX, nextZ); ok {
			nextX, nextY, nextZ = nx, float64(ny), nz
		} else {
			nextX, nextZ = ent.X, ent.Z
		}
	}
	yaw := yawToward(dx, dz)
	s.entities.Update(breezeID, func(e *world.Entity) {
		e.X = nextX
		e.Y = nextY
		e.Z = nextZ
		e.Yaw = yaw
		e.HeadYaw = yaw
		e.VelocityX = clampVelocityShort(vx)
		e.VelocityY = clampVelocityShort(vy)
		e.VelocityZ = clampVelocityShort(vz)
	})

	st := s.ensureBreezeState(breezeID)
	if st != nil {
		mgr := s.breezeManager()
		mgr.mu.Lock()
		st.Pose = PoseLongJumping
		st.LongJumpCooldownTicks = BreezeLongJumpCooldownTicks
		mgr.mu.Unlock()
	}

	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB, EncodeBreezePoseDataPacket(breezeID, PoseLongJumping))
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlaySetEntityMotion, EncodeSetEntityMotionPacket(breezeID, vx, vy, vz))
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundBreezeJump, SoundSourceHostile, nextX, nextY, nextZ, 1.0, 1.0, 0))
	return true
}

// TickBreezes advances all Breeze entities by one tick: selects the nearest
// target player (or iron golem), slides around the target, long-jumps to keep
// distance, and inhales + fires breeze_wind_charge projectiles.
func (s *Server) TickBreezes(rng *rand.Rand) {
	if s == nil || s.entities == nil || s.Difficulty() == DifficultyPeaceful {
		return
	}
	var targets []playerTarget
	targets = s.appendPlayerTargets(targets)

	allEnts := s.entities.AppendAll(nil)
	for i := range allEnts {
		ent := &allEnts[i]
		if canonicalResourceName(ent.TypeName) != MobTypeBreeze {
			continue
		}
		st := s.ensureBreezeState(ent.ID)
		if st == nil {
			continue
		}
		dim := ent.EffectiveDimension()
		cache := s.DimensionChunks(dim)
		feetY := int(math.Floor(ent.Y + 0.01))
		cp := ent.ChunkPos()

		mgr := s.breezeManager()
		mgr.mu.Lock()
		if st.ShootCooldownTicks > 0 {
			st.ShootCooldownTicks--
		}
		if st.LongJumpCooldownTicks > 0 {
			st.LongJumpCooldownTicks--
		}
		if st.ShootPoseTicks > 0 {
			st.ShootPoseTicks--
			if st.ShootPoseTicks == 0 && st.Pose == PoseShooting {
				st.Pose = PoseStanding
			}
		}
		inhale := st.InhaleTicks
		shootCD := st.ShootCooldownTicks
		jumpCD := st.LongJumpCooldownTicks
		mgr.mu.Unlock()

		target, found := selectTargetWithinRange(targets, ent.X, ent.Y, ent.Z, BreezeAggroRange)
		if !found {
			if st.Pose != PoseStanding {
				mgr.mu.Lock()
				st.Pose = PoseStanding
				st.InhaleTicks = 0
				mgr.mu.Unlock()
				s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB, EncodeBreezePoseDataPacket(ent.ID, PoseStanding))
			}
			continue
		}

		dx := target.x - ent.X
		dz := target.z - ent.Z
		horizDist := math.Hypot(dx, dz)
		yaw := yawToward(dx, dz)

		// 1. If currently inhaling for a wind-charge shot, count down and fire when done.
		if inhale > 0 {
			inhale--
			mgr.mu.Lock()
			st.InhaleTicks = inhale
			mgr.mu.Unlock()
			if inhale == 0 {
				_, _ = s.ShootBreezeWindChargeAt(ent.ID, target.x, target.y, target.z)
			}
			continue
		}

		// 2. Start inhaling to shoot when shoot cooldown reaches 0 and within range.
		if shootCD <= 0 && horizDist <= BreezeIdealMaxDist {
			mgr.mu.Lock()
			st.InhaleTicks = BreezeInhaleDurationTicks
			st.Pose = PoseInhaling
			mgr.mu.Unlock()
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB, EncodeBreezePoseDataPacket(ent.ID, PoseInhaling))
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
				EncodeSoundPacket(SoundBreezeInhale, SoundSourceHostile, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
			continue
		}

		// 3. Long-jump if target is too close (< 3.5 blocks) or too far (> 14 blocks) and cooldown is ready.
		if jumpCD <= 0 && (horizDist < BreezeIdealMinDist || horizDist > 14.0) {
			var goalX, goalZ float64
			if horizDist < BreezeIdealMinDist && horizDist > 1e-4 {
				goalX = ent.X - (dx/horizDist)*6.0
				goalZ = ent.Z - (dz/horizDist)*6.0
			} else if horizDist > 1e-4 {
				goalX = ent.X + (dx/horizDist)*6.0
				goalZ = ent.Z + (dz/horizDist)*6.0
			}
			s.PerformBreezeLongJump(ent.ID, goalX, goalZ)
			continue
		}

		// 4. Slide laterally around the target at BreezeSlideSpeed.
		if horizDist > 1e-4 {
			perpX := -dz / horizDist
			perpZ := dx / horizDist
			candX := ent.X + perpX*BreezeSlideSpeed
			candZ := ent.Z + perpZ*BreezeSlideSpeed
			nextX, nextY, nextZ := ent.X, ent.Y, ent.Z
			if cache != nil {
				if nx, ny, nz, ok := tryApplyHorizontalStep(cache, ent.X, feetY, ent.Z, candX, candZ); ok {
					nextX, nextY, nextZ = nx, float64(ny), nz
				}
			} else {
				nextX, nextZ = candX, candZ
			}
			s.entities.Update(ent.ID, func(e *world.Entity) {
				e.X = nextX
				e.Y = nextY
				e.Z = nextZ
				e.Yaw = yaw
				e.HeadYaw = yaw
			})
		}
	}
}
