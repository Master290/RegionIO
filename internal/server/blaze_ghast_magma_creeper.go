package server

import (
	"errors"
	"math"
	"math/rand"
	"strings"
	"sync"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

// ============================================================================
// BLAZE, GHAST, MAGMA CUBE & CHARGED CREEPER (VANILLA 26.1.2, PROTOCOL 775)
// ============================================================================

const (
	MobTypeBlaze       = "minecraft:blaze"
	MobTypeGhast       = "minecraft:ghast"
	EntityTypeFireball = "minecraft:fireball"

	// Vanilla 26.1.2 BuiltInRegistries.ENTITY_TYPE numeric network IDs (protocol 775).
	EntityTypeBlazeID         int = 14
	EntityTypeFireballID      int = 52
	EntityTypeGhastID         int = 57
	EntityTypeSmallFireballID int = 118

	// Default Max Health values (vanilla 26.1.2).
	DefaultBlazeMaxHealth     float32 = 20.0 // 10 hearts
	DefaultGhastMaxHealth     float32 = 10.0 // 5 hearts
	DefaultMagmaCubeMaxHealth float32 = 16.0 // Size 4 big magma cube (size^2)

	// Blaze combat, flight, fireball burst, smoke, and vulnerability parameters.
	BlazeMeleeAttackDamage          float32 = 6.0
	BlazeMeleeAttackReach           float64 = 2.0
	BlazeAttackCooldownTicks        int     = 20
	BlazeSmallFireballDamage        float32 = 5.0
	BlazeSmallFireballIgniteTicks   int     = 100 // 5 seconds on fire
	BlazeSmallFireballSpeed         float64 = 0.9
	BlazeAggroRange                 float64 = 48.0
	BlazeFlySpeed                   float64 = 0.23
	BlazeHoverVerticalSpeed         float64 = 0.15
	BlazeHoverHeightOffset          float64 = 1.5
	BlazeChargeWarmupTicks          int     = 60  // 3.0s charge-up before firing burst
	BlazeBurstShotIntervalTicks     int     = 6   // 0.3s between each of the 3 fireballs
	BlazePostBurstCooldownTicks     int     = 100 // 5.0s cooldown after 3-shot burst
	BlazeBurstFireballCount         int     = 3
	BlazeWaterDamage                float32 = 1.0
	BlazeWaterDamageIntervalTicks   int     = 20
	BlazeSnowballDamage             float32 = 3.0
	BlazeSmokeIntervalTicks         int     = 2
	BlazeSmokeParticleID                    = "minecraft:large_smoke"

	// Ghast combat, float, charge cry, and fireball parameters.
	GhastAggroRange              float64 = 64.0
	GhastFlySpeed                float64 = 0.14
	GhastChargeWarnTicks         int     = 10 // Warn cry + red charging face at tick 10
	GhastChargeFireTicks         int     = 20 // Fires large fireball at tick 20
	GhastPostShotCooldownTicks   int     = -40
	GhastFireballSpeed           float64 = 0.85
	GhastFireballDirectHitDamage float32 = 6.0
	GhastDeflectedFireballDamage float32 = 1000.0
	GhastDefaultExplosionPower   int     = 1

	// Magma Cube & Slime jump, split, armor, and lava-swim parameters.
	MagmaCubeSizeSmall         int     = 1
	MagmaCubeSizeMedium        int     = 2
	MagmaCubeSizeLarge         int     = 4
	MagmaCubeAggroRange        float64 = 16.0
	MagmaCubeBaseJumpDelayMin  int     = 40
	MagmaCubeBaseJumpDelayMax  int     = 100
	MagmaCubeLavaJumpVelocity  float64 = 0.28
	MagmaCubeJumpBaseVelocityY float64 = 0.42
	MagmaCubeArmorPerSize      int     = 3 // 3 * size armor points (3, 6, 12)

	// Item and advancement IDs.
	BlazeRodItemID            = "minecraft:blaze_rod"
	AdvancementReturnToSender = "minecraft:nether/return_to_sender"
	ZombieHeadItemID          = "minecraft:zombie_head"
	SkeletonSkullItemID       = "minecraft:skeleton_skull"
	WitherSkeletonSkullItemID = "minecraft:wither_skeleton_skull"
	CreeperHeadItemID         = "minecraft:creeper_head"
	PiglinHeadItemID          = "minecraft:piglin_head"

	// Charged Creeper lightning strike and skull drop parameters.
	CreeperLightningStrikeRadius float64 = 4.0

	// Experience orb rewards on death (vanilla 26.1.2).
	BlazeXPReward int = 10
	GhastXPReward int = 5

	// SynchedEntityData indices in 26.1.2 (protocol 775):
	//   - Blaze.DATA_FLAGS_ID (index 16, BYTE serializer = 0, bit 0x01 = charged/on fire)
	//   - Ghast.DATA_IS_CHARGING (index 16, BOOLEAN serializer = 8)
	//   - Slime/MagmaCube.ID_SIZE (index 16, INT serializer = 1)
	EntityDataBlazeFlagsID    byte = 16
	BlazeFlagCharged          byte = 0x01
	EntityDataGhastChargingID byte = 16
	EntityDataSlimeSizeID     byte = 16

	// Sound events (vanilla 26.1.2).
	SoundBlazeAmbient          = "minecraft:entity.blaze.ambient"
	SoundBlazeBurn             = "minecraft:entity.blaze.burn"
	SoundBlazeDeath            = "minecraft:entity.blaze.death"
	SoundBlazeHurt             = "minecraft:entity.blaze.hurt"
	SoundBlazeShoot            = "minecraft:entity.blaze.shoot"
	SoundGhastAmbient          = "minecraft:entity.ghast.ambient"
	SoundGhastDeath            = "minecraft:entity.ghast.death"
	SoundGhastHurt             = "minecraft:entity.ghast.hurt"
	SoundGhastScream           = "minecraft:entity.ghast.scream"
	SoundGhastShoot            = "minecraft:entity.ghast.shoot"
	SoundGhastWarn             = "minecraft:entity.ghast.warn"
	SoundMagmaCubeDeath        = "minecraft:entity.magma_cube.death"
	SoundMagmaCubeDeathSmall   = "minecraft:entity.magma_cube.death_small"
	SoundMagmaCubeHurt         = "minecraft:entity.magma_cube.hurt"
	SoundMagmaCubeHurtSmall    = "minecraft:entity.magma_cube.hurt_small"
	SoundMagmaCubeJump         = "minecraft:entity.magma_cube.jump"
	SoundMagmaCubeSquish       = "minecraft:entity.magma_cube.squish"
	SoundMagmaCubeSquishSmall  = "minecraft:entity.magma_cube.squish_small"
	SoundSlimeAttack           = "minecraft:entity.slime.attack"
	SoundSlimeDeath            = "minecraft:entity.slime.death"
	SoundSlimeHurt             = "minecraft:entity.slime.hurt"
	SoundSlimeJump             = "minecraft:entity.slime.jump"
	SoundSlimeSquish           = "minecraft:entity.slime.squish"
)

// BlazeState tracks hovering, fire-charge state (`DATA_FLAGS_ID`), 3-fireball
// burst progress, smoke emission, and water damage timer for a `minecraft:blaze`.
type BlazeState struct {
	EntityID         int32
	Dimension        string
	Charged          bool
	AttackStep       int // 0=cooldown, 1=charging, 2..4=firing 3 small_fireballs
	AttackTime       int
	AttackCooldown   int
	WaterDamageTimer int
	SmokeEmitted     int
	FireballsShot    int
	TargetPlayer     *PlayerSession
	TickCounter      int
}

// GhastState tracks floating waypoints, `DATA_IS_CHARGING`, cry/warn/shoot
// timers, and target player for a `minecraft:ghast`.
type GhastState struct {
	EntityID       int32
	Dimension      string
	Charging       bool
	ChargeTicks    int
	ExplosionPower int
	WaypointX      float64
	WaypointY      float64
	WaypointZ      float64
	MoveCooldown   int
	CryCount       int
	TargetPlayer   *PlayerSession
	TickCounter    int
}

// FireballProjectileState tracks an active `minecraft:fireball` (Ghast large
// fireball) or `minecraft:small_fireball` (Blaze fireball) entity in flight.
type FireballProjectileState struct {
	EntityID       int32
	TypeName       string
	Dimension      string
	ShooterID      int32
	ShooterType    string
	VX, VY, VZ     float64
	ExplosionPower int
	Deflected      bool
	DeflectedBy    *PlayerSession
	AgeTicks       int
}

// MagmaCubeState tracks jump delay, lava swimming, and melee cooldown for an
// active `minecraft:magma_cube` or `minecraft:slime`.
type MagmaCubeState struct {
	EntityID       int32
	TypeName       string
	Dimension      string
	JumpDelay      int
	AttackCooldown int
	InLava         bool
	JumpCount      int
	TargetPlayer   *PlayerSession
	TickCounter    int
}

type netherHostileManager struct {
	mu         sync.RWMutex
	blazes     map[int32]*BlazeState
	ghasts     map[int32]*GhastState
	fireballs  map[int32]*FireballProjectileState
	magmaCubes map[int32]*MagmaCubeState
}

var serverNetherHostileManagers sync.Map // map[*Server]*netherHostileManager

func (s *Server) netherHostileManager() *netherHostileManager {
	if s == nil {
		return &netherHostileManager{
			blazes:     make(map[int32]*BlazeState),
			ghasts:     make(map[int32]*GhastState),
			fireballs:  make(map[int32]*FireballProjectileState),
			magmaCubes: make(map[int32]*MagmaCubeState),
		}
	}
	if v, ok := serverNetherHostileManagers.Load(s); ok {
		return v.(*netherHostileManager)
	}
	mgr := &netherHostileManager{
		blazes:     make(map[int32]*BlazeState),
		ghasts:     make(map[int32]*GhastState),
		fireballs:  make(map[int32]*FireballProjectileState),
		magmaCubes: make(map[int32]*MagmaCubeState),
	}
	actual, _ := serverNetherHostileManagers.LoadOrStore(s, mgr)
	return actual.(*netherHostileManager)
}

// NetherHostileXPReward returns the vanilla 26.1.2 XP reward for Blaze (10),
// Ghast (5), or MagmaCube/Slime (`slimeSize`: 1, 2, or 4).
func NetherHostileXPReward(typeName string, slimeSize int) int {
	switch canonicalResourceName(typeName) {
	case MobTypeBlaze:
		return BlazeXPReward
	case MobTypeGhast:
		return GhastXPReward
	case MobTypeMagmaCube, MobTypeSlime:
		if slimeSize < 1 {
			slimeSize = 1
		}
		return slimeSize
	default:
		return 0
	}
}

// IsFireballEntityType reports whether typeName is `minecraft:fireball` or `minecraft:small_fireball`.
func IsFireballEntityType(typeName string) bool {
	norm := canonicalResourceName(typeName)
	return norm == EntityTypeFireball || norm == EntityTypeSmallFireball
}

// ActiveFireballs returns snapshots of all currently active `minecraft:fireball`
// and `minecraft:small_fireball` projectiles.
func (s *Server) ActiveFireballs() []FireballProjectileState {
	if s == nil {
		return nil
	}
	mgr := s.netherHostileManager()
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()
	out := make([]FireballProjectileState, 0, len(mgr.fireballs))
	for _, fb := range mgr.fireballs {
		if fb != nil {
			out = append(out, *fb)
		}
	}
	return out
}

// ============================================================================
// SYNCHED ENTITY DATA PACKETS (BLAZE, GHAST, SLIME/MAGMA CUBE, CREEPER)
// ============================================================================

// EncodeBlazeEntityDataPacket encodes a ClientboundSetEntityDataPacket (0x63)
// updating Blaze.DATA_FLAGS_ID (index 16, BYTE: 0x01 = charged/on fire) and
// Entity shared flags (index 0, BYTE: 0x01 = on fire).
func EncodeBlazeEntityDataPacket(entityID int32, charged bool) []byte {
	var flags byte
	if charged {
		flags = BlazeFlagCharged
	}
	w := protocol.NewWriter(16)
	w.VarInt(entityID)
	w.Byte(0) // Entity shared flags (index 0)
	w.VarInt(EntityDataSerializerByte)
	w.Byte(flags)
	w.Byte(EntityDataBlazeFlagsID)
	w.VarInt(EntityDataSerializerByte)
	w.Byte(flags)
	w.Byte(0xFF)
	return w.Bytes()
}

// DecodeBlazeEntityDataPacket decodes a ClientboundSetEntityDataPacket (0x63)
// produced by EncodeBlazeEntityDataPacket.
func DecodeBlazeEntityDataPacket(data []byte) (entityID int32, charged bool, err error) {
	r := protocol.NewReader(data)
	entityID, err = r.VarInt()
	if err != nil {
		return 0, false, err
	}
	for {
		idx, err := r.ReadByte()
		if err != nil {
			return 0, false, err
		}
		if idx == 0xFF {
			return entityID, charged, nil
		}
		ser, err := r.VarInt()
		if err != nil {
			return 0, false, err
		}
		if ser != EntityDataSerializerByte {
			return 0, false, errors.New("blaze: unexpected entity data serializer")
		}
		val, err := r.ReadByte()
		if err != nil {
			return 0, false, err
		}
		if idx == EntityDataBlazeFlagsID {
			charged = (val & BlazeFlagCharged) != 0
		}
	}
}

// EncodeGhastEntityDataPacket encodes a ClientboundSetEntityDataPacket (0x63)
// updating Ghast.DATA_IS_CHARGING (index 16, BOOLEAN).
func EncodeGhastEntityDataPacket(entityID int32, charging bool) []byte {
	w := protocol.NewWriter(12)
	w.VarInt(entityID)
	w.Byte(EntityDataGhastChargingID)
	w.VarInt(EntityDataSerializerBool)
	w.Bool(charging)
	w.Byte(0xFF)
	return w.Bytes()
}

// DecodeGhastEntityDataPacket decodes a ClientboundSetEntityDataPacket (0x63)
// produced by EncodeGhastEntityDataPacket.
func DecodeGhastEntityDataPacket(data []byte) (entityID int32, charging bool, err error) {
	r := protocol.NewReader(data)
	entityID, err = r.VarInt()
	if err != nil {
		return 0, false, err
	}
	idx, err := r.ReadByte()
	if err != nil || idx != EntityDataGhastChargingID {
		return 0, false, errors.New("ghast: unexpected entity data index")
	}
	ser, err := r.VarInt()
	if err != nil || ser != EntityDataSerializerBool {
		return 0, false, errors.New("ghast: unexpected entity data serializer")
	}
	charging, err = r.Bool()
	if err != nil {
		return 0, false, err
	}
	term, err := r.ReadByte()
	if err != nil || term != 0xFF {
		return 0, false, errors.New("ghast: missing 0xFF terminator")
	}
	return entityID, charging, nil
}

// EncodeSlimeSizeDataPacket encodes a ClientboundSetEntityDataPacket (0x63)
// updating Slime/MagmaCube.ID_SIZE (index 16, INT).
func EncodeSlimeSizeDataPacket(entityID, size int32) []byte {
	w := protocol.NewWriter(12)
	w.VarInt(entityID)
	w.Byte(EntityDataSlimeSizeID)
	w.VarInt(EntityDataSerializerInt)
	w.VarInt(size)
	w.Byte(0xFF)
	return w.Bytes()
}

// DecodeSlimeSizeDataPacket decodes a ClientboundSetEntityDataPacket (0x63)
// produced by EncodeSlimeSizeDataPacket.
func DecodeSlimeSizeDataPacket(data []byte) (entityID, size int32, err error) {
	r := protocol.NewReader(data)
	entityID, err = r.VarInt()
	if err != nil {
		return 0, 0, err
	}
	idx, err := r.ReadByte()
	if err != nil || idx != EntityDataSlimeSizeID {
		return 0, 0, errors.New("slime: unexpected entity data index")
	}
	ser, err := r.VarInt()
	if err != nil || ser != EntityDataSerializerInt {
		return 0, 0, errors.New("slime: unexpected entity data serializer")
	}
	size, err = r.VarInt()
	if err != nil {
		return 0, 0, err
	}
	term, err := r.ReadByte()
	if err != nil || term != 0xFF {
		return 0, 0, errors.New("slime: missing 0xFF terminator")
	}
	return entityID, size, nil
}

// EncodeCreeperEntityDataPacket encodes a ClientboundSetEntityDataPacket (0x63)
// updating Creeper.DATA_SWELL_DIR (index 17, INT), DATA_IS_POWERED (index 18,
// BOOLEAN), and DATA_IS_IGNITED (index 19, BOOLEAN).
func EncodeCreeperEntityDataPacket(entityID, swellDir int32, powered, ignited bool) []byte {
	w := protocol.NewWriter(20)
	w.VarInt(entityID)
	w.Byte(EntityDataCreeperSwellDirID)
	w.VarInt(EntityDataSerializerInt)
	w.VarInt(swellDir)
	w.Byte(EntityDataCreeperPoweredID)
	w.VarInt(EntityDataSerializerBool)
	w.Bool(powered)
	w.Byte(EntityDataCreeperIgnitedID)
	w.VarInt(EntityDataSerializerBool)
	w.Bool(ignited)
	w.Byte(0xFF)
	return w.Bytes()
}

// DecodeCreeperEntityDataPacket decodes a ClientboundSetEntityDataPacket (0x63)
// produced by EncodeCreeperEntityDataPacket.
func DecodeCreeperEntityDataPacket(data []byte) (entityID, swellDir int32, powered, ignited bool, err error) {
	r := protocol.NewReader(data)
	entityID, err = r.VarInt()
	if err != nil {
		return 0, 0, false, false, err
	}
	for {
		idx, err := r.ReadByte()
		if err != nil {
			return 0, 0, false, false, err
		}
		if idx == 0xFF {
			return entityID, swellDir, powered, ignited, nil
		}
		ser, err := r.VarInt()
		if err != nil {
			return 0, 0, false, false, err
		}
		switch idx {
		case EntityDataCreeperSwellDirID:
			if ser != EntityDataSerializerInt {
				return 0, 0, false, false, errors.New("creeper: unexpected swell_dir serializer")
			}
			swellDir, err = r.VarInt()
		case EntityDataCreeperPoweredID:
			if ser != EntityDataSerializerBool {
				return 0, 0, false, false, errors.New("creeper: unexpected is_powered serializer")
			}
			powered, err = r.Bool()
		case EntityDataCreeperIgnitedID:
			if ser != EntityDataSerializerBool {
				return 0, 0, false, false, errors.New("creeper: unexpected is_ignited serializer")
			}
			ignited, err = r.Bool()
		default:
			return 0, 0, false, false, errors.New("creeper: unexpected entity data index")
		}
		if err != nil {
			return 0, 0, false, false, err
		}
	}
}

// ============================================================================
// 1. BLAZE (FIREBALL BURSTS, PATH ON FIRE, SMOKE, WATER/SNOWBALL DAMAGE)
// ============================================================================

// SpawnBlaze spawns a `minecraft:blaze` at (x, y, z) in dimension, initializes
// its 20 HP health and flight/fireball state, and broadcasts PlayAddEntity.
func (s *Server) SpawnBlaze(dimension string, x, y, z float64) (int32, error) {
	if s == nil || s.entities == nil {
		return 0, errors.New("server: nil entity manager")
	}
	normDim := world.NormalizeDimension(dimension)
	typeID := ResolveEntityTypeID(MobTypeBlaze)
	if typeID < 0 {
		typeID = EntityTypeBlazeID
	}
	id := s.entities.Add(&world.Entity{
		TypeID:    typeID,
		TypeName:  MobTypeBlaze,
		Dimension: normDim,
		X:         x,
		Y:         y,
		Z:         z,
		OnGround:  false,
	})

	tr := s.entityTracker()
	tr.mu.Lock()
	mem := tr.mobStateFor(id, tr.aiTickID)
	mem.healthInit = true
	mem.health = DefaultBlazeMaxHealth
	mem.lastHealth = DefaultBlazeMaxHealth
	tr.mu.Unlock()

	mgr := s.netherHostileManager()
	mgr.mu.Lock()
	mgr.blazes[id] = &BlazeState{
		EntityID:   id,
		Dimension:  normDim,
		AttackStep: 0,
		AttackTime: 20,
	}
	mgr.mu.Unlock()

	if ent := s.entities.Get(id); ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, ent))
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, PlaySetEntityDataCB, EncodeBlazeEntityDataPacket(id, false))
	}
	return id, nil
}

