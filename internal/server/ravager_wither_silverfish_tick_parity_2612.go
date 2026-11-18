package server

import (
	"math"
	"math/rand"
	"strings"

	"regionio/internal/nbt"
	"regionio/internal/protocol"
	"regionio/internal/world"
)

// Exact vanilla Minecraft 26.1.2 (protocol 775) constants for Ravager, Wither,
// WitherSkull, WitherSkeleton, and Silverfish verified against server-26.1.2.jar.
const (
	// Ravager (`net.minecraft.world.entity.monster.Ravager`, `EntityType.RAVAGER`)
	RavagerHitboxWidth2612          float64 = 1.95
	RavagerHitboxHeight2612         float64 = 2.2
	RavagerLeavesInflate2612        float64 = 0.2
	RavagerPassengerOffsetY2612     float64 = 2.2625
	RavagerPassengerOffsetZ2612     float64 = -0.0625
	RavagerBaseMoveSpeed2612        float64 = 0.30
	RavagerChaseMoveSpeed2612       float64 = 0.35
	RavagerKnockbackResistance2612  float64 = 0.75
	RavagerAttackKnockback2612      float64 = 1.5
	RavagerAttackTickDuration2612   int     = 10
	RavagerRoarTotalTicks2612       int     = 20
	RavagerRoarShockwaveTick2612    int     = 10 // Ravager.aiStep(): --roarTick; if (roarTick == 10) roar();
	RavagerShieldStunChance2612     float64 = 0.5
	RavagerStrongKnockbackHoriz2612 float64 = 4.0
	RavagerStrongKnockbackVert2612  float64 = 0.2

	// Wither & WitherSkull (`WitherBoss.java`, `WitherSkull.java`, `AbstractHurtingProjectile.java`)
	WitherHitboxWidth2612             float64 = 0.9
	WitherHitboxHeight2612            float64 = 3.5
	WitherEyeHeight2612               float64 = 2.975 // 3.5 * 0.85
	WitherSkullHitboxSize2612         float64 = 0.3125
	WitherSkullInitialSpeed2612       float64 = 0.1
	WitherSkullAccelerationPower2612  float64 = 0.1
	WitherSkullBlackInertia2612       float64 = 0.95
	WitherSkullBlueInertia2612        float64 = 0.73
	WitherSkullWaterInertia2612       float64 = 0.80
	WitherCenterBlueSkullChance2612   float32 = 0.001 // 0.1% on head 0 in WitherBoss.performRangedAttack
	WitherSkullOwnerlessMagicDmg2612  float32 = 5.0
	WitherSideHeadMaxRangeSq2612      float64 = 900.0 // 30.0^2

	// WitherSkeleton & Silverfish (`WitherSkeleton.java`, `Silverfish.java`, `EntityType`)
	WitherSkeletonWidth2612           float64 = 0.7
	WitherSkeletonHeight2612          float64 = 2.4
	WitherSkeletonEyeHeight2612       float64 = 2.1
	WitherSkeletonBowFireTicks2612    int     = 100 // Arrow ignited 100s -> ignites hit entity for 5s (100 ticks)
	SilverfishWidth2612               float64 = 0.4
	SilverfishHeight2612              float64 = 0.3
	SilverfishEyeHeight2612           float64 = 0.13
	SilverfishMergeRandomDenom2612    int     = 10 // SilverfishMergeWithStoneGoal: random.nextInt(10) == 0
)

// ComputeRavagerStrongKnockback2612 implements `Ravager.strongKnockback(Entity)`
// from `26.1.2.jar`:
//
//	double dx = entity.getX() - this.getX();
//	double dz = entity.getZ() - this.getZ();
//	double d = Math.max(dx * dx + dz * dz, 0.001D);
//	entity.push(dx / d * 4.0D, 0.2D, dz / d * 4.0D);
func ComputeRavagerStrongKnockback2612(ravagerX, ravagerZ, targetX, targetZ float64) (vx, vy, vz float64) {
	dx := targetX - ravagerX
	dz := targetZ - ravagerZ
	d := math.Max(dx*dx+dz*dz, 0.001)
	return (dx / d) * RavagerStrongKnockbackHoriz2612, RavagerStrongKnockbackVert2612, (dz / d) * RavagerStrongKnockbackHoriz2612
}

