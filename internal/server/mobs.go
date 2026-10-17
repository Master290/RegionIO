package server

import (
	"errors"
	"math"
	"math/rand"
	"strings"
	"sync/atomic"

	"regionio/internal/protocol"
	"regionio/internal/registry"
	"regionio/internal/world"
)

// Mob entity type identifiers and vanilla 26.1.2 BuiltInRegistries.ENTITY_TYPE IDs.
const (
	MobTypeSkeleton = "minecraft:skeleton"
	MobTypeCreeper  = "minecraft:creeper"
	MobTypeSpider   = "minecraft:spider"
	MobTypeCow      = "minecraft:cow"
	MobTypeSheep    = "minecraft:sheep"
	MobTypeChicken          = "minecraft:chicken"
	MobTypeArrow            = "minecraft:arrow"
	EntityTypeFishingBobber = "minecraft:fishing_bobber"

	// Vanilla 26.1.2 BuiltInRegistries.ENTITY_TYPE numeric network IDs (protocol 775).
	EntityTypeArrowID         int = 6
	EntityTypeChickenID       int = 26
	EntityTypeCowID           int = 30
	EntityTypeCreeperID       int = 32
	EntityTypePigID           int = 100
	EntityTypeSheepID         int = 111
	EntityTypeSkeletonID      int = 115
	EntityTypeSpiderID        int = 124
	EntityTypeZombieID        int = 150
	EntityTypeFishingBobberID int = 156

	// Default Mob Max Health values (vanilla 26.1.2).
	DefaultSkeletonMaxHealth float32 = 20.0
	DefaultCreeperMaxHealth  float32 = 20.0
	DefaultSpiderMaxHealth   float32 = 16.0
	DefaultCowMaxHealth      float32 = 10.0
	DefaultSheepMaxHealth    float32 = 8.0
	DefaultChickenMaxHealth  float32 = 4.0

	// Hostile Skeleton parameters (ranged bow combat + movement).
	SkeletonChaseSpeed         = 0.14
	SkeletonWanderSpeed        = 0.08
	SkeletonAggroRange         = 32.0
	SkeletonBowRange           = 15.0
	SkeletonIdealStopDist      = 8.0
	SkeletonShootCooldownTicks = 40
	SkeletonArrowBaseDamage    = float32(3.0)

	// Hostile Creeper parameters (swell / fuse / explosion).
	CreeperChaseSpeed        = 0.13
	CreeperWanderSpeed       = 0.08
	CreeperAggroRange        = 32.0
	CreeperIgniteRange       = 3.0
	CreeperCancelRange       = 7.0
	CreeperFuseDurationTicks = 30 // 1.5s at 20 TPS
	CreeperExplosionRadius   = 3.0
	CreeperMaxBaseDamage     = float32(43.0)

	// Hostile/Neutral Spider parameters (light-dependent aggro + leap + bite).
	SpiderChaseSpeed          = 0.18
	SpiderWanderSpeed         = 0.10
	SpiderAggroRange          = 16.0
	SpiderHostileMaxLight     = byte(11)
	SpiderAttackReach         = 1.9
	SpiderAttackVerticalReach = 1.6
	SpiderAttackStopDist      = 1.0
	SpiderAttackDamage        = float32(2.0)
	SpiderAttackCooldownTicks = 20
	SpiderLeapMinDist         = 2.0
	SpiderLeapMaxDist         = 4.0
	SpiderLeapSpeed           = 0.38
	SpiderLeapCooldownTicks   = 25

	// Passive Animal parameters (Cow, Sheep, Chicken, Pig).
	CowWanderSpeed           = 0.08
	SheepWanderSpeed         = 0.08
	ChickenWanderSpeed       = 0.09
	AnimalPanicSpeedMult     = 1.5
	AnimalPanicDurationTicks = 60
	AnimalTemptRange         = 10.0
	AnimalTemptStopDist      = 1.75
	AnimalBreedRange         = 8.0
	AnimalLoveDurationTicks  = 600  // 30s in love
	AnimalBreedCooldownTicks = 6000 // 5m breeding cooldown
	ChickenEggMinTicks       = 6000 // 5m..10m egg timer
	ChickenEggMaxTicks       = 12000
	ChickenMaxFallVelocity   = int16(-400) // slow flapping fall
	SheepWoolRegrowTicks     = 1200

	// SynchedEntityData constants for Creeper and Sheep in Minecraft 26.1.2 (protocol 775).
	EntityDataCreeperSwellDirID byte  = 17 // Creeper.DATA_SWELL_DIR (INT serializer = 1)
	EntityDataCreeperPoweredID  byte  = 18 // Creeper.DATA_IS_POWERED (BOOLEAN serializer = 8)
	EntityDataCreeperIgnitedID  byte  = 19 // Creeper.DATA_IS_IGNITED (BOOLEAN serializer = 8)
	EntityDataSheepWoolID       byte  = 18 // Sheep.DATA_WOOL_ID (BYTE serializer = 0: color | 0x10 sheared)
	EntityDataSerializerByte    int32 = 0
	EntityDataSerializerInt     int32 = 1
	EntityDataSerializerBool    int32 = 8

	// ClientboundEntityEventPacket (0x22) event ID for breeding hearts.
	EntityEventLoveHearts byte = 18
)

