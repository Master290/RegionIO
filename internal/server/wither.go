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

// Wither boss, Wither Skeleton, and Wither Skull constants for Minecraft 26.1.2 (protocol 775).
const (
	MobTypeWither          = "minecraft:wither"
	MobTypeWitherSkeleton  = "minecraft:wither_skeleton"
	EntityTypeWitherSkull  = "minecraft:wither_skull"

	// Vanilla 26.1.2 BuiltInRegistries.ENTITY_TYPE numeric network IDs (protocol 775).
	EntityTypeWitherID         int = 145
	EntityTypeWitherSkeletonID int = 146
	EntityTypeWitherSkullID    int = 147

	// Wither health and phase thresholds (vanilla WitherBoss).
	DefaultWitherMaxHealth         float32 = 300.0
	DefaultWitherSkeletonMaxHealth float32 = 20.0
	WitherInitialSpawnHealth       float32 = 100.0 // MaxHealth / 3 during start of invulnerable charge
	WitherArmorHealthThreshold     float32 = 150.0 // Phase 2 (isPowered): health <= MaxHealth / 2
	WitherInvulnerableTicks        int32   = 220   // 11.0s charging phase at 20 TPS

	// Wither combat, explosion, and regeneration parameters.
	WitherSpawnExplosionRadius       float64 = 7.0
	WitherSkullExplosionRadius       float64 = 1.0
	WitherSkullSpeed                 float64 = 0.95
	WitherSkullDirectDamage          float32 = 8.0
	WitherSkullHitHealAmount         float32 = 5.0
	WitherPassiveRegenIntervalTicks  int     = 20
	WitherPassiveRegenAmount         float32 = 1.0
	WitherBlockBreakTicks            int     = 20
	WitherMainHeadShootCooldown      int     = 40
	WitherSideHeadShootCooldown      int     = 25
	WitherSkullMaxAgeTicks           int     = 200
	WitherAggroRange                 float64 = 40.0
	WitherBossBarRange               float64 = 64.0
	WitherHoverHeightOffset          float64 = 5.0
	WitherHorizontalFlySpeed         float64 = 0.30
	WitherVerticalFlySpeed           float64 = 0.25
	WitherEffectDurationNormal       int32   = 200 // 10s Wither II on Normal
	WitherEffectDurationHard         int32   = 800 // 40s Wither II on Hard
	WitherEffectAmplifier            int32   = 1   // Wither II (amplifier 1)
	WitherDeathXP                    int32   = 50

	// Canonical item and block IDs for Wither summoning and loot.
	NetherStarItemID                = "minecraft:nether_star"
	WitherSkeletonSkullBlockID      = "minecraft:wither_skeleton_skull"
	WitherSkeletonWallSkullBlockID  = "minecraft:wither_skeleton_wall_skull"
	SoulSandBlockID                 = "minecraft:soul_sand"
	SoulSoilBlockID                 = "minecraft:soul_soil"

	// SynchedEntityData indices for LivingEntity, WitherBoss, and WitherSkull in 26.1.2 (protocol 775).
	EntityDataLivingHealthID         byte = 9  // LivingEntity.DATA_HEALTH_ID (FLOAT serializer = 3)
	EntityDataWitherTargetAID        byte = 16 // WitherBoss.DATA_TARGET_A (INT serializer = 1)
	EntityDataWitherTargetBID        byte = 17 // WitherBoss.DATA_TARGET_B (INT serializer = 1)
	EntityDataWitherTargetCID        byte = 18 // WitherBoss.DATA_TARGET_C (INT serializer = 1)
	EntityDataWitherInvID            byte = 19 // WitherBoss.DATA_ID_INV (INT serializer = 1)
	EntityDataWitherSkullDangerousID byte = 8  // WitherSkull.DATA_DANGEROUS (BOOLEAN serializer = 8)
)

// WitherPhase identifies the current combat phase of a Wither boss.
type WitherPhase uint8

const (
	// WitherPhaseCharging is the 220-tick invulnerable spawn phase (DATA_ID_INV > 0).
	WitherPhaseCharging WitherPhase = iota
	// WitherPhaseAerial is Phase 1 (Health > 150.0): hovers ~5 blocks above its target and fires skulls.
	WitherPhaseAerial
	// WitherPhaseArmored is Phase 2 / Wither Armor (Health <= 150.0): immune to projectiles and flies at target height.
	WitherPhaseArmored
)

// WitherPatternMatch describes a matched 4-soul-sand/soil + 3-wither-skeleton-skull
// T-shape structure in the world.
type WitherPatternMatch struct {
	CenterX    int  // X of the central column
	TopY       int  // Y of the 3 skull blocks (arms are at TopY-1, base is at TopY-2)
	CenterZ    int  // Z of the central column
	ArmsAlongX bool // true if the 3-wide crossbar runs along the X axis, false if along Z
}

// Blocks returns the 7 block positions comprising the Wither summoning pattern
// (3 skulls at TopY, 3 upper soul blocks at TopY-1, and 1 lower base block at TopY-2).
func (m WitherPatternMatch) Blocks() [7]world.BlockPos {
	dx, dz := 0, 1
	if m.ArmsAlongX {
		dx, dz = 1, 0
	}
	return [7]world.BlockPos{
		{X: m.CenterX - dx, Y: m.TopY, Z: m.CenterZ - dz},
		{X: m.CenterX, Y: m.TopY, Z: m.CenterZ},
		{X: m.CenterX + dx, Y: m.TopY, Z: m.CenterZ + dz},
		{X: m.CenterX - dx, Y: m.TopY - 1, Z: m.CenterZ - dz},
		{X: m.CenterX, Y: m.TopY - 1, Z: m.CenterZ},
		{X: m.CenterX + dx, Y: m.TopY - 1, Z: m.CenterZ + dz},
		{X: m.CenterX, Y: m.TopY - 2, Z: m.CenterZ},
	}
}

// WitherState holds the authoritative server state for an active Wither boss.
type WitherState struct {
	EntityID           int32
	Dimension          string
	X, Y, Z            float64
	Yaw, Pitch         float32
	Health             float32
	MaxHealth          float32
	InvulnerableTicks  int32
	TargetEntityIDs    [3]int32
	DestroyBlocksTicks int
	ShootCooldownMain  int
	ShootCooldownLeft  int
	ShootCooldownRight int
	IdleHeadUpdates    [2]int
	TickCounter        int
	BossBarUUID        [16]byte
}

// Phase returns the Wither's current combat phase.
func (w WitherState) Phase() WitherPhase {
	if w.InvulnerableTicks > 0 {
		return WitherPhaseCharging
	}
	if w.Health <= WitherArmorHealthThreshold {
		return WitherPhaseArmored
	}
	return WitherPhaseAerial
}

// IsPowered reports whether the Wither has its Wither Armor shield active
// (Health <= MaxHealth / 2, deflecting incoming arrows/projectiles).
func (w WitherState) IsPowered() bool {
	return w.Health <= w.MaxHealth*0.5
}

// WitherSkullProjectile holds the state of an in-flight minecraft:wither_skull projectile.
type WitherSkullProjectile struct {
	EntityID      int32
	OwnerEntityID int32
	Dimension     string
	X, Y, Z       float64
	VX, VY, VZ    float64
	Yaw, Pitch    float32
	Dangerous     bool
	AgeTicks      int
}

