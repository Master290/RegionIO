package server

import (
	"fmt"
	"math"
	"math/rand"
	"sync"

	"regionio/internal/protocol"
	"regionio/internal/registry"
	"regionio/internal/world"
)

// Vanilla 26.1.2 Bow, Crossbow, Trident, Arrow, and Shield constants.
const (
	BowItemID           = "minecraft:bow"
	TridentItemID       = "minecraft:trident"
	ShieldItemID        = "minecraft:shield"
	SpectralArrowItemID = "minecraft:spectral_arrow"
	TippedArrowItemID   = "minecraft:tipped_arrow"

	EntityTypeTrident = "minecraft:trident"

	// Bow & Arrow parameters.
	BowFullChargeTicks    int     = 20
	BowMinChargePower     float32 = 0.1
	BowMaxArrowVelocity   float64 = 3.0
	BowDefaultInaccuracy  float64 = 1.0
	ArrowBaseDamage       float64 = 2.0
	FlameArrowFireTicks   int     = 2000 // 100s burning arrow
	FlameTargetFireTicks  int     = 100  // 5s target ignition
	PunchKnockbackPerLvl  float64 = 0.6
	SpectralGlowingTicks  int32   = 200 // 10s Glowing

	// Crossbow parameters.
	CrossbowBaseChargeTicks     int     = 25
	CrossbowQuickChargeStep     int     = 5
	CrossbowArrowBaseDamage     float32 = 9.0
	CrossbowMultishotAngleDeg   float32 = 10.0

	// Trident parameters.
	TridentMinChargeTicks       int     = 10
	TridentThrownBaseDamage     float32 = 8.0
	TridentMeleeBaseDamage      float32 = 9.0
	TridentThrowVelocity        float64 = 2.5
	TridentImpalingBonusPerLvl  float32 = 2.5
	TridentRiptideSpinTicks     int     = 20
	TridentLoyaltyReturnStep    float64 = 0.65
	ChannelingLightningDamage   float32 = 5.0

	// Shield parameters.
	EntityEventShieldBlock          byte    = 29
	EntityEventShieldDisabled       byte    = 30
	ShieldDurabilityDamageThreshold float32 = 3.0
	ShieldAxeDisableTicks           int     = 100 // 5.0s at 20 TPS
)

// ArrowPickupRule mirrors vanilla AbstractArrow.Pickup.
type ArrowPickupRule uint8

const (
	ArrowPickupDisallowed ArrowPickupRule = iota
	ArrowPickupAllowed
	ArrowPickupCreativeOnly
)

// GroundedArrowState tracks a shot arrow (or spectral/tipped arrow) that has
// embedded into a block and can be picked up by players.
type GroundedArrowState struct {
	EntityID   int32
	Dimension  string
	X, Y, Z    float64
	Stack      world.ItemStack
	PotionType string
	PickupRule ArrowPickupRule
	OwnerID    int32
	AgeTicks   int
}

// ThrownTridentState tracks an in-flight, grounded, or Loyalty-returning
// `minecraft:trident` entity thrown by a player.
type ThrownTridentState struct {
	EntityID      int32
	Owner         *PlayerSession
	Dimension     string
	X, Y, Z       float64
	VX, VY, VZ    float64
	Stack         world.ItemStack
	Meta          ItemMeta
	OriginalSlot  int
	LoyaltyLevel  int
	Channeling    bool
	ImpalingLevel int
	Grounded      bool
	Returning     bool
	DealtDamage   bool
	ReturnTicks   int
}

type playerRangedDrawState struct {
	mu              sync.Mutex
	active          bool
	weapon          string
	hand            int32
	slot            int
	drawTicks       int
	explicitlyTimed bool
	riptideTicks    int
	riptideImpaling int
}

type rangedWeaponsManager struct {
	mu       sync.Mutex
	arrows   map[int32]*GroundedArrowState
	tridents map[int32]*ThrownTridentState
	players  map[*PlayerSession]*playerRangedDrawState
}

var serverRangedManagers sync.Map // map[*Server]*rangedWeaponsManager

func (s *Server) rangedManager() *rangedWeaponsManager {
	if s == nil {
		return nil
	}
	if v, ok := serverRangedManagers.Load(s); ok {
		return v.(*rangedWeaponsManager)
	}
	mgr := &rangedWeaponsManager{
		arrows:   make(map[int32]*GroundedArrowState),
		tridents: make(map[int32]*ThrownTridentState),
		players:  make(map[*PlayerSession]*playerRangedDrawState),
	}
	actual, _ := serverRangedManagers.LoadOrStore(s, mgr)
	return actual.(*rangedWeaponsManager)
}

func (s *Server) playerDrawState(session *PlayerSession) *playerRangedDrawState {
	if s == nil || session == nil {
		return nil
	}
	mgr := s.rangedManager()
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	st := mgr.players[session]
	if st == nil {
		st = &playerRangedDrawState{}
		mgr.players[session] = st
	}
	return st
}

// ---------------------------------------------------------------------------
// 1. Bow Mechanics (Draw Curve, Velocity/Inaccuracy, Power/Punch/Flame/Infinity, Pickup)
// ---------------------------------------------------------------------------

// BowPowerForTime implements vanilla `BowItem.getPowerForTime(int chargeTicks)`:
//
//	f = float32(chargeTicks) / 20.0
//	f = (f*f + f*2.0) / 3.0
//	if f < 0.1 { return 0 }
//	if f > 1.0 { return 1.0 }
func BowPowerForTime(chargeTicks int) float32 {
	if chargeTicks <= 0 {
		return 0
	}
	f := float32(chargeTicks) / float32(BowFullChargeTicks)
	f = (f*f + f*2.0) / 3.0
	if f < BowMinChargePower {
		return 0
	}
	if f > 1.0 {
		f = 1.0
	}
	return f
}

// ComputeProjectileShootVector computes the 3D launch velocity vector for a
// projectile fired along (yaw, pitch) with speed `velocity` and Gaussian
// spread `inaccuracy` (when rng != nil).
func ComputeProjectileShootVector(yaw, pitch float32, velocity, inaccuracy float64, rng *rand.Rand) (vx, vy, vz float64) {
	lx, ly, lz := ElytraLookVector(yaw, pitch)
	if rng != nil && inaccuracy > 0 {
		const spreadScale = 0.007499999832361937
		lx += rng.NormFloat64() * spreadScale * inaccuracy
		ly += rng.NormFloat64() * spreadScale * inaccuracy
		lz += rng.NormFloat64() * spreadScale * inaccuracy
	}
	mag := math.Sqrt(lx*lx + ly*ly + lz*lz)
	if mag > 1e-6 {
		lx /= mag
		ly /= mag
		lz /= mag
	}
	return lx * velocity, ly * velocity, lz * velocity
}

// ComputeBowArrowDamage computes the damage dealt by an arrow fired at charge
// factor `power` (in [0.1, 1.0]) with `minecraft:power` enchantment level `powerLevel`.
func ComputeBowArrowDamage(power float32, powerLevel int) float32 {
	if power <= 0 {
		return 0
	}
	base := float32(6.0) * power
	if powerLevel > 0 {
		base += (float32(powerLevel)*1.5 + 0.5) * power
	}
	if base < 1.0 {
		base = 1.0
	}
	return base
}