var (
	hostileMobTypes = [4]string{
		MobTypeZombie,
		MobTypeSkeleton,
		MobTypeCreeper,
		MobTypeSpider,
	}
	passiveAnimalTypes = [4]string{
		MobTypePig,
		MobTypeCow,
		MobTypeSheep,
		MobTypeChicken,
	}
	fallbackEntityTypeIDs = map[string]int{
		MobTypeArrow:              EntityTypeArrowID,
		MobTypeChicken:            EntityTypeChickenID,
		MobTypeCow:                EntityTypeCowID,
		MobTypeCreeper:            EntityTypeCreeperID,
		MobTypePig:                EntityTypePigID,
		MobTypeSheep:              EntityTypeSheepID,
		MobTypeSkeleton:           EntityTypeSkeletonID,
		MobTypeSpider:             EntityTypeSpiderID,
		MobTypeVillager:           int(EntityTypeVillagerID),
		MobTypeZombie:             EntityTypeZombieID,
		EntityTypeFishingBobber:   EntityTypeFishingBobberID,
		EntityTypeArmorStand:      EntityTypeArmorStandID,
		EntityTypeGlowItemFrame:   EntityTypeGlowItemFrameID,
		EntityTypeItemFrame:       EntityTypeItemFrameID,
		EntityTypePainting:        EntityTypePaintingID,
		EntityTypeAreaEffectCloud: EntityTypeAreaEffectCloudID,
		EntityTypeDragonFireball:  EntityTypeDragonFireballID,
		MobTypeEnderDragon:        EntityTypeEnderDragonID,
		EntityTypeEndCrystal:      EntityTypeEndCrystalID,
		MobTypeEvoker:             EntityTypeEvokerID,
		MobTypePillager:           EntityTypePillagerID,
		MobTypeRavager:            EntityTypeRavagerID,
		MobTypeVindicator:         EntityTypeVindicatorID,
		MobTypeWitch:              EntityTypeWitchID,
		MobTypeWither:             EntityTypeWitherID,
		MobTypeWitherSkeleton:     EntityTypeWitherSkeletonID,
		EntityTypeWitherSkull:     EntityTypeWitherSkullID,
	}
	creeperGriefingDisabled atomic.Bool
)

// AllHostileMobTypes returns a copy of the 4 hostile mob type identifiers
// (zombie, skeleton, creeper, spider).
func AllHostileMobTypes() []string {
	out := make([]string, len(hostileMobTypes))
	copy(out, hostileMobTypes[:])
	return out
}

// AllPassiveAnimalTypes returns a copy of the 4 passive farm animal type
// identifiers (pig, cow, sheep, chicken).
func AllPassiveAnimalTypes() []string {
	out := make([]string, len(passiveAnimalTypes))
	copy(out, passiveAnimalTypes[:])
	return out
}

// IsPassiveAnimal reports whether typeName is a breedable passive farm animal
// (pig, cow, sheep, or chicken).
func IsPassiveAnimal(typeName string) bool {
	switch canonicalResourceName(typeName) {
	case MobTypePig, MobTypeCow, MobTypeSheep, MobTypeChicken:
		return true
	default:
		return false
	}
}

// ResolveEntityTypeID returns the protocol 775 BuiltInRegistries.ENTITY_TYPE
// numeric ID for typeName, consulting registry.EntityTypeIndex first and
// falling back to the verified 26.1.2 built-in table.
func ResolveEntityTypeID(typeName string) int {
	norm := canonicalResourceName(typeName)
	if idx := registry.EntityTypeIndex(norm); idx >= 0 {
		return idx
	}
	if idx, ok := fallbackEntityTypeIDs[norm]; ok {
		return idx
	}
	return -1
}

// CreeperBlockGriefing reports whether creeper explosions destroy nearby
// non-indestructible blocks (true by default).
func (s *Server) CreeperBlockGriefing() bool {
	return !creeperGriefingDisabled.Load()
}

// SetCreeperBlockGriefing enables or disables block destruction by creeper explosions.
func (s *Server) SetCreeperBlockGriefing(enabled bool) {
	creeperGriefingDisabled.Store(!enabled)
}

// ResolveMobMaxHealth returns the vanilla 26.1.2 maximum health for a mob type.
func ResolveMobMaxHealth(typeName string) float32 {
	switch canonicalResourceName(typeName) {
	case MobTypeZombie:
		return DefaultZombieMaxHealth
	case MobTypeSkeleton:
		return DefaultSkeletonMaxHealth
	case MobTypeCreeper:
		return DefaultCreeperMaxHealth
	case MobTypeSpider:
		return DefaultSpiderMaxHealth
	case MobTypePig:
		return DefaultPigMaxHealth
	case MobTypeCow:
		return DefaultCowMaxHealth
	case MobTypeSheep:
		return DefaultSheepMaxHealth
	case MobTypeChicken:
		return DefaultChickenMaxHealth
	case MobTypeVillager:
		return DefaultVillagerMaxHealth
	case MobTypePillager:
		return DefaultPillagerMaxHealth
	case MobTypeVindicator:
		return DefaultVindicatorMaxHealth
	case MobTypeEvoker:
		return DefaultEvokerMaxHealth
	case MobTypeRavager:
		return DefaultRavagerMaxHealth
	case MobTypeWitch:
		return DefaultWitchMaxHealth
	case MobTypeWither:
		return DefaultWitherMaxHealth
	case MobTypeWitherSkeleton:
		return DefaultWitherSkeletonMaxHealth
	case MobTypeEnderDragon:
		return DefaultEnderDragonMaxHealth
	default:
		return DefaultMobMaxHealth
	}
}

func animalWanderSpeed(typeName string) float64 {
	switch canonicalResourceName(typeName) {
	case MobTypeCow:
		return CowWanderSpeed
	case MobTypeSheep:
		return SheepWanderSpeed
	case MobTypeChicken:
		return ChickenWanderSpeed
	case MobTypeVillager:
		return VillagerWanderSpeed
	default:
		return PigWanderSpeed
	}
}

