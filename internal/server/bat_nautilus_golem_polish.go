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

// --- 1. Bat (`minecraft:bat`, Entity ID 10), Nautilus (`minecraft:nautilus`, Entity ID 88),
//        and Zombie Nautilus (`minecraft:zombie_nautilus`, Entity ID 152) Constants ---

const (
	// MobTypeBat is the canonical 26.1.2 entity identifier for Bat (`minecraft:bat`).
	MobTypeBat = "minecraft:bat"
	// EntityTypeBatID is the protocol 775 numeric entity type ID for `minecraft:bat`.
	EntityTypeBatID int = 10
	// DefaultBatMaxHealth is the vanilla 26.1.2 max health for a Bat (6.0 HP = 3 hearts).
	DefaultBatMaxHealth float32 = 6.0

	// EntityDataBatFlagsID is `Bat.DATA_ID_FLAGS` at SynchedEntityData index 16 (`BYTE` serializer 0).
	EntityDataBatFlagsID byte = 16
	// BatFlagResting is bit 0x01 (`FLAG_RESTING = 1`) in `Bat.DATA_ID_FLAGS`.
	BatFlagResting byte = 0x01

	// BatWakePlayerHorizontalDist is the horizontal distance (4.0 blocks) within which
	// a nearby non-spectator player wakes a resting Bat.
	BatWakePlayerHorizontalDist = 4.0
	// BatFlightSpeed is the horizontal flight speed per tick for an active Bat.
	BatFlightSpeed = 0.10
	// BatVerticalFlightDamping is the vertical flight damping factor (`0.6` in `Bat.customServerAiStep`).
	BatVerticalFlightDamping = 0.6

	SoundBatAmbient = "minecraft:entity.bat.ambient"
	SoundBatHurt    = "minecraft:entity.bat.hurt"
	SoundBatDeath   = "minecraft:entity.bat.death"
	SoundBatTakeoff = "minecraft:entity.bat.takeoff"

	// MobTypeNautilus is the canonical 26.1.2 entity identifier for Nautilus (`minecraft:nautilus`).
	MobTypeNautilus = "minecraft:nautilus"
	// EntityTypeNautilusID is the protocol 775 numeric entity type ID for `minecraft:nautilus`.
	EntityTypeNautilusID int = 88
	// DefaultNautilusMaxHealth is the vanilla 26.1.2 max health for a Nautilus (15.0 HP = 7.5 hearts).
	DefaultNautilusMaxHealth float32 = 15.0

	// MobTypeZombieNautilus is the canonical 26.1.2 entity identifier for Zombie Nautilus (`minecraft:zombie_nautilus`).
	MobTypeZombieNautilus = "minecraft:zombie_nautilus"
	// EntityTypeZombieNautilusID is the protocol 775 numeric entity type ID for `minecraft:zombie_nautilus`.
	EntityTypeZombieNautilusID int = 152
	// DefaultZombieNautilusMaxHealth is the vanilla 26.1.2 max health for a Zombie Nautilus (15.0 HP = 7.5 hearts).
	DefaultZombieNautilusMaxHealth float32 = 15.0

	// Nautilus movement, attack, air supply, dash, and rider effect constants (from `AbstractNautilus` & `Nautilus` in 26.1.2.jar).
	NautilusSwimSpeed                  = 1.0
	ZombieNautilusSwimSpeed            = 1.1
	NautilusAttackDamage       float32 = 3.0
	NautilusKnockbackResist            = 0.3
	NautilusTotalAirSupplyTick         = 300 // 15s out of water before dry-out damage
	NautilusDryOutDamage       float32 = 2.0
	NautilusDashCooldownTicks          = 40
	NautilusDashMinDuration            = 5
	NautilusDashWaterMomentum          = 1.2
	NautilusDashLandMomentum           = 0.5
	NautilusTameSuccessChance          = 3 // 1/3 chance (`random.nextInt(3) == 0`)

	BreathOfTheNautilusDurationTicks int32 = 60
	BreathOfTheNautilusRefreshTicks        = 40

	// SynchedEntityData indices and serializers for AbstractNautilus & ZombieNautilus (protocol 775).
	EntityDataTamableOwnerID               byte  = 18 // TamableAnimal.DATA_OWNERUUID_ID (OPTIONAL_LIVING_ENTITY_REFERENCE serializer = 14)
	EntityDataSerializerOptionalUUID       int32 = 14
	EntityDataNautilusDashID               byte  = 19 // AbstractNautilus.DASH (BOOLEAN serializer = 8)
	EntityDataZombieNautilusVariantID      byte  = 20 // ZombieNautilus.DATA_VARIANT_ID (ZOMBIE_NAUTILUS_VARIANT serializer = 32)
	EntityDataSerializerZombieNautilusVar  int32 = 32
	ZombieNautilusVariantTemperate         int32 = 0 // ZombieNautilusVariants.TEMPERATE
	ZombieNautilusVariantWarm              int32 = 1 // ZombieNautilusVariants.WARM

	SoundNautilusAmbient       = "minecraft:entity.nautilus.ambient"
	SoundNautilusHurt          = "minecraft:entity.nautilus.hurt"
	SoundNautilusDeath         = "minecraft:entity.nautilus.death"
	SoundNautilusDash          = "minecraft:entity.nautilus.dash"
	SoundNautilusEat           = "minecraft:entity.nautilus.eat"
	SoundZombieNautilusAmbient = "minecraft:entity.zombie_nautilus.ambient"
	SoundZombieNautilusHurt    = "minecraft:entity.zombie_nautilus.hurt"
	SoundZombieNautilusDeath   = "minecraft:entity.zombie_nautilus.death"

	// Snow Golem (`minecraft:snow_golem`) 26.1.2 SynchedEntityData & combat constants.
	EntityDataSnowGolemPumpkinID byte    = 16   // SnowGolem.DATA_PUMPKIN_ID (BYTE serializer = 0)
	SnowGolemPumpkinFlag         byte    = 0x10 // SnowGolem.PUMPKIN_FLAG = 16
	SnowGolemShootCooldownTicks  int     = 20   // RangedAttackGoal(this, 1.25, 20, 10.0F)
	SnowGolemShootRange          float64 = 10.0
	SnowGolemWanderSpeed         float64 = 0.20
	SnowGolemMeltDamage          float32 = 1.0
	SnowballBlazeDamage          float32 = 3.0 // Snowball.onHitEntity deals 3 damage to Blaze, 0 to other mobs

	SoundSnowGolemAmbient = "minecraft:entity.snow_golem.ambient"
	SoundSnowGolemHurt    = "minecraft:entity.snow_golem.hurt"
	SoundSnowGolemDeath   = "minecraft:entity.snow_golem.death"
	SoundSnowGolemShoot   = "minecraft:entity.snow_golem.shoot"
	SoundSnowGolemShear   = "minecraft:entity.snow_golem.shear"

	// Copper Golem (`minecraft:copper_golem`) 26.1.2 SynchedEntityData & weathering constants.
	EntityDataCopperGolemWeatherStateID       byte  = 16 // CopperGolem.DATA_WEATHER_STATE (serializer 38)
	EntityDataCopperGolemStateID              byte  = 17 // CopperGolem.COPPER_GOLEM_STATE (serializer 37)
	EntityDataSerializerCopperGolemState      int32 = 37
	EntityDataSerializerWeatheringCopperState int32 = 38

	CopperGolemBehaviorStateIdle           int32 = 0 // CopperGolemState.IDLE
	CopperGolemBehaviorStateGettingItem    int32 = 1 // CopperGolemState.GETTING_ITEM
	CopperGolemBehaviorStateGettingNoItem  int32 = 2 // CopperGolemState.GETTING_NO_ITEM
	CopperGolemBehaviorStateDroppingItem   int32 = 3 // CopperGolemState.DROPPING_ITEM
	CopperGolemBehaviorStateDroppingNoItem int32 = 4 // CopperGolemState.DROPPING_NO_ITEM

	CopperGolemWeatheringTickFrom int     = 504000
	CopperGolemWeatheringTickTo   int     = 552000
	CopperGolemTurnToStatueChance float32 = 0.0058

	SoundCopperGolemSpawn        = "minecraft:entity.copper_golem.spawn"
	SoundCopperGolemShear        = "minecraft:entity.copper_golem.shear"
	SoundCopperGolemBecomeStatue = "minecraft:entity.copper_golem.become_statue"
	SoundCopperGolemHurt         = "minecraft:entity.copper_golem.hurt"
	SoundCopperGolemDeath        = "minecraft:entity.copper_golem.death"
	SoundAxeScrape               = "minecraft:item.axe.scrape"
	SoundAxeWaxOff               = "minecraft:item.axe.wax_off"
	SoundHoneycombWaxOn          = "minecraft:item.honeycomb.wax_on"
)

// BatState tracks per-entity resting state and target flight waypoint for a `minecraft:bat`.
type BatState struct {
	EntityID  int32
	Dimension string
	Resting   bool
	TargetX   float64
	TargetY   float64
	TargetZ   float64
	HasTarget bool
}

// NautilusState tracks per-entity taming, saddle, body armor, dash, air supply,
// breeding, and variant state for `minecraft:nautilus` and `minecraft:zombie_nautilus`.
type NautilusState struct {
	EntityID     int32
	TypeName     string
	Dimension    string
	Baby         bool
	Tamed        bool
	OwnerUUID    [16]byte
	HasOwner     bool
	Saddled      bool
	BodyArmor    world.ItemStack
	Dashing      bool
	DashCooldown int
	DashDuration int
	AirSupply    int
	VariantID    int32 // 0 = temperate, 1 = warm (used by ZombieNautilus)
	RiderUUID    [16]byte
	HasRider     bool
	RiderTicks   int
	InLoveTicks  int
	BreedCD      int
}

// SnowGolemRuntimeState tracks pumpkin presence and snowball attack cooldown for a `minecraft:snow_golem`.
type SnowGolemRuntimeState struct {
	EntityID      int32
	Dimension     string
	HasPumpkin    bool
	ShootCooldown int
}

// CopperGolemExtraState tracks 26.1.2 behavior state (`IDLE`, `GETTING_ITEM`, etc.),
// held item, antenna item (shearable poppy), and last lightning bolt UUID for `minecraft:copper_golem`.
type CopperGolemExtraState struct {
	BehaviorState     int32
	MainHandItem      world.ItemStack
	AntennaItem       world.ItemStack
	LastLightningUUID [16]byte
	HasLightningUUID  bool
}

