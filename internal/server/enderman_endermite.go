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
// ENDERMAN & ENDERMITE (VANILLA MINECRAFT 26.1.2, PROTOCOL 775)
// ============================================================================

const (
	MobTypeEnderman  = "minecraft:enderman"
	MobTypeEndermite = "minecraft:endermite"

	// Vanilla 26.1.2 BuiltInRegistries.ENTITY_TYPE numeric network IDs (protocol 775).
	EntityTypeEndermanID  int = 41
	EntityTypeEndermiteID int = 42

	// Default Max Health values (vanilla 26.1.2).
	DefaultEndermanMaxHealth  float32 = 40.0 // 20 hearts
	DefaultEndermiteMaxHealth float32 = 8.0  // 4 hearts

	// Enderman combat, movement, stare aggro, and teleport parameters.
	EndermanAttackDamage             float32 = 7.0
	EndermanAttackReach              float64 = 2.2
	EndermanAttackCooldownTicks      int     = 20
	EndermanWanderSpeed              float64 = 0.10
	EndermanChaseSpeed               float64 = 0.30
	EndermanStareRange               float64 = 64.0
	EndermanEyeHeight                float64 = 2.55
	EndermanHeight                   float64 = 2.9
	EndermanStareConeTolerance       float64 = 0.025
	EndermanTeleportHorizontalRange  int     = 32
	EndermanTeleportVerticalRange    int     = 16
	EndermanWaterDamage              float32 = 1.0
	EndermanWaterDamageIntervalTicks int     = 20
	EndermanAngerDurationTicks       int     = 400
	EndermanBlockActionCooldownTicks int     = 40

	// Endermite combat, movement, pearl spawn chance, and lifetime parameters.
	EndermiteAttackDamage          float32 = 2.0
	EndermiteAttackReach           float64 = 1.5
	EndermiteAttackCooldownTicks   int     = 20
	EndermiteWanderSpeed           float64 = 0.12
	EndermiteChaseSpeed            float64 = 0.25
	EndermiteAggroRange            float64 = 16.0
	EndermiteMaxLifetimeTicks      int     = 2400 // 2 minutes (2400 ticks) before despawn
	EndermiteEnderPearlSpawnChance float64 = 0.05 // 5% chance when an ender pearl lands

	// Experience orb rewards on death (vanilla 26.1.2).
	EndermanXPReward  int = 5
	EndermiteXPReward int = 3

	// SynchedEntityData indices for EnderMan in 26.1.2 (protocol 775):
	//   - Index 16: DATA_CARRY_STATE (OPTIONAL_BLOCK_STATE serializer = 15, VarInt blockStateID; 0 = empty)
	//   - Index 17: DATA_CREEPY (BOOLEAN serializer = 8)
	//   - Index 18: DATA_STARED_AT (BOOLEAN serializer = 8)
	EntityDataEndermanCarryStateID   byte  = 16
	EntityDataEndermanCreepyID       byte  = 17
	EntityDataEndermanStaredAtID     byte  = 18
	EntityDataSerializerOptBlockState int32 = 15

	// Sound events (vanilla 26.1.2).
	SoundEndermanAmbient  = "minecraft:entity.enderman.ambient"
	SoundEndermanHurt     = "minecraft:entity.enderman.hurt"
	SoundEndermanDeath    = "minecraft:entity.enderman.death"
	SoundEndermanTeleport = "minecraft:entity.enderman.teleport"
	SoundEndermanStare    = "minecraft:entity.enderman.stare"
	SoundEndermanScream   = "minecraft:entity.enderman.scream"
	SoundEndermiteAmbient = "minecraft:entity.endermite.ambient"
	SoundEndermiteHurt    = "minecraft:entity.endermite.hurt"
	SoundEndermiteDeath   = "minecraft:entity.endermite.death"
	SoundEndermiteStep    = "minecraft:entity.endermite.step"
)

// EndermanState tracks carried block state, stare/scream state, target player
// or Endermite, teleport counter, and timers for an active `minecraft:enderman`.
type EndermanState struct {
	EntityID            int32
	Dimension           string
	CarriedBlockState   uint16
	Creepy              bool
	StaredAt            bool
	TargetPlayer        *PlayerSession
	TargetMobEntityID   int32
	AngerTicks          int
	AttackCooldown      int
	WaterDamageTimer    int
	BlockActionCooldown int
	TeleportCount       int
	TickCounter         int
	LastRecordedHealth  float32
}

// EndermiteState tracks lifetime, persistence, player-spawned flag, and combat
// state for an active `minecraft:endermite`.
type EndermiteState struct {
	EntityID            int32
	Dimension           string
	LifetimeTicks       int
	PersistenceRequired bool
	PlayerSpawned       bool
	TargetPlayer        *PlayerSession
	AttackCooldown      int
	TickCounter         int
}

type endermanEndermiteManager struct {
	mu         sync.RWMutex
	endermen   map[int32]*EndermanState
	endermites map[int32]*EndermiteState
}

var serverEndermanManagers sync.Map // map[*Server]*endermanEndermiteManager

func (s *Server) endermanManager() *endermanEndermiteManager {
	if s == nil {
		return &endermanEndermiteManager{
			endermen:   make(map[int32]*EndermanState),
			endermites: make(map[int32]*EndermiteState),
		}
	}
	if v, ok := serverEndermanManagers.Load(s); ok {
		return v.(*endermanEndermiteManager)
	}
	mgr := &endermanEndermiteManager{
		endermen:   make(map[int32]*EndermanState),
		endermites: make(map[int32]*EndermiteState),
	}
	actual, _ := serverEndermanManagers.LoadOrStore(s, mgr)
	return actual.(*endermanEndermiteManager)
}

// EndermanOrEndermiteXPReward returns the vanilla 26.1.2 XP reward for Enderman (5)
// or Endermite (3), or 0 for other mob types.
func EndermanOrEndermiteXPReward(typeName string) int {
	switch canonicalResourceName(typeName) {
	case MobTypeEnderman:
		return EndermanXPReward
	case MobTypeEndermite:
		return EndermiteXPReward
	default:
		return 0
	}
}

// EncodeEndermanEntityDataPacket encodes a ClientboundSetEntityDataPacket (0x63)
// updating EnderMan.DATA_CARRY_STATE (index 16, OPTIONAL_BLOCK_STATE),
// DATA_CREEPY (index 17, BOOLEAN), and DATA_STARED_AT (index 18, BOOLEAN).
func EncodeEndermanEntityDataPacket(entityID int32, carriedBlockState uint16, creepy, staredAt bool) []byte {
	w := protocol.NewWriter(24)
	w.VarInt(entityID)
	w.Byte(EntityDataEndermanCarryStateID)
	w.VarInt(EntityDataSerializerOptBlockState)
	w.VarInt(int32(carriedBlockState))
	w.Byte(EntityDataEndermanCreepyID)
	w.VarInt(EntityDataSerializerBool)
	w.Bool(creepy)
	w.Byte(EntityDataEndermanStaredAtID)
	w.VarInt(EntityDataSerializerBool)
	w.Bool(staredAt)
	w.Byte(0xFF)
	return w.Bytes()
}

