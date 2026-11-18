package server

import (
	"math"
	"math/rand"

	"regionio/internal/nbt"
	"regionio/internal/protocol"
	"regionio/internal/registry"
	"regionio/internal/world"
)

// ============================================================================
// PHANTOM, DROWNED & STRIDER 26.1.2 TICK-BY-TICK PARITY
// Verified directly against server-26.1.2.jar:
//   - Phantom, Phantom$PhantomAttackStrategyGoal, Phantom$PhantomCircleAroundAnchorGoal,
//     Phantom$PhantomSweepAttackGoal, Phantom$PhantomAttackPlayerTargetGoal,
//     Phantom$PhantomMoveControl, PhantomSpawner
//   - Drowned, Drowned$DrownedTridentAttackGoal, Drowned$DrownedAttackGoal,
//     Drowned$DrownedGoToWaterGoal, Drowned$DrownedGoToBeachGoal,
//     Drowned$DrownedSwimUpGoal, Drowned$DrownedMoveControl, RangedAttackGoal,
//     Zombie$ZombieAttackTurtleEggGoal / RemoveBlockGoal
//   - Strider, Strider$StriderGoToLavaGoal, Strider$StriderPathNavigation,
//     ItemBasedSteering, FoodOnAStickItem, LivingEntity.isSensitiveToWater
// ============================================================================

const (
	// Vanilla 26.1.2 EntityType.PHANTOM base hitbox & eyeHeight:
	// `.sized(0.9F, 0.5F).eyeHeight(0.175F)` scaled by `(1.0F + 0.15F * size)` in `Phantom.getDefaultDimensions`.
	PhantomBaseWidth2612     float32 = 0.9
	PhantomBaseHeight2612    float32 = 0.5
	PhantomBaseEyeHeight2612 float32 = 0.175
	PhantomSizeScaleStep2612 float32 = 0.15

	// Vanilla Overworld sea level (`ServerLevel.getSeaLevel()` = 63).
	OverworldSeaLevel2612 = 63

	// `PhantomAttackStrategyGoal`:
	//   - `start()`: `nextSweepTick = adjustedTickDelay(10)` (10 ticks)
	//   - `tick()` on `CIRCLE -> SWOOP`: `nextSweepTick = adjustedTickDelay((8 + nextInt(4)) * 20)` (`160..220` ticks)
	PhantomInitialSweepDelayTicks2612 = 10
	PhantomResweepMinSeconds2612      = 8
	PhantomResweepSpanSeconds2612     = 4

	// `PhantomAttackPlayerTargetGoal`: `reducedTickDelay(60)` = 30 ticks between target scans.
	PhantomTargetScanCooldownTicks2612 = 30

	// `Drowned`:
	//   - `DrownedTridentAttackGoal(this, 1.0D, 40, 10.0F)`
	//   - `performRangedAttack`: `velocity = 1.6F`, `inaccuracy = 14 - difficulty.getId() * 4`
	ItemFishingRod                         = "minecraft:fishing_rod"
	DrownedTridentAttackIntervalTicks2612  = 40
	DrownedTridentAttackRadius2612         = 10.0
	DrownedTridentMinRangedDist2612        = 2.0
	DrownedTridentProjectileSpeed2612      = 1.6
	DrownedTurtleEggDestroyThreshold2612   = 60 // `RemoveBlockGoal`: breaks when `ticksSinceReachedGoal > 60`
	SoundZombieDestroyEgg2612              = "minecraft:entity.zombie.destroy_egg"
	SoundTurtleEggBreak2612                = "minecraft:block.turtle_egg.break"
	SoundDrownedAmbientWater2612           = "minecraft:entity.drowned.ambient_water"
	SoundDrownedHurtWater2612              = "minecraft:entity.drowned.hurt_water"
	SoundDrownedDeathWater2612             = "minecraft:entity.drowned.death_water"
	SoundDrownedStep2612                   = "minecraft:entity.drowned.step"

	// `Strider`:
	//   - `Attributes.MOVEMENT_SPEED = 0.175D`
	//   - `SUFFOCATING_MODIFIER = -0.34D` (`ADD_MULTIPLIED_BASE` -> `0.175 * 0.66 = 0.1155`)
	//   - `STEERING_MODIFIER = 0.55F`, `SUFFOCATE_STEERING_MODIFIER = 0.35F`
	//   - `ItemBasedSteering.boostFactor() = 1.0F + 1.15F * sin((float)boostTime / (float)boostTimeTotal * PI)`
	StriderBaseMovementSpeed2612       float64 = 0.175
	StriderSuffocatingSpeedMult2612    float64 = 0.66 // 1.0 + (-0.34)
	StriderWarmSteeringModifier2612    float64 = 0.55
	StriderColdSteeringModifier2612    float64 = 0.35
	StriderBoostSineAmplitude2612      float32 = 1.15
	StriderSubmergedFloatDamping2612   float64 = 0.50
	StriderSubmergedFloatUpImpulse2612 float64 = 0.05
	SoundStriderStepLava2612                   = "minecraft:entity.strider.step_lava"
	SoundStriderStep2612                       = "minecraft:entity.strider.step"
)

// ============================================================================
// 1. PHANTOM HELPERS & NBT (26.1.2)
// ============================================================================

// PhantomDimensionsAndDamage2612 computes `(width, height, eyeHeight, attackDamage)`
// for a `minecraft:phantom` with `ID_SIZE = size` (`0..64`), matching
// `Phantom.getDefaultDimensions` (`scale(1.0F + 0.15F * size)`) and
// `Phantom.updatePhantomSizeInfo` (`ATTACK_DAMAGE = 6 + size`) in `26.1.2.jar`.
func PhantomDimensionsAndDamage2612(size int32) (width, height, eyeHeight, attackDamage float32) {
	if size < 0 {
		size = 0
	}
	if size > PhantomMaxSize {
		size = PhantomMaxSize
	}
	scale := float32(1.0) + PhantomSizeScaleStep2612*float32(size)
	return PhantomBaseWidth2612 * scale,
		PhantomBaseHeight2612 * scale,
		PhantomBaseEyeHeight2612 * scale,
		PhantomAttackDamage + float32(size)
}

// computePhantomAnchorAboveTarget2612 implements `PhantomAttackStrategyGoal.setAnchorAboveTarget()`:
// `anchorPoint = target.blockPosition().above(20 + random.nextInt(20))`, and if
// `anchorPoint.getY() < seaLevel`, clamps `anchorPoint.Y = seaLevel + 1` (`64`).
func computePhantomAnchorAboveTarget2612(targetX, targetY, targetZ float64, seaLevel int, rng *rand.Rand) (ax, ay, az float64) {
	bx := int(math.Floor(targetX))
	by := int(math.Floor(targetY))
	bz := int(math.Floor(targetZ))
	above := 20
	if rng != nil {
		above += rng.Intn(20) // 20..39
	} else {
		above = 22
	}
	anchorY := by + above
	if anchorY < seaLevel {
		anchorY = seaLevel + 1
	}
	return float64(bx), float64(anchorY), float64(bz)
}

func nextPhantomResweepDelay2612(rng *rand.Rand) int {
	sec := PhantomResweepMinSeconds2612
	if rng != nil {
		sec += rng.Intn(PhantomResweepSpanSeconds2612) // 8..11 seconds -> 160..220 ticks
	}
	return sec * 20
}

// initPhantomCircleGoal2612 implements `PhantomCircleAroundAnchorGoal.start()` (`Phantom.java:1770-1804`).
func initPhantomCircleGoal2612(st *PhantomState, rng *rand.Rand) {
	if st == nil {
		return
	}
	if rng != nil {
		st.CircleDistance = 5.0 + float64(rng.Float32())*10.0 // 5.0..15.0
		st.CircleHeight = -4.0 + float64(rng.Float32())*9.0   // -4.0..5.0
		if rng.Intn(2) == 0 {
			st.Clockwise = 1.0
		} else {
			st.Clockwise = -1.0
		}
	} else {
		st.CircleDistance = PhantomCircleRadius
		st.CircleHeight = 0.0
		st.Clockwise = 1.0
	}
	st.CircleInitialized = true
	selectNextPhantomCirclePoint2612(st)
}

// selectNextPhantomCirclePoint2612 implements `PhantomCircleAroundAnchorGoal.selectNext()` (`Phantom.java:1934-1986`).
func selectNextPhantomCirclePoint2612(st *PhantomState) {
	if st == nil {
		return
	}
	if st.CircleDistance < 5.0 {
		st.CircleDistance = PhantomCircleRadius
	}
	if st.Clockwise == 0 {
		st.Clockwise = 1.0
	}
	st.OrbitAngle += st.Clockwise * 15.0 * (math.Pi / 180.0)
	st.MoveTargetX = st.AnchorX + st.CircleDistance*math.Cos(st.OrbitAngle)
	st.MoveTargetY = st.AnchorY - 4.0 + st.CircleHeight
	st.MoveTargetZ = st.AnchorZ + st.CircleDistance*math.Sin(st.OrbitAngle)
}

