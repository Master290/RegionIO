package server

import (
	"errors"
	"math"
	"math/rand"
	"sync"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

// ============================================================================
// HOSTILE & BOSS MOB 26.1.2 POLISH
// (Spider, Cave Spider, Cobweb/Weaving, Slime/MagmaCube, ZombieVillager,
//  Illagers/Witch/Ravager, Wither & Ender Dragon helpers — vanilla 26.1.2 / protocol 775)
// ============================================================================

const (
	// MobTypeCaveSpider is the canonical entity identifier for CaveSpider.
	MobTypeCaveSpider = "minecraft:cave_spider"

	// EntityTypeCaveSpiderID is BuiltInRegistries.ENTITY_TYPE numeric ID for
	// "minecraft:cave_spider" in Minecraft 26.1.2 (protocol 775).
	EntityTypeCaveSpiderID int = 22

	// DefaultCaveSpiderMaxHealth is CaveSpider's MAX_HEALTH attribute in 26.1.2 (12.0 HP vs Spider's 16.0 HP).
	DefaultCaveSpiderMaxHealth float32 = 12.0

	// EntityDataSpiderFlagsID is Spider.DATA_FLAGS_ID (index 16, BYTE serializer 0) in 26.1.2.
	EntityDataSpiderFlagsID byte = 16

	// SpiderFlagClimbing is bit 0x01 in Spider.DATA_FLAGS_ID indicating wall climbing.
	SpiderFlagClimbing byte = 0x01

	// SpiderArmadilloAvoidRange is the 6.0-block radius within which spiders and
	// cave spiders flee from unrolled armadillos (!armadillo.isScared()) in 26.1.2.
	SpiderArmadilloAvoidRange = 6.0

	// CaveSpiderPoisonDurationNormalTicks (7s = 140t) and CaveSpiderPoisonDurationHardTicks (15s = 300t)
	// match CaveSpider.doHurtTarget in 26.1.2.
	CaveSpiderPoisonDurationNormalTicks int32 = 140
	CaveSpiderPoisonDurationHardTicks   int32 = 300

	// Cobweb velocity multipliers from WebBlock.entityInside in 26.1.2:
	//   - Normal entity: (0.25, 0.05, 0.25)
	//   - Entity with MobEffects.WEAVING: (0.5, 0.25, 0.5)
	//   - Spider / CaveSpider: (1.0, 1.0, 1.0) (immune in Spider.makeStuckInBlock)
	CobwebHorizontalSlowMultiplier        = 0.25
	CobwebVerticalSlowMultiplier          = 0.05
	CobwebWeavingHorizontalSlowMultiplier = 0.50
	CobwebWeavingVerticalSlowMultiplier   = 0.25

	// EntityDataZombieVillagerDataFinalizedID is ZombieVillager.DATA_VILLAGER_DATA_FINALIZED
	// (index 21, BOOLEAN serializer 8) in Minecraft 26.1.2.
	EntityDataZombieVillagerDataFinalizedID byte = 21

	// ZombieVillagerMaxCatalystBlocks is the maximum number of iron_bars and bed
	// blocks counted within a 4-block cube to accelerate ZombieVillager curing in 26.1.2.
	ZombieVillagerMaxCatalystBlocks = 14
)

var spiderClimbingFlags sync.Map // map[int32]bool

// IsSpiderFamilyMob reports whether typeName is `minecraft:spider` or `minecraft:cave_spider`.
func IsSpiderFamilyMob(typeName string) bool {
	switch canonicalResourceName(typeName) {
	case MobTypeSpider, MobTypeCaveSpider:
		return true
	default:
		return false
	}
}

// EncodeSpiderFlagsDataPacket encodes a ClientboundSetEntityDataPacket (0x63)
// updating `Spider.DATA_FLAGS_ID` (index 16, BYTE serializer 0, bit 0x01 = climbing).
func EncodeSpiderFlagsDataPacket(entityID int32, climbing bool) []byte {
	var flags byte
	if climbing {
		flags |= SpiderFlagClimbing
	}
	w := protocol.NewWriter(12)
	w.VarInt(entityID)
	w.Byte(EntityDataSpiderFlagsID)
	w.VarInt(EntityDataSerializerByte)
	w.Byte(flags)
	w.Byte(0xFF)
	return w.Bytes()
}

// DecodeSpiderFlagsDataPacket decodes a ClientboundSetEntityDataPacket (0x63)
// produced by EncodeSpiderFlagsDataPacket.
func DecodeSpiderFlagsDataPacket(r *protocol.Reader) (entityID int32, climbing bool, err error) {
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
			break
		}
		ser, err := r.VarInt()
		if err != nil {
			return 0, false, err
		}
		if ser != EntityDataSerializerByte {
			return 0, false, errors.New("spider: unexpected entity data serializer")
		}
		b, err := r.ReadByte()
		if err != nil {
			return 0, false, err
		}
		if idx == EntityDataSpiderFlagsID {
			climbing = (b & SpiderFlagClimbing) != 0
		}
	}
	return entityID, climbing, nil
}