type batNautilusGolemTracker struct {
	mu          sync.RWMutex
	bats        map[int32]*BatState
	nautiluses  map[int32]*NautilusState
	snowGolems  map[int32]*SnowGolemRuntimeState
	copperExtra map[int32]*CopperGolemExtraState
}

var serverBatNautilusTrackers sync.Map // map[*Server]*batNautilusGolemTracker

func (s *Server) batNautilusTracker() *batNautilusGolemTracker {
	if v, ok := serverBatNautilusTrackers.Load(s); ok {
		return v.(*batNautilusGolemTracker)
	}
	actual, _ := serverBatNautilusTrackers.LoadOrStore(s, &batNautilusGolemTracker{
		bats:        make(map[int32]*BatState),
		nautiluses:  make(map[int32]*NautilusState),
		snowGolems:  make(map[int32]*SnowGolemRuntimeState),
		copperExtra: make(map[int32]*CopperGolemExtraState),
	})
	return actual.(*batNautilusGolemTracker)
}

// --- 2. Bat (`minecraft:bat`) Implementation ---

// EncodeBatFlagsDataPacket encodes a ClientboundSetEntityDataPacket (0x63)
// payload setting `Bat.DATA_ID_FLAGS` (index 16, BYTE serializer 0: bit 0x01 = resting).
func EncodeBatFlagsDataPacket(entityID int32, resting bool) []byte {
	var flags byte
	if resting {
		flags |= BatFlagResting
	}
	w := protocol.NewWriter(8)
	w.VarInt(entityID)
	w.Byte(EntityDataBatFlagsID)
	w.VarInt(EntityDataSerializerByte)
	w.Byte(flags)
	w.Byte(0xFF)
	return w.Bytes()
}

// DecodeBatFlagsDataPacket decodes a ClientboundSetEntityDataPacket payload
// produced by EncodeBatFlagsDataPacket.
func DecodeBatFlagsDataPacket(r *protocol.Reader) (entityID int32, resting bool, err error) {
	if r == nil {
		return 0, false, errors.New("nil reader")
	}
	entityID, err = r.VarInt()
	if err != nil {
		return 0, false, err
	}
	idx, err := r.ReadByte()
	if err != nil {
		return 0, false, err
	}
	if idx != EntityDataBatFlagsID {
		return 0, false, errors.New("unexpected bat data accessor index")
	}
	ser, err := r.VarInt()
	if err != nil {
		return 0, false, err
	}
	if ser != EntityDataSerializerByte {
		return 0, false, errors.New("unexpected bat data serializer")
	}
	flags, err := r.ReadByte()
	if err != nil {
		return 0, false, err
	}
	term, err := r.ReadByte()
	if err != nil {
		return 0, false, err
	}
	if term != 0xFF {
		return 0, false, errors.New("missing 0xFF entity data terminator")
	}
	return entityID, (flags & BatFlagResting) != 0, nil
}

// IsBatImmuneToFallDamage reports that `minecraft:bat` is immune to fall damage
// (`Bat.isIgnoringBlockTriggers() == true`, `Bat.checkFallDamage` is a no-op).
func IsBatImmuneToFallDamage() bool {
	return true
}

// DoesBatTriggerPressurePlateOrTripwire reports that `minecraft:bat` does not
// trigger pressure plates or tripwires (`Bat.isIgnoringBlockTriggers() == true`).
func DoesBatTriggerPressurePlateOrTripwire() bool {
	return false
}

// RollBatDeathLoot2612 returns the vanilla 26.1.2 death loot for `minecraft:bat`
// (`data/minecraft/loot_table/entities/bat.json` has `"pools": []`).
func RollBatDeathLoot2612() []world.ItemStack {
	return nil
}

// BatKillXP2612 returns the vanilla 26.1.2 XP dropped by a killed Bat (`0` XP).
func BatKillXP2612() int32 {
	return 0
}

// CanSpawnBatAt2612 checks vanilla 26.1.2 `Bat.checkBatSpawnRules`:
//   - Must be below the world surface (`y < surfaceY`)
//   - Block below must be solid/spawnable
//   - Local max light level must be `<= maxLightThreshold` (`0..3`, or `0..6` during Halloween,
//     defaulting to `<= 3` when rng is nil).
func CanSpawnBatAt2612(cache *world.Cache, x, y, z int, maxLight byte, surfaceY int, rng *rand.Rand) bool {
	if y >= surfaceY {
		return false
	}
	threshold := byte(3)
	if rng != nil {
		threshold = byte(rng.Intn(4)) // 0..3
	}
	if maxLight > threshold {
		return false
	}
	if cache != nil {
		below := cache.GetBlock(x, y-1, z)
		if isPassableBlock(below) || below == world.StateWater {
			return false
		}
	}
	return true
}

// SpawnBat spawns a `minecraft:bat` (entity ID 10, 6.0 HP) at (x, y, z) in dimension
// with the given initial `resting` state and broadcasts its `DATA_ID_FLAGS` packet.
func (s *Server) SpawnBat(dimension string, x, y, z float64, resting bool) (int32, error) {
	if s == nil || s.entities == nil {
		return 0, errors.New("server: nil entity manager")
	}
	normDim := world.NormalizeDimension(dimension)
	typeID := ResolveEntityTypeID(MobTypeBat)
	if typeID < 0 {
		typeID = EntityTypeBatID
	}
	id := s.entities.Add(&world.Entity{
		TypeID:    typeID,
		TypeName:  MobTypeBat,
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
	mem.health = DefaultBatMaxHealth
	mem.lastHealth = DefaultBatMaxHealth
	tr.mu.Unlock()

	bt := s.batNautilusTracker()
	bt.mu.Lock()
	bt.bats[id] = &BatState{
		EntityID:  id,
		Dimension: normDim,
		Resting:   resting,
	}
	bt.mu.Unlock()

	if ent := s.entities.Get(id); ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, ent))
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, PlaySetEntityDataCB, EncodeBatFlagsDataPacket(id, resting))
	}
	return id, nil
}

// GetBatState returns a snapshot of the BatState for entityID.
func (s *Server) GetBatState(entityID int32) (BatState, bool) {
	if s == nil || entityID <= 0 {
		return BatState{}, false
	}
	bt := s.batNautilusTracker()
	bt.mu.RLock()
	defer bt.mu.RUnlock()
	st, ok := bt.bats[entityID]
	if !ok || st == nil {
		return BatState{}, false
	}
	return *st, true
}

// IsBatResting reports whether the Bat with entityID is currently hanging upside-down (`FLAG_RESTING`).
func (s *Server) IsBatResting(entityID int32) bool {
	st, ok := s.GetBatState(entityID)
	return ok && st.Resting
}

// SetBatResting updates the resting state of a Bat and broadcasts `EncodeBatFlagsDataPacket`.
func (s *Server) SetBatResting(entityID int32, resting bool) bool {
	if s == nil || s.entities == nil || entityID <= 0 {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeBat {
		return false
	}
	bt := s.batNautilusTracker()
	bt.mu.Lock()
	st := bt.bats[entityID]
	if st == nil {
		st = &BatState{EntityID: entityID, Dimension: ent.EffectiveDimension()}
		bt.bats[entityID] = st
	}
	changed := st.Resting != resting
	st.Resting = resting
	if resting {
		st.HasTarget = false
	}
	bt.mu.Unlock()

	if changed {
		cp := ent.ChunkPos()
		dim := ent.EffectiveDimension()
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB, EncodeBatFlagsDataPacket(entityID, resting))
		if !resting {
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
				EncodeSoundPacket(SoundBatTakeoff, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 0.05, 1.0, 0))
		}
	}
	return true
}

// WakeBatOnHurt wakes a resting Bat when it takes damage (`Bat.hurtServer`).
func (s *Server) WakeBatOnHurt(entityID int32) bool {
	if !s.IsBatResting(entityID) {
		return false
	}
	return s.SetBatResting(entityID, false)
}

func isSolidCeilingBlock(cache *world.Cache, x, y, z int) bool {
	if cache == nil || y < world.MinY || y >= world.MinY+world.WorldHeight {
		return false
	}
	st := cache.GetBlock(x, y, z)
	return !isPassableBlock(st) && st != world.StateWater
}

