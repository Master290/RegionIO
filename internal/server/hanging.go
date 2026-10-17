package server

import (
	"encoding/binary"
	"errors"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"regionio/internal/nbt"
	"regionio/internal/protocol"
	"regionio/internal/registry"
	"regionio/internal/world"
)

// Entity type identifiers, item identifiers, and vanilla 26.1.2 (protocol 775)
// BuiltInRegistries.ENTITY_TYPE numeric network IDs for decorative entities.
const (
	EntityTypeArmorStand    = "minecraft:armor_stand"
	EntityTypeGlowItemFrame = "minecraft:glow_item_frame"
	EntityTypeItemFrame     = "minecraft:item_frame"
	EntityTypePainting      = "minecraft:painting"

	EntityTypeArmorStandID    int = 5
	EntityTypeGlowItemFrameID int = 60
	EntityTypeItemFrameID     int = 73
	EntityTypePaintingID      int = 93

	ItemFrameItemID     = "minecraft:item_frame"
	GlowItemFrameItemID = "minecraft:glow_item_frame"
	ArmorStandItemID    = "minecraft:armor_stand"
	PaintingItemID      = "minecraft:painting"

	// 3D Direction ordinals matching net.minecraft.core.Direction.get3DDataValue().
	DirectionDown  int = 0
	DirectionUp    int = 1
	DirectionNorth int = 2
	DirectionSouth int = 3
	DirectionWest  int = 4
	DirectionEast  int = 5

	// Item frame rotation steps (0..7).
	ItemFrameRotationSteps = 8

	// SynchedEntityData indices and EntityDataSerializers IDs in protocol 775 (26.1.2).
	// HangingEntity extends BlockAttachedEntity (no fields) -> Entity (indices 0..7).
	EntityDataHangingDirectionID byte = 8  // HangingEntity.DATA_DIRECTION (DIRECTION serializer = 12)
	EntityDataItemFrameItemID    byte = 9  // ItemFrame.DATA_ITEM (ITEM_STACK serializer = 7)
	EntityDataItemFrameRotID     byte = 10 // ItemFrame.DATA_ROTATION (INT serializer = 1)
	EntityDataPaintingVariantID  byte = 9  // Painting.DATA_PAINTING_VARIANT_ID (PAINTING_VARIANT serializer = 34)

	// ArmorStand extends LivingEntity (indices 0..14).
	EntityDataArmorStandFlagsID    byte = 15 // ArmorStand.DATA_CLIENT_FLAGS (BYTE serializer = 0)
	EntityDataArmorStandHeadPoseID byte = 16 // ArmorStand.DATA_HEAD_POSE (ROTATIONS serializer = 9)
	EntityDataArmorStandBodyPoseID byte = 17 // ArmorStand.DATA_BODY_POSE (ROTATIONS serializer = 9)
	EntityDataArmorStandLArmPoseID byte = 18 // ArmorStand.DATA_LEFT_ARM_POSE (ROTATIONS serializer = 9)
	EntityDataArmorStandRArmPoseID byte = 19 // ArmorStand.DATA_RIGHT_ARM_POSE (ROTATIONS serializer = 9)
	EntityDataArmorStandLLegPoseID byte = 20 // ArmorStand.DATA_LEFT_LEG_POSE (ROTATIONS serializer = 9)
	EntityDataArmorStandRLegPoseID byte = 21 // ArmorStand.DATA_RIGHT_LEG_POSE (ROTATIONS serializer = 9)

	// ArmorStand client flag bits (ArmorStand.DATA_CLIENT_FLAGS).
	ArmorStandFlagSmall       byte = 0x01
	ArmorStandFlagShowArms    byte = 0x04
	ArmorStandFlagNoBasePlate byte = 0x08
	ArmorStandFlagMarker      byte = 0x10

	// Protocol 775 EntityDataSerializers registry indices.
	EntityDataSerializerFloat           int32 = 3
	EntityDataSerializerItemStack       int32 = 7
	EntityDataSerializerRotations       int32 = 9
	EntityDataSerializerDirection       int32 = 12
	EntityDataSerializerPaintingVariant int32 = 34

	// EquipmentSlot ordinals matching net.minecraft.world.entity.EquipmentSlot in 26.1.2.
	EquipmentSlotMainHand int = 0
	EquipmentSlotOffHand  int = 1
	EquipmentSlotFeet     int = 2
	EquipmentSlotLegs     int = 3
	EquipmentSlotChest    int = 4
	EquipmentSlotHead     int = 5
	EquipmentSlotCount    int = 6

	// ClientboundEntityEventPacket (0x22) event IDs for ArmorStand.
	EntityEventArmorStandHit byte = 32

	// Vanilla ArmorStand two-hit destruction window in ticks (75 ticks = 3.75s at 20 TPS).
	ArmorStandTwoHitWindowTicks int64 = 75
)

// Rotations3f represents a 3-axis Euler rotation (pitch, yaw, roll in degrees)
// used for ArmorStand pose metadata.
type Rotations3f [3]float32

// Default ArmorStand pose angles from net.minecraft.world.entity.decoration.ArmorStand.
var (
	DefaultArmorStandHeadPose = Rotations3f{0, 0, 0}
	DefaultArmorStandBodyPose = Rotations3f{0, 0, 0}
	DefaultArmorStandLArmPose = Rotations3f{-10, 0, -10}
	DefaultArmorStandRArmPose = Rotations3f{-15, 0, 10}
	DefaultArmorStandLLegPose = Rotations3f{-1, 0, -1}
	DefaultArmorStandRLegPose = Rotations3f{1, 0, 1}
)

// PaintingVariantDef describes a vanilla 26.1.2 minecraft:painting_variant entry.
type PaintingVariantDef struct {
	ID            string
	RegistryIndex int
	Width         int
	Height        int
	Placeable     bool
}

// Area returns Width * Height in blocks.
func (v PaintingVariantDef) Area() int {
	return v.Width * v.Height
}

// allPaintingVariants lists all 51 vanilla 26.1.2 minecraft:painting_variant
// entries in exact synced_registries.json order (indices 0..50).
var allPaintingVariants = [51]PaintingVariantDef{
	{ID: "minecraft:alban", RegistryIndex: 0, Width: 1, Height: 1, Placeable: true},
	{ID: "minecraft:aztec", RegistryIndex: 1, Width: 1, Height: 1, Placeable: true},
	{ID: "minecraft:aztec2", RegistryIndex: 2, Width: 1, Height: 1, Placeable: true},
	{ID: "minecraft:backyard", RegistryIndex: 3, Width: 3, Height: 4, Placeable: true},
	{ID: "minecraft:baroque", RegistryIndex: 4, Width: 2, Height: 2, Placeable: true},
	{ID: "minecraft:bomb", RegistryIndex: 5, Width: 1, Height: 1, Placeable: true},
	{ID: "minecraft:bouquet", RegistryIndex: 6, Width: 3, Height: 3, Placeable: true},
	{ID: "minecraft:burning_skull", RegistryIndex: 7, Width: 4, Height: 4, Placeable: true},
	{ID: "minecraft:bust", RegistryIndex: 8, Width: 2, Height: 2, Placeable: true},
	{ID: "minecraft:cavebird", RegistryIndex: 9, Width: 3, Height: 3, Placeable: true},
	{ID: "minecraft:changing", RegistryIndex: 10, Width: 4, Height: 2, Placeable: true},
	{ID: "minecraft:cotan", RegistryIndex: 11, Width: 3, Height: 3, Placeable: true},
	{ID: "minecraft:courbet", RegistryIndex: 12, Width: 2, Height: 1, Placeable: true},
	{ID: "minecraft:creebet", RegistryIndex: 13, Width: 2, Height: 1, Placeable: true},
	{ID: "minecraft:dennis", RegistryIndex: 14, Width: 3, Height: 3, Placeable: true},
	{ID: "minecraft:donkey_kong", RegistryIndex: 15, Width: 4, Height: 3, Placeable: true},
	{ID: "minecraft:earth", RegistryIndex: 16, Width: 2, Height: 2, Placeable: false},
	{ID: "minecraft:endboss", RegistryIndex: 17, Width: 3, Height: 3, Placeable: true},
	{ID: "minecraft:fern", RegistryIndex: 18, Width: 3, Height: 3, Placeable: true},
	{ID: "minecraft:fighters", RegistryIndex: 19, Width: 4, Height: 2, Placeable: true},
	{ID: "minecraft:finding", RegistryIndex: 20, Width: 4, Height: 2, Placeable: true},
	{ID: "minecraft:fire", RegistryIndex: 21, Width: 2, Height: 2, Placeable: false},
	{ID: "minecraft:graham", RegistryIndex: 22, Width: 1, Height: 2, Placeable: true},
	{ID: "minecraft:humble", RegistryIndex: 23, Width: 2, Height: 2, Placeable: true},
	{ID: "minecraft:kebab", RegistryIndex: 24, Width: 1, Height: 1, Placeable: true},
	{ID: "minecraft:lowmist", RegistryIndex: 25, Width: 4, Height: 2, Placeable: true},
	{ID: "minecraft:match", RegistryIndex: 26, Width: 2, Height: 2, Placeable: true},
	{ID: "minecraft:meditative", RegistryIndex: 27, Width: 1, Height: 1, Placeable: true},
	{ID: "minecraft:orb", RegistryIndex: 28, Width: 4, Height: 4, Placeable: true},
	{ID: "minecraft:owlemons", RegistryIndex: 29, Width: 3, Height: 3, Placeable: true},
	{ID: "minecraft:passage", RegistryIndex: 30, Width: 4, Height: 2, Placeable: true},
	{ID: "minecraft:pigscene", RegistryIndex: 31, Width: 4, Height: 4, Placeable: true},
	{ID: "minecraft:plant", RegistryIndex: 32, Width: 1, Height: 1, Placeable: true},
	{ID: "minecraft:pointer", RegistryIndex: 33, Width: 4, Height: 4, Placeable: true},
	{ID: "minecraft:pond", RegistryIndex: 34, Width: 3, Height: 4, Placeable: true},
	{ID: "minecraft:pool", RegistryIndex: 35, Width: 2, Height: 1, Placeable: true},
	{ID: "minecraft:prairie_ride", RegistryIndex: 36, Width: 1, Height: 2, Placeable: true},
	{ID: "minecraft:sea", RegistryIndex: 37, Width: 2, Height: 1, Placeable: true},
	{ID: "minecraft:skeleton", RegistryIndex: 38, Width: 4, Height: 3, Placeable: true},
	{ID: "minecraft:skull_and_roses", RegistryIndex: 39, Width: 2, Height: 2, Placeable: true},
	{ID: "minecraft:stage", RegistryIndex: 40, Width: 2, Height: 2, Placeable: true},
	{ID: "minecraft:sunflowers", RegistryIndex: 41, Width: 3, Height: 3, Placeable: true},
	{ID: "minecraft:sunset", RegistryIndex: 42, Width: 2, Height: 1, Placeable: true},
	{ID: "minecraft:tides", RegistryIndex: 43, Width: 3, Height: 3, Placeable: true},
	{ID: "minecraft:unpacked", RegistryIndex: 44, Width: 4, Height: 4, Placeable: true},
	{ID: "minecraft:void", RegistryIndex: 45, Width: 2, Height: 2, Placeable: true},
	{ID: "minecraft:wanderer", RegistryIndex: 46, Width: 1, Height: 2, Placeable: true},
	{ID: "minecraft:wasteland", RegistryIndex: 47, Width: 1, Height: 1, Placeable: true},
	{ID: "minecraft:water", RegistryIndex: 48, Width: 2, Height: 2, Placeable: false},
	{ID: "minecraft:wind", RegistryIndex: 49, Width: 2, Height: 2, Placeable: false},
	{ID: "minecraft:wither", RegistryIndex: 50, Width: 2, Height: 2, Placeable: true},
}

