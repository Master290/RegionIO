package server

import (
	"math"
	"math/rand"
	"strings"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

// ============================================================================
// WITCH & ILLAGERS (PILLAGER, VINDICATOR, EVOKER, VEX) TICK-BY-TICK PARITY
// Verified against Minecraft Java Edition 26.1.2 (`server-26.1.2.jar`):
//   - net.minecraft.world.entity.monster.Witch
//   - net.minecraft.world.entity.ai.goal.RangedAttackGoal
//   - net.minecraft.world.entity.ai.goal.target.NearestHealableRaiderTargetGoal
//   - net.minecraft.world.entity.projectile.throwableitemprojectile.AbstractThrownPotion
//   - net.minecraft.world.entity.projectile.throwableitemprojectile.ThrownSplashPotion
//   - net.minecraft.world.entity.monster.illager.Pillager
//   - net.minecraft.world.entity.ai.goal.RangedCrossbowAttackGoal
//   - net.minecraft.world.item.CrossbowItem / ProjectileWeaponItem
//   - net.minecraft.world.entity.monster.illager.Vindicator
//   - net.minecraft.world.entity.monster.illager.AbstractIllager
//   - net.minecraft.world.entity.monster.illager.SpellcasterIllager
//   - net.minecraft.world.entity.monster.illager.Evoker
//   - net.minecraft.world.entity.projectile.EvokerFangs
//   - net.minecraft.world.entity.monster.Vex
//   - net.minecraft.world.entity.ai.goal.AvoidEntityGoal
// ============================================================================

// PillagerCrossbowState2612 mirrors `RangedCrossbowAttackGoal.CrossbowState` in 26.1.2.
type PillagerCrossbowState2612 uint8

const (
	PillagerCrossbowUncharged     PillagerCrossbowState2612 = 0
	PillagerCrossbowCharging      PillagerCrossbowState2612 = 1
	PillagerCrossbowCharged       PillagerCrossbowState2612 = 2
	PillagerCrossbowReadyToAttack PillagerCrossbowState2612 = 3
)

const (
	// Witch (`Witch.java`) constants in 26.1.2.
	WitchBaseMovementSpeed2612            float64 = 0.25
	WitchDrinkingSpeedModifier2612        float64 = -0.25 // SPEED_MODIFIER_DRINKING (-0.25 ADD_VALUE)
	WitchEyeHeight2612                    float64 = 1.62
	WitchSelfDrinkWaterBreathChance2612   float32 = 0.15
	WitchSelfDrinkFireResistChance2612    float32 = 0.15
	WitchSelfDrinkHealingChance2612       float32 = 0.05
	WitchSelfDrinkSwiftnessChance2612     float32 = 0.50
	WitchSwiftnessMinDistSq2612           float64 = 121.0 // > 11.0 blocks
	WitchWeaknessSplashChance2612         float32 = 0.25
	WitchRaiderHealingThresholdHP2612     float32 = 4.0
	WitchHealRaidersCooldownTicks2612     int     = 100 // Goal.reducedTickDelay(200)
	WitchSplashAimEyeOffset2612           float64 = 1.100000023841858
	WitchSplashParabolaFactor2612         float64 = 0.2
	WitchSplashCloseVelocity2612          float64 = 0.45 // horizDist <= 2.0
	WitchSplashNormalVelocity2612         float64 = 0.75 // horizDist > 2.0
	WitchSplashInaccuracy2612             float64 = 8.0
	WitchSplashGravity2612                float64 = 0.05
	WitchSplashAirDrag2612                float64 = 0.99

	// Pillager (`Pillager.java` + `RangedCrossbowAttackGoal.java` + `Items.CROSSBOW`) constants in 26.1.2.
	PillagerMovementSpeed2612             float64 = 0.35
	PillagerFollowRange2612               float64 = 32.0
	PillagerCrossbowAttackRadius2612      float64 = 8.0
	PillagerCrossbowAttackRadiusSq2612    float64 = 64.0
	PillagerCrossbowMinSeeTime2612        int     = 5
	PillagerCrossbowAttackDelayMin2612    int     = 20
	PillagerCrossbowAttackDelayRange2612  int     = 20 // 20 + nextInt(20) -> 20..39
	PillagerCrossbowMaxDurability2612     int32   = 465

	// Vindicator (`Vindicator.java` + `BreakDoorGoal.java`) constants in 26.1.2.
	VindicatorMovementSpeed2612           float64 = 0.35
	VindicatorFollowRange2612             float64 = 12.0
	VindicatorMeleeIntervalTicks2612      int     = 20
	VindicatorMeleeBaseDamage2612         float32 = 13.0 // 5.0 base + iron_axe (+8) -> scaled by difficulty
	VindicatorDoorBreakTicks2612          int     = 240  // BreakDoorGoal: Math.max(240, 6)

	// Evoker & EvokerFangs & Vex (`Evoker.java`, `SpellcasterIllager.java`, `EvokerFangs.java`, `Vex.java`) constants in 26.1.2.
	EvokerMovementSpeed2612               float64 = 0.50
	EvokerFollowRange2612                 float64 = 12.0
	EvokerAvoidPlayerMaxDist2612          float64 = 8.0
	EvokerAvoidPlayerWalkMod2612          float64 = 0.60
	EvokerAvoidPlayerSprintMod2612        float64 = 1.00
	EvokerAvoidSprintDistSq2612           float64 = 49.0

	EvokerFangsSpellWarmupTicks2612       int = 20
	EvokerFangsSpellCastTicks2612         int = 40
	EvokerFangsSpellIntervalTicks2612     int = 100

	EvokerSummonVexSpellWarmupTicks2612   int = 20
	EvokerSummonVexSpellCastTicks2612     int = 100
	EvokerSummonVexSpellIntervalTicks2612 int = 340
	EvokerSummonVexCheckRange2612         float64 = 16.0
	EvokerSummonVexMaxNearby2612          int     = 8

	EvokerWololoSpellWarmupTicks2612      int = 40
	EvokerWololoSpellCastTicks2612        int = 60
	EvokerWololoSpellIntervalTicks2612    int = 140
	EvokerWololoVertRange2612             float64 = 4.0

	EvokerFangsLineCount2612              int     = 16
	EvokerFangsCloseDistSq2612            float64 = 9.0 // distanceToSqr < 9.0 -> 5 inner + 8 outer rings
	EvokerFangsInitialLifeTicks2612       int     = 22
	EvokerFangsDamageLifeTick2612         int     = 14 // lifeTicks == 14 (warmupDelayTicks == -8)

	VexLimitedLifeMinSec2612              int = 30
	VexLimitedLifeRandSec2612             int = 90 // 20 * (30 + nextInt(90)) -> 600..2380 ticks
	VexStarveDamageIntervalTicks2612      int = 20
)

// WitchThrownSplashPotionState2612 tracks an in-flight `minecraft:splash_potion`
// projectile thrown by a Witch (`ThrownSplashPotion` in 26.1.2).
type WitchThrownSplashPotionState2612 struct {
	EntityID     int32
	Dimension    string
	OwnerWitchID int32
	PotionType   string
	VX, VY, VZ   float64
	AgeTicks     int
}

// IsWitchResistantDamageType2612 matches `DamageTypeTags.WITCH_RESISTANT_TO` in 26.1.2:
// `minecraft:magic`, `minecraft:indirect_magic`, `minecraft:sonic_boom`, `minecraft:thorns`.
func IsWitchResistantDamageType2612(damageType string) bool {
	norm := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(damageType)), "minecraft:")
	switch norm {
	case "magic", "indirect_magic", "sonic_boom", "thorns":
		return true
	default:
		return false
	}
}

// IsFireDamageType2612 matches `DamageTypeTags.IS_FIRE` in 26.1.2:
// `in_fire`, `campfire`, `on_fire`, `lava`, `hot_floor`, `unattributed_fireball`, `fireball`.
func IsFireDamageType2612(damageType string) bool {
	norm := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(damageType)), "minecraft:")
	switch norm {
	case "in_fire", "campfire", "on_fire", "lava", "hot_floor", "unattributed_fireball", "fireball", "fire":
		return true
	default:
		return false
	}
}

// ComputeWitchDamageAfterMagicAbsorb2612 implements `Witch.getDamageAfterMagicAbsorb`
// (`Witch.java:3710-3732` in `26.1.2.jar`):
//   - If `witchID > 0 && attackerEntityID == witchID`: returns `0` (immune to own potions);
//   - If `damageType` is in `WITCH_RESISTANT_TO`: multiplies damage by `0.15` (85% reduction).
func ComputeWitchDamageAfterMagicAbsorb2612(witchID, attackerEntityID int32, damageType string, rawDamage float32) float32 {
	if rawDamage <= 0 {
		return 0
	}
	if witchID > 0 && attackerEntityID == witchID {
		return 0
	}
	if IsWitchResistantDamageType2612(damageType) {
		return rawDamage * WitchMagicDamageMultiplier
	}
	return rawDamage
}

// OnWitchDamagedBySource2612 records the last damage source category on Witch `witchID`
// so `Witch.aiStep()` can drink `Potions.FIRE_RESISTANCE` when hurt by a `DamageTypeTags.IS_FIRE`
// source even if not currently burning (`getLastDamageSource().is(IS_FIRE)`).
func (s *Server) OnWitchDamagedBySource2612(witchID int32, damageType string) {
	if s == nil || witchID <= 0 {
		return
	}
	s.ensureIllagerOrRaiderState(witchID, MobTypeWitch, false)
	isFire := IsFireDamageType2612(damageType)
	mgr := s.illagerManager()
	mgr.mu.Lock()
	if st := mgr.witches[witchID]; st != nil {
		st.LastDamageWasFire = isFire
	}
	mgr.mu.Unlock()
}

// EntityEffectAmplifier2612 returns `(amplifier, true)` if `entityID` has `effectID` active.
func (s *Server) EntityEffectAmplifier2612(entityID int32, effectID string) (int32, bool) {
	if s == nil || entityID <= 0 {
		return 0, false
	}
	id := canonicalEffectID(effectID)
	es := s.entityEffectState()
	es.mu.Lock()
	defer es.mu.Unlock()
	m := es.effects[entityID]
	if m == nil {
		return 0, false
	}
	inst, ok := m[id]
	if !ok {
		return 0, false
	}
	return inst.Amplifier, true
}