// stepPhantomCircleGoal2612 implements `PhantomCircleAroundAnchorGoal.tick()` (`Phantom.java:1806-1932`).
func stepPhantomCircleGoal2612(cache *world.Cache, ent *world.Entity, st *PhantomState, rng *rand.Rand) {
	if st == nil || ent == nil {
		return
	}
	if !st.CircleInitialized {
		initPhantomCircleGoal2612(st, rng)
	}
	if rng != nil {
		if rng.Intn(350) == 0 {
			st.CircleHeight = -4.0 + float64(rng.Float32())*9.0
		}
		if rng.Intn(250) == 0 {
			st.CircleDistance += 1.0
			if st.CircleDistance > 15.0 {
				st.CircleDistance = 5.0
				st.Clockwise = -st.Clockwise
			}
		}
		if rng.Intn(450) == 0 {
			st.OrbitAngle = float64(rng.Float32()) * 2.0 * math.Pi
			selectNextPhantomCirclePoint2612(st)
		}
	}
	dx := st.MoveTargetX - ent.X
	dy := st.MoveTargetY - ent.Y
	dz := st.MoveTargetZ - ent.Z
	if dx*dx+dy*dy+dz*dz < 4.0 {
		selectNextPhantomCirclePoint2612(st)
	}
	if cache != nil {
		bx, by, bz := int(math.Floor(ent.X)), int(math.Floor(ent.Y)), int(math.Floor(ent.Z))
		if st.MoveTargetY < ent.Y && by-1 >= world.MinY && cache.GetBlock(bx, by-1, bz) != world.StateAir {
			if st.CircleHeight < 1.0 {
				st.CircleHeight = 1.0
			}
			selectNextPhantomCirclePoint2612(st)
		}
		if st.MoveTargetY > ent.Y && by+1 < world.MinY+world.WorldHeight && cache.GetBlock(bx, by+1, bz) != world.StateAir {
			if st.CircleHeight > -1.0 {
				st.CircleHeight = -1.0
			}
			selectNextPhantomCirclePoint2612(st)
		}
	}
}

func wrapDegrees2612(deg float64) float64 {
	d := math.Mod(deg, 360.0)
	if d >= 180.0 {
		d -= 360.0
	}
	if d < -180.0 {
		d += 360.0
	}
	return d
}

func degreesDifferenceAbs2612(a, b float64) float64 {
	return math.Abs(wrapDegrees2612(b - a))
}

func approachDegrees2612(cur, target, maxStep float64) float64 {
	diff := wrapDegrees2612(target - cur)
	if diff > maxStep {
		diff = maxStep
	} else if diff < -maxStep {
		diff = -maxStep
	}
	return wrapDegrees2612(cur + diff)
}

func approachFloat2612(cur, target, step float64) float64 {
	if cur < target {
		if cur+step > target {
			return target
		}
		return cur + step
	}
	if cur-step < target {
		return target
	}
	return cur - step
}

// stepPhantomMoveControl2612 implements `PhantomMoveControl.tick()` + flying movement (`Phantom.java:2023-2267`).
func stepPhantomMoveControl2612(cache *world.Cache, ent *world.Entity, st *PhantomState) (nx, ny, nz float64, yaw, pitch float32, horizCollided bool) {
	if ent == nil || st == nil {
		return 0, 0, 0, 0, 0, false
	}
	if st.MoveSpeed <= 0 {
		st.MoveSpeed = 0.1
	}
	curYaw := float64(ent.Yaw)
	curPitch := float64(ent.Pitch)

	dx := st.MoveTargetX - ent.X
	dy := st.MoveTargetY - ent.Y
	dz := st.MoveTargetZ - ent.Z
	horiz := math.Hypot(dx, dz)

	if horiz > 1e-5 {
		scale := 1.0 - math.Abs(dy*0.7)/horiz
		dx *= scale
		dz *= scale
		horiz = math.Hypot(dx, dz)
		dist3D := math.Sqrt(dx*dx + dy*dy + dz*dz)
		if dist3D > 1e-6 {
			oldYaw := curYaw
			targetAngleDeg := wrapDegrees2612(math.Atan2(dz, dx) * (180.0 / math.Pi))
			wrappedCur := wrapDegrees2612(curYaw + 90.0)
			curYaw = approachDegrees2612(wrappedCur, targetAngleDeg, 4.0) - 90.0

			if degreesDifferenceAbs2612(oldYaw, curYaw) < 3.0 {
				step := 0.005 * (1.8 / st.MoveSpeed)
				st.MoveSpeed = approachFloat2612(st.MoveSpeed, 1.8, step)
			} else {
				st.MoveSpeed = approachFloat2612(st.MoveSpeed, 0.2, 0.025)
			}

			curPitch = -(math.Atan2(-dy, horiz) * (180.0 / math.Pi))
			headingRad := (curYaw + 90.0) * (math.Pi / 180.0)
			pitchRad := curPitch * (math.Pi / 180.0)

			wantVX := st.MoveSpeed * math.Cos(headingRad) * math.Abs(dx/dist3D)
			wantVZ := st.MoveSpeed * math.Sin(headingRad) * math.Abs(dz/dist3D)
			wantVY := st.MoveSpeed * math.Sin(pitchRad) * math.Abs(dy/dist3D)

			st.VX += (wantVX - st.VX) * 0.2
			st.VY += (wantVY - st.VY) * 0.2
			st.VZ += (wantVZ - st.VZ) * 0.2
		}
	}

	candX := ent.X + st.VX
	candY := ent.Y + st.VY
	candZ := ent.Z + st.VZ

	if cache != nil {
		bx, by, bz := int(math.Floor(candX)), int(math.Floor(ent.Y)), int(math.Floor(candZ))
		if by >= world.MinY && by < world.MinY+world.WorldHeight {
			blk := cache.GetBlock(bx, by, bz)
			if blk != world.StateAir && !isPassableBlock(blk) && blk != world.StateWater {
				horizCollided = true
				candX = ent.X
				candZ = ent.Z
				st.VX = 0
				st.VZ = 0
				curYaw = wrapDegrees2612(curYaw + 180.0)
				st.MoveSpeed = 0.1
			}
		}
		byVert := int(math.Floor(candY))
		if byVert >= world.MinY && byVert < world.MinY+world.WorldHeight {
			blkY := cache.GetBlock(int(math.Floor(candX)), byVert, int(math.Floor(candZ)))
			if blkY != world.StateAir && !isPassableBlock(blkY) && blkY != world.StateWater {
				candY = ent.Y
				st.VY = 0
			}
		}
	}

	// `travelFlying` air drag (`0.91` per tick after moving):
	st.VX *= 0.91
	st.VY *= 0.91
	st.VZ *= 0.91

	return candX, candY, candZ, float32(curYaw), float32(curPitch), horizCollided
}

// phantomSwoopIntersectsPlayer2612 implements `PhantomSweepAttackGoal.tick()`:
// `this.getBoundingBox().inflate(0.2D).intersects(target.getBoundingBox())` using
// size-scaled Phantom dimensions (`0.9*(1+0.15*size)` x `0.5*(1+0.15*size)`).
func phantomSwoopIntersectsPlayer2612(px, py, pz float64, size int32, targetSnap PlayerSnapshot) bool {
	w, h, _, _ := PhantomDimensionsAndDamage2612(size)
	halfW := float64(w)*0.5 + 0.2
	minPX, maxPX := px-halfW, px+halfW
	minPY, maxPY := py-0.2, py+float64(h)+0.2
	minPZ, maxPZ := pz-halfW, pz+halfW

	const playerHalfW = 0.3
	const playerHeight = 1.8
	minTX, maxTX := targetSnap.X-playerHalfW, targetSnap.X+playerHalfW
	minTY, maxTY := targetSnap.Y, targetSnap.Y+playerHeight
	minTZ, maxTZ := targetSnap.Z-playerHalfW, targetSnap.Z+playerHalfW

	return maxPX > minTX && minPX < maxTX &&
		maxPY > minTY && minPY < maxTY &&
		maxPZ > minTZ && minPZ < maxTZ
}

// findPhantomNearestAttackablePlayer2612 implements `PhantomAttackPlayerTargetGoal.canUse()`:
// scans Survival/Adventure players in `boundingBox.inflate(16.0, 64.0, 16.0)` within `64.0`
// combat range, sorted by descending Y (`Comparator.comparing(Entity::getY).reversed()`).
func (s *Server) findPhantomNearestAttackablePlayer2612(dim string, px, py, pz float64, size int32) *PlayerSession {
	if s == nil {
		return nil
	}
	normDim := world.NormalizeDimension(dim)
	w, h, _, _ := PhantomDimensionsAndDamage2612(size)
	halfW := float64(w) * 0.5
	minX, maxX := px-halfW-16.0, px+halfW+16.0
	minY, maxY := py-64.0, py+float64(h)+64.0
	minZ, maxZ := pz-halfW-16.0, pz+halfW+16.0

	var best *PlayerSession
	bestY := -1e18
	for _, p := range s.onlinePlayersSnapshot() {
		if p == nil || p.IsDead() {
			continue
		}
		snap := p.Snapshot()
		if snap.Health <= 0 || snap.GameMode == GameModeCreative || snap.GameMode == GameModeSpectator {
			continue
		}
		if world.NormalizeDimension(snap.Dimension) != normDim {
			continue
		}
		if snap.X+0.3 < minX || snap.X-0.3 > maxX ||
			snap.Y+1.8 < minY || snap.Y > maxY ||
			snap.Z+0.3 < minZ || snap.Z-0.3 > maxZ {
			continue
		}
		dx, dy, dz := snap.X-px, snap.Y-py, snap.Z-pz
		if dx*dx+dy*dy+dz*dz > PhantomAggroRange*PhantomAggroRange {
			continue
		}
		if best == nil || snap.Y > bestY {
			best = p
			bestY = snap.Y
		}
	}
	return best
}

