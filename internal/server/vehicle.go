package server

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"regionio/internal/nbt"
	"regionio/internal/protocol"
	"regionio/internal/registry"
	"regionio/internal/world"
)

// Vehicle packet IDs for Minecraft 26.1.2 (protocol 775).
const (
	PlayMoveVehicleCB  int32 = protocol.PlayMoveVehicle
	PlayMoveVehicleSB  int32 = protocol.PlayMoveVehicleSB
	PlayPaddleBoatSB   int32 = protocol.PlayPaddleBoatSB
	PlayPlayerInputSB  int32 = protocol.PlayPlayerInputSB
	PlayVehicleMountCB int32 = protocol.PlaySetPassengers
)

// Additional minecart and boat/raft entity_type identifiers for 26.1.2.
const (
	EntityTypeSpawnerMinecart      = "minecraft:spawner_minecart"
	EntityTypeCommandBlockMinecart = "minecraft:command_block_minecart"

	EntityTypeOakBoat          = "minecraft:oak_boat"
	EntityTypeSpruceBoat       = "minecraft:spruce_boat"
	EntityTypeBirchBoat        = "minecraft:birch_boat"
	EntityTypeJungleBoat       = "minecraft:jungle_boat"
	EntityTypeAcaciaBoat       = "minecraft:acacia_boat"
	EntityTypeCherryBoat       = "minecraft:cherry_boat"
	EntityTypeDarkOakBoat      = "minecraft:dark_oak_boat"
	EntityTypeMangroveBoat     = "minecraft:mangrove_boat"
	EntityTypePaleOakBoat      = "minecraft:pale_oak_boat"
	EntityTypeBambooRaft       = "minecraft:bamboo_raft"
	EntityTypeOakChestBoat     = "minecraft:oak_chest_boat"
	EntityTypeSpruceChestBoat  = "minecraft:spruce_chest_boat"
	EntityTypeBirchChestBoat   = "minecraft:birch_chest_boat"
	EntityTypeJungleChestBoat  = "minecraft:jungle_chest_boat"
	EntityTypeAcaciaChestBoat  = "minecraft:acacia_chest_boat"
	EntityTypeCherryChestBoat  = "minecraft:cherry_chest_boat"
	EntityTypeDarkOakChestBoat = "minecraft:dark_oak_chest_boat"
	EntityTypeMangroveChestBoat = "minecraft:mangrove_chest_boat"
	EntityTypePaleOakChestBoat = "minecraft:pale_oak_chest_boat"
	EntityTypeBambooChestRaft  = "minecraft:bamboo_chest_raft"
)

// ServerboundPlayerInputPacket (0x2b) bitflags in Minecraft 26.1.2 (protocol 775).
const (
	PlayerInputFlagForward  byte = 1 << 0
	PlayerInputFlagBackward byte = 1 << 1
	PlayerInputFlagLeft     byte = 1 << 2
	PlayerInputFlagRight    byte = 1 << 3
	PlayerInputFlagJump     byte = 1 << 4
	PlayerInputFlagShift    byte = 1 << 5
	PlayerInputFlagSprint   byte = 1 << 6
)

// SynchedEntityData indices for VehicleEntity, AbstractBoat, AbstractMinecart,
// and MinecartFurnace in Minecraft 26.1.2 (protocol 775).
const (
	EntityDataVehicleHurtID         byte = 8  // INT serializer (1)
	EntityDataVehicleHurtDirID      byte = 9  // INT serializer (1)
	EntityDataVehicleDamageID       byte = 10 // FLOAT serializer (3)
	EntityDataBoatLeftPaddleID      byte = 11 // BOOLEAN serializer (8)
	EntityDataBoatRightPaddleID     byte = 12 // BOOLEAN serializer (8)
	EntityDataBoatBubbleTimeID      byte = 13 // INT serializer (1)
	EntityDataMinecartDisplayBlock  byte = 11 // OPTIONAL_BLOCK_STATE serializer (15)
	EntityDataMinecartDisplayOffset byte = 12 // INT serializer (1)
	EntityDataFurnaceMinecartFuelID byte = 13 // BOOLEAN serializer (8)
)

// Vehicle physics, capacity, fuel, fuse, and damage constants (vanilla 26.1.2).
const (
	MaxBoatPassengers      = 2
	MaxChestBoatPassengers = 1
	MaxMinecartPassengers  = 1

	HopperMinecartSlotCount = 5
	VehicleChestSlotCount   = 27

	// VehicleEntity break threshold: when accumulated wobble damage exceeds 40.0
	// (equivalent to 4.0 HP * 10), the vehicle breaks and drops its item(s).
	VehicleBreakDamageThreshold float32 = 40.0
	VehicleDamageDecayPerTick   float32 = 1.0
	VehicleHurtDurationTicks    int32   = 10

	// Minecart rail physics constants.
	MinecartMaxRailSpeed          float64 = 0.4 // 8.0 m/s
	MinecartWaterMaxRailSpeed     float64 = 0.2 // 4.0 m/s when waterlogged
	MinecartSlopeGravity          float64 = 0.0078125
	MinecartPoweredRailBoost      float64 = 0.06
	MinecartPoweredRailLaunch     float64 = 0.02
	MinecartUnpoweredRailBrake    float64 = 0.5
	MinecartDragOccupied          float64 = 0.997
	MinecartDragEmpty             float64 = 0.96
	MinecartDragContainer         float64 = 0.95
	MinecartDragFurnace           float64 = 0.94
	MinecartOffRailGravity        float64 = 0.04
	MinecartOffRailGroundFriction float64 = 0.5
	MinecartOffRailAirDrag        float64 = 0.95

	// Furnace minecart fuel constants (coal / charcoal).
	FurnaceMinecartFuelPerCoal = 3600
	FurnaceMinecartMaxFuel     = 32000
	FurnaceMinecartPushAccel   = 0.04

	// TNT minecart fuse constants.
	TNTMinecartDefaultFuseTicks = 80
	TNTMinecartRailPrimeTicks   = 80
	TNTMinecartExplosionRadius  = 4.0

	// Detector rail check interval (20 ticks = 1.0s).
	DetectorRailCheckIntervalTicks = 20

	// Boat medium friction & buoyancy constants (AbstractBoat.getWaterLevelAbove / floatBoat).
	BoatFrictionWater     float64 = 0.90
	BoatFrictionIce       float64 = 0.98
	BoatFrictionBlueIce   float64 = 0.989
	BoatFrictionLand      float64 = 0.60
	BoatFrictionAir       float64 = 0.90
	BoatGravity           float64 = 0.04
	BoatForwardAccel      float64 = 0.04
	BoatBackwardAccel     float64 = 0.005
	BoatTurnAccelDeg      float32 = 4.0
	BoatYawDecay          float32 = 0.8
	BoatMaxSubmergedTicks int     = 60
)

// BoatMedium classifies the surface medium beneath or around a boat.
type BoatMedium int8

const (
	BoatMediumInAir BoatMedium = iota
	BoatMediumInWater
	BoatMediumUnderWater
	BoatMediumOnIce
	BoatMediumOnBlueIce
	BoatMediumOnLand
)

// PlayerInput represents a decoded ServerboundPlayerInputPacket (0x2b).
type PlayerInput struct {
	Forward  bool
	Backward bool
	Left     bool
	Right    bool
	Jump     bool
	Shift    bool
	Sprint   bool
}

// Flags returns the 26.1.2 1-byte bitfield representation of PlayerInput.
func (in PlayerInput) Flags() byte {
	var b byte
	if in.Forward {
		b |= PlayerInputFlagForward
	}
	if in.Backward {
		b |= PlayerInputFlagBackward
	}
	if in.Left {
		b |= PlayerInputFlagLeft
	}
	if in.Right {
		b |= PlayerInputFlagRight
	}
	if in.Jump {
		b |= PlayerInputFlagJump
	}
	if in.Shift {
		b |= PlayerInputFlagShift
	}
	if in.Sprint {
		b |= PlayerInputFlagSprint
	}
	return b
}

// VehicleState holds the server-side state for any active minecart or boat/raft entity.
type VehicleState struct {
	EntityID    int32
	TypeName    string
	Dimension   string
	Passengers  []int32
	Damage      float32
	HurtTime    int32
	HurtDir     int32
	CustomName  string
	LeftPaddle  bool
	RightPaddle bool
	BubbleTime  int32
	DeltaRot    float32
	Submerged   int

	// Container inventory (27 slots for chest_minecart / *_chest_boat, 5 slots for hopper_minecart).
	Items [VehicleChestSlotCount]world.ItemStack

	// Furnace minecart fields.
	Fuel  int
	PushX float64
	PushZ float64

	// TNT minecart fields (-1 when not primed).
	FuseTicks int

	// Hopper minecart fields.
	HopperEnabled  bool
	TransferCooldown int
}

// Clone returns a deep copy of VehicleState.
func (v VehicleState) Clone() VehicleState {
	cp := v
	if len(v.Passengers) > 0 {
		cp.Passengers = make([]int32, len(v.Passengers))
		copy(cp.Passengers, v.Passengers)
	}
	return cp
}

// HopperMinecartTransferHook is an optional callback that Agent C's hopper system
// can register to perform per-tick item transfers between a hopper_minecart and
// blocks/containers above or below it.
type HopperMinecartTransferHook func(s *Server, entityID int32, dim string, x, y, z float64) bool

// DetectorRailStateHook is an optional callback that Agent C's redstone system
// can register to be notified when a detector_rail changes its powered state.
type DetectorRailStateHook func(s *Server, dim string, x, y, z int, powered bool)

type serverVehicleManager struct {
	mu                sync.Mutex
	vehicles          map[int32]*VehicleState
	byPassengerPlayer map[*PlayerSession]int32 // session -> vehicleEntityID
	byPassengerEntity map[int32]int32          // passengerEntityID -> vehicleEntityID
	playerInputs      map[*PlayerSession]PlayerInput
	activeDetectorRails map[dimBlockKey]int
	hopperHook        HopperMinecartTransferHook
	detectorRailHook  DetectorRailStateHook
}

var serverVehicleManagers sync.Map // map[*Server]*serverVehicleManager

func (s *Server) vehicleManager() *serverVehicleManager {
	if s == nil {
		return &serverVehicleManager{
			vehicles:            make(map[int32]*VehicleState),
			byPassengerPlayer:   make(map[*PlayerSession]int32),
			byPassengerEntity:   make(map[int32]int32),
			playerInputs:        make(map[*PlayerSession]PlayerInput),
			activeDetectorRails: make(map[dimBlockKey]int),
		}
	}
	if v, ok := serverVehicleManagers.Load(s); ok {
		return v.(*serverVehicleManager)
	}
	initMgr := &serverVehicleManager{
		vehicles:            make(map[int32]*VehicleState),
		byPassengerPlayer:   make(map[*PlayerSession]int32),
		byPassengerEntity:   make(map[int32]int32),
		playerInputs:        make(map[*PlayerSession]PlayerInput),
		activeDetectorRails: make(map[dimBlockKey]int),
	}
	actual, _ := serverVehicleManagers.LoadOrStore(s, initMgr)
	return actual.(*serverVehicleManager)
}

// RegisterHopperMinecartHook registers a callback invoked every tick for each
// enabled `minecraft:hopper_minecart` so the hopper subsystem can pull from
// containers above or push into hoppers below.
func (s *Server) RegisterHopperMinecartHook(hook HopperMinecartTransferHook) {
	if s == nil {
		return
	}
	mgr := s.vehicleManager()
	mgr.mu.Lock()
	mgr.hopperHook = hook
	mgr.mu.Unlock()
}

// RegisterDetectorRailHook registers a callback invoked whenever a
// `minecraft:detector_rail` changes its `powered` state due to a minecart.
func (s *Server) RegisterDetectorRailHook(hook DetectorRailStateHook) {
	if s == nil {
		return
	}
	mgr := s.vehicleManager()
	mgr.mu.Lock()
	mgr.detectorRailHook = hook
	mgr.mu.Unlock()
}

// --- Vehicle Classification & Item Mapping ---

// IsMinecartEntity reports whether typeName is a minecart entity type.
func IsMinecartEntity(typeName string) bool {
	norm := canonicalResourceName(typeName)
	switch norm {
	case EntityTypeMinecart, EntityTypeChestMinecart, EntityTypeFurnaceMinecart,
		EntityTypeTNTMinecart, EntityTypeHopperMinecart,
		EntityTypeSpawnerMinecart, EntityTypeCommandBlockMinecart:
		return true
	default:
		return false
	}
}

// IsBoatEntity reports whether typeName is any boat or raft entity (including chest boats/rafts).
func IsBoatEntity(typeName string) bool {
	norm := canonicalResourceName(typeName)
	return strings.HasSuffix(norm, "_boat") || strings.HasSuffix(norm, "_raft")
}

// IsChestBoatEntity reports whether typeName is a chest boat or chest raft entity.
func IsChestBoatEntity(typeName string) bool {
	norm := canonicalResourceName(typeName)
	return strings.HasSuffix(norm, "_chest_boat") || strings.HasSuffix(norm, "_chest_raft")
}

// IsVehicleEntity reports whether typeName is a minecart or boat/raft entity.
func IsVehicleEntity(typeName string) bool {
	return IsMinecartEntity(typeName) || IsBoatEntity(typeName)
}

// IsRideableVehicleEntity reports whether a player or mob can mount the vehicle
// (rideable `minecraft:minecart` or any boat/raft).
func IsRideableVehicleEntity(typeName string) bool {
	norm := canonicalResourceName(typeName)
	return norm == EntityTypeMinecart || IsBoatEntity(norm)
}

// IsBoatItem reports whether itemID is a boat or raft item.
func IsBoatItem(itemID string) bool {
	return IsBoatEntity(itemID)
}

// IsContainerVehicleEntity reports whether the vehicle has an inventory screen
// (`minecraft:chest_minecart`, `minecraft:hopper_minecart`, or `*_chest_boat`/`*_chest_raft`).
func IsContainerVehicleEntity(typeName string) bool {
	norm := canonicalResourceName(typeName)
	return norm == EntityTypeChestMinecart || norm == EntityTypeHopperMinecart || IsChestBoatEntity(norm)
}

// IsVehicleContainerEntity is an alias for IsContainerVehicleEntity.
func IsVehicleContainerEntity(typeName string) bool {
	return IsContainerVehicleEntity(typeName)
}

// MaxVehiclePassengers returns the maximum number of simultaneous passengers for typeName.
func MaxVehiclePassengers(typeName string) int {
	norm := canonicalResourceName(typeName)
	switch {
	case norm == EntityTypeMinecart:
		return MaxMinecartPassengers
	case IsChestBoatEntity(norm):
		return MaxChestBoatPassengers
	case IsBoatEntity(norm):
		return MaxBoatPassengers
	default:
		return 0
	}
}

// VehicleEntityTypeForItem maps a minecart or boat/raft item ID to its entity_type ID.
func VehicleEntityTypeForItem(itemID string) (string, bool) {
	norm := canonicalResourceName(itemID)
	if IsMinecartEntity(norm) || IsBoatEntity(norm) {
		return norm, true
	}
	return "", false
}

// VehicleDropItemForType returns the primary item ID dropped when a vehicle breaks.
func VehicleDropItemForType(typeName string) string {
	norm := canonicalResourceName(typeName)
	switch norm {
	case EntityTypeSpawnerMinecart, EntityTypeCommandBlockMinecart:
		return EntityTypeMinecart
	default:
		if IsVehicleEntity(norm) {
			return norm
		}
		return ""
	}
}

// IsRailBlockState reports whether state is any vanilla rail block (`rail`,
// `powered_rail`, `detector_rail`, `activator_rail`).
func IsRailBlockState(state uint16) bool {
	return isRailBlock(state)
}

// --- Packet Encoding & Decoding (Protocol 775 / 26.1.2) ---

// EncodePlayerInputPacket encodes a 26.1.2 ServerboundPlayerInputPacket (0x2b).
func EncodePlayerInputPacket(in PlayerInput) []byte {
	return []byte{in.Flags()}
}

// DecodePlayerInputPacket decodes a ServerboundPlayerInputPacket (0x2b),
// supporting both the 26.1.2 1-byte bitfield format and the legacy 9-byte
// (float32 xxa, float32 zza, byte flags) format.
func DecodePlayerInputPacket(r *protocol.Reader) (PlayerInput, error) {
	if r == nil || r.Remaining() == 0 {
		return PlayerInput{}, errors.New("vehicle: empty player_input packet")
	}
	if r.Remaining() >= 9 {
		xxa, err := r.Float32()
		if err != nil {
			return PlayerInput{}, err
		}
		zza, err := r.Float32()
		if err != nil {
			return PlayerInput{}, err
		}
		flags, err := r.ReadByte()
		if err != nil {
			return PlayerInput{}, err
		}
		return PlayerInput{
			Forward:  zza > 0 || (flags&PlayerInputFlagForward) != 0,
			Backward: zza < 0 || (flags&PlayerInputFlagBackward) != 0,
			Left:     xxa > 0 || (flags&PlayerInputFlagLeft) != 0,
			Right:    xxa < 0 || (flags&PlayerInputFlagRight) != 0,
			Jump:     (flags & 0x01) != 0,
			Shift:    (flags & 0x02) != 0,
		}, nil
	}
	flags, err := r.ReadByte()
	if err != nil {
		return PlayerInput{}, err
	}
	return PlayerInput{
		Forward:  (flags & PlayerInputFlagForward) != 0,
		Backward: (flags & PlayerInputFlagBackward) != 0,
		Left:     (flags & PlayerInputFlagLeft) != 0,
		Right:    (flags & PlayerInputFlagRight) != 0,
		Jump:     (flags & PlayerInputFlagJump) != 0,
		Shift:    (flags & PlayerInputFlagShift) != 0,
		Sprint:   (flags & PlayerInputFlagSprint) != 0,
	}, nil
}

