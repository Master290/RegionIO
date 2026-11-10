package server

import (
	"math"
	"math/rand"
	"strings"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

// ============================================================================
// ZOMBIE & SPIDER TICK-BY-TICK PARITY (Vanilla Minecraft 26.1.2 / Protocol 775)
// Verified against server-26.1.2.jar:
//   - net.minecraft.world.entity.ai.goal.ZombieAttackGoal
//   - net.minecraft.world.entity.ai.goal.MeleeAttackGoal
//   - net.minecraft.world.entity.ai.goal.BreakDoorGoal
//   - net.minecraft.world.entity.monster.zombie.Zombie
//   - net.minecraft.world.entity.monster.zombie.Husk
//   - net.minecraft.world.entity.monster.spider.Spider
//   - net.minecraft.world.entity.monster.spider.Spider$SpiderAttackGoal
//   - net.minecraft.world.entity.monster.spider.Spider$SpiderTargetGoal
//   - net.minecraft.world.entity.monster.spider.Spider$SpiderEffectsGroupData
//   - net.minecraft.world.entity.ai.goal.LeapAtTargetGoal
//   - net.minecraft.world.entity.LivingEntity (handleOnClimbable)
// ============================================================================

const (
	// ZombieAttackIntervalTicks is MeleeAttackGoal.DEFAULT_ATTACK_INTERVAL (20 ticks)
	// in vanilla 26.1.2.
	ZombieAttackIntervalTicks = 20

	// ZombieRaiseArmMinTicks is the minimum raiseArmTicks (5) in ZombieAttackGoal.tick()
	// before a Zombie raises its arms (`setAggressive(true)`).
	ZombieRaiseArmMinTicks = 5

	// ZombieDefaultAttackReach is Mob.DEFAULT_ATTACK_REACH = Math.sqrt(2.04) - 0.6
	// (~0.8282857) in vanilla 26.1.2.
	ZombieDefaultAttackReach = 0.82828568570857

	// ZombieDoorBreakTimeTicks is BreakDoorGoal.DEFAULT_DOOR_BREAK_TIME (240 ticks = 12s)
	// in vanilla 26.1.2.
	ZombieDoorBreakTimeTicks = 240

	// ZombieDoorBreakMaxDistance is the 2.0-block radius check in
	// BreakDoorGoal.canContinueToUse (`doorPos.closerToCenterThan(mob.position(), 2.0)`).
	ZombieDoorBreakMaxDistance = 2.0

	// ZombieReinforcementAttempts is Zombie.REINFORCEMENT_ATTEMPTS (50) in vanilla 26.1.2.
	ZombieReinforcementAttempts = 50

	// ZombieReinforcementNoPlayerRadius is the 7.0-block player exclusion radius
	// (`!level.hasNearbyAlivePlayer(x, y, z, 7.0)`) in Zombie.hurtServer.
	ZombieReinforcementNoPlayerRadius = 7.0

	// Vanilla 26.1.2 LevelEvent IDs used by Zombie BreakDoorGoal and conversions.
	LevelEventSoundZombieWoodenDoor      int32 = 1019 // LevelEvent.SOUND_ZOMBIE_WOODEN_DOOR
	LevelEventSoundZombieBreakWoodenDoor int32 = 1021 // LevelEvent.SOUND_ZOMBIE_BREAK_WOODEN_DOOR

	// SpiderClimbHorizontalClamp is LivingEntity.handleOnClimbable horizontal
	// deltaMovement clamp (0.15 blocks/tick) in vanilla 26.1.2.
	SpiderClimbHorizontalClamp = 0.15

	// SpiderClimbMinDescentSpeed is LivingEntity.handleOnClimbable minimum vertical
	// deltaMovement (-0.15 blocks/tick) when onClimbable() in vanilla 26.1.2.
	SpiderClimbMinDescentSpeed = -0.15

	// SpiderClimbAscentSpeed is the 0.2 blocks/tick upward vertical velocity set
	// in LivingEntity.handleRelativeFrictionAndCalculateMovement when colliding
	// horizontally or jumping on a climbable surface in vanilla 26.1.2.
	SpiderClimbAscentSpeed = 0.2

	// SpiderLeapVerticalVelocity is the 0.4F vertical velocity (`yd`) passed to
	// `new LeapAtTargetGoal(this, 0.4F)` in Spider.registerGoals in vanilla 26.1.2.
	SpiderLeapVerticalVelocity = 0.4

	// SpiderLeapHorizontalScale (0.4) and SpiderLeapMomentumRetention (0.2) match
	// LeapAtTargetGoal.start() in vanilla 26.1.2:
	//   vec31 = vec31.normalize().scale(0.4).add(vec3.scale(0.2))
	SpiderLeapHorizontalScale   = 0.4
	SpiderLeapMomentumRetention = 0.2

	// SpiderDaylightDeaggroDenominator is the 1-in-100 per-tick roll
	// (`random.nextInt(100) == 0`) in `Spider.SpiderAttackGoal.canContinueToUse()`
	// when `getLightLevelDependentMagicValue() >= 0.5F` (internal light >= 12).
	SpiderDaylightDeaggroDenominator = 100
)

// EncodeBlockDestructionPacket encodes a ClientboundBlockDestructionPacket (0x05)
// with entity `breakerEntityID`, block position `(x, y, z)`, and `progress`
// (`0..9`, or `-1` / `0xFF` to clear crack overlay).
func EncodeBlockDestructionPacket(breakerEntityID int32, x, y, z int, progress int) []byte {
	w := protocol.NewWriter(16)
	w.VarInt(breakerEntityID)
	w.Position(x, y, z)
	w.Byte(byte(progress))
	return w.Bytes()
}

// DecodeBlockDestructionPacket decodes a ClientboundBlockDestructionPacket (0x05).
func DecodeBlockDestructionPacket(r *protocol.Reader) (breakerEntityID int32, x, y, z int, progress int8, err error) {
	breakerEntityID, err = r.VarInt()
	if err != nil {
		return 0, 0, 0, 0, 0, err
	}
	x, y, z, err = r.Position()
	if err != nil {
		return 0, 0, 0, 0, 0, err
	}
	b, err := r.ReadByte()
	if err != nil {
		return 0, 0, 0, 0, 0, err
	}
	return breakerEntityID, x, y, z, int8(b), nil
}

// ZombieAttackGoal2612 models vanilla 26.1.2 `ZombieAttackGoal` + `MeleeAttackGoal`
// tick-by-tick state (`raiseArmTicks`, `ticksUntilNextAttack`, and `Aggressive` flag).
type ZombieAttackGoal2612 struct {
	RaiseArmTicks        int
	TicksUntilNextAttack int
	Aggressive           bool
}

// Start initializes `ZombieAttackGoal` (`raiseArmTicks = 0`), matching
// `ZombieAttackGoal.start()` in vanilla 26.1.2.
func (g *ZombieAttackGoal2612) Start() {
	g.RaiseArmTicks = 0
}

// Stop resets `Aggressive = false`, matching `ZombieAttackGoal.stop()` in vanilla 26.1.2.
func (g *ZombieAttackGoal2612) Stop() {
	g.Aggressive = false
}

// Tick advances `MeleeAttackGoal.tick()` + `ZombieAttackGoal.tick()` by 1 tick:
//   - Decrements `TicksUntilNextAttack = max(TicksUntilNextAttack - 1, 0)`
//   - If `inMeleeRange && hasLineOfSight && TicksUntilNextAttack <= 0`, resets
//     `TicksUntilNextAttack = 20` (`ZombieAttackIntervalTicks`) and performs the attack!
//   - Increments `RaiseArmTicks++` and sets `Aggressive = (RaiseArmTicks >= 5 && TicksUntilNextAttack < 10)`
//     (`getTicksUntilNextAttack() < getAttackInterval() / 2`).
func (g *ZombieAttackGoal2612) Tick(inMeleeRange, hasLineOfSight bool) (attacked bool) {
	if g.TicksUntilNextAttack > 0 {
		g.TicksUntilNextAttack--
	}
	if inMeleeRange && hasLineOfSight && g.TicksUntilNextAttack <= 0 {
		g.TicksUntilNextAttack = ZombieAttackIntervalTicks
		attacked = true
	}
	g.RaiseArmTicks++
	if g.RaiseArmTicks >= ZombieRaiseArmMinTicks && g.TicksUntilNextAttack < ZombieAttackIntervalTicks/2 {
		g.Aggressive = true
	} else {
		g.Aggressive = false
	}
	return attacked
}

// ZombieBreakDoorGoal2612 models vanilla 26.1.2 `BreakDoorGoal` tick-by-tick.
type ZombieBreakDoorGoal2612 struct {
	DoorX, DoorY, DoorZ int
	BreakTime           int
	DoorBreakTime       int
	LastBreakProgress   int
	Active              bool
}

// NewZombieBreakDoorGoal2612 creates a `ZombieBreakDoorGoal2612` targeting the
// door at `(doorX, doorY, doorZ)` with `DEFAULT_DOOR_BREAK_TIME = 240` ticks.
func NewZombieBreakDoorGoal2612(doorX, doorY, doorZ int) *ZombieBreakDoorGoal2612 {
	return &ZombieBreakDoorGoal2612{
		DoorX:             doorX,
		DoorY:             doorY,
		DoorZ:             doorZ,
		DoorBreakTime:     ZombieDoorBreakTimeTicks,
		LastBreakProgress: -1,
	}
}

func isDoorBlockState2612(state uint16) bool {
	if state == world.StateAir {
		return false
	}
	return strings.HasSuffix(canonicalResourceName(world.BlockName(state)), "_door")
}

func isClosedWoodenDoorState2612(state uint16) bool {
	if !isDoorBlockState2612(state) {
		return false
	}
	// Iron doors cannot be broken by Zombies (`BlockTags.MOB_INTERACTABLE_DOORS` / wooden doors only).
	if canonicalResourceName(world.BlockName(state)) == "minecraft:iron_door" {
		return false
	}
	_, props, ok := world.BlockStateProperties(state)
	if !ok {
		return false
	}
	return props["open"] != "true"
}

// CanUse implements `BreakDoorGoal.canUse()` for a Zombie in vanilla 26.1.2:
//   - Zombie must have `CanBreakDoors == true`
//   - `GameRules.MOB_GRIEFING` (`s.MobGriefing()`) must be enabled
//   - `Difficulty` must be `DifficultyHard` (`Zombie.DOOR_BREAKING_PREDICATE`)
//   - Block at `(DoorX, DoorY, DoorZ)` must be a closed wooden door (`!isOpen()`).
func (g *ZombieBreakDoorGoal2612) CanUse(s *Server, dim string, canBreakDoors bool) bool {
	if g == nil || s == nil || !canBreakDoors {
		return false
	}
	if !s.MobGriefing() || s.Difficulty() != DifficultyHard {
		return false
	}
	cache := s.DimensionChunks(world.NormalizeDimension(dim))
	if cache == nil {
		return false
	}
	return isClosedWoodenDoorState2612(cache.GetBlock(g.DoorX, g.DoorY, g.DoorZ))
}

// Start implements `BreakDoorGoal.start()`: resets `BreakTime = 0` and `LastBreakProgress = -1`.
func (g *ZombieBreakDoorGoal2612) Start() {
	if g == nil {
		return
	}
	g.BreakTime = 0
	g.LastBreakProgress = -1
	g.Active = true
}

// CanContinueToUse implements `BreakDoorGoal.canContinueToUse()` in vanilla 26.1.2:
//   - `BreakTime <= DoorBreakTime` (`240`)
//   - Door is still closed (`!isOpen()`)
//   - `doorPos.closerToCenterThan(mob.position(), 2.0D)` (distSq < 4.0)
//   - `Difficulty == DifficultyHard`.
func (g *ZombieBreakDoorGoal2612) CanContinueToUse(s *Server, dim string, mobX, mobY, mobZ float64) bool {
	if g == nil || !g.Active || s == nil {
		return false
	}
	maxTime := g.DoorBreakTime
	if maxTime <= 0 {
		maxTime = ZombieDoorBreakTimeTicks
	}
	if g.BreakTime > maxTime || s.Difficulty() != DifficultyHard {
		return false
	}
	cache := s.DimensionChunks(world.NormalizeDimension(dim))
	if cache == nil || !isClosedWoodenDoorState2612(cache.GetBlock(g.DoorX, g.DoorY, g.DoorZ)) {
		return false
	}
	dx := (float64(g.DoorX) + 0.5) - mobX
	dy := (float64(g.DoorY) + 0.5) - mobY
	dz := (float64(g.DoorZ) + 0.5) - mobZ
	return (dx*dx + dy*dy + dz*dz) < ZombieDoorBreakMaxDistance*ZombieDoorBreakMaxDistance
}

// Stop implements `BreakDoorGoal.stop()`: clears block destruction progress (`-1`).
func (g *ZombieBreakDoorGoal2612) Stop(s *Server, dim string, zombieID int32) {
	if g == nil {
		return
	}
	g.Active = false
	if s != nil {
		normDim := world.NormalizeDimension(dim)
		s.BroadcastDimensionChunk(normDim, int32(g.DoorX>>4), int32(g.DoorZ>>4), protocol.PlayBlockDestruction,
			EncodeBlockDestructionPacket(zombieID, g.DoorX, g.DoorY, g.DoorZ, -1))
	}
}

// Tick implements `BreakDoorGoal.tick()` in vanilla 26.1.2:
//   - `random.nextInt(20) == 0` -> broadcasts `LevelEvent(1019, doorPos, 0)`
//   - Increments `BreakTime++`
//   - Computes `progress = int(float32(BreakTime) / float32(DoorBreakTime) * 10.0)`;
//     when `progress != LastBreakProgress`, broadcasts `PlayBlockDestruction(zombieID, doorPos, progress)`
//     and updates `LastBreakProgress = progress`
//   - When `BreakTime == DoorBreakTime` (`240`) on `DifficultyHard`, removes the
//     door block (both lower and upper halves) and broadcasts `LevelEvent(1021)`
//     and `LevelEvent(2001, Block.getId(oldState))`!
func (g *ZombieBreakDoorGoal2612) Tick(s *Server, dim string, zombieID int32, rng *rand.Rand) (progress int, brokeDoor bool) {
	if g == nil || s == nil {
		return -1, false
	}
	normDim := world.NormalizeDimension(dim)
	cx := int32(g.DoorX >> 4)
	cz := int32(g.DoorZ >> 4)

	if rng != nil && rng.Intn(20) == 0 {
		s.BroadcastDimensionChunk(normDim, cx, cz, PlayLevelEventCB,
			EncodeLevelEventPacket(LevelEventSoundZombieWoodenDoor, g.DoorX, g.DoorY, g.DoorZ, 0, false))
	}

	g.BreakTime++
	maxTime := g.DoorBreakTime
	if maxTime <= 0 {
		maxTime = ZombieDoorBreakTimeTicks
	}
	progress = int(float32(g.BreakTime) / float32(maxTime) * 10.0)
	if progress != g.LastBreakProgress {
		s.BroadcastDimensionChunk(normDim, cx, cz, protocol.PlayBlockDestruction,
			EncodeBlockDestructionPacket(zombieID, g.DoorX, g.DoorY, g.DoorZ, progress))
		g.LastBreakProgress = progress
	}

	if g.BreakTime == maxTime && s.Difficulty() == DifficultyHard {
		cache := s.DimensionChunks(normDim)
		if cache != nil {
			oldState := cache.GetBlock(g.DoorX, g.DoorY, g.DoorZ)
			if isClosedWoodenDoorState2612(oldState) {
				_, props, _ := world.BlockStateProperties(oldState)
				cache.SetBlock(g.DoorX, g.DoorY, g.DoorZ, world.StateAir)
				s.broadcastMobBlockUpdate(normDim, g.DoorX, g.DoorY, g.DoorZ, world.StateAir)
				// Also clear the partner half of the 2-block door.
				partnerY := g.DoorY + 1
				if props["half"] == "upper" {
					partnerY = g.DoorY - 1
				}
				if partnerY >= world.MinY && partnerY < world.MinY+world.WorldHeight && isDoorBlockState2612(cache.GetBlock(g.DoorX, partnerY, g.DoorZ)) {
					cache.SetBlock(g.DoorX, partnerY, g.DoorZ, world.StateAir)
					s.broadcastMobBlockUpdate(normDim, g.DoorX, partnerY, g.DoorZ, world.StateAir)
				}
				s.BroadcastDimensionChunk(normDim, cx, cz, PlayLevelEventCB,
					EncodeLevelEventPacket(LevelEventSoundZombieBreakWoodenDoor, g.DoorX, g.DoorY, g.DoorZ, 0, false))
				s.BroadcastDimensionChunk(normDim, cx, cz, PlayLevelEventCB,
					EncodeLevelEventPacket(LevelEventParticlesDestroyBlock, g.DoorX, g.DoorY, g.DoorZ, world.BlockBreakLevelEventData(oldState), false))
				brokeDoor = true
			}
		}
	}
	return progress, brokeDoor
}

// StepSpiderClimbMovement2612 implements vanilla 26.1.2 `Spider.tick()` (`setClimbing(horizontalCollision)`),
// `Spider.onClimbable()` (`isClimbing()`), `LivingEntity.handleOnClimbable`, and
// `LivingEntity.handleRelativeFrictionAndCalculateMovement`:
//   - Updates `Spider.DATA_FLAGS_ID` bit `0x01` (`SetSpiderClimbing(spiderID, horizontalCollision)`)
//   - When climbing (`onClimbable()`):
//   - Clamps `vx` and `vz` to `[-0.15, 0.15]`
//   - Clamps `vy = max(vy, -0.15)`
//   - When `horizontalCollision || jumping`, sets `vy = 0.2` (`SpiderClimbAscentSpeed`)!
func (s *Server) StepSpiderClimbMovement2612(spiderID int32, vx, vy, vz float64, horizontalCollision, jumping bool) (outVX, outVY, outVZ float64, climbing bool) {
	outVX, outVY, outVZ = vx, vy, vz
	if s != nil && spiderID > 0 {
		s.SetSpiderClimbing(spiderID, horizontalCollision)
		climbing = s.IsSpiderClimbing(spiderID)
	} else {
		climbing = horizontalCollision
	}
	if !climbing {
		return outVX, outVY, outVZ, false
	}
	if outVX < -SpiderClimbHorizontalClamp {
		outVX = -SpiderClimbHorizontalClamp
	} else if outVX > SpiderClimbHorizontalClamp {
		outVX = SpiderClimbHorizontalClamp
	}
	if outVZ < -SpiderClimbHorizontalClamp {
		outVZ = -SpiderClimbHorizontalClamp
	} else if outVZ > SpiderClimbHorizontalClamp {
		outVZ = SpiderClimbHorizontalClamp
	}
	if outVY < SpiderClimbMinDescentSpeed {
		outVY = SpiderClimbMinDescentSpeed
	}
	if horizontalCollision || jumping {
		outVY = SpiderClimbAscentSpeed
	}
	return outVX, outVY, outVZ, true
}

// ComputeSpiderLeapVelocity2612 implements vanilla 26.1.2 `LeapAtTargetGoal`:
//   - `canUse()`: `!hasControllingPassenger && distSq >= 4.0 && distSq <= 16.0 && onGround && rng.Intn(5) == 0`
//   - `start()`: sets horizontal velocity to `normalize(target - spider) * 0.4 + oldVel * 0.2`
//     and vertical velocity to `yd = 0.4`.
func ComputeSpiderLeapVelocity2612(
	spiderX, spiderY, spiderZ float64,
	oldVX, oldVZ float64,
	targetX, targetY, targetZ float64,
	onGround, hasControllingPassenger bool,
	rng *rand.Rand,
) (newVX, newVY, newVZ float64, leaped bool) {
	if hasControllingPassenger || !onGround {
		return oldVX, 0, oldVZ, false
	}
	dx := targetX - spiderX
	dy := targetY - spiderY
	dz := targetZ - spiderZ
	distSq := dx*dx + dy*dy + dz*dz
	if distSq < 4.0 || distSq > 16.0 {
		return oldVX, 0, oldVZ, false
	}
	if rng != nil && rng.Intn(5) != 0 {
		return oldVX, 0, oldVZ, false
	}
	horizSq := dx*dx + dz*dz
	var hx, hz float64
	if horizSq > 1.0e-7 {
		invLen := 1.0 / math.Sqrt(horizSq)
		hx = (dx*invLen)*SpiderLeapHorizontalScale + oldVX*SpiderLeapMomentumRetention
		hz = (dz*invLen)*SpiderLeapHorizontalScale + oldVZ*SpiderLeapMomentumRetention
	}
	return hx, SpiderLeapVerticalVelocity, hz, true
}