// SpawnMob spawns a mob entity of typeName at (x, y, z), initializes its AI
// and health state, and broadcasts PlayAddEntity to nearby viewers.
func (s *Server) SpawnMob(x, y, z float64, typeName string) (int32, error) {
	if s == nil || s.entities == nil {
		return 0, errors.New("server: nil entity manager")
	}
	norm := canonicalResourceName(typeName)
	if norm == MobTypeVillager {
		return s.SpawnVillager(x, y, z, VillagerProfessionFarmer, MinVillagerLevel)
	}
	if norm == MobTypeWither {
		return s.SpawnWither(world.DimensionOverworld, x, y, z, true)
	}
	if norm == MobTypeEnderDragon {
		return s.SpawnEnderDragon(x, y, z)
	}
	typeID := ResolveEntityTypeID(norm)
	if typeID < 0 {
		return 0, errors.New("server: unknown mob entity type " + norm)
	}
	id := s.entities.Add(&world.Entity{
		TypeID:   typeID,
		TypeName: norm,
		X:        x,
		Y:        y,
		Z:        z,
		OnGround: true,
	})
	tr := s.entityTracker()
	tr.mu.Lock()
	mem := tr.mobStateFor(id, tr.aiTickID)
	mem.healthInit = true
	mem.health = ResolveMobMaxHealth(norm)
	mem.lastHealth = mem.health
	if norm == MobTypeChicken && mem.eggTimer <= 0 {
		mem.eggTimer = ChickenEggMinTicks
	}
	tr.mu.Unlock()

	if ent := s.entities.Get(id); ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastChunk(cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, ent))
	}
	return id, nil
}

// CreeperFuseState returns the current fuse tick counter (0..30) and swell
// direction (-1, 0, or 1) for a creeper entity.
func (s *Server) CreeperFuseState(entityID int32) (fuseTicks int, swellDir int8) {
	if s == nil {
		return 0, 0
	}
	tr := s.entityTracker()
	tr.mu.Lock()
	defer tr.mu.Unlock()
	mem := tr.mobStates[entityID]
	if mem == nil {
		return 0, 0
	}
	return mem.creeperFuseTicks, mem.creeperSwellDir
}

// ProvokeSpider marks a spider entity as provoked so it chases and attacks
// players even in bright daylight.
func (s *Server) ProvokeSpider(entityID int32) {
	if s == nil {
		return
	}
	tr := s.entityTracker()
	tr.mu.Lock()
	mem := tr.mobStateFor(entityID, tr.aiTickID)
	mem.provoked = true
	tr.mu.Unlock()
}

// SetChickenEggTimer sets the remaining ticks until a chicken entity lays its
// next egg.
func (s *Server) SetChickenEggTimer(entityID int32, ticks int) {
	if s == nil {
		return
	}
	tr := s.entityTracker()
	tr.mu.Lock()
	mem := tr.mobStateFor(entityID, tr.aiTickID)
	mem.eggTimer = ticks
	tr.mu.Unlock()
}

// EncodeCreeperSwellDataPacket encodes a ClientboundSetEntityDataPacket (0x63)
// updating a creeper's DATA_SWELL_DIR (-1 defusing, 0 idle, 1 swelling) and
// DATA_IS_IGNITED flag.
func EncodeCreeperSwellDataPacket(entityID int32, swellDir int32, ignited bool) []byte {
	w := protocol.NewWriter(16)
	w.VarInt(entityID)
	w.Byte(EntityDataCreeperSwellDirID)
	w.VarInt(EntityDataSerializerInt)
	w.VarInt(swellDir)
	w.Byte(EntityDataCreeperIgnitedID)
	w.VarInt(EntityDataSerializerBool)
	w.Bool(ignited)
	w.Byte(0xFF)
	return w.Bytes()
}

// EncodeSheepWoolDataPacket encodes a ClientboundSetEntityDataPacket (0x63)
// updating a sheep's DATA_WOOL_ID byte (low 4 bits = color 0..15, 0x10 = sheared).
func EncodeSheepWoolDataPacket(entityID int32, color byte, sheared bool) []byte {
	flags := color & 0x0F
	if sheared {
		flags |= 0x10
	}
	w := protocol.NewWriter(12)
	w.VarInt(entityID)
	w.Byte(EntityDataSheepWoolID)
	w.VarInt(EntityDataSerializerByte)
	w.Byte(flags)
	w.Byte(0xFF)
	return w.Bytes()
}

// IsAnimalBreedingFood reports whether itemName is a valid temptation and
// breeding food for a passive animal of typeName in vanilla 26.1.2.
func IsAnimalBreedingFood(typeName, itemName string) bool {
	mob := canonicalResourceName(typeName)
	item := canonicalResourceName(itemName)
	switch mob {
	case MobTypeCow, MobTypeSheep:
		return item == "minecraft:wheat"
	case MobTypePig:
		return item == "minecraft:carrot" || item == "minecraft:potato" || item == "minecraft:beetroot"
	case MobTypeChicken:
		switch item {
		case "minecraft:wheat_seeds", "minecraft:beetroot_seeds",
			"minecraft:melon_seeds", "minecraft:pumpkin_seeds",
			"minecraft:torchflower_seeds", "minecraft:pitcher_pod":
			return true
		}
	}
	return false
}

// IsSpiderHostile reports whether a spider at (x, feetY, z) is currently
// hostile toward players: true when provoked by an attack or when effective
// ambient light at its position is <= SpiderHostileMaxLight (11).
func (s *Server) IsSpiderHostile(x float64, feetY int, z float64, provoked bool) bool {
	if provoked {
		return true
	}
	if s == nil || s.chunks == nil {
		return true
	}
	bx := int(math.Floor(x))
	bz := int(math.Floor(z))
	_, _, internalLight := s.EffectiveLightAt(bx, feetY, bz)
	return internalLight <= SpiderHostileMaxLight
}