// EncodePhantomEntityNBT serializes `"anchor_pos"` (`BlockPos.CODEC` `IntArray{x, y, z}`)
// and `"size"` (`int`) matching `Phantom.addAdditionalSaveData` in `26.1.2.jar`.
func EncodePhantomEntityNBT(st PhantomState) *nbt.Compound {
	c := nbt.NewCompound()
	c.Set("anchor_pos", nbt.IntArray{
		int32(math.Floor(st.AnchorX)),
		int32(math.Floor(st.AnchorY)),
		int32(math.Floor(st.AnchorZ)),
	})
	sz := st.Size
	if sz < 0 {
		sz = 0
	}
	if sz > PhantomMaxSize {
		sz = PhantomMaxSize
	}
	c.Set("size", nbt.Int(sz))
	return c
}

// DecodePhantomEntityNBT parses `"anchor_pos"` and `"size"` matching `Phantom.readAdditionalSaveData`.
func DecodePhantomEntityNBT(c *nbt.Compound) (PhantomState, bool) {
	if c == nil {
		return PhantomState{}, false
	}
	var out PhantomState
	if v, ok := c.Get("anchor_pos"); ok {
		if ia, ok := v.(nbt.IntArray); ok && len(ia) == 3 {
			out.AnchorX = float64(ia[0])
			out.AnchorY = float64(ia[1])
			out.AnchorZ = float64(ia[2])
		}
	}
	if v, ok := c.Get("size"); ok {
		if iv, ok := v.(nbt.Int); ok {
			sz := int32(iv)
			if sz < 0 {
				sz = 0
			}
			if sz > PhantomMaxSize {
				sz = PhantomMaxSize
			}
			out.Size = sz
		}
	}
	return out, true
}

// SavePhantomEntityNBT exports a live `minecraft:phantom` state to NBT.
func (s *Server) SavePhantomEntityNBT(entityID int32) (*nbt.Compound, bool) {
	st, ok := s.GetPhantomState(entityID)
	if !ok {
		return nil, false
	}
	return EncodePhantomEntityNBT(st), true
}

// LoadPhantomEntityNBT restores `"anchor_pos"` and `"size"` onto a live `minecraft:phantom`.
func (s *Server) LoadPhantomEntityNBT(entityID int32, c *nbt.Compound) bool {
	if s == nil || s.entities == nil || entityID <= 0 {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypePhantom {
		return false
	}
	parsed, ok := DecodePhantomEntityNBT(c)
	if !ok {
		return false
	}
	mgr := s.phantomManager()
	mgr.mu.Lock()
	cur, exists := mgr.phantoms[entityID]
	if !exists || cur == nil {
		mgr.mu.Unlock()
		return false
	}
	cur.AnchorX = parsed.AnchorX
	cur.AnchorY = parsed.AnchorY
	cur.AnchorZ = parsed.AnchorZ
	cur.Size = parsed.Size
	dim := cur.Dimension
	mgr.mu.Unlock()

	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB, EncodePhantomSizeDataPacket(entityID, parsed.Size))
	return true
}

// ============================================================================
// 2. DROWNED HELPERS, THROWN TRIDENT PROJECTILE & GOAL AI (26.1.2)
// ============================================================================

// DrownedThrownTridentState2612 tracks an in-flight or grounded `ThrownTrident`
// (`minecraft:trident`) projectile thrown by a `minecraft:drowned` (`Pickup.DISALLOWED`).
type DrownedThrownTridentState2612 struct {
	EntityID       int32
	ShooterID      int32
	Dimension      string
	X, Y, Z        float64
	VX, VY, VZ     float64
	Grounded       bool
	GroundedTicks  int
	FlightTicks    int
	DealtDamage    bool
}

// IsDrownedThrownTrident2612 reports whether `entityID` is a Drowned-thrown `ThrownTrident`
// stepped by `tickDrownedThrownTridents2612`.
func (s *Server) IsDrownedThrownTrident2612(entityID int32) bool {
	if s == nil || entityID <= 0 {
		return false
	}
	mgr := s.undeadManager()
	mgr.mu.RLock()
	_, ok := mgr.drownedTridents[entityID]
	mgr.mu.RUnlock()
	return ok
}

// IsLevelBrightOutside2612 implements `Level.isBrightOutside()` for Overworld daytime
// without rain or thunder (`dimension == Overworld && !IsNight() && !IsRaining() && !IsThundering()`).
func IsLevelBrightOutside2612(s *Server, dimension string) bool {
	if s == nil || world.NormalizeDimension(dimension) != world.DimensionOverworld {
		return false
	}
	return !s.IsNight() && !s.IsRaining() && !s.IsThundering()
}

func isWaterBlockOrFluid2612(blk uint16) bool {
	if blk == world.StateWater || world.BlockName(blk) == "minecraft:bubble_column" {
		return true
	}
	if kind, _, _, _, _, ok := world.ClassifyFluidState(blk); ok && kind == world.FluidWater {
		return true
	}
	return false
}

// IsPlayerInWater2612 reports whether `p` is touching water (`Entity.isInWater()`).
func IsPlayerInWater2612(s *Server, p *PlayerSession) bool {
	if s == nil || p == nil || p.IsDead() {
		return false
	}
	snap := p.Snapshot()
	dim := world.NormalizeDimension(snap.Dimension)
	if _, fh, kind, sub := s.EntitySubmergedFluidDepth(dim, snap.X, snap.Y, snap.Z); sub && fh > 0 && kind == world.FluidWater {
		return true
	}
	cache := s.DimensionChunks(dim)
	if cache == nil {
		return false
	}
	bx, by, bz := int(math.Floor(snap.X)), int(math.Floor(snap.Y)), int(math.Floor(snap.Z))
	if by >= world.MinY && by < world.MinY+world.WorldHeight && isWaterBlockOrFluid2612(cache.GetBlock(bx, by, bz)) {
		return true
	}
	return false
}

// IsMobEntityInWater2612 reports whether `ent` is touching water (`Entity.isInWater()`).
func IsMobEntityInWater2612(s *Server, ent *world.Entity) bool {
	if s == nil || ent == nil {
		return false
	}
	dim := ent.EffectiveDimension()
	if _, fh, kind, sub := s.EntitySubmergedFluidDepth(dim, ent.X, ent.Y, ent.Z); sub && fh > 0 && kind == world.FluidWater {
		return true
	}
	cache := s.DimensionChunks(dim)
	if cache == nil {
		return false
	}
	bx, by, bz := int(math.Floor(ent.X)), int(math.Floor(ent.Y)), int(math.Floor(ent.Z))
	if by >= world.MinY && by < world.MinY+world.WorldHeight && isWaterBlockOrFluid2612(cache.GetBlock(bx, by, bz)) {
		return true
	}
	return false
}

// DrownedOkTargetPlayer2612 implements `Drowned.okTarget(LivingEntity)` (`Drowned.java:4388-4405`):
// returns `false` when `level.isBrightOutside() && !target.isInWater()`.
func DrownedOkTargetPlayer2612(s *Server, dim string, p *PlayerSession) bool {
	if s == nil || p == nil || p.IsDead() {
		return false
	}
	if IsLevelBrightOutside2612(s, dim) && !IsPlayerInWater2612(s, p) {
		return false
	}
	return true
}

// DrownedOkTargetMob2612 implements `Drowned.okTarget(LivingEntity)` for non-player mob targets.
func DrownedOkTargetMob2612(s *Server, dim string, target *world.Entity) bool {
	if s == nil || target == nil {
		return false
	}
	if IsLevelBrightOutside2612(s, dim) && !IsMobEntityInWater2612(s, target) {
		return false
	}
	return true
}

// RollDrownedDefaultEquipment2612 implements `Drowned.populateDefaultEquipmentSlots` +
// `Drowned.finalizeSpawn` (`Drowned.java:4083-4104, 4329-4359`):
//   - MainHand: if `nextFloat() > 0.9` (`10%`), `nextInt(16) < 10` -> `minecraft:trident` (`6.25%`),
//     else `minecraft:fishing_rod` (`3.75%`).
//   - OffHand: if `nextFloat() < 0.03` (`3%`), `minecraft:nautilus_shell` with guaranteed `100%` drop.
func RollDrownedDefaultEquipment2612(rng *rand.Rand) (mainHand, offHand world.ItemStack, guaranteedOffHand bool) {
	if rng == nil {
		return world.ItemStack{}, world.ItemStack{}, false
	}
	if float64(rng.Float32()) > 0.9 {
		if rng.Intn(16) < 10 {
			mainHand = world.NewNamedItemStack(ItemTrident, 1)
		} else {
			mainHand = world.NewNamedItemStack(ItemFishingRod, 1)
		}
	}
	if rng.Float32() < 0.03 {
		offHand = world.NewNamedItemStack(ItemNautilusShell, 1)
		guaranteedOffHand = true
	}
	return mainHand, offHand, guaranteedOffHand
}