// AllPaintingVariants returns all 51 vanilla 26.1.2 painting variants.
func AllPaintingVariants() []PaintingVariantDef {
	out := make([]PaintingVariantDef, len(allPaintingVariants))
	copy(out, allPaintingVariants[:])
	return out
}

// PlaceablePaintingVariants returns the 47 painting variants in #minecraft:placeable.
func PlaceablePaintingVariants() []PaintingVariantDef {
	out := make([]PaintingVariantDef, 0, 47)
	for _, v := range allPaintingVariants {
		if v.Placeable {
			out = append(out, v)
		}
	}
	return out
}

// LookupPaintingVariant looks up a painting variant by resource ID (e.g. "minecraft:kebab" or "kebab").
func LookupPaintingVariant(id string) (PaintingVariantDef, bool) {
	norm := canonicalResourceName(id)
	for _, v := range allPaintingVariants {
		if v.ID == norm {
			if regIdx := registry.Index("minecraft:painting_variant", norm); regIdx >= 0 {
				v.RegistryIndex = regIdx
			}
			return v, true
		}
	}
	return PaintingVariantDef{}, false
}

// PaintingVariantByIndex looks up a painting variant by its registry index (0..50).
func PaintingVariantByIndex(idx int) (PaintingVariantDef, bool) {
	if idx < 0 || idx >= len(allPaintingVariants) {
		return PaintingVariantDef{}, false
	}
	return allPaintingVariants[idx], true
}

// IsDecorationEntity reports whether typeName is an item frame, glow item frame,
// armor stand, or painting.
func IsDecorationEntity(typeName string) bool {
	switch canonicalResourceName(typeName) {
	case EntityTypeItemFrame, EntityTypeGlowItemFrame, EntityTypeArmorStand, EntityTypePainting:
		return true
	default:
		return false
	}
}

// IsHangingEntity reports whether typeName is a block-attached hanging entity
// (item frame, glow item frame, or painting).
func IsHangingEntity(typeName string) bool {
	switch canonicalResourceName(typeName) {
	case EntityTypeItemFrame, EntityTypeGlowItemFrame, EntityTypePainting:
		return true
	default:
		return false
	}
}

// IsDecorationItem reports whether itemID is an item_frame, glow_item_frame,
// armor_stand, or painting item.
func IsDecorationItem(itemID string) bool {
	switch canonicalResourceName(itemID) {
	case ItemFrameItemID, GlowItemFrameItemID, ArmorStandItemID, PaintingItemID:
		return true
	default:
		return false
	}
}

// DirectionOffset returns the unit block step (dx, dy, dz) for a 3D Direction (0..5).
func DirectionOffset(dir int) (dx, dy, dz int) {
	switch dir {
	case DirectionDown:
		return 0, -1, 0
	case DirectionUp:
		return 0, 1, 0
	case DirectionNorth:
		return 0, 0, -1
	case DirectionSouth:
		return 0, 0, 1
	case DirectionWest:
		return -1, 0, 0
	case DirectionEast:
		return 1, 0, 0
	default:
		return 0, 0, 1
	}
}

// OppositeDirection returns the opposite 3D Direction ordinal (0..5).
func OppositeDirection(dir int) int {
	switch dir {
	case DirectionDown:
		return DirectionUp
	case DirectionUp:
		return DirectionDown
	case DirectionNorth:
		return DirectionSouth
	case DirectionSouth:
		return DirectionNorth
	case DirectionWest:
		return DirectionEast
	case DirectionEast:
		return DirectionWest
	default:
		return DirectionNorth
	}
}

// IsHorizontalDirection reports whether dir is NORTH (2), SOUTH (3), WEST (4), or EAST (5).
func IsHorizontalDirection(dir int) bool {
	return dir >= DirectionNorth && dir <= DirectionEast
}

// CounterClockWiseDirection returns the horizontal direction rotated 90 degrees
// counter-clockwise around the Y axis (matching Direction.getCounterClockWise()).
func CounterClockWiseDirection(dir int) int {
	switch dir {
	case DirectionNorth:
		return DirectionWest
	case DirectionSouth:
		return DirectionEast
	case DirectionWest:
		return DirectionSouth
	case DirectionEast:
		return DirectionNorth
	default:
		return DirectionEast
	}
}

// HangingDirectionYawPitch returns the vanilla entity yaw and pitch for a
// hanging entity facing dir (0..5).
func HangingDirectionYawPitch(dir int) (yaw, pitch float32) {
	switch dir {
	case DirectionDown:
		return 0, 90
	case DirectionUp:
		return 0, -90
	case DirectionNorth:
		return 180, 0
	case DirectionSouth:
		return 0, 0
	case DirectionWest:
		return 90, 0
	case DirectionEast:
		return 270, 0
	default:
		return 0, 0
	}
}

// ItemFrameState holds the server-side state of an item_frame or glow_item_frame entity.
type ItemFrameState struct {
	EntityID       int32
	Glow           bool
	Dimension      string
	BlockX         int
	BlockY         int
	BlockZ         int
	Direction      int // 0..5
	Item           world.ItemStack
	ItemMeta       ItemMeta
	MapID          int32
	HasMapID       bool
	Rotation       int // 0..7
	Fixed          bool
	Invisible      bool
	hadWallSupport bool
}

// AnalogOutput returns the redstone comparator signal strength (0..8) emitted
// by this item frame, matching vanilla ItemFrame.getAnalogOutput():
// 0 when empty, or (Rotation % 8) + 1 (1..8) when holding an item.
func (st ItemFrameState) AnalogOutput() int {
	if st.Item.IsEmpty() {
		return 0
	}
	rot := st.Rotation % ItemFrameRotationSteps
	if rot < 0 {
		rot += ItemFrameRotationSteps
	}
	return rot + 1
}

// ArmorStandState holds the server-side state and equipment of an armor_stand entity.
type ArmorStandState struct {
	EntityID      int32
	Dimension     string
	Flags         byte // ArmorStandFlagSmall, ArmorStandFlagShowArms, ArmorStandFlagNoBasePlate, ArmorStandFlagMarker
	HeadPose      Rotations3f
	BodyPose      Rotations3f
	LeftArmPose   Rotations3f
	RightArmPose  Rotations3f
	LeftLegPose   Rotations3f
	RightLegPose  Rotations3f
	Equipment     [EquipmentSlotCount]world.ItemStack
	EquipmentMeta [EquipmentSlotCount]ItemMeta
	DisabledSlots int32
	Invisible     bool
	Invulnerable  bool
	LastHitTick   int64
}

// PaintingState holds the server-side state of a painting entity.
type PaintingState struct {
	EntityID       int32
	Dimension      string
	BlockX         int
	BlockY         int
	BlockZ         int
	Direction      int // 2..5 (NORTH, SOUTH, WEST, EAST)
	Variant        PaintingVariantDef
	hadWallSupport bool
}

type interactAtKey struct {
	playerUUID [16]byte
	entityID   int32
}

type decorationTracker struct {
	mu          sync.RWMutex
	itemFrames  map[int32]*ItemFrameState
	armorStands map[int32]*ArmorStandState
	paintings   map[int32]*PaintingState
	lastHitY    map[interactAtKey]float64
	rng         *rand.Rand
	loadedDisk  bool
}

var serverDecorationTrackers sync.Map // map[*Server]*decorationTracker

func (s *Server) decorationTracker() *decorationTracker {
	if tr, ok := serverDecorationTrackers.Load(s); ok {
		return tr.(*decorationTracker)
	}
	initTr := &decorationTracker{
		itemFrames:  make(map[int32]*ItemFrameState),
		armorStands: make(map[int32]*ArmorStandState),
		paintings:   make(map[int32]*PaintingState),
		lastHitY:    make(map[interactAtKey]float64),
		rng:         rand.New(rand.NewSource(time.Now().UnixNano())),
	}
	actual, _ := serverDecorationTrackers.LoadOrStore(s, initTr)
	return actual.(*decorationTracker)
}

func (s *Server) recordInteractAtY(session *PlayerSession, entityID int32, targetY float64) {
	if s == nil || session == nil {
		return
	}
	dt := s.decorationTracker()
	dt.mu.Lock()
	dt.lastHitY[interactAtKey{playerUUID: session.Profile.UUID, entityID: entityID}] = targetY
	dt.mu.Unlock()
}

func (s *Server) consumeInteractAtY(session *PlayerSession, entityID int32) (float64, bool) {
	if s == nil || session == nil {
		return 0, false
	}
	dt := s.decorationTracker()
	dt.mu.Lock()
	defer dt.mu.Unlock()
	k := interactAtKey{playerUUID: session.Profile.UUID, entityID: entityID}
	v, ok := dt.lastHitY[k]
	if ok {
		delete(dt.lastHitY, k)
	}
	return v, ok
}

// IsSolidSupportBlock reports whether state can support an attached item frame
// or painting on its face (solid non-air, non-liquid block).
func IsSolidSupportBlock(state uint16) bool {
	if state == world.StateAir || state == world.StateWater || state == world.StateLava {
		return false
	}
	if isPassableBlock(state) {
		return false
	}
	return true
}

// IsUnobstructedHangingAir reports whether the block state at a hanging entity's
// air cell allows an item frame or painting to exist inside it.
func IsUnobstructedHangingAir(state uint16) bool {
	if state == world.StateAir || state == world.StateWater {
		return true
	}
	return isPassableBlock(state)
}

func (s *Server) isSupportBlockSolidAt(dim string, x, y, z int) bool {
	if s == nil {
		return false
	}
	cache := s.DimensionChunks(dim)
	if cache == nil {
		cache = s.chunks
	}
	if cache == nil {
		return false
	}
	return IsSolidSupportBlock(cache.GetBlock(x, y, z))
}

func (s *Server) isHangingCellUnobstructedAt(dim string, x, y, z int) bool {
	if s == nil {
		return true
	}
	cache := s.DimensionChunks(dim)
	if cache == nil {
		cache = s.chunks
	}
	if cache == nil {
		return true
	}
	return IsUnobstructedHangingAir(cache.GetBlock(x, y, z))
}

// PaintingCoveredBlocks returns the list of (x, y, z) attachment air blocks covered
// by a painting of (width, height) anchored at (anchorX, anchorY, anchorZ) facing
// direction (2..5), matching vanilla Painting.calculateBoundingBox.
func PaintingCoveredBlocks(anchorX, anchorY, anchorZ, direction, width, height int) [][3]int {
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}
	ccw := CounterClockWiseDirection(direction)
	ccwDX, _, ccwDZ := DirectionOffset(ccw)
	startW := -(width - 1) / 2
	startH := -(height - 1) / 2

	blocks := make([][3]int, 0, width*height)
	for h := 0; h < height; h++ {
		by := anchorY + startH + h
		for w := 0; w < width; w++ {
			offW := startW + w
			bx := anchorX + ccwDX*offW
			bz := anchorZ + ccwDZ*offW
			blocks = append(blocks, [3]int{bx, by, bz})
		}
	}
	return blocks
}