// TickBats steps all `minecraft:bat` entities:
//   - Resting bats check that the ceiling block above (`y+1`) is still solid and
//     that no non-spectator player is within 4.0 blocks horizontally; if disturbed,
//     they wake (`FLAG_RESTING = 0`) and play `entity.bat.takeoff`.
//   - Flying bats move erratically toward a 3D target waypoint with `0.6` vertical
//     damping and can re-perch when beneath a solid ceiling block away from players.
func (s *Server) TickBats(rng *rand.Rand) {
	if s == nil || s.entities == nil {
		return
	}
	if rng == nil {
		rng = rand.New(rand.NewSource(1))
	}
	all := s.entities.AppendAll(nil)
	var batEnts []world.Entity
	for i := range all {
		if canonicalResourceName(all[i].TypeName) == MobTypeBat {
			batEnts = append(batEnts, all[i])
		}
	}
	if len(batEnts) == 0 {
		return
	}

	s.playersMu.RLock()
	players := make([]*PlayerSession, 0, len(s.players))
	for _, p := range s.players {
		if p != nil && !p.IsDead() {
			players = append(players, p)
		}
	}
	s.playersMu.RUnlock()

	bt := s.batNautilusTracker()
	for i := range batEnts {
		ent := &batEnts[i]
		dim := ent.EffectiveDimension()
		cache := s.DimensionChunks(dim)
		bx, by, bz := int(math.Floor(ent.X)), int(math.Floor(ent.Y)), int(math.Floor(ent.Z))

		bt.mu.Lock()
		st := bt.bats[ent.ID]
		if st == nil {
			st = &BatState{EntityID: ent.ID, Dimension: dim, Resting: true}
			bt.bats[ent.ID] = st
		}
		resting := st.Resting
		bt.mu.Unlock()

		nearPlayer := false
		for _, p := range players {
			if world.NormalizeDimension(p.Dimension()) != dim {
				continue
			}
			ps := p.Snapshot()
			if ps.GameMode == GameModeSpectator || ps.Health <= 0 {
				continue
			}
			dx := ps.X - ent.X
			dy := ps.Y - ent.Y
			dz := ps.Z - ent.Z
			if dx*dx+dy*dy+dz*dz <= BatWakePlayerHorizontalDist*BatWakePlayerHorizontalDist {
				nearPlayer = true
				break
			}
		}

		if resting {
			ceilingOK := isSolidCeilingBlock(cache, bx, by+1, bz)
			if !ceilingOK || nearPlayer || rng.Intn(200) == 0 {
				s.SetBatResting(ent.ID, false)
			} else {
				s.entities.Update(ent.ID, func(e *world.Entity) {
					e.VelocityX, e.VelocityY, e.VelocityZ = 0, 0, 0
				})
			}
			continue
		}

		// Flying bat AI (`Bat.customServerAiStep`).
		bt.mu.Lock()
		if !st.HasTarget || rng.Intn(30) == 0 {
			tx := ent.X + float64(rng.Intn(7)-rng.Intn(7))
			ty := ent.Y + float64(rng.Intn(6)-2)
			tz := ent.Z + float64(rng.Intn(7)-rng.Intn(7))
			if cache != nil {
				tbx, tby, tbz := int(math.Floor(tx)), int(math.Floor(ty)), int(math.Floor(tz))
				if tby > world.MinY && tby < world.MinY+world.WorldHeight && isPassableBlock(cache.GetBlock(tbx, tby, tbz)) {
					st.TargetX, st.TargetY, st.TargetZ = tx, ty, tz
					st.HasTarget = true
				}
			} else {
				st.TargetX, st.TargetY, st.TargetZ = tx, ty, tz
				st.HasTarget = true
			}
		}
		tx, ty, tz := st.TargetX, st.TargetY, st.TargetZ
		hasTgt := st.HasTarget
		bt.mu.Unlock()

		if hasTgt {
			dx := tx - ent.X
			dy := (ty - ent.Y) * BatVerticalFlightDamping
			dz := tz - ent.Z
			dist := math.Sqrt(dx*dx + dy*dy + dz*dz)
			if dist > 0.05 {
				vx := (dx / dist) * BatFlightSpeed
				vy := (dy / dist) * BatFlightSpeed * BatVerticalFlightDamping
				vz := (dz / dist) * BatFlightSpeed
				nx, ny, nz := ent.X+vx, ent.Y+vy, ent.Z+vz
				canMove := true
				if cache != nil {
					nbx, nby, nbz := int(math.Floor(nx)), int(math.Floor(ny)), int(math.Floor(nz))
					if nby < world.MinY || nby >= world.MinY+world.WorldHeight || !isPassableBlock(cache.GetBlock(nbx, nby, nbz)) {
						canMove = false
					}
				}
				if canMove {
					yaw := yawToward(vx, vz)
					s.entities.Update(ent.ID, func(e *world.Entity) {
						e.X, e.Y, e.Z = nx, ny, nz
						e.Yaw = yaw
						e.HeadYaw = yaw
						e.VelocityX = clampVelocityShort(vx)
						e.VelocityY = clampVelocityShort(vy)
						e.VelocityZ = clampVelocityShort(vz)
					})
				}
			}
		}

		// Re-perch under a solid ceiling block when no player is within 4.0 blocks.
		if !nearPlayer && isSolidCeilingBlock(cache, bx, by+1, bz) && rng.Intn(100) == 0 {
			s.SetBatResting(ent.ID, true)
		}
	}
}

// --- 3. Nautilus (`minecraft:nautilus`) & Zombie Nautilus (`minecraft:zombie_nautilus`) Implementation ---

// IsNautilusMobType reports whether typeName is `minecraft:nautilus` or `minecraft:zombie_nautilus`.
func IsNautilusMobType(typeName string) bool {
	switch canonicalResourceName(typeName) {
	case MobTypeNautilus, MobTypeZombieNautilus:
		return true
	default:
		return false
	}
}

// IsNautilusTamingItem2612 reports whether itemName is in `#minecraft:nautilus_taming_items`
// (`minecraft:pufferfish` or `minecraft:pufferfish_bucket`, verified from 26.1.2.jar).
func IsNautilusTamingItem2612(itemName string) bool {
	switch canonicalResourceName(itemName) {
	case "minecraft:pufferfish", "minecraft:pufferfish_bucket":
		return true
	default:
		return false
	}
}

// IsNautilusBucketFood2612 reports whether itemName is in `#minecraft:nautilus_bucket_food`
// (`pufferfish_bucket`, `cod_bucket`, `salmon_bucket`, `tropical_fish_bucket`, verified from 26.1.2.jar).
// Feeding any bucket food to a Nautilus or Zombie Nautilus returns `minecraft:water_bucket`!
func IsNautilusBucketFood2612(itemName string) bool {
	switch canonicalResourceName(itemName) {
	case "minecraft:pufferfish_bucket",
		"minecraft:cod_bucket",
		"minecraft:salmon_bucket",
		"minecraft:tropical_fish_bucket":
		return true
	default:
		return false
	}
}

// IsNautilusFoodItem2612 reports whether itemName is in `#minecraft:nautilus_food`
// (`#minecraft:fishes` + `#minecraft:nautilus_bucket_food`, verified from 26.1.2.jar).
func IsNautilusFoodItem2612(itemName string) bool {
	if IsNautilusBucketFood2612(itemName) {
		return true
	}
	switch canonicalResourceName(itemName) {
	case "minecraft:cod",
		"minecraft:cooked_cod",
		"minecraft:salmon",
		"minecraft:cooked_salmon",
		"minecraft:tropical_fish",
		"minecraft:pufferfish":
		return true
	default:
		return false
	}
}

// IsNautilusArmorItem2612 reports whether itemName is a 26.1.2 Nautilus body armor item
// and returns its armor defense and toughness values.
func IsNautilusArmorItem2612(itemName string) (defense, toughness float32, ok bool) {
	switch canonicalResourceName(itemName) {
	case "minecraft:copper_nautilus_armor":
		return 4.0, 0.0, true
	case "minecraft:golden_nautilus_armor":
		return 7.0, 0.0, true
	case "minecraft:iron_nautilus_armor":
		return 5.0, 0.0, true
	case "minecraft:diamond_nautilus_armor":
		return 11.0, 2.0, true
	case "minecraft:netherite_nautilus_armor":
		return 13.0, 3.0, true
	default:
		return 0, 0, false
	}
}

// CanNautilusBeAffectedByEffect2612 implements `AbstractNautilus.canBeAffected`:
// both `minecraft:nautilus` and `minecraft:zombie_nautilus` are immune to `minecraft:poison`.
func CanNautilusBeAffectedByEffect2612(effectID string) bool {
	return canonicalEffectID(effectID) != EffectPoison
}

// SelectZombieNautilusVariantForBiome returns `ZombieNautilusVariantWarm` (1) for
// warm/lukewarm ocean biomes (`#minecraft:spawns_warm_variant_zombie_nautilus`)
// and `ZombieNautilusVariantTemperate` (0) otherwise.
func SelectZombieNautilusVariantForBiome(biomeName string) int32 {
	b := strings.ToLower(strings.TrimSpace(biomeName))
	if strings.Contains(b, "warm_ocean") || strings.Contains(b, "lukewarm_ocean") {
		return ZombieNautilusVariantWarm
	}
	return ZombieNautilusVariantTemperate
}

// EncodeNautilusEntityDataPacket encodes a ClientboundSetEntityDataPacket (0x63)
// for `minecraft:nautilus`:
//   - index 17 (`TamableAnimal.DATA_FLAGS_ID`, BYTE serializer 0: `0x04` = tamed)
//   - index 18 (`TamableAnimal.DATA_OWNERUUID_ID`, OPTIONAL_LIVING_ENTITY_REFERENCE serializer 14)
//   - index 19 (`AbstractNautilus.DASH`, BOOLEAN serializer 8)
func EncodeNautilusEntityDataPacket(entityID int32, tamed bool, ownerUUID *[16]byte, dashing bool) []byte {
	var flags byte
	if tamed {
		flags |= TamableFlagTamed
	}
	w := protocol.NewWriter(36)
	w.VarInt(entityID)
	w.Byte(EntityDataTamableFlagsID)
	w.VarInt(EntityDataSerializerByte)
	w.Byte(flags)

	w.Byte(EntityDataTamableOwnerID)
	w.VarInt(EntityDataSerializerOptionalUUID)
	if ownerUUID != nil {
		w.Bool(true)
		w.UUID(*ownerUUID)
	} else {
		w.Bool(false)
	}

	w.Byte(EntityDataNautilusDashID)
	w.VarInt(EntityDataSerializerBool)
	w.Bool(dashing)

	w.Byte(0xFF)
	return w.Bytes()
}

// DecodeNautilusEntityDataPacket decodes a ClientboundSetEntityDataPacket payload
// produced by EncodeNautilusEntityDataPacket.
func DecodeNautilusEntityDataPacket(r *protocol.Reader) (entityID int32, flags byte, ownerUUID *[16]byte, dashing bool, err error) {
	if r == nil {
		return 0, 0, nil, false, errors.New("nil reader")
	}
	entityID, err = r.VarInt()
	if err != nil {
		return 0, 0, nil, false, err
	}
	for {
		idx, err := r.ReadByte()
		if err != nil {
			return 0, 0, nil, false, err
		}
		if idx == 0xFF {
			return entityID, flags, ownerUUID, dashing, nil
		}
		ser, err := r.VarInt()
		if err != nil {
			return 0, 0, nil, false, err
		}
		switch idx {
		case EntityDataTamableFlagsID:
			if ser != EntityDataSerializerByte {
				return 0, 0, nil, false, errors.New("unexpected tamable flags serializer")
			}
			flags, err = r.ReadByte()
			if err != nil {
				return 0, 0, nil, false, err
			}
		case EntityDataTamableOwnerID:
			if ser != EntityDataSerializerOptionalUUID {
				return 0, 0, nil, false, errors.New("unexpected owner uuid serializer")
			}
			present, err := r.Bool()
			if err != nil {
				return 0, 0, nil, false, err
			}
			if present {
				u, err := r.UUID()
				if err != nil {
					return 0, 0, nil, false, err
				}
				ownerUUID = &u
			}
		case EntityDataNautilusDashID:
			if ser != EntityDataSerializerBool {
				return 0, 0, nil, false, errors.New("unexpected nautilus dash serializer")
			}
			dashing, err = r.Bool()
			if err != nil {
				return 0, 0, nil, false, err
			}
		default:
			return 0, 0, nil, false, errors.New("unexpected entity data index in nautilus packet")
		}
	}
}