// StartDrawingRangedWeapon begins drawing/charging a `minecraft:bow`,
// `minecraft:trident`, or uncharged `minecraft:crossbow` in `hand`.
func (s *Server) StartDrawingRangedWeapon(session *PlayerSession, hand int32) bool {
	if s == nil || session == nil || session.IsDead() {
		return false
	}
	slot, stack := session.handSlotAndStack(hand)
	if stack.IsEmpty() {
		return false
	}
	itemID := canonicalItemName(stack.ResolvedID())
	if itemID != BowItemID && itemID != TridentItemID && itemID != CrossbowItemID {
		return false
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator {
		return false
	}
	if itemID == TridentItemID {
		meta := GetSlotItemMeta(session, slot)
		if meta.EnchantmentLevel("minecraft:riptide") > 0 && !s.CanUseRiptide(session) {
			return false
		}
	}
	if itemID == CrossbowItemID {
		if len(GetSlotChargedProjectiles(session, slot)) > 0 {
			return false
		}
		if ammoSlot, ammoStack := s.findCrossbowAmmo(session, hand); ammoSlot < 0 && ammoStack.IsEmpty() {
			return false
		}
		cx := int32(int64(math.Floor(snap.X)) >> 4)
		cz := int32(int64(math.Floor(snap.Z)) >> 4)
		s.BroadcastDimensionChunk(snap.Dimension, cx, cz, PlaySoundPacketID,
			EncodeSoundPacket("minecraft:item.crossbow.loading_start", SoundSourcePlayers, snap.X, snap.Y, snap.Z, 1.0, 1.0, 0))
	}

	st := getSurvivalState(session)
	if st != nil {
		st.mu.Lock()
		st.drawingWeapon = itemID
		st.drawingHand = hand
		st.mu.Unlock()
	}
	ds := s.playerDrawState(session)
	if ds != nil {
		ds.mu.Lock()
		ds.active = true
		ds.weapon = itemID
		ds.hand = hand
		ds.slot = slot
		ds.drawTicks = 0
		ds.explicitlyTimed = false
		ds.mu.Unlock()
	}
	return true
}

// SetWeaponDrawTicks sets the elapsed draw/charge ticks on session's active ranged weapon.
func (s *Server) SetWeaponDrawTicks(session *PlayerSession, ticks int) {
	ds := s.playerDrawState(session)
	if ds == nil {
		return
	}
	if ticks < 0 {
		ticks = 0
	}
	ds.mu.Lock()
	ds.drawTicks = ticks
	ds.explicitlyTimed = true
	ds.mu.Unlock()
}

// WeaponDrawTicks returns the elapsed draw/charge ticks on session's active ranged weapon.
func (s *Server) WeaponDrawTicks(session *PlayerSession) int {
	ds := s.playerDrawState(session)
	if ds == nil {
		return 0
	}
	ds.mu.Lock()
	defer ds.mu.Unlock()
	return ds.drawTicks
}

// TickRangedWeaponDraw increments session's active weapon draw counter by 1 tick
// and emits crossbow mid-charge / charge-complete sounds at the appropriate ticks.
func (s *Server) TickRangedWeaponDraw(session *PlayerSession) {
	if s == nil || session == nil || session.IsDead() {
		return
	}
	ds := s.playerDrawState(session)
	if ds == nil {
		return
	}
	ds.mu.Lock()
	if !ds.active {
		ds.mu.Unlock()
		return
	}
	hand := ds.hand
	weapon := ds.weapon
	ds.drawTicks++
	ticks := ds.drawTicks
	ds.explicitlyTimed = true
	ds.mu.Unlock()

	slot, curStack := session.handSlotAndStack(hand)
	if curStack.IsEmpty() || canonicalItemName(curStack.ResolvedID()) != weapon {
		ds.mu.Lock()
		ds.active = false
		ds.weapon = ""
		ds.drawTicks = 0
		ds.explicitlyTimed = false
		ds.mu.Unlock()
		return
	}

	if weapon == CrossbowItemID {
		meta := GetSlotItemMeta(session, slot)
		dur := CrossbowChargeDuration(meta.EnchantmentLevel("minecraft:quick_charge"))
		snap := session.Snapshot()
		cx := int32(int64(math.Floor(snap.X)) >> 4)
		cz := int32(int64(math.Floor(snap.Z)) >> 4)
		if dur > 0 && ticks == dur/2 {
			s.BroadcastDimensionChunk(snap.Dimension, cx, cz, PlaySoundPacketID,
				EncodeSoundPacket("minecraft:item.crossbow.loading_middle", SoundSourcePlayers, snap.X, snap.Y, snap.Z, 1.0, 1.0, 0))
		}
	}
}

func (s *Server) findBowAmmo(session *PlayerSession, hand int32) (ammoSlot int, ammoStack world.ItemStack) {
	if session == nil {
		return -1, world.ItemStack{}
	}
	otherHand := HandOffHand
	if hand == HandOffHand {
		otherHand = HandMain
	}
	otherSlot, otherStack := session.handSlotAndStack(otherHand)
	if isCrossbowArrowProjectile(otherStack) {
		return otherSlot, otherStack
	}
	for idx := world.HotbarContainerOffset; idx < world.HotbarContainerOffset+world.HotbarSlotCount; idx++ {
		cand := session.InventorySlot(idx)
		if isCrossbowArrowProjectile(cand) {
			return idx, cand
		}
	}
	for idx := 0; idx < world.PlayerInventorySlots; idx++ {
		cand := session.InventorySlot(idx)
		if isCrossbowArrowProjectile(cand) {
			return idx, cand
		}
	}
	return -1, world.ItemStack{}
}

// ShootBow fires an arrow from a `minecraft:bow` held in `hand` after `chargeTicks`
// of draw time, applying BowPowerForTime, `power`, `punch`, `flame`, `infinity`,
// spectral/tipped arrow effects, grounded arrow pickup registration, and
// `TriggerShotArrow` / `TriggerTargetHit`.
func (s *Server) ShootBow(session *PlayerSession, hand int32, chargeTicks int, rng *rand.Rand) (int32, bool) {
	if s == nil || session == nil || session.IsDead() {
		return 0, false
	}
	slot, stack := session.handSlotAndStack(hand)
	if stack.IsEmpty() || canonicalItemName(stack.ResolvedID()) != BowItemID {
		return 0, false
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator {
		return 0, false
	}

	power := BowPowerForTime(chargeTicks)
	if power < BowMinChargePower {
		return 0, false
	}

	meta := GetSlotItemMeta(session, slot)
	hasInfinity := meta.EnchantmentLevel("minecraft:infinity") > 0
	powerLevel := meta.EnchantmentLevel("minecraft:power")
	punchLevel := meta.EnchantmentLevel("minecraft:punch")
	hasFlame := meta.EnchantmentLevel("minecraft:flame") > 0

	ammoSlot, ammoStack := s.findBowAmmo(session, hand)
	if ammoSlot < 0 && snap.GameMode != GameModeCreative {
		return 0, false
	}
	arrowItemID := "minecraft:arrow"
	var potionType string
	if !ammoStack.IsEmpty() {
		arrowItemID = canonicalItemName(ammoStack.ResolvedID())
		if arrowItemID == TippedArrowItemID && ammoSlot >= 0 {
			potionType = GetSlotArrowPotionType(session, ammoSlot)
		}
	}

	// Determine arrow pickup rule and consume ammo unless Creative or Infinity on regular arrow.
	pickupRule := ArrowPickupAllowed
	if snap.GameMode == GameModeCreative {
		pickupRule = ArrowPickupCreativeOnly
	} else {
		isRegularArrow := arrowItemID == "minecraft:arrow"
		if hasInfinity && isRegularArrow {
			pickupRule = ArrowPickupCreativeOnly
		} else if ammoSlot >= 0 {
			var updated world.ItemStack
			if ammoStack.Count > 1 {
				updated = world.NewItemStack(ammoStack.ResolvedItemID(), ammoStack.Count-1)
			} else if arrowItemID == TippedArrowItemID {
				SetSlotPotionType(session, ammoSlot, "")
			}
			session.SetInventorySlot(ammoSlot, updated)
			if session.send != nil {
				stID := session.NextContainerStateID()
				_ = session.send(protocol.PlayContainerSetSlot, EncodeContainerSlot(0, stID, ammoSlot, updated))
			}
		}
		s.DamagePlayerItem(session, slot, 1)
	}

	s.RecordItemUsed(session, BowItemID, 1)
	dmg := ComputeBowArrowDamage(power, powerLevel)
	velocity := float64(power) * BowMaxArrowVelocity
	vx, vy, vz := ComputeProjectileShootVector(snap.Yaw, snap.Pitch, velocity, BowDefaultInaccuracy, rng)

	entTypeName := MobTypeArrow
	if arrowItemID == SpectralArrowItemID {
		entTypeName = EntityTypeSpectralArrow
	}
	var projID int32 = -1
	if s.entities != nil {
		typeID := registry.EntityTypeIndex(entTypeName)
		if typeID < 0 {
			typeID = EntityTypeArrowID
		}
		projID = s.entities.Add(&world.Entity{
			TypeID:    typeID,
			TypeName:  entTypeName,
			Dimension: world.NormalizeDimension(snap.Dimension),
			X:         snap.X,
			Y:         snap.Y + 1.5,
			Z:         snap.Z,
			Yaw:       snap.Yaw,
			Pitch:     snap.Pitch,
			VelocityX: clampVelocityShort(vx),
			VelocityY: clampVelocityShort(vy),
			VelocityZ: clampVelocityShort(vz),
		})
		if ent := s.entities.Get(projID); ent != nil {
			cp := ent.ChunkPos()
			s.BroadcastDimensionChunk(ent.Dimension, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, ent))
		}
	}
	if hasFlame && projID > 0 {
		s.SetEntityFireTicks(projID, FlameArrowFireTicks)
	}

	cx := int32(int64(math.Floor(snap.X)) >> 4)
	cz := int32(int64(math.Floor(snap.Z)) >> 4)
	s.BroadcastDimensionChunk(snap.Dimension, cx, cz, PlaySoundPacketID,
		EncodeSoundPacket("minecraft:entity.arrow.shoot", SoundSourcePlayers, snap.X, snap.Y, snap.Z, 1.0, 1.0, 0))

	s.resolveArrowTrajectory(
		session,
		projID,
		arrowItemID,
		potionType,
		snap.Yaw,
		snap.Pitch,
		dmg,
		punchLevel,
		hasFlame,
		0, // piercingLevel
		pickupRule,
		32.0,
	)
	return projID, true
}

func (s *Server) resolveArrowTrajectory(
	session *PlayerSession,
	projID int32,
	arrowItemID string,
	potionType string,
	yaw, pitch float32,
	damage float32,
	punchLevel int,
	onFire bool,
	piercingLevel int,
	pickupRule ArrowPickupRule,
	maxRange float64,
) (killedMobTypes []string) {
	if s == nil || session == nil {
		return nil
	}
	snap := session.Snapshot()
	dim := world.NormalizeDimension(snap.Dimension)
	startX, startY, startZ := snap.X, snap.Y+1.5, snap.Z
	if projID == 0 {
		projID = -1
	}
	if s.PlayerFireTicks(session) > 0 {
		onFire = true
	}
	if onFire && projID > 0 && !s.IsEntityOnFire(projID) {
		s.SetEntityFireTicks(projID, FlameArrowFireTicks)
	}

	s.EmitVibration(VibrationEvent{
		Dimension:          dim,
		Type:               GameEventProjectileShoot,
		X:                  startX,
		Y:                  startY,
		Z:                  startZ,
		SourceEntityID:     snap.EntityID,
		ProjectileEntityID: projID,
		Player:             session,
		Sneaking:           session.IsSneaking(),
		BlockOrItemID:      arrowItemID,
	})

	lx, ly, lz := ElytraLookVector(yaw, pitch)
	landX := startX + lx*maxRange
	landY := startY + ly*maxRange
	landZ := startZ + lz*maxRange

	maxEntityHits := 1
	if piercingLevel > 0 {
		maxEntityHits = piercingLevel + 1
	}
	hitEntities := make(map[int32]bool)
	entityHits := 0
	consumedArrowOnHit := false

	cache := s.DimensionChunks(dim)
	steps := int(maxRange * 2.0)
	if steps < 1 {
		steps = 1
	}
	prevX, prevY, prevZ := startX, startY, startZ

	for step := 1; step <= steps; step++ {
		dist := float64(step) * 0.5
		rx := startX + lx*dist
		ry := startY + ly*dist
		rz := startZ + lz*dist

		// 1. Check online players (PvP arrow hit + Shield directional deflection unless Piercing > 0).
		for _, targetPlayer := range s.onlinePlayersSnapshot() {
			if targetPlayer == nil || targetPlayer == session || targetPlayer.IsDead() || hitEntities[targetPlayer.EntityID] {
				continue
			}
			tSnap := targetPlayer.Snapshot()
			if world.NormalizeDimension(tSnap.Dimension) != dim {
				continue
			}
			if math.Abs(tSnap.X-rx) <= 0.8 && ry >= tSnap.Y-0.2 && ry <= tSnap.Y+2.0 && math.Abs(tSnap.Z-rz) <= 0.8 {
				if !s.CanAttackPlayer(session, targetPlayer) {
					continue
				}
				hitEntities[targetPlayer.EntityID] = true
				if s.DeflectArrowFromSource(targetPlayer, projID, damage, snap.X, snap.Z, true, piercingLevel) {
					landX, landY, landZ = rx, ry, rz
					entityHits = maxEntityHits
					break
				}
				dealt := s.DamagePlayerWithArmorBypassShield(targetPlayer, damage, "arrow")
				if dealt > 0 {
					if onFire {
						s.SetPlayerFireTicks(targetPlayer, FlameTargetFireTicks)
					}
					s.applyArrowPotionEffectsToPlayer(targetPlayer, arrowItemID, potionType)
					if punchLevel > 0 {
						kb := BaseKnockbackStrength + float64(punchLevel)*PunchKnockbackPerLvl
						kvx, kvy, kvz := ComputeKnockback(snap.X, snap.Z, yaw, tSnap.X, tSnap.Z, kb)
						if targetPlayer.send != nil {
							_ = targetPlayer.send(protocol.PlaySetEntityMotion, EncodeSetEntityMotionPacket(targetPlayer.EntityID, kvx, kvy, kvz))
						}
					}
					s.TriggerShotArrow(session, "minecraft:player", targetPlayer.IsDead(), math.Hypot(rx-startX, rz-startZ))
				}
				entityHits++
				if piercingLevel <= 0 {
					consumedArrowOnHit = true
				}
				if entityHits >= maxEntityHits {
					landX, landY, landZ = rx, ry, rz
					break
				}
			}
		}
		if entityHits >= maxEntityHits {
			break
		}

		// 2. Check world entities (mobs, armor stands, vehicles).
		if s.entities != nil {
			cx := int32(int64(math.Floor(rx)) >> 4)
			cz := int32(int64(math.Floor(rz)) >> 4)
			nearby := s.entities.AppendInChunkRange(nil, cx, cz, 1)
			for i := range nearby {
				ent := &nearby[i]
				if ent.ID == snap.EntityID || ent.ID == projID || hitEntities[ent.ID] ||
					ent.IsItem() || ent.IsExperienceOrb() ||
					ent.TypeName == EntityTypeFireworkRocket || IsHangingEntity(ent.TypeName) ||
					IsStandardProjectileEntityType(ent.TypeName) {
					continue
				}
				if ent.EffectiveDimension() != dim {
					continue
				}
				if math.Abs(ent.X-rx) <= 0.8 && ry >= ent.Y-0.2 && ry <= ent.Y+2.0 && math.Abs(ent.Z-rz) <= 0.8 {
					hitEntities[ent.ID] = true
					if ent.TypeName == EntityTypeArmorStand {
						dmg := damage
						if dmg <= 0 {
							dmg = 1.0
						}
						s.DamageArmorStand(ent.ID, dmg, "arrow", session)
						consumedArrowOnHit = true
						entityHits = maxEntityHits
						break
					}
					if IsVehicleEntity(ent.TypeName) {
						dmg := damage
						if dmg <= 0 {
							dmg = 1.0
						}
						s.DamageVehicle(ent.ID, dmg, session)
						consumedArrowOnHit = true
						entityHits = maxEntityHits
						break
					}
					normType := canonicalResourceName(ent.TypeName)
					if normType == MobTypeEnderman {
						// Endermen dodge/reflect arrows and cannot be hit by projectiles.
						s.OnEndermanDamaged(ent.ID, session, nil)
						entityHits = maxEntityHits
						break
					}
					if normType == MobTypeBreeze {
						if _, _, _, deflected := s.TryDeflectProjectileByBreeze(ent.ID, arrowItemID, lx, ly, lz); deflected {
							entityHits = maxEntityHits
							break
						}
					}

					kb := 0.3 + float64(punchLevel)*PunchKnockbackPerLvl
					var killed bool
					switch ent.TypeName {
					case MobTypeWither:
						_, killed = s.DamageWither(ent.ID, damage, session, true)
					case MobTypeEnderDragon:
						_, killed = s.DamageEnderDragon(ent.ID, damage, session, true)
					default:
						killed = s.applyDamageToMob(ent, damage, session, kb, damage >= 6.0, true)
					}
					if !killed {
						if onFire {
							s.SetEntityFireTicks(ent.ID, FlameTargetFireTicks)
						}
						s.applyArrowPotionEffectsToEntity(ent.ID, arrowItemID, potionType)
					} else {
						killedMobTypes = append(killedMobTypes, normType)
					}
					s.TriggerShotArrow(session, normType, killed, math.Hypot(rx-startX, rz-startZ))
					entityHits++
					if piercingLevel <= 0 {
						consumedArrowOnHit = true
					}
					if entityHits >= maxEntityHits {
						break
					}
				}
			}
			if entityHits >= maxEntityHits {
				landX, landY, landZ = rx, ry, rz
				break
			}
		}

		// 3. Check block impact.
		if cache != nil {
			bx := int(math.Floor(rx))
			by := int(math.Floor(ry))
			bz := int(math.Floor(rz))
			hitState := cache.GetBlock(bx, by, bz)
			if world.IsBellBlock(hitState) {
				if s.RingBellAt(dim, bx, by, bz, "", projID, session) {
					s.OnVillageBellRung(dim, bx, by, bz)
				}
			}
			if IsCampfireBlock(hitState) && onFire {
				s.TryIgniteCampfireWithProjectile(dim, bx, by, bz, true)
			}
			if !isPassableBlock(hitState) {
				shattered, targetPower := s.OnProjectileBlockImpact(dim, bx, by, bz, prevX, prevY, prevZ, rx, ry, rz, projID, arrowItemID)
				if targetPower > 0 {
					s.TriggerTargetHit(session, targetPower, math.Hypot(rx-startX, rz-startZ))
				}
				if shattered {
					consumedArrowOnHit = true
				}
				landX, landY, landZ = rx, ry, rz
				break
			}
		}
		prevX, prevY, prevZ = rx, ry, rz
	}

	s.EmitVibration(VibrationEvent{
		Dimension:          dim,
		Type:               GameEventProjectileLand,
		X:                  landX,
		Y:                  landY,
		Z:                  landZ,
		SourceEntityID:     snap.EntityID,
		ProjectileEntityID: projID,
		Player:             session,
		Sneaking:           session.IsSneaking(),
		BlockOrItemID:      arrowItemID,
	})

	if projID > 0 && s.entities != nil {
		if consumedArrowOnHit {
			if ent := s.entities.Get(projID); ent != nil {
				cp := ent.ChunkPos()
				s.entities.Remove(projID)
				s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayRemoveEntities, encodeRemoveEntitiesPacket(nil, []int32{projID}))
			}
		} else {
			// Arrow embedded in ground/block (or pierced through targets and landed) -> register for pickup!
			s.entities.Update(projID, func(e *world.Entity) {
				e.X = landX
				e.Y = landY
				e.Z = landZ
				e.VelocityX = 0
				e.VelocityY = 0
				e.VelocityZ = 0
				e.OnGround = true
			})
			arrowStack := world.NewNamedItemStack(arrowItemID, 1)
			mgr := s.rangedManager()
			mgr.mu.Lock()
			mgr.arrows[projID] = &GroundedArrowState{
				EntityID:   projID,
				Dimension:  dim,
				X:          landX,
				Y:          landY,
				Z:          landZ,
				Stack:      arrowStack,
				PotionType: potionType,
				PickupRule: pickupRule,
				OwnerID:    snap.EntityID,
			}
			mgr.mu.Unlock()
		}
	}
	return killedMobTypes
}

