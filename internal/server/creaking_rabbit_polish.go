package server

import (
	"errors"
	"math"
	"math/rand"
	"strconv"
	"strings"
	"sync"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

// Creaking, Creaking Heart, and Rabbit constants for Minecraft 26.1.2 (protocol 775).
const (
	MobTypeCreaking = "minecraft:creaking"
	MobTypeRabbit   = "minecraft:rabbit"

	// BuiltInRegistries.ENTITY_TYPE fallback numeric IDs in 26.1.2 (protocol 775).
	EntityTypeCreakingID = 29
	EntityTypeRabbitID   = 106

	// Block & item identifiers for Pale Garden / Creaking / Rabbit.
	CreakingHeartBlockID       = "minecraft:creaking_heart"
	PaleOakLogBlockID          = "minecraft:pale_oak_log"
	StrippedPaleOakLogBlockID  = "minecraft:stripped_pale_oak_log"
	PaleOakWoodBlockID         = "minecraft:pale_oak_wood"
	StrippedPaleOakWoodBlockID = "minecraft:stripped_pale_oak_wood"
	ResinClumpItemID           = "minecraft:resin_clump"

	RabbitHideItemID   = "minecraft:rabbit_hide"
	RawRabbitItemID    = "minecraft:rabbit"
	CookedRabbitItemID = "minecraft:cooked_rabbit"
	RabbitFootItemID   = "minecraft:rabbit_foot"

	// Creaking stats & behavior constants (Creaking.java & CreakingHeartBlockEntity.java in 26.1.2).
	DefaultCreakingMaxHealth     float32 = 1.0  // 0.5 hearts (standalone); heart-bound Creakings are invulnerable
	CreakingAttackDamage         float32 = 3.0  // 3.0 HP melee damage on Normal
	CreakingAttackReach          float64 = 2.0  // Melee attack reach
	CreakingAttackCooldownTicks  int     = 20   // 1.0s between melee swings
	CreakingChaseSpeed           float64 = 0.24 // Stalking speed when not observed
	CreakingFollowRange          float64 = 32.0 // Player detection & gaze check radius
	CreakingMaxHeartDist         float64 = 34.0 // Max distance from linked creaking_heart before despawn
	CreakingHeartSpawnHorizRange int     = 16   // +/- 16 horizontal blocks around creaking_heart
	CreakingHeartSpawnVertRange  int     = 8    // +/- 8 vertical blocks around creaking_heart
	CreakingResinCooldownTicks   int     = 100  // 5 seconds (100 ticks) cooldown between resin clump bursts
	CreakingDeathTearingTicks    int     = 20   // 1 second tearing-down death animation when heart is broken
	CreakingGazeCosThreshold     float64 = 0.50 // ~60-degree view cone threshold for freezing Creaking

	// Creaking Heart block state values (`creaking_heart_state` property in 26.1.2).
	CreakingHeartStateUprooted = "uprooted"
	CreakingHeartStateDormant  = "dormant"
	CreakingHeartStateAwake    = "awake"

	// SynchedEntityData indices for Creaking in protocol 775 (26.1.2):
	// Creaking extends Monster -> PathfinderMob -> Mob (15) -> LivingEntity (8..14) -> Entity (0..7).
	EntityDataCreakingCanMoveID       byte  = 16 // Creaking.DATA_CAN_MOVE (BOOLEAN serializer = 8)
	EntityDataCreakingIsActiveID      byte  = 17 // Creaking.DATA_IS_ACTIVE (BOOLEAN serializer = 8)
	EntityDataCreakingIsTearingDownID byte  = 18 // Creaking.DATA_IS_TEARING_DOWN (BOOLEAN serializer = 8)
	EntityDataCreakingHomePosID       byte  = 19 // Creaking.DATA_HOME_POS (OPTIONAL_BLOCK_POS serializer = 11)
	EntityDataSerializerOptionalBPos  int32 = 11 // EntityDataSerializers.OPTIONAL_BLOCK_POS

	// Rabbit stats, variants & SynchedEntityData constants (Rabbit.java in 26.1.2).
	DefaultRabbitMaxHealth  float32 = 3.0 // 1.5 hearts
	KillerBunnyAttackDamage float32 = 8.0 // 4 hearts melee attack for The Killer Bunny (variant 99)
	KillerBunnyArmorPoints  float32 = 3.0 // Natural armor points for The Killer Bunny
	KillerBunnyXPReward     int32   = 5
	RabbitAdultXPReward     int32   = 2

	RabbitHopSpeed   float64 = 0.14
	RabbitPanicSpeed float64 = 0.22
	RabbitFleeSpeed  float64 = 0.20
	RabbitTemptSpeed float64 = 0.12

	RabbitFleeWolfRange   float64 = 10.0
	RabbitFleePlayerRange float64 = 8.0
	RabbitTemptRange      float64 = 10.0
	RabbitKillerRange     float64 = 16.0

	// Rabbit variant IDs (`Rabbit.Variant` in 26.1.2).
	RabbitVariantBrown          int32 = 0
	RabbitVariantWhite          int32 = 1
	RabbitVariantBlack          int32 = 2
	RabbitVariantWhiteSplotched int32 = 3
	RabbitVariantGold           int32 = 4
	RabbitVariantSalt           int32 = 5
	RabbitVariantEvil           int32 = 99 // The Killer Bunny

	// SynchedEntityData index for Rabbit in protocol 775 (26.1.2):
	// Rabbit extends Animal -> AgeableMob (16: DATA_BABY_ID) -> PathfinderMob -> Mob (15).
	EntityDataRabbitVariantID byte = 17 // Rabbit.DATA_TYPE_ID (INT serializer = 1)

	// Canonical sounds for Creaking, Creaking Heart, and Rabbit in 26.1.2.
	SoundCreakingAmbient     = "minecraft:entity.creaking.ambient"
	SoundCreakingActivate    = "minecraft:entity.creaking.activate"
	SoundCreakingDeactivate  = "minecraft:entity.creaking.deactivate"
	SoundCreakingAttack      = "minecraft:entity.creaking.attack"
	SoundCreakingDeath       = "minecraft:entity.creaking.death"
	SoundCreakingStep        = "minecraft:entity.creaking.step"
	SoundCreakingFreeze      = "minecraft:entity.creaking.freeze"
	SoundCreakingUnfreeze    = "minecraft:entity.creaking.unfreeze"
	SoundCreakingSpawn       = "minecraft:entity.creaking.spawn"
	SoundCreakingSway        = "minecraft:entity.creaking.sway"
	SoundCreakingTwitch      = "minecraft:entity.creaking.twitch"
	SoundCreakingHeartHurt   = "minecraft:block.creaking_heart.hurt"
	SoundCreakingHeartBreak  = "minecraft:block.creaking_heart.break"
	SoundCreakingHeartSpawn  = "minecraft:block.creaking_heart.spawn"
	SoundRabbitAmbient       = "minecraft:entity.rabbit.ambient"
	SoundRabbitAttack        = "minecraft:entity.rabbit.attack"
	SoundRabbitDeath         = "minecraft:entity.rabbit.death"
	SoundRabbitHurt          = "minecraft:entity.rabbit.hurt"
	SoundRabbitJump          = "minecraft:entity.rabbit.jump"
	SoundChorusFruitTeleport = "minecraft:item.chorus_fruit.teleport"
)

// CreakingState holds server-side state for a `minecraft:creaking` entity.
type CreakingState struct {
	EntityID       int32
	Dimension      string
	CanMove        bool
	IsActive       bool
	IsTearingDown  bool
	IsHeartBound   bool
	HeartX         int
	HeartY         int
	HeartZ         int
	TargetPlayer   *PlayerSession
	AttackCooldown int
	TearingTicks   int
}

// CreakingHeartState holds server-side state for a `minecraft:creaking_heart` block.
type CreakingHeartState struct {
	Dimension          string
	X                  int
	Y                  int
	Z                  int
	Natural            bool
	LinkedCreakingID   int32
	ResinCooldownTicks int
}

// RabbitState holds server-side state for a `minecraft:rabbit` entity.
type RabbitState struct {
	EntityID        int32
	Dimension       string
	Variant         int32 // 0=brown, 1=white, 2=black, 3=white_splotched, 4=gold, 5=salt, 99=evil
	IsToast         bool  // True when named "Toast" (special memorial skin)
	CustomName      string
	PanicTicks      int
	MoreCarrotTicks int
	AttackCooldown  int
}

type creakingBlockKey struct {
	dim     string
	x, y, z int
}

type creakingRabbitManager struct {
	mu       sync.RWMutex
	creaking map[int32]*CreakingState
	hearts   map[creakingBlockKey]*CreakingHeartState
	rabbits  map[int32]*RabbitState
}

var serverCreakingRabbitManagers sync.Map // map[*Server]*creakingRabbitManager

func (s *Server) creakingRabbitMgr() *creakingRabbitManager {
	if s == nil {
		return &creakingRabbitManager{
			creaking: make(map[int32]*CreakingState),
			hearts:   make(map[creakingBlockKey]*CreakingHeartState),
			rabbits:  make(map[int32]*RabbitState),
		}
	}
	if v, ok := serverCreakingRabbitManagers.Load(s); ok {
		return v.(*creakingRabbitManager)
	}
	actual, _ := serverCreakingRabbitManagers.LoadOrStore(s, &creakingRabbitManager{
		creaking: make(map[int32]*CreakingState),
		hearts:   make(map[creakingBlockKey]*CreakingHeartState),
		rabbits:  make(map[int32]*RabbitState),
	})
	return actual.(*creakingRabbitManager)
}

func packBlockPosInt64(x, y, z int) int64 {
	ux := uint64(int64(x) & 0x3FFFFFF)
	uz := uint64(int64(z) & 0x3FFFFFF)
	uy := uint64(int64(y) & 0xFFF)
	return int64((ux << 38) | (uz << 12) | uy)
}

func unpackBlockPosInt64(v int64) (x, y, z int) {
	ux := v >> 38
	uz := (v << 26) >> 38
	uy := (v << 52) >> 52
	return int(ux), int(uy), int(uz)
}

// ============================================================================
// 1. CREAKING (minecraft:creaking) & CREAKING HEART (minecraft:creaking_heart)
// ============================================================================

// EncodeCreakingEntityDataPacket encodes a ClientboundSetEntityDataPacket (0x63)
// updating `DATA_CAN_MOVE` (16), `DATA_IS_ACTIVE` (17), `DATA_IS_TEARING_DOWN` (18),
// and `DATA_HOME_POS` (19, `Optional<BlockPos>`) for protocol 775.
func EncodeCreakingEntityDataPacket(entityID int32, canMove, isActive, isTearingDown, hasHomePos bool, homeX, homeY, homeZ int) []byte {
	w := protocol.NewWriter(36)
	w.VarInt(entityID)

	w.Byte(EntityDataCreakingCanMoveID)
	w.VarInt(EntityDataSerializerBool)
	w.Bool(canMove)

	w.Byte(EntityDataCreakingIsActiveID)
	w.VarInt(EntityDataSerializerBool)
	w.Bool(isActive)

	w.Byte(EntityDataCreakingIsTearingDownID)
	w.VarInt(EntityDataSerializerBool)
	w.Bool(isTearingDown)

	w.Byte(EntityDataCreakingHomePosID)
	w.VarInt(EntityDataSerializerOptionalBPos)
	w.Bool(hasHomePos)
	if hasHomePos {
		w.Int64(packBlockPosInt64(homeX, homeY, homeZ))
	}

	w.Byte(0xFF)
	return w.Bytes()
}

// DecodeCreakingEntityDataPacket decodes a ClientboundSetEntityDataPacket (0x63)
// produced by EncodeCreakingEntityDataPacket.
func DecodeCreakingEntityDataPacket(data []byte) (entityID int32, canMove, isActive, isTearingDown, hasHomePos bool, homeX, homeY, homeZ int, err error) {
	r := protocol.NewReader(data)
	entityID, err = r.VarInt()
	if err != nil {
		return 0, false, false, false, false, 0, 0, 0, err
	}
	for {
		idx, err := r.ReadByte()
		if err != nil {
			return 0, false, false, false, false, 0, 0, 0, err
		}
		if idx == 0xFF {
			return entityID, canMove, isActive, isTearingDown, hasHomePos, homeX, homeY, homeZ, nil
		}
		ser, err := r.VarInt()
		if err != nil {
			return 0, false, false, false, false, 0, 0, 0, err
		}
		switch idx {
		case EntityDataCreakingCanMoveID:
			if ser != EntityDataSerializerBool {
				return 0, false, false, false, false, 0, 0, 0, errors.New("creaking: unexpected can_move serializer")
			}
			canMove, err = r.Bool()
			if err != nil {
				return 0, false, false, false, false, 0, 0, 0, err
			}
		case EntityDataCreakingIsActiveID:
			if ser != EntityDataSerializerBool {
				return 0, false, false, false, false, 0, 0, 0, errors.New("creaking: unexpected is_active serializer")
			}
			isActive, err = r.Bool()
			if err != nil {
				return 0, false, false, false, false, 0, 0, 0, err
			}
		case EntityDataCreakingIsTearingDownID:
			if ser != EntityDataSerializerBool {
				return 0, false, false, false, false, 0, 0, 0, errors.New("creaking: unexpected is_tearing_down serializer")
			}
			isTearingDown, err = r.Bool()
			if err != nil {
				return 0, false, false, false, false, 0, 0, 0, err
			}
		case EntityDataCreakingHomePosID:
			if ser != EntityDataSerializerOptionalBPos {
				return 0, false, false, false, false, 0, 0, 0, errors.New("creaking: unexpected home_pos serializer")
			}
			hasHomePos, err = r.Bool()
			if err != nil {
				return 0, false, false, false, false, 0, 0, 0, err
			}
			if hasHomePos {
				packed, err := r.Int64()
				if err != nil {
					return 0, false, false, false, false, 0, 0, 0, err
				}
				homeX, homeY, homeZ = unpackBlockPosInt64(packed)
			}
		default:
			return 0, false, false, false, false, 0, 0, 0, errors.New("creaking: unexpected entity data index")
		}
	}
}

// IsPaleOakLogBlock reports whether state is a Pale Oak log or wood block
// (`minecraft:pale_oak_log`, `minecraft:stripped_pale_oak_log`, `minecraft:pale_oak_wood`,
// `minecraft:stripped_pale_oak_wood`).
func IsPaleOakLogBlock(state uint16) bool {
	switch world.BlockName(state) {
	case PaleOakLogBlockID, StrippedPaleOakLogBlockID, PaleOakWoodBlockID, StrippedPaleOakWoodBlockID:
		return true
	default:
		return false
	}
}

// IsPaleOakLogWithAxis reports whether state is a Pale Oak log/wood block aligned
// along `requiredAxis` (`"x"`, `"y"`, or `"z"`).
func IsPaleOakLogWithAxis(state uint16, requiredAxis string) bool {
	if !IsPaleOakLogBlock(state) {
		return false
	}
	_, props, ok := world.BlockStateProperties(state)
	if !ok {
		return true
	}
	ax, hasAxis := props["axis"]
	return !hasAxis || ax == requiredAxis
}

// IsCreakingHeartSandwiched checks whether the `minecraft:creaking_heart` block at
// `(hx, hy, hz)` in `cache` is properly sandwiched between two Pale Oak log blocks
// along its `axis` (`"x"`, `"y"`, or `"z"`).
func IsCreakingHeartSandwiched(cache *world.Cache, hx, hy, hz int) (sandwiched bool, axis string) {
	if cache == nil {
		return false, "y"
	}
	st := cache.GetBlock(hx, hy, hz)
	if world.BlockName(st) != CreakingHeartBlockID {
		return false, "y"
	}
	axis = "y"
	if _, props, ok := world.BlockStateProperties(st); ok && props["axis"] != "" {
		axis = props["axis"]
	}
	var dx, dy, dz int
	switch axis {
	case "x":
		dx = 1
	case "z":
		dz = 1
	default:
		axis = "y"
		dy = 1
	}
	if hy-dy < world.MinY || hy+dy >= world.MinY+world.WorldHeight {
		return false, axis
	}
	negState := cache.GetBlock(hx-dx, hy-dy, hz-dz)
	posState := cache.GetBlock(hx+dx, hy+dy, hz+dz)
	return IsPaleOakLogWithAxis(negState, axis) && IsPaleOakLogWithAxis(posState, axis), axis
}

// EvaluateCreakingHeartState computes the vanilla 26.1.2 `creaking_heart_state`
// property (`"uprooted"`, `"dormant"`, or `"awake"`) for a `minecraft:creaking_heart`
// at `(hx, hy, hz)` in `dimension`:
//   - `"uprooted"` if not sandwiched between two same-axis Pale Oak log blocks
//   - `"awake"` if sandwiched AND it is currently night (`s.IsNight()`) or thundering (`s.IsThundering()`)
//   - `"dormant"` if sandwiched during daytime (clear/rain without thunder).
func (s *Server) EvaluateCreakingHeartState(dimension string, hx, hy, hz int) string {
	if s == nil {
		return CreakingHeartStateUprooted
	}
	cache := s.DimensionChunks(world.NormalizeDimension(dimension))
	sandwiched, _ := IsCreakingHeartSandwiched(cache, hx, hy, hz)
	if !sandwiched {
		return CreakingHeartStateUprooted
	}
	if s.IsNight() || s.IsThundering() {
		return CreakingHeartStateAwake
	}
	return CreakingHeartStateDormant
}

// RegisterCreakingHeart registers a `minecraft:creaking_heart` block at `(hx, hy, hz)`
// in `dimension` so it participates in Pale Garden night activation and Creaking spawning.
func (s *Server) RegisterCreakingHeart(dimension string, hx, hy, hz int, natural bool) {
	if s == nil {
		return
	}
	normDim := world.NormalizeDimension(dimension)
	key := creakingBlockKey{dim: normDim, x: hx, y: hy, z: hz}
	cm := s.creakingRabbitMgr()
	cm.mu.Lock()
	if existing, ok := cm.hearts[key]; ok && existing != nil {
		existing.Natural = natural
	} else {
		cm.hearts[key] = &CreakingHeartState{
			Dimension: normDim,
			X:         hx,
			Y:         hy,
			Z:         hz,
			Natural:   natural,
		}
	}
	cm.mu.Unlock()
}

// SpawnCreaking spawns a standalone (unlinked, `1.0 HP`) `minecraft:creaking` at
// `(x, y, z)` in `dimension`.
func (s *Server) SpawnCreaking(dimension string, x, y, z float64) (int32, error) {
	return s.spawnCreakingInternal(dimension, x, y, z, false, 0, 0, 0)
}

// SpawnCreakingLinkedToHeart spawns a heart-bound (invulnerable) `minecraft:creaking`
// at `(x, y, z)` in `dimension` linked to the `minecraft:creaking_heart` at `(hx, hy, hz)`.
func (s *Server) SpawnCreakingLinkedToHeart(dimension string, x, y, z float64, hx, hy, hz int) (int32, error) {
	return s.spawnCreakingInternal(dimension, x, y, z, true, hx, hy, hz)
}

func (s *Server) spawnCreakingInternal(dimension string, x, y, z float64, heartBound bool, hx, hy, hz int) (int32, error) {
	if s == nil || s.entities == nil {
		return 0, errors.New("server: nil entity manager")
	}
	normDim := world.NormalizeDimension(dimension)
	typeID := ResolveEntityTypeID(MobTypeCreaking)
	if typeID < 0 {
		typeID = EntityTypeCreakingID
	}
	id := s.entities.Add(&world.Entity{
		TypeID:    typeID,
		TypeName:  MobTypeCreaking,
		Dimension: normDim,
		X:         x,
		Y:         y,
		Z:         z,
		OnGround:  true,
	})
	s.SetMobHealth(id, DefaultCreakingMaxHealth)

	cm := s.creakingRabbitMgr()
	cm.mu.Lock()
	cm.creaking[id] = &CreakingState{
		EntityID:     id,
		Dimension:    normDim,
		CanMove:      true,
		IsActive:     heartBound,
		IsHeartBound: heartBound,
		HeartX:       hx,
		HeartY:       hy,
		HeartZ:       hz,
	}
	if heartBound {
		key := creakingBlockKey{dim: normDim, x: hx, y: hy, z: hz}
		hs, ok := cm.hearts[key]
		if !ok || hs == nil {
			hs = &CreakingHeartState{
				Dimension: normDim,
				X:         hx,
				Y:         hy,
				Z:         hz,
				Natural:   true,
			}
			cm.hearts[key] = hs
		}
		hs.LinkedCreakingID = id
	}
	cm.mu.Unlock()

	if ent := s.entities.Get(id); ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, ent))
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, PlaySetEntityDataCB,
			EncodeCreakingEntityDataPacket(id, true, heartBound, false, heartBound, hx, hy, hz))
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, PlaySoundPacketID,
			EncodeSoundPacket(SoundCreakingSpawn, SoundSourceHostile, x, y, z, 1.0, 1.0, 0))
	}
	return id, nil
}