// EncodeMoveVehiclePacket encodes a clientbound (0x39) or serverbound (0x22)
// move_vehicle packet: Float64(x), Float64(y), Float64(z), Float32(yaw), Float32(pitch).
func EncodeMoveVehiclePacket(x, y, z float64, yaw, pitch float32) []byte {
	w := protocol.NewWriter(32)
	w.Float64(x)
	w.Float64(y)
	w.Float64(z)
	w.Float32(yaw)
	w.Float32(pitch)
	return w.Bytes()
}

// DecodeMoveVehiclePacket decodes a move_vehicle packet.
func DecodeMoveVehiclePacket(r *protocol.Reader) (x, y, z float64, yaw, pitch float32, onGround bool, err error) {
	if r == nil {
		return 0, 0, 0, 0, 0, false, errors.New("vehicle: nil reader")
	}
	if x, err = r.Float64(); err != nil {
		return
	}
	if y, err = r.Float64(); err != nil {
		return
	}
	if z, err = r.Float64(); err != nil {
		return
	}
	if yaw, err = r.Float32(); err != nil {
		return
	}
	if pitch, err = r.Float32(); err != nil {
		return
	}
	if r.Remaining() > 0 {
		onGround, _ = r.Bool()
	}
	return
}

// EncodePaddleBoatPacket encodes a ServerboundPaddleBoatPacket (0x23):
// Bool(leftPaddleTurning), Bool(rightPaddleTurning).
func EncodePaddleBoatPacket(leftPaddle, rightPaddle bool) []byte {
	w := protocol.NewWriter(2)
	w.Bool(leftPaddle)
	w.Bool(rightPaddle)
	return w.Bytes()
}

// DecodePaddleBoatPacket decodes a ServerboundPaddleBoatPacket (0x23).
func DecodePaddleBoatPacket(r *protocol.Reader) (leftPaddle, rightPaddle bool, err error) {
	if r == nil {
		return false, false, errors.New("vehicle: nil reader")
	}
	if leftPaddle, err = r.Bool(); err != nil {
		return false, false, err
	}
	if rightPaddle, err = r.Bool(); err != nil {
		return false, false, err
	}
	return leftPaddle, rightPaddle, nil
}

// EncodeVehiclePassengersPacket encodes a ClientboundSetPassengersPacket (0x6b):
// VarInt(vehicleEntityID), VarInt(len(passengerIDs)), VarInt(passengerID)...
func EncodeVehiclePassengersPacket(vehicleEntityID int32, passengerIDs []int32) []byte {
	w := protocol.NewWriter(8 + len(passengerIDs)*4)
	w.VarInt(vehicleEntityID)
	w.VarInt(int32(len(passengerIDs)))
	for _, pid := range passengerIDs {
		w.VarInt(pid)
	}
	return w.Bytes()
}

// DecodeVehiclePassengersPacket decodes a ClientboundSetPassengersPacket (0x6b).
func DecodeVehiclePassengersPacket(r *protocol.Reader) (vehicleEntityID int32, passengerIDs []int32, err error) {
	if r == nil {
		return 0, nil, errors.New("vehicle: nil reader")
	}
	vehicleEntityID, err = r.VarInt()
	if err != nil {
		return 0, nil, err
	}
	n, err := r.VarInt()
	if err != nil || n < 0 || n > 16 {
		return 0, nil, errors.New("vehicle: invalid passenger count")
	}
	passengerIDs = make([]int32, n)
	for i := int32(0); i < n; i++ {
		passengerIDs[i], err = r.VarInt()
		if err != nil {
			return 0, nil, err
		}
	}
	return vehicleEntityID, passengerIDs, nil
}

// EncodeVehicleEntityDataPacket encodes VehicleEntity hurt/damage synched entity
// data (indices 8, 9, 10).
func EncodeVehicleEntityDataPacket(entityID, hurtTime, hurtDir int32, damage float32) []byte {
	w := protocol.NewWriter(24)
	w.VarInt(entityID)
	w.Byte(EntityDataVehicleHurtID)
	w.VarInt(EntityDataSerializerInt)
	w.VarInt(hurtTime)
	w.Byte(EntityDataVehicleHurtDirID)
	w.VarInt(EntityDataSerializerInt)
	w.VarInt(hurtDir)
	w.Byte(EntityDataVehicleDamageID)
	w.VarInt(EntityDataSerializerFloat)
	w.Float32(damage)
	w.Byte(0xFF)
	return w.Bytes()
}

// EncodeBoatPaddleDataPacket encodes AbstractBoat paddle turning and bubble time
// synched entity data (indices 11, 12, 13).
func EncodeBoatPaddleDataPacket(entityID int32, leftPaddle, rightPaddle bool, bubbleTime int32) []byte {
	w := protocol.NewWriter(20)
	w.VarInt(entityID)
	w.Byte(EntityDataBoatLeftPaddleID)
	w.VarInt(EntityDataSerializerBool)
	w.Bool(leftPaddle)
	w.Byte(EntityDataBoatRightPaddleID)
	w.VarInt(EntityDataSerializerBool)
	w.Bool(rightPaddle)
	w.Byte(EntityDataBoatBubbleTimeID)
	w.VarInt(EntityDataSerializerInt)
	w.VarInt(bubbleTime)
	w.Byte(0xFF)
	return w.Bytes()
}

// EncodeFurnaceMinecartDataPacket encodes MinecartFurnace lit fuel synched entity
// data (index 13).
func EncodeFurnaceMinecartDataPacket(entityID int32, lit bool) []byte {
	w := protocol.NewWriter(10)
	w.VarInt(entityID)
	w.Byte(EntityDataFurnaceMinecartFuelID)
	w.VarInt(EntityDataSerializerBool)
	w.Bool(lit)
	w.Byte(0xFF)
	return w.Bytes()
}

// VehicleEntityDataPackets returns the PlaySetEntityData packets reflecting the
// live wobble/hurt state, boat paddle/bubble-time state, or furnace minecart
// lit state for entityID.
func (s *Server) VehicleEntityDataPackets(entityID int32) [][]byte {
	if s == nil || entityID <= 0 {
		return nil
	}
	st, ok := s.VehicleStateByID(entityID)
	if !ok {
		return [][]byte{EncodeVehicleEntityDataPacket(entityID, 0, 1, 0.0)}
	}
	hurtDir := st.HurtDir
	if hurtDir == 0 {
		hurtDir = 1
	}
	pkts := [][]byte{
		EncodeVehicleEntityDataPacket(entityID, st.HurtTime, hurtDir, st.Damage),
	}
	if IsBoatEntity(st.TypeName) && (st.LeftPaddle || st.RightPaddle || st.BubbleTime > 0) {
		pkts = append(pkts, EncodeBoatPaddleDataPacket(entityID, st.LeftPaddle, st.RightPaddle, st.BubbleTime))
	} else if st.TypeName == EntityTypeFurnaceMinecart && st.Fuel > 0 {
		pkts = append(pkts, EncodeFurnaceMinecartDataPacket(entityID, true))
	}
	return pkts
}

// --- Spawning & State Accessors ---

// SpawnVehicle spawns a minecart or boat/raft entity of typeName in dim at
// (x, y, z) with initial yaw and tracks its VehicleState.
func (s *Server) SpawnVehicle(dim, typeName string, x, y, z float64, yaw float32) (int32, *world.Entity) {
	if s == nil || s.entities == nil {
		return 0, nil
	}
	normType := canonicalResourceName(typeName)
	if !IsVehicleEntity(normType) {
		return 0, nil
	}
	normDim := world.NormalizeDimension(dim)
	typeID := registry.EntityTypeIndex(normType)
	if typeID < 0 {
		typeID = 0
	}

	// If spawning a minecart on a rail block, snap Y to the rail height.
	if IsMinecartEntity(normType) {
		if cache := s.DimensionChunks(normDim); cache != nil {
			bx := int(math.Floor(x))
			by := int(math.Floor(y))
			bz := int(math.Floor(z))
			if isRailBlock(cache.GetBlock(bx, by, bz)) {
				y = railElevationAt(cache.GetBlock(bx, by, bz), x, by, z)
			}
		}
	}

	ent := &world.Entity{
		TypeID:    typeID,
		TypeName:  normType,
		Dimension: normDim,
		X:         x,
		Y:         y,
		Z:         z,
		Yaw:       yaw,
		HeadYaw:   yaw,
		OnGround:  true,
		Health:    6.0,
		MaxHealth: 6.0,
	}
	entityID := s.entities.Add(ent)

	st := &VehicleState{
		EntityID:      entityID,
		TypeName:      normType,
		Dimension:     normDim,
		HurtDir:       1,
		FuseTicks:     -1,
		HopperEnabled: true,
	}
	mgr := s.vehicleManager()
	mgr.mu.Lock()
	mgr.vehicles[entityID] = st
	mgr.mu.Unlock()

	saved := s.entities.Get(entityID)
	if saved != nil {
		cp := saved.ChunkPos()
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, saved))
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlaySetEntityData,
			EncodeVehicleEntityDataPacket(entityID, 0, 1, 0))
	}

	// If a minecart spawns directly on a detector_rail or activator_rail, evaluate immediately.
	if IsMinecartEntity(normType) {
		s.evaluateMinecartRailTriggers(entityID)
	}
	return entityID, saved
}

func (s *Server) ensureVehicleState(entityID int32) *VehicleState {
	if s == nil || s.entities == nil || entityID <= 0 {
		return nil
	}
	ent := s.entities.Get(entityID)
	if ent == nil || !IsVehicleEntity(ent.TypeName) {
		return nil
	}
	normType := canonicalResourceName(ent.TypeName)
	mgr := s.vehicleManager()
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	st, ok := mgr.vehicles[entityID]
	if !ok || st == nil {
		st = &VehicleState{
			EntityID:      entityID,
			TypeName:      normType,
			Dimension:     ent.EffectiveDimension(),
			HurtDir:       1,
			FuseTicks:     -1,
			HopperEnabled: true,
		}
		mgr.vehicles[entityID] = st
	}
	return st
}

// VehicleStateByID returns a snapshot of the VehicleState for entityID.
func (s *Server) VehicleStateByID(entityID int32) (VehicleState, bool) {
	if s == nil || entityID <= 0 {
		return VehicleState{}, false
	}
	st := s.ensureVehicleState(entityID)
	if st == nil {
		return VehicleState{}, false
	}
	mgr := s.vehicleManager()
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	return st.Clone(), true
}