type serverWitherManager struct {
	mu      sync.RWMutex
	withers map[int32]*WitherState
	skulls  map[int32]*WitherSkullProjectile
}

var serverWithers sync.Map // map[*Server]*serverWitherManager

func (s *Server) witherManager() *serverWitherManager {
	if v, ok := serverWithers.Load(s); ok {
		return v.(*serverWitherManager)
	}
	mgr := &serverWitherManager{
		withers: make(map[int32]*WitherState),
		skulls:  make(map[int32]*WitherSkullProjectile),
	}
	actual, _ := serverWithers.LoadOrStore(s, mgr)
	return actual.(*serverWitherManager)
}

// IsWitherBaseBlock reports whether blockName is soul_sand or soul_soil.
func IsWitherBaseBlock(blockName string) bool {
	switch canonicalResourceName(blockName) {
	case SoulSandBlockID, SoulSoilBlockID:
		return true
	default:
		return false
	}
}

// IsWitherBaseBlockState reports whether block state ID is soul_sand or soul_soil.
func IsWitherBaseBlockState(state uint16) bool {
	if state == world.StateAir {
		return false
	}
	return IsWitherBaseBlock(world.BlockName(state))
}

// IsWitherSkullBlock reports whether blockName is wither_skeleton_skull or
// wither_skeleton_wall_skull.
func IsWitherSkullBlock(blockName string) bool {
	switch canonicalResourceName(blockName) {
	case WitherSkeletonSkullBlockID, WitherSkeletonWallSkullBlockID:
		return true
	default:
		return false
	}
}

// IsWitherSkullBlockState reports whether block state ID is a wither skeleton skull.
func IsWitherSkullBlockState(state uint16) bool {
	if state == world.StateAir {
		return false
	}
	return IsWitherSkullBlock(world.BlockName(state))
}

// FindWitherPatternAt inspects the blocks around (x, y, z) in cache to detect a
// completed Wither T-shape (4 soul_sand/soul_soil + 3 wither_skeleton_skull on top).
// (x, y, z) may be any of the 3 skull blocks or any of the 4 soul base blocks.
func FindWitherPatternAt(cache *world.Cache, x, y, z int) (WitherPatternMatch, bool) {
	if cache == nil {
		return WitherPatternMatch{}, false
	}
	// Check candidate center positions (cx, topY, cz) near (x, y, z).
	for dy := 0; dy <= 2; dy++ {
		topY := y + dy
		if topY-2 < world.MinY || topY >= world.MinY+world.WorldHeight {
			continue
		}
		for _, armsAlongX := range []bool{true, false} {
			dx, dz := 0, 1
			if armsAlongX {
				dx, dz = 1, 0
			}
			for offset := -1; offset <= 1; offset++ {
				cx := x + offset*dx
				cz := z + offset*dz
				if checkWitherPatternCenter(cache, cx, topY, cz, armsAlongX) {
					return WitherPatternMatch{
						CenterX:    cx,
						TopY:       topY,
						CenterZ:    cz,
						ArmsAlongX: armsAlongX,
					}, true
				}
			}
		}
	}
	return WitherPatternMatch{}, false
}

func checkWitherPatternCenter(cache *world.Cache, cx, topY, cz int, armsAlongX bool) bool {
	dx, dz := 0, 1
	if armsAlongX {
		dx, dz = 1, 0
	}
	// 3 wither skeleton skulls across the top row at topY.
	if !IsWitherSkullBlockState(cache.GetBlock(cx-dx, topY, cz-dz)) ||
		!IsWitherSkullBlockState(cache.GetBlock(cx, topY, cz)) ||
		!IsWitherSkullBlockState(cache.GetBlock(cx+dx, topY, cz+dz)) {
		return false
	}
	// 3 soul sand / soul soil blocks across topY-1.
	if !IsWitherBaseBlockState(cache.GetBlock(cx-dx, topY-1, cz-dz)) ||
		!IsWitherBaseBlockState(cache.GetBlock(cx, topY-1, cz)) ||
		!IsWitherBaseBlockState(cache.GetBlock(cx+dx, topY-1, cz+dz)) {
		return false
	}
	// 1 soul sand / soul soil stem block at (cx, topY-2, cz).
	if !IsWitherBaseBlockState(cache.GetBlock(cx, topY-2, cz)) {
		return false
	}
	// Under-arm spaces at (cx±dx, topY-2, cz±dz) must be air/passable per vanilla pattern.
	if !isPassableBlock(cache.GetBlock(cx-dx, topY-2, cz-dz)) ||
		!isPassableBlock(cache.GetBlock(cx+dx, topY-2, cz+dz)) {
		return false
	}
	return true
}

// TrySummonWitherAt checks if placing a block at (x, y, z) in dimension completes
// a Wither T-shape pattern. When matched (and difficulty is not Peaceful), it
// clears the 7 pattern blocks, spawns the Wither in its 220-tick charging phase,
// creates its purple Wither boss bar, and returns (entityID, true).
func (s *Server) TrySummonWitherAt(dimension string, x, y, z int, summoner *PlayerSession) (int32, bool) {
	if s == nil || s.Difficulty() == DifficultyPeaceful {
		return 0, false
	}
	normDim := world.NormalizeDimension(dimension)
	cache := s.DimensionChunks(normDim)
	if cache == nil {
		return 0, false
	}
	match, ok := FindWitherPatternAt(cache, x, y, z)
	if !ok {
		return 0, false
	}

	// Clear all 7 pattern blocks to air and broadcast block updates.
	for _, pos := range match.Blocks() {
		cache.SetBlock(pos.X, pos.Y, pos.Z, world.StateAir)
		w := protocol.NewWriter(12)
		w.Position(pos.X, pos.Y, pos.Z)
		w.VarInt(int32(world.StateAir))
		s.BroadcastDimensionChunk(normDim, int32(pos.X>>4), int32(pos.Z>>4), protocol.PlayBlockUpdate, w.Bytes())
	}

	spawnX := float64(match.CenterX) + 0.5
	spawnY := float64(match.TopY-2) + 0.55
	spawnZ := float64(match.CenterZ) + 0.5
	id, err := s.SpawnWither(normDim, spawnX, spawnY, spawnZ, true)
	if err != nil {
		return 0, false
	}
	if !match.ArmsAlongX {
		s.entities.Update(id, func(e *world.Entity) {
			e.Yaw = 90
			e.HeadYaw = 90
		})
	}
	if summoner != nil {
		s.GrantCriterion(summoner, "minecraft:nether/summon_wither", "summoned")
	}
	return id, true
}

