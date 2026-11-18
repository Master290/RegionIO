package server

import (
	"encoding/binary"
	"math"
	"math/rand"
	"strings"

	"regionio/internal/nbt"
	"regionio/internal/protocol"
	"regionio/internal/world"
)

// ============================================================================
// GUARDIAN, ELDER GUARDIAN, SHULKER & SHULKER BULLET 26.1.2 TICK-BY-TICK PARITY
// Verified directly against server-26.1.2.jar:
//   - Guardian, Guardian$GuardianAttackGoal, Guardian$GuardianAttackSelector,
//     Guardian$GuardianMoveControl, ElderGuardian, MobEffectUtil
//   - Shulker, Shulker$ShulkerAttackGoal, Shulker$ShulkerPeekGoal,
//     Shulker$ShulkerNearestAttackGoal, Shulker$ShulkerDefenseAttackGoal
//   - ShulkerBullet
// ============================================================================

const (
	// GuardianAttackGoalInitialTime is `GuardianAttackGoal.start()` `attackTime = -10` (`bipush -10`).
	GuardianAttackGoalInitialTime int = -10

	// GuardianMinAttackDistSq is `GuardianAttackSelector` / `GuardianAttackGoal.canContinueToUse`
	// `distanceToSqr > 9.0D` (`3.0` blocks).
	GuardianMinAttackDistSq float64 = 9.0

	// GuardianEyeHeight2612 and ElderGuardianEyeHeight2612 from `EntityType.java` in 26.1.2.jar:
	//   - GUARDIAN: `.sized(0.85F, 0.85F).eyeHeight(0.425F)`
	//   - ELDER_GUARDIAN: `.sized(1.9975F, 1.9975F).eyeHeight(0.99875F)` (`ELDER_SIZE_SCALE = 2.35F`).
	GuardianWidth2612          float64 = 0.85
	GuardianHeight2612         float64 = 0.85
	GuardianEyeHeight2612      float64 = 0.425
	ElderGuardianWidth2612     float64 = 1.9975
	ElderGuardianHeight2612    float64 = 1.9975
	ElderGuardianEyeHeight2612 float64 = 0.99875

	// GuardianLandFlopDeltaY and GuardianLandFlopHorizSpread match `Guardian.aiStep()`:
	// `deltaMovement.add((nextFloat()*2 - 1)*0.4F, 0.5D, (nextFloat()*2 - 1)*0.4F)` when `onGround()`.
	GuardianLandFlopDeltaY      float64 = 0.50
	GuardianLandFlopHorizSpread float64 = 0.40
	GuardianIdleWaterSinkDeltaY float64 = -0.005

	// EntityEventGuardianAttackSound is `level.broadcastEntityEvent(guardian, (byte)21)` when `attackTime == 0`.
	EntityEventGuardianAttackSound byte = 21

	// Additional Elder Guardian land sounds from `ElderGuardian.java` in 26.1.2.jar.
	SoundElderGuardianFlop        = "minecraft:entity.elder_guardian.flop"
	SoundElderGuardianAmbientLand = "minecraft:entity.elder_guardian.ambient_land"
	SoundElderGuardianHurtLand    = "minecraft:entity.elder_guardian.hurt_land"
	SoundElderGuardianDeathLand   = "minecraft:entity.elder_guardian.death_land"

	// ShulkerPeekStepPerTick is `Shulker.PEEK_PER_TICK = 0.05F`.
	ShulkerPeekStepPerTick float32 = 0.05

	// ShulkerAttackMaxContinueDistSq is `ShulkerAttackGoal.tick` `distanceToSqr < 400.0D` (`20.0` blocks).
	ShulkerAttackMaxContinueDistSq float64 = 400.0

	// ShulkerAttackInitialDelayTicks is `ShulkerAttackGoal.start` `attackTime = 20`.
	ShulkerAttackInitialDelayTicks int = 20

	// ShulkerBullet constants from `ShulkerBullet.java` in 26.1.2.jar:
	ShulkerBulletBaseTargetDelta float64 = 0.15  // `selectNextMoveDirection`: `0.15D`
	ShulkerBulletAccelFactor     float64 = 1.025 // `tick`: `clamp(targetDelta * 1.025D, -1.0D, 1.0D)`
	ShulkerBulletSteerLerp       float64 = 0.20  // `tick`: `deltaMovement.add((targetDelta - v) * 0.2D)`
	ShulkerBulletGravity         float64 = 0.04  // `getDefaultGravity() = 0.04D`
	ShulkerBulletWidth2612       float64 = 0.3125
	ShulkerBulletHeight2612      float64 = 0.3125
)

// Direction axis IDs (`net.minecraft.core.Direction$Axis`).
const (
	ShulkerAxisNone int8 = -1
	ShulkerAxisX    int8 = 0
	ShulkerAxisY    int8 = 1
	ShulkerAxisZ    int8 = 2
)

// shulkerDirectionAxis returns the `Direction.Axis` (`X=0, Y=1, Z=2`) for a `Direction` (`0..5`).
func shulkerDirectionAxis(dir byte) int8 {
	switch dir {
	case ShulkerFaceDown, ShulkerFaceUp:
		return ShulkerAxisY
	case ShulkerFaceNorth, ShulkerFaceSouth:
		return ShulkerAxisZ
	case ShulkerFaceWest, ShulkerFaceEast:
		return ShulkerAxisX
	default:
		return ShulkerAxisNone
	}
}

