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

// Frog (minecraft:frog), Slime (minecraft:slime), Magma Cube (minecraft:magma_cube),
// and Frogspawn (minecraft:frogspawn) constants, variants, SynchedEntityData
// indices, and sounds for Minecraft 26.1.2 (protocol 775).
const (
	MobTypeFrog      = "minecraft:frog"
	EntityTypeFrogID = 55

	MobTypeSlime      = "minecraft:slime"
	EntityTypeSlimeID = 117

	MobTypeMagmaCube      = "minecraft:magma_cube"
	EntityTypeMagmaCubeID = 80

	DefaultFrogMaxHealth      float32 = 10.0
	DefaultSmallSlimeHealth   float32 = 1.0
	FrogWanderSpeed           float64 = 0.08
	FrogChaseSpeed            float64 = 0.11
	FrogTongueRange           float64 = 4.0
	FrogPreyChaseRange        float64 = 10.0
	FrogTongueCooldownTicks   int     = 40
	FrogTongueAnimTicks       int     = 15
	FrogJumpCooldownMinTicks  int     = 60
	FrogJumpCooldownMaxTicks  int     = 140
	FrogJumpVerticalImpulse   float64 = 0.42
	FrogJumpHorizontalImpulse float64 = 0.35
	FrogFallDamageReduction   float32 = 5.0

	// minecraft:frog_variant synchronized registry indices (synced_registries.json order).
	FrogVariantCold      int32 = 0 // "minecraft:cold" (Green Frog -> verdant_froglight)
	FrogVariantTemperate int32 = 1 // "minecraft:temperate" (Orange Frog -> ochre_froglight)
	FrogVariantWarm      int32 = 2 // "minecraft:warm" (White Frog -> pearlescent_froglight)

	// Items & blocks associated with Frogs, Slimes, and Magma Cubes.
	SlimeBallItemID            = "minecraft:slime_ball"
	MagmaCreamItemID           = "minecraft:magma_cream"
	OchreFroglightItemID       = "minecraft:ochre_froglight"
	VerdantFroglightItemID     = "minecraft:verdant_froglight"
	PearlescentFroglightItemID = "minecraft:pearlescent_froglight"
	FrogspawnBlockName         = "minecraft:frogspawn"
	FrogspawnItemID            = "minecraft:frogspawn"

	// Frogspawn hatching constants (in ticks at 20 TPS).
	FrogspawnMinHatchTicks    = 3600  // 3 minutes
	FrogspawnMaxHatchTicks    = 12000 // 10 minutes
	FrogspawnMinTadpoles      = 2
	FrogspawnMaxTadpoles      = 5

	// SynchedEntityData indices and serializers for Frog in Minecraft 26.1.2 (protocol 775).
	// Frog extends Animal -> AgeableMob (16) -> PathfinderMob (15) -> Mob -> LivingEntity -> Entity.
	EntityDataFrogVariantID               byte  = 17 // Frog.DATA_VARIANT_ID (FROG_VARIANT serializer = 22)
	EntityDataFrogTongueTargetID          byte  = 18 // Frog.DATA_TONGUE_TARGET_ID (OPTIONAL_UNSIGNED_INT serializer = 20)
	EntityDataSerializerOptionalUnsigned  int32 = 20
	EntityDataSerializerFrogVariant       int32 = 22

	// Canonical Frog sounds.
	SoundFrogAmbient  = "minecraft:entity.frog.ambient"
	SoundFrogEat      = "minecraft:entity.frog.eat"
	SoundFrogHurt     = "minecraft:entity.frog.hurt"
	SoundFrogDeath    = "minecraft:entity.frog.death"
	SoundFrogLongJump = "minecraft:entity.frog.long_jump"
	SoundFrogStep     = "minecraft:entity.frog.step"
	SoundFrogTongue   = "minecraft:entity.frog.tongue"
	SoundFrogLaySpawn = "minecraft:entity.frog.lay_spawn"
	SoundFrogspawnHatch = "minecraft:block.frogspawn.hatch"
)

// FrogBehaviorState holds the server-side AI, variant, tongue-attack, jumping,
// and gravid (frogspawn-laying) state for a `minecraft:frog` entity.
type FrogBehaviorState struct {
	EntityID            int32
	Variant             int32
	TongueTargetID      int32
	TongueAnimTicks     int
	TongueCooldownTicks int
	JumpCooldownTicks   int
	CroakTicks          int
	Pose                byte
	Gravid              bool
	LastDroppedItemID   string
}

type frogspawnPosKey struct {
	dim     string
	x, y, z int
}

// FrogspawnTrackerState tracks a placed `minecraft:frogspawn` block waiting to hatch into tadpoles.
type FrogspawnTrackerState struct {
	Dimension      string
	X, Y, Z        int
	RemainingTicks int
}

type serverFrogManager struct {
	mu         sync.RWMutex
	frogs      map[int32]*FrogBehaviorState
	slimeSizes map[int32]int
	frogspawns map[frogspawnPosKey]*FrogspawnTrackerState
}

var frogManagers sync.Map // map[*Server]*serverFrogManager

func (s *Server) frogManager() *serverFrogManager {
	if s == nil {
		return &serverFrogManager{
			frogs:      make(map[int32]*FrogBehaviorState),
			slimeSizes: make(map[int32]int),
			frogspawns: make(map[frogspawnPosKey]*FrogspawnTrackerState),
		}
	}
	if v, ok := frogManagers.Load(s); ok {
		return v.(*serverFrogManager)
	}
	mgr := &serverFrogManager{
		frogs:      make(map[int32]*FrogBehaviorState),
		slimeSizes: make(map[int32]int),
		frogspawns: make(map[frogspawnPosKey]*FrogspawnTrackerState),
	}
	actual, _ := frogManagers.LoadOrStore(s, mgr)
	return actual.(*serverFrogManager)
}