// GetSlotArrowPotionType returns the canonical potion ID stored on session's
// inventory slot (supporting `minecraft:tipped_arrow` as well as potion bottles).
func GetSlotArrowPotionType(session *PlayerSession, slot int) string {
	if session == nil || slot < 0 || slot >= world.PlayerInventorySlots {
		return ""
	}
	if pot := GetSlotPotionType(session, slot); pot != "" {
		return pot
	}
	tr := getPotionMetaTracker(session)
	if tr == nil {
		return PotionWater
	}
	tr.mu.Lock()
	raw := tr.invPotions[slot]
	tr.mu.Unlock()
	if raw == "" {
		return PotionWater
	}
	return canonicalPotionID(raw)
}

func (s *Server) applyArrowPotionEffectsToPlayer(target *PlayerSession, arrowItemID, potionType string) {
	if s == nil || target == nil {
		return
	}
	switch canonicalItemName(arrowItemID) {
	case SpectralArrowItemID:
		s.ApplyPlayerEffect(target, NewStatusEffectInstance(EffectGlowing, SpectralGlowingTicks, 0))
	case TippedArrowItemID:
		if potionType == "" {
			return
		}
		for _, ef := range PotionEffects(potionType) {
			if def, ok := MobEffectByID(ef.ID); ok && def.Instantaneous {
				s.ApplyPlayerEffectScaled(target, ef, 1.0)
			} else {
				scaled := ef.Clone()
				scaled.Duration = ef.Duration / 8
				if scaled.Duration < 1 {
					scaled.Duration = 1
				}
				s.ApplyPlayerEffect(target, scaled)
			}
		}
	}
}

func (s *Server) applyArrowPotionEffectsToEntity(entityID int32, arrowItemID, potionType string) {
	if s == nil || entityID <= 0 {
		return
	}
	switch canonicalItemName(arrowItemID) {
	case SpectralArrowItemID:
		s.ApplyEntityEffectScaled(entityID, NewStatusEffectInstance(EffectGlowing, SpectralGlowingTicks, 0), 1.0)
	case TippedArrowItemID:
		if potionType == "" {
			return
		}
		for _, ef := range PotionEffects(potionType) {
			if def, ok := MobEffectByID(ef.ID); ok && def.Instantaneous {
				s.ApplyEntityEffectScaled(entityID, ef, 1.0)
			} else {
				s.ApplyEntityEffectScaled(entityID, ef, 0.125)
			}
		}
	}
}

// GroundedArrowByID returns a copy of the GroundedArrowState for entityID if present.
func (s *Server) GroundedArrowByID(entityID int32) (GroundedArrowState, bool) {
	if s == nil || entityID <= 0 {
		return GroundedArrowState{}, false
	}
	mgr := s.rangedManager()
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	st, ok := mgr.arrows[entityID]
	if !ok || st == nil {
		return GroundedArrowState{}, false
	}
	return *st, true
}