// DecodeEndermanEntityDataPacket decodes a ClientboundSetEntityDataPacket (0x63)
// produced by EncodeEndermanEntityDataPacket.
func DecodeEndermanEntityDataPacket(data []byte) (entityID int32, carriedBlockState uint16, creepy, staredAt bool, err error) {
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
			return entityID, carriedBlockState, creepy, staredAt, nil
		}
		ser, err := r.VarInt()
		if err != nil {
			return 0, 0, false, false, err
		}
		switch idx {
		case EntityDataEndermanCarryStateID:
			if ser != EntityDataSerializerOptBlockState {
				return 0, 0, false, false, errors.New("enderman: unexpected serializer for carry state")
			}
			rawState, err := r.VarInt()
			if err != nil {
				return 0, 0, false, false, err
			}
			if rawState < 0 || rawState > 0xFFFF {
				return 0, 0, false, false, errors.New("enderman: carry block state out of uint16 range")
			}
			carriedBlockState = uint16(rawState)
		case EntityDataEndermanCreepyID:
			if ser != EntityDataSerializerBool {
				return 0, 0, false, false, errors.New("enderman: unexpected serializer for creepy flag")
			}
			creepy, err = r.Bool()
			if err != nil {
				return 0, 0, false, false, err
			}
		case EntityDataEndermanStaredAtID:
			if ser != EntityDataSerializerBool {
				return 0, 0, false, false, errors.New("enderman: unexpected serializer for stared_at flag")
			}
			staredAt, err = r.Bool()
			if err != nil {
				return 0, 0, false, false, err
			}
		default:
			return 0, 0, false, false, errors.New("enderman: unexpected entity data index")
		}
	}
}

// SpawnEnderman spawns a `minecraft:enderman` at (x, y, z) in dimension,
// initializes its 40 HP health and SynchedEntityData, and broadcasts PlayAddEntity.
func (s *Server) SpawnEnderman(dimension string, x, y, z float64) (int32, error) {
	if s == nil || s.entities == nil {
		return 0, errors.New("server: nil entity manager")
	}
	normDim := world.NormalizeDimension(dimension)
	typeID := ResolveEntityTypeID(MobTypeEnderman)
	if typeID < 0 {
		typeID = EntityTypeEndermanID
	}
	id := s.entities.Add(&world.Entity{
		TypeID:    typeID,
		TypeName:  MobTypeEnderman,
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
	mem.health = DefaultEndermanMaxHealth
	mem.lastHealth = DefaultEndermanMaxHealth
	tr.mu.Unlock()

	mgr := s.endermanManager()
	mgr.mu.Lock()
	mgr.endermen[id] = &EndermanState{
		EntityID:            id,
		Dimension:           normDim,
		BlockActionCooldown: EndermanBlockActionCooldownTicks,
		LastRecordedHealth:  DefaultEndermanMaxHealth,
	}
	mgr.mu.Unlock()

	if ent := s.entities.Get(id); ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, ent))
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, PlaySetEntityDataCB,
			EncodeEndermanEntityDataPacket(id, 0, false, false))
	}
	return id, nil
}

// SpawnEndermite spawns a `minecraft:endermite` at (x, y, z) in dimension,
// initializes its 8 HP health and 2400-tick lifetime, and alerts nearby Endermen.
func (s *Server) SpawnEndermite(dimension string, x, y, z float64, playerSpawned bool) (int32, error) {
	if s == nil || s.entities == nil {
		return 0, errors.New("server: nil entity manager")
	}
	normDim := world.NormalizeDimension(dimension)
	typeID := ResolveEntityTypeID(MobTypeEndermite)
	if typeID < 0 {
		typeID = EntityTypeEndermiteID
	}
	id := s.entities.Add(&world.Entity{
		TypeID:    typeID,
		TypeName:  MobTypeEndermite,
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
	mem.health = DefaultEndermiteMaxHealth
	mem.lastHealth = DefaultEndermiteMaxHealth
	tr.mu.Unlock()

	mgr := s.endermanManager()
	mgr.mu.Lock()
	mgr.endermites[id] = &EndermiteState{
		EntityID:      id,
		Dimension:     normDim,
		PlayerSpawned: playerSpawned,
	}
	mgr.mu.Unlock()

	if ent := s.entities.Get(id); ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, ent))
	}
	s.AlertEndermenToEndermite(normDim, x, y, z, id)
	return id, nil
}

// GetEndermanState returns a snapshot of the EndermanState for entityID.
func (s *Server) GetEndermanState(entityID int32) (EndermanState, bool) {
	if s == nil {
		return EndermanState{}, false
	}
	mgr := s.endermanManager()
	mgr.mu.RLock()
	st, ok := mgr.endermen[entityID]
	var out EndermanState
	if ok && st != nil {
		out = *st
	}
	mgr.mu.RUnlock()
	return out, ok && st != nil
}

// GetEndermiteState returns a snapshot of the EndermiteState for entityID.
func (s *Server) GetEndermiteState(entityID int32) (EndermiteState, bool) {
	if s == nil {
		return EndermiteState{}, false
	}
	mgr := s.endermanManager()
	mgr.mu.RLock()
	st, ok := mgr.endermites[entityID]
	var out EndermiteState
	if ok && st != nil {
		out = *st
	}
	mgr.mu.RUnlock()
	return out, ok && st != nil
}

// IsPlayerWearingCarvedPumpkin reports whether session is wearing a
// `minecraft:carved_pumpkin` in the helmet slot (`ArmorSlotHead = 5`), which
// disguises the player's gaze from Endermen in vanilla 26.1.2.
func IsPlayerWearingCarvedPumpkin(session *PlayerSession) bool {
	if session == nil {
		return false
	}
	headStack := session.InventorySlot(ArmorSlotHead)
	if headStack.IsEmpty() {
		return false
	}
	return canonicalResourceName(headStack.ResolvedID()) == "minecraft:carved_pumpkin"
}

