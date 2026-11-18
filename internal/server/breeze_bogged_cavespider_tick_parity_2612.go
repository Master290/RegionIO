package server

import (
	"math"
	"math/rand"
	"strings"
	"time"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

// ============================================================================
// BREEZE, WIND CHARGE, BOGGED & CAVE SPIDER 26.1.2 TICK-BY-TICK PARITY
// Verified directly against server-26.1.2.jar:
//   - Breeze, BreezeAi, BreezeAi$SlideToTargetSink, BreezeUtil, Shoot,
//     ShootWhenStuck, Slide, LongJump, LongJumpUtil
//   - AbstractWindCharge, BreezeWindCharge, WindCharge, ServerExplosion,
//     SimpleExplosionDamageCalculator, BlockSetType, DoorBlock, TrapDoorBlock,
//     FenceGateBlock, ButtonBlock, LeverBlock, BellBlock, AbstractCandleBlock
//   - Bogged, AbstractSkeleton, RangedBowAttackGoal, Arrow
//   - CaveSpider, Spider
// ============================================================================

const (
	// BreezeShootMaxRangeSq is Shoot.ATTACK_RANGE_MAX_SQRT^2 = 16^2 = 256.0
	// (Shoot.isTargetWithinRange uses strict `< 256.0D`).
	BreezeShootMaxRangeSq float64 = 256.0

	// BreezeTargetRememberTicks is BreezeAi.TICKS_TO_REMEMBER_SEEN_TARGET = 100.
	BreezeTargetRememberTicks int = 100

	// BreezeSlideShootMemoryExpiryTicks is SlideToTargetSink.stop BREEZE_SHOOT expiry = 60L.
	BreezeSlideShootMemoryExpiryTicks int = 60

	// BreezeLongJumpMaxDurationTicks is LongJump.<init> maxDuration = 200.
	BreezeLongJumpMaxDurationTicks int = 200

	// BreezeShootMaxDurationTicks is Shoot.<init> maxDuration = 15 + 1 + 4 = 20.
	BreezeShootMaxDurationTicks int = BreezeInhaleDurationTicks + 1 + BreezeShootRecoverTicks

	// BreezeSlideMinDurationTicks and BreezeSlideMaxDurationTicks match
	// `new BreezeAi.SlideToTargetSink(20, 40)` in BreezeAi.initIdleActivity.
	BreezeSlideMinDurationTicks int = 20
	BreezeSlideMaxDurationTicks int = 40

	// BreezeGravity is Mob.getGravity() = Attributes.GRAVITY default 0.08D.
	BreezeGravity float64 = 0.08

	// LongJumpUtilPullBackDist is the 0.5D horizontal pull-back in LongJumpUtil.calculateJumpVectorForAngle.
	LongJumpUtilPullBackDist float64 = 0.5

	// LongJumpUtilVelocityScale is the 0.95F (0.949999988079071D) scale in LongJumpUtil.calculateJumpVectorForAngle.
	LongJumpUtilVelocityScale float64 = 0.949999988079071

	// WindChargeKnockbackMultiplier is WindCharge.EXPLOSION_DAMAGE_CALCULATOR knockbackMultiplier = 1.22F.
	WindChargeKnockbackMultiplier float64 = 1.22

	// BreezeWindChargeKnockbackMultiplier is AbstractWindCharge.EXPLOSION_DAMAGE_CALCULATOR default = 1.0F.
	BreezeWindChargeKnockbackMultiplier float64 = 1.0

	// WindChargeNoDeflectTicks is WindCharge.noDeflectTicks = 5.
	WindChargeNoDeflectTicks int = 5

	// WindChargeBlockHitNormalOffset is the 0.25D hit-normal offset in AbstractWindCharge.onHitBlock.
	WindChargeBlockHitNormalOffset float64 = 0.25

	// CaveSpider hitbox constants from EntityType.CAVE_SPIDER in 26.1.2.jar:
	// `.sized(0.7F, 0.5F).eyeHeight(0.45F)`.
	CaveSpiderWidth     float64 = 0.7
	CaveSpiderHeight    float64 = 0.5
	CaveSpiderEyeHeight float64 = 0.45

	// Spider hitbox constants from EntityType.SPIDER in 26.1.2.jar:
	// `.sized(1.4F, 0.9F).eyeHeight(0.65F)`.
	SpiderWidth     float64 = 1.4
	SpiderHeight    float64 = 0.9
	SpiderEyeHeight float64 = 0.65
)

// IsBlockDangerousForBreeze2612 implements `EntityType.BREEZE.isBlockDangerous(BlockState)`
// from `26.1.2.jar` (`NodeEvaluator.isBurningBlock(state)` + `wither_rose`, `sweet_berry_bush`,
// `cactus`, `powder_snow`).
func IsBlockDangerousForBreeze2612(state uint16) bool {
	if state == world.StateAir {
		return false
	}
	if state == world.StateLava || state == world.StatePowderSnow {
		return true
	}
	name, props, ok := world.BlockStateProperties(state)
	if !ok {
		name = world.BlockName(state)
	}
	norm := canonicalResourceName(name)
	switch norm {
	case "minecraft:lava", "minecraft:fire", "minecraft:soul_fire", "minecraft:magma_block",
		"minecraft:wither_rose", "minecraft:sweet_berry_bush", "minecraft:cactus", "minecraft:powder_snow":
		return true
	case "minecraft:campfire", "minecraft:soul_campfire":
		return props == nil || props["lit"] != "false"
	default:
		return false
	}
}

func isWaterOrBubbleBlockState2612(state uint16) bool {
	if state == world.StateWater {
		return true
	}
	name, props, ok := world.BlockStateProperties(state)
	if !ok {
		name = world.BlockName(state)
	}
	norm := canonicalResourceName(name)
	if norm == "minecraft:water" || norm == "minecraft:bubble_column" {
		return true
	}
	return ok && props["waterlogged"] == "true"
}

func isColliderBlockState2612(state uint16) bool {
	if state == world.StateAir || state == world.StateWater || state == world.StateLava || state == world.StatePowderSnow {
		return false
	}
	if isPassableBlock(state) {
		return false
	}
	name := canonicalResourceName(world.BlockName(state))
	switch name {
	case "minecraft:air", "minecraft:cave_air", "minecraft:void_air",
		"minecraft:water", "minecraft:lava", "minecraft:bubble_column",
		"minecraft:fire", "minecraft:soul_fire", "minecraft:cobweb",
		"minecraft:sweet_berry_bush", "minecraft:wither_rose", "minecraft:tripwire",
		"minecraft:light", "minecraft:structure_void":
		return false
	}
	if strings.HasSuffix(name, "_button") || strings.HasSuffix(name, "_pressure_plate") ||
		strings.HasSuffix(name, "_banner") || strings.HasSuffix(name, "_sign") ||
		name == "minecraft:lever" || name == "minecraft:torch" || name == "minecraft:wall_torch" ||
		name == "minecraft:soul_torch" || name == "minecraft:soul_wall_torch" ||
		name == "minecraft:redstone_torch" || name == "minecraft:redstone_wall_torch" ||
		name == "minecraft:redstone_wire" {
		return false
	}
	if strings.HasSuffix(name, "_door") || strings.HasSuffix(name, "_fence_gate") {
		if _, props, ok := world.BlockStateProperties(state); ok && props["open"] == "true" {
			return false
		}
	}
	return true
}

// BreezeHasLineOfSight2612 implements `BreezeUtil.hasLineOfSight(Breeze, Vec3)`
// from `26.1.2.jar` (`maxRange = max(50.0, followRange)`, `ClipContext.Block.COLLIDER`, `Fluid.NONE`).
func BreezeHasLineOfSight2612(cache *world.Cache, bx, by, bz, tx, ty, tz, followRange float64) bool {
	maxRange := 50.0
	if followRange > maxRange {
		maxRange = followRange
	}
	dx := tx - bx
	dy := ty - by
	dz := tz - bz
	dist := math.Sqrt(dx*dx + dy*dy + dz*dz)
	if dist > maxRange {
		return false
	}
	if cache == nil || dist < 1e-5 {
		return true
	}
	steps := int(math.Ceil(dist / 0.25))
	startCell := [3]int{int(math.Floor(bx)), int(math.Floor(by)), int(math.Floor(bz))}
	for i := 1; i <= steps; i++ {
		t := float64(i) / float64(steps)
		px := bx + dx*t
		py := by + dy*t
		pz := bz + dz*t
		ix, iy, iz := int(math.Floor(px)), int(math.Floor(py)), int(math.Floor(pz))
		if [3]int{ix, iy, iz} == startCell {
			continue
		}
		if iy < world.MinY || iy >= world.MinY+world.WorldHeight {
			return false
		}
		if isColliderBlockState2612(cache.GetBlock(ix, iy, iz)) {
			return false
		}
	}
	return true
}

// SnapBreezeJumpTargetToSurface2612 implements `LongJump.snapToSurface(LivingEntity, Vec3)`:
// raycasts `10.0` blocks DOWN (then `10.0` blocks UP if DOWN misses) for `ClipContext.Block.COLLIDER`
// (`Fluid.NONE`), returning `hitBlockPos.above()`.
func SnapBreezeJumpTargetToSurface2612(cache *world.Cache, x, y, z float64) (bx, by, bz int, ok bool) {
	ix := int(math.Floor(x))
	iz := int(math.Floor(z))
	startY := int(math.Floor(y))
	if cache == nil {
		return ix, startY, iz, true
	}
	// 1. Raycast 10 blocks DOWN for first collider block.
	for dy := 0; dy <= 10; dy++ {
		cy := startY - dy
		if cy < world.MinY || cy >= world.MinY+world.WorldHeight-1 {
			continue
		}
		if isColliderBlockState2612(cache.GetBlock(ix, cy, iz)) {
			return ix, cy + 1, iz, true
		}
	}
	// 2. Fallback: raycast 10 blocks UP for first collider block.
	for dy := 1; dy <= 10; dy++ {
		cy := startY + dy
		if cy < world.MinY || cy >= world.MinY+world.WorldHeight-1 {
			continue
		}
		if isColliderBlockState2612(cache.GetBlock(ix, cy, iz)) {
			return ix, cy + 1, iz, true
		}
	}
	return 0, 0, 0, false
}

// BreezeRandomPointBehindTarget2612 implements `BreezeUtil.randomPointBehindTarget(LivingEntity, RandomSource)`:
// `yaw = targetHeadYaw + 180 + gaussian * 45`, `dist = lerp(nextFloat(), 4.0, 8.0)`.
func BreezeRandomPointBehindTarget2612(targetX, targetY, targetZ float64, targetHeadYaw float32, rng *rand.Rand) (px, py, pz float64) {
	gauss := 0.0
	u := 0.5
	if rng != nil {
		gauss = rng.NormFloat64()
		u = rng.Float64()
	}
	yawDeg := float64(targetHeadYaw) + 180.0 + gauss*45.0
	dist := 4.0 + u*4.0 // lerp(u, 4.0, 8.0)
	rad := yawDeg * (math.Pi / 180.0)
	dirX := -math.Sin(rad)
	dirZ := math.Cos(rad)
	return targetX + dirX*dist, targetY, targetZ + dirZ*dist
}

// BreezeRandomPointInMiddleCircle2612 implements `Slide.randomPointInMiddleCircle(Breeze, LivingEntity)`:
// `vec = target.pos - breeze.pos`, `d = vec.length() - lerp(nextDouble(), 8.0, 4.0)`, `breeze.pos + normalize(vec) * d`.
func BreezeRandomPointInMiddleCircle2612(breezeX, breezeY, breezeZ, targetX, targetY, targetZ float64, rng *rand.Rand) (px, py, pz float64) {
	dx := targetX - breezeX
	dy := targetY - breezeY
	dz := targetZ - breezeZ
	length := math.Sqrt(dx*dx + dy*dy + dz*dz)
	if length < 1e-6 {
		return breezeX + 6.0, breezeY, breezeZ
	}
	u := 0.5
	if rng != nil {
		u = rng.Float64()
	}
	ringRadius := 8.0 + u*(4.0-8.0) // Mth.lerp(u, 8.0, 4.0)
	stepDist := length - ringRadius
	inv := stepDist / length
	return breezeX + dx*inv, breezeY + dy*inv, breezeZ + dz*inv
}

func isAABBCollisionFree2612(cache *world.Cache, cx, yBottom, cz, width, height float64) bool {
	if cache == nil {
		return true
	}
	halfW := width*0.5 - 1e-4
	if halfW < 0.05 {
		halfW = 0.05
	}
	minX := int(math.Floor(cx - halfW))
	maxX := int(math.Floor(cx + halfW))
	minY := int(math.Floor(yBottom + 1e-4))
	maxY := int(math.Floor(yBottom + height - 1e-4))
	minZ := int(math.Floor(cz - halfW))
	maxZ := int(math.Floor(cz + halfW))
	for by := minY; by <= maxY; by++ {
		if by < world.MinY || by >= world.MinY+world.WorldHeight {
			return false
		}
		for bx := minX; bx <= maxX; bx++ {
			for bz := minZ; bz <= maxZ; bz++ {
				if isColliderBlockState2612(cache.GetBlock(bx, by, bz)) {
					return false
				}
			}
		}
	}
	return true
}

func isClearJumpTransition2612(cache *world.Cache, width, height, x0, y0, z0, x1, y1, z1 float64) bool {
	if cache == nil {
		return true
	}
	dx := x1 - x0
	dy := y1 - y0
	dz := z1 - z0
	segLen := math.Sqrt(dx*dx + dy*dy + dz*dz)
	minDim := math.Min(width, height)
	if minDim <= 0.1 {
		minDim = 0.6
	}
	steps := int(math.Ceil(segLen / minDim))
	if steps < 1 {
		steps = 1
	}
	var ux, uy, uz float64
	if segLen > 1e-6 {
		ux, uy, uz = dx/segLen, dy/segLen, dz/segLen
	}
	cx, cy, cz := x0, y0, z0
	stepAdvance := minDim * 0.8999999761581421
	for i := 0; i < steps; i++ {
		if i == steps-1 {
			cx, cy, cz = x1, y1, z1
		} else {
			cx += ux * stepAdvance
			cy += uy * stepAdvance
			cz += uz * stepAdvance
		}
		if !isAABBCollisionFree2612(cache, cx, cy, cz, width, height) {
			return false
		}
	}
	return true
}

// CalculateBreezeJumpVectorForAngle2612 implements `LongJumpUtil.calculateJumpVectorForAngle`
// from `26.1.2.jar` (`0.5D` pull-back, ballistic parabola with `gravity = 0.08D`, `0.95` velocity
// scale, optional `isClearTransition` trajectory check with `0.6 x 1.77` `LONG_JUMPING` AABB).
func CalculateBreezeJumpVectorForAngle2612(
	cache *world.Cache,
	mobX, mobY, mobZ float64,
	targetX, targetY, targetZ float64,
	maxJumpVel float64,
	angleDeg int,
	checkClearTransition bool,
) (vx, vy, vz float64, ok bool) {
	rawDX := targetX - mobX
	rawDZ := targetZ - mobZ
	rawHoriz := math.Hypot(rawDX, rawDZ)
	if rawHoriz < 1e-5 {
		return 0, 0, 0, false
	}
	pullX := (rawDX / rawHoriz) * LongJumpUtilPullBackDist
	pullZ := (rawDZ / rawHoriz) * LongJumpUtilPullBackDist
	aimX := targetX - pullX
	aimY := targetY
	aimZ := targetZ - pullZ

	dx := aimX - mobX
	dy := aimY - mobY
	dz := aimZ - mobZ
	dSq := dx*dx + dz*dz
	d := math.Sqrt(dSq)
	if d < 1e-5 {
		return 0, 0, 0, false
	}

	theta := float64(float32(angleDeg) * 3.1415927 / 180.0)
	xzAngle := math.Atan2(dz, dx)
	sin2Theta := math.Sin(2.0 * theta)
	cosTheta := math.Cos(theta)
	sinTheta := math.Sin(theta)
	cosSq := cosTheta * cosTheta

	denom := d*sin2Theta - 2.0*dy*cosSq
	if denom <= 1e-7 {
		return 0, 0, 0, false
	}
	vSq := (dSq * BreezeGravity) / denom
	if vSq < 0 {
		return 0, 0, 0, false
	}
	v := math.Sqrt(vSq)
	if v > maxJumpVel {
		return 0, 0, 0, false
	}

	vHoriz := v * cosTheta
	vVert := v * sinTheta
	cosXZ := math.Cos(xzAngle)
	sinXZ := math.Sin(xzAngle)

	if checkClearTransition && cache != nil && vHoriz > 1e-6 {
		samples := int(math.Ceil(d/vHoriz)) * 2
		var prevSet bool
		var prevX, prevY, prevZ float64
		distStep := 0.0
		for i := 0; i < samples-1; i++ {
			distStep += d / float64(samples)
			yOff := (sinTheta/cosTheta)*distStep - (distStep*distStep*BreezeGravity)/(2.0*vSq*cosSq)
			px := mobX + distStep*cosXZ
			py := mobY + yOff
			pz := mobZ + distStep*sinXZ
			if prevSet {
				if !isClearJumpTransition2612(cache, BreezeWidth, BreezeHeight, prevX, prevY, prevZ, px, py, pz) {
					return 0, 0, 0, false
				}
			}
			prevX, prevY, prevZ = px, py, pz
			prevSet = true
		}
	}

	vx = vHoriz * cosXZ * LongJumpUtilVelocityScale
	vy = vVert * LongJumpUtilVelocityScale
	vz = vHoriz * sinXZ * LongJumpUtilVelocityScale
	return vx, vy, vz, true
}

// SelectBreezeOptimalJumpVector2612 implements `LongJump.calculateOptimalJumpVector`
// (`ALLOWED_ANGLES = [40, 55, 60, 75, 80]` shuffled, `maxJumpVelocity = 0.058333334F * followRange`,
// plus `JUMP_BOOST` vertical bonus `normalize(v).y * getJumpBoostPower()`).
func SelectBreezeOptimalJumpVector2612(
	cache *world.Cache,
	rng *rand.Rand,
	mobX, mobY, mobZ float64,
	jumpBlockX, jumpBlockY, jumpBlockZ int,
	followRange float64,
	jumpBoostAmplifier int,
	checkClearTransition bool,
) (vx, vy, vz float64, ok bool) {
	angles := BreezeAllowedJumpAngles
	if rng != nil {
		for i := len(angles) - 1; i > 0; i-- {
			j := rng.Intn(i + 1)
			angles[i], angles[j] = angles[j], angles[i]
		}
	}
	if followRange <= 0 {
		followRange = BreezeFollowRange
	}
	maxVel := float64(float32(BreezeMaxJumpVelocityMultiplier) * float32(followRange))
	tx := float64(jumpBlockX) + 0.5
	ty := float64(jumpBlockY)
	tz := float64(jumpBlockZ) + 0.5

	for _, ang := range angles {
		jx, jy, jz, valid := CalculateBreezeJumpVectorForAngle2612(
			cache, mobX, mobY, mobZ, tx, ty, tz, maxVel, ang, checkClearTransition,
		)
		if !valid {
			continue
		}
		if jumpBoostAmplifier >= 0 {
			mag := math.Sqrt(jx*jx + jy*jy + jz*jz)
			if mag > 1e-6 {
				boostPower := 0.1 * float64(jumpBoostAmplifier+1)
				jy += (jy / mag) * boostPower
			}
		}
		return jx, jy, jz, true
	}
	return 0, 0, 0, false
}

// CanBreezeSelectJumpLandingBlock2612 checks whether `jumpTarget = (jx, jy, jz)` is a
// safe, non-water, non-dangerous landing position with line of sight from `(bx, by, bz)`.
func CanBreezeSelectJumpLandingBlock2612(
	cache *world.Cache,
	bx, by, bz float64,
	jx, jy, jz int,
	followRange float64,
) bool {
	if jy <= world.MinY || jy >= world.MinY+world.WorldHeight-1 {
		return false
	}
	if cache != nil {
		atState := cache.GetBlock(jx, jy, jz)
		belowState := cache.GetBlock(jx, jy-1, jz)
		// Reject landing into water/lava or onto a dangerous block (`lava`, `magma`, `fire`, `cactus`, `berry`, `powder_snow`).
		if isWaterOrBubbleBlockState2612(atState) || isWaterOrBubbleBlockState2612(belowState) {
			return false
		}
		if IsBlockDangerousForBreeze2612(atState) || IsBlockDangerousForBreeze2612(belowState) {
			return false
		}
		if !isColliderBlockState2612(belowState) {
			return false
		}
		if isColliderBlockState2612(atState) || isColliderBlockState2612(cache.GetBlock(jx, jy+1, jz)) {
			return false
		}
	}
	cx := float64(jx) + 0.5
	cy := float64(jy) + 0.5
	cz := float64(jz) + 0.5
	if BreezeHasLineOfSight2612(cache, bx, by, bz, cx, cy, cz, followRange) {
		return true
	}
	return BreezeHasLineOfSight2612(cache, bx, by, bz, cx, cy+4.0, cz, followRange)
}

func breezeWaterStateAt(cache *world.Cache, x, y, z float64) (inWater bool, waterDepth float64, inLava bool) {
	if cache == nil {
		return false, 0, false
	}
	bx := int(math.Floor(x))
	by := int(math.Floor(y + 0.01))
	bz := int(math.Floor(z))
	feetBlk := cache.GetBlock(bx, by, bz)
	if feetBlk == world.StateLava || canonicalResourceName(world.BlockName(feetBlk)) == "minecraft:lava" {
		return false, 0, true
	}
	h0 := float64(cache.FluidHeightAt(bx, by, bz))
	if h0 <= 0 && !isWaterOrBubbleBlockState2612(feetBlk) {
		return false, 0, false
	}
	if h0 <= 0 {
		h0 = 0.8888889
	}
	depth := h0 - (y - float64(by))
	if isWaterOrBubbleBlockState2612(cache.GetBlock(bx, by+1, bz)) {
		depth = 1.0 + float64(cache.FluidHeightAt(bx, by+1, bz)) - (y - float64(by))
		if depth < 1.5 {
			depth = 1.5
		}
	}
	if depth < 0.05 {
		depth = 0.05
	}
	return true, depth, false
}

// ============================================================================
// WIND CHARGE / BREEZE WIND CHARGE SERVER EXPLOSION RAYCAST & KNOCKBACK
// ============================================================================

func isWindChargeExplosionImmuneBlock2612(state uint16) bool {
	if state == world.StateAir {
		return false
	}
	switch canonicalResourceName(world.BlockName(state)) {
	case "minecraft:barrier", "minecraft:bedrock":
		return true
	default:
		return false
	}
}

// CalculateWindChargeExplodedPositions2612 implements `ServerExplosion.calculateExplodedPositions()`
// with `SimpleExplosionDamageCalculator` (`#minecraft:blocks_wind_charge_explosions` = `barrier`, `bedrock` ->
// resistance `3600000.0F`; all other blocks have `Optional.empty()` resistance so rays attenuate only by
// `0.22500001F` per `0.3`-block step).
func CalculateWindChargeExplodedPositions2612(cache *world.Cache, cx, cy, cz, radius float64) [][3]int {
	if cache == nil || radius <= 1e-5 {
		return nil
	}
	seen := make(map[[3]int]struct{}, 128)
	out := make([][3]int, 0, 128)

	for ix := 0; ix < 16; ix++ {
		for iy := 0; iy < 16; iy++ {
			for iz := 0; iz < 16; iz++ {
				if ix != 0 && ix != 15 && iy != 0 && iy != 15 && iz != 0 && iz != 15 {
					continue
				}
				dx := float64(float32(ix)/15.0*2.0 - 1.0)
				dy := float64(float32(iy)/15.0*2.0 - 1.0)
				dz := float64(float32(iz)/15.0*2.0 - 1.0)
				norm := math.Sqrt(dx*dx + dy*dy + dz*dz)
				if norm <= 1e-6 {
					continue
				}
				dx /= norm
				dy /= norm
				dz /= norm

				// Midpoint of vanilla `radius * (0.7F + random.nextFloat() * 0.6F)` = `radius * 1.0F`.
				power := float32(radius)
				rx, ry, rz := cx, cy, cz
				for power > 0 {
					bx := int(math.Floor(rx))
					by := int(math.Floor(ry))
					bz := int(math.Floor(rz))
					if by < world.MinY || by >= world.MinY+world.WorldHeight {
						break
					}
					st := cache.GetBlock(bx, by, bz)
					if isWindChargeExplosionImmuneBlock2612(st) {
						power -= (3600000.0 + 0.3) * 0.3
					}
					if power > 0 {
						key := [3]int{bx, by, bz}
						if _, exists := seen[key]; !exists {
							seen[key] = struct{}{}
							out = append(out, key)
						}
					}
					rx += dx * 0.30000001192092896
					ry += dy * 0.30000001192092896
					rz += dz * 0.30000001192092896
					power -= 0.22500001
				}
			}
		}
	}
	return out
}

func windChargeSeenPercent2612(cache *world.Cache, cx, cy, cz, ex, ey, ez, eyeHeight float64) float64 {
	if cache == nil {
		return 1.0
	}
	samples := [2]float64{ey + 0.1, ey + eyeHeight}
	unblocked := 0
	for _, sy := range samples {
		dx := ex - cx
		dy := sy - cy
		dz := ez - cz
		dist := math.Sqrt(dx*dx + dy*dy + dz*dz)
		if dist < 1e-4 {
			unblocked++
			continue
		}
		steps := int(math.Ceil(dist / 0.3))
		blocked := false
		for i := 1; i < steps; i++ {
			t := float64(i) / float64(steps)
			bx := int(math.Floor(cx + dx*t))
			by := int(math.Floor(cy + dy*t))
			bz := int(math.Floor(cz + dz*t))
			if by >= world.MinY && by < world.MinY+world.WorldHeight {
				if isColliderBlockState2612(cache.GetBlock(bx, by, bz)) {
					blocked = true
					break
				}
			}
		}
		if !blocked {
			unblocked++
		}
	}
	return float64(unblocked) / 2.0
}

// ComputeWindChargeEntityImpulse2612 implements `ServerExplosion.hurtEntities()`
// for `WindCharge` (`radius = 1.2`, `mult = 1.22`) and `BreezeWindCharge` (`radius = 3.0`, `mult = 1.0`).
func ComputeWindChargeEntityImpulse2612(
	cache *world.Cache,
	cx, cy, cz float64,
	ex, ey, ez, eyeHeight float64,
	isBreezeCharge bool,
	isCreativeFlying bool,
	explosionKBResistance float64,
) (dvx, dvy, dvz float64, affected bool) {
	if isCreativeFlying {
		return 0, 0, 0, false
	}
	radius := WindChargeBurstRadius
	kbMult := WindChargeKnockbackMultiplier
	if isBreezeCharge {
		radius = BreezeWindChargeBurstRadius
		kbMult = BreezeWindChargeKnockbackMultiplier
	}
	maxDist := radius * 2.0
	if maxDist <= 1e-5 {
		return 0, 0, 0, false
	}
	dxPos := ex - cx
	dyPos := ey - cy
	dzPos := ez - cz
	dist := math.Sqrt(dxPos*dxPos + dyPos*dyPos + dzPos*dzPos)
	distNorm := dist / maxDist
	if distNorm > 1.0 {
		return 0, 0, 0, false
	}

	seen := windChargeSeenPercent2612(cache, cx, cy, cz, ex, ey, ez, eyeHeight)
	if seen <= 0 {
		return 0, 0, 0, false
	}
	if explosionKBResistance < 0 {
		explosionKBResistance = 0
	} else if explosionKBResistance > 1 {
		explosionKBResistance = 1
	}

	eyeX := ex
	eyeY := ey + eyeHeight
	eyeZ := ez
	dirX := eyeX - cx
	dirY := eyeY - cy
	dirZ := eyeZ - cz
	dirLen := math.Sqrt(dirX*dirX + dirY*dirY + dirZ*dirZ)
	if dirLen < 1e-6 {
		dirX, dirY, dirZ, dirLen = 0, 1, 0, 1
	}
	dirX /= dirLen
	dirY /= dirLen
	dirZ /= dirLen

	scale := (1.0 - distNorm) * seen * kbMult * (1.0 - explosionKBResistance)
	if scale <= 1e-6 {
		return 0, 0, 0, false
	}
	return dirX * scale, dirY * scale, dirZ * scale, true
}

// ============================================================================
// BOGGED ENTITY NBT PERSISTENCE & HAND-AWARE SHEARING
// ============================================================================

// SaveBoggedEntityNBT serializes the live Bogged entity's `"sheared"` state into an NBT map
// matching `Bogged.addAdditionalSaveData` in `26.1.2.jar`.
func (s *Server) SaveBoggedEntityNBT(entityID int32) (map[string]any, bool) {
	if s == nil || s.entities == nil {
		return nil, false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeBogged {
		return nil, false
	}
	return EncodeBoggedShearedNBT(s.IsBoggedSheared(entityID)), true
}

// LoadBoggedEntityNBT restores `"sheared"` from an NBT map onto a live Bogged entity
// (`Bogged.readAdditionalSaveData`) and broadcasts `Bogged.DATA_SHEARED` (index 16).
func (s *Server) LoadBoggedEntityNBT(entityID int32, nbtMap map[string]any) bool {
	if s == nil || s.entities == nil {
		return false
	}
	return s.SetBoggedSheared(entityID, DecodeBoggedShearedNBT(nbtMap))
}

func (s *Server) defaultInteractionRand() *rand.Rand {
	gt, _ := s.WorldTime()
	seed := time.Now().UnixNano() ^ (gt * 0x5DEECE66D)
	return rand.New(rand.NewSource(seed))
}

// InteractWithBoggedHand implements `Bogged.mobInteract(Player, InteractionHand)`
// for either `MainHand` (`hand == 0`) or `OffHand` (`hand == 1`), rolling `shearing/bogged`
// with `rng` and damaging the shears in `hand` by 1 durability.
func (s *Server) InteractWithBoggedHand(session *PlayerSession, entityID int32, hand int32, rng *rand.Rand) (dropped []world.ItemStack, sheared bool) {
	if s == nil || s.entities == nil || session == nil || session.IsDead() {
		return nil, false
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator || snap.Health <= 0 {
		return nil, false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeBogged {
		return nil, false
	}
	dx := ent.X - snap.X
	dy := ent.Y - snap.Y
	dz := ent.Z - snap.Z
	if snap.GameMode != GameModeCreative && (dx*dx+dy*dy+dz*dz) > MaxMeleeAttackReach*MaxMeleeAttackReach {
		return nil, false
	}
	slotIdx, held := session.handSlotAndStack(hand)
	if held.IsEmpty() || canonicalResourceName(held.ResolvedID()) != ShearsItemID {
		return nil, false
	}
	if !s.ReadyForBoggedShearing(entityID) {
		return nil, false
	}
	if rng == nil {
		rng = s.defaultInteractionRand()
	}

	tr := s.entityTracker()
	tr.mu.Lock()
	mem := tr.mobStateFor(entityID, tr.aiTickID)
	if mem.sheared {
		tr.mu.Unlock()
		return nil, false
	}
	mem.sheared = true
	tr.mu.Unlock()

	dim := ent.EffectiveDimension()
	cp := ent.ChunkPos()
	dropped = RollBoggedShearMushrooms(rng)
	for _, stack := range dropped {
		s.SpawnItemEntityInDimension(dim, ent.X, ent.Y+BoggedHeight, ent.Z, stack)
	}

	if snap.GameMode != GameModeCreative {
		meta := GetSlotItemMeta(session, slotIdx)
		unbreakingLvl := meta.EnchantmentLevel("minecraft:unbreaking")
		if unbreakingLvl <= 0 || rng.Intn(int(unbreakingLvl)+1) == 0 {
			meta.Damage++
		}
		if meta.Damage >= BoggedShearsMaxDurability {
			session.SetInventorySlot(slotIdx, world.ItemStack{})
			SetSlotItemMeta(session, slotIdx, ItemMeta{})
		} else {
			SetSlotItemMeta(session, slotIdx, meta)
		}
	}

	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
		EncodeBoggedShearedDataPacket(entityID, true))
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundBoggedShear, SoundSourcePlayers, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
	s.EmitVibration(VibrationEvent{
		Dimension:      dim,
		Type:           GameEventShear,
		X:              ent.X,
		Y:              ent.Y,
		Z:              ent.Z,
		Player:         session,
		SourceEntityID: session.EntityID,
	})
	_ = protocol.PlaySetEntityData
	return dropped, true
}

// ============================================================================
// CAVE SPIDER (0.7 x 0.5) vs SPIDER (1.4 x 0.9) SIZED PATHFINDING & 1x1 PASSAGE
// ============================================================================

// SpiderDimensionsForType2612 returns the `(width, height, eyeHeight)` from `EntityType`
// in `26.1.2.jar` (`0.7, 0.5, 0.45` for `cave_spider`, `1.4, 0.9, 0.65` for `spider`).
func SpiderDimensionsForType2612(typeName string) (width, height, eyeHeight float64) {
	if canonicalResourceName(typeName) == MobTypeCaveSpider {
		return CaveSpiderWidth, CaveSpiderHeight, CaveSpiderEyeHeight
	}
	return SpiderWidth, SpiderHeight, SpiderEyeHeight
}

func isColumnPassableForSize2612(chunks *world.Cache, x, feetY, z int, width, height float64) bool {
	if chunks == nil {
		return true
	}
	if !isPassableBlock(chunks.GetBlock(x, feetY, z)) {
		return false
	}
	blocksTall := int(math.Ceil(height - 1e-4))
	for dy := 1; dy < blocksTall; dy++ {
		if !isPassableBlock(chunks.GetBlock(x, feetY+dy, z)) {
			return false
		}
	}
	// Entities wider than 1.0 block (e.g. Spider width = 1.4) cannot squeeze through a 1-block-wide
	// gap pinched by solid walls on both opposite sides along X or along Z.
	if width > 1.0 {
		pinchedX := isColliderBlockState2612(chunks.GetBlock(x-1, feetY, z)) &&
			isColliderBlockState2612(chunks.GetBlock(x+1, feetY, z))
		pinchedZ := isColliderBlockState2612(chunks.GetBlock(x, feetY, z-1)) &&
			isColliderBlockState2612(chunks.GetBlock(x, feetY, z+1))
		if pinchedX || pinchedZ {
			return false
		}
	}
	return true
}

func resolveStepYSized2612(chunks *world.Cache, fromX, fromY, fromZ, toX, toZ, maxDrop int, width, height float64) (int, bool) {
	if chunks == nil {
		return fromY, true
	}
	if fromY <= world.MinY+1 || fromY >= world.MinY+world.WorldHeight-2 {
		return 0, false
	}
	if isColumnPassableForSize2612(chunks, toX, fromY, toZ, width, height) {
		for drop := 0; drop <= maxDrop; drop++ {
			candFeetY := fromY - drop
			if candFeetY <= world.MinY+1 {
				break
			}
			if drop > 0 && !isColumnPassableForSize2612(chunks, toX, candFeetY, toZ, width, height) {
				break
			}
			floorState := chunks.GetBlock(toX, candFeetY-1, toZ)
			if isHazardBlock(floorState) {
				return 0, false
			}
			if isSolidGroundBlock(floorState) {
				return candFeetY, true
			}
		}
		return 0, false
	}
	stepBlock := chunks.GetBlock(toX, fromY, toZ)
	headClearY := fromY + int(math.Ceil(height-1e-4))
	if headClearY < fromY+1 {
		headClearY = fromY + 1
	}
	if isSolidGroundBlock(stepBlock) {
		if isPassableBlock(chunks.GetBlock(fromX, headClearY, fromZ)) &&
			isColumnPassableForSize2612(chunks, toX, fromY+1, toZ, width, height) {
			return fromY + 1, true
		}
	}
	return 0, false
}

func canStepBetweenSized2612(chunks *world.Cache, fromX, fromY, fromZ, toX, toZ int, width, height float64) (int, bool) {
	nextY, ok := resolveStepYSized2612(chunks, fromX, fromY, fromZ, toX, toZ, MaxStepDownBlocks, width, height)
	if !ok {
		return 0, false
	}
	dx := toX - fromX
	dz := toZ - fromZ
	if dx != 0 && dz != 0 {
		if nextY > fromY {
			return 0, false
		}
		if !isColumnPassableForSize2612(chunks, fromX+dx, fromY, fromZ, width, height) ||
			!isColumnPassableForSize2612(chunks, fromX, fromY, fromZ+dz, width, height) {
			return 0, false
		}
		if _, ok1 := resolveStepYSized2612(chunks, fromX, fromY, fromZ, fromX+dx, fromZ, MaxStepDownBlocks, width, height); !ok1 {
			return 0, false
		}
		if _, ok2 := resolveStepYSized2612(chunks, fromX, fromY, fromZ, fromX, fromZ+dz, MaxStepDownBlocks, width, height); !ok2 {
			return 0, false
		}
	}
	return nextY, true
}

func hasDirectWalkableLineSized2612(chunks *world.Cache, startX float64, feetY int, startZ, goalX, goalZ, width, height float64) bool {
	if chunks == nil {
		return true
	}
	dx := goalX - startX
	dz := goalZ - startZ
	dist := math.Hypot(dx, dz)
	if dist < 1e-4 {
		return true
	}
	if dist > 16.0 {
		return false
	}
	steps := int(math.Ceil(dist / 0.25))
	if steps < 1 {
		steps = 1
	}
	curBX := int(math.Floor(startX))
	curBZ := int(math.Floor(startZ))
	curY := feetY
	for i := 1; i <= steps; i++ {
		frac := float64(i) / float64(steps)
		nbx := int(math.Floor(startX + dx*frac))
		nbz := int(math.Floor(startZ + dz*frac))
		if nbx == curBX && nbz == curBZ {
			continue
		}
		ny, ok := canStepBetweenSized2612(chunks, curBX, curY, curBZ, nbx, nbz, width, height)
		if !ok {
			return false
		}
		curBX, curBZ, curY = nbx, nbz, ny
	}
	return true
}

// FindPathSized2612 computes a bounded A* path honoring the mob's exact `(width, height)` AABB
// so `CaveSpider` (`0.7 x 0.5`) traverses `1x1` tunnels while `Spider` (`1.4 x 0.9`) and 2-block-tall
// mobs cannot squeeze through `1x1` openings.
func FindPathSized2612(chunks *world.Cache, startX, startY, startZ, goalX, goalY, goalZ, maxNodes int, width, height float64) []PathNode {
	if startX == goalX && startZ == goalZ {
		return nil
	}
	if maxNodes <= 0 {
		maxNodes = MaxPathSearchNodes
	}
	nodes := make([]astarNode, 0, maxNodes)
	indexByPos := make(map[[3]int]int, maxNodes)

	startH := pathHeuristic(startX, startY, startZ, goalX, goalY, goalZ)
	nodes = append(nodes, astarNode{
		x: startX, y: startY, z: startZ,
		g: 0, h: startH, f: startH, parent: -1,
	})
	indexByPos[[3]int{startX, startY, startZ}] = 0

	bestIdx := 0
	bestH := startH
	expanded := 0

	for expanded < maxNodes {
		currIdx := -1
		currF := math.MaxFloat64
		for i := range nodes {
			if !nodes[i].closed && nodes[i].f < currF {
				currF = nodes[i].f
				currIdx = i
			}
		}
		if currIdx < 0 {
			break
		}
		curr := nodes[currIdx]
		nodes[currIdx].closed = true
		expanded++

		if curr.h < bestH-1e-4 {
			bestH = curr.h
			bestIdx = currIdx
		}
		if curr.x == goalX && curr.z == goalZ {
			bestIdx = currIdx
			break
		}

		for _, d := range neighborDirs {
			nx := curr.x + d[0]
			nz := curr.z + d[2]
			ny, ok := canStepBetweenSized2612(chunks, curr.x, curr.y, curr.z, nx, nz, width, height)
			if !ok {
				continue
			}
			stepCost := 1.0
			if d[0] != 0 && d[2] != 0 {
				stepCost = math.Sqrt2
			}
			if ny != curr.y {
				stepCost += 0.5
			}
			candG := curr.g + stepCost
			key := [3]int{nx, ny, nz}
			if prevIdx, seen := indexByPos[key]; seen {
				if !nodes[prevIdx].closed && candG < nodes[prevIdx].g {
					nodes[prevIdx].g = candG
					nodes[prevIdx].f = candG + nodes[prevIdx].h
					nodes[prevIdx].parent = currIdx
				}
				continue
			}
			h := pathHeuristic(nx, ny, nz, goalX, goalY, goalZ)
			indexByPos[key] = len(nodes)
			nodes = append(nodes, astarNode{
				x: nx, y: ny, z: nz,
				g: candG, h: h, f: candG + h, parent: currIdx,
			})
		}
	}

	if bestIdx <= 0 {
		return nil
	}
	var rev []PathNode
	for idx := bestIdx; idx > 0; idx = nodes[idx].parent {
		n := nodes[idx]
		rev = append(rev, PathNode{X: n.x, Y: n.y, Z: n.z})
		if len(rev) >= MaxPathWaypoints {
			break
		}
	}
	for l, r := 0, len(rev)-1; l < r; l, r = l+1, r-1 {
		rev[l], rev[r] = rev[r], rev[l]
	}
	return rev
}

func tryApplyHorizontalStepSized2612(chunks *world.Cache, curX float64, curY int, curZ, candX, candZ, width, height float64) (float64, int, float64, bool) {
	if chunks == nil {
		return candX, curY, candZ, true
	}
	curBX := int(math.Floor(curX))
	curBZ := int(math.Floor(curZ))
	candBX := int(math.Floor(candX))
	candBZ := int(math.Floor(candZ))
	if candBX == curBX && candBZ == curBZ {
		if !isColumnPassableForSize2612(chunks, candBX, curY, candBZ, width, height) {
			return curX, curY, curZ, false
		}
		return candX, curY, candZ, true
	}
	ny, ok := canStepBetweenSized2612(chunks, curBX, curY, curBZ, candBX, candBZ, width, height)
	if !ok {
		return curX, curY, curZ, false
	}
	return candX, ny, candZ, true
}

func stepHostileChaseTowardSized2612(
	chunks *world.Cache,
	mem *mobMemory,
	x float64,
	feetY int,
	z float64,
	yaw float32,
	targetX, targetY, targetZ float64,
	chaseSpeed, stopDist, width, height float64,
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
		hasDirectWalkableLineSized2612(chunks, x, feetY, z, targetX, targetZ, width, height)

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
			mem.waypoints = FindPathSized2612(chunks, startX, feetY, startZ, goalBX, targetFeetY, goalBZ, MaxPathSearchNodes, width, height)
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
			if nx, ny, nz, ok := tryApplyHorizontalStepSized2612(chunks, x, feetY, z, candX, candZ, width, height); ok {
				nextX, nextY, nextZ = nx, float64(ny), nz
			} else {
				mem.repathCooldown = 0
			}
		}
	}
	return nextX, nextY, nextZ, nextYaw
}