// hasConflictingHangingLocked checks if an existing item frame or painting in dim
// overlaps any of the given (x, y, z) cells on the same wall plane/direction.
// Caller must hold dt.mu (at least RLock).
func (s *Server) hasConflictingHangingLocked(dt *decorationTracker, excludeEntityID int32, dim string, direction int, cells [][3]int) bool {
	dim = world.NormalizeDimension(dim)
	for id, st := range dt.itemFrames {
		if id == excludeEntityID || world.NormalizeDimension(st.Dimension) != dim {
			continue
		}
		if st.Direction != direction && st.BlockX == cells[0][0] && st.BlockY == cells[0][1] && st.BlockZ == cells[0][2] {
			// Two item frames can occupy different faces inside the same air block unless both are horizontal/same face.
			continue
		}
		for _, c := range cells {
			if st.BlockX == c[0] && st.BlockY == c[1] && st.BlockZ == c[2] && st.Direction == direction {
				return true
			}
		}
	}
	for id, pt := range dt.paintings {
		if id == excludeEntityID || world.NormalizeDimension(pt.Dimension) != dim {
			continue
		}
		if pt.Direction != direction {
			continue
		}
		ptCells := PaintingCoveredBlocks(pt.BlockX, pt.BlockY, pt.BlockZ, pt.Direction, pt.Variant.Width, pt.Variant.Height)
		for _, pc := range ptCells {
			for _, c := range cells {
				if pc[0] == c[0] && pc[1] == c[1] && pc[2] == c[2] {
					return true
				}
			}
		}
	}
	return false
}

// SpawnItemFrame spawns an item_frame or glow_item_frame at the attachment block
// position (blockX, blockY, blockZ) facing direction (0..5) in dim.
func (s *Server) SpawnItemFrame(dim string, blockX, blockY, blockZ, direction int, glow bool) (int32, error) {
	if s == nil || s.entities == nil {
		return 0, errors.New("server: nil entity manager")
	}
	if direction < DirectionDown || direction > DirectionEast {
		return 0, errors.New("server: invalid item frame direction")
	}
	dim = world.NormalizeDimension(dim)

	dt := s.decorationTracker()
	dt.mu.Lock()
	if s.hasConflictingHangingLocked(dt, 0, dim, direction, [][3]int{{blockX, blockY, blockZ}}) {
		dt.mu.Unlock()
		return 0, errors.New("server: hanging entity already occupies block face")
	}

	typeName := EntityTypeItemFrame
	typeID := ResolveEntityTypeID(EntityTypeItemFrame)
	if typeID < 0 {
		typeID = EntityTypeItemFrameID
	}
	if glow {
		typeName = EntityTypeGlowItemFrame
		typeID = ResolveEntityTypeID(EntityTypeGlowItemFrame)
		if typeID < 0 {
			typeID = EntityTypeGlowItemFrameID
		}
	}

	yaw, pitch := HangingDirectionYawPitch(direction)
	dx, dy, dz := DirectionOffset(direction)
	supX, supY, supZ := blockX-dx, blockY-dy, blockZ-dz
	hasSupport := s.isSupportBlockSolidAt(dim, supX, supY, supZ)

	ent := &world.Entity{
		TypeID:    typeID,
		TypeName:  typeName,
		Dimension: dim,
		X:         float64(blockX),
		Y:         float64(blockY),
		Z:         float64(blockZ),
		Yaw:       yaw,
		Pitch:     pitch,
		HeadYaw:   yaw,
		OnGround:  false,
	}
	entityID := s.entities.Add(ent)
	st := &ItemFrameState{
		EntityID:       entityID,
		Glow:           glow,
		Dimension:      dim,
		BlockX:         blockX,
		BlockY:         blockY,
		BlockZ:         blockZ,
		Direction:      direction,
		hadWallSupport: hasSupport,
	}
	dt.itemFrames[entityID] = st
	dt.mu.Unlock()

	if spawned := s.entities.Get(entityID); spawned != nil {
		cp := spawned.ChunkPos()
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayAddEntity,
			s.EncodeDecorationAddEntityPacket(nil, spawned))
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
			s.DecorationEntityDataPacket(entityID))
		placeSound := "minecraft:entity.item_frame.place"
		if glow {
			placeSound = "minecraft:entity.glow_item_frame.place"
		}
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
			EncodeSoundPacket(placeSound, SoundSourceBlocks, float64(blockX)+0.5, float64(blockY)+0.5, float64(blockZ)+0.5, 1.0, 1.0, 0))
	}
	return entityID, nil
}

// PlaceItemFrame places an item frame or glow item frame when session right-clicks
// the supporting block at (clickedX, clickedY, clickedZ) on face (0..5).
func (s *Server) PlaceItemFrame(session *PlayerSession, clickedX, clickedY, clickedZ, face int, glow bool) (int32, bool) {
	if s == nil || s.entities == nil || session == nil || session.IsDead() {
		return 0, false
	}
	if face < DirectionDown || face > DirectionEast {
		return 0, false
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator || snap.GameMode == GameModeAdventure {
		return 0, false
	}
	dim := world.NormalizeDimension(snap.Dimension)

	// Verify supporting block at (clickedX, clickedY, clickedZ) is solid.
	if !s.isSupportBlockSolidAt(dim, clickedX, clickedY, clickedZ) {
		return 0, false
	}
	dx, dy, dz := DirectionOffset(face)
	posX, posY, posZ := clickedX+dx, clickedY+dy, clickedZ+dz
	if posY < world.MinY || posY >= world.MinY+world.WorldHeight {
		return 0, false
	}
	if !s.isHangingCellUnobstructedAt(dim, posX, posY, posZ) {
		return 0, false
	}

	entityID, err := s.SpawnItemFrame(dim, posX, posY, posZ, face, glow)
	if err != nil || entityID == 0 {
		return 0, false
	}

	expectedItem := ItemFrameItemID
	if glow {
		expectedItem = GlowItemFrameItemID
	}
	s.consumeDecorationItemFromPlayer(session, expectedItem)
	return entityID, true
}

// ItemFrameState returns a snapshot of the item frame state for entityID.
func (s *Server) ItemFrameState(entityID int32) (ItemFrameState, bool) {
	if s == nil {
		return ItemFrameState{}, false
	}
	dt := s.decorationTracker()
	dt.mu.RLock()
	defer dt.mu.RUnlock()
	st, ok := dt.itemFrames[entityID]
	if !ok || st == nil {
		return ItemFrameState{}, false
	}
	cp := *st
	cp.ItemMeta = st.ItemMeta.Clone()
	return cp, true
}

// ItemFrameAnalogOutput returns the redstone comparator analog signal (0..8)
// for the item frame with entityID (0 when empty, 1..8 for rotations 0..7).
func (s *Server) ItemFrameAnalogOutput(entityID int32) int {
	st, ok := s.ItemFrameState(entityID)
	if !ok {
		return 0
	}
	return st.AnalogOutput()
}

// ItemFrameComparatorSignalAt returns the maximum comparator analog signal (0..8)
// from any item frame attached to the supporting block at (supportX, supportY, supportZ)
// in dim (or at the frame block position itself).
func (s *Server) ItemFrameComparatorSignalAt(dim string, blockX, blockY, blockZ int) int {
	if s == nil {
		return 0
	}
	dim = world.NormalizeDimension(dim)
	dt := s.decorationTracker()
	dt.mu.RLock()
	defer dt.mu.RUnlock()

	best := 0
	for _, st := range dt.itemFrames {
		if st == nil || world.NormalizeDimension(st.Dimension) != dim {
			continue
		}
		dx, dy, dz := DirectionOffset(st.Direction)
		supX, supY, supZ := st.BlockX-dx, st.BlockY-dy, st.BlockZ-dz
		if (st.BlockX == blockX && st.BlockY == blockY && st.BlockZ == blockZ) ||
			(supX == blockX && supY == blockY && supZ == blockZ) {
			if sig := st.AnalogOutput(); sig > best {
				best = sig
			}
		}
	}
	return best
}

// InteractWithItemFrame handles a player right-clicking an item frame or glow item frame:
//   - If empty and player holds an item in hand: inserts 1 item into the frame (rotation 0).
//   - If already holding an item: increments rotation by 1 modulo 8 (0..7).
func (s *Server) InteractWithItemFrame(session *PlayerSession, entityID int32, hand int32) bool {
	if s == nil || s.entities == nil || session == nil || session.IsDead() {
		return false
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator || snap.Health <= 0 {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || (ent.TypeName != EntityTypeItemFrame && ent.TypeName != EntityTypeGlowItemFrame) {
		return false
	}
	if !isWithinDecorationReach(snap, ent) {
		return false
	}

	slot, held := session.handSlotAndStack(hand)
	heldMapID, heldHasMapID := GetSlotMapID(session, slot)

	dt := s.decorationTracker()
	dt.mu.Lock()
	st := dt.itemFrames[entityID]
	if st == nil || st.Fixed {
		dt.mu.Unlock()
		return false
	}

	var soundName string
	glow := st.Glow
	dim := st.Dimension
	bx, by, bz := st.BlockX, st.BlockY, st.BlockZ
	dir := st.Direction
	framedMapID := int32(0)
	framedHasMap := false

	if st.Item.IsEmpty() {
		if held.IsEmpty() {
			dt.mu.Unlock()
			return false
		}
		inserted := world.NewItemStack(held.ResolvedItemID(), 1)
		if inserted.IsEmpty() {
			inserted = world.NewNamedItemStack(held.ResolvedID(), 1)
		}
		if inserted.IsEmpty() {
			dt.mu.Unlock()
			return false
		}
		st.Item = inserted
		st.ItemMeta = GetSlotItemMeta(session, slot)
		if canonicalResourceName(inserted.ResolvedID()) == FilledMapItemID && heldHasMapID {
			st.MapID = heldMapID
			st.HasMapID = true
			framedMapID = heldMapID
			framedHasMap = true
		} else {
			st.MapID = 0
			st.HasMapID = false
		}
		st.Rotation = 0
		dt.mu.Unlock()

		if framedHasMap {
			s.AddItemFrameToMap(framedMapID, entityID, bx, by, bz, dir)
		}

		// Consume 1 item from the player's hand when in survival/adventure, or when stack has 1 item in tests.
		if snap.GameMode != GameModeCreative {
			rem := held.Count - 1
			if rem <= 0 {
				session.SetInventorySlot(slot, world.ItemStack{})
				SetSlotItemMeta(session, slot, ItemMeta{})
				ClearSlotMapID(session, slot)
			} else {
				session.SetInventorySlot(slot, world.NewItemStack(held.ResolvedItemID(), rem))
			}
		}

		soundName = "minecraft:entity.item_frame.add_item"
		if glow {
			soundName = "minecraft:entity.glow_item_frame.add_item"
		}
	} else {
		st.Rotation = (st.Rotation + 1) % ItemFrameRotationSteps
		framedMapID = st.MapID
		framedHasMap = st.HasMapID
		dt.mu.Unlock()

		if framedHasMap {
			s.AddItemFrameToMap(framedMapID, entityID, bx, by, bz, dir)
		}

		soundName = "minecraft:entity.item_frame.rotate_item"
		if glow {
			soundName = "minecraft:entity.glow_item_frame.rotate_item"
		}
	}

	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
		s.DecorationEntityDataPacket(entityID))
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(soundName, SoundSourceBlocks, ent.X+0.5, ent.Y+0.5, ent.Z+0.5, 1.0, 1.0, 0))
	return true
}

// AttackItemFrame handles a player left-clicking (attacking) an item frame:
//   - If the frame contains an item, pops and drops the item without breaking the frame.
//   - If the frame is empty, breaks the frame and drops item_frame / glow_item_frame.
func (s *Server) AttackItemFrame(session *PlayerSession, entityID int32) (handled bool, broken bool) {
	if s == nil || s.entities == nil {
		return false, false
	}
	if session != nil {
		snap := session.Snapshot()
		if snap.GameMode == GameModeSpectator || snap.Health <= 0 {
			return false, false
		}
	}
	ent := s.entities.Get(entityID)
	if ent == nil || (ent.TypeName != EntityTypeItemFrame && ent.TypeName != EntityTypeGlowItemFrame) {
		return false, false
	}
	if session != nil && !isWithinDecorationReach(session.Snapshot(), ent) {
		return false, false
	}

	dt := s.decorationTracker()
	dt.mu.Lock()
	st := dt.itemFrames[entityID]
	if st == nil || st.Fixed {
		dt.mu.Unlock()
		return false, false
	}

	glow := st.Glow
	dim := st.Dimension
	bx, by, bz := st.BlockX, st.BlockY, st.BlockZ

	if !st.Item.IsEmpty() {
		popped := st.Item
		hadMap := st.HasMapID
		mapID := st.MapID
		st.Item = world.ItemStack{}
		st.ItemMeta = ItemMeta{}
		st.MapID = 0
		st.HasMapID = false
		st.Rotation = 0
		dt.mu.Unlock()

		if hadMap {
			s.RemoveItemFrameFromMap(mapID, entityID, bx, by, bz)
		}

		s.SpawnItemEntityInDimension(dim, float64(bx)+0.5, float64(by)+0.25, float64(bz)+0.5, popped)
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
			s.DecorationEntityDataPacket(entityID))
		rmSound := "minecraft:entity.item_frame.remove_item"
		if glow {
			rmSound = "minecraft:entity.glow_item_frame.remove_item"
		}
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
			EncodeSoundPacket(rmSound, SoundSourceBlocks, float64(bx)+0.5, float64(by)+0.5, float64(bz)+0.5, 1.0, 1.0, 0))
		return true, false
	}

	delete(dt.itemFrames, entityID)
	dt.mu.Unlock()

	s.breakAndRemoveItemFrame(ent, glow, world.ItemStack{}, true)
	return true, true
}

