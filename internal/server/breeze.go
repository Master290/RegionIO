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
	BreezeSlideSpeed                float64 = 0.378 // 0.63 * 0.6 (MOVEMENT_SPEED * SPEED_MULTIPLIER_WHEN_SLIDING)
	BreezeSlideSpeedMultiplier      float64 = 0.6
	BreezeLongJumpHorizSpeed        float64 = 0.85
	BreezeLongJumpVertSpeed         float64 = 0.52
	BreezeMaxJumpVelocityMultiplier float64 = 0.058333334
	BreezeRequiredAirBlocksAbove    int     = 4
	BreezeInhaleDurationTicks       int     = 15 // Shoot.SHOOT_INITIAL_DELAY_TICKS = Math.round(15.0F)
	BreezeShootRecoverTicks         int     = 4  // Shoot.SHOOT_RECOVER_DELAY_TICKS = Math.round(4.0F)
	BreezeShootPoseTicks            int     = 4  // Matches Shoot recovery phase before stop() resets STANDING
	BreezeShootBrainCooldownTicks   int     = 10 // Shoot.SHOOT_COOLDOWN_TICKS = Math.round(10.0F)
	BreezeShootCooldownTicks        int     = 10 // Shoot.SHOOT_COOLDOWN_TICKS = 10
	BreezeJumpInhaleDurationTicks   int     = 10 // LongJump.INHALING_DURATION_TICKS = Math.round(10.0F)
	BreezeJumpCooldownNormalTicks   int     = 10 // LongJump.JUMP_COOLDOWN_TICKS = 10
	BreezeJumpCooldownWhenHurtTicks int     = 2  // LongJump.JUMP_COOLDOWN_WHEN_HURT_TICKS = 2
	BreezeLongJumpCooldownTicks     int     = 10 // LongJump.JUMP_COOLDOWN_TICKS = 10
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
	ShootMemoryTicks      int
	ShootActive           bool
	ShootActiveTicks      int
	ShotFiredInCycle      bool
	JumpTargetSet         bool
	JumpTargetX           int
	JumpTargetY           int
	JumpTargetZ           int
	JumpActive            bool
	JumpActiveTicks       int
	JumpVX                float64
	JumpVY                float64
	JumpVZ                float64
	LeavingWater          bool
	WalkTargetSet         bool
	WalkTargetX           int
	WalkTargetY           int
	WalkTargetZ           int
	SlideTicksRemaining   int
	TargetUnseenTicks     int
	FallDistance          float64
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
	atName := canonicalResourceName(world.BlockName(cache.GetBlock(bx, by, bz)))
	belowName := canonicalResourceName(world.BlockName(cache.GetBlock(bx, by-1, bz)))
	if atName == "minecraft:honey_block" || belowName == "minecraft:honey_block" {
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
	if st.LongJumpCooldownTicks == 0 || st.LongJumpCooldownTicks > BreezeJumpCooldownWhenHurtTicks {
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
			ShootCooldownTicks:    0,
			LongJumpCooldownTicks: 0,
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

// TryDeflectProjectileByBreeze implements `Breeze.deflection` + `ProjectileDeflection.REVERSE`
// from `26.1.2.jar`: deflects every incoming projectile EXCEPT `wind_charge` and
// `breeze_wind_charge`, scaling velocity by exact `-0.5D` (`projectile.getDeltaMovement().scale(-0.5)`),
// incrementing `DeflectedCount`, and playing `entity.breeze.deflect`.
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

	outVX = -0.5 * inVX
	outVY = -0.5 * inVY
	outVZ = -0.5 * inVZ

	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundBreezeDeflect, SoundSourceHostile, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
	return outVX, outVY, outVZ, true
}

func (s *Server) spawnBreezeShotProjectile(
	breezeID int32,
	ent *world.Entity,
	targetX, targetY, targetZ, targetHeight float64,
	targetIsPassenger bool,
) (int32, error) {
	dim := ent.EffectiveDimension()
	spawnX := ent.X
	spawnY := BreezeFiringYPosition(ent.Y)
	spawnZ := ent.Z

	aimY := BreezeShootTargetY(targetY, targetHeight, targetIsPassenger)
	dx := targetX - spawnX
	dy := aimY - spawnY
	dz := targetZ - spawnZ
	dist := math.Sqrt(dx*dx + dy*dy + dz*dz)
	if dist < 1e-4 {
		dx, dy, dz, dist = 0, 0, 1, 1
	}
	vx := (dx / dist) * BreezeProjectileMovementScale
	vy := (dy / dist) * BreezeProjectileMovementScale
	vz := (dz / dist) * BreezeProjectileMovementScale

	chargeID, err := s.SpawnWindCharge(dim, spawnX, spawnY, spawnZ, vx, vy, vz, breezeID, true)
	if err != nil {
		return 0, err
	}
	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundBreezeShoot, SoundSourceHostile, ent.X, ent.Y, ent.Z, 1.5, 1.0, 0))
	return chargeID, nil
}