// GetCreakingState returns a snapshot of the CreakingState for entityID.
func (s *Server) GetCreakingState(entityID int32) (CreakingState, bool) {
	if s == nil || entityID <= 0 {
		return CreakingState{}, false
	}
	cm := s.creakingRabbitMgr()
	cm.mu.RLock()
	st, ok := cm.creaking[entityID]
	var out CreakingState
	if ok && st != nil {
		out = *st
	}
	cm.mu.RUnlock()
	return out, ok && st != nil
}

// LinkCreakingToHeart links an existing `minecraft:creaking` entity to the
// `minecraft:creaking_heart` block at `(hx, hy, hz)`.
func (s *Server) LinkCreakingToHeart(entityID int32, hx, hy, hz int) bool {
	if s == nil || s.entities == nil || entityID <= 0 {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeCreaking {
		return false
	}
	dim := ent.EffectiveDimension()
	cm := s.creakingRabbitMgr()
	cm.mu.Lock()
	st, ok := cm.creaking[entityID]
	if !ok || st == nil {
		st = &CreakingState{
			EntityID:  entityID,
			Dimension: dim,
			CanMove:   true,
		}
		cm.creaking[entityID] = st
	}
	st.IsHeartBound = true
	st.IsActive = true
	st.HeartX = hx
	st.HeartY = hy
	st.HeartZ = hz
	canMove := st.CanMove
	key := creakingBlockKey{dim: dim, x: hx, y: hy, z: hz}
	hs, exists := cm.hearts[key]
	if !exists || hs == nil {
		hs = &CreakingHeartState{Dimension: dim, X: hx, Y: hy, Z: hz, Natural: true}
		cm.hearts[key] = hs
	}
	hs.LinkedCreakingID = entityID
	cm.mu.Unlock()

	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
		EncodeCreakingEntityDataPacket(entityID, canMove, true, false, true, hx, hy, hz))
	return true
}