// EncodeZombieNautilusEntityDataPacket encodes a ClientboundSetEntityDataPacket (0x63)
// for `minecraft:zombie_nautilus` (indices 17, 18, 19 from `AbstractNautilus`, plus
// index 20 `DATA_VARIANT_ID` with serializer 32 `ZOMBIE_NAUTILUS_VARIANT`).
func EncodeZombieNautilusEntityDataPacket(entityID int32, tamed bool, ownerUUID *[16]byte, dashing bool, variantID int32) []byte {
	if variantID < ZombieNautilusVariantTemperate || variantID > ZombieNautilusVariantWarm {
		variantID = ZombieNautilusVariantTemperate
	}
	var flags byte
	if tamed {
		flags |= TamableFlagTamed
	}
	w := protocol.NewWriter(40)
	w.VarInt(entityID)
	w.Byte(EntityDataTamableFlagsID)
	w.VarInt(EntityDataSerializerByte)
	w.Byte(flags)

	w.Byte(EntityDataTamableOwnerID)
	w.VarInt(EntityDataSerializerOptionalUUID)
	if ownerUUID != nil {
		w.Bool(true)
		w.UUID(*ownerUUID)
	} else {
		w.Bool(false)
	}

	w.Byte(EntityDataNautilusDashID)
	w.VarInt(EntityDataSerializerBool)
	w.Bool(dashing)

	w.Byte(EntityDataZombieNautilusVariantID)
	w.VarInt(EntityDataSerializerZombieNautilusVar)
	w.VarInt(variantID)

	w.Byte(0xFF)
	return w.Bytes()
}

// DecodeZombieNautilusEntityDataPacket decodes a ClientboundSetEntityDataPacket payload
// produced by EncodeZombieNautilusEntityDataPacket.
func DecodeZombieNautilusEntityDataPacket(r *protocol.Reader) (entityID int32, flags byte, ownerUUID *[16]byte, dashing bool, variantID int32, err error) {
	if r == nil {
		return 0, 0, nil, false, 0, errors.New("nil reader")
	}
	entityID, err = r.VarInt()
	if err != nil {
		return 0, 0, nil, false, 0, err
	}
	for {
		idx, err := r.ReadByte()
		if err != nil {
			return 0, 0, nil, false, 0, err
		}
		if idx == 0xFF {
			return entityID, flags, ownerUUID, dashing, variantID, nil
		}
		ser, err := r.VarInt()
		if err != nil {
			return 0, 0, nil, false, 0, err
		}
		switch idx {
		case EntityDataTamableFlagsID:
			if ser != EntityDataSerializerByte {
				return 0, 0, nil, false, 0, errors.New("unexpected tamable flags serializer")
			}
			flags, err = r.ReadByte()
			if err != nil {
				return 0, 0, nil, false, 0, err
			}
		case EntityDataTamableOwnerID:
			if ser != EntityDataSerializerOptionalUUID {
				return 0, 0, nil, false, 0, errors.New("unexpected owner uuid serializer")
			}
			present, err := r.Bool()
			if err != nil {
				return 0, 0, nil, false, 0, err
			}
			if present {
				u, err := r.UUID()
				if err != nil {
					return 0, 0, nil, false, 0, err
				}
				ownerUUID = &u
			}
		case EntityDataNautilusDashID:
			if ser != EntityDataSerializerBool {
				return 0, 0, nil, false, 0, errors.New("unexpected nautilus dash serializer")
			}
			dashing, err = r.Bool()
			if err != nil {
				return 0, 0, nil, false, 0, err
			}
		case EntityDataZombieNautilusVariantID:
			if ser != EntityDataSerializerZombieNautilusVar {
				return 0, 0, nil, false, 0, errors.New("unexpected zombie nautilus variant serializer")
			}
			variantID, err = r.VarInt()
			if err != nil {
				return 0, 0, nil, false, 0, err
			}
		default:
			return 0, 0, nil, false, 0, errors.New("unexpected entity data index in zombie nautilus packet")
		}
	}
}

// SpawnNautilus spawns a `minecraft:nautilus` (entity ID 88, 15.0 HP) at (x, y, z) in dimension.
func (s *Server) SpawnNautilus(dimension string, x, y, z float64, baby bool) (int32, error) {
	if s == nil || s.entities == nil {
		return 0, errors.New("server: nil entity manager")
	}
	normDim := world.NormalizeDimension(dimension)
	typeID := ResolveEntityTypeID(MobTypeNautilus)
	if typeID < 0 {
		typeID = EntityTypeNautilusID
	}
	id := s.entities.Add(&world.Entity{
		TypeID:    typeID,
		TypeName:  MobTypeNautilus,
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
	mem.health = DefaultNautilusMaxHealth
	mem.lastHealth = DefaultNautilusMaxHealth
	tr.mu.Unlock()

	if baby {
		s.SetMobBaby(id, true)
	}

	bt := s.batNautilusTracker()
	bt.mu.Lock()
	bt.nautiluses[id] = &NautilusState{
		EntityID:  id,
		TypeName:  MobTypeNautilus,
		Dimension: normDim,
		Baby:      baby,
		AirSupply: NautilusTotalAirSupplyTick,
	}
	bt.mu.Unlock()

	if ent := s.entities.Get(id); ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, ent))
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, PlaySetEntityDataCB, EncodeNautilusEntityDataPacket(id, false, nil, false))
	}
	return id, nil
}

// SpawnZombieNautilus spawns a `minecraft:zombie_nautilus` (entity ID 152, 15.0 HP) at (x, y, z) in dimension.
// Note: `ZombieNautilus.isBaby()` is always `false` in vanilla 26.1.2.
func (s *Server) SpawnZombieNautilus(dimension string, x, y, z float64, variantID int32) (int32, error) {
	if s == nil || s.entities == nil {
		return 0, errors.New("server: nil entity manager")
	}
	normDim := world.NormalizeDimension(dimension)
	if variantID < ZombieNautilusVariantTemperate || variantID > ZombieNautilusVariantWarm {
		variantID = ZombieNautilusVariantTemperate
	}
	typeID := ResolveEntityTypeID(MobTypeZombieNautilus)
	if typeID < 0 {
		typeID = EntityTypeZombieNautilusID
	}
	id := s.entities.Add(&world.Entity{
		TypeID:    typeID,
		TypeName:  MobTypeZombieNautilus,
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
	mem.health = DefaultZombieNautilusMaxHealth
	mem.lastHealth = DefaultZombieNautilusMaxHealth
	tr.mu.Unlock()

	bt := s.batNautilusTracker()
	bt.mu.Lock()
	bt.nautiluses[id] = &NautilusState{
		EntityID:  id,
		TypeName:  MobTypeZombieNautilus,
		Dimension: normDim,
		Baby:      false,
		VariantID: variantID,
		AirSupply: NautilusTotalAirSupplyTick,
	}
	bt.mu.Unlock()

	if ent := s.entities.Get(id); ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, ent))
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, PlaySetEntityDataCB,
			EncodeZombieNautilusEntityDataPacket(id, false, nil, false, variantID))
	}
	return id, nil
}

// GetNautilusState returns a snapshot of the NautilusState for entityID.
func (s *Server) GetNautilusState(entityID int32) (NautilusState, bool) {
	if s == nil || entityID <= 0 {
		return NautilusState{}, false
	}
	bt := s.batNautilusTracker()
	bt.mu.RLock()
	defer bt.mu.RUnlock()
	st, ok := bt.nautiluses[entityID]
	if !ok || st == nil {
		return NautilusState{}, false
	}
	return *st, true
}

func (s *Server) broadcastNautilusSyncData(entityID int32, st *NautilusState) {
	if s == nil || s.entities == nil || st == nil {
		return
	}
	ent := s.entities.Get(entityID)
	if ent == nil {
		return
	}
	var ownerPtr *[16]byte
	if st.HasOwner {
		u := st.OwnerUUID
		ownerPtr = &u
	}
	cp := ent.ChunkPos()
	dim := ent.EffectiveDimension()
	if canonicalResourceName(ent.TypeName) == MobTypeZombieNautilus {
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
			EncodeZombieNautilusEntityDataPacket(entityID, st.Tamed, ownerPtr, st.Dashing, st.VariantID))
	} else {
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
			EncodeNautilusEntityDataPacket(entityID, st.Tamed, ownerPtr, st.Dashing))
	}
}

func consumeNautilusFoodOrBucketInSurvival(session *PlayerSession, itemID string) {
	if session == nil {
		return
	}
	if IsNautilusBucketFood2612(itemID) {
		snap := session.Snapshot()
		if snap.GameMode != GameModeCreative {
			slotIdx := world.HotbarContainerOffset + int(snap.SelectedSlot)
			session.SetInventorySlot(slotIdx, world.NewNamedItemStack("minecraft:water_bucket", 1))
		}
		return
	}
	consumeOneHeldItemInSurvival(session)
}

