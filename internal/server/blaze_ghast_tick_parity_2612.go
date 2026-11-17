package server

import (
	"math"
	"math/rand"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

// ============================================================================
// BLAZE & GHAST TICK-BY-TICK PARITY (Vanilla Minecraft 26.1.2 / Protocol 775)
// Verified against server-26.1.2.jar:
//   - net.minecraft.world.entity.monster.Blaze
//   - net.minecraft.world.entity.monster.Blaze$BlazeAttackGoal
//   - net.minecraft.world.entity.projectile.hurtingprojectile.SmallFireball
//   - net.minecraft.world.entity.projectile.throwableitemprojectile.Snowball
//   - net.minecraft.world.entity.monster.Ghast
//   - net.minecraft.world.entity.monster.Ghast$GhastShootFireballGoal
//   - net.minecraft.world.entity.monster.Ghast$GhastMoveControl
//   - net.minecraft.world.entity.monster.Ghast$RandomFloatAroundGoal
//   - net.minecraft.world.entity.monster.Ghast$GhastLookGoal
//   - net.minecraft.world.entity.projectile.hurtingprojectile.LargeFireball
//   - net.minecraft.world.entity.projectile.hurtingprojectile.AbstractHurtingProjectile
//   - net.minecraft.world.entity.projectile.ProjectileDeflection
// ============================================================================

const (
	// Blaze dimensions, hover, and attack constants (Blaze.java, EntityType.java)
	BlazeWidth2612                      float64 = 0.6
	BlazeHeight2612                     float64 = 1.8
	BlazeEyeHeight2612                  float64 = 1.53  // 1.8 * 0.85
	BlazeSmallFireballSpawnOffsetY2612  float64 = 1.4   // blaze.getY(0.5D) + 0.5D = Y + 0.9 + 0.5
	BlazeDefaultAllowedHeightOffset2612 float64 = 0.5   // Blaze.<init>: allowedHeightOffset = 0.5F
	BlazeHeightOffsetIntervalTicks2612  int     = 100   // Blaze.customServerAiStep: nextHeightOffsetChangeTick = 100
	BlazeHeightOffsetTriangleMode2612   float64 = 0.5   // random.triangle(0.5D, 6.891D)
	BlazeHeightOffsetTriangleDev2612    float64 = 6.891 // random.triangle(0.5D, 6.891D)
	BlazeHoverAscentTargetVY2612        float64 = 0.30000001192092896
	BlazeHoverAscentAccelFactor2612     float64 = 0.30000001192092896 // dm.add(0, (0.3 - dm.y) * 0.3, 0)
	BlazeFallSlowdownMultiplier2612     float64 = 0.6                 // Blaze.aiStep: dm.multiply(1.0, 0.6, 1.0) when !onGround && dm.y < 0
	BlazeMeleeDistSqThreshold2612       float64 = 4.0                 // BlazeAttackGoal.tick: d0 < 4.0D (2.0 blocks)
	BlazeFireballSpreadFactor2612       float64 = 0.5                 // d4 = sqrt(sqrt(d0)) * 0.5D
	BlazeFireballTriangleMultiplier2612 float64 = 2.297               // random.triangle(d1, 2.297D * d4)
	BlazeLostSightChaseMaxTicks2612     int     = 5                   // BlazeAttackGoal.tick: if (lastSeen < 5) setWantedPosition(...)
	BlazeWaterHurtCooldownTicks2612     int     = 10                  // LivingEntity.hurtServer invulnerableTime 20 -> 10
	LevelEventSoundBlazeFireball2612    int32   = 1018                // LevelEvent.SOUND_BLAZE_FIREBALL

	// Ghast dimensions, flight, charge, and deflection constants (Ghast.java, EntityType.java)
	GhastWidth2612                     float64 = 4.0
	GhastHeight2612                    float64 = 4.0
	GhastEyeHeight2612                 float64 = 2.6   // EntityType.GHAST: eyeHeight(2.6F)
	GhastFollowRange2612               float64 = 100.0 // Attributes.FOLLOW_RANGE = 100.0D
	GhastFlyingSpeedAttr2612           float64 = 0.06  // Attributes.FLYING_SPEED = 0.06D
	GhastMoveImpulsePerStep2612        float64 = 0.10  // FLYING_SPEED * 5.0D / 3.0D = 0.10D
	GhastFlyingAirInertia2612          float64 = 0.91  // LivingEntity.travelFlying air friction
	GhastShootMaxDist2612              float64 = 64.0  // GhastShootFireballGoal: distanceToSqr < 4096.0D
	GhastShootMaxDistSqr2612           float64 = 4096.0
	GhastFireballMuzzleForwardDist2612 float64 = 4.0  // viewVector * 4.0D
	GhastFireballMuzzleOffsetY2612     float64 = 2.5  // ghast.getY(0.5D) + 0.5D = Y + 2.0 + 0.5
	GhastRandomFloatRadius2612         float64 = 16.0 // RandomFloatAroundGoal: (nextFloat()*2 - 1) * 16.0F
	GhastRandomFloatMinDistSq2612      float64 = 1.0  // distSq < 1.0D || distSq > 3600.0D
	GhastRandomFloatMaxDistSq2612      float64 = 3600.0
	GhastCourseCooldownMinTicks2612    int     = 2 // GhastMoveControl: nextInt(5) + 2 -> 2..6 ticks
	GhastCourseCooldownMaxTicks2612    int     = 6
	HurtingProjectileInitialAccel2612  float64 = 0.10 // AbstractHurtingProjectile.INITAL_ACCELERATION_POWER = 0.1D
	HurtingProjectileAirInertia2612    float64 = 0.95 // AbstractHurtingProjectile.getInertia() = 0.95F
	LevelEventSoundGhastWarn2612       int32   = 1015 // LevelEvent.SOUND_GHAST_WARNING
	LevelEventSoundGhastShoot2612      int32   = 1016 // LevelEvent.SOUND_GHAST_FIREBALL
)

// SampleRandomTriangle2612 implements `RandomSource.triangle(mode, deviation)`
// (`mode + deviation * (nextDouble() - nextDouble())`) from vanilla 26.1.2.
func SampleRandomTriangle2612(rng *rand.Rand, mode, deviation float64) float64 {
	if rng == nil {
		return mode
	}
	return mode + deviation*(rng.Float64()-rng.Float64())
}

// ComputeBlazeFireballDirection2612 computes the normalized `(dirX, dirY, dirZ)`
// aim vector for a `SmallFireball` fired by a Blaze at `(bx, by, bz)` toward a
// target at `(tx, ty, tz)` with target height `targetHeight`, matching
// `Blaze$BlazeAttackGoal.tick()` (`blaze_2612.txt:525-646`):
//   - `d0 = (tx-bx)^2 + (ty-by)^2 + (tz-bz)^2` (`blaze.distanceToSqr(target)`)
//   - `d1 = tx - bx`
//   - `d2 = (ty + targetHeight*0.5) - (by + BlazeHeight2612*0.5)`
//   - `d3 = tz - bz`
//   - `d4 = Math.sqrt(Math.sqrt(d0)) * 0.5`
//   - `vec = (triangle(d1, 2.297*d4), d2, triangle(d3, 2.297*d4)).normalize()`
func ComputeBlazeFireballDirection2612(bx, by, bz, tx, ty, tz, targetHeight float64, rng *rand.Rand) (dirX, dirY, dirZ, spreadHalfWidth float64) {
	rawDX := tx - bx
	rawDY := ty - by
	rawDZ := tz - bz
	d0 := rawDX*rawDX + rawDY*rawDY + rawDZ*rawDZ

	d1 := tx - bx
	d2 := (ty + targetHeight*0.5) - (by + BlazeHeight2612*0.5)
	d3 := tz - bz
	d4 := math.Sqrt(math.Sqrt(d0)) * BlazeFireballSpreadFactor2612
	dev := BlazeFireballTriangleMultiplier2612 * d4

	vx := SampleRandomTriangle2612(rng, d1, dev)
	vy := d2
	vz := SampleRandomTriangle2612(rng, d3, dev)
	mag := math.Sqrt(vx*vx + vy*vy + vz*vz)
	if mag < 1e-9 {
		return 0, 0, 1, dev
	}
	return vx / mag, vy / mag, vz / mag, dev
}

// StepBlazeVerticalHover2612 implements `Blaze.customServerAiStep` + `Blaze.aiStep`
// vertical hovering and slow-falling (`blaze_2612.txt:160-176, 253-311`):
//   - Decrements `nextOffsetTick--`; when `<= 0`, resets to `100` and samples
//     `allowedOffset = triangle(0.5, 6.891)`!
//   - If `hasTarget && targetEyeY > blazeEyeY + allowedOffset`:
//     `vy += (0.3 - vy) * 0.3`
//   - If `!onGround && vy < 0`:
//     `vy *= 0.6`!
func StepBlazeVerticalHover2612(
	curVY float64,
	onGround bool,
	blazeY float64,
	hasTarget bool,
	targetEyeY float64,
	allowedOffset float64,
	nextOffsetTick int,
	rng *rand.Rand,
) (newVY, newAllowedOffset float64, newNextOffsetTick int) {
	newAllowedOffset = allowedOffset
	newNextOffsetTick = nextOffsetTick - 1
	if newNextOffsetTick <= 0 {
		newNextOffsetTick = BlazeHeightOffsetIntervalTicks2612
		newAllowedOffset = SampleRandomTriangle2612(rng, BlazeHeightOffsetTriangleMode2612, BlazeHeightOffsetTriangleDev2612)
	}
	newVY = curVY
	blazeEyeY := blazeY + BlazeEyeHeight2612
	if hasTarget && targetEyeY > blazeEyeY+newAllowedOffset {
		newVY += (BlazeHoverAscentTargetVY2612 - newVY) * BlazeHoverAscentAccelFactor2612
	}
	if !onGround && newVY < 0 {
		newVY *= BlazeFallSlowdownMultiplier2612
	}
	return newVY, newAllowedOffset, newNextOffsetTick
}

// CanGhastBoxReach2612 implements `Ghast$GhastMoveControl.canReach(Vec3)`
// (`ghast_2612.txt:541-609`): sweeps the Ghast's `4.0 x 4.0 x 4.0` AABB from
// `(x, y, z)` to `(wantedX, wantedY, wantedZ)` and returns false if any block
// along the segment has a non-empty collision shape intersecting the swept AABB.
func CanGhastBoxReach2612(cache *world.Cache, x, y, z, wantedX, wantedY, wantedZ float64) bool {
	if cache == nil {
		return true
	}
	dx := wantedX - x
	dy := wantedY - y
	dz := wantedZ - z
	dist := math.Sqrt(dx*dx + dy*dy + dz*dz)
	steps := int(math.Ceil(dist))
	if steps < 1 {
		steps = 1
	}
	halfW := GhastWidth2612 * 0.5 // 2.0
	for s := 1; s <= steps; s++ {
		t := float64(s) / float64(steps)
		cx := x + dx*t
		cy := y + dy*t
		cz := z + dz*t
		aabb := world.AABB{
			MinX: cx - halfW,
			MinY: cy,
			MinZ: cz - halfW,
			MaxX: cx + halfW,
			MaxY: cy + GhastHeight2612,
			MaxZ: cz + halfW,
		}
		minBX := int(math.Floor(aabb.MinX + 1e-6))
		maxBX := int(math.Floor(aabb.MaxX - 1e-6))
		minBY := int(math.Floor(aabb.MinY + 1e-6))
		maxBY := int(math.Floor(aabb.MaxY - 1e-6))
		minBZ := int(math.Floor(aabb.MinZ + 1e-6))
		maxBZ := int(math.Floor(aabb.MaxZ - 1e-6))
		for by := minBY; by <= maxBY; by++ {
			if by < world.MinY || by >= world.MinY+world.WorldHeight {
				return false
			}
			for bx := minBX; bx <= maxBX; bx++ {
				for bz := minBZ; bz <= maxBZ; bz++ {
					st := cache.GetBlock(bx, by, bz)
					if st == world.StateAir {
						continue
					}
					for _, box := range world.BlockCollisionBoxesAt(bx, by, bz, st) {
						if aabb.MaxX > box.MinX && aabb.MinX < box.MaxX &&
							aabb.MaxY > box.MinY && aabb.MinY < box.MaxY &&
							aabb.MaxZ > box.MinZ && aabb.MinZ < box.MaxZ {
							return false
						}
					}
				}
			}
		}
	}
	return true
}

// ComputeGhastFireballSpawnAndAim2612 computes the exact spawn position
// `(spawnX, spawnY, spawnZ)` and aim vector `(dirX, dirY, dirZ)` when
// `Ghast$GhastShootFireballGoal` fires at `chargeTime == 20` (`ghast_2612.txt:819-914`):
//   - `view = ElytraLookVector(ghastYaw, ghastPitch)`
//   - `spawnX = gx + view.x * 4.0`, `spawnY = gy + 2.5` (`getY(0.5) + 0.5`), `spawnZ = gz + view.z * 4.0`
//   - `d2 = tx - spawnX`, `d3 = (ty + targetHeight*0.5) - spawnY`, `d4 = tz - spawnZ`
func ComputeGhastFireballSpawnAndAim2612(gx, gy, gz float64, ghastYaw, ghastPitch float32, tx, ty, tz, targetHeight float64) (spawnX, spawnY, spawnZ, dirX, dirY, dirZ float64) {
	vx, _, vz := ElytraLookVector(ghastYaw, ghastPitch)
	spawnX = gx + vx*GhastFireballMuzzleForwardDist2612
	spawnY = gy + GhastFireballMuzzleOffsetY2612
	spawnZ = gz + vz*GhastFireballMuzzleForwardDist2612

	dx := tx - spawnX
	dy := (ty + targetHeight*0.5) - spawnY
	dz := tz - spawnZ
	mag := math.Sqrt(dx*dx + dy*dy + dz*dz)
	if mag < 1e-9 {
		return spawnX, spawnY, spawnZ, vx, 0, vz
	}
	return spawnX, spawnY, spawnZ, dx / mag, dy / mag, dz / mag
}

// DeflectGhastFireballByProjectile2612 implements `Projectile.onHit` (`fireballs_snowball_2612.txt:1473-1491`)
// deflecting an active `minecraft:fireball` (`LargeFireball`, `#minecraft:redirectable_projectile`)
// when struck by another projectile (`arrow`, `trident`, `snowball`, etc.):
//   - If `shooter != nil`, uses `ProjectileDeflection.AIM_DEFLECT` (`shooter.getLookAngle()`)
//     and transfers ownership (`ShooterID = shooter.EntityID`, `DeflectedBy = shooter`).
//   - Otherwise redirects along the hitting projectile's normalized velocity `(projVX, projVY, projVZ)`.
//   - Preserves the fireball's `ExplosionPower`!
func (s *Server) DeflectGhastFireballByProjectile2612(fireballID int32, shooter *PlayerSession, projVX, projVY, projVZ float64) bool {
	if s == nil || s.entities == nil || fireballID <= 0 {
		return false
	}
	ent := s.entities.Get(fireballID)
	if ent == nil || canonicalResourceName(ent.TypeName) != EntityTypeFireball {
		return false
	}

	var dirX, dirY, dirZ float64
	var newShooterID int32
	newShooterType := "minecraft:projectile"
	if shooter != nil && !shooter.IsDead() {
		pSnap := shooter.Snapshot()
		dirX, dirY, dirZ = ElytraLookVector(pSnap.Yaw, pSnap.Pitch)
		newShooterID = pSnap.EntityID
		newShooterType = "minecraft:player"
	} else {
		mag := math.Sqrt(projVX*projVX + projVY*projVY + projVZ*projVZ)
		if mag < 1e-6 {
			dirX, dirY, dirZ = 0, 0, 1
		} else {
			dirX, dirY, dirZ = projVX/mag, projVY/mag, projVZ/mag
		}
	}
	vx := dirX * (GhastFireballSpeed * 1.25)
	vy := dirY * (GhastFireballSpeed * 1.25)
	vz := dirZ * (GhastFireballSpeed * 1.25)

	mgr := s.netherHostileManager()
	mgr.mu.Lock()
	st, ok := mgr.fireballs[fireballID]
	if !ok || st == nil {
		st = &FireballProjectileState{
			EntityID:       fireballID,
			TypeName:       EntityTypeFireball,
			Dimension:      ent.EffectiveDimension(),
			ExplosionPower: GhastDefaultExplosionPower,
		}
		mgr.fireballs[fireballID] = st
	}
	st.VX = vx
	st.VY = vy
	st.VZ = vz
	st.Deflected = true
	st.DeflectedBy = shooter
	if newShooterID != 0 {
		st.ShooterID = newShooterID
	}
	st.ShooterType = newShooterType
	mgr.mu.Unlock()

	yaw := yawToward(vx, vz)
	s.entities.Update(fireballID, func(e *world.Entity) {
		e.Yaw = yaw
		e.VelocityX = clampVelocityShort(vx)
		e.VelocityY = clampVelocityShort(vy)
		e.VelocityZ = clampVelocityShort(vz)
	})
	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, protocol.PlaySetEntityMotion,
		EncodeSetEntityMotionPacket(fireballID, vx, vy, vz))
	return true
}