// UseVehicleItemOnBlock handles placing a minecart on a rail block or a boat/raft
// on water/ground when right-clicking a block in handleUseItemOn.
func (s *Server) UseVehicleItemOnBlock(
	session *PlayerSession,
	hand int32,
	blockX, blockY, blockZ int,
	face int32,
	cursorX, cursorY, cursorZ float32,
) (int32, bool) {
	if s == nil || session == nil || session.IsDead() {
		return 0, false
	}
	_, stack := session.handSlotAndStack(hand)
	if stack.IsEmpty() {
		return 0, false
	}
	vehType, ok := VehicleEntityTypeForItem(stack.ResolvedID())
	if !ok {
		return 0, false
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator {
		return 0, false
	}
	dim := world.NormalizeDimension(snap.Dimension)
	cache := s.DimensionChunks(dim)
	if cache == nil {
		return 0, false
	}

	clickedState := cache.GetBlock(blockX, blockY, blockZ)
	if IsMinecartEntity(vehType) {
		// Minecarts can only be placed directly on rail blocks.
		if !isRailBlock(clickedState) {
			return 0, false
		}
		if !s.consumeHandItemOne(session, hand) {
			return 0, false
		}
		s.RecordItemUsed(session, vehType, 1)
		spawnX := float64(blockX) + 0.5
		spawnY := railElevationAt(clickedState, spawnX, blockY, float64(blockZ)+0.5)
		spawnZ := float64(blockZ) + 0.5
		id, _ := s.SpawnVehicle(dim, vehType, spawnX, spawnY, spawnZ, snap.Yaw)
		s.EmitVibration(VibrationEvent{
			Dimension:      dim,
			Type:           GameEventEntityPlace,
			X:              spawnX,
			Y:              spawnY,
			Z:              spawnZ,
			Player:         session,
			SourceEntityID: snap.EntityID,
			Sneaking:       session.IsSneaking(),
			BlockOrItemID:  vehType,
		})
		return id, id != 0
	}

	// Boats / rafts: place on top of the clicked block (or on water surface).
	var offX, offY, offZ int
	switch face {
	case 0:
		offY = -1
	case 1:
		offY = 1
	case 2:
		offZ = -1
	case 3:
		offZ = 1
	case 4:
		offX = -1
	case 5:
		offX = 1
	}
	targetX := blockX + offX
	targetY := blockY + offY
	targetZ := blockZ + offZ
	if world.BlockName(clickedState) == "minecraft:water" {
		targetX, targetY, targetZ = blockX, blockY+1, blockZ
	}
	spawnX := float64(targetX) + 0.5
	spawnY := float64(targetY)
	spawnZ := float64(targetZ) + 0.5
	if world.BlockName(cache.GetBlock(targetX, targetY-1, targetZ)) == "minecraft:water" {
		spawnY = float64(targetY) - 0.1
	}
	if !s.consumeHandItemOne(session, hand) {
		return 0, false
	}
	s.RecordItemUsed(session, vehType, 1)
	id, _ := s.SpawnVehicle(dim, vehType, spawnX, spawnY, spawnZ, snap.Yaw)
	s.EmitVibration(VibrationEvent{
		Dimension:      dim,
		Type:           GameEventEntityPlace,
		X:              spawnX,
		Y:              spawnY,
		Z:              spawnZ,
		Player:         session,
		SourceEntityID: snap.EntityID,
		Sneaking:       session.IsSneaking(),
		BlockOrItemID:  vehType,
	})
	return id, id != 0
}

// UseBoatItemInAir handles right-clicking in air (PlayUseItemSB) while holding
// a boat/raft item and looking at water or a solid surface within 5 blocks
// (matching vanilla BoatItem.use Fluid.SOURCE_ONLY raytrace).
func (s *Server) UseBoatItemInAir(session *PlayerSession, hand int32) (int32, bool) {
	if s == nil || session == nil || session.IsDead() {
		return 0, false
	}
	_, stack := session.handSlotAndStack(hand)
	if stack.IsEmpty() {
		return 0, false
	}
	vehType, ok := VehicleEntityTypeForItem(stack.ResolvedID())
	if !ok || !IsBoatEntity(vehType) {
		return 0, false
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator {
		return 0, false
	}
	dim := world.NormalizeDimension(snap.Dimension)
	cache := s.DimensionChunks(dim)
	if cache == nil {
		return 0, false
	}

	lx, ly, lz := ElytraLookVector(snap.Yaw, snap.Pitch)
	eyeX, eyeY, eyeZ := snap.X, snap.Y+1.62, snap.Z
	for step := 1; step <= 20; step++ {
		dist := float64(step) * 0.25
		rx := eyeX + lx*dist
		ry := eyeY + ly*dist
		rz := eyeZ + lz*dist
		bx, by, bz := int(math.Floor(rx)), int(math.Floor(ry)), int(math.Floor(rz))
		st := cache.GetBlock(bx, by, bz)
		if st == world.StateAir {
			continue
		}
		name := world.BlockName(st)
		if name == "minecraft:water" || !isPassableBlock(st) {
			if !s.consumeHandItemOne(session, hand) {
				return 0, false
			}
			s.RecordItemUsed(session, vehType, 1)
			spawnY := float64(by + 1)
			if name == "minecraft:water" {
				spawnY = float64(by+1) - 0.1
			}
			id, _ := s.SpawnVehicle(dim, vehType, float64(bx)+0.5, spawnY, float64(bz)+0.5, snap.Yaw)
			return id, id != 0
		}
	}
	return 0, false
}

// --- Mounting, Dismounting & Entity Interaction (1) ---

// PlayerMountedVehicle returns the entity ID of the vehicle currently ridden by
// session, or 0 if session is not riding a vehicle.
func (s *Server) PlayerMountedVehicle(session *PlayerSession) int32 {
	if s == nil || session == nil {
		return 0
	}
	mgr := s.vehicleManager()
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	return mgr.byPassengerPlayer[session]
}

// VehiclePassengers returns a copy of the ordered passenger entity IDs riding vehicleID.
func (s *Server) VehiclePassengers(vehicleID int32) []int32 {
	st, ok := s.VehicleStateByID(vehicleID)
	if !ok {
		return nil
	}
	return st.Passengers
}

// VehicleDriverEntityID returns the controlling passenger (seat 0) entity ID
// of vehicleID, or 0 if unridden.
func (s *Server) VehicleDriverEntityID(vehicleID int32) int32 {
	passengers := s.VehiclePassengers(vehicleID)
	if len(passengers) == 0 {
		return 0
	}
	return passengers[0]
}

func (s *Server) broadcastVehiclePassengers(dim string, cx, cz int32, vehicleID int32, passengers []int32) {
	pkt := EncodeVehiclePassengersPacket(vehicleID, passengers)
	s.BroadcastDimensionChunk(dim, cx, cz, protocol.PlaySetPassengers, pkt)
}

// MountPlayerOnVehicle mounts session onto the rideable vehicle (boat, chest
// boat, or rideable minecart) with vehicleID, syncing passenger lists via
// ClientboundSetPassengersPacket (0x6b) and emitting GameEventEntityMount.
func (s *Server) MountPlayerOnVehicle(session *PlayerSession, vehicleID int32) (seatIndex int, ok bool) {
	if s == nil || s.entities == nil || session == nil || session.IsDead() {
		return -1, false
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator || snap.Health <= 0 {
		return -1, false
	}
	ent := s.entities.Get(vehicleID)
	if ent == nil || !IsRideableVehicleEntity(ent.TypeName) {
		return -1, false
	}
	// Vanilla: cannot mount a rideable minecart that is currently on a powered activator_rail.
	if canonicalResourceName(ent.TypeName) == EntityTypeMinecart && s.isVehicleOnPoweredActivatorRail(ent) {
		return -1, false
	}

	// If the player is already riding another vehicle, dismount first.
	if curVeh := s.PlayerMountedVehicle(session); curVeh != 0 && curVeh != vehicleID {
		s.DismountPlayerVehicle(session)
	}

	st := s.ensureVehicleState(vehicleID)
	if st == nil {
		return -1, false
	}
	maxCap := MaxVehiclePassengers(st.TypeName)
	if maxCap <= 0 {
		return -1, false
	}

	pid := session.EntityID
	if pid == 0 {
		pid = -1
	}
	mgr := s.vehicleManager()
	mgr.mu.Lock()
	for i, existing := range st.Passengers {
		if existing == pid {
			mgr.byPassengerPlayer[session] = vehicleID
			mgr.byPassengerEntity[pid] = vehicleID
			mgr.mu.Unlock()
			return i, true
		}
	}
	if len(st.Passengers) >= maxCap {
		mgr.mu.Unlock()
		return -1, false
	}
	st.Passengers = append(st.Passengers, pid)
	seatIndex = len(st.Passengers) - 1
	mgr.byPassengerPlayer[session] = vehicleID
	mgr.byPassengerEntity[pid] = vehicleID
	passengersCopy := make([]int32, len(st.Passengers))
	copy(passengersCopy, st.Passengers)
	mgr.mu.Unlock()

	s.SetPlayerTransform(session, ent.X, ent.Y, ent.Z, snap.Yaw, snap.Pitch, false)
	session.SetFallDistance(0)
	cp := ent.ChunkPos()
	dim := ent.EffectiveDimension()
	s.broadcastVehiclePassengers(dim, cp.X, cp.Z, vehicleID, passengersCopy)
	s.checkBoatWithGoatAdvancement(vehicleID)
	s.refreshVehicleDetectorRailComparators(vehicleID)
	s.EmitVibration(VibrationEvent{
		Dimension:      dim,
		Type:           GameEventEntityMount,
		X:              ent.X,
		Y:              ent.Y,
		Z:              ent.Z,
		Player:         session,
		SourceEntityID: snap.EntityID,
		Sneaking:       session.IsSneaking(),
	})
	return seatIndex, true
}

// MountEntityOnVehicle mounts a non-player mob entity onto vehicleID.
func (s *Server) MountEntityOnVehicle(passengerEntityID, vehicleID int32) (seatIndex int, ok bool) {
	if s == nil || s.entities == nil || passengerEntityID <= 0 || vehicleID <= 0 || passengerEntityID == vehicleID {
		return -1, false
	}
	vehEnt := s.entities.Get(vehicleID)
	passEnt := s.entities.Get(passengerEntityID)
	if vehEnt == nil || passEnt == nil || !IsRideableVehicleEntity(vehEnt.TypeName) {
		return -1, false
	}
	if passEnt.IsItem() || passEnt.IsExperienceOrb() || IsVehicleEntity(passEnt.TypeName) || IsDecorationEntity(passEnt.TypeName) {
		return -1, false
	}
	st := s.ensureVehicleState(vehicleID)
	if st == nil {
		return -1, false
	}
	maxCap := MaxVehiclePassengers(st.TypeName)
	mgr := s.vehicleManager()
	mgr.mu.Lock()
	if curVeh := mgr.byPassengerEntity[passengerEntityID]; curVeh != 0 {
		mgr.mu.Unlock()
		return -1, false
	}
	if len(st.Passengers) >= maxCap {
		mgr.mu.Unlock()
		return -1, false
	}
	st.Passengers = append(st.Passengers, passengerEntityID)
	seatIndex = len(st.Passengers) - 1
	mgr.byPassengerEntity[passengerEntityID] = vehicleID
	passengersCopy := make([]int32, len(st.Passengers))
	copy(passengersCopy, st.Passengers)
	mgr.mu.Unlock()

	s.entities.Update(passengerEntityID, func(e *world.Entity) {
		e.X = vehEnt.X
		e.Y = vehEnt.Y
		e.Z = vehEnt.Z
	})
	cp := vehEnt.ChunkPos()
	s.broadcastVehiclePassengers(vehEnt.EffectiveDimension(), cp.X, cp.Z, vehicleID, passengersCopy)
	s.checkBoatWithGoatAdvancement(vehicleID)
	s.refreshVehicleDetectorRailComparators(vehicleID)
	return seatIndex, true
}

func (s *Server) checkBoatWithGoatAdvancement(vehicleID int32) {
	if s == nil || s.entities == nil || vehicleID <= 0 {
		return
	}
	mgr := s.vehicleManager()
	mgr.mu.Lock()
	st := mgr.vehicles[vehicleID]
	if st == nil || !IsBoatEntity(st.TypeName) || len(st.Passengers) < 2 {
		mgr.mu.Unlock()
		return
	}
	passengers := make([]int32, len(st.Passengers))
	copy(passengers, st.Passengers)
	var sessions []*PlayerSession
	for sess, vid := range mgr.byPassengerPlayer {
		if vid == vehicleID && sess != nil {
			sessions = append(sessions, sess)
		}
	}
	mgr.mu.Unlock()
	if len(sessions) == 0 {
		return
	}
	hasGoat := false
	for _, pid := range passengers {
		if pid > 0 {
			if ent := s.entities.Get(pid); ent != nil && canonicalResourceName(ent.TypeName) == "minecraft:goat" {
				hasGoat = true
				break
			}
		}
	}
	if hasGoat {
		for _, sess := range sessions {
			s.TriggerRideBoatWithGoat(sess)
		}
	}
}

// PlayerMountedCamel returns the entity ID of the Camel currently ridden by
// session, or 0 if session is not riding a Camel.
func (s *Server) PlayerMountedCamel(session *PlayerSession) int32 {
	if s == nil || session == nil {
		return 0
	}
	pid := session.EntityID
	mgr := s.camelManager()
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()
	for camelID, st := range mgr.camels {
		if st == nil {
			continue
		}
		for _, r := range st.Passengers {
			if r == pid {
				return camelID
			}
		}
	}
	return 0
}

// DismountPlayerFromAllCamels removes playerEntityID from any Camel's passenger
// list and broadcasts updated SetPassengers packets.
func (s *Server) DismountPlayerFromAllCamels(playerEntityID int32) bool {
	if s == nil {
		return false
	}
	mgr := s.camelManager()
	mgr.mu.RLock()
	var camelIDs []int32
	for cid, st := range mgr.camels {
		if st == nil {
			continue
		}
		for _, r := range st.Passengers {
			if r == playerEntityID {
				camelIDs = append(camelIDs, cid)
				break
			}
		}
	}
	mgr.mu.RUnlock()
	if len(camelIDs) == 0 {
		return false
	}
	dismounted := false
	if sess := s.playerByEntityID(playerEntityID); sess != nil {
		for _, cid := range camelIDs {
			if s.DismountCamel(sess, cid) {
				dismounted = true
			}
		}
		return dismounted
	}
	for _, cid := range camelIDs {
		mgr.mu.Lock()
		st := mgr.camels[cid]
		var passengersCopy []int32
		removed := false
		if st != nil {
			for i, r := range st.Passengers {
				if r == playerEntityID {
					st.Passengers = append(st.Passengers[:i], st.Passengers[i+1:]...)
					removed = true
					break
				}
			}
			passengersCopy = make([]int32, len(st.Passengers))
			copy(passengersCopy, st.Passengers)
		}
		mgr.mu.Unlock()
		if removed {
			dismounted = true
			if s.entities != nil {
				if ent := s.entities.Get(cid); ent != nil {
					cp := ent.ChunkPos()
					s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, PlaySetPassengersCB,
						EncodeSetPassengersPacket(cid, passengersCopy))
				}
			}
		}
	}
	return dismounted
}

// PlayerMountedEquine returns the entity ID of the horse/donkey/mule/llama/
// skeleton_horse/zombie_horse currently ridden by session, or 0 if not riding one.
func (s *Server) PlayerMountedEquine(session *PlayerSession) int32 {
	if s == nil || session == nil {
		return 0
	}
	pid := session.EntityID
	mgr := s.equineManager()
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()
	for eqID, st := range mgr.equines {
		if st != nil && st.RiderEntityID == pid && pid != 0 {
			return eqID
		}
	}
	return 0
}

// DismountPlayerFromAllEquines removes playerEntityID from any equine/llama mount.
func (s *Server) DismountPlayerFromAllEquines(playerEntityID int32) bool {
	if s == nil || playerEntityID <= 0 {
		return false
	}
	mgr := s.equineManager()
	mgr.mu.RLock()
	var eqIDs []int32
	for eqID, st := range mgr.equines {
		if st != nil && st.RiderEntityID == playerEntityID {
			eqIDs = append(eqIDs, eqID)
		}
	}
	mgr.mu.RUnlock()
	dismounted := false
	for _, eqID := range eqIDs {
		if s.DismountEquine(eqID) {
			dismounted = true
		}
	}
	return dismounted
}

// DismountPlayerVehicle dismounts session from any vehicle they are currently
// riding (including Minecarts, Boats, Camels, and Equines/Llamas), places the player at a safe
// dismount position, broadcasts SetPassengers, and emits GameEventEntityDismount.
func (s *Server) DismountPlayerVehicle(session *PlayerSession) bool {
	if s == nil || session == nil {
		return false
	}
	camelDismounted := false
	if camelID := s.PlayerMountedCamel(session); camelID > 0 {
		camelDismounted = s.DismountCamel(session, camelID)
	} else if s.DismountPlayerFromAllCamels(session.EntityID) {
		camelDismounted = true
	}
	if eqID := s.PlayerMountedEquine(session); eqID > 0 {
		if s.DismountEquine(eqID) {
			camelDismounted = true
		}
	} else if s.DismountPlayerFromAllEquines(session.EntityID) {
		camelDismounted = true
	}

	mgr := s.vehicleManager()
	mgr.mu.Lock()
	vehicleID, mounted := mgr.byPassengerPlayer[session]
	if !mounted || vehicleID <= 0 {
		mgr.mu.Unlock()
		return camelDismounted
	}
	delete(mgr.byPassengerPlayer, session)
	pid := session.EntityID
	if pid == 0 {
		pid = -1
	}
	delete(mgr.byPassengerEntity, pid)

	var passengersCopy []int32
	if st := mgr.vehicles[vehicleID]; st != nil {
		for i, existing := range st.Passengers {
			if existing == pid {
				st.Passengers = append(st.Passengers[:i], st.Passengers[i+1:]...)
				break
			}
		}
		passengersCopy = make([]int32, len(st.Passengers))
		copy(passengersCopy, st.Passengers)
	}
	mgr.mu.Unlock()

	snap := session.Snapshot()
	dim := world.NormalizeDimension(snap.Dimension)
	dismountX, dismountY, dismountZ := snap.X, snap.Y, snap.Z
	var cx, cz int32
	if s.entities != nil {
		if ent := s.entities.Get(vehicleID); ent != nil {
			dim = ent.EffectiveDimension()
			cp := ent.ChunkPos()
			cx, cz = cp.X, cp.Z
			dismountX, dismountY, dismountZ = s.resolveSafeDismountPos(dim, ent.X, ent.Y, ent.Z)
		}
	}
	s.SetPlayerTransform(session, dismountX, dismountY, dismountZ, snap.Yaw, snap.Pitch, true)
	session.SetFallDistance(0)
	s.broadcastVehiclePassengers(dim, cx, cz, vehicleID, passengersCopy)
	s.refreshVehicleDetectorRailComparators(vehicleID)
	s.EmitVibration(VibrationEvent{
		Dimension:      dim,
		Type:           GameEventEntityDismount,
		X:              dismountX,
		Y:              dismountY,
		Z:              dismountZ,
		Player:         session,
		SourceEntityID: snap.EntityID,
		Sneaking:       session.IsSneaking(),
	})
	return true
}

// DismountAllVehiclePassengers ejects all player and entity passengers from vehicleID.
func (s *Server) DismountAllVehiclePassengers(vehicleID int32) int {
	if s == nil || vehicleID <= 0 {
		return 0
	}
	mgr := s.vehicleManager()
	mgr.mu.Lock()
	st := mgr.vehicles[vehicleID]
	if st == nil || len(st.Passengers) == 0 {
		mgr.mu.Unlock()
		return 0
	}
	ejected := make([]int32, len(st.Passengers))
	copy(ejected, st.Passengers)
	st.Passengers = nil
	var sessionsToDismount []*PlayerSession
	for sess, vid := range mgr.byPassengerPlayer {
		if vid == vehicleID {
			delete(mgr.byPassengerPlayer, sess)
			sessionsToDismount = append(sessionsToDismount, sess)
		}
	}
	for _, pid := range ejected {
		delete(mgr.byPassengerEntity, pid)
	}
	mgr.mu.Unlock()

	var dim string = world.DimensionOverworld
	var cx, cz int32
	var vx, vy, vz float64
	if s.entities != nil {
		if ent := s.entities.Get(vehicleID); ent != nil {
			dim = ent.EffectiveDimension()
			cp := ent.ChunkPos()
			cx, cz = cp.X, cp.Z
			vx, vy, vz = ent.X, ent.Y, ent.Z
		}
	}
	dx, dy, dz := s.resolveSafeDismountPos(dim, vx, vy, vz)
	for _, sess := range sessionsToDismount {
		snap := sess.Snapshot()
		s.SetPlayerTransform(sess, dx, dy, dz, snap.Yaw, snap.Pitch, true)
		sess.SetFallDistance(0)
		s.EmitVibration(VibrationEvent{
			Dimension:      dim,
			Type:           GameEventEntityDismount,
			X:              dx,
			Y:              dy,
			Z:              dz,
			Player:         sess,
			SourceEntityID: snap.EntityID,
			Sneaking:       sess.IsSneaking(),
		})
	}
	s.broadcastVehiclePassengers(dim, cx, cz, vehicleID, nil)
	s.refreshVehicleDetectorRailComparators(vehicleID)
	return len(ejected)
}

func (s *Server) resolveSafeDismountPos(dim string, vx, vy, vz float64) (float64, float64, float64) {
	cache := s.DimensionChunks(dim)
	if cache == nil {
		return vx + 1.0, vy, vz
	}
	offsets := [][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}, {1, 1}, {-1, 1}, {1, -1}, {-1, -1}}
	bx0, by0, bz0 := int(math.Floor(vx)), int(math.Floor(vy)), int(math.Floor(vz))
	for _, off := range offsets {
		tx, tz := bx0+off[0], bz0+off[1]
		for dy := 0; dy <= 1; dy++ {
			ty := by0 + dy
			if isPassableBlock(cache.GetBlock(tx, ty, tz)) && isPassableBlock(cache.GetBlock(tx, ty+1, tz)) {
				return float64(tx) + 0.5, float64(ty), float64(tz) + 0.5
			}
		}
	}
	return vx, vy + 1.0, vz
}

// InteractWithVehicle handles right-clicking a minecart or boat/raft entity:
//   - Sneaking + right-clicking a `*_chest_boat` or any right-click on
//     `minecraft:chest_minecart` / `minecraft:hopper_minecart` opens its container menu.
//   - Right-clicking a `minecraft:furnace_minecart` while holding `minecraft:coal` or
//     `minecraft:charcoal` adds 3600 ticks of fuel and sets its push vector.
//   - Right-clicking a `minecraft:minecart`, `*_boat`, `*_raft`, or non-sneaking
//     `*_chest_boat` mounts the player onto the vehicle.
func (s *Server) InteractWithVehicle(session *PlayerSession, entityID int32) (handled bool, containerID int32, err error) {
	if s == nil || s.entities == nil || session == nil || session.IsDead() {
		return false, 0, nil
	}
	ent := s.entities.Get(entityID)
	if ent == nil || !IsVehicleEntity(ent.TypeName) {
		return false, 0, nil
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator || snap.Health <= 0 {
		return false, 0, nil
	}
	dx := ent.X - snap.X
	dy := ent.Y - snap.Y
	dz := ent.Z - snap.Z
	if (snap.X != 0 || snap.Y != 0 || snap.Z != 0) &&
		snap.GameMode != GameModeCreative &&
		(dx*dx+dy*dy+dz*dz) > 36.0 {
		return false, 0, nil
	}

	normType := canonicalResourceName(ent.TypeName)
	switch {
	case normType == EntityTypeChestMinecart || normType == EntityTypeHopperMinecart:
		cid, openErr := s.OpenVehicleContainer(session, entityID)
		return openErr == nil, cid, openErr

	case IsChestBoatEntity(normType):
		if session.IsSneaking() {
			cid, openErr := s.OpenVehicleContainer(session, entityID)
			return openErr == nil, cid, openErr
		}
		_, mounted := s.MountPlayerOnVehicle(session, entityID)
		return mounted, 0, nil

	case normType == EntityTypeFurnaceMinecart:
		fueled := s.RefuelFurnaceMinecart(session, entityID)
		return fueled, 0, nil

	case IsRideableVehicleEntity(normType):
		if session.IsSneaking() {
			return false, 0, nil
		}
		_, mounted := s.MountPlayerOnVehicle(session, entityID)
		return mounted, 0, nil
	}
	return false, 0, nil
}

// --- Packet Handlers (2: Player Input, Move Vehicle, Paddle Boat) ---

// PlayerVehicleInput returns the most recent PlayerInput received from session.
func (s *Server) PlayerVehicleInput(session *PlayerSession) PlayerInput {
	if s == nil || session == nil {
		return PlayerInput{}
	}
	mgr := s.vehicleManager()
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	return mgr.playerInputs[session]
}

// HandlePlayerInputPacket processes a ServerboundPlayerInputPacket (0x2b):
// updates the player's steering input and sneaking/sprinting state, and
// dismounts the player from any ridden vehicle when Shift (sneak) is pressed.
func (s *Server) HandlePlayerInputPacket(session *PlayerSession, r *protocol.Reader) error {
	in, err := DecodePlayerInputPacket(r)
	if err != nil {
		return err
	}
	if s == nil || session == nil {
		return nil
	}
	mgr := s.vehicleManager()
	mgr.mu.Lock()
	mgr.playerInputs[session] = in
	mgr.mu.Unlock()

	session.SetSneaking(in.Shift)
	session.SetSprinting(in.Sprint)

	if in.Shift {
		if s.PlayerMountedVehicle(session) != 0 || s.PlayerMountedCamel(session) != 0 || s.PlayerMountedEquine(session) != 0 {
			s.DismountPlayerVehicle(session)
		}
		if session.Snapshot().GameMode == GameModeSpectator && session.CameraEntityID() != session.EntityID {
			s.SetSpectatorCamera(session, session.EntityID)
		}
	}
	return nil
}

// HandlePaddleBoatPacket processes a ServerboundPaddleBoatPacket (0x23) from the
// driver of a boat/raft, updating LeftPaddle/RightPaddle and broadcasting
// synched entity data to nearby players.
func (s *Server) HandlePaddleBoatPacket(session *PlayerSession, r *protocol.Reader) error {
	left, right, err := DecodePaddleBoatPacket(r)
	if err != nil {
		return err
	}
	if s == nil || s.entities == nil || session == nil {
		return nil
	}
	vehID := s.PlayerMountedVehicle(session)
	if vehID <= 0 {
		return nil
	}
	ent := s.entities.Get(vehID)
	if ent == nil || !IsBoatEntity(ent.TypeName) {
		return nil
	}
	st := s.ensureVehicleState(vehID)
	if st == nil {
		return nil
	}
	mgr := s.vehicleManager()
	mgr.mu.Lock()
	pid := session.EntityID
	if pid == 0 {
		pid = -1
	}
	if len(st.Passengers) == 0 || st.Passengers[0] != pid {
		mgr.mu.Unlock()
		return nil
	}
	st.LeftPaddle = left
	st.RightPaddle = right
	bubble := st.BubbleTime
	mgr.mu.Unlock()

	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, protocol.PlaySetEntityData,
		EncodeBoatPaddleDataPacket(vehID, left, right, bubble))
	return nil
}

// HandleMoveVehiclePacket processes a ServerboundMoveVehiclePacket (0x22) from
// the controlling passenger of a boat, minecart, camel, or equine, validating movement,
// applying rail/water/ice/step-height constraints, syncing passenger coordinates,
// awarding distance stats, and broadcasting the vehicle's updated position.
func (s *Server) HandleMoveVehiclePacket(session *PlayerSession, r *protocol.Reader) error {
	nx, ny, nz, yaw, pitch, _, err := DecodeMoveVehiclePacket(r)
	if err != nil {
		return err
	}
	if s == nil || s.entities == nil || session == nil || session.IsDead() {
		return nil
	}
	if math.IsNaN(nx) || math.IsNaN(ny) || math.IsNaN(nz) ||
		math.IsInf(nx, 0) || math.IsInf(ny, 0) || math.IsInf(nz, 0) {
		return nil
	}
	vehID := s.PlayerMountedVehicle(session)
	if vehID <= 0 {
		if camelID := s.PlayerMountedCamel(session); camelID > 0 && s.CamelDriver(camelID) == session.EntityID {
			return s.handleMoveMountedCamel(session, camelID, nx, ny, nz, yaw, pitch)
		}
		if eqID := s.PlayerMountedEquine(session); eqID > 0 && s.EquineRider(eqID) == session.EntityID {
			return s.handleMoveMountedEquine(session, eqID, nx, ny, nz, yaw, pitch)
		}
		return nil
	}
	ent := s.entities.Get(vehID)
	if ent == nil {
		return nil
	}
	pid := session.EntityID
	if pid == 0 {
		pid = -1
	}
	if driver := s.VehicleDriverEntityID(vehID); driver != pid {
		return nil
	}

	oldX, oldY, oldZ := ent.X, ent.Y, ent.Z
	dx := nx - oldX
	dy := ny - oldY
	dz := nz - oldZ
	distSq := dx*dx + dy*dy + dz*dz

	// Anti-cheat rubberband if a single move_vehicle packet teleports > 10 blocks (100 sq).
	if distSq > 100.0 {
		if session.send != nil {
			_ = session.send(protocol.PlayMoveVehicle, EncodeMoveVehiclePacket(oldX, oldY, oldZ, ent.Yaw, ent.Pitch))
		}
		return nil
	}

	dim := ent.EffectiveDimension()
	if IsMinecartEntity(ent.TypeName) {
		if cache := s.DimensionChunks(dim); cache != nil {
			bx, by, bz := int(math.Floor(nx)), int(math.Floor(ny)), int(math.Floor(nz))
			if isRailBlock(cache.GetBlock(bx, by, bz)) {
				ny = railElevationAt(cache.GetBlock(bx, by, bz), nx, by, nz)
			} else if isRailBlock(cache.GetBlock(bx, by-1, bz)) {
				ny = railElevationAt(cache.GetBlock(bx, by-1, bz), nx, by-1, nz)
			}
		}
	} else if IsBoatEntity(ent.TypeName) {
		s.breakLilyPadsInBoatPath(dim, nx, ny, nz)
	}

	vx := nx - oldX
	vy := ny - oldY
	vz := nz - oldZ
	s.entities.Update(vehID, func(e *world.Entity) {
		e.X = nx
		e.Y = ny
		e.Z = nz
		e.Yaw = yaw
		e.Pitch = pitch
		e.HeadYaw = yaw
		e.VelocityX = clampVelocityShort(vx)
		e.VelocityY = clampVelocityShort(vy)
		e.VelocityZ = clampVelocityShort(vz)
	})

	s.syncVehiclePassengerPositions(vehID, nx, ny, nz)

	// Award distance statistics in centimeters.
	horizCm := int32(math.Round(math.Sqrt(dx*dx+dz*dz) * 100.0))
	if horizCm > 0 {
		if IsBoatEntity(ent.TypeName) {
			s.AwardCustomStat(session, StatBoatOneCm, horizCm)
		} else if IsMinecartEntity(ent.TypeName) {
			s.AwardCustomStat(session, StatMinecartOneCm, horizCm)
		}
	}

	if IsMinecartEntity(ent.TypeName) {
		s.evaluateMinecartRailTriggers(vehID)
	}

	if updated := s.entities.Get(vehID); updated != nil {
		cp := updated.ChunkPos()
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayTeleportEntity, encodeTeleportEntityPacket(nil, updated))
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayMoveVehicle, EncodeMoveVehiclePacket(nx, ny, nz, yaw, pitch))
	}
	return nil
}