// TrySpawnCreakingNearHeart searches within `+/-16` horizontal and `+/-8` vertical
// blocks of an awake `minecraft:creaking_heart` at `(hx, hy, hz)` for a walkable
// air space and spawns a heart-bound `minecraft:creaking`.
func (s *Server) TrySpawnCreakingNearHeart(dimension string, hx, hy, hz int, rng *rand.Rand) (int32, bool) {
	if s == nil || s.entities == nil || s.Difficulty() == DifficultyPeaceful || !s.SpawnMonsters() {
		return 0, false
	}
	normDim := world.NormalizeDimension(dimension)
	cache := s.DimensionChunks(normDim)
	for attempt := 0; attempt < 16; attempt++ {
		var dx, dz, dy int
		if rng != nil {
			dx = rng.Intn(2*CreakingHeartSpawnHorizRange+1) - CreakingHeartSpawnHorizRange
			dz = rng.Intn(2*CreakingHeartSpawnHorizRange+1) - CreakingHeartSpawnHorizRange
			dy = rng.Intn(2*CreakingHeartSpawnVertRange+1) - CreakingHeartSpawnVertRange
		} else {
			dx = 2 + attempt
			dz = 0
			dy = 0
		}
		if dx == 0 && dz == 0 {
			dx = 2
		}
		tx, ty, tz := hx+dx, hy+dy, hz+dz
		if ty < world.MinY+1 || ty+2 >= world.MinY+world.WorldHeight {
			continue
		}
		if cache != nil {
			// Walk vertically down/up a few blocks to find solid ground with 3 air/passable blocks above.
			foundGround := false
			for offset := -4; offset <= 4; offset++ {
				cy := ty + offset
				if cy < world.MinY+1 || cy+2 >= world.MinY+world.WorldHeight {
					continue
				}
				below := cache.GetBlock(tx, cy-1, tz)
				b0 := cache.GetBlock(tx, cy, tz)
				b1 := cache.GetBlock(tx, cy+1, tz)
				b2 := cache.GetBlock(tx, cy+2, tz)
				if below != world.StateAir && !isPassableBlock(below) &&
					(b0 == world.StateAir || isPassableBlock(b0)) &&
					(b1 == world.StateAir || isPassableBlock(b1)) &&
					(b2 == world.StateAir || isPassableBlock(b2)) {
					ty = cy
					foundGround = true
					break
				}
			}
			if !foundGround {
				continue
			}
		}
		id, err := s.SpawnCreakingLinkedToHeart(normDim, float64(tx)+0.5, float64(ty), float64(tz)+0.5, hx, hy, hz)
		if err == nil && id > 0 {
			cx := int32(hx >> 4)
			cz := int32(hz >> 4)
			s.BroadcastDimensionChunk(normDim, cx, cz, PlaySoundPacketID,
				EncodeSoundPacket(SoundCreakingHeartSpawn, SoundSourceBlocks, float64(hx)+0.5, float64(hy)+0.5, float64(hz)+0.5, 1.0, 1.0, 0))
			return id, true
		}
	}
	return 0, false
}

// UpdateCreakingHeartAt evaluates the `minecraft:creaking_heart` block at `(hx, hy, hz)`
// in `dimension`, updates its `creaking_heart_state` (`uprooted`, `dormant`, or `awake`),
// and spawns/despawns its linked `minecraft:creaking` according to Pale Garden night rules.
func (s *Server) UpdateCreakingHeartAt(dimension string, hx, hy, hz int, rng *rand.Rand) (newHeartState string, spawnedCreakingID int32) {
	if s == nil {
		return CreakingHeartStateUprooted, 0
	}
	normDim := world.NormalizeDimension(dimension)
	cache := s.DimensionChunks(normDim)
	if cache == nil {
		return CreakingHeartStateUprooted, 0
	}
	curBlock := cache.GetBlock(hx, hy, hz)
	if world.BlockName(curBlock) != CreakingHeartBlockID {
		return CreakingHeartStateUprooted, 0
	}
	_, props, _ := world.BlockStateProperties(curBlock)
	axis := props["axis"]
	if axis == "" {
		axis = "y"
	}
	natural := props["natural"] == "true"
	s.RegisterCreakingHeart(normDim, hx, hy, hz, natural)

	newHeartState = s.EvaluateCreakingHeartState(normDim, hx, hy, hz)
	if props["creaking_heart_state"] != newHeartState {
		if updatedState, ok := world.BlockStateIDByProperties(CreakingHeartBlockID, map[string]string{
			"axis":                 axis,
			"creaking_heart_state": newHeartState,
			"natural":              props["natural"],
		}); ok {
			cache.SetBlock(hx, hy, hz, updatedState)
			s.broadcastMobBlockUpdate(normDim, hx, hy, hz, updatedState)
		}
	}

	key := creakingBlockKey{dim: normDim, x: hx, y: hy, z: hz}
	cm := s.creakingRabbitMgr()
	cm.mu.RLock()
	hs := cm.hearts[key]
	var linkedID int32
	if hs != nil {
		linkedID = hs.LinkedCreakingID
	}
	cm.mu.RUnlock()

	if linkedID > 0 && s.entities != nil && s.entities.Get(linkedID) == nil {
		cm.mu.Lock()
		if hs := cm.hearts[key]; hs != nil {
			hs.LinkedCreakingID = 0
		}
		cm.mu.Unlock()
		linkedID = 0
	}

	switch newHeartState {
	case CreakingHeartStateUprooted:
		if linkedID > 0 {
			s.DespawnHeartBoundCreaking(linkedID, true)
		}
	case CreakingHeartStateDormant:
		if linkedID > 0 {
			// During daytime, despawn the linked Creaking if no player is currently looking at it.
			if lookedAt, _ := s.IsAnyPlayerLookingAtCreaking(linkedID); !lookedAt {
				s.DespawnHeartBoundCreaking(linkedID, false)
			}
		}
	case CreakingHeartStateAwake:
		if linkedID == 0 {
			// Spawn a linked Creaking when a survival/adventure player is within 32 blocks.
			playerNear := false
			maxDistSq := CreakingFollowRange * CreakingFollowRange
			for _, p := range s.onlinePlayersSnapshot() {
				if p == nil || p.IsDead() {
					continue
				}
				pSnap := p.Snapshot()
				if world.NormalizeDimension(pSnap.Dimension) != normDim ||
					pSnap.GameMode == GameModeCreative || pSnap.GameMode == GameModeSpectator {
					continue
				}
				dx := pSnap.X - (float64(hx) + 0.5)
				dy := pSnap.Y - (float64(hy) + 0.5)
				dz := pSnap.Z - (float64(hz) + 0.5)
				if dx*dx+dy*dy+dz*dz <= maxDistSq {
					playerNear = true
					break
				}
			}
			if playerNear {
				spawnedCreakingID, _ = s.TrySpawnCreakingNearHeart(normDim, hx, hy, hz, rng)
			}
		}
	}
	return newHeartState, spawnedCreakingID
}