// SpawnWither spawns a minecraft:wither boss at (x, y, z) in dimension.
// If startCharging is true, the Wither begins in its 220-tick invulnerable
// charging phase at 100/300 HP; otherwise it starts immediately active at 300/300 HP.
func (s *Server) SpawnWither(dimension string, x, y, z float64, startCharging bool) (int32, error) {
	if s == nil || s.entities == nil {
		return 0, errors.New("server: nil entity manager")
	}
	normDim := world.NormalizeDimension(dimension)
	typeID := ResolveEntityTypeID(MobTypeWither)
	if typeID < 0 {
		typeID = EntityTypeWitherID
	}

	initHealth := DefaultWitherMaxHealth
	var invTicks int32
	if startCharging {
		initHealth = WitherInitialSpawnHealth
		invTicks = WitherInvulnerableTicks
	}

	id := s.entities.Add(&world.Entity{
		TypeID:    typeID,
		TypeName:  MobTypeWither,
		Dimension: normDim,
		X:         x,
		Y:         y,
		Z:         z,
		OnGround:  false,
	})

	// Initialize tracker health so MobHealth(id) returns the Wither's health.
	tr := s.entityTracker()
	tr.mu.Lock()
	mem := tr.mobStateFor(id, tr.aiTickID)
	mem.healthInit = true
	mem.health = initHealth
	mem.lastHealth = initHealth
	tr.mu.Unlock()

	// Create purple Wither boss bar (DarkenScreen=true, matching vanilla WitherBoss.bossEvent).
	bar := s.CreateEntityBossBar(
		id,
		ChatComponent{Translate: "entity.minecraft.wither"},
		BossBarColorPurple,
		BossBarOverlayProgress,
		true,  // darkenScreen
		false, // playBossMusic
		false, // createWorldFog
	)
	s.UpdateRuntimeBossBarHealth(bar.UUID, initHealth, DefaultWitherMaxHealth)
	s.SyncRuntimeBossBarPlayersInRange(bar.UUID, normDim, x, y, z, WitherBossBarRange)

	st := &WitherState{
		EntityID:           id,
		Dimension:          normDim,
		X:                  x,
		Y:                  y,
		Z:                  z,
		Health:             initHealth,
		MaxHealth:          DefaultWitherMaxHealth,
		InvulnerableTicks:  invTicks,
		ShootCooldownMain:  WitherMainHeadShootCooldown,
		ShootCooldownLeft:  WitherSideHeadShootCooldown,
		ShootCooldownRight: WitherSideHeadShootCooldown,
		BossBarUUID:        bar.UUID,
	}

	mgr := s.witherManager()
	mgr.mu.Lock()
	mgr.withers[id] = st
	mgr.mu.Unlock()

	if ent := s.entities.Get(id); ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, ent))
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, PlaySetEntityDataCB,
			EncodeWitherEntityDataPacket(id, initHealth, 0, 0, 0, invTicks))
	}
	return id, nil
}

// GetWitherState returns a copy of the WitherState for entityID, or false if not found.
func (s *Server) GetWitherState(entityID int32) (WitherState, bool) {
	if s == nil {
		return WitherState{}, false
	}
	mgr := s.witherManager()
	mgr.mu.RLock()
	st, ok := mgr.withers[entityID]
	var out WitherState
	if ok && st != nil {
		out = *st
	}
	mgr.mu.RUnlock()
	return out, ok && st != nil
}

// ActiveWithers returns snapshots of all currently active Wither bosses on the server.
func (s *Server) ActiveWithers() []WitherState {
	if s == nil {
		return nil
	}
	mgr := s.witherManager()
	mgr.mu.RLock()
	out := make([]WitherState, 0, len(mgr.withers))
	for _, st := range mgr.withers {
		if st != nil {
			out = append(out, *st)
		}
	}
	mgr.mu.RUnlock()
	return out
}

// SetWitherInvulnerableTicks sets the Wither's invulnerable charging ticks
// (DATA_ID_INV, 220..0) and broadcasts the updated SynchedEntityData.
func (s *Server) SetWitherInvulnerableTicks(entityID, ticks int32) bool {
	if s == nil {
		return false
	}
	if ticks < 0 {
		ticks = 0
	}
	mgr := s.witherManager()
	mgr.mu.Lock()
	st, ok := mgr.withers[entityID]
	if !ok || st == nil {
		mgr.mu.Unlock()
		return false
	}
	st.InvulnerableTicks = ticks
	snap := *st
	mgr.mu.Unlock()

	cx := int32(int64(math.Floor(snap.X)) >> 4)
	cz := int32(int64(math.Floor(snap.Z)) >> 4)
	s.BroadcastDimensionChunk(snap.Dimension, cx, cz, PlaySetEntityDataCB,
		EncodeWitherEntityDataPacket(entityID, snap.Health, snap.TargetEntityIDs[0], snap.TargetEntityIDs[1], snap.TargetEntityIDs[2], snap.InvulnerableTicks))
	return true
}

// EncodeWitherEntityDataPacket encodes ClientboundSetEntityDataPacket (0x63) for
// a WitherBoss in protocol 775:
//   - index 9  (FLOAT): DATA_HEALTH_ID
//   - index 16 (INT):   DATA_TARGET_A
//   - index 17 (INT):   DATA_TARGET_B
//   - index 18 (INT):   DATA_TARGET_C
//   - index 19 (INT):   DATA_ID_INV
func EncodeWitherEntityDataPacket(entityID int32, health float32, targetA, targetB, targetC, invulnerableTicks int32) []byte {
	w := protocol.NewWriter(36)
	w.VarInt(entityID)

	w.Byte(EntityDataLivingHealthID)
	w.VarInt(EntityDataSerializerFloat)
	w.Float32(health)

	w.Byte(EntityDataWitherTargetAID)
	w.VarInt(EntityDataSerializerInt)
	w.VarInt(targetA)

	w.Byte(EntityDataWitherTargetBID)
	w.VarInt(EntityDataSerializerInt)
	w.VarInt(targetB)

	w.Byte(EntityDataWitherTargetCID)
	w.VarInt(EntityDataSerializerInt)
	w.VarInt(targetC)

	w.Byte(EntityDataWitherInvID)
	w.VarInt(EntityDataSerializerInt)
	w.VarInt(invulnerableTicks)

	w.Byte(0xFF)
	return w.Bytes()
}

// EncodeWitherSkullEntityDataPacket encodes ClientboundSetEntityDataPacket (0x63)
// for a WitherSkull projectile in protocol 775:
//   - index 8 (BOOLEAN): DATA_DANGEROUS (true = blue skull, false = black skull)
func EncodeWitherSkullEntityDataPacket(entityID int32, dangerous bool) []byte {
	w := protocol.NewWriter(12)
	w.VarInt(entityID)
	w.Byte(EntityDataWitherSkullDangerousID)
	w.VarInt(EntityDataSerializerBool)
	w.Bool(dangerous)
	w.Byte(0xFF)
	return w.Bytes()
}

// WitherEntityDataPacket returns the current SynchedEntityData packet payload
// for a tracked Wither entity, or nil if unknown.
func (s *Server) WitherEntityDataPacket(entityID int32) []byte {
	st, ok := s.GetWitherState(entityID)
	if !ok {
		return nil
	}
	return EncodeWitherEntityDataPacket(
		entityID,
		st.Health,
		st.TargetEntityIDs[0],
		st.TargetEntityIDs[1],
		st.TargetEntityIDs[2],
		st.InvulnerableTicks,
	)
}