// ResolveFrogVariantForBiome maps a biome identifier to the corresponding
// vanilla 26.1.2 `minecraft:frog_variant` ID:
//   - Cold / snowy / end biomes -> FrogVariantCold (0, green frog)
//   - Warm / desert / jungle / savanna / badlands / mangrove / nether biomes -> FrogVariantWarm (2, white frog)
//   - Temperate / default biomes -> FrogVariantTemperate (1, orange frog)
func ResolveFrogVariantForBiome(biomeName string) int32 {
	b := strings.ToLower(strings.TrimSpace(biomeName))
	b = strings.TrimPrefix(b, "minecraft:")
	switch {
	case strings.Contains(b, "snow"),
		strings.Contains(b, "ice"),
		strings.Contains(b, "frozen"),
		strings.Contains(b, "jagged_peaks"),
		b == "grove",
		strings.Contains(b, "deep_dark"),
		strings.Contains(b, "end"):
		return FrogVariantCold
	case strings.Contains(b, "desert"),
		strings.Contains(b, "jungle"),
		strings.Contains(b, "savanna"),
		strings.Contains(b, "badlands"),
		strings.Contains(b, "mangrove"),
		strings.Contains(b, "warm_ocean"),
		strings.Contains(b, "lukewarm_ocean"),
		strings.Contains(b, "nether"),
		strings.Contains(b, "crimson"),
		strings.Contains(b, "warped"),
		strings.Contains(b, "soul_sand"),
		strings.Contains(b, "basalt"):
		return FrogVariantWarm
	default:
		return FrogVariantTemperate
	}
}

// FrogVariantName returns the canonical resource identifier for a Frog variant ID.
func FrogVariantName(variant int32) string {
	switch variant {
	case FrogVariantCold:
		return "minecraft:cold"
	case FrogVariantWarm:
		return "minecraft:warm"
	default:
		return "minecraft:temperate"
	}
}

// FroglightItemForVariant returns the froglight item dropped when a Frog of
// variant eats a small magma_cube in vanilla 26.1.2:
//   - FrogVariantCold (0)      -> "minecraft:verdant_froglight"
//   - FrogVariantTemperate (1) -> "minecraft:ochre_froglight"
//   - FrogVariantWarm (2)      -> "minecraft:pearlescent_froglight"
func FroglightItemForVariant(variant int32) string {
	switch variant {
	case FrogVariantCold:
		return VerdantFroglightItemID
	case FrogVariantWarm:
		return PearlescentFroglightItemID
	default:
		return OchreFroglightItemID
	}
}

func (s *Server) ensureFrogState(entityID int32, defaultVariant int32) *FrogBehaviorState {
	if s == nil || entityID <= 0 {
		return nil
	}
	if defaultVariant < FrogVariantCold || defaultVariant > FrogVariantWarm {
		defaultVariant = FrogVariantTemperate
	}
	mgr := s.frogManager()
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	st, ok := mgr.frogs[entityID]
	if !ok || st == nil {
		st = &FrogBehaviorState{
			EntityID:          entityID,
			Variant:           defaultVariant,
			JumpCooldownTicks: FrogJumpCooldownMinTicks,
			Pose:              PoseStanding,
		}
		mgr.frogs[entityID] = st
	}
	return st
}

// GetFrogState returns a snapshot of the FrogBehaviorState for entityID.
func (s *Server) GetFrogState(entityID int32) (FrogBehaviorState, bool) {
	if s == nil || entityID <= 0 {
		return FrogBehaviorState{}, false
	}
	mgr := s.frogManager()
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()
	st, ok := mgr.frogs[entityID]
	if !ok || st == nil {
		return FrogBehaviorState{}, false
	}
	return *st, true
}