// shulkerOppositeDirection returns `dir.getOpposite()` for `Direction` (`0..5`).
func shulkerOppositeDirection(dir byte) byte {
	switch dir {
	case ShulkerFaceDown:
		return ShulkerFaceUp
	case ShulkerFaceUp:
		return ShulkerFaceDown
	case ShulkerFaceNorth:
		return ShulkerFaceSouth
	case ShulkerFaceSouth:
		return ShulkerFaceNorth
	case ShulkerFaceWest:
		return ShulkerFaceEast
	case ShulkerFaceEast:
		return ShulkerFaceWest
	default:
		return ShulkerFaceUp
	}
}

// AvoidsGuardianThorns2612 implements `Guardian.hurtServer` spike retaliation exclusion rules
// from `server-26.1.2.jar` (`DamageTypeTags.AVOIDS_GUARDIAN_THORNS` = `magic`, `thorns`,
// `#is_explosion`, plus requiring `source.getDirectEntity() instanceof LivingEntity`).
func AvoidsGuardianThorns2612(damageCause string, directEntityIsLiving bool) bool {
	if !directEntityIsLiving {
		return true
	}
	norm := strings.ToLower(strings.TrimSpace(damageCause))
	norm = strings.TrimPrefix(norm, "minecraft:")
	switch norm {
	case "magic", "thorns",
		"explosion", "explosion.player", "player_explosion", "bad_respawn_point", "fireworks",
		"arrow", "trident", "mob_projectile", "projectile", "fireball", "unattributed_fireball", "wind_charge":
		return true
	default:
		return false
	}
}

// GuardianLaserRawDamages2612 computes the raw `indirectMagic` (`f`) and `mobAttack`
// (`ATTACK_DAMAGE`) values from `Guardian$GuardianAttackGoal.tick` in `26.1.2.jar`:
//   - `f = 1.0F`; if `difficulty == HARD`, `f += 2.0F`; if `isElder`, `f += 2.0F`
//     (Guardian: `1.0` Easy/Normal, `3.0` Hard; ElderGuardian: `3.0` Easy/Normal, `5.0` Hard).
//   - `mobAttack = 6.0F` (Guardian) or `8.0F` (ElderGuardian).
func GuardianLaserRawDamages2612(isElder bool, difficulty Difficulty) (magicRaw, mobAttackRaw float32) {
	magicRaw = 1.0
	if difficulty == DifficultyHard {
		magicRaw += 2.0
	}
	if isElder {
		magicRaw += 2.0
		mobAttackRaw = ElderGuardianLaserDamage // 8.0
	} else {
		mobAttackRaw = GuardianLaserDamage // 6.0
	}
	return magicRaw, mobAttackRaw
}

// ApplyGuardianLaserToPlayer2612 applies the mixed 2-part Guardian/ElderGuardian laser hit
// (`indirectMagic` + `doHurtTarget` `mobAttack` on the same tick) with exact `Player.hurtServer`
// difficulty scaling and `LivingEntity.hurtServer` `invulnerableTime > 10` (`amount - lastHurt`)
// residual armor calculation.
func (s *Server) ApplyGuardianLaserToPlayer2612(
	guardianID int32,
	guardianType string,
	gx, gz float64,
	isElder bool,
	target *PlayerSession,
) float32 {
	if s == nil || target == nil || target.IsDead() {
		return 0
	}
	diff := s.Difficulty()
	if diff == DifficultyPeaceful {
		return 0
	}
	snap := target.Snapshot()
	if snap.GameMode == GameModeCreative || snap.GameMode == GameModeSpectator || snap.Health <= 0 {
		return 0
	}
	hpBefore := snap.Health
	magicRaw, mobRaw := GuardianLaserRawDamages2612(isElder, diff)

	// Both `indirect_magic` (`when_caused_by_living_non_player`) and `mob_attack` scale by difficulty on Players.
	scaledMagic := ScaleDamageByDifficulty(magicRaw, diff)
	scaledMob := ScaleDamageByDifficulty(mobRaw, diff)

	// 1. `target.hurtServer(serverLevel, damageSources().indirectMagic(guardian, guardian), f)` (bypasses armor!).
	if scaledMagic > 0 {
		s.DamagePlayerWithEffects(target, scaledMagic, "indirect_magic")
	}
	// 2. `guardian.doHurtTarget(serverLevel, target)` on the same tick (`invulnerableTime == 20 > 10`, `lastHurt = scaledMagic`):
	//    `actuallyHurt(mobAttack, scaledMob - scaledMagic)` (reduced by armor & toughness!).
	if !target.IsDead() && scaledMob > scaledMagic {
		residual := scaledMob - scaledMagic
		s.DamagePlayerWithArmor(target, residual, "mob")
		tSnap := target.Snapshot()
		kvx, kvy, kvz := ComputeKnockback(gx, gz, yawToward(tSnap.X-gx, tSnap.Z-gz), tSnap.X, tSnap.Z, 0.4)
		if target.send != nil {
			_ = target.send(protocol.PlaySetEntityMotion, EncodeSetEntityMotionPacket(target.EntityID, kvx, kvy, kvz))
		}
	}
	_ = guardianID
	_ = guardianType
	hpAfter := target.Snapshot().Health
	if hpBefore > hpAfter {
		return hpBefore - hpAfter
	}
	return 0
}