func (s *Server) handleMoveMountedEquine(session *PlayerSession, eqID int32, nx, ny, nz float64, yaw, pitch float32) error {
	if s == nil || s.entities == nil || session == nil {
		return nil
	}
	ent := s.entities.Get(eqID)
	if ent == nil {
		return nil
	}
	oldX, oldY, oldZ := ent.X, ent.Y, ent.Z
	dx := nx - oldX
	dy := ny - oldY
	dz := nz - oldZ
	if dx*dx+dy*dy+dz*dz > 100.0 {
		if session.send != nil {
			_ = session.send(protocol.PlayMoveVehicle, EncodeMoveVehiclePacket(oldX, oldY, oldZ, ent.Yaw, ent.Pitch))
		}
		return nil
	}
	dim := ent.EffectiveDimension()
	vx, vy, vz := nx-oldX, ny-oldY, nz-oldZ
	s.entities.Update(eqID, func(e *world.Entity) {
		e.X = nx
		e.Y = ny
		e.Z = nz
		e.Yaw = yaw
		e.Pitch = pitch
		e.HeadYaw = yaw
		e.VelocityX = clampVelocityShort(vx)
		e.VelocityY = clampVelocityShort(vy)
		e.VelocityZ = clampVelocityShort(vz)
	})
	rs := session.Snapshot()
	s.SetPlayerTransform(session, nx, ny, nz, rs.Yaw, rs.Pitch, false)
	horizCm := int32(math.Round(math.Sqrt(dx*dx+dz*dz) * 100.0))
	if horizCm > 0 {
		s.AwardCustomStat(session, StatHorseOneCm, horizCm)
	}
	if updated := s.entities.Get(eqID); updated != nil {
		cp := updated.ChunkPos()
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayTeleportEntity, encodeTeleportEntityPacket(nil, updated))
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayMoveVehicle, EncodeMoveVehiclePacket(nx, ny, nz, yaw, pitch))
	}
	return nil
}

func (s *Server) handleMoveMountedCamel(session *PlayerSession, camelID int32, nx, ny, nz float64, yaw, pitch float32) error {
	if s == nil || s.entities == nil || session == nil {
		return nil
	}
	ent := s.entities.Get(camelID)
	if ent == nil {
		return nil
	}
	oldX, oldY, oldZ := ent.X, ent.Y, ent.Z
	dx := nx - oldX
	dy := ny - oldY
	dz := nz - oldZ
	if dx*dx+dy*dy+dz*dz > 100.0 {
		if session.send != nil {
			_ = session.send(protocol.PlayMoveVehicle, EncodeMoveVehiclePacket(oldX, oldY, oldZ, ent.Yaw, ent.Pitch))
		}
		return nil
	}
	dim := ent.EffectiveDimension()
	if cache := s.DimensionChunks(dim); cache != nil {
		curBX, curBZ := int(math.Floor(oldX)), int(math.Floor(oldZ))
		candBX, candBZ := int(math.Floor(nx)), int(math.Floor(nz))
		feetY := int(math.Floor(oldY + 0.01))
		if candBX != curBX || candBZ != curBZ {
			if stepY, ok := ResolveCamelStepMove(cache, curBX, feetY, curBZ, candBX, candBZ); ok {
				if float64(stepY) > ny {
					ny = float64(stepY)
				}
			}
		}
	}
	vx, vy, vz := nx-oldX, ny-oldY, nz-oldZ
	s.entities.Update(camelID, func(e *world.Entity) {
		e.X = nx
		e.Y = ny
		e.Z = nz
		e.Yaw = yaw
		e.Pitch = pitch
		e.HeadYaw = yaw
		e.VelocityX = clampVelocityShort(vx)
		e.VelocityY = clampVelocityShort(vy)
		e.VelocityZ = clampVelocityShort(vz)
	})
	for _, pid := range s.CamelPassengers(camelID) {
		if rider := s.playerByEntityID(pid); rider != nil {
			rs := rider.Snapshot()
			s.SetPlayerTransform(rider, nx, ny, nz, rs.Yaw, rs.Pitch, false)
		}
	}
	if updated := s.entities.Get(camelID); updated != nil {
		cp := updated.ChunkPos()
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayTeleportEntity, encodeTeleportEntityPacket(nil, updated))
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayMoveVehicle, EncodeMoveVehiclePacket(nx, ny, nz, yaw, pitch))
	}
	return nil
}

func (s *Server) syncVehiclePassengerPositions(vehicleID int32, vx, vy, vz float64) {
	mgr := s.vehicleManager()
	mgr.mu.Lock()
	st := mgr.vehicles[vehicleID]
	var typeName string
	var passengers []int32
	var sessions []*PlayerSession
	if st != nil {
		typeName = st.TypeName
		passengers = make([]int32, len(st.Passengers))
		copy(passengers, st.Passengers)
		for sess, vid := range mgr.byPassengerPlayer {
			if vid == vehicleID {
				sessions = append(sessions, sess)
			}
		}
	}
	mgr.mu.Unlock()

	for _, sess := range sessions {
		ps := sess.Snapshot()
		horizCm := int32(math.Round(math.Hypot(vx-ps.X, vz-ps.Z) * 100.0))
		s.SetPlayerTransform(sess, vx, vy, vz, ps.Yaw, ps.Pitch, false)
		sess.SetFallDistance(0)
		if horizCm > 0 {
			if IsBoatEntity(typeName) {
				s.AwardCustomStat(sess, StatBoatOneCm, horizCm)
			} else if IsMinecartEntity(typeName) {
				s.AwardCustomStat(sess, StatMinecartOneCm, horizCm)
			}
		}
	}
	if s.entities != nil {
		for _, pid := range passengers {
			if pid > 0 {
				s.entities.Update(pid, func(e *world.Entity) {
					e.X = vx
					e.Y = vy
					e.Z = vz
				})
			}
		}
	}
}

// --- Minecart Rail Physics & Variants (3 & 4) ---

// ResolveRailPlacementAt computes the placed rail block state (shape, curve,
// ascending slope, and initial redstone powered state) at (x, y, z).
func ResolveRailPlacementAt(cache *world.Cache, x, y, z int, baseState uint16, playerHorizFacing string) uint16 {
	if !isRailBlock(baseState) {
		return baseState
	}
	name := world.BlockName(baseState)
	shape := "north_south"
	if playerHorizFacing == "east" || playerHorizFacing == "west" {
		shape = "east_west"
	}

	if cache != nil {
		hasRailAt := func(rx, ry, rz int) bool {
			if ry >= world.MinY && ry < world.MinY+world.WorldHeight && isRailBlock(cache.GetBlock(rx, ry, rz)) {
				return true
			}
			if ry+1 < world.MinY+world.WorldHeight && isRailBlock(cache.GetBlock(rx, ry+1, rz)) {
				return true
			}
			if ry-1 >= world.MinY && isRailBlock(cache.GetBlock(rx, ry-1, rz)) {
				return true
			}
			return false
		}
		hasN := hasRailAt(x, y, z-1)
		hasS := hasRailAt(x, y, z+1)
		hasW := hasRailAt(x-1, y, z)
		hasE := hasRailAt(x+1, y, z)

		if (hasN || hasS) && !hasW && !hasE {
			shape = "north_south"
		} else if (hasW || hasE) && !hasN && !hasS {
			shape = "east_west"
		} else if name == "minecraft:rail" {
			switch {
			case hasS && hasE && !hasN && !hasW:
				shape = "south_east"
			case hasS && hasW && !hasN && !hasE:
				shape = "south_west"
			case hasN && hasW && !hasS && !hasE:
				shape = "north_west"
			case hasN && hasE && !hasS && !hasW:
				shape = "north_east"
			}
		}

		if y+1 < world.MinY+world.WorldHeight {
			switch shape {
			case "north_south":
				if isRailBlock(cache.GetBlock(x, y+1, z-1)) {
					shape = "ascending_north"
				} else if isRailBlock(cache.GetBlock(x, y+1, z+1)) {
					shape = "ascending_south"
				}
			case "east_west":
				if isRailBlock(cache.GetBlock(x+1, y+1, z)) {
					shape = "ascending_east"
				} else if isRailBlock(cache.GetBlock(x-1, y+1, z)) {
					shape = "ascending_west"
				}
			}
		}
	}

	out := baseState
	if st, ok := world.WithBlockProperty(out, "shape", shape); ok {
		out = st
	}
	if cache != nil && (name == "minecraft:powered_rail" || name == "minecraft:activator_rail") {
		if cache.IsBlockPowered(x, y, z) {
			if st, ok := world.WithBlockProperty(out, "powered", "true"); ok {
				out = st
			}
		}
	}
	return out
}