// WitchEffectiveMovementSpeed2612 computes the Witch's `Attributes.MOVEMENT_SPEED` value
// using vanilla `AttributeInstance.calculateValue()`:
//   - Base `0.25` + `ADD_VALUE` (`-0.25` when `SPEED_MODIFIER_DRINKING` is active -> `0.0`);
//   - `ADD_MULTIPLIED_TOTAL` for `MobEffects.SPEED` (`+0.20 * (amp + 1)`) and
//     `MobEffects.SLOWNESS` (`-0.15 * (amp + 1)`).
func (s *Server) WitchEffectiveMovementSpeed2612(witchID int32) float64 {
	base := WitchBaseMovementSpeed2612
	if s != nil {
		mgr := s.illagerManager()
		mgr.mu.RLock()
		if st := mgr.witches[witchID]; st != nil && st.Drinking {
			base += st.SpeedModifierDrinking
		}
		mgr.mu.RUnlock()
	}
	if base <= 0 {
		return 0
	}
	val := base
	if s != nil {
		if amp, ok := s.EntityEffectAmplifier2612(witchID, EffectSpeed); ok {
			val *= 1.0 + 0.20*float64(amp+1)
		}
		if amp, ok := s.EntityEffectAmplifier2612(witchID, EffectSlowness); ok {
			mul := 1.0 - 0.15*float64(amp+1)
			if mul < 0 {
				mul = 0
			}
			val *= mul
		}
	}
	if val < 0 {
		return 0
	}
	return val
}

// EncodeWitchPotionEquipmentPacket2612 encodes `ClientboundSetEquipmentPacket` for a Witch's
// `MAINHAND` holding `minecraft:potion` with `DataComponentPotionContents` (or empty).
func EncodeWitchPotionEquipmentPacket2612(entityID int32, stack world.ItemStack, potionID string) []byte {
	w := protocol.NewWriter(32)
	w.VarInt(entityID)
	w.Byte(IllagerEquipSlotMainHand)
	if stack.IsEmpty() {
		w.VarInt(0)
	} else {
		EncodeSlotWithPotionContents(w, stack, potionID)
	}
	return w.Bytes()
}

func (s *Server) equipWitchMainHandPotion2612(witchID int32, potionID string) {
	if s == nil || witchID <= 0 {
		return
	}
	var stack world.ItemStack
	if potionID != "" {
		stack = world.NewNamedItemStack("minecraft:potion", 1)
	}
	ds := s.dispenserState()
	ds.mu.Lock()
	eq := ds.mobEquipment[witchID]
	eq[EquipmentSlotMainHand] = stack
	ds.mobEquipment[witchID] = eq
	ds.mu.Unlock()

	if s.entities != nil {
		if ent := s.entities.Get(witchID); ent != nil {
			cp := ent.ChunkPos()
			s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, protocol.PlaySetEquipment,
				EncodeWitchPotionEquipmentPacket2612(witchID, stack, potionID))
		}
	}
}

// TrySelectWitchSelfPotionAiStep2612 implements the exact random self-potion selection in
// `Witch.aiStep()` (`Witch.java:3503-3581` in `26.1.2.jar`):
//  1. `nextFloat() < 0.15` && `isEyeInFluid(WATER)` && `!hasEffect(WATER_BREATHING)` -> `water_breathing`
//  2. `else if nextFloat() < 0.15` && `(isOnFire() || lastDamageWasFire)` && `!hasEffect(FIRE_RESISTANCE)` -> `fire_resistance`
//  3. `else if nextFloat() < 0.05` && `health < maxHealth` -> `healing`
//  4. `else if nextFloat() < 0.50` && `hasTarget` && `!hasEffect(SPEED)` && `targetDistSq > 121.0` -> `swiftness`
func (s *Server) TrySelectWitchSelfPotionAiStep2612(witchID int32, hasTarget bool, targetDistSq float64, rng *rand.Rand) string {
	if s == nil || s.entities == nil {
		return WitchPotionNone
	}
	ent := s.entities.Get(witchID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeWitch {
		return WitchPotionNone
	}
	roll := func(chance float32) bool {
		if rng == nil {
			return true
		}
		return rng.Float32() < chance
	}

	dim := ent.EffectiveDimension()
	mgr := s.illagerManager()
	mgr.mu.RLock()
	lastFire := false
	if st := mgr.witches[witchID]; st != nil {
		lastFire = st.LastDamageWasFire
	}
	mgr.mu.RUnlock()

	if roll(WitchSelfDrinkWaterBreathChance2612) &&
		s.isEntityEyesUnderWater(dim, ent.X, ent.Y, ent.Z, WitchEyeHeight2612) &&
		!s.HasEntityEffect(witchID, EffectWaterBreathing) {
		return WitchPotionWaterBreathing
	}
	if roll(WitchSelfDrinkFireResistChance2612) &&
		(s.IsEntityOnFire(witchID) || lastFire) &&
		!s.HasEntityEffect(witchID, EffectFireResistance) {
		return WitchPotionFireResistance
	}
	if roll(WitchSelfDrinkHealingChance2612) && s.MobHealth(witchID) < DefaultWitchMaxHealth {
		return WitchPotionHealing
	}
	if roll(WitchSelfDrinkSwiftnessChance2612) &&
		hasTarget &&
		!s.HasEntityEffect(witchID, EffectSpeed) &&
		targetDistSq > WitchSwiftnessMinDistSq2612 {
		return WitchPotionSwiftness
	}
	return WitchPotionNone
}

// ComputeWitchSplashPotionAim2612 implements the exact velocity lead, parabola, and launch
// speed of `Witch.performRangedAttack` + `Projectile.getMovementToShoot` (`26.1.2.jar`):
//   - `dx = targetX + targetVX - witchX`
//   - `dy = targetEyeY - 1.100000023841858 - witchY`
//   - `dz = targetZ + targetVZ - witchZ`
//   - `horizDist = hypot(dx, dz)`
//   - Aim vector: `(dx, dy + horizDist * 0.2, dz)`
//   - Velocity scalar: `0.45` if `horizDist <= 2.0`, else `0.75`; inaccuracy `8.0`.
func ComputeWitchSplashPotionAim2612(
	witchX, witchY, witchZ float64,
	targetX, targetEyeY, targetZ float64,
	targetVX, targetVZ float64,
	rng *rand.Rand,
) (spawnX, spawnY, spawnZ, vx, vy, vz, horizDist float64) {
	spawnX = witchX
	spawnY = witchY + WitchEyeHeight2612 - 0.1
	spawnZ = witchZ

	dx := targetX + targetVX - witchX
	dy := targetEyeY - WitchSplashAimEyeOffset2612 - witchY
	dz := targetZ + targetVZ - witchZ
	horizDist = math.Hypot(dx, dz)

	aimX := dx
	aimY := dy + horizDist*WitchSplashParabolaFactor2612
	aimZ := dz
	mag := math.Sqrt(aimX*aimX + aimY*aimY + aimZ*aimZ)
	if mag < 1e-9 {
		aimX, aimY, aimZ, mag = 0, 1, 0, 1
	}
	nx, ny, nz := aimX/mag, aimY/mag, aimZ/mag
	if rng != nil {
		spread := 0.0172275 * WitchSplashInaccuracy2612
		nx += (rng.Float64() - rng.Float64()) * spread
		ny += (rng.Float64() - rng.Float64()) * spread
		nz += (rng.Float64() - rng.Float64()) * spread
	}
	speed := WitchSplashNormalVelocity2612
	if horizDist <= 2.0 {
		speed = WitchSplashCloseVelocity2612
	}
	vx = nx * speed
	vy = ny * speed
	vz = nz * speed
	return spawnX, spawnY, spawnZ, vx, vy, vz, horizDist
}

// SelectWitchCombatSplashPotion2612 implements the offensive splash potion selection in
// `Witch.performRangedAttack` (`Witch.java:3797-3835` in `26.1.2.jar`):
//  1. `horizDist >= 8.0 && !hasSlowness` -> `Potions.SLOWNESS`
//  2. `else if targetHP >= 8.0 && !hasPoison` -> `Potions.POISON`
//  3. `else if horizDist <= 3.0 && !hasWeakness && nextFloat() < 0.25` -> `Potions.WEAKNESS`
//  4. `else` -> `Potions.HARMING`
func SelectWitchCombatSplashPotion2612(
	horizDist float64,
	targetHP float32,
	hasSlowness, hasPoison, hasWeakness bool,
	rng *rand.Rand,
) string {
	if horizDist >= 8.0 && !hasSlowness {
		return WitchPotionSlowness
	}
	if targetHP >= 8.0 && !hasPoison {
		return WitchPotionPoison
	}
	if horizDist <= 3.0 && !hasWeakness {
		if rng == nil || rng.Float32() < WitchWeaknessSplashChance2612 {
			return WitchPotionWeakness
		}
	}
	return WitchPotionHarming
}

// SelectWitchRaiderSupportPotion2612 implements Raider ally potion choice in
// `Witch.performRangedAttack` (`Witch.java:3780-3796`):
//   - `allyHP <= 4.0` -> `Potions.HEALING`
//   - `allyHP > 4.0`  -> `Potions.REGENERATION`
func SelectWitchRaiderSupportPotion2612(allyHP float32) string {
	if allyHP <= WitchRaiderHealingThresholdHP2612 {
		return WitchPotionHealing
	}
	return WitchPotionRegeneration
}

// SpawnWitchSplashPotionProjectile2612 spawns a `minecraft:splash_potion` projectile entity
// (`ThrownSplashPotion`) in `dim` owned by `witchID`.
func (s *Server) SpawnWitchSplashPotionProjectile2612(
	dim string,
	witchID int32,
	potionType string,
	spawnX, spawnY, spawnZ float64,
	vx, vy, vz float64,
) int32 {
	if s == nil || s.entities == nil {
		return 0
	}
	normDim := world.NormalizeDimension(dim)
	id := s.entities.Add(&world.Entity{
		TypeID:    ResolveEntityTypeID(EntityTypeSplashPotion),
		TypeName:  EntityTypeSplashPotion,
		Dimension: normDim,
		X:         spawnX,
		Y:         spawnY,
		Z:         spawnZ,
		VelocityX: clampVelocityShort(vx),
		VelocityY: clampVelocityShort(vy),
		VelocityZ: clampVelocityShort(vz),
	})
	mgr := s.illagerManager()
	mgr.mu.Lock()
	mgr.splashPotions[id] = &WitchThrownSplashPotionState2612{
		EntityID:     id,
		Dimension:    normDim,
		OwnerWitchID: witchID,
		PotionType:   potionType,
		VX:           vx,
		VY:           vy,
		VZ:           vz,
	}
	if st := mgr.witches[witchID]; st != nil {
		st.LastThrownPotion = potionType
		st.LastThrownProjectileID = id
	}
	mgr.mu.Unlock()

	if ent := s.entities.Get(id); ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, ent))
	}
	return id
}