// selectTargetWithinRange finds the nearest valid player within aggroRange of (x, y, z).
// Survival and Adventure players take priority over Creative players; dead and
// Spectator players are ignored.
func selectTargetWithinRange(targets []playerTarget, x, y, z, aggroRange float64) (playerTarget, bool) {
	maxDistSq := aggroRange * aggroRange
	bestSurvivalIdx := -1
	bestSurvivalDistSq := maxDistSq
	bestFallbackIdx := -1
	bestFallbackDistSq := maxDistSq

	for i := range targets {
		pt := &targets[i]
		if pt.session == nil || pt.health <= 0 || pt.gameMode == GameModeSpectator || pt.session.IsDead() {
			continue
		}
		dx := pt.x - x
		dy := pt.y - y
		dz := pt.z - z
		distSq := dx*dx + dy*dy + dz*dz
		if distSq > maxDistSq {
			continue
		}
		if pt.gameMode == GameModeSurvival || pt.gameMode == GameModeAdventure {
			if bestSurvivalIdx < 0 || distSq < bestSurvivalDistSq {
				bestSurvivalIdx = i
				bestSurvivalDistSq = distSq
			}
		} else if bestFallbackIdx < 0 || distSq < bestFallbackDistSq {
			bestFallbackIdx = i
			bestFallbackDistSq = distSq
		}
	}
	if bestSurvivalIdx >= 0 {
		return targets[bestSurvivalIdx], true
	}
	if bestFallbackIdx >= 0 {
		return targets[bestFallbackIdx], true
	}
	return playerTarget{}, false
}

// stepHostileChaseToward advances a mob along a path or direct line toward
// target at chaseSpeed, stopping when horizontal distance <= stopDist.
func stepHostileChaseToward(
	chunks *world.Cache,
	mem *mobMemory,
	x float64,
	feetY int,
	z float64,
	yaw float32,
	targetX, targetY, targetZ float64,
	chaseSpeed, stopDist float64,
) (nextX, nextY, nextZ float64, nextYaw float32) {
	dx := targetX - x
	dz := targetZ - z
	horizDist := math.Hypot(dx, dz)

	nextYaw = yawToward(dx, dz)
	if math.IsNaN(float64(nextYaw)) {
		nextYaw = yaw
	}
	nextX = x
	nextY = float64(feetY)
	nextZ = z

	if horizDist <= stopDist {
		return nextX, nextY, nextZ, nextYaw
	}

	targetFeetY := int(math.Floor(targetY + 0.01))
	directOK := math.Abs(float64(targetFeetY-feetY)) <= 1 &&
		hasDirectWalkableLine(chunks, x, feetY, z, targetX, targetZ)

	var moveGoalX, moveGoalZ float64
	if directOK {
		mem.waypoints = mem.waypoints[:0]
		mem.waypointIdx = 0
		moveGoalX = targetX
		moveGoalZ = targetZ
	} else {
		needRepath := mem.repathCooldown <= 0 || mem.waypointIdx >= len(mem.waypoints)
		if !needRepath && len(mem.waypoints) > 0 {
			last := mem.waypoints[len(mem.waypoints)-1]
			if absInt(last.X-int(math.Floor(targetX))) > 2 || absInt(last.Z-int(math.Floor(targetZ))) > 2 {
				needRepath = true
			}
		}
		if needRepath {
			startX := int(math.Floor(x))
			startZ := int(math.Floor(z))
			goalBX := int(math.Floor(targetX))
			goalBZ := int(math.Floor(targetZ))
			mem.waypoints = FindPath(chunks, startX, feetY, startZ, goalBX, targetFeetY, goalBZ, MaxPathSearchNodes)
			mem.waypointIdx = 0
			mem.repathCooldown = PathRepathIntervalTicks
		}
		for mem.waypointIdx < len(mem.waypoints) {
			wp := mem.waypoints[mem.waypointIdx]
			wx := float64(wp.X) + 0.5
			wz := float64(wp.Z) + 0.5
			if math.Hypot(wx-x, wz-z) <= waypointAdvanceDist {
				mem.waypointIdx++
				continue
			}
			break
		}
		if mem.waypointIdx < len(mem.waypoints) {
			wp := mem.waypoints[mem.waypointIdx]
			moveGoalX = float64(wp.X) + 0.5
			moveGoalZ = float64(wp.Z) + 0.5
		} else {
			moveGoalX = targetX
			moveGoalZ = targetZ
		}
	}

	stepDX := moveGoalX - x
	stepDZ := moveGoalZ - z
	stepDist := math.Hypot(stepDX, stepDZ)
	if stepDist > 1e-5 {
		stepLen := chaseSpeed
		if stepLen > stepDist {
			stepLen = stepDist
		}
		if horizDist-stepLen < stopDist {
			stepLen = math.Max(0, horizDist-stopDist)
		}
		if stepLen > 0 {
			candX := x + (stepDX/stepDist)*stepLen
			candZ := z + (stepDZ/stepDist)*stepLen
			if nx, ny, nz, ok := tryApplyHorizontalStep(chunks, x, feetY, z, candX, candZ); ok {
				nextX, nextY, nextZ = nx, float64(ny), nz
			} else {
				mem.repathCooldown = 0
			}
		}
	}
	return nextX, nextY, nextZ, nextYaw
}