// SetFrogVariant sets the variant (FrogVariantCold, FrogVariantTemperate, or
// FrogVariantWarm) on the Frog with entityID and broadcasts its updated metadata.
func (s *Server) SetFrogVariant(entityID int32, variant int32) bool {
	if s == nil || s.entities == nil {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeFrog {
		return false
	}
	if variant < FrogVariantCold || variant > FrogVariantWarm {
		variant = FrogVariantTemperate
	}
	st := s.ensureFrogState(entityID, variant)
	mgr := s.frogManager()
	mgr.mu.Lock()
	st.Variant = variant
	snap := *st
	mgr.mu.Unlock()
	s.broadcastFrogState(ent, snap)
	return true
}

// EncodeFrogEntityDataPacket encodes a ClientboundSetEntityDataPacket (0x63)
// for a `minecraft:frog` entity with its Pose (index 6), Variant (index 17,
// FROG_VARIANT serializer 22), and TongueTargetID (index 18,
// OPTIONAL_UNSIGNED_INT serializer 20: 0 if none, targetEntityID+1 if present).
func EncodeFrogEntityDataPacket(entityID int32, pose byte, variant int32, tongueTargetID int32) []byte {
	if variant < FrogVariantCold || variant > FrogVariantWarm {
		variant = FrogVariantTemperate
	}
	var optTarget int32
	if tongueTargetID > 0 {
		optTarget = tongueTargetID + 1
	}
	w := protocol.NewWriter(24)
	w.VarInt(entityID)
	w.Byte(EntityDataPoseID)
	w.VarInt(EntityDataSerializerPose)
	w.VarInt(int32(pose))
	w.Byte(EntityDataFrogVariantID)
	w.VarInt(EntityDataSerializerFrogVariant)
	w.VarInt(variant)
	w.Byte(EntityDataFrogTongueTargetID)
	w.VarInt(EntityDataSerializerOptionalUnsigned)
	w.VarInt(optTarget)
	w.Byte(0xFF)
	return w.Bytes()
}

// DecodeFrogEntityDataPacket decodes a ClientboundSetEntityDataPacket (0x63)
// produced by EncodeFrogEntityDataPacket.
func DecodeFrogEntityDataPacket(r *protocol.Reader) (entityID int32, pose byte, variant int32, tongueTargetID int32, err error) {
	entityID, err = r.VarInt()
	if err != nil {
		return 0, 0, 0, 0, err
	}
	for {
		idx, err := r.ReadByte()
		if err != nil {
			return 0, 0, 0, 0, err
		}
		if idx == 0xFF {
			break
		}
		ser, err := r.VarInt()
		if err != nil {
			return 0, 0, 0, 0, err
		}
		switch idx {
		case EntityDataPoseID:
			if ser != EntityDataSerializerPose {
				return 0, 0, 0, 0, errors.New("frog: unexpected DATA_POSE serializer")
			}
			v, err := r.VarInt()
			if err != nil {
				return 0, 0, 0, 0, err
			}
			pose = byte(v)
		case EntityDataFrogVariantID:
			if ser != EntityDataSerializerFrogVariant {
				return 0, 0, 0, 0, errors.New("frog: unexpected DATA_VARIANT_ID serializer")
			}
			variant, err = r.VarInt()
			if err != nil {
				return 0, 0, 0, 0, err
			}
		case EntityDataFrogTongueTargetID:
			if ser != EntityDataSerializerOptionalUnsigned {
				return 0, 0, 0, 0, errors.New("frog: unexpected DATA_TONGUE_TARGET_ID serializer")
			}
			opt, err := r.VarInt()
			if err != nil {
				return 0, 0, 0, 0, err
			}
			if opt > 0 {
				tongueTargetID = opt - 1
			}
		default:
			return 0, 0, 0, 0, errors.New("frog: unexpected SynchedEntityData index")
		}
	}
	return entityID, pose, variant, tongueTargetID, nil
}

func (s *Server) broadcastFrogState(ent *world.Entity, st FrogBehaviorState) {
	if s == nil || ent == nil {
		return
	}
	cp := ent.ChunkPos()
	dim := ent.EffectiveDimension()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
		EncodeFrogEntityDataPacket(ent.ID, st.Pose, st.Variant, st.TongueTargetID))
}

// SpawnFrog spawns a `minecraft:frog` at (x, y, z) in dimension with variant
// (or biome-resolved variant if variant < 0) and broadcasts its creation and metadata.
func (s *Server) SpawnFrog(dimension string, x, y, z float64, variant int32) (int32, error) {
	if s == nil || s.entities == nil {
		return 0, errors.New("server: nil server or entity manager")
	}
	normDim := world.NormalizeDimension(dimension)
	if variant < FrogVariantCold || variant > FrogVariantWarm {
		if normDim == world.DimensionNether {
			variant = FrogVariantWarm
		} else if normDim == world.DimensionEnd {
			variant = FrogVariantCold
		} else {
			variant = FrogVariantTemperate
		}
	}
	typeID := ResolveEntityTypeID(MobTypeFrog)
	if typeID < 0 {
		typeID = EntityTypeFrogID
	}
	id := s.entities.Add(&world.Entity{
		TypeID:    typeID,
		TypeName:  MobTypeFrog,
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
	mem.health = DefaultFrogMaxHealth
	mem.lastHealth = DefaultFrogMaxHealth
	tr.mu.Unlock()

	st := s.ensureFrogState(id, variant)
	if ent := s.entities.Get(id); ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, ent))
		if st != nil {
			s.broadcastFrogState(ent, *st)
		}
	}
	return id, nil
}

// SpawnSlime spawns a `minecraft:slime` at (x, y, z) in dimension with the
// given size (1 = small, 2 = medium, 4 = big).
func (s *Server) SpawnSlime(dimension string, x, y, z float64, size int) (int32, error) {
	return s.spawnSlimeOrMagmaCube(dimension, x, y, z, size, MobTypeSlime, EntityTypeSlimeID)
}

// SpawnMagmaCube spawns a `minecraft:magma_cube` at (x, y, z) in dimension with
// the given size (1 = small, 2 = medium, 4 = big).
func (s *Server) SpawnMagmaCube(dimension string, x, y, z float64, size int) (int32, error) {
	return s.spawnSlimeOrMagmaCube(dimension, x, y, z, size, MobTypeMagmaCube, EntityTypeMagmaCubeID)
}