// ApplySplashPotionFromEntityAt2612 detonates a `ThrownSplashPotion` at `(x, y, z)` with
// `ownerEntityID` tracked so a Witch is 100% immune to her own splash potions (`0.0F`)
// while other Witches reduce `Instant Damage` by 85% (`* 0.15F`).
func (s *Server) ApplySplashPotionFromEntityAt2612(dim string, x, y, z float64, potionID string, directHitEntityID, ownerEntityID int32) int {
	if s == nil {
		return 0
	}
	if ownerEntityID <= 0 {
		return s.ApplySplashPotionAt(dim, x, y, z, potionID, directHitEntityID)
	}
	// Temporarily mark self-inflicted splash immunity for `ownerEntityID` if she is a Witch.
	normDim := world.NormalizeDimension(dim)
	mgr := s.illagerManager()
	mgr.mu.Lock()
	mgr.activeSplashOwnerID = ownerEntityID
	mgr.mu.Unlock()
	affected := s.ApplySplashPotionAt(normDim, x, y, z, potionID, directHitEntityID)
	mgr.mu.Lock()
	mgr.activeSplashOwnerID = 0
	mgr.mu.Unlock()
	return affected
}

func (s *Server) tickWitchSplashPotions2612() {
	mgr := s.illagerManager()
	mgr.mu.RLock()
	if len(mgr.splashPotions) == 0 {
		mgr.mu.RUnlock()
		return
	}
	ids := make([]int32, 0, len(mgr.splashPotions))
	for id := range mgr.splashPotions {
		ids = append(ids, id)
	}
	mgr.mu.RUnlock()

	for _, id := range ids {
		ent := s.entities.Get(id)
		if ent == nil {
			mgr.mu.Lock()
			delete(mgr.splashPotions, id)
			mgr.mu.Unlock()
			continue
		}
		mgr.mu.Lock()
		sp := mgr.splashPotions[id]
		if sp == nil {
			mgr.mu.Unlock()
			continue
		}
		sp.AgeTicks++
		vx, vy, vz := sp.VX, sp.VY, sp.VZ
		dim := sp.Dimension
		ownerID := sp.OwnerWitchID
		potType := sp.PotionType
		age := sp.AgeTicks
		mgr.mu.Unlock()

		nextX := ent.X + vx
		nextY := ent.Y + vy
		nextZ := ent.Z + vz
		hit := age >= 200
		hitX, hitY, hitZ := nextX, nextY, nextZ
		var directID int32

		// Check player collision along segment.
		for _, p := range s.onlinePlayersSnapshot() {
			if p == nil || p.IsDead() {
				continue
			}
			ps := p.Snapshot()
			if world.NormalizeDimension(ps.Dimension) != dim || ps.GameMode == GameModeSpectator {
				continue
			}
			if segmentHitsEntityAABB2612(ent.X, ent.Y, ent.Z, nextX, nextY, nextZ, ps.X, ps.Y, ps.Z, 0.3, 1.8) {
				hit = true
				hitX, hitY, hitZ = ps.X, ps.Y+0.5, ps.Z
				directID = ps.EntityID
				break
			}
		}
		// Check living mob collision along segment (skipping owner on tick 1 before leaving collision range).
		if !hit {
			cx := int32(int64(math.Floor(nextX)) >> 4)
			cz := int32(int64(math.Floor(nextZ)) >> 4)
			ents := s.entities.AppendInChunkRange(nil, cx, cz, 1)
			for i := range ents {
				other := &ents[i]
				if other.ID == id || (other.ID == ownerID && age <= 2) ||
					other.EffectiveDimension() != dim || !IsAttackableLivingMobEntity2612(other) {
					continue
				}
				if segmentHitsEntityAABB2612(ent.X, ent.Y, ent.Z, nextX, nextY, nextZ, other.X, other.Y, other.Z, 0.4, 1.8) {
					hit = true
					hitX, hitY, hitZ = other.X, other.Y+0.5, other.Z
					directID = other.ID
					break
				}
			}
		}
		// Check block collision along segment.
		if !hit {
			if cache := s.DimensionChunks(dim); cache != nil {
				const subSteps = 4
				for stp := 1; stp <= subSteps; stp++ {
					t := float64(stp) / float64(subSteps)
					sx := ent.X + vx*t
					sy := ent.Y + vy*t
					sz := ent.Z + vz*t
					bx, by, bz := int(math.Floor(sx)), int(math.Floor(sy)), int(math.Floor(sz))
					if by >= world.MinY && by < world.MinY+world.WorldHeight {
						bst := cache.GetBlock(bx, by, bz)
						if len(world.BlockCollisionBoxesAt(bx, by, bz, bst)) > 0 {
							for _, box := range world.BlockCollisionBoxesAt(bx, by, bz, bst) {
								if sx >= box.MinX && sx <= box.MaxX && sy >= box.MinY && sy <= box.MaxY && sz >= box.MinZ && sz <= box.MaxZ {
									hit = true
									hitX, hitY, hitZ = sx, sy, sz
									break
								}
							}
							if hit {
								break
							}
						}
					}
				}
			}
		}

		if hit {
			cp := ent.ChunkPos()
			s.ApplySplashPotionFromEntityAt2612(dim, hitX, hitY, hitZ, potType, directID, ownerID)
			mgr.mu.Lock()
			delete(mgr.splashPotions, id)
			mgr.mu.Unlock()
			s.entities.Remove(id)
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayRemoveEntities,
				encodeRemoveEntitiesPacket(nil, []int32{id}))
			continue
		}

		// Advance projectile with air drag (0.99) and gravity (-0.05).
		newVX := vx * WitchSplashAirDrag2612
		newVY := vy*WitchSplashAirDrag2612 - WitchSplashGravity2612
		newVZ := vz * WitchSplashAirDrag2612
		mgr.mu.Lock()
		if spNow := mgr.splashPotions[id]; spNow != nil {
			spNow.VX, spNow.VY, spNow.VZ = newVX, newVY, newVZ
		}
		mgr.mu.Unlock()
		s.entities.Update(id, func(e *world.Entity) {
			e.X = nextX
			e.Y = nextY
			e.Z = nextZ
			e.VelocityX = clampVelocityShort(newVX)
			e.VelocityY = clampVelocityShort(newVY)
			e.VelocityZ = clampVelocityShort(newVZ)
		})
	}
}

func segmentHitsEntityAABB2612(x0, y0, z0, x1, y1, z1, ex, ey, ez, halfW, height float64) bool {
	minX, maxX := ex-halfW-0.25, ex+halfW+0.25
	minY, maxY := ey-0.25, ey+height+0.25
	minZ, maxZ := ez-halfW-0.25, ez+halfW+0.25
	for i := 0; i <= 4; i++ {
		t := float64(i) / 4.0
		px := x0 + (x1-x0)*t
		py := y0 + (y1-y0)*t
		pz := z0 + (z1-z0)*t
		if px >= minX && px <= maxX && py >= minY && py <= maxY && pz >= minZ && pz <= maxZ {
			return true
		}
	}
	return false
}

// ============================================================================
// PILLAGER CROSSBOW (`RangedCrossbowAttackGoal` + `CrossbowItem` + `Multishot` / `Firework`)
// ============================================================================