// EncodeGuardianElderEffectGameEvent encodes `ClientboundGameEventPacket(GUARDIAN_ELDER_EFFECT = 10, param)`
// (`param = 1.0F` when audible, `0.0F` when silent).
func EncodeGuardianElderEffectGameEvent(silent bool) []byte {
	w := protocol.NewWriter(5)
	w.Byte(GameEventGuardianElderEffect)
	if silent {
		w.Float32(0.0)
	} else {
		w.Float32(1.0)
	}
	return w.Bytes()
}

// ShulkerPhysicalPeek2612 implements `Shulker.getPhysicalPeek(float amount)`:
// `0.5F - Mth.sin((0.5F + amount) * PI) * 0.5F`.
func ShulkerPhysicalPeek2612(amount float32) float32 {
	if amount <= 0 {
		return 0
	}
	if amount >= 1.0 {
		return 1.0
	}
	return 0.5 - float32(math.Sin(float64(0.5+amount)*math.Pi))*0.5
}

func aabbExpandTowards2612(minX, minY, minZ, maxX, maxY, maxZ, dx, dy, dz float64) (float64, float64, float64, float64, float64, float64) {
	if dx < 0 {
		minX += dx
	} else if dx > 0 {
		maxX += dx
	}
	if dy < 0 {
		minY += dy
	} else if dy > 0 {
		maxY += dy
	}
	if dz < 0 {
		minZ += dz
	} else if dz > 0 {
		maxZ += dz
	}
	return minX, minY, minZ, maxX, maxY, maxZ
}

func aabbContract2612(minX, minY, minZ, maxX, maxY, maxZ, dx, dy, dz float64) (float64, float64, float64, float64, float64, float64) {
	if dx < 0 {
		minX -= dx
	} else if dx > 0 {
		maxX -= dx
	}
	if dy < 0 {
		minY -= dy
	} else if dy > 0 {
		maxY -= dy
	}
	if dz < 0 {
		minZ -= dz
	} else if dz > 0 {
		maxZ -= dz
	}
	return minX, minY, minZ, maxX, maxY, maxZ
}

// ShulkerProgressDeltaAABB2612 implements `Shulker.getProgressDeltaAabb(scale, oppositeDir, from, to, pos)`
// from `server-26.1.2.jar` (`Shulker.java` bytecode lines 2683-2790).
func ShulkerProgressDeltaAABB2612(
	scale float32,
	oppositeDir byte,
	fromPeek, toPeek float32,
	posX, posY, posZ float64,
) (minX, minY, minZ, maxX, maxY, maxZ float64) {
	if scale <= 0 {
		scale = 1.0
	}
	sc := float64(scale)
	minX, minY, minZ = -0.5*sc, 0.0, -0.5*sc
	maxX, maxY, maxZ = 0.5*sc, sc, 0.5*sc

	maxP := math.Max(float64(fromPeek), float64(toPeek))
	minP := math.Min(float64(fromPeek), float64(toPeek))
	sx, sy, sz := shulkerFaceDelta(oppositeDir)

	minX, minY, minZ, maxX, maxY, maxZ = aabbExpandTowards2612(
		minX, minY, minZ, maxX, maxY, maxZ,
		float64(sx)*maxP*sc, float64(sy)*maxP*sc, float64(sz)*maxP*sc,
	)
	minX, minY, minZ, maxX, maxY, maxZ = aabbContract2612(
		minX, minY, minZ, maxX, maxY, maxZ,
		float64(-sx)*(1.0+minP)*sc, float64(-sy)*(1.0+minP)*sc, float64(-sz)*(1.0+minP)*sc,
	)
	return minX + posX, minY + posY, minZ + posZ, maxX + posX, maxY + posY, maxZ + posZ
}

// ShulkerProgressAABB2612 implements `Shulker.getProgressAabb(scale, oppositeDir, physicalPeek, pos)`.
func ShulkerProgressAABB2612(
	scale float32,
	attachFace byte,
	physicalPeek float32,
	posX, posY, posZ float64,
) (minX, minY, minZ, maxX, maxY, maxZ float64) {
	opp := shulkerOppositeDirection(attachFace)
	return ShulkerProgressDeltaAABB2612(scale, opp, -1.0, physicalPeek, posX, posY, posZ)
}