// SetSpiderClimbing updates the wall-climbing state (`DATA_FLAGS_ID` bit 0x01)
// of a spider or cave spider and broadcasts `PlaySetEntityData` to viewers.
func (s *Server) SetSpiderClimbing(entityID int32, climbing bool) bool {
	if s == nil || s.entities == nil || entityID <= 0 {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || !IsSpiderFamilyMob(ent.TypeName) {
		return false
	}
	if climbing {
		spiderClimbingFlags.Store(entityID, true)
	} else {
		spiderClimbingFlags.Delete(entityID)
	}
	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, protocol.PlaySetEntityData,
		EncodeSpiderFlagsDataPacket(entityID, climbing))
	return true
}

// IsSpiderClimbing reports whether a spider or cave spider currently has its
// climbing bit (`0x01` in `DATA_FLAGS_ID`) set.
func (s *Server) IsSpiderClimbing(entityID int32) bool {
	if entityID <= 0 {
		return false
	}
	v, ok := spiderClimbingFlags.Load(entityID)
	return ok && v == true
}

// SpawnCaveSpider spawns a `minecraft:cave_spider` entity (12.0 HP, entity ID 22)
// in `dimension` at `(x, y, z)`.
func (s *Server) SpawnCaveSpider(dimension string, x, y, z float64) (int32, error) {
	if s == nil || s.entities == nil {
		return 0, errors.New("server: nil entity manager")
	}
	normDim := world.NormalizeDimension(dimension)
	typeID := ResolveEntityTypeID(MobTypeCaveSpider)
	if typeID < 0 {
		typeID = EntityTypeCaveSpiderID
	}
	id := s.entities.Add(&world.Entity{
		TypeID:    typeID,
		TypeName:  MobTypeCaveSpider,
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
	mem.health = DefaultCaveSpiderMaxHealth
	mem.lastHealth = DefaultCaveSpiderMaxHealth
	tr.mu.Unlock()

	if ent := s.entities.Get(id); ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, ent))
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlaySetEntityData, EncodeSpiderFlagsDataPacket(id, false))
	}
	return id, nil
}

// CaveSpiderPoisonDurationTicks returns the Poison I duration in ticks inflicted
// by `CaveSpider.doHurtTarget` in vanilla 26.1.2:
//   - Peaceful / Easy: 0 ticks (no Poison)
//   - Normal: 140 ticks (7 seconds)
//   - Hard: 300 ticks (15 seconds)
func CaveSpiderPoisonDurationTicks(difficulty Difficulty) int32 {
	switch difficulty {
	case DifficultyNormal:
		return CaveSpiderPoisonDurationNormalTicks
	case DifficultyHard:
		return CaveSpiderPoisonDurationHardTicks
	default:
		return 0
	}
}

// AttackPlayerFromCaveSpider performs a melee bite from a `minecraft:cave_spider`
// against `target` and inflicts difficulty-scaled `minecraft:poison` (7s on
// Normal, 15s on Hard) when the bite deals damage.
func (s *Server) AttackPlayerFromCaveSpider(spiderID int32, spiderX, spiderZ float64, target *PlayerSession) (dealt float32, poisoned bool) {
	if s == nil || target == nil || target.IsDead() {
		return 0, false
	}
	dealt = s.AttackPlayerFromMob(spiderID, MobTypeCaveSpider, spiderX, spiderZ, target, SpiderAttackDamage)
	if dealt <= 0 || target.IsDead() {
		return dealt, false
	}
	dur := CaveSpiderPoisonDurationTicks(s.Difficulty())
	if dur > 0 {
		s.ApplyPlayerEffect(target, NewStatusEffectInstance(EffectPoison, dur, 0))
		poisoned = true
	}
	return dealt, poisoned
}