// IsPlayerLookingAtCreaking checks whether `player` is looking at the Creaking
// at `(creakingX, creakingY, creakingZ)` in `dimension`:
//   - Player must be alive, in the same dimension, in Survival or Adventure mode,
//     and within `CreakingFollowRange` (`32` blocks).
//   - Wearing `minecraft:carved_pumpkin` on the head slot (`ArmorSlotHead`) or
//     having `EffectBlindness` exempts the player from freezing the Creaking.
//   - Checks that the normalized vector from player's eyes (`Y + 1.62`) to the
//     Creaking (`Y + 1.35`) is within the player's view cone (`dot > 0.50`) and
//     has unobstructed line-of-sight (`HasGuardianLaserLineOfSight`).
func (s *Server) IsPlayerLookingAtCreaking(player *PlayerSession, dimension string, creakingX, creakingY, creakingZ float64) bool {
	if s == nil || player == nil || player.IsDead() {
		return false
	}
	snap := player.Snapshot()
	normDim := world.NormalizeDimension(dimension)
	if world.NormalizeDimension(snap.Dimension) != normDim {
		return false
	}
	if snap.GameMode == GameModeCreative || snap.GameMode == GameModeSpectator {
		return false
	}
	if player.HasPlayerEffect(EffectBlindness) {
		return false
	}
	headStack := player.InventorySlot(ArmorSlotHead)
	if !headStack.IsEmpty() && canonicalResourceName(headStack.ResolvedID()) == "minecraft:carved_pumpkin" {
		return false
	}

	eyeX := snap.X
	eyeY := snap.Y + 1.62
	eyeZ := snap.Z
	targetX := creakingX
	targetY := creakingY + 1.35
	targetZ := creakingZ

	dx := targetX - eyeX
	dy := targetY - eyeY
	dz := targetZ - eyeZ
	distSq := dx*dx + dy*dy + dz*dz
	if distSq > CreakingFollowRange*CreakingFollowRange || distSq < 1e-6 {
		return false
	}
	dist := math.Sqrt(distSq)
	toX, toY, toZ := dx/dist, dy/dist, dz/dist

	yawRad := float64(snap.Yaw) * (math.Pi / 180.0)
	pitchRad := float64(snap.Pitch) * (math.Pi / 180.0)
	cosPitch := math.Cos(pitchRad)
	lookX := -math.Sin(yawRad) * cosPitch
	lookY := -math.Sin(pitchRad)
	lookZ := math.Cos(yawRad) * cosPitch

	dot := lookX*toX + lookY*toY + lookZ*toZ
	if dot <= CreakingGazeCosThreshold {
		return false
	}

	cache := s.DimensionChunks(normDim)
	return HasGuardianLaserLineOfSight(cache, eyeX, eyeY, eyeZ, targetX, targetY, targetZ)
}