// WitherSkullEntityDataPacket returns the current SynchedEntityData packet payload
// for a tracked WitherSkull projectile, or nil if unknown.
func (s *Server) WitherSkullEntityDataPacket(entityID int32) []byte {
	if s == nil {
		return nil
	}
	mgr := s.witherManager()
	mgr.mu.RLock()
	sk, ok := mgr.skulls[entityID]
	var dangerous bool
	if ok && sk != nil {
		dangerous = sk.Dangerous
	}
	mgr.mu.RUnlock()
	if !ok || sk == nil {
		return nil
	}
	return EncodeWitherSkullEntityDataPacket(entityID, dangerous)
}

// IsWitherDestroyableBlock reports whether a Wither explosion or block-breaking
// dash can destroy blockName in vanilla 26.1.2 (blocks in #minecraft:wither_immune
// like bedrock, end_portal, end_portal_frame, end_gateway, barrier, command_block
// cannot be destroyed).
func IsWitherDestroyableBlock(blockName string, allowStrongBlocks bool) bool {
	norm := canonicalResourceName(blockName)
	switch norm {
	case "minecraft:air", "minecraft:cave_air", "minecraft:void_air",
		"minecraft:water", "minecraft:lava",
		"minecraft:bedrock", "minecraft:barrier", "minecraft:light",
		"minecraft:end_portal", "minecraft:end_portal_frame", "minecraft:end_gateway",
		"minecraft:command_block", "minecraft:repeating_command_block", "minecraft:chain_command_block",
		"minecraft:structure_block", "minecraft:jigsaw", "minecraft:moving_piston",
		"minecraft:reinforced_deepslate":
		return false
	case "minecraft:obsidian", "minecraft:crying_obsidian", "minecraft:respawn_anchor",
		"minecraft:ancient_debris", "minecraft:netherite_block", "minecraft:enchanting_table",
		"minecraft:ender_chest", "minecraft:anvil", "minecraft:chipped_anvil", "minecraft:damaged_anvil":
		// Dangerous (blue) skulls and Wither body block-breaking dash can break obsidian,
		// whereas normal black skulls cannot.
		return allowStrongBlocks
	default:
		return true
	}
}

// TriggerWitherExplosion executes an explosion of radius at (x, y, z) in dimension,
// delegating to DetonateExplosion (or ExplodeMob) for raycast shielding, blast
// damage/knockback, and block drops, plus breaking high-resistance blocks when
// breakStrongBlocks is true (dangerous blue Wither skulls).
func (s *Server) TriggerWitherExplosion(dimension string, x, y, z, radius float64, breakStrongBlocks bool, sourceEntityID int32) int {
	if s == nil || radius <= 0 {
		return 0
	}
	normDim := world.NormalizeDimension(dimension)
	canGrief := s.MobGriefing() && s.CreeperBlockGriefing()

	res := s.DetonateExplosion(ExplosionOptions{
		Dimension:      normDim,
		X:              x,
		Y:              y,
		Z:              z,
		Radius:         radius,
		Category:       ExplosionSourceMob,
		DamageCause:    MobTypeWither,
		SourceEntityID: sourceEntityID,
		SourceMobType:  MobTypeWither,
		CausesFire:     false,
		BreakBlocks:    &canGrief,
	})

	cache := s.DimensionChunks(normDim)
	if breakStrongBlocks && cache != nil && canGrief {
		rInt := int(math.Ceil(radius))
		if rInt > 5 {
			rInt = 5
		}
		rSq := int(math.Ceil(radius * radius))
		if rSq > 25 {
			rSq = 25
		}
		bx0 := int(math.Floor(x))
		by0 := int(math.Floor(y))
		bz0 := int(math.Floor(z))
		for dy := -rInt; dy <= rInt; dy++ {
			by := by0 + dy
			if by <= world.MinY+1 || by >= world.MinY+world.WorldHeight-1 {
				continue
			}
			for dx := -rInt; dx <= rInt; dx++ {
				for dz := -rInt; dz <= rInt; dz++ {
					if dx*dx+dy*dy+dz*dz > rSq {
						continue
					}
					bx, bz := bx0+dx, bz0+dz
					st := cache.GetBlock(bx, by, bz)
					if st == world.StateAir || IsChestBlock(st) || IsFurnaceBlock(st) {
						continue
					}
					if !IsWitherDestroyableBlock(world.BlockName(st), breakStrongBlocks) {
						continue
					}
					cache.SetBlock(bx, by, bz, world.StateAir)
				}
			}
		}
	}

	return res.PlayersDamaged
}

// ShootWitherSkull spawns and launches a minecraft:wither_skull projectile from
// headIndex (0 = main center head, 1 = left head, 2 = right head) of the Wither
// with witherID toward (targetX, targetY, targetZ).
func (s *Server) ShootWitherSkull(witherID int32, headIndex int, targetX, targetY, targetZ float64, dangerous bool) (int32, bool) {
	if s == nil || s.entities == nil {
		return 0, false
	}
	st, ok := s.GetWitherState(witherID)
	if !ok {
		return 0, false
	}
	headX, headY, headZ := witherHeadPosition(st, headIndex)
	dx := targetX - headX
	dy := targetY - headY
	dz := targetZ - headZ
	dist := math.Sqrt(dx*dx + dy*dy + dz*dz)
	if dist < 1e-4 {
		dx, dy, dz, dist = 0, 0, 1, 1
	}
	vx := (dx / dist) * WitherSkullSpeed
	vy := (dy / dist) * WitherSkullSpeed
	vz := (dz / dist) * WitherSkullSpeed
	yaw := yawToward(dx, dz)
	pitch := float32(-math.Atan2(dy, math.Hypot(dx, dz)) * (180.0 / math.Pi))

	typeID := ResolveEntityTypeID(EntityTypeWitherSkull)
	if typeID < 0 {
		typeID = EntityTypeWitherSkullID
	}
	skullID := s.entities.Add(&world.Entity{
		TypeID:    typeID,
		TypeName:  EntityTypeWitherSkull,
		Dimension: st.Dimension,
		X:         headX,
		Y:         headY,
		Z:         headZ,
		Yaw:       yaw,
		Pitch:     pitch,
		HeadYaw:   yaw,
		VelocityX: clampVelocityShort(vx),
		VelocityY: clampVelocityShort(vy),
		VelocityZ: clampVelocityShort(vz),
		OnGround:  false,
	})

	proj := &WitherSkullProjectile{
		EntityID:      skullID,
		OwnerEntityID: witherID,
		Dimension:     st.Dimension,
		X:             headX,
		Y:             headY,
		Z:             headZ,
		VX:            vx,
		VY:            vy,
		VZ:            vz,
		Yaw:           yaw,
		Pitch:         pitch,
		Dangerous:     dangerous,
	}
	mgr := s.witherManager()
	mgr.mu.Lock()
	mgr.skulls[skullID] = proj
	mgr.mu.Unlock()

	cx := int32(int64(math.Floor(headX)) >> 4)
	cz := int32(int64(math.Floor(headZ)) >> 4)
	if ent := s.entities.Get(skullID); ent != nil {
		s.BroadcastDimensionChunk(st.Dimension, cx, cz, protocol.PlayAddEntity, encodeAddEntityPacket(nil, ent))
		s.BroadcastDimensionChunk(st.Dimension, cx, cz, PlaySetEntityDataCB, EncodeWitherSkullEntityDataPacket(skullID, dangerous))
	}
	s.BroadcastDimensionChunk(st.Dimension, cx, cz, PlaySoundPacketID,
		EncodeSoundPacket("minecraft:entity.wither.shoot", SoundSourceHostile, headX, headY, headZ, 2.0, 1.0, 0))
	return skullID, true
}