// CanSpiderBeAffectedByEffect reports whether a `minecraft:spider` or
// `minecraft:cave_spider` can be affected by `effectID` in vanilla 26.1.2
// (`Spider.canBeAffected` returns false for `MobEffects.POISON`).
func CanSpiderBeAffectedByEffect(effectID string) bool {
	return canonicalResourceName(effectID) != EffectPoison
}

// HasUnrolledArmadilloNearSpider searches for an unrolled (`!IsArmadilloRolledUp`)
// `minecraft:armadillo` within `radius` (6.0 blocks) of `(x, y, z)` in `dimension`,
// matching `Spider.registerGoals` `AvoidEntityGoal(Armadillo.class, 6.0F, 1.0, 1.2, a -> !a.isScared())`.
func (s *Server) HasUnrolledArmadilloNearSpider(dimension string, x, y, z, radius float64) (armadilloID int32, ax, ay, az float64, ok bool) {
	if s == nil || s.entities == nil || radius <= 0 {
		return 0, 0, 0, 0, false
	}
	normDim := world.NormalizeDimension(dimension)
	maxDistSq := radius * radius
	bestDistSq := maxDistSq + 1.0
	all := s.entities.AppendAll(nil)
	for i := range all {
		e := &all[i]
		if e.EffectiveDimension() != normDim || canonicalResourceName(e.TypeName) != MobTypeArmadillo {
			continue
		}
		if s.IsArmadilloRolledUp(e.ID) {
			continue
		}
		dx := e.X - x
		dy := e.Y - y
		dz := e.Z - z
		distSq := dx*dx + dy*dy + dz*dz
		if distSq <= maxDistSq && distSq < bestDistSq {
			bestDistSq = distSq
			armadilloID = e.ID
			ax, ay, az = e.X, e.Y, e.Z
			ok = true
		}
	}
	return armadilloID, ax, ay, az, ok
}

// SelectSpiderGroupEffect2612 implements `Spider.SpiderEffectsGroupData.setRandomEffect(RandomSource)`
// in vanilla 26.1.2:
//   - roll 0 or 1 (40%): `minecraft:speed`
//   - roll 2 (20%): `minecraft:strength`
//   - roll 3 (20%): `minecraft:regeneration`
//   - roll 4 (20%): `minecraft:invisibility`
func SelectSpiderGroupEffect2612(rng *rand.Rand) string {
	if rng == nil {
		return EffectSpeed
	}
	switch rng.Intn(5) {
	case 0, 1:
		return EffectSpeed
	case 2:
		return EffectStrength
	case 3:
		return EffectRegeneration
	default:
		return EffectInvisibility
	}
}

// SpawnSpiderWith2612GroupData spawns a `minecraft:spider` and applies vanilla
// 26.1.2 `Spider.finalizeSpawn`:
//   - 1% chance (`rng.Intn(100) == 0`) to spawn a `minecraft:skeleton` Spider Jockey riding the spider.
//   - On Hard difficulty (`DifficultyHard`), `0.1 * specialMultiplier` chance to grant a permanent
//     status effect (`Speed`, `Strength`, `Regeneration`, or `Invisibility`).
func (s *Server) SpawnSpiderWith2612GroupData(dimension string, x, y, z float64, difficulty Difficulty, specialMultiplier float32, rng *rand.Rand) (spiderID, jockeySkeletonID int32, permanentEffectID string, err error) {
	spiderID, err = s.SpawnMobInDimension(dimension, x, y, z, MobTypeSpider)
	if err != nil {
		return 0, 0, "", err
	}
	if rng == nil {
		rng = rand.New(rand.NewSource(1))
	}
	if rng.Intn(100) == 0 {
		skelID, skelErr := s.SpawnMobInDimension(dimension, x, y, z, MobTypeSkeleton)
		if skelErr == nil && skelID > 0 {
			jockeySkeletonID = skelID
			_, _ = s.MountEntityOnVehicle(skelID, spiderID)
		}
	}
	if difficulty == DifficultyHard && rng.Float32() < 0.1*specialMultiplier {
		permanentEffectID = SelectSpiderGroupEffect2612(rng)
		s.ApplyEntityEffectScaled(spiderID, NewStatusEffectInstance(permanentEffectID, InfiniteEffectDuration, 0), 1.0)
	}
	return spiderID, jockeySkeletonID, permanentEffectID, nil
}

