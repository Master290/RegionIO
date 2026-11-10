package server

import (
	"math"
	"math/rand"
	"strings"

	"regionio/internal/world"
)

// ============================================================================
// VANILLA 26.1.2 (PROTOCOL 775) TICK-BY-TICK PARITY: CREEPER & SKELETON FAMILY
// Verified against server-26.1.2.jar:
//   - net.minecraft.world.entity.monster.Creeper
//   - net.minecraft.world.entity.ai.goal.SwellGoal
//   - net.minecraft.world.entity.ai.goal.AvoidEntityGoal
//   - net.minecraft.world.entity.monster.skeleton.AbstractSkeleton
//   - net.minecraft.world.entity.monster.skeleton.Skeleton
//   - net.minecraft.world.entity.monster.skeleton.Stray
//   - net.minecraft.world.entity.monster.skeleton.Bogged
//   - net.minecraft.world.entity.ai.goal.RangedBowAttackGoal
//   - net.minecraft.world.entity.projectile.ProjectileUtil
//   - net.minecraft.world.entity.projectile.arrow.AbstractArrow
//   - net.minecraft.world.item.BowItem
//   - net.minecraft.world.entity.Mob (isSunBurnTick / burnUndead)
// ============================================================================

const (
	// Creeper (net.minecraft.world.entity.monster.Creeper & SwellGoal)
	CreeperDefaultMaxSwell               int     = 30
	CreeperDefaultExplosionRadius        int     = 3
	CreeperChargedRadiusMultiplier       float32 = 2.0
	CreeperSwellGoalStartDistSq          float64 = 9.0  // 3.0 blocks (distanceToSqr < 9.0D)
	CreeperSwellGoalCancelDistSq         float64 = 49.0 // 7.0 blocks (distanceToSqr > 49.0D)
	CreeperFallSwellMultiplier           float64 = 1.5  // (int)(fallDistance * 1.5F)
	CreeperFallSwellCapMargin            int     = 5    // maxSwell - 5
	CreeperAvoidCatMaxDist               float64 = 6.0
	CreeperAvoidCatWalkSpeedModifier     float64 = 1.0
	CreeperAvoidCatSprintSpeedModifier   float64 = 1.2
	CreeperAvoidCatSprintDistSqThreshold float64 = 49.0 // entity.distanceToSqr(toAvoid) < 49.0D
	CreeperLingeringCloudRadius          float32 = 2.5
	CreeperLingeringCloudRadiusOnUse     float32 = -0.5
	CreeperLingeringCloudWaitTime        int32   = 10
	CreeperLingeringCloudDuration        int32   = 300
	CreeperLingeringCloudPotionScale     float32 = 0.25
	CreeperFlintAndSteelMaxDurability    int32   = 64

	// Skeleton / Stray / Bogged & RangedBowAttackGoal
	SkeletonEasyNormalAttackIntervalTicks int     = 40   // AbstractSkeleton.getHardAttackInterval() = 20, else 40
	SkeletonHardAttackIntervalTicks       int     = 20   // Hard difficulty: 20 ticks
	BoggedEasyNormalAttackIntervalTicks   int     = 70   // Bogged.getAttackInterval() = 70
	BoggedHardAttackIntervalTicks         int     = 50   // Bogged.getHardAttackInterval() = 50
	RangedBowAttackRadius                 float64 = 15.0 // attackRadiusSqr = 225.0F
	RangedBowAttackRadiusSqr              float64 = 225.0
	RangedBowSpeedModifier                float64 = 1.0
	SkeletonBaseMovementSpeed             float64 = 0.25
	SkeletonMeleeAttackSpeedModifier      float64 = 1.2 // AbstractSkeleton$1 MeleeAttackGoal(this, 1.2D, false)
	RangedBowStrafeSeeTimeThreshold       int     = 20
	RangedBowStrafeFlipIntervalTicks      int     = 20
	RangedBowStrafeDirectionFlipChance    float32 = 0.3
	RangedBowStrafeForwardMaxDistRatio    float64 = 0.75 // d0 > attackRadiusSqr * 0.75F (168.75) -> strafingBackwards = false
	RangedBowStrafeBackwardMinDistRatio   float64 = 0.25 // d0 < attackRadiusSqr * 0.25F (56.25)  -> strafingBackwards = true
	RangedBowStrafeSpeed                  float32 = 0.5  // MoveControl.strafe(±0.5F, ±0.5F)
	RangedBowMinSeeTimeToStartUsing       int     = -60  // seeTime >= -60
	RangedBowCancelUsingUnseenTicks       int     = -60  // !hasLineOfSight && seeTime < -60
	RangedBowRequiredPullTicks            int     = 20   // i >= 20 -> stopUsingItem() + performRangedAttack
	SkeletonArrowLaunchVelocity           float32 = 1.6  // shoot(d0, d1 + d3 * 0.2, d2, 1.6F, (float)(14 - diff * 4))
	SkeletonArrowGravityAimFactor         float64 = 0.20000000298023224
	AbstractArrowMobBaseDamageMultiplier  float64 = 2.0 // power * 2.0F
	AbstractArrowMobDifficultyBonusScale  float64 = 0.11
	AbstractArrowMobTriangleSpread        float64 = 0.57425
	StrayArrowSlownessDurationTicks       int32   = 600 // MobEffects.SLOWNESS, 600 ticks (30s), amplifier 0
	UndeadSunBurnIgnitionSeconds          int32   = 8   // igniteForSeconds(8.0F) -> 160 ticks
	UndeadSunBurnIgnitionTicks            int32   = 160
)