// GetBlazeState returns a snapshot of the BlazeState for entityID.
func (s *Server) GetBlazeState(entityID int32) (BlazeState, bool) {
	if s == nil {
		return BlazeState{}, false
	}
	mgr := s.netherHostileManager()
	mgr.mu.RLock()
	st, ok := mgr.blazes[entityID]
	var out BlazeState
	if ok && st != nil {
		out = *st
	}
	mgr.mu.RUnlock()
	return out, ok && st != nil
}

// IsBlazeOnFire reports whether the Blaze with entityID is currently charged
// (`DATA_FLAGS_ID & 0x01 != 0`, visually burning on its flight path).
func (s *Server) IsBlazeOnFire(entityID int32) bool {
	st, ok := s.GetBlazeState(entityID)
	return ok && st.Charged
}

// SetBlazeCharged sets whether the Blaze with entityID is charged (`DATA_FLAGS_ID`
// bit 0x01) and broadcasts its updated SynchedEntityData packet.
func (s *Server) SetBlazeCharged(entityID int32, charged bool) bool {
	if s == nil || s.entities == nil {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeBlaze {
		return false
	}
	mgr := s.netherHostileManager()
	mgr.mu.Lock()
	st, ok := mgr.blazes[entityID]
	if !ok || st == nil {
		mgr.mu.Unlock()
		return false
	}
	changed := st.Charged != charged
	st.Charged = charged
	mgr.mu.Unlock()

	if changed {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, PlaySetEntityDataCB,
			EncodeBlazeEntityDataPacket(entityID, charged))
	}
	return true
}