// RollSpiderDeathLoot2612 rolls the vanilla 26.1.2 death loot table for
// `minecraft:spider` and `minecraft:cave_spider`:
//   - Pool 1: `0..2` `minecraft:string` (+ `0..lootingLevel`)
//   - Pool 2 (`killed_by_player`): `-1..1` (`rng.Intn(3) - 1` + `0..lootingLevel`) `minecraft:spider_eye`
func RollSpiderDeathLoot2612(killedByPlayer bool, rng *rand.Rand, lootingLevel int) []world.ItemStack {
	if lootingLevel < 0 {
		lootingLevel = 0
	}
	var out []world.ItemStack
	stringCount := 1
	if rng != nil {
		stringCount = rng.Intn(3)
		if lootingLevel > 0 {
			stringCount += rng.Intn(lootingLevel + 1)
		}
	}
	if stringCount > 0 {
		if st := world.NewNamedItemStack("minecraft:string", int32(stringCount)); !st.IsEmpty() {
			out = append(out, st)
		}
	}
	if killedByPlayer {
		eyeCount := 1
		if rng != nil {
			eyeCount = rng.Intn(3) - 1
			if lootingLevel > 0 {
				eyeCount += rng.Intn(lootingLevel + 1)
			}
		}
		if eyeCount > 0 {
			if st := world.NewNamedItemStack(SpiderEyeItemID, int32(eyeCount)); !st.IsEmpty() {
				out = append(out, st)
			}
		}
	}
	return out
}

// ============================================================================
// COBWEB & WEAVING 26.1.2 POLISH
// ============================================================================

// CobwebMotionMultiplier returns the `(mx, my, mz)` stuck-in-block motion
// multipliers applied by `WebBlock.entityInside` and `Spider.makeStuckInBlock`
// in Minecraft 26.1.2:
//   - `minecraft:spider` and `minecraft:cave_spider`: `(1.0, 1.0, 1.0)` (immune)
//   - Entities with `minecraft:weaving`: `(0.5, 0.25, 0.5)`
//   - All other entities: `(0.25, 0.05, 0.25)`
func CobwebMotionMultiplier(entityType string, hasWeaving bool) (mx, my, mz float64) {
	if IsSpiderFamilyMob(entityType) {
		return 1.0, 1.0, 1.0
	}
	if hasWeaving {
		return CobwebWeavingHorizontalSlowMultiplier, CobwebWeavingVerticalSlowMultiplier, CobwebWeavingHorizontalSlowMultiplier
	}
	return CobwebHorizontalSlowMultiplier, CobwebVerticalSlowMultiplier, CobwebHorizontalSlowMultiplier
}

// ApplyCobwebSlowdownAt checks whether `entityID` is standing in a
// `minecraft:cobweb` block and returns the vanilla 26.1.2 motion multiplier
// `(mx, my, mz)` and `inCobweb = true`, scaling the entity's velocity when
// `mx < 1.0`.
func (s *Server) ApplyCobwebSlowdownAt(entityID int32) (mx, my, mz float64, inCobweb bool) {
	if s == nil || s.entities == nil || entityID <= 0 {
		return 1.0, 1.0, 1.0, false
	}
	ent := s.entities.Get(entityID)
	if ent == nil {
		return 1.0, 1.0, 1.0, false
	}
	cache := s.DimensionChunks(ent.EffectiveDimension())
	if cache == nil {
		return 1.0, 1.0, 1.0, false
	}
	bx := int(math.Floor(ent.X))
	by := int(math.Floor(ent.Y))
	bz := int(math.Floor(ent.Z))
	if world.BlockName(cache.GetBlock(bx, by, bz)) != "minecraft:cobweb" {
		return 1.0, 1.0, 1.0, false
	}
	hasWeaving := s.HasEntityEffect(entityID, EffectWeaving)
	mx, my, mz = CobwebMotionMultiplier(ent.TypeName, hasWeaving)
	if mx < 1.0 || my < 1.0 || mz < 1.0 {
		s.entities.Update(entityID, func(e *world.Entity) {
			e.VelocityX = int16(float64(e.VelocityX) * mx)
			e.VelocityY = int16(float64(e.VelocityY) * my)
			e.VelocityZ = int16(float64(e.VelocityZ) * mz)
		})
	}
	return mx, my, mz, true
}