// TryPickupGroundedProjectiles checks for nearby grounded arrows and non-loyalty
// tridents around session and picks them up according to vanilla pickup rules.
func (s *Server) TryPickupGroundedProjectiles(session *PlayerSession) int32 {
	if s == nil || session == nil || session.IsDead() {
		return 0
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator || snap.Health <= 0 {
		return 0
	}
	playerDim := world.NormalizeDimension(snap.Dimension)
	mgr := s.rangedManager()

	var picked int32

	// 1. Check grounded arrows.
	mgr.mu.Lock()
	arrowIDs := make([]int32, 0, len(mgr.arrows))
	for id := range mgr.arrows {
		arrowIDs = append(arrowIDs, id)
	}
	mgr.mu.Unlock()

	for _, id := range arrowIDs {
		mgr.mu.Lock()
		st, ok := mgr.arrows[id]
		if !ok || st == nil {
			mgr.mu.Unlock()
			continue
		}
		arrowCopy := *st
		mgr.mu.Unlock()

		if arrowCopy.Dimension != playerDim || arrowCopy.PickupRule == ArrowPickupDisallowed {
			continue
		}
		if math.Abs(arrowCopy.X-snap.X) > ItemPickupHorizontalRadius ||
			math.Abs(arrowCopy.Z-snap.Z) > ItemPickupHorizontalRadius ||
			math.Abs(arrowCopy.Y-snap.Y) > ItemPickupVerticalRadius {
			continue
		}
		if arrowCopy.PickupRule == ArrowPickupCreativeOnly && snap.GameMode != GameModeCreative {
			continue
		}

		var changedSlots []int
		if arrowCopy.PickupRule == ArrowPickupAllowed {
			added, slots := session.AddItemToInventory(arrowCopy.Stack)
			if added <= 0 && snap.GameMode != GameModeCreative {
				continue
			}
			changedSlots = slots
			if arrowCopy.PotionType != "" {
				for _, sl := range changedSlots {
					SetSlotPotionType(session, sl, arrowCopy.PotionType)
				}
			}
		}

		mgr.mu.Lock()
		delete(mgr.arrows, id)
		mgr.mu.Unlock()

		picked++
		cx := int32(int64(math.Floor(arrowCopy.X)) >> 4)
		cz := int32(int64(math.Floor(arrowCopy.Z)) >> 4)
		s.BroadcastDimensionChunk(playerDim, cx, cz, protocol.PlayTakeItemEntity,
			EncodeTakeItemEntity(id, snap.EntityID, 1))
		if s.entities != nil {
			s.entities.Remove(id)
		}
		s.BroadcastDimensionChunk(playerDim, cx, cz, protocol.PlayRemoveEntities,
			encodeRemoveEntitiesPacket(nil, []int32{id}))

		if session.send != nil {
			for _, sl := range changedSlots {
				stID := session.NextContainerStateID()
				_ = session.send(protocol.PlayContainerSetSlot, EncodeContainerSlot(0, stID, sl, session.InventorySlot(sl)))
			}
		}
	}

	// 2. Check grounded non-Loyalty tridents owned by session.
	mgr.mu.Lock()
	tridentIDs := make([]int32, 0, len(mgr.tridents))
	for id, tr := range mgr.tridents {
		if tr != nil && tr.Grounded && !tr.Returning && tr.Owner == session {
			tridentIDs = append(tridentIDs, id)
		}
	}
	mgr.mu.Unlock()

	for _, id := range tridentIDs {
		mgr.mu.Lock()
		tr := mgr.tridents[id]
		if tr == nil || !tr.Grounded {
			mgr.mu.Unlock()
			continue
		}
		tx, ty, tz, tDim := tr.X, tr.Y, tr.Z, tr.Dimension
		mgr.mu.Unlock()

		if tDim != playerDim {
			continue
		}
		if math.Abs(tx-snap.X) > ItemPickupHorizontalRadius ||
			math.Abs(tz-snap.Z) > ItemPickupHorizontalRadius ||
			math.Abs(ty-snap.Y) > ItemPickupVerticalRadius {
			continue
		}
		if s.ReturnThrownTrident(id) {
			picked++
		}
	}

	return picked
}

// ---------------------------------------------------------------------------
// 2. Crossbow Mechanics (Quick Charge, Multishot, Piercing, ChargedProjectiles Codec)
// ---------------------------------------------------------------------------

// CrossbowChargeDuration returns the number of ticks required to charge a
// `minecraft:crossbow` with `quickChargeLevel` (`minecraft:quick_charge` 0..3+):
// `max(0, 25 - 5 * quickChargeLevel)`.
func CrossbowChargeDuration(quickChargeLevel int) int {
	if quickChargeLevel < 0 {
		quickChargeLevel = 0
	}
	dur := CrossbowBaseChargeTicks - CrossbowQuickChargeStep*quickChargeLevel
	if dur < 0 {
		return 0
	}
	return dur
}

// EncodeChargedProjectilesComponent writes the protocol 775 payload of
// DataComponentChargedProjectiles (49): VarInt(len) + ItemStack[len].
func EncodeChargedProjectilesComponent(w *protocol.Writer, projs []ChargedProjectile) {
	w.VarInt(int32(len(projs)))
	for _, p := range projs {
		if p.Fireworks != nil {
			EncodeSlotWithFireworks(w, p.Stack, *p.Fireworks)
		} else if p.Stack.IsEmpty() || p.Stack.ResolvedItemID() <= 0 {
			w.VarInt(0)
		} else {
			w.VarInt(p.Stack.Count)
			w.VarInt(p.Stack.ResolvedItemID())
			w.VarInt(0)
			w.VarInt(0)
		}
	}
}

// DecodeChargedProjectilesComponent reads the protocol 775 payload of
// DataComponentChargedProjectiles (49).
func DecodeChargedProjectilesComponent(r *protocol.Reader) ([]ChargedProjectile, error) {
	count, err := r.VarInt()
	if err != nil {
		return nil, err
	}
	if count < 0 || count > 16 {
		return nil, fmt.Errorf("server: invalid charged projectile count %d", count)
	}
	out := make([]ChargedProjectile, 0, count)
	for i := int32(0); i < count; i++ {
		itemCount, err := r.VarInt()
		if err != nil {
			return nil, err
		}
		if itemCount <= 0 {
			out = append(out, ChargedProjectile{})
			continue
		}
		itemID, err := r.VarInt()
		if err != nil {
			return nil, err
		}
		addComp, err := r.VarInt()
		if err != nil {
			return nil, err
		}
		remComp, err := r.VarInt()
		if err != nil {
			return nil, err
		}
		cp := ChargedProjectile{Stack: world.NewItemStack(itemID, itemCount)}
		for a := int32(0); a < addComp; a++ {
			compID, err := r.VarInt()
			if err != nil {
				return nil, err
			}
			if compID == DataComponentFireworks {
				fw, err := DecodeFireworksComponent(r)
				if err != nil {
					return nil, err
				}
				cp.Fireworks = &fw
			}
		}
		for rm := int32(0); rm < remComp; rm++ {
			if _, err := r.VarInt(); err != nil {
				return nil, err
			}
		}
		out = append(out, cp)
	}
	return out, nil
}

// EncodeSlotWithChargedProjectiles writes a protocol 775 ItemStack carrying a
// `minecraft:charged_projectiles` (49) component.
func EncodeSlotWithChargedProjectiles(w *protocol.Writer, stack world.ItemStack, projs []ChargedProjectile) {
	if stack.IsEmpty() {
		w.VarInt(0)
		return
	}
	itemID := stack.ResolvedItemID()
	if itemID <= 0 {
		w.VarInt(0)
		return
	}
	if len(projs) == 0 {
		w.VarInt(stack.Count)
		w.VarInt(itemID)
		w.VarInt(0)
		w.VarInt(0)
		return
	}
	w.VarInt(stack.Count)
	w.VarInt(itemID)
	w.VarInt(1) // 1 added component
	w.VarInt(0) // 0 removed components
	w.VarInt(DataComponentChargedProjectiles)
	EncodeChargedProjectilesComponent(w, projs)
}

func (s *Server) findCrossbowAmmo(session *PlayerSession, hand int32) (ammoSlot int, ammoStack world.ItemStack) {
	if session == nil {
		return -1, world.ItemStack{}
	}
	otherHand := HandOffHand
	if hand == HandOffHand {
		otherHand = HandMain
	}
	otherSlot, otherStack := session.handSlotAndStack(otherHand)
	if isCrossbowHeldProjectile(otherStack) {
		return otherSlot, otherStack
	}
	for idx := world.HotbarContainerOffset; idx < world.HotbarContainerOffset+world.HotbarSlotCount; idx++ {
		st := session.InventorySlot(idx)
		if isCrossbowArrowProjectile(st) {
			return idx, st
		}
	}
	for idx := 9; idx < world.HotbarContainerOffset; idx++ {
		st := session.InventorySlot(idx)
		if isCrossbowArrowProjectile(st) {
			return idx, st
		}
	}
	for idx := 0; idx < 9; idx++ {
		st := session.InventorySlot(idx)
		if isCrossbowArrowProjectile(st) {
			return idx, st
		}
	}
	if session.Snapshot().GameMode == GameModeCreative {
		return -1, world.NewNamedItemStack("minecraft:arrow", 1)
	}
	return -1, world.ItemStack{}
}

// ReleaseCrossbowDraw handles releasing `use_item` while charging a
// `minecraft:crossbow`: if `drawTicks >= CrossbowChargeDuration(quickChargeLevel)`,
// loads the projectile(s) into `ChargedProjectiles`; if released early, aborts
// charging without consuming ammo.
func (s *Server) ReleaseCrossbowDraw(session *PlayerSession, hand int32, drawTicks int) bool {
	if s == nil || session == nil || session.IsDead() {
		return false
	}
	slot, stack := session.handSlotAndStack(hand)
	if stack.IsEmpty() || canonicalItemName(stack.ResolvedID()) != CrossbowItemID {
		return false
	}
	if len(GetSlotChargedProjectiles(session, slot)) > 0 {
		return false
	}
	meta := GetSlotItemMeta(session, slot)
	reqTicks := CrossbowChargeDuration(meta.EnchantmentLevel("minecraft:quick_charge"))
	if drawTicks < reqTicks {
		return false
	}
	return s.LoadCrossbowProjectile(session, hand)
}

// UnloadCrossbow clears any charged projectiles from the crossbow in `slot` and
// syncs the updated slot to `session`.
func (s *Server) UnloadCrossbow(session *PlayerSession, slot int) bool {
	if s == nil || session == nil {
		return false
	}
	if len(GetSlotChargedProjectiles(session, slot)) == 0 {
		return false
	}
	ClearSlotChargedProjectiles(session, slot)
	if session.send != nil {
		stID := session.NextContainerStateID()
		_ = session.send(protocol.PlayContainerSetSlot, EncodeContainerSlot(0, stID, slot, session.InventorySlot(slot)))
	}
	return true
}

// ---------------------------------------------------------------------------
// 3. Trident Mechanics (Throw, Loyalty, Riptide, Channeling, Impaling)
// ---------------------------------------------------------------------------

// IsAquaticMobForImpaling reports whether `typeName` takes extra damage from
// the `minecraft:impaling` enchantment in vanilla 26.1.2 (all aquatic mobs).
func IsAquaticMobForImpaling(typeName string) bool {
	switch canonicalResourceName(typeName) {
	case MobTypeAxolotl,
		MobTypeCod,
		MobTypeDolphin,
		MobTypeDrowned,
		MobTypeElderGuardian,
		MobTypeGlowSquid,
		MobTypeGuardian,
		MobTypePufferfish,
		MobTypeSalmon,
		MobTypeSquid,
		MobTypeTadpole,
		MobTypeTropicalFish,
		MobTypeTurtle:
		return true
	default:
		return false
	}
}

// ImpalingDamageBonus returns the extra damage (`+2.5 * level`) dealt by a
// weapon with `minecraft:impaling` level `impalingLevel` against `targetType`
// (or any target in water/rain when `targetInWaterOrRain` is true).
func ImpalingDamageBonus(impalingLevel int, targetType string, targetInWaterOrRain bool) float32 {
	if impalingLevel <= 0 {
		return 0
	}
	if IsAquaticMobForImpaling(targetType) || targetInWaterOrRain {
		return float32(impalingLevel) * TridentImpalingBonusPerLvl
	}
	return 0
}

// CanUseRiptide reports whether `session` is currently in water, a bubble
// column, or exposed to rain/thunder, allowing a `minecraft:riptide` trident
// to launch the player.
func (s *Server) CanUseRiptide(session *PlayerSession) bool {
	if s == nil || session == nil || session.IsDead() {
		return false
	}
	snap := session.Snapshot()
	dim := world.NormalizeDimension(snap.Dimension)
	cache := s.DimensionChunks(dim)
	bx := int(math.Floor(snap.X))
	by := int(math.Floor(snap.Y))
	bz := int(math.Floor(snap.Z))
	if cache != nil {
		if isWaterOrBubbleOrWaterloggedAt(cache, bx, by, bz) || isWaterOrBubbleOrWaterloggedAt(cache, bx, by+1, bz) {
			return true
		}
	}
	if dim == world.DimensionOverworld && (s.IsRaining() || s.IsThundering()) {
		if cache == nil || isSkyExposedAt(cache, bx, by+1, bz) {
			return true
		}
	}
	return false
}

func isSkyExposedAt(cache *world.Cache, bx, by, bz int) bool {
	if cache == nil {
		return true
	}
	maxY := world.MinY + world.WorldHeight - 1
	for y := by; y <= maxY; y++ {
		st := cache.GetBlock(bx, y, bz)
		if st != world.StateAir && !isPassableBlock(st) {
			return false
		}
	}
	return true
}

// TridentRiptideLaunchSpeed returns the launch speed for a `minecraft:riptide`
// trident at level `riptideLevel` (`3.0 * (1 + level) / 4.0`: `1.5`, `2.25`, `3.0`).
func TridentRiptideLaunchSpeed(riptideLevel int) float64 {
	if riptideLevel <= 0 {
		return 0
	}
	return 3.0 * float64(1+riptideLevel) / 4.0
}

// ExecuteRiptideLaunch launches `session` along their look vector using a
// `minecraft:riptide` trident in `slot`, sending `PlaySetEntityMotion`,
// broadcasting `minecraft:item.trident.riptide_1..3`, applying 1 durability wear,
// and executing a spin-attack sweep along the launch path.
func (s *Server) ExecuteRiptideLaunch(session *PlayerSession, slot int, riptideLevel int) bool {
	if s == nil || session == nil || riptideLevel <= 0 || !s.CanUseRiptide(session) {
		return false
	}
	snap := session.Snapshot()
	speed := TridentRiptideLaunchSpeed(riptideLevel)
	lx, ly, lz := ElytraLookVector(snap.Yaw, snap.Pitch)
	vx, vy, vz := lx*speed, ly*speed, lz*speed

	if session.send != nil {
		_ = session.send(protocol.PlaySetEntityMotion, EncodeSetEntityMotionPacket(snap.EntityID, vx, vy, vz))
	}

	meta := GetSlotItemMeta(session, slot)
	impaling := meta.EnchantmentLevel("minecraft:impaling")

	if snap.GameMode == GameModeSurvival || snap.GameMode == GameModeAdventure {
		s.DamagePlayerItem(session, slot, 1)
	}
	s.RecordItemUsed(session, TridentItemID, 1)

	soundName := "minecraft:item.trident.riptide_1"
	if riptideLevel == 2 {
		soundName = "minecraft:item.trident.riptide_2"
	} else if riptideLevel >= 3 {
		soundName = "minecraft:item.trident.riptide_3"
	}
	cx := int32(int64(math.Floor(snap.X)) >> 4)
	cz := int32(int64(math.Floor(snap.Z)) >> 4)
	s.BroadcastDimensionChunk(snap.Dimension, cx, cz, PlaySoundPacketID,
		EncodeSoundPacket(soundName, SoundSourcePlayers, snap.X, snap.Y, snap.Z, 1.0, 1.0, 0))

	if ds := s.playerDrawState(session); ds != nil {
		ds.mu.Lock()
		ds.riptideTicks = TridentRiptideSpinTicks
		ds.riptideImpaling = impaling
		ds.mu.Unlock()
	}

	// Perform immediate spin-attack check along the Riptide dash vector.
	s.executeRiptideSpinSweep(session, snap.X, snap.Y, snap.Z, vx, vy, vz, impaling)
	return true
}

func (s *Server) executeRiptideSpinSweep(session *PlayerSession, startX, startY, startZ, vx, vy, vz float64, impalingLevel int) {
	if s == nil || s.entities == nil || session == nil {
		return
	}
	snap := session.Snapshot()
	dim := world.NormalizeDimension(snap.Dimension)
	cx := int32(int64(math.Floor(startX)) >> 4)
	cz := int32(int64(math.Floor(startZ)) >> 4)
	nearby := s.entities.AppendInChunkRange(nil, cx, cz, 1)
	for i := range nearby {
		ent := &nearby[i]
		if ent.ID == snap.EntityID || ent.IsItem() || ent.IsExperienceOrb() || IsHangingEntity(ent.TypeName) {
			continue
		}
		if ent.EffectiveDimension() != dim {
			continue
		}
		// Check if entity is within the Riptide dash path.
		for step := 0; step <= 4; step++ {
			t := float64(step) * 0.25
			px := startX + vx*t
			py := startY + vy*t
			pz := startZ + vz*t
			if math.Abs(ent.X-px) <= 1.2 && math.Abs(ent.Y-py) <= 2.0 && math.Abs(ent.Z-pz) <= 1.2 {
				dmg := TridentMeleeBaseDamage + ImpalingDamageBonus(impalingLevel, ent.TypeName, true)
				s.applyDamageToMob(ent, dmg, session, BaseKnockbackStrength, false, true)
				break
			}
		}
	}
}

// ThrowTrident throws or Riptide-launches a `minecraft:trident` held in `hand`
// after `chargeTicks` of draw time.
func (s *Server) ThrowTrident(session *PlayerSession, hand int32, chargeTicks int) (int32, bool) {
	if s == nil || session == nil || session.IsDead() {
		return 0, false
	}
	slot, stack := session.handSlotAndStack(hand)
	if stack.IsEmpty() || canonicalItemName(stack.ResolvedID()) != TridentItemID {
		return 0, false
	}
	if chargeTicks < TridentMinChargeTicks {
		return 0, false
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator {
		return 0, false
	}

	meta := GetSlotItemMeta(session, slot)
	riptideLevel := meta.EnchantmentLevel("minecraft:riptide")
	if riptideLevel > 0 {
		if s.ExecuteRiptideLaunch(session, slot, riptideLevel) {
			return snap.EntityID, true
		}
		return 0, false
	}

	loyaltyLevel := meta.EnchantmentLevel("minecraft:loyalty")
	hasChanneling := meta.EnchantmentLevel("minecraft:channeling") > 0
	impalingLevel := meta.EnchantmentLevel("minecraft:impaling")

	// Apply 1 durability wear before removing the thrown trident stack from the player's hand.
	tridentStack := world.NewNamedItemStack(TridentItemID, 1)
	tridentMeta := meta.Clone()
	if snap.GameMode == GameModeSurvival || snap.GameMode == GameModeAdventure {
		broken := s.DamagePlayerItem(session, slot, 1)
		tridentMeta = GetSlotItemMeta(session, slot).Clone()
		SetSlotItemMeta(session, slot, ItemMeta{})
		if !broken {
			var remaining world.ItemStack
			if stack.Count > 1 {
				remaining = world.NewItemStack(stack.ResolvedItemID(), stack.Count-1)
			}
			session.SetInventorySlot(slot, remaining)
			if session.send != nil {
				stID := session.NextContainerStateID()
				_ = session.send(protocol.PlayContainerSetSlot, EncodeContainerSlot(0, stID, slot, remaining))
			}
		}
	}

	s.RecordItemUsed(session, TridentItemID, 1)
	vx, vy, vz := ComputeProjectileShootVector(snap.Yaw, snap.Pitch, TridentThrowVelocity, 1.0, nil)
	dim := world.NormalizeDimension(snap.Dimension)

	var tridentID int32 = -1
	if s.entities != nil {
		typeID := registry.EntityTypeIndex(EntityTypeTrident)
		if typeID < 0 {
			typeID = EntityTypeArrowID
		}
		tridentID = s.entities.Add(&world.Entity{
			TypeID:    typeID,
			TypeName:  EntityTypeTrident,
			Dimension: dim,
			X:         snap.X,
			Y:         snap.Y + 1.5,
			Z:         snap.Z,
			Yaw:       snap.Yaw,
			Pitch:     snap.Pitch,
			VelocityX: clampVelocityShort(vx),
			VelocityY: clampVelocityShort(vy),
			VelocityZ: clampVelocityShort(vz),
		})
		if ent := s.entities.Get(tridentID); ent != nil {
			cp := ent.ChunkPos()
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, ent))
		}
	}

	cx := int32(int64(math.Floor(snap.X)) >> 4)
	cz := int32(int64(math.Floor(snap.Z)) >> 4)
	s.BroadcastDimensionChunk(dim, cx, cz, PlaySoundPacketID,
		EncodeSoundPacket("minecraft:item.trident.throw", SoundSourcePlayers, snap.X, snap.Y, snap.Z, 1.0, 1.0, 0))

	landX, landY, landZ, hitEntity := s.resolveTridentTrajectory(
		session,
		tridentID,
		snap.Yaw,
		snap.Pitch,
		impalingLevel,
		hasChanneling,
		32.0,
	)

	if tridentID > 0 && s.entities != nil {
		s.entities.Update(tridentID, func(e *world.Entity) {
			e.X = landX
			e.Y = landY
			e.Z = landZ
			e.VelocityX = 0
			e.VelocityY = 0
			e.VelocityZ = 0
			e.OnGround = true
		})
		mgr := s.rangedManager()
		mgr.mu.Lock()
		mgr.tridents[tridentID] = &ThrownTridentState{
			EntityID:      tridentID,
			Owner:         session,
			Dimension:     dim,
			X:             landX,
			Y:             landY,
			Z:             landZ,
			Stack:         tridentStack,
			Meta:          tridentMeta,
			OriginalSlot:  slot,
			LoyaltyLevel:  loyaltyLevel,
			Channeling:    hasChanneling,
			ImpalingLevel: impalingLevel,
			Grounded:      true,
			Returning:     loyaltyLevel > 0,
			DealtDamage:   hitEntity,
		}
		mgr.mu.Unlock()
	}

	return tridentID, true
}