// IsAnyPlayerLookingAtCreaking checks whether any online Survival/Adventure player
// is currently looking at the `minecraft:creaking` with `entityID`.
func (s *Server) IsAnyPlayerLookingAtCreaking(entityID int32) (lookedAt bool, observer *PlayerSession) {
	if s == nil || s.entities == nil || entityID <= 0 {
		return false, nil
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeCreaking {
		return false, nil
	}
	dim := ent.EffectiveDimension()
	for _, p := range s.onlinePlayersSnapshot() {
		if s.IsPlayerLookingAtCreaking(p, dim, ent.X, ent.Y, ent.Z) {
			return true, p
		}
	}
	return false, nil
}

// SpawnResinClumpsAroundHeart attaches up to `count` (`2..3`) `minecraft:resin_clump`
// multiface blocks onto faces adjacent to `minecraft:pale_oak_log` / `minecraft:stripped_pale_oak_log`
// blocks within 2 blocks of `(hx, hy, hz)` in `dimension`.
func (s *Server) SpawnResinClumpsAroundHeart(dimension string, hx, hy, hz int, count int, rng *rand.Rand) int {
	if s == nil {
		return 0
	}
	normDim := world.NormalizeDimension(dimension)
	cache := s.DimensionChunks(normDim)
	if cache == nil {
		return 0
	}
	if count <= 0 {
		count = 2
		if rng != nil {
			count = 2 + rng.Intn(2) // 2..3 resin clumps
		}
	}

	type candidateFace struct {
		x, y, z int
		face    string
	}
	var candidates []candidateFace
	faceOffsets := [6]struct {
		dx, dy, dz int
		attachFace string
	}{
		{0, 1, 0, "down"},   // air above log -> attaches to "down" face (onto top of log)
		{0, -1, 0, "up"},    // air below log -> attaches to "up" face (onto bottom of log)
		{0, 0, -1, "south"}, // air north of log -> attaches to "south" face (onto north side of log)
		{0, 0, 1, "north"},  // air south of log -> attaches to "north" face (onto south side of log)
		{-1, 0, 0, "east"},  // air west of log -> attaches to "east" face (onto west side of log)
		{1, 0, 0, "west"},   // air east of log -> attaches to "west" face (onto east side of log)
	}

	for dy := -2; dy <= 2; dy++ {
		ly := hy + dy
		if ly < world.MinY+1 || ly >= world.MinY+world.WorldHeight-1 {
			continue
		}
		for dx := -1; dx <= 1; dx++ {
			lx := hx + dx
			for dz := -1; dz <= 1; dz++ {
				lz := hz + dz
				if !IsPaleOakLogBlock(cache.GetBlock(lx, ly, lz)) {
					continue
				}
				for _, fo := range faceOffsets {
					ax, ay, az := lx+fo.dx, ly+fo.dy, lz+fo.dz
					cur := cache.GetBlock(ax, ay, az)
					if cur == world.StateAir || world.BlockName(cur) == ResinClumpItemID {
						candidates = append(candidates, candidateFace{x: ax, y: ay, z: az, face: fo.attachFace})
					}
				}
			}
		}
	}
	if len(candidates) == 0 {
		return 0
	}
	if rng != nil {
		rng.Shuffle(len(candidates), func(i, j int) {
			candidates[i], candidates[j] = candidates[j], candidates[i]
		})
	}

	placed := 0
	for _, cand := range candidates {
		if placed >= count {
			break
		}
		if newState, _, ok := cache.PlaceMultifaceFaceAt(cand.x, cand.y, cand.z, ResinClumpItemID, cand.face); ok && newState != world.StateAir {
			cache.SetBlock(cand.x, cand.y, cand.z, newState)
			s.broadcastMobBlockUpdate(normDim, cand.x, cand.y, cand.z, newState)
			placed++
		}
	}
	return placed
}

// OnCreakingHit handles a player or projectile striking a `minecraft:creaking`:
//   - If the Creaking is heart-bound (`IsHeartBound == true`), it is completely
//     invulnerable to direct damage (`invulnerable = true`), plays `SoundCreakingSway`,
//     and if its linked `minecraft:creaking_heart`'s resin cooldown is 0, spawns
//     `2..3` `minecraft:resin_clump` blocks on adjacent Pale Oak logs (`ResinCooldownTicks = 100`).
//   - If the Creaking is standalone (`IsHeartBound == false`), returns `invulnerable = false`
//     so normal damage (`1.0 HP`) applies.
func (s *Server) OnCreakingHit(entityID int32, attacker *PlayerSession, rng *rand.Rand) (invulnerable bool, resinPlaced int) {
	if s == nil || s.entities == nil || entityID <= 0 {
		return false, 0
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeCreaking {
		return false, 0
	}
	cm := s.creakingRabbitMgr()
	cm.mu.Lock()
	st, ok := cm.creaking[entityID]
	if !ok || st == nil || !st.IsHeartBound {
		cm.mu.Unlock()
		return false, 0
	}
	if attacker != nil {
		st.TargetPlayer = attacker
	}
	dim := ent.EffectiveDimension()
	hx, hy, hz := st.HeartX, st.HeartY, st.HeartZ
	key := creakingBlockKey{dim: dim, x: hx, y: hy, z: hz}
	hs := cm.hearts[key]
	canExudeResin := false
	if hs != nil && hs.ResinCooldownTicks <= 0 {
		hs.ResinCooldownTicks = CreakingResinCooldownTicks
		canExudeResin = true
	}
	cm.mu.Unlock()

	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundCreakingSway, SoundSourceHostile, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
	s.BroadcastDimensionChunk(dim, int32(hx>>4), int32(hz>>4), PlaySoundPacketID,
		EncodeSoundPacket(SoundCreakingHeartHurt, SoundSourceBlocks, float64(hx)+0.5, float64(hy)+0.5, float64(hz)+0.5, 1.0, 1.0, 0))

	if canExudeResin && attacker != nil {
		resinPlaced = s.SpawnResinClumpsAroundHeart(dim, hx, hy, hz, 0, rng)
	}
	return true, resinPlaced
}

// DespawnHeartBoundCreaking removes a heart-bound `minecraft:creaking` when its
// `minecraft:creaking_heart` is destroyed (`tearingDown = true`) or when daytime arrives.
func (s *Server) DespawnHeartBoundCreaking(entityID int32, tearingDown bool) bool {
	if s == nil || s.entities == nil || entityID <= 0 {
		return false
	}
	ent := s.entities.Get(entityID)
	cm := s.creakingRabbitMgr()
	cm.mu.Lock()
	st := cm.creaking[entityID]
	if st != nil && st.IsHeartBound {
		key := creakingBlockKey{dim: st.Dimension, x: st.HeartX, y: st.HeartY, z: st.HeartZ}
		if hs := cm.hearts[key]; hs != nil && hs.LinkedCreakingID == entityID {
			hs.LinkedCreakingID = 0
		}
	}
	delete(cm.creaking, entityID)
	cm.mu.Unlock()

	if ent == nil {
		return false
	}
	dim := ent.EffectiveDimension()
	cp := ent.ChunkPos()
	if tearingDown && st != nil {
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
			EncodeCreakingEntityDataPacket(entityID, false, true, true, st.IsHeartBound, st.HeartX, st.HeartY, st.HeartZ))
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
			EncodeSoundPacket(SoundCreakingDeath, SoundSourceHostile, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
	} else {
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
			EncodeSoundPacket(SoundCreakingDeactivate, SoundSourceHostile, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
	}
	s.entities.Remove(entityID)
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayRemoveEntities,
		encodeRemoveEntitiesPacket(nil, []int32{entityID}))
	return true
}

// OnCreakingHeartBroken handles a `minecraft:creaking_heart` block being broken
// at `(hx, hy, hz)` in `dimension`:
//   - Immediately tears down & despawns its linked `minecraft:creaking`
//   - If broken with Silk Touch, drops `1x minecraft:creaking_heart` and `0` XP
//   - Otherwise drops `1..3` `minecraft:resin_clump` (up to `4` with Fortune) and
//     `20..24` XP when `natural == true`.
func (s *Server) OnCreakingHeartBroken(dimension string, hx, hy, hz int, destroyer *PlayerSession, silkTouch bool, fortuneLevel int, rng *rand.Rand) (destroyedCreakingID int32, drops []world.ItemStack, xpReward int32) {
	if s == nil {
		return 0, nil, 0
	}
	normDim := world.NormalizeDimension(dimension)
	key := creakingBlockKey{dim: normDim, x: hx, y: hy, z: hz}
	cm := s.creakingRabbitMgr()
	cm.mu.Lock()
	hs := cm.hearts[key]
	natural := true
	if hs != nil {
		destroyedCreakingID = hs.LinkedCreakingID
		natural = hs.Natural
	}
	delete(cm.hearts, key)
	cm.mu.Unlock()

	if destroyedCreakingID > 0 {
		s.DespawnHeartBoundCreaking(destroyedCreakingID, true)
	}
	if s.entities != nil {
		// Also check any Creaking referencing (hx, hy, hz).
		cm.mu.RLock()
		var extraIDs []int32
		for id, st := range cm.creaking {
			if st != nil && st.IsHeartBound && st.Dimension == normDim && st.HeartX == hx && st.HeartY == hy && st.HeartZ == hz {
				extraIDs = append(extraIDs, id)
			}
		}
		cm.mu.RUnlock()
		for _, id := range extraIDs {
			s.DespawnHeartBoundCreaking(id, true)
			if destroyedCreakingID == 0 {
				destroyedCreakingID = id
			}
		}
	}

	cx := int32(hx >> 4)
	cz := int32(hz >> 4)
	s.BroadcastDimensionChunk(normDim, cx, cz, PlaySoundPacketID,
		EncodeSoundPacket(SoundCreakingHeartBreak, SoundSourceBlocks, float64(hx)+0.5, float64(hy)+0.5, float64(hz)+0.5, 1.0, 1.0, 0))

	if silkTouch {
		if heartItem := world.NewNamedItemStack(CreakingHeartBlockID, 1); !heartItem.IsEmpty() {
			drops = []world.ItemStack{heartItem}
		}
		return destroyedCreakingID, drops, 0
	}

	clumpCount := int32(2)
	if rng != nil {
		clumpCount = int32(1 + rng.Intn(3)) // 1..3
		if fortuneLevel > 0 {
			clumpCount += int32(rng.Intn(fortuneLevel + 1))
		}
	}
	if clumpCount > 4 {
		clumpCount = 4
	}
	if clumpStack := world.NewNamedItemStack(ResinClumpItemID, clumpCount); !clumpStack.IsEmpty() {
		drops = []world.ItemStack{clumpStack}
	}
	if natural {
		xpReward = 20
		if rng != nil {
			xpReward = int32(20 + rng.Intn(5)) // 20..24 XP
		}
		if destroyer != nil && xpReward > 0 {
			s.SpawnExperienceOrbsInDimension(normDim, float64(hx)+0.5, float64(hy)+0.5, float64(hz)+0.5, xpReward)
		}
	}
	return destroyedCreakingID, drops, xpReward
}

// OnCreakingKilled cleans up CreakingState when a standalone Creaking dies.
func (s *Server) OnCreakingKilled(entityID int32) {
	if s == nil || entityID <= 0 {
		return
	}
	cm := s.creakingRabbitMgr()
	cm.mu.Lock()
	if st := cm.creaking[entityID]; st != nil && st.IsHeartBound {
		key := creakingBlockKey{dim: st.Dimension, x: st.HeartX, y: st.HeartY, z: st.HeartZ}
		if hs := cm.hearts[key]; hs != nil && hs.LinkedCreakingID == entityID {
			hs.LinkedCreakingID = 0
		}
	}
	delete(cm.creaking, entityID)
	cm.mu.Unlock()
}

// TickCreakingsAndHearts steps all registered `minecraft:creaking_heart` blocks
// and live `minecraft:creaking` entities by 1 server tick (20 TPS).
func (s *Server) TickCreakingsAndHearts(rng *rand.Rand) {
	if s == nil || s.entities == nil {
		return
	}
	cm := s.creakingRabbitMgr()
	cm.mu.Lock()
	heartKeys := make([]creakingBlockKey, 0, len(cm.hearts))
	for k, hs := range cm.hearts {
		if hs != nil {
			if hs.ResinCooldownTicks > 0 {
				hs.ResinCooldownTicks--
			}
			heartKeys = append(heartKeys, k)
		}
	}
	creakingIDs := make([]int32, 0, len(cm.creaking))
	for id := range cm.creaking {
		creakingIDs = append(creakingIDs, id)
	}
	cm.mu.Unlock()

	for _, k := range heartKeys {
		s.UpdateCreakingHeartAt(k.dim, k.x, k.y, k.z, rng)
	}
	for _, id := range creakingIDs {
		s.tickSingleCreaking(id, rng)
	}
}

func (s *Server) tickSingleCreaking(entityID int32, _ *rand.Rand) {
	ent := s.entities.Get(entityID)
	cm := s.creakingRabbitMgr()
	cm.mu.Lock()
	st, ok := cm.creaking[entityID]
	if !ok || st == nil || ent == nil {
		delete(cm.creaking, entityID)
		cm.mu.Unlock()
		return
	}
	if st.AttackCooldown > 0 {
		st.AttackCooldown--
	}
	snap := *st
	cm.mu.Unlock()

	dim := ent.EffectiveDimension()
	cache := s.DimensionChunks(dim)

	// 1. Validate heart link and max distance (34 blocks) for heart-bound Creakings.
	if snap.IsHeartBound {
		if cache != nil && world.BlockName(cache.GetBlock(snap.HeartX, snap.HeartY, snap.HeartZ)) != CreakingHeartBlockID {
			s.DespawnHeartBoundCreaking(entityID, true)
			return
		}
		hdx := ent.X - (float64(snap.HeartX) + 0.5)
		hdy := ent.Y - (float64(snap.HeartY) + 0.5)
		hdz := ent.Z - (float64(snap.HeartZ) + 0.5)
		if hdx*hdx+hdy*hdy+hdz*hdz > CreakingMaxHeartDist*CreakingMaxHeartDist {
			s.DespawnHeartBoundCreaking(entityID, true)
			return
		}
		// Despawn during daytime when no player is observing it.
		if !s.IsNight() && !s.IsThundering() {
			if looked, _ := s.IsAnyPlayerLookingAtCreaking(entityID); !looked {
				s.DespawnHeartBoundCreaking(entityID, false)
				return
			}
		}
	}

	if s.Difficulty() == DifficultyPeaceful {
		return
	}

	// 2. Freeze-when-looked-at check ("Weeping Angel" mechanic).
	lookedAt, _ := s.IsAnyPlayerLookingAtCreaking(entityID)
	canMove := !lookedAt

	// 3. Find nearest Survival/Adventure player within 32 blocks.
	var bestPlayer *PlayerSession
	bestDistSq := CreakingFollowRange*CreakingFollowRange + 1.0
	for _, p := range s.onlinePlayersSnapshot() {
		if p == nil || p.IsDead() {
			continue
		}
		pSnap := p.Snapshot()
		if world.NormalizeDimension(pSnap.Dimension) != dim ||
			pSnap.GameMode == GameModeCreative || pSnap.GameMode == GameModeSpectator {
			continue
		}
		dx := pSnap.X - ent.X
		dy := pSnap.Y - ent.Y
		dz := pSnap.Z - ent.Z
		dSq := dx*dx + dy*dy + dz*dz
		if dSq <= CreakingFollowRange*CreakingFollowRange && dSq < bestDistSq {
			bestDistSq = dSq
			bestPlayer = p
		}
	}
	isActive := bestPlayer != nil || snap.IsHeartBound

	if canMove != snap.CanMove || isActive != snap.IsActive {
		cm.mu.Lock()
		if cur := cm.creaking[entityID]; cur != nil {
			cur.CanMove = canMove
			cur.IsActive = isActive
			cur.TargetPlayer = bestPlayer
		}
		cm.mu.Unlock()
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
			EncodeCreakingEntityDataPacket(entityID, canMove, isActive, false, snap.IsHeartBound, snap.HeartX, snap.HeartY, snap.HeartZ))
		sound := SoundCreakingUnfreeze
		if !canMove {
			sound = SoundCreakingFreeze
		}
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
			EncodeSoundPacket(sound, SoundSourceHostile, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
	}

	// If frozen by a player's gaze, do NOT move or attack!
	if !canMove || bestPlayer == nil {
		return
	}

	// 4. Stalk & melee attack target player when unobserved.
	pSnap := bestPlayer.Snapshot()
	feetY := int(math.Floor(ent.Y + 0.01))
	s.stepEntityTowardPoint(ent, cache, feetY, pSnap.X, pSnap.Y, pSnap.Z, CreakingChaseSpeed, 1.2)

	updated := s.entities.Get(entityID)
	if updated == nil {
		return
	}
	dx := pSnap.X - updated.X
	dy := pSnap.Y - updated.Y
	dz := pSnap.Z - updated.Z
	if dx*dx+dy*dy+dz*dz <= CreakingAttackReach*CreakingAttackReach && snap.AttackCooldown <= 0 {
		cm.mu.Lock()
		if cur := cm.creaking[entityID]; cur != nil {
			cur.AttackCooldown = CreakingAttackCooldownTicks
		}
		cm.mu.Unlock()
		cp := updated.ChunkPos()
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
			EncodeSoundPacket(SoundCreakingAttack, SoundSourceHostile, updated.X, updated.Y, updated.Z, 1.0, 1.0, 0))
		s.AttackPlayerFromMob(entityID, MobTypeCreaking, updated.X, updated.Z, bestPlayer, CreakingAttackDamage)
	}
}

// ============================================================================
// 2. RABBIT (minecraft:rabbit) — Variants, Toast, Killer Bunny, Panic, Crops
// ============================================================================

// EncodeRabbitEntityDataPacket encodes a ClientboundSetEntityDataPacket (0x63)
// updating `AgeableMob.DATA_BABY_ID` (16, BOOLEAN) and `Rabbit.DATA_TYPE_ID`
// (17, INT) for protocol 775.
func EncodeRabbitEntityDataPacket(entityID int32, isBaby bool, variant int32) []byte {
	w := protocol.NewWriter(20)
	w.VarInt(entityID)

	w.Byte(EntityDataAgeableBabyID)
	w.VarInt(EntityDataSerializerBool)
	w.Bool(isBaby)

	w.Byte(EntityDataRabbitVariantID)
	w.VarInt(EntityDataSerializerInt)
	w.VarInt(variant)

	w.Byte(0xFF)
	return w.Bytes()
}

// DecodeRabbitEntityDataPacket decodes a ClientboundSetEntityDataPacket (0x63)
// produced by EncodeRabbitEntityDataPacket.
func DecodeRabbitEntityDataPacket(data []byte) (entityID int32, isBaby bool, variant int32, err error) {
	r := protocol.NewReader(data)
	entityID, err = r.VarInt()
	if err != nil {
		return 0, false, 0, err
	}
	for {
		idx, err := r.ReadByte()
		if err != nil {
			return 0, false, 0, err
		}
		if idx == 0xFF {
			return entityID, isBaby, variant, nil
		}
		ser, err := r.VarInt()
		if err != nil {
			return 0, false, 0, err
		}
		switch idx {
		case EntityDataAgeableBabyID:
			if ser != EntityDataSerializerBool {
				return 0, false, 0, errors.New("rabbit: unexpected baby serializer")
			}
			isBaby, err = r.Bool()
			if err != nil {
				return 0, false, 0, err
			}
		case EntityDataRabbitVariantID:
			if ser != EntityDataSerializerInt {
				return 0, false, 0, errors.New("rabbit: unexpected variant serializer")
			}
			variant, err = r.VarInt()
			if err != nil {
				return 0, false, 0, err
			}
		default:
			return 0, false, 0, errors.New("rabbit: unexpected entity data index")
		}
	}
}

// ResolveRabbitVariantForBiome resolves the vanilla 26.1.2 `Rabbit.Variant`
// (`0..5`) for `biomeName`:
//   - Snowy / frozen biomes: `80%` `RabbitVariantWhite` (`1`), `20%` `RabbitVariantWhiteSplotched` (`3`)
//   - Desert / badlands biomes: `100%` `RabbitVariantGold` (`4`)
//   - Other biomes: `50%` `RabbitVariantBrown` (`0`), `40%` `RabbitVariantSalt` (`5`), `10%` `RabbitVariantBlack` (`2`).
func ResolveRabbitVariantForBiome(biomeName string, rng *rand.Rand) int32 {
	switch canonicalResourceName(biomeName) {
	case "minecraft:snowy_plains", "minecraft:ice_spikes", "minecraft:snowy_taiga",
		"minecraft:snowy_beach", "minecraft:snowy_slopes", "minecraft:grove",
		"minecraft:frozen_peaks", "minecraft:jagged_peaks", "minecraft:frozen_river",
		"minecraft:frozen_ocean", "minecraft:deep_frozen_ocean":
		if rng != nil && rng.Intn(100) >= 80 {
			return RabbitVariantWhiteSplotched
		}
		return RabbitVariantWhite
	case "minecraft:desert", "minecraft:badlands", "minecraft:eroded_badlands", "minecraft:wooded_badlands":
		return RabbitVariantGold
	default:
		if rng == nil {
			return RabbitVariantBrown
		}
		r := rng.Intn(100)
		switch {
		case r < 50:
			return RabbitVariantBrown
		case r < 90:
			return RabbitVariantSalt
		default:
			return RabbitVariantBlack
		}
	}
}

// InheritRabbitVariant computes a bred baby rabbit's variant from its two parents
// (`47.5%` parent A, `47.5%` parent B, `5%` biome-based variant).
func InheritRabbitVariant(parentAVariant, parentBVariant int32, biomeName string, rng *rand.Rand) int32 {
	if rng == nil {
		return parentAVariant
	}
	r := rng.Float64()
	if r < 0.475 {
		return parentAVariant
	}
	if r < 0.95 {
		return parentBVariant
	}
	return ResolveRabbitVariantForBiome(biomeName, rng)
}

// IsRabbitFoodItem reports whether itemName is a valid rabbit temptation and
// breeding food (`minecraft:carrot`, `minecraft:golden_carrot`, `minecraft:dandelion`).
func IsRabbitFoodItem(itemName string) bool {
	switch canonicalResourceName(itemName) {
	case "minecraft:carrot", "minecraft:golden_carrot", "minecraft:dandelion":
		return true
	default:
		return false
	}
}

// SpawnRabbit spawns a `minecraft:rabbit` (`3.0 HP`) at `(x, y, z)` in `dimension`.
// Passing `variant < 0` automatically selects a biome-appropriate variant.
func (s *Server) SpawnRabbit(dimension string, x, y, z float64, baby bool, variant int32, rng *rand.Rand) (int32, error) {
	if s == nil || s.entities == nil {
		return 0, errors.New("server: nil entity manager")
	}
	normDim := world.NormalizeDimension(dimension)
	if variant < 0 {
		biome := "minecraft:plains"
		if cache := s.DimensionChunks(normDim); cache != nil {
			biome = inferBiomeAtPosition(cache, int(math.Floor(x)), int(math.Floor(y)), int(math.Floor(z)))
		}
		variant = ResolveRabbitVariantForBiome(biome, rng)
	}

	typeID := ResolveEntityTypeID(MobTypeRabbit)
	if typeID < 0 {
		typeID = EntityTypeRabbitID
	}
	id := s.entities.Add(&world.Entity{
		TypeID:    typeID,
		TypeName:  MobTypeRabbit,
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
	mem.health = DefaultRabbitMaxHealth
	mem.lastHealth = DefaultRabbitMaxHealth
	if baby {
		mem.ageTicks = BabyMobGrowthTicks
	}
	tr.mu.Unlock()

	cm := s.creakingRabbitMgr()
	cm.mu.Lock()
	cm.rabbits[id] = &RabbitState{
		EntityID:  id,
		Dimension: normDim,
		Variant:   variant,
	}
	cm.mu.Unlock()

	if ent := s.entities.Get(id); ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, ent))
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, PlaySetEntityDataCB,
			EncodeRabbitEntityDataPacket(id, baby, variant))
	}
	return id, nil
}