func (s *Server) spawnSlimeOrMagmaCube(dimension string, x, y, z float64, size int, typeName string, fallbackID int) (int32, error) {
	if s == nil || s.entities == nil {
		return 0, errors.New("server: nil server or entity manager")
	}
	if size <= 0 {
		size = 1
	}
	normDim := world.NormalizeDimension(dimension)
	typeID := ResolveEntityTypeID(typeName)
	if typeID < 0 {
		typeID = fallbackID
	}
	id := s.entities.Add(&world.Entity{
		TypeID:    typeID,
		TypeName:  typeName,
		Dimension: normDim,
		X:         x,
		Y:         y,
		Z:         z,
		OnGround:  true,
	})
	hp := float32(size * size)
	tr := s.entityTracker()
	tr.mu.Lock()
	mem := tr.mobStateFor(id, tr.aiTickID)
	mem.healthInit = true
	mem.health = hp
	mem.lastHealth = hp
	tr.mu.Unlock()

	mgr := s.frogManager()
	mgr.mu.Lock()
	mgr.slimeSizes[id] = size
	mgr.mu.Unlock()
	_ = s.ensureMagmaOrSlimeState(id, typeName, normDim)

	if ent := s.entities.Get(id); ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, ent))
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlaySetEntityData, EncodeSlimeSizeDataPacket(id, int32(size)))
	}
	return id, nil
}

// SlimeSize returns the tracked size of a slime or magma_cube entity (defaults to 1).
func (s *Server) SlimeSize(entityID int32) int {
	if s == nil || entityID <= 0 {
		return 1
	}
	mgr := s.frogManager()
	mgr.mu.RLock()
	sz, ok := mgr.slimeSizes[entityID]
	mgr.mu.RUnlock()
	if !ok || sz <= 0 {
		return 1
	}
	return sz
}

// SetSlimeSize sets the size (1, 2, or 4) of a slime or magma_cube entity.
func (s *Server) SetSlimeSize(entityID int32, size int) {
	if s == nil || entityID <= 0 {
		return
	}
	if size <= 0 {
		size = 1
	}
	mgr := s.frogManager()
	mgr.mu.Lock()
	mgr.slimeSizes[entityID] = size
	mgr.mu.Unlock()
	if s.entities != nil {
		if ent := s.entities.Get(entityID); ent != nil {
			_ = s.ensureMagmaOrSlimeState(entityID, ent.TypeName, ent.EffectiveDimension())
			s.SetMobHealth(entityID, float32(size*size))
			cp := ent.ChunkPos()
			s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, protocol.PlaySetEntityData,
				EncodeSlimeSizeDataPacket(entityID, int32(size)))
		}
	}
}

// IsSmallFrogPrey reports whether target is a small (size == 1) `minecraft:slime`
// or `minecraft:magma_cube` that a Frog can eat with its tongue.
func (s *Server) IsSmallFrogPrey(target *world.Entity) bool {
	if s == nil || target == nil {
		return false
	}
	norm := canonicalResourceName(target.TypeName)
	if norm != MobTypeSlime && norm != MobTypeMagmaCube {
		return false
	}
	return s.SlimeSize(target.ID) == 1
}

// FrogTongueEatEntity performs a Frog's tongue attack against targetEntityID
// (a small slime or small magma_cube):
//   - Sets PoseUsingTongue (9) and DATA_TONGUE_TARGET_ID = targetEntityID
//   - Removes the prey entity and plays entity.frog.tongue + entity.frog.eat
//   - Drops `minecraft:slime_ball` for a small slime, or the variant-specific
//     Froglight (`ochre_froglight`, `verdant_froglight`, `pearlescent_froglight`)
//     for a small magma_cube!
func (s *Server) FrogTongueEatEntity(frogID, targetEntityID int32) (droppedItemEntityID int32, droppedItemName string, ok bool) {
	if s == nil || s.entities == nil || frogID <= 0 || targetEntityID <= 0 {
		return 0, "", false
	}
	frogEnt := s.entities.Get(frogID)
	targetEnt := s.entities.Get(targetEntityID)
	if frogEnt == nil || targetEnt == nil || canonicalResourceName(frogEnt.TypeName) != MobTypeFrog {
		return 0, "", false
	}
	if !s.IsSmallFrogPrey(targetEnt) {
		return 0, "", false
	}

	st := s.ensureFrogState(frogID, FrogVariantTemperate)
	mgr := s.frogManager()
	mgr.mu.Lock()
	variant := st.Variant
	targetType := canonicalResourceName(targetEnt.TypeName)
	if targetType == MobTypeMagmaCube {
		droppedItemName = FroglightItemForVariant(variant)
	} else {
		droppedItemName = SlimeBallItemID
	}
	st.Pose = PoseUsingTongue
	st.TongueTargetID = targetEntityID
	st.TongueAnimTicks = FrogTongueAnimTicks
	st.TongueCooldownTicks = FrogTongueCooldownTicks
	st.LastDroppedItemID = droppedItemName
	snap := *st
	delete(mgr.slimeSizes, targetEntityID)
	mgr.mu.Unlock()
	if bg := s.netherHostileManager(); bg != nil {
		bg.mu.Lock()
		delete(bg.magmaCubes, targetEntityID)
		bg.mu.Unlock()
	}

	dim := frogEnt.EffectiveDimension()
	cp := frogEnt.ChunkPos()
	tx, ty, tz := targetEnt.X, targetEnt.Y, targetEnt.Z

	// Face the target and broadcast tongue pose + target metadata.
	yaw := yawToward(tx-frogEnt.X, tz-frogEnt.Z)
	s.entities.Update(frogID, func(e *world.Entity) {
		e.Yaw = yaw
		e.HeadYaw = yaw
	})
	s.broadcastFrogState(frogEnt, snap)

	// Remove eaten prey and spawn its drop (`slime_ball` or `*_froglight`).
	s.entities.Remove(targetEntityID)
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayRemoveEntities,
		encodeRemoveEntitiesPacket(nil, []int32{targetEntityID}))

	dropStack := world.NewNamedItemStack(droppedItemName, 1)
	if !dropStack.IsEmpty() {
		droppedItemEntityID, _ = s.SpawnItemEntityInDimension(dim, tx, ty+0.2, tz, dropStack)
	}

	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundFrogTongue, SoundSourceNeutral, frogEnt.X, frogEnt.Y, frogEnt.Z, 1.0, 1.0, 0))
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundFrogEat, SoundSourceNeutral, frogEnt.X, frogEnt.Y, frogEnt.Z, 1.0, 1.0, 0))
	return droppedItemEntityID, droppedItemName, true
}