// IsRavagerImmobile2612 implements `Ravager.isImmobile()` (`attackTick > 0 || stunnedTick > 0 || roarTick > 0`).
func IsRavagerImmobile2612(st RavagerBehaviorState) bool {
	return st.AttackTicks > 0 || st.StunnedTicks > 0 || st.RoarTicks > 0
}

// IsRavagerEntityImmobile2612 reports whether the Ravager with `entityID` is
// currently immobilized by attack recovery, shield stun, or roaring.
func (s *Server) IsRavagerEntityImmobile2612(entityID int32) bool {
	if s == nil || entityID <= 0 {
		return false
	}
	st, ok := s.GetRavagerState(entityID)
	return ok && IsRavagerImmobile2612(st)
}

// OnRavagerBlockedByShieldFromPlayer2612 implements `Ravager.blockedByItem(LivingEntity)`
// in `26.1.2.jar` when `blocker`'s shield blocks a melee attack from `ravagerID`:
//   - Only executes when `roarTick == 0`.
//   - With `50%` probability (`random.nextDouble() < 0.5`): stuns the Ravager for
//     `40` ticks (`stunnedTick = 40`), plays `SoundRavagerStunned`, broadcasts
//     `EntityEventRavagerStunned` (`39`), and nudges `blocker` away (`blocker.push(this)`).
//   - Otherwise (`50%`): applies `strongKnockback(blocker)` without stunning the Ravager.
func (s *Server) OnRavagerBlockedByShieldFromPlayer2612(ravagerID int32, blocker *PlayerSession, rng *rand.Rand) (stunned bool) {
	if s == nil || s.entities == nil || ravagerID <= 0 {
		return false
	}
	ent := s.entities.Get(ravagerID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeRavager {
		return false
	}
	s.ensureIllagerOrRaiderState(ravagerID, MobTypeRavager, false)

	mgr := s.illagerManager()
	mgr.mu.Lock()
	st := mgr.ravagers[ravagerID]
	if st == nil || st.RoarTicks > 0 {
		mgr.mu.Unlock()
		return false
	}
	var roll float64
	if rng != nil {
		roll = rng.Float64()
	} else {
		roll = rand.Float64()
	}
	if roll < RavagerShieldStunChance2612 {
		st.StunnedTicks = RavagerStunDurationTicks
		stunned = true
	}
	mgr.mu.Unlock()

	if stunned {
		dim := ent.EffectiveDimension()
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
			EncodeSoundPacket(SoundRavagerStunned, SoundSourceHostile, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayEntityEvent,
			EncodeEntityEventPacket(ravagerID, EntityEventRavagerStunned))
		if blocker != nil && blocker.send != nil {
			bSnap := blocker.Snapshot()
			dx := bSnap.X - ent.X
			dz := bSnap.Z - ent.Z
			dist := math.Hypot(dx, dz)
			if dist > 1e-4 {
				_ = blocker.send(protocol.PlaySetEntityMotion,
					EncodeSetEntityMotionPacket(blocker.EntityID, (dx/dist)*0.15, 0.05, (dz/dist)*0.15))
			}
		}
		return true
	}

	if blocker != nil && blocker.send != nil {
		bSnap := blocker.Snapshot()
		vx, vy, vz := ComputeRavagerStrongKnockback2612(ent.X, ent.Z, bSnap.X, bSnap.Z)
		_ = blocker.send(protocol.PlaySetEntityMotion, EncodeSetEntityMotionPacket(blocker.EntityID, vx, vy, vz))
	}
	return false
}

// IsRavagerLeafBlock2612 reports whether `blockState` belongs to `#minecraft:leaves`
// (`LeavesBlock` in `Ravager.aiStep` in `26.1.2.jar`).
func IsRavagerLeafBlock2612(blockState uint16) bool {
	if blockState == world.StateAir {
		return false
	}
	switch canonicalResourceName(world.BlockName(blockState)) {
	case "minecraft:oak_leaves",
		"minecraft:spruce_leaves",
		"minecraft:birch_leaves",
		"minecraft:jungle_leaves",
		"minecraft:acacia_leaves",
		"minecraft:dark_oak_leaves",
		"minecraft:mangrove_leaves",
		"minecraft:cherry_leaves",
		"minecraft:pale_oak_leaves",
		"minecraft:azalea_leaves",
		"minecraft:flowering_azalea_leaves":
		return true
	default:
		return strings.HasSuffix(canonicalResourceName(world.BlockName(blockState)), "_leaves")
	}
}

// EncodeRavagerDataPacket encodes `Raider.IS_CELEBRATING` (index 16, BOOLEAN) for a Ravager.
func EncodeRavagerDataPacket(entityID int32, celebrating bool) []byte {
	w := protocol.NewWriter(12)
	w.VarInt(entityID)
	w.Byte(EntityDataRaiderCelebratingID)
	w.VarInt(EntityDataSerializerBool)
	w.Bool(celebrating)
	w.Byte(0xFF)
	return w.Bytes()
}

// StepWitherSkullInertia2612 applies one tick of `AbstractHurtingProjectile.applyInertia()`
// and `WitherSkull.getInertia()` (`0.73` for dangerous blue skull, `0.95` for black skull,
// `0.80` when in water, `accelerationPower = 0.1`):
//
//	Vec3 next = delta.add(delta.normalize().scale(accelerationPower)).scale(inertia);
func StepWitherSkullInertia2612(vx, vy, vz, accPower float64, dangerous, inWater bool) (nvx, nvy, nvz float64) {
	if accPower <= 0 {
		accPower = WitherSkullAccelerationPower2612
	}
	inertia := WitherSkullBlackInertia2612
	if inWater {
		inertia = WitherSkullWaterInertia2612
	} else if dangerous {
		inertia = WitherSkullBlueInertia2612
	}
	mag := math.Sqrt(vx*vx + vy*vy + vz*vz)
	if mag > 1e-6 {
		vx += (vx / mag) * accPower
		vy += (vy / mag) * accPower
		vz += (vz / mag) * accPower
	}
	return vx * inertia, vy * inertia, vz * inertia
}

// ShootWitherSkeletonBowArrowAtPlayer fires a flaming arrow from a bow-wielding
// `minecraft:wither_skeleton` (`WitherSkeleton.getArrow`: `arrow.igniteForSeconds(100.0F)`),
// dealing difficulty-scaled skeleton arrow damage and igniting `target` for 5 seconds
// (`100` ticks) if hit and not fire-immune!
func (s *Server) ShootWitherSkeletonBowArrowAtPlayer(witherSkelID int32, skelX, skelY, skelZ float64, target *PlayerSession) float32 {
	if s == nil || target == nil || target.IsDead() {
		return 0
	}
	dealt := s.ShootSkeletonArrowAtPlayer(witherSkelID, skelX, skelY, skelZ, target)
	if dealt > 0 && !target.IsDead() && !target.HasPlayerEffect(EffectFireResistance) {
		if s.PlayerFireTicks(target) < WitherSkeletonBowFireTicks2612 {
			s.SetPlayerFireTicks(target, WitherSkeletonBowFireTicks2612)
		}
	}
	return dealt
}

// silverfishWakeOffsetAt2612 returns the `i`-th signed coordinate offset in the
// expanding sequence `0, 1, -1, 2, -2, ..., radius, -radius` used by
// `SilverfishWakeUpFriendsGoal.tick()` (`k <= 0 ? 1 - k : 0 - k`).
func silverfishWakeOffsets2612(radius int) []int {
	out := make([]int, 0, 2*radius+1)
	k := 0
	for k <= radius && k >= -radius {
		out = append(out, k)
		if k <= 0 {
			k = 1 - k
		} else {
			k = -k
		}
	}
	return out
}

var silverfishSixDirections2612 = [6][3]int{
	{0, -1, 0}, // DOWN
	{0, 1, 0},  // UP
	{0, 0, -1}, // NORTH
	{0, 0, 1},  // SOUTH
	{-1, 0, 0}, // WEST
	{1, 0, 0},  // EAST
}

// ============================================================================
// NBT PERSISTENCE FOR RAVAGER, WITHER & WITHER SKULL (26.1.2.jar)
// ============================================================================

// RavagerEntityNBT2612 holds persistent NBT tags for `minecraft:ravager` (`Ravager.java`).
type RavagerEntityNBT2612 struct {
	AttackTick int32
	StunTick   int32
	RoarTick   int32
	Health     float32
}

// EncodeRavagerEntityNBT serializes a Ravager's persistent fields (`AttackTick`, `StunTick`, `RoarTick`, `Health`).
func EncodeRavagerEntityNBT(st RavagerBehaviorState, health float32) *nbt.Compound {
	return nbt.NewCompound().
		Set("id", nbt.String(MobTypeRavager)).
		Set("AttackTick", nbt.Int(int32(st.AttackTicks))).
		Set("StunTick", nbt.Int(int32(st.StunnedTicks))).
		Set("RoarTick", nbt.Int(int32(st.RoarTicks))).
		Set("Health", nbt.Float(health))
}

// DecodeRavagerEntityNBT deserializes `AttackTick`, `StunTick`, `RoarTick`, and `Health` from NBT.
func DecodeRavagerEntityNBT(c *nbt.Compound) (RavagerEntityNBT2612, bool) {
	if c == nil {
		return RavagerEntityNBT2612{}, false
	}
	out := RavagerEntityNBT2612{Health: DefaultRavagerMaxHealth}
	if t, ok := c.Get("AttackTick"); ok {
		if v, ok := t.(nbt.Int); ok && v > 0 {
			out.AttackTick = int32(v)
		}
	}
	if t, ok := c.Get("StunTick"); ok {
		if v, ok := t.(nbt.Int); ok && v > 0 {
			out.StunTick = int32(v)
		}
	}
	if t, ok := c.Get("RoarTick"); ok {
		if v, ok := t.(nbt.Int); ok && v > 0 {
			out.RoarTick = int32(v)
		}
	}
	if t, ok := c.Get("Health"); ok {
		if v, ok := t.(nbt.Float); ok && float32(v) > 0 {
			out.Health = float32(v)
		}
	}
	return out, true
}

// SaveRavagerEntityNBT exports the live `minecraft:ravager` state to an NBT compound.
func (s *Server) SaveRavagerEntityNBT(ravagerID int32) (*nbt.Compound, bool) {
	if s == nil || s.entities == nil {
		return nil, false
	}
	ent := s.entities.Get(ravagerID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeRavager {
		return nil, false
	}
	st, _ := s.GetRavagerState(ravagerID)
	return EncodeRavagerEntityNBT(st, s.MobHealth(ravagerID)), true
}

// LoadRavagerEntityNBT restores a live `minecraft:ravager` state from an NBT compound.
func (s *Server) LoadRavagerEntityNBT(ravagerID int32, c *nbt.Compound) bool {
	if s == nil || s.entities == nil {
		return false
	}
	ent := s.entities.Get(ravagerID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeRavager {
		return false
	}
	parsed, ok := DecodeRavagerEntityNBT(c)
	if !ok {
		return false
	}
	s.ensureIllagerOrRaiderState(ravagerID, MobTypeRavager, false)
	mgr := s.illagerManager()
	mgr.mu.Lock()
	if st := mgr.ravagers[ravagerID]; st != nil {
		st.AttackTicks = int(parsed.AttackTick)
		st.StunnedTicks = int(parsed.StunTick)
		st.RoarTicks = int(parsed.RoarTick)
	}
	mgr.mu.Unlock()
	s.SetMobHealth(ravagerID, parsed.Health)
	return true
}

// EncodeWitherEntityNBT serializes a WitherBoss's persistent NBT (`Invul`, `Health`, `CustomName`).
func EncodeWitherEntityNBT(st WitherState, customName string) *nbt.Compound {
	c := nbt.NewCompound().
		Set("id", nbt.String(MobTypeWither)).
		Set("Invul", nbt.Int(st.InvulnerableTicks)).
		Set("Health", nbt.Float(st.Health))
	if strings.TrimSpace(customName) != "" {
		c.Set("CustomName", nbt.String(customName))
	}
	return c
}

// DecodeWitherEntityNBT deserializes `Invul`, `Health`, and `CustomName` for a WitherBoss.
func DecodeWitherEntityNBT(c *nbt.Compound) (invul int32, health float32, customName string, ok bool) {
	if c == nil {
		return 0, 0, "", false
	}
	health = DefaultWitherMaxHealth
	if t, has := c.Get("Invul"); has {
		if v, isInt := t.(nbt.Int); isInt && v >= 0 {
			invul = int32(v)
		}
	}
	if t, has := c.Get("Health"); has {
		if v, isF := t.(nbt.Float); isF && float32(v) > 0 {
			health = float32(v)
		}
	}
	if t, has := c.Get("CustomName"); has {
		if v, isS := t.(nbt.String); isS {
			customName = string(v)
		}
	}
	return invul, health, customName, true
}

// SaveWitherEntityNBT exports a live `minecraft:wither` state to NBT.
func (s *Server) SaveWitherEntityNBT(witherID int32) (*nbt.Compound, bool) {
	st, ok := s.GetWitherState(witherID)
	if !ok {
		return nil, false
	}
	var customName string
	if bar, has := s.GetEntityBossBar(witherID); has && bar != nil && bar.Name.Text != "" {
		customName = bar.Name.Text
	}
	return EncodeWitherEntityNBT(st, customName), true
}

// LoadWitherEntityNBT restores `Invul`, `Health`, and `CustomName` (updating `bossEvent`) onto a live Wither.
func (s *Server) LoadWitherEntityNBT(witherID int32, c *nbt.Compound) bool {
	invul, health, customName, ok := DecodeWitherEntityNBT(c)
	if !ok || s == nil {
		return false
	}
	mgr := s.witherManager()
	mgr.mu.Lock()
	st, exists := mgr.withers[witherID]
	if !exists || st == nil {
		mgr.mu.Unlock()
		return false
	}
	st.InvulnerableTicks = invul
	st.Health = health
	if st.Health > st.MaxHealth {
		st.Health = st.MaxHealth
	}
	snap := *st
	mgr.mu.Unlock()

	s.SetMobHealth(witherID, snap.Health)
	if snap.InvulnerableTicks > 0 {
		prog := float32(1.0) - float32(snap.InvulnerableTicks)/float32(WitherInvulnerableTicks)
		if prog < 0 {
			prog = 0
		}
		s.UpdateRuntimeBossBarHealth(snap.BossBarUUID, prog*snap.MaxHealth, snap.MaxHealth)
	} else {
		s.UpdateRuntimeBossBarHealth(snap.BossBarUUID, snap.Health, snap.MaxHealth)
	}
	if customName != "" {
		s.SetWitherCustomName(witherID, customName)
	}
	return true
}

// EncodeWitherSkullEntityNBT serializes a `minecraft:wither_skull` (`dangerous`, `acceleration_power`).
func EncodeWitherSkullEntityNBT(sk WitherSkullProjectile) *nbt.Compound {
	var dang byte
	if sk.Dangerous {
		dang = 1
	}
	acc := sk.AccelerationPower
	if acc <= 0 {
		acc = WitherSkullAccelerationPower2612
	}
	return nbt.NewCompound().
		Set("id", nbt.String(EntityTypeWitherSkull)).
		Set("dangerous", nbt.Byte(dang)).
		Set("acceleration_power", nbt.Double(acc))
}

// DecodeWitherSkullEntityNBT deserializes `dangerous` and `acceleration_power` for a `minecraft:wither_skull`.
func DecodeWitherSkullEntityNBT(c *nbt.Compound) (dangerous bool, accPower float64, ok bool) {
	if c == nil {
		return false, 0, false
	}
	accPower = WitherSkullAccelerationPower2612
	if t, has := c.Get("dangerous"); has {
		if v, isB := t.(nbt.Byte); isB {
			dangerous = v != 0
		}
	}
	if t, has := c.Get("acceleration_power"); has {
		if v, isD := t.(nbt.Double); isD && float64(v) >= 0 {
			accPower = float64(v)
		}
	}
	return dangerous, accPower, true
}