func (s *Server) resolveTridentTrajectory(
	session *PlayerSession,
	tridentID int32,
	yaw, pitch float32,
	impalingLevel int,
	hasChanneling bool,
	maxRange float64,
) (landX, landY, landZ float64, hitAnyEntity bool) {
	snap := session.Snapshot()
	dim := world.NormalizeDimension(snap.Dimension)
	startX, startY, startZ := snap.X, snap.Y+1.5, snap.Z
	projID := tridentID
	if projID == 0 {
		projID = -1
	}

	s.EmitVibration(VibrationEvent{
		Dimension:          dim,
		Type:               GameEventProjectileShoot,
		X:                  startX,
		Y:                  startY,
		Z:                  startZ,
		SourceEntityID:     snap.EntityID,
		ProjectileEntityID: projID,
		Player:             session,
		Sneaking:           session.IsSneaking(),
		BlockOrItemID:      TridentItemID,
	})

	lx, ly, lz := ElytraLookVector(yaw, pitch)
	landX = startX + lx*maxRange
	landY = startY + ly*maxRange
	landZ = startZ + lz*maxRange

	cache := s.DimensionChunks(dim)
	steps := int(maxRange * 2.0)
	if steps < 1 {
		steps = 1
	}
	prevX, prevY, prevZ := startX, startY, startZ

	for step := 1; step <= steps; step++ {
		dist := float64(step) * 0.5
		rx := startX + lx*dist
		ry := startY + ly*dist
		rz := startZ + lz*dist

		// 1. Check online players.
		for _, targetPlayer := range s.onlinePlayersSnapshot() {
			if targetPlayer == nil || targetPlayer == session || targetPlayer.IsDead() {
				continue
			}
			tSnap := targetPlayer.Snapshot()
			if world.NormalizeDimension(tSnap.Dimension) != dim {
				continue
			}
			if math.Abs(tSnap.X-rx) <= 0.8 && ry >= tSnap.Y-0.2 && ry <= tSnap.Y+2.0 && math.Abs(tSnap.Z-rz) <= 0.8 {
				if !s.CanAttackPlayer(session, targetPlayer) {
					continue
				}
				hitAnyEntity = true
				landX, landY, landZ = rx, ry, rz
				if !s.DeflectArrowFromSource(targetPlayer, projID, TridentThrownBaseDamage, snap.X, snap.Z, true, 0) {
					inWaterOrRain := s.CanUseRiptide(targetPlayer)
					dmg := TridentThrownBaseDamage + ImpalingDamageBonus(impalingLevel, "minecraft:player", inWaterOrRain)
					s.DamagePlayerWithArmorBypassShield(targetPlayer, dmg, "trident")
					s.TriggerThrownTridentHit(session, false)
					if hasChanneling {
						s.tryStrikeChannelingLightningOnEntity(session, dim, tSnap.X, tSnap.Y, tSnap.Z, 0, "minecraft:player")
					}
				}
				break
			}
		}
		if hitAnyEntity {
			break
		}

		// 2. Check world entities (mobs, armor stands, vehicles).
		if s.entities != nil {
			cx := int32(int64(math.Floor(rx)) >> 4)
			cz := int32(int64(math.Floor(rz)) >> 4)
			nearby := s.entities.AppendInChunkRange(nil, cx, cz, 1)
			for i := range nearby {
				ent := &nearby[i]
				if ent.ID == snap.EntityID || ent.ID == projID || ent.IsItem() || ent.IsExperienceOrb() ||
					ent.TypeName == EntityTypeFireworkRocket || IsHangingEntity(ent.TypeName) ||
					IsStandardProjectileEntityType(ent.TypeName) {
					continue
				}
				if ent.EffectiveDimension() != dim {
					continue
				}
				if math.Abs(ent.X-rx) <= 0.8 && ry >= ent.Y-0.2 && ry <= ent.Y+2.0 && math.Abs(ent.Z-rz) <= 0.8 {
					hitAnyEntity = true
					landX, landY, landZ = rx, ry, rz
					if ent.TypeName == EntityTypeArmorStand {
						s.DamageArmorStand(ent.ID, TridentThrownBaseDamage, "trident", session)
						break
					}
					if IsVehicleEntity(ent.TypeName) {
						s.DamageVehicle(ent.ID, TridentThrownBaseDamage, session)
						break
					}
					normType := canonicalResourceName(ent.TypeName)
					if normType == MobTypeEnderman {
						s.OnEndermanDamaged(ent.ID, session, nil)
						break
					}
					inWaterOrRain := s.IsEntityInWaterOrRainAt(dim, ent.X, ent.Y, ent.Z)
					dmg := TridentThrownBaseDamage + ImpalingDamageBonus(impalingLevel, normType, inWaterOrRain)
					switch ent.TypeName {
					case MobTypeWither:
						s.DamageWither(ent.ID, dmg, session, true)
					case MobTypeEnderDragon:
						s.DamageEnderDragon(ent.ID, dmg, session, true)
					default:
						s.applyDamageToMob(ent, dmg, session, 0.3, false, true)
					}

					tcx := int32(int64(math.Floor(ent.X)) >> 4)
					tcz := int32(int64(math.Floor(ent.Z)) >> 4)
					s.BroadcastDimensionChunk(dim, tcx, tcz, PlaySoundPacketID,
						EncodeSoundPacket("minecraft:item.trident.hit", SoundSourcePlayers, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))

					struckVillager := false
					if hasChanneling {
						struckVillager = s.tryStrikeChannelingLightningOnEntity(session, dim, ent.X, ent.Y, ent.Z, ent.ID, normType)
					}
					s.TriggerThrownTridentHit(session, struckVillager)
					break
				}
			}
			if hitAnyEntity {
				break
			}
		}

		// 3. Check block impact.
		if cache != nil {
			bx := int(math.Floor(rx))
			by := int(math.Floor(ry))
			bz := int(math.Floor(rz))
			hitState := cache.GetBlock(bx, by, bz)
			if world.IsBellBlock(hitState) {
				if s.RingBellAt(dim, bx, by, bz, "", projID, session) {
					s.OnVillageBellRung(dim, bx, by, bz)
				}
			}
			if !isPassableBlock(hitState) {
				_, targetPower := s.OnProjectileBlockImpact(dim, bx, by, bz, prevX, prevY, prevZ, rx, ry, rz, projID, TridentItemID)
				if targetPower > 0 {
					s.TriggerTargetHit(session, targetPower, math.Hypot(rx-startX, rz-startZ))
				}
				if hasChanneling && world.BlockName(hitState) == "minecraft:lightning_rod" && s.IsThundering() && isSkyExposedAt(cache, bx, by+1, bz) {
					s.strikeChannelingLightningOnRod(session, dim, bx, by, bz)
				}
				bcx := int32(bx >> 4)
				bcz := int32(bz >> 4)
				s.BroadcastDimensionChunk(dim, bcx, bcz, PlaySoundPacketID,
					EncodeSoundPacket("minecraft:item.trident.hit_ground", SoundSourcePlayers, rx, ry, rz, 1.0, 1.0, 0))
				landX, landY, landZ = rx, ry, rz
				break
			}
		}
		prevX, prevY, prevZ = rx, ry, rz
	}

	s.EmitVibration(VibrationEvent{
		Dimension:          dim,
		Type:               GameEventProjectileLand,
		X:                  landX,
		Y:                  landY,
		Z:                  landZ,
		SourceEntityID:     snap.EntityID,
		ProjectileEntityID: projID,
		Player:             session,
		Sneaking:           session.IsSneaking(),
		BlockOrItemID:      TridentItemID,
	})
	return landX, landY, landZ, hitAnyEntity
}