// SpawnBlazeSmallFireball spawns a `minecraft:small_fireball` projectile (ID 118)
// launched by a Blaze (or dispenser) at (x, y, z) along direction (dirX, dirY, dirZ).
func (s *Server) SpawnBlazeSmallFireball(dimension string, shooterID int32, x, y, z, dirX, dirY, dirZ float64) (int32, error) {
	if s == nil || s.entities == nil {
		return 0, errors.New("server: nil entity manager")
	}
	normDim := world.NormalizeDimension(dimension)
	mag := math.Sqrt(dirX*dirX + dirY*dirY + dirZ*dirZ)
	if mag < 1e-6 {
		dirX, dirY, dirZ = 0, 0, 1
		mag = 1.0
	}
	vx := (dirX / mag) * BlazeSmallFireballSpeed
	vy := (dirY / mag) * BlazeSmallFireballSpeed
	vz := (dirZ / mag) * BlazeSmallFireballSpeed

	typeID := ResolveEntityTypeID(EntityTypeSmallFireball)
	if typeID < 0 {
		typeID = EntityTypeSmallFireballID
	}
	id := s.entities.Add(&world.Entity{
		TypeID:    typeID,
		TypeName:  EntityTypeSmallFireball,
		Dimension: normDim,
		X:         x,
		Y:         y,
		Z:         z,
		Yaw:       yawToward(vx, vz),
		VelocityX: clampVelocityShort(vx),
		VelocityY: clampVelocityShort(vy),
		VelocityZ: clampVelocityShort(vz),
		OnGround:  false,
	})

	mgr := s.netherHostileManager()
	mgr.mu.Lock()
	mgr.fireballs[id] = &FireballProjectileState{
		EntityID:    id,
		TypeName:    EntityTypeSmallFireball,
		Dimension:   normDim,
		ShooterID:   shooterID,
		ShooterType: MobTypeBlaze,
		VX:          vx,
		VY:          vy,
		VZ:          vz,
	}
	mgr.mu.Unlock()

	if ent := s.entities.Get(id); ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, ent))
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, PlaySoundPacketID,
			EncodeSoundPacket(SoundBlazeShoot, SoundSourceHostile, x, y, z, 1.0, 1.0, 0))
	}
	return id, nil
}

