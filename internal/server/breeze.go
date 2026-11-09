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
	BreezeMovementSpeed    float64 = 0.63
	BreezeAttackDamage     float32 = 3.0
	BreezeFollowRange      float64 = 24.0
	BreezeDeathXP          int     = 10
	BreezeMaxHeadYRot      int     = 30
	BreezeHeadRotSpeed     int     = 25
	BreezeWidth            float64 = 0.6
	BreezeHeight           float64 = 1.77
	BreezeEyeHeight        float64 = 1.3452

	BreezeRodItemID  = "minecraft:breeze_rod"
	BreezeMinRodDrop = 1
	BreezeMaxRodDrop = 2

	// Breeze AI movement, long-jump, and wind-charge shooting parameters (BreezeAi, Shoot, LongJump, Slide).
	BreezeAggroRange                float64 = 24.0
	BreezeIdealMinDist              float64 = 4.0
	BreezeIdealMaxDist              float64 = 16.0
	BreezeJumpCircleInnerRadius     float64 = 4.0
	BreezeJumpCircleMiddleRadius    float64 = 8.0
	BreezeJumpCircleOuterRadius     float64 = 24.0
	BreezeJumpCircleDistanceY       float64 = 10.0
	BreezeSlideSpeed                float64 = 0.16
	BreezeSlideSpeedMultiplier      float64 = 0.6
	BreezeLongJumpHorizSpeed        float64 = 0.85
	BreezeLongJumpVertSpeed         float64 = 0.52
	BreezeMaxJumpVelocityMultiplier float64 = 0.058333334
	BreezeRequiredAirBlocksAbove    int     = 4
	BreezeInhaleDurationTicks       int     = 15 // Shoot.SHOOT_INITIAL_DELAY_TICKS = Math.round(15.0F)
	BreezeShootRecoverTicks         int     = 4  // Shoot.SHOOT_RECOVER_DELAY_TICKS = Math.round(4.0F)
	BreezeShootPoseTicks            int     = 15
	BreezeShootBrainCooldownTicks   int     = 10 // Shoot.SHOOT_COOLDOWN_TICKS = Math.round(10.0F)
	BreezeShootCooldownTicks        int     = 40 // 2.0s between wind-charge shots
	BreezeJumpInhaleDurationTicks   int     = 10 // LongJump.INHALING_DURATION_TICKS = Math.round(10.0F)
	BreezeJumpCooldownNormalTicks   int     = 10 // LongJump.JUMP_COOLDOWN_TICKS = 10
	BreezeJumpCooldownWhenHurtTicks int     = 2  // LongJump.JUMP_COOLDOWN_WHEN_HURT_TICKS = 2
	BreezeLongJumpCooldownTicks     int     = 80 // 4.0s between long jumps
	BreezeShootWhenStuckExpiryTicks int     = 60
	BreezeShootAfterJumpExpiryTicks int     = 100
	BreezeProjectileMovementScale   float64 = 0.7
	BreezeShootUncertaintyBase      int     = 5
	BreezeShootUncertaintyMult      int     = 4
	BreezeWhirlMaxSoundTicks        int     = 80
	BreezeFallLandSoundMinDist      float64 = 3.0

	// Trial spawner configuration identifiers and parameters for Breeze.
	TrialSpawnerConfigBreezeNormal               = "minecraft:trial_chamber/breeze/normal"
	TrialSpawnerConfigBreezeOminous              = "minecraft:trial_chamber/breeze/ominous"
	TrialSpawnerBreezeNormalTotalMobs    float32 = 2.0
	TrialSpawnerBreezeOminousTotalMobs   float32 = 4.0
	TrialSpawnerBreezeAddedPerPlayer     float32 = 1.0
	TrialSpawnerBreezeSimultaneousMobs   float32 = 1.0
	TrialSpawnerBreezeSimultaneousAdded  float32 = 0.5
	TrialSpawnerBreezeTicksBetweenSpawns int     = 20

	// Canonical Breeze sounds.
	SoundBreezeIdleGround = "minecraft:entity.breeze.idle_ground"
	SoundBreezeIdleAir    = "minecraft:entity.breeze.idle_air"
	SoundBreezeInhale     = "minecraft:entity.breeze.inhale"
	SoundBreezeShoot      = "minecraft:entity.breeze.shoot"
	SoundBreezeJump       = "minecraft:entity.breeze.jump"
	SoundBreezeLand       = "minecraft:entity.breeze.land"
	SoundBreezeSlide      = "minecraft:entity.breeze.slide"
	SoundBreezeDeflect    = "minecraft:entity.breeze.deflect"
	SoundBreezeWhirl      = "minecraft:entity.breeze.whirl"
	SoundBreezeHurt       = "minecraft:entity.breeze.hurt"
	SoundBreezeDeath      = "minecraft:entity.breeze.death"
	SoundBreezeCharge     = "minecraft:entity.breeze.charge"
)