// IsEntityInWaterOrRainAt reports whether coordinates (x, y, z) in dim are
// inside water/bubble column or exposed to Overworld rain/thunder.
func (s *Server) IsEntityInWaterOrRainAt(dim string, x, y, z float64) bool {
	if s == nil {
		return false
	}
	normDim := world.NormalizeDimension(dim)
	cache := s.DimensionChunks(normDim)
	bx := int(math.Floor(x))
	by := int(math.Floor(y))
	bz := int(math.Floor(z))
	if cache != nil && (isWaterOrBubbleOrWaterloggedAt(cache, bx, by, bz) || isWaterOrBubbleOrWaterloggedAt(cache, bx, by+1, bz)) {
		return true
	}
	if normDim == world.DimensionOverworld && (s.IsRaining() || s.IsThundering()) {
		if cache == nil || isSkyExposedAt(cache, bx, by+1, bz) {
			return true
		}
	}
	return false
}

func (s *Server) tryStrikeChannelingLightningOnEntity(
	session *PlayerSession,
	dim string,
	x, y, z float64,
	targetEntityID int32,
	targetType string,
) (struckVillager bool) {
	if s == nil || !s.IsThundering() {
		return false
	}
	normDim := world.NormalizeDimension(dim)
	cache := s.DimensionChunks(normDim)
	bx, by, bz := int(math.Floor(x)), int(math.Floor(y)), int(math.Floor(z))
	if cache != nil && !isSkyExposedAt(cache, bx, by+1, bz) {
		return false
	}

	boltID, sx, sy, sz, redirected := s.SpawnLightningBolt(normDim, x, y, z, nil)
	if boltID <= 0 {
		return false
	}
	cx := int32(int64(math.Floor(sx)) >> 4)
	cz := int32(int64(math.Floor(sz)) >> 4)
	s.BroadcastDimensionChunk(normDim, cx, cz, PlaySoundPacketID,
		EncodeSoundPacket("minecraft:item.trident.thunder", SoundSourcePlayers, sx, sy, sz, 5.0, 1.0, 0))

	normTarget := canonicalResourceName(targetType)
	if redirected {
		if normTarget == MobTypeVillager {
			s.TriggerLightningStrike(session, true, true, false)
		}
		return false
	}

	if targetEntityID > 0 && s.entities != nil {
		switch normTarget {
		case MobTypeVillager:
			struckVillager = true
			_, _ = s.OnVillagerStruckByLightning(targetEntityID)
		case MobTypePig:
			if s.Difficulty() != DifficultyPeaceful {
				if ent := s.entities.Get(targetEntityID); ent != nil {
					px, py, pz := ent.X, ent.Y, ent.Z
					s.entities.Remove(targetEntityID)
					s.BroadcastDimensionChunk(normDim, cx, cz, protocol.PlayRemoveEntities, encodeRemoveEntitiesPacket(nil, []int32{targetEntityID}))
					_, _ = s.SpawnMobInDimension(normDim, px, py, pz, MobTypeZombifiedPiglin)
				}
			}
		case MobTypeCreeper:
			_ = s.ChargeCreepersNearLightning(normDim, sx, sy, sz)
		default:
			if ent := s.entities.Get(targetEntityID); ent != nil {
				s.applyDamageToMob(ent, ChannelingLightningDamage, session, 0, false, false)
			}
		}
	}
	return struckVillager
}