// OnBlazeHitBySnowball applies the vanilla 3.0 damage vulnerability when a
// `minecraft:blaze` is struck by a `minecraft:snowball`.
func (s *Server) OnBlazeHitBySnowball(entityID int32, thrower *PlayerSession) (dealt float32, killed bool) {
	if s == nil || s.entities == nil {
		return 0, false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeBlaze {
		return 0, false
	}
	killed = s.applyDamageToMob(ent, BlazeSnowballDamage, thrower, 0.2, false, false)
	return BlazeSnowballDamage, killed
}

// OnBlazeDamaged aggros the Blaze against attacker when hurt.
func (s *Server) OnBlazeDamaged(entityID int32, attacker *PlayerSession) {
	if s == nil || attacker == nil || attacker.IsDead() {
		return
	}
	aSnap := attacker.Snapshot()
	if aSnap.GameMode == GameModeCreative || aSnap.GameMode == GameModeSpectator {
		return
	}
	mgr := s.netherHostileManager()
	mgr.mu.Lock()
	if st, ok := mgr.blazes[entityID]; ok && st != nil {
		st.TargetPlayer = attacker
	}
	mgr.mu.Unlock()
}

// OnBlazeKilled cleans up state when a Blaze dies.
func (s *Server) OnBlazeKilled(entityID int32) {
	if s == nil {
		return
	}
	mgr := s.netherHostileManager()
	mgr.mu.Lock()
	delete(mgr.blazes, entityID)
	mgr.mu.Unlock()
}

// RollBlazeDeathLoot rolls vanilla 26.1.2 `minecraft:blaze` death loot:
// 0..1 `minecraft:blaze_rod` (+ 0..lootingLevel bonus) when killed by a player
// (when rng == nil and killedByPlayer == true, returns 1 + lootingLevel rods).
func RollBlazeDeathLoot(rng *rand.Rand, lootingLevel int, killedByPlayer bool) []world.ItemStack {
	if !killedByPlayer {
		return nil
	}
	if lootingLevel < 0 {
		lootingLevel = 0
	}
	count := int32(1)
	if rng != nil {
		count = int32(rng.Intn(2)) // 0..1
		if lootingLevel > 0 {
			count += int32(rng.Intn(lootingLevel + 1))
		}
	} else if lootingLevel > 0 {
		count += int32(lootingLevel)
	}
	if count <= 0 {
		return nil
	}
	if stack := world.NewNamedItemStack("minecraft:blaze_rod", count); !stack.IsEmpty() {
		return []world.ItemStack{stack}
	}
	return nil
}

// ============================================================================
// 2. GHAST (FIREBALL AIM/EXPLODE, DEFLECTION, CRY SOUNDS, CHARGING FACE)
// ============================================================================

// SpawnGhast spawns a `minecraft:ghast` at (x, y, z) in dimension, initializes
// its 10 HP health and floating/charging state, and broadcasts PlayAddEntity.
func (s *Server) SpawnGhast(dimension string, x, y, z float64) (int32, error) {
	if s == nil || s.entities == nil {
		return 0, errors.New("server: nil entity manager")
	}
	normDim := world.NormalizeDimension(dimension)
	typeID := ResolveEntityTypeID(MobTypeGhast)
	if typeID < 0 {
		typeID = EntityTypeGhastID
	}
	id := s.entities.Add(&world.Entity{
		TypeID:    typeID,
		TypeName:  MobTypeGhast,
		Dimension: normDim,
		X:         x,
		Y:         y,
		Z:         z,
		OnGround:  false,
	})

	tr := s.entityTracker()
	tr.mu.Lock()
	mem := tr.mobStateFor(id, tr.aiTickID)
	mem.healthInit = true
	mem.health = DefaultGhastMaxHealth
	mem.lastHealth = DefaultGhastMaxHealth
	tr.mu.Unlock()

	mgr := s.netherHostileManager()
	mgr.mu.Lock()
	mgr.ghasts[id] = &GhastState{
		EntityID:       id,
		Dimension:      normDim,
		ExplosionPower: GhastDefaultExplosionPower,
		WaypointX:      x,
		WaypointY:      y,
		WaypointZ:      z,
	}
	mgr.mu.Unlock()

	if ent := s.entities.Get(id); ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, ent))
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, PlaySetEntityDataCB, EncodeGhastEntityDataPacket(id, false))
	}
	return id, nil
}

// GetGhastState returns a snapshot of the GhastState for entityID.
func (s *Server) GetGhastState(entityID int32) (GhastState, bool) {
	if s == nil {
		return GhastState{}, false
	}
	mgr := s.netherHostileManager()
	mgr.mu.RLock()
	st, ok := mgr.ghasts[entityID]
	var out GhastState
	if ok && st != nil {
		out = *st
	}
	mgr.mu.RUnlock()
	return out, ok && st != nil
}

// SetGhastCharging sets whether the Ghast with entityID is in its pre-shot
// charging state (`DATA_IS_CHARGING`, index 16) and broadcasts entity data.
func (s *Server) SetGhastCharging(entityID int32, charging bool) bool {
	if s == nil || s.entities == nil {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeGhast {
		return false
	}
	mgr := s.netherHostileManager()
	mgr.mu.Lock()
	st, ok := mgr.ghasts[entityID]
	if !ok || st == nil {
		mgr.mu.Unlock()
		return false
	}
	changed := st.Charging != charging
	st.Charging = charging
	mgr.mu.Unlock()

	if changed {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, PlaySetEntityDataCB,
			EncodeGhastEntityDataPacket(entityID, charging))
	}
	return true
}

// SpawnGhastFireball spawns a `minecraft:fireball` (large fireball, ID 52)
// launched by a Ghast at (x, y, z) along direction (dirX, dirY, dirZ).
func (s *Server) SpawnGhastFireball(dimension string, ghastID int32, x, y, z, dirX, dirY, dirZ float64, explosionPower int) (int32, error) {
	if s == nil || s.entities == nil {
		return 0, errors.New("server: nil entity manager")
	}
	if explosionPower <= 0 {
		explosionPower = GhastDefaultExplosionPower
	}
	normDim := world.NormalizeDimension(dimension)
	mag := math.Sqrt(dirX*dirX + dirY*dirY + dirZ*dirZ)
	if mag < 1e-6 {
		dirX, dirY, dirZ = 0, 0, 1
		mag = 1.0
	}
	vx := (dirX / mag) * GhastFireballSpeed
	vy := (dirY / mag) * GhastFireballSpeed
	vz := (dirZ / mag) * GhastFireballSpeed

	typeID := ResolveEntityTypeID(EntityTypeFireball)
	if typeID < 0 {
		typeID = EntityTypeFireballID
	}
	id := s.entities.Add(&world.Entity{
		TypeID:    typeID,
		TypeName:  EntityTypeFireball,
		Dimension: normDim,
		X:         x,
		Y:         y,
		Z:         z,
		Yaw:       yawToward(vx, vz),
		VelocityX: clampVelocityShort(vx),
		VelocityY: clampVelocityShort(vy),
		VelocityZ: clampVelocityShort(vz),
		OnGround:  false,
	})

	mgr := s.netherHostileManager()
	mgr.mu.Lock()
	mgr.fireballs[id] = &FireballProjectileState{
		EntityID:       id,
		TypeName:       EntityTypeFireball,
		Dimension:      normDim,
		ShooterID:      ghastID,
		ShooterType:    MobTypeGhast,
		VX:             vx,
		VY:             vy,
		VZ:             vz,
		ExplosionPower: explosionPower,
	}
	mgr.mu.Unlock()

	if ent := s.entities.Get(id); ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, ent))
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, PlaySoundPacketID,
			EncodeSoundPacket(SoundGhastShoot, SoundSourceHostile, x, y, z, 10.0, 1.0, 0))
	}
	return id, nil
}

// GetFireballProjectileState returns a snapshot of the FireballProjectileState
// for a `minecraft:fireball` or `minecraft:small_fireball` entity.
func (s *Server) GetFireballProjectileState(entityID int32) (FireballProjectileState, bool) {
	if s == nil {
		return FireballProjectileState{}, false
	}
	mgr := s.netherHostileManager()
	mgr.mu.RLock()
	st, ok := mgr.fireballs[entityID]
	var out FireballProjectileState
	if ok && st != nil {
		out = *st
	}
	mgr.mu.RUnlock()
	return out, ok && st != nil
}

// DeflectGhastFireball deflects an active `minecraft:fireball` along player's
// look direction when the player attacks or hits it with a projectile, marking
// `Deflected = true` and `DeflectedBy = player` (enabling the `"Return to Sender"`
// `minecraft:nether/return_to_sender` advancement if it kills a Ghast!).
func (s *Server) DeflectGhastFireball(fireballID int32, player *PlayerSession) bool {
	if s == nil || s.entities == nil || player == nil || player.IsDead() {
		return false
	}
	ent := s.entities.Get(fireballID)
	if ent == nil || canonicalResourceName(ent.TypeName) != EntityTypeFireball {
		return false
	}
	pSnap := player.Snapshot()
	lx, ly, lz := ElytraLookVector(pSnap.Yaw, pSnap.Pitch)
	vx := lx * (GhastFireballSpeed * 1.25)
	vy := ly * (GhastFireballSpeed * 1.25)
	vz := lz * (GhastFireballSpeed * 1.25)

	mgr := s.netherHostileManager()
	mgr.mu.Lock()
	st, ok := mgr.fireballs[fireballID]
	if !ok || st == nil {
		st = &FireballProjectileState{
			EntityID:       fireballID,
			TypeName:       EntityTypeFireball,
			Dimension:      ent.EffectiveDimension(),
			ExplosionPower: GhastDefaultExplosionPower,
		}
		mgr.fireballs[fireballID] = st
	}
	st.VX = vx
	st.VY = vy
	st.VZ = vz
	st.Deflected = true
	st.DeflectedBy = player
	mgr.mu.Unlock()

	yaw := yawToward(vx, vz)
	s.entities.Update(fireballID, func(e *world.Entity) {
		e.Yaw = yaw
		e.VelocityX = clampVelocityShort(vx)
		e.VelocityY = clampVelocityShort(vy)
		e.VelocityZ = clampVelocityShort(vz)
	})
	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, protocol.PlaySetEntityMotion,
		EncodeSetEntityMotionPacket(fireballID, vx, vy, vz))
	return true
}

// OnGhastKilled cleans up state when a Ghast dies.
func (s *Server) OnGhastKilled(entityID int32) {
	if s == nil {
		return
	}
	mgr := s.netherHostileManager()
	mgr.mu.Lock()
	delete(mgr.ghasts, entityID)
	mgr.mu.Unlock()
}