// CanDrownedReplaceCurrentItem2612 implements `Drowned.canReplaceCurrentItem` & `wantsToPickUp`
// (`Drowned.java:4361-4375, 4666-4679`):
//   - Never drops/replaces a held `minecraft:nautilus_shell`;
//   - Never picks up `#minecraft:spears`;
//   - Prefers `#minecraft:drowned_preferred_weapons` (`minecraft:trident`).
func CanDrownedReplaceCurrentItem2612(newStack, currentStack world.ItemStack) bool {
	if newStack.IsEmpty() {
		return false
	}
	newID := canonicalResourceName(newStack.ResolvedID())
	curID := canonicalResourceName(currentStack.ResolvedID())
	if curID == ItemNautilusShell {
		return false
	}
	if len(newID) >= 6 && newID[len(newID)-6:] == "_spear" {
		return false
	}
	if currentStack.IsEmpty() {
		return true
	}
	if curID == ItemTrident && newID != ItemTrident {
		return false
	}
	if newID == ItemTrident && curID != ItemTrident {
		return true
	}
	newDmg, _ := StackAttackStats(newStack)
	curDmg, _ := StackAttackStats(currentStack)
	return newDmg > curDmg
}

// DrownedTridentInaccuracy2612 returns `14 - difficulty.getId() * 4` (`Drowned.java:4609-4617`):
// Easy = `10`, Normal = `6`, Hard = `2`.
func DrownedTridentInaccuracy2612(diff Difficulty) float64 {
	inacc := 14 - int(diff)*4
	if inacc < 0 {
		inacc = 0
	}
	return float64(inacc)
}

// SpawnDrownedThrownTrident2612 implements `Drowned.performRangedAttack` (`Drowned.java:4550-4633`):
// spawns an `EntityTypeTrident` (`minecraft:trident`) projectile with `1.6F` velocity,
// `(dx, target.getY(0.33333334) - trident.getY() + horizDist * 0.2, dz)` aim vector,
// `14 - difficulty.id * 4` inaccuracy, `AnimateSwingMainHand`, and `SoundDrownedShoot`.
func (s *Server) SpawnDrownedThrownTrident2612(drownedID int32, targetX, targetY, targetHeight, targetZ float64, rng *rand.Rand) (int32, bool) {
	if s == nil || s.entities == nil || drownedID <= 0 {
		return 0, false
	}
	ent := s.entities.Get(drownedID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeDrowned {
		return 0, false
	}
	dim := ent.EffectiveDimension()
	eyeH := 1.74
	if s.IsMobBaby(drownedID) {
		eyeH = 0.775
	}
	shootX := ent.X
	shootY := ent.Y + eyeH - 0.1
	shootZ := ent.Z

	dx := targetX - ent.X
	dy := (targetY + targetHeight/3.0) - shootY
	dz := targetZ - ent.Z
	horizDist := math.Hypot(dx, dz)
	aimY := dy + horizDist*0.2

	normLen := math.Sqrt(dx*dx + aimY*aimY + dz*dz)
	if normLen < 1e-6 {
		dx, aimY, dz = 1.0, 0.0, 0.0
		normLen = 1.0
	}
	ux, uy, uz := dx/normLen, aimY/normLen, dz/normLen
	inacc := DrownedTridentInaccuracy2612(s.Difficulty())
	if rng != nil && inacc > 0 {
		ux += rng.NormFloat64() * 0.0075 * inacc
		uy += rng.NormFloat64() * 0.0075 * inacc
		uz += rng.NormFloat64() * 0.0075 * inacc
		n2 := math.Sqrt(ux*ux + uy*uy + uz*uz)
		if n2 > 1e-6 {
			ux, uy, uz = ux/n2, uy/n2, uz/n2
		}
	}
	vx := ux * DrownedTridentProjectileSpeed2612
	vy := uy * DrownedTridentProjectileSpeed2612
	vz := uz * DrownedTridentProjectileSpeed2612

	yaw := yawToward(vx, vz)
	pitch := float32(-math.Atan2(vy, math.Hypot(vx, vz)) * (180.0 / math.Pi))

	typeID := registry.EntityTypeIndex(EntityTypeTrident)
	if typeID < 0 {
		typeID = EntityTypeArrowID
	}
	projID := s.entities.Add(&world.Entity{
		TypeID:    typeID,
		TypeName:  EntityTypeTrident,
		Dimension: dim,
		X:         shootX,
		Y:         shootY,
		Z:         shootZ,
		Yaw:       yaw,
		Pitch:     pitch,
		VelocityX: clampVelocityShort(vx),
		VelocityY: clampVelocityShort(vy),
		VelocityZ: clampVelocityShort(vz),
	})

	mgr := s.undeadManager()
	mgr.mu.Lock()
	if mgr.drownedTridents == nil {
		mgr.drownedTridents = make(map[int32]*DrownedThrownTridentState2612)
	}
	mgr.drownedTridents[projID] = &DrownedThrownTridentState2612{
		EntityID:  projID,
		ShooterID: drownedID,
		Dimension: dim,
		X:         shootX,
		Y:         shootY,
		Z:         shootZ,
		VX:        vx,
		VY:        vy,
		VZ:        vz,
	}
	mgr.mu.Unlock()

	cp := ent.ChunkPos()
	if pEnt := s.entities.Get(projID); pEnt != nil {
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, pEnt))
	}
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlayAnimateCB, EncodeAnimatePacket(drownedID, AnimateSwingMainHand))
	pitchSnd := float32(1.0)
	if rng != nil {
		pitchSnd = 1.0 / (rng.Float32()*0.4 + 0.8)
	}
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundDrownedShoot, SoundSourceHostile, ent.X, ent.Y+eyeH, ent.Z, 1.0, pitchSnd, 0))
	return projID, true
}

func (s *Server) tickDrownedThrownTridents2612(rng *rand.Rand) {
	if s == nil || s.entities == nil {
		return
	}
	mgr := s.undeadManager()
	mgr.mu.RLock()
	if len(mgr.drownedTridents) == 0 {
		mgr.mu.RUnlock()
		return
	}
	ids := make([]int32, 0, len(mgr.drownedTridents))
	for id := range mgr.drownedTridents {
		ids = append(ids, id)
	}
	mgr.mu.RUnlock()

	for _, id := range ids {
		s.stepSingleDrownedTrident2612(id, rng)
	}
}

func (s *Server) removeDrownedTrident2612(projID int32) {
	mgr := s.undeadManager()
	mgr.mu.Lock()
	delete(mgr.drownedTridents, projID)
	mgr.mu.Unlock()
	if ent := s.entities.Get(projID); ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, protocol.PlayRemoveEntities,
			encodeRemoveEntitiesPacket(nil, []int32{projID}))
	}
	s.entities.Remove(projID)
}