// IsPlayerStaringAtEnderman implements vanilla `EnderMan.isLookingAtMe(Player)`:
//   - Returns false if player is dead, in Creative/Spectator mode, or wearing
//     `minecraft:carved_pumpkin` on their head.
//   - Returns true when the player is within 64 blocks of the Enderman, their
//     look vector points within `0.025 / dist` of the Enderman's eye height
//     (`Y + 2.55`), and there is an unobstructed line of sight.
func (s *Server) IsPlayerStaringAtEnderman(session *PlayerSession, entityID int32) bool {
	if s == nil || s.entities == nil || session == nil || session.IsDead() {
		return false
	}
	if IsPlayerWearingCarvedPumpkin(session) {
		return false
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeCreative || snap.GameMode == GameModeSpectator || snap.Health <= 0 {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeEnderman {
		return false
	}
	dim := ent.EffectiveDimension()
	if world.NormalizeDimension(snap.Dimension) != dim {
		return false
	}

	playerEyeY := snap.Y + 1.62
	if session.IsSneaking() {
		playerEyeY = snap.Y + 1.27
	}
	endermanEyeY := ent.Y + EndermanEyeHeight

	dx := ent.X - snap.X
	dy := endermanEyeY - playerEyeY
	dz := ent.Z - snap.Z
	dist := math.Sqrt(dx*dx + dy*dy + dz*dz)
	if dist <= 1e-4 || dist > EndermanStareRange {
		return false
	}

	lx, ly, lz := ElytraLookVector(snap.Yaw, snap.Pitch)
	dot := lx*(dx/dist) + ly*(dy/dist) + lz*(dz/dist)
	threshold := 1.0 - (EndermanStareConeTolerance / dist)
	if dot <= threshold {
		return false
	}

	return s.hasLineOfSightBetween(s.DimensionChunks(dim), snap.X, playerEyeY, snap.Z, ent.X, endermanEyeY, ent.Z)
}

func (s *Server) hasLineOfSightBetween(cache *world.Cache, x0, y0, z0, x1, y1, z1 float64) bool {
	if cache == nil {
		return true
	}
	dx := x1 - x0
	dy := y1 - y0
	dz := z1 - z0
	dist := math.Sqrt(dx*dx + dy*dy + dz*dz)
	if dist <= 0.5 {
		return true
	}
	steps := int(math.Ceil(dist * 2.0))
	for i := 1; i < steps; i++ {
		t := float64(i) / float64(steps)
		bx := int(math.Floor(x0 + dx*t))
		by := int(math.Floor(y0 + dy*t))
		bz := int(math.Floor(z0 + dz*t))
		if by < world.MinY || by >= world.MinY+world.WorldHeight {
			continue
		}
		blk := cache.GetBlock(bx, by, bz)
		blkName := world.BlockName(blk)
		if !isPassableBlock(blk) && !strings.Contains(blkName, "glass") && !strings.Contains(blkName, "leaves") {
			return false
		}
	}
	return true
}

// ProvokeEndermanByStare angers the Enderman with entityID toward player due to
// eye contact, setting `Creepy = true` and `StaredAt = true`, broadcasting
// `minecraft:entity.enderman.stare` + `minecraft:entity.enderman.scream`, and
// syncing entity data.
func (s *Server) ProvokeEndermanByStare(entityID int32, player *PlayerSession) bool {
	if s == nil || s.entities == nil || player == nil || player.IsDead() {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeEnderman {
		return false
	}
	mgr := s.endermanManager()
	mgr.mu.Lock()
	st, ok := mgr.endermen[entityID]
	if !ok || st == nil {
		mgr.mu.Unlock()
		return false
	}
	firstStare := !st.StaredAt
	st.TargetPlayer = player
	st.TargetMobEntityID = 0
	st.Creepy = true
	st.StaredAt = true
	st.AngerTicks = EndermanAngerDurationTicks
	carried := st.CarriedBlockState
	mgr.mu.Unlock()

	dim := ent.EffectiveDimension()
	cp := ent.ChunkPos()
	if firstStare {
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
			EncodeSoundPacket(SoundEndermanStare, SoundSourceHostile, ent.X, ent.Y, ent.Z, 2.5, 1.0, 0))
	}
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundEndermanScream, SoundSourceHostile, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
		EncodeEndermanEntityDataPacket(entityID, carried, true, true))
	return true
}

// IsEndermanInWaterOrRain reports whether an Enderman at (x, y, z) in dimension
// is currently touching water (source/flowing water or water cauldron) or
// exposed to rain/thunderstorm under open sky in the Overworld.
func (s *Server) IsEndermanInWaterOrRain(dimension string, x, y, z float64) bool {
	if s == nil {
		return false
	}
	normDim := world.NormalizeDimension(dimension)
	cache := s.DimensionChunks(normDim)
	bx := int(math.Floor(x))
	by := int(math.Floor(y))
	bz := int(math.Floor(z))
	if cache != nil {
		if cache.FluidHeightAt(bx, by, bz) > 0 {
			return true
		}
		blk := cache.GetBlock(bx, by, bz)
		if blk == world.StateWater || world.BlockName(blk) == "minecraft:water_cauldron" {
			return true
		}
	}
	if normDim == world.DimensionOverworld && (s.IsRaining() || s.IsThundering()) {
		if s.HasOpenSkyAbove(cache, bx, by, bz) {
			return true
		}
	}
	return false
}

// CanEndermanTeleportTo reports whether (bx, by, bz) in cache is a valid dry
// 3-block-tall landing spot above a solid non-fluid block for an Enderman.
func (s *Server) CanEndermanTeleportTo(cache *world.Cache, bx, by, bz int) bool {
	if by <= world.MinY || by+2 >= world.MinY+world.WorldHeight {
		return false
	}
	if cache == nil {
		return by >= 64
	}
	below := cache.GetBlock(bx, by-1, bz)
	if isPassableBlock(below) || below == world.StateWater || world.BlockName(below) == "minecraft:lava" {
		return false
	}
	if cache.FluidHeightAt(bx, by-1, bz) > 0 {
		return false
	}
	for dy := 0; dy < 3; dy++ {
		blk := cache.GetBlock(bx, by+dy, bz)
		blkName := world.BlockName(blk)
		if !isPassableBlock(blk) || blk == world.StateWater || blkName == "minecraft:lava" || blkName == "minecraft:water_cauldron" {
			return false
		}
		if cache.FluidHeightAt(bx, by+dy, bz) > 0 {
			return false
		}
	}
	return true
}

// TeleportEndermanTo teleports the Enderman with entityID to (targetX, targetY, targetZ)
// if the destination block column is a valid dry solid surface, broadcasting
// `minecraft:entity.enderman.teleport` and emitting `GameEventTeleport`.
func (s *Server) TeleportEndermanTo(entityID int32, targetX, targetY, targetZ float64) bool {
	if s == nil || s.entities == nil {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeEnderman {
		return false
	}
	dim := ent.EffectiveDimension()
	cache := s.DimensionChunks(dim)
	bx := int(math.Floor(targetX))
	by := int(math.Floor(targetY))
	bz := int(math.Floor(targetZ))
	if !s.CanEndermanTeleportTo(cache, bx, by, bz) {
		return false
	}

	oldX, oldY, oldZ := ent.X, ent.Y, ent.Z
	oldCP := ent.ChunkPos()
	s.entities.Update(entityID, func(e *world.Entity) {
		e.X = targetX
		e.Y = float64(by)
		e.Z = targetZ
		e.VelocityX = 0
		e.VelocityY = 0
		e.VelocityZ = 0
		e.OnGround = true
	})

	mgr := s.endermanManager()
	mgr.mu.Lock()
	if st, ok := mgr.endermen[entityID]; ok && st != nil {
		st.TeleportCount++
	}
	mgr.mu.Unlock()

	newCP := world.ChunkPos{
		X: int32(int64(bx) >> 4),
		Z: int32(int64(bz) >> 4),
	}
	s.BroadcastDimensionChunk(dim, oldCP.X, oldCP.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundEndermanTeleport, SoundSourceHostile, oldX, oldY, oldZ, 1.0, 1.0, 0))
	s.BroadcastDimensionChunk(dim, newCP.X, newCP.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundEndermanTeleport, SoundSourceHostile, targetX, float64(by), targetZ, 1.0, 1.0, 0))
	s.EmitVibration(VibrationEvent{
		Dimension:      dim,
		Type:           GameEventTeleport,
		X:              targetX,
		Y:              float64(by),
		Z:              targetZ,
		SourceEntityID: entityID,
	})
	return true
}