// BreezeAllowedJumpAngles lists the pitch angles (degrees) allowed by vanilla LongJump.ALLOWED_ANGLES.
var BreezeAllowedJumpAngles = [5]int{40, 55, 60, 75, 80}

// BreezeBehaviorState holds the server-side AI state for a Breeze entity.
type BreezeBehaviorState struct {
	EntityID              int32
	Pose                  byte
	InhaleTicks           int
	JumpInhaleTicks       int
	ShootRecoverTicks     int
	ShootPoseTicks        int
	ShootCooldownTicks    int
	LongJumpCooldownTicks int
	SoundTick             int
	HurtRecentlyTicks     int
	ShootAfterJumpTicks   int
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
			for l := int32(0); l < lootingLevel; l++ {
				count += int32(BreezeMinRodDrop + rng.Intn(BreezeMaxRodDrop-BreezeMinRodDrop+1))
			}
		}
	}
	if count > 8 {
		count = 8
	}
	return world.NewNamedItemStack(BreezeRodItemID, count)
}

// RollBreezeDeathLoot rolls the vanilla 26.1.2 `entities/breeze.json` loot table,
// which requires `killed_by_player` and drops 1..2 breeze_rod (+ 1..2 per Looting level).
func RollBreezeDeathLoot(rng *rand.Rand, killedByPlayer bool, lootingLevel int32) []world.ItemStack {
	if !killedByPlayer {
		return nil
	}
	if stack := RollBreezeRodDrops(rng, lootingLevel); !stack.IsEmpty() {
		return []world.ItemStack{stack}
	}
	return nil
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

// CanBreezeAttackTarget reports whether a Breeze can target an entity of entityType.
// In vanilla 26.1.2 (`Breeze.canAttack`), Breeze attacks `minecraft:player` and `minecraft:iron_golem`.
func CanBreezeAttackTarget(entityType string) bool {
	norm := canonicalResourceName(entityType)
	return norm == "minecraft:player" || norm == MobTypeIronGolem
}

// IsBreezeInvulnerableTo reports whether a Breeze is invulnerable to a damage source
// from `attackerType` or fall damage (`Breeze.isInvulnerableTo`, `Breeze.causeFallDamage`).
func IsBreezeInvulnerableTo(attackerType string, isFallDamage bool) bool {
	if isFallDamage {
		return true
	}
	return canonicalResourceName(attackerType) == MobTypeBreeze
}

// WithinBreezeInnerCircleRange implements vanilla `Breeze.withinInnerCircleRange(Vec3)`:
// checks whether (targetX, targetY, targetZ) is within 4.0 blocks horizontally and
// 10.0 blocks vertically of the Breeze's block center.
func WithinBreezeInnerCircleRange(breezeX, breezeY, breezeZ, targetX, targetY, targetZ float64) bool {
	cx := math.Floor(breezeX) + 0.5
	cy := math.Floor(breezeY) + 0.5
	cz := math.Floor(breezeZ) + 0.5
	dx := targetX - cx
	dy := math.Abs(targetY - cy)
	dz := targetZ - cz
	return dx*dx+dz*dz < BreezeJumpCircleInnerRadius*BreezeJumpCircleInnerRadius && dy < BreezeJumpCircleDistanceY
}

// BreezeFiringYPosition computes vanilla `Breeze.getFiringYPosition()`:
// `getY() + getBbHeight()/2.0 + 0.3` (`breezeY + 1.77/2.0 + 0.3 = breezeY + 1.185`).
func BreezeFiringYPosition(breezeY float64) float64 {
	return breezeY + BreezeHeight*0.5 + 0.3
}

// BreezeShootTargetY computes vanilla `Shoot.tick` target Y coordinate:
// `target.getY(target.isPassenger() ? 0.8 : 0.3)`.
func BreezeShootTargetY(targetY, targetHeight float64, isPassenger bool) float64 {
	if isPassenger {
		return targetY + targetHeight*0.8
	}
	return targetY + targetHeight*0.3
}

// BreezeShootInaccuracy computes vanilla `Shoot` projectile uncertainty for the
// given difficulty ID (`5 - difficultyID * 4`).
func BreezeShootInaccuracy(difficultyID int) float32 {
	return float32(BreezeShootUncertaintyBase - difficultyID*BreezeShootUncertaintyMult)
}

// IsBreezeStuck reports whether the Breeze is stuck according to vanilla
// `ShootWhenStuck` (`isPassenger() || isInWater() || hasEffect(LEVITATION)`).
func IsBreezeStuck(isPassenger, inWater, hasLevitation bool) bool {
	return isPassenger || inWater || hasLevitation
}

// CanBreezeJumpFromPosition implements vanilla `LongJump.checkExtraStartConditions`:
//   - Returns false if the block beneath `(bx, by-1, bz)` is `minecraft:honey_block`.
//   - Returns false if any of the 4 blocks above `(bx, by+1..by+4, bz)` is neither air nor water.
func CanBreezeJumpFromPosition(cache *world.Cache, bx, by, bz int) bool {
	if cache == nil {
		return true
	}
	belowName := canonicalResourceName(world.BlockName(cache.GetBlock(bx, by-1, bz)))
	if belowName == "minecraft:honey_block" {
		return false
	}
	for i := 1; i <= BreezeRequiredAirBlocksAbove; i++ {
		st := cache.GetBlock(bx, by+i, bz)
		if st == world.StateAir {
			continue
		}
		name := canonicalResourceName(world.BlockName(st))
		if name == "minecraft:air" || name == "minecraft:cave_air" || name == "minecraft:void_air" || name == "minecraft:water" || name == "minecraft:bubble_column" {
			continue
		}
		if _, props, ok := world.BlockStateProperties(st); ok && props["waterlogged"] == "true" {
			continue
		}
		return false
	}
	return true
}

// BreezeAmbientSound returns the ambient sound for a Breeze (`SoundBreezeIdleGround`
// when onGround, `SoundBreezeIdleAir` when airborne), or `""` when suppressed
// because the Breeze is on the ground and has an active target (`Breeze.playAmbientSound`).
func BreezeAmbientSound(onGround, hasTarget bool) string {
	if onGround && hasTarget {
		return ""
	}
	if onGround {
		return SoundBreezeIdleGround
	}
	return SoundBreezeIdleAir
}

// OnBreezeFallLanded handles vanilla `Breeze.causeFallDamage`: Breeze takes 0 fall
// damage, and if `fallDistance > 3.0`, plays `SoundBreezeLand`.
func (s *Server) OnBreezeFallLanded(breezeID int32, fallDistance float64) bool {
	if s == nil || s.entities == nil {
		return false
	}
	ent := s.entities.Get(breezeID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeBreeze {
		return false
	}
	if fallDistance > BreezeFallLandSoundMinDist {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, PlaySoundPacketID,
			EncodeSoundPacket(SoundBreezeLand, SoundSourceHostile, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
	}
	return true
}

// OnBreezeDamaged records that the Breeze took damage so its next long-jump cooldown
// uses `BreezeJumpCooldownWhenHurtTicks` (2 ticks) instead of 10 ticks.
func (s *Server) OnBreezeDamaged(breezeID int32) {
	if s == nil || breezeID <= 0 {
		return
	}
	st := s.ensureBreezeState(breezeID)
	if st == nil {
		return
	}
	mgr := s.breezeManager()
	mgr.mu.Lock()
	st.HurtRecentlyTicks = 100
	if st.LongJumpCooldownTicks > BreezeJumpCooldownWhenHurtTicks {
		st.LongJumpCooldownTicks = BreezeJumpCooldownWhenHurtTicks
	}
	mgr.mu.Unlock()
}

// OnBreezeKilled cleans up server-side state when a Breeze dies.
func (s *Server) OnBreezeKilled(breezeID int32) {
	if s == nil || breezeID <= 0 {
		return
	}
	mgr := s.breezeManager()
	mgr.mu.Lock()
	delete(mgr.breezes, breezeID)
	mgr.mu.Unlock()
}

// SpawnTrialSpawnerWaveMob spawns a real entity for a `minecraft:trial_spawner`
// wave (`minecraft:breeze`, `minecraft:bogged`, `minecraft:zombie`, `minecraft:skeleton`,
// `minecraft:spider`, `minecraft:cave_spider`, `minecraft:silverfish`, `minecraft:slime`, etc.).
func (s *Server) SpawnTrialSpawnerWaveMob(
	dim string,
	spawnerX, spawnerY, spawnerZ int,
	entityTypeID string,
	equipmentLootTable string,
	ominous bool,
	spawnX, spawnY, spawnZ float64,
) (int32, bool) {
	if s == nil || s.entities == nil {
		return 0, false
	}
	normType := canonicalResourceName(entityTypeID)
	if normType == "" {
		normType = MobTypeBreeze
	}
	id, err := s.SpawnMobInDimension(dim, spawnX, spawnY, spawnZ, normType)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// EnableTrialSpawnerEntityIntegration wires `minecraft:trial_spawner` wave spawning,
// liveness checks, and ominous upgrade despawning to the server's real entity manager.
func (s *Server) EnableTrialSpawnerEntityIntegration() {
	if s == nil {
		return
	}
	s.SetTrialSpawnerMobSpawnHook(func(
		dim string,
		spawnerX, spawnerY, spawnerZ int,
		entityTypeID string,
		equipmentLootTable string,
		ominous bool,
		spawnX, spawnY, spawnZ float64,
	) (int32, bool) {
		return s.SpawnTrialSpawnerWaveMob(dim, spawnerX, spawnerY, spawnerZ, entityTypeID, equipmentLootTable, ominous, spawnX, spawnY, spawnZ)
	})
	s.SetTrialSpawnerMobAliveHook(func(dim string, entityID int32) bool {
		if s.entities == nil {
			return false
		}
		ent := s.entities.Get(entityID)
		if ent == nil {
			return false
		}
		return s.MobHealth(entityID) > 0
	})
	s.SetTrialSpawnerMobDespawnHook(func(dim string, entityID int32) {
		if s.entities == nil {
			return
		}
		if ent := s.entities.Get(entityID); ent != nil {
			cp := ent.ChunkPos()
			s.entities.Remove(entityID)
			s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, protocol.PlayRemoveEntities,
				encodeRemoveEntitiesPacket(nil, []int32{entityID}))
		}
		s.OnBreezeKilled(entityID)
		s.OnBoggedKilled(entityID)
	})
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
		st.ShootRecoverTicks = BreezeShootRecoverTicks
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
		if st.HurtRecentlyTicks > 0 {
			st.LongJumpCooldownTicks = BreezeJumpCooldownWhenHurtTicks
		} else {
			st.LongJumpCooldownTicks = BreezeLongJumpCooldownTicks
		}
		st.ShootAfterJumpTicks = BreezeShootAfterJumpExpiryTicks
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
		if st.HurtRecentlyTicks > 0 {
			st.HurtRecentlyTicks--
		}
		if st.ShootAfterJumpTicks > 0 {
			st.ShootAfterJumpTicks--
		}
		if st.ShootRecoverTicks > 0 {
			st.ShootRecoverTicks--
		}
		if st.ShootPoseTicks > 0 {
			st.ShootPoseTicks--
			if st.ShootPoseTicks == 0 && st.Pose == PoseShooting {
				st.Pose = PoseStanding
			}
		}
		st.SoundTick--
		playWhirl := false
		if st.SoundTick <= 0 {
			nextWhirl := 40
			if rng != nil {
				nextWhirl = 1 + rng.Intn(BreezeWhirlMaxSoundTicks)
			}
			st.SoundTick = nextWhirl
			playWhirl = true
		}
		inhale := st.InhaleTicks
		shootCD := st.ShootCooldownTicks
		jumpCD := st.LongJumpCooldownTicks
		mgr.mu.Unlock()

		if playWhirl {
			pitch := float32(0.9)
			vol := float32(0.9)
			if rng != nil {
				pitch = 0.7 + 0.4*rng.Float32()
				vol = 0.8 + 0.2*rng.Float32()
			}
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
				EncodeSoundPacket(SoundBreezeWhirl, SoundSourceHostile, ent.X, ent.Y, ent.Z, vol, pitch, 0))
		}

		target, found := selectTargetWithinRange(targets, ent.X, ent.Y, ent.Z, BreezeAggroRange)
		if !found {
			bestDistSq := BreezeAggroRange * BreezeAggroRange
			for j := range allEnts {
				other := &allEnts[j]
				if other.ID == ent.ID || other.EffectiveDimension() != dim || !CanBreezeAttackTarget(other.TypeName) {
					continue
				}
				ddx := other.X - ent.X
				ddy := other.Y - ent.Y
				ddz := other.Z - ent.Z
				dSq := ddx*ddx + ddy*ddy + ddz*ddz
				if dSq <= bestDistSq {
					bestDistSq = dSq
					target = playerTarget{x: other.X, y: other.Y, z: other.Z}
					found = true
				}
			}
		}
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