func (s *Server) stepSingleDrownedTrident2612(projID int32, rng *rand.Rand) {
	pEnt := s.entities.Get(projID)
	mgr := s.undeadManager()
	mgr.mu.Lock()
	tr, ok := mgr.drownedTridents[projID]
	if !ok || tr == nil || pEnt == nil {
		delete(mgr.drownedTridents, projID)
		mgr.mu.Unlock()
		return
	}
	if tr.Grounded {
		tr.GroundedTicks++
		despawn := tr.GroundedTicks >= 1200
		mgr.mu.Unlock()
		if despawn {
			s.removeDrownedTrident2612(projID)
		}
		return
	}
	tr.FlightTicks++
	snap := *tr
	mgr.mu.Unlock()

	if snap.FlightTicks > 200 {
		s.removeDrownedTrident2612(projID)
		return
	}

	dim := snap.Dimension
	cache := s.DimensionChunks(dim)
	const subSteps = 4
	curX, curY, curZ := snap.X, snap.Y, snap.Z
	stepVX, stepVY, stepVZ := snap.VX/subSteps, snap.VY/subSteps, snap.VZ/subSteps
	groundedNow := false
	dealtNow := snap.DealtDamage

	for step := 1; step <= subSteps; step++ {
		nx, ny, nz := curX+stepVX, curY+stepVY, curZ+stepVZ

		// 1. Check Survival/Adventure players along segment:
		if !dealtNow {
			for _, pl := range s.onlinePlayersSnapshot() {
				if pl == nil || pl.IsDead() {
					continue
				}
				pSnap := pl.Snapshot()
				if pSnap.GameMode == GameModeCreative || pSnap.GameMode == GameModeSpectator || world.NormalizeDimension(pSnap.Dimension) != dim {
					continue
				}
				if math.Abs(pSnap.X-nx) <= 0.6 && ny >= pSnap.Y-0.15 && ny <= pSnap.Y+1.95 && math.Abs(pSnap.Z-nz) <= 0.6 {
					dealtNow = true
					if s.DeflectProjectileWithShield(pl) {
						snap.VX = -snap.VX * 0.1
						snap.VY = -snap.VY * 0.1
						snap.VZ = -snap.VZ * 0.1
					} else {
						s.AttackPlayerFromMob(snap.ShooterID, MobTypeDrowned, curX, curZ, pl, DrownedTridentBaseDamage)
						snap.VX = -snap.VX * 0.01
						snap.VY = -snap.VY * 0.1
						snap.VZ = -snap.VZ * 0.01
					}
					break
				}
			}
		}

		// 2. Check non-shooter mobs / ShulkerBullets along segment:
		if !dealtNow {
			cx := int32(int64(math.Floor(nx)) >> 4)
			cz := int32(int64(math.Floor(nz)) >> 4)
			nearby := s.entities.AppendInChunkRange(nil, cx, cz, 1)
			for i := range nearby {
				cand := &nearby[i]
				if cand.ID == projID || cand.ID == snap.ShooterID || cand.EffectiveDimension() != dim ||
					cand.IsItem() || cand.IsExperienceOrb() || IsHangingEntity(cand.TypeName) {
					continue
				}
				cNorm := canonicalResourceName(cand.TypeName)
				if cNorm == EntityTypeShulkerBullet {
					if math.Abs(cand.X-nx) <= 0.8 && ny >= cand.Y-0.5 && ny <= cand.Y+0.8 && math.Abs(cand.Z-nz) <= 0.8 {
						s.DestroyShulkerBullet(cand.ID)
						dealtNow = true
						break
					}
					continue
				}
				if IsStandardProjectileEntityType(cand.TypeName) {
					continue
				}
				if cNorm == MobTypeEnderman {
					if math.Abs(cand.X-nx) <= 1.0 && ny >= cand.Y-0.2 && ny <= cand.Y+EndermanHeight && math.Abs(cand.Z-nz) <= 1.0 {
						s.TryEndermanDodgeProjectile(cand.ID, EntityTypeTrident, rng)
						continue
					}
				}
				if math.Abs(cand.X-nx) <= 0.8 && ny >= cand.Y-0.2 && ny <= cand.Y+2.0 && math.Abs(cand.Z-nz) <= 0.8 {
					dealtNow = true
					s.applyDamageToMob(cand, DrownedTridentBaseDamage, nil, BaseKnockbackStrength, false, false)
					snap.VX = -snap.VX * 0.01
					snap.VY = -snap.VY * 0.1
					snap.VZ = -snap.VZ * 0.01
					break
				}
			}
		}

		// 3. Check solid block impact (`ThrownTrident` passes cleanly through water & lava!):
		if cache != nil {
			bx, by, bz := int(math.Floor(nx)), int(math.Floor(ny)), int(math.Floor(nz))
			if by >= world.MinY && by < world.MinY+world.WorldHeight {
				blk := cache.GetBlock(bx, by, bz)
				if (!isPassableBlock(blk) && !isFluidProjectilePassable2612(blk)) ||
					world.IsWoodenButtonBlock(blk) || world.IsBellBlock(blk) || world.IsChorusFlowerBlock(blk) {
					_, _ = s.OnProjectileBlockImpact(dim, bx, by, bz, curX, curY, curZ, nx, ny, nz, projID, EntityTypeTrident)
					curX, curY, curZ = nx, ny, nz
					groundedNow = true
					break
				}
			}
		}
		curX, curY, curZ = nx, ny, nz
		if dealtNow {
			break
		}
	}

	// `ThrownTrident.getWaterInertia() = 0.99F` (matches air `0.99F`!) + `gravity = 0.05`:
	nextVX, nextVY, nextVZ := snap.VX*0.99, (snap.VY-0.05)*0.99, snap.VZ*0.99
	if groundedNow {
		nextVX, nextVY, nextVZ = 0, 0, 0
	}

	mgr.mu.Lock()
	if cur, ok := mgr.drownedTridents[projID]; ok && cur != nil {
		cur.X, cur.Y, cur.Z = curX, curY, curZ
		cur.VX, cur.VY, cur.VZ = nextVX, nextVY, nextVZ
		cur.Grounded = groundedNow
		cur.DealtDamage = dealtNow
	}
	mgr.mu.Unlock()

	s.entities.Update(projID, func(e *world.Entity) {
		e.X, e.Y, e.Z = curX, curY, curZ
		e.VelocityX = clampVelocityShort(nextVX)
		e.VelocityY = clampVelocityShort(nextVY)
		e.VelocityZ = clampVelocityShort(nextVZ)
		e.OnGround = groundedNow
	})
}

func findDrownedNearbyWaterPos2612(cache *world.Cache, bx, by, bz int, rng *rand.Rand) (wx, wy, wz float64, found bool) {
	if cache == nil {
		return 0, 0, 0, false
	}
	// First try exact vanilla 10 random samples in `[-10..9] x [-5..2] x [-10..9]`:
	if rng != nil {
		for i := 0; i < 10; i++ {
			ox := bx + rng.Intn(20) - 10
			oy := by + 2 - rng.Intn(8)
			oz := bz + rng.Intn(20) - 10
			if oy >= world.MinY && oy < world.MinY+world.WorldHeight && isWaterBlockOrFluid2612(cache.GetBlock(ox, oy, oz)) {
				return float64(ox) + 0.5, float64(oy), float64(oz) + 0.5, true
			}
		}
	}
	// Deterministic closest-match scan within `[-10..10] x [-5..2] x [-10..10]` so short unit tests never flake on RNG:
	bestDistSq := 1e18
	for dy := -5; dy <= 2; dy++ {
		oy := by + dy
		if oy < world.MinY || oy >= world.MinY+world.WorldHeight {
			continue
		}
		for dx := -10; dx <= 10; dx++ {
			ox := bx + dx
			for dz := -10; dz <= 10; dz++ {
				oz := bz + dz
				if !isWaterBlockOrFluid2612(cache.GetBlock(ox, oy, oz)) {
					continue
				}
				dSq := float64(dx*dx + dy*dy + dz*dz)
				if dSq < bestDistSq {
					bestDistSq = dSq
					wx, wy, wz = float64(ox)+0.5, float64(oy), float64(oz)+0.5
					found = true
				}
			}
		}
	}
	return wx, wy, wz, found
}

func findDrownedNearbyBeachPos2612(cache *world.Cache, bx, by, bz int) (tx, ty, tz float64, found bool) {
	if cache == nil {
		return 0, 0, 0, false
	}
	bestDistSq := 1e18
	for dy := -2; dy <= 2; dy++ {
		oy := by + dy
		if oy < world.MinY || oy+2 >= world.MinY+world.WorldHeight {
			continue
		}
		for dx := -8; dx <= 8; dx++ {
			ox := bx + dx
			for dz := -8; dz <= 8; dz++ {
				oz := bz + dz
				ground := cache.GetBlock(ox, oy, oz)
				if ground == world.StateAir || isPassableBlock(ground) || isWaterBlockOrFluid2612(ground) || IsLavaFluidBlock2612(ground) {
					continue
				}
				if cache.GetBlock(ox, oy+1, oz) != world.StateAir || cache.GetBlock(ox, oy+2, oz) != world.StateAir {
					continue
				}
				dSq := float64(dx*dx + dy*dy + dz*dz)
				if dSq < bestDistSq {
					bestDistSq = dSq
					tx, ty, tz = float64(ox)+0.5, float64(oy+1), float64(oz)+0.5
					found = true
				}
			}
		}
	}
	return tx, ty, tz, found
}

func findNearbyTurtleEggForZombie2612(cache *world.Cache, bx, by, bz int) (ex, ey, ez int, found bool) {
	if cache == nil {
		return 0, 0, 0, false
	}
	bestDistSq := 1e18
	for dy := -2; dy <= 2; dy++ {
		oy := by + dy
		if oy < world.MinY || oy+2 >= world.MinY+world.WorldHeight {
			continue
		}
		for dx := -12; dx <= 12; dx++ {
			ox := bx + dx
			for dz := -12; dz <= 12; dz++ {
				oz := bz + dz
				if world.BlockName(cache.GetBlock(ox, oy, oz)) != TurtleEggBlockName {
					continue
				}
				if cache.GetBlock(ox, oy+1, oz) != world.StateAir || cache.GetBlock(ox, oy+2, oz) != world.StateAir {
					continue
				}
				dSq := float64(dx*dx + dy*dy + dz*dz)
				if dSq < bestDistSq {
					bestDistSq = dSq
					ex, ey, ez = ox, oy, oz
					found = true
				}
			}
		}
	}
	return ex, ey, ez, found
}

func (s *Server) moveDrownedToward2612(ent *world.Entity, cache *world.Cache, inWater bool, goalX, goalY, goalZ, speed, stopHorizDist float64) {
	if s == nil || ent == nil {
		return
	}
	dx := goalX - ent.X
	dy := goalY - ent.Y
	dz := goalZ - ent.Z
	horiz := math.Hypot(dx, dz)
	yaw := ent.Yaw
	if horiz > 1e-4 {
		yaw = yawToward(dx, dz)
	}
	nx, ny, nz := ent.X, ent.Y, ent.Z
	if inWater {
		dist3D := math.Sqrt(dx*dx + dy*dy + dz*dz)
		if dist3D > stopHorizDist && dist3D > 1e-4 {
			step := speed
			if step > dist3D {
				step = dist3D
			}
			nx += (dx / dist3D) * step
			ny += (dy / dist3D) * step
			nz += (dz / dist3D) * step
		}
		// `Attributes.STEP_HEIGHT = 1.0D` when climbing out onto beach:
		if cache != nil {
			bx, by, bz := int(math.Floor(nx)), int(math.Floor(ny)), int(math.Floor(nz))
			if by >= world.MinY && by+2 < world.MinY+world.WorldHeight {
				blk := cache.GetBlock(bx, by, bz)
				if blk != world.StateAir && !isPassableBlock(blk) && !isWaterBlockOrFluid2612(blk) &&
					cache.GetBlock(bx, by+1, bz) == world.StateAir && cache.GetBlock(bx, by+2, bz) == world.StateAir {
					ny = float64(by + 1)
				}
			}
		}
	} else if horiz > stopHorizDist && horiz > 1e-4 {
		step := speed
		if step > horiz {
			step = horiz
		}
		candX := ent.X + (dx/horiz)*step
		candZ := ent.Z + (dz/horiz)*step
		feetY := int(math.Floor(ent.Y))
		if cache != nil {
			if sx, sy, sz, ok := tryApplyHorizontalStep(cache, ent.X, feetY, ent.Z, candX, candZ); ok {
				nx, ny, nz = sx, float64(sy), sz
			} else {
				nx, nz = candX, candZ
			}
		} else {
			nx, nz = candX, candZ
		}
	}
	s.entities.Update(ent.ID, func(e *world.Entity) {
		e.X, e.Y, e.Z, e.Yaw, e.HeadYaw = nx, ny, nz, yaw, yaw
	})
}