// GetShulkerBoundingBox returns the current world-space AABB of a live `minecraft:shulker`
// interpolated by its `CurrentPeek` along `AttachFace.getOpposite()`.
func (s *Server) GetShulkerBoundingBox(entityID int32) (minX, minY, minZ, maxX, maxY, maxZ float64, ok bool) {
	if s == nil || s.entities == nil || entityID <= 0 {
		return 0, 0, 0, 0, 0, 0, false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeShulker {
		return 0, 0, 0, 0, 0, 0, false
	}
	st, exists := s.GetShulkerState(entityID)
	if !exists {
		return 0, 0, 0, 0, 0, 0, false
	}
	phys := ShulkerPhysicalPeek2612(st.CurrentPeek)
	minX, minY, minZ, maxX, maxY, maxZ = ShulkerProgressAABB2612(1.0, st.AttachFace, phys, ent.X, ent.Y, ent.Z)
	return minX, minY, minZ, maxX, maxY, maxZ, true
}

// IsShulkerClosed reports `Shulker.isClosed()` (`getRawPeekAmount() == 0`).
func (s *Server) IsShulkerClosed(entityID int32) bool {
	st, ok := s.GetShulkerState(entityID)
	return ok && st.Peek == 0
}

// ShulkerArmorValue returns the Shulker's `Attributes.ARMOR` value from `Shulker.setRawPeekAmount`:
// `+20.0` (`COVERED_ARMOR_MODIFIER`, `ADD_VALUE`) when closed (`Peek == 0`), `0.0` when `Peek > 0`.
func (s *Server) ShulkerArmorValue(entityID int32) float32 {
	if s.IsShulkerClosed(entityID) {
		return float32(ShulkerClosedArmorBonus)
	}
	return 0.0
}

// stepShulkerPeekInterpolation implements `Shulker.updatePeekAmount()` + `Shulker.onPeekAmountChange()`:
// advances `CurrentPeek` toward `float32(Peek)*0.01` by `0.05` per tick and pushes any non-Spectator,
// non-Shulker entities intersecting the expanding lid delta AABB along `AttachFace.getOpposite()`.
func (s *Server) stepShulkerPeekInterpolation(entityID int32, ent *world.Entity) {
	if s == nil || ent == nil {
		return
	}
	sm := s.shulkerMgr()
	sm.mu.Lock()
	st := sm.shulkers[entityID]
	if st == nil {
		sm.mu.Unlock()
		return
	}
	st.PrevPeek = st.CurrentPeek
	target := float32(st.Peek) * 0.01
	if st.CurrentPeek == target {
		sm.mu.Unlock()
		return
	}
	if st.CurrentPeek > target {
		next := st.CurrentPeek - ShulkerPeekStepPerTick
		if next < target {
			next = target
		}
		if next > 1.0 {
			next = 1.0
		}
		st.CurrentPeek = next
	} else {
		next := st.CurrentPeek + ShulkerPeekStepPerTick
		if next < 0.0 {
			next = 0.0
		}
		if next > target {
			next = target
		}
		st.CurrentPeek = next
	}
	prevP := st.PrevPeek
	curP := st.CurrentPeek
	face := st.AttachFace
	sm.mu.Unlock()

	physCur := ShulkerPhysicalPeek2612(curP)
	physPrev := ShulkerPhysicalPeek2612(prevP)
	delta := float64(physCur - physPrev)
	if delta <= 0 {
		return
	}
	opp := shulkerOppositeDirection(face)
	ox, oy, oz := shulkerFaceDelta(opp)
	pushX, pushY, pushZ := float64(ox)*delta, float64(oy)*delta, float64(oz)*delta
	dMinX, dMinY, dMinZ, dMaxX, dMaxY, dMaxZ := ShulkerProgressDeltaAABB2612(1.0, opp, physPrev, physCur, ent.X, ent.Y, ent.Z)
	const eps = 1e-4

	dim := ent.EffectiveDimension()
	for _, p := range s.onlinePlayersSnapshot() {
		if p == nil || p.IsDead() {
			continue
		}
		pSnap := p.Snapshot()
		if world.NormalizeDimension(pSnap.Dimension) != dim || pSnap.GameMode == GameModeSpectator {
			continue
		}
		pMinX, pMaxX := pSnap.X-0.3, pSnap.X+0.3
		pMinY, pMaxY := pSnap.Y, pSnap.Y+1.8
		pMinZ, pMaxZ := pSnap.Z-0.3, pSnap.Z+0.3
		if pMaxX >= dMinX-eps && pMinX <= dMaxX+eps &&
			pMaxY >= dMinY-eps && pMinY <= dMaxY+eps &&
			pMaxZ >= dMinZ-eps && pMinZ <= dMaxZ+eps {
			s.SetPlayerTransform(p, pSnap.X+pushX, pSnap.Y+pushY, pSnap.Z+pushZ, pSnap.Yaw, pSnap.Pitch, pSnap.OnGround)
		}
	}

	allEnts := s.entities.AppendAll(nil)
	for i := range allEnts {
		other := &allEnts[i]
		if other.ID == entityID || other.EffectiveDimension() != dim || canonicalResourceName(other.TypeName) == MobTypeShulker {
			continue
		}
		w, h := shulkerEntityBoxDimensions2612(other.TypeName)
		hw := w * 0.5
		eMinX, eMaxX := other.X-hw, other.X+hw
		eMinY, eMaxY := other.Y, other.Y+h
		eMinZ, eMaxZ := other.Z-hw, other.Z+hw
		if eMaxX >= dMinX-eps && eMinX <= dMaxX+eps &&
			eMaxY >= dMinY-eps && eMinY <= dMaxY+eps &&
			eMaxZ >= dMinZ-eps && eMinZ <= dMaxZ+eps {
			s.entities.Update(other.ID, func(e *world.Entity) {
				e.X += pushX
				e.Y += pushY
				e.Z += pushZ
			})
		}
	}
}

func isFluidProjectilePassable2612(state uint16) bool {
	if state == world.StateWater {
		return true
	}
	switch canonicalResourceName(world.BlockName(state)) {
	case "minecraft:water", "minecraft:lava", "minecraft:bubble_column":
		return true
	default:
		return false
	}
}

// isShulkerBlockEmptyAir2612 checks `BlockState.isAir()` for `Shulker.isPositionBlocked`
// and `Shulker.teleportSomewhere` (strictly air — water, lava, plants, etc. are NOT air!).
func isShulkerBlockEmptyAir2612(state uint16) bool {
	if state == world.StateAir {
		return true
	}
	name := canonicalResourceName(world.BlockName(state))
	return name == "minecraft:air" || name == "minecraft:cave_air" || name == "minecraft:void_air"
}

// isSturdyShulkerSupportFace2612 checks `loadedAndEntityCanStandOnFace(anchorPos, shulker, attachFace.getOpposite())`.
func isSturdyShulkerSupportFace2612(anchorState uint16, supportSurfaceDir byte) bool {
	if anchorState == world.StateAir || anchorState == world.StateWater || anchorState == world.StateLava || isPassableBlock(anchorState) {
		return false
	}
	name, props, hasProps := world.BlockStateProperties(anchorState)
	if !hasProps {
		name = world.BlockName(anchorState)
	}
	norm := canonicalResourceName(name)
	if norm == "minecraft:water" || norm == "minecraft:lava" || norm == "minecraft:bubble_column" ||
		norm == "minecraft:moving_piston" || norm == "minecraft:piston_head" ||
		norm == "minecraft:cactus" || norm == "minecraft:bamboo" || norm == "minecraft:chorus_plant" ||
		norm == "minecraft:chorus_flower" || norm == "minecraft:end_rod" || norm == "minecraft:chain" ||
		norm == "minecraft:iron_bars" || norm == "minecraft:bell" || norm == "minecraft:lantern" ||
		norm == "minecraft:soul_lantern" || norm == "minecraft:pointed_dripstone" ||
		norm == "minecraft:scaffolding" || norm == "minecraft:COBWEB" || norm == "minecraft:powder_snow" ||
		strings.HasSuffix(norm, "shulker_box") || strings.HasSuffix(norm, "_fence") ||
		strings.HasSuffix(norm, "_fence_gate") || strings.HasSuffix(norm, "_wall") ||
		strings.HasSuffix(norm, "_pane") || strings.HasSuffix(norm, "_leaves") ||
		strings.HasSuffix(norm, "_door") || strings.HasSuffix(norm, "_trapdoor") ||
		strings.HasSuffix(norm, "_chest") || strings.HasSuffix(norm, "_bed") ||
		strings.HasSuffix(norm, "_amethyst_bud") || norm == "minecraft:amethyst_cluster" {
		return false
	}
	if strings.HasSuffix(norm, "_slab") && hasProps {
		switch props["type"] {
		case "double":
			return true
		case "top":
			return supportSurfaceDir == ShulkerFaceUp
		case "bottom":
			return supportSurfaceDir == ShulkerFaceDown
		default:
			return false
		}
	}
	if strings.HasSuffix(norm, "_stairs") && hasProps {
		if supportSurfaceDir == ShulkerFaceUp {
			return props["half"] == "top"
		}
		if supportSurfaceDir == ShulkerFaceDown {
			return props["half"] == "bottom"
		}
		return false
	}
	return true
}

func shulkerEntityBoxDimensions2612(typeName string) (width, height float64) {
	switch canonicalResourceName(typeName) {
	case MobTypeGuardian:
		return GuardianWidth2612, GuardianHeight2612
	case MobTypeElderGuardian:
		return ElderGuardianWidth2612, ElderGuardianHeight2612
	case MobTypeShulker:
		return 1.0, 1.0
	case EntityTypeShulkerBullet:
		return ShulkerBulletWidth2612, ShulkerBulletHeight2612
	case MobTypeEnderman:
		return 0.6, EndermanHeight
	default:
		return 0.6, 1.8
	}
}

// shulkerTargetWithinSearchArea2612 implements `ShulkerNearestAttackGoal.getTargetSearchArea(followRange)`:
//   - Axis.X (`WEST` / `EAST`): `inflate(4.0, followRange, followRange)`
//   - Axis.Z (`NORTH` / `SOUTH`): `inflate(followRange, followRange, 4.0)`
//   - Axis.Y (`DOWN` / `UP`): `inflate(followRange, 4.0, followRange)`.
func shulkerTargetWithinSearchArea2612(sx, sy, sz float64, attachFace byte, followRange, tx, ty, tz float64) bool {
	if followRange <= 0 {
		followRange = ShulkerAggroRange
	}
	infX, infY, infZ := followRange, 4.0, followRange
	switch shulkerDirectionAxis(attachFace) {
	case ShulkerAxisX:
		infX, infY, infZ = 4.0, followRange, followRange
	case ShulkerAxisZ:
		infX, infY, infZ = followRange, followRange, 4.0
	}
	return tx >= sx-0.5-infX && tx <= sx+0.5+infX &&
		ty+1.8 >= sy-infY && ty <= sy+1.0+infY &&
		tz >= sz-0.5-infZ && tz <= sz+0.5+infZ
}

// SelectShulkerBulletNextMoveDirection2612 implements `ShulkerBullet.selectNextMoveDirection(Axis, Entity)`
// from `server-26.1.2.jar` (`ShulkerBullet.java` bytecode lines 3888-4183):
//   - When `dist >= 2.0` from target block center, selects ONLY among open axis-aligned `Direction`s
//     whose axis != `currentAxis` (strictly forbidding 180-degree `axis.getOpposite()` reversals!).
//   - Sets `(targetDeltaX, targetDeltaY, targetDeltaZ) = normalize(aim - pos) * 0.15D`.
//   - Sets `flightSteps = 10 + random.nextInt(5) * 10` (`10..50` ticks).
func SelectShulkerBulletNextMoveDirection2612(
	cache *world.Cache,
	rng *rand.Rand,
	bx, by, bz float64,
	currentAxis int8,
	hasTarget bool,
	targetX, targetY, targetZ, targetHeight float64,
) (moveDir int8, txd, tyd, tzd float64, flightSteps int) {
	yOffset := 0.5
	var tbx, tby, tbz int
	if !hasTarget {
		tbx = int(math.Floor(bx))
		tby = int(math.Floor(by)) - 1
		tbz = int(math.Floor(bz))
	} else {
		if targetHeight > 0 {
			yOffset = targetHeight * 0.5
		}
		tbx = int(math.Floor(targetX))
		tby = int(math.Floor(targetY + yOffset))
		tbz = int(math.Floor(targetZ))
	}
	aimX := float64(tbx) + 0.5
	aimY := float64(tby) + yOffset
	aimZ := float64(tbz) + 0.5

	cdx := (float64(tbx) + 0.5) - bx
	cdy := (float64(tby) + 0.5) - by
	cdz := (float64(tbz) + 0.5) - bz
	closerThan2 := (cdx*cdx + cdy*cdy + cdz*cdz) < 4.0

	moveDir = -1
	isEmptyAt := func(x, y, z int) bool {
		if cache == nil {
			return true
		}
		if y < world.MinY || y >= world.MinY+world.WorldHeight {
			return false
		}
		return isShulkerBlockEmptyAir2612(cache.GetBlock(x, y, z))
	}

	if !closerThan2 {
		curBX := int(math.Floor(bx))
		curBY := int(math.Floor(by))
		curBZ := int(math.Floor(bz))
		var candidates [3]byte
		candLen := 0

		if currentAxis != ShulkerAxisX {
			if curBX < tbx && isEmptyAt(curBX+1, curBY, curBZ) {
				candidates[candLen] = ShulkerFaceEast
				candLen++
			} else if curBX > tbx && isEmptyAt(curBX-1, curBY, curBZ) {
				candidates[candLen] = ShulkerFaceWest
				candLen++
			}
		}
		if currentAxis != ShulkerAxisY {
			if curBY < tby && isEmptyAt(curBX, curBY+1, curBZ) {
				candidates[candLen] = ShulkerFaceUp
				candLen++
			} else if curBY > tby && isEmptyAt(curBX, curBY-1, curBZ) {
				candidates[candLen] = ShulkerFaceDown
				candLen++
			}
		}
		if currentAxis != ShulkerAxisZ {
			if curBZ < tbz && isEmptyAt(curBX, curBY, curBZ+1) {
				candidates[candLen] = ShulkerFaceSouth
				candLen++
			} else if curBZ > tbz && isEmptyAt(curBX, curBY, curBZ-1) {
				candidates[candLen] = ShulkerFaceNorth
				candLen++
			}
		}

		var chosen byte
		if candLen > 0 {
			idx := 0
			if rng != nil && candLen > 1 {
				idx = rng.Intn(candLen)
			}
			chosen = candidates[idx]
		} else {
			if rng != nil {
				chosen = byte(rng.Intn(6))
			} else {
				chosen = ShulkerFaceUp
			}
			for tries := 5; tries > 0; tries-- {
				sdx, sdy, sdz := shulkerFaceDelta(chosen)
				if isEmptyAt(curBX+sdx, curBY+sdy, curBZ+sdz) {
					break
				}
				if rng != nil {
					chosen = byte(rng.Intn(6))
				} else {
					chosen = (chosen + 1) % 6
				}
			}
		}
		moveDir = int8(chosen)
		sdx, sdy, sdz := shulkerFaceDelta(chosen)
		aimX = bx + float64(sdx)
		aimY = by + float64(sdy)
		aimZ = bz + float64(sdz)
	}

	ddx := aimX - bx
	ddy := aimY - by
	ddz := aimZ - bz
	dist := math.Sqrt(ddx*ddx + ddy*ddy + ddz*ddz)
	if dist > 0 {
		txd = (ddx / dist) * ShulkerBulletBaseTargetDelta
		tyd = (ddy / dist) * ShulkerBulletBaseTargetDelta
		tzd = (ddz / dist) * ShulkerBulletBaseTargetDelta
	}

	flightSteps = 10
	if rng != nil {
		flightSteps = 10 + rng.Intn(5)*10
	}
	return moveDir, txd, tyd, tzd, flightSteps
}

// ============================================================================
// SHULKER & SHULKER BULLET NBT SERIALIZATION (26.1.2.jar ValueInput/ValueOutput)
// ============================================================================

// EncodeShulkerEntityNBT serializes a `ShulkerState` into an exact vanilla 26.1.2
// entity `*nbt.Compound` (`"AttachFace"` byte `0..5`, `"Peek"` byte `0..100`, `"Color"` byte `0..16`).
func EncodeShulkerEntityNBT(st ShulkerState) *nbt.Compound {
	c := nbt.NewCompound()
	c.Set("id", nbt.String(MobTypeShulker))
	face := st.AttachFace
	if face > 5 {
		face = ShulkerFaceDown
	}
	color := st.Color
	if color > ShulkerDefaultColor {
		color = ShulkerDefaultColor
	}
	c.Set("AttachFace", nbt.Byte(int8(face)))
	c.Set("Peek", nbt.Byte(int8(st.Peek)))
	c.Set("Color", nbt.Byte(int8(color)))
	if st.PersistenceRequired {
		c.Set("PersistenceRequired", nbt.Byte(1))
	}
	return c
}

// DecodeShulkerEntityNBT parses `"AttachFace"`, `"Peek"`, and `"Color"` from a vanilla
// 26.1.2 Shulker `*nbt.Compound` (defaulting to `AttachFace=0`, `Peek=0`, `Color=16`).
func DecodeShulkerEntityNBT(c *nbt.Compound) (attachFace, peek, color byte, persistenceRequired, ok bool) {
	if c == nil {
		return ShulkerFaceDown, ShulkerPeekClosed, ShulkerDefaultColor, false, false
	}
	attachFace = ShulkerFaceDown
	if v, has := c.Get("AttachFace"); has {
		if b, isB := v.(nbt.Byte); isB && b >= 0 && b <= 5 {
			attachFace = byte(b)
		}
	}
	peek = ShulkerPeekClosed
	if v, has := c.Get("Peek"); has {
		if b, isB := v.(nbt.Byte); isB {
			if b < 0 {
				peek = 0
			} else if b > 100 {
				peek = 100
			} else {
				peek = byte(b)
			}
		}
	}
	color = ShulkerDefaultColor
	if v, has := c.Get("Color"); has {
		if b, isB := v.(nbt.Byte); isB && b >= 0 && b <= nbt.Byte(ShulkerDefaultColor) {
			color = byte(b)
		}
	}
	if v, has := c.Get("PersistenceRequired"); has {
		if b, isB := v.(nbt.Byte); isB && b != 0 {
			persistenceRequired = true
		}
	}
	return attachFace, peek, color, persistenceRequired, true
}

// SaveShulkerEntityNBT exports the live Shulker entity's `AttachFace`, `Peek`, and `Color` to NBT.
func (s *Server) SaveShulkerEntityNBT(entityID int32) (*nbt.Compound, bool) {
	st, ok := s.GetShulkerState(entityID)
	if !ok {
		return nil, false
	}
	return EncodeShulkerEntityNBT(st), true
}

// LoadShulkerEntityNBT applies `AttachFace`, `Peek`, and `Color` from `c` onto a live Shulker entity,
// updating its AABB interpolation, `+20` closed-shell armor state, and `SynchedEntityData` packet.
func (s *Server) LoadShulkerEntityNBT(entityID int32, c *nbt.Compound) bool {
	if s == nil || s.entities == nil || entityID <= 0 {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeShulker {
		return false
	}
	face, peek, color, persist, ok := DecodeShulkerEntityNBT(c)
	if !ok {
		return false
	}
	bx, by, bz := int(math.Floor(ent.X)), int(math.Floor(ent.Y)), int(math.Floor(ent.Z))
	dx, dy, dz := shulkerFaceDelta(face)

	st := s.ensureShulkerState(entityID)
	sm := s.shulkerMgr()
	sm.mu.Lock()
	st.AttachFace = face
	st.Peek = peek
	st.CurrentPeek = float32(peek) * 0.01
	st.PrevPeek = st.CurrentPeek
	st.Color = color
	st.AnchorX = bx + dx
	st.AnchorY = by + dy
	st.AnchorZ = bz + dz
	if persist {
		st.PersistenceRequired = true
	}
	sm.mu.Unlock()

	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, PlaySetEntityDataCB,
		EncodeShulkerEntityDataPacket(entityID, face, peek, color))
	return true
}

func uuidBytesToIntArray2612(u [16]byte) nbt.IntArray {
	return nbt.IntArray{
		int32(binary.BigEndian.Uint32(u[0:4])),
		int32(binary.BigEndian.Uint32(u[4:8])),
		int32(binary.BigEndian.Uint32(u[8:12])),
		int32(binary.BigEndian.Uint32(u[12:16])),
	}
}

func intArrayToUUIDBytes2612(arr nbt.IntArray) ([16]byte, bool) {
	var out [16]byte
	if len(arr) != 4 {
		return out, false
	}
	binary.BigEndian.PutUint32(out[0:4], uint32(arr[0]))
	binary.BigEndian.PutUint32(out[4:8], uint32(arr[1]))
	binary.BigEndian.PutUint32(out[8:12], uint32(arr[2]))
	binary.BigEndian.PutUint32(out[12:16], uint32(arr[3]))
	return out, true
}

// EncodeShulkerBulletEntityNBT serializes a `ShulkerBulletState` into an exact vanilla 26.1.2
// `*nbt.Compound` (`"Steps"`, `"TXD"`, `"TYD"`, `"TZD"`, `"Dir"`, `"Target"`).
func EncodeShulkerBulletEntityNBT(b ShulkerBulletState) *nbt.Compound {
	c := nbt.NewCompound()
	c.Set("id", nbt.String(EntityTypeShulkerBullet))
	c.Set("Steps", nbt.Int(int32(b.FlightSteps)))
	c.Set("TXD", nbt.Double(b.TargetDeltaX))
	c.Set("TYD", nbt.Double(b.TargetDeltaY))
	c.Set("TZD", nbt.Double(b.TargetDeltaZ))
	if b.MoveDir >= 0 && b.MoveDir <= 5 {
		c.Set("Dir", nbt.Byte(b.MoveDir))
	}
	if b.HasTargetUUID {
		c.Set("Target", uuidBytesToIntArray2612(b.TargetUUID))
	}
	return c
}

// DecodeShulkerBulletEntityNBT parses `"Steps"`, `"TXD"`, `"TYD"`, `"TZD"`, `"Dir"`, `"Target"`
// from a vanilla 26.1.2 `ShulkerBullet` `*nbt.Compound`.
func DecodeShulkerBulletEntityNBT(c *nbt.Compound) (ShulkerBulletState, bool) {
	if c == nil {
		return ShulkerBulletState{}, false
	}
	out := ShulkerBulletState{MoveDir: -1}
	if v, has := c.Get("Steps"); has {
		if iv, ok := v.(nbt.Int); ok {
			out.FlightSteps = int(iv)
		}
	}
	if v, has := c.Get("TXD"); has {
		if dv, ok := v.(nbt.Double); ok {
			out.TargetDeltaX = float64(dv)
		}
	}
	if v, has := c.Get("TYD"); has {
		if dv, ok := v.(nbt.Double); ok {
			out.TargetDeltaY = float64(dv)
		}
	}
	if v, has := c.Get("TZD"); has {
		if dv, ok := v.(nbt.Double); ok {
			out.TargetDeltaZ = float64(dv)
		}
	}
	if v, has := c.Get("Dir"); has {
		if bv, ok := v.(nbt.Byte); ok && bv >= 0 && bv <= 5 {
			out.MoveDir = int8(bv)
		}
	}
	if v, has := c.Get("Target"); has {
		if ia, ok := v.(nbt.IntArray); ok {
			if u, uok := intArrayToUUIDBytes2612(ia); uok {
				out.TargetUUID = u
				out.HasTargetUUID = true
			}
		}
	}
	return out, true
}

// SaveShulkerBulletEntityNBT exports a live `minecraft:shulker_bullet` state to NBT.
func (s *Server) SaveShulkerBulletEntityNBT(bulletID int32) (*nbt.Compound, bool) {
	if s == nil || bulletID <= 0 {
		return nil, false
	}
	sm := s.shulkerMgr()
	sm.mu.RLock()
	b, ok := sm.bullets[bulletID]
	var snap ShulkerBulletState
	if ok && b != nil {
		snap = *b
	}
	sm.mu.RUnlock()
	if !ok || b == nil {
		return nil, false
	}
	return EncodeShulkerBulletEntityNBT(snap), true
}

// LoadShulkerBulletEntityNBT restores a live `minecraft:shulker_bullet` state from NBT.
func (s *Server) LoadShulkerBulletEntityNBT(bulletID int32, c *nbt.Compound) bool {
	if s == nil || s.entities == nil || bulletID <= 0 {
		return false
	}
	ent := s.entities.Get(bulletID)
	if ent == nil || canonicalResourceName(ent.TypeName) != EntityTypeShulkerBullet {
		return false
	}
	parsed, ok := DecodeShulkerBulletEntityNBT(c)
	if !ok {
		return false
	}
	sm := s.shulkerMgr()
	sm.mu.Lock()
	cur := sm.bullets[bulletID]
	if cur == nil {
		cur = &ShulkerBulletState{
			EntityID:  bulletID,
			Dimension: ent.EffectiveDimension(),
		}
		sm.bullets[bulletID] = cur
	}
	cur.FlightSteps = parsed.FlightSteps
	cur.TargetDeltaX = parsed.TargetDeltaX
	cur.TargetDeltaY = parsed.TargetDeltaY
	cur.TargetDeltaZ = parsed.TargetDeltaZ
	cur.MoveDir = parsed.MoveDir
	cur.TargetUUID = parsed.TargetUUID
	cur.HasTargetUUID = parsed.HasTargetUUID
	if cur.MoveDir >= 0 && cur.MoveDir <= 5 {
		sdx, sdy, sdz := shulkerFaceDelta(byte(cur.MoveDir))
		cur.StepDirX, cur.StepDirY, cur.StepDirZ = float64(sdx), float64(sdy), float64(sdz)
	}
	sm.mu.Unlock()
	return true
}