// stepHostileSkeletonAI advances a skeleton by one tick: chases players up to
// bow range (8..15 blocks) and fires ranged arrow attacks on a 40-tick cooldown.
func stepHostileSkeletonAI(
	chunks *world.Cache,
	rng *rand.Rand,
	mem *mobMemory,
	targets []playerTarget,
	x float64,
	feetY int,
	z float64,
	yaw float32,
) (nextX, nextY, nextZ float64, nextYaw float32, shootTarget *PlayerSession) {
	if mem.attackCooldown > 0 {
		mem.attackCooldown--
	}
	if mem.repathCooldown > 0 {
		mem.repathCooldown--
	}

	target, found := selectTargetWithinRange(targets, x, float64(feetY), z, SkeletonAggroRange)
	if !found {
		if mem.state == mobStateChase {
			mem.state = mobStateIdle
			mem.stateTicks = 20
			mem.waypoints = mem.waypoints[:0]
			mem.waypointIdx = 0
		}
		nextX, nextY, nextZ, nextYaw = stepWanderAI(chunks, rng, mem, x, feetY, z, yaw, SkeletonWanderSpeed)
		return nextX, nextY, nextZ, nextYaw, nil
	}

	mem.state = mobStateChase
	nextX, nextY, nextZ, nextYaw = stepHostileChaseToward(
		chunks, mem, x, feetY, z, yaw,
		target.x, target.y, target.z,
		SkeletonChaseSpeed, SkeletonIdealStopDist,
	)

	dist3D := math.Sqrt((target.x-nextX)*(target.x-nextX) + (target.y-nextY)*(target.y-nextY) + (target.z-nextZ)*(target.z-nextZ))
	if dist3D <= SkeletonBowRange && mem.attackCooldown <= 0 {
		shootTarget = target.session
		mem.attackCooldown = SkeletonShootCooldownTicks
	}
	return nextX, nextY, nextZ, nextYaw, shootTarget
}

// ShootSkeletonArrowAtPlayer fires a skeleton arrow at target, broadcasting the
// bow shoot sound and applying ranged arrow damage and knockback.
func (s *Server) ShootSkeletonArrowAtPlayer(skeletonID int32, skelX, skelY, skelZ float64, target *PlayerSession) float32 {
	if s == nil || target == nil || target.IsDead() {
		return 0
	}
	cx := int32(int64(math.Floor(skelX)) >> 4)
	cz := int32(int64(math.Floor(skelZ)) >> 4)
	s.BroadcastChunk(cx, cz, PlaySoundPacketID,
		EncodeSoundPacket("minecraft:entity.skeleton.shoot", SoundSourceHostile, skelX, skelY, skelZ, 1.0, 1.0, 0))
	return s.AttackPlayerFromMob(skeletonID, MobTypeSkeleton, skelX, skelZ, target, SkeletonArrowBaseDamage)
}

// stepHostileCreeperAI advances a creeper by one tick: chases players within
// 32 blocks, starts its 30-tick fuse when within 3 blocks (stopping movement
// while swelling), defuses if the player moves past 7 blocks, and explodes when
// the fuse reaches 30 ticks.
func stepHostileCreeperAI(
	chunks *world.Cache,
	rng *rand.Rand,
	mem *mobMemory,
	targets []playerTarget,
	x float64,
	feetY int,
	z float64,
	yaw float32,
) (nextX, nextY, nextZ float64, nextYaw float32, swellChanged bool, exploded bool) {
	if mem.repathCooldown > 0 {
		mem.repathCooldown--
	}

	target, found := selectTargetWithinRange(targets, x, float64(feetY), z, CreeperAggroRange)
	if !found {
		if mem.creeperFuseTicks > 0 {
			if mem.creeperSwellDir != -1 {
				mem.creeperSwellDir = -1
				swellChanged = true
			}
			mem.creeperFuseTicks--
			if mem.creeperFuseTicks <= 0 {
				mem.creeperFuseTicks = 0
				mem.creeperSwellDir = 0
				swellChanged = true
			}
		}
		if mem.state == mobStateChase {
			mem.state = mobStateIdle
			mem.stateTicks = 20
			mem.waypoints = mem.waypoints[:0]
			mem.waypointIdx = 0
		}
		nextX, nextY, nextZ, nextYaw = stepWanderAI(chunks, rng, mem, x, feetY, z, yaw, CreeperWanderSpeed)
		return nextX, nextY, nextZ, nextYaw, swellChanged, false
	}

	mem.state = mobStateChase
	dx := target.x - x
	dy := target.y - float64(feetY)
	dz := target.z - z
	dist := math.Sqrt(dx*dx + dy*dy + dz*dz)

	nextYaw = yawToward(dx, dz)
	if math.IsNaN(float64(nextYaw)) {
		nextYaw = yaw
	}

	// Determine swell direction: ignite at <= 3.0 blocks, continue swelling while <= 7.0 blocks.
	if dist <= CreeperIgniteRange || (mem.creeperSwellDir > 0 && dist <= CreeperCancelRange) {
		if mem.creeperSwellDir != 1 {
			mem.creeperSwellDir = 1
			swellChanged = true
		}
		mem.creeperFuseTicks++
		if mem.creeperFuseTicks >= CreeperFuseDurationTicks {
			return x, float64(feetY), z, nextYaw, swellChanged, true
		}
		// Creepers stand still while actively swelling.
		return x, float64(feetY), z, nextYaw, swellChanged, false
	}

	// Target is beyond ignite/cancel range: defuse if fuse was partially charged.
	if mem.creeperFuseTicks > 0 {
		if mem.creeperSwellDir != -1 {
			mem.creeperSwellDir = -1
			swellChanged = true
		}
		mem.creeperFuseTicks--
		if mem.creeperFuseTicks <= 0 {
			mem.creeperFuseTicks = 0
			mem.creeperSwellDir = 0
			swellChanged = true
		}
	}

	nextX, nextY, nextZ, nextYaw = stepHostileChaseToward(
		chunks, mem, x, feetY, z, yaw,
		target.x, target.y, target.z,
		CreeperChaseSpeed, 1.5,
	)
	return nextX, nextY, nextZ, nextYaw, swellChanged, false
}

// ComputeCreeperExplosionDamage calculates the base (Normal difficulty)
// explosion damage at distance dist from an explosion of radius, matching
// vanilla's linear falloff formula inside 2*radius.
func ComputeCreeperExplosionDamage(dist, radius float64) float32 {
	maxDiameter := radius * 2.0
	if maxDiameter <= 0 || dist >= maxDiameter {
		return 0
	}
	impact := 1.0 - (dist / maxDiameter)
	if impact <= 0 {
		return 0
	}
	raw := (impact*impact + impact) / 2.0 * 7.0 * maxDiameter + 1.0
	if raw > float64(CreeperMaxBaseDamage) {
		raw = float64(CreeperMaxBaseDamage)
	}
	return float32(raw)
}