// ComputeFrogFallDamage computes fall damage for a Frog in vanilla 26.1.2,
// applying the -5.0 HP fall damage reduction (`max(0, ceil(fallDistance - 3) - 5)`).
func ComputeFrogFallDamage(fallDistance float64) float32 {
	raw := float32(math.Ceil(fallDistance-3.0)) - FrogFallDamageReduction
	if raw <= 0 {
		return 0
	}
	return raw
}

// ResolveSlimeOrMagmaCubeLoot returns the canonical item ID dropped when a
// `minecraft:slime` or `minecraft:magma_cube` of size is killed or eaten by a Frog:
//   - Small (size == 1) magma_cube eaten by a Frog -> variant Froglight (`ochre_froglight`, `verdant_froglight`, `pearlescent_froglight`)
//   - Small (size == 1) slime eaten by a Frog or killed normally -> `minecraft:slime_ball`
//   - Medium/large (size > 1) magma_cube killed normally -> `minecraft:magma_cream`
func ResolveSlimeOrMagmaCubeLoot(typeName string, size int, eatenByFrog bool, frogVariant int32) string {
	norm := canonicalResourceName(typeName)
	if norm == MobTypeMagmaCube {
		if eatenByFrog && size <= 1 {
			return FroglightItemForVariant(frogVariant)
		}
		if !eatenByFrog && size > 1 {
			return MagmaCreamItemID
		}
		return ""
	}
	if norm == MobTypeSlime {
		if size <= 1 {
			return SlimeBallItemID
		}
	}
	return ""
}

// SplitSlimeOrMagmaCubeOnDeath handles the death of a `minecraft:slime` or
// `minecraft:magma_cube` in normal combat:
//   - If size > 1 (e.g., 4 -> 2, or 2 -> 1), removes the parent entity and
//     spawns 2..4 child slimes/magma cubes of size/2 around its position; for
//     `minecraft:magma_cube` with size > 1, also drops `minecraft:magma_cream`.
//   - If size == 1, removes the entity and drops `minecraft:slime_ball` for a small slime.
func (s *Server) SplitSlimeOrMagmaCubeOnDeath(entityID int32, rng *rand.Rand) (childIDs []int32, droppedItemIDs []int32) {
	if s == nil || s.entities == nil || entityID <= 0 {
		return nil, nil
	}
	ent := s.entities.Get(entityID)
	if ent == nil {
		return nil, nil
	}
	typeName := canonicalResourceName(ent.TypeName)
	if typeName != MobTypeSlime && typeName != MobTypeMagmaCube {
		return nil, nil
	}
	size := s.SlimeSize(entityID)
	mgr := s.frogManager()
	mgr.mu.Lock()
	delete(mgr.slimeSizes, entityID)
	mgr.mu.Unlock()
	if bg := s.netherHostileManager(); bg != nil {
		bg.mu.Lock()
		delete(bg.magmaCubes, entityID)
		bg.mu.Unlock()
	}

	dim := ent.EffectiveDimension()
	cp := ent.ChunkPos()
	x, y, z := ent.X, ent.Y, ent.Z

	s.entities.Remove(entityID)
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayRemoveEntities,
		encodeRemoveEntitiesPacket(nil, []int32{entityID}))

	tr := s.entityTracker()
	tr.mu.Lock()
	delete(tr.mobStates, entityID)
	tr.mu.Unlock()

	lootID := ResolveSlimeOrMagmaCubeLoot(typeName, size, false, FrogVariantTemperate)
	if lootID != "" {
		dropStack := world.NewNamedItemStack(lootID, 1)
		if !dropStack.IsEmpty() {
			if itemEntID, _ := s.SpawnItemEntityInDimension(dim, x, y+0.25, z, dropStack); itemEntID > 0 {
				droppedItemIDs = append(droppedItemIDs, itemEntID)
			}
		}
	}

	if size > 1 {
		nextSize := size / 2
		if nextSize < 1 {
			nextSize = 1
		}
		count := 2
		if rng != nil {
			count = 2 + rng.Intn(3) // 2..4 children
		} else {
			count = 3
		}
		fallbackID := EntityTypeSlimeID
		if typeName == MobTypeMagmaCube {
			fallbackID = EntityTypeMagmaCubeID
		}
		for i := 0; i < count; i++ {
			ox := float64((i%2)*2-1) * 0.35
			oz := float64(((i/2)%2)*2-1) * 0.35
			if cid, err := s.spawnSlimeOrMagmaCube(dim, x+ox, y, z+oz, nextSize, typeName, fallbackID); err == nil && cid > 0 {
				childIDs = append(childIDs, cid)
			}
		}
	}
	return childIDs, droppedItemIDs
}