func witherHeadPosition(st WitherState, headIndex int) (x, y, z float64) {
	if headIndex <= 0 {
		return st.X, st.Y + 3.0, st.Z
	}
	rad := float64(st.Yaw+180.0) * (math.Pi / 180.0)
	if headIndex == 1 {
		rad += math.Pi / 2.0
	} else {
		rad -= math.Pi / 2.0
	}
	return st.X + math.Cos(rad)*1.3, st.Y + 2.2, st.Z + math.Sin(rad)*1.3
}

// DamageWither applies damage to a Wither boss, enforcing:
//   - Complete damage immunity while InvulnerableTicks > 0 (charging phase).
//   - Projectile immunity when in Phase 2 (Wither Armor: Health <= 150.0).
//   - Setting DestroyBlocksTicks = 20 when damaged so the Wither chews through blocks.
//   - Syncing the Wither's health metadata and purple boss bar progress.
//   - Dropping 1x minecraft:nether_star + 50 XP and removing the boss bar on death.
func (s *Server) DamageWither(entityID int32, damage float32, attacker *PlayerSession, isProjectile bool) (dealt float32, killed bool) {
	if s == nil || s.entities == nil || damage <= 0 {
		return 0, false
	}
	mgr := s.witherManager()
	mgr.mu.Lock()
	st, ok := mgr.withers[entityID]
	if !ok || st == nil {
		mgr.mu.Unlock()
		return 0, false
	}
	// Immune during the 220-tick charging phase.
	if st.InvulnerableTicks > 0 {
		mgr.mu.Unlock()
		return 0, false
	}
	// In Phase 2 (Wither Armor, Health <= 150), arrows and projectiles bounce off harmlessly.
	if isProjectile && st.IsPowered() {
		mgr.mu.Unlock()
		return 0, false
	}

	st.Health -= damage
	st.DestroyBlocksTicks = WitherBlockBreakTicks
	if attacker != nil && attacker.EntityID != 0 {
		st.TargetEntityIDs[0] = attacker.EntityID
	}
	if st.Health <= 0 {
		st.Health = 0
		killed = true
		delete(mgr.withers, entityID)
	}
	snap := *st
	mgr.mu.Unlock()

	// Sync tracker health.
	tr := s.entityTracker()
	tr.mu.Lock()
	if killed {
		delete(tr.mobStates, entityID)
	} else {
		mem := tr.mobStateFor(entityID, tr.aiTickID)
		mem.healthInit = true
		mem.health = snap.Health
		mem.lastHealth = snap.Health
	}
	tr.mu.Unlock()

	cx := int32(int64(math.Floor(snap.X)) >> 4)
	cz := int32(int64(math.Floor(snap.Z)) >> 4)
	var attackerID int32
	var hurtYaw float32
	if attacker != nil {
		aSnap := attacker.Snapshot()
		attackerID = aSnap.EntityID
		hurtYaw = yawToward(aSnap.X-snap.X, aSnap.Z-snap.Z)
	}

	s.BroadcastDimensionChunk(snap.Dimension, cx, cz, protocol.PlayHurtAnimation, EncodeHurtAnimation(entityID, hurtYaw))
	s.BroadcastDimensionChunk(snap.Dimension, cx, cz, protocol.PlayDamageEvent, EncodeDamageEventPacket(entityID, attackerID))

	if !killed {
		s.EmitVibration(VibrationEvent{
			Dimension:      snap.Dimension,
			Type:           GameEventEntityDamage,
			X:              snap.X,
			Y:              snap.Y,
			Z:              snap.Z,
			Player:         attacker,
			SourceEntityID: entityID,
		})
		s.UpdateRuntimeBossBarHealth(snap.BossBarUUID, snap.Health, snap.MaxHealth)
		s.BroadcastDimensionChunk(snap.Dimension, cx, cz, PlaySetEntityDataCB,
			EncodeWitherEntityDataPacket(entityID, snap.Health, snap.TargetEntityIDs[0], snap.TargetEntityIDs[1], snap.TargetEntityIDs[2], snap.InvulnerableTicks))
		s.BroadcastDimensionChunk(snap.Dimension, cx, cz, PlaySoundPacketID,
			EncodeSoundPacket("minecraft:entity.wither.hurt", SoundSourceHostile, snap.X, snap.Y, snap.Z, 1.0, 1.0, 0))
		return damage, false
	}

	// Wither died: remove boss bar, broadcast death sound + event, remove entity, drop Nether Star + 50 XP.
	s.RemoveEntityBossBar(entityID)
	s.BroadcastDimensionChunk(snap.Dimension, cx, cz, PlaySoundPacketID,
		EncodeSoundPacket("minecraft:entity.wither.death", SoundSourceHostile, snap.X, snap.Y, snap.Z, 1.0, 1.0, 0))
	s.BroadcastDimensionChunk(snap.Dimension, cx, cz, protocol.PlayEntityEvent,
		EncodeEntityEventPacket(entityID, EntityEventDeath))
	s.entities.Remove(entityID)
	s.BroadcastDimensionChunk(snap.Dimension, cx, cz, protocol.PlayRemoveEntities,
		encodeRemoveEntitiesPacket(nil, []int32{entityID}))

	star := world.NewNamedItemStack(NetherStarItemID, 1)
	if !star.IsEmpty() {
		s.SpawnItemEntityInDimension(snap.Dimension, snap.X, snap.Y+0.5, snap.Z, star)
	}
	consumed := s.TriggerSculkCatalystOnMobDeath(snap.Dimension, snap.X, snap.Y, snap.Z, int(WitherDeathXP), attacker)
	if !consumed {
		s.SpawnExperienceOrbsInDimension(snap.Dimension, snap.X, snap.Y+0.5, snap.Z, WitherDeathXP)
	}
	s.EmitVibration(VibrationEvent{
		Dimension:      snap.Dimension,
		Type:           GameEventEntityDie,
		X:              snap.X,
		Y:              snap.Y,
		Z:              snap.Z,
		Player:         attacker,
		SourceEntityID: entityID,
	})

	if attacker != nil {
		s.TriggerEntityKilled(attacker, MobTypeWither)
		s.IncrementCriteriaObjectives(attacker.Profile.Name, "totalKillCount", 1)
	}
	return damage, true
}