// ============================================================================
// SLIME & MAGMA CUBE 26.1.2 POLISH
// ============================================================================

// SlimeAttributesBySize2612 computes the vanilla 26.1.2 attributes for a
// `minecraft:slime` (`isMagmaCube == false`) or `minecraft:magma_cube`
// (`isMagmaCube == true`) of `size`:
//   - `size` is clamped to `[1, 127]` (`Mth.clamp(size, 1, 127)`).
//   - `maxHealth = size * size` (`1, 4, 16` for sizes `1, 2, 4`).
//   - `movementSpeed = 0.2 + 0.1 * size` (`0.3, 0.4, 0.6`).
//   - For `Slime`: `dealsDamage = size > 1` (`!isTiny()`), `attackDamage = size` (`0` when `size == 1`), `armor = 0`.
//   - For `MagmaCube`: `dealsDamage = true` (even at `size == 1`), `attackDamage = size + 2` (`3, 4, 6`), `armor = size * 3` (`3, 6, 12`).
func SlimeAttributesBySize2612(isMagmaCube bool, size int) (maxHealth float32, movementSpeed float64, attackDamage float32, armor int, dealsDamage bool) {
	if size < 1 {
		size = 1
	} else if size > 127 {
		size = 127
	}
	maxHealth = float32(size * size)
	movementSpeed = 0.2 + 0.1*float64(size)
	if isMagmaCube {
		dealsDamage = true
		attackDamage = float32(size + 2)
		armor = size * 3
		return maxHealth, movementSpeed, attackDamage, armor, dealsDamage
	}
	dealsDamage = size > 1
	if dealsDamage {
		attackDamage = float32(size)
	}
	return maxHealth, movementSpeed, attackDamage, 0, dealsDamage
}

// IsSlimeChunk2612 implements vanilla `WorldgenRandom.seedSlimeChunk(chunkX, chunkZ, worldSeed, 987234911L).nextInt(10) == 0`
// using the exact 48-bit `java.util.Random` LCG.
func IsSlimeChunk2612(worldSeed int64, chunkX, chunkZ int) bool {
	cx := int32(chunkX)
	cz := int32(chunkZ)
	term1 := int64(cx * cx * 4987142)
	term2 := int64(cx * 5947611)
	term3 := int64(cz*cz) * 4392871
	term4 := int64(cz * 389711)
	seed := (worldSeed + term1 + term2 + term3 + term4) ^ 987234911
	state := (uint64(seed) ^ 0x5DEECE66D) & ((1 << 48) - 1)
	state = (state*0x5DEECE66D + 0xB) & ((1 << 48) - 1)
	bits := int32(state >> 17)
	return (bits % 10) == 0
}

// RollSlimeOrMagmaCubeDeathLoot2612 rolls the vanilla 26.1.2 death loot for
// `minecraft:slime` and `minecraft:magma_cube`:
//   - When killed by a Frog at `size == 1`:
//   - `Slime` drops `1` `minecraft:slime_ball`.
//   - `MagmaCube` drops `1` froglight matching `frogVariant` (`ochre_froglight`, `verdant_froglight`, or `pearlescent_froglight`).
//   - Otherwise:
//   - `Slime` drops `0..2` `minecraft:slime_ball` (+ `0..lootingLevel`) ONLY when `size == 1`.
//   - `MagmaCube` drops `minecraft:magma_cream` ONLY when `size > 1` (`rng.Intn(4) - 2` + `0..lootingLevel`, clamped to >= 0).
func RollSlimeOrMagmaCubeDeathLoot2612(isMagmaCube bool, size int, killedByFrog bool, frogVariant int32, rng *rand.Rand, lootingLevel int) []world.ItemStack {
	if lootingLevel < 0 {
		lootingLevel = 0
	}
	if killedByFrog && size == 1 {
		if isMagmaCube {
			flItem := FroglightItemForVariant(frogVariant)
			if st := world.NewNamedItemStack(flItem, 1); !st.IsEmpty() {
				return []world.ItemStack{st}
			}
			return nil
		}
		if st := world.NewNamedItemStack(SlimeBallItemID, 1); !st.IsEmpty() {
			return []world.ItemStack{st}
		}
		return nil
	}
	if isMagmaCube {
		if size <= 1 {
			return nil
		}
		count := 1
		if rng != nil {
			count = rng.Intn(4) - 2
			if lootingLevel > 0 {
				count += rng.Intn(lootingLevel + 1)
			}
		}
		if count <= 0 {
			return nil
		}
		if st := world.NewNamedItemStack(MagmaCreamItemID, int32(count)); !st.IsEmpty() {
			return []world.ItemStack{st}
		}
		return nil
	}
	if size != 1 {
		return nil
	}
	count := 1
	if rng != nil {
		count = rng.Intn(3)
		if lootingLevel > 0 {
			count += rng.Intn(lootingLevel + 1)
		}
	}
	if count <= 0 {
		return nil
	}
	if st := world.NewNamedItemStack(SlimeBallItemID, int32(count)); !st.IsEmpty() {
		return []world.ItemStack{st}
	}
	return nil
}