// SetPillagerCrossbowEnchantments2612 sets `quick_charge` and `multishot` levels on
// Pillager `pillagerID`'s crossbow.
func (s *Server) SetPillagerCrossbowEnchantments2612(pillagerID int32, quickChargeLevel, multishotLevel int) bool {
	if s == nil || s.entities == nil {
		return false
	}
	ent := s.entities.Get(pillagerID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypePillager {
		return false
	}
	s.ensureIllagerOrRaiderState(pillagerID, MobTypePillager, false)
	mgr := s.illagerManager()
	mgr.mu.Lock()
	if st := mgr.illagers[pillagerID]; st != nil {
		st.QuickChargeLevel = quickChargeLevel
		st.MultishotLevel = multishotLevel
	}
	mgr.mu.Unlock()
	return true
}

// SetPillagerCrossbowDamage2612 sets the current durability damage (`0..464`) on
// Pillager `pillagerID`'s equipped crossbow.
func (s *Server) SetPillagerCrossbowDamage2612(pillagerID int32, damage int32) bool {
	if s == nil || s.entities == nil {
		return false
	}
	ent := s.entities.Get(pillagerID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypePillager {
		return false
	}
	s.ensureIllagerOrRaiderState(pillagerID, MobTypePillager, false)
	mgr := s.illagerManager()
	mgr.mu.Lock()
	if st := mgr.illagers[pillagerID]; st != nil {
		st.CrossbowDamage = damage
	}
	mgr.mu.Unlock()
	return true
}

func (s *Server) loadPillagerCrossbowProjectiles2612(pillagerID int32, multishotLevel int) []ChargedProjectile {
	count := 1
	if multishotLevel > 0 {
		count = 3
	}
	offhand := s.MobEquipment(pillagerID, EquipmentSlotOffHand)
	var baseProj ChargedProjectile
	if !offhand.IsEmpty() && canonicalResourceName(offhand.ResolvedID()) == "minecraft:firework_rocket" {
		fw := FireworksComponent{FlightDuration: DefaultFireworkFlightDuration}
		baseProj = ChargedProjectile{
			Stack:     world.NewNamedItemStack("minecraft:firework_rocket", 1),
			Fireworks: &fw,
		}
		// Consume 1 firework rocket from OFFHAND (`ProjectileWeaponItem.useAmmo`).
		rem := offhand.Count - 1
		if rem <= 0 {
			s.SetMobEquipment(pillagerID, EquipmentSlotOffHand, world.ItemStack{})
		} else {
			s.SetMobEquipment(pillagerID, EquipmentSlotOffHand, world.NewNamedItemStack(offhand.ResolvedID(), rem))
		}
	} else {
		baseProj = ChargedProjectile{
			Stack: world.NewNamedItemStack("minecraft:arrow", 1),
		}
	}
	projs := make([]ChargedProjectile, count)
	for i := 0; i < count; i++ {
		cp := ChargedProjectile{Stack: baseProj.Stack}
		if baseProj.Fireworks != nil {
			fwCopy := baseProj.Fireworks.Clone()
			cp.Fireworks = &fwCopy
		}
		projs[i] = cp
	}
	// Broadcast crossbow with CHARGED_PROJECTILES in MAINHAND.
	if ent := s.entities.Get(pillagerID); ent != nil {
		mainStack := s.MobEquipment(pillagerID, EquipmentSlotMainHand)
		if !mainStack.IsEmpty() {
			w := protocol.NewWriter(48)
			w.VarInt(pillagerID)
			w.Byte(IllagerEquipSlotMainHand)
			EncodeSlotWithChargedProjectiles(w, mainStack, projs)
			cp := ent.ChunkPos()
			s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, protocol.PlaySetEquipment, w.Bytes())
		}
	}
	return projs
}

// PerformPillagerCrossbowShooting2612 fires the loaded `ChargedProjectiles` of Pillager
// `pillagerID` at `(targetX, targetY, targetZ)` (`targetPlayer` if player, `targetMob` if mob),
// applying `getDurabilityUse` per shot projectile (`1` per arrow, `3` per firework rocket)
// and breaking the crossbow if `CrossbowDamage >= 465`.
func (s *Server) PerformPillagerCrossbowShooting2612(
	pillagerID int32,
	targetPlayer *PlayerSession,
	targetMob *world.Entity,
	rng *rand.Rand,
) (spawnedIDs []int32, crossbowBroke bool) {
	if s == nil || s.entities == nil {
		return nil, false
	}
	ent := s.entities.Get(pillagerID)
	if ent == nil {
		return nil, false
	}
	mainStack := s.MobEquipment(pillagerID, EquipmentSlotMainHand)
	if mainStack.IsEmpty() || canonicalResourceName(mainStack.ResolvedID()) != "minecraft:crossbow" {
		return nil, false
	}

	mgr := s.illagerManager()
	mgr.mu.Lock()
	st := mgr.illagers[pillagerID]
	if st == nil {
		mgr.mu.Unlock()
		return nil, false
	}
	charged := append([]ChargedProjectile(nil), st.ChargedProjectiles...)
	st.ChargedProjectiles = nil
	if len(charged) == 0 {
		mgr.mu.Unlock()
		charged = s.loadPillagerCrossbowProjectiles2612(pillagerID, st.MultishotLevel)
	} else {
		mgr.mu.Unlock()
	}

	dim := ent.EffectiveDimension()
	var tx, ty, tz float64
	if targetPlayer != nil {
		ps := targetPlayer.Snapshot()
		tx, ty, tz = ps.X, ps.Y+0.6, ps.Z
	} else if targetMob != nil {
		tx, ty, tz = targetMob.X, targetMob.Y+0.6, targetMob.Z
	} else {
		tx, ty, tz = ent.X+1.0, ent.Y+1.5, ent.Z
	}

	dx := tx - ent.X
	dz := tz - ent.Z
	horizDist := math.Hypot(dx, dz)
	dy := ty - (ent.Y + 1.5) + horizDist*0.2
	baseMag := math.Sqrt(dx*dx + dy*dy + dz*dz)
	if baseMag < 1e-6 {
		dx, dy, dz, baseMag = 1, 0, 0, 1
	}
	ux, uy, uz := dx/baseMag, dy/baseMag, dz/baseMag

	yawAnglesDeg := []float64{0}
	if len(charged) == 3 {
		yawAnglesDeg = []float64{0, 10.0, -10.0}
	}

	var totalDurabilityCost int32
	cp := ent.ChunkPos()
	for i, proj := range charged {
		angleRad := 0.0
		if i < len(yawAnglesDeg) {
			angleRad = yawAnglesDeg[i] * (math.Pi / 180.0)
		}
		cosA, sinA := math.Cos(angleRad), math.Sin(angleRad)
		rx := ux*cosA - uz*sinA
		ry := uy
		rz := ux*sinA + uz*cosA

		vx := rx * PillagerArrowSpeed
		vy := ry * PillagerArrowSpeed
		vz := rz * PillagerArrowSpeed

		projID := canonicalResourceName(proj.Stack.ResolvedID())
		if projID == "minecraft:firework_rocket" {
			totalDurabilityCost += CrossbowFireworkDurabilityCost
			fw := FireworksComponent{FlightDuration: DefaultFireworkFlightDuration}
			if proj.Fireworks != nil {
				fw = proj.Fireworks.Clone()
			}
			fid, _ := s.SpawnFireworkRocket(dim, ent.X, ent.Y+1.47, ent.Z, vx, vy, vz, fw, nil, nil, true)
			if fid != 0 {
				spawnedIDs = append(spawnedIDs, fid)
			}
		} else {
			totalDurabilityCost += CrossbowArrowDurabilityCost
			aid := s.entities.Add(&world.Entity{
				TypeID:    EntityTypeArrowID,
				TypeName:  MobTypeArrow,
				Dimension: dim,
				X:         ent.X + rx*0.5,
				Y:         ent.Y + 1.5,
				Z:         ent.Z + rz*0.5,
				VelocityX: clampVelocityShort(vx),
				VelocityY: clampVelocityShort(vy),
				VelocityZ: clampVelocityShort(vz),
			})
			if aid != 0 {
				spawnedIDs = append(spawnedIDs, aid)
				if aEnt := s.entities.Get(aid); aEnt != nil {
					s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, aEnt))
				}
			}
		}
	}

	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundCrossbowShoot, SoundSourceHostile, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))

	// Apply direct hit damage to target when within firing line (matching other RegionIO ranged mobs).
	if len(spawnedIDs) > 0 {
		if targetPlayer != nil && !targetPlayer.IsDead() {
			s.AttackPlayerFromMob(pillagerID, MobTypePillager, ent.X, ent.Z, targetPlayer, PillagerArrowDamage)
		} else if targetMob != nil {
			dmg := ScaleDamageByDifficulty(PillagerArrowDamage, s.Difficulty())
			s.applyDamageToMob(targetMob, dmg, nil, 0.3, false, false)
		}
	}

	// Apply durability damage (`hurtAndBreak` per fired projectile); break crossbow at >= 465!
	mgr.mu.Lock()
	if stNow := mgr.illagers[pillagerID]; stNow != nil {
		stNow.CrossbowDamage += totalDurabilityCost
		if stNow.CrossbowDamage >= PillagerCrossbowMaxDurability2612 {
			crossbowBroke = true
		}
	}
	mgr.mu.Unlock()

	if crossbowBroke {
		s.SetMobEquipment(pillagerID, EquipmentSlotMainHand, world.ItemStack{})
	} else {
		// Sync cleared CHARGED_PROJECTILES back to client.
		s.SetMobEquipment(pillagerID, EquipmentSlotMainHand, mainStack)
	}
	_ = rng
	return spawnedIDs, crossbowBroke
}

func (s *Server) tickPillagerCrossbowAI2612(pillagerID int32, ent *world.Entity, rng *rand.Rand) {
	if s.Difficulty() == DifficultyPeaceful {
		return
	}
	mainHand := s.MobEquipment(pillagerID, EquipmentSlotMainHand)
	holdingCrossbow := !mainHand.IsEmpty() && canonicalResourceName(mainHand.ResolvedID()) == "minecraft:crossbow"
	if !holdingCrossbow {
		mgr := s.illagerManager()
		mgr.mu.Lock()
		if st := mgr.illagers[pillagerID]; st != nil && st.CrossbowState != PillagerCrossbowUncharged {
			st.CrossbowState = PillagerCrossbowUncharged
			st.CrossbowAttackDelay = 0
			st.CrossbowSeeTime = 0
			st.ChargedProjectiles = nil
		}
		mgr.mu.Unlock()
		s.SetPillagerChargingCrossbow(pillagerID, false)
		return
	}

	dim := ent.EffectiveDimension()
	cache := s.DimensionChunks(dim)
	targetPlayer, targetMob, tx, ty, tz, hasTarget := s.findIllagerCombatTarget2612(pillagerID, ent, PillagerFollowRange2612, false)
	if !hasTarget {
		mgr := s.illagerManager()
		mgr.mu.Lock()
		wasCharging := false
		if st := mgr.illagers[pillagerID]; st != nil {
			wasCharging = st.ChargingCrossbow
			st.CrossbowState = PillagerCrossbowUncharged
			st.CrossbowSeeTime = 0
			st.CrossbowAttackDelay = 0
			st.ChargedProjectiles = nil
		}
		mgr.mu.Unlock()
		if wasCharging {
			s.SetPillagerChargingCrossbow(pillagerID, false)
		}
		return
	}

	hasLOS := hasColliderLineOfSight2612(cache, ent.X, ent.Y+1.62, ent.Z, tx, ty+1.4, tz)
	dx, dy, dz := tx-ent.X, ty-ent.Y, tz-ent.Z
	distSq := dx*dx + dy*dy + dz*dz

	mgr := s.illagerManager()
	mgr.mu.Lock()
	st := mgr.illagers[pillagerID]
	if st == nil {
		mgr.mu.Unlock()
		return
	}
	wasSeeing := st.CrossbowSeeTime > 0
	if hasLOS != wasSeeing {
		st.CrossbowSeeTime = 0
	}
	if hasLOS {
		st.CrossbowSeeTime++
	} else {
		st.CrossbowSeeTime--
	}
	seeTime := st.CrossbowSeeTime
	cbState := st.CrossbowState
	attackDelay := st.CrossbowAttackDelay
	qcLevel := st.QuickChargeLevel
	msLevel := st.MultishotLevel
	mgr.mu.Unlock()

	needsMove := (distSq > PillagerCrossbowAttackRadiusSq2612 || seeTime < PillagerCrossbowMinSeeTime2612) && attackDelay == 0
	if needsMove {
		speed := PillagerMovementSpeed2612 * 0.7 // ground path speed
		if cbState != PillagerCrossbowUncharged {
			speed *= 0.5
		}
		s.moveIllagerHorizontallyToward2612(pillagerID, ent, tx, tz, speed)
	}

	switch cbState {
	case PillagerCrossbowUncharged:
		if !needsMove {
			mgr.mu.Lock()
			if stNow := mgr.illagers[pillagerID]; stNow != nil {
				stNow.CrossbowState = PillagerCrossbowCharging
				stNow.CrossbowChargeTicks = 0
			}
			mgr.mu.Unlock()
			s.SetPillagerChargingCrossbow(pillagerID, true)
		}

	case PillagerCrossbowCharging:
		reqTicks := CrossbowChargeDuration(qcLevel)
		mgr.mu.Lock()
		var doneCharging bool
		if stNow := mgr.illagers[pillagerID]; stNow != nil {
			stNow.CrossbowChargeTicks++
			if stNow.CrossbowChargeTicks >= reqTicks {
				doneCharging = true
				stNow.CrossbowState = PillagerCrossbowCharged
				delay := PillagerCrossbowAttackDelayMin2612
				if rng != nil {
					delay += rng.Intn(PillagerCrossbowAttackDelayRange2612)
				} else {
					delay += 10
				}
				stNow.CrossbowAttackDelay = delay
			}
		}
		mgr.mu.Unlock()
		if doneCharging {
			loaded := s.loadPillagerCrossbowProjectiles2612(pillagerID, msLevel)
			mgr.mu.Lock()
			if stNow := mgr.illagers[pillagerID]; stNow != nil {
				stNow.ChargedProjectiles = loaded
			}
			mgr.mu.Unlock()
			s.SetPillagerChargingCrossbow(pillagerID, false)
		}

	case PillagerCrossbowCharged:
		mgr.mu.Lock()
		if stNow := mgr.illagers[pillagerID]; stNow != nil {
			stNow.CrossbowAttackDelay--
			if stNow.CrossbowAttackDelay <= 0 {
				stNow.CrossbowAttackDelay = 0
				stNow.CrossbowState = PillagerCrossbowReadyToAttack
			}
		}
		mgr.mu.Unlock()

	case PillagerCrossbowReadyToAttack:
		if hasLOS {
			s.PerformPillagerCrossbowShooting2612(pillagerID, targetPlayer, targetMob, rng)
			mgr.mu.Lock()
			if stNow := mgr.illagers[pillagerID]; stNow != nil {
				stNow.CrossbowState = PillagerCrossbowUncharged
			}
			mgr.mu.Unlock()
		}
	}
}