// RollGhastDeathLoot rolls vanilla 26.1.2 `minecraft:ghast` death loot:
//   - 0..1 `minecraft:ghast_tear` (+ 0..lootingLevel bonus)
//   - 0..2 `minecraft:gunpowder` (+ 0..lootingLevel bonus)
func RollGhastDeathLoot(rng *rand.Rand, lootingLevel int) []world.ItemStack {
	if lootingLevel < 0 {
		lootingLevel = 0
	}
	tears := int32(1)
	powder := int32(1)
	if rng != nil {
		tears = int32(rng.Intn(2))  // 0..1
		powder = int32(rng.Intn(3)) // 0..2
		if lootingLevel > 0 {
			tears += int32(rng.Intn(lootingLevel + 1))
			powder += int32(rng.Intn(lootingLevel + 1))
		}
	} else if lootingLevel > 0 {
		tears += int32(lootingLevel)
		powder += int32(lootingLevel)
	}
	var out []world.ItemStack
	if tears > 0 {
		if s1 := world.NewNamedItemStack("minecraft:ghast_tear", tears); !s1.IsEmpty() {
			out = append(out, s1)
		}
	}
	if powder > 0 {
		if s2 := world.NewNamedItemStack("minecraft:gunpowder", powder); !s2.IsEmpty() {
			out = append(out, s2)
		}
	}
	return out
}

// ============================================================================
// 3. MAGMA CUBE & SLIME (SIZE SPLIT, FIRE/LAVA IMMUNE, ARMOR, LAVA JUMP AI)
// ============================================================================

// MagmaCubeAttackDamage returns the base melee contact damage dealt by a
// `minecraft:magma_cube` of the given size (`size + 2`: size 1 -> 3.0, size 2 -> 4.0, size 4 -> 6.0).
func MagmaCubeAttackDamage(size int) float32 {
	if size < 1 {
		size = 1
	}
	return float32(size + 2)
}

// SlimeAttackDamage returns the base melee contact damage dealt by a
// `minecraft:slime` of the given size (size 1 deals 0 damage; size > 1 deals `float32(size)`).
func SlimeAttackDamage(size int) float32 {
	if size <= 1 {
		return 0
	}
	return float32(size)
}

// MagmaCubeArmorPoints returns the vanilla intrinsic armor points of a
// `minecraft:magma_cube` of the given size (`3 * size`: size 1 -> 3, size 2 -> 6, size 4 -> 12).
func MagmaCubeArmorPoints(size int) int {
	if size < 1 {
		size = 1
	}
	pts := size * MagmaCubeArmorPerSize
	if pts > 20 {
		pts = 20
	}
	return pts
}

// ReduceMagmaCubeDamageByArmor applies a Magma Cube's intrinsic `3 * size`
// armor reduction to incoming physical damage using `DamageAfterArmor`.
func (s *Server) ReduceMagmaCubeDamageByArmor(entityID int32, rawDamage float32) float32 {
	if s == nil || rawDamage <= 0 {
		return rawDamage
	}
	size := s.SlimeSize(entityID)
	armor := float32(MagmaCubeArmorPoints(size))
	return DamageAfterArmor(rawDamage, armor, 0)
}

// IsMagmaCubeImmuneToDamageCause reports whether a `minecraft:magma_cube` is
// completely immune to the given damage cause (`fire`, `in_fire`, `on_fire`,
// `lava`, `hot_floor`, `fall`).
func IsMagmaCubeImmuneToDamageCause(cause string) bool {
	norm := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(cause)), "minecraft:")
	switch norm {
	case "fire", "in_fire", "on_fire", "lava", "hot_floor", "fall", "campfire":
		return true
	default:
		return false
	}
}

func (s *Server) ensureMagmaOrSlimeState(entityID int32, typeName, dimension string) *MagmaCubeState {
	mgr := s.netherHostileManager()
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	if st, ok := mgr.magmaCubes[entityID]; ok && st != nil {
		return st
	}
	st := &MagmaCubeState{
		EntityID:  entityID,
		TypeName:  canonicalResourceName(typeName),
		Dimension: world.NormalizeDimension(dimension),
		JumpDelay: 20,
	}
	mgr.magmaCubes[entityID] = st
	return st
}

// GetMagmaCubeState returns a snapshot of the MagmaCubeState for entityID.
func (s *Server) GetMagmaCubeState(entityID int32) (MagmaCubeState, bool) {
	if s == nil {
		return MagmaCubeState{}, false
	}
	mgr := s.netherHostileManager()
	mgr.mu.RLock()
	st, ok := mgr.magmaCubes[entityID]
	var out MagmaCubeState
	if ok && st != nil {
		out = *st
	}
	mgr.mu.RUnlock()
	return out, ok && st != nil
}

// ============================================================================
// 4. CHARGED CREEPER (LIGHTNING STRIKE, DATA_IS_POWERED, IGNITION, SKULL DROPS)
// ============================================================================

// OnLightningStrikeCreeper charges the `minecraft:creeper` with entityID when
// struck by lightning, setting `DATA_IS_POWERED = true` (explosion radius 6.0)
// and broadcasting its updated SynchedEntityData packet.
func (s *Server) OnLightningStrikeCreeper(entityID int32) bool {
	if s == nil || s.entities == nil || entityID <= 0 {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeCreeper {
		return false
	}
	s.SetCreeperCharged(entityID, true)
	return true
}

// ChargeCreepersNearLightning scans within `CreeperLightningStrikeRadius` (4.0
// blocks horizontally, -3..+6 blocks vertically) of a lightning strike at
// (strikeX, strikeY, strikeZ) in dimension and charges all `minecraft:creeper`
// entities found.
func (s *Server) ChargeCreepersNearLightning(dimension string, strikeX, strikeY, strikeZ float64) []int32 {
	if s == nil || s.entities == nil {
		return nil
	}
	normDim := world.NormalizeDimension(dimension)
	var chargedIDs []int32
	allEnts := s.entities.AppendAll(nil)
	for i := range allEnts {
		ent := &allEnts[i]
		if ent.EffectiveDimension() != normDim || canonicalResourceName(ent.TypeName) != MobTypeCreeper {
			continue
		}
		dx := math.Abs(ent.X - strikeX)
		dy := ent.Y - strikeY
		dz := math.Abs(ent.Z - strikeZ)
		if dx <= CreeperLightningStrikeRadius && dz <= CreeperLightningStrikeRadius && dy >= -3.0 && dy <= 6.0 {
			if s.OnLightningStrikeCreeper(ent.ID) {
				chargedIDs = append(chargedIDs, ent.ID)
			}
		}
	}
	return chargedIDs
}

// IgniteCreeper manually ignites a `minecraft:creeper` (e.g. when a player uses
// `minecraft:flint_and_steel` or `minecraft:fire_charge` on it), setting
// `DATA_IS_IGNITED = true` and `DATA_SWELL_DIR = 1` so it swells for 30 ticks
// and explodes even without a nearby target player.
func (s *Server) IgniteCreeper(entityID int32, session *PlayerSession) bool {
	if s == nil || s.entities == nil || entityID <= 0 {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeCreeper {
		return false
	}
	tr := s.entityTracker()
	tr.mu.Lock()
	mem := tr.mobStateFor(entityID, tr.aiTickID)
	mem.creeperIgnited = true
	mem.creeperSwellDir = 1
	tr.mu.Unlock()

	powered := s.IsCreeperCharged(entityID)
	cp := ent.ChunkPos()
	dim := ent.EffectiveDimension()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
		EncodeCreeperEntityDataPacket(entityID, 1, powered, true))
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket("minecraft:entity.creeper.primed", SoundSourceHostile, ent.X, ent.Y, ent.Z, 1.0, 0.5, 0))
	return true
}

// IsCreeperIgnited reports whether the Creeper with entityID has been manually
// ignited (`DATA_IS_IGNITED == true`).
func (s *Server) IsCreeperIgnited(entityID int32) bool {
	if s == nil || entityID <= 0 {
		return false
	}
	tr := s.entityTracker()
	tr.mu.Lock()
	defer tr.mu.Unlock()
	mem := tr.mobStates[entityID]
	return mem != nil && mem.creeperIgnited
}

// CreeperHeadDropForVictim returns the vanilla 26.1.2 mob head item ID dropped
// when a Charged Creeper explosion kills a mob of `victimType` (`zombie`,
// `skeleton`, `wither_skeleton`, `creeper`, `piglin`).
func CreeperHeadDropForVictim(victimType string) (headItemID string, ok bool) {
	switch canonicalResourceName(victimType) {
	case MobTypeZombie:
		return "minecraft:zombie_head", true
	case MobTypeSkeleton:
		return "minecraft:skeleton_skull", true
	case MobTypeWitherSkeleton:
		return "minecraft:wither_skeleton_skull", true
	case MobTypeCreeper:
		return "minecraft:creeper_head", true
	case MobTypePiglin:
		return "minecraft:piglin_head", true
	default:
		return "", false
	}
}