// HealWither heals an active Wither boss by amount (clamped to MaxHealth) and
// updates its boss bar and SynchedEntityData.
func (s *Server) HealWither(entityID int32, amount float32) float32 {
	if s == nil || amount <= 0 {
		return 0
	}
	mgr := s.witherManager()
	mgr.mu.Lock()
	st, ok := mgr.withers[entityID]
	if !ok || st == nil || st.Health <= 0 {
		mgr.mu.Unlock()
		return 0
	}
	st.Health += amount
	if st.Health > st.MaxHealth {
		st.Health = st.MaxHealth
	}
	snap := *st
	mgr.mu.Unlock()

	s.SetMobHealth(entityID, snap.Health)
	s.UpdateRuntimeBossBarHealth(snap.BossBarUUID, snap.Health, snap.MaxHealth)
	cx := int32(int64(math.Floor(snap.X)) >> 4)
	cz := int32(int64(math.Floor(snap.Z)) >> 4)
	s.BroadcastDimensionChunk(snap.Dimension, cx, cz, PlaySetEntityDataCB,
		EncodeWitherEntityDataPacket(entityID, snap.Health, snap.TargetEntityIDs[0], snap.TargetEntityIDs[1], snap.TargetEntityIDs[2], snap.InvulnerableTicks))
	return snap.Health
}

// TickWithers steps all active Wither bosses and in-flight WitherSkull projectiles
// by one game tick (20 TPS).
func (s *Server) TickWithers(rng *rand.Rand) {
	if s == nil || s.entities == nil {
		return
	}
	// In Peaceful difficulty, vanilla WitherBoss immediately despawns.
	if s.Difficulty() == DifficultyPeaceful {
		for _, w := range s.ActiveWithers() {
			s.RemoveEntityBossBar(w.EntityID)
			s.entities.Remove(w.EntityID)
			mgr := s.witherManager()
			mgr.mu.Lock()
			delete(mgr.withers, w.EntityID)
			mgr.mu.Unlock()
			cx := int32(int64(math.Floor(w.X)) >> 4)
			cz := int32(int64(math.Floor(w.Z)) >> 4)
			s.BroadcastDimensionChunk(w.Dimension, cx, cz, protocol.PlayRemoveEntities,
				encodeRemoveEntitiesPacket(nil, []int32{w.EntityID}))
		}
		return
	}

	mgr := s.witherManager()
	mgr.mu.RLock()
	witherIDs := make([]int32, 0, len(mgr.withers))
	for id := range mgr.withers {
		witherIDs = append(witherIDs, id)
	}
	skullIDs := make([]int32, 0, len(mgr.skulls))
	for id := range mgr.skulls {
		skullIDs = append(skullIDs, id)
	}
	mgr.mu.RUnlock()

	for _, id := range witherIDs {
		s.tickSingleWither(id, rng)
	}
	for _, id := range skullIDs {
		s.tickSingleWitherSkull(id)
	}
}

func (s *Server) tickSingleWither(entityID int32, rng *rand.Rand) {
	ent := s.entities.Get(entityID)
	mgr := s.witherManager()
	mgr.mu.Lock()
	st, ok := mgr.withers[entityID]
	if !ok || st == nil || ent == nil {
		delete(mgr.withers, entityID)
		mgr.mu.Unlock()
		s.RemoveEntityBossBar(entityID)
		return
	}
	st.X = ent.X
	st.Y = ent.Y
	st.Z = ent.Z
	st.Yaw = ent.Yaw
	st.TickCounter++

	// 1. Invulnerable charging phase (220..1 ticks).
	if st.InvulnerableTicks > 0 {
		nextInv := st.InvulnerableTicks - 1
		st.InvulnerableTicks = nextInv
		if nextInv%10 == 0 {
			st.Health += 10.0
			if st.Health > st.MaxHealth {
				st.Health = st.MaxHealth
			}
		}
		snap := *st
		mgr.mu.Unlock()

		s.SetMobHealth(entityID, snap.Health)
		s.UpdateRuntimeBossBarHealth(snap.BossBarUUID, snap.Health, snap.MaxHealth)
		s.SyncRuntimeBossBarPlayersInRange(snap.BossBarUUID, snap.Dimension, snap.X, snap.Y, snap.Z, WitherBossBarRange)

		cx := int32(int64(math.Floor(snap.X)) >> 4)
		cz := int32(int64(math.Floor(snap.Z)) >> 4)
		s.BroadcastDimensionChunk(snap.Dimension, cx, cz, PlaySetEntityDataCB,
			EncodeWitherEntityDataPacket(entityID, snap.Health, snap.TargetEntityIDs[0], snap.TargetEntityIDs[1], snap.TargetEntityIDs[2], snap.InvulnerableTicks))

		if nextInv <= 0 {
			// Charging complete: trigger radius-7 explosion and play global wither spawn sound!
			s.TriggerWitherExplosion(snap.Dimension, snap.X, snap.Y+1.5, snap.Z, WitherSpawnExplosionRadius, true, entityID)
			s.BroadcastDimensionChunk(snap.Dimension, cx, cz, PlaySoundPacketID,
				EncodeSoundPacket("minecraft:entity.wither.spawn", SoundSourceHostile, snap.X, snap.Y, snap.Z, 10.0, 1.0, 0))
		}
		return
	}

	// 2. Active combat phase (Phase 1 Aerial or Phase 2 Armored).
	if st.TickCounter%WitherPassiveRegenIntervalTicks == 0 && st.Health < st.MaxHealth {
		st.Health += WitherPassiveRegenAmount
		if st.Health > st.MaxHealth {
			st.Health = st.MaxHealth
		}
	}

	// Find nearest valid player target in the Wither's dimension.
	target := s.findNearestWitherTarget(st.Dimension, st.X, st.Y, st.Z, WitherAggroRange)
	var targetChanged bool
	if target != nil {
		if st.TargetEntityIDs[0] != target.EntityID {
			st.TargetEntityIDs[0] = target.EntityID
			targetChanged = true
		}
	} else if st.TargetEntityIDs[0] != 0 {
		st.TargetEntityIDs[0] = 0
		targetChanged = true
	}

	nextX, nextY, nextZ := st.X, st.Y, st.Z
	nextYaw := st.Yaw
	var shootTarget *PlayerSession
	var shootMain, shootLeft, shootRight, shootBlue bool

	if target != nil {
		tSnap := target.Snapshot()
		desiredY := tSnap.Y
		if !st.IsPowered() {
			// Phase 1: hover ~5 blocks above the target.
			desiredY += WitherHoverHeightOffset
		}
		dy := desiredY - st.Y
		if math.Abs(dy) > 0.2 {
			stepY := dy * 0.3
			if stepY > WitherVerticalFlySpeed {
				stepY = WitherVerticalFlySpeed
			} else if stepY < -WitherVerticalFlySpeed {
				stepY = -WitherVerticalFlySpeed
			}
			nextY += stepY
		}

		dx := tSnap.X - st.X
		dz := tSnap.Z - st.Z
		horizDist := math.Hypot(dx, dz)
		if horizDist > 1e-3 {
			nextYaw = yawToward(dx, dz)
		}
		// Keep ~2..9 blocks horizontal distance in Phase 1, or close in during Phase 2.
		stopDist := 6.0
		if st.IsPowered() {
			stopDist = 2.0
		}
		if horizDist > stopDist {
			step := WitherHorizontalFlySpeed
			if horizDist-stopDist < step {
				step = horizDist - stopDist
			}
			nextX += (dx / horizDist) * step
			nextZ += (dz / horizDist) * step
		}

		st.ShootCooldownMain--
		if st.ShootCooldownMain <= 0 {
			st.ShootCooldownMain = WitherMainHeadShootCooldown
			shootMain = true
			shootTarget = target
			if rng != nil && rng.Float32() < 0.10 {
				shootBlue = true
			}
		}
		st.ShootCooldownLeft--
		if st.ShootCooldownLeft <= 0 {
			st.ShootCooldownLeft = WitherSideHeadShootCooldown
			shootLeft = true
			shootTarget = target
		}
		st.ShootCooldownRight--
		if st.ShootCooldownRight <= 0 {
			st.ShootCooldownRight = WitherSideHeadShootCooldown
			shootRight = true
			shootTarget = target
		}
	}

	// Block breaking counter when recently hurt (WitherBoss.destroyBlocksTick).
	breakBlocksNow := false
	if st.DestroyBlocksTicks > 0 {
		st.DestroyBlocksTicks--
		if st.DestroyBlocksTicks == 0 {
			breakBlocksNow = true
		}
	}

	st.X, st.Y, st.Z, st.Yaw = nextX, nextY, nextZ, nextYaw
	snap := *st
	mgr.mu.Unlock()

	s.entities.Update(entityID, func(e *world.Entity) {
		e.X = nextX
		e.Y = nextY
		e.Z = nextZ
		e.Yaw = nextYaw
		e.HeadYaw = nextYaw
	})
	s.SetMobHealth(entityID, snap.Health)
	s.UpdateRuntimeBossBarHealth(snap.BossBarUUID, snap.Health, snap.MaxHealth)
	s.SyncRuntimeBossBarPlayersInRange(snap.BossBarUUID, snap.Dimension, snap.X, snap.Y, snap.Z, WitherBossBarRange)

	if targetChanged || snap.TickCounter%WitherPassiveRegenIntervalTicks == 0 {
		cx := int32(int64(math.Floor(snap.X)) >> 4)
		cz := int32(int64(math.Floor(snap.Z)) >> 4)
		s.BroadcastDimensionChunk(snap.Dimension, cx, cz, PlaySetEntityDataCB,
			EncodeWitherEntityDataPacket(entityID, snap.Health, snap.TargetEntityIDs[0], snap.TargetEntityIDs[1], snap.TargetEntityIDs[2], snap.InvulnerableTicks))
	}

	if breakBlocksNow && s.CreeperBlockGriefing() {
		s.breakBlocksAroundWither(snap)
	}

	if shootTarget != nil && !shootTarget.IsDead() {
		tSnap := shootTarget.Snapshot()
		if shootMain {
			s.ShootWitherSkull(entityID, 0, tSnap.X, tSnap.Y+1.0, tSnap.Z, shootBlue)
		}
		if shootLeft {
			s.ShootWitherSkull(entityID, 1, tSnap.X, tSnap.Y+1.0, tSnap.Z, false)
		}
		if shootRight {
			s.ShootWitherSkull(entityID, 2, tSnap.X, tSnap.Y+1.0, tSnap.Z, false)
		}
	}
}