// CreeperCustomConfig holds per-creeper custom NBT configuration (`Fuse`, `ExplosionRadius`).
type CreeperCustomConfig struct {
	MaxSwell        int
	ExplosionRadius int
}

var creeperConfigMap syncMapInt32[CreeperCustomConfig]

type syncMapInt32[V any] struct {
	m map[int32]V
}

// SetCreeperCustomConfig sets the custom `Fuse` (`maxSwell`, default 30) and
// `ExplosionRadius` (default 3) for a creeper entity.
func (s *Server) SetCreeperCustomConfig(entityID int32, maxSwell, explosionRadius int) {
	if s == nil || entityID <= 0 {
		return
	}
	if maxSwell <= 0 {
		maxSwell = CreeperDefaultMaxSwell
	}
	if explosionRadius < 0 {
		explosionRadius = CreeperDefaultExplosionRadius
	}
	tr := s.entityTracker()
	tr.mu.Lock()
	mem := tr.mobStateFor(entityID, tr.aiTickID)
	mem.creeperMaxSwell = maxSwell
	mem.creeperExplosionRadius = explosionRadius
	mem.creeperCustomRadiusSet = true
	tr.mu.Unlock()
}

// GetCreeperCustomConfig returns the effective `maxSwell` (default 30) and base
// `explosionRadius` (default 3) for `entityID`.
func (s *Server) GetCreeperCustomConfig(entityID int32) (maxSwell, explosionRadius int) {
	maxSwell = CreeperDefaultMaxSwell
	explosionRadius = CreeperDefaultExplosionRadius
	if s == nil || entityID <= 0 {
		return maxSwell, explosionRadius
	}
	tr := s.entityTracker()
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if mem := tr.mobStates[entityID]; mem != nil {
		if mem.creeperMaxSwell > 0 {
			maxSwell = mem.creeperMaxSwell
		}
		if mem.creeperCustomRadiusSet {
			explosionRadius = mem.creeperExplosionRadius
		}
	}
	return maxSwell, explosionRadius
}

// EncodeCreeperNBT2612 encodes a Creeper's NBT state matching vanilla 26.1.2
// `Creeper.addAdditionalSaveData(ValueOutput)`:
//   - `powered` (bool)
//   - `Fuse` (int16, default 30)
//   - `ExplosionRadius` (byte, default 3)
//   - `ignited` (bool)
func EncodeCreeperNBT2612(powered bool, fuse int16, explosionRadius byte, ignited bool) map[string]any {
	return map[string]any{
		"powered":         powered,
		"Fuse":            fuse,
		"ExplosionRadius": explosionRadius,
		"ignited":         ignited,
	}
}

// DecodeCreeperNBT2612 decodes a Creeper's NBT state matching vanilla 26.1.2
// `Creeper.readAdditionalSaveData(ValueInput)` with defaults `Fuse = 30` and
// `ExplosionRadius = 3`.
func DecodeCreeperNBT2612(nbtMap map[string]any) (powered bool, fuse int, explosionRadius int, ignited bool) {
	fuse = CreeperDefaultMaxSwell
	explosionRadius = CreeperDefaultExplosionRadius
	if nbtMap == nil {
		return
	}
	if v, ok := nbtMap["powered"].(bool); ok {
		powered = v
	}
	switch v := nbtMap["Fuse"].(type) {
	case int16:
		fuse = int(v)
	case int32:
		fuse = int(v)
	case int:
		fuse = v
	}
	switch v := nbtMap["ExplosionRadius"].(type) {
	case byte:
		explosionRadius = int(v)
	case int8:
		explosionRadius = int(v)
	case int16:
		explosionRadius = int(v)
	case int32:
		explosionRadius = int(v)
	case int:
		explosionRadius = v
	}
	if v, ok := nbtMap["ignited"].(bool); ok {
		ignited = v
	}
	return
}

// ApplyCreeperFallDamageSwell applies vanilla `Creeper.causeFallDamage`:
// adds `int(fallDistance * 1.5)` to the creeper's `swell` counter, capped at
// `maxSwell - 5` (e.g. `25` when `maxSwell == 30`).
func (s *Server) ApplyCreeperFallDamageSwell(entityID int32, fallDistance float64) int {
	if s == nil || entityID <= 0 || fallDistance <= 0 {
		return 0
	}
	tr := s.entityTracker()
	tr.mu.Lock()
	defer tr.mu.Unlock()
	mem := tr.mobStateFor(entityID, tr.aiTickID)
	maxSwell := mem.creeperMaxSwell
	if maxSwell <= 0 {
		maxSwell = CreeperDefaultMaxSwell
	}
	add := int(fallDistance * CreeperFallSwellMultiplier)
	mem.creeperFuseTicks += add
	capSwell := maxSwell - CreeperFallSwellCapMargin
	if capSwell < 0 {
		capSwell = 0
	}
	if mem.creeperFuseTicks > capSwell {
		mem.creeperFuseTicks = capSwell
	}
	return mem.creeperFuseTicks
}