func (s *Server) selectDrownedTarget2612(ent *world.Entity, cache *world.Cache) (bestPlayer *PlayerSession, bestMob *world.Entity, targetInWater bool) {
	dim := ent.EffectiveDimension()
	eyeH := 1.74
	if s.IsMobBaby(ent.ID) {
		eyeH = 0.775
	}
	bestDistSq := ZombieAggroRange*ZombieAggroRange + 1.0

	// Priority 2: `Player` with `Drowned::okTarget`:
	for _, p := range s.onlinePlayersSnapshot() {
		if !DrownedOkTargetPlayer2612(s, dim, p) {
			continue
		}
		pSnap := p.Snapshot()
		if pSnap.GameMode == GameModeCreative || pSnap.GameMode == GameModeSpectator || world.NormalizeDimension(pSnap.Dimension) != dim {
			continue
		}
		dx, dy, dz := pSnap.X-ent.X, pSnap.Y-ent.Y, pSnap.Z-ent.Z
		dSq := dx*dx + dy*dy + dz*dz
		if dSq <= ZombieAggroRange*ZombieAggroRange && dSq < bestDistSq &&
			hasColliderLineOfSight2612(cache, ent.X, ent.Y+eyeH, ent.Z, pSnap.X, pSnap.Y+1.62, pSnap.Z) {
			bestDistSq = dSq
			bestPlayer = p
			targetInWater = IsPlayerInWater2612(s, p)
		}
	}
	if bestPlayer != nil {
		return bestPlayer, nil, targetInWater
	}

	// Priority 3 & 5: `AbstractVillager` (`villager`, `wandering_trader`), `IronGolem`, `Axolotl`, and baby `Turtle` on land!
	allEnts := s.entities.AppendAll(nil)
	for i := range allEnts {
		other := &allEnts[i]
		if other.ID == ent.ID || other.EffectiveDimension() != dim {
			continue
		}
		oNorm := canonicalResourceName(other.TypeName)
		validMob := false
		switch oNorm {
		case MobTypeVillager, MobTypeWanderingTrader, MobTypeIronGolem, MobTypeAxolotl:
			validMob = DrownedOkTargetMob2612(s, dim, other)
		case MobTypeTurtle:
			// `Turtle.BABY_ON_LAND_SELECTOR`: `isBaby() && !isInWater()`
			validMob = s.IsMobBaby(other.ID) && !IsMobEntityInWater2612(s, other) && DrownedOkTargetMob2612(s, dim, other)
		}
		if !validMob {
			continue
		}
		dx, dy, dz := other.X-ent.X, other.Y-ent.Y, other.Z-ent.Z
		dSq := dx*dx + dy*dy + dz*dz
		if dSq <= ZombieAggroRange*ZombieAggroRange && dSq < bestDistSq &&
			hasColliderLineOfSight2612(cache, ent.X, ent.Y+eyeH, ent.Z, other.X, other.Y+0.8, other.Z) {
			bestDistSq = dSq
			cpMob := *other
			bestMob = &cpMob
			targetInWater = IsMobEntityInWater2612(s, other)
		}
	}
	return nil, bestMob, targetInWater
}