func (s *Server) ensureRabbitState(entityID int32) *RabbitState {
	if s == nil || entityID <= 0 {
		return nil
	}
	cm := s.creakingRabbitMgr()
	cm.mu.Lock()
	defer cm.mu.Unlock()
	rs, ok := cm.rabbits[entityID]
	if !ok || rs == nil {
		dim := world.DimensionOverworld
		if s.entities != nil {
			if ent := s.entities.Get(entityID); ent != nil {
				dim = ent.EffectiveDimension()
			}
		}
		rs = &RabbitState{
			EntityID:  entityID,
			Dimension: dim,
			Variant:   RabbitVariantBrown,
		}
		cm.rabbits[entityID] = rs
	}
	return rs
}

// GetRabbitState returns a snapshot of the RabbitState for entityID.
func (s *Server) GetRabbitState(entityID int32) (RabbitState, bool) {
	if s == nil || entityID <= 0 {
		return RabbitState{}, false
	}
	cm := s.creakingRabbitMgr()
	cm.mu.RLock()
	rs, ok := cm.rabbits[entityID]
	var out RabbitState
	if ok && rs != nil {
		out = *rs
	}
	cm.mu.RUnlock()
	return out, ok && rs != nil
}

// SetRabbitVariant updates a rabbit's variant (`0..5` or `99` for The Killer Bunny)
// and broadcasts `EncodeRabbitEntityDataPacket`.
func (s *Server) SetRabbitVariant(entityID int32, variant int32) bool {
	if s == nil || s.entities == nil || entityID <= 0 {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeRabbit {
		return false
	}
	rs := s.ensureRabbitState(entityID)
	cm := s.creakingRabbitMgr()
	cm.mu.Lock()
	rs.Variant = variant
	cm.mu.Unlock()

	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, PlaySetEntityDataCB,
		EncodeRabbitEntityDataPacket(entityID, s.IsMobBaby(entityID), variant))
	return true
}

// ApplyRabbitCustomName applies a custom Name Tag to a `minecraft:rabbit`:
//   - Naming a rabbit `"Toast"` activates the special Toast memorial skin (`IsToast = true`).
//   - Naming a rabbit `"The Killer Bunny"` sets its variant to `RabbitVariantEvil` (`99`).
func (s *Server) ApplyRabbitCustomName(entityID int32, customName string) bool {
	if s == nil || s.entities == nil || entityID <= 0 {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeRabbit {
		return false
	}
	trimmed := strings.TrimSpace(customName)
	rs := s.ensureRabbitState(entityID)
	cm := s.creakingRabbitMgr()
	cm.mu.Lock()
	rs.CustomName = trimmed
	if trimmed == "Toast" {
		rs.IsToast = true
	}
	if trimmed == "The Killer Bunny" {
		rs.Variant = RabbitVariantEvil
	}
	variant := rs.Variant
	cm.mu.Unlock()

	if trimmed == "The Killer Bunny" {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, PlaySetEntityDataCB,
			EncodeRabbitEntityDataPacket(entityID, s.IsMobBaby(entityID), variant))
	}
	return true
}