func railShapeProperty(state uint16) string {
	_, props, ok := world.BlockStateProperties(state)
	if !ok {
		return "north_south"
	}
	if sh := props["shape"]; sh != "" {
		return sh
	}
	return "north_south"
}

func railPoweredProperty(state uint16) bool {
	_, props, ok := world.BlockStateProperties(state)
	return ok && props["powered"] == "true"
}

func railElevationAt(railState uint16, x float64, railY int, z float64) float64 {
	baseY := float64(railY) + 0.0625
	shape := railShapeProperty(railState)
	fracX := x - math.Floor(x)
	fracZ := z - math.Floor(z)
	switch shape {
	case "ascending_east":
		return baseY + math.Max(0, math.Min(1, fracX))
	case "ascending_west":
		return baseY + math.Max(0, math.Min(1, 1.0-fracX))
	case "ascending_north":
		return baseY + math.Max(0, math.Min(1, 1.0-fracZ))
	case "ascending_south":
		return baseY + math.Max(0, math.Min(1, fracZ))
	default:
		return baseY
	}
}

// RefuelFurnaceMinecart adds 3600 ticks of fuel to a `minecraft:furnace_minecart`
// when right-clicked with `minecraft:coal` or `minecraft:charcoal`, setting its
// push direction along session's horizontal look vector.
func (s *Server) RefuelFurnaceMinecart(session *PlayerSession, entityID int32) bool {
	if s == nil || s.entities == nil || session == nil || session.IsDead() {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != EntityTypeFurnaceMinecart {
		return false
	}
	held := session.HeldItem()
	if held.IsEmpty() {
		return false
	}
	itemID := canonicalResourceName(held.ResolvedID())
	if itemID != "minecraft:coal" && itemID != "minecraft:charcoal" {
		return false
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator {
		return false
	}
	if snap.GameMode != GameModeCreative {
		if _, _, ok := s.ConsumeHeldItem(session, 1); !ok {
			return false
		}
	}

	st := s.ensureVehicleState(entityID)
	if st == nil {
		return false
	}
	lx, _, lz := ElytraLookVector(snap.Yaw, 0)
	horizLen := math.Hypot(lx, lz)
	if horizLen > 1e-6 {
		lx /= horizLen
		lz /= horizLen
	}

	mgr := s.vehicleManager()
	mgr.mu.Lock()
	if st.Fuel+FurnaceMinecartFuelPerCoal <= FurnaceMinecartMaxFuel {
		st.Fuel += FurnaceMinecartFuelPerCoal
	} else {
		st.Fuel = FurnaceMinecartMaxFuel
	}
	st.PushX = lx
	st.PushZ = lz
	lit := st.Fuel > 0
	mgr.mu.Unlock()

	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, protocol.PlaySetEntityData,
		EncodeFurnaceMinecartDataPacket(entityID, lit))
	return true
}

// PrimeTNTMinecart ignites a `minecraft:tnt_minecart` with the specified fuse
// duration in ticks (default 80 ticks = 4.0s).
func (s *Server) PrimeTNTMinecart(entityID int32, fuseTicks int) bool {
	if s == nil || s.entities == nil || entityID <= 0 {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != EntityTypeTNTMinecart {
		return false
	}
	if fuseTicks <= 0 {
		fuseTicks = TNTMinecartDefaultFuseTicks
	}
	st := s.ensureVehicleState(entityID)
	if st == nil {
		return false
	}
	mgr := s.vehicleManager()
	mgr.mu.Lock()
	if st.FuseTicks >= 0 {
		mgr.mu.Unlock()
		return false
	}
	st.FuseTicks = fuseTicks
	mgr.mu.Unlock()

	cp := ent.ChunkPos()
	dim := ent.EffectiveDimension()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket("minecraft:entity.tnt.primed", SoundSourceBlocks, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
	s.EmitVibration(VibrationEvent{
		Dimension:      dim,
		Type:           GameEventPrimeFuse,
		X:              ent.X,
		Y:              ent.Y,
		Z:              ent.Z,
		SourceEntityID: entityID,
	})
	return true
}

// DetonateTNTMinecart explodes a `minecraft:tnt_minecart`, damaging nearby
// players, mobs, and decorations, destroying nearby non-rail blocks (when
// griefing is enabled), removing the minecart entity, and emitting GameEventExplode.
func (s *Server) DetonateTNTMinecart(entityID int32) (playersDamaged, blocksDestroyed int) {
	if s == nil || s.entities == nil || entityID <= 0 {
		return 0, 0
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != EntityTypeTNTMinecart {
		return 0, 0
	}
	dim := ent.EffectiveDimension()
	ex, ey, ez := ent.X, ent.Y, ent.Z
	cp := ent.ChunkPos()

	s.RemoveVehicle(entityID)

	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket("minecraft:entity.generic.explode", SoundSourceBlocks, ex, ey, ez, 4.0, 0.8, 0))
	s.DamageDecorationsInRadius(dim, ex, ey, ez, TNTMinecartExplosionRadius*2.0, 28.0, "explosion")
	s.EmitVibration(VibrationEvent{
		Dimension:      dim,
		Type:           GameEventExplode,
		X:              ex,
		Y:              ey,
		Z:              ez,
		SourceEntityID: entityID,
	})

	// Damage nearby players and mobs within radius 8.0.
	maxDist := TNTMinecartExplosionRadius * 2.0
	for _, p := range s.onlinePlayersSnapshot() {
		if p == nil || p.IsDead() {
			continue
		}
		ps := p.Snapshot()
		if world.NormalizeDimension(ps.Dimension) != dim || ps.GameMode == GameModeSpectator || ps.GameMode == GameModeCreative {
			continue
		}
		dist := math.Sqrt(sqFloat(ps.X-ex) + sqFloat(ps.Y-ey) + sqFloat(ps.Z-ez))
		if dist > maxDist {
			continue
		}
		impact := 1.0 - (dist / maxDist)
		dmg := float32((impact*impact+impact)*0.5*7.0*maxDist + 1.0)
		if s.DamagePlayerWithArmor(p, dmg, "explosion") > 0 {
			playersDamaged++
		}
	}

	nearby := s.entities.AppendInChunkRange(nil, cp.X, cp.Z, 1)
	for i := range nearby {
		other := &nearby[i]
		if other.ID == entityID || other.IsItem() || other.IsExperienceOrb() ||
			other.EffectiveDimension() != dim || IsDecorationEntity(other.TypeName) {
			continue
		}
		dist := math.Sqrt(sqFloat(other.X-ex) + sqFloat(other.Y-ey) + sqFloat(other.Z-ez))
		if dist > maxDist {
			continue
		}
		impact := 1.0 - (dist / maxDist)
		dmg := float32((impact*impact+impact)*0.5*7.0*maxDist + 1.0)
		if IsVehicleEntity(other.TypeName) {
			s.DamageVehicle(other.ID, dmg*10.0, nil)
			continue
		}
		s.applyDamageToMob(other, dmg, nil, 0.4, false, false)
	}

	// Destroy nearby blocks (Vanilla: TNT minecart explosions do NOT destroy rails or blocks directly under rails).
	if cache := s.DimensionChunks(dim); cache != nil && s.CreeperBlockGriefing() {
		bx0, by0, bz0 := int(math.Floor(ex)), int(math.Floor(ey)), int(math.Floor(ez))
		var changes []world.BlockChange
		var lightChunks []world.ChunkPos
		seenLight := make(map[world.ChunkPos]bool)
		for dy := -2; dy <= 2; dy++ {
			ty := by0 + dy
			if ty <= world.MinY || ty >= world.MinY+world.WorldHeight-1 {
				continue
			}
			for dx := -2; dx <= 2; dx++ {
				for dz := -2; dz <= 2; dz++ {
					if dx*dx+dy*dy+dz*dz > 6 {
						continue
					}
					tx, tz := bx0+dx, bz0+dz
					st := cache.GetBlock(tx, ty, tz)
					if st == world.StateAir || st == world.StateBedrock || st == world.StateWater || st == world.StateLava ||
						isRailBlock(st) || isRailBlock(cache.GetBlock(tx, ty+1, tz)) {
						continue
					}
					name := world.BlockName(st)
					if name == "minecraft:obsidian" || name == "minecraft:crying_obsidian" ||
						name == "minecraft:end_portal_frame" || name == "minecraft:end_portal" ||
						name == "minecraft:nether_portal" || name == "minecraft:barrier" ||
						name == "minecraft:ancient_debris" {
						continue
					}
					if valid, lc := cache.SetBlockWithLight(tx, ty, tz, world.StateAir); valid {
						blocksDestroyed++
						s.OnWorkstationBlockBroken(dim, tx, ty, tz)
						changes = append(changes, world.BlockChange{
							Pos:      world.BlockPos{X: tx, Y: ty, Z: tz},
							OldState: st,
							NewState: world.StateAir,
						})
						for _, lcp := range lc {
							if !seenLight[lcp] {
								seenLight[lcp] = true
								lightChunks = append(lightChunks, lcp)
							}
						}
					}
				}
			}
		}
		if len(changes) > 0 || len(lightChunks) > 0 {
			s.broadcastDimensionBlockChanges(dim, cache, changes, lightChunks)
		}
	}
	return playersDamaged, blocksDestroyed
}

// IsHopperMinecartEnabled reports whether a `minecraft:hopper_minecart` is
// currently unlocked/enabled (not disabled by a powered activator_rail).
func (s *Server) IsHopperMinecartEnabled(entityID int32) bool {
	st, ok := s.VehicleStateByID(entityID)
	return ok && st.HopperEnabled
}

// SetHopperMinecartEnabled sets whether a `minecraft:hopper_minecart` is enabled.
func (s *Server) SetHopperMinecartEnabled(entityID int32, enabled bool) bool {
	st := s.ensureVehicleState(entityID)
	if st == nil {
		return false
	}
	mgr := s.vehicleManager()
	mgr.mu.Lock()
	st.HopperEnabled = enabled
	mgr.mu.Unlock()
	return true
}

func (s *Server) isVehicleOnPoweredActivatorRail(ent *world.Entity) bool {
	if s == nil || ent == nil {
		return false
	}
	cache := s.DimensionChunks(ent.EffectiveDimension())
	if cache == nil {
		return false
	}
	bx, by, bz := int(math.Floor(ent.X)), int(math.Floor(ent.Y)), int(math.Floor(ent.Z))
	st := cache.GetBlock(bx, by, bz)
	if !isRailBlock(st) && by > world.MinY {
		st = cache.GetBlock(bx, by-1, bz)
	}
	return world.BlockName(st) == "minecraft:activator_rail" && railPoweredProperty(st)
}

func (s *Server) evaluateMinecartRailTriggers(entityID int32) {
	if s == nil || s.entities == nil {
		return
	}
	ent := s.entities.Get(entityID)
	if ent == nil || !IsMinecartEntity(ent.TypeName) {
		return
	}
	dim := ent.EffectiveDimension()
	cache := s.DimensionChunks(dim)
	if cache == nil {
		return
	}
	bx, by, bz := int(math.Floor(ent.X)), int(math.Floor(ent.Y)), int(math.Floor(ent.Z))
	railState := cache.GetBlock(bx, by, bz)
	if !isRailBlock(railState) && by > world.MinY {
		if below := cache.GetBlock(bx, by-1, bz); isRailBlock(below) {
			by--
			railState = below
		}
	}
	if !isRailBlock(railState) {
		return
	}
	railName := world.BlockName(railState)
	powered := railPoweredProperty(railState)
	normType := canonicalResourceName(ent.TypeName)

	switch railName {
	case "minecraft:detector_rail":
		s.activateDetectorRailAt(dim, cache, bx, by, bz, railState)

	case "minecraft:activator_rail":
		switch normType {
		case EntityTypeMinecart:
			if powered {
				s.DismountAllVehiclePassengers(entityID)
			}
		case EntityTypeTNTMinecart:
			if powered {
				s.PrimeTNTMinecart(entityID, TNTMinecartRailPrimeTicks)
			}
		case EntityTypeHopperMinecart:
			s.SetHopperMinecartEnabled(entityID, !powered)
		}
	}
}

func (s *Server) activateDetectorRailAt(dim string, cache *world.Cache, x, y, z int, railState uint16) {
	key := dimBlockKey{dim: dim, pos: world.BlockPos{X: x, Y: y, Z: z}}
	mgr := s.vehicleManager()
	mgr.mu.Lock()
	mgr.activeDetectorRails[key] = DetectorRailCheckIntervalTicks
	hook := mgr.detectorRailHook
	mgr.mu.Unlock()

	if !railPoweredProperty(railState) {
		_, props, ok := world.BlockStateProperties(railState)
		if !ok {
			props = map[string]string{"shape": "north_south"}
		}
		cpProps := make(map[string]string, len(props)+1)
		for k, v := range props {
			cpProps[k] = v
		}
		cpProps["powered"] = "true"
		if nextState, ok := world.BlockStateByName("minecraft:detector_rail", cpProps); ok {
			s.BindNoteBlockAndJukeboxRedstone(dim)
			if valid, lc := cache.SetBlockWithLight(x, y, z, nextState); valid {
				s.broadcastDimensionBlockChanges(dim, cache, []world.BlockChange{
					{Pos: world.BlockPos{X: x, Y: y, Z: z}, OldState: railState, NewState: nextState},
				}, lc)
				if nbRes := cache.UpdateNeighbors(x, y, z); len(nbRes.Changes) > 0 || len(nbRes.LightChanged) > 0 {
					s.broadcastDimensionBlockChanges(dim, cache, nbRes.Changes, nbRes.LightChanged)
				}
				if y > world.MinY {
					if nbBelow := cache.UpdateNeighbors(x, y-1, z); len(nbBelow.Changes) > 0 || len(nbBelow.LightChanged) > 0 {
						s.broadcastDimensionBlockChanges(dim, cache, nbBelow.Changes, nbBelow.LightChanged)
					}
				}
				if compRes := s.UpdateComparatorsAt(dim, x, y, z); len(compRes.Changes) > 0 || len(compRes.LightChanged) > 0 {
					s.broadcastDimensionBlockChanges(dim, cache, compRes.Changes, compRes.LightChanged)
				}
				if hook != nil {
					hook(s, dim, x, y, z, true)
				}
			}
		}
	}
}

func (s *Server) tickDetectorRails() {
	mgr := s.vehicleManager()
	mgr.mu.Lock()
	if len(mgr.activeDetectorRails) == 0 {
		mgr.mu.Unlock()
		return
	}
	var toCheck []dimBlockKey
	for k, rem := range mgr.activeDetectorRails {
		rem--
		if rem <= 0 {
			toCheck = append(toCheck, k)
		} else {
			mgr.activeDetectorRails[k] = rem
		}
	}
	hook := mgr.detectorRailHook
	mgr.mu.Unlock()

	for _, key := range toCheck {
		cache := s.DimensionChunks(key.dim)
		if cache == nil {
			mgr.mu.Lock()
			delete(mgr.activeDetectorRails, key)
			mgr.mu.Unlock()
			continue
		}
		curState := cache.GetBlock(key.pos.X, key.pos.Y, key.pos.Z)
		if world.BlockName(curState) != "minecraft:detector_rail" {
			mgr.mu.Lock()
			delete(mgr.activeDetectorRails, key)
			mgr.mu.Unlock()
			continue
		}
		if s.hasMinecartOnRailAt(key.dim, key.pos.X, key.pos.Y, key.pos.Z) {
			mgr.mu.Lock()
			mgr.activeDetectorRails[key] = DetectorRailCheckIntervalTicks
			mgr.mu.Unlock()
			continue
		}
		mgr.mu.Lock()
		delete(mgr.activeDetectorRails, key)
		mgr.mu.Unlock()

		if railPoweredProperty(curState) {
			_, props, _ := world.BlockStateProperties(curState)
			cpProps := make(map[string]string, len(props)+1)
			for k, v := range props {
				cpProps[k] = v
			}
			cpProps["powered"] = "false"
			if nextState, ok := world.BlockStateByName("minecraft:detector_rail", cpProps); ok {
				s.BindNoteBlockAndJukeboxRedstone(key.dim)
				if valid, lc := cache.SetBlockWithLight(key.pos.X, key.pos.Y, key.pos.Z, nextState); valid {
					s.broadcastDimensionBlockChanges(key.dim, cache, []world.BlockChange{
						{Pos: key.pos, OldState: curState, NewState: nextState},
					}, lc)
					if nbRes := cache.UpdateNeighbors(key.pos.X, key.pos.Y, key.pos.Z); len(nbRes.Changes) > 0 || len(nbRes.LightChanged) > 0 {
						s.broadcastDimensionBlockChanges(key.dim, cache, nbRes.Changes, nbRes.LightChanged)
					}
					if key.pos.Y > world.MinY {
						if nbBelow := cache.UpdateNeighbors(key.pos.X, key.pos.Y-1, key.pos.Z); len(nbBelow.Changes) > 0 || len(nbBelow.LightChanged) > 0 {
							s.broadcastDimensionBlockChanges(key.dim, cache, nbBelow.Changes, nbBelow.LightChanged)
						}
					}
					if compRes := s.UpdateComparatorsAt(key.dim, key.pos.X, key.pos.Y, key.pos.Z); len(compRes.Changes) > 0 || len(compRes.LightChanged) > 0 {
						s.broadcastDimensionBlockChanges(key.dim, cache, compRes.Changes, compRes.LightChanged)
					}
					if hook != nil {
						hook(s, key.dim, key.pos.X, key.pos.Y, key.pos.Z, false)
					}
				}
			}
		}
	}
}

func (s *Server) refreshVehicleDetectorRailComparators(entityID int32) {
	if s == nil || s.entities == nil || entityID <= 0 {
		return
	}
	ent := s.entities.Get(entityID)
	if ent == nil || !IsMinecartEntity(ent.TypeName) {
		return
	}
	dim := ent.EffectiveDimension()
	cache := s.DimensionChunks(dim)
	if cache == nil {
		return
	}
	bx, by, bz := int(math.Floor(ent.X)), int(math.Floor(ent.Y)), int(math.Floor(ent.Z))
	if world.BlockName(cache.GetBlock(bx, by, bz)) != "minecraft:detector_rail" {
		if by > world.MinY && world.BlockName(cache.GetBlock(bx, by-1, bz)) == "minecraft:detector_rail" {
			by--
		} else {
			return
		}
	}
	if compRes := s.UpdateComparatorsAt(dim, bx, by, bz); len(compRes.Changes) > 0 || len(compRes.LightChanged) > 0 {
		s.broadcastDimensionBlockChanges(dim, cache, compRes.Changes, compRes.LightChanged)
	}
}

func (s *Server) hasMinecartOnRailAt(dim string, rx, ry, rz int) bool {
	if s == nil || s.entities == nil {
		return false
	}
	normDim := world.NormalizeDimension(dim)
	cx, cz := int32(rx>>4), int32(rz>>4)
	nearby := s.entities.AppendInChunkRange(nil, cx, cz, 1)
	for i := range nearby {
		ent := &nearby[i]
		if !IsMinecartEntity(ent.TypeName) || ent.EffectiveDimension() != normDim {
			continue
		}
		bx, by, bz := int(math.Floor(ent.X)), int(math.Floor(ent.Y)), int(math.Floor(ent.Z))
		if bx == rx && bz == rz && (by == ry || by == ry+1) {
			return true
		}
	}
	return false
}

// DetectorRailComparatorOutput computes the redstone comparator signal strength
// (0..15) for a `minecraft:detector_rail` at (dim, x, y, z):
//   - If a `minecraft:chest_minecart` or `minecraft:hopper_minecart` is on the rail,
//     returns its container fullness signal (0..15).
//   - If a `minecraft:minecart` with a passenger is on the rail, returns 15.
func (s *Server) DetectorRailComparatorOutput(dim string, x, y, z int) int {
	if s == nil || s.entities == nil {
		return 0
	}
	normDim := world.NormalizeDimension(dim)
	cx, cz := int32(x>>4), int32(z>>4)
	nearby := s.entities.AppendInChunkRange(nil, cx, cz, 1)
	for i := range nearby {
		ent := &nearby[i]
		if !IsMinecartEntity(ent.TypeName) || ent.EffectiveDimension() != normDim {
			continue
		}
		bx, by, bz := int(math.Floor(ent.X)), int(math.Floor(ent.Y)), int(math.Floor(ent.Z))
		if bx != x || bz != z || (by != y && by != y+1) {
			continue
		}
		st, ok := s.VehicleStateByID(ent.ID)
		if !ok {
			continue
		}
		switch st.TypeName {
		case EntityTypeChestMinecart:
			return computeSlotsComparatorSignal(st.Items[:VehicleChestSlotCount])
		case EntityTypeHopperMinecart:
			return computeSlotsComparatorSignal(st.Items[:HopperMinecartSlotCount])
		case EntityTypeMinecart:
			if len(st.Passengers) > 0 {
				return 15
			}
		}
	}
	return 0
}

func computeSlotsComparatorSignal(slots []world.ItemStack) int {
	if len(slots) == 0 {
		return 0
	}
	var sumFrac float64
	nonEmpty := 0
	for _, st := range slots {
		if st.IsEmpty() || st.Count <= 0 {
			continue
		}
		nonEmpty++
		maxStack := float64(MaxItemStackSize)
		if IsDamageableItem(st.ResolvedID()) || st.ResolvedID() == "minecraft:potion" ||
			st.ResolvedID() == "minecraft:splash_potion" || st.ResolvedID() == "minecraft:lingering_potion" {
			maxStack = 1.0
		}
		frac := float64(st.Count) / maxStack
		if frac > 1.0 {
			frac = 1.0
		}
		sumFrac += frac
	}
	if nonEmpty == 0 {
		return 0
	}
	avg := sumFrac / float64(len(slots))
	return int(math.Floor(avg*14.0)) + 1
}

// --- Boat Surface Medium & Physics Helpers (5) ---

// ClassifyBoatMedium determines the surface medium (water, underwater, ice,
// blue ice, land, or air) for a boat at (x, y, z) in dim.
func (s *Server) ClassifyBoatMedium(dim string, x, y, z float64) BoatMedium {
	if s == nil {
		return BoatMediumInAir
	}
	cache := s.DimensionChunks(world.NormalizeDimension(dim))
	if cache == nil {
		return BoatMediumInAir
	}
	bx := int(math.Floor(x))
	by := int(math.Floor(y))
	bz := int(math.Floor(z))

	curState := cache.GetBlock(bx, by, bz)
	curName := world.BlockName(curState)
	aboveState := cache.GetBlock(bx, by+1, bz)
	aboveName := world.BlockName(aboveState)

	if (curName == "minecraft:water" || curName == "minecraft:bubble_column") &&
		(aboveName == "minecraft:water" || aboveName == "minecraft:bubble_column") {
		return BoatMediumUnderWater
	}
	if curName == "minecraft:water" || curName == "minecraft:bubble_column" {
		return BoatMediumInWater
	}
	belowState := cache.GetBlock(bx, int(math.Floor(y-0.05)), bz)
	belowName := world.BlockName(belowState)
	if belowName == "minecraft:water" || belowName == "minecraft:bubble_column" {
		return BoatMediumInWater
	}
	switch belowName {
	case "minecraft:blue_ice":
		return BoatMediumOnBlueIce
	case "minecraft:ice", "minecraft:packed_ice", "minecraft:frosted_ice":
		return BoatMediumOnIce
	}
	if !isPassableBlock(belowState) {
		return BoatMediumOnLand
	}
	return BoatMediumInAir
}

// BoatFrictionForMedium returns the horizontal velocity retention factor per
// tick for a boat in medium (`0.9` in water, `0.98` on ice, `0.989` on blue ice,
// `0.6` on land, `0.9` in air).
func BoatFrictionForMedium(medium BoatMedium) float64 {
	switch medium {
	case BoatMediumOnBlueIce:
		return BoatFrictionBlueIce
	case BoatMediumOnIce:
		return BoatFrictionIce
	case BoatMediumInWater, BoatMediumUnderWater:
		return BoatFrictionWater
	case BoatMediumOnLand:
		return BoatFrictionLand
	default:
		return BoatFrictionAir
	}
}

func (s *Server) breakLilyPadsInBoatPath(dim string, x, y, z float64) {
	cache := s.DimensionChunks(dim)
	if cache == nil {
		return
	}
	by := int(math.Floor(y))
	for dy := 0; dy <= 1; dy++ {
		for dx := -1; dx <= 1; dx++ {
			for dz := -1; dz <= 1; dz++ {
				bx := int(math.Floor(x + float64(dx)*0.6))
				bz := int(math.Floor(z + float64(dz)*0.6))
				st := cache.GetBlock(bx, by+dy, bz)
				if world.BlockName(st) == "minecraft:lily_pad" {
					if broken, upd := cache.BreakBlockWithUpdates(bx, by+dy, bz); broken {
						s.broadcastDimensionBlockChanges(dim, cache, upd.Changes, upd.LightChanged)
						if s.DoTileDrops() {
							s.SpawnItemEntityInDimension(dim, float64(bx)+0.5, float64(by+dy)+0.25, float64(bz)+0.5,
								world.NewNamedItemStack("minecraft:lily_pad", 1))
						}
					}
				}
			}
		}
	}
}

// --- Per-Tick Vehicle Physics (TickVehicles) ---

// TickVehicles advances all active minecart and boat/raft entities by one server tick:
//   - Decays vehicle wobble damage and hurt timers
//   - Steps TNT minecart fuse timers and triggers detonation at 0
//   - Steps furnace minecart fuel and self-propulsion along rails
//   - Steps hopper minecart item pickup and invokes HopperMinecartTransferHook
//   - Steps minecart rail physics (powered rail boost/brake, slopes, curves, detector/activator rails)
//   - Steps boat water buoyancy, ice/land friction, and driver steering inputs
//   - Updates active detector rails
func (s *Server) TickVehicles() {
	if s == nil || s.entities == nil {
		return
	}
	mgr := s.vehicleManager()
	mgr.mu.Lock()
	if len(mgr.vehicles) == 0 && len(mgr.activeDetectorRails) == 0 {
		mgr.mu.Unlock()
		return
	}
	ids := make([]int32, 0, len(mgr.vehicles))
	for id := range mgr.vehicles {
		ids = append(ids, id)
	}
	mgr.mu.Unlock()

	for _, id := range ids {
		s.tickSingleVehicle(id)
	}
	s.tickDetectorRails()
}

func (s *Server) tickSingleVehicle(entityID int32) {
	ent := s.entities.Get(entityID)
	if ent == nil || !IsVehicleEntity(ent.TypeName) {
		s.RemoveVehicle(entityID)
		return
	}

	mgr := s.vehicleManager()
	mgr.mu.Lock()
	st, ok := mgr.vehicles[entityID]
	if !ok || st == nil {
		mgr.mu.Unlock()
		return
	}

	// 1. Decay hurt timer and wobble damage.
	metaChanged := false
	if st.HurtTime > 0 {
		st.HurtTime--
		metaChanged = true
	}
	if st.Damage > 0 {
		st.Damage -= VehicleDamageDecayPerTick
		if st.Damage < 0 {
			st.Damage = 0
		}
		metaChanged = true
	}
	hurtTime, hurtDir, dmg := st.HurtTime, st.HurtDir, st.Damage

	// 2. Check TNT minecart fuse countdown.
	if st.TypeName == EntityTypeTNTMinecart && st.FuseTicks >= 0 {
		st.FuseTicks--
		if st.FuseTicks <= 0 {
			mgr.mu.Unlock()
			s.DetonateTNTMinecart(entityID)
			return
		}
	}
	mgr.mu.Unlock()

	dim := ent.EffectiveDimension()
	cp := ent.ChunkPos()
	if metaChanged {
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlaySetEntityData,
			EncodeVehicleEntityDataPacket(entityID, hurtTime, hurtDir, dmg))
	}

	if IsMinecartEntity(ent.TypeName) {
		s.tickSingleMinecart(entityID, ent)
	} else if IsBoatEntity(ent.TypeName) {
		s.tickSingleBoat(entityID, ent)
	}
}

func (s *Server) tickSingleMinecart(entityID int32, ent *world.Entity) {
	dim := ent.EffectiveDimension()
	cache := s.DimensionChunks(dim)
	vx := float64(ent.VelocityX) / 8000.0
	vy := float64(ent.VelocityY) / 8000.0
	vz := float64(ent.VelocityZ) / 8000.0

	mgr := s.vehicleManager()
	mgr.mu.Lock()
	st := mgr.vehicles[entityID]
	if st == nil {
		mgr.mu.Unlock()
		return
	}
	typeName := st.TypeName
	hasPassengers := len(st.Passengers) > 0
	var driverInput PlayerInput
	var hasDriverInput bool
	if hasPassengers {
		driverID := st.Passengers[0]
		for sess, vid := range mgr.byPassengerPlayer {
			if vid == entityID && (sess.EntityID == driverID || (sess.EntityID == 0 && driverID == -1)) {
				driverInput = mgr.playerInputs[sess]
				hasDriverInput = true
				break
			}
		}
	}

	// Furnace minecart fuel tick.
	var furnaceLitChanged bool
	var furnaceNowLit bool
	var pushX, pushZ float64
	if typeName == EntityTypeFurnaceMinecart && st.Fuel > 0 {
		st.Fuel--
		pushX, pushZ = st.PushX, st.PushZ
		if st.Fuel == 0 {
			st.PushX, st.PushZ = 0, 0
			furnaceLitChanged = true
			furnaceNowLit = false
		}
	}
	hopperEnabled := st.HopperEnabled
	hopperHook := mgr.hopperHook
	mgr.mu.Unlock()

	if furnaceLitChanged {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlaySetEntityData,
			EncodeFurnaceMinecartDataPacket(entityID, furnaceNowLit))
	}

	// Check if on a rail block.
	bx, by, bz := int(math.Floor(ent.X)), int(math.Floor(ent.Y)), int(math.Floor(ent.Z))
	var railState uint16
	onRail := false
	if cache != nil {
		cand := cache.GetBlock(bx, by, bz)
		if isRailBlock(cand) {
			railState = cand
			onRail = true
		} else if by > world.MinY {
			below := cache.GetBlock(bx, by-1, bz)
			if isRailBlock(below) {
				by--
				railState = below
				onRail = true
			}
		}
	}

	nextX, nextY, nextZ := ent.X, ent.Y, ent.Z
	nextYaw := ent.Yaw
	onGround := ent.OnGround

	if onRail {
		shape := railShapeProperty(railState)
		railName := world.BlockName(railState)
		powered := railPoweredProperty(railState)

		// 1. Slope gravity on ascending rails.
		slopeAccel := MinecartSlopeGravity
		switch shape {
		case "ascending_east":
			vx -= slopeAccel
		case "ascending_west":
			vx += slopeAccel
		case "ascending_north":
			vz += slopeAccel
		case "ascending_south":
			vz -= slopeAccel
		}

		// 2. Player passenger forward input nudge on rails.
		if hasDriverInput && driverInput.Forward {
			rad := float64(ent.Yaw) * math.Pi / 180.0
			fx := -math.Sin(rad)
			fz := math.Cos(rad)
			if vx*vx+vz*vz < 0.01 {
				vx += fx * 0.01
				vz += fz * 0.01
			}
		}

		// 3. Furnace minecart self-propulsion.
		if typeName == EntityTypeFurnaceMinecart && (pushX != 0 || pushZ != 0) {
			vx += pushX * FurnaceMinecartPushAccel
			vz += pushZ * FurnaceMinecartPushAccel
		}

		// 4. Curve redirection and axis alignment.
		vx, vz = alignVelocityAlongRailShape(shape, vx, vz, ent.X, ent.Z, bx, bz)

		// 5. Powered rail boost or brake.
		if railName == "minecraft:powered_rail" {
			if powered {
				speed := math.Hypot(vx, vz)
				if speed > 0.01 {
					vx += (vx / speed) * MinecartPoweredRailBoost
					vz += (vz / speed) * MinecartPoweredRailBoost
				} else if cache != nil {
					// Launch away from adjacent solid block along the rail axis.
					switch shape {
					case "east_west", "ascending_east", "ascending_west":
						if !isPassableBlock(cache.GetBlock(bx-1, by, bz)) {
							vx = MinecartPoweredRailLaunch
						} else if !isPassableBlock(cache.GetBlock(bx+1, by, bz)) {
							vx = -MinecartPoweredRailLaunch
						}
					case "north_south", "ascending_north", "ascending_south":
						if !isPassableBlock(cache.GetBlock(bx, by, bz-1)) {
							vz = MinecartPoweredRailLaunch
						} else if !isPassableBlock(cache.GetBlock(bx, by, bz+1)) {
							vz = -MinecartPoweredRailLaunch
						}
					}
				}
			} else {
				vx *= MinecartUnpoweredRailBrake
				vz *= MinecartUnpoweredRailBrake
				if math.Hypot(vx, vz) < 0.003 {
					vx, vz = 0, 0
				}
			}
		}

		// 6. Clamp to max rail speed (0.4 blocks/tick) and apply drag.
		if vx > MinecartMaxRailSpeed {
			vx = MinecartMaxRailSpeed
		} else if vx < -MinecartMaxRailSpeed {
			vx = -MinecartMaxRailSpeed
		}
		if vz > MinecartMaxRailSpeed {
			vz = MinecartMaxRailSpeed
		} else if vz < -MinecartMaxRailSpeed {
			vz = -MinecartMaxRailSpeed
		}

		nextX += vx
		nextZ += vz
		nbx, nbz := int(math.Floor(nextX)), int(math.Floor(nextZ))

		// Check solid block collision at destination (and prime TNT minecart on high-speed impact).
		if cache != nil && (nbx != bx || nbz != bz) {
			destBlock := cache.GetBlock(nbx, by, nbz)
			if !isRailBlock(destBlock) && !isPassableBlock(destBlock) {
				// Ascending step onto raised rail at by+1?
				if isRailBlock(cache.GetBlock(nbx, by+1, nbz)) {
					by++
					railState = cache.GetBlock(nbx, by, nbz)
				} else {
					speedSq := vx*vx + vz*vz
					vx, vz = 0, 0
					nextX, nextZ = ent.X, ent.Z
					if typeName == EntityTypeTNTMinecart && speedSq >= 0.01 {
						s.PrimeTNTMinecart(entityID, TNTMinecartDefaultFuseTicks)
					}
				}
			} else if isRailBlock(destBlock) {
				railState = destBlock
			} else if by > world.MinY && isRailBlock(cache.GetBlock(nbx, by-1, nbz)) {
				by--
				railState = cache.GetBlock(nbx, by, nbz)
			}
		}

		nextY = railElevationAt(railState, nextX, by, nextZ)
		vy = 0
		onGround = true

		drag := MinecartDragEmpty
		switch {
		case hasPassengers:
			drag = MinecartDragOccupied
		case typeName == EntityTypeFurnaceMinecart:
			drag = MinecartDragFurnace
		case typeName == EntityTypeChestMinecart || typeName == EntityTypeHopperMinecart:
			drag = MinecartDragContainer
		}
		vx *= drag
		vz *= drag
		if math.Abs(vx) < 1e-4 {
			vx = 0
		}
		if math.Abs(vz) < 1e-4 {
			vz = 0
		}
		if vx != 0 || vz != 0 {
			nextYaw = float32(math.Atan2(-vx, vz) * 180.0 / math.Pi)
		}
	} else {
		// Off-rail physics.
		vy -= MinecartOffRailGravity
		nextX += vx
		nextY += vy
		nextZ += vz
		if cache != nil {
			belowY := int(math.Floor(nextY - 0.05))
			if belowY >= world.MinY && !isPassableBlock(cache.GetBlock(int(math.Floor(nextX)), belowY, int(math.Floor(nextZ)))) {
				nextY = float64(belowY + 1)
				vy = 0
				onGround = true
				vx *= MinecartOffRailGroundFriction
				vz *= MinecartOffRailGroundFriction
			} else {
				onGround = false
				vx *= MinecartOffRailAirDrag
				vy *= MinecartOffRailAirDrag
				vz *= MinecartOffRailAirDrag
			}
		}
	}

	nvx, nvy, nvz := clampVelocityShort(vx), clampVelocityShort(vy), clampVelocityShort(vz)
	posChanged := nextX != ent.X || nextY != ent.Y || nextZ != ent.Z ||
		nvx != ent.VelocityX || nvy != ent.VelocityY || nvz != ent.VelocityZ
	if posChanged {
		s.entities.Update(entityID, func(e *world.Entity) {
			e.X = nextX
			e.Y = nextY
			e.Z = nextZ
			e.Yaw = nextYaw
			e.HeadYaw = nextYaw
			e.OnGround = onGround
			e.VelocityX = nvx
			e.VelocityY = nvy
			e.VelocityZ = nvz
		})
		s.syncVehiclePassengerPositions(entityID, nextX, nextY, nextZ)
		if updated := s.entities.Get(entityID); updated != nil {
			cp := updated.ChunkPos()
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayTeleportEntity, encodeTeleportEntityPacket(nil, updated))
		}
	}

	// Evaluate detector/activator rail triggers at the new position.
	s.evaluateMinecartRailTriggers(entityID)

	// TNT minecart ignites when entering fire, soul_fire, or lava.
	if typeName == EntityTypeTNTMinecart && cache != nil {
		curName := world.BlockName(cache.GetBlock(int(math.Floor(nextX)), int(math.Floor(nextY)), int(math.Floor(nextZ))))
		if curName == "minecraft:fire" || curName == "minecraft:soul_fire" || curName == "minecraft:lava" {
			s.PrimeTNTMinecart(entityID, TNTMinecartDefaultFuseTicks)
		}
	}

	// Empty moving rideable minecarts pick up nearby mobs on collision.
	if typeName == EntityTypeMinecart && !hasPassengers && (vx*vx+vz*vz > 0.0025) {
		s.tryPickupMobIntoVehicle(entityID, dim, nextX, nextY, nextZ)
	}

	// Hopper minecart item pickup and hopper transfer hook.
	if typeName == EntityTypeHopperMinecart && hopperEnabled {
		s.tickHopperMinecartItems(entityID, dim, nextX, nextY, nextZ, hopperHook)
	}
}