// InteractWithNautilus handles right-clicking a `minecraft:nautilus` or `minecraft:zombie_nautilus`:
//   - Wild adult + `#minecraft:nautilus_taming_items` (`pufferfish` / `pufferfish_bucket`):
//     consumes item (replacing bucket food with `minecraft:water_bucket` in survival) and
//     tames with a 1/3 chance (`EntityEventTamingSuccess = 7` or `EntityEventTamingFailed = 6`).
//   - Tamed adult + `minecraft:saddle`: equips saddle.
//   - Tamed adult + `*_nautilus_armor`: equips body armor.
//   - Tamed adult + `minecraft:shears`: shears off body armor (or saddle if unarmored) when not ridden.
//   - Tamed or baby + `#minecraft:nautilus_food`: heals if hurt (`+3.0` HP), or puts adult
//     `minecraft:nautilus` into love mode to breed a baby `minecraft:nautilus` (`ZombieNautilus` cannot breed).
//   - Tamed saddled adult + empty/non-food hand: mounts player and grants `EffectBreathOfTheNautilus` (60 ticks).
func (s *Server) InteractWithNautilus(session *PlayerSession, entityID int32, rng *rand.Rand) (handled bool, tamedNow bool, offspringID int32) {
	if s == nil || s.entities == nil || session == nil || session.IsDead() {
		return false, false, 0
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator || snap.Health <= 0 {
		return false, false, 0
	}
	ent := s.entities.Get(entityID)
	if ent == nil || !IsNautilusMobType(ent.TypeName) {
		return false, false, 0
	}
	dx := ent.X - snap.X
	dy := ent.Y - snap.Y
	dz := ent.Z - snap.Z
	if snap.GameMode != GameModeCreative && (dx*dx+dy*dy+dz*dz) > MaxMeleeAttackReach*MaxMeleeAttackReach {
		return false, false, 0
	}
	if rng == nil {
		rng = rand.New(rand.NewSource(1))
	}

	normType := canonicalResourceName(ent.TypeName)
	dim := ent.EffectiveDimension()
	cp := ent.ChunkPos()
	held := session.HeldItem()
	heldID := ""
	if !held.IsEmpty() {
		heldID = canonicalResourceName(held.ResolvedID())
	}

	bt := s.batNautilusTracker()
	bt.mu.Lock()
	st := bt.nautiluses[entityID]
	if st == nil {
		st = &NautilusState{
			EntityID:  entityID,
			TypeName:  normType,
			Dimension: dim,
			Baby:      s.IsMobBaby(entityID),
			AirSupply: NautilusTotalAirSupplyTick,
		}
		bt.nautiluses[entityID] = st
	}
	isTamed := st.Tamed
	isBaby := st.Baby || s.IsMobBaby(entityID)
	bt.mu.Unlock()

	// 1. Wild nautilus taming with `#minecraft:nautilus_taming_items` (`pufferfish` / `pufferfish_bucket`).
	if !isTamed && !isBaby && IsNautilusTamingItem2612(heldID) {
		consumeNautilusFoodOrBucketInSurvival(session, heldID)
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
			EncodeSoundPacket(SoundNautilusEat, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
		if rng.Intn(NautilusTameSuccessChance) == 0 {
			bt.mu.Lock()
			st.Tamed = true
			st.OwnerUUID = session.Profile.UUID
			st.HasOwner = true
			snapSt := *st
			bt.mu.Unlock()
			s.broadcastNautilusSyncData(entityID, &snapSt)
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayEntityEvent,
				EncodeEntityEventPacket(entityID, EntityEventTamingSucceeded))
			return true, true, 0
		}
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayEntityEvent,
			EncodeEntityEventPacket(entityID, EntityEventTamingFailed))
		return true, false, 0
	}

	// 2. Tamed equipment interactions (saddle, nautilus armor, shears).
	if isTamed && !isBaby {
		if heldID == "minecraft:saddle" {
			bt.mu.Lock()
			alreadySaddled := st.Saddled
			if !alreadySaddled {
				st.Saddled = true
			}
			bt.mu.Unlock()
			if !alreadySaddled {
				consumeOneHeldItemInSurvival(session)
				return true, false, 0
			}
		}
		if _, _, isArmor := IsNautilusArmorItem2612(heldID); isArmor {
			bt.mu.Lock()
			canEquip := st.BodyArmor.IsEmpty()
			if canEquip {
				st.BodyArmor = world.NewNamedItemStack(heldID, 1)
			}
			bt.mu.Unlock()
			if canEquip {
				consumeOneHeldItemInSurvival(session)
				return true, false, 0
			}
		}
		if heldID == "minecraft:shears" {
			bt.mu.Lock()
			var dropped world.ItemStack
			if !st.HasRider {
				if !st.BodyArmor.IsEmpty() {
					dropped = st.BodyArmor
					st.BodyArmor = world.ItemStack{}
				} else if st.Saddled {
					dropped = world.NewNamedItemStack("minecraft:saddle", 1)
					st.Saddled = false
				}
			}
			bt.mu.Unlock()
			if !dropped.IsEmpty() {
				s.SpawnItemEntityInDimension(dim, ent.X, ent.Y+0.5, ent.Z, dropped)
				s.DamageHeldItem(session, 1)
				return true, false, 0
			}
		}
	}

	// 3. Feeding `#minecraft:nautilus_food` to heal or breed (`Nautilus` only for breeding).
	if (isTamed || isBaby) && IsNautilusFoodItem2612(heldID) {
		curHP := s.MobHealth(entityID)
		maxHP := ResolveMobMaxHealth(normType)
		if curHP < maxHP {
			nextHP := curHP + 3.0
			if nextHP > maxHP {
				nextHP = maxHP
			}
			s.SetMobHealth(entityID, nextHP)
			consumeNautilusFoodOrBucketInSurvival(session, heldID)
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
				EncodeSoundPacket(SoundNautilusEat, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
			return true, false, 0
		}
		if normType == MobTypeNautilus && !isBaby {
			bt.mu.Lock()
			canLove := st.BreedCD <= 0 && st.InLoveTicks <= 0
			if canLove {
				st.InLoveTicks = AnimalLoveDurationTicks
			}
			bt.mu.Unlock()
			if canLove {
				consumeNautilusFoodOrBucketInSurvival(session, heldID)
				s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayEntityEvent,
					EncodeEntityEventPacket(entityID, EntityEventLoveHearts))
				childID := s.tryMateNautilus(entityID)
				return true, false, childID
			}
		}
	}

	// 4. Mounting a tamed, saddled adult Nautilus / Zombie Nautilus.
	if isTamed && !isBaby {
		bt.mu.Lock()
		canMount := st.Saddled && !st.HasRider
		if canMount {
			st.RiderUUID = session.Profile.UUID
			st.HasRider = true
			st.RiderTicks = 0
		}
		bt.mu.Unlock()
		if canMount {
			s.ApplyPlayerEffect(session, NewStatusEffectInstance(EffectBreathOfTheNautilus, BreathOfTheNautilusDurationTicks, 0))
			return true, false, 0
		}
	}
	return false, false, 0
}

func (s *Server) tryMateNautilus(entityID int32) int32 {
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeNautilus {
		return 0
	}
	dim := ent.EffectiveDimension()
	bt := s.batNautilusTracker()
	bt.mu.Lock()
	stA := bt.nautiluses[entityID]
	if stA == nil || stA.InLoveTicks <= 0 {
		bt.mu.Unlock()
		return 0
	}
	var mateID int32
	for otherID, stB := range bt.nautiluses {
		if otherID == entityID || stB == nil || stB.TypeName != MobTypeNautilus || stB.Dimension != dim || stB.Baby || stB.InLoveTicks <= 0 {
			continue
		}
		otherEnt := s.entities.Get(otherID)
		if otherEnt == nil {
			continue
		}
		dx := otherEnt.X - ent.X
		dy := otherEnt.Y - ent.Y
		dz := otherEnt.Z - ent.Z
		if dx*dx+dy*dy+dz*dz <= AnimalBreedRange*AnimalBreedRange {
			mateID = otherID
			stA.InLoveTicks = 0
			stA.BreedCD = AnimalBreedCooldownTicks
			stB.InLoveTicks = 0
			stB.BreedCD = AnimalBreedCooldownTicks
			break
		}
	}
	ownerUUID := stA.OwnerUUID
	hasOwner := stA.HasOwner
	tamed := stA.Tamed
	bt.mu.Unlock()

	if mateID == 0 {
		return 0
	}
	childID, err := s.SpawnNautilus(dim, ent.X, ent.Y, ent.Z, true)
	if err != nil || childID <= 0 {
		return 0
	}
	if tamed && hasOwner {
		bt.mu.Lock()
		if cst := bt.nautiluses[childID]; cst != nil {
			cst.Tamed = true
			cst.OwnerUUID = ownerUUID
			cst.HasOwner = true
			snapChild := *cst
			bt.mu.Unlock()
			s.broadcastNautilusSyncData(childID, &snapChild)
		} else {
			bt.mu.Unlock()
		}
	}
	return childID
}

// DismountNautilus dismounts any riding player from Nautilus / Zombie Nautilus entityID.
func (s *Server) DismountNautilus(entityID int32) bool {
	if s == nil || entityID <= 0 {
		return false
	}
	bt := s.batNautilusTracker()
	bt.mu.Lock()
	defer bt.mu.Unlock()
	st := bt.nautiluses[entityID]
	if st == nil || !st.HasRider {
		return false
	}
	st.HasRider = false
	st.RiderUUID = [16]byte{}
	st.RiderTicks = 0
	return true
}

// ExecuteNautilusDash performs `AbstractNautilus.executeRidersJump(jumpPower)` for a
// tamed, saddled Nautilus or Zombie Nautilus when its dash cooldown is 0:
//   - Computes dash momentum: `(jumpPower / 100.0) * (1.2 in water, 0.5 on land)`
//   - Sets `Dashing = true`, `DashCooldown = 40` ticks, `DashDuration = 5` ticks
//   - Updates entity velocity along yaw/pitch and broadcasts `PlaySetEntityDataCB` + `entity.nautilus.dash`.
func (s *Server) ExecuteNautilusDash(entityID int32, jumpPower int, inWater bool) (vx, vy, vz float64, ok bool) {
	if s == nil || s.entities == nil || entityID <= 0 || jumpPower <= 0 {
		return 0, 0, 0, false
	}
	if jumpPower > 100 {
		jumpPower = 100
	}
	ent := s.entities.Get(entityID)
	if ent == nil || !IsNautilusMobType(ent.TypeName) {
		return 0, 0, 0, false
	}

	bt := s.batNautilusTracker()
	bt.mu.Lock()
	st := bt.nautiluses[entityID]
	if st == nil || !st.Tamed || !st.Saddled || st.DashCooldown > 0 {
		bt.mu.Unlock()
		return 0, 0, 0, false
	}
	st.Dashing = true
	st.DashCooldown = NautilusDashCooldownTicks
	st.DashDuration = NautilusDashMinDuration
	snapSt := *st
	bt.mu.Unlock()

	mult := NautilusDashLandMomentum
	if inWater {
		mult = NautilusDashWaterMomentum
	}
	speed := (float64(jumpPower) / 100.0) * mult
	yawRad := float64(ent.Yaw) * (math.Pi / 180.0)
	pitchRad := float64(ent.Pitch) * (math.Pi / 180.0)
	vx = -math.Sin(yawRad) * math.Cos(pitchRad) * speed
	vy = -math.Sin(pitchRad) * speed
	vz = math.Cos(yawRad) * math.Cos(pitchRad) * speed

	s.entities.Update(entityID, func(e *world.Entity) {
		e.X += vx
		e.Y += vy
		e.Z += vz
		e.VelocityX = clampVelocityShort(vx)
		e.VelocityY = clampVelocityShort(vy)
		e.VelocityZ = clampVelocityShort(vz)
	})

	s.broadcastNautilusSyncData(entityID, &snapSt)
	cp := ent.ChunkPos()
	dim := ent.EffectiveDimension()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundNautilusDash, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
	return vx, vy, vz, true
}