func (s *Server) breakBlocksAroundWither(st WitherState) {
	cache := s.DimensionChunks(st.Dimension)
	if cache == nil {
		return
	}
	bx0 := int(math.Floor(st.X))
	by0 := int(math.Floor(st.Y))
	bz0 := int(math.Floor(st.Z))
	brokeAny := false
	for dy := 0; dy <= 3; dy++ {
		by := by0 + dy
		if by <= world.MinY+1 || by >= world.MinY+world.WorldHeight-1 {
			continue
		}
		for dx := -1; dx <= 1; dx++ {
			for dz := -1; dz <= 1; dz++ {
				bx, bz := bx0+dx, bz0+dz
				blk := cache.GetBlock(bx, by, bz)
				if blk == world.StateAir || IsChestBlock(blk) || IsFurnaceBlock(blk) {
					continue
				}
				if !IsWitherDestroyableBlock(world.BlockName(blk), true) {
					continue
				}
				cache.SetBlock(bx, by, bz, world.StateAir)
				brokeAny = true
			}
		}
	}
	if brokeAny {
		cx := int32(int64(math.Floor(st.X)) >> 4)
		cz := int32(int64(math.Floor(st.Z)) >> 4)
		s.BroadcastDimensionChunk(st.Dimension, cx, cz, PlaySoundPacketID,
			EncodeSoundPacket("minecraft:entity.wither.break_block", SoundSourceHostile, st.X, st.Y, st.Z, 2.0, 1.0, 0))
	}
}

func (s *Server) tickSingleWitherSkull(skullID int32) {
	mgr := s.witherManager()
	mgr.mu.Lock()
	sk, ok := mgr.skulls[skullID]
	if !ok || sk == nil {
		mgr.mu.Unlock()
		return
	}
	sk.AgeTicks++
	sk.X += sk.VX
	sk.Y += sk.VY
	sk.Z += sk.VZ
	snap := *sk
	mgr.mu.Unlock()

	if snap.AgeTicks > WitherSkullMaxAgeTicks {
		s.removeWitherSkull(skullID, snap.Dimension, snap.X, snap.Z)
		return
	}

	s.entities.Update(skullID, func(e *world.Entity) {
		e.X = snap.X
		e.Y = snap.Y
		e.Z = snap.Z
	})

	// Check player collision (radius 1.25 blocks).
	for _, sess := range s.onlinePlayersSnapshot() {
		if sess == nil || sess.IsDead() {
			continue
		}
		pSnap := sess.Snapshot()
		if world.NormalizeDimension(pSnap.Dimension) != snap.Dimension {
			continue
		}
		if pSnap.GameMode == GameModeSpectator || pSnap.Health <= 0 {
			continue
		}
		dx := pSnap.X - snap.X
		dy := (pSnap.Y + 0.9) - snap.Y
		dz := pSnap.Z - snap.Z
		if dx*dx+dy*dy+dz*dz <= 1.5625 {
			s.impactWitherSkull(snap, sess)
			return
		}
	}

	// Check living non-undead mob collision (radius 1.25 blocks).
	for _, mobEnt := range s.entities.AppendAll(nil) {
		if mobEnt.ID == snap.EntityID || mobEnt.ID == snap.OwnerEntityID || mobEnt.IsItem() {
			continue
		}
		if mobEnt.EffectiveDimension() != snap.Dimension || !IsValidWitherTargetMob(mobEnt.TypeName) {
			continue
		}
		if s.MobHealth(mobEnt.ID) <= 0 {
			continue
		}
		dx := mobEnt.X - snap.X
		dy := (mobEnt.Y + 0.8) - snap.Y
		dz := mobEnt.Z - snap.Z
		if dx*dx+dy*dy+dz*dz <= 1.5625 {
			s.impactWitherSkullOnMob(snap, mobEnt)
			return
		}
	}

	// Check solid block collision.
	cache := s.DimensionChunks(snap.Dimension)
	if cache != nil {
		bx := int(math.Floor(snap.X))
		by := int(math.Floor(snap.Y))
		bz := int(math.Floor(snap.Z))
		if by >= world.MinY && by < world.MinY+world.WorldHeight {
			blk := cache.GetBlock(bx, by, bz)
			if !isPassableBlock(blk) && blk != world.StateWater {
				_, _ = s.OnProjectileBlockImpact(snap.Dimension, bx, by, bz, snap.X-snap.VX, snap.Y-snap.VY, snap.Z-snap.VZ, snap.X, snap.Y, snap.Z, snap.EntityID, EntityTypeWitherSkull)
				s.impactWitherSkull(snap, nil)
				return
			}
		}
	}
}