// TriggerCreeperExplosion detonates the creeper with entityID at (x, y, z):
// removes the creeper entity, broadcasts explosion sound, applies distance-scaled
// explosion damage and knockback to nearby players, and optionally destroys
// nearby fragile blocks.
func (s *Server) TriggerCreeperExplosion(entityID int32, x, y, z float64) (playersDamaged int) {
	if s == nil {
		return 0
	}
	cx := int32(int64(math.Floor(x)) >> 4)
	cz := int32(int64(math.Floor(z)) >> 4)

	if s.entities != nil && entityID > 0 {
		s.entities.Remove(entityID)
		s.BroadcastChunk(cx, cz, protocol.PlayRemoveEntities, encodeRemoveEntitiesPacket(nil, []int32{entityID}))
	}
	s.BroadcastChunk(cx, cz, PlaySoundPacketID,
		EncodeSoundPacket("minecraft:entity.generic.explode", SoundSourceHostile, x, y, z, 4.0, 0.8, 0))

	// Damage and knock back players within 2 * CreeperExplosionRadius (6.0 blocks).
	maxRange := CreeperExplosionRadius * 2.0
	var sessions []*PlayerSession
	s.playersMu.RLock()
	for _, sess := range s.players {
		if sess != nil && !sess.IsDead() {
			sessions = append(sessions, sess)
		}
	}
	s.playersMu.RUnlock()

	for _, sess := range sessions {
		snap := sess.Snapshot()
		if snap.GameMode == GameModeCreative || snap.GameMode == GameModeSpectator || snap.Health <= 0 {
			continue
		}
		dx := snap.X - x
		dy := snap.Y - y
		dz := snap.Z - z
		dist := math.Sqrt(dx*dx + dy*dy + dz*dz)
		if dist >= maxRange {
			continue
		}
		baseDmg := ComputeCreeperExplosionDamage(dist, CreeperExplosionRadius)
		scaled := ScaleDamageByDifficulty(baseDmg, s.Difficulty())
		if scaled <= 0 {
			continue
		}
		dealt := s.DamagePlayerWithArmor(sess, scaled, "creeper")
		if dealt > 0 {
			playersDamaged++
			if sess.IsDead() {
				s.TriggerKilledByEntity(sess, MobTypeCreeper)
			}
			kb := (1.0 - dist/maxRange) * 0.8
			vx, vy, vz := ComputeKnockback(x, z, 0, snap.X, snap.Z, kb)
			if sess.send != nil {
				_ = sess.send(protocol.PlaySetEntityMotion, EncodeSetEntityMotionPacket(sess.EntityID, vx, vy, vz))
			}
		}
	}

	// Optional block griefing in radius 2 around explosion center (excluding bedrock/liquids/air).
	if s.chunks != nil && s.CreeperBlockGriefing() {
		bx0 := int(math.Floor(x))
		by0 := int(math.Floor(y))
		bz0 := int(math.Floor(z))
		for dy := -1; dy <= 2; dy++ {
			by := by0 + dy
			if by <= world.MinY+1 || by >= world.MinY+world.WorldHeight-1 {
				continue
			}
			for dx := -2; dx <= 2; dx++ {
				for dz := -2; dz <= 2; dz++ {
					if dx*dx+dy*dy+dz*dz > 4 {
						continue
					}
					bx, bz := bx0+dx, bz0+dz
					st := s.chunks.GetBlock(bx, by, bz)
					if st == world.StateAir || st == world.StateBedrock || st == world.StateWater || st == world.StateLava {
						continue
					}
					if IsChestBlock(st) || IsFurnaceBlock(st) {
						continue
					}
					s.chunks.SetBlock(bx, by, bz, world.StateAir)
				}
			}
		}
	}

	return playersDamaged
}

// stepHostileSpiderAI advances a spider by one tick: neutral in bright daylight
// unless provoked, hostile in darkness/night (light <= 11), leaps toward target
// at 2..4 blocks, and bites within melee reach.
func (s *Server) stepHostileSpiderAI(
	chunks *world.Cache,
	rng *rand.Rand,
	mem *mobMemory,
	targets []playerTarget,
	x float64,
	feetY int,
	z float64,
	yaw float32,
) (nextX, nextY, nextZ float64, nextYaw float32, attackTarget *PlayerSession, leaped bool) {
	if mem.attackCooldown > 0 {
		mem.attackCooldown--
	}
	if mem.leapCooldown > 0 {
		mem.leapCooldown--
	}
	if mem.repathCooldown > 0 {
		mem.repathCooldown--
	}

	if !s.IsSpiderHostile(x, feetY, z, mem.provoked) {
		if mem.state == mobStateChase {
			mem.state = mobStateIdle
			mem.stateTicks = 20
			mem.waypoints = mem.waypoints[:0]
			mem.waypointIdx = 0
		}
		nextX, nextY, nextZ, nextYaw = stepWanderAI(chunks, rng, mem, x, feetY, z, yaw, SpiderWanderSpeed)
		return nextX, nextY, nextZ, nextYaw, nil, false
	}

	target, found := selectTargetWithinRange(targets, x, float64(feetY), z, SpiderAggroRange)
	if !found {
		if mem.state == mobStateChase {
			mem.state = mobStateIdle
			mem.stateTicks = 20
			mem.waypoints = mem.waypoints[:0]
			mem.waypointIdx = 0
		}
		nextX, nextY, nextZ, nextYaw = stepWanderAI(chunks, rng, mem, x, feetY, z, yaw, SpiderWanderSpeed)
		return nextX, nextY, nextZ, nextYaw, nil, false
	}

	mem.state = mobStateChase
	dx := target.x - x
	dz := target.z - z
	horizDist := math.Hypot(dx, dz)

	// Spider leap/pounce when 2..4 blocks away and leapCooldown == 0.
	speed := SpiderChaseSpeed
	if horizDist >= SpiderLeapMinDist && horizDist <= SpiderLeapMaxDist && mem.leapCooldown <= 0 {
		speed = SpiderLeapSpeed
		mem.leapCooldown = SpiderLeapCooldownTicks
		leaped = true
	}

	nextX, nextY, nextZ, nextYaw = stepHostileChaseToward(
		chunks, mem, x, feetY, z, yaw,
		target.x, target.y, target.z,
		speed, SpiderAttackStopDist,
	)

	finalDX := target.x - nextX
	finalDY := math.Abs(target.y - nextY)
	finalDZ := target.z - nextZ
	finalHoriz := math.Hypot(finalDX, finalDZ)
	if finalHoriz <= SpiderAttackReach && finalDY <= SpiderAttackVerticalReach && mem.attackCooldown <= 0 {
		attackTarget = target.session
		mem.attackCooldown = SpiderAttackCooldownTicks
	}
	return nextX, nextY, nextZ, nextYaw, attackTarget, leaped
}