// TickNautiluses ticks all `minecraft:nautilus` and `minecraft:zombie_nautilus` entities:
//   - Ticks `DashCooldown` and `DashDuration` (resetting `DASH = false` after 5 ticks).
//   - Refreshes `EffectBreathOfTheNautilus` (`60` ticks) every `40` ticks on any riding player.
//   - Ticks `Nautilus` 300-tick air supply (`Nautilus.handleAirSupply`), dealing 2.0 dry-out
//     damage every 20 ticks when out of water and rain for >300 ticks.
//   - Checks daylight sunburn for `ZombieNautilus` when out of water and not wearing body armor.
func (s *Server) TickNautiluses(rng *rand.Rand) {
	if s == nil || s.entities == nil {
		return
	}
	all := s.entities.AppendAll(nil)
	var nautEnts []world.Entity
	for i := range all {
		if IsNautilusMobType(all[i].TypeName) {
			nautEnts = append(nautEnts, all[i])
		}
	}
	if len(nautEnts) == 0 {
		return
	}

	bt := s.batNautilusTracker()
	for i := range nautEnts {
		ent := &nautEnts[i]
		normType := canonicalResourceName(ent.TypeName)
		dim := ent.EffectiveDimension()
		cache := s.DimensionChunks(dim)
		bx, by, bz := int(math.Floor(ent.X)), int(math.Floor(ent.Y)), int(math.Floor(ent.Z))
		inWater := false
		if cache != nil && by >= world.MinY && by < world.MinY+world.WorldHeight {
			if cache.FluidHeightAt(bx, by, bz) > 0 || cache.GetBlock(bx, by, bz) == world.StateWater {
				inWater = true
			}
		}

		var syncNeeded bool
		var snapSt NautilusState
		var dryDamage bool
		var riderUUID [16]byte
		var refreshRiderEffect bool

		bt.mu.Lock()
		st := bt.nautiluses[ent.ID]
		if st == nil {
			st = &NautilusState{
				EntityID:  ent.ID,
				TypeName:  normType,
				Dimension: dim,
				AirSupply: NautilusTotalAirSupplyTick,
			}
			bt.nautiluses[ent.ID] = st
		}
		if st.DashCooldown > 0 {
			st.DashCooldown--
		}
		if st.DashDuration > 0 {
			st.DashDuration--
			if st.DashDuration == 0 && st.Dashing {
				st.Dashing = false
				syncNeeded = true
			}
		}
		if st.BreedCD > 0 {
			st.BreedCD--
		}
		if st.InLoveTicks > 0 {
			st.InLoveTicks--
		}
		if st.HasRider {
			st.RiderTicks++
			if st.RiderTicks%BreathOfTheNautilusRefreshTicks == 0 {
				riderUUID = st.RiderUUID
				refreshRiderEffect = true
			}
		}
		if normType == MobTypeNautilus {
			if inWater || s.IsRaining() {
				st.AirSupply = NautilusTotalAirSupplyTick
			} else {
				st.AirSupply--
				if st.AirSupply <= -20 {
					st.AirSupply = 0
					dryDamage = true
				}
			}
		}
		if syncNeeded {
			snapSt = *st
		}
		bt.mu.Unlock()

		if syncNeeded {
			s.broadcastNautilusSyncData(ent.ID, &snapSt)
		}
		if refreshRiderEffect {
			s.playersMu.RLock()
			var rider *PlayerSession
			for _, p := range s.players {
				if p != nil && p.Profile.UUID == riderUUID && !p.IsDead() {
					rider = p
					break
				}
			}
			s.playersMu.RUnlock()
			if rider != nil {
				s.ApplyPlayerEffect(rider, NewStatusEffectInstance(EffectBreathOfTheNautilus, BreathOfTheNautilusDurationTicks, 0))
			}
		}
		if dryDamage {
			s.applyDamageToMob(ent, NautilusDryOutDamage, nil, 0, false, false)
		}
	}
}

// RollNautilusDeathLoot2612 rolls the vanilla 26.1.2 death loot for `minecraft:nautilus`
// (`data/minecraft/loot_table/entities/nautilus.json`):
//   - Drops `1` `minecraft:nautilus_shell` when `killedByPlayer == true` with a `5%` base
//     chance + `1%` per Looting level (`random_chance_with_enchanted_bonus`: `0.05 + 0.01 * looting`).
func RollNautilusDeathLoot2612(killedByPlayer bool, lootingLevel int, rng *rand.Rand) []world.ItemStack {
	if !killedByPlayer {
		return nil
	}
	if lootingLevel < 0 {
		lootingLevel = 0
	}
	chance := float32(0.05) + float32(lootingLevel)*0.01
	if rng == nil {
		rng = rand.New(rand.NewSource(1))
	}
	if rng.Float32() < chance {
		if stack := world.NewNamedItemStack("minecraft:nautilus_shell", 1); !stack.IsEmpty() {
			return []world.ItemStack{stack}
		}
	}
	return nil
}

// RollZombieNautilusDeathLoot2612 rolls the vanilla 26.1.2 death loot for `minecraft:zombie_nautilus`
// (`data/minecraft/loot_table/entities/zombie_nautilus.json`):
//   - Drops `0..3` `minecraft:rotten_flesh` (+ `0..lootingLevel` bonus) when `killedByPlayer == true`.
func RollZombieNautilusDeathLoot2612(killedByPlayer bool, lootingLevel int, rng *rand.Rand) []world.ItemStack {
	if !killedByPlayer {
		return nil
	}
	if lootingLevel < 0 {
		lootingLevel = 0
	}
	if rng == nil {
		rng = rand.New(rand.NewSource(1))
	}
	count := int32(rng.Intn(4)) // 0..3
	if lootingLevel > 0 {
		count += int32(rng.Intn(lootingLevel + 1))
	}
	if count <= 0 {
		return nil
	}
	if stack := world.NewNamedItemStack("minecraft:rotten_flesh", count); !stack.IsEmpty() {
		return []world.ItemStack{stack}
	}
	return nil
}

// --- 4. Snow Golem (`minecraft:snow_golem`) 26.1.2 Polish ---

// EncodeSnowGolemDataPacket encodes a ClientboundSetEntityDataPacket (0x63)
// setting `SnowGolem.DATA_PUMPKIN_ID` (index 16, BYTE serializer 0: `0x10` = has pumpkin).
func EncodeSnowGolemDataPacket(entityID int32, hasPumpkin bool) []byte {
	var flags byte
	if hasPumpkin {
		flags |= SnowGolemPumpkinFlag
	}
	w := protocol.NewWriter(8)
	w.VarInt(entityID)
	w.Byte(EntityDataSnowGolemPumpkinID)
	w.VarInt(EntityDataSerializerByte)
	w.Byte(flags)
	w.Byte(0xFF)
	return w.Bytes()
}

// DecodeSnowGolemDataPacket decodes a ClientboundSetEntityDataPacket payload
// produced by EncodeSnowGolemDataPacket.
func DecodeSnowGolemDataPacket(r *protocol.Reader) (entityID int32, hasPumpkin bool, err error) {
	if r == nil {
		return 0, false, errors.New("nil reader")
	}
	entityID, err = r.VarInt()
	if err != nil {
		return 0, false, err
	}
	idx, err := r.ReadByte()
	if err != nil {
		return 0, false, err
	}
	if idx != EntityDataSnowGolemPumpkinID {
		return 0, false, errors.New("unexpected snow golem data accessor index")
	}
	ser, err := r.VarInt()
	if err != nil {
		return 0, false, err
	}
	if ser != EntityDataSerializerByte {
		return 0, false, errors.New("unexpected snow golem data serializer")
	}
	flags, err := r.ReadByte()
	if err != nil {
		return 0, false, err
	}
	term, err := r.ReadByte()
	if err != nil {
		return 0, false, err
	}
	if term != 0xFF {
		return 0, false, errors.New("missing 0xFF entity data terminator")
	}
	return entityID, (flags & SnowGolemPumpkinFlag) != 0, nil
}

func (s *Server) ensureSnowGolemState(entityID int32, dim string) *SnowGolemRuntimeState {
	bt := s.batNautilusTracker()
	bt.mu.Lock()
	defer bt.mu.Unlock()
	st := bt.snowGolems[entityID]
	if st == nil {
		st = &SnowGolemRuntimeState{
			EntityID:   entityID,
			Dimension:  world.NormalizeDimension(dim),
			HasPumpkin: true,
		}
		bt.snowGolems[entityID] = st
	}
	return st
}