// ExecuteFrogJump performs a Frog hop/long-jump along its current facing yaw.
func (s *Server) ExecuteFrogJump(frogID int32) (vx, vy, vz float64, jumped bool) {
	if s == nil || s.entities == nil {
		return 0, 0, 0, false
	}
	ent := s.entities.Get(frogID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeFrog {
		return 0, 0, 0, false
	}
	st := s.ensureFrogState(frogID, FrogVariantTemperate)
	mgr := s.frogManager()
	mgr.mu.Lock()
	st.Pose = PoseLongJumping
	st.JumpCooldownTicks = FrogJumpCooldownMinTicks
	snap := *st
	mgr.mu.Unlock()

	rad := float64(ent.Yaw) * (math.Pi / 180.0)
	vx = -math.Sin(rad) * FrogJumpHorizontalImpulse
	vy = FrogJumpVerticalImpulse
	vz = math.Cos(rad) * FrogJumpHorizontalImpulse

	s.entities.Update(frogID, func(e *world.Entity) {
		e.X += vx
		e.Z += vz
		e.VelocityX = clampVelocityShort(vx)
		e.VelocityY = clampVelocityShort(vy)
		e.VelocityZ = clampVelocityShort(vz)
		e.OnGround = false
	})
	s.broadcastFrogState(ent, snap)
	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundFrogLongJump, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
	return vx, vy, vz, true
}

// PlaceFrogspawn places a `minecraft:frogspawn` block at (x, y, z) above water
// in dimension and registers it to hatch into 2..5 tadpoles.
func (s *Server) PlaceFrogspawn(dimension string, x, y, z int, rng *rand.Rand) bool {
	if s == nil {
		return false
	}
	normDim := world.NormalizeDimension(dimension)
	cache := s.DimensionChunks(normDim)
	if cache == nil || y <= world.MinY || y >= world.MinY+world.WorldHeight {
		return false
	}
	spawnState, ok := world.BlockStateByName(FrogspawnBlockName, nil)
	if !ok {
		return false
	}
	cache.SetBlock(x, y, z, spawnState)
	s.broadcastMobBlockUpdate(normDim, x, y, z, spawnState)

	hatchTicks := FrogspawnMinHatchTicks
	if rng != nil {
		hatchTicks += rng.Intn(FrogspawnMaxHatchTicks - FrogspawnMinHatchTicks + 1)
	}
	mgr := s.frogManager()
	mgr.mu.Lock()
	mgr.frogspawns[frogspawnPosKey{dim: normDim, x: x, y: y, z: z}] = &FrogspawnTrackerState{
		Dimension:      normDim,
		X:              x,
		Y:              y,
		Z:              z,
		RemainingTicks: hatchTicks,
	}
	mgr.mu.Unlock()
	return true
}

// HatchFrogspawn hatches the `minecraft:frogspawn` block at (x, y, z) in
// dimension into 2..5 `minecraft:tadpole` entities.
func (s *Server) HatchFrogspawn(dimension string, x, y, z int, rng *rand.Rand) []int32 {
	if s == nil {
		return nil
	}
	normDim := world.NormalizeDimension(dimension)
	cache := s.DimensionChunks(normDim)
	if cache != nil {
		cache.SetBlock(x, y, z, world.StateAir)
		s.broadcastMobBlockUpdate(normDim, x, y, z, world.StateAir)
	}
	mgr := s.frogManager()
	mgr.mu.Lock()
	delete(mgr.frogspawns, frogspawnPosKey{dim: normDim, x: x, y: y, z: z})
	mgr.mu.Unlock()

	count := FrogspawnMinTadpoles
	if rng != nil {
		count += rng.Intn(FrogspawnMaxTadpoles - FrogspawnMinTadpoles + 1)
	} else {
		count = 3
	}
	var spawned []int32
	for i := 0; i < count; i++ {
		ox := float64(x) + 0.3 + float64(i%2)*0.4
		oz := float64(z) + 0.3 + float64((i/2)%2)*0.4
		if tid, err := s.SpawnTadpole(normDim, ox, float64(y), oz); err == nil && tid > 0 {
			spawned = append(spawned, tid)
		}
	}
	cx := int32(x >> 4)
	cz := int32(z >> 4)
	s.BroadcastDimensionChunk(normDim, cx, cz, PlaySoundPacketID,
		EncodeSoundPacket(SoundFrogspawnHatch, SoundSourceBlocks, float64(x)+0.5, float64(y), float64(z)+0.5, 1.0, 1.0, 0))
	return spawned
}

// InteractWithFrog handles a player right-clicking a `minecraft:frog` with
// `minecraft:slime_ball` to enter love mode and breed (laying `minecraft:frogspawn`
// or spawning tadpoles).
func (s *Server) InteractWithFrog(session *PlayerSession, entityID int32) (handled bool, frogspawnPlaced bool) {
	if s == nil || s.entities == nil || session == nil || session.IsDead() {
		return false, false
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator || snap.Health <= 0 {
		return false, false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeFrog {
		return false, false
	}
	dx := ent.X - snap.X
	dy := ent.Y - snap.Y
	dz := ent.Z - snap.Z
	if snap.GameMode != GameModeCreative && (dx*dx+dy*dy+dz*dz) > MaxMeleeAttackReach*MaxMeleeAttackReach {
		return false, false
	}
	held := session.HeldItem()
	if held.IsEmpty() || canonicalResourceName(held.ResolvedID()) != SlimeBallItemID {
		return false, false
	}
	if s.MobBreedCooldown(entityID) > 0 || s.MobInLoveTicks(entityID) > 0 {
		return false, false
	}
	if !s.SetMobInLove(entityID, AnimalLoveDurationTicks, session) {
		return false, false
	}
	consumeOneHeldItemInSurvival(session)

	// Check if another adult Frog nearby is also in love.
	dim := ent.EffectiveDimension()
	allEnts := s.entities.AppendAll(nil)
	var mate *world.Entity
	for i := range allEnts {
		other := &allEnts[i]
		if other.ID == entityID || canonicalResourceName(other.TypeName) != MobTypeFrog || other.EffectiveDimension() != dim {
			continue
		}
		odx := other.X - ent.X
		ody := other.Y - ent.Y
		odz := other.Z - ent.Z
		if odx*odx+ody*ody+odz*odz <= AnimalBreedRange*AnimalBreedRange && s.MobInLoveTicks(other.ID) > 0 {
			mate = other
			break
		}
	}
	if mate == nil {
		return true, false
	}

	tr := s.entityTracker()
	tr.mu.Lock()
	if m1 := tr.mobStates[entityID]; m1 != nil {
		m1.inLoveTicks = 0
		m1.breedCooldown = AnimalBreedCooldownTicks
		m1.loveCause = nil
	}
	if m2 := tr.mobStates[mate.ID]; m2 != nil {
		m2.inLoveTicks = 0
		m2.breedCooldown = AnimalBreedCooldownTicks
		m2.loveCause = nil
	}
	tr.mu.Unlock()

	st := s.ensureFrogState(entityID, FrogVariantTemperate)
	mgr := s.frogManager()
	mgr.mu.Lock()
	st.Gravid = true
	mgr.mu.Unlock()

	bx := int(math.Floor(ent.X))
	by := int(math.Floor(ent.Y))
	bz := int(math.Floor(ent.Z))
	if wx, wy, wz, foundWater := s.findNearbyFrogspawnWaterPos(dim, bx, by, bz, 3); foundWater {
		bx, by, bz = wx, wy, wz
	} else if _, _, _, distantWater := s.findNearbyFrogspawnWaterPos(dim, bx, by, bz, 8); distantWater {
		// Gravid frog will walk/hop to the nearby water block in TickFrogs before laying frogspawn.
		s.GrantCriterion(session, "minecraft:husbandry/breed_an_animal", "bred")
		return true, false
	}
	if s.PlaceFrogspawn(dim, bx, by, bz, nil) {
		mgr.mu.Lock()
		st.Gravid = false
		mgr.mu.Unlock()
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
			EncodeSoundPacket(SoundFrogLaySpawn, SoundSourceNeutral, float64(bx)+0.5, float64(by), float64(bz)+0.5, 1.0, 1.0, 0))
		s.GrantCriterion(session, "minecraft:husbandry/breed_an_animal", "bred")
		return true, true
	}
	return true, false
}

func (s *Server) findNearbyFrogspawnWaterPos(dim string, cx, cy, cz, radius int) (wx, wy, wz int, ok bool) {
	cache := s.DimensionChunks(world.NormalizeDimension(dim))
	if cache == nil {
		return 0, 0, 0, false
	}
	bestDistSq := (radius*2 + 1) * (radius*2 + 1) * 3
	for dy := -2; dy <= 2; dy++ {
		y := cy + dy
		if y <= world.MinY || y >= world.MinY+world.WorldHeight {
			continue
		}
		for dz := -radius; dz <= radius; dz++ {
			for dx := -radius; dx <= radius; dx++ {
				x := cx + dx
				z := cz + dz
				if cache.GetBlock(x, y-1, z) == world.StateWater && cache.GetBlock(x, y, z) == world.StateAir {
					dSq := dx*dx + dy*dy + dz*dz
					if dSq < bestDistSq {
						bestDistSq = dSq
						wx, wy, wz = x, y, z
						ok = true
					}
				}
			}
		}
	}
	return wx, wy, wz, ok
}

// TickFrogs advances all `minecraft:frog` entities and tracked `minecraft:frogspawn`
// blocks by one server tick.
func (s *Server) TickFrogs(rng *rand.Rand) {
	if s == nil || s.entities == nil {
		return
	}
	allEnts := s.entities.AppendAll(nil)
	mgr := s.frogManager()

	for i := range allEnts {
		ent := &allEnts[i]
		if canonicalResourceName(ent.TypeName) != MobTypeFrog {
			continue
		}
		st := s.ensureFrogState(ent.ID, FrogVariantTemperate)
		if st == nil {
			continue
		}
		mgr.mu.Lock()
		if st.TongueCooldownTicks > 0 {
			st.TongueCooldownTicks--
		}
		if st.JumpCooldownTicks > 0 {
			st.JumpCooldownTicks--
		}
		if st.CroakTicks > 0 {
			st.CroakTicks--
			if st.CroakTicks == 0 && st.Pose == PoseCroaking {
				st.Pose = PoseStanding
				snap := *st
				mgr.mu.Unlock()
				s.broadcastFrogState(ent, snap)
				continue
			}
		} else if st.Pose == PoseLongJumping && ent.OnGround {
			st.Pose = PoseStanding
			snap := *st
			mgr.mu.Unlock()
			s.broadcastFrogState(ent, snap)
			mgr.mu.Lock()
		}
		if st.TongueAnimTicks > 0 {
			st.TongueAnimTicks--
			if st.TongueAnimTicks == 0 {
				st.Pose = PoseStanding
				st.TongueTargetID = 0
				snap := *st
				mgr.mu.Unlock()
				s.broadcastFrogState(ent, snap)
				continue
			}
		}
		isGravid := st.Gravid
		canTongue := st.TongueCooldownTicks <= 0
		mgr.mu.Unlock()

		dim := ent.EffectiveDimension()

		// 1. Gravid frog seeks water surface within 8 blocks to lay frogspawn.
		if isGravid {
			bx := int(math.Floor(ent.X))
			by := int(math.Floor(ent.Y))
			bz := int(math.Floor(ent.Z))
			if wx, wy, wz, found := s.findNearbyFrogspawnWaterPos(dim, bx, by, bz, 8); found {
				tx := float64(wx) + 0.5
				tz := float64(wz) + 0.5
				dx := tx - ent.X
				dz := tz - ent.Z
				horiz := math.Hypot(dx, dz)
				if horiz <= 1.8 {
					if s.PlaceFrogspawn(dim, wx, wy, wz, rng) {
						mgr.mu.Lock()
						st.Gravid = false
						mgr.mu.Unlock()
						cp := ent.ChunkPos()
						s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
							EncodeSoundPacket(SoundFrogLaySpawn, SoundSourceNeutral, tx, float64(wy), tz, 1.0, 1.0, 0))
					}
				} else {
					nx := ent.X + (dx/horiz)*FrogChaseSpeed
					nz := ent.Z + (dz/horiz)*FrogChaseSpeed
					yaw := yawToward(dx, dz)
					s.entities.Update(ent.ID, func(e *world.Entity) {
						e.X = nx
						e.Z = nz
						e.Yaw = yaw
						e.HeadYaw = yaw
					})
				}
				continue
			}
		}

		if !canTongue {
			continue
		}

		// 2. Search for small (size == 1) slime or magma_cube within FrogPreyChaseRange.
		var bestPrey *world.Entity
		bestDistSq := FrogPreyChaseRange * FrogPreyChaseRange
		for j := range allEnts {
			cand := &allEnts[j]
			if cand.ID == ent.ID || cand.EffectiveDimension() != dim || !s.IsSmallFrogPrey(cand) {
				continue
			}
			dx := cand.X - ent.X
			dy := cand.Y - ent.Y
			dz := cand.Z - ent.Z
			dSq := dx*dx + dy*dy + dz*dz
			if dSq <= bestDistSq {
				bestDistSq = dSq
				bestPrey = cand
			}
		}
		if bestPrey != nil {
			dist := math.Sqrt(bestDistSq)
			if dist <= FrogTongueRange {
				_, _, _ = s.FrogTongueEatEntity(ent.ID, bestPrey.ID)
			} else {
				// Hop/move toward the prey.
				dx := bestPrey.X - ent.X
				dz := bestPrey.Z - ent.Z
				horiz := math.Hypot(dx, dz)
				if horiz > 1e-4 {
					nx := ent.X + (dx/horiz)*FrogChaseSpeed
					nz := ent.Z + (dz/horiz)*FrogChaseSpeed
					yaw := yawToward(dx, dz)
					s.entities.Update(ent.ID, func(e *world.Entity) {
						e.X = nx
						e.Z = nz
						e.Yaw = yaw
						e.HeadYaw = yaw
					})
				}
			}
			continue
		}

		// 3. Follow nearby player holding slime_ball within 10 blocks.
		for _, sess := range s.onlinePlayersSnapshot() {
			if sess == nil || sess.IsDead() {
				continue
			}
			ps := sess.Snapshot()
			if world.NormalizeDimension(ps.Dimension) != dim || ps.GameMode == GameModeSpectator {
				continue
			}
			held := sess.HeldItem()
			if held.IsEmpty() || canonicalResourceName(held.ResolvedID()) != SlimeBallItemID {
				continue
			}
			dx := ps.X - ent.X
			dz := ps.Z - ent.Z
			horiz := math.Hypot(dx, dz)
			if horiz > 1.8 && horiz <= 10.0 {
				nx := ent.X + (dx/horiz)*FrogWanderSpeed
				nz := ent.Z + (dz/horiz)*FrogWanderSpeed
				yaw := yawToward(dx, dz)
				s.entities.Update(ent.ID, func(e *world.Entity) {
					e.X = nx
					e.Z = nz
					e.Yaw = yaw
					e.HeadYaw = yaw
				})
				break
			}
		}
	}

	s.TickFrogspawns(rng)
}

// TickFrogspawns advances all tracked `minecraft:frogspawn` blocks by one tick
// and hatches them into tadpoles when their timer reaches 0.
func (s *Server) TickFrogspawns(rng *rand.Rand) {
	if s == nil {
		return
	}
	mgr := s.frogManager()
	mgr.mu.RLock()
	if len(mgr.frogspawns) == 0 {
		mgr.mu.RUnlock()
		return
	}
	keys := make([]frogspawnPosKey, 0, len(mgr.frogspawns))
	for k := range mgr.frogspawns {
		keys = append(keys, k)
	}
	mgr.mu.RUnlock()

	for _, key := range keys {
		mgr.mu.Lock()
		fs := mgr.frogspawns[key]
		if fs == nil {
			mgr.mu.Unlock()
			continue
		}
		fs.RemainingTicks--
		ready := fs.RemainingTicks <= 0
		mgr.mu.Unlock()
		if ready {
			s.HatchFrogspawn(key.dim, key.x, key.y, key.z, rng)
		}
	}
}