func alignVelocityAlongRailShape(shape string, vx, vz, x, z float64, bx, bz int) (float64, float64) {
	speed := math.Hypot(vx, vz)
	switch shape {
	case "north_south", "ascending_north", "ascending_south":
		return 0, vz
	case "east_west", "ascending_east", "ascending_west":
		return vx, 0
	case "south_east":
		// Connects South (+Z) and East (+X)
		if vx < 0 {
			return 0, speed
		}
		if vz < 0 {
			return speed, 0
		}
	case "south_west":
		// Connects South (+Z) and West (-X)
		if vx > 0 {
			return 0, speed
		}
		if vz < 0 {
			return -speed, 0
		}
	case "north_west":
		// Connects North (-Z) and West (-X)
		if vx > 0 {
			return 0, -speed
		}
		if vz > 0 {
			return -speed, 0
		}
	case "north_east":
		// Connects North (-Z) and East (+X)
		if vx < 0 {
			return 0, -speed
		}
		if vz > 0 {
			return speed, 0
		}
	}
	return vx, vz
}

func (s *Server) tickHopperMinecartItems(entityID int32, dim string, x, y, z float64, hook HopperMinecartTransferHook) {
	// 1. Pull dropped item entities within [x-0.75..x+0.75, y-0.5..y+1.5, z-0.75..z+0.75].
	cx, cz := int32(int64(math.Floor(x))>>4), int32(int64(math.Floor(z))>>4)
	nearby := s.entities.AppendInChunkRange(nil, cx, cz, 1)
	for i := range nearby {
		itemEnt := &nearby[i]
		if !itemEnt.IsItem() || itemEnt.IsExperienceOrb() || itemEnt.Item.IsEmpty() || itemEnt.EffectiveDimension() != dim {
			continue
		}
		if math.Abs(itemEnt.X-x) <= 0.85 && itemEnt.Y >= y-0.5 && itemEnt.Y <= y+1.75 && math.Abs(itemEnt.Z-z) <= 0.85 {
			live := s.entities.Get(itemEnt.ID)
			if live == nil || live.Item.IsEmpty() {
				continue
			}
			added := s.AddItemToVehicleInventory(entityID, live.Item)
			if added > 0 {
				if added >= live.Item.Count {
					s.entities.Remove(live.ID)
					cp := live.ChunkPos()
					s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayRemoveEntities,
						encodeRemoveEntitiesPacket(nil, []int32{live.ID}))
				} else {
					rem := world.NewItemStack(live.Item.ResolvedItemID(), live.Item.Count-added)
					s.entities.Update(live.ID, func(e *world.Entity) {
						e.Item = rem
					})
				}
				_ = s.syncVehicleContainerViewers(nil, entityID)
			}
		}
	}

	// 2. Pull from container block/entity above (bx, by+1, bz) and push into hopper block below (bx, by-1, bz).
	s.TickHopperMinecartTransfer(entityID, dim, x, y, z)

	// 3. Invoke custom registered hopper hook if one is set.
	if hook != nil {
		hook(s, entityID, dim, x, y, z)
	}
}