// tickSingleDrowned2612 steps a single `minecraft:drowned` by 1 game tick (20 TPS)
// with full vanilla 26.1.2 goal priorities.
func (s *Server) tickSingleDrowned2612(ent *world.Entity, rng *rand.Rand) {
	if s == nil || ent == nil || s.Difficulty() == DifficultyPeaceful {
		return
	}
	dim := ent.EffectiveDimension()
	cache := s.DimensionChunks(dim)
	s.ensureUndeadState(ent.ID, MobTypeDrowned, rng)

	inWater := IsMobEntityInWater2612(s, ent)
	brightOutside := IsLevelBrightOutside2612(s, dim)

	mgr := s.undeadManager()
	mgr.mu.Lock()
	st := mgr.undead[ent.ID]
	if st == nil {
		mgr.mu.Unlock()
		return
	}
	if st.AttackCooldown > 0 {
		st.AttackCooldown--
	}
	hasTrident := canonicalResourceName(st.MainHand.ResolvedID()) == ItemTrident
	meleeCD := st.AttackCooldown
	mgr.mu.Unlock()

	bx, by, bz := int(math.Floor(ent.X)), int(math.Floor(ent.Y)), int(math.Floor(ent.Z))

	// Goal Priority 1: `DrownedGoToWaterGoal` (`isBrightOutside() && !isInWater()`):
	// preempts `MOVE` goals (including `DrownedTridentAttackGoal` & `DrownedAttackGoal`) so
	// daytime Drowned on land seeks water unless a water-submerged target is already in reach!
	if brightOutside && !inWater {
		if wx, wy, wz, ok := findDrownedNearbyWaterPos2612(cache, bx, by, bz, rng); ok {
			mgr.mu.Lock()
			if cur := mgr.undead[ent.ID]; cur != nil {
				cur.UsingTrident = false
				cur.Aggressive = false
				cur.RangedAttackTime = -1
				cur.SearchingForLand = false
				cur.Swimming = false
			}
			mgr.mu.Unlock()
			s.moveDrownedToward2612(ent, cache, false, wx, wy, wz, DrownedLandSpeed, 0.25)
			return
		}
	}

	// Target acquisition (`okTarget` enforced!):
	targetPlayer, targetMob, targetInWater := s.selectDrownedTarget2612(ent, cache)
	hasTarget := targetPlayer != nil || targetMob != nil

	// Goal Priority 2: `DrownedTridentAttackGoal(1.0, 40, 10.0F)` & `DrownedAttackGoal`:
	if hasTarget {
		var tx, ty, tz, tHeight float64
		if targetPlayer != nil {
			pSnap := targetPlayer.Snapshot()
			tx, ty, tz, tHeight = pSnap.X, pSnap.Y, pSnap.Z, 1.8
		} else {
			tx, ty, tz, tHeight = targetMob.X, targetMob.Y, targetMob.Z, 1.4
		}

		speed := DrownedLandSpeed
		if inWater {
			speed = DrownedSwimSpeed
		}
		dx, dy, dz := tx-ent.X, ty-ent.Y, tz-ent.Z
		horizDist := math.Hypot(dx, dz)
		dist3D := math.Sqrt(dx*dx + dy*dy + dz*dz)
		wantsSwim := inWater && targetInWater

		// Switch between Ranged Trident (`dist > 2.0` when holding trident) and Melee (`<= ZombieAttackReach` or unarmed):
		if hasTrident && horizDist > ZombieAttackReach && dist3D > DrownedTridentMinRangedDist2612 {
			if dist3D > DrownedTridentAttackRadius2612 {
				s.moveDrownedToward2612(ent, cache, inWater, tx, ty, tz, speed, DrownedTridentAttackRadius2612-0.5)
			} else {
				yaw := yawToward(dx, dz)
				s.entities.Update(ent.ID, func(e *world.Entity) {
					e.Yaw, e.HeadYaw = yaw, yaw
				})
			}

			eyeH := 1.74
			if s.IsMobBaby(ent.ID) {
				eyeH = 0.775
			}
			canSee := hasColliderLineOfSight2612(cache, ent.X, ent.Y+eyeH, ent.Z, tx, ty+tHeight*0.8, tz)

			shouldFire := false
			mgr.mu.Lock()
			if cur := mgr.undead[ent.ID]; cur != nil {
				cur.UsingTrident = true
				cur.Aggressive = true
				cur.SearchingForLand = false
				cur.Swimming = wantsSwim
				if canSee {
					cur.RangedSeeTime++
				} else {
					cur.RangedSeeTime = 0
				}
				// Exact `RangedAttackGoal.tick()` cadence:
				// starts at `-1` -> sets `attackTime = 40` on tick 1 (does NOT fire on tick 1!),
				// then decrements `attackTime` each tick and fires when `attackTime == 0`!
				if cur.RangedAttackTime < 0 {
					cur.RangedAttackTime = DrownedTridentAttackIntervalTicks2612
				} else {
					cur.RangedAttackTime--
					if cur.RangedAttackTime <= 0 {
						if canSee && dist3D <= DrownedTridentAttackRadius2612 {
							shouldFire = true
						}
						cur.RangedAttackTime = DrownedTridentAttackIntervalTicks2612
					}
				}
			}
			mgr.mu.Unlock()

			if shouldFire {
				curEnt := s.entities.Get(ent.ID)
				if curEnt != nil {
					_, _ = s.SpawnDrownedThrownTrident2612(ent.ID, tx, ty, tHeight, tz, rng)
				}
			}
			return
		}

		// Melee fallback (`DrownedAttackGoal`): either unarmed/fishing_rod OR target within `<= 2.0` blocks!
		mgr.mu.Lock()
		if cur := mgr.undead[ent.ID]; cur != nil {
			cur.UsingTrident = false
			cur.Aggressive = true
			cur.RangedAttackTime = -1
			cur.SearchingForLand = false
			cur.Swimming = wantsSwim
		}
		mgr.mu.Unlock()

		s.moveDrownedToward2612(ent, cache, inWater, tx, ty, tz, speed, ZombieAttackStopDist)
		curEnt := s.entities.Get(ent.ID)
		if curEnt == nil {
			return
		}
		mdx, mdy, mdz := tx-curEnt.X, ty-curEnt.Y, tz-curEnt.Z
		if math.Hypot(mdx, mdz) <= ZombieAttackReach && math.Abs(mdy) <= ZombieAttackVerticalReach && meleeCD <= 0 {
			mgr.mu.Lock()
			if cur := mgr.undead[ent.ID]; cur != nil {
				cur.AttackCooldown = ZombieAttackCooldownTicks
			}
			mgr.mu.Unlock()
			cp := curEnt.ChunkPos()
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlayAnimateCB, EncodeAnimatePacket(ent.ID, AnimateSwingMainHand))
			if targetPlayer != nil {
				s.AttackPlayerFromMob(ent.ID, MobTypeDrowned, curEnt.X, curEnt.Z, targetPlayer, ZombieAttackDamage)
			} else if targetMob != nil {
				s.applyDamageToMob(targetMob, ZombieAttackDamage, nil, BaseKnockbackStrength, false, false)
			}
		}
		return
	}

	// Reset combat flags when idle:
	mgr.mu.Lock()
	if cur := mgr.undead[ent.ID]; cur != nil {
		cur.UsingTrident = false
		cur.Aggressive = false
		cur.RangedAttackTime = -1
		cur.RangedSeeTime = 0
	}
	mgr.mu.Unlock()

	// Goal Priority 4: `ZombieAttackTurtleEggGoal` (`RemoveBlockGoal(Blocks.TURTLE_EGG)`):
	if s.MobGriefing() && s.CreeperBlockGriefing() {
		if ex, ey, ez, ok := findNearbyTurtleEggForZombie2612(cache, bx, by, bz); ok {
			goalX, goalY, goalZ := float64(ex)+0.5, float64(ey), float64(ez)+0.5
			dist := math.Hypot(goalX-ent.X, goalZ-ent.Z)
			if dist > 1.14 {
				spd := DrownedLandSpeed
				if inWater {
					spd = DrownedSwimSpeed
				}
				s.moveDrownedToward2612(ent, cache, inWater, goalX, goalY, goalZ, spd, 0.5)
			} else {
				mgr.mu.Lock()
				trampleTicks := 0
				if cur := mgr.undead[ent.ID]; cur != nil {
					if !cur.HasTrampleEgg || cur.TrampleEggX != ex || cur.TrampleEggY != ey || cur.TrampleEggZ != ez {
						cur.HasTrampleEgg = true
						cur.TrampleEggX, cur.TrampleEggY, cur.TrampleEggZ = ex, ey, ez
						cur.TrampleEggTicks = 0
					}
					cur.TrampleEggTicks++
					trampleTicks = cur.TrampleEggTicks
				}
				mgr.mu.Unlock()

				hopVY := 0.3
				if trampleTicks%2 == 0 {
					hopVY = -0.3
				}
				s.entities.Update(ent.ID, func(e *world.Entity) {
					e.VelocityY = clampVelocityShort(hopVY)
				})
				if trampleTicks%6 == 0 {
					cp := ent.ChunkPos()
					s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
						EncodeSoundPacket(SoundZombieDestroyEgg2612, SoundSourceHostile, goalX, goalY, goalZ, 0.5, 1.0, 0))
				}
				if trampleTicks > DrownedTurtleEggDestroyThreshold2612 && cache != nil {
					cache.SetBlock(ex, ey, ez, world.StateAir)
					s.broadcastMobBlockUpdate(dim, ex, ey, ez, world.StateAir)
					cp := ent.ChunkPos()
					s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
						EncodeSoundPacket(SoundTurtleEggBreak2612, SoundSourceBlocks, goalX, goalY, goalZ, 0.7, 1.0, 0))
					mgr.mu.Lock()
					if cur := mgr.undead[ent.ID]; cur != nil {
						cur.HasTrampleEgg = false
						cur.TrampleEggTicks = 0
					}
					mgr.mu.Unlock()
				}
			}
			return
		}
	}

	// Goal Priority 5: `DrownedGoToBeachGoal` (`!isBrightOutside && inWater && Y >= seaLevel - 3`):
	if !brightOutside && inWater && ent.Y >= float64(OverworldSeaLevel2612-3) {
		if bxGoal, byGoal, bzGoal, ok := findDrownedNearbyBeachPos2612(cache, bx, by, bz); ok {
			mgr.mu.Lock()
			if cur := mgr.undead[ent.ID]; cur != nil {
				cur.SearchingForLand = false
				cur.Swimming = false
			}
			mgr.mu.Unlock()
			s.moveDrownedToward2612(ent, cache, true, bxGoal, byGoal, bzGoal, DrownedSwimSpeed, 0.2)
			return
		}
	}

	// Goal Priority 6: `DrownedSwimUpGoal` (`!isBrightOutside && inWater && Y < seaLevel - 2`):
	if !brightOutside && inWater && ent.Y < float64(OverworldSeaLevel2612-2) {
		mgr.mu.Lock()
		if cur := mgr.undead[ent.ID]; cur != nil {
			cur.SearchingForLand = true
			cur.Swimming = true
		}
		mgr.mu.Unlock()
		targetY := float64(OverworldSeaLevel2612 - 1)
		s.moveDrownedToward2612(ent, cache, true, ent.X, targetY, ent.Z, DrownedSwimSpeed, 0.1)
		return
	}

	mgr.mu.Lock()
	if cur := mgr.undead[ent.ID]; cur != nil {
		cur.SearchingForLand = false
		cur.Swimming = false
	}
	mgr.mu.Unlock()
}

// ============================================================================
// 3. STRIDER HELPERS, FLOAT PHYSICS, BOOST CURVE & NBT (26.1.2)
// ============================================================================

// IsLavaFluidBlock2612 reports whether `blk` has `FluidTags.LAVA` (`minecraft:lava`,
// including both source `level=0` and flowing `level=1..15`).
func IsLavaFluidBlock2612(blk uint16) bool {
	if blk == world.StateAir {
		return false
	}
	if world.BlockName(blk) == "minecraft:lava" {
		return true
	}
	if kind, _, _, _, _, ok := world.ClassifyFluidState(blk); ok && kind == world.FluidLava {
		return true
	}
	return false
}

// IsStriderWarmBlock2612 reports whether `blk` belongs to `#minecraft:strider_warm_blocks`
// (`minecraft:lava`) or `minecraft:lava_cauldron`.
func IsStriderWarmBlock2612(blk uint16) bool {
	if IsLavaFluidBlock2612(blk) {
		return true
	}
	return world.BlockName(blk) == "minecraft:lava_cauldron"
}

// StriderMovementSpeed2612 returns the `Attributes.MOVEMENT_SPEED` of a `minecraft:strider`:
// `0.175` when warm vs `0.175 * (1.0 - 0.34) = 0.1155` when `DATA_SUFFOCATING` is true (`Strider.java:2775-2799, 3720-3726`).
func StriderMovementSpeed2612(suffocating bool) float64 {
	if suffocating {
		return StriderBaseMovementSpeed2612 * StriderSuffocatingSpeedMult2612
	}
	return StriderBaseMovementSpeed2612
}

// StriderBoostFactor2612 implements `ItemBasedSteering.boostFactor()` (`ItemBasedSteering.java:758-783`):
// `1.0F + 1.15F * Mth.sin((float)boostTime / (float)boostTimeTotal * PI)` (`1.0x -> 2.15x -> 1.0x`).
func StriderBoostFactor2612(boosting bool, boostTime, boostTimeTotal int32) float32 {
	if !boosting || boostTimeTotal <= 0 {
		return 1.0
	}
	ratio := float64(boostTime) / float64(boostTimeTotal)
	if ratio < 0 {
		ratio = 0
	}
	if ratio > 1 {
		ratio = 1
	}
	return 1.0 + StriderBoostSineAmplitude2612*float32(math.Sin(ratio*math.Pi))
}

// StriderRiddenSpeed2612 implements `Strider.getRiddenSpeed(Player)` (`Strider.java:3147-3166`):
// `getAttributeValue(MOVEMENT_SPEED) * (isSuffocating() ? 0.35F : 0.55F) * steering.boostFactor()`.
func StriderRiddenSpeed2612(suffocating, boosting bool, boostTime, boostTimeTotal int32) float64 {
	steerMod := StriderWarmSteeringModifier2612
	if suffocating {
		steerMod = StriderColdSteeringModifier2612
	}
	return StriderMovementSpeed2612(suffocating) * steerMod * float64(StriderBoostFactor2612(boosting, boostTime, boostTimeTotal))
}