// InteractWithRabbit handles right-clicking a `minecraft:rabbit` with a Name Tag
// (`"Toast"` / `"The Killer Bunny"`) or rabbit food (`carrot`, `golden_carrot`, `dandelion`).
func (s *Server) InteractWithRabbit(session *PlayerSession, entityID int32, rng *rand.Rand) (handled bool, offspringID int32) {
	if s == nil || s.entities == nil || session == nil || session.IsDead() {
		return false, 0
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator || snap.Health <= 0 {
		return false, 0
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeRabbit {
		return false, 0
	}
	held := session.HeldItem()
	if held.IsEmpty() {
		return false, 0
	}
	heldID := canonicalResourceName(held.ResolvedID())

	// 1. Check Name Tag ("Toast" or "The Killer Bunny").
	if heldID == "minecraft:name_tag" {
		slotIdx := world.HotbarContainerOffset + int(snap.SelectedSlot)
		meta := GetSlotItemMeta(session, slotIdx)
		if meta.CustomName != "" {
			s.ApplyRabbitCustomName(entityID, meta.CustomName)
			consumeOneHeldItemInSurvival(session)
			return true, 0
		}
	}

	// 2. Check rabbit breeding / growth food (`carrot`, `golden_carrot`, `dandelion`).
	if !IsRabbitFoodItem(heldID) {
		return false, 0
	}
	if s.IsMobBaby(entityID) {
		fed, _ := s.FeedBabyMob(session, entityID, heldID)
		return fed, 0
	}
	if s.MobBreedCooldown(entityID) <= 0 && s.MobInLoveTicks(entityID) <= 0 {
		consumeOneHeldItemInSurvival(session)
		s.SetMobInLove(entityID, AnimalLoveDurationTicks, session)
		offspringID, _ = s.TryBreedMobWithNearbyMate(entityID, AnimalBreedRange, rng)
		return true, offspringID
	}
	return false, 0
}

// TryRabbitRaidCarrotsAt checks for a `minecraft:carrots` crop block at or
// adjacent to the rabbit with `entityID`; if found with `age > 0`, decrements
// `age` by 1 (or turns `age == 0` into `minecraft:air`) and sets `MoreCarrotTicks = 40`.
func (s *Server) TryRabbitRaidCarrotsAt(entityID int32) (raided bool, newAge int) {
	if s == nil || s.entities == nil || !s.MobGriefing() || entityID <= 0 {
		return false, -1
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeRabbit {
		return false, -1
	}
	dim := ent.EffectiveDimension()
	cache := s.DimensionChunks(dim)
	if cache == nil {
		return false, -1
	}
	bx := int(math.Floor(ent.X))
	by := int(math.Floor(ent.Y))
	bz := int(math.Floor(ent.Z))

	for dy := 0; dy <= 1; dy++ {
		for dx := -1; dx <= 1; dx++ {
			for dz := -1; dz <= 1; dz++ {
				cx, cy, cz := bx+dx, by+dy, bz+dz
				if cy < world.MinY || cy >= world.MinY+world.WorldHeight {
					continue
				}
				st := cache.GetBlock(cx, cy, cz)
				if world.BlockName(st) != "minecraft:carrots" {
					continue
				}
				_, props, _ := world.BlockStateProperties(st)
				age, err := strconv.Atoi(props["age"])
				if err != nil || age < 0 {
					continue
				}
				var nextState uint16
				if age <= 0 {
					nextState = world.StateAir
					newAge = 0
				} else {
					newAge = age - 1
					nextState, _ = world.BlockStateIDByProperties("minecraft:carrots", map[string]string{
						"age": strconv.Itoa(newAge),
					})
				}
				cache.SetBlock(cx, cy, cz, nextState)
				s.broadcastMobBlockUpdate(dim, cx, cy, cz, nextState)
				rs := s.ensureRabbitState(entityID)
				cm := s.creakingRabbitMgr()
				cm.mu.Lock()
				rs.MoreCarrotTicks = 40
				cm.mu.Unlock()
				return true, newAge
			}
		}
	}
	return false, -1
}

// OnRabbitDamaged triggers a 60-tick panic sprint (`RabbitPanicSpeed`) when a
// rabbit takes damage.
func (s *Server) OnRabbitDamaged(entityID int32, _ *PlayerSession, _ int32) {
	if s == nil || entityID <= 0 {
		return
	}
	rs := s.ensureRabbitState(entityID)
	if rs == nil {
		return
	}
	cm := s.creakingRabbitMgr()
	cm.mu.Lock()
	rs.PanicTicks = 60
	cm.mu.Unlock()
}

// OnRabbitKilled cleans up RabbitState when a rabbit dies.
func (s *Server) OnRabbitKilled(entityID int32) {
	if s == nil || entityID <= 0 {
		return
	}
	cm := s.creakingRabbitMgr()
	cm.mu.Lock()
	delete(cm.rabbits, entityID)
	cm.mu.Unlock()
}

// RollRabbitDeathLoot rolls vanilla 26.1.2 `minecraft:rabbit` death loot:
//   - `0..1` (`+ 0..lootingLevel`) `minecraft:rabbit_hide`
//   - `0..1` (`+ 0..lootingLevel`) `minecraft:rabbit` (`minecraft:cooked_rabbit` if `onFire`)
//   - `10%` (`+ 3% * lootingLevel`) `1x` `minecraft:rabbit_foot`.
func RollRabbitDeathLoot(rng *rand.Rand, lootingLevel int, onFire bool) []world.ItemStack {
	if lootingLevel < 0 {
		lootingLevel = 0
	}
	var out []world.ItemStack

	hideCount := int32(1)
	if rng != nil {
		hideCount = int32(rng.Intn(2)) // 0..1
		if lootingLevel > 0 {
			hideCount += int32(rng.Intn(lootingLevel + 1))
		}
	}
	if hideCount > 0 {
		if hide := world.NewNamedItemStack(RabbitHideItemID, hideCount); !hide.IsEmpty() {
			out = append(out, hide)
		}
	}

	meatCount := int32(1)
	if rng != nil {
		meatCount = int32(rng.Intn(2)) // 0..1
		if lootingLevel > 0 {
			meatCount += int32(rng.Intn(lootingLevel + 1))
		}
	}
	if meatCount > 0 {
		meatID := RawRabbitItemID
		if onFire {
			meatID = CookedRabbitItemID
		}
		if meat := world.NewNamedItemStack(meatID, meatCount); !meat.IsEmpty() {
			out = append(out, meat)
		}
	}

	footChance := 0.10 + 0.03*float64(lootingLevel)
	dropFoot := rng == nil || rng.Float64() < footChance
	if dropFoot {
		if foot := world.NewNamedItemStack(RabbitFootItemID, 1); !foot.IsEmpty() {
			out = append(out, foot)
		}
	}
	return out
}

// TickRabbits steps all live `minecraft:rabbit` entities by 1 server tick (20 TPS).
func (s *Server) TickRabbits(rng *rand.Rand) {
	if s == nil || s.entities == nil {
		return
	}
	cm := s.creakingRabbitMgr()
	cm.mu.RLock()
	if len(cm.rabbits) == 0 {
		cm.mu.RUnlock()
		return
	}
	ids := make([]int32, 0, len(cm.rabbits))
	for id := range cm.rabbits {
		ids = append(ids, id)
	}
	cm.mu.RUnlock()

	for _, id := range ids {
		s.tickSingleRabbit(id, rng)
	}
}

func (s *Server) tickSingleRabbit(entityID int32, rng *rand.Rand) {
	ent := s.entities.Get(entityID)
	cm := s.creakingRabbitMgr()
	cm.mu.Lock()
	rs, ok := cm.rabbits[entityID]
	if !ok || rs == nil || ent == nil {
		delete(cm.rabbits, entityID)
		cm.mu.Unlock()
		return
	}
	if rs.PanicTicks > 0 {
		rs.PanicTicks--
	}
	if rs.MoreCarrotTicks > 0 {
		rs.MoreCarrotTicks--
	}
	if rs.AttackCooldown > 0 {
		rs.AttackCooldown--
	}
	snap := *rs
	cm.mu.Unlock()

	dim := ent.EffectiveDimension()
	cache := s.DimensionChunks(dim)
	feetY := int(math.Floor(ent.Y + 0.01))

	// 1. The Killer Bunny (`Variant == 99`) aggressively chases & attacks players and wolves!
	if snap.Variant == RabbitVariantEvil && s.Difficulty() != DifficultyPeaceful {
		var bestPlayer *PlayerSession
		bestDistSq := RabbitKillerRange*RabbitKillerRange + 1.0
		for _, p := range s.onlinePlayersSnapshot() {
			if p == nil || p.IsDead() {
				continue
			}
			pSnap := p.Snapshot()
			if world.NormalizeDimension(pSnap.Dimension) != dim ||
				pSnap.GameMode == GameModeCreative || pSnap.GameMode == GameModeSpectator {
				continue
			}
			dx := pSnap.X - ent.X
			dy := pSnap.Y - ent.Y
			dz := pSnap.Z - ent.Z
			dSq := dx*dx + dy*dy + dz*dz
			if dSq <= RabbitKillerRange*RabbitKillerRange && dSq < bestDistSq {
				bestDistSq = dSq
				bestPlayer = p
			}
		}
		if bestPlayer != nil {
			pSnap := bestPlayer.Snapshot()
			s.stepEntityTowardPoint(ent, cache, feetY, pSnap.X, pSnap.Y, pSnap.Z, RabbitPanicSpeed, 1.0)
			if bestDistSq <= 4.0 && snap.AttackCooldown <= 0 {
				cm.mu.Lock()
				if cur := cm.rabbits[entityID]; cur != nil {
					cur.AttackCooldown = 15
				}
				cm.mu.Unlock()
				cp := ent.ChunkPos()
				s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
					EncodeSoundPacket(SoundRabbitAttack, SoundSourceHostile, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
				s.AttackPlayerFromMob(entityID, MobTypeRabbit, ent.X, ent.Z, bestPlayer, KillerBunnyAttackDamage)
			}
			return
		}
	}

	// 2. Flee from hostile wolves (`minecraft:wolf`) or foxes (`minecraft:fox`) within 10 blocks!
	allEnts := s.entities.AppendAll(nil)
	for i := range allEnts {
		other := &allEnts[i]
		if other.ID == entityID || other.EffectiveDimension() != dim {
			continue
		}
		onorm := canonicalResourceName(other.TypeName)
		if onorm != MobTypeWolf && onorm != MobTypeFox {
			continue
		}
		dx := ent.X - other.X
		dz := ent.Z - other.Z
		dist := math.Hypot(dx, dz)
		if dist <= RabbitFleeWolfRange && dist > 1e-4 {
			goalX := ent.X + (dx/dist)*4.0
			goalZ := ent.Z + (dz/dist)*4.0
			s.stepEntityTowardPoint(ent, cache, feetY, goalX, ent.Y, goalZ, RabbitFleeSpeed, 0.2)
			return
		}
	}

	// 3. Panic sprint when damaged (`PanicTicks > 0`).
	if snap.PanicTicks > 0 {
		ang := 0.5
		if rng != nil {
			ang = rng.Float64() * 2.0 * math.Pi
		}
		goalX := ent.X + math.Cos(ang)*3.0
		goalZ := ent.Z + math.Sin(ang)*3.0
		s.stepEntityTowardPoint(ent, cache, feetY, goalX, ent.Y, goalZ, RabbitPanicSpeed, 0.2)
		return
	}

	// 4. Check nearby players: follow if holding carrot/golden_carrot/dandelion (`Tempt`), otherwise flee!
	for _, p := range s.onlinePlayersSnapshot() {
		if p == nil || p.IsDead() {
			continue
		}
		pSnap := p.Snapshot()
		if world.NormalizeDimension(pSnap.Dimension) != dim || pSnap.GameMode == GameModeSpectator {
			continue
		}
		held := p.HeldItem()
		off := p.InventorySlot(OffHandContainerSlot)
		holdingFood := (!held.IsEmpty() && IsRabbitFoodItem(held.ResolvedID())) ||
			(!off.IsEmpty() && IsRabbitFoodItem(off.ResolvedID()))
		dx := pSnap.X - ent.X
		dz := pSnap.Z - ent.Z
		dist := math.Hypot(dx, dz)
		if holdingFood && dist <= RabbitTemptRange && dist > 1.5 {
			s.stepEntityTowardPoint(ent, cache, feetY, pSnap.X, pSnap.Y, pSnap.Z, RabbitTemptSpeed, 1.5)
			return
		}
		if !holdingFood && !p.IsSneaking() && pSnap.GameMode != GameModeCreative && dist <= RabbitFleePlayerRange && dist > 1e-4 {
			goalX := ent.X - (dx/dist)*4.0
			goalZ := ent.Z - (dz/dist)*4.0
			s.stepEntityTowardPoint(ent, cache, feetY, goalX, ent.Y, goalZ, RabbitFleeSpeed, 0.2)
			return
		}
	}

	// 5. Raid carrot crops when hungry (`MoreCarrotTicks == 0`).
	if snap.MoreCarrotTicks == 0 {
		s.TryRabbitRaidCarrotsAt(entityID)
	}
}

// ============================================================================
// 3. FOX & PANDA POLISH HELPERS (Totem in Mouth, Chorus Teleport, Lazy/Playful)
// ============================================================================

// TryFoxUseTotemOfUndying checks if the `minecraft:fox` with `entityID` is
// holding a `minecraft:totem_of_undying` in its mouth when taking fatal damage;
// if so, consumes the totem, sets health to `1.0 HP`, applies Regeneration II,
// Fire Resistance I, and Absorption II, broadcasts `EntityEventTotemActivated` (35),
// and returns `true` so the fox survives!
func (s *Server) TryFoxUseTotemOfUndying(entityID int32) bool {
	if s == nil || s.entities == nil || entityID <= 0 {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeFox {
		return false
	}
	mgr := s.wildPetMgr()
	mgr.mu.Lock()
	fs, ok := mgr.foxes[entityID]
	if !ok || fs == nil || fs.HeldItem.IsEmpty() || canonicalResourceName(fs.HeldItem.ResolvedID()) != TotemOfUndyingItemID {
		mgr.mu.Unlock()
		return false
	}
	fs.HeldItem = world.ItemStack{}
	fs.EatTicks = 0
	mgr.mu.Unlock()

	s.SetMobHealth(entityID, 1.0)
	s.ApplyEntityEffectScaled(entityID, NewStatusEffectInstance(EffectRegeneration, TotemRegenerationDuration, TotemRegenerationAmplifier), 1.0)
	s.ApplyEntityEffectScaled(entityID, NewStatusEffectInstance(EffectFireResistance, TotemFireResistanceDuration, TotemFireResistanceAmplifier), 1.0)
	s.ApplyEntityEffectScaled(entityID, NewStatusEffectInstance(EffectAbsorption, TotemAbsorptionDuration, TotemAbsorptionAmplifier), 1.0)

	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, protocol.PlayEntityEvent,
		EncodeEntityEventPacket(entityID, EntityEventTotemActivated))
	return true
}

// EatFoxHeldItem consumes the edible item currently held in the fox's mouth,
// playing `SoundFoxEat` and, if the item is `minecraft:chorus_fruit`, teleporting
// the fox to a nearby safe block within 8 blocks.
func (s *Server) EatFoxHeldItem(entityID int32, rng *rand.Rand) (ate bool, teleported bool) {
	if s == nil || s.entities == nil || entityID <= 0 {
		return false, false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeFox {
		return false, false
	}
	mgr := s.wildPetMgr()
	mgr.mu.Lock()
	fs, ok := mgr.foxes[entityID]
	if !ok || fs == nil || fs.HeldItem.IsEmpty() || !IsFoxEdibleMouthItem(fs.HeldItem.ResolvedID()) {
		mgr.mu.Unlock()
		return false, false
	}
	eatenID := canonicalResourceName(fs.HeldItem.ResolvedID())
	fs.HeldItem = world.ItemStack{}
	fs.EatTicks = 0
	mgr.mu.Unlock()

	dim := ent.EffectiveDimension()
	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundFoxEat, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))

	if curHP := s.MobHealth(entityID); curHP > 0 && curHP < DefaultFoxMaxHealth {
		newHP := curHP + 2.0
		if newHP > DefaultFoxMaxHealth {
			newHP = DefaultFoxMaxHealth
		}
		s.SetMobHealth(entityID, newHP)
	}

	if eatenID == "minecraft:chorus_fruit" {
		dx := 3.0
		dz := 2.0
		if rng != nil {
			dx = (rng.Float64()*2.0 - 1.0) * 6.0
			dz = (rng.Float64()*2.0 - 1.0) * 6.0
			if math.Abs(dx) < 1.0 {
				dx = 2.0
			}
		}
		nx := ent.X + dx
		nz := ent.Z + dz
		s.entities.Update(entityID, func(e *world.Entity) {
			e.X = nx
			e.Z = nz
		})
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
			EncodeSoundPacket(SoundChorusFruitTeleport, SoundSourceNeutral, nx, ent.Y, nz, 1.0, 1.0, 0))
		teleported = true
	}
	return true, teleported
}

// SetPandaLyingOnBack sets whether a Panda (`PandaGeneLazy`) is lying on its
// back (`PandaFlagOnBack = 0x10`) and broadcasts `EncodePandaEntityDataPacket`.
func (s *Server) SetPandaLyingOnBack(entityID int32, onBack bool) bool {
	if s == nil || s.entities == nil || entityID <= 0 {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypePanda {
		return false
	}
	ps := s.ensurePandaState(entityID)
	mgr := s.wildPetMgr()
	mgr.mu.Lock()
	if onBack {
		ps.Flags |= PandaFlagOnBack
	} else {
		ps.Flags &^= PandaFlagOnBack
	}
	mainG, hiddenG, flags := ps.MainGene, ps.HiddenGene, ps.Flags
	eat, sneeze, unhappy := ps.EatCounter, ps.SneezeCounter, ps.UnhappyCounter
	mgr.mu.Unlock()

	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, PlaySetEntityDataCB,
		EncodePandaEntityDataPacket(entityID, mainG, hiddenG, flags, eat, sneeze, unhappy))
	return true
}

// TriggerPandaRoll triggers a playful somersault roll (`PandaFlagRolling = 0x04`)
// on a `minecraft:panda` and broadcasts `EncodePandaEntityDataPacket`.
func (s *Server) TriggerPandaRoll(entityID int32) bool {
	if s == nil || s.entities == nil || entityID <= 0 {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypePanda {
		return false
	}
	ps := s.ensurePandaState(entityID)
	mgr := s.wildPetMgr()
	mgr.mu.Lock()
	ps.Flags |= PandaFlagRolling
	mainG, hiddenG, flags := ps.MainGene, ps.HiddenGene, ps.Flags
	eat, sneeze, unhappy := ps.EatCounter, ps.SneezeCounter, ps.UnhappyCounter
	mgr.mu.Unlock()

	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, PlaySetEntityDataCB,
		EncodePandaEntityDataPacket(entityID, mainG, hiddenG, flags, eat, sneeze, unhappy))
	return true
}

// RollPandaDeathLoot rolls vanilla 26.1.2 `minecraft:panda` death loot (`1x minecraft:bamboo`).
func RollPandaDeathLoot(_ *rand.Rand) []world.ItemStack {
	if bamboo := world.NewNamedItemStack("minecraft:bamboo", 1); !bamboo.IsEmpty() {
		return []world.ItemStack{bamboo}
	}
	return nil
}