// TeleportEndermanRandomly attempts to teleport the Enderman with entityID to a
// dry solid surface within 32 blocks horizontally and 16 blocks vertically.
func (s *Server) TeleportEndermanRandomly(entityID int32, rng *rand.Rand) (newX, newY, newZ float64, teleported bool) {
	if s == nil || s.entities == nil {
		return 0, 0, 0, false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeEnderman {
		return 0, 0, 0, false
	}
	dim := ent.EffectiveDimension()
	cache := s.DimensionChunks(dim)
	originBX := int(math.Floor(ent.X))
	originBY := int(math.Floor(ent.Y))
	originBZ := int(math.Floor(ent.Z))

	// 1. Try random candidate offsets (vanilla 32-block horizontal, 16-block vertical range).
	if rng != nil {
		for attempt := 0; attempt < 32; attempt++ {
			dx := rng.Intn(2*EndermanTeleportHorizontalRange+1) - EndermanTeleportHorizontalRange
			dz := rng.Intn(2*EndermanTeleportHorizontalRange+1) - EndermanTeleportHorizontalRange
			if dx == 0 && dz == 0 {
				dx = 4
			}
			dy := rng.Intn(2*EndermanTeleportVerticalRange+1) - EndermanTeleportVerticalRange
			candBX := originBX + dx
			candBY := originBY + dy
			candBZ := originBZ + dz

			// Drop down to the nearest solid ground in the column if starting in air.
			if cache != nil {
				for candBY > world.MinY+1 && isPassableBlock(cache.GetBlock(candBX, candBY-1, candBZ)) {
					candBY--
				}
			}
			tx := float64(candBX) + 0.5
			ty := float64(candBY)
			tz := float64(candBZ) + 0.5
			if !s.IsEndermanInWaterOrRain(dim, tx, ty, tz) && s.TeleportEndermanTo(entityID, tx, ty, tz) {
				return tx, ty, tz, true
			}
		}
	}

	// 2. Deterministic ring search around the Enderman so tests and compact arenas
	// reliably find a nearby dry solid block.
	offsets := [][2]int{
		{5, 0}, {-5, 0}, {0, 5}, {0, -5},
		{4, 4}, {-4, 4}, {4, -4}, {-4, -4},
		{3, 0}, {-3, 0}, {0, 3}, {0, -3},
		{6, 2}, {-6, 2}, {2, 6}, {2, -6},
		{8, 0}, {-8, 0}, {0, 8}, {0, -8},
	}
	for pass := 0; pass < 2; pass++ {
		allowRain := pass == 1
		for _, off := range offsets {
			candBX := originBX + off[0]
			candBZ := originBZ + off[1]
			for dy := 4; dy >= -4; dy-- {
				candBY := originBY + dy
				tx := float64(candBX) + 0.5
				ty := float64(candBY)
				tz := float64(candBZ) + 0.5
				if !allowRain && s.IsEndermanInWaterOrRain(dim, tx, ty, tz) {
					continue
				}
				if s.TeleportEndermanTo(entityID, tx, ty, tz) {
					return tx, ty, tz, true
				}
			}
		}
	}
	return ent.X, ent.Y, ent.Z, false
}

// IsWaterSplashProjectile reports whether projectileType represents a water
// splash/lingering bottle that damages Endermen rather than being dodged.
func IsWaterSplashProjectile(projectileType string) bool {
	norm := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(projectileType)), "minecraft:")
	switch norm {
	case "water_splash", "splash_water", "water_potion", "splash_potion_water", "lingering_potion_water":
		return true
	default:
		return false
	}
}

// TryEndermanDodgeProjectile attempts to make the Enderman with entityID dodge
// an incoming projectile by teleporting away before impact.
//   - Standard projectiles (`arrow`, `spectral_arrow`, `trident`, `snowball`,
//     `egg`, `ender_pearl`, `fireball`, `small_fireball`, `wind_charge`,
//     `breeze_wind_charge`, `llama_spit`) are dodged (`dodged = true`) and deal
//     0 damage to the Enderman.
//   - Water splash potions are NOT dodged (`dodged = false`); use
//     `OnWaterSplashHitEnderman` to deal 1.0 water damage + teleport.
func (s *Server) TryEndermanDodgeProjectile(entityID int32, projectileType string, rng *rand.Rand) (dodged bool) {
	if s == nil || s.entities == nil {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeEnderman {
		return false
	}
	if IsWaterSplashProjectile(projectileType) {
		return false
	}
	_, _, _, teleported := s.TeleportEndermanRandomly(entityID, rng)
	if !teleported {
		// Even if enclosed in a 1x3 box with no alternate landing spot, Enderman
		// is immune to projectile impact in vanilla (`EnderMan.hurt` returns false
		// for `DamageTypeTags.IS_PROJECTILE`).
		return true
	}
	return true
}

// OnWaterSplashHitEnderman applies 1.0 water-sensitivity damage to the Enderman
// with entityID when splashed by a water bottle and immediately teleports it away.
func (s *Server) OnWaterSplashHitEnderman(entityID int32, rng *rand.Rand) (dealt float32, teleported bool) {
	if s == nil || s.entities == nil {
		return 0, false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeEnderman {
		return 0, false
	}
	killed := s.applyDamageToMob(ent, EndermanWaterDamage, nil, 0, false, false)
	if killed {
		return EndermanWaterDamage, false
	}
	_, _, _, teleported = s.TeleportEndermanRandomly(entityID, rng)
	mgr := s.endermanManager()
	mgr.mu.Lock()
	if st, ok := mgr.endermen[entityID]; ok && st != nil {
		st.LastRecordedHealth = s.MobHealth(entityID)
	}
	mgr.mu.Unlock()
	return EndermanWaterDamage, teleported
}

// IsEndermanHoldableBlockName reports whether blockName belongs to the vanilla
// 26.1.2 `#minecraft:enderman_holdable` block tag.
func IsEndermanHoldableBlockName(blockName string) bool {
	norm := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(blockName)), "minecraft:")
	switch norm {
	case "dirt", "grass_block", "podzol", "coarse_dirt", "mycelium", "rooted_dirt",
		"mud", "muddy_mangrove_roots", "moss_block", "pale_moss_block",
		"sand", "red_sand", "gravel", "clay",
		"dandelion", "poppy", "blue_orchid", "allium", "azure_bluet",
		"red_tulip", "orange_tulip", "white_tulip", "pink_tulip",
		"oxeye_daisy", "cornflower", "lily_of_the_valley", "wither_rose",
		"torchflower", "closed_eyeblossom", "open_eyeblossom", "cactus_flower",
		"brown_mushroom", "red_mushroom", "crimson_fungus", "warped_fungus",
		"crimson_roots", "warped_roots", "crimson_nylium", "warped_nylium",
		"netherrack", "pumpkin", "carved_pumpkin", "melon", "cactus", "tnt":
		return true
	default:
		return false
	}
}