// ShootBreezeWindChargeAt fires a minecraft:breeze_wind_charge projectile from
// the Breeze with breezeID toward (targetX, targetY, targetZ) using vanilla
// `Breeze.getFiringYPosition()` (`Y + 1.185`) and `PROJECTILE_MOVEMENT_SCALE` (`0.7F`).
func (s *Server) ShootBreezeWindChargeAt(breezeID int32, targetX, targetY, targetZ float64) (int32, error) {
	if s == nil || s.entities == nil {
		return 0, errors.New("server: nil entity manager")
	}
	ent := s.entities.Get(breezeID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeBreeze {
		return 0, errors.New("breeze: invalid shooter entity")
	}
	chargeID, err := s.spawnBreezeShotProjectile(breezeID, ent, targetX, targetY, targetZ, 1.8, false)
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
		st.ShootCooldownTicks = BreezeShootBrainCooldownTicks
		st.LastShotChargeID = chargeID
		mgr.mu.Unlock()
	}

	dim := ent.EffectiveDimension()
	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB, EncodeBreezePoseDataPacket(breezeID, PoseShooting))
	return chargeID, nil
}

// PerformBreezeLongJump launches the Breeze on a parabolic trajectory toward (goalX, goalZ),
// setting PoseLongJumping, discardFriction airborne velocity, and broadcasting motion + jump sound.
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

	jumpBoostAmp := -1
	for _, eff := range s.EntityEffects(breezeID) {
		if canonicalEffectID(eff.ID) == EffectJumpBoost {
			jumpBoostAmp = int(eff.Amplifier)
			break
		}
	}

	gx, gy, gz := int(math.Floor(goalX)), int(math.Floor(ent.Y+0.01)), int(math.Floor(goalZ))
	if sx, sy, sz, snapped := SnapBreezeJumpTargetToSurface2612(cache, goalX, ent.Y+1.0, goalZ); snapped {
		gx, gy, gz = sx, sy, sz
	}
	vx, vy, vz, ok := SelectBreezeOptimalJumpVector2612(cache, nil, ent.X, ent.Y, ent.Z, gx, gy, gz, BreezeFollowRange, jumpBoostAmp, true)
	if !ok {
		vx = (dx / dist) * BreezeLongJumpHorizSpeed
		vy = BreezeLongJumpVertSpeed
		vz = (dz / dist) * BreezeLongJumpHorizSpeed
	}

	yaw := yawToward(dx, dz)
	inWater, _, _ := breezeWaterStateAt(cache, ent.X, ent.Y, ent.Z)
	s.entities.Update(breezeID, func(e *world.Entity) {
		e.OnGround = false
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
		st.JumpActive = true
		st.JumpActiveTicks = 1
		st.JumpTargetSet = true
		st.JumpTargetX, st.JumpTargetY, st.JumpTargetZ = gx, gy, gz
		st.JumpVX, st.JumpVY, st.JumpVZ = vx, vy, vz
		st.LeavingWater = inWater
		mgr.mu.Unlock()
	}

	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB, EncodeBreezePoseDataPacket(breezeID, PoseLongJumping))
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlaySetEntityMotion, EncodeSetEntityMotionPacket(breezeID, vx, vy, vz))
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundBreezeJump, SoundSourceHostile, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
	return true
}