// ============================================================================
// VINDICATOR (`MeleeAttackGoal`, `RaiderOpenDoorGoal`, `VindicatorBreakDoorGoal`, `"Johnny"`)
// ============================================================================

// IsAttackableLivingMobEntity2612 reports whether `ent` is an attackable `LivingEntity`
// (`LivingEntity.attackable() == true`, excluding items, XP orbs, armor stands, projectiles,
// vehicles, decorations, displays, markers, TNT, crystals, fangs, etc.).
func IsAttackableLivingMobEntity2612(ent *world.Entity) bool {
	if ent == nil || ent.IsItem() || ent.IsExperienceOrb() {
		return false
	}
	norm := canonicalResourceName(ent.TypeName)
	if norm == "" || norm == "minecraft:player" || norm == EntityTypeArmorStand {
		return false
	}
	if IsDecorationEntity(norm) ||
		IsStandardProjectileEntityType(norm) ||
		IsFireballEntityType(norm) ||
		IsWindChargeEntityType(norm) ||
		IsVehicleEntity(norm) {
		return false
	}
	if strings.HasSuffix(norm, "_boat") || strings.HasSuffix(norm, "_raft") || strings.Contains(norm, "minecart") {
		return false
	}
	switch norm {
	case EntityTypeFishingBobber, EntityTypeLlamaSpit, EntityTypeLightning, EntityTypeEvokerFangs,
		"minecraft:area_effect_cloud", "minecraft:end_crystal", "minecraft:tnt",
		"minecraft:falling_block", "minecraft:shulker_bullet", "minecraft:wither_skull",
		"minecraft:dragon_fireball", "minecraft:eye_of_ender", "minecraft:potion",
		"minecraft:splash_potion", "minecraft:lingering_potion", "minecraft:experience_bottle",
		"minecraft:firework_rocket", "minecraft:ominous_item_spawner", "minecraft:interaction",
		"minecraft:block_display", "minecraft:item_display", "minecraft:text_display", "minecraft:marker":
		return false
	}
	return true
}

// IsValidJohnnyTargetEntity2612 implements `VindicatorJohnnyAttackGoal` + `TargetingConditions.test`
// + `AbstractIllager.considersEntityAsAlly` + `AbstractIllager.canAttack` + `Mob.canAttack`
// (`26.1.2.jar`):
//   - Must be an attackable `LivingEntity` (`ArmorStand.attackable() == false`);
//   - Excludes `#minecraft:illager` (`pillager`, `vindicator`, `evoker`, `illusioner` via `ILLAGER_FRIENDS`);
//   - Excludes `minecraft:ghast` (`Mob.canAttack`);
//   - Excludes baby `AbstractVillager` (`villager` / `wandering_trader` when `IsMobBaby`).
func (s *Server) IsValidJohnnyTargetEntity2612(vindicatorID int32, candidate *world.Entity) bool {
	if candidate == nil || candidate.ID == vindicatorID || !IsAttackableLivingMobEntity2612(candidate) {
		return false
	}
	norm := canonicalResourceName(candidate.TypeName)
	if IsIllagerMob(norm) {
		return false
	}
	if norm == MobTypeGhast {
		return false
	}
	if s != nil && (norm == MobTypeVillager || norm == MobTypeWanderingTrader) && s.IsMobBaby(candidate.ID) {
		return false
	}
	if s != nil && s.MobHealth(candidate.ID) <= 0 {
		return false
	}
	return true
}

// IsEntityInActiveRaid2612 reports whether `entityID` belongs to an active Raid (`Raider.hasActiveRaid()`).
func (s *Server) IsEntityInActiveRaid2612(entityID int32) bool {
	if s == nil || entityID <= 0 {
		return false
	}
	rm := s.raidManager()
	rm.mu.Lock()
	defer rm.mu.Unlock()
	rs := rm.raiderIndex[entityID]
	if rs == nil || rs.RaidID <= 0 {
		return false
	}
	r := rm.raids[rs.RaidID]
	return r != nil && r.Status == RaidStatusOngoing
}

// IsMobInteractableDoorState2612 matches `DoorBlock.isWoodenDoor` (`#minecraft:mob_interactable_doors`
// in `26.1.2.jar`: all wooden doors + all copper door variants, excluding `iron_door`).
func IsMobInteractableDoorState2612(state uint16) bool {
	if state == world.StateAir {
		return false
	}
	name := canonicalResourceName(world.BlockName(state))
	return strings.HasSuffix(name, "_door") && name != "minecraft:iron_door"
}

// SetDoorOpenAt2612 sets `open=true/false` on both halves of the door at `(x, y, z)`.
func (s *Server) SetDoorOpenAt2612(dim string, x, y, z int, open bool) bool {
	if s == nil {
		return false
	}
	normDim := world.NormalizeDimension(dim)
	cache := s.DimensionChunks(normDim)
	if cache == nil {
		return false
	}
	st := cache.GetBlock(x, y, z)
	if !IsMobInteractableDoorState2612(st) {
		return false
	}
	name, props, ok := world.BlockStateProperties(st)
	if !ok {
		return false
	}
	openStr := "false"
	if open {
		openStr = "true"
	}
	if props["open"] == openStr {
		return false
	}
	lowerY := y
	if props["half"] == "upper" {
		lowerY = y - 1
	}
	changed := false
	for _, dy := range []int{0, 1} {
		by := lowerY + dy
		cur := cache.GetBlock(x, by, z)
		curName, curProps, cok := world.BlockStateProperties(cur)
		if !cok || canonicalResourceName(curName) != canonicalResourceName(name) {
			continue
		}
		np := make(map[string]string, len(curProps))
		for k, v := range curProps {
			np[k] = v
		}
		np["open"] = openStr
		if nextSt, nok := world.BlockStateByName(curName, np); nok && nextSt != cur {
			if applied, upd := cache.ApplyBlockWithUpdates(x, by, z, nextSt); applied {
				s.broadcastDimensionBlockChanges(normDim, cache, upd.Changes, upd.LightChanged)
				changed = true
			}
		}
	}
	return changed
}

func (s *Server) removeBothDoorHalvesAt2612(dim string, x, y, z int) bool {
	normDim := world.NormalizeDimension(dim)
	cache := s.DimensionChunks(normDim)
	if cache == nil {
		return false
	}
	st := cache.GetBlock(x, y, z)
	if !IsMobInteractableDoorState2612(st) {
		return false
	}
	name, props, ok := world.BlockStateProperties(st)
	if !ok {
		return false
	}
	lowerY := y
	if props["half"] == "upper" {
		lowerY = y - 1
	}
	cx, cz := int32(x>>4), int32(z>>4)
	s.BroadcastDimensionChunk(normDim, cx, cz, PlayLevelEventCB,
		EncodeLevelEventPacket(LevelEventSoundZombieBreakWoodenDoor, x, lowerY, z, 0, false))
	s.BroadcastDimensionChunk(normDim, cx, cz, PlayLevelEventCB,
		EncodeLevelEventPacket(2001, x, lowerY, z, int32(st), false))

	for _, dy := range []int{0, 1} {
		by := lowerY + dy
		cur := cache.GetBlock(x, by, z)
		if canonicalResourceName(world.BlockName(cur)) == canonicalResourceName(name) {
			if applied, upd := cache.ApplyBlockWithUpdates(x, by, z, world.StateAir); applied {
				s.broadcastDimensionBlockChanges(normDim, cache, upd.Changes, upd.LightChanged)
			} else {
				cache.SetBlock(x, by, z, world.StateAir)
			}
		}
	}
	return true
}