// stepFloatStrider2612 implements `Strider.floatStrider()` + `canStandOnFluid(FluidTags.LAVA)` (`Strider.java:3320-3367`):
//   - When submerged in lava (`lava` at `by+1`, OR `lava` at `by` with `ent.Y < float64(by)+0.5`),
//     applies `deltaMovement = deltaMovement.scale(0.5D).add(0.0D, 0.05D, 0.0D)` so the Strider
//     smoothly floats upward tick-by-tick to the surface;
//   - When on top of the lava surface (`lava` at `by` with non-lava at `by+1` and `ent.Y >= float64(by)+0.5`,
//     or `lava` at `by-1` with non-lava at `by`), sets `OnGround = true` and resets vertical sink.
func (s *Server) stepFloatStrider2612(cache *world.Cache, ent *world.Entity, st *StriderState) bool {
	if s == nil || cache == nil || ent == nil || st == nil {
		return false
	}
	bx := int(math.Floor(ent.X))
	by := int(math.Floor(ent.Y))
	bz := int(math.Floor(ent.Z))
	if by < world.MinY || by >= world.MinY+world.WorldHeight {
		return false
	}

	hereLava := IsLavaFluidBlock2612(cache.GetBlock(bx, by, bz))
	aboveLava := by+1 < world.MinY+world.WorldHeight && IsLavaFluidBlock2612(cache.GetBlock(bx, by+1, bz))
	belowLava := by-1 >= world.MinY && IsStriderWarmBlock2612(cache.GetBlock(bx, by-1, bz))
	hereWarm := hereLava || IsStriderWarmBlock2612(cache.GetBlock(bx, by, bz))

	if !hereWarm && !belowLava {
		st.FloatVY = 0
		return false
	}

	// Submerged in deep lava (`aboveLava`, or inside `by` below the `0.5` liquid collision plane):
	if hereLava && (aboveLava || ent.Y < float64(by)+0.5) {
		st.FloatVY = st.FloatVY*StriderSubmergedFloatDamping2612 + StriderSubmergedFloatUpImpulse2612
		ny := ent.Y + st.FloatVY
		onSurfaceNow := !aboveLava && ny >= float64(by)+0.5
		if onSurfaceNow && ny < float64(by+1) {
			// Settled onto the liquid collision top of the surface lava block:
			s.entities.Update(ent.ID, func(e *world.Entity) {
				e.Y = ny
				e.VelocityY = clampVelocityShort(st.FloatVY)
				e.OnGround = true
			})
		} else {
			s.entities.Update(ent.ID, func(e *world.Entity) {
				e.Y = ny
				e.VelocityY = clampVelocityShort(st.FloatVY)
				e.OnGround = false
			})
		}
		return true
	}

	// Standing on the top lava block (`hereLava && !aboveLava && ent.Y >= by+0.5` or `belowLava && !hereLava`):
	st.FloatVY = 0
	targetY := ent.Y
	if hereLava && !aboveLava {
		targetY = float64(by + 1)
	}
	s.entities.Update(ent.ID, func(e *world.Entity) {
		e.Y = targetY
		e.VelocityY = 0
		e.OnGround = true
	})
	return true
}

// stepStriderUnmountedGoals2612 runs `TemptGoal(1.4D)` (`#strider_tempt_items`:
// `warped_fungus`, `warped_fungus_on_a_stick`) and `StriderGoToLavaGoal(1.0D)` (`8x2x8`).
func (s *Server) stepStriderUnmountedGoals2612(ent *world.Entity, cache *world.Cache, st *StriderState) {
	if s == nil || ent == nil || st == nil {
		return
	}
	dim := ent.EffectiveDimension()
	suffocating := st.Shivering
	baseSpeed := StriderMovementSpeed2612(suffocating)

	// Priority 3: `TemptGoal(1.4D)` toward nearest player within 10 blocks holding `#strider_tempt_items`:
	bestTemptDistSq := 100.0 + 1.0
	var temptPlayer *PlayerSession
	for _, p := range s.onlinePlayersSnapshot() {
		if p == nil || p.IsDead() {
			continue
		}
		pSnap := p.Snapshot()
		if pSnap.GameMode == GameModeSpectator || world.NormalizeDimension(pSnap.Dimension) != dim {
			continue
		}
		mainID := canonicalResourceName(p.HeldItem().ResolvedID())
		offID := canonicalResourceName(p.InventorySlot(45).ResolvedID())
		if mainID != "minecraft:warped_fungus" && mainID != "minecraft:warped_fungus_on_a_stick" &&
			offID != "minecraft:warped_fungus" && offID != "minecraft:warped_fungus_on_a_stick" {
			continue
		}
		dx, dy, dz := pSnap.X-ent.X, pSnap.Y-ent.Y, pSnap.Z-ent.Z
		dSq := dx*dx + dy*dy + dz*dz
		if dSq <= 100.0 && dSq < bestTemptDistSq {
			bestTemptDistSq = dSq
			temptPlayer = p
		}
	}
	if temptPlayer != nil {
		pSnap := temptPlayer.Snapshot()
		feetY := int(math.Floor(ent.Y + 0.01))
		s.stepEntityTowardPoint(ent, cache, feetY, pSnap.X, pSnap.Y, pSnap.Z, baseSpeed*1.4, 2.5)
		return
	}

	// Priority 4: `StriderGoToLavaGoal` (`!isInLava()` -> search `8x2x8` for `Blocks.LAVA` with pathfindable above):
	if suffocating && cache != nil {
		bx, by, bz := int(math.Floor(ent.X)), int(math.Floor(ent.Y)), int(math.Floor(ent.Z))
		bestDistSq := 1e18
		var goalX, goalZ float64
		found := false
		for dy := -2; dy <= 2; dy++ {
			oy := by + dy
			if oy < world.MinY || oy+1 >= world.MinY+world.WorldHeight {
				continue
			}
			for dx := -8; dx <= 8; dx++ {
				ox := bx + dx
				for dz := -8; dz <= 8; dz++ {
					oz := bz + dz
					if !IsLavaFluidBlock2612(cache.GetBlock(ox, oy, oz)) {
						continue
					}
					above := cache.GetBlock(ox, oy+1, oz)
					if above != world.StateAir && !isPassableBlock(above) {
						continue
					}
					dSq := float64(dx*dx + dy*dy + dz*dz)
					if dSq < bestDistSq {
						bestDistSq = dSq
						goalX, goalZ = float64(ox)+0.5, float64(oz)+0.5
						found = true
					}
				}
			}
		}
		if found {
			dx, dz := goalX-ent.X, goalZ-ent.Z
			horiz := math.Hypot(dx, dz)
			if horiz > 0.2 {
				step := baseSpeed
				if step > horiz {
					step = horiz
				}
				nx := ent.X + (dx/horiz)*step
				nz := ent.Z + (dz/horiz)*step
				yaw := yawToward(dx, dz)
				s.entities.Update(ent.ID, func(e *world.Entity) {
					e.X, e.Z, e.Yaw, e.HeadYaw = nx, nz, yaw, yaw
				})
				s.UpdateStriderEnvironment(ent.ID)
			}
		}
	}
}

// EncodeStriderEntityNBT serializes a `minecraft:strider`'s saddle equipment (`equipment.saddle`)
// matching vanilla 26.1.2 `Mob.addAdditionalSaveData` (`EntityEquipment.CODEC`).
func EncodeStriderEntityNBT(st StriderState) *nbt.Compound {
	c := nbt.NewCompound()
	if st.Saddled {
		eq := nbt.NewCompound()
		saddle := nbt.NewCompound()
		saddle.Set("id", nbt.String("minecraft:saddle"))
		saddle.Set("count", nbt.Int(1))
		eq.Set("saddle", saddle)
		c.Set("equipment", eq)
	}
	return c
}

// DecodeStriderEntityNBT parses `equipment.saddle` (or legacy `Saddle`) from a Strider NBT compound.
func DecodeStriderEntityNBT(c *nbt.Compound) (StriderState, bool) {
	if c == nil {
		return StriderState{}, false
	}
	var out StriderState
	if v, ok := c.Get("equipment"); ok {
		if eq, ok := v.(*nbt.Compound); ok && eq != nil {
			if sv, ok := eq.Get("saddle"); ok {
				if sc, ok := sv.(*nbt.Compound); ok && sc != nil {
					if idv, ok := sc.Get("id"); ok {
						if s, ok := idv.(nbt.String); ok && canonicalResourceName(string(s)) == "minecraft:saddle" {
							out.Saddled = true
						}
					}
				}
			}
		}
	}
	if v, ok := c.Get("Saddle"); ok {
		if b, ok := v.(nbt.Byte); ok && b != 0 {
			out.Saddled = true
		}
	}
	return out, true
}

// SaveStriderEntityNBT exports a live `minecraft:strider` state to NBT.
func (s *Server) SaveStriderEntityNBT(entityID int32) (*nbt.Compound, bool) {
	st, ok := s.GetStriderState(entityID)
	if !ok {
		return nil, false
	}
	return EncodeStriderEntityNBT(st), true
}

// LoadStriderEntityNBT restores a live `minecraft:strider` state from NBT.
func (s *Server) LoadStriderEntityNBT(entityID int32, c *nbt.Compound) bool {
	if s == nil || s.entities == nil || entityID <= 0 {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeStrider {
		return false
	}
	parsed, ok := DecodeStriderEntityNBT(c)
	if !ok {
		return false
	}
	return s.EquipStriderSaddle(entityID, parsed.Saddled)
}