// ============================================================================
// TICK LOOP FOR BLAZES, GHASTS, FIREBALLS, AND MAGMA CUBES / SLIMES
// ============================================================================

// TickBlazesGhastsAndMagmaCubes steps all active `minecraft:blaze`,
// `minecraft:ghast`, `minecraft:fireball`, `minecraft:small_fireball`,
// `minecraft:magma_cube`, and `minecraft:slime` entities for one tick (20 TPS).
func (s *Server) TickBlazesGhastsAndMagmaCubes(rng *rand.Rand) {
	if s == nil || s.entities == nil {
		return
	}
	mgr := s.netherHostileManager()
	mgr.mu.RLock()
	blazeIDs := make([]int32, 0, len(mgr.blazes))
	for id := range mgr.blazes {
		blazeIDs = append(blazeIDs, id)
	}
	ghastIDs := make([]int32, 0, len(mgr.ghasts))
	for id := range mgr.ghasts {
		ghastIDs = append(ghastIDs, id)
	}
	fireballIDs := make([]int32, 0, len(mgr.fireballs))
	for id := range mgr.fireballs {
		fireballIDs = append(fireballIDs, id)
	}
	cubeIDs := make([]int32, 0, len(mgr.magmaCubes))
	for id := range mgr.magmaCubes {
		cubeIDs = append(cubeIDs, id)
	}
	mgr.mu.RUnlock()

	if len(blazeIDs) == 0 && len(ghastIDs) == 0 && len(fireballIDs) == 0 && len(cubeIDs) == 0 {
		return
	}

	if s.Difficulty() == DifficultyPeaceful {
		for _, id := range blazeIDs {
			s.despawnHostileEntityByID(id)
		}
		for _, id := range ghastIDs {
			s.despawnHostileEntityByID(id)
		}
		for _, id := range fireballIDs {
			s.despawnHostileEntityByID(id)
		}
		for _, id := range cubeIDs {
			s.despawnHostileEntityByID(id)
		}
		mgr.mu.Lock()
		clear(mgr.blazes)
		clear(mgr.ghasts)
		clear(mgr.fireballs)
		clear(mgr.magmaCubes)
		mgr.mu.Unlock()
		return
	}

	for _, id := range blazeIDs {
		s.tickSingleBlaze(id, rng)
	}
	for _, id := range ghastIDs {
		s.tickSingleGhast(id, rng)
	}
	for _, id := range fireballIDs {
		s.tickSingleFireballProjectile(id, rng)
	}
	for _, id := range cubeIDs {
		s.tickSingleMagmaCubeOrSlime(id, rng)
	}
}

func (s *Server) despawnHostileEntityByID(entityID int32) {
	if ent := s.entities.Get(entityID); ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, protocol.PlayRemoveEntities,
			encodeRemoveEntitiesPacket(nil, []int32{entityID}))
	}
	s.entities.Remove(entityID)
}

func (s *Server) tickSingleBlaze(entityID int32, rng *rand.Rand) {
	ent := s.entities.Get(entityID)
	mgr := s.netherHostileManager()
	mgr.mu.Lock()
	st, ok := mgr.blazes[entityID]
	if !ok || st == nil || ent == nil {
		delete(mgr.blazes, entityID)
		mgr.mu.Unlock()
		return
	}
	st.TickCounter++
	if st.AttackCooldown > 0 {
		st.AttackCooldown--
	}
	if st.WaterDamageTimer > 0 {
		st.WaterDamageTimer--
	}
	if st.AttackTime > 0 {
		st.AttackTime--
	}
	// Blazes continuously emit large_smoke particles, especially when on fire/charged.
	if st.TickCounter%BlazeSmokeIntervalTicks == 0 {
		st.SmokeEmitted++
	}
	snap := *st
	mgr.mu.Unlock()

	dim := ent.EffectiveDimension()

	// 1. Water or rain contact damages Blaze by 1.0 every 20 ticks.
	if s.IsEndermanInWaterOrRain(dim, ent.X, ent.Y, ent.Z) && snap.WaterDamageTimer <= 0 {
		mgr.mu.Lock()
		if cur, ok := mgr.blazes[entityID]; ok && cur != nil {
			cur.WaterDamageTimer = BlazeWaterDamageIntervalTicks
		}
		mgr.mu.Unlock()
		if s.applyDamageToMob(ent, BlazeWaterDamage, nil, 0, false, false) {
			s.OnBlazeKilled(entityID)
			return
		}
	}

	// 2. Acquire nearest survival/adventure target player within 48 blocks.
	target := snap.TargetPlayer
	if target == nil || target.IsDead() || world.NormalizeDimension(target.Dimension()) != dim ||
		target.Snapshot().GameMode == GameModeCreative || target.Snapshot().GameMode == GameModeSpectator {
		target = s.findNearestWitherTarget(dim, ent.X, ent.Y, ent.Z, BlazeAggroRange)
		mgr.mu.Lock()
		if cur, ok := mgr.blazes[entityID]; ok && cur != nil {
			cur.TargetPlayer = target
		}
		mgr.mu.Unlock()
	}

	if target == nil || target.IsDead() {
		if snap.Charged {
			s.SetBlazeCharged(entityID, false)
		}
		return
	}

	tSnap := target.Snapshot()
	dx := tSnap.X - ent.X
	dy := (tSnap.Y + 1.0) - (ent.Y + 0.9)
	dz := tSnap.Z - ent.Z
	distSq := dx*dx + dy*dy + dz*dz
	dist := math.Sqrt(distSq)

	// 3. Melee contact attack if within 2.0 blocks.
	if dist <= BlazeMeleeAttackReach && snap.AttackCooldown <= 0 {
		s.AttackPlayerFromMob(entityID, MobTypeBlaze, ent.X, ent.Z, target, BlazeMeleeAttackDamage)
		mgr.mu.Lock()
		if cur, ok := mgr.blazes[entityID]; ok && cur != nil {
			cur.AttackCooldown = BlazeAttackCooldownTicks
		}
		mgr.mu.Unlock()
	}

	// 4. BlazeAttackGoal state machine:
	//   - AttackStep == 0: cooldown before charging; when AttackTime <= 0 -> AttackStep = 1, SetBlazeCharged(true), AttackTime = 60
	//   - AttackStep == 1: 60-tick fiery charge-up; when AttackTime <= 0 -> AttackStep = 2, AttackTime = 6
	//   - AttackStep == 2..4: fires 1 small_fireball every 6 ticks (3 fireballs total); after 3rd shot -> AttackStep = 0, SetBlazeCharged(false), AttackTime = 100
	if snap.AttackTime <= 0 {
		switch snap.AttackStep {
		case 0:
			s.SetBlazeCharged(entityID, true)
			mgr.mu.Lock()
			if cur, ok := mgr.blazes[entityID]; ok && cur != nil {
				cur.AttackStep = 1
				cur.AttackTime = BlazeChargeWarmupTicks
			}
			mgr.mu.Unlock()
		case 1, 2, 3:
			_, _ = s.SpawnBlazeSmallFireball(dim, entityID, ent.X, ent.Y+1.1, ent.Z, dx, dy, dz)
			nextStep := snap.AttackStep + 1
			nextTime := BlazeBurstShotIntervalTicks
			if nextStep > BlazeBurstFireballCount {
				nextStep = 0
				nextTime = BlazePostBurstCooldownTicks
				s.SetBlazeCharged(entityID, false)
			}
			mgr.mu.Lock()
			if cur, ok := mgr.blazes[entityID]; ok && cur != nil {
				cur.AttackStep = nextStep
				cur.AttackTime = nextTime
				cur.FireballsShot++
			}
			mgr.mu.Unlock()
		}
	}

	// 5. Hover / flight path movement: while charged or below target eye level,
	// hover upward and advance along its fiery path toward the target.
	nextX, nextY, nextZ := ent.X, ent.Y, ent.Z
	desiredY := tSnap.Y + BlazeHoverHeightOffset
	var vy float64
	if ent.Y < desiredY {
		vy = BlazeHoverVerticalSpeed
		if ent.Y+vy > desiredY {
			vy = desiredY - ent.Y
		}
		nextY += vy
	} else if ent.Y > desiredY+1.5 {
		vy = -BlazeHoverVerticalSpeed * 0.5
		nextY += vy
	}
	var vx, vz float64
	horizDist := math.Hypot(dx, dz)
	if horizDist > 3.0 {
		vx = (dx / horizDist) * BlazeFlySpeed
		vz = (dz / horizDist) * BlazeFlySpeed
		nextX += vx
		nextZ += vz
	}
	yaw := yawToward(dx, dz)
	s.entities.Update(entityID, func(e *world.Entity) {
		e.X = nextX
		e.Y = nextY
		e.Z = nextZ
		e.Yaw = yaw
		e.HeadYaw = yaw
		e.VelocityX = clampVelocityShort(vx)
		e.VelocityY = clampVelocityShort(vy)
		e.VelocityZ = clampVelocityShort(vz)
		e.OnGround = false
	})
}

