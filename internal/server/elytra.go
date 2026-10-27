package server

import (
	"math"
	"math/rand"
	"sync"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

// Elytra item, durability, physics, and protocol 775 (Minecraft 26.1.2) constants.
const (
	// ElytraItemID is the canonical item identifier for Elytra wings.
	ElytraItemID = "minecraft:elytra"
	// ElytraRepairMaterialID is the canonical repair item for Elytra in an Anvil.
	ElytraRepairMaterialID = "minecraft:phantom_membrane"

	// MaxElytraDurability is the vanilla maximum durability of minecraft:elytra (432).
	MaxElytraDurability int32 = 432
	// ElytraUsableMaxDamage is the maximum damage value at which an Elytra remains
	// usable (431 = MaxElytraDurability - 1). At Damage == 431 (1 durability left),
	// the Elytra enters its broken texture state and cannot be used to glide until
	// repaired via Mending or Phantom Membranes on an Anvil, but does not vanish.
	ElytraUsableMaxDamage int32 = MaxElytraDurability - 1

	// ElytraDurabilityTickInterval is the number of flight ticks between 1-point
	// durability wear checks in survival/adventure mode (20 ticks = 1.0 second).
	ElytraDurabilityTickInterval int = 20

	//DataComponentGlider is BuiltInRegistries.DATA_COMPONENT_TYPE index 34 ("minecraft:glider").
	DataComponentGlider int32 = 34

	// PlayerCommandStartFallFlying is ServerboundPlayerCommandPacket$Action.START_FALL_FLYING
	// ordinal in Minecraft 26.1.2 (protocol 775):
	//   STOP_SLEEPING(0), START_SPRINTING(1), STOP_SPRINTING(2),
	//   START_RIDING_JUMP(3), STOP_RIDING_JUMP(4), OPEN_INVENTORY(5), START_FALL_FLYING(6).
	PlayerCommandStartFallFlying int32 = 6
	// PlayerCommandStartFallFlyingLegacy is the pre-26.1 ordinal (8) accepted as a fallback.
	PlayerCommandStartFallFlyingLegacy int32 = 8

	// Entity.DATA_SHARED_FLAGS_ID (index 0, BYTE serializer 0) bit masks.
	EntityDataSharedFlagsID    byte = 0
	EntitySharedFlagOnFire     byte = 0x01
	EntitySharedFlagShiftKey   byte = 0x02
	EntitySharedFlagSprinting  byte = 0x08
	EntitySharedFlagSwimming   byte = 0x10
	EntitySharedFlagInvisible  byte = 0x20
	EntitySharedFlagGlowing    byte = 0x40
	EntitySharedFlagFallFlying byte = 0x80 // FLAG_FALL_FLYING = 7 (1 << 7)

	// Vanilla 26.1.2 LivingEntity.updateFallFlyingMovement aerodynamic constants.
	ElytraGravityPerTick       float64 = 0.08
	ElytraHorizontalDrag       float64 = 0.99
	ElytraVerticalDrag         float64 = 0.98
	ElytraSafeGlideFallSpeed   float64 = -0.5
	ElytraSafeFallDistanceCap  float64 = 1.0
	ElytraWallCollisionMinLoss float64 = 0.3
	ElytraWallDamageMultiplier float64 = 10.0
	ElytraWallMinAppliedDamage float32 = 1.0
)

type playerElytraState struct {
	mu          sync.Mutex
	fallFlying  bool
	flightTicks int
	vx          float64
	vy          float64
	vz          float64
}

var playerElytraStates sync.Map // map[*PlayerSession]*playerElytraState

func getElytraState(session *PlayerSession) *playerElytraState {
	if session == nil {
		return nil
	}
	if v, ok := playerElytraStates.Load(session); ok {
		return v.(*playerElytraState)
	}
	actual, _ := playerElytraStates.LoadOrStore(session, &playerElytraState{})
	return actual.(*playerElytraState)
}

// IsElytraItem reports whether itemName is minecraft:elytra.
func IsElytraItem(itemName string) bool {
	return canonicalItemName(itemName) == ElytraItemID
}

// IsElytraStackUsable reports whether stack and meta represent an unbroken
// minecraft:elytra that can start or sustain fall-flying (Damage < 431,
// matching vanilla ItemStack.nextDamageWillBreak() == false).
func IsElytraStackUsable(stack world.ItemStack, meta ItemMeta) bool {
	if stack.IsEmpty() || !IsElytraItem(stack.ResolvedID()) {
		return false
	}
	return meta.Damage < ElytraUsableMaxDamage
}

// HasUsableChestElytra reports whether session is wearing a usable (unbroken)
// minecraft:elytra in the chest armor slot (ArmorSlotChest = 6).
func HasUsableChestElytra(session *PlayerSession) bool {
	if session == nil {
		return false
	}
	chestStack := session.InventorySlot(ArmorSlotChest)
	if chestStack.IsEmpty() {
		return false
	}
	meta := GetSlotItemMeta(session, ArmorSlotChest)
	return IsElytraStackUsable(chestStack, meta)
}

// IsFallFlying reports whether session is currently gliding with an Elytra.
func (session *PlayerSession) IsFallFlying() bool {
	if session == nil {
		return false
	}
	st := getElytraState(session)
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.fallFlying
}

// IsPlayerFallFlying reports whether session is currently gliding with an Elytra.
func (s *Server) IsPlayerFallFlying(session *PlayerSession) bool {
	return session != nil && session.IsFallFlying()
}

// ElytraFlightTicks returns the number of ticks session has been continuously
// fall-flying during the current glide, or 0 if not fall-flying.
func (session *PlayerSession) ElytraFlightTicks() int {
	if session == nil {
		return 0
	}
	st := getElytraState(session)
	st.mu.Lock()
	defer st.mu.Unlock()
	if !st.fallFlying {
		return 0
	}
	return st.flightTicks
}

// ElytraVelocity returns session's current server-tracked Elytra glide velocity
// vector (vx, vy, vz) in blocks/tick.
func (session *PlayerSession) ElytraVelocity() (vx, vy, vz float64) {
	if session == nil {
		return 0, 0, 0
	}
	st := getElytraState(session)
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.vx, st.vy, st.vz
}

// SetElytraVelocity sets session's server-tracked Elytra glide velocity vector
// (vx, vy, vz) in blocks/tick.
func (session *PlayerSession) SetElytraVelocity(vx, vy, vz float64) {
	if session == nil {
		return
	}
	st := getElytraState(session)
	st.mu.Lock()
	st.vx = vx
	st.vy = vy
	st.vz = vz
	st.mu.Unlock()
}

func (s *Server) isPlayerInFluidForElytra(session *PlayerSession) bool {
	if s == nil || session == nil {
		return false
	}
	snap := session.Snapshot()
	cache := s.DimensionChunks(snap.Dimension)
	if cache == nil {
		return false
	}
	bx := int(math.Floor(snap.X))
	by := int(math.Floor(snap.Y))
	bz := int(math.Floor(snap.Z))
	state := cache.GetBlock(bx, by, bz)
	if state == world.StateWater || state == world.StateLava {
		return true
	}
	switch world.BlockName(state) {
	case "minecraft:water", "minecraft:lava", "minecraft:bubble_column":
		return true
	default:
		return false
	}
}

// CanPlayerStartFallFlying reports whether session meets all vanilla 26.1.2
// conditions in Player.tryToStartFallFlying():
//   - Alive and not in Spectator mode
//   - Airborne (!OnGround) and not already fall-flying
//   - Not affected by Levitation (minecraft:levitation)
//   - Not in water, lava, or bubble column
//   - Not riding a vehicle/mount and not sleeping
//   - Wearing an unbroken minecraft:elytra in ArmorSlotChest (slot 6)
func (s *Server) CanPlayerStartFallFlying(session *PlayerSession) bool {
	if session == nil || session.IsDead() {
		return false
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator || snap.OnGround {
		return false
	}
	if session.IsFallFlying() {
		return false
	}
	if session.HasPlayerEffect(EffectLevitation) {
		return false
	}
	if s != nil {
		if s.isPlayerInFluidForElytra(session) {
			return false
		}
		if s.PlayerMountedVehicle(session) > 0 || s.PlayerMountedEquine(session) > 0 || s.PlayerMountedCamel(session) > 0 {
			return false
		}
		if s.IsPlayerSleeping(session) {
			return false
		}
	}
	return HasUsableChestElytra(session)
}

// ComputePlayerSharedFlags builds the Entity.DATA_SHARED_FLAGS_ID (index 0) byte
// for session, combining sneaking (0x02), sprinting (0x08), invisible (0x20),
// glowing (0x40), and fall-flying (0x80).
func ComputePlayerSharedFlags(session *PlayerSession) byte {
	if session == nil {
		return 0
	}
	var flags byte
	if session.IsSneaking() {
		flags |= EntitySharedFlagShiftKey
	}
	if session.IsSprinting() {
		flags |= EntitySharedFlagSprinting
	}
	if session.HasPlayerEffect(EffectInvisibility) {
		flags |= EntitySharedFlagInvisible
	}
	if session.HasPlayerEffect(EffectGlowing) {
		flags |= EntitySharedFlagGlowing
	}
	if session.IsFallFlying() {
		flags |= EntitySharedFlagFallFlying
	}
	return flags
}

// ComputePlayerPose returns the net.minecraft.world.entity.Pose ordinal for session:
//   - PoseFallFlying (1) when fall-flying with an Elytra
//   - PoseCrouching (5) when sneaking on ground/air without fall-flying
//   - PoseStanding (0) otherwise
func ComputePlayerPose(session *PlayerSession) byte {
	if session == nil {
		return PoseStanding
	}
	if session.IsFallFlying() {
		return PoseFallFlying
	}
	if session.IsSneaking() {
		return PoseCrouching
	}
	return PoseStanding
}

// EncodePlayerSharedFlagsDataPacket encodes a clientbound PlaySetEntityData
// (0x61) packet setting Entity.DATA_SHARED_FLAGS_ID (index 0, BYTE serializer 0)
// to flags for entityID.
func EncodePlayerSharedFlagsDataPacket(entityID int32, flags byte) []byte {
	w := protocol.NewWriter(10)
	w.VarInt(entityID)
	w.Byte(EntityDataSharedFlagsID)
	w.VarInt(EntityDataSerializerByte)
	w.Byte(flags)
	w.Byte(0xFF)
	return w.Bytes()
}

// EncodePlayerSharedFlagsAndPoseDataPacket encodes a clientbound PlaySetEntityData
// (0x61) packet setting both Entity.DATA_SHARED_FLAGS_ID (index 0, BYTE serializer 0)
// and Entity.DATA_POSE (index 6, POSE serializer 21) for entityID.
func EncodePlayerSharedFlagsAndPoseDataPacket(entityID int32, flags, pose byte) []byte {
	w := protocol.NewWriter(16)
	w.VarInt(entityID)
	w.Byte(EntityDataSharedFlagsID)
	w.VarInt(EntityDataSerializerByte)
	w.Byte(flags)
	w.Byte(EntityDataPoseID)
	w.VarInt(EntityDataSerializerPose)
	w.VarInt(int32(pose))
	w.Byte(0xFF)
	return w.Bytes()
}

// DecodePlayerSharedFlagsAndPoseDataPacket decodes a PlaySetEntityData packet
// produced by EncodePlayerSharedFlagsAndPoseDataPacket or EncodePlayerSharedFlagsDataPacket.
func DecodePlayerSharedFlagsAndPoseDataPacket(r *protocol.Reader) (entityID int32, flags, pose byte, err error) {
	entityID, err = r.VarInt()
	if err != nil {
		return 0, 0, 0, err
	}
	for r.Remaining() > 0 {
		idx, err := r.ReadByte()
		if err != nil {
			return entityID, flags, pose, err
		}
		if idx == 0xFF {
			break
		}
		ser, err := r.VarInt()
		if err != nil {
			return entityID, flags, pose, err
		}
		switch {
		case idx == EntityDataSharedFlagsID && ser == EntityDataSerializerByte:
			b, err := r.ReadByte()
			if err != nil {
				return entityID, flags, pose, err
			}
			flags = b
		case idx == EntityDataPoseID && ser == EntityDataSerializerPose:
			p, err := r.VarInt()
			if err != nil {
				return entityID, flags, pose, err
			}
			pose = byte(p)
		default:
			return entityID, flags, pose, nil
		}
	}
	return entityID, flags, pose, nil
}

// PlayerSharedFlagsAndPosePacketByEntityID returns a PlaySetEntityData payload
// with the player's shared flags and pose if entityID belongs to a connected
// player with non-default flags or pose, or nil otherwise.
func (s *Server) PlayerSharedFlagsAndPosePacketByEntityID(entityID int32) []byte {
	if s == nil || entityID == 0 {
		return nil
	}
	for _, p := range s.onlinePlayersSnapshot() {
		if p != nil && p.EntityID == entityID {
			flags := ComputePlayerSharedFlags(p)
			pose := ComputePlayerPose(p)
			if s.IsPlayerSleeping(p) {
				pose = PoseSleeping
			}
			if flags == 0 && pose == PoseStanding {
				return nil
			}
			return EncodePlayerSharedFlagsAndPoseDataPacket(entityID, flags, pose)
		}
	}
	return nil
}

func (s *Server) broadcastPlayerSharedFlags(session *PlayerSession) {
	if session == nil {
		return
	}
	snap := session.Snapshot()
	flags := ComputePlayerSharedFlags(session)
	pose := ComputePlayerPose(session)
	if s != nil && s.IsPlayerSleeping(session) {
		pose = PoseSleeping
	}
	pkt := EncodePlayerSharedFlagsAndPoseDataPacket(snap.EntityID, flags, pose)
	if session.send != nil {
		_ = session.send(PlaySetEntityDataCB, pkt)
	}
	if s != nil {
		cx := int32(int64(math.Floor(snap.X)) >> 4)
		cz := int32(int64(math.Floor(snap.Z)) >> 4)
		s.BroadcastDimensionChunk(snap.Dimension, cx, cz, PlaySetEntityDataCB, pkt)
	}
}

// StartPlayerFallFlying attempts to activate Elytra gliding for session
// (triggered by ServerboundPlayerCommandPacket START_FALL_FLYING).
// Returns true if fall-flying started.
func (s *Server) StartPlayerFallFlying(session *PlayerSession) bool {
	if !s.CanPlayerStartFallFlying(session) {
		return false
	}
	snap := session.Snapshot()
	lx, _, lz := ElytraLookVector(snap.Yaw, snap.Pitch)

	st := getElytraState(session)
	st.mu.Lock()
	st.fallFlying = true
	st.flightTicks = 0
	if st.vx == 0 && st.vy == 0 && st.vz == 0 {
		// Seed initial forward glide momentum if none was set prior to activation.
		horiz := 0.25
		if session.IsSprinting() {
			horiz = 0.35
		}
		st.vx = lx * horiz
		st.vy = -0.05
		st.vz = lz * horiz
	}
	st.mu.Unlock()

	s.broadcastPlayerSharedFlags(session)
	return true
}

// StopPlayerFallFlying ends Elytra gliding for session and synchronizes
// Entity.DATA_SHARED_FLAGS_ID (clearing bit 0x80) to the player and chunk viewers.
// Returns true if the player was previously fall-flying.
func (s *Server) StopPlayerFallFlying(session *PlayerSession) bool {
	if session == nil {
		return false
	}
	st := getElytraState(session)
	st.mu.Lock()
	wasFlying := st.fallFlying
	st.fallFlying = false
	st.flightTicks = 0
	st.vx = 0
	st.vy = 0
	st.vz = 0
	st.mu.Unlock()

	if wasFlying {
		s.broadcastPlayerSharedFlags(session)
	}
	return wasFlying
}

// ElytraLookVector computes the unit 3D look direction vector (lx, ly, lz) from
// Minecraft yaw and pitch angles in degrees.
func ElytraLookVector(yawDeg, pitchDeg float32) (lx, ly, lz float64) {
	yawRad := float64(yawDeg) * (math.Pi / 180.0)
	pitchRad := float64(pitchDeg) * (math.Pi / 180.0)
	cosPitch := math.Cos(pitchRad)
	lx = -math.Sin(yawRad) * cosPitch
	ly = -math.Sin(pitchRad)
	lz = math.Cos(yawRad) * cosPitch
	return lx, ly, lz
}

// StepElytraGlideVelocity computes one tick of vanilla 26.1.2 aerodynamic
// Elytra glide physics (LivingEntity.updateFallFlyingMovement):
//   - Applies pitch-scaled gravity: vy += 0.08 * (-1.0 + cos(pitch)^2 * 0.75)
//   - Converts downward vertical velocity into forward thrust when diving
//   - Converts horizontal velocity into upward lift when pitching up (pitch < 0)
//   - Steers horizontal velocity toward the look direction
//   - Applies aerodynamic drag: vx *= 0.99, vy *= 0.98, vz *= 0.99
func StepElytraGlideVelocity(vx, vy, vz float64, yawDeg, pitchDeg float32) (nx, ny, nz float64) {
	lx, _, lz := ElytraLookVector(yawDeg, pitchDeg)
	pitchRad := float64(pitchDeg) * (math.Pi / 180.0)
	cosPitch := math.Cos(pitchRad)
	cosPitchSq := cosPitch * cosPitch

	lookHoriz := math.Hypot(lx, lz)
	velHoriz := math.Hypot(vx, vz)

	// 1. Gravity reduced by horizontal wing aspect (cos(pitch)^2 * 0.75).
	vy += ElytraGravityPerTick * (-1.0 + cosPitchSq*0.75)

	// 2. Downward velocity conversion into forward speed.
	if vy < 0.0 && lookHoriz > 0.0 {
		lift := vy * -0.1 * cosPitchSq
		vy += lift
		vx += lx * lift / lookHoriz
		vz += lz * lift / lookHoriz
	}

	// 3. Upward pitch (pitchRad < 0): trade horizontal speed for climb lift.
	if pitchRad < 0.0 && lookHoriz > 0.0 {
		climb := velHoriz * (-math.Sin(pitchRad)) * 0.04
		vy += climb * 3.2
		vx -= lx * climb / lookHoriz
		vz -= lz * climb / lookHoriz
	}

	// 4. Steer horizontal velocity vector toward look direction.
	if lookHoriz > 0.0 {
		vx += (lx/lookHoriz*velHoriz - vx) * 0.1
		vz += (lz/lookHoriz*velHoriz - vz) * 0.1
	}

	// 5. Aerodynamic air resistance.
	return vx * ElytraHorizontalDrag, vy * ElytraVerticalDrag, vz * ElytraHorizontalDrag
}

// ComputeElytraWallCollisionDamage computes the vanilla kinetic energy wall
// collision damage when a fall-flying player collides horizontally with blocks:
//
//	speedLoss = prevHorizSpeed - newHorizSpeed
//	if speedLoss >= 0.3 -> damage = speedLoss * 10.0 (applied when > 1.0)
func ComputeElytraWallCollisionDamage(prevHorizSpeed, newHorizSpeed float64) float32 {
	speedLoss := prevHorizSpeed - newHorizSpeed
	if speedLoss < ElytraWallCollisionMinLoss {
		return 0
	}
	dmg := float32(speedLoss * ElytraWallDamageMultiplier)
	if dmg <= ElytraWallMinAppliedDamage {
		return 0
	}
	return dmg
}

// DamagePlayerElytra applies durability wear to the minecraft:elytra equipped in
// session's chest slot (ArmorSlotChest = 6), respecting Unbreakable and
// minecraft:unbreaking (using the 1/(level+1) tool/elytra roll).
// Unlike regular armor, a vanilla Elytra never breaks into nothing: its damage
// caps at ElytraUsableMaxDamage (431 = MaxElytraDurability - 1), leaving 1
// durability point in the unusable "broken_elytra" state.
func (s *Server) DamagePlayerElytra(session *PlayerSession, amount int32, rng *rand.Rand) (newDamage int32, unusable bool) {
	if session == nil || amount <= 0 {
		return 0, false
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeCreative || snap.GameMode == GameModeSpectator {
		meta := GetSlotItemMeta(session, ArmorSlotChest)
		return meta.Damage, false
	}
	stack := session.InventorySlot(ArmorSlotChest)
	if stack.IsEmpty() || !IsElytraItem(stack.ResolvedID()) {
		return 0, true
	}
	meta := GetSlotItemMeta(session, ArmorSlotChest)
	if meta.Damage >= ElytraUsableMaxDamage {
		return meta.Damage, true
	}

	unbreaking := meta.EnchantmentLevel("minecraft:unbreaking")
	var applied int32
	for i := int32(0); i < amount; i++ {
		// In vanilla, Elytra uses the standard item Unbreaking formula 1/(level+1)
		// rather than the 60% + 40%/(level+1) armor formula.
		if unbreaking > 0 && shouldIgnoreDurabilityDamage(unbreaking, false, rng) {
			continue
		}
		applied++
	}
	if applied <= 0 {
		return meta.Damage, meta.Damage >= ElytraUsableMaxDamage
	}

	outMeta := meta.Clone()
	outMeta.Damage += applied
	if outMeta.Damage >= ElytraUsableMaxDamage {
		outMeta.Damage = ElytraUsableMaxDamage
		unusable = true
	}
	SetSlotItemMeta(session, ArmorSlotChest, outMeta)

	if session.send != nil {
		stateID := session.NextContainerStateID()
		trim, _ := GetSlotArmorTrim(session, ArmorSlotChest)
		_ = session.send(protocol.PlayContainerSetSlot, EncodeContainerSlotWithMeta(0, stateID, ArmorSlotChest, stack, outMeta, trim))
	}
	if unusable && s != nil {
		cx := int32(int64(math.Floor(snap.X)) >> 4)
		cz := int32(int64(math.Floor(snap.Z)) >> 4)
		s.BroadcastDimensionChunk(snap.Dimension, cx, cz, PlaySoundPacketID,
			EncodeSoundPacket("minecraft:entity.item.break", SoundSourcePlayers, snap.X, snap.Y, snap.Z, 0.8, 1.0, 0))
	}
	return outMeta.Damage, unusable
}

// EncodeContainerSlotWithMeta encodes a clientbound container_set_slot (0x14)
// packet body including ItemMeta (damage, enchantments, repair_cost) and
// optional ArmorTrim data components so the client updates durability bars
// and broken_elytra texture state.
func EncodeContainerSlotWithMeta(containerID, stateID int32, slot int, stack world.ItemStack, meta ItemMeta, trim ArmorTrim) []byte {
	w := protocol.NewWriter(32)
	w.VarInt(containerID)
	w.VarInt(stateID)
	w.Uint16(uint16(int16(slot)))
	WriteSlotWithArmorTrim(w, stack, meta, trim)
	return w.Bytes()
}

// UseEquippableArmorOrElytra handles right-clicking in air while holding an
// equippable armor piece or `minecraft:elytra`, equipping or swapping it into
// the corresponding armor slot (5..8) while respecting `minecraft:binding_curse`
// in Survival/Adventure mode, synchronizing slots with full ItemMeta/ArmorTrim,
// broadcasting equipment to chunk viewers, and playing the equip sound.
func (s *Server) UseEquippableArmorOrElytra(session *PlayerSession, hand int32) bool {
	if s == nil || session == nil || session.IsDead() {
		return false
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator {
		return false
	}
	handSlot, handStack := session.handSlotAndStack(hand)
	if handSlot < 0 || handStack.IsEmpty() {
		return false
	}
	itemName := canonicalItemName(handStack.ResolvedID())
	if !IsElytraItem(itemName) && !IsArmorItem(itemName) {
		return false
	}
	armorSlot, ok := WearableArmorSlotForItem(itemName)
	if !ok || armorSlot < ArmorSlotHead || armorSlot > ArmorSlotFeet {
		return false
	}

	existing := session.InventorySlot(armorSlot)
	if !existing.IsEmpty() {
		if HasBindingCurseOnSlot(session, armorSlot) || handStack.Count != 1 {
			return false
		}
	}

	handMeta := GetSlotItemMeta(session, handSlot)
	handTrim, _ := GetSlotArmorTrim(session, handSlot)
	existingMeta := GetSlotItemMeta(session, armorSlot)
	existingTrim, _ := GetSlotArmorTrim(session, armorSlot)

	var newHandStack world.ItemStack
	var newHandMeta ItemMeta
	var newHandTrim ArmorTrim
	newArmorStack := world.NewItemStack(handStack.ResolvedItemID(), 1)
	newArmorMeta := handMeta.Clone()
	newArmorTrim := handTrim

	if existing.IsEmpty() {
		if handStack.Count > 1 {
			newHandStack = world.NewItemStack(handStack.ResolvedItemID(), handStack.Count-1)
			newHandMeta = handMeta.Clone()
			newHandTrim = handTrim
		}
	} else {
		newHandStack = existing
		newHandMeta = existingMeta.Clone()
		newHandTrim = existingTrim
	}

	session.SetInventorySlot(armorSlot, newArmorStack)
	SetSlotItemMeta(session, armorSlot, newArmorMeta)
	SetSlotArmorTrim(session, armorSlot, newArmorTrim)

	session.SetInventorySlot(handSlot, newHandStack)
	SetSlotItemMeta(session, handSlot, newHandMeta)
	SetSlotArmorTrim(session, handSlot, newHandTrim)

	if session.send != nil {
		stateID := session.NextContainerStateID()
		_ = session.send(protocol.PlayContainerSetSlot, EncodeContainerSlotWithMeta(0, stateID, armorSlot, newArmorStack, newArmorMeta, newArmorTrim))
		stateID2 := session.NextContainerStateID()
		_ = session.send(protocol.PlayContainerSetSlot, EncodeContainerSlotWithMeta(0, stateID2, handSlot, newHandStack, newHandMeta, newHandTrim))
	}

	snd := "minecraft:item.armor.equip_generic"
	if IsElytraItem(itemName) {
		snd = "minecraft:item.armor.equip_elytra"
	}
	cx := int32(int64(math.Floor(snap.X)) >> 4)
	cz := int32(int64(math.Floor(snap.Z)) >> 4)
	s.BroadcastDimensionChunk(snap.Dimension, cx, cz, PlaySoundPacketID,
		EncodeSoundPacket(snd, SoundSourcePlayers, snap.X, snap.Y, snap.Z, 1.0, 1.0, 0))
	s.SendPlayerEquipmentPacket(session)
	s.RecordItemUsed(session, itemName, 1)
	return true
}

// SendPlayerEquipmentPacket broadcasts session's 6 equipment slots
// (MainHand=0, OffHand=1, Feet=2, Legs=3, Chest=4, Head=5) to chunk viewers via
// ClientboundSetEquipmentPacket (0x66).
func (s *Server) SendPlayerEquipmentPacket(session *PlayerSession) {
	if s == nil || session == nil {
		return
	}
	snap := session.Snapshot()
	mainSlot := int(snap.SelectedSlot) + world.HotbarContainerOffset
	invSlots := [6]int{mainSlot, OffHandContainerSlot, ArmorSlotFeet, ArmorSlotLegs, ArmorSlotChest, ArmorSlotHead}
	w := protocol.NewWriter(64)
	w.VarInt(snap.EntityID)
	for eqSlot := 0; eqSlot < 6; eqSlot++ {
		slotByte := byte(eqSlot)
		if eqSlot < 5 {
			slotByte |= 0x80
		}
		w.Byte(slotByte)
		invIdx := invSlots[eqSlot]
		st := session.InventorySlot(invIdx)
		meta := GetSlotItemMeta(session, invIdx)
		trim, _ := GetSlotArmorTrim(session, invIdx)
		WriteSlotWithArmorTrim(w, st, meta, trim)
	}
	cx := int32(int64(math.Floor(snap.X)) >> 4)
	cz := int32(int64(math.Floor(snap.Z)) >> 4)
	s.BroadcastDimensionChunk(snap.Dimension, cx, cz, protocol.PlaySetEquipment, w.Bytes())
}

// RepairPlayerElytraWithMending repairs a damaged Mending-enchanted Elytra in
// session's chest slot (ArmorSlotChest = 6) using up to xpPoints experience
// (2 durability repaired per 1 XP point), returning the durability repaired and
// leftover XP points.
func (s *Server) RepairPlayerElytraWithMending(session *PlayerSession, xpPoints int32) (repairedDurability, remainingXP int32) {
	if session == nil || xpPoints <= 0 {
		return 0, xpPoints
	}
	stack := session.InventorySlot(ArmorSlotChest)
	if stack.IsEmpty() || !IsElytraItem(stack.ResolvedID()) {
		return 0, xpPoints
	}
	meta := GetSlotItemMeta(session, ArmorSlotChest)
	if meta.Damage <= 0 || meta.EnchantmentLevel("minecraft:mending") <= 0 {
		return 0, xpPoints
	}

	maxRepair := xpPoints * 2
	repaired := meta.Damage
	if repaired > maxRepair {
		repaired = maxRepair
	}
	outMeta := meta.Clone()
	outMeta.Damage -= repaired
	SetSlotItemMeta(session, ArmorSlotChest, outMeta)

	if session.send != nil {
		stateID := session.NextContainerStateID()
		trim, _ := GetSlotArmorTrim(session, ArmorSlotChest)
		_ = session.send(protocol.PlayContainerSetSlot, EncodeContainerSlotWithMeta(0, stateID, ArmorSlotChest, stack, outMeta, trim))
	}
	xpUsed := (repaired + 1) / 2
	rem := xpPoints - xpUsed
	if rem < 0 {
		rem = 0
	}
	return repaired, rem
}

func (s *Server) isHorizontallyBlockedAt(dim string, x, y, z float64) bool {
	if s == nil {
		return false
	}
	cache := s.DimensionChunks(dim)
	if cache == nil {
		return false
	}
	bx := int(math.Floor(x))
	bz := int(math.Floor(z))
	feetY := int(math.Floor(y))
	headY := int(math.Floor(y + 0.6))
	return !isPassableBlock(cache.GetBlock(bx, feetY, bz)) || !isPassableBlock(cache.GetBlock(bx, headY, bz))
}

// HandlePlayerElytraMove processes player movement while fall-flying, applying:
//   - Kinetic wall collision damage ("fly_into_wall") when horizontal velocity
//     drops by >= 0.3 due to block collision
//   - Fall distance capping (<= 1.0) during gentle glides (vy > -0.5)
//   - Stopping fall-flying and applying landing fall damage when touching ground
func (s *Server) HandlePlayerElytraMove(
	session *PlayerSession,
	oldX, oldY, oldZ, newX, newY, newZ float64,
	wasOnGround, onGround bool,
) (wallDamage, fallDamage float32) {
	if s == nil || session == nil || session.IsDead() {
		return 0, 0
	}
	if !session.IsFallFlying() {
		return 0, s.HandlePlayerFall(session, oldY, newY, wasOnGround, onGround)
	}

	snap := session.Snapshot()
	st := getElytraState(session)
	st.mu.Lock()
	prevVX, prevVY, prevVZ := st.vx, st.vy, st.vz
	st.mu.Unlock()

	dx := newX - oldX
	dy := newY - oldY
	dz := newZ - oldZ
	prevHoriz := math.Hypot(prevVX, prevVZ)
	newHoriz := math.Hypot(dx, dz)

	// Check horizontal wall collision along the previous flight vector.
	collidedHoriz := false
	if prevHoriz >= ElytraWallCollisionMinLoss && newHoriz < prevHoriz {
		probeX := newX + (prevVX/prevHoriz)*0.4
		probeZ := newZ + (prevVZ/prevHoriz)*0.4
		if s.isHorizontallyBlockedAt(snap.Dimension, probeX, newY, probeZ) ||
			s.isHorizontallyBlockedAt(snap.Dimension, oldX+prevVX, newY, oldZ+prevVZ) ||
			newHoriz <= 0.001 {
			collidedHoriz = true
		}
	}

	if collidedHoriz {
		wallDamage = ComputeElytraWallCollisionDamage(prevHoriz, newHoriz)
		if wallDamage > 0 {
			snd := "minecraft:entity.player.small_fall"
			if wallDamage >= 4.0 {
				snd = "minecraft:entity.player.big_fall"
			}
			cx := int32(int64(math.Floor(newX)) >> 4)
			cz := int32(int64(math.Floor(newZ)) >> 4)
			s.BroadcastDimensionChunk(snap.Dimension, cx, cz, PlaySoundPacketID,
				EncodeSoundPacket(snd, SoundSourcePlayers, newX, newY, newZ, 1.0, 1.0, 0))
			s.DamagePlayerWithEffects(session, wallDamage, "fly_into_wall")
		}
	}

	// Update tracked glide velocity from actual movement.
	st.mu.Lock()
	st.vx = dx
	st.vy = dy
	st.vz = dz
	st.mu.Unlock()

	// While gliding at a shallow descent (vy > -0.5), vanilla resets fallDistance to 1.0
	// so landing gently does not deal accumulated fall damage.
	effVY := dy
	if effVY == 0 && prevVY != 0 {
		effVY = prevVY
	}
	surv := getSurvivalState(session)
	if effVY > ElytraSafeGlideFallSpeed {
		surv.mu.Lock()
		if surv.fallDistance > ElytraSafeFallDistanceCap {
			surv.fallDistance = ElytraSafeFallDistanceCap
		}
		surv.mu.Unlock()
	} else if dy < 0 && !onGround {
		surv.mu.Lock()
		surv.fallDistance += -dy
		surv.mu.Unlock()
	}

	if onGround || s.isPlayerInFluidForElytra(session) {
		// If the player nose-dived into the ground at steep vertical speed (effVY <= -0.5),
		// apply accumulated fall damage before clearing glide state.
		if onGround && effVY <= ElytraSafeGlideFallSpeed {
			fallDamage = s.HandlePlayerFall(session, oldY, newY, wasOnGround, true)
		} else {
			surv.mu.Lock()
			surv.fallDistance = 0
			surv.mu.Unlock()
		}
		s.StopPlayerFallFlying(session)
	}
	return wallDamage, fallDamage
}

// TickPlayerElytra advances one server tick of Elytra flight for session:
//   - Validates that session can continue gliding (airborne, not levitating,
//     not in fluid, not mounted/sleeping, wearing a usable unbroken Elytra in slot 6)
//   - Advances glide physics velocity (StepElytraGlideVelocity)
//   - Resets fallDistance to 1.0 when vertical speed vy > -0.5
//   - Applies 1 durability damage every 20 flight ticks in survival/adventure mode
func (s *Server) TickPlayerElytra(session *PlayerSession) {
	if s == nil || session == nil {
		return
	}
	if !session.IsFallFlying() {
		return
	}
	snap := session.Snapshot()
	if session.IsDead() || snap.OnGround || snap.GameMode == GameModeSpectator ||
		session.HasPlayerEffect(EffectLevitation) || s.isPlayerInFluidForElytra(session) ||
		s.PlayerMountedVehicle(session) > 0 || s.PlayerMountedEquine(session) > 0 || s.PlayerMountedCamel(session) > 0 ||
		s.IsPlayerSleeping(session) || !HasUsableChestElytra(session) {
		s.StopPlayerFallFlying(session)
		return
	}

	st := getElytraState(session)
	st.mu.Lock()
	if !st.fallFlying {
		st.mu.Unlock()
		return
	}
	st.flightTicks++
	ticks := st.flightTicks
	st.vx, st.vy, st.vz = StepElytraGlideVelocity(st.vx, st.vy, st.vz, snap.Yaw, snap.Pitch)
	vy := st.vy
	st.mu.Unlock()

	if vy > ElytraSafeGlideFallSpeed {
		surv := getSurvivalState(session)
		surv.mu.Lock()
		if surv.fallDistance > ElytraSafeFallDistanceCap {
			surv.fallDistance = ElytraSafeFallDistanceCap
		}
		surv.mu.Unlock()
	}

	if ticks > 0 && ticks%ElytraDurabilityTickInterval == 0 {
		if snap.GameMode == GameModeSurvival || snap.GameMode == GameModeAdventure {
			if _, unusable := s.DamagePlayerElytra(session, 1, nil); unusable {
				s.StopPlayerFallFlying(session)
			}
		}
	}
}

// TickElytraPlayers ticks Elytra flight physics and durability wear for all
// connected players.
func (s *Server) TickElytraPlayers() {
	if s == nil {
		return
	}
	for _, p := range s.onlinePlayersSnapshot() {
		if p != nil && p.IsFallFlying() {
			s.TickPlayerElytra(p)
		}
	}
}