// ============================================================================
// ZOMBIE VILLAGER 26.1.2 SYNCHED ENTITY DATA & CURE POLISH
// ============================================================================

// EncodeZombieVillagerFullDataPacket2612 encodes a ClientboundSetEntityDataPacket (0x63)
// carrying all three 26.1.2 `ZombieVillager` SynchedEntityData entries:
//   - index 19 (`DATA_CONVERTING_ID`, `BOOLEAN` serializer 8)
//   - index 20 (`DATA_VILLAGER_DATA`, `VILLAGER_DATA` serializer 18)
//   - index 21 (`DATA_VILLAGER_DATA_FINALIZED`, `BOOLEAN` serializer 8)
func EncodeZombieVillagerFullDataPacket2612(entityID int32, converting bool, villagerTypeID, professionID, level int32, dataFinalized bool) []byte {
	if level < MinVillagerLevel {
		level = MinVillagerLevel
	}
	if level > MaxVillagerLevel {
		level = MaxVillagerLevel
	}
	w := protocol.NewWriter(24)
	w.VarInt(entityID)
	w.Byte(EntityDataZombieVillagerConvertingID)
	w.VarInt(EntityDataSerializerBool)
	w.Bool(converting)
	w.Byte(EntityDataZombieVillagerDataID)
	w.VarInt(EntityDataSerializerVillagerData)
	w.VarInt(villagerTypeID)
	w.VarInt(professionID)
	w.VarInt(level)
	w.Byte(EntityDataZombieVillagerDataFinalizedID)
	w.VarInt(EntityDataSerializerBool)
	w.Bool(dataFinalized)
	w.Byte(0xFF)
	return w.Bytes()
}

// DecodeZombieVillagerFullDataPacket2612 decodes a ClientboundSetEntityDataPacket (0x63)
// produced by EncodeZombieVillagerFullDataPacket2612.
func DecodeZombieVillagerFullDataPacket2612(r *protocol.Reader) (entityID int32, converting bool, villagerTypeID, professionID, level int32, dataFinalized bool, err error) {
	entityID, err = r.VarInt()
	if err != nil {
		return 0, false, 0, 0, 0, false, err
	}
	for {
		idx, err := r.ReadByte()
		if err != nil {
			return 0, false, 0, 0, 0, false, err
		}
		if idx == 0xFF {
			break
		}
		ser, err := r.VarInt()
		if err != nil {
			return 0, false, 0, 0, 0, false, err
		}
		switch idx {
		case EntityDataZombieVillagerConvertingID:
			if ser != EntityDataSerializerBool {
				return 0, false, 0, 0, 0, false, errors.New("zombie_villager: unexpected converting serializer")
			}
			converting, err = r.Bool()
			if err != nil {
				return 0, false, 0, 0, 0, false, err
			}
		case EntityDataZombieVillagerDataID:
			if ser != EntityDataSerializerVillagerData {
				return 0, false, 0, 0, 0, false, errors.New("zombie_villager: unexpected villager_data serializer")
			}
			villagerTypeID, err = r.VarInt()
			if err != nil {
				return 0, false, 0, 0, 0, false, err
			}
			professionID, err = r.VarInt()
			if err != nil {
				return 0, false, 0, 0, 0, false, err
			}
			level, err = r.VarInt()
			if err != nil {
				return 0, false, 0, 0, 0, false, err
			}
		case EntityDataZombieVillagerDataFinalizedID:
			if ser != EntityDataSerializerBool {
				return 0, false, 0, 0, 0, false, errors.New("zombie_villager: unexpected finalized serializer")
			}
			dataFinalized, err = r.Bool()
			if err != nil {
				return 0, false, 0, 0, 0, false, err
			}
		default:
			return 0, false, 0, 0, 0, false, errors.New("zombie_villager: unexpected entity data index")
		}
	}
	return entityID, converting, villagerTypeID, professionID, level, dataFinalized, nil
}