func (s *Server) tickSingleGhast(entityID int32, rng *rand.Rand) {
	ent := s.entities.Get(entityID)
	mgr := s.netherHostileManager()
	mgr.mu.Lock()
	st, ok := mgr.ghasts[entityID]
	if !ok || st == nil || ent == nil {
		delete(mgr.ghasts, entityID)
		mgr.mu.Unlock()
		return
	}
	st.TickCounter++
	if st.MoveCooldown > 0 {
		st.MoveCooldown--
	}
	snap := *st
	mgr.mu.Unlock()

	dim := ent.EffectiveDimension()

	// 1. Periodic ambient crying sound (`minecraft:entity.ghast.ambient`).
	if snap.TickCounter%80 == 0 {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
			EncodeSoundPacket(SoundGhastAmbient, SoundSourceHostile, ent.X, ent.Y, ent.Z, 10.0, 1.0, 0))
		mgr.mu.Lock()
		if cur, ok := mgr.ghasts[entityID]; ok && cur != nil {
			cur.CryCount++
		}
		mgr.mu.Unlock()
	}

	// 2. Acquire nearest survival/adventure target player within 64 blocks with line of sight.
	target := snap.TargetPlayer
	if target == nil || target.IsDead() || world.NormalizeDimension(target.Dimension()) != dim ||
		target.Snapshot().GameMode == GameModeCreative || target.Snapshot().GameMode == GameModeSpectator {
		target = s.findNearestWitherTarget(dim, ent.X, ent.Y, ent.Z, GhastAggroRange)
		mgr.mu.Lock()
		if cur, ok := mgr.ghasts[entityID]; ok && cur != nil {
			cur.TargetPlayer = target
		}
		mgr.mu.Unlock()
	}

	if target != nil && !target.IsDead() {
		tSnap := target.Snapshot()
		dx := tSnap.X - ent.X
		dy := (tSnap.Y + 0.8) - (ent.Y + 2.0)
		dz := tSnap.Z - ent.Z
		dist := math.Sqrt(dx*dx + dy*dy + dz*dz)
		hasLOS := dist <= GhastAggroRange && s.hasLineOfSightBetween(s.DimensionChunks(dim), ent.X, ent.Y+2.0, ent.Z, tSnap.X, tSnap.Y+1.0, tSnap.Z)

		yaw := yawToward(dx, dz)
		s.entities.Update(entityID, func(e *world.Entity) {
			e.Yaw = yaw
			e.HeadYaw = yaw
		})

		if hasLOS {
			charge := snap.ChargeTicks + 1
			cp := ent.ChunkPos()
			if charge == GhastChargeWarnTicks {
				s.SetGhastCharging(entityID, true)
				s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
					EncodeSoundPacket(SoundGhastWarn, SoundSourceHostile, ent.X, ent.Y, ent.Z, 10.0, 1.0, 0))
				mgr.mu.Lock()
				if cur, ok := mgr.ghasts[entityID]; ok && cur != nil {
					cur.CryCount++
				}
				mgr.mu.Unlock()
			}
			if charge >= GhastChargeFireTicks {
				horiz := math.Hypot(dx, dz)
				spawnX, spawnY, spawnZ := ent.X, ent.Y+1.5, ent.Z
				if horiz > 1e-4 {
					spawnX += (dx / horiz) * 2.0
					spawnZ += (dz / horiz) * 2.0
				}
				_, _ = s.SpawnGhastFireball(dim, entityID, spawnX, spawnY, spawnZ, dx, dy, dz, snap.ExplosionPower)
				s.SetGhastCharging(entityID, false)
				charge = GhastPostShotCooldownTicks
			}
			mgr.mu.Lock()
			if cur, ok := mgr.ghasts[entityID]; ok && cur != nil {
				cur.ChargeTicks = charge
			}
			mgr.mu.Unlock()
			return
		}
	}

	// Lost sight or no target: decay charge and clear DATA_IS_CHARGING.
	if snap.ChargeTicks > 0 || snap.Charging {
		nextCharge := snap.ChargeTicks - 1
		if nextCharge < 0 {
			nextCharge = 0
		}
		mgr.mu.Lock()
		if cur, ok := mgr.ghasts[entityID]; ok && cur != nil {
			cur.ChargeTicks = nextCharge
		}
		mgr.mu.Unlock()
		if nextCharge < GhastChargeWarnTicks && snap.Charging {
			s.SetGhastCharging(entityID, false)
		}
	}
}

func (s *Server) tickSingleFireballProjectile(entityID int32, rng *rand.Rand) {
	ent := s.entities.Get(entityID)
	mgr := s.netherHostileManager()
	mgr.mu.Lock()
	st, ok := mgr.fireballs[entityID]
	if !ok || st == nil || ent == nil {
		delete(mgr.fireballs, entityID)
		mgr.mu.Unlock()
		return
	}
	st.AgeTicks++
	snap := *st
	mgr.mu.Unlock()

	if snap.AgeTicks > 200 {
		s.despawnHostileEntityByID(entityID)
		mgr.mu.Lock()
		delete(mgr.fireballs, entityID)
		mgr.mu.Unlock()
		return
	}

	dim := ent.EffectiveDimension()
	nx := ent.X + snap.VX
	ny := ent.Y + snap.VY
	nz := ent.Z + snap.VZ

	// 1. Check collision against players.
	for _, p := range s.onlinePlayersSnapshot() {
		if p == nil || p.IsDead() {
			continue
		}
		pSnap := p.Snapshot()
		if world.NormalizeDimension(pSnap.Dimension) != dim || pSnap.GameMode == GameModeSpectator {
			continue
		}
		if math.Abs(pSnap.X-nx) <= 1.0 && ny >= pSnap.Y-0.3 && ny <= pSnap.Y+2.0 && math.Abs(pSnap.Z-nz) <= 1.0 {
			s.impactFireballAt(entityID, snap, dim, nx, ny, nz, p, nil)
			return
		}
	}

	// 2. Check collision against mobs (especially a deflected Ghast fireball hitting a Ghast!).
	allEnts := s.entities.AppendAll(nil)
	for i := range allEnts {
		other := &allEnts[i]
		if other.ID == entityID || other.EffectiveDimension() != dim || other.IsItem() || other.IsExperienceOrb() {
			continue
		}
		if !snap.Deflected && other.ID == snap.ShooterID {
			continue
		}
		halfW := 0.8
		height := 2.0
		if canonicalResourceName(other.TypeName) == MobTypeGhast {
			halfW = 2.2
			height = 4.0
		}
		if math.Abs(other.X-nx) <= halfW && ny >= other.Y-0.3 && ny <= other.Y+height && math.Abs(other.Z-nz) <= halfW {
			if canonicalResourceName(other.TypeName) == MobTypeEnderman && s.TryEndermanDodgeProjectile(other.ID, snap.TypeName, rng) {
				continue
			}
			live := s.entities.Get(other.ID)
			s.impactFireballAt(entityID, snap, dim, nx, ny, nz, nil, live)
			return
		}
	}

	// 3. Check collision against solid blocks.
	if cache := s.DimensionChunks(dim); cache != nil {
		bx, by, bz := int(math.Floor(nx)), int(math.Floor(ny)), int(math.Floor(nz))
		if by >= world.MinY && by < world.MinY+world.WorldHeight {
			blk := cache.GetBlock(bx, by, bz)
			if !isPassableBlock(blk) && blk != world.StateWater {
				s.impactFireballAt(entityID, snap, dim, nx, ny, nz, nil, nil)
				return
			}
		}
	}

	// 4. Advance fireball in flight.
	s.entities.Update(entityID, func(e *world.Entity) {
		e.X = nx
		e.Y = ny
		e.Z = nz
	})
}