// InteractWithCreeper2612 handles a player right-clicking a Creeper with
// `minecraft:flint_and_steel` (damages durability by 1) or `minecraft:fire_charge`
// (consumes 1 item from stack), matching `Creeper.mobInteract` in 26.1.2.
func (s *Server) InteractWithCreeper2612(session *PlayerSession, entityID int32) bool {
	if s == nil || s.entities == nil || session == nil || session.IsDead() {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeCreeper {
		return false
	}
	snap := session.Snapshot()
	slotIdx := world.HotbarContainerOffset + int(snap.SelectedSlot)
	held := session.InventorySlot(slotIdx)
	if held.IsEmpty() {
		return false
	}
	itemID := canonicalResourceName(held.ResolvedID())
	isFireCharge := itemID == "minecraft:fire_charge"
	isFlintAndSteel := itemID == "minecraft:flint_and_steel"
	if !isFireCharge && !isFlintAndSteel {
		return false
	}

	if !s.IgniteCreeper(entityID, session) {
		return false
	}

	if snap.GameMode != GameModeCreative && snap.GameMode != GameModeSpectator {
		if isFireCharge {
			rem := held.Count - 1
			if rem <= 0 {
				session.SetInventorySlot(slotIdx, world.ItemStack{})
				SetSlotItemMeta(session, slotIdx, ItemMeta{})
			} else {
				held.Count = rem
				session.SetInventorySlot(slotIdx, held)
			}
		} else if isFlintAndSteel {
			meta := GetSlotItemMeta(session, slotIdx)
			meta.Damage++
			if meta.Damage >= CreeperFlintAndSteelMaxDurability {
				session.SetInventorySlot(slotIdx, world.ItemStack{})
				SetSlotItemMeta(session, slotIdx, ItemMeta{})
			} else {
				SetSlotItemMeta(session, slotIdx, meta)
			}
		}
	}
	return true
}

// SpawnCreeperLingeringCloud spawns an `area_effect_cloud` with exact vanilla
// 26.1.2 `Creeper.spawnLingeringCloud()` parameters when a creeper with active
// status effects explodes:
//   - `Radius = 2.5F`
//   - `RadiusOnUse = -0.5F`
//   - `WaitTime = 10`
//   - `Duration = 300` (`duration / 2` where initial `duration = 600`)
//   - `RadiusPerTick = -2.5F / 300.0F` (`-radius / maxDuration`)
//   - `Effects = cloned active effects from the creeper`
func (s *Server) SpawnCreeperLingeringCloud(dim string, x, y, z float64, effects []StatusEffectInstance) int32 {
	if s == nil || len(effects) == 0 {
		return 0
	}
	normDim := world.NormalizeDimension(dim)
	copied := make([]StatusEffectInstance, len(effects))
	for i, eff := range effects {
		copied[i] = eff.Clone()
	}
	st := s.lingeringCloudState()
	st.mu.Lock()
	st.nextID++
	id := st.nextID
	cloud := &LingeringPotionCloud{
		ID:            id,
		Dimension:     normDim,
		X:             x,
		Y:             y,
		Z:             z,
		Effects:       copied,
		Radius:        CreeperLingeringCloudRadius,
		RadiusPerTick: -CreeperLingeringCloudRadius / float32(CreeperLingeringCloudDuration),
		RadiusOnUse:   CreeperLingeringCloudRadiusOnUse,
		Age:           0,
		Duration:      CreeperLingeringCloudDuration,
		WaitTime:      CreeperLingeringCloudWaitTime,
		victimNextUse: make(map[int32]int32),
	}
	st.clouds[id] = cloud
	st.mu.Unlock()
	return id
}

func hasCreeperLineOfSight(chunks *world.Cache, creeperX, creeperFeetY, creeperZ, targetX, targetY, targetZ float64) bool {
	if chunks == nil {
		return true
	}
	startX := creeperX
	startY := creeperFeetY + 1.445 // Creeper eye height (1.7 * 0.85)
	startZ := creeperZ
	endX := targetX
	endY := targetY + 1.62 // Player eye height
	endZ := targetZ

	dx := endX - startX
	dy := endY - startY
	dz := endZ - startZ
	dist := math.Sqrt(dx*dx + dy*dy + dz*dz)
	if dist <= 1e-4 {
		return true
	}
	steps := int(math.Ceil(dist * 4.0))
	for i := 1; i < steps; i++ {
		t := float64(i) / float64(steps)
		px := int(math.Floor(startX + dx*t))
		py := int(math.Floor(startY + dy*t))
		pz := int(math.Floor(startZ + dz*t))
		if py < world.MinY || py >= world.MinY+world.WorldHeight {
			continue
		}
		if !isPassableBlock(chunks.GetBlock(px, py, pz)) {
			return false
		}
	}
	return true
}

// ============================================================================
// DETERMINISTIC TICK-BY-TICK CREEPER SIMULATOR (Creeper.tick + SwellGoal + AvoidEntityGoal)
// ============================================================================

// CreeperTickSim2612 models the exact tick-by-tick state of `net.minecraft.world.entity.monster.Creeper`,
// `SwellGoal`, and `AvoidEntityGoal<Cat/Ocelot>` in Minecraft 26.1.2.
type CreeperTickSim2612 struct {
	X, Y, Z         float64
	Swell           int
	OldSwell        int
	MaxSwell        int
	ExplosionRadius int
	SwellDir        int
	Powered         bool
	Ignited         bool
	Exploded        bool
	LastBlastRadius float32
	ActiveEffects   []StatusEffectInstance
	SpawnedCloud    *LingeringPotionCloud
}

// NewCreeperTickSim2612 constructs a vanilla 26.1.2 Creeper simulator with
// `maxSwell = 30` and `explosionRadius = 3`.
func NewCreeperTickSim2612(x, y, z float64) *CreeperTickSim2612 {
	return &CreeperTickSim2612{
		X:               x,
		Y:               y,
		Z:               z,
		MaxSwell:        CreeperDefaultMaxSwell,
		ExplosionRadius: CreeperDefaultExplosionRadius,
		SwellDir:        -1,
	}
}

// CauseFallDamage applies `Creeper.causeFallDamage` (`swell += (int)(fallDistance * 1.5F)`,
// capped at `maxSwell - 5`).
func (c *CreeperTickSim2612) CauseFallDamage(fallDistance float64) {
	if c == nil || fallDistance <= 0 {
		return
	}
	c.Swell += int(fallDistance * CreeperFallSwellMultiplier)
	if c.Swell > c.MaxSwell-CreeperFallSwellCapMargin {
		c.Swell = c.MaxSwell - CreeperFallSwellCapMargin
	}
}

// StepTick advances the Creeper by 1 game tick (20 TPS):
//  1. Evaluates `AvoidEntityGoal` if a Cat/Ocelot is within 6.0 blocks (`walkSpeedModifier = 1.0`,
//     `sprintSpeedModifier = 1.2` when `distSq < 49.0`), which preempts `SwellGoal` (`swellDir = -1`).
//  2. Otherwise evaluates `SwellGoal.canUse()` (`swellDir > 0 || distSq < 9.0`) and `SwellGoal.tick()`:
//     sets `swellDir = -1` if `target == nil || distSq > 49.0 || !hasLineOfSight`; else `swellDir = 1`.
//  3. Executes `Creeper.tick()`:
//     `oldSwell = swell`; if `ignited`, `swellDir = 1`; `swell += swellDir`; clamps `swell >= 0`;
//     if `swell >= maxSwell`, triggers `explodeCreeper()` (`radius = explosionRadius * (powered ? 2.0 : 1.0)`)
//     and `spawnLingeringCloud()` if `len(ActiveEffects) > 0`.
func (c *CreeperTickSim2612) StepTick(
	hasTarget bool,
	targetX, targetY, targetZ float64,
	hasLineOfSight bool,
	hasCatNear bool,
	catX, catY, catZ float64,
) (fleeSpeedModifier float64) {
	if c == nil || c.Exploded {
		return 0
	}

	// 1. Goal evaluation (AvoidEntityGoal has priority 3, SwellGoal priority 2,
	//    but when fleeing a cat, target is cleared or creeper moves away and stops swelling).
	if hasCatNear && !c.Ignited {
		dx := c.X - catX
		dy := c.Y - catY
		dz := c.Z - catZ
		distSq := dx*dx + dy*dy + dz*dz
		if distSq <= CreeperAvoidCatMaxDist*CreeperAvoidCatMaxDist {
			c.SwellDir = -1
			if distSq < CreeperAvoidCatSprintDistSqThreshold {
				fleeSpeedModifier = CreeperAvoidCatSprintSpeedModifier
			} else {
				fleeSpeedModifier = CreeperAvoidCatWalkSpeedModifier
			}
			horiz := math.Hypot(dx, dz)
			if horiz > 1e-4 {
				step := CreeperChaseSpeed * fleeSpeedModifier
				c.X += (dx / horiz) * step
				c.Z += (dz / horiz) * step
			}
		}
	} else if !c.Ignited {
		if !hasTarget {
			c.SwellDir = -1
		} else {
			dx := targetX - c.X
			dy := targetY - c.Y
			dz := targetZ - c.Z
			distSq := dx*dx + dy*dy + dz*dz
			canUseSwell := c.SwellDir > 0 || distSq < CreeperSwellGoalStartDistSq
			if canUseSwell {
				if distSq > CreeperSwellGoalCancelDistSq || !hasLineOfSight {
					c.SwellDir = -1
				} else {
					c.SwellDir = 1
				}
			} else {
				c.SwellDir = -1
			}
		}
	}

	// 2. Creeper.tick() swell progression:
	c.OldSwell = c.Swell
	if c.Ignited {
		c.SwellDir = 1
	}
	c.Swell += c.SwellDir
	if c.Swell < 0 {
		c.Swell = 0
	}
	if c.Swell >= c.MaxSwell {
		c.Swell = c.MaxSwell
		c.Exploded = true
		mult := float32(1.0)
		if c.Powered {
			mult = CreeperChargedRadiusMultiplier
		}
		c.LastBlastRadius = float32(c.ExplosionRadius) * mult
		if len(c.ActiveEffects) > 0 {
			copied := make([]StatusEffectInstance, len(c.ActiveEffects))
			for i, e := range c.ActiveEffects {
				copied[i] = e.Clone()
			}
			c.SpawnedCloud = &LingeringPotionCloud{
				X:             c.X,
				Y:             c.Y,
				Z:             c.Z,
				Effects:       copied,
				Radius:        CreeperLingeringCloudRadius,
				RadiusOnUse:   CreeperLingeringCloudRadiusOnUse,
				WaitTime:      CreeperLingeringCloudWaitTime,
				Duration:      CreeperLingeringCloudDuration,
				RadiusPerTick: -CreeperLingeringCloudRadius / float32(CreeperLingeringCloudDuration),
			}
		}
	}
	return fleeSpeedModifier
}

// ============================================================================
// DETERMINISTIC TICK-BY-TICK SKELETON / RANGED BOW / MELEE / SUNBURN SIMULATOR
// ============================================================================

// SkeletonAttackGoalKind identifies whether `AbstractSkeleton.reassessWeaponGoal()`
// selected `RangedBowAttackGoal` or `MeleeAttackGoal`.
type SkeletonAttackGoalKind uint8

const (
	SkeletonGoalNone SkeletonAttackGoalKind = iota
	SkeletonGoalRangedBow
	SkeletonGoalMelee
)

// SkeletonArrowShot2612 records the exact parameters of an arrow fired by
// `AbstractSkeleton.performRangedAttack(LivingEntity, float)` in 26.1.2.
type SkeletonArrowShot2612 struct {
	Tick             int
	PullTicks        int
	BowPower         float32
	Velocity         float32
	Inaccuracy       float32
	BaseDamage       float64
	DeltaX           float64
	DeltaY           float64
	DeltaZ           float64
	SlownessDuration int32
	PoisonDuration   int32
}

// BowPowerForPullTicks implements vanilla `BowItem.getPowerForTime(int)`:
//   `f = (float)ticks / 20.0F; f = (f * f + f * 2.0F) / 3.0F; if (f > 1.0F) f = 1.0F;`
func BowPowerForPullTicks(pullTicks int) float32 {
	f := float32(pullTicks) / 20.0
	f = (f*f + f*2.0) / 3.0
	if f > 1.0 {
		f = 1.0
	}
	return f
}

// SkeletonArrowInaccuracyForDifficulty returns `(float)(14 - difficulty.getId() * 4)`
// from `AbstractSkeleton.performRangedAttack`:
//   - Peaceful (0): 14.0
//   - Easy (1):     10.0
//   - Normal (2):   6.0
//   - Hard (3):     2.0
func SkeletonArrowInaccuracyForDifficulty(difficulty Difficulty) float32 {
	return float32(14 - int(difficulty)*4)
}

// ComputeMobArrowBaseDamage2612 implements vanilla `AbstractArrow.setBaseDamageFromMob(float power)`:
//   `setBaseDamage((double)(power * 2.0F) + random.triangle((double)difficulty.getId() * 0.11D, 0.57425D))`
// where `random.triangle(mode, spread) = mode + spread * (rng.Float64() - rng.Float64())`.
func ComputeMobArrowBaseDamage2612(power float32, difficulty Difficulty, rng *rand.Rand) float64 {
	mode := float64(difficulty) * AbstractArrowMobDifficultyBonusScale
	tri := mode
	if rng != nil {
		tri = mode + AbstractArrowMobTriangleSpread*(rng.Float64()-rng.Float64())
	}
	return float64(power)*AbstractArrowMobBaseDamageMultiplier + tri
}

// SkeletonAttackIntervalForDifficulty returns the vanilla `RangedBowAttackGoal`
// `attackIntervalMin` for `mobType` and `difficulty`:
//   - `minecraft:skeleton` / `minecraft:stray`: 20 ticks on Hard, 40 ticks on Easy/Normal
//   - `minecraft:bogged`: 50 ticks on Hard, 70 ticks on Easy/Normal
func SkeletonAttackIntervalForDifficulty(mobType string, difficulty Difficulty) int {
	if canonicalResourceName(mobType) == MobTypeBogged {
		return BoggedAttackIntervalForDifficulty(difficulty)
	}
	if difficulty == DifficultyHard {
		return SkeletonHardAttackIntervalTicks
	}
	return SkeletonEasyNormalAttackIntervalTicks
}

// IsSkeletonHoldingBow reports whether the skeleton entity is holding
// `minecraft:bow` (as opposed to a melee sword or empty hand when custom gear is set).
func (s *Server) IsSkeletonHoldingBow(entityID int32) bool {
	if s == nil || entityID <= 0 {
		return true
	}
	mgr := s.undeadManager()
	mgr.mu.RLock()
	st := mgr.undead[entityID]
	if st == nil {
		mgr.mu.RUnlock()
		return true
	}
	mainID := canonicalResourceName(st.MainHand.ResolvedID())
	offID := canonicalResourceName(st.OffHand.ResolvedID())
	mgr.mu.RUnlock()
	if mainID == "" && offID == "" {
		return false
	}
	return mainID == "minecraft:bow" || offID == "minecraft:bow"
}

// SkeletonTickSim2612 models the exact tick-by-tick behavior of `AbstractSkeleton`
// (`Skeleton`, `Stray`, `Bogged`), `RangedBowAttackGoal`, `MeleeAttackGoal`,
// sun burning with/without helmet (`Mob.isSunBurnTick` / `Mob.burnUndead`), and
// `Skeleton -> Stray` powder snow conversion (`Skeleton.tick`).
type SkeletonTickSim2612 struct {
	MobType           string
	Difficulty        Difficulty
	X, Y, Z           float64
	Yaw               float32
	MainHandItem      string
	OffHandItem       string
	HelmetItem        string
	HelmetDamage      int32
	HelmetMaxDamage   int32
	RemainingFireTicks int32

	// Weapon goal state (AbstractSkeleton.reassessWeaponGoal + RangedBowAttackGoal)
	ActiveGoal        SkeletonAttackGoalKind
	AttackIntervalMin int
	AttackRadiusSqr   float64
	AttackTime        int
	SeeTime           int
	StrafingClockwise bool
	StrafingBackwards bool
	StrafingTime      int
	UsingBow          bool
	TicksUsingItem    int
	StrafeForwards    float32
	StrafeRight       float32
	NavigatingToTarget bool
	MeleeAttackTicks  int
	MeleeHits         int

	// Powder snow conversion state (Skeleton.tick)
	InPowderSnowTime   int
	FreezeConversionTime int
	FreezeConverting   bool
	ConvertedToStray   bool

	// History of arrows fired
	ShotsFired []SkeletonArrowShot2612
	TickCount  int
}

// NewSkeletonTickSim2612 creates a deterministic 26.1.2 skeleton simulator
// holding `minecraft:bow` and evaluates `ReassessWeaponGoal()`.
func NewSkeletonTickSim2612(mobType string, difficulty Difficulty, x, y, z float64) *SkeletonTickSim2612 {
	sim := &SkeletonTickSim2612{
		MobType:            canonicalResourceName(mobType),
		Difficulty:         difficulty,
		X:                  x,
		Y:                  y,
		Z:                  z,
		MainHandItem:       "minecraft:bow",
		AttackRadiusSqr:    RangedBowAttackRadiusSqr,
		AttackTime:         -1,
		StrafingTime:       -1,
		InPowderSnowTime:   -1,
	}
	sim.ReassessWeaponGoal()
	return sim
}

// ReassessWeaponGoal implements `AbstractSkeleton.reassessWeaponGoal()`:
// selects `RangedBowAttackGoal` (with difficulty-based `attackIntervalMin`) when
// holding `minecraft:bow` in main or off hand, or `MeleeAttackGoal` (`speedModifier = 1.2`) otherwise.
func (sim *SkeletonTickSim2612) ReassessWeaponGoal() {
	if sim == nil {
		return
	}
	interval := SkeletonAttackIntervalForDifficulty(sim.MobType, sim.Difficulty)
	sim.AttackIntervalMin = interval
	mainID := canonicalResourceName(strings.TrimSpace(sim.MainHandItem))
	offID := canonicalResourceName(strings.TrimSpace(sim.OffHandItem))
	if mainID == "minecraft:bow" || offID == "minecraft:bow" {
		if sim.ActiveGoal != SkeletonGoalRangedBow {
			sim.ActiveGoal = SkeletonGoalRangedBow
			sim.AttackTime = -1
			sim.SeeTime = 0
			sim.StrafingTime = -1
			sim.UsingBow = false
			sim.TicksUsingItem = 0
		}
	} else {
		sim.ActiveGoal = SkeletonGoalMelee
		sim.UsingBow = false
		sim.TicksUsingItem = 0
		sim.StrafeForwards = 0
		sim.StrafeRight = 0
	}
}

// SetMainHandItem updates the skeleton's main-hand item and triggers
// `ReassessWeaponGoal()`, matching `AbstractSkeleton.onEquipItem`.
func (sim *SkeletonTickSim2612) SetMainHandItem(itemID string) {
	if sim == nil {
		return
	}
	sim.MainHandItem = canonicalResourceName(itemID)
	sim.ReassessWeaponGoal()
}

// StepSunBurnTick implements `Mob.aiStep()` -> `isSunBurnTick()` -> `burnUndead()`
// from `net.minecraft.world.entity.Mob` in 26.1.2:
//   - If wearing a helmet: if `HelmetDamage > 0 && rng.Intn(2) == 0`, damages the
//     helmet by `rng.Intn(2)` (`hurtAndBreak(random.nextInt(2), this, EquipmentSlot.HEAD)`);
//     if `HelmetDamage >= HelmetMaxDamage`, breaks the helmet (`HelmetItem = ""`).
//     Does NOT ignite the skeleton while wearing a helmet!
//   - If not wearing a helmet: calls `igniteForSeconds(8.0F)` (`RemainingFireTicks = 160`).
func (sim *SkeletonTickSim2612) StepSunBurnTick(inDaylightAndOpenSky, inWaterOrPowderSnow bool, rng *rand.Rand) (burned bool, helmetBroke bool) {
	if sim == nil || !inDaylightAndOpenSky || inWaterOrPowderSnow {
		return false, false
	}
	if !DoesUndeadBurnInDaylight(sim.MobType) {
		return false, false
	}
	if strings.TrimSpace(sim.HelmetItem) != "" {
		// Vanilla Mob.burnUndead():
		// if (itemStack.isDamageableItem()) {
		//     Item item = itemStack.getItem();
		//     itemStack.setDamageValue(itemStack.getDamageValue() + this.random.nextInt(2));
		//     if (itemStack.getDamageValue() >= itemStack.getMaxDamage()) {
		//         this.onEquippedItemBroken(item, EquipmentSlot.HEAD);
		//         this.setItemSlot(EquipmentSlot.HEAD, ItemStack.EMPTY);
		//     }
		// }
		addDmg := int32(1)
		if rng != nil {
			addDmg = int32(rng.Intn(2))
		}
		sim.HelmetDamage += addDmg
		if sim.HelmetMaxDamage > 0 && sim.HelmetDamage >= sim.HelmetMaxDamage {
			sim.HelmetItem = ""
			sim.HelmetDamage = 0
			return false, true
		}
		return false, false
	}
	if sim.RemainingFireTicks < UndeadSunBurnIgnitionTicks {
		sim.RemainingFireTicks = UndeadSunBurnIgnitionTicks
	}
	return true, false
}

// StepPowderSnowConversionTick implements `Skeleton.tick()` powder snow freeze
// conversion in 26.1.2:
//   - Only `minecraft:skeleton` converts to `minecraft:stray` in powder snow.
//   - When `inPowderSnow == true`:
//     - If `FreezeConverting`: `--FreezeConversionTime`; if `< 0` (after 300 ticks),
//       converts to `minecraft:stray` (`ConvertedToStray = true`, `MobType = "minecraft:stray"`).
//     - Else: `++InPowderSnowTime`; if `>= 140`, starts conversion (`FreezeConverting = true`, `FreezeConversionTime = 300`).
//   - When `inPowderSnow == false`:
//     - Immediately resets `InPowderSnowTime = -1` and `FreezeConverting = false` (`FreezeConversionTime = 0`).
func (sim *SkeletonTickSim2612) StepPowderSnowConversionTick(inPowderSnow bool) (converted bool) {
	if sim == nil || sim.MobType != MobTypeSkeleton || sim.ConvertedToStray {
		return false
	}
	if inPowderSnow {
		if sim.FreezeConverting {
			sim.FreezeConversionTime--
			if sim.FreezeConversionTime < 0 {
				sim.FreezeConverting = false
				sim.ConvertedToStray = true
				sim.MobType = MobTypeStray
				return true
			}
		} else {
			if sim.InPowderSnowTime < 0 {
				sim.InPowderSnowTime = 0
			}
			sim.InPowderSnowTime++
			if sim.InPowderSnowTime >= SkeletonPowderSnowFreezeTicks {
				sim.FreezeConversionTime = SkeletonStrayConversionTicks
				sim.FreezeConverting = true
			}
		}
	} else {
		sim.InPowderSnowTime = -1
		sim.FreezeConverting = false
		sim.FreezeConversionTime = 0
	}
	return false
}

// StepCombatTick advances the skeleton's active weapon goal (`RangedBowAttackGoal`
// or `MeleeAttackGoal`) by 1 tick (`RangedBowAttackGoal.tick()` in `26.1.2.jar`).
func (sim *SkeletonTickSim2612) StepCombatTick(
	hasTarget bool,
	targetX, targetY, targetZ float64,
	targetBbHeight float64,
	hasLineOfSight bool,
	rng *rand.Rand,
) (shot *SkeletonArrowShot2612, meleeAttacked bool) {
	if sim == nil {
		return nil, false
	}
	sim.TickCount++
	if sim.RemainingFireTicks > 0 {
		sim.RemainingFireTicks--
	}
	if !hasTarget {
		sim.SeeTime = 0
		sim.AttackTime = -1
		sim.UsingBow = false
		sim.TicksUsingItem = 0
		sim.StrafeForwards = 0
		sim.StrafeRight = 0
		sim.NavigatingToTarget = false
		return nil, false
	}

	dx := targetX - sim.X
	dy := targetY - sim.Y
	dz := targetZ - sim.Z
	distSq := dx*dx + dy*dy + dz*dz
	horizDist := math.Hypot(dx, dz)

	if sim.ActiveGoal == SkeletonGoalMelee {
		sim.StrafeForwards = 0
		sim.StrafeRight = 0
		if sim.MeleeAttackTicks > 0 {
			sim.MeleeAttackTicks--
		}
		stepSpeed := SkeletonBaseMovementSpeed * SkeletonMeleeAttackSpeedModifier
		if horizDist > 1.2 {
			sim.NavigatingToTarget = true
			if horizDist > 1e-4 {
				step := math.Min(stepSpeed, horizDist-1.0)
				sim.X += (dx / horizDist) * step
				sim.Z += (dz / horizDist) * step
			}
		} else {
			sim.NavigatingToTarget = false
		}
		if horizDist <= 1.8 && sim.MeleeAttackTicks <= 0 {
			sim.MeleeAttackTicks = 20
			sim.MeleeHits++
			return nil, true
		}
		return nil, false
	}

	// RangedBowAttackGoal.tick() (RangedBowAttackGoal.class:134-388):
	if sim.UsingBow {
		sim.TicksUsingItem++
	}
	wasSeeing := sim.SeeTime > 0
	if hasLineOfSight != wasSeeing {
		sim.SeeTime = 0
	}
	if hasLineOfSight {
		sim.SeeTime++
	} else {
		sim.SeeTime--
	}

	if distSq <= sim.AttackRadiusSqr && sim.SeeTime >= RangedBowStrafeSeeTimeThreshold {
		sim.NavigatingToTarget = false
		sim.StrafingTime++
	} else {
		sim.NavigatingToTarget = true
		sim.StrafingTime = -1
		sim.StrafeForwards = 0
		sim.StrafeRight = 0
		if horizDist > 1e-4 {
			step := math.Min(SkeletonChaseSpeed, horizDist)
			sim.X += (dx / horizDist) * step
			sim.Z += (dz / horizDist) * step
		}
	}

	if sim.StrafingTime >= RangedBowStrafeFlipIntervalTicks {
		flipCW := float32(0.0)
		flipBack := float32(0.0)
		if rng != nil {
			flipCW = rng.Float32()
			flipBack = rng.Float32()
		}
		if flipCW < RangedBowStrafeDirectionFlipChance {
			sim.StrafingClockwise = !sim.StrafingClockwise
		}
		if flipBack < RangedBowStrafeDirectionFlipChance {
			sim.StrafingBackwards = !sim.StrafingBackwards
		}
		sim.StrafingTime = 0
	}

	if sim.StrafingTime > -1 {
		if distSq > sim.AttackRadiusSqr*RangedBowStrafeForwardMaxDistRatio {
			sim.StrafingBackwards = false
		} else if distSq < sim.AttackRadiusSqr*RangedBowStrafeBackwardMinDistRatio {
			sim.StrafingBackwards = true
		}
		if sim.StrafingBackwards {
			sim.StrafeForwards = -RangedBowStrafeSpeed
		} else {
			sim.StrafeForwards = RangedBowStrafeSpeed
		}
		if sim.StrafingClockwise {
			sim.StrafeRight = RangedBowStrafeSpeed
		} else {
			sim.StrafeRight = -RangedBowStrafeSpeed
		}
		// Apply horizontal strafe displacement along local forward/right vectors toward target:
		if horizDist > 1e-4 {
			fwdX, fwdZ := dx/horizDist, dz/horizDist
			rightX, rightZ := -fwdZ, fwdX
			speed := SkeletonBaseMovementSpeed * 0.5 // MoveControl.strafe scales by speed
			sim.X += (fwdX*float64(sim.StrafeForwards) + rightX*float64(sim.StrafeRight)) * speed
			sim.Z += (fwdZ*float64(sim.StrafeForwards) + rightZ*float64(sim.StrafeRight)) * speed
		}
	}

	sim.Yaw = yawToward(dx, dz)

	if sim.UsingBow {
		if !hasLineOfSight && sim.SeeTime < RangedBowCancelUsingUnseenTicks {
			sim.UsingBow = false
			sim.TicksUsingItem = 0
		} else if hasLineOfSight {
			if sim.TicksUsingItem >= RangedBowRequiredPullTicks {
				pull := sim.TicksUsingItem
				sim.UsingBow = false
				sim.TicksUsingItem = 0
				power := BowPowerForPullTicks(pull)
				inaccuracy := SkeletonArrowInaccuracyForDifficulty(sim.Difficulty)
				baseDmg := ComputeMobArrowBaseDamage2612(power, sim.Difficulty, rng)
				if targetBbHeight <= 0 {
					targetBbHeight = 1.8
				}
				arrowSpawnY := sim.Y + 1.99 - 0.1 // getEyeY() - 0.1F
				aimDX := targetX - sim.X
				aimDY := (targetY + targetBbHeight*0.3333333333333333) - arrowSpawnY
				aimDZ := targetZ - sim.Z
				aimHoriz := math.Sqrt(aimDX*aimDX + aimDZ*aimDZ)
				shotVal := SkeletonArrowShot2612{
					Tick:       sim.TickCount,
					PullTicks:  pull,
					BowPower:   power,
					Velocity:   SkeletonArrowLaunchVelocity,
					Inaccuracy: inaccuracy,
					BaseDamage: baseDmg,
					DeltaX:     aimDX,
					DeltaY:     aimDY + aimHoriz*SkeletonArrowGravityAimFactor,
					DeltaZ:     aimDZ,
				}
				switch sim.MobType {
				case MobTypeStray:
					shotVal.SlownessDuration = StrayArrowSlownessDurationTicks
				case MobTypeBogged:
					shotVal.PoisonDuration = BoggedPoisonDurationTicks
				}
				sim.ShotsFired = append(sim.ShotsFired, shotVal)
				sim.AttackTime = sim.AttackIntervalMin
				return &shotVal, false
			}
		}
	} else {
		sim.AttackTime--
		if sim.AttackTime <= 0 && sim.SeeTime >= RangedBowMinSeeTimeToStartUsing {
			sim.UsingBow = true
			sim.TicksUsingItem = 0
		}
	}
	return nil, false
}