// ZombieVillagerCureStrengthEffect2612 returns the `minecraft:strength`
// `StatusEffectInstance` granted by `ZombieVillager.startConverting` in
// Minecraft 26.1.2 (`if (difficulty != Difficulty.EASY) addEffect(STRENGTH, time, Math.min(difficulty.getId() - 1, 0))`).
func ZombieVillagerCureStrengthEffect2612(difficulty Difficulty, durationTicks int32) (StatusEffectInstance, bool) {
	if difficulty == DifficultyPeaceful || difficulty == DifficultyEasy || durationTicks <= 0 {
		return StatusEffectInstance{}, false
	}
	amp := int(difficulty) - 1
	if amp > 0 {
		amp = 0
	}
	return NewStatusEffectInstance(EffectStrength, durationTicks, int32(amp)), true
}

// ComputeZombieVillagerCureTickStep2612 computes `ZombieVillager.getConversionProgress`
// in Minecraft 26.1.2:
//   - Base progress is `1` tick.
//   - With a `1%` (`0.01`) chance per tick, iterates over up to `14` nearby
//     `minecraft:iron_bars` and bed blocks within a 4-block cube, adding `+1`
//     for each block with a `30%` (`0.3`) chance.
func ComputeZombieVillagerCureTickStep2612(catalystBlockCount int, rng *rand.Rand) int {
	if catalystBlockCount > ZombieVillagerMaxCatalystBlocks {
		catalystBlockCount = ZombieVillagerMaxCatalystBlocks
	}
	step := 1
	if rng != nil && catalystBlockCount > 0 && rng.Float32() < 0.01 {
		for i := 0; i < catalystBlockCount; i++ {
			if rng.Float32() < 0.3 {
				step++
			}
		}
	}
	return step
}

// ============================================================================
// WITHER, ILLAGER, WITCH & RAVAGER 26.1.2 DEATH LOOT & XP POLISH
// ============================================================================

var witchLootPool2612 = [8]string{
	"minecraft:glowstone_dust",
	"minecraft:sugar",
	"minecraft:redstone",
	SpiderEyeItemID,
	"minecraft:glass_bottle",
	"minecraft:gunpowder",
	"minecraft:stick",
	"minecraft:stick", // weight 2 in vanilla entities/witch.json
}

// RollWitchDeathLoot2612 rolls the vanilla 26.1.2 `entities/witch.json` loot table:
// `1..3` rolls (`1 + rng.Intn(3)`) from the 7-item pool (`glowstone_dust`, `sugar`,
// `redstone`, `spider_eye`, `glass_bottle`, `gunpowder`, `stick` weight 2), with
// each roll producing `0..2` items (`+ 0..lootingLevel`).
func RollWitchDeathLoot2612(rng *rand.Rand, lootingLevel int) []world.ItemStack {
	if lootingLevel < 0 {
		lootingLevel = 0
	}
	if rng == nil {
		if st := world.NewNamedItemStack("minecraft:redstone", 1); !st.IsEmpty() {
			return []world.ItemStack{st}
		}
		return nil
	}
	rolls := 1 + rng.Intn(3)
	counts := make(map[string]int32, rolls)
	var order []string
	for i := 0; i < rolls; i++ {
		itemID := witchLootPool2612[rng.Intn(len(witchLootPool2612))]
		c := rng.Intn(3)
		if lootingLevel > 0 {
			c += rng.Intn(lootingLevel + 1)
		}
		if c <= 0 {
			continue
		}
		if _, exists := counts[itemID]; !exists {
			order = append(order, itemID)
		}
		counts[itemID] += int32(c)
	}
	var out []world.ItemStack
	for _, itemID := range order {
		if st := world.NewNamedItemStack(itemID, counts[itemID]); !st.IsEmpty() {
			out = append(out, st)
		}
	}
	return out
}