func findClosedAdjacentInteractableDoor2612(cache *world.Cache, mobX, mobY, mobZ, targetX, targetZ float64) (dx, dy, dz int, found bool) {
	if cache == nil {
		return 0, 0, 0, false
	}
	bx, by, bz := int(math.Floor(mobX)), int(math.Floor(mobY)), int(math.Floor(mobZ))
	// Prefer the block directly along `(targetX - mobX, targetZ - mobZ)` first, then 4 cardinal neighbors + current cell.
	stepX, stepZ := 0, 0
	if math.Abs(targetX-mobX) >= math.Abs(targetZ-mobZ) {
		if targetX > mobX {
			stepX = 1
		} else if targetX < mobX {
			stepX = -1
		}
	} else {
		if targetZ > mobZ {
			stepZ = 1
		} else if targetZ < mobZ {
			stepZ = -1
		}
	}
	candidates := [][2]int{
		{bx + stepX, bz + stepZ},
		{bx, bz},
		{bx + 1, bz},
		{bx - 1, bz},
		{bx, bz + 1},
		{bx, bz - 1},
	}
	for _, c := range candidates {
		for _, yOff := range []int{0, 1} {
			cy := by + yOff
			st := cache.GetBlock(c[0], cy, c[1])
			if isClosedWoodenDoorState2612(st) {
				_, props, ok := world.BlockStateProperties(st)
				if ok && props["half"] == "upper" {
					cy--
				}
				distSq := (float64(c[0])+0.5-mobX)*(float64(c[0])+0.5-mobX) + (float64(c[1])+0.5-mobZ)*(float64(c[1])+0.5-mobZ)
				if distSq <= 2.25 {
					return c[0], cy, c[1], true
				}
			}
		}
	}
	return 0, 0, 0, false
}

func (s *Server) tickVindicatorAI2612(vindID int32, ent *world.Entity, rng *rand.Rand) {
	if s.Difficulty() == DifficultyPeaceful {
		return
	}
	dim := ent.EffectiveDimension()
	cache := s.DimensionChunks(dim)
	inRaid := s.IsEntityInActiveRaid2612(vindID)

	mgr := s.illagerManager()
	mgr.mu.Lock()
	st := mgr.illagers[vindID]
	if st == nil {
		mgr.mu.Unlock()
		return
	}
	if st.MeleeAttackCooldown > 0 {
		st.MeleeAttackCooldown--
	}
	isJohnny := st.IsJohnny
	canAttackNow := st.MeleeAttackCooldown <= 0
	doorActive := st.DoorBreakActive
	doorX, doorY, doorZ := st.DoorBreakX, st.DoorBreakY, st.DoorBreakZ
	mgr.mu.Unlock()

	targetPlayer, targetMob, tx, tz := (*PlayerSession)(nil), (*world.Entity)(nil), ent.X, ent.Z
	var ty float64
	hasTarget := false
	targetPlayer, targetMob, tx, ty, tz, hasTarget = s.findIllagerCombatTarget2612(vindID, ent, VindicatorFollowRange2612, isJohnny)
	_ = ty

	// 1. VindicatorBreakDoorGoal (priority 2) & RaiderOpenDoorGoal (priority 3) when in an active Raid!
	canBreakDiff := s.Difficulty() == DifficultyNormal || s.Difficulty() == DifficultyHard
	if doorActive {
		// Check BreakDoorGoal.canContinueToUse: active raid, valid difficulty, mobGriefing, closed door within 2.0 blocks.
		stillClosed := cache != nil && isClosedWoodenDoorState2612(cache.GetBlock(doorX, doorY, doorZ))
		ddx := float64(doorX) + 0.5 - ent.X
		ddy := float64(doorY) + 0.5 - ent.Y
		ddz := float64(doorZ) + 0.5 - ent.Z
		if !inRaid || !canBreakDiff || !s.MobGriefing() || !stillClosed || (ddx*ddx+ddy*ddy+ddz*ddz) >= 4.0 {
			mgr.mu.Lock()
			if stNow := mgr.illagers[vindID]; stNow != nil {
				stNow.DoorBreakActive = false
				stNow.DoorBreakTicks = 0
				stNow.DoorLastProgress = -1
			}
			mgr.mu.Unlock()
			s.BroadcastDimensionChunk(dim, int32(doorX>>4), int32(doorZ>>4), protocol.PlayBlockDestruction,
				EncodeBlockDestructionPacket(vindID, doorX, doorY, doorZ, -1))
		} else {
			if rng != nil && rng.Intn(20) == 0 {
				s.BroadcastDimensionChunk(dim, int32(doorX>>4), int32(doorZ>>4), PlayLevelEventCB,
					EncodeLevelEventPacket(LevelEventSoundZombieWoodenDoor, doorX, doorY, doorZ, 0, false))
			}
			var bt, prevProg int
			mgr.mu.Lock()
			if stNow := mgr.illagers[vindID]; stNow != nil {
				stNow.DoorBreakTicks++
				bt = stNow.DoorBreakTicks
				prevProg = stNow.DoorLastProgress
			}
			mgr.mu.Unlock()
			prog := int(float32(bt) / float32(VindicatorDoorBreakTicks2612) * 10.0)
			if prog != prevProg {
				mgr.mu.Lock()
				if stNow := mgr.illagers[vindID]; stNow != nil {
					stNow.DoorLastProgress = prog
				}
				mgr.mu.Unlock()
				s.BroadcastDimensionChunk(dim, int32(doorX>>4), int32(doorZ>>4), protocol.PlayBlockDestruction,
					EncodeBlockDestructionPacket(vindID, doorX, doorY, doorZ, prog))
			}
			if bt >= VindicatorDoorBreakTicks2612 {
				s.removeBothDoorHalvesAt2612(dim, doorX, doorY, doorZ)
				mgr.mu.Lock()
				if stNow := mgr.illagers[vindID]; stNow != nil {
					stNow.DoorBreakActive = false
					stNow.DoorBreakTicks = 0
					stNow.DoorLastProgress = -1
				}
				mgr.mu.Unlock()
				s.BroadcastDimensionChunk(dim, int32(doorX>>4), int32(doorZ>>4), protocol.PlayBlockDestruction,
					EncodeBlockDestructionPacket(vindID, doorX, doorY, doorZ, -1))
			}
			return // MOVE flag occupied by VindicatorBreakDoorGoal while breaking door
		}
	} else if inRaid {
		if fdx, fdy, fdz, foundDoor := findClosedAdjacentInteractableDoor2612(cache, ent.X, ent.Y, ent.Z, tx, tz); foundDoor {
			if s.MobGriefing() && canBreakDiff {
				// VindicatorBreakDoorGoal (priority 2): starts 240-tick door break when `nextInt(5) == 0` (or immediately if rng==nil).
				if rng == nil || rng.Intn(5) == 0 {
					mgr.mu.Lock()
					if stNow := mgr.illagers[vindID]; stNow != nil {
						stNow.DoorBreakActive = true
						stNow.DoorBreakX, stNow.DoorBreakY, stNow.DoorBreakZ = fdx, fdy, fdz
						stNow.DoorBreakTicks = 0
						stNow.DoorLastProgress = -1
					}
					mgr.mu.Unlock()
					return
				}
			} else {
				// RaiderOpenDoorGoal (priority 3): opens wooden/copper door when BreakDoorGoal cannot run (e.g. Easy or mobGriefing=false)!
				s.SetDoorOpenAt2612(dim, fdx, fdy, fdz, true)
			}
		}
	}

	if !hasTarget {
		return
	}

	dx := tx - ent.X
	dz := tz - ent.Z
	dist := math.Hypot(dx, dz)
	if dist > 1.65 {
		s.moveIllagerHorizontallyToward2612(vindID, ent, tx, tz, VindicatorMovementSpeed2612*0.8)
	}
	if dist <= 2.1 && canAttackNow {
		mgr.mu.Lock()
		if stNow := mgr.illagers[vindID]; stNow != nil {
			stNow.MeleeAttackCooldown = VindicatorMeleeIntervalTicks2612
		}
		mgr.mu.Unlock()
		if targetPlayer != nil {
			s.AttackPlayerFromMob(vindID, MobTypeVindicator, ent.X, ent.Z, targetPlayer, VindicatorMeleeBaseDamage2612)
		} else if targetMob != nil {
			dmg := ScaleDamageByDifficulty(VindicatorMeleeBaseDamage2612, s.Difficulty())
			s.applyDamageToMob(targetMob, dmg, nil, 0.3, false, true)
		}
	}
}

// ============================================================================
// EVOKER, EVOKER FANGS & VEX (`SpellcasterIllager`, `createSpellEntity`, `AvoidEntityGoal`)
// ============================================================================

func isBlockFaceSturdyUp2612(state uint16) bool {
	if state == world.StateAir {
		return false
	}
	boxes := world.BlockCollisionBoxes(state)
	if len(boxes) == 0 {
		return false
	}
	for _, b := range boxes {
		if b.MinX <= 0.001 && b.MaxX >= 0.999 && b.MinZ <= 0.001 && b.MaxZ >= 0.999 && b.MaxY >= 0.999 {
			return true
		}
	}
	return false
}

// ResolveEvokerFangGroundY2612 implements `Evoker.EvokerAttackSpellGoal.createSpellEntity`
// (`Evoker.java:4371-4440` in `26.1.2.jar`):
//   - Scans `pos` downward from `BlockPos.containing(x, maxY, z)` while `pos.Y >= floor(minY) - 1`;
//   - Requires `pos.below()` to have a sturdy `UP` face (`isFaceSturdy(Direction.UP)`);
//   - Adds `shape.max(Axis.Y)` of `pos` (`0.0` for air, `0.5` for bottom slab, `1.0` for full block).
func ResolveEvokerFangGroundY2612(cache *world.Cache, x, z, minY, maxY float64) (spawnY float64, ok bool) {
	if cache == nil {
		return minY, true
	}
	bx := int(math.Floor(x))
	bz := int(math.Floor(z))
	py := int(math.Floor(maxY))
	limitY := int(math.Floor(minY)) - 1

	for py >= limitY {
		belowY := py - 1
		if belowY < world.MinY || py >= world.MinY+world.WorldHeight {
			py--
			continue
		}
		belowState := cache.GetBlock(bx, belowY, bz)
		if isBlockFaceSturdyUp2612(belowState) {
			yOffset := 0.0
			curState := cache.GetBlock(bx, py, bz)
			if curState != world.StateAir {
				for _, box := range world.BlockCollisionBoxes(curState) {
					if box.MaxY > yOffset {
						yOffset = box.MaxY
					}
				}
			}
			return float64(py) + yOffset, true
		}
		py--
	}
	return 0, false
}