func (s *Server) strikeChannelingLightningOnRod(session *PlayerSession, dim string, bx, by, bz int) {
	normDim := world.NormalizeDimension(dim)
	sx := float64(bx) + 0.5
	sy := float64(by + 1)
	sz := float64(bz) + 0.5
	boltID, _, _, _, _ := s.SpawnLightningBolt(normDim, sx, sy, sz, nil)
	if boltID <= 0 {
		return
	}
	cx := int32(bx >> 4)
	cz := int32(bz >> 4)
	s.BroadcastDimensionChunk(normDim, cx, cz, PlaySoundPacketID,
		EncodeSoundPacket("minecraft:item.trident.thunder", SoundSourcePlayers, sx, sy, sz, 5.0, 1.0, 0))

	villagerNearby := false
	if s.entities != nil {
		nearby := s.entities.AppendInChunkRange(nil, cx, cz, 1)
		for i := range nearby {
			ent := &nearby[i]
			if canonicalResourceName(ent.TypeName) == MobTypeVillager &&
				math.Hypot(ent.X-sx, ent.Z-sz) <= 16.0 {
				villagerNearby = true
				break
			}
		}
	}
	s.TriggerLightningStrike(session, true, villagerNearby, false)
}

// ThrownTridentByID returns a copy of the ThrownTridentState for entityID if present.
func (s *Server) ThrownTridentByID(entityID int32) (ThrownTridentState, bool) {
	if s == nil || entityID <= 0 {
		return ThrownTridentState{}, false
	}
	mgr := s.rangedManager()
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	tr, ok := mgr.tridents[entityID]
	if !ok || tr == nil {
		return ThrownTridentState{}, false
	}
	copySt := *tr
	copySt.Meta = tr.Meta.Clone()
	return copySt, true
}

// ReturnThrownTrident returns the thrown trident with `tridentEntityID` to its
// owner's inventory (preferring `OriginalSlot` if empty), preserves its
// `ItemMeta` (durability and enchantments), broadcasts `PlayTakeItemEntity`,
// `PlayRemoveEntities`, and `minecraft:item.trident.return`.
func (s *Server) ReturnThrownTrident(tridentEntityID int32) bool {
	if s == nil || tridentEntityID <= 0 {
		return false
	}
	mgr := s.rangedManager()
	mgr.mu.Lock()
	tr, ok := mgr.tridents[tridentEntityID]
	if !ok || tr == nil || tr.Owner == nil || tr.Owner.IsDead() {
		mgr.mu.Unlock()
		return false
	}
	owner := tr.Owner
	snap := owner.Snapshot()
	if snap.GameMode == GameModeSpectator {
		mgr.mu.Unlock()
		return false
	}
	trCopy := *tr
	trCopy.Meta = tr.Meta.Clone()
	delete(mgr.tridents, tridentEntityID)
	mgr.mu.Unlock()

	targetSlot := -1
	if snap.GameMode != GameModeCreative {
		if trCopy.OriginalSlot >= 0 && trCopy.OriginalSlot < world.PlayerInventorySlots && owner.InventorySlot(trCopy.OriginalSlot).IsEmpty() {
			targetSlot = trCopy.OriginalSlot
			owner.SetInventorySlot(targetSlot, trCopy.Stack)
			SetSlotItemMeta(owner, targetSlot, trCopy.Meta)
		} else {
			added, slots := owner.AddItemToInventory(trCopy.Stack)
			if added > 0 && len(slots) > 0 {
				targetSlot = slots[0]
				SetSlotItemMeta(owner, targetSlot, trCopy.Meta)
			}
		}
	}

	dim := world.NormalizeDimension(snap.Dimension)
	cx := int32(int64(math.Floor(snap.X)) >> 4)
	cz := int32(int64(math.Floor(snap.Z)) >> 4)

	s.BroadcastDimensionChunk(dim, cx, cz, protocol.PlayTakeItemEntity,
		EncodeTakeItemEntity(tridentEntityID, snap.EntityID, 1))
	if s.entities != nil {
		s.entities.Remove(tridentEntityID)
	}
	s.BroadcastDimensionChunk(dim, cx, cz, protocol.PlayRemoveEntities,
		encodeRemoveEntitiesPacket(nil, []int32{tridentEntityID}))
	s.BroadcastDimensionChunk(dim, cx, cz, PlaySoundPacketID,
		EncodeSoundPacket("minecraft:item.trident.return", SoundSourcePlayers, snap.X, snap.Y, snap.Z, 1.0, 1.0, 0))

	if targetSlot >= 0 && owner.send != nil {
		stID := owner.NextContainerStateID()
		_ = owner.send(protocol.PlayContainerSetSlot, EncodeContainerSlot(0, stID, targetSlot, owner.InventorySlot(targetSlot)))
	}
	return true
}

// TickThrownTridents advances all Loyalty-returning tridents toward their
// owners and returns them upon arrival.
func (s *Server) TickThrownTridents() {
	if s == nil {
		return
	}
	mgr := s.rangedManager()
	mgr.mu.Lock()
	ids := make([]int32, 0, len(mgr.tridents))
	for id, tr := range mgr.tridents {
		if tr != nil && tr.Returning && tr.LoyaltyLevel > 0 {
			ids = append(ids, id)
		}
	}
	mgr.mu.Unlock()

	for _, id := range ids {
		mgr.mu.Lock()
		tr := mgr.tridents[id]
		if tr == nil || !tr.Returning || tr.Owner == nil || tr.Owner.IsDead() {
			mgr.mu.Unlock()
			continue
		}
		owner := tr.Owner
		oSnap := owner.Snapshot()
		if oSnap.GameMode == GameModeSpectator {
			mgr.mu.Unlock()
			continue
		}
		targetX, targetY, targetZ := oSnap.X, oSnap.Y+1.0, oSnap.Z
		dx := targetX - tr.X
		dy := targetY - tr.Y
		dz := targetZ - tr.Z
		dist := math.Sqrt(dx*dx + dy*dy + dz*dz)
		stepSpeed := TridentLoyaltyReturnStep * float64(tr.LoyaltyLevel)
		tr.ReturnTicks++

		if dist <= stepSpeed || dist <= 1.5 || tr.ReturnTicks >= 40 {
			mgr.mu.Unlock()
			s.ReturnThrownTrident(id)
			continue
		}

		vx := (dx / dist) * stepSpeed
		vy := (dy / dist) * stepSpeed
		vz := (dz / dist) * stepSpeed
		tr.X += vx
		tr.Y += vy
		tr.Z += vz
		tr.VX, tr.VY, tr.VZ = vx, vy, vz
		nx, ny, nz, dim := tr.X, tr.Y, tr.Z, tr.Dimension
		mgr.mu.Unlock()

		if s.entities != nil {
			s.entities.Update(id, func(e *world.Entity) {
				e.X = nx
				e.Y = ny
				e.Z = nz
				e.VelocityX = clampVelocityShort(vx)
				e.VelocityY = clampVelocityShort(vy)
				e.VelocityZ = clampVelocityShort(vz)
			})
			if ent := s.entities.Get(id); ent != nil {
				cp := ent.ChunkPos()
				s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayTeleportEntity, encodeTeleportEntityPacket(nil, ent))
				s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlaySetEntityMotion, EncodeSetEntityMotionPacket(id, vx, vy, vz))
			}
		}
	}
}

// ---------------------------------------------------------------------------
// 4. Shield Mechanics (180° Block Angle, Axe Disable, Arrow Deflection, Durability)
// ---------------------------------------------------------------------------