func (s *Server) breakAndRemoveItemFrame(ent *world.Entity, glow bool, contained world.ItemStack, dropFrame bool) {
	if s == nil || ent == nil {
		return
	}
	dim := ent.EffectiveDimension()
	cp := ent.ChunkPos()
	s.entities.Remove(ent.ID)
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayRemoveEntities,
		encodeRemoveEntitiesPacket(nil, []int32{ent.ID}))

	brkSound := "minecraft:entity.item_frame.break"
	frameItemName := ItemFrameItemID
	if glow {
		brkSound = "minecraft:entity.glow_item_frame.break"
		frameItemName = GlowItemFrameItemID
	}
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(brkSound, SoundSourceBlocks, ent.X+0.5, ent.Y+0.5, ent.Z+0.5, 1.0, 1.0, 0))

	if !contained.IsEmpty() {
		s.SpawnItemEntityInDimension(dim, ent.X+0.5, ent.Y+0.25, ent.Z+0.5, contained)
	}
	if dropFrame {
		if frameStack := world.NewNamedItemStack(frameItemName, 1); !frameStack.IsEmpty() {
			s.SpawnItemEntityInDimension(dim, ent.X+0.5, ent.Y+0.25, ent.Z+0.5, frameStack)
		}
	}
}

// SpawnPainting spawns a specific painting variant at (blockX, blockY, blockZ)
// facing direction (2..5: NORTH, SOUTH, WEST, EAST) in dim.
func (s *Server) SpawnPainting(dim string, blockX, blockY, blockZ, direction int, variantID string) (int32, error) {
	if s == nil || s.entities == nil {
		return 0, errors.New("server: nil entity manager")
	}
	if !IsHorizontalDirection(direction) {
		return 0, errors.New("server: painting direction must be horizontal (2..5)")
	}
	variant, ok := LookupPaintingVariant(variantID)
	if !ok {
		return 0, errors.New("server: unknown painting variant " + variantID)
	}
	dim = world.NormalizeDimension(dim)

	cells := PaintingCoveredBlocks(blockX, blockY, blockZ, direction, variant.Width, variant.Height)
	dt := s.decorationTracker()
	dt.mu.Lock()
	if s.hasConflictingHangingLocked(dt, 0, dim, direction, cells) {
		dt.mu.Unlock()
		return 0, errors.New("server: painting overlaps existing hanging entity")
	}

	hasSupport := s.doesPaintingSurviveOnWall(dim, blockX, blockY, blockZ, direction, variant.Width, variant.Height)
	typeID := ResolveEntityTypeID(EntityTypePainting)
	if typeID < 0 {
		typeID = EntityTypePaintingID
	}
	yaw, pitch := HangingDirectionYawPitch(direction)
	ent := &world.Entity{
		TypeID:    typeID,
		TypeName:  EntityTypePainting,
		Dimension: dim,
		X:         float64(blockX),
		Y:         float64(blockY),
		Z:         float64(blockZ),
		Yaw:       yaw,
		Pitch:     pitch,
		HeadYaw:   yaw,
		OnGround:  false,
	}
	entityID := s.entities.Add(ent)
	dt.paintings[entityID] = &PaintingState{
		EntityID:       entityID,
		Dimension:      dim,
		BlockX:         blockX,
		BlockY:         blockY,
		BlockZ:         blockZ,
		Direction:      direction,
		Variant:        variant,
		hadWallSupport: hasSupport,
	}
	dt.mu.Unlock()

	if spawned := s.entities.Get(entityID); spawned != nil {
		cp := spawned.ChunkPos()
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayAddEntity,
			s.EncodeDecorationAddEntityPacket(nil, spawned))
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
			s.DecorationEntityDataPacket(entityID))
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
			EncodeSoundPacket("minecraft:entity.painting.place", SoundSourceBlocks, float64(blockX)+0.5, float64(blockY)+0.5, float64(blockZ)+0.5, 1.0, 1.0, 0))
	}
	return entityID, nil
}

// doesPaintingSurviveOnWall checks whether every block in the width x height
// footprint of a painting at (anchorX, anchorY, anchorZ) facing direction has a
// solid supporting wall block behind it and an unobstructed air space in front.
func (s *Server) doesPaintingSurviveOnWall(dim string, anchorX, anchorY, anchorZ, direction, width, height int) bool {
	if !IsHorizontalDirection(direction) {
		return false
	}
	dx, dy, dz := DirectionOffset(direction)
	cells := PaintingCoveredBlocks(anchorX, anchorY, anchorZ, direction, width, height)
	for _, c := range cells {
		bx, by, bz := c[0], c[1], c[2]
		if by < world.MinY || by >= world.MinY+world.WorldHeight {
			return false
		}
		if !s.isHangingCellUnobstructedAt(dim, bx, by, bz) {
			return false
		}
		supX, supY, supZ := bx-dx, by-dy, bz-dz
		if !s.isSupportBlockSolidAt(dim, supX, supY, supZ) {
			return false
		}
	}
	return true
}

// SelectFittingPaintingVariants evaluates all 47 placeable vanilla 26.1.2
// painting variants at attachment position (posX, posY, posZ) facing direction
// (2..5) in dim, filters to those whose width x height footprint fits on the
// solid wall without overlapping existing hanging entities, and keeps only the
// largest fitting area (maxArea = width * height), matching Painting.create.
func (s *Server) SelectFittingPaintingVariants(dim string, posX, posY, posZ, direction int) []PaintingVariantDef {
	if s == nil || !IsHorizontalDirection(direction) {
		return nil
	}
	dim = world.NormalizeDimension(dim)
	dt := s.decorationTracker()
	dt.mu.RLock()
	defer dt.mu.RUnlock()

	var fitting []PaintingVariantDef
	maxArea := 0
	for _, v := range allPaintingVariants {
		if !v.Placeable {
			continue
		}
		if !s.doesPaintingSurviveOnWall(dim, posX, posY, posZ, direction, v.Width, v.Height) {
			continue
		}
		cells := PaintingCoveredBlocks(posX, posY, posZ, direction, v.Width, v.Height)
		if s.hasConflictingHangingLocked(dt, 0, dim, direction, cells) {
			continue
		}
		fitting = append(fitting, v)
		if area := v.Area(); area > maxArea {
			maxArea = area
		}
	}
	if len(fitting) == 0 {
		return nil
	}

	largest := make([]PaintingVariantDef, 0, len(fitting))
	for _, v := range fitting {
		if v.Area() >= maxArea {
			largest = append(largest, v)
		}
	}
	return largest
}

// PlacePainting places a painting when session right-clicks the wall block at
// (clickedX, clickedY, clickedZ) on horizontal face (2..5), choosing randomly
// among the largest-area fitting 26.1.2 variants as in vanilla Painting.create.
func (s *Server) PlacePainting(session *PlayerSession, clickedX, clickedY, clickedZ, face int) (int32, PaintingVariantDef, bool) {
	if s == nil || s.entities == nil || session == nil || session.IsDead() {
		return 0, PaintingVariantDef{}, false
	}
	if !IsHorizontalDirection(face) {
		return 0, PaintingVariantDef{}, false
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator || snap.GameMode == GameModeAdventure {
		return 0, PaintingVariantDef{}, false
	}
	dim := world.NormalizeDimension(snap.Dimension)

	dx, dy, dz := DirectionOffset(face)
	posX, posY, posZ := clickedX+dx, clickedY+dy, clickedZ+dz
	candidates := s.SelectFittingPaintingVariants(dim, posX, posY, posZ, face)
	if len(candidates) == 0 {
		return 0, PaintingVariantDef{}, false
	}

	dt := s.decorationTracker()
	dt.mu.Lock()
	chosen := candidates[dt.rng.Intn(len(candidates))]
	dt.mu.Unlock()

	entityID, err := s.SpawnPainting(dim, posX, posY, posZ, face, chosen.ID)
	if err != nil || entityID == 0 {
		return 0, PaintingVariantDef{}, false
	}
	s.consumeDecorationItemFromPlayer(session, PaintingItemID)
	return entityID, chosen, true
}

// PaintingState returns a snapshot of the painting state for entityID.
func (s *Server) PaintingState(entityID int32) (PaintingState, bool) {
	if s == nil {
		return PaintingState{}, false
	}
	dt := s.decorationTracker()
	dt.mu.RLock()
	defer dt.mu.RUnlock()
	pt, ok := dt.paintings[entityID]
	if !ok || pt == nil {
		return PaintingState{}, false
	}
	return *pt, true
}

// AttackPainting handles left-clicking (attacking) a painting: breaks the
// painting, removes the entity, plays entity.painting.break, and drops
// minecraft:painting.
func (s *Server) AttackPainting(session *PlayerSession, entityID int32) (handled bool, broken bool) {
	if s == nil || s.entities == nil {
		return false, false
	}
	if session != nil {
		snap := session.Snapshot()
		if snap.GameMode == GameModeSpectator || snap.Health <= 0 {
			return false, false
		}
	}
	ent := s.entities.Get(entityID)
	if ent == nil || ent.TypeName != EntityTypePainting {
		return false, false
	}
	if session != nil && !isWithinDecorationReach(session.Snapshot(), ent) {
		return false, false
	}

	dt := s.decorationTracker()
	dt.mu.Lock()
	_, ok := dt.paintings[entityID]
	if !ok {
		dt.mu.Unlock()
		return false, false
	}
	delete(dt.paintings, entityID)
	dt.mu.Unlock()

	s.breakAndRemovePainting(ent, true)
	return true, true
}

func (s *Server) breakAndRemovePainting(ent *world.Entity, dropItem bool) {
	if s == nil || ent == nil {
		return
	}
	dim := ent.EffectiveDimension()
	cp := ent.ChunkPos()
	s.entities.Remove(ent.ID)
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayRemoveEntities,
		encodeRemoveEntitiesPacket(nil, []int32{ent.ID}))
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket("minecraft:entity.painting.break", SoundSourceBlocks, ent.X+0.5, ent.Y+0.5, ent.Z+0.5, 1.0, 1.0, 0))
	if dropItem {
		if stack := world.NewNamedItemStack(PaintingItemID, 1); !stack.IsEmpty() {
			s.SpawnItemEntityInDimension(dim, ent.X+0.5, ent.Y+0.25, ent.Z+0.5, stack)
		}
	}
}

