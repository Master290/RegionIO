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
	BlazeWaterDamageIntervalTicks   int     = BlazeWaterHurtCooldownTicks2612 // 10 ticks (invulnerableTime 20 -> 10)
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
	MagmaCubeBaseJumpDelayMax  int     = 116 // (10..29) * 4
	MagmaCubeLavaJumpVelocity  float64 = MagmaCubeLavaJumpBase2612
	MagmaCubeJumpBaseVelocityY float64 = SlimeBaseJumpPower2612
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
	EntityID                   int32
	Dimension                  string
	Charged                    bool
	AttackStep                 int // 0=cooldown, 1=charging (60t), 2..4=firing 3 small_fireballs (6t each)
	AttackTime                 int
	AttackCooldown             int
	LastSeen                   int
	AllowedHeightOffset        float64
	NextHeightOffsetChangeTick int
	VY                         float64
	WaterDamageTimer           int
	SmokeEmitted               int
	FireballsShot              int
	TargetPlayer               *PlayerSession
	TickCounter                int
}

// GhastState tracks floating waypoints, `DATA_IS_CHARGING`, cry/warn/shoot
// timers, and target player for a `minecraft:ghast`.
type GhastState struct {
	EntityID       int32
	Dimension      string
	Charging       bool
	ChargeTicks    int
	ExplosionPower int
	HasWanted      bool
	WaypointX      float64
	WaypointY      float64
	WaypointZ      float64
	VX, VY, VZ     float64
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
	EntityID          int32
	TypeName          string
	Dimension         string
	JumpDelay         int
	AttackCooldown    int
	InLava            bool
	JumpCount         int
	WantedYaw         float32
	NextRandomizeTime int
	GrowTiredTimer    int
	TargetPlayer      *PlayerSession
	TickCounter       int
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
		EntityID:            id,
		Dimension:           normDim,
		AttackStep:          0,
		AttackTime:          0,
		AllowedHeightOffset: BlazeDefaultAllowedHeightOffset2612,
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
	st.ShooterID = pSnap.EntityID
	st.ShooterType = "minecraft:player"
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
	canon := canonicalResourceName(typeName)
	initDelay := 15
	if canon == MobTypeMagmaCube {
		initDelay = 60
	}
	st := &MagmaCubeState{
		EntityID:  entityID,
		TypeName:  canon,
		Dimension: world.NormalizeDimension(dimension),
		JumpDelay: initDelay,
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

func (s *Server) isBlazeTouchingWaterOrRain2612(dim string, x, y, z float64) bool {
	minBY := int(math.Floor(y))
	maxBY := int(math.Floor(y + BlazeHeight2612 - 0.01))
	for by := minBY; by <= maxBY; by++ {
		if s.IsEndermanInWaterOrRain(dim, x, float64(by)+0.1, z) {
			return true
		}
	}
	return false
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
	st.AttackTime-- // BlazeAttackGoal.tick: --this.attackTime
	if st.TickCounter%BlazeSmokeIntervalTicks == 0 {
		st.SmokeEmitted++
	}
	snap := *st
	mgr.mu.Unlock()

	dim := ent.EffectiveDimension()
	cache := s.DimensionChunks(dim)

	// 1. Water or rain contact (`isSensitiveToWater()` -> 1.0 HP every 10 ticks).
	if s.isBlazeTouchingWaterOrRain2612(dim, ent.X, ent.Y, ent.Z) && snap.WaterDamageTimer <= 0 {
		mgr.mu.Lock()
		if cur, ok := mgr.blazes[entityID]; ok && cur != nil {
			cur.WaterDamageTimer = BlazeWaterDamageIntervalTicks - 1
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

	hasTarget := target != nil && !target.IsDead()
	var tSnap PlayerSnapshot
	var targetEyeY float64
	if hasTarget {
		tSnap = target.Snapshot()
		targetEyeY = tSnap.Y + 1.62
	}

	// 3. Vertical hover (`Blaze.customServerAiStep` + `Blaze.aiStep`: 100-tick `allowedHeightOffset`, ascent `+0.3*(0.3-vy)`, slow fall `vy *= 0.6`).
	newVY, newAllowedOffset, newNextOffsetTick := StepBlazeVerticalHover2612(
		snap.VY,
		ent.OnGround,
		ent.Y,
		hasTarget,
		targetEyeY,
		snap.AllowedHeightOffset,
		snap.NextHeightOffsetChangeTick,
		rng,
	)
	mgr.mu.Lock()
	if cur, ok := mgr.blazes[entityID]; ok && cur != nil {
		cur.AllowedHeightOffset = newAllowedOffset
		cur.NextHeightOffsetChangeTick = newNextOffsetTick
		cur.VY = newVY
	}
	mgr.mu.Unlock()

	if !hasTarget {
		if snap.Charged {
			s.SetBlazeCharged(entityID, false)
		}
		mgr.mu.Lock()
		if cur, ok := mgr.blazes[entityID]; ok && cur != nil {
			cur.AttackStep = 0
			cur.LastSeen = 0
		}
		mgr.mu.Unlock()
		return
	}

	dx := tSnap.X - ent.X
	dyRaw := tSnap.Y - ent.Y
	dz := tSnap.Z - ent.Z
	d0 := dx*dx + dyRaw*dyRaw + dz*dz

	hasLOS := hasColliderLineOfSight2612(cache, ent.X, ent.Y+BlazeEyeHeight2612, ent.Z, tSnap.X, targetEyeY, tSnap.Z)
	newLastSeen := 0
	if !hasLOS {
		newLastSeen = snap.LastSeen + 1
	}
	mgr.mu.Lock()
	if cur, ok := mgr.blazes[entityID]; ok && cur != nil {
		cur.LastSeen = newLastSeen
	}
	mgr.mu.Unlock()

	var vx, vz float64
	newYaw := ent.Yaw

	// 4. `BlazeAttackGoal.tick()` (`blaze_2612.txt:482-691`):
	if d0 < BlazeMeleeDistSqThreshold2612 {
		// Branch A (`d0 < 4.0D` = 2.0 blocks): melee attack (20-tick cooldown) + move toward target; requires LOS!
		if !hasLOS {
			return
		}
		if snap.AttackTime <= 0 {
			s.AttackPlayerFromMob(entityID, MobTypeBlaze, ent.X, ent.Z, target, BlazeMeleeAttackDamage)
			mgr.mu.Lock()
			if cur, ok := mgr.blazes[entityID]; ok && cur != nil {
				cur.AttackTime = BlazeAttackCooldownTicks
			}
			mgr.mu.Unlock()
		}
		horizDist := math.Hypot(dx, dz)
		if horizDist > 1e-4 {
			step := BlazeFlySpeed
			if step > horizDist {
				step = horizDist
			}
			vx = (dx / horizDist) * step
			vz = (dz / horizDist) * step
			newYaw = yawToward(dx, dz)
		}
	} else if d0 < BlazeAggroRange*BlazeAggroRange && hasLOS {
		// Branch B (`d0 < followDist^2 && hasLineOfSight`): 4-step burst state machine!
		if snap.AttackTime <= 0 {
			nextStep := snap.AttackStep + 1
			var nextTime int
			if nextStep == 1 {
				nextTime = BlazeChargeWarmupTicks // 60
				s.SetBlazeCharged(entityID, true)
			} else if nextStep <= 4 {
				nextTime = BlazeBurstShotIntervalTicks // 6 (steps 2, 3, 4 all set attackTime = 6!)
			} else {
				nextTime = BlazePostBurstCooldownTicks // 100
				nextStep = 0
				s.SetBlazeCharged(entityID, false)
			}
			shotFired := 0
			if nextStep > 1 {
				dirX, dirY, dirZ, _ := ComputeBlazeFireballDirection2612(ent.X, ent.Y, ent.Z, tSnap.X, tSnap.Y, tSnap.Z, 1.8, rng)
				_, _ = s.SpawnBlazeSmallFireball(dim, entityID, ent.X, ent.Y+BlazeSmallFireballSpawnOffsetY2612, ent.Z, dirX, dirY, dirZ)
				shotFired = 1
			}
			mgr.mu.Lock()
			if cur, ok := mgr.blazes[entityID]; ok && cur != nil {
				cur.AttackStep = nextStep
				cur.AttackTime = nextTime
				cur.FireballsShot += shotFired
			}
			mgr.mu.Unlock()
		}
		if math.Hypot(dx, dz) > 1e-4 {
			newYaw = RotlerpDegrees2612(ent.Yaw, yawToward(dx, dz), 10.0)
		}
	} else if newLastSeen < BlazeLostSightChaseMaxTicks2612 {
		// Branch C (`!hasLOS` and `lastSeen < 5`): advance toward target to regain line of sight!
		horizDist := math.Hypot(dx, dz)
		if horizDist > 1e-4 {
			vx = (dx / horizDist) * BlazeFlySpeed
			vz = (dz / horizDist) * BlazeFlySpeed
			newYaw = yawToward(dx, dz)
		}
	}

	nextX := ent.X + vx
	nextY := ent.Y + newVY
	nextZ := ent.Z + vz
	onGround := false
	if cache != nil {
		belowY := int(math.Floor(nextY - 0.01))
		bx, bz := int(math.Floor(nextX)), int(math.Floor(nextZ))
		if belowY >= world.MinY && belowY < world.MinY+world.WorldHeight && !isPassableBlock(cache.GetBlock(bx, belowY, bz)) && nextY < float64(belowY+1) {
			nextY = float64(belowY + 1)
			newVY = 0
			onGround = true
			mgr.mu.Lock()
			if cur, ok := mgr.blazes[entityID]; ok && cur != nil {
				cur.VY = 0
			}
			mgr.mu.Unlock()
		}
	}
	s.entities.Update(entityID, func(e *world.Entity) {
		e.X = nextX
		e.Y = nextY
		e.Z = nextZ
		e.Yaw = newYaw
		e.HeadYaw = newYaw
		e.VelocityX = clampVelocityShort(vx)
		e.VelocityY = clampVelocityShort(newVY)
		e.VelocityZ = clampVelocityShort(vz)
		e.OnGround = onGround
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
	snap := *st
	mgr.mu.Unlock()

	dim := ent.EffectiveDimension()
	cache := s.DimensionChunks(dim)

	// 1. Periodic ambient crying sound (`minecraft:entity.ghast.ambient`).
	if snap.TickCounter%80 == 0 {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
			EncodeSoundPacket(SoundGhastAmbient, SoundSourceHostile, ent.X, ent.Y, ent.Z, 5.0, 1.0, 0))
		mgr.mu.Lock()
		if cur, ok := mgr.ghasts[entityID]; ok && cur != nil {
			cur.CryCount++
		}
		mgr.mu.Unlock()
	}

	// 2. Free flight (`RandomFloatAroundGoal` ±16 blocks + `GhastMoveControl` 4x4x4 AABB sweep every `2..6` ticks):
	hasWanted := snap.HasWanted
	wx, wy, wz := snap.WaypointX, snap.WaypointY, snap.WaypointZ
	if hasWanted {
		ddx, ddy, ddz := wx-ent.X, wy-ent.Y, wz-ent.Z
		dSq := ddx*ddx + ddy*ddy + ddz*ddz
		if dSq < GhastRandomFloatMinDistSq2612 || dSq > GhastRandomFloatMaxDistSq2612 {
			hasWanted = false
		}
	}
	if !hasWanted {
		if rng != nil {
			wx = ent.X + float64((rng.Float32()*2.0-1.0)*float32(GhastRandomFloatRadius2612))
			wy = ent.Y + float64((rng.Float32()*2.0-1.0)*float32(GhastRandomFloatRadius2612))
			wz = ent.Z + float64((rng.Float32()*2.0-1.0)*float32(GhastRandomFloatRadius2612))
		} else {
			wx, wy, wz = ent.X+4.0, ent.Y+1.0, ent.Z
		}
		minFlightY := float64(world.FlatSurfaceY + 2)
		if wy < minFlightY {
			wy = ent.Y + math.Abs(ent.Y-wy)
		}
		hasWanted = true
	}

	moveCD := snap.MoveCooldown
	vx, vy, vz := snap.VX, snap.VY, snap.VZ
	if hasWanted {
		moveCD--
		if moveCD <= 0 {
			stepAdd := 3
			if rng != nil {
				stepAdd = GhastCourseCooldownMinTicks2612 + rng.Intn(GhastCourseCooldownMaxTicks2612-GhastCourseCooldownMinTicks2612+1)
			}
			moveCD += stepAdd
			ddx, ddy, ddz := wx-ent.X, wy-ent.Y, wz-ent.Z
			dist := math.Sqrt(ddx*ddx + ddy*ddy + ddz*ddz)
			if dist > 1e-6 && CanGhastBoxReach2612(cache, ent.X, ent.Y, ent.Z, wx, wy, wz) {
				vx += (ddx / dist) * GhastMoveImpulsePerStep2612
				vy += (ddy / dist) * GhastMoveImpulsePerStep2612
				vz += (ddz / dist) * GhastMoveImpulsePerStep2612
			} else {
				hasWanted = false
			}
		}
	}

	nx, ny, nz := ent.X+vx, ent.Y+vy, ent.Z+vz
	if !CanGhastBoxReach2612(cache, ent.X, ent.Y, ent.Z, nx, ny, nz) {
		nx, ny, nz = ent.X, ent.Y, ent.Z
		vx, vy, vz = 0, 0, 0
		hasWanted = false
	}
	nextVX := vx * GhastFlyingAirInertia2612
	nextVY := vy * GhastFlyingAirInertia2612
	nextVZ := vz * GhastFlyingAirInertia2612

	// 3. Acquire nearest survival/adventure target player within FOLLOW_RANGE (100 blocks, |dy| <= 4.0 on initial acquisition).
	target := snap.TargetPlayer
	if target != nil {
		if target.IsDead() || world.NormalizeDimension(target.Dimension()) != dim {
			target = nil
		} else {
			ts := target.Snapshot()
			dxT, dyT, dzT := ts.X-nx, ts.Y-ny, ts.Z-nz
			if ts.GameMode == GameModeCreative || ts.GameMode == GameModeSpectator ||
				dxT*dxT+dyT*dyT+dzT*dzT > GhastFollowRange2612*GhastFollowRange2612 {
				target = nil
			}
		}
	}
	if target == nil {
		bestSq := GhastFollowRange2612 * GhastFollowRange2612
		for _, p := range s.onlinePlayersSnapshot() {
			if p == nil || p.IsDead() {
				continue
			}
			ps := p.Snapshot()
			if world.NormalizeDimension(ps.Dimension) != dim || ps.GameMode == GameModeCreative || ps.GameMode == GameModeSpectator {
				continue
			}
			if math.Abs(ps.Y-ny) > 4.0 {
				continue
			}
			dxT, dyT, dzT := ps.X-nx, ps.Y-ny, ps.Z-nz
			dSq := dxT*dxT + dyT*dyT + dzT*dzT
			if dSq <= bestSq {
				bestSq = dSq
				target = p
			}
		}
	}

	// 4. `GhastLookGoal` (`Ghast.faceMovementDirection`):
	yaw := ent.Yaw
	if target != nil && !target.IsDead() {
		ts := target.Snapshot()
		dxT, dyT, dzT := ts.X-nx, ts.Y-ny, ts.Z-nz
		if dxT*dxT+dyT*dyT+dzT*dzT < GhastShootMaxDistSqr2612 && math.Hypot(dxT, dzT) > 1e-5 {
			yaw = yawToward(dxT, dzT)
		}
	} else if math.Hypot(vx, vz) > 1e-5 {
		yaw = yawToward(vx, vz)
	}

	s.entities.Update(entityID, func(e *world.Entity) {
		e.X = nx
		e.Y = ny
		e.Z = nz
		e.Yaw = yaw
		e.HeadYaw = yaw
		e.VelocityX = clampVelocityShort(vx)
		e.VelocityY = clampVelocityShort(vy)
		e.VelocityZ = clampVelocityShort(vz)
		e.OnGround = false
	})

	mgr.mu.Lock()
	if cur, ok := mgr.ghasts[entityID]; ok && cur != nil {
		cur.HasWanted = hasWanted
		cur.WaypointX = wx
		cur.WaypointY = wy
		cur.WaypointZ = wz
		cur.VX = nextVX
		cur.VY = nextVY
		cur.VZ = nextVZ
		cur.MoveCooldown = moveCD
		cur.TargetPlayer = target
	}
	mgr.mu.Unlock()

	// 5. `GhastShootFireballGoal` (`chargeTime` starts at 0, warn at 10, `DATA_IS_CHARGING = (chargeTime > 10)`, shoot at 20 -> reset -40):
	if target != nil && !target.IsDead() {
		tSnap := target.Snapshot()
		dx := tSnap.X - nx
		dy := tSnap.Y - ny
		dz := tSnap.Z - nz
		distSq := dx*dx + dy*dy + dz*dz
		hasLOS := distSq < GhastShootMaxDistSqr2612 &&
			hasColliderLineOfSight2612(cache, nx, ny+GhastEyeHeight2612, nz, tSnap.X, tSnap.Y+1.62, tSnap.Z)

		charge := snap.ChargeTicks
		if hasLOS {
			charge++
			cp := world.ChunkPos{X: int32(int64(math.Floor(nx)) >> 4), Z: int32(int64(math.Floor(nz)) >> 4)}
			if charge == GhastChargeWarnTicks {
				s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
					EncodeSoundPacket(SoundGhastWarn, SoundSourceHostile, nx, ny, nz, 10.0, 1.0, 0))
				mgr.mu.Lock()
				if cur, ok := mgr.ghasts[entityID]; ok && cur != nil {
					cur.CryCount++
				}
				mgr.mu.Unlock()
			}
			if charge == GhastChargeFireTicks {
				spawnX, spawnY, spawnZ, dirX, dirY, dirZ := ComputeGhastFireballSpawnAndAim2612(
					nx, ny, nz, yaw, 0, tSnap.X, tSnap.Y, tSnap.Z, 1.8,
				)
				_, _ = s.SpawnGhastFireball(dim, entityID, spawnX, spawnY, spawnZ, dirX, dirY, dirZ, snap.ExplosionPower)
				charge = GhastPostShotCooldownTicks // -40
			}
		} else if charge > 0 {
			charge--
		}
		mgr.mu.Lock()
		if cur, ok := mgr.ghasts[entityID]; ok && cur != nil {
			cur.ChargeTicks = charge
		}
		mgr.mu.Unlock()
		s.SetGhastCharging(entityID, charge > GhastChargeWarnTicks)
		return
	}

	// No target (`GhastShootFireballGoal.stop()`):
	mgr.mu.Lock()
	if cur, ok := mgr.ghasts[entityID]; ok && cur != nil {
		cur.ChargeTicks = 0
	}
	mgr.mu.Unlock()
	if snap.Charging {
		s.SetGhastCharging(entityID, false)
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
	_, noAI, silent, _ := s.GetSlimeOrMagmaCubeCustomProps2612(entityID)
	if noAI {
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
	if st.GrowTiredTimer > 0 {
		st.GrowTiredTimer--
	}
	snap := *st
	mgr.mu.Unlock()

	dim := ent.EffectiveDimension()
	isMagma := snap.TypeName == MobTypeMagmaCube
	size := s.SlimeSize(entityID)

	// 1. Check lava / water immersion (`SlimeFloatGoal` 80% per-tick jump + `MagmaCube.jumpInLiquid(LAVA)` = `0.22 + 0.05*size`).
	cache := s.DimensionChunks(dim)
	inLava := false
	inWater := false
	if cache != nil {
		bx, by, bz := int(math.Floor(ent.X)), int(math.Floor(ent.Y)), int(math.Floor(ent.Z))
		blk := cache.GetBlock(bx, by, bz)
		blkName := canonicalResourceName(world.BlockName(blk))
		if blkName == "minecraft:lava" {
			inLava = true
		} else if blk == world.StateWater || cache.FluidHeightAt(bx, by, bz) > 0 {
			inWater = true
		}
	}
	if isMagma && inLava {
		if !snap.InLava || rng == nil || rng.Float32() < SlimeFloatGoalJumpChance2612 {
			vy := ComputeMagmaCubeLavaJumpVelocity2612(size)
			s.entities.Update(entityID, func(e *world.Entity) {
				e.Y += vy
				e.VelocityY = clampVelocityShort(vy)
				e.OnGround = false
			})
		}
	} else if inWater {
		if rng == nil || rng.Float32() < SlimeFloatGoalJumpChance2612 {
			vy := SlimeWaterJumpDeltaY2612
			s.entities.Update(entityID, func(e *world.Entity) {
				e.Y += vy
				e.VelocityY = clampVelocityShort(vy)
				e.OnGround = false
			})
		}
	}
	mgr.mu.Lock()
	if cur, ok := mgr.magmaCubes[entityID]; ok && cur != nil {
		cur.InLava = inLava
	}
	mgr.mu.Unlock()

	ent = s.entities.Get(entityID)
	if ent == nil {
		return
	}

	// 2. Acquire nearest survival/adventure target player within 16 blocks with `|player.Y - slime.Y| <= 4.0`
	//    (`Slime.registerGoals` selector + `SlimeAttackGoal.growTiredTimer = 300`).
	target := snap.TargetPlayer
	if target != nil {
		if target.IsDead() || world.NormalizeDimension(target.Dimension()) != dim {
			target = nil
		} else {
			ts := target.Snapshot()
			dx, dy, dz := ts.X-ent.X, ts.Y-ent.Y, ts.Z-ent.Z
			if ts.GameMode == GameModeCreative || ts.GameMode == GameModeSpectator ||
				dx*dx+dy*dy+dz*dz > MagmaCubeAggroRange*MagmaCubeAggroRange || snap.GrowTiredTimer <= 0 {
				target = nil
			}
		}
	}
	if target == nil {
		bestDistSq := MagmaCubeAggroRange * MagmaCubeAggroRange
		for _, p := range s.onlinePlayersSnapshot() {
			if p == nil || p.IsDead() {
				continue
			}
			ps := p.Snapshot()
			if world.NormalizeDimension(ps.Dimension) != dim || ps.GameMode == GameModeCreative || ps.GameMode == GameModeSpectator {
				continue
			}
			if math.Abs(ps.Y-ent.Y) > SlimeTargetMaxVerticalDiff2612 {
				continue
			}
			dx, dy, dz := ps.X-ent.X, ps.Y-ent.Y, ps.Z-ent.Z
			dSq := dx*dx + dy*dy + dz*dz
			if dSq <= bestDistSq {
				bestDistSq = dSq
				target = p
			}
		}
		mgr.mu.Lock()
		if cur, ok := mgr.magmaCubes[entityID]; ok && cur != nil {
			cur.TargetPlayer = target
			if target != nil {
				cur.GrowTiredTimer = SlimeAttackGrowTiredTicks2612
			}
		}
		mgr.mu.Unlock()
	}

	// 2b. Rotation (`SlimeAttackGoal`: `lookAt(target, 10.0F, 10.0F)` vs `SlimeRandomDirectionGoal` + `SlimeMoveControl` `rotlerp 90.0F`).
	curYaw := ent.Yaw
	if target != nil && !target.IsDead() {
		tSnap := target.Snapshot()
		dx := tSnap.X - ent.X
		dz := tSnap.Z - ent.Z
		if math.Hypot(dx, dz) > 1e-4 {
			goalYaw := yawToward(dx, dz)
			curYaw = RotlerpDegrees2612(ent.Yaw, goalYaw, SlimeAttackLookMaxTurnDeg2612)
		}
		mgr.mu.Lock()
		if cur, ok := mgr.magmaCubes[entityID]; ok && cur != nil {
			cur.WantedYaw = curYaw
		}
		mgr.mu.Unlock()
	} else {
		nextRand := snap.NextRandomizeTime - 1
		wantedYaw := snap.WantedYaw
		if nextRand <= 0 {
			if rng != nil {
				nextRand = SlimeRandomDirMinTicks2612 + rng.Intn(SlimeRandomDirMaxTicks2612-SlimeRandomDirMinTicks2612+1)
				wantedYaw = float32(rng.Intn(360)) - 180.0
			} else {
				nextRand = 60
			}
		}
		curYaw = RotlerpDegrees2612(ent.Yaw, wantedYaw, SlimeMoveControlMaxTurnDeg2612)
		mgr.mu.Lock()
		if cur, ok := mgr.magmaCubes[entityID]; ok && cur != nil {
			cur.WantedYaw = wantedYaw
			cur.NextRandomizeTime = nextRand
		}
		mgr.mu.Unlock()
	}
	s.entities.Update(entityID, func(e *world.Entity) {
		e.Yaw = curYaw
		e.HeadYaw = curYaw
	})
	ent = s.entities.Get(entityID)
	if ent == nil {
		return
	}

	// 3. Hop/jump along facing yaw when JumpDelay reaches 0 (`SlimeMoveControl.tick` + `jumpFromGround`).
	if snap.JumpDelay <= 0 {
		hasTarget := target != nil && !target.IsDead()
		nextDelay := RollSlimeJumpDelay2612(isMagma, size, hasTarget, rng)
		_, hopSpeed, _, _, _ := SlimeAttributesBySize2612(isMagma, size)
		if inLava || inWater {
			hopSpeed *= SlimeFloatSpeedModifier2612
		}
		if hasTarget {
			tSnap := target.Snapshot()
			hDist := math.Hypot(tSnap.X-ent.X, tSnap.Z-ent.Z)
			if hopSpeed > hDist {
				hopSpeed = hDist
			}
		}
		rad := float64(curYaw) * (math.Pi / 180.0)
		stepX := -math.Sin(rad) * hopSpeed
		stepZ := math.Cos(rad) * hopSpeed
		jumpVY := ComputeSlimeJumpVelocityY2612(isMagma, size)
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
			e.Yaw = curYaw
			e.HeadYaw = curYaw
			e.VelocityX = clampVelocityShort(stepX)
			e.VelocityY = clampVelocityShort(jumpVY)
			e.VelocityZ = clampVelocityShort(stepZ)
		})
		if !silent {
			_, _, _, jumpSnd := SlimeSoundSet2612(isMagma, size)
			vol := float32(0.4 * float64(size))
			cp := ent.ChunkPos()
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
				EncodeSoundPacket(jumpSnd, SoundSourceHostile, nextX, nextY, nextZ, vol, 1.0, 0))
		}
		mgr.mu.Lock()
		if cur, ok := mgr.magmaCubes[entityID]; ok && cur != nil {
			cur.JumpDelay = nextDelay
			cur.JumpCount++
		}
		mgr.mu.Unlock()
	}

	// 4. Melee contact damage (`Slime.dealDamage`: requires `isDealsDamage()` + melee range + `hasLineOfSight`!).
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
			if dx*dx+dy*dy+dz*dz <= reach*reach &&
				hasColliderLineOfSight2612(cache, ent.X, ent.Y+SlimeBaseEyeHeight2612*float64(size), ent.Z, tSnap.X, tSnap.Y+1.62, tSnap.Z) {
				s.AttackPlayerFromMob(entityID, snap.TypeName, ent.X, ent.Z, target, dmg)
				if !silent {
					cp := ent.ChunkPos()
					s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
						EncodeSoundPacket(SoundSlimeAttack, SoundSourceHostile, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
				}
				mgr.mu.Lock()
				if cur, ok := mgr.magmaCubes[entityID]; ok && cur != nil {
					cur.AttackCooldown = 20
				}
				mgr.mu.Unlock()
			}
		}
	}
}