// IsEvokerAlliedToEntity2612 implements `Evoker.considersEntityAsAlly` + `Entity.isAlliedTo`
// (`Evoker.java:5108-5136` & `AbstractIllager.java:4140-4163` in `26.1.2.jar`):
//   - Self (`other.ID == evokerID`);
//   - `#minecraft:illager` (`pillager`, `vindicator`, `evoker`, `illusioner`);
//   - `minecraft:vex` whose `OwnerEvokerID` is `evokerID` or another `#minecraft:illager`.
func (s *Server) IsEvokerAlliedToEntity2612(evokerID int32, other *world.Entity) bool {
	if other == nil {
		return false
	}
	if evokerID > 0 && other.ID == evokerID {
		return true
	}
	norm := canonicalResourceName(other.TypeName)
	if IsIllagerMob(norm) {
		return true
	}
	if norm == MobTypeVex && s != nil {
		if vs, ok := s.GetVexState(other.ID); ok && vs.OwnerEvokerID > 0 {
			if vs.OwnerEvokerID == evokerID {
				return true
			}
			if ownerEnt := s.entities.Get(vs.OwnerEvokerID); ownerEnt != nil && IsIllagerMob(ownerEnt.TypeName) {
				return true
			}
		}
	}
	return false
}

// CountNearbyAliveVexes2612 counts alive `minecraft:vex` entities within `evoker.getBoundingBox().inflate(16.0)`
// (`EvokerSummonSpellGoal.canUse` in `26.1.2.jar`).
func (s *Server) CountNearbyAliveVexes2612(dim string, x, y, z float64) int {
	if s == nil || s.entities == nil {
		return 0
	}
	normDim := world.NormalizeDimension(dim)
	count := 0
	for _, ent := range s.entities.AppendAll(nil) {
		if ent.EffectiveDimension() != normDim || canonicalResourceName(ent.TypeName) != MobTypeVex {
			continue
		}
		if s.MobHealth(ent.ID) <= 0 {
			continue
		}
		if math.Abs(ent.X-x) <= EvokerSummonVexCheckRange2612+0.3 &&
			math.Abs(ent.Y-y) <= EvokerSummonVexCheckRange2612+1.0 &&
			math.Abs(ent.Z-z) <= EvokerSummonVexCheckRange2612+0.3 {
			count++
		}
	}
	return count
}

// CanEvokerStartSummonVex2612 implements the `random.nextInt(8) + 1 > nearbyVexCount` check
// in `EvokerSummonSpellGoal.canUse()` (`Evoker.java:4603-4636`).
func (s *Server) CanEvokerStartSummonVex2612(dim string, x, y, z float64, rng *rand.Rand) bool {
	nearby := s.CountNearbyAliveVexes2612(dim, x, y, z)
	if nearby >= EvokerSummonVexMaxNearby2612 {
		return false
	}
	roll := EvokerSummonVexMaxNearby2612
	if rng != nil {
		roll = rng.Intn(EvokerSummonVexMaxNearby2612) + 1
	}
	return roll > nearby
}

func (s *Server) findEvokerWololoBlueSheep2612(dim string, ex, ey, ez float64, rng *rand.Rand) (int32, bool) {
	if s == nil || s.entities == nil || !s.MobGriefing() {
		return 0, false
	}
	normDim := world.NormalizeDimension(dim)
	const woolBlue byte = 11
	var matches []int32
	for _, other := range s.entities.AppendAll(nil) {
		if other.EffectiveDimension() != normDim || canonicalResourceName(other.TypeName) != MobTypeSheep {
			continue
		}
		if s.MobHealth(other.ID) <= 0 || s.IsMobBaby(other.ID) || s.IsSheepSheared(other.ID) || s.SheepWoolColor(other.ID) != woolBlue {
			continue
		}
		if math.Abs(other.X-ex) <= EvokerWololoRange &&
			math.Abs(other.Y-ey) <= EvokerWololoVertRange2612 &&
			math.Abs(other.Z-ez) <= EvokerWololoRange {
			matches = append(matches, other.ID)
		}
	}
	if len(matches) == 0 {
		return 0, false
	}
	if rng != nil && len(matches) > 1 {
		return matches[rng.Intn(len(matches))], true
	}
	return matches[0], true
}