// SpawnArmorStand spawns an armor_stand entity at (x, y, z) with yaw in dim.
func (s *Server) SpawnArmorStand(dim string, x, y, z float64, yaw float32) (int32, error) {
	if s == nil || s.entities == nil {
		return 0, errors.New("server: nil entity manager")
	}
	dim = world.NormalizeDimension(dim)
	typeID := ResolveEntityTypeID(EntityTypeArmorStand)
	if typeID < 0 {
		typeID = EntityTypeArmorStandID
	}
	ent := &world.Entity{
		TypeID:    typeID,
		TypeName:  EntityTypeArmorStand,
		Dimension: dim,
		X:         x,
		Y:         y,
		Z:         z,
		Yaw:       yaw,
		Pitch:     0,
		HeadYaw:   yaw,
		OnGround:  true,
		Health:    20.0,
		MaxHealth: 20.0,
	}
	entityID := s.entities.Add(ent)

	dt := s.decorationTracker()
	dt.mu.Lock()
	dt.armorStands[entityID] = &ArmorStandState{
		EntityID:     entityID,
		Dimension:    dim,
		Flags:        ArmorStandFlagShowArms, // enable arms so both armor & held items can be equipped/displayed
		HeadPose:     DefaultArmorStandHeadPose,
		BodyPose:     DefaultArmorStandBodyPose,
		LeftArmPose:  DefaultArmorStandLArmPose,
		RightArmPose: DefaultArmorStandRArmPose,
		LeftLegPose:  DefaultArmorStandLLegPose,
		RightLegPose: DefaultArmorStandRLegPose,
		LastHitTick:  -1000,
	}
	dt.mu.Unlock()

	if spawned := s.entities.Get(entityID); spawned != nil {
		cp := spawned.ChunkPos()
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayAddEntity,
			encodeAddEntityPacket(nil, spawned))
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
			s.DecorationEntityDataPacket(entityID))
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
			EncodeSoundPacket("minecraft:entity.armor_stand.place", SoundSourceBlocks, x, y, z, 1.0, 1.0, 0))
	}
	return entityID, nil
}

// PlaceArmorStand places an armor stand when session right-clicks block
// (clickedX, clickedY, clickedZ) on face (0..5).
func (s *Server) PlaceArmorStand(session *PlayerSession, clickedX, clickedY, clickedZ, face int) (int32, bool) {
	if s == nil || s.entities == nil || session == nil || session.IsDead() {
		return 0, false
	}
	if face < DirectionDown || face > DirectionEast {
		return 0, false
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator || snap.GameMode == GameModeAdventure {
		return 0, false
	}
	dim := world.NormalizeDimension(snap.Dimension)
	dx, dy, dz := DirectionOffset(face)
	posX, posY, posZ := clickedX+dx, clickedY+dy, clickedZ+dz
	if posY < world.MinY || posY+1 >= world.MinY+world.WorldHeight {
		return 0, false
	}
	// Armor stand is 1.975 blocks tall, so both (posX, posY, posZ) and (posX, posY+1, posZ) must be passable.
	if !s.isHangingCellUnobstructedAt(dim, posX, posY, posZ) || !s.isHangingCellUnobstructedAt(dim, posX, posY+1, posZ) {
		return 0, false
	}

	// Vanilla ArmorStandItem snaps yaw to 22.5-degree increments facing the player:
	// floor((playerYaw + 180 + 11.25) * 16 / 360) * 22.5
	rawYaw := math.Mod(float64(snap.Yaw)+180.0, 360.0)
	if rawYaw < 0 {
		rawYaw += 360.0
	}
	snappedYaw := float32(math.Floor((rawYaw+11.25)*16.0/360.0) * 22.5)

	entityID, err := s.SpawnArmorStand(dim, float64(posX)+0.5, float64(posY), float64(posZ)+0.5, snappedYaw)
	if err != nil || entityID == 0 {
		return 0, false
	}
	s.consumeDecorationItemFromPlayer(session, ArmorStandItemID)
	return entityID, true
}

// ArmorStandState returns a snapshot of the armor stand state for entityID.
func (s *Server) ArmorStandState(entityID int32) (ArmorStandState, bool) {
	if s == nil {
		return ArmorStandState{}, false
	}
	dt := s.decorationTracker()
	dt.mu.RLock()
	defer dt.mu.RUnlock()
	st, ok := dt.armorStands[entityID]
	if !ok || st == nil {
		return ArmorStandState{}, false
	}
	cp := *st
	for i := 0; i < EquipmentSlotCount; i++ {
		cp.EquipmentMeta[i] = st.EquipmentMeta[i].Clone()
	}
	return cp, true
}

// SetArmorStandEquipment sets an equipment slot (EquipmentSlotMainHand..EquipmentSlotHead)
// on the armor stand with entityID and broadcasts ClientboundSetEquipmentPacket (0x66).
func (s *Server) SetArmorStandEquipment(entityID int32, equipSlot int, stack world.ItemStack) bool {
	if s == nil || equipSlot < 0 || equipSlot >= EquipmentSlotCount {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || ent.TypeName != EntityTypeArmorStand {
		return false
	}
	dt := s.decorationTracker()
	dt.mu.Lock()
	st := dt.armorStands[entityID]
	if st == nil {
		dt.mu.Unlock()
		return false
	}
	st.Equipment[equipSlot] = stack
	st.EquipmentMeta[equipSlot] = ItemMeta{}
	eq := st.Equipment
	dim := st.Dimension
	dt.mu.Unlock()

	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlaySetEquipment,
		EncodeArmorStandEquipmentPacket(entityID, eq))
	return true
}

// EquipmentSlotForItem determines the target ArmorStand EquipmentSlot (0..5)
// for a held item:
//   - Helmets / turtle_helmet / carved_pumpkin / player_head / skull -> EquipmentSlotHead (5)
//   - Chestplates / elytra -> EquipmentSlotChest (4)
//   - Leggings -> EquipmentSlotLegs (3)
//   - Boots -> EquipmentSlotFeet (2)
//   - Shield -> EquipmentSlotOffHand (1)
//   - Everything else (weapons, tools, blocks, items) -> EquipmentSlotMainHand (0)
func EquipmentSlotForItem(itemName string) int {
	norm := canonicalResourceName(itemName)
	if stats, ok := ArmorItemStats(norm); ok {
		switch stats.Slot {
		case ArmorSlotHead:
			return EquipmentSlotHead
		case ArmorSlotChest:
			return EquipmentSlotChest
		case ArmorSlotLegs:
			return EquipmentSlotLegs
		case ArmorSlotFeet:
			return EquipmentSlotFeet
		}
	}
	switch {
	case strings.HasSuffix(norm, "_helmet"),
		norm == "minecraft:carved_pumpkin",
		strings.HasSuffix(norm, "_head"),
		strings.HasSuffix(norm, "_skull"):
		return EquipmentSlotHead
	case strings.HasSuffix(norm, "_chestplate"),
		norm == "minecraft:elytra":
		return EquipmentSlotChest
	case strings.HasSuffix(norm, "_leggings"):
		return EquipmentSlotLegs
	case strings.HasSuffix(norm, "_boots"):
		return EquipmentSlotFeet
	case norm == "minecraft:shield":
		return EquipmentSlotOffHand
	default:
		return EquipmentSlotMainHand
	}
}

// ArmorStandClickedSlot maps a local vertical click offset relY (relative to
// the armor stand's feet Y) to an EquipmentSlot, matching vanilla
// ArmorStand.getClickedSlot:
//   - relY < 0.1 || relY >= 0.55 && relY < 0.9 -> Legs (or Feet if < 0.55)
//   - Specifically in vanilla:
//     [0.1, 0.55)  -> EquipmentSlotFeet (2)
//     [0.4, 1.2)   -> EquipmentSlotLegs (3) / [0.9, 1.6) -> EquipmentSlotChest (4)
//     >= 1.6       -> EquipmentSlotHead (5)
func ArmorStandClickedSlot(relY float64, isSmall bool) int {
	if isSmall {
		relY *= 2.0
	}
	switch {
	case relY >= 0.1 && relY < 0.55:
		return EquipmentSlotFeet
	case relY >= 0.9 && relY < 1.6:
		return EquipmentSlotChest
	case relY >= 0.4 && relY < 1.2:
		return EquipmentSlotLegs
	case relY >= 1.6:
		return EquipmentSlotHead
	default:
		return EquipmentSlotMainHand
	}
}

// InteractWithArmorStand handles right-clicking an armor stand to equip, swap,
// or unequip armor and hand items. If hasClickY is true, clickY is the local Y
// offset from InteractAt; otherwise when unequipping with an empty hand it
// removes the first non-empty piece in order Head -> Chest -> Legs -> Feet -> MainHand -> OffHand.
func (s *Server) InteractWithArmorStand(session *PlayerSession, entityID int32, hand int32, clickY float64, hasClickY bool) bool {
	if s == nil || s.entities == nil || session == nil || session.IsDead() {
		return false
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator || snap.Health <= 0 {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || ent.TypeName != EntityTypeArmorStand {
		return false
	}
	if !isWithinDecorationReach(snap, ent) {
		return false
	}

	dt := s.decorationTracker()
	dt.mu.Lock()
	st := dt.armorStands[entityID]
	if st == nil || (st.Flags&ArmorStandFlagMarker) != 0 {
		dt.mu.Unlock()
		return false
	}

	invSlot, held := session.handSlotAndStack(hand)
	heldMeta := GetSlotItemMeta(session, invSlot)
	isSmall := (st.Flags & ArmorStandFlagSmall) != 0

	targetSlot := -1
	if !held.IsEmpty() {
		if canonicalResourceName(held.ResolvedID()) == "minecraft:name_tag" {
			dt.mu.Unlock()
			return false
		}
		targetSlot = EquipmentSlotForItem(held.ResolvedID())
	} else {
		// Empty hand: unequip clicked slot if occupied, else find first occupied slot.
		if hasClickY {
			clicked := ArmorStandClickedSlot(clickY, isSmall)
			if !st.Equipment[clicked].IsEmpty() {
				targetSlot = clicked
			}
		}
		if targetSlot < 0 {
			for _, candidate := range []int{
				EquipmentSlotHead,
				EquipmentSlotChest,
				EquipmentSlotLegs,
				EquipmentSlotFeet,
				EquipmentSlotMainHand,
				EquipmentSlotOffHand,
			} {
				if !st.Equipment[candidate].IsEmpty() {
					targetSlot = candidate
					break
				}
			}
		}
		if targetSlot < 0 {
			dt.mu.Unlock()
			return false
		}
	}

	// Check disabled slots mask.
	if st.DisabledSlots != 0 && (st.DisabledSlots&(1<<targetSlot)) != 0 {
		dt.mu.Unlock()
		return false
	}

	prevStandItem := st.Equipment[targetSlot]
	prevStandMeta := st.EquipmentMeta[targetSlot].Clone()

	if held.IsEmpty() {
		// Unequip from armor stand into player's hand (or inventory).
		st.Equipment[targetSlot] = world.ItemStack{}
		st.EquipmentMeta[targetSlot] = ItemMeta{}
		eq := st.Equipment
		dim := st.Dimension
		dt.mu.Unlock()

		session.SetInventorySlot(invSlot, prevStandItem)
		SetSlotItemMeta(session, invSlot, prevStandMeta)

		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlaySetEquipment,
			EncodeArmorStandEquipmentPacket(entityID, eq))
		return true
	}

	// Equipping or swapping 1 item from player's hand into targetSlot.
	if held.Count > 1 && !prevStandItem.IsEmpty() && snap.GameMode != GameModeCreative {
		// Vanilla swapItem refuses to swap a stacked held item (count > 1) into an already occupied slot.
		dt.mu.Unlock()
		return false
	}

	equipStack := world.NewItemStack(held.ResolvedItemID(), 1)
	if equipStack.IsEmpty() {
		equipStack = world.NewNamedItemStack(held.ResolvedID(), 1)
	}
	st.Equipment[targetSlot] = equipStack
	st.EquipmentMeta[targetSlot] = heldMeta.Clone()
	eq := st.Equipment
	dim := st.Dimension
	dt.mu.Unlock()

	if !prevStandItem.IsEmpty() {
		// Direct swap with the single held item.
		session.SetInventorySlot(invSlot, prevStandItem)
		SetSlotItemMeta(session, invSlot, prevStandMeta)
	} else if held.Count <= 1 {
		session.SetInventorySlot(invSlot, world.ItemStack{})
		SetSlotItemMeta(session, invSlot, ItemMeta{})
	} else {
		session.SetInventorySlot(invSlot, world.NewItemStack(held.ResolvedItemID(), held.Count-1))
	}

	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlaySetEquipment,
		EncodeArmorStandEquipmentPacket(entityID, eq))
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket("minecraft:item.armor.equip_generic", SoundSourcePlayers, ent.X, ent.Y+1.0, ent.Z, 1.0, 1.0, 0))
	return true
}