// IsEndermanHoldableBlock reports whether block state `state` belongs to the
// vanilla 26.1.2 `#minecraft:enderman_holdable` block tag.
func IsEndermanHoldableBlock(state uint16) bool {
	if state == world.StateAir {
		return false
	}
	return IsEndermanHoldableBlockName(world.BlockName(state))
}

// EndermanCarriedBlock returns the block state currently carried by the
// Enderman with entityID (or `world.StateAir` = 0 if carrying nothing).
func (s *Server) EndermanCarriedBlock(entityID int32) uint16 {
	st, ok := s.GetEndermanState(entityID)
	if !ok {
		return world.StateAir
	}
	return st.CarriedBlockState
}

// SetEndermanCarriedBlock sets the carried block state for the Enderman with
// entityID and broadcasts its updated SynchedEntityData packet.
func (s *Server) SetEndermanCarriedBlock(entityID int32, state uint16) bool {
	if s == nil || s.entities == nil {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeEnderman {
		return false
	}
	mgr := s.endermanManager()
	mgr.mu.Lock()
	st, ok := mgr.endermen[entityID]
	if !ok || st == nil {
		mgr.mu.Unlock()
		return false
	}
	st.CarriedBlockState = state
	creepy := st.Creepy
	staredAt := st.StaredAt
	mgr.mu.Unlock()

	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, PlaySetEntityDataCB,
		EncodeEndermanEntityDataPacket(entityID, state, creepy, staredAt))
	return true
}

// TryEndermanPickUpBlock attempts to make the Enderman with entityID pick up the
// `#minecraft:enderman_holdable` block at (bx, by, bz) in its dimension.
// Requires `mobGriefing=true` and that the Enderman is not already carrying a block.
func (s *Server) TryEndermanPickUpBlock(entityID int32, bx, by, bz int) bool {
	if s == nil || s.entities == nil || !s.MobGriefing() {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeEnderman {
		return false
	}
	dim := ent.EffectiveDimension()
	cache := s.DimensionChunks(dim)
	if cache == nil || by < world.MinY || by >= world.MinY+world.WorldHeight {
		return false
	}
	state := cache.GetBlock(bx, by, bz)
	if !IsEndermanHoldableBlock(state) {
		return false
	}

	mgr := s.endermanManager()
	mgr.mu.Lock()
	st, ok := mgr.endermen[entityID]
	if !ok || st == nil || st.CarriedBlockState != world.StateAir {
		mgr.mu.Unlock()
		return false
	}
	if valid, lc := cache.SetBlockWithLight(bx, by, bz, world.StateAir); valid {
		st.CarriedBlockState = state
		st.BlockActionCooldown = EndermanBlockActionCooldownTicks
		creepy := st.Creepy
		staredAt := st.StaredAt
		mgr.mu.Unlock()

		s.BroadcastDimensionBlocks(dim, []world.BlockChange{
			{Pos: world.BlockPos{X: bx, Y: by, Z: bz}, OldState: state, NewState: world.StateAir},
		}, lc)
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
			EncodeEndermanEntityDataPacket(entityID, state, creepy, staredAt))
		s.EmitVibration(VibrationEvent{
			Dimension:      dim,
			Type:           GameEventBlockDestroy,
			X:              float64(bx) + 0.5,
			Y:              float64(by) + 0.5,
			Z:              float64(bz) + 0.5,
			SourceEntityID: entityID,
		})
		return true
	}
	mgr.mu.Unlock()
	return false
}

// TryEndermanPlaceBlock attempts to make the Enderman with entityID place its
// carried block at (bx, by, bz) in its dimension.
// Requires `mobGriefing=true`, `CarriedBlockState != 0`, air at (bx, by, bz),
// and a solid non-fluid block at (bx, by-1, bz).
func (s *Server) TryEndermanPlaceBlock(entityID int32, bx, by, bz int) bool {
	if s == nil || s.entities == nil || !s.MobGriefing() {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeEnderman {
		return false
	}
	dim := ent.EffectiveDimension()
	cache := s.DimensionChunks(dim)
	if cache == nil || by <= world.MinY || by >= world.MinY+world.WorldHeight {
		return false
	}
	if cache.GetBlock(bx, by, bz) != world.StateAir {
		return false
	}
	below := cache.GetBlock(bx, by-1, bz)
	if isPassableBlock(below) || below == world.StateWater || world.BlockName(below) == "minecraft:lava" {
		return false
	}

	mgr := s.endermanManager()
	mgr.mu.Lock()
	st, ok := mgr.endermen[entityID]
	if !ok || st == nil || st.CarriedBlockState == world.StateAir {
		mgr.mu.Unlock()
		return false
	}
	carried := st.CarriedBlockState
	if valid, lc := cache.SetBlockWithLight(bx, by, bz, carried); valid {
		st.CarriedBlockState = world.StateAir
		st.BlockActionCooldown = EndermanBlockActionCooldownTicks
		creepy := st.Creepy
		staredAt := st.StaredAt
		mgr.mu.Unlock()

		s.BroadcastDimensionBlocks(dim, []world.BlockChange{
			{Pos: world.BlockPos{X: bx, Y: by, Z: bz}, OldState: world.StateAir, NewState: carried},
		}, lc)
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
			EncodeEndermanEntityDataPacket(entityID, world.StateAir, creepy, staredAt))
		s.EmitVibration(VibrationEvent{
			Dimension:      dim,
			Type:           GameEventBlockPlace,
			X:              float64(bx) + 0.5,
			Y:              float64(by) + 0.5,
			Z:              float64(bz) + 0.5,
			SourceEntityID: entityID,
		})
		return true
	}
	mgr.mu.Unlock()
	return false
}

// OnEndermanDamaged handles an Enderman taking melee or environmental damage:
//   - Provokes the Enderman against attacker (if non-nil survival/adventure player).
//   - Updates LastRecordedHealth so TickEndermenAndEndermites stays in sync.
func (s *Server) OnEndermanDamaged(entityID int32, attacker *PlayerSession, rng *rand.Rand) bool {
	if s == nil || s.entities == nil {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeEnderman {
		return false
	}
	mgr := s.endermanManager()
	mgr.mu.Lock()
	st, ok := mgr.endermen[entityID]
	if !ok || st == nil {
		mgr.mu.Unlock()
		return false
	}
	st.LastRecordedHealth = s.MobHealth(entityID)
	var syncNeeded bool
	var carried uint16
	var creepy, staredAt bool
	if attacker != nil && !attacker.IsDead() {
		aSnap := attacker.Snapshot()
		if aSnap.GameMode != GameModeCreative && aSnap.GameMode != GameModeSpectator {
			st.TargetPlayer = attacker
			st.TargetMobEntityID = 0
			st.Creepy = true
			st.AngerTicks = EndermanAngerDurationTicks
			carried = st.CarriedBlockState
			creepy = st.Creepy
			staredAt = st.StaredAt
			syncNeeded = true
		}
	}
	mgr.mu.Unlock()

	if syncNeeded {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, PlaySetEntityDataCB,
			EncodeEndermanEntityDataPacket(entityID, carried, creepy, staredAt))
	}
	return true
}