// stepPassiveAnimalAI advances a passive animal (cow, sheep, chicken, pig) or
// villager by one tick: handles panic fleeing when hurt, food temptation toward
// nearby players, sheep wool regrowth, chicken egg laying, and idle wandering.
func (s *Server) stepPassiveAnimalAI(
	entityID int32,
	typeName string,
	chunks *world.Cache,
	rng *rand.Rand,
	mem *mobMemory,
	targets []playerTarget,
	x float64,
	feetY int,
	z float64,
	yaw float32,
) (nextX, nextY, nextZ float64, nextYaw float32) {
	if mem.repathCooldown > 0 {
		mem.repathCooldown--
	}
	if mem.breedCooldown > 0 {
		mem.breedCooldown--
	}
	if mem.inLoveTicks > 0 {
		mem.inLoveTicks--
	}

	norm := canonicalResourceName(typeName)
	baseSpeed := animalWanderSpeed(norm)

	// Chicken egg-laying timer.
	if norm == MobTypeChicken {
		if mem.eggTimer <= 0 {
			mem.eggTimer = ChickenEggMinTicks
			if rng != nil {
				mem.eggTimer += rng.Intn(ChickenEggMaxTicks - ChickenEggMinTicks + 1)
			}
		}
		mem.eggTimer--
		if mem.eggTimer <= 0 {
			if rng != nil {
				mem.eggTimer = ChickenEggMinTicks + rng.Intn(ChickenEggMaxTicks-ChickenEggMinTicks+1)
			} else {
				mem.eggTimer = ChickenEggMinTicks
			}
			if s != nil {
				eggStack := world.NewNamedItemStack("minecraft:egg", 1)
				if !eggStack.IsEmpty() {
					s.SpawnItemEntity(x, float64(feetY)+0.25, z, eggStack)
					cx := int32(int64(math.Floor(x)) >> 4)
					cz := int32(int64(math.Floor(z)) >> 4)
					s.BroadcastChunk(cx, cz, PlaySoundPacketID,
						EncodeSoundPacket("minecraft:entity.chicken.egg", SoundSourceNeutral, x, float64(feetY), z, 1.0, 1.0, 0))
				}
			}
		}
	}

	// Sheep wool regrowth timer when standing on grass.
	if norm == MobTypeSheep && mem.sheared {
		mem.woolRegrowTimer++
		if mem.woolRegrowTimer >= SheepWoolRegrowTicks {
			mem.sheared = false
			mem.woolRegrowTimer = 0
			if s != nil {
				cx := int32(int64(math.Floor(x)) >> 4)
				cz := int32(int64(math.Floor(z)) >> 4)
				s.BroadcastChunk(cx, cz, PlaySetEntityDataCB, EncodeSheepWoolDataPacket(entityID, 0, false))
			}
		}
	}

	// 1. Panic flee state when recently hurt.
	if mem.panicTicks > 0 {
		mem.panicTicks--
		return stepWanderAI(chunks, rng, mem, x, feetY, z, yaw, baseSpeed*AnimalPanicSpeedMult)
	}

	// 2. Temptation: follow nearest player within AnimalTemptRange holding breeding food.
	if IsPassiveAnimal(norm) {
		if temptTarget, ok := findTemptingPlayer(targets, norm, x, float64(feetY), z); ok {
			return stepHostileChaseToward(
				chunks, mem, x, feetY, z, yaw,
				temptTarget.x, temptTarget.y, temptTarget.z,
				baseSpeed*1.1, AnimalTemptStopDist,
			)
		}
	}

	// 3. Default passive wander.
	return stepWanderAI(chunks, rng, mem, x, feetY, z, yaw, baseSpeed)
}

func findTemptingPlayer(targets []playerTarget, animalType string, x, y, z float64) (playerTarget, bool) {
	maxDistSq := AnimalTemptRange * AnimalTemptRange
	bestIdx := -1
	bestDistSq := maxDistSq
	for i := range targets {
		pt := &targets[i]
		if pt.session == nil || pt.health <= 0 || pt.gameMode == GameModeSpectator || pt.session.IsDead() {
			continue
		}
		held := pt.session.HeldItem()
		if held.IsEmpty() || !IsAnimalBreedingFood(animalType, held.ResolvedID()) {
			continue
		}
		dx := pt.x - x
		dy := pt.y - y
		dz := pt.z - z
		distSq := dx*dx + dy*dy + dz*dz
		if distSq <= bestDistSq {
			bestIdx = i
			bestDistSq = distSq
		}
	}
	if bestIdx >= 0 {
		return targets[bestIdx], true
	}
	return playerTarget{}, false
}