// RollEvokerDeathLoot2612 rolls the vanilla 26.1.2 `entities/evoker.json` loot table:
//   - Pool 1: always `1` `minecraft:totem_of_undying` (unaffected by Looting)
//   - Pool 2 (`killed_by_player`): `0..1` `minecraft:emerald` (`+ 0..lootingLevel`)
func RollEvokerDeathLoot2612(killedByPlayer bool, rng *rand.Rand, lootingLevel int) []world.ItemStack {
	if lootingLevel < 0 {
		lootingLevel = 0
	}
	var out []world.ItemStack
	if totem := world.NewNamedItemStack("minecraft:totem_of_undying", 1); !totem.IsEmpty() {
		out = append(out, totem)
	}
	if killedByPlayer {
		emeralds := 1
		if rng != nil {
			emeralds = rng.Intn(2)
			if lootingLevel > 0 {
				emeralds += rng.Intn(lootingLevel + 1)
			}
		}
		if emeralds > 0 {
			if em := world.NewNamedItemStack("minecraft:emerald", int32(emeralds)); !em.IsEmpty() {
				out = append(out, em)
			}
		}
	}
	return out
}

// RollVindicatorDeathLoot2612 rolls the vanilla 26.1.2 `entities/vindicator.json`
// loot table (`0..1` `minecraft:emerald` `+ 0..lootingLevel` when killed by player).
func RollVindicatorDeathLoot2612(killedByPlayer bool, rng *rand.Rand, lootingLevel int) []world.ItemStack {
	if !killedByPlayer {
		return nil
	}
	if lootingLevel < 0 {
		lootingLevel = 0
	}
	emeralds := 1
	if rng != nil {
		emeralds = rng.Intn(2)
		if lootingLevel > 0 {
			emeralds += rng.Intn(lootingLevel + 1)
		}
	}
	if emeralds <= 0 {
		return nil
	}
	if em := world.NewNamedItemStack("minecraft:emerald", int32(emeralds)); !em.IsEmpty() {
		return []world.ItemStack{em}
	}
	return nil
}

// RollPillagerDeathLoot2612 rolls the vanilla 26.1.2 `entities/pillager.json`
// loot table (`0..2` `minecraft:arrow` `+ 0..lootingLevel`).
func RollPillagerDeathLoot2612(rng *rand.Rand, lootingLevel int) []world.ItemStack {
	if lootingLevel < 0 {
		lootingLevel = 0
	}
	arrows := 1
	if rng != nil {
		arrows = rng.Intn(3)
		if lootingLevel > 0 {
			arrows += rng.Intn(lootingLevel + 1)
		}
	}
	if arrows <= 0 {
		return nil
	}
	if st := world.NewNamedItemStack(MobTypeArrow, int32(arrows)); !st.IsEmpty() {
		return []world.ItemStack{st}
	}
	return nil
}

// RollRavagerDeathLoot2612 rolls the vanilla 26.1.2 `entities/ravager.json`
// loot table (always `1` `minecraft:saddle`).
func RollRavagerDeathLoot2612() []world.ItemStack {
	if st := world.NewNamedItemStack("minecraft:saddle", 1); !st.IsEmpty() {
		return []world.ItemStack{st}
	}
	return nil
}

// RollWitherDeathLoot2612 returns the vanilla 26.1.2 `WitherBoss.dropCustomDeathLoot`
// drop (always `1` `minecraft:nether_star`, unaffected by Looting).
func RollWitherDeathLoot2612() []world.ItemStack {
	if st := world.NewNamedItemStack(NetherStarItemID, 1); !st.IsEmpty() {
		return []world.ItemStack{st}
	}
	return nil
}

// HostileBossPolishXPReward returns the vanilla 26.1.2 experience reward for
// zombie variants (`husk`, `drowned`, `stray`, `zombie_villager`, including
// `12` XP for baby zombie variants), illagers (`vex`, `illusioner`),
// `silverfish`, and `cave_spider`.
func HostileBossPolishXPReward(typeName string, isBaby bool) int {
	norm := canonicalResourceName(typeName)
	if isBaby {
		switch norm {
		case MobTypeZombie, MobTypeHusk, MobTypeDrowned, MobTypeZombieVillager:
			return 12
		default:
			return 0
		}
	}
	switch norm {
	case MobTypeHusk, MobTypeDrowned, MobTypeStray, MobTypeZombieVillager,
		MobTypeVex, MobTypeIllusioner, MobTypeSilverfish, MobTypeCaveSpider:
		return 5
	default:
		return 0
	}
}