// AttackArmorStand handles a player left-clicking (attacking) an armor stand:
//   - First hit (or hit > 75 ticks after the previous hit): records the hit tick,
//     broadcasts EntityEventArmorStandHit (32) and entity.armor_stand.hit sound.
//   - Second hit within 75 ticks: breaks the armor stand, drops minecraft:armor_stand
//     and all equipped armor/hand items, and broadcasts entity.armor_stand.break.
func (s *Server) AttackArmorStand(session *PlayerSession, entityID int32) (handled bool, broken bool) {
	if s == nil || s.entities == nil {
		return false, false
	}
	if session != nil {
		snap := session.Snapshot()
		if snap.GameMode == GameModeSpectator || snap.Health <= 0 {
			return false, false
		}
	}
	ent := s.entities.Get(entityID)
	if ent == nil || ent.TypeName != EntityTypeArmorStand {
		return false, false
	}
	if session != nil && !isWithinDecorationReach(session.Snapshot(), ent) {
		return false, false
	}

	gameTime, _ := s.WorldTime()
	// Also combine with entity tracker tick ID so two-hit window works even when
	// tests don't advance the world clock between hits.
	dt := s.decorationTracker()
	dt.mu.Lock()
	st := dt.armorStands[entityID]
	if st == nil || st.Invulnerable {
		dt.mu.Unlock()
		return false, false
	}

	nowTick := gameTime
	if tr := s.entityTracker(); tr != nil {
		if int64(tr.aiTickID) > nowTick {
			nowTick = int64(tr.aiTickID)
		}
	}

	dim := st.Dimension
	cp := ent.ChunkPos()

	if nowTick-st.LastHitTick > ArmorStandTwoHitWindowTicks {
		st.LastHitTick = nowTick
		dt.mu.Unlock()

		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayEntityEvent,
			EncodeEntityEventPacket(entityID, EntityEventArmorStandHit))
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
			EncodeSoundPacket("minecraft:entity.armor_stand.hit", SoundSourceBlocks, ent.X, ent.Y+1.0, ent.Z, 1.0, 1.0, 0))
		return true, false
	}

	eq := st.Equipment
	delete(dt.armorStands, entityID)
	dt.mu.Unlock()

	s.entities.Remove(entityID)
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayEntityEvent,
		EncodeEntityEventPacket(entityID, EntityEventArmorStandHit))
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayRemoveEntities,
		encodeRemoveEntitiesPacket(nil, []int32{entityID}))
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket("minecraft:entity.armor_stand.break", SoundSourceBlocks, ent.X, ent.Y+1.0, ent.Z, 1.0, 1.0, 0))

	if standItem := world.NewNamedItemStack(ArmorStandItemID, 1); !standItem.IsEmpty() {
		s.SpawnItemEntityInDimension(dim, ent.X, ent.Y+0.25, ent.Z, standItem)
	}
	for slot := 0; slot < EquipmentSlotCount; slot++ {
		if !eq[slot].IsEmpty() {
			s.SpawnItemEntityInDimension(dim, ent.X, ent.Y+0.5, ent.Z, eq[slot])
		}
	}
	return true, true
}

// UseDecorationItemOn attempts to place an item_frame, glow_item_frame,
// armor_stand, or painting when session right-clicks block (blockX, blockY, blockZ)
// on face (0..5) using hand (0 = main hand, 1 = offhand).
func (s *Server) UseDecorationItemOn(session *PlayerSession, hand int32, blockX, blockY, blockZ, face int) (entityID int32, placed bool) {
	if s == nil || session == nil || session.IsDead() {
		return 0, false
	}
	_, stack := session.handSlotAndStack(hand)
	if stack.IsEmpty() {
		return 0, false
	}
	switch canonicalResourceName(stack.ResolvedID()) {
	case ItemFrameItemID:
		return s.PlaceItemFrame(session, blockX, blockY, blockZ, face, false)
	case GlowItemFrameItemID:
		return s.PlaceItemFrame(session, blockX, blockY, blockZ, face, true)
	case ArmorStandItemID:
		return s.PlaceArmorStand(session, blockX, blockY, blockZ, face)
	case PaintingItemID:
		id, _, ok := s.PlacePainting(session, blockX, blockY, blockZ, face)
		return id, ok
	default:
		return 0, false
	}
}

// InteractWithDecoration dispatches right-click interaction on an item frame,
// glow item frame, or armor stand.
func (s *Server) InteractWithDecoration(session *PlayerSession, entityID int32, hand int32) bool {
	clickY, hasY := s.consumeInteractAtY(session, entityID)
	return s.InteractWithDecorationAt(session, entityID, hand, 0, clickY, 0, hasY)
}