func (s *Server) impactWitherSkull(sk WitherSkullProjectile, directHitPlayer *PlayerSession) {
	s.removeWitherSkull(sk.EntityID, sk.Dimension, sk.X, sk.Z)

	if directHitPlayer != nil && !directHitPlayer.IsDead() {
		scaled := ScaleDamageByDifficulty(WitherSkullDirectDamage, s.Difficulty())
		dealt := s.DamagePlayerWithArmor(directHitPlayer, scaled, "wither_skull")
		if dealt > 0 {
			// Direct hit heals the firing Wither by +5.0 HP (vanilla WitherSkull.onHitEntity).
			if sk.OwnerEntityID > 0 {
				s.HealWither(sk.OwnerEntityID, WitherSkullHitHealAmount)
			}
			// Inflict Wither II (amplifier 1) for 10s on Normal or 40s on Hard.
			diff := s.Difficulty()
			var dur int32
			if diff == DifficultyNormal {
				dur = WitherEffectDurationNormal
			} else if diff == DifficultyHard {
				dur = WitherEffectDurationHard
			}
			if dur > 0 && !directHitPlayer.IsDead() {
				s.ApplyPlayerEffect(directHitPlayer, NewStatusEffectInstance(EffectWither, dur, WitherEffectAmplifier))
			}
			if directHitPlayer.IsDead() {
				s.TriggerKilledByEntity(directHitPlayer, MobTypeWither)
				s.BroadcastDeathMessage(directHitPlayer, MobTypeWither, MobTypeWither)
			}
		}
	}

	s.TriggerWitherExplosion(sk.Dimension, sk.X, sk.Y, sk.Z, WitherSkullExplosionRadius, sk.Dangerous, sk.OwnerEntityID)
}

func (s *Server) impactWitherSkullOnMob(sk WitherSkullProjectile, targetMob world.Entity) {
	s.removeWitherSkull(sk.EntityID, sk.Dimension, sk.X, sk.Z)

	cache := s.DimensionChunks(sk.Dimension)
	bx, by, bz := int(math.Floor(targetMob.X)), int(math.Floor(targetMob.Y)), int(math.Floor(targetMob.Z))
	var preGround uint16
	var hadValidRoseGround bool
	if cache != nil {
		preGround = cache.GetBlock(bx, by-1, bz)
		hadValidRoseGround = CanPlaceWitherRoseOn(world.BlockName(preGround))
	}

	scaled := ScaleDamageByDifficulty(WitherSkullDirectDamage, s.Difficulty())
	killed := s.applyDamageToMob(&targetMob, scaled, nil, 0.3, false, false)
	if sk.OwnerEntityID > 0 {
		s.HealWither(sk.OwnerEntityID, WitherSkullHitHealAmount)
	}
	if !killed {
		diff := s.Difficulty()
		var dur int32
		if diff == DifficultyNormal {
			dur = WitherEffectDurationNormal
		} else if diff == DifficultyHard {
			dur = WitherEffectDurationHard
		}
		if dur > 0 {
			s.ApplyEntityEffectScaled(targetMob.ID, NewStatusEffectInstance(EffectWither, dur, WitherEffectAmplifier), 1.0)
		}
	}
	s.TriggerWitherExplosion(sk.Dimension, sk.X, sk.Y, sk.Z, WitherSkullExplosionRadius, sk.Dangerous, sk.OwnerEntityID)
	if killed {
		if cache != nil && hadValidRoseGround && cache.GetBlock(bx, by-1, bz) == world.StateAir {
			cache.SetBlock(bx, by-1, bz, preGround)
		}
		s.OnMobKilledByWither(sk.Dimension, targetMob.X, targetMob.Y, targetMob.Z)
	}
}

func (s *Server) removeWitherSkull(skullID int32, dim string, x, z float64) {
	mgr := s.witherManager()
	mgr.mu.Lock()
	delete(mgr.skulls, skullID)
	mgr.mu.Unlock()

	if s.entities != nil {
		s.entities.Remove(skullID)
	}
	cx := int32(int64(math.Floor(x)) >> 4)
	cz := int32(int64(math.Floor(z)) >> 4)
	s.BroadcastDimensionChunk(dim, cx, cz, protocol.PlayRemoveEntities, encodeRemoveEntitiesPacket(nil, []int32{skullID}))
}

func (s *Server) findNearestWitherTarget(dim string, x, y, z, maxRange float64) *PlayerSession {
	maxDistSq := maxRange * maxRange
	var bestSurvival *PlayerSession
	bestSurvivalDistSq := maxDistSq
	var bestFallback *PlayerSession
	bestFallbackDistSq := maxDistSq

	for _, sess := range s.onlinePlayersSnapshot() {
		if sess == nil || sess.IsDead() {
			continue
		}
		snap := sess.Snapshot()
		if world.NormalizeDimension(snap.Dimension) != dim || snap.GameMode == GameModeSpectator || snap.Health <= 0 {
			continue
		}
		dx := snap.X - x
		dy := snap.Y - y
		dz := snap.Z - z
		dSq := dx*dx + dy*dy + dz*dz
		if dSq > maxDistSq {
			continue
		}
		if snap.GameMode == GameModeSurvival || snap.GameMode == GameModeAdventure {
			if bestSurvival == nil || dSq < bestSurvivalDistSq {
				bestSurvival = sess
				bestSurvivalDistSq = dSq
			}
		} else if bestFallback == nil || dSq < bestFallbackDistSq {
			bestFallback = sess
			bestFallbackDistSq = dSq
		}
	}
	if bestSurvival != nil {
		return bestSurvival
	}
	return bestFallback
}

// IsBossOrBossSupportEntity reports whether typeName is a Wither, Wither Skull,
// Ender Dragon, End Crystal, Dragon Fireball, or Area Effect Cloud managed by
// the dedicated boss fight tick loops.
func IsBossOrBossSupportEntity(typeName string) bool {
	switch strings.ToLower(strings.TrimSpace(typeName)) {
	case MobTypeWither, EntityTypeWitherSkull,
		MobTypeEnderDragon, EntityTypeEndCrystal,
		EntityTypeDragonFireball, EntityTypeAreaEffectCloud,
		EntityTypeFireworkRocket:
		return true
	default:
		return false
	}
}

// BossOrSupportEntityDataPacket returns the initial SynchedEntityData packet
// payload when a viewer starts tracking a boss or boss-support entity.
func (s *Server) BossOrSupportEntityDataPacket(entityID int32, typeName string) []byte {
	switch canonicalResourceName(typeName) {
	case MobTypeWither:
		return s.WitherEntityDataPacket(entityID)
	case EntityTypeWitherSkull:
		return s.WitherSkullEntityDataPacket(entityID)
	case MobTypeEnderDragon:
		return s.EnderDragonEntityDataPacket(entityID)
	case EntityTypeEndCrystal:
		return s.EndCrystalEntityDataPacket(entityID)
	case EntityTypeFireworkRocket:
		return s.FireworkRocketEntityDataPacket(entityID)
	default:
		return nil
	}
}