// InteractWithAnimal handles right-clicking a passive farm animal (cow, sheep,
// pig, chicken):
//   - Cow + bucket -> fills milk_bucket
//   - Sheep + shears -> shears 1..3 white_wool
//   - Animal + breeding food -> enters love mode (600 ticks), and if another
//     same-species animal nearby is also in love, spawns an offspring!
func (s *Server) InteractWithAnimal(session *PlayerSession, entityID int32) (handled bool, offspringID int32) {
	if s == nil || s.entities == nil || session == nil || session.IsDead() {
		return false, 0
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator || snap.Health <= 0 {
		return false, 0
	}
	ent := s.entities.Get(entityID)
	if ent == nil || !IsPassiveAnimal(ent.TypeName) {
		return false, 0
	}
	dx := ent.X - snap.X
	dy := ent.Y - snap.Y
	dz := ent.Z - snap.Z
	if snap.GameMode != GameModeCreative && (dx*dx+dy*dy+dz*dz) > MaxMeleeAttackReach*MaxMeleeAttackReach {
		return false, 0
	}

	held := session.HeldItem()
	if held.IsEmpty() {
		return false, 0
	}
	heldID := canonicalResourceName(held.ResolvedID())
	mobType := canonicalResourceName(ent.TypeName)
	cp := ent.ChunkPos()

	// 1. Cow milking with an empty bucket.
	if mobType == MobTypeCow && heldID == "minecraft:bucket" {
		milk := world.NewNamedItemStack("minecraft:milk_bucket", 1)
		if !milk.IsEmpty() {
			slotIdx := world.HotbarContainerOffset + int(snap.SelectedSlot)
			if snap.GameMode != GameModeCreative {
				if held.Count <= 1 {
					session.SetInventorySlot(slotIdx, milk)
				} else {
					session.SetInventorySlot(slotIdx, world.NewItemStack(held.ResolvedItemID(), held.Count-1))
					_, _ = session.AddItemToInventory(milk)
				}
			} else {
				_, _ = session.AddItemToInventory(milk)
			}
			s.BroadcastChunk(cp.X, cp.Z, PlaySoundPacketID,
				EncodeSoundPacket("minecraft:entity.cow.milk", SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
			return true, 0
		}
	}

	// 2. Sheep shearing with shears.
	if mobType == MobTypeSheep && heldID == "minecraft:shears" {
		tr := s.entityTracker()
		tr.mu.Lock()
		mem := tr.mobStateFor(entityID, tr.aiTickID)
		alreadySheared := mem.sheared
		if !alreadySheared {
			mem.sheared = true
			mem.woolRegrowTimer = 0
		}
		tr.mu.Unlock()
		if !alreadySheared {
			wool := world.NewNamedItemStack("minecraft:white_wool", 2)
			if !wool.IsEmpty() {
				s.SpawnItemEntity(ent.X, ent.Y+0.5, ent.Z, wool)
			}
			s.BroadcastChunk(cp.X, cp.Z, PlaySetEntityDataCB, EncodeSheepWoolDataPacket(entityID, 0, true))
			s.BroadcastChunk(cp.X, cp.Z, PlaySoundPacketID,
				EncodeSoundPacket("minecraft:entity.sheep.shear", SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
			return true, 0
		}
		return false, 0
	}

	// 3. Breeding food -> enter love mode and check for a mate in love.
	if !IsAnimalBreedingFood(mobType, heldID) {
		return false, 0
	}

	tr := s.entityTracker()
	tr.mu.Lock()
	mem := tr.mobStateFor(entityID, tr.aiTickID)
	if mem.breedCooldown > 0 || mem.inLoveTicks > 0 {
		tr.mu.Unlock()
		return false, 0
	}
	mem.inLoveTicks = AnimalLoveDurationTicks

	// Search for a nearby mate of the same species that is also in love.
	var mateID int32
	var mateX, mateY, mateZ float64
	allEnts := s.entities.AppendAll(nil)
	for i := range allEnts {
		other := &allEnts[i]
		if other.ID == entityID || canonicalResourceName(other.TypeName) != mobType {
			continue
		}
		odx := other.X - ent.X
		ody := other.Y - ent.Y
		odz := other.Z - ent.Z
		if odx*odx+ody*ody+odz*odz > AnimalBreedRange*AnimalBreedRange {
			continue
		}
		otherMem := tr.mobStates[other.ID]
		if otherMem != nil && otherMem.inLoveTicks > 0 && otherMem.breedCooldown <= 0 {
			mateID = other.ID
			mateX, mateY, mateZ = other.X, other.Y, other.Z
			otherMem.inLoveTicks = 0
			otherMem.breedCooldown = AnimalBreedCooldownTicks
			mem.inLoveTicks = 0
			mem.breedCooldown = AnimalBreedCooldownTicks
			break
		}
	}
	tr.mu.Unlock()

	if snap.GameMode != GameModeCreative {
		slotIdx := world.HotbarContainerOffset + int(snap.SelectedSlot)
		rem := held.Count - 1
		if rem <= 0 {
			session.SetInventorySlot(slotIdx, world.ItemStack{})
		} else {
			session.SetInventorySlot(slotIdx, world.NewItemStack(held.ResolvedItemID(), rem))
		}
	}

	s.BroadcastChunk(cp.X, cp.Z, protocol.PlayEntityEvent, EncodeEntityEventPacket(entityID, EntityEventLoveHearts))
	if mateID > 0 {
		midX := (ent.X + mateX) * 0.5
		midY := math.Max(ent.Y, mateY)
		midZ := (ent.Z + mateZ) * 0.5
		offspringID, _ = s.SpawnMob(midX, midY, midZ, mobType)
	}
	return true, offspringID
}

func mobAttackCauseName(mobType string) string {
	short := strings.TrimPrefix(canonicalResourceName(mobType), "minecraft:")
	if short == "" {
		return "mob"
	}
	return short
}