// InteractWithDecorationAt dispatches right-click interaction on a decoration
// entity with an optional local hit offset (clickX, clickY, clickZ).
func (s *Server) InteractWithDecorationAt(session *PlayerSession, entityID int32, hand int32, clickX, clickY, clickZ float64, hasClickPos bool) bool {
	if s == nil || s.entities == nil || session == nil {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil {
		return false
	}
	switch canonicalResourceName(ent.TypeName) {
	case EntityTypeItemFrame, EntityTypeGlowItemFrame:
		return s.InteractWithItemFrame(session, entityID, hand)
	case EntityTypeArmorStand:
		return s.InteractWithArmorStand(session, entityID, hand, clickY, hasClickPos)
	default:
		return false
	}
}

// AttackDecoration dispatches left-click attack on an item frame, glow item frame,
// armor stand, or painting.
func (s *Server) AttackDecoration(session *PlayerSession, entityID int32) (handled bool, broken bool) {
	if s == nil || s.entities == nil {
		return false, false
	}
	ent := s.entities.Get(entityID)
	if ent == nil {
		return false, false
	}
	switch canonicalResourceName(ent.TypeName) {
	case EntityTypeItemFrame, EntityTypeGlowItemFrame:
		return s.AttackItemFrame(session, entityID)
	case EntityTypeArmorStand:
		return s.AttackArmorStand(session, entityID)
	case EntityTypePainting:
		return s.AttackPainting(session, entityID)
	default:
		return false, false
	}
}

// CheckHangingSupport validates whether the hanging entity (item frame, glow
// item frame, or painting) with entityID still has valid solid wall/block
// support; if not, it breaks the entity and drops its items.
func (s *Server) CheckHangingSupport(entityID int32) (broken bool) {
	if s == nil || s.entities == nil {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil {
		return false
	}
	dt := s.decorationTracker()
	switch canonicalResourceName(ent.TypeName) {
	case EntityTypeItemFrame, EntityTypeGlowItemFrame:
		dt.mu.Lock()
		st := dt.itemFrames[entityID]
		if st == nil || st.Fixed {
			dt.mu.Unlock()
			return false
		}
		dx, dy, dz := DirectionOffset(st.Direction)
		supX, supY, supZ := st.BlockX-dx, st.BlockY-dy, st.BlockZ-dz
		if s.isSupportBlockSolidAt(st.Dimension, supX, supY, supZ) &&
			s.isHangingCellUnobstructedAt(st.Dimension, st.BlockX, st.BlockY, st.BlockZ) {
			st.hadWallSupport = true
			dt.mu.Unlock()
			return false
		}
		glow := st.Glow
		contained := st.Item
		hadMap := st.HasMapID
		mapID := st.MapID
		bx, by, bz := st.BlockX, st.BlockY, st.BlockZ
		delete(dt.itemFrames, entityID)
		dt.mu.Unlock()

		if hadMap {
			s.RemoveItemFrameFromMap(mapID, entityID, bx, by, bz)
		}
		s.breakAndRemoveItemFrame(ent, glow, contained, true)
		return true

	case EntityTypePainting:
		dt.mu.Lock()
		pt := dt.paintings[entityID]
		if pt == nil {
			dt.mu.Unlock()
			return false
		}
		if s.doesPaintingSurviveOnWall(pt.Dimension, pt.BlockX, pt.BlockY, pt.BlockZ, pt.Direction, pt.Variant.Width, pt.Variant.Height) {
			pt.hadWallSupport = true
			dt.mu.Unlock()
			return false
		}
		delete(dt.paintings, entityID)
		dt.mu.Unlock()

		s.breakAndRemovePainting(ent, true)
		return true
	}
	return false
}

// CheckHangingEntitiesAroundBlock checks all hanging entities in dim attached to
// or overlapping (x, y, z) after a block break/change, breaking any that lost support.
func (s *Server) CheckHangingEntitiesAroundBlock(dim string, x, y, z int) (brokenCount int) {
	if s == nil || s.entities == nil {
		return 0
	}
	dim = world.NormalizeDimension(dim)
	dt := s.decorationTracker()
	dt.mu.RLock()
	var ids []int32
	for id, st := range dt.itemFrames {
		if st != nil && world.NormalizeDimension(st.Dimension) == dim {
			ids = append(ids, id)
		}
	}
	for id, pt := range dt.paintings {
		if pt != nil && world.NormalizeDimension(pt.Dimension) == dim {
			ids = append(ids, id)
		}
	}
	dt.mu.RUnlock()

	for _, id := range ids {
		if s.CheckHangingSupport(id) {
			brokenCount++
		}
	}
	return brokenCount
}

// TickDecorations ticks support validation for hanging entities (item frames and
// paintings) and gravity for armor stands.
func (s *Server) TickDecorations() {
	if s == nil || s.entities == nil {
		return
	}
	dt := s.decorationTracker()
	dt.mu.Lock()
	if !dt.loadedDisk && s.cfg.WorldDir != "" {
		dt.loadedDisk = true
		dt.mu.Unlock()
		_ = s.LoadDecorationsFile()
		dt.mu.Lock()
	}

	if len(dt.itemFrames) == 0 && len(dt.paintings) == 0 && len(dt.armorStands) == 0 {
		dt.mu.Unlock()
		return
	}

	var checkFrames []int32
	for id, st := range dt.itemFrames {
		if st == nil {
			delete(dt.itemFrames, id)
			continue
		}
		if s.entities.Get(id) == nil {
			delete(dt.itemFrames, id)
			continue
		}
		dx, dy, dz := DirectionOffset(st.Direction)
		if s.isSupportBlockSolidAt(st.Dimension, st.BlockX-dx, st.BlockY-dy, st.BlockZ-dz) {
			st.hadWallSupport = true
		} else if st.hadWallSupport {
			checkFrames = append(checkFrames, id)
		}
	}

	var checkPaintings []int32
	for id, pt := range dt.paintings {
		if pt == nil {
			delete(dt.paintings, id)
			continue
		}
		if s.entities.Get(id) == nil {
			delete(dt.paintings, id)
			continue
		}
		if s.doesPaintingSurviveOnWall(pt.Dimension, pt.BlockX, pt.BlockY, pt.BlockZ, pt.Direction, pt.Variant.Width, pt.Variant.Height) {
			pt.hadWallSupport = true
		} else if pt.hadWallSupport {
			checkPaintings = append(checkPaintings, id)
		}
	}

	var standIDs []int32
	for id, st := range dt.armorStands {
		if st == nil || s.entities.Get(id) == nil {
			delete(dt.armorStands, id)
			continue
		}
		standIDs = append(standIDs, id)
	}
	dt.mu.Unlock()

	for _, id := range checkFrames {
		s.CheckHangingSupport(id)
	}
	for _, id := range checkPaintings {
		s.CheckHangingSupport(id)
	}
	for _, id := range standIDs {
		ent := s.entities.Get(id)
		if ent == nil {
			continue
		}
		cache := s.DimensionChunks(ent.EffectiveDimension())
		if cache == nil {
			cache = s.chunks
		}
		if cache == nil {
			continue
		}
		yBelow := int(math.Floor(ent.Y - 0.1))
		blockBelow := cache.GetBlock(int(math.Floor(ent.X)), yBelow, int(math.Floor(ent.Z)))
		s.entities.Update(id, func(e *world.Entity) {
			if isPassableBlock(blockBelow) || blockBelow == world.StateWater {
				e.OnGround = false
				e.VelocityY -= 80
				if e.VelocityY < -3000 {
					e.VelocityY = -3000
				}
				e.Y += float64(e.VelocityY) / 8000.0
			} else {
				e.OnGround = true
				e.VelocityY = 0
				e.Y = float64(yBelow + 1)
			}
		})
	}
}

func isWithinDecorationReach(snap PlayerSnapshot, ent *world.Entity) bool {
	if snap.GameMode == GameModeCreative {
		return true
	}
	dx := ent.X - snap.X
	dy := ent.Y - snap.Y
	dz := ent.Z - snap.Z
	return (dx*dx + dy*dy + dz*dz) <= MaxMeleeAttackReach*MaxMeleeAttackReach
}

func (s *Server) consumeDecorationItemFromPlayer(session *PlayerSession, itemID string) {
	if session == nil {
		return
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeCreative {
		return
	}
	norm := canonicalResourceName(itemID)
	mainSlot := session.HeldInventorySlot()
	if mainSlot >= 0 {
		held := session.InventorySlot(mainSlot)
		if !held.IsEmpty() && canonicalResourceName(held.ResolvedID()) == norm {
			rem := held.Count - 1
			if rem <= 0 {
				session.SetInventorySlot(mainSlot, world.ItemStack{})
				SetSlotItemMeta(session, mainSlot, ItemMeta{})
			} else {
				session.SetInventorySlot(mainSlot, world.NewItemStack(held.ResolvedItemID(), rem))
			}
			return
		}
	}
	offHeld := session.InventorySlot(45)
	if !offHeld.IsEmpty() && canonicalResourceName(offHeld.ResolvedID()) == norm {
		rem := offHeld.Count - 1
		if rem <= 0 {
			session.SetInventorySlot(45, world.ItemStack{})
			SetSlotItemMeta(session, 45, ItemMeta{})
		} else {
			session.SetInventorySlot(45, world.NewItemStack(offHeld.ResolvedItemID(), rem))
		}
	}
}

// EncodeDecorationAddEntityPacket encodes ClientboundAddEntityPacket (0x01) for
// an item_frame, glow_item_frame, painting, or armor_stand.
// For hanging entities (item frames and paintings), the trailing Data VarInt is
// the 3D Direction ordinal (0..5) and the coordinates are the block position,
// matching vanilla ItemFrame.getAddEntityPacket and Painting.getAddEntityPacket.
func (s *Server) EncodeDecorationAddEntityPacket(dst []byte, e *world.Entity) []byte {
	if e == nil {
		return dst
	}
	dataVal := int32(0)
	x, y, z := e.X, e.Y, e.Z
	if s != nil {
		dt := s.decorationTracker()
		dt.mu.RLock()
		switch canonicalResourceName(e.TypeName) {
		case EntityTypeItemFrame, EntityTypeGlowItemFrame:
			if st := dt.itemFrames[e.ID]; st != nil {
				dataVal = int32(st.Direction)
				x, y, z = float64(st.BlockX), float64(st.BlockY), float64(st.BlockZ)
			}
		case EntityTypePainting:
			if pt := dt.paintings[e.ID]; pt != nil {
				dataVal = int32(pt.Direction)
				x, y, z = float64(pt.BlockX), float64(pt.BlockY), float64(pt.BlockZ)
			}
		}
		dt.mu.RUnlock()
	}

	dst = protocol.AppendVarInt(dst, e.ID)
	dst = append(dst, e.UUID[:]...)
	dst = protocol.AppendVarInt(dst, int32(e.TypeID))
	dst = binary.BigEndian.AppendUint64(dst, math.Float64bits(x))
	dst = binary.BigEndian.AppendUint64(dst, math.Float64bits(y))
	dst = binary.BigEndian.AppendUint64(dst, math.Float64bits(z))
	dst = append(dst, 0) // zero velocity LPVec3
	dst = append(dst,
		encodeAngleByte(e.Pitch),
		encodeAngleByte(e.Yaw),
		encodeAngleByte(e.HeadYaw),
	)
	dst = protocol.AppendVarInt(dst, dataVal)
	return dst
}

// EncodeItemFrameEntityDataPacket encodes ClientboundSetEntityDataPacket (0x63)
// for an item_frame or glow_item_frame in protocol 775:
//   - index 8 (DIRECTION, serializer 12): VarInt(direction)
//   - index 9 (ITEM_STACK, serializer 7): OptionalItemStack
//   - index 10 (INT, serializer 1): VarInt(rotation)
func EncodeItemFrameEntityDataPacket(entityID int32, direction int, item world.ItemStack, rotation int) []byte {
	return EncodeItemFrameMapEntityDataPacket(entityID, direction, item, rotation, 0, false)
}

// EncodeItemFrameMapEntityDataPacket encodes ClientboundSetEntityDataPacket (0x63)
// for an item_frame or glow_item_frame, including DataComponentMapID (46) when
// hasMapID is true and the framed item is minecraft:filled_map.
func EncodeItemFrameMapEntityDataPacket(entityID int32, direction int, item world.ItemStack, rotation int, mapID int32, hasMapID bool) []byte {
	w := protocol.NewWriter(36)
	w.VarInt(entityID)

	// Index 8: DATA_DIRECTION
	w.Byte(EntityDataHangingDirectionID)
	w.VarInt(EntityDataSerializerDirection)
	w.VarInt(int32(direction))

	// Index 9: DATA_ITEM
	w.Byte(EntityDataItemFrameItemID)
	w.VarInt(EntityDataSerializerItemStack)
	EncodeSlotWithMapID(w, item, mapID, hasMapID)

	// Index 10: DATA_ROTATION
	w.Byte(EntityDataItemFrameRotID)
	w.VarInt(EntityDataSerializerInt)
	w.VarInt(int32(rotation % ItemFrameRotationSteps))

	w.Byte(0xFF)
	return w.Bytes()
}

// EncodePaintingEntityDataPacket encodes ClientboundSetEntityDataPacket (0x63)
// for a painting in protocol 775:
//   - index 8 (DIRECTION, serializer 12): VarInt(direction)
//   - index 9 (PAINTING_VARIANT, serializer 34): Holder<PaintingVariant> = VarInt(variantRegistryIndex + 1)
func EncodePaintingEntityDataPacket(entityID int32, direction int, variantRegistryIndex int) []byte {
	w := protocol.NewWriter(20)
	w.VarInt(entityID)

	// Index 8: DATA_DIRECTION
	w.Byte(EntityDataHangingDirectionID)
	w.VarInt(EntityDataSerializerDirection)
	w.VarInt(int32(direction))

	// Index 9: DATA_PAINTING_VARIANT_ID (Holder.Reference encoded as registryIndex + 1)
	w.Byte(EntityDataPaintingVariantID)
	w.VarInt(EntityDataSerializerPaintingVariant)
	if variantRegistryIndex < 0 {
		variantRegistryIndex = 0
	}
	w.VarInt(int32(variantRegistryIndex + 1))

	w.Byte(0xFF)
	return w.Bytes()
}

// EncodeArmorStandEntityDataPacket encodes ClientboundSetEntityDataPacket (0x63)
// for an armor_stand in protocol 775:
//   - index 15 (BYTE, serializer 0): flags (small, showArms, noBasePlate, marker)
//   - indices 16..21 (ROTATIONS, serializer 9): 6 x (3 x Float32) poses
func EncodeArmorStandEntityDataPacket(st ArmorStandState) []byte {
	w := protocol.NewWriter(96)
	w.VarInt(st.EntityID)

	// Index 15: DATA_CLIENT_FLAGS
	w.Byte(EntityDataArmorStandFlagsID)
	w.VarInt(EntityDataSerializerByte)
	w.Byte(st.Flags)

	poses := [6]struct {
		id  byte
		rot Rotations3f
	}{
		{EntityDataArmorStandHeadPoseID, st.HeadPose},
		{EntityDataArmorStandBodyPoseID, st.BodyPose},
		{EntityDataArmorStandLArmPoseID, st.LeftArmPose},
		{EntityDataArmorStandRArmPoseID, st.RightArmPose},
		{EntityDataArmorStandLLegPoseID, st.LeftLegPose},
		{EntityDataArmorStandRLegPoseID, st.RightLegPose},
	}
	for _, p := range poses {
		w.Byte(p.id)
		w.VarInt(EntityDataSerializerRotations)
		w.Float32(p.rot[0])
		w.Float32(p.rot[1])
		w.Float32(p.rot[2])
	}

	w.Byte(0xFF)
	return w.Bytes()
}

// EncodeArmorStandEquipmentPacket encodes ClientboundSetEquipmentPacket (0x66)
// for all 6 equipment slots (MAINHAND=0 .. HEAD=5) of an armor stand.
func EncodeArmorStandEquipmentPacket(entityID int32, equipment [EquipmentSlotCount]world.ItemStack) []byte {
	w := protocol.NewWriter(64)
	w.VarInt(entityID)
	for slot := 0; slot < EquipmentSlotCount; slot++ {
		slotByte := byte(slot)
		if slot < EquipmentSlotCount-1 {
			slotByte |= 0x80 // CONTINUE_MASK (-128)
		}
		w.Byte(slotByte)
		stack := equipment[slot]
		itemID := stack.ResolvedItemID()
		if stack.IsEmpty() || itemID <= 0 {
			w.VarInt(0)
		} else {
			w.VarInt(stack.Count)
			w.VarInt(itemID)
			w.VarInt(0)
			w.VarInt(0)
		}
	}
	return w.Bytes()
}

// DecorationEntityDataPacket returns the encoded ClientboundSetEntityDataPacket (0x63)
// for an item frame, glow item frame, painting, or armor stand.
func (s *Server) DecorationEntityDataPacket(entityID int32) []byte {
	if s == nil {
		return nil
	}
	dt := s.decorationTracker()
	dt.mu.RLock()
	defer dt.mu.RUnlock()

	if st := dt.itemFrames[entityID]; st != nil {
		return EncodeItemFrameMapEntityDataPacket(entityID, st.Direction, st.Item, st.Rotation, st.MapID, st.HasMapID)
	}
	if pt := dt.paintings[entityID]; pt != nil {
		return EncodePaintingEntityDataPacket(entityID, pt.Direction, pt.Variant.RegistryIndex)
	}
	if as := dt.armorStands[entityID]; as != nil {
		return EncodeArmorStandEntityDataPacket(*as)
	}
	return nil
}

// ArmorStandEquipmentPacket returns the encoded ClientboundSetEquipmentPacket (0x66)
// for the armor stand with entityID, or nil if not found.
func (s *Server) ArmorStandEquipmentPacket(entityID int32) []byte {
	if s == nil {
		return nil
	}
	dt := s.decorationTracker()
	dt.mu.RLock()
	st := dt.armorStands[entityID]
	if st == nil {
		dt.mu.RUnlock()
		return nil
	}
	eq := st.Equipment
	dt.mu.RUnlock()
	return EncodeArmorStandEquipmentPacket(entityID, eq)
}

// SaveDecorationsToNBT serializes all active item frames, glow item frames,
// paintings, and armor stands on s into an NBT Compound tag.
func (s *Server) SaveDecorationsToNBT() *nbt.Compound {
	root := nbt.NewCompound()
	if s == nil || s.entities == nil {
		root.Set("Decorations", nbt.List{ElemID: nbt.TagEnd})
		return root
	}

	dt := s.decorationTracker()
	dt.mu.RLock()
	defer dt.mu.RUnlock()

	var elems []nbt.Tag

	for id, st := range dt.itemFrames {
		if st == nil || s.entities.Get(id) == nil {
			continue
		}
		c := nbt.NewCompound()
		if st.Glow {
			c.Set("id", nbt.String(EntityTypeGlowItemFrame))
		} else {
			c.Set("id", nbt.String(EntityTypeItemFrame))
		}
		c.Set("Dimension", nbt.String(world.NormalizeDimension(st.Dimension)))
		c.Set("TileX", nbt.Int(int32(st.BlockX)))
		c.Set("TileY", nbt.Int(int32(st.BlockY)))
		c.Set("TileZ", nbt.Int(int32(st.BlockZ)))
		c.Set("Facing", nbt.Byte(int8(st.Direction)))
		c.Set("ItemRotation", nbt.Byte(int8(st.Rotation)))
		if st.Fixed {
			c.Set("Fixed", nbt.Byte(1))
		}
		if !st.Item.IsEmpty() {
			itemTag := nbt.NewCompound()
			itemTag.Set("id", nbt.String(st.Item.ResolvedID()))
			itemTag.Set("count", nbt.Int(st.Item.Count))
			if st.HasMapID {
				itemTag.Set("map", nbt.Int(st.MapID))
			}
			c.Set("Item", itemTag)
		}
		elems = append(elems, c)
	}

	for id, pt := range dt.paintings {
		if pt == nil || s.entities.Get(id) == nil {
			continue
		}
		c := nbt.NewCompound()
		c.Set("id", nbt.String(EntityTypePainting))
		c.Set("Dimension", nbt.String(world.NormalizeDimension(pt.Dimension)))
		c.Set("TileX", nbt.Int(int32(pt.BlockX)))
		c.Set("TileY", nbt.Int(int32(pt.BlockY)))
		c.Set("TileZ", nbt.Int(int32(pt.BlockZ)))
		c.Set("facing", nbt.Byte(int8(pt.Direction)))
		c.Set("variant", nbt.String(pt.Variant.ID))
		elems = append(elems, c)
	}

	for id, as := range dt.armorStands {
		ent := s.entities.Get(id)
		if as == nil || ent == nil {
			continue
		}
		c := nbt.NewCompound()
		c.Set("id", nbt.String(EntityTypeArmorStand))
		c.Set("Dimension", nbt.String(world.NormalizeDimension(as.Dimension)))
		c.Set("X", nbt.Double(ent.X))
		c.Set("Y", nbt.Double(ent.Y))
		c.Set("Z", nbt.Double(ent.Z))
		c.Set("Yaw", nbt.Float(ent.Yaw))
		c.Set("Flags", nbt.Byte(int8(as.Flags)))
		c.Set("DisabledSlots", nbt.Int(as.DisabledSlots))

		var eqElems []nbt.Tag
		for slot := 0; slot < EquipmentSlotCount; slot++ {
			st := as.Equipment[slot]
			slotTag := nbt.NewCompound()
			slotTag.Set("Slot", nbt.Byte(int8(slot)))
			if !st.IsEmpty() {
				slotTag.Set("id", nbt.String(st.ResolvedID()))
				slotTag.Set("count", nbt.Int(st.Count))
			}
			eqElems = append(eqElems, slotTag)
		}
		c.Set("Equipment", nbt.List{ElemID: nbt.TagCompound, Elems: eqElems})
		elems = append(elems, c)
	}

	if len(elems) == 0 {
		root.Set("Decorations", nbt.List{ElemID: nbt.TagEnd})
	} else {
		root.Set("Decorations", nbt.List{ElemID: nbt.TagCompound, Elems: elems})
	}
	return root
}

// LoadDecorationsFromNBT restores item frames, glow item frames, paintings, and
// armor stands from an NBT Compound produced by SaveDecorationsToNBT.
func (s *Server) LoadDecorationsFromNBT(root *nbt.Compound) (loaded int) {
	if s == nil || s.entities == nil || root == nil {
		return 0
	}
	listTag, ok := root.Get("Decorations")
	if !ok {
		return 0
	}
	list, ok := listTag.(nbt.List)
	if !ok {
		return 0
	}

	for _, elem := range list.Elems {
		c, ok := elem.(*nbt.Compound)
		if !ok || c == nil {
			continue
		}
		idType := nbtStringField(c, "id")
		dim := nbtStringField(c, "Dimension")
		if dim == "" {
			dim = world.DimensionOverworld
		}

		switch canonicalResourceName(idType) {
		case EntityTypeItemFrame, EntityTypeGlowItemFrame:
			glow := canonicalResourceName(idType) == EntityTypeGlowItemFrame
			bx := int(nbtIntField(c, "TileX"))
			by := int(nbtIntField(c, "TileY"))
			bz := int(nbtIntField(c, "TileZ"))
			dir := int(nbtByteField(c, "Facing"))
			rot := int(nbtByteField(c, "ItemRotation"))
			fixed := nbtByteField(c, "Fixed") != 0

			entID, err := s.SpawnItemFrame(dim, bx, by, bz, dir, glow)
			if err != nil || entID == 0 {
				continue
			}
			var itemStack world.ItemStack
			var mapID int32
			var hasMapID bool
			if itemRaw, hasItem := c.Get("Item"); hasItem {
				if itemComp, ok := itemRaw.(*nbt.Compound); ok && itemComp != nil {
					itemName := nbtStringField(itemComp, "id")
					count := nbtIntField(itemComp, "count")
					if count <= 0 {
						count = 1
					}
					itemStack = world.NewNamedItemStack(itemName, count)
					if _, hasMap := itemComp.Get("map"); hasMap {
						mapID = nbtIntField(itemComp, "map")
						hasMapID = true
					}
				}
			}
			dt := s.decorationTracker()
			dt.mu.Lock()
			if st := dt.itemFrames[entID]; st != nil {
				st.Item = itemStack
				st.MapID = mapID
				st.HasMapID = hasMapID
				st.Rotation = rot % ItemFrameRotationSteps
				st.Fixed = fixed
			}
			dt.mu.Unlock()
			if hasMapID {
				s.AddItemFrameToMap(mapID, entID, bx, by, bz, dir)
			}
			loaded++

		case EntityTypePainting:
			bx := int(nbtIntField(c, "TileX"))
			by := int(nbtIntField(c, "TileY"))
			bz := int(nbtIntField(c, "TileZ"))
			dir := int(nbtByteField(c, "facing"))
			variant := nbtStringField(c, "variant")
			if variant == "" {
				variant = "minecraft:kebab"
			}
			entID, err := s.SpawnPainting(dim, bx, by, bz, dir, variant)
			if err != nil || entID == 0 {
				continue
			}
			loaded++

		case EntityTypeArmorStand:
			x := nbtDoubleField(c, "X")
			y := nbtDoubleField(c, "Y")
			z := nbtDoubleField(c, "Z")
			yaw := nbtFloatField(c, "Yaw")
			flags := byte(nbtByteField(c, "Flags"))
			disabled := nbtIntField(c, "DisabledSlots")

			entID, err := s.SpawnArmorStand(dim, x, y, z, yaw)
			if err != nil || entID == 0 {
				continue
			}
			dt := s.decorationTracker()
			dt.mu.Lock()
			if st := dt.armorStands[entID]; st != nil {
				if flags != 0 {
					st.Flags = flags
				}
				st.DisabledSlots = disabled
				if eqRaw, hasEq := c.Get("Equipment"); hasEq {
					if eqList, ok := eqRaw.(nbt.List); ok {
						for _, slotElem := range eqList.Elems {
							sc, ok := slotElem.(*nbt.Compound)
							if !ok || sc == nil {
								continue
							}
							slotIdx := int(nbtByteField(sc, "Slot"))
							itemName := nbtStringField(sc, "id")
							count := nbtIntField(sc, "count")
							if slotIdx >= 0 && slotIdx < EquipmentSlotCount && itemName != "" && count > 0 {
								st.Equipment[slotIdx] = world.NewNamedItemStack(itemName, count)
							}
						}
					}
				}
			}
			dt.mu.Unlock()
			loaded++
		}
	}
	return loaded
}

// SaveDecorationsFile persists all decorative entities to <WorldDir>/decorations.nbt
// when WorldDir is configured.
func (s *Server) SaveDecorationsFile() error {
	if s == nil || s.cfg.WorldDir == "" {
		return nil
	}
	comp := s.SaveDecorationsToNBT()
	data := nbt.MarshalNamed("", comp)
	if err := os.MkdirAll(s.cfg.WorldDir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(s.cfg.WorldDir, "decorations.nbt")
	return os.WriteFile(path, data, 0o644)
}

// LoadDecorationsFile loads saved decorative entities from <WorldDir>/decorations.nbt
// when present.
func (s *Server) LoadDecorationsFile() error {
	if s == nil || s.cfg.WorldDir == "" {
		return nil
	}
	path := filepath.Join(s.cfg.WorldDir, "decorations.nbt")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	_, tag, err := nbt.UnmarshalNamed(data)
	if err != nil {
		return err
	}
	comp, ok := tag.(*nbt.Compound)
	if !ok || comp == nil {
		return nil
	}
	dt := s.decorationTracker()
	dt.mu.Lock()
	dt.loadedDisk = true
	dt.mu.Unlock()
	s.LoadDecorationsFromNBT(comp)
	return nil
}

func nbtStringField(c *nbt.Compound, key string) string {
	if t, ok := c.Get(key); ok {
		if s, ok := t.(nbt.String); ok {
			return string(s)
		}
	}
	return ""
}

func nbtIntField(c *nbt.Compound, key string) int32 {
	if t, ok := c.Get(key); ok {
		switch v := t.(type) {
		case nbt.Int:
			return int32(v)
		case nbt.Short:
			return int32(v)
		case nbt.Byte:
			return int32(v)
		}
	}
	return 0
}

func nbtByteField(c *nbt.Compound, key string) int8 {
	if t, ok := c.Get(key); ok {
		if v, ok := t.(nbt.Byte); ok {
			return int8(v)
		}
	}
	return 0
}

func nbtFloatField(c *nbt.Compound, key string) float32 {
	if t, ok := c.Get(key); ok {
		if v, ok := t.(nbt.Float); ok {
			return float32(v)
		}
	}
	return 0
}

func nbtDoubleField(c *nbt.Compound, key string) float64 {
	if t, ok := c.Get(key); ok {
		if v, ok := t.(nbt.Double); ok {
			return float64(v)
		}
	}
	return 0
}