func (s *Server) tickEvokerAI2612(evokerID int32, ent *world.Entity, rng *rand.Rand) {
	if s.Difficulty() == DifficultyPeaceful {
		return
	}
	dim := ent.EffectiveDimension()
	cp := ent.ChunkPos()

	mgr := s.illagerManager()
	mgr.mu.Lock()
	st := mgr.illagers[evokerID]
	if st == nil {
		mgr.mu.Unlock()
		return
	}
	celeb := st.Celebrating

	// 1. If currently casting a spell (`SpellCastTicks > 0`):
	//    - `EvokerCastingSpellGoal` (priority 1) stops movement and faces target;
	//    - `SpellcasterUseSpellGoal.tick()` decrements `SpellWarmupDelay` and fires `performSpellCasting()` at 0;
	//    - `SpellcasterIllager.customServerAiStep()` decrements `SpellCastTicks` and clears `DATA_SPELL_CASTING_ID` when 0.
	if st.SpellCastTicks > 0 {
		var fireSpell byte
		tx, ty, tz := st.SpellTargetX, st.SpellTargetY, st.SpellTargetZ
		sheepID := st.WololoSheepID
		if st.SpellWarmupDelay > 0 {
			st.SpellWarmupDelay--
			if st.SpellWarmupDelay == 0 {
				fireSpell = st.ActiveSpell
			}
		}
		st.SpellCastTicks--
		clearSpell := false
		if st.SpellCastTicks == 0 {
			st.ActiveSpell = IllagerSpellNone
			clearSpell = true
		}
		mgr.mu.Unlock()

		switch fireSpell {
		case IllagerSpellFangs:
			// Refresh live target position if still present.
			if _, _, ltx, lty, ltz, ok := s.findIllagerCombatTarget2612(evokerID, ent, EvokerFollowRange2612, false); ok {
				tx, ty, tz = ltx, lty, ltz
			}
			s.performEvokerFangsSpellEntities2612(evokerID, ent, tx, ty, tz)
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
				EncodeSoundPacket(SoundEvokerCastSpell, SoundSourceHostile, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
		case IllagerSpellSummonVex:
			s.performEvokerSummonVexEntities2612(evokerID, ent, rng)
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
				EncodeSoundPacket(SoundEvokerCastSpell, SoundSourceHostile, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
		case IllagerSpellWololo:
			if s.MobGriefing() && sheepID > 0 {
				if shEnt := s.entities.Get(sheepID); shEnt != nil && s.MobHealth(sheepID) > 0 && s.SheepWoolColor(sheepID) == 11 {
					s.SetSheepWoolColor(sheepID, 14)
				}
			}
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
				EncodeSoundPacket(SoundEvokerCastSpell, SoundSourceHostile, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
		}
		if clearSpell {
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
				EncodeSpellcasterDataPacket(evokerID, celeb, IllagerSpellNone))
		}
		return
	}

	fangsCD := st.FangsCooldown
	vexCD := st.SummonVexCooldown
	wololoCD := st.WololoCooldown
	mgr.mu.Unlock()

	_, _, tx, ty, tz, hasTarget := s.findIllagerCombatTarget2612(evokerID, ent, EvokerFollowRange2612, false)

	// 2. `AvoidEntityGoal(Player, 8.0F, 0.6D, 1.0D)` (priority 2) — retreats from nearby player when NOT casting!
	//    Note: `SpellcasterUseSpellGoal` has NO Goal.Flag.MOVE, so `AvoidEntityGoal` runs alongside spell selection
	//    until a spell starts (at which point `SpellCastTicks > 0` activates `EvokerCastingSpellGoal` priority 1).
	s.stepEvokerAvoidNearestPlayer2612(evokerID, ent)

	// 3. Spell start goals in exact priority order:
	//    Priority 4: `EvokerSummonSpellGoal` (`hasTarget && vexCD <= 0 && CanEvokerStartSummonVex2612`)
	//    Priority 5: `EvokerAttackSpellGoal` (`hasTarget && fangsCD <= 0`)
	//    Priority 6: `EvokerWololoSpellGoal` (`!hasTarget && wololoCD <= 0 && MobGriefing() && blueSheepFound`)
	if hasTarget && vexCD <= 0 && s.CanEvokerStartSummonVex2612(dim, ent.X, ent.Y, ent.Z, rng) {
		mgr.mu.Lock()
		if stNow := mgr.illagers[evokerID]; stNow != nil {
			stNow.ActiveSpell = IllagerSpellSummonVex
			stNow.SpellWarmupDelay = EvokerSummonVexSpellWarmupTicks2612
			stNow.SpellCastTicks = EvokerSummonVexSpellCastTicks2612
			stNow.SummonVexCooldown = EvokerSummonVexSpellIntervalTicks2612
		}
		mgr.mu.Unlock()
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
			EncodeSpellcasterDataPacket(evokerID, celeb, IllagerSpellSummonVex))
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
			EncodeSoundPacket(SoundEvokerPrepareSummon, SoundSourceHostile, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
		return
	}

	if hasTarget && fangsCD <= 0 {
		mgr.mu.Lock()
		if stNow := mgr.illagers[evokerID]; stNow != nil {
			stNow.ActiveSpell = IllagerSpellFangs
			stNow.SpellWarmupDelay = EvokerFangsSpellWarmupTicks2612
			stNow.SpellCastTicks = EvokerFangsSpellCastTicks2612
			stNow.FangsCooldown = EvokerFangsSpellIntervalTicks2612
			stNow.SpellTargetX, stNow.SpellTargetY, stNow.SpellTargetZ = tx, ty, tz
		}
		mgr.mu.Unlock()
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
			EncodeSpellcasterDataPacket(evokerID, celeb, IllagerSpellFangs))
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
			EncodeSoundPacket(SoundEvokerPrepareAttack, SoundSourceHostile, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
		return
	}

	if !hasTarget && wololoCD <= 0 && s.MobGriefing() {
		if sheepID, found := s.findEvokerWololoBlueSheep2612(dim, ent.X, ent.Y, ent.Z, rng); found {
			mgr.mu.Lock()
			if stNow := mgr.illagers[evokerID]; stNow != nil {
				stNow.ActiveSpell = IllagerSpellWololo
				stNow.SpellWarmupDelay = EvokerWololoSpellWarmupTicks2612
				stNow.SpellCastTicks = EvokerWololoSpellCastTicks2612
				stNow.WololoCooldown = EvokerWololoSpellIntervalTicks2612
				stNow.WololoSheepID = sheepID
			}
			mgr.mu.Unlock()
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
				EncodeSpellcasterDataPacket(evokerID, celeb, IllagerSpellWololo))
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
				EncodeSoundPacket(SoundEvokerPrepareWololo, SoundSourceHostile, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
			return
		}
	}
}

func (s *Server) stepEvokerAvoidNearestPlayer2612(evokerID int32, ent *world.Entity) bool {
	dim := ent.EffectiveDimension()
	var closest *PlayerSession
	bestSq := EvokerAvoidPlayerMaxDist2612 * EvokerAvoidPlayerMaxDist2612
	for _, p := range s.onlinePlayersSnapshot() {
		if p == nil || p.IsDead() {
			continue
		}
		ps := p.Snapshot()
		if world.NormalizeDimension(ps.Dimension) != dim || ps.GameMode == GameModeCreative || ps.GameMode == GameModeSpectator {
			continue
		}
		dx, dy, dz := ps.X-ent.X, ps.Y-ent.Y, ps.Z-ent.Z
		dSq := dx*dx + dy*dy + dz*dz
		if dSq <= bestSq {
			bestSq = dSq
			closest = p
		}
	}
	if closest == nil {
		return false
	}
	ps := closest.Snapshot()
	awayX := ent.X - ps.X
	awayZ := ent.Z - ps.Z
	dist := math.Hypot(awayX, awayZ)
	if dist < 1e-4 {
		awayX, dist = -1.0, 1.0
	}
	mod := EvokerAvoidPlayerWalkMod2612
	if bestSq < EvokerAvoidSprintDistSq2612 {
		mod = EvokerAvoidPlayerSprintMod2612
	}
	step := EvokerMovementSpeed2612 * mod * 0.5
	return s.moveIllagerHorizontallyToward2612(evokerID, ent, ent.X+(awayX/dist)*4.0, ent.Z+(awayZ/dist)*4.0, step)
}

func (s *Server) performEvokerFangsSpellEntities2612(evokerID int32, ent *world.Entity, targetX, targetY, targetZ float64) []int32 {
	dim := ent.EffectiveDimension()
	cache := s.DimensionChunks(dim)
	minY := math.Min(targetY, ent.Y)
	maxY := math.Max(targetY, ent.Y) + 1.0
	dx := targetX - ent.X
	dz := targetZ - ent.Z
	dy := targetY - ent.Y
	distSq := dx*dx + dy*dy + dz*dz
	baseAngle := math.Atan2(dz, dx)

	spawnOne := func(fx, fz float64, angle float64, warmup int) int32 {
		fy, ok := ResolveEvokerFangGroundY2612(cache, fx, fz, minY, maxY)
		if !ok {
			return 0
		}
		fid, err := s.SpawnEvokerFangs(dim, fx, fy, fz, warmup, evokerID)
		if err != nil || fid == 0 {
			return 0
		}
		yawDeg := float32(angle * (180.0 / math.Pi))
		s.entities.Update(fid, func(e *world.Entity) {
			e.Yaw = yawDeg
		})
		s.EmitGameEvent(dim, GameEventEntityPlace, fx, fy, fz, VibrationSource{EntityID: evokerID})
		return fid
	}

	var fangIDs []int32
	if distSq < EvokerFangsCloseDistSq2612 {
		for i := 0; i < 5; i++ {
			ang := baseAngle + float64(i)*math.Pi*0.4
			if fid := spawnOne(ent.X+math.Cos(ang)*1.5, ent.Z+math.Sin(ang)*1.5, ang, 0); fid != 0 {
				fangIDs = append(fangIDs, fid)
			}
		}
		for i := 0; i < 8; i++ {
			ang := baseAngle + float64(i)*math.Pi*2.0/8.0 + 1.2566371
			if fid := spawnOne(ent.X+math.Cos(ang)*2.5, ent.Z+math.Sin(ang)*2.5, ang, 3); fid != 0 {
				fangIDs = append(fangIDs, fid)
			}
		}
		return fangIDs
	}

	for i := 0; i < EvokerFangsLineCount2612; i++ {
		stepDist := 1.25 * float64(i+1)
		fx := ent.X + math.Cos(baseAngle)*stepDist
		fz := ent.Z + math.Sin(baseAngle)*stepDist
		if fid := spawnOne(fx, fz, baseAngle, i); fid != 0 {
			fangIDs = append(fangIDs, fid)
		}
	}
	return fangIDs
}

func (s *Server) performEvokerSummonVexEntities2612(evokerID int32, ent *world.Entity, rng *rand.Rand) []int32 {
	dim := ent.EffectiveDimension()
	baseBX := int(math.Floor(ent.X))
	baseBY := int(math.Floor(ent.Y))
	baseBZ := int(math.Floor(ent.Z))
	var vexIDs []int32

	for i := 0; i < EvokerSummonVexCount; i++ {
		ox, oz := (i%3)-1, ((i+1)%3)-1
		lifeTicks := 20 * (VexLimitedLifeMinSec2612 + 30)
		if rng != nil {
			ox = -2 + rng.Intn(5)
			oz = -2 + rng.Intn(5)
			lifeTicks = 20 * (VexLimitedLifeMinSec2612 + rng.Intn(VexLimitedLifeRandSec2612))
		}
		bx, by, bz := baseBX+ox, baseBY+1, baseBZ+oz
		vid, err := s.SpawnVex(dim, float64(bx)+0.5, float64(by), float64(bz)+0.5, evokerID)
		if err == nil && vid != 0 {
			mgr := s.illagerManager()
			mgr.mu.Lock()
			if vs := mgr.vexes[vid]; vs != nil {
				vs.HasLimitedLife = true
				vs.LimitedLifeTicks = lifeTicks
				vs.MaxLifeTicks = lifeTicks
				vs.HasBoundOrigin = true
				vs.BoundOriginX = bx
				vs.BoundOriginY = by
				vs.BoundOriginZ = bz
			}
			mgr.mu.Unlock()
			s.EmitGameEvent(dim, GameEventEntityPlace, float64(bx)+0.5, float64(by), float64(bz)+0.5, VibrationSource{EntityID: evokerID})
			vexIDs = append(vexIDs, vid)
		}
	}
	return vexIDs
}

// ============================================================================
// SHARED ILLAGER TARGETING & COLLISION-SAFE HORIZONTAL MOVEMENT
// ============================================================================

func (s *Server) findIllagerCombatTarget2612(
	illagerID int32,
	ent *world.Entity,
	followRange float64,
	isJohnny bool,
) (bestPlayer *PlayerSession, bestMob *world.Entity, tx, ty, tz float64, ok bool) {
	dim := ent.EffectiveDimension()
	maxSq := followRange * followRange

	// Priority 2: Nearest survival/adventure player.
	bestPSq := maxSq + 1.0
	for _, p := range s.onlinePlayersSnapshot() {
		if p == nil || p.IsDead() {
			continue
		}
		ps := p.Snapshot()
		if world.NormalizeDimension(ps.Dimension) != dim || ps.GameMode == GameModeCreative || ps.GameMode == GameModeSpectator {
			continue
		}
		dx, dy, dz := ps.X-ent.X, ps.Y-ent.Y, ps.Z-ent.Z
		dSq := dx*dx + dy*dy + dz*dz
		if dSq <= maxSq && dSq < bestPSq {
			bestPSq = dSq
			bestPlayer = p
			tx, ty, tz = ps.X, ps.Y, ps.Z
		}
	}
	if bestPlayer != nil {
		return bestPlayer, nil, tx, ty, tz, true
	}

	// Priority 3: Adult Villager / Wandering Trader / Iron Golem (or Priority 4: any Johnny target).
	bestMSq := maxSq + 1.0
	allEnts := s.entities.AppendAll(nil)
	for i := range allEnts {
		other := &allEnts[i]
		if other.ID == illagerID || other.EffectiveDimension() != dim {
			continue
		}
		norm := canonicalResourceName(other.TypeName)
		valid := false
		if norm == MobTypeIronGolem || ((norm == MobTypeVillager || norm == MobTypeWanderingTrader) && !s.IsMobBaby(other.ID)) {
			valid = s.MobHealth(other.ID) > 0
		} else if isJohnny && s.IsValidJohnnyTargetEntity2612(illagerID, other) {
			valid = true
		}
		if !valid {
			continue
		}
		dx, dy, dz := other.X-ent.X, other.Y-ent.Y, other.Z-ent.Z
		dSq := dx*dx + dy*dy + dz*dz
		if dSq <= maxSq && dSq < bestMSq {
			bestMSq = dSq
			cand := *other
			bestMob = &cand
			tx, ty, tz = other.X, other.Y, other.Z
		}
	}
	if bestMob != nil {
		return nil, bestMob, tx, ty, tz, true
	}
	return nil, nil, 0, 0, 0, false
}

func (s *Server) moveIllagerHorizontallyToward2612(entityID int32, ent *world.Entity, targetX, targetZ, stepSpeed float64) bool {
	if s == nil || s.entities == nil || ent == nil || stepSpeed <= 0 {
		return false
	}
	dx := targetX - ent.X
	dz := targetZ - ent.Z
	dist := math.Hypot(dx, dz)
	if dist < 1e-4 {
		return false
	}
	step := math.Min(stepSpeed, dist)
	nx := ent.X + (dx/dist)*step
	nz := ent.Z + (dz/dist)*step
	// Respect solid blocks / closed doors at feet & head height.
	if cache := s.DimensionChunks(ent.EffectiveDimension()); cache != nil {
		bx, by, bz := int(math.Floor(nx)), int(math.Floor(ent.Y)), int(math.Floor(nz))
		for _, checkY := range []int{by, by + 1} {
			st := cache.GetBlock(bx, checkY, bz)
			if st == world.StateAir {
				continue
			}
			for _, box := range world.BlockCollisionBoxesAt(bx, checkY, bz, st) {
				if nx+0.25 > box.MinX && nx-0.25 < box.MaxX &&
					ent.Y+1.8 > box.MinY && ent.Y < box.MaxY &&
					nz+0.25 > box.MinZ && nz-0.25 < box.MaxZ {
					return false
				}
			}
		}
	}
	yaw := yawToward(dx, dz)
	s.entities.Update(entityID, func(e *world.Entity) {
		e.X = nx
		e.Z = nz
		e.Yaw = yaw
		e.HeadYaw = yaw
	})
	return true
}