func (s *Server) tickSingleBoat(entityID int32, ent *world.Entity) {
	dim := ent.EffectiveDimension()
	cache := s.DimensionChunks(dim)
	vx := float64(ent.VelocityX) / 8000.0
	vy := float64(ent.VelocityY) / 8000.0
	vz := float64(ent.VelocityZ) / 8000.0
	yaw := ent.Yaw

	mgr := s.vehicleManager()
	mgr.mu.Lock()
	st := mgr.vehicles[entityID]
	if st == nil {
		mgr.mu.Unlock()
		return
	}
	typeName := st.TypeName
	passengerCount := len(st.Passengers)
	var driverInput PlayerInput
	var hasDriver bool
	if passengerCount > 0 {
		driverID := st.Passengers[0]
		for sess, vid := range mgr.byPassengerPlayer {
			if vid == entityID && (sess.EntityID == driverID || (sess.EntityID == 0 && driverID == -1)) {
				driverInput = mgr.playerInputs[sess]
				hasDriver = true
				break
			}
		}
	}
	deltaRot := st.DeltaRot
	submerged := st.Submerged
	bubbleTime := st.BubbleTime
	leftPaddle, rightPaddle := st.LeftPaddle, st.RightPaddle
	mgr.mu.Unlock()

	medium := s.ClassifyBoatMedium(dim, ent.X, ent.Y, ent.Z)
	friction := BoatFrictionForMedium(medium)

	// Apply medium-specific vertical buoyancy / gravity.
	switch medium {
	case BoatMediumUnderWater:
		submerged++
		if submerged >= BoatMaxSubmergedTicks {
			s.DismountAllVehiclePassengers(entityID)
		}
		vy = (vy + 0.06) * 0.6
	case BoatMediumInWater:
		submerged = 0
		waterY := math.Floor(ent.Y) + 0.9
		if cache != nil {
			curName := world.BlockName(cache.GetBlock(int(math.Floor(ent.X)), int(math.Floor(ent.Y)), int(math.Floor(ent.Z))))
			if curName != "minecraft:water" && curName != "minecraft:bubble_column" {
				waterY = math.Floor(ent.Y-0.05) + 0.9
			}
		}
		vy = (waterY - ent.Y) * 0.2
	case BoatMediumOnIce, BoatMediumOnBlueIce, BoatMediumOnLand:
		submerged = 0
		vy = 0
	default:
		submerged = 0
		vy -= BoatGravity
	}

	// Bubble column rocking / upward bounce / whirlpool sinking.
	var bubbleMetaChanged bool
	if cache != nil {
		bx, by, bz := int(math.Floor(ent.X)), int(math.Floor(ent.Y)), int(math.Floor(ent.Z))
		colState := cache.GetBlock(bx, by, bz)
		if world.BlockName(colState) != "minecraft:bubble_column" {
			colState = cache.GetBlock(bx, int(math.Floor(ent.Y-0.05)), bz)
		}
		if world.BlockName(colState) == "minecraft:bubble_column" {
			bubbleTime++
			bubbleMetaChanged = true
			_, props, _ := world.BlockStateProperties(colState)
			if props["drag"] == "true" {
				vy -= 0.03
				if bubbleTime >= 60 {
					s.DismountAllVehiclePassengers(entityID)
					vy -= 0.06
				}
			} else {
				if vy < 0.06 {
					vy = 0.06
				}
			}
		} else if bubbleTime > 0 {
			bubbleTime = 0
			bubbleMetaChanged = true
		}
	}

	// Apply driver steering input when mounted.
	if hasDriver {
		deltaRot *= BoatYawDecay
		if driverInput.Left {
			deltaRot -= 1.0
		}
		if driverInput.Right {
			deltaRot += 1.0
		}
		yaw += deltaRot * BoatTurnAccelDeg
		rad := float64(yaw) * math.Pi / 180.0
		forwardX := -math.Sin(rad)
		forwardZ := math.Cos(rad)
		var accel float64
		if driverInput.Forward {
			accel += BoatForwardAccel
		}
		if driverInput.Backward {
			accel -= BoatBackwardAccel
		}
		if driverInput.Right != driverInput.Left && !driverInput.Forward && !driverInput.Backward {
			accel += 0.005
		}
		vx += forwardX * accel
		vz += forwardZ * accel
	} else {
		deltaRot = 0
	}

	vx *= friction
	vz *= friction
	if math.Abs(vx) < 1e-4 {
		vx = 0
	}
	if math.Abs(vz) < 1e-4 {
		vz = 0
	}

	mgr.mu.Lock()
	if st := mgr.vehicles[entityID]; st != nil {
		st.DeltaRot = deltaRot
		st.Submerged = submerged
		st.BubbleTime = bubbleTime
	}
	mgr.mu.Unlock()

	if bubbleMetaChanged {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlaySetEntityData,
			EncodeBoatPaddleDataPacket(entityID, leftPaddle, rightPaddle, bubbleTime))
	}

	nextX := ent.X + vx
	nextY := ent.Y + vy
	nextZ := ent.Z + vz

	// Check horizontal solid block collision.
	if cache != nil {
		nbx, nby, nbz := int(math.Floor(nextX)), int(math.Floor(nextY)), int(math.Floor(nextZ))
		destState := cache.GetBlock(nbx, nby, nbz)
		destName := world.BlockName(destState)
		if destName != "minecraft:water" && destName != "minecraft:bubble_column" &&
			destName != "minecraft:lily_pad" && !isPassableBlock(destState) {
			nextX, nextZ = ent.X, ent.Z
			vx, vz = 0, 0
		}
		belowY := int(math.Floor(nextY - 0.05))
		if belowY >= world.MinY {
			belowState := cache.GetBlock(int(math.Floor(nextX)), belowY, int(math.Floor(nextZ)))
			belowName := world.BlockName(belowState)
			if belowName != "minecraft:water" && belowName != "minecraft:bubble_column" && !isPassableBlock(belowState) && nextY < float64(belowY+1) {
				nextY = float64(belowY + 1)
				vy = 0
			}
		}
	}

	if vx != 0 || vz != 0 {
		s.breakLilyPadsInBoatPath(dim, nextX, nextY, nextZ)
	}

	posChanged := nextX != ent.X || nextY != ent.Y || nextZ != ent.Z || yaw != ent.Yaw
	if posChanged {
		s.entities.Update(entityID, func(e *world.Entity) {
			e.X = nextX
			e.Y = nextY
			e.Z = nextZ
			e.Yaw = yaw
			e.HeadYaw = yaw
			e.VelocityX = clampVelocityShort(vx)
			e.VelocityY = clampVelocityShort(vy)
			e.VelocityZ = clampVelocityShort(vz)
		})
		s.syncVehiclePassengerPositions(entityID, nextX, nextY, nextZ)
		if updated := s.entities.Get(entityID); updated != nil {
			cp := updated.ChunkPos()
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayTeleportEntity, encodeTeleportEntityPacket(nil, updated))
		}
	}

	// Boats with open passenger seats pick up nearby non-aquatic mobs on collision.
	if passengerCount < MaxVehiclePassengers(typeName) && (vx*vx+vz*vz > 0.0025) {
		s.tryPickupMobIntoVehicle(entityID, dim, nextX, nextY, nextZ)
	}
}

func (s *Server) tryPickupMobIntoVehicle(vehicleID int32, dim string, x, y, z float64) {
	cx, cz := int32(int64(math.Floor(x))>>4), int32(int64(math.Floor(z))>>4)
	nearby := s.entities.AppendInChunkRange(nil, cx, cz, 1)
	for i := range nearby {
		other := &nearby[i]
		if other.ID == vehicleID || other.IsItem() || other.IsExperienceOrb() ||
			IsVehicleEntity(other.TypeName) || IsDecorationEntity(other.TypeName) ||
			other.EffectiveDimension() != dim {
			continue
		}
		if math.Abs(other.X-x) <= 0.8 && math.Abs(other.Y-y) <= 0.8 && math.Abs(other.Z-z) <= 0.8 {
			if _, ok := s.MountEntityOnVehicle(other.ID, vehicleID); ok {
				return
			}
		}
	}
}

// --- Vehicle Damage, Destruction & Item Drops (6) ---

// DamageVehicle applies damage to a minecart or boat/raft entity:
//   - In Creative mode (attacker in Creative) or when accumulated wobble damage
//     exceeds 40.0 (or damage * 10 > 40.0), destroys the vehicle immediately,
//     dismounts all passengers, and drops the vehicle item + any container contents.
//   - Returns true if the vehicle was destroyed.
func (s *Server) DamageVehicle(entityID int32, damage float32, attacker *PlayerSession) (destroyed bool) {
	if s == nil || s.entities == nil || entityID <= 0 || damage <= 0 {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || !IsVehicleEntity(ent.TypeName) {
		return false
	}
	st := s.ensureVehicleState(entityID)
	if st == nil {
		return false
	}

	creativeInstantBreak := attacker != nil && attacker.Snapshot().GameMode == GameModeCreative
	wobbleDelta := damage * 10.0

	mgr := s.vehicleManager()
	mgr.mu.Lock()
	st.HurtDir = -st.HurtDir
	if st.HurtDir == 0 {
		st.HurtDir = 1
	}
	st.HurtTime = VehicleHurtDurationTicks
	st.Damage += wobbleDelta
	curDamage := st.Damage
	hurtDir := st.HurtDir
	typeName := st.TypeName
	mgr.mu.Unlock()

	dim := ent.EffectiveDimension()
	cp := ent.ChunkPos()
	s.EmitVibration(VibrationEvent{
		Dimension:      dim,
		Type:           GameEventEntityDamage,
		X:              ent.X,
		Y:              ent.Y,
		Z:              ent.Z,
		Player:         attacker,
		SourceEntityID: entityID,
	})

	if !creativeInstantBreak && curDamage <= VehicleBreakDamageThreshold {
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlaySetEntityData,
			EncodeVehicleEntityDataPacket(entityID, VehicleHurtDurationTicks, hurtDir, curDamage))
		return false
	}

	// Vehicle broke!
	if typeName == EntityTypeTNTMinecart && damage >= 4.0 && !creativeInstantBreak {
		s.PrimeTNTMinecart(entityID, 20)
		return true
	}
	s.DestroyVehicle(entityID, !creativeInstantBreak)
	return true
}