// OnEndermanKilled removes the Enderman's state on death and returns its
// carried block as an ItemStack drop (if it was holding a block).
func (s *Server) OnEndermanKilled(entityID int32) (carriedDrop world.ItemStack, hasCarriedDrop bool) {
	if s == nil {
		return world.ItemStack{}, false
	}
	mgr := s.endermanManager()
	mgr.mu.Lock()
	st, ok := mgr.endermen[entityID]
	delete(mgr.endermen, entityID)
	mgr.mu.Unlock()
	if !ok || st == nil || st.CarriedBlockState == world.StateAir {
		return world.ItemStack{}, false
	}
	blockName := world.BlockName(st.CarriedBlockState)
	if blockName == "" || blockName == "minecraft:air" {
		return world.ItemStack{}, false
	}
	if !strings.HasPrefix(blockName, "minecraft:") {
		blockName = "minecraft:" + blockName
	}
	drop := world.NewNamedItemStack(blockName, 1)
	return drop, !drop.IsEmpty()
}

// RollEndermanDeathLoot rolls vanilla 26.1.2 `minecraft:enderman` death loot:
//   - 0..1 `minecraft:ender_pearl` (+ 0..lootingLevel bonus; when rng == nil,
//     returns 1 + max(0, lootingLevel) pearls deterministically).
//   - Plus the carried block item if `carriedBlockState != world.StateAir`.
func RollEndermanDeathLoot(rng *rand.Rand, lootingLevel int, carriedBlockState uint16) []world.ItemStack {
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
	var out []world.ItemStack
	if count > 0 {
		if pearl := world.NewNamedItemStack("minecraft:ender_pearl", count); !pearl.IsEmpty() {
			out = append(out, pearl)
		}
	}
	if carriedBlockState != world.StateAir {
		blockName := world.BlockName(carriedBlockState)
		if blockName != "" && blockName != "minecraft:air" {
			if !strings.HasPrefix(blockName, "minecraft:") {
				blockName = "minecraft:" + blockName
			}
			if blkDrop := world.NewNamedItemStack(blockName, 1); !blkDrop.IsEmpty() {
				out = append(out, blkDrop)
			}
		}
	}
	return out
}

// ShouldSpawnEndermiteFromEnderPearlRoll reports whether a `[0.0, 1.0)` random
// roll triggers the vanilla 5% (`0.05`) Endermite spawn when an ender pearl lands.
func ShouldSpawnEndermiteFromEnderPearlRoll(roll float64) bool {
	return roll >= 0.0 && roll < EndermiteEnderPearlSpawnChance
}

// TrySpawnEndermiteFromEnderPearl evaluates the vanilla 26.1.2 5% (`0.05`)
// chance to spawn a player-spawned `minecraft:endermite` at (x, y, z) in
// dimension when an `minecraft:ender_pearl` lands/teleports.
func (s *Server) TrySpawnEndermiteFromEnderPearl(dimension string, x, y, z float64, rng *rand.Rand) (entityID int32, spawned bool) {
	if s == nil || s.entities == nil || s.Difficulty() == DifficultyPeaceful {
		return 0, false
	}
	if !s.DoMobSpawning() || !s.SpawnMonsters() {
		return 0, false
	}
	if rng == nil || !ShouldSpawnEndermiteFromEnderPearlRoll(rng.Float64()) {
		return 0, false
	}
	id, err := s.SpawnEndermite(dimension, x, y, z, true)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// AlertEndermenToEndermite causes all Endermen within 64 blocks of (x, y, z)
// in dimension to aggressively target the Endermite with endermiteID.
func (s *Server) AlertEndermenToEndermite(dimension string, x, y, z float64, endermiteID int32) int {
	if s == nil || s.entities == nil || endermiteID <= 0 {
		return 0
	}
	normDim := world.NormalizeDimension(dimension)
	rangeSq := EndermanStareRange * EndermanStareRange
	mgr := s.endermanManager()
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	alerted := 0
	for id, st := range mgr.endermen {
		if st == nil || st.Dimension != normDim {
			continue
		}
		ent := s.entities.Get(id)
		if ent == nil {
			continue
		}
		dx, dy, dz := ent.X-x, ent.Y-y, ent.Z-z
		if dx*dx+dy*dy+dz*dz <= rangeSq {
			st.TargetMobEntityID = endermiteID
			st.Creepy = true
			st.AngerTicks = EndermanAngerDurationTicks
			alerted++
		}
	}
	return alerted
}

// SetEndermitePersistenceRequired sets whether the Endermite with entityID is
// exempt from the 2400-tick (2-minute) lifetime despawn.
func (s *Server) SetEndermitePersistenceRequired(entityID int32, persistent bool) bool {
	if s == nil {
		return false
	}
	mgr := s.endermanManager()
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	st, ok := mgr.endermites[entityID]
	if !ok || st == nil {
		return false
	}
	st.PersistenceRequired = persistent
	return true
}

// SetEndermiteLifetimeTicks sets the current age in ticks of the Endermite with
// entityID (despawns at >= `EndermiteMaxLifetimeTicks = 2400` unless persistent).
func (s *Server) SetEndermiteLifetimeTicks(entityID int32, ticks int) bool {
	if s == nil {
		return false
	}
	mgr := s.endermanManager()
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	st, ok := mgr.endermites[entityID]
	if !ok || st == nil {
		return false
	}
	st.LifetimeTicks = ticks
	return true
}

// OnEndermiteDamaged aggros the hit Endermite and alerts nearby Endermites
// within 16 blocks against attacker.
func (s *Server) OnEndermiteDamaged(entityID int32, attacker *PlayerSession) {
	if s == nil || s.entities == nil || attacker == nil || attacker.IsDead() {
		return
	}
	aSnap := attacker.Snapshot()
	if aSnap.GameMode == GameModeCreative || aSnap.GameMode == GameModeSpectator {
		return
	}
	ent := s.entities.Get(entityID)
	if ent == nil {
		return
	}
	dim := ent.EffectiveDimension()
	rangeSq := EndermiteAggroRange * EndermiteAggroRange
	mgr := s.endermanManager()
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	for id, st := range mgr.endermites {
		if st == nil || st.Dimension != dim {
			continue
		}
		if id == entityID {
			st.TargetPlayer = attacker
			continue
		}
		if other := s.entities.Get(id); other != nil {
			dx, dy, dz := other.X-ent.X, other.Y-ent.Y, other.Z-ent.Z
			if dx*dx+dy*dy+dz*dz <= rangeSq && st.TargetPlayer == nil {
				st.TargetPlayer = attacker
			}
		}
	}
}

// OnEndermiteKilled cleans up state when an Endermite dies.
func (s *Server) OnEndermiteKilled(entityID int32) {
	if s == nil {
		return
	}
	mgr := s.endermanManager()
	mgr.mu.Lock()
	delete(mgr.endermites, entityID)
	for _, st := range mgr.endermen {
		if st != nil && st.TargetMobEntityID == entityID {
			st.TargetMobEntityID = 0
		}
	}
	mgr.mu.Unlock()
}

// TickEndermenAndEndermites steps all active `minecraft:enderman` and
// `minecraft:endermite` entities for one server tick (20 TPS).
func (s *Server) TickEndermenAndEndermites(rng *rand.Rand) {
	if s == nil || s.entities == nil {
		return
	}
	mgr := s.endermanManager()
	mgr.mu.RLock()
	if len(mgr.endermen) == 0 && len(mgr.endermites) == 0 {
		mgr.mu.RUnlock()
		return
	}
	endermanIDs := make([]int32, 0, len(mgr.endermen))
	for id := range mgr.endermen {
		endermanIDs = append(endermanIDs, id)
	}
	endermiteIDs := make([]int32, 0, len(mgr.endermites))
	for id := range mgr.endermites {
		endermiteIDs = append(endermiteIDs, id)
	}
	mgr.mu.RUnlock()

	if s.Difficulty() == DifficultyPeaceful {
		for _, id := range endermanIDs {
			if ent := s.entities.Get(id); ent != nil {
				cp := ent.ChunkPos()
				s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, protocol.PlayRemoveEntities,
					encodeRemoveEntitiesPacket(nil, []int32{id}))
			}
			s.entities.Remove(id)
		}
		for _, id := range endermiteIDs {
			if ent := s.entities.Get(id); ent != nil {
				cp := ent.ChunkPos()
				s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, protocol.PlayRemoveEntities,
					encodeRemoveEntitiesPacket(nil, []int32{id}))
			}
			s.entities.Remove(id)
		}
		mgr.mu.Lock()
		clear(mgr.endermen)
		clear(mgr.endermites)
		mgr.mu.Unlock()
		return
	}

	for _, id := range endermiteIDs {
		s.tickSingleEndermite(id, rng)
	}
	for _, id := range endermanIDs {
		s.tickSingleEnderman(id, rng)
	}
}