// TickBreezes advances all Breeze entities by one tick matching `Breeze.tick` +
// `BreezeAi` (`StopAttackingIfTargetInvalid`, `Shoot`, `LongJump`, `ShootWhenStuck`,
// `Slide` + `SlideToTargetSink`) in `server-26.1.2.jar`.
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
		if s.MobHealth(ent.ID) <= 0 {
			continue
		}
		st := s.ensureBreezeState(ent.ID)
		if st == nil {
			continue
		}
		dim := ent.EffectiveDimension()
		cache := s.DimensionChunks(dim)
		cp := ent.ChunkPos()

		curX, curY, curZ := ent.X, ent.Y, ent.Z
		onGround := ent.OnGround
		inWater, waterDepth, inLava := breezeWaterStateAt(cache, curX, curY, curZ)
		isPassenger := s.EntityMountedVehicle(ent.ID) > 0
		hasLevitation := s.HasEntityEffect(ent.ID, EffectLevitation)
		shouldSwim := (inWater && waterDepth > BreezeEyeHeight) || inLava

		mgr := s.breezeManager()
		mgr.mu.Lock()

		// 1. Tick Brain memory expiries (`Brain.tickMemories`) and whirl sound timer (`Breeze.tick`).
		if st.ShootCooldownTicks > 0 {
			st.ShootCooldownTicks--
		}
		if st.LongJumpCooldownTicks > 0 {
			st.LongJumpCooldownTicks--
		}
		if st.HurtRecentlyTicks > 0 {
			st.HurtRecentlyTicks--
		}
		if st.ShootMemoryTicks > 0 {
			st.ShootMemoryTicks--
			st.ShootAfterJumpTicks = st.ShootMemoryTicks
		} else if st.ShootAfterJumpTicks > 0 {
			st.ShootAfterJumpTicks--
			st.ShootMemoryTicks = st.ShootAfterJumpTicks
		}
		if st.InhaleTicks > 0 {
			st.InhaleTicks--
		}
		if st.ShootRecoverTicks > 0 {
			st.ShootRecoverTicks--
		}
		if st.ShootPoseTicks > 0 {
			st.ShootPoseTicks--
		}
		if st.JumpInhaleTicks > 0 {
			st.JumpInhaleTicks--
		}

		if st.SoundTick <= 0 {
			nextWhirl := 40
			if rng != nil {
				nextWhirl = 1 + rng.Intn(BreezeWhirlMaxSoundTicks)
			}
			st.SoundTick = nextWhirl
		} else {
			st.SoundTick--
		}
		playWhirl := st.SoundTick == 0

		// 2. Airborne / falling / swimming physics (`Monster.tick` + `discardFriction`).
		var landedFromFall float64
		if st.Pose == PoseLongJumping && st.JumpActive {
			vx, vy, vz := st.JumpVX, st.JumpVY, st.JumpVZ
			candX := curX + vx
			candY := curY + vy
			candZ := curZ + vz

			if cache != nil {
				// Horizontal collider check.
				if !isAABBCollisionFree2612(cache, candX, curY+0.05, candZ, BreezeWidth, BreezeHeight) {
					candX, candZ = curX, curZ
					vx, vz = 0, 0
				}
				if vy > 0 && !isAABBCollisionFree2612(cache, candX, candY, candZ, BreezeWidth, BreezeHeight) {
					candY = curY
					vy = 0
				}
				belowY := int(math.Floor(candY - 0.01))
				bx, bz := int(math.Floor(candX)), int(math.Floor(candZ))
				if vy <= 0 && belowY >= world.MinY && belowY < world.MinY+world.WorldHeight && isColliderBlockState2612(cache.GetBlock(bx, belowY, bz)) {
					candY = float64(belowY + 1)
					onGround = true
					landedFromFall = st.FallDistance
					st.FallDistance = 0
					vx, vy, vz = 0, 0, 0
				} else {
					onGround = false
					if vy < 0 {
						st.FallDistance += -vy
					}
					vy -= BreezeGravity // `setDiscardFriction(true)` -> pure gravity, no 0.91 air drag!
				}
			} else {
				if candY <= 64.0 && vy <= 0 {
					candY = 64.0
					onGround = true
					vx, vy, vz = 0, 0, 0
				} else {
					onGround = false
					vy -= BreezeGravity
				}
			}
			curX, curY, curZ = candX, candY, candZ
			st.JumpVX, st.JumpVY, st.JumpVZ = vx, vy, vz
			inWater, waterDepth, inLava = breezeWaterStateAt(cache, curX, curY, curZ)
			s.entities.Update(ent.ID, func(e *world.Entity) {
				e.X, e.Y, e.Z = curX, curY, curZ
				e.OnGround = onGround
				e.VelocityX = clampVelocityShort(vx)
				e.VelocityY = clampVelocityShort(vy)
				e.VelocityZ = clampVelocityShort(vz)
			})
		} else if shouldSwim {
			curY += 0.04
			onGround = false
			st.FallDistance = 0
			s.entities.Update(ent.ID, func(e *world.Entity) {
				e.Y = curY
				e.OnGround = false
				e.VelocityY = clampVelocityShort(0.04)
			})
		} else if (!onGround || ent.VelocityY != 0) && !isPassenger && !hasLevitation && cache != nil {
			bx, belowY, bz := int(math.Floor(curX)), int(math.Floor(curY-0.05)), int(math.Floor(curZ))
			if belowY >= world.MinY && belowY < world.MinY+world.WorldHeight && !isColliderBlockState2612(cache.GetBlock(bx, belowY, bz)) && !inWater {
				vy := float64(ent.VelocityY)/8000.0 - BreezeGravity
				if vy < -3.0 {
					vy = -3.0
				}
				nextY := curY + vy
				landBlockY := int(math.Floor(nextY - 0.01))
				if landBlockY >= world.MinY && isColliderBlockState2612(cache.GetBlock(bx, landBlockY, bz)) {
					st.FallDistance += curY - float64(landBlockY+1)
					landedFromFall = st.FallDistance
					st.FallDistance = 0
					curY = float64(landBlockY + 1)
					onGround = true
					vy = 0
				} else {
					st.FallDistance += -vy
					curY = nextY
					onGround = false
				}
				s.entities.Update(ent.ID, func(e *world.Entity) {
					e.Y = curY
					e.OnGround = onGround
					e.VelocityY = clampVelocityShort(vy)
				})
			} else if !inWater {
				if st.FallDistance > 0 {
					landedFromFall = st.FallDistance
					st.FallDistance = 0
				}
				onGround = true
			}
		}
		mgr.mu.Unlock()

		if landedFromFall > BreezeFallLandSoundMinDist {
			s.OnBreezeFallLanded(ent.ID, landedFromFall)
		}
		if playWhirl {
			pitch := float32(0.9)
			vol := float32(0.9)
			if rng != nil {
				pitch = 0.7 + 0.4*rng.Float32()
				vol = 0.8 + 0.2*rng.Float32()
			}
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
				EncodeSoundPacket(SoundBreezeWhirl, SoundSourceHostile, curX, curY, curZ, vol, pitch, 0))
		}

		// 3. Target sensing (`BreezeAttackEntitySensor` + `StopAttackingIfTargetInvalid` 100t).
		var (
			target            playerTarget
			targetFound       bool
			targetHeight      = 1.8
			targetHeadYaw     float32
			targetIsPassenger bool
		)
		if pt, ok := selectTargetWithinRange(targets, curX, curY, curZ, BreezeFollowRange); ok {
			target = pt
			targetFound = true
			if pt.session != nil {
				snap := pt.session.Snapshot()
				targetHeadYaw = snap.Yaw
				targetIsPassenger = s.EntityMountedVehicle(snap.EntityID) > 0
			}
		} else {
			bestDistSq := BreezeFollowRange * BreezeFollowRange
			for j := range allEnts {
				other := &allEnts[j]
				if other.ID == ent.ID || other.EffectiveDimension() != dim || !CanBreezeAttackTarget(other.TypeName) || s.MobHealth(other.ID) <= 0 {
					continue
				}
				ddx := other.X - curX
				ddy := other.Y - curY
				ddz := other.Z - curZ
				dSq := ddx*ddx + ddy*ddy + ddz*ddz
				if dSq <= bestDistSq {
					bestDistSq = dSq
					target = playerTarget{x: other.X, y: other.Y, z: other.Z}
					targetHeight = 2.7
					targetHeadYaw = other.HeadYaw
					targetIsPassenger = s.EntityMountedVehicle(other.ID) > 0
					targetFound = true
				}
			}
		}

		if targetFound {
			hasLOS := BreezeHasLineOfSight2612(cache, curX, curY+BreezeEyeHeight, curZ, target.x, target.y+targetHeight*0.5, target.z, BreezeFollowRange)
			mgr.mu.Lock()
			if hasLOS {
				st.TargetUnseenTicks = 0
			} else {
				st.TargetUnseenTicks++
				if st.TargetUnseenTicks >= BreezeTargetRememberTicks {
					targetFound = false
				}
			}
			mgr.mu.Unlock()
		}

		if !targetFound {
			mgr.mu.Lock()
			prevPose := st.Pose
			st.Pose = PoseStanding
			st.ShootActive = false
			st.ShootActiveTicks = 0
			st.ShotFiredInCycle = false
			st.InhaleTicks = 0
			st.ShootRecoverTicks = 0
			st.ShootPoseTicks = 0
			st.JumpActive = false
			st.JumpTargetSet = false
			st.JumpInhaleTicks = 0
			st.LeavingWater = false
			st.WalkTargetSet = false
			st.SlideTicksRemaining = 0
			mgr.mu.Unlock()
			if prevPose != PoseStanding {
				s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB, EncodeBreezePoseDataPacket(ent.ID, PoseStanding))
			}
			continue
		}

		dx := target.x - curX
		dy := target.y - curY
		dz := target.z - curZ
		distSq := dx*dx + dy*dy + dz*dz
		dist3D := math.Sqrt(distSq)
		yaw := yawToward(dx, dz)

		// 4. Tick currently active behavior (`Shoot`, `LongJump`, or `SlideToTargetSink`).
		mgr.mu.Lock()
		shootActive := st.ShootActive
		jumpActive := st.JumpActive
		walkActive := st.WalkTargetSet
		mgr.mu.Unlock()

		if shootActive {
			s.entities.Update(ent.ID, func(e *world.Entity) {
				e.Yaw, e.HeadYaw = yaw, yaw
			})
			mgr.mu.Lock()
			st.ShootActiveTicks++
			canStill := st.ShootMemoryTicks > 0 && st.ShootActiveTicks < BreezeShootMaxDurationTicks
			if !canStill {
				wasShooting := st.Pose == PoseShooting
				if wasShooting {
					st.Pose = PoseStanding
				}
				st.ShootActive = false
				st.ShootActiveTicks = 0
				st.ShotFiredInCycle = false
				st.InhaleTicks = 0
				st.ShootRecoverTicks = 0
				st.ShootPoseTicks = 0
				st.ShootCooldownTicks = BreezeShootBrainCooldownTicks
				st.ShootMemoryTicks = 0
				st.ShootAfterJumpTicks = 0
				mgr.mu.Unlock()
				if wasShooting {
					s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB, EncodeBreezePoseDataPacket(ent.ID, PoseStanding))
				}
				continue
			}
			needFire := st.InhaleTicks == 0 && st.ShootRecoverTicks == 0 && !st.ShotFiredInCycle
			if needFire {
				st.ShotFiredInCycle = true
				st.ShootRecoverTicks = BreezeShootRecoverTicks
				st.ShootPoseTicks = BreezeShootRecoverTicks
			}
			mgr.mu.Unlock()
			if needFire {
				if curEnt := s.entities.Get(ent.ID); curEnt != nil {
					if cid, err := s.spawnBreezeShotProjectile(ent.ID, curEnt, target.x, target.y, target.z, targetHeight, targetIsPassenger); err == nil {
						mgr.mu.Lock()
						st.LastShotChargeID = cid
						mgr.mu.Unlock()
					}
				}
			}
			continue
		}

		if jumpActive {
			mgr.mu.Lock()
			st.JumpActiveTicks++
			if !inWater && st.LeavingWater {
				st.LeavingWater = false
			}
			finishedInhaling := st.Pose == PoseInhaling && st.JumpInhaleTicks == 0
			finishedJumping := st.Pose == PoseLongJumping && (onGround || (inWater && !st.LeavingWater))
			timedOut := st.JumpActiveTicks > BreezeLongJumpMaxDurationTicks
			jx, jy, jz := st.JumpTargetX, st.JumpTargetY, st.JumpTargetZ
			mgr.mu.Unlock()

			if finishedInhaling {
				jumpBoostAmp := -1
				for _, eff := range s.EntityEffects(ent.ID) {
					if canonicalEffectID(eff.ID) == EffectJumpBoost {
						jumpBoostAmp = int(eff.Amplifier)
						break
					}
				}
				vx, vy, vz, ok := SelectBreezeOptimalJumpVector2612(cache, rng, curX, curY, curZ, jx, jy, jz, BreezeFollowRange, jumpBoostAmp, false)
				if !ok {
					mgr.mu.Lock()
					st.Pose = PoseStanding
					st.JumpActive = false
					st.JumpActiveTicks = 0
					st.JumpTargetSet = false
					st.LeavingWater = false
					mgr.mu.Unlock()
					s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB, EncodeBreezePoseDataPacket(ent.ID, PoseStanding))
					continue
				}
				jumpYaw := yawToward(float64(jx)+0.5-curX, float64(jz)+0.5-curZ)
				mgr.mu.Lock()
				if inWater {
					st.LeavingWater = true
				}
				st.Pose = PoseLongJumping
				st.JumpVX, st.JumpVY, st.JumpVZ = vx, vy, vz
				mgr.mu.Unlock()
				s.entities.Update(ent.ID, func(e *world.Entity) {
					e.OnGround = false
					e.Yaw, e.HeadYaw = jumpYaw, jumpYaw
					e.VelocityX = clampVelocityShort(vx)
					e.VelocityY = clampVelocityShort(vy)
					e.VelocityZ = clampVelocityShort(vz)
				})
				s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB, EncodeBreezePoseDataPacket(ent.ID, PoseLongJumping))
				s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlaySetEntityMotion, EncodeSetEntityMotionPacket(ent.ID, vx, vy, vz))
				s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
					EncodeSoundPacket(SoundBreezeJump, SoundSourceHostile, curX, curY, curZ, 1.0, 1.0, 0))
				continue
			}

			if finishedJumping || timedOut {
				mgr.mu.Lock()
				st.Pose = PoseStanding
				st.JumpActive = false
				st.JumpActiveTicks = 0
				st.JumpTargetSet = false
				st.JumpInhaleTicks = 0
				st.LeavingWater = false
				st.JumpVX, st.JumpVY, st.JumpVZ = 0, 0, 0
				if finishedJumping {
					if st.HurtRecentlyTicks > 0 {
						st.LongJumpCooldownTicks = BreezeJumpCooldownWhenHurtTicks
					} else {
						st.LongJumpCooldownTicks = BreezeJumpCooldownNormalTicks
					}
					st.ShootMemoryTicks = BreezeShootAfterJumpExpiryTicks
					st.ShootAfterJumpTicks = BreezeShootAfterJumpExpiryTicks
				}
				mgr.mu.Unlock()
				s.entities.Update(ent.ID, func(e *world.Entity) {
					e.OnGround = onGround
					e.VelocityX, e.VelocityY, e.VelocityZ = 0, 0, 0
				})
				s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB, EncodeBreezePoseDataPacket(ent.ID, PoseStanding))
				if finishedJumping {
					s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
						EncodeSoundPacket(SoundBreezeLand, SoundSourceHostile, curX, curY, curZ, 1.0, 1.0, 0))
				}
			}
			continue
		}

		if walkActive {
			mgr.mu.Lock()
			wx := float64(st.WalkTargetX) + 0.5
			wz := float64(st.WalkTargetZ) + 0.5
			st.SlideTicksRemaining--
			remTicks := st.SlideTicksRemaining
			mgr.mu.Unlock()

			feetY := int(math.Floor(curY + 0.01))
			wdx := wx - curX
			wdz := wz - curZ
			wDist := math.Hypot(wdx, wdz)
			stopSlide := !onGround || inWater || remTicks <= 0 || wDist <= 1.0
			if !stopSlide && wDist > 1e-4 {
				step := BreezeSlideSpeed
				if step > wDist {
					step = wDist
				}
				candX := curX + (wdx/wDist)*step
				candZ := curZ + (wdz/wDist)*step
				moved := false
				if cache != nil {
					if nx, ny, nz, stepOK := tryApplyHorizontalStep(cache, curX, feetY, curZ, candX, candZ); stepOK {
						curX, curY, curZ = nx, float64(ny), nz
						moved = true
					}
				} else {
					curX, curZ = candX, candZ
					moved = true
				}
				if moved {
					s.entities.Update(ent.ID, func(e *world.Entity) {
						e.X, e.Y, e.Z = curX, curY, curZ
						e.Yaw, e.HeadYaw = yaw, yaw
					})
					if math.Hypot(wx-curX, wz-curZ) <= 1.0 {
						stopSlide = true
					}
				} else {
					stopSlide = true
				}
			}
			if stopSlide {
				// `SlideToTargetSink.stop()`: resets `Pose.STANDING` and sets `BREEZE_SHOOT` = 60 ticks!
				mgr.mu.Lock()
				st.WalkTargetSet = false
				st.SlideTicksRemaining = 0
				st.Pose = PoseStanding
				st.ShootMemoryTicks = BreezeSlideShootMemoryExpiryTicks
				st.ShootAfterJumpTicks = BreezeSlideShootMemoryExpiryTicks
				mgr.mu.Unlock()
				s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB, EncodeBreezePoseDataPacket(ent.ID, PoseStanding))
			}
			continue
		}

		// 5. Start highest-priority eligible FIGHT behavior (`1: Shoot` -> `2: LongJump` -> `3: ShootWhenStuck` -> `4: Slide`).
		mgr.mu.Lock()
		pose := st.Pose
		shootMem := st.ShootMemoryTicks
		shootCD := st.ShootCooldownTicks
		jumpCD := st.LongJumpCooldownTicks
		mgr.mu.Unlock()

		// Priority 1: `Shoot`
		if shootMem > 0 && shootCD == 0 && pose == PoseStanding {
			if distSq >= BreezeShootMaxRangeSq {
				// `Shoot.checkExtraStartConditions`: target >= 16 blocks -> erase `BREEZE_SHOOT`!
				mgr.mu.Lock()
				st.ShootMemoryTicks = 0
				st.ShootAfterJumpTicks = 0
				shootMem = 0
				mgr.mu.Unlock()
			} else {
				mgr.mu.Lock()
				st.Pose = PoseShooting
				st.ShootActive = true
				st.ShootActiveTicks = 1
				st.ShotFiredInCycle = false
				st.InhaleTicks = BreezeInhaleDurationTicks
				st.ShootRecoverTicks = 0
				mgr.mu.Unlock()
				s.entities.Update(ent.ID, func(e *world.Entity) {
					e.Yaw, e.HeadYaw = yaw, yaw
				})
				s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB, EncodeBreezePoseDataPacket(ent.ID, PoseShooting))
				s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
					EncodeSoundPacket(SoundBreezeInhale, SoundSourceHostile, curX, curY, curZ, 1.0, 1.0, 0))
				continue
			}
		}

		// Priority 2: `LongJump`
		bx, by, bz := int(math.Floor(curX)), int(math.Floor(curY+0.01)), int(math.Floor(curZ))
		if jumpCD == 0 && shootMem == 0 && (onGround || inWater) && !shouldSwim &&
			dist3D < BreezeFollowRange && (float32(dist3D)-4.0) > 0 &&
			CanBreezeJumpFromPosition(cache, bx, by, bz) {
			jumpSelected := false
			var selX, selY, selZ int
			for attempt := 0; attempt < 4; attempt++ {
				px, py, pz := BreezeRandomPointBehindTarget2612(target.x, target.y, target.z, targetHeadYaw, rng)
				jx, jy, jz, snapped := SnapBreezeJumpTargetToSurface2612(cache, px, py, pz)
				if !snapped || !CanBreezeSelectJumpLandingBlock2612(cache, curX, curY, curZ, jx, jy, jz, BreezeFollowRange) {
					continue
				}
				if _, _, _, vecOK := SelectBreezeOptimalJumpVector2612(cache, rng, curX, curY, curZ, jx, jy, jz, BreezeFollowRange, -1, false); !vecOK {
					continue
				}
				selX, selY, selZ = jx, jy, jz
				jumpSelected = true
				break
			}
			if jumpSelected {
				lookYaw := yawToward(float64(selX)+0.5-curX, float64(selZ)+0.5-curZ)
				mgr.mu.Lock()
				st.JumpTargetSet = true
				st.JumpTargetX, st.JumpTargetY, st.JumpTargetZ = selX, selY, selZ
				st.JumpInhaleTicks = BreezeJumpInhaleDurationTicks
				st.JumpActive = true
				st.JumpActiveTicks = 0
				st.Pose = PoseInhaling
				mgr.mu.Unlock()
				s.entities.Update(ent.ID, func(e *world.Entity) {
					e.Yaw, e.HeadYaw = lookYaw, lookYaw
				})
				s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB, EncodeBreezePoseDataPacket(ent.ID, PoseInhaling))
				s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
					EncodeSoundPacket(SoundBreezeCharge, SoundSourceHostile, curX, curY, curZ, 1.0, 1.0, 0))
				continue
			}
		}

		// Priority 3: `ShootWhenStuck` (`isPassenger || isInWater || hasEffect(LEVITATION)`)
		if shootMem == 0 && IsBreezeStuck(isPassenger, inWater, hasLevitation) {
			mgr.mu.Lock()
			st.ShootMemoryTicks = BreezeShootWhenStuckExpiryTicks
			st.ShootAfterJumpTicks = BreezeShootWhenStuckExpiryTicks
			if shootCD == 0 && st.Pose == PoseStanding && distSq < BreezeShootMaxRangeSq {
				st.Pose = PoseShooting
				st.ShootActive = true
				st.ShootActiveTicks = 1
				st.ShotFiredInCycle = false
				st.InhaleTicks = BreezeInhaleDurationTicks
				st.ShootRecoverTicks = 0
				mgr.mu.Unlock()
				s.entities.Update(ent.ID, func(e *world.Entity) {
					e.Yaw, e.HeadYaw = yaw, yaw
				})
				s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB, EncodeBreezePoseDataPacket(ent.ID, PoseShooting))
				s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
					EncodeSoundPacket(SoundBreezeInhale, SoundSourceHostile, curX, curY, curZ, 1.0, 1.0, 0))
			} else {
				mgr.mu.Unlock()
			}
			continue
		}

		// Priority 4: `Slide` + `SlideToTargetSink`
		if jumpCD == 0 && shootMem == 0 && onGround && !inWater && pose == PoseStanding {
			var slideX, slideY, slideZ float64
			picked := false
			if WithinBreezeInnerCircleRange(curX, curY, curZ, target.x, target.y, target.z) {
				awayDX := curX - target.x
				awayDZ := curZ - target.z
				awayLen := math.Hypot(awayDX, awayDZ)
				if awayLen < 1e-4 {
					awayDX, awayDZ, awayLen = 1, 0, 1
				}
				candX := curX + (awayDX/awayLen)*5.0
				candZ := curZ + (awayDZ/awayLen)*5.0
				if BreezeHasLineOfSight2612(cache, curX, curY, curZ, candX, curY, candZ, BreezeFollowRange) {
					slideX, slideY, slideZ = candX, curY, candZ
					picked = true
				}
			}
			if !picked {
				useBehind := rng != nil && rng.Intn(2) == 0
				if useBehind {
					slideX, slideY, slideZ = BreezeRandomPointBehindTarget2612(target.x, target.y, target.z, targetHeadYaw, rng)
				} else {
					slideX, slideY, slideZ = BreezeRandomPointInMiddleCircle2612(curX, curY, curZ, target.x, target.y, target.z, rng)
				}
			}
			dur := BreezeSlideMinDurationTicks
			if rng != nil {
				dur += rng.Intn(BreezeSlideMaxDurationTicks - BreezeSlideMinDurationTicks + 1)
			}
			wx, wy, wz := int(math.Floor(slideX)), int(math.Floor(slideY)), int(math.Floor(slideZ))
			mgr.mu.Lock()
			st.WalkTargetSet = true
			st.WalkTargetX, st.WalkTargetY, st.WalkTargetZ = wx, wy, wz
			st.SlideTicksRemaining = dur
			st.Pose = PoseSliding
			mgr.mu.Unlock()
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB, EncodeBreezePoseDataPacket(ent.ID, PoseSliding))
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
				EncodeSoundPacket(SoundBreezeSlide, SoundSourceHostile, curX, curY, curZ, 1.0, 1.0, 0))
		}
	}
}