// CanShieldBlockFrom reports whether a player at (playerX, playerZ) looking at
// horizontal angle `playerYaw` can block an attack or projectile originating from
// (sourceX, sourceZ) using a raised shield (front 180° horizontal hemisphere:
// dot product of horizontal vector to source with horizontal look vector > 0).
func CanShieldBlockFrom(playerX, playerZ float64, playerYaw float32, sourceX, sourceZ float64) bool {
	dx := sourceX - playerX
	dz := sourceZ - playerZ
	dist := math.Hypot(dx, dz)
	if dist < 1e-5 {
		return true
	}
	yawRad := float64(playerYaw) * (math.Pi / 180.0)
	lookX := -math.Sin(yawRad)
	lookZ := math.Cos(yawRad)
	return (dx*lookX + dz*lookZ) > 0.0
}

// IsAxeWeapon reports whether `itemID` is a vanilla axe capable of disabling a
// player's raised shield on a melee hit.
func IsAxeWeapon(itemID string) bool {
	switch canonicalItemName(itemID) {
	case "minecraft:wooden_axe",
		"minecraft:stone_axe",
		"minecraft:copper_axe",
		"minecraft:iron_axe",
		"minecraft:golden_axe",
		"minecraft:diamond_axe",
		"minecraft:netherite_axe":
		return true
	default:
		return false
	}
}

// IsAxeWieldingMob reports whether `mobType` wields an axe (or disables shields
// on melee strikes like Vindicator, Piglin Brute, and Warden).
func IsAxeWieldingMob(mobType string) bool {
	switch canonicalResourceName(mobType) {
	case MobTypeVindicator, MobTypePiglinBrute, MobTypeWarden:
		return true
	default:
		return false
	}
}

// ActiveShieldSlot returns the inventory slot (`36..44` or `45`) and stack of
// session's held `minecraft:shield` (preferring main hand, then offhand).
func ActiveShieldSlot(session *PlayerSession) (slot int, stack world.ItemStack, ok bool) {
	if session == nil {
		return -1, world.ItemStack{}, false
	}
	mainSlot, mainStack := session.handSlotAndStack(HandMain)
	if !mainStack.IsEmpty() && canonicalItemName(mainStack.ResolvedID()) == ShieldItemID {
		return mainSlot, mainStack, true
	}
	offSlot, offStack := session.handSlotAndStack(HandOffHand)
	if !offStack.IsEmpty() && canonicalItemName(offStack.ResolvedID()) == ShieldItemID {
		return offSlot, offStack, true
	}
	return -1, world.ItemStack{}, false
}

// DamageActiveShield applies vanilla shield durability wear when `blockedDamage >= 3.0`:
// `wear = 1 + int32(math.Floor(float64(blockedDamage)))`.
func (s *Server) DamageActiveShield(session *PlayerSession, blockedDamage float32) (wearApplied int32, broken bool) {
	if s == nil || session == nil || blockedDamage < ShieldDurabilityDamageThreshold {
		return 0, false
	}
	snap := session.Snapshot()
	if snap.GameMode != GameModeSurvival && snap.GameMode != GameModeAdventure {
		return 0, false
	}
	shieldSlot, _, ok := ActiveShieldSlot(session)
	if !ok || shieldSlot < 0 {
		return 0, false
	}
	wear := int32(1 + math.Floor(float64(blockedDamage)))
	if wear <= 0 {
		return 0, false
	}
	broken = s.DamagePlayerItem(session, shieldSlot, wear)
	if broken {
		session.SetBlockingWithShield(false)
		cx := int32(int64(math.Floor(snap.X)) >> 4)
		cz := int32(int64(math.Floor(snap.Z)) >> 4)
		s.BroadcastDimensionChunk(snap.Dimension, cx, cz, PlaySoundPacketID,
			EncodeSoundPacket("minecraft:item.shield.break", SoundSourcePlayers, snap.X, snap.Y, snap.Z, 1.0, 0.9, 0))
	}
	return wear, broken
}

// DisableShieldWithAxe disables `session`'s raised shield for `ticks` (`100`
// ticks = 5.0s), sets the item cooldown on `"minecraft:shield"`, sends
// `PlayCooldown` (`0x18`) and `PlayEntityEvent` (`EntityEventShieldDisabled = 30`),
// and broadcasts `minecraft:item.shield.break`.
func (s *Server) DisableShieldWithAxe(session *PlayerSession, ticks int) {
	if s == nil || session == nil {
		return
	}
	if ticks <= 0 {
		ticks = ShieldAxeDisableTicks
	}
	s.DisablePlayerShield(session, ticks)
	session.SetItemCooldown(ShieldItemID, ticks)
	snap := session.Snapshot()
	cx := int32(int64(math.Floor(snap.X)) >> 4)
	cz := int32(int64(math.Floor(snap.Z)) >> 4)
	s.BroadcastDimensionChunk(snap.Dimension, cx, cz, protocol.PlayEntityEvent,
		EncodeEntityEventPacket(snap.EntityID, EntityEventShieldDisabled))
	if session.send != nil {
		_ = session.send(protocol.PlayCooldown, EncodeCooldownPacket(ShieldItemID, int32(ticks)))
	}
}

// TryBlockDamageWithShield checks whether `session`'s active shield blocks an
// incoming hit of `rawDamage` from `(sourceX, sourceZ)`:
//   - Requires `session.IsBlockingWithShield()`, shield not on cooldown, and `piercingLevel <= 0`
//   - When `hasSourcePos` is true, requires `CanShieldBlockFrom(playerX, playerZ, playerYaw, sourceX, sourceZ)`
//   - Applies shield durability wear (`1 + floor(rawDamage)` when `rawDamage >= 3.0`)
//   - If `IsAxeItem(attackerHeldItem)`, disables the shield for 100 ticks via `DisableShieldWithAxe`
//   - Otherwise broadcasts `EntityEventShieldBlock` (29) and `minecraft:item.shield.block`
func (s *Server) TryBlockDamageWithShield(
	session *PlayerSession,
	rawDamage float32,
	sourceX, sourceZ float64,
	hasSourcePos bool,
	attackerHeldItem string,
	isProjectile bool,
	piercingLevel int,
) (blocked bool, shieldDisabled bool) {
	if s == nil || session == nil || !session.IsBlockingWithShield() {
		return false, false
	}
	if s.IsPlayerShieldDisabled(session) || session.IsItemOnCooldown(ShieldItemID) {
		return false, false
	}
	if piercingLevel > 0 {
		return false, false
	}
	snap := session.Snapshot()
	if hasSourcePos && !CanShieldBlockFrom(snap.X, snap.Z, snap.Yaw, sourceX, sourceZ) {
		return false, false
	}

	s.DamageActiveShield(session, rawDamage)

	if IsAxeWeapon(attackerHeldItem) {
		s.DisableShieldWithAxe(session, ShieldAxeDisableTicks)
		return true, true
	}

	cx := int32(int64(math.Floor(snap.X)) >> 4)
	cz := int32(int64(math.Floor(snap.Z)) >> 4)
	s.BroadcastDimensionChunk(snap.Dimension, cx, cz, protocol.PlayEntityEvent,
		EncodeEntityEventPacket(snap.EntityID, EntityEventShieldBlock))
	s.BroadcastDimensionChunk(snap.Dimension, cx, cz, PlaySoundPacketID,
		EncodeSoundPacket("minecraft:item.shield.block", SoundSourcePlayers, snap.X, snap.Y, snap.Z, 1.0, 1.0, 0))
	if isProjectile {
		s.TriggerDeflectedProjectile(session)
	}
	return true, false
}

// DeflectArrowFromSource attempts to deflect an incoming arrow/projectile with
// `session`'s raised shield, checking block angle and `piercingLevel == 0`,
// reversing the projectile entity's velocity on deflection, applying shield
// durability wear, and firing `TriggerDeflectedProjectile`.
func (s *Server) DeflectArrowFromSource(
	session *PlayerSession,
	arrowEntityID int32,
	rawDamage float32,
	sourceX, sourceZ float64,
	hasSourcePos bool,
	piercingLevel int,
) bool {
	blocked, _ := s.TryBlockDamageWithShield(session, rawDamage, sourceX, sourceZ, hasSourcePos, "", true, piercingLevel)
	if !blocked {
		return false
	}
	if arrowEntityID > 0 && s.entities != nil {
		s.entities.Update(arrowEntityID, func(e *world.Entity) {
			e.VelocityX = -e.VelocityX / 5
			e.VelocityY = -e.VelocityY / 5
			e.VelocityZ = -e.VelocityZ / 5
			e.Yaw += 180.0
		})
	}
	return true
}

// DamagePlayerWithArmorBypassShield applies `DamagePlayerWithArmor` while
// bypassing shield projectile deflection (used when a `minecraft:piercing`
// arrow pierces through a raised shield or after a directional shield check
// determined the hit came from behind).
func (s *Server) DamagePlayerWithArmorBypassShield(session *PlayerSession, rawDamage float32, cause string) float32 {
	if s == nil || session == nil || rawDamage <= 0 || session.IsDead() {
		return 0
	}
	wasBlocking := session.IsBlockingWithShield()
	if wasBlocking {
		session.SetBlockingWithShield(false)
		defer session.SetBlockingWithShield(true)
	}
	return s.DamagePlayerWithArmor(session, rawDamage, cause)
}

// ---------------------------------------------------------------------------
// 5. Unified Server Tick for Ranged Weapons, Tridents, and Arrow Pickups
// ---------------------------------------------------------------------------

// TickRangedWeaponsAndProjectiles steps active ranged weapon draw timers,
// Riptide spin-attack dashes, Loyalty returning tridents, and grounded
// arrow/trident pickups for all connected players.
func (s *Server) TickRangedWeaponsAndProjectiles() {
	if s == nil {
		return
	}
	for _, p := range s.onlinePlayersSnapshot() {
		if p == nil || p.IsDead() {
			continue
		}
		p.TickItemCooldowns()
		s.TickRangedWeaponDraw(p)
		if ds := s.playerDrawState(p); ds != nil {
			ds.mu.Lock()
			ripTicks := ds.riptideTicks
			ripImp := ds.riptideImpaling
			if ripTicks > 0 {
				ds.riptideTicks--
			}
			ds.mu.Unlock()
			if ripTicks > 0 {
				snap := p.Snapshot()
				lx, ly, lz := ElytraLookVector(snap.Yaw, snap.Pitch)
				s.executeRiptideSpinSweep(p, snap.X, snap.Y, snap.Z, lx, ly, lz, ripImp)
			}
		}
		s.TryPickupGroundedProjectiles(p)
	}
	s.TickThrownTridents()
}