func (s *Server) tickSingleEnderman(entityID int32, rng *rand.Rand) {
	ent := s.entities.Get(entityID)
	mgr := s.endermanManager()
	mgr.mu.Lock()
	st, ok := mgr.endermen[entityID]
	if !ok || st == nil || ent == nil {
		delete(mgr.endermen, entityID)
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
	if st.BlockActionCooldown > 0 {
		st.BlockActionCooldown--
	}
	if st.AngerTicks > 0 {
		st.AngerTicks--
		if st.AngerTicks == 0 {
			st.TargetPlayer = nil
			st.TargetMobEntityID = 0
			st.Creepy = false
			st.StaredAt = false
		}
	}
	curHP := s.MobHealth(entityID)
	tookExternalDamage := curHP > 0 && curHP < st.LastRecordedHealth
	st.LastRecordedHealth = curHP
	snap := *st
	mgr.mu.Unlock()

	dim := ent.EffectiveDimension()

	// 1. If the Enderman just took external damage (e.g. splash water bottle), teleport away!
	if tookExternalDamage {
		s.TeleportEndermanRandomly(entityID, rng)
		ent = s.entities.Get(entityID)
		if ent == nil {
			return
		}
	}

	// 2. Water / Rain contact: deals 1.0 water damage and immediately teleports away.
	if s.IsEndermanInWaterOrRain(dim, ent.X, ent.Y, ent.Z) {
		if snap.WaterDamageTimer <= 0 {
			mgr.mu.Lock()
			if cur, ok := mgr.endermen[entityID]; ok && cur != nil {
				cur.WaterDamageTimer = EndermanWaterDamageIntervalTicks
			}
			mgr.mu.Unlock()
			killed := s.applyDamageToMob(ent, EndermanWaterDamage, nil, 0, false, false)
			if killed {
				s.OnEndermanKilled(entityID)
				return
			}
			mgr.mu.Lock()
			if cur, ok := mgr.endermen[entityID]; ok && cur != nil {
				cur.LastRecordedHealth = s.MobHealth(entityID)
			}
			mgr.mu.Unlock()
		}
		s.TeleportEndermanRandomly(entityID, rng)
		ent = s.entities.Get(entityID)
		if ent == nil {
			return
		}
	}

	// 3. Scan for player stare aggro if not currently targeting a player.
	if snap.TargetPlayer == nil || snap.TargetPlayer.IsDead() {
		for _, p := range s.onlinePlayersSnapshot() {
			if s.IsPlayerStaringAtEnderman(p, entityID) {
				s.ProvokeEndermanByStare(entityID, p)
				mgr.mu.RLock()
				if cur, ok := mgr.endermen[entityID]; ok && cur != nil {
					snap = *cur
				}
				mgr.mu.RUnlock()
				break
			}
		}
	}

	// 4. Scan for nearby Endermites within 64 blocks if not chasing a player.
	if (snap.TargetPlayer == nil || snap.TargetPlayer.IsDead()) && snap.TargetMobEntityID == 0 {
		if miteID := s.findNearestEndermite(dim, ent.X, ent.Y, ent.Z, EndermanStareRange); miteID > 0 {
			mgr.mu.Lock()
			if cur, ok := mgr.endermen[entityID]; ok && cur != nil {
				cur.TargetMobEntityID = miteID
				cur.Creepy = true
				cur.AngerTicks = EndermanAngerDurationTicks
				snap = *cur
			}
			mgr.mu.Unlock()
		}
	}

	// 5. Chase and attack target Endermite or target Player.
	if snap.TargetMobEntityID > 0 {
		miteEnt := s.entities.Get(snap.TargetMobEntityID)
		if miteEnt == nil || miteEnt.EffectiveDimension() != dim || canonicalResourceName(miteEnt.TypeName) != MobTypeEndermite {
			mgr.mu.Lock()
			if cur, ok := mgr.endermen[entityID]; ok && cur != nil {
				cur.TargetMobEntityID = 0
				cur.Creepy = false
			}
			mgr.mu.Unlock()
		} else {
			s.stepEndermanTowardPoint(entityID, ent, miteEnt.X, miteEnt.Y, miteEnt.Z, EndermanChaseSpeed)
			ent = s.entities.Get(entityID)
			if ent != nil && snap.AttackCooldown <= 0 {
				dx := miteEnt.X - ent.X
				dy := miteEnt.Y - ent.Y
				dz := miteEnt.Z - ent.Z
				if dx*dx+dy*dy+dz*dz <= EndermanAttackReach*EndermanAttackReach {
					killed := s.applyDamageToMob(miteEnt, EndermanAttackDamage, nil, 0.3, false, false)
					if killed {
						s.OnEndermiteKilled(miteEnt.ID)
					}
					mgr.mu.Lock()
					if cur, ok := mgr.endermen[entityID]; ok && cur != nil {
						cur.AttackCooldown = EndermanAttackCooldownTicks
					}
					mgr.mu.Unlock()
				}
			}
			return
		}
	}

	if snap.TargetPlayer != nil && !snap.TargetPlayer.IsDead() {
		pSnap := snap.TargetPlayer.Snapshot()
		if world.NormalizeDimension(pSnap.Dimension) != dim || pSnap.GameMode == GameModeCreative || pSnap.GameMode == GameModeSpectator {
			mgr.mu.Lock()
			if cur, ok := mgr.endermen[entityID]; ok && cur != nil {
				cur.TargetPlayer = nil
				cur.Creepy = false
				cur.StaredAt = false
			}
			mgr.mu.Unlock()
			return
		}
		s.stepEndermanTowardPoint(entityID, ent, pSnap.X, pSnap.Y, pSnap.Z, EndermanChaseSpeed)
		ent = s.entities.Get(entityID)
		if ent != nil && snap.AttackCooldown <= 0 {
			dx := pSnap.X - ent.X
			dy := pSnap.Y - ent.Y
			dz := pSnap.Z - ent.Z
			if dx*dx+dy*dy+dz*dz <= EndermanAttackReach*EndermanAttackReach {
				s.AttackPlayerFromMob(entityID, MobTypeEnderman, ent.X, ent.Z, snap.TargetPlayer, EndermanAttackDamage)
				mgr.mu.Lock()
				if cur, ok := mgr.endermen[entityID]; ok && cur != nil {
					cur.AttackCooldown = EndermanAttackCooldownTicks
				}
				mgr.mu.Unlock()
			}
		}
		return
	}

	// 6. Idle AI: occasional block pickup or placement when mobGriefing=true.
	if snap.BlockActionCooldown <= 0 && s.MobGriefing() && rng != nil {
		bx := int(math.Floor(ent.X)) + rng.Intn(3) - 1
		by := int(math.Floor(ent.Y)) + rng.Intn(3) - 1
		bz := int(math.Floor(ent.Z)) + rng.Intn(3) - 1
		if snap.CarriedBlockState == world.StateAir {
			if rng.Intn(20) == 0 {
				s.TryEndermanPickUpBlock(entityID, bx, by, bz)
			}
		} else {
			if rng.Intn(80) == 0 {
				s.TryEndermanPlaceBlock(entityID, bx, by, bz)
			}
		}
	}
}

func (s *Server) stepEndermanTowardPoint(entityID int32, ent *world.Entity, goalX, goalY, goalZ, speed float64) {
	dx := goalX - ent.X
	dz := goalZ - ent.Z
	dist := math.Hypot(dx, dz)
	if dist <= 1e-4 {
		return
	}
	step := speed
	if step > dist {
		step = dist
	}
	vx := (dx / dist) * step
	vz := (dz / dist) * step
	nextX := ent.X + vx
	nextZ := ent.Z + vz
	nextY := ent.Y
	curFeetY := int(math.Floor(ent.Y + 0.01))
	if cache := s.DimensionChunks(ent.EffectiveDimension()); cache != nil {
		if nx, ny, nz, ok := tryApplyHorizontalStep(cache, ent.X, curFeetY, ent.Z, nextX, nextZ); ok {
			nextX, nextY, nextZ = nx, float64(ny), nz
		}
	}
	yaw := yawToward(dx, dz)
	s.entities.Update(entityID, func(e *world.Entity) {
		e.X = nextX
		e.Y = nextY
		e.Z = nextZ
		e.Yaw = yaw
		e.HeadYaw = yaw
		e.VelocityX = clampVelocityShort(vx)
		e.VelocityZ = clampVelocityShort(vz)
		e.OnGround = true
	})
}

func (s *Server) findNearestEndermite(dim string, x, y, z, maxRange float64) int32 {
	mgr := s.endermanManager()
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()
	bestDistSq := maxRange * maxRange
	var bestID int32
	for id, st := range mgr.endermites {
		if st == nil || st.Dimension != dim {
			continue
		}
		ent := s.entities.Get(id)
		if ent == nil {
			continue
		}
		dx, dy, dz := ent.X-x, ent.Y-y, ent.Z-z
		dSq := dx*dx + dy*dy + dz*dz
		if dSq <= bestDistSq {
			bestDistSq = dSq
			bestID = id
		}
	}
	return bestID
}

func (s *Server) tickSingleEndermite(entityID int32, rng *rand.Rand) {
	ent := s.entities.Get(entityID)
	mgr := s.endermanManager()
	mgr.mu.Lock()
	st, ok := mgr.endermites[entityID]
	if !ok || st == nil || ent == nil {
		delete(mgr.endermites, entityID)
		mgr.mu.Unlock()
		return
	}
	st.TickCounter++
	st.LifetimeTicks++
	if st.AttackCooldown > 0 {
		st.AttackCooldown--
	}
	snap := *st
	mgr.mu.Unlock()

	dim := ent.EffectiveDimension()

	// 1. Vanilla 2400-tick (2-minute) lifetime despawn unless PersistenceRequired.
	if !snap.PersistenceRequired && snap.LifetimeTicks >= EndermiteMaxLifetimeTicks {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayRemoveEntities,
			encodeRemoveEntitiesPacket(nil, []int32{entityID}))
		s.entities.Remove(entityID)
		s.OnEndermiteKilled(entityID)
		return
	}

	// 2. Acquire nearest survival/adventure player within 16 blocks.
	target := snap.TargetPlayer
	if target == nil || target.IsDead() || world.NormalizeDimension(target.Dimension()) != dim ||
		target.Snapshot().GameMode == GameModeCreative || target.Snapshot().GameMode == GameModeSpectator {
		target = s.findNearestWitherTarget(dim, ent.X, ent.Y, ent.Z, EndermiteAggroRange)
		mgr.mu.Lock()
		if cur, ok := mgr.endermites[entityID]; ok && cur != nil {
			cur.TargetPlayer = target
		}
		mgr.mu.Unlock()
	}

	if target == nil || target.IsDead() {
		return
	}

	tSnap := target.Snapshot()
	s.stepEndermanTowardPoint(entityID, ent, tSnap.X, tSnap.Y, tSnap.Z, EndermiteChaseSpeed)
	ent = s.entities.Get(entityID)
	if ent != nil && snap.AttackCooldown <= 0 {
		dx := tSnap.X - ent.X
		dy := tSnap.Y - ent.Y
		dz := tSnap.Z - ent.Z
		if dx*dx+dy*dy+dz*dz <= EndermiteAttackReach*EndermiteAttackReach {
			s.AttackPlayerFromMob(entityID, MobTypeEndermite, ent.X, ent.Z, target, EndermiteAttackDamage)
			mgr.mu.Lock()
			if cur, ok := mgr.endermites[entityID]; ok && cur != nil {
				cur.AttackCooldown = EndermiteAttackCooldownTicks
			}
			mgr.mu.Unlock()
		}
	}
}