func (s *Server) impactFireballAt(
	fireballID int32,
	fb FireballProjectileState,
	dim string,
	x, y, z float64,
	hitPlayer *PlayerSession,
	hitMob *world.Entity,
) {
	mgr := s.netherHostileManager()
	mgr.mu.Lock()
	delete(mgr.fireballs, fireballID)
	mgr.mu.Unlock()
	s.despawnHostileEntityByID(fireballID)

	if fb.TypeName == EntityTypeSmallFireball {
		if hitPlayer != nil && !hitPlayer.IsDead() {
			pSnap := hitPlayer.Snapshot()
			if pSnap.GameMode != GameModeCreative && pSnap.GameMode != GameModeSpectator {
				scaled := ScaleDamageByDifficulty(BlazeSmallFireballDamage, s.Difficulty())
				s.DamagePlayerWithArmor(hitPlayer, scaled, "fireball")
				s.SetPlayerFireTicks(hitPlayer, BlazeSmallFireballIgniteTicks)
			}
		} else if hitMob != nil {
			if !IsFireImmuneMob(hitMob.TypeName) {
				s.applyDamageToMob(hitMob, BlazeSmallFireballDamage, fb.DeflectedBy, 0.2, false, false)
				s.SetEntityFireTicks(hitMob.ID, BlazeSmallFireballIgniteTicks)
			}
		} else if s.MobGriefing() {
			if cache := s.DimensionChunks(dim); cache != nil {
				bx, by, bz := int(math.Floor(x)), int(math.Floor(y)), int(math.Floor(z))
				if cache.GetBlock(bx, by, bz) == world.StateAir {
					if fireSt, ok := cache.ResolveFireStateForPlacement(bx, by, bz, 0); ok {
						if placed, upd := cache.ApplyBlockWithUpdates(bx, by, bz, fireSt); placed {
							s.broadcastDimensionBlockChanges(dim, cache, upd.Changes, upd.LightChanged)
						}
					}
				}
			}
		}
		return
	}

	// Large Ghast fireball (`minecraft:fireball`):
	if hitPlayer != nil && !hitPlayer.IsDead() {
		pSnap := hitPlayer.Snapshot()
		if pSnap.GameMode != GameModeCreative && pSnap.GameMode != GameModeSpectator {
			scaled := ScaleDamageByDifficulty(GhastFireballDirectHitDamage, s.Difficulty())
			s.DamagePlayerWithArmor(hitPlayer, scaled, "fireball")
		}
	}
	if hitMob != nil {
		dmg := GhastFireballDirectHitDamage
		if fb.Deflected && canonicalResourceName(hitMob.TypeName) == MobTypeGhast {
			dmg = GhastDeflectedFireballDamage
		}
		killed := s.applyDamageToMob(hitMob, dmg, fb.DeflectedBy, 0.4, false, false)
		if killed && fb.Deflected && fb.DeflectedBy != nil && canonicalResourceName(hitMob.TypeName) == MobTypeGhast {
			s.GrantCriterion(fb.DeflectedBy, "minecraft:nether/return_to_sender", "killed_ghast")
		}
	}
	radius := float64(fb.ExplosionPower)
	if radius <= 0 {
		radius = GhastFireballExplosionRadius
	}
	s.ExplodeMob(dim, x, y, z, radius, s.MobGriefing(), s.MobGriefing() && s.CreeperBlockGriefing(), fb.ShooterID)
}

func (s *Server) tickSingleMagmaCubeOrSlime(entityID int32, rng *rand.Rand) {
	ent := s.entities.Get(entityID)
	mgr := s.netherHostileManager()
	mgr.mu.Lock()
	st, ok := mgr.magmaCubes[entityID]
	if !ok || st == nil || ent == nil {
		delete(mgr.magmaCubes, entityID)
		mgr.mu.Unlock()
		return
	}
	st.TickCounter++
	if st.JumpDelay > 0 {
		st.JumpDelay--
	}
	if st.AttackCooldown > 0 {
		st.AttackCooldown--
	}
	snap := *st
	mgr.mu.Unlock()

	dim := ent.EffectiveDimension()
	isMagma := snap.TypeName == MobTypeMagmaCube
	size := s.SlimeSize(entityID)

	// 1. Check lava immersion: Magma Cubes float and jump upward in lava!
	cache := s.DimensionChunks(dim)
	inLava := false
	if cache != nil {
		bx, by, bz := int(math.Floor(ent.X)), int(math.Floor(ent.Y)), int(math.Floor(ent.Z))
		if world.BlockName(cache.GetBlock(bx, by, bz)) == "minecraft:lava" {
			inLava = true
		}
	}
	if isMagma && inLava {
		vy := MagmaCubeLavaJumpVelocity + float64(size)*0.04
		s.entities.Update(entityID, func(e *world.Entity) {
			e.Y += vy
			e.VelocityY = clampVelocityShort(vy)
			e.OnGround = false
		})
	}
	mgr.mu.Lock()
	if cur, ok := mgr.magmaCubes[entityID]; ok && cur != nil {
		cur.InLava = inLava
	}
	mgr.mu.Unlock()

	// 2. Acquire nearest survival/adventure target player within 16 blocks.
	target := snap.TargetPlayer
	if target == nil || target.IsDead() || world.NormalizeDimension(target.Dimension()) != dim ||
		target.Snapshot().GameMode == GameModeCreative || target.Snapshot().GameMode == GameModeSpectator {
		target = s.findNearestWitherTarget(dim, ent.X, ent.Y, ent.Z, MagmaCubeAggroRange)
		mgr.mu.Lock()
		if cur, ok := mgr.magmaCubes[entityID]; ok && cur != nil {
			cur.TargetPlayer = target
		}
		mgr.mu.Unlock()
	}

	// 3. Hop/jump toward target when JumpDelay reaches 0.
	if snap.JumpDelay <= 0 {
		nextDelay := 20
		if rng != nil {
			nextDelay = MagmaCubeBaseJumpDelayMin + rng.Intn(MagmaCubeBaseJumpDelayMax-MagmaCubeBaseJumpDelayMin+1)
		}
		if target != nil {
			nextDelay /= 3
			if nextDelay < 6 {
				nextDelay = 6
			}
		}
		var stepX, stepZ float64
		yaw := ent.Yaw
		if target != nil && !target.IsDead() {
			tSnap := target.Snapshot()
			dx := tSnap.X - ent.X
			dz := tSnap.Z - ent.Z
			hDist := math.Hypot(dx, dz)
			if hDist > 1e-4 {
				hopSpeed := 0.25 + float64(size)*0.04
				if hopSpeed > hDist {
					hopSpeed = hDist
				}
				stepX = (dx / hDist) * hopSpeed
				stepZ = (dz / hDist) * hopSpeed
				yaw = yawToward(dx, dz)
			}
		}
		jumpVY := MagmaCubeJumpBaseVelocityY
		if isMagma {
			jumpVY += float64(size) * 0.1
		}
		nextX := ent.X + stepX
		nextZ := ent.Z + stepZ
		nextY := ent.Y
		curFeetY := int(math.Floor(ent.Y + 0.01))
		if cache != nil {
			if nx, ny, nz, stepOK := tryApplyHorizontalStep(cache, ent.X, curFeetY, ent.Z, nextX, nextZ); stepOK {
				nextX, nextY, nextZ = nx, float64(ny), nz
			}
		}
		s.entities.Update(entityID, func(e *world.Entity) {
			e.X = nextX
			e.Y = nextY
			e.Z = nextZ
			e.Yaw = yaw
			e.HeadYaw = yaw
			e.VelocityX = clampVelocityShort(stepX)
			e.VelocityY = clampVelocityShort(jumpVY)
			e.VelocityZ = clampVelocityShort(stepZ)
		})
		jumpSnd := SoundSlimeJump
		if isMagma {
			jumpSnd = SoundMagmaCubeJump
		}
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
			EncodeSoundPacket(jumpSnd, SoundSourceHostile, nextX, nextY, nextZ, 0.8, 1.0, 0))
		mgr.mu.Lock()
		if cur, ok := mgr.magmaCubes[entityID]; ok && cur != nil {
			cur.JumpDelay = nextDelay
			cur.JumpCount++
		}
		mgr.mu.Unlock()
	}

	// 4. Melee contact damage against target player (Magma Cubes deal damage even at size 1!).
	if target != nil && !target.IsDead() && snap.AttackCooldown <= 0 {
		ent = s.entities.Get(entityID)
		if ent == nil {
			return
		}
		var dmg float32
		if isMagma {
			dmg = MagmaCubeAttackDamage(size)
		} else {
			dmg = SlimeAttackDamage(size)
		}
		if dmg > 0 {
			tSnap := target.Snapshot()
			reach := 0.8 + float64(size)*0.35
			dx := tSnap.X - ent.X
			dy := tSnap.Y - ent.Y
			dz := tSnap.Z - ent.Z
			if dx*dx+dy*dy+dz*dz <= reach*reach {
				s.AttackPlayerFromMob(entityID, snap.TypeName, ent.X, ent.Z, target, dmg)
				mgr.mu.Lock()
				if cur, ok := mgr.magmaCubes[entityID]; ok && cur != nil {
					cur.AttackCooldown = 20
				}
				mgr.mu.Unlock()
			}
		}
	}
}