// DestroyVehicle dismounts all passengers, drops the vehicle item and any stored
// container items (when dropItems is true and doTileDrops is enabled), and
// removes the vehicle entity from the world.
func (s *Server) DestroyVehicle(entityID int32, dropItems bool) bool {
	if s == nil || s.entities == nil || entityID <= 0 {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || !IsVehicleEntity(ent.TypeName) {
		return false
	}
	dim := ent.EffectiveDimension()
	vx, vy, vz := ent.X, ent.Y, ent.Z
	typeName := canonicalResourceName(ent.TypeName)

	st, hadState := s.VehicleStateByID(entityID)
	s.DismountAllVehiclePassengers(entityID)
	s.RemoveVehicle(entityID)

	if dropItems && s.DoTileDrops() {
		if dropID := VehicleDropItemForType(typeName); dropID != "" {
			s.SpawnItemEntityInDimension(dim, vx, vy+0.25, vz, world.NewNamedItemStack(dropID, 1))
		}
		if hadState && IsContainerVehicleEntity(typeName) {
			slotCount := VehicleChestSlotCount
			if typeName == EntityTypeHopperMinecart {
				slotCount = HopperMinecartSlotCount
			}
			for i := 0; i < slotCount; i++ {
				if !st.Items[i].IsEmpty() {
					s.SpawnItemEntityInDimension(dim, vx, vy+0.25, vz, st.Items[i])
				}
			}
		}
	}
	return true
}

// RemoveVehicle removes a vehicle entity and cleans up its VehicleState.
func (s *Server) RemoveVehicle(entityID int32) {
	if s == nil || entityID <= 0 {
		return
	}
	s.DismountAllVehiclePassengers(entityID)
	mgr := s.vehicleManager()
	mgr.mu.Lock()
	delete(mgr.vehicles, entityID)
	mgr.mu.Unlock()

	if s.entities != nil {
		if ent := s.entities.Get(entityID); ent != nil {
			dim := ent.EffectiveDimension()
			cp := ent.ChunkPos()
			s.entities.Remove(entityID)
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayRemoveEntities,
				encodeRemoveEntitiesPacket(nil, []int32{entityID}))
		}
	}
}

// --- Vehicle Container Inventory API (4 & 5: Chest Minecart, Hopper Minecart, Chest Boat) ---

// VehicleContainerSlotCount returns the inventory slot count for a container
// vehicle (27 for `minecraft:chest_minecart` and `*_chest_boat`, 5 for
// `minecraft:hopper_minecart`, or 0 for non-container vehicles).
func (s *Server) VehicleContainerSlotCount(entityID int32) int {
	st, ok := s.VehicleStateByID(entityID)
	if !ok {
		return 0
	}
	switch {
	case st.TypeName == EntityTypeHopperMinecart:
		return HopperMinecartSlotCount
	case st.TypeName == EntityTypeChestMinecart || IsChestBoatEntity(st.TypeName):
		return VehicleChestSlotCount
	default:
		return 0
	}
}

// VehicleContainerSlots returns a copy of all 27 inventory slots of a vehicle.
func (s *Server) VehicleContainerSlots(entityID int32) [VehicleChestSlotCount]world.ItemStack {
	st, ok := s.VehicleStateByID(entityID)
	if !ok {
		return [VehicleChestSlotCount]world.ItemStack{}
	}
	return st.Items
}

// VehicleContainerSlot returns a single slot from a container vehicle's inventory.
func (s *Server) VehicleContainerSlot(entityID int32, slot int) world.ItemStack {
	st, ok := s.VehicleStateByID(entityID)
	if !ok || slot < 0 || slot >= s.VehicleContainerSlotCount(entityID) {
		return world.ItemStack{}
	}
	return st.Items[slot]
}

// SetVehicleContainerSlot updates a single slot in a container vehicle's inventory.
func (s *Server) SetVehicleContainerSlot(entityID int32, slot int, stack world.ItemStack) bool {
	st := s.ensureVehicleState(entityID)
	if st == nil {
		return false
	}
	maxSlots := VehicleChestSlotCount
	if st.TypeName == EntityTypeHopperMinecart {
		maxSlots = HopperMinecartSlotCount
	} else if st.TypeName != EntityTypeChestMinecart && !IsChestBoatEntity(st.TypeName) {
		return false
	}
	if slot < 0 || slot >= maxSlots {
		return false
	}
	mgr := s.vehicleManager()
	mgr.mu.Lock()
	if stack.IsEmpty() {
		st.Items[slot] = world.ItemStack{}
	} else {
		st.Items[slot] = world.NewItemStack(stack.ResolvedItemID(), stack.Count)
	}
	mgr.mu.Unlock()
	s.refreshVehicleDetectorRailComparators(entityID)
	return true
}

// AddItemToVehicleInventory merges stack into a container vehicle's inventory
// (0..slotCount-1) and returns the number of items added.
func (s *Server) AddItemToVehicleInventory(entityID int32, stack world.ItemStack) int32 {
	if s == nil || stack.IsEmpty() || stack.Count <= 0 {
		return 0
	}
	st := s.ensureVehicleState(entityID)
	if st == nil {
		return 0
	}
	slotCount := VehicleChestSlotCount
	if st.TypeName == EntityTypeHopperMinecart {
		slotCount = HopperMinecartSlotCount
	} else if st.TypeName != EntityTypeChestMinecart && !IsChestBoatEntity(st.TypeName) {
		return 0
	}

	itemID := stack.ResolvedItemID()
	if itemID <= 0 {
		return 0
	}
	maxStack := MaxItemStackSize
	if IsDamageableItem(stack.ResolvedID()) {
		maxStack = 1
	}

	mgr := s.vehicleManager()
	mgr.mu.Lock()

	remaining := stack.Count
	var added int32
	// Pass 1: stack onto existing matching slots.
	for i := 0; i < slotCount && remaining > 0; i++ {
		cur := st.Items[i]
		if !cur.IsEmpty() && cur.ResolvedItemID() == itemID && cur.Count < maxStack {
			space := maxStack - cur.Count
			take := remaining
			if take > space {
				take = space
			}
			st.Items[i].Count += take
			remaining -= take
			added += take
		}
	}
	// Pass 2: place into empty slots.
	for i := 0; i < slotCount && remaining > 0; i++ {
		if st.Items[i].IsEmpty() {
			take := remaining
			if take > maxStack {
				take = maxStack
			}
			st.Items[i] = world.NewItemStack(itemID, take)
			remaining -= take
			added += take
		}
	}
	mgr.mu.Unlock()
	if added > 0 {
		s.refreshVehicleDetectorRailComparators(entityID)
	}
	return added
}

// --- NBT Serialization & Persistence (7) ---

// WriteVehicleToNBT serializes a VehicleState and its world coordinates to an NBT Compound.
func (s *Server) WriteVehicleToNBT(entityID int32) (*nbt.Compound, bool) {
	if s == nil || s.entities == nil || entityID <= 0 {
		return nil, false
	}
	ent := s.entities.Get(entityID)
	st, ok := s.VehicleStateByID(entityID)
	if ent == nil || !ok {
		return nil, false
	}

	c := nbt.NewCompound().
		Set("id", nbt.String(st.TypeName)).
		Set("Dimension", nbt.String(ent.EffectiveDimension())).
		Set("Pos", nbt.List{
			ElemID: nbt.TagDouble,
			Elems:  []nbt.Tag{nbt.Double(ent.X), nbt.Double(ent.Y), nbt.Double(ent.Z)},
		}).
		Set("Motion", nbt.List{
			ElemID: nbt.TagDouble,
			Elems: []nbt.Tag{
				nbt.Double(float64(ent.VelocityX) / 8000.0),
				nbt.Double(float64(ent.VelocityY) / 8000.0),
				nbt.Double(float64(ent.VelocityZ) / 8000.0),
			},
		}).
		Set("Rotation", nbt.List{
			ElemID: nbt.TagFloat,
			Elems:  []nbt.Tag{nbt.Float(ent.Yaw), nbt.Float(ent.Pitch)},
		})

	if st.CustomName != "" {
		c.Set("CustomName", nbt.String(st.CustomName))
	}
	if st.TypeName == EntityTypeFurnaceMinecart {
		c.Set("Fuel", nbt.Short(int16(st.Fuel)))
		c.Set("PushX", nbt.Double(st.PushX))
		c.Set("PushZ", nbt.Double(st.PushZ))
	}
	if st.TypeName == EntityTypeTNTMinecart {
		c.Set("TNTFuse", nbt.Int(int32(st.FuseTicks)))
	}
	if st.TypeName == EntityTypeHopperMinecart {
		var enabledByte int8
		if st.HopperEnabled {
			enabledByte = 1
		}
		c.Set("Enabled", nbt.Byte(enabledByte))
		c.Set("TransferCooldown", nbt.Int(int32(st.TransferCooldown)))
	}
	if IsContainerVehicleEntity(st.TypeName) {
		slotCount := VehicleChestSlotCount
		if st.TypeName == EntityTypeHopperMinecart {
			slotCount = HopperMinecartSlotCount
		}
		var itemElems []nbt.Tag
		for i := 0; i < slotCount; i++ {
			stack := st.Items[i]
			if stack.IsEmpty() || stack.Count <= 0 {
				continue
			}
			ic := nbt.NewCompound().
				Set("Slot", nbt.Byte(int8(i))).
				Set("id", nbt.String(stack.ResolvedID())).
				Set("count", nbt.Int(stack.Count))
			itemElems = append(itemElems, ic)
		}
		c.Set("Items", nbt.List{
			ElemID: nbt.TagCompound,
			Elems:  itemElems,
		})
	}
	return c, true
}

// SpawnVehicleFromNBT deserializes a vehicle entity from an NBT Compound and
// spawns it in the world.
func (s *Server) SpawnVehicleFromNBT(c *nbt.Compound) (int32, bool) {
	if s == nil || s.entities == nil || c == nil {
		return 0, false
	}
	idTag, ok := c.Get("id")
	if !ok {
		return 0, false
	}
	typeStr, ok := idTag.(nbt.String)
	if !ok {
		return 0, false
	}
	typeName := canonicalResourceName(string(typeStr))
	if !IsVehicleEntity(typeName) {
		return 0, false
	}

	dim := world.DimensionOverworld
	if dt, ok := c.Get("Dimension"); ok {
		if ds, ok := dt.(nbt.String); ok && string(ds) != "" {
			dim = world.NormalizeDimension(string(ds))
		}
	}

	var x, y, z float64
	if pt, ok := c.Get("Pos"); ok {
		if pl, ok := pt.(nbt.List); ok && len(pl.Elems) >= 3 {
			if d0, ok := pl.Elems[0].(nbt.Double); ok {
				x = float64(d0)
			}
			if d1, ok := pl.Elems[1].(nbt.Double); ok {
				y = float64(d1)
			}
			if d2, ok := pl.Elems[2].(nbt.Double); ok {
				z = float64(d2)
			}
		}
	}

	var vx, vy, vz float64
	if mt, ok := c.Get("Motion"); ok {
		if ml, ok := mt.(nbt.List); ok && len(ml.Elems) >= 3 {
			if d0, ok := ml.Elems[0].(nbt.Double); ok {
				vx = float64(d0)
			}
			if d1, ok := ml.Elems[1].(nbt.Double); ok {
				vy = float64(d1)
			}
			if d2, ok := ml.Elems[2].(nbt.Double); ok {
				vz = float64(d2)
			}
		}
	}

	var yaw, pitch float32
	if rt, ok := c.Get("Rotation"); ok {
		if rl, ok := rt.(nbt.List); ok && len(rl.Elems) >= 2 {
			if f0, ok := rl.Elems[0].(nbt.Float); ok {
				yaw = float32(f0)
			}
			if f1, ok := rl.Elems[1].(nbt.Float); ok {
				pitch = float32(f1)
			}
		}
	}

	entityID, _ := s.SpawnVehicle(dim, typeName, x, y, z, yaw)
	if entityID == 0 {
		return 0, false
	}
	s.entities.Update(entityID, func(e *world.Entity) {
		e.Pitch = pitch
		e.VelocityX = clampVelocityShort(vx)
		e.VelocityY = clampVelocityShort(vy)
		e.VelocityZ = clampVelocityShort(vz)
	})

	st := s.ensureVehicleState(entityID)
	mgr := s.vehicleManager()
	mgr.mu.Lock()
	if cn, ok := c.Get("CustomName"); ok {
		if cs, ok := cn.(nbt.String); ok {
			st.CustomName = string(cs)
		}
	}
	if ft, ok := c.Get("Fuel"); ok {
		if fs, ok := ft.(nbt.Short); ok {
			st.Fuel = int(fs)
		}
	}
	if px, ok := c.Get("PushX"); ok {
		if pd, ok := px.(nbt.Double); ok {
			st.PushX = float64(pd)
		}
	}
	if pz, ok := c.Get("PushZ"); ok {
		if pd, ok := pz.(nbt.Double); ok {
			st.PushZ = float64(pd)
		}
	}
	if fuseTag, ok := c.Get("TNTFuse"); ok {
		if fi, ok := fuseTag.(nbt.Int); ok {
			st.FuseTicks = int(fi)
		}
	}
	if enTag, ok := c.Get("Enabled"); ok {
		if eb, ok := enTag.(nbt.Byte); ok {
			st.HopperEnabled = eb != 0
		}
	}
	if tcTag, ok := c.Get("TransferCooldown"); ok {
		if ti, ok := tcTag.(nbt.Int); ok {
			st.TransferCooldown = int(ti)
		}
	}
	if itTag, ok := c.Get("Items"); ok {
		if il, ok := itTag.(nbt.List); ok {
			for _, elem := range il.Elems {
				ic, ok := elem.(*nbt.Compound)
				if !ok || ic == nil {
					continue
				}
				slotTag, okS := ic.Get("Slot")
				idTag, okI := ic.Get("id")
				cntTag, okC := ic.Get("count")
				if !okS || !okI || !okC {
					continue
				}
				slotIdx := int(slotTag.(nbt.Byte))
				itemID := string(idTag.(nbt.String))
				var count int32
				switch cv := cntTag.(type) {
				case nbt.Int:
					count = int32(cv)
				case nbt.Byte:
					count = int32(cv)
				}
				if slotIdx >= 0 && slotIdx < VehicleChestSlotCount && count > 0 {
					st.Items[slotIdx] = world.NewNamedItemStack(itemID, count)
				}
			}
		}
	}
	mgr.mu.Unlock()
	return entityID, true
}

// SaveVehiclesFile persists all active vehicles to <WorldDir>/data/vehicles.dat.
func (s *Server) SaveVehiclesFile() error {
	if s == nil || s.cfg.WorldDir == "" {
		return nil
	}
	dir := filepath.Join(s.cfg.WorldDir, "data")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	mgr := s.vehicleManager()
	mgr.mu.Lock()
	ids := make([]int32, 0, len(mgr.vehicles))
	for id := range mgr.vehicles {
		ids = append(ids, id)
	}
	mgr.mu.Unlock()

	elems := make([]nbt.Tag, 0, len(ids))
	for _, id := range ids {
		if comp, ok := s.WriteVehicleToNBT(id); ok && comp != nil {
			elems = append(elems, comp)
		}
	}
	root := nbt.NewCompound().Set("Vehicles", nbt.List{
		ElemID: nbt.TagCompound,
		Elems:  elems,
	})
	return os.WriteFile(filepath.Join(dir, "vehicles.dat"), nbt.Marshal(root), 0o644)
}

// LoadVehiclesFile restores saved vehicles from <WorldDir>/data/vehicles.dat.
func (s *Server) LoadVehiclesFile() error {
	if s == nil || s.cfg.WorldDir == "" {
		return nil
	}
	raw, err := os.ReadFile(filepath.Join(s.cfg.WorldDir, "data", "vehicles.dat"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	rootTag, err := nbt.Unmarshal(raw)
	if err != nil || rootTag == nil {
		return err
	}
	root, ok := rootTag.(*nbt.Compound)
	if !ok || root == nil {
		return nil
	}
	listTag, ok := root.Get("Vehicles")
	if !ok {
		return nil
	}
	list, ok := listTag.(nbt.List)
	if !ok {
		return nil
	}
	for _, elem := range list.Elems {
		if comp, ok := elem.(*nbt.Compound); ok && comp != nil {
			s.SpawnVehicleFromNBT(comp)
		}
	}
	return nil
}