// HasSnowGolemPumpkin reports whether the `minecraft:snow_golem` with entityID
// still wears its carved pumpkin head (`DEFAULT_PUMPKIN = true`).
func (s *Server) HasSnowGolemPumpkin(entityID int32) bool {
	if s == nil || s.entities == nil || entityID <= 0 {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeSnowGolem {
		return false
	}
	st := s.ensureSnowGolemState(entityID, ent.EffectiveDimension())
	bt := s.batNautilusTracker()
	bt.mu.RLock()
	defer bt.mu.RUnlock()
	return st.HasPumpkin
}

// SetSnowGolemPumpkin sets whether the Snow Golem wears its carved pumpkin and
// broadcasts `EncodeSnowGolemDataPacket`.
func (s *Server) SetSnowGolemPumpkin(entityID int32, hasPumpkin bool) bool {
	if s == nil || s.entities == nil || entityID <= 0 {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeSnowGolem {
		return false
	}
	st := s.ensureSnowGolemState(entityID, ent.EffectiveDimension())
	bt := s.batNautilusTracker()
	bt.mu.Lock()
	st.HasPumpkin = hasPumpkin
	bt.mu.Unlock()

	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, PlaySetEntityDataCB,
		EncodeSnowGolemDataPacket(entityID, hasPumpkin))
	return true
}

// InteractWithSnowGolem handles shearing a `minecraft:snow_golem` with `minecraft:shears`
// (`SnowGolem.mobInteract` & `SnowGolem.shear` in 26.1.2.jar):
//   - Requires `HasSnowGolemPumpkin(entityID) == true` and held `minecraft:shears`
//   - Removes pumpkin (`SetSnowGolemPumpkin(entityID, false)`)
//   - Plays `minecraft:entity.snow_golem.shear`
//   - Drops 1 `minecraft:carved_pumpkin` item entity (`loot_table/shearing/snow_golem.json`)
//   - Damages held shears by 1 in survival.
func (s *Server) InteractWithSnowGolem(session *PlayerSession, entityID int32) (sheared bool, dropped world.ItemStack) {
	if s == nil || s.entities == nil || session == nil || session.IsDead() {
		return false, world.ItemStack{}
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator || snap.Health <= 0 {
		return false, world.ItemStack{}
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeSnowGolem {
		return false, world.ItemStack{}
	}
	dx := ent.X - snap.X
	dy := ent.Y - snap.Y
	dz := ent.Z - snap.Z
	if snap.GameMode != GameModeCreative && (dx*dx+dy*dy+dz*dz) > MaxMeleeAttackReach*MaxMeleeAttackReach {
		return false, world.ItemStack{}
	}
	held := session.HeldItem()
	if held.IsEmpty() || canonicalResourceName(held.ResolvedID()) != "minecraft:shears" {
		return false, world.ItemStack{}
	}
	if !s.HasSnowGolemPumpkin(entityID) {
		return false, world.ItemStack{}
	}
	s.SetSnowGolemPumpkin(entityID, false)
	dropped = world.NewNamedItemStack("minecraft:carved_pumpkin", 1)
	if !dropped.IsEmpty() {
		s.SpawnItemEntityInDimension(ent.EffectiveDimension(), ent.X, ent.Y+1.2, ent.Z, dropped)
	}
	s.DamageHeldItem(session, 1)
	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundSnowGolemShear, SoundSourcePlayers, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
	return true, dropped
}

// ShouldSnowGolemMeltAt reports whether a Snow Golem at (dimension, biomeTemp, inWaterOrRain)
// takes melting heat damage (`EnvironmentAttributes.SNOW_GOLEM_MELTS`: Nether or `biomeTemp > 1.0`)
// or water-sensitivity damage (`isSensitiveToWater() == true`).
func ShouldSnowGolemMeltAt(dimension string, biomeTemp float32, inWaterOrRain bool) (meltFromHeat bool, hurtFromWater bool) {
	normDim := world.NormalizeDimension(dimension)
	if normDim == world.DimensionNether || biomeTemp > 1.0 {
		meltFromHeat = true
	}
	if inWaterOrRain {
		hurtFromWater = true
	}
	return meltFromHeat, hurtFromWater
}

// SnowballDamageAgainstMob returns the vanilla 26.1.2 damage dealt by a thrown
// snowball to targetMobType (`3.0` to `minecraft:blaze`, `0.0` to all other mobs).
func SnowballDamageAgainstMob(targetMobType string) float32 {
	if canonicalResourceName(targetMobType) == MobTypeBlaze {
		return SnowballBlazeDamage
	}
	return 0.0
}

// RollSnowGolemDeathLoot2612 rolls the vanilla 26.1.2 death loot for `minecraft:snow_golem`
// (`data/minecraft/loot_table/entities/snow_golem.json`: `0..15` `minecraft:snowball`).
func RollSnowGolemDeathLoot2612(rng *rand.Rand) []world.ItemStack {
	if rng == nil {
		rng = rand.New(rand.NewSource(1))
	}
	count := int32(rng.Intn(16)) // 0..15
	if count <= 0 {
		return nil
	}
	if stack := world.NewNamedItemStack("minecraft:snowball", count); !stack.IsEmpty() {
		return []world.ItemStack{stack}
	}
	return nil
}

// TickSnowGolems steps all `minecraft:snow_golem` entities:
//   - Applies 1.0 damage when in water/rain or in a warm dimension/biome (`Nether`).
//   - Places `minecraft:snow` layers at the golem's 4 footprint corners on solid ground.
//   - Fires snowballs every 20 ticks at the nearest targetable hostile mob within 10 blocks
//     (dealing 3.0 damage to `minecraft:blaze` and 0.0 damage to other mobs).
func (s *Server) TickSnowGolems(rng *rand.Rand) {
	if s == nil || s.entities == nil {
		return
	}
	all := s.entities.AppendAll(nil)
	var snowGolems []world.Entity
	var hostiles []world.Entity
	for i := range all {
		tName := canonicalResourceName(all[i].TypeName)
		if tName == MobTypeSnowGolem {
			snowGolems = append(snowGolems, all[i])
		} else if IsIronGolemTargetableHostile(tName) || tName == MobTypeCreeper {
			hostiles = append(hostiles, all[i])
		}
	}
	if len(snowGolems) == 0 {
		return
	}

	snowLayerState, hasSnowLayer := world.BlockStateIDByProperties("minecraft:snow", map[string]string{"layers": "1"})
	if !hasSnowLayer {
		snowLayerState, hasSnowLayer = world.BlockStateIDByProperties("minecraft:snow", nil)
	}

	bt := s.batNautilusTracker()
	for i := range snowGolems {
		golem := &snowGolems[i]
		dim := golem.EffectiveDimension()
		cache := s.DimensionChunks(dim)
		bx, by, bz := int(math.Floor(golem.X)), int(math.Floor(golem.Y)), int(math.Floor(golem.Z))

		inWater := false
		if cache != nil && by >= world.MinY && by < world.MinY+world.WorldHeight {
			if cache.FluidHeightAt(bx, by, bz) > 0 || cache.GetBlock(bx, by, bz) == world.StateWater {
				inWater = true
			}
		}
		meltHeat, hurtWater := ShouldSnowGolemMeltAt(dim, 0.5, inWater)
		if meltHeat || hurtWater {
			if s.applyDamageToMob(golem, SnowGolemMeltDamage, nil, 0, false, false) {
				continue
			}
		}

		// Place snow trail at the 4 footprint corners (`SnowGolem.aiStep` lines 85..221).
		if !meltHeat && hasSnowLayer && cache != nil && s.CreeperBlockGriefing() {
			for corner := 0; corner < 4; corner++ {
				cx := int(math.Floor(golem.X + float64((corner%2)*2-1)*0.25))
				cy := int(math.Floor(golem.Y))
				cz := int(math.Floor(golem.Z + float64(((corner/2)%2)*2-1)*0.25))
				if cy <= world.MinY || cy >= world.MinY+world.WorldHeight {
					continue
				}
				if cache.GetBlock(cx, cy, cz) == world.StateAir {
					below := cache.GetBlock(cx, cy-1, cz)
					if !isPassableBlock(below) && below != world.StateWater {
						cache.SetBlock(cx, cy, cz, snowLayerState)
					}
				}
			}
		}

		st := s.ensureSnowGolemState(golem.ID, dim)
		bt.mu.Lock()
		if st.ShootCooldown > 0 {
			st.ShootCooldown--
		}
		cd := st.ShootCooldown
		bt.mu.Unlock()

		if cd > 0 {
			continue
		}

		// Find nearest Enemy within 10 blocks (`RangedAttackGoal(this, 1.25, 20, 10.0F)`).
		bestDistSq := SnowGolemShootRange * SnowGolemShootRange
		var target *world.Entity
		for j := range hostiles {
			h := &hostiles[j]
			if h.EffectiveDimension() != dim {
				continue
			}
			dx := h.X - golem.X
			dy := h.Y - golem.Y
			dz := h.Z - golem.Z
			dSq := dx*dx + dy*dy + dz*dz
			if dSq <= bestDistSq {
				bestDistSq = dSq
				target = h
			}
		}
		if target != nil {
			bt.mu.Lock()
			st.ShootCooldown = SnowGolemShootCooldownTicks
			bt.mu.Unlock()

			cp := golem.ChunkPos()
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
				EncodeSoundPacket(SoundSnowGolemShoot, SoundSourceNeutral, golem.X, golem.Y, golem.Z, 1.0, 1.0, 0))
			dmg := SnowballDamageAgainstMob(target.TypeName)
			if dmg > 0 {
				s.applyDamageToMob(target, dmg, nil, 0.2, false, false)
			}
		}
	}
}

// --- 5. Copper Golem (`minecraft:copper_golem`) 26.1.2 SynchedEntityData, Waxing, Scraping & Lightning ---

// EncodeCopperGolemEntityDataPacket2612 encodes a ClientboundSetEntityDataPacket (0x63)
// for `minecraft:copper_golem`:
//   - index 16 (`DATA_WEATHER_STATE`, serializer 38 `WEATHERING_COPPER_STATE`: `0..3`)
//   - index 17 (`COPPER_GOLEM_STATE`, serializer 37 `COPPER_GOLEM_STATE`: `0..4`)
func EncodeCopperGolemEntityDataPacket2612(entityID int32, weatherState world.CopperOxidationStage, golemState int32) []byte {
	if weatherState < world.CopperStageUnaffected || weatherState > world.CopperStageOxidized {
		weatherState = world.CopperStageUnaffected
	}
	if golemState < CopperGolemBehaviorStateIdle || golemState > CopperGolemBehaviorStateDroppingNoItem {
		golemState = CopperGolemBehaviorStateIdle
	}
	w := protocol.NewWriter(16)
	w.VarInt(entityID)
	w.Byte(EntityDataCopperGolemWeatherStateID)
	w.VarInt(EntityDataSerializerWeatheringCopperState)
	w.VarInt(int32(weatherState))

	w.Byte(EntityDataCopperGolemStateID)
	w.VarInt(EntityDataSerializerCopperGolemState)
	w.VarInt(golemState)

	w.Byte(0xFF)
	return w.Bytes()
}

// DecodeCopperGolemEntityDataPacket2612 decodes a ClientboundSetEntityDataPacket payload
// produced by EncodeCopperGolemEntityDataPacket2612.
func DecodeCopperGolemEntityDataPacket2612(r *protocol.Reader) (entityID int32, weatherState world.CopperOxidationStage, golemState int32, err error) {
	if r == nil {
		return 0, 0, 0, errors.New("nil reader")
	}
	entityID, err = r.VarInt()
	if err != nil {
		return 0, 0, 0, err
	}
	for {
		idx, err := r.ReadByte()
		if err != nil {
			return 0, 0, 0, err
		}
		if idx == 0xFF {
			return entityID, weatherState, golemState, nil
		}
		ser, err := r.VarInt()
		if err != nil {
			return 0, 0, 0, err
		}
		switch idx {
		case EntityDataCopperGolemWeatherStateID:
			if ser != EntityDataSerializerWeatheringCopperState {
				return 0, 0, 0, errors.New("unexpected copper golem weather state serializer")
			}
			v, err := r.VarInt()
			if err != nil {
				return 0, 0, 0, err
			}
			weatherState = world.CopperOxidationStage(v)
		case EntityDataCopperGolemStateID:
			if ser != EntityDataSerializerCopperGolemState {
				return 0, 0, 0, errors.New("unexpected copper golem state serializer")
			}
			golemState, err = r.VarInt()
			if err != nil {
				return 0, 0, 0, err
			}
		default:
			return 0, 0, 0, errors.New("unexpected entity data index in copper golem packet")
		}
	}
}

func (s *Server) ensureCopperGolemExtra(entityID int32) *CopperGolemExtraState {
	bt := s.batNautilusTracker()
	bt.mu.Lock()
	defer bt.mu.Unlock()
	ex := bt.copperExtra[entityID]
	if ex == nil {
		ex = &CopperGolemExtraState{BehaviorState: CopperGolemBehaviorStateIdle}
		bt.copperExtra[entityID] = ex
	}
	return ex
}

// SetCopperGolemBehaviorState sets the 26.1.2 `CopperGolemState` (`0=IDLE`, `1=GETTING_ITEM`,
// `2=GETTING_NO_ITEM`, `3=DROPPING_ITEM`, `4=DROPPING_NO_ITEM`) and broadcasts `PlaySetEntityDataCB`.
func (s *Server) SetCopperGolemBehaviorState(entityID int32, behaviorState int32) bool {
	if s == nil || s.entities == nil || entityID <= 0 {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeCopperGolem {
		return false
	}
	st, ok := s.GetCopperGolemState(entityID)
	if !ok {
		return false
	}
	ex := s.ensureCopperGolemExtra(entityID)
	bt := s.batNautilusTracker()
	bt.mu.Lock()
	ex.BehaviorState = behaviorState
	bt.mu.Unlock()

	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, PlaySetEntityDataCB,
		EncodeCopperGolemEntityDataPacket2612(entityID, st.OxidationStage, behaviorState))
	return true
}

// SetCopperGolemAntennaItem sets or clears the shearable antenna item (`EQUIPMENT_SLOT_ANTENNA`,
// e.g. `minecraft:poppy` placed by an Iron Golem) on a Copper Golem.
func (s *Server) SetCopperGolemAntennaItem(entityID int32, stack world.ItemStack) bool {
	if s == nil || s.entities == nil || entityID <= 0 {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeCopperGolem {
		return false
	}
	ex := s.ensureCopperGolemExtra(entityID)
	bt := s.batNautilusTracker()
	bt.mu.Lock()
	ex.AntennaItem = stack
	bt.mu.Unlock()
	return true
}

// SetCopperGolemMainHandItem sets or clears the item carried in the Copper Golem's main hand.
func (s *Server) SetCopperGolemMainHandItem(entityID int32, stack world.ItemStack) bool {
	if s == nil || s.entities == nil || entityID <= 0 {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeCopperGolem {
		return false
	}
	ex := s.ensureCopperGolemExtra(entityID)
	bt := s.batNautilusTracker()
	bt.mu.Lock()
	ex.MainHandItem = stack
	bt.mu.Unlock()
	return true
}

func isAxeItemID(itemName string) bool {
	switch canonicalResourceName(itemName) {
	case "minecraft:wooden_axe",
		"minecraft:stone_axe",
		"minecraft:copper_axe",
		"minecraft:iron_axe",
		"minecraft:golden_axe",
		"minecraft:diamond_axe",
		"minecraft:netherite_axe":
		return true
	default:
		return strings.HasSuffix(canonicalResourceName(itemName), "_axe")
	}
}

// InteractWithCopperGolem2612 implements `CopperGolem.mobInteract` from 26.1.2.jar:
//  1. Empty hand when Copper Golem is holding an item -> throws held item to player (`"drop_held_item"`).
//  2. `minecraft:shears` when Copper Golem has an antenna item (`#minecraft:shearable_from_copper_golem`)
//     -> shears antenna item, plays `entity.copper_golem.shear`, damages shears by 1 (`"shear_antenna"`).
//  3. `minecraft:honeycomb` when not waxed -> sets `Waxed = true` (`nextWeatheringTick = -2L`),
//     consumes 1 honeycomb in survival, plays `item.honeycomb.wax_on` (`"wax_on"`).
//  4. Axe (`#minecraft:axes`) when waxed -> sets `Waxed = false` (`nextWeatheringTick = -1L`),
//     plays `item.axe.wax_off`, damages axe by 1 (`"wax_off"`).
//  5. Axe (`#minecraft:axes`) when not waxed and `OxidationStage > CopperStageUnaffected` ->
//     decrements `OxidationStage` by 1 stage, resets `OxidationTicks = 0`, broadcasts
//     `EncodeCopperGolemEntityDataPacket2612`, plays `item.axe.scrape`, damages axe by 1 (`"scrape"`).
func (s *Server) InteractWithCopperGolem2612(session *PlayerSession, entityID int32) (action string, handled bool) {
	if s == nil || s.entities == nil || session == nil || session.IsDead() {
		return "", false
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator || snap.Health <= 0 {
		return "", false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeCopperGolem {
		return "", false
	}
	dx := ent.X - snap.X
	dy := ent.Y - snap.Y
	dz := ent.Z - snap.Z
	if snap.GameMode != GameModeCreative && (dx*dx+dy*dy+dz*dz) > MaxMeleeAttackReach*MaxMeleeAttackReach {
		return "", false
	}

	dim := ent.EffectiveDimension()
	cp := ent.ChunkPos()
	held := session.HeldItem()
	ex := s.ensureCopperGolemExtra(entityID)
	bt := s.batNautilusTracker()

	// 1. Empty hand -> drop Copper Golem's held item if non-empty.
	if held.IsEmpty() {
		bt.mu.Lock()
		carried := ex.MainHandItem
		if !carried.IsEmpty() {
			ex.MainHandItem = world.ItemStack{}
		}
		bt.mu.Unlock()
		if !carried.IsEmpty() {
			s.SpawnItemEntityInDimension(dim, ent.X, ent.Y+0.5, ent.Z, carried)
			return "drop_held_item", true
		}
		return "", false
	}

	heldID := canonicalResourceName(held.ResolvedID())

	// 2. Shears -> shear antenna item (`EQUIPMENT_SLOT_ANTENNA`).
	if heldID == "minecraft:shears" {
		bt.mu.Lock()
		ant := ex.AntennaItem
		if !ant.IsEmpty() {
			ex.AntennaItem = world.ItemStack{}
		}
		bt.mu.Unlock()
		if !ant.IsEmpty() {
			s.SpawnItemEntityInDimension(dim, ent.X, ent.Y+1.5, ent.Z, ant)
			s.DamageHeldItem(session, 1)
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
				EncodeSoundPacket(SoundCopperGolemShear, SoundSourcePlayers, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
			return "shear_antenna", true
		}
	}

	mgr := s.copperGolemMgr()
	mgr.mu.Lock()
	st := mgr.golems[entityID]
	if st == nil {
		st = &CopperGolemState{
			EntityID:       entityID,
			Dimension:      dim,
			OxidationStage: world.CopperStageUnaffected,
			Pose:           "standing",
			Facing:         "north",
		}
		mgr.golems[entityID] = st
	}

	// 3. Honeycomb -> wax an unwaxed Copper Golem (`nextWeatheringTick = -2L`).
	if heldID == "minecraft:honeycomb" && !st.Waxed {
		st.Waxed = true
		mgr.mu.Unlock()
		consumeOneHeldItemInSurvival(session)
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
			EncodeSoundPacket(SoundHoneycombWaxOn, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
		return "wax_on", true
	}

	// 4. Axe -> remove wax if waxed (`levelEvent 3004`).
	if isAxeItemID(heldID) && st.Waxed {
		st.Waxed = false
		st.OxidationTicks = 0
		mgr.mu.Unlock()
		s.DamageHeldItem(session, 1)
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
			EncodeSoundPacket(SoundAxeWaxOff, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
		return "wax_off", true
	}

	// 5. Axe -> scrape 1 oxidation stage if unwaxed and oxidized (`levelEvent 3005`).
	if isAxeItemID(heldID) && !st.Waxed && st.OxidationStage > world.CopperStageUnaffected {
		st.OxidationStage--
		st.OxidationTicks = 0
		newStage := st.OxidationStage
		mgr.mu.Unlock()

		bt.mu.RLock()
		beh := ex.BehaviorState
		bt.mu.RUnlock()

		s.DamageHeldItem(session, 1)
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
			EncodeCopperGolemEntityDataPacket2612(entityID, newStage, beh))
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
			EncodeSoundPacket(SoundAxeScrape, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
		return "scrape", true
	}
	mgr.mu.Unlock()
	return "", false
}

// OnCopperGolemStruckByLightning implements `CopperGolem.thunderHit` from 26.1.2.jar:
// when a Copper Golem is struck by a lightning bolt, if its `OxidationStage` is greater
// than `CopperStageUnaffected`, it steps back one oxidation stage (`weatherState.previous()`)
// and resets its weathering timer.
func (s *Server) OnCopperGolemStruckByLightning(entityID int32) (newStage world.CopperOxidationStage, deoxidized bool) {
	if s == nil || s.entities == nil || entityID <= 0 {
		return world.CopperStageUnaffected, false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeCopperGolem {
		return world.CopperStageUnaffected, false
	}
	mgr := s.copperGolemMgr()
	mgr.mu.Lock()
	st := mgr.golems[entityID]
	if st == nil || st.OxidationStage <= world.CopperStageUnaffected {
		var cur world.CopperOxidationStage
		if st != nil {
			cur = st.OxidationStage
		}
		mgr.mu.Unlock()
		return cur, false
	}
	st.OxidationStage--
	st.OxidationTicks = 0
	newStage = st.OxidationStage
	mgr.mu.Unlock()

	ex := s.ensureCopperGolemExtra(entityID)
	bt := s.batNautilusTracker()
	bt.mu.RLock()
	beh := ex.BehaviorState
	bt.mu.RUnlock()

	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, PlaySetEntityDataCB,
		EncodeCopperGolemEntityDataPacket2612(entityID, newStage, beh))
	return newStage, true
}
