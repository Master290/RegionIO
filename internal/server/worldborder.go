package server

import (
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"regionio/internal/nbt"
	"regionio/internal/protocol"
)

// Clientbound Play packet IDs for World Border (protocol 775 / Minecraft 26.1.2).
const (
	PlayInitializeBorder         = protocol.PlayInitializeBorder
	PlaySetBorderCenter          = protocol.PlaySetBorderCenter
	PlaySetBorderLerpSize        = protocol.PlaySetBorderLerpSize
	PlaySetBorderSize            = protocol.PlaySetBorderSize
	PlaySetBorderWarningDelay    = protocol.PlaySetBorderWarningDelay
	PlaySetBorderWarningDistance = protocol.PlaySetBorderWarningDistance
)

// Vanilla 26.1.2 WorldBorder constants and defaults.
const (
	DefaultBorderCenterX         = 0.0
	DefaultBorderCenterZ         = 0.0
	DefaultBorderSize            = 59999968.0
	MinBorderSize                = 1.0
	MaxBorderSize                = 59999968.0
	MaxBorderCenterCoord         = 29999984.0
	DefaultBorderAbsoluteMaxSize = 29999984
	DefaultBorderDamagePerBlock  = 0.2
	DefaultBorderSafeZone        = 5.0
	DefaultBorderWarningBlocks   = 5
	DefaultBorderWarningTime     = 15
	BorderDamageIntervalTicks    = 20
	borderClampEpsilon           = 1e-5
)

// BorderStatus represents whether the world border is shrinking, growing, or stationary.
type BorderStatus uint8

const (
	BorderStatusShrinking BorderStatus = iota
	BorderStatusGrowing
	BorderStatusStationary
)

// Color returns the vanilla RGB color associated with the border status.
func (s BorderStatus) Color() int32 {
	switch s {
	case BorderStatusShrinking:
		return 0xFF3030
	case BorderStatusGrowing:
		return 0x40FF80
	default:
		return 0x20A0FF
	}
}

func (s BorderStatus) String() string {
	switch s {
	case BorderStatusShrinking:
		return "shrinking"
	case BorderStatusGrowing:
		return "growing"
	default:
		return "stationary"
	}
}

// WorldBorderSettings holds the serializable configuration of a WorldBorder,
// matching net.minecraft.world.level.border.WorldBorder$Settings.CODEC in 26.1.2.
type WorldBorderSettings struct {
	CenterX        float64
	CenterZ        float64
	DamagePerBlock float64
	SafeZone       float64
	WarningBlocks  int32
	WarningTime    int32
	Size           float64
	LerpTime       int64
	LerpTarget     float64
}

// DefaultWorldBorderSettings returns the vanilla 26.1.2 default world border settings.
func DefaultWorldBorderSettings() WorldBorderSettings {
	return WorldBorderSettings{
		CenterX:        DefaultBorderCenterX,
		CenterZ:        DefaultBorderCenterZ,
		DamagePerBlock: DefaultBorderDamagePerBlock,
		SafeZone:       DefaultBorderSafeZone,
		WarningBlocks:  DefaultBorderWarningBlocks,
		WarningTime:    DefaultBorderWarningTime,
		Size:           DefaultBorderSize,
		LerpTime:       0,
		LerpTarget:     DefaultBorderSize,
	}
}

// ToNBT serializes WorldBorderSettings to an *nbt.Compound using vanilla 26.1.2 keys.
func (s WorldBorderSettings) ToNBT() *nbt.Compound {
	c := nbt.NewCompound()
	c.Set("center_x", nbt.Double(s.CenterX))
	c.Set("center_z", nbt.Double(s.CenterZ))
	c.Set("damage_per_block", nbt.Double(s.DamagePerBlock))
	c.Set("safe_zone", nbt.Double(s.SafeZone))
	c.Set("warning_blocks", nbt.Int(s.WarningBlocks))
	c.Set("warning_time", nbt.Int(s.WarningTime))
	c.Set("size", nbt.Double(s.Size))
	c.Set("lerp_time", nbt.Long(s.LerpTime))
	c.Set("lerp_target", nbt.Double(s.LerpTarget))
	return c
}

// WorldBorderSettingsFromNBT decodes WorldBorderSettings from an *nbt.Compound,
// falling back to DefaultWorldBorderSettings for missing fields and supporting
// both 26.1.2 snake_case keys and legacy PascalCase keys.
func WorldBorderSettingsFromNBT(c *nbt.Compound) WorldBorderSettings {
	cfg := DefaultWorldBorderSettings()
	if c == nil {
		return cfg
	}
	if t, ok := c.Get("data"); ok {
		if inner, ok := t.(*nbt.Compound); ok && inner != nil {
			c = inner
		}
	}
	if t, ok := c.Get("world_border"); ok {
		if sub, ok := t.(*nbt.Compound); ok && sub != nil {
			c = sub
		}
	}
	if v, ok := nbtFloat64(c, "center_x", "BorderCenterX"); ok {
		cfg.CenterX = clampFloat64(v, -MaxBorderCenterCoord, MaxBorderCenterCoord)
	}
	if v, ok := nbtFloat64(c, "center_z", "BorderCenterZ"); ok {
		cfg.CenterZ = clampFloat64(v, -MaxBorderCenterCoord, MaxBorderCenterCoord)
	}
	if v, ok := nbtFloat64(c, "damage_per_block", "BorderDamagePerBlock"); ok && v >= 0 {
		cfg.DamagePerBlock = v
	}
	if v, ok := nbtFloat64(c, "safe_zone", "BorderSafeZone"); ok && v >= 0 {
		cfg.SafeZone = v
	}
	if v, ok := nbtInt32(c, "warning_blocks", "BorderWarningBlocks"); ok && v >= 0 {
		cfg.WarningBlocks = v
	}
	if v, ok := nbtInt32(c, "warning_time", "BorderWarningTime"); ok && v >= 0 {
		cfg.WarningTime = v
	}
	if v, ok := nbtFloat64(c, "size", "BorderSize"); ok {
		cfg.Size = clampFloat64(v, MinBorderSize, MaxBorderSize)
		cfg.LerpTarget = cfg.Size
	}
	if v, ok := nbtInt64(c, "lerp_time", "BorderSizeLerpTime"); ok && v >= 0 {
		cfg.LerpTime = v
	}
	if v, ok := nbtFloat64(c, "lerp_target", "BorderSizeLerpTarget"); ok {
		cfg.LerpTarget = clampFloat64(v, MinBorderSize, MaxBorderSize)
	}
	return cfg
}

func nbtFloat64(c *nbt.Compound, keys ...string) (float64, bool) {
	if c == nil {
		return 0, false
	}
	for _, k := range keys {
		t, ok := c.Get(k)
		if !ok {
			continue
		}
		switch v := t.(type) {
		case nbt.Double:
			return float64(v), true
		case nbt.Float:
			return float64(v), true
		case nbt.Int:
			return float64(v), true
		case nbt.Long:
			return float64(v), true
		}
	}
	return 0, false
}

func nbtInt32(c *nbt.Compound, keys ...string) (int32, bool) {
	if c == nil {
		return 0, false
	}
	for _, k := range keys {
		t, ok := c.Get(k)
		if !ok {
			continue
		}
		switch v := t.(type) {
		case nbt.Int:
			return int32(v), true
		case nbt.Short:
			return int32(v), true
		case nbt.Byte:
			return int32(v), true
		case nbt.Long:
			return int32(v), true
		}
	}
	return 0, false
}

func nbtInt64(c *nbt.Compound, keys ...string) (int64, bool) {
	if c == nil {
		return 0, false
	}
	for _, k := range keys {
		t, ok := c.Get(k)
		if !ok {
			continue
		}
		switch v := t.(type) {
		case nbt.Long:
			return int64(v), true
		case nbt.Int:
			return int64(v), true
		}
	}
	return 0, false
}

// WorldBorder models the vanilla Minecraft 26.1.2 world border state, including
// static and moving (lerping) extents, bounds calculation, distance queries,
// position clamping, and outside-border damage computation.
type WorldBorder struct {
	mu              sync.RWMutex
	centerX         float64
	centerZ         float64
	size            float64
	previousSize    float64
	lerpFrom        float64
	lerpTarget      float64
	lerpDuration    float64
	lerpRemaining   int64
	absoluteMaxSize int32
	damagePerBlock  float64
	safeZone        float64
	warningBlocks   int32
	warningTime     int32
}

// NewWorldBorder constructs a WorldBorder initialized with vanilla 26.1.2 defaults.
func NewWorldBorder() *WorldBorder {
	wb := &WorldBorder{
		centerX:         DefaultBorderCenterX,
		centerZ:         DefaultBorderCenterZ,
		size:            DefaultBorderSize,
		previousSize:    DefaultBorderSize,
		lerpFrom:        DefaultBorderSize,
		lerpTarget:      DefaultBorderSize,
		lerpDuration:    0,
		lerpRemaining:   0,
		absoluteMaxSize: DefaultBorderAbsoluteMaxSize,
		damagePerBlock:  DefaultBorderDamagePerBlock,
		safeZone:        DefaultBorderSafeZone,
		warningBlocks:   DefaultBorderWarningBlocks,
		warningTime:     DefaultBorderWarningTime,
	}
	return wb
}

// CenterX returns the X coordinate of the world border center.
func (wb *WorldBorder) CenterX() float64 {
	wb.mu.RLock()
	defer wb.mu.RUnlock()
	return wb.centerX
}

// CenterZ returns the Z coordinate of the world border center.
func (wb *WorldBorder) CenterZ() float64 {
	wb.mu.RLock()
	defer wb.mu.RUnlock()
	return wb.centerZ
}

// Center returns the (X, Z) coordinates of the world border center.
func (wb *WorldBorder) Center() (float64, float64) {
	wb.mu.RLock()
	defer wb.mu.RUnlock()
	return wb.centerX, wb.centerZ
}

// SetCenter sets the world border center coordinates, clamped to [-MaxBorderCenterCoord, MaxBorderCenterCoord].
func (wb *WorldBorder) SetCenter(x, z float64) {
	wb.mu.Lock()
	wb.centerX = clampFloat64(x, -MaxBorderCenterCoord, MaxBorderCenterCoord)
	wb.centerZ = clampFloat64(z, -MaxBorderCenterCoord, MaxBorderCenterCoord)
	wb.mu.Unlock()
}

// Size returns the current diameter (width) of the world border in blocks.
func (wb *WorldBorder) Size() float64 {
	wb.mu.RLock()
	defer wb.mu.RUnlock()
	return wb.size
}

// PreviousSize returns the border diameter from the previous game tick.
func (wb *WorldBorder) PreviousSize() float64 {
	wb.mu.RLock()
	defer wb.mu.RUnlock()
	return wb.previousSize
}

// LerpTarget returns the target diameter of an active border size transition,
// or the current size when stationary.
func (wb *WorldBorder) LerpTarget() float64 {
	wb.mu.RLock()
	defer wb.mu.RUnlock()
	if wb.lerpRemaining > 0 {
		return wb.lerpTarget
	}
	return wb.size
}

// LerpTime returns the remaining ticks for an active border size transition (0 if stationary).
func (wb *WorldBorder) LerpTime() int64 {
	wb.mu.RLock()
	defer wb.mu.RUnlock()
	return wb.lerpRemaining
}

// LerpSpeed returns the rate of size change per tick (0 if stationary).
func (wb *WorldBorder) LerpSpeed() float64 {
	wb.mu.RLock()
	defer wb.mu.RUnlock()
	if wb.lerpRemaining <= 0 || wb.lerpDuration <= 0 {
		return 0
	}
	return math.Abs(wb.lerpFrom-wb.lerpTarget) / wb.lerpDuration
}

// Status returns whether the border is shrinking, growing, or stationary.
func (wb *WorldBorder) Status() BorderStatus {
	wb.mu.RLock()
	defer wb.mu.RUnlock()
	if wb.lerpRemaining <= 0 {
		return BorderStatusStationary
	}
	if wb.lerpTarget < wb.lerpFrom {
		return BorderStatusShrinking
	}
	if wb.lerpTarget > wb.lerpFrom {
		return BorderStatusGrowing
	}
	return BorderStatusStationary
}

// SetSize immediately sets the world border to a static diameter in [MinBorderSize, MaxBorderSize].
func (wb *WorldBorder) SetSize(size float64) {
	clamped := clampFloat64(size, MinBorderSize, MaxBorderSize)
	wb.mu.Lock()
	wb.size = clamped
	wb.previousSize = clamped
	wb.lerpFrom = clamped
	wb.lerpTarget = clamped
	wb.lerpDuration = 0
	wb.lerpRemaining = 0
	wb.mu.Unlock()
}

// LerpSizeBetween transitions the world border diameter from oldSize to newSize
// over lerpTicks game ticks (matching WorldBorder.lerpSizeBetween in 26.1.2).
// If lerpTicks <= 0 or oldSize == newSize, the border snaps immediately to newSize.
func (wb *WorldBorder) LerpSizeBetween(oldSize, newSize float64, lerpTicks int64) {
	from := clampFloat64(oldSize, MinBorderSize, MaxBorderSize)
	to := clampFloat64(newSize, MinBorderSize, MaxBorderSize)
	if lerpTicks <= 0 || from == to {
		wb.SetSize(to)
		return
	}
	wb.mu.Lock()
	wb.size = from
	wb.previousSize = from
	wb.lerpFrom = from
	wb.lerpTarget = to
	wb.lerpDuration = float64(lerpTicks)
	wb.lerpRemaining = lerpTicks
	wb.mu.Unlock()
}

// Tick advances a moving world border by one game tick, matching
// WorldBorder$MovingBorderExtent.update() in Minecraft 26.1.2.
func (wb *WorldBorder) Tick() {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	if wb.lerpRemaining <= 0 {
		wb.previousSize = wb.size
		return
	}
	wb.lerpRemaining--
	wb.previousSize = wb.size
	if wb.lerpRemaining <= 0 || wb.lerpDuration <= 0 {
		wb.size = wb.lerpTarget
		wb.lerpFrom = wb.lerpTarget
		wb.lerpDuration = 0
		wb.lerpRemaining = 0
		return
	}
	progress := (wb.lerpDuration - float64(wb.lerpRemaining)) / wb.lerpDuration
	if progress >= 1.0 {
		wb.size = wb.lerpTarget
	} else if progress <= 0.0 {
		wb.size = wb.lerpFrom
	} else {
		wb.size = wb.lerpFrom + progress*(wb.lerpTarget-wb.lerpFrom)
	}
}

// AbsoluteMaxSize returns the maximum coordinate radius of the border (default 29999984).
func (wb *WorldBorder) AbsoluteMaxSize() int32 {
	wb.mu.RLock()
	defer wb.mu.RUnlock()
	return wb.absoluteMaxSize
}

// SetAbsoluteMaxSize updates the maximum coordinate radius of the border.
func (wb *WorldBorder) SetAbsoluteMaxSize(maxSize int32) {
	if maxSize < 1 {
		maxSize = 1
	}
	wb.mu.Lock()
	wb.absoluteMaxSize = maxSize
	wb.mu.Unlock()
}

// DamagePerBlock returns the damage dealt per block per second outside the border buffer.
func (wb *WorldBorder) DamagePerBlock() float64 {
	wb.mu.RLock()
	defer wb.mu.RUnlock()
	return wb.damagePerBlock
}

// SetDamagePerBlock sets the damage dealt per block outside the border buffer.
func (wb *WorldBorder) SetDamagePerBlock(dmg float64) {
	if dmg < 0 {
		dmg = 0
	}
	wb.mu.Lock()
	wb.damagePerBlock = dmg
	wb.mu.Unlock()
}

// SafeZone returns the damage buffer distance outside the border before damage begins.
func (wb *WorldBorder) SafeZone() float64 {
	wb.mu.RLock()
	defer wb.mu.RUnlock()
	return wb.safeZone
}

// SetSafeZone sets the damage buffer distance outside the border.
func (wb *WorldBorder) SetSafeZone(safeZone float64) {
	if safeZone < 0 {
		safeZone = 0
	}
	wb.mu.Lock()
	wb.safeZone = safeZone
	wb.mu.Unlock()
}

// WarningBlocks returns the warning distance in blocks.
func (wb *WorldBorder) WarningBlocks() int32 {
	wb.mu.RLock()
	defer wb.mu.RUnlock()
	return wb.warningBlocks
}

// SetWarningBlocks sets the warning distance in blocks.
func (wb *WorldBorder) SetWarningBlocks(blocks int32) {
	if blocks < 0 {
		blocks = 0
	}
	wb.mu.Lock()
	wb.warningBlocks = blocks
	wb.mu.Unlock()
}

// WarningTime returns the warning time in ticks/seconds.
func (wb *WorldBorder) WarningTime() int32 {
	wb.mu.RLock()
	defer wb.mu.RUnlock()
	return wb.warningTime
}

// SetWarningTime sets the warning time.
func (wb *WorldBorder) SetWarningTime(warningTime int32) {
	if warningTime < 0 {
		warningTime = 0
	}
	wb.mu.Lock()
	wb.warningTime = warningTime
	wb.mu.Unlock()
}

func (wb *WorldBorder) boundsLocked() (minX, maxX, minZ, maxZ float64) {
	absMax := float64(wb.absoluteMaxSize)
	half := wb.size / 2.0
	minX = clampFloat64(wb.centerX-half, -absMax, absMax)
	maxX = clampFloat64(wb.centerX+half, -absMax, absMax)
	minZ = clampFloat64(wb.centerZ-half, -absMax, absMax)
	maxZ = clampFloat64(wb.centerZ+half, -absMax, absMax)
	return minX, maxX, minZ, maxZ
}

// MinX returns the minimum X coordinate of the world border.
func (wb *WorldBorder) MinX() float64 {
	wb.mu.RLock()
	defer wb.mu.RUnlock()
	minX, _, _, _ := wb.boundsLocked()
	return minX
}

// MaxX returns the maximum X coordinate of the world border.
func (wb *WorldBorder) MaxX() float64 {
	wb.mu.RLock()
	defer wb.mu.RUnlock()
	_, maxX, _, _ := wb.boundsLocked()
	return maxX
}

// MinZ returns the minimum Z coordinate of the world border.
func (wb *WorldBorder) MinZ() float64 {
	wb.mu.RLock()
	defer wb.mu.RUnlock()
	_, _, minZ, _ := wb.boundsLocked()
	return minZ
}

// MaxZ returns the maximum Z coordinate of the world border.
func (wb *WorldBorder) MaxZ() float64 {
	wb.mu.RLock()
	defer wb.mu.RUnlock()
	_, _, _, maxZ := wb.boundsLocked()
	return maxZ
}

// DistanceToBorder returns the signed distance from (x, z) to the closest border edge.
// Positive values indicate (x, z) is inside the border; negative values indicate it is outside.
func (wb *WorldBorder) DistanceToBorder(x, z float64) float64 {
	wb.mu.RLock()
	minX, maxX, minZ, maxZ := wb.boundsLocked()
	wb.mu.RUnlock()

	dNorth := z - minZ
	dSouth := maxZ - z
	dWest := x - minX
	dEast := maxX - x
	return math.Min(math.Min(dWest, dEast), math.Min(dNorth, dSouth))
}

// IsWithinBounds reports whether (x, z) lies inside the world border.
func (wb *WorldBorder) IsWithinBounds(x, z float64) bool {
	return wb.IsWithinBoundsMargin(x, z, 0)
}

// IsWithinBoundsMargin reports whether (x, z) lies inside the world border expanded by margin.
func (wb *WorldBorder) IsWithinBoundsMargin(x, z, margin float64) bool {
	wb.mu.RLock()
	minX, maxX, minZ, maxZ := wb.boundsLocked()
	wb.mu.RUnlock()
	return x >= minX-margin && x < maxX+margin && z >= minZ-margin && z < maxZ+margin
}

// IsBlockWithinBounds reports whether block position (bx, bz) is inside the world border.
func (wb *WorldBorder) IsBlockWithinBounds(bx, bz int) bool {
	return wb.IsWithinBounds(float64(bx), float64(bz))
}

// IsChunkWithinBounds reports whether chunk (cx, cz) is inside the world border.
func (wb *WorldBorder) IsChunkWithinBounds(cx, cz int32) bool {
	minBX := float64(cx << 4)
	minBZ := float64(cz << 4)
	maxBX := float64((cx << 4) + 15)
	maxBZ := float64((cz << 4) + 15)
	return wb.IsWithinBounds(minBX, minBZ) && wb.IsWithinBounds(maxBX, maxBZ)
}

// ClampToBounds clamps (x, y, z) to remain strictly inside [MinX, MaxX - 1e-5] x [MinZ, MaxZ - 1e-5],
// matching WorldBorder.clampVec3ToBound in Minecraft 26.1.2.
func (wb *WorldBorder) ClampToBounds(x, y, z float64) (float64, float64, float64) {
	wb.mu.RLock()
	minX, maxX, minZ, maxZ := wb.boundsLocked()
	wb.mu.RUnlock()

	upperX := maxX - borderClampEpsilon
	if upperX < minX {
		upperX = minX
	}
	upperZ := maxZ - borderClampEpsilon
	if upperZ < minZ {
		upperZ = minZ
	}
	return clampFloat64(x, minX, upperX), y, clampFloat64(z, minZ, upperZ)
}

// CalculateDamage computes the outside-border damage for a position (x, z).
// In vanilla 26.1.2 (ServerPlayer.tick / LivingEntity), if distanceToBorder + safeZone < 0
// and damagePerBlock > 0, damage is max(1.0, floor(-dist * damagePerBlock)).
// Returns 0 if inside the border or within the safeZone buffer.
func (wb *WorldBorder) CalculateDamage(x, z float64) float32 {
	wb.mu.RLock()
	minX, maxX, minZ, maxZ := wb.boundsLocked()
	safeZone := wb.safeZone
	dmgPerBlock := wb.damagePerBlock
	wb.mu.RUnlock()

	if dmgPerBlock <= 0.0 {
		return 0
	}
	dNorth := z - minZ
	dSouth := maxZ - z
	dWest := x - minX
	dEast := maxX - x
	dist := math.Min(math.Min(dWest, dEast), math.Min(dNorth, dSouth)) + safeZone
	if dist >= 0.0 {
		return 0
	}
	dmg := math.Floor(-dist * dmgPerBlock)
	if dmg < 1.0 {
		dmg = 1.0
	}
	return float32(dmg)
}

// Settings returns a snapshot of the WorldBorder's current settings for persistence.
func (wb *WorldBorder) Settings() WorldBorderSettings {
	wb.mu.RLock()
	defer wb.mu.RUnlock()
	target := wb.size
	if wb.lerpRemaining > 0 {
		target = wb.lerpTarget
	}
	return WorldBorderSettings{
		CenterX:        wb.centerX,
		CenterZ:        wb.centerZ,
		DamagePerBlock: wb.damagePerBlock,
		SafeZone:       wb.safeZone,
		WarningBlocks:  wb.warningBlocks,
		WarningTime:    wb.warningTime,
		Size:           wb.size,
		LerpTime:       wb.lerpRemaining,
		LerpTarget:     target,
	}
}

// ApplySettings applies a WorldBorderSettings snapshot to wb.
func (wb *WorldBorder) ApplySettings(cfg WorldBorderSettings) {
	wb.mu.Lock()
	wb.centerX = clampFloat64(cfg.CenterX, -MaxBorderCenterCoord, MaxBorderCenterCoord)
	wb.centerZ = clampFloat64(cfg.CenterZ, -MaxBorderCenterCoord, MaxBorderCenterCoord)
	if cfg.DamagePerBlock >= 0 {
		wb.damagePerBlock = cfg.DamagePerBlock
	}
	if cfg.SafeZone >= 0 {
		wb.safeZone = cfg.SafeZone
	}
	if cfg.WarningBlocks >= 0 {
		wb.warningBlocks = cfg.WarningBlocks
	}
	if cfg.WarningTime >= 0 {
		wb.warningTime = cfg.WarningTime
	}
	size := clampFloat64(cfg.Size, MinBorderSize, MaxBorderSize)
	target := clampFloat64(cfg.LerpTarget, MinBorderSize, MaxBorderSize)
	if cfg.LerpTime > 0 && size != target {
		wb.size = size
		wb.previousSize = size
		wb.lerpFrom = size
		wb.lerpTarget = target
		wb.lerpDuration = float64(cfg.LerpTime)
		wb.lerpRemaining = cfg.LerpTime
	} else {
		wb.size = size
		wb.previousSize = size
		wb.lerpFrom = size
		wb.lerpTarget = size
		wb.lerpDuration = 0
		wb.lerpRemaining = 0
	}
	wb.mu.Unlock()
}

func clampFloat64(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// --- Wire Packet Encoders & Decoders (Protocol 775 / 26.1.2) ---

// InitializeBorderPacket holds the decoded fields of ClientboundInitializeBorderPacket (0x2b).
type InitializeBorderPacket struct {
	NewCenterX         float64
	NewCenterZ         float64
	OldSize            float64
	NewSize            float64
	LerpTime           int64
	NewAbsoluteMaxSize int32
	WarningBlocks      int32
	WarningTime        int32
}

// EncodeInitializeBorderPacket encodes ClientboundInitializeBorderPacket (0x2b) for wb.
func EncodeInitializeBorderPacket(wb *WorldBorder) []byte {
	if wb == nil {
		wb = NewWorldBorder()
	}
	wb.mu.RLock()
	pkt := InitializeBorderPacket{
		NewCenterX:         wb.centerX,
		NewCenterZ:         wb.centerZ,
		OldSize:            wb.size,
		NewSize:            wb.size,
		LerpTime:           wb.lerpRemaining,
		NewAbsoluteMaxSize: wb.absoluteMaxSize,
		WarningBlocks:      wb.warningBlocks,
		WarningTime:        wb.warningTime,
	}
	if wb.lerpRemaining > 0 {
		pkt.NewSize = wb.lerpTarget
	}
	wb.mu.RUnlock()
	return EncodeInitializeBorderPacketFields(pkt)
}

// EncodeInitializeBorderPacketFields encodes ClientboundInitializeBorderPacket (0x2b) from explicit fields.
func EncodeInitializeBorderPacketFields(pkt InitializeBorderPacket) []byte {
	w := protocol.NewWriter(48)
	w.Float64(pkt.NewCenterX)
	w.Float64(pkt.NewCenterZ)
	w.Float64(pkt.OldSize)
	w.Float64(pkt.NewSize)
	w.VarLong(pkt.LerpTime)
	w.VarInt(pkt.NewAbsoluteMaxSize)
	w.VarInt(pkt.WarningBlocks)
	w.VarInt(pkt.WarningTime)
	return w.Bytes()
}

// DecodeInitializeBorderPacket decodes a ClientboundInitializeBorderPacket (0x2b) payload.
func DecodeInitializeBorderPacket(data []byte) (InitializeBorderPacket, error) {
	r := protocol.NewReader(data)
	var pkt InitializeBorderPacket
	var err error
	if pkt.NewCenterX, err = r.Float64(); err != nil {
		return pkt, err
	}
	if pkt.NewCenterZ, err = r.Float64(); err != nil {
		return pkt, err
	}
	if pkt.OldSize, err = r.Float64(); err != nil {
		return pkt, err
	}
	if pkt.NewSize, err = r.Float64(); err != nil {
		return pkt, err
	}
	if pkt.LerpTime, err = r.VarLong(); err != nil {
		return pkt, err
	}
	if pkt.NewAbsoluteMaxSize, err = r.VarInt(); err != nil {
		return pkt, err
	}
	if pkt.WarningBlocks, err = r.VarInt(); err != nil {
		return pkt, err
	}
	if pkt.WarningTime, err = r.VarInt(); err != nil {
		return pkt, err
	}
	return pkt, nil
}

// EncodeSetBorderCenterPacket encodes ClientboundSetBorderCenterPacket (0x58).
func EncodeSetBorderCenterPacket(centerX, centerZ float64) []byte {
	w := protocol.NewWriter(16)
	w.Float64(centerX)
	w.Float64(centerZ)
	return w.Bytes()
}

// DecodeSetBorderCenterPacket decodes ClientboundSetBorderCenterPacket (0x58).
func DecodeSetBorderCenterPacket(data []byte) (centerX, centerZ float64, err error) {
	r := protocol.NewReader(data)
	if centerX, err = r.Float64(); err != nil {
		return 0, 0, err
	}
	if centerZ, err = r.Float64(); err != nil {
		return 0, 0, err
	}
	return centerX, centerZ, nil
}

// EncodeSetBorderLerpSizePacket encodes ClientboundSetBorderLerpSizePacket (0x59).
func EncodeSetBorderLerpSizePacket(oldSize, newSize float64, lerpTime int64) []byte {
	w := protocol.NewWriter(24)
	w.Float64(oldSize)
	w.Float64(newSize)
	w.VarLong(lerpTime)
	return w.Bytes()
}

// DecodeSetBorderLerpSizePacket decodes ClientboundSetBorderLerpSizePacket (0x59).
func DecodeSetBorderLerpSizePacket(data []byte) (oldSize, newSize float64, lerpTime int64, err error) {
	r := protocol.NewReader(data)
	if oldSize, err = r.Float64(); err != nil {
		return 0, 0, 0, err
	}
	if newSize, err = r.Float64(); err != nil {
		return 0, 0, 0, err
	}
	if lerpTime, err = r.VarLong(); err != nil {
		return 0, 0, 0, err
	}
	return oldSize, newSize, lerpTime, nil
}

// EncodeSetBorderSizePacket encodes ClientboundSetBorderSizePacket (0x5a).
func EncodeSetBorderSizePacket(size float64) []byte {
	w := protocol.NewWriter(8)
	w.Float64(size)
	return w.Bytes()
}

// DecodeSetBorderSizePacket decodes ClientboundSetBorderSizePacket (0x5a).
func DecodeSetBorderSizePacket(data []byte) (float64, error) {
	r := protocol.NewReader(data)
	return r.Float64()
}

// EncodeSetBorderWarningDelayPacket encodes ClientboundSetBorderWarningDelayPacket (0x5b).
func EncodeSetBorderWarningDelayPacket(warningDelay int32) []byte {
	w := protocol.NewWriter(5)
	w.VarInt(warningDelay)
	return w.Bytes()
}

// DecodeSetBorderWarningDelayPacket decodes ClientboundSetBorderWarningDelayPacket (0x5b).
func DecodeSetBorderWarningDelayPacket(data []byte) (int32, error) {
	r := protocol.NewReader(data)
	return r.VarInt()
}

// EncodeSetBorderWarningDistancePacket encodes ClientboundSetBorderWarningDistancePacket (0x5c).
func EncodeSetBorderWarningDistancePacket(warningBlocks int32) []byte {
	w := protocol.NewWriter(5)
	w.VarInt(warningBlocks)
	return w.Bytes()
}

// DecodeSetBorderWarningDistancePacket decodes ClientboundSetBorderWarningDistancePacket (0x5c).
func DecodeSetBorderWarningDistancePacket(data []byte) (int32, error) {
	r := protocol.NewReader(data)
	return r.VarInt()
}

// --- Server State & Lifecycle Integration ---

type serverWorldBorderState struct {
	border     *WorldBorder
	damageTick sync.Map // map[*PlayerSession]int
}

var serverWorldBorders sync.Map // map[*Server]*serverWorldBorderState

func (s *Server) worldBorderState() *serverWorldBorderState {
	if s == nil {
		return &serverWorldBorderState{border: NewWorldBorder()}
	}
	if v, ok := serverWorldBorders.Load(s); ok {
		return v.(*serverWorldBorderState)
	}
	created := &serverWorldBorderState{
		border: NewWorldBorder(),
	}
	actual, _ := serverWorldBorders.LoadOrStore(s, created)
	return actual.(*serverWorldBorderState)
}

// WorldBorder returns the server's active WorldBorder instance.
func (s *Server) WorldBorder() *WorldBorder {
	return s.worldBorderState().border
}

// SendWorldBorderToPlayer sends ClientboundInitializeBorderPacket (0x2b) to session.
func (s *Server) SendWorldBorderToPlayer(session *PlayerSession) {
	if s == nil || session == nil {
		return
	}
	session.mu.Lock()
	send := session.send
	session.mu.Unlock()
	if send == nil {
		return
	}
	_ = send(PlayInitializeBorder, EncodeInitializeBorderPacket(s.WorldBorder()))
}

// BroadcastWorldBorderInitialize sends ClientboundInitializeBorderPacket (0x2b) to all connected players.
func (s *Server) BroadcastWorldBorderInitialize() {
	if s == nil {
		return
	}
	s.Broadcast(PlayInitializeBorder, EncodeInitializeBorderPacket(s.WorldBorder()))
}

// BroadcastWorldBorderCenter updates the world border center and broadcasts
// ClientboundSetBorderCenterPacket (0x58) to all connected players.
func (s *Server) SetWorldBorderCenter(x, z float64) {
	if s == nil {
		return
	}
	wb := s.WorldBorder()
	wb.SetCenter(x, z)
	cx, cz := wb.Center()
	s.Broadcast(PlaySetBorderCenter, EncodeSetBorderCenterPacket(cx, cz))
}

// SetWorldBorderSize immediately sets the world border size and broadcasts
// ClientboundSetBorderSizePacket (0x5a) to all connected players.
func (s *Server) SetWorldBorderSize(size float64) {
	if s == nil {
		return
	}
	wb := s.WorldBorder()
	wb.SetSize(size)
	s.Broadcast(PlaySetBorderSize, EncodeSetBorderSizePacket(wb.Size()))
}

// LerpWorldBorderSize starts a world border size transition over lerpTicks game ticks
// and broadcasts ClientboundSetBorderLerpSizePacket (0x59) (or SetBorderSize if immediate).
func (s *Server) LerpWorldBorderSize(oldSize, newSize float64, lerpTicks int64) {
	if s == nil {
		return
	}
	wb := s.WorldBorder()
	wb.LerpSizeBetween(oldSize, newSize, lerpTicks)
	if wb.LerpTime() > 0 {
		s.Broadcast(PlaySetBorderLerpSize, EncodeSetBorderLerpSizePacket(wb.Size(), wb.LerpTarget(), wb.LerpTime()))
	} else {
		s.Broadcast(PlaySetBorderSize, EncodeSetBorderSizePacket(wb.Size()))
	}
}

// SetWorldBorderWarningTime updates the warning delay and broadcasts
// ClientboundSetBorderWarningDelayPacket (0x5b) to all connected players.
func (s *Server) SetWorldBorderWarningTime(warningTime int32) {
	if s == nil {
		return
	}
	wb := s.WorldBorder()
	wb.SetWarningTime(warningTime)
	s.Broadcast(PlaySetBorderWarningDelay, EncodeSetBorderWarningDelayPacket(wb.WarningTime()))
}

// SetWorldBorderWarningDistance updates the warning distance and broadcasts
// ClientboundSetBorderWarningDistancePacket (0x5c) to all connected players.
func (s *Server) SetWorldBorderWarningDistance(warningBlocks int32) {
	if s == nil {
		return
	}
	wb := s.WorldBorder()
	wb.SetWarningBlocks(warningBlocks)
	s.Broadcast(PlaySetBorderWarningDistance, EncodeSetBorderWarningDistancePacket(wb.WarningBlocks()))
}

// SetWorldBorderDamageAmount sets the damage per block outside the border buffer.
func (s *Server) SetWorldBorderDamageAmount(damagePerBlock float64) {
	if s == nil {
		return
	}
	s.WorldBorder().SetDamagePerBlock(damagePerBlock)
}

// SetWorldBorderDamageBuffer sets the safe zone buffer distance outside the border.
func (s *Server) SetWorldBorderDamageBuffer(safeZone float64) {
	if s == nil {
		return
	}
	s.WorldBorder().SetSafeZone(safeZone)
}

// TickWorldBorder advances any active border size interpolation by one game tick
// and applies outside-border damage to Survival/Adventure players beyond the safe zone.
func (s *Server) TickWorldBorder() {
	if s == nil {
		return
	}
	st := s.worldBorderState()
	wb := st.border
	wb.Tick()

	s.playersMu.RLock()
	players := make([]*PlayerSession, 0, len(s.players))
	for _, p := range s.players {
		players = append(players, p)
	}
	s.playersMu.RUnlock()

	for _, player := range players {
		if player == nil || player.IsDead() {
			st.damageTick.Delete(player)
			continue
		}
		snap := player.Snapshot()
		if snap.GameMode == GameModeCreative || snap.GameMode == GameModeSpectator {
			st.damageTick.Delete(player)
			continue
		}
		dmg := wb.CalculateDamage(snap.X, snap.Z)
		if dmg <= 0 {
			st.damageTick.Delete(player)
			continue
		}
		ticks := 0
		if v, ok := st.damageTick.Load(player); ok {
			ticks = v.(int)
		}
		if ticks <= 0 {
			s.DamagePlayerWithEffects(player, dmg, "out_of_border")
			st.damageTick.Store(player, BorderDamageIntervalTicks-1)
		} else {
			st.damageTick.Store(player, ticks-1)
		}
	}
}

// ClampPlayerToWorldBorder clamps session's position to inside the world border
// if the player is currently outside the border bounds, teleporting the client.
// Returns true if the player's position was clamped.
func (s *Server) ClampPlayerToWorldBorder(session *PlayerSession) bool {
	if s == nil || session == nil {
		return false
	}
	wb := s.WorldBorder()
	snap := session.Snapshot()
	if wb.IsWithinBounds(snap.X, snap.Z) {
		return false
	}
	cx, cy, cz := wb.ClampToBounds(snap.X, snap.Y, snap.Z)
	_ = s.TeleportPlayer(session, cx, cy, cz, snap.Yaw, snap.Pitch)
	return true
}

// EnforceWorldBorderMovement prevents a player who was inside (or at the edge of)
// the world border from walking outside it, clamping the destination coordinates
// and synchronizing the client position when clamped.
func (s *Server) EnforceWorldBorderMovement(session *PlayerSession, oldX, oldZ, newX, newY, newZ float64) (float64, float64, bool) {
	if s == nil || session == nil {
		return newX, newZ, false
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator {
		return newX, newZ, false
	}
	wb := s.WorldBorder()
	if wb.IsWithinBounds(newX, newZ) {
		return newX, newZ, false
	}
	// If the player was previously inside (or within 1 block of the border edge),
	// clamp their movement so the border acts as a solid barrier.
	if wb.IsWithinBoundsMargin(oldX, oldZ, 1.0) {
		cx, _, cz := wb.ClampToBounds(newX, newY, newZ)
		return cx, cz, true
	}
	return newX, newZ, false
}

// --- /worldborder Command Implementation ---

// HandleWorldBorderCommand executes the vanilla 26.1.2 /worldborder command:
//   - /worldborder get
//   - /worldborder set <distance> [<time>]
//   - /worldborder add <distance> [<time>]
//   - /worldborder center <x> <z>
//   - /worldborder damage amount <damagePerBlock>
//   - /worldborder damage buffer <distance>
//   - /worldborder warning distance <distance>
//   - /worldborder warning time <time>
func (s *Server) HandleWorldBorderCommand(sender *PlayerSession, args []string) string {
	if s == nil {
		return "Server unavailable"
	}
	if len(args) == 0 {
		return "Usage: /worldborder <add|set|center|damage|get|warning> ..."
	}
	wb := s.WorldBorder()
	sub := strings.ToLower(strings.TrimSpace(args[0]))

	switch sub {
	case "get":
		return fmt.Sprintf("The world border is currently %.0f blocks wide", math.Round(wb.Size()))

	case "set":
		if len(args) < 2 {
			return "Usage: /worldborder set <distance> [<time>]"
		}
		dist, err := strconv.ParseFloat(args[1], 64)
		if err != nil || math.IsNaN(dist) || math.IsInf(dist, 0) {
			return fmt.Sprintf("Invalid distance: %s", args[1])
		}
		var lerpTicks int64
		if len(args) >= 3 {
			lerpTicks, err = parseWorldBorderTimeArg(args[2])
			if err != nil {
				return err.Error()
			}
		}
		return s.applyWorldBorderSizeCommand(dist, lerpTicks)

	case "add":
		if len(args) < 2 {
			return "Usage: /worldborder add <distance> [<time>]"
		}
		delta, err := strconv.ParseFloat(args[1], 64)
		if err != nil || math.IsNaN(delta) || math.IsInf(delta, 0) {
			return fmt.Sprintf("Invalid distance: %s", args[1])
		}
		newSize := wb.Size() + delta
		var lerpTicks int64
		if len(args) >= 3 {
			parsedTicks, err := parseWorldBorderTimeArg(args[2])
			if err != nil {
				return err.Error()
			}
			lerpTicks = wb.LerpTime() + parsedTicks
		}
		return s.applyWorldBorderSizeCommand(newSize, lerpTicks)

	case "center":
		if len(args) < 3 {
			return "Usage: /worldborder center <x> <z>"
		}
		var baseX, baseZ float64
		if sender != nil {
			snap := sender.Snapshot()
			baseX = snap.X
			baseZ = snap.Z
		}
		x, err := parseWorldBorderCoord(args[1], baseX)
		if err != nil {
			return fmt.Sprintf("Invalid X coordinate: %s", args[1])
		}
		z, err := parseWorldBorderCoord(args[2], baseZ)
		if err != nil {
			return fmt.Sprintf("Invalid Z coordinate: %s", args[2])
		}
		if math.Abs(x) > MaxBorderCenterCoord || math.Abs(z) > MaxBorderCenterCoord {
			return fmt.Sprintf("World border center cannot be further out than %.0f blocks", MaxBorderCenterCoord)
		}
		curX, curZ := wb.Center()
		if curX == x && curZ == z {
			return "Nothing changed. The world border is already centered there"
		}
		s.SetWorldBorderCenter(x, z)
		return fmt.Sprintf("Set the center of the world border to %.2f, %.2f", x, z)

	case "damage":
		if len(args) < 3 {
			return "Usage: /worldborder damage <amount|buffer> <value>"
		}
		mode := strings.ToLower(strings.TrimSpace(args[1]))
		switch mode {
		case "amount":
			val, err := strconv.ParseFloat(args[2], 64)
			if err != nil || math.IsNaN(val) || math.IsInf(val, 0) || val < 0 {
				return "World border damage amount must not be less than 0.0"
			}
			if wb.DamagePerBlock() == val {
				return "Nothing changed. The world border damage is already that amount"
			}
			s.SetWorldBorderDamageAmount(val)
			return fmt.Sprintf("Set the world border damage to %.2f per block each second", val)

		case "buffer":
			val, err := strconv.ParseFloat(args[2], 64)
			if err != nil || math.IsNaN(val) || math.IsInf(val, 0) || val < 0 {
				return "World border damage buffer must not be less than 0.0"
			}
			if wb.SafeZone() == val {
				return "Nothing changed. The world border damage buffer is already that distance"
			}
			s.SetWorldBorderDamageBuffer(val)
			return fmt.Sprintf("Set the world border damage buffer to %.2f blocks", val)

		default:
			return "Usage: /worldborder damage <amount|buffer> <value>"
		}

	case "warning":
		if len(args) < 3 {
			return "Usage: /worldborder warning <distance|time> <value>"
		}
		mode := strings.ToLower(strings.TrimSpace(args[1]))
		switch mode {
		case "distance":
			dist, err := strconv.ParseInt(args[2], 10, 32)
			if err != nil || dist < 0 {
				return "World border warning distance must not be less than 0"
			}
			if wb.WarningBlocks() == int32(dist) {
				return "Nothing changed. The world border warning is already that distance"
			}
			s.SetWorldBorderWarningDistance(int32(dist))
			return fmt.Sprintf("Set the world border warning to %d blocks away", dist)

		case "time":
			ticks, err := parseWorldBorderWarningTimeArg(args[2])
			if err != nil {
				return err.Error()
			}
			if wb.WarningTime() == ticks {
				return "Nothing changed. The world border warning is already that amount of time"
			}
			s.SetWorldBorderWarningTime(ticks)
			return fmt.Sprintf("Set the world border warning to %d seconds away", ticks)

		default:
			return "Usage: /worldborder warning <distance|time> <value>"
		}

	default:
		return "Usage: /worldborder <add|set|center|damage|get|warning> ..."
	}
}

func (s *Server) applyWorldBorderSizeCommand(targetSize float64, lerpTicks int64) string {
	if targetSize < MinBorderSize {
		return fmt.Sprintf("World border cannot be smaller than %.1f blocks wide", MinBorderSize)
	}
	if targetSize > MaxBorderSize {
		return fmt.Sprintf("World border cannot be bigger than %.0f blocks wide", MaxBorderSize)
	}
	wb := s.WorldBorder()
	oldSize := wb.Size()
	if oldSize == targetSize && wb.LerpTime() == 0 {
		return "Nothing changed. The world border is already that size"
	}
	if lerpTicks > 0 {
		s.LerpWorldBorderSize(oldSize, targetSize, lerpTicks)
		secs := float64(lerpTicks) / 20.0
		if targetSize > oldSize {
			return fmt.Sprintf("Growing the world border to %.1f blocks wide over %.2f seconds", targetSize, secs)
		}
		return fmt.Sprintf("Shrinking the world border to %.1f blocks wide over %.2f seconds", targetSize, secs)
	}
	s.SetWorldBorderSize(targetSize)
	return fmt.Sprintf("Set the world border to %.1f blocks wide", targetSize)
}

func parseWorldBorderCoord(token string, base float64) (float64, error) {
	token = strings.TrimSpace(token)
	if strings.HasPrefix(token, "~") {
		rest := strings.TrimPrefix(token, "~")
		if rest == "" {
			return base, nil
		}
		off, err := strconv.ParseFloat(rest, 64)
		if err != nil || math.IsNaN(off) || math.IsInf(off, 0) {
			return 0, errors.New("invalid relative coordinate")
		}
		return base + off, nil
	}
	v, err := strconv.ParseFloat(token, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, errors.New("invalid coordinate")
	}
	return v, nil
}

// parseWorldBorderTimeArg parses a <time> argument for /worldborder set and add.
// Supports TimeArgument suffixes ("d" = 24000 ticks, "s" = 20 ticks, "t" = 1 tick)
// as well as plain numbers (interpreted as seconds: n * 20 ticks, matching standard
// /worldborder <distance> <seconds> usage, or 0 for immediate).
func parseWorldBorderTimeArg(raw string) (int64, error) {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" {
		return 0, errors.New("time must not be empty")
	}
	mult := 20.0 // plain numbers in /worldborder set <dist> <time> are seconds (20 ticks/sec)
	numStr := raw
	switch {
	case strings.HasSuffix(raw, "d"):
		mult = 24000.0
		numStr = strings.TrimSuffix(raw, "d")
	case strings.HasSuffix(raw, "s"):
		mult = 20.0
		numStr = strings.TrimSuffix(raw, "s")
	case strings.HasSuffix(raw, "t"):
		mult = 1.0
		numStr = strings.TrimSuffix(raw, "t")
	}
	val, err := strconv.ParseFloat(numStr, 64)
	if err != nil || math.IsNaN(val) || math.IsInf(val, 0) || val < 0 {
		return 0, errors.New("World border time must not be less than 0")
	}
	return int64(math.Round(val * mult)), nil
}

func parseWorldBorderWarningTimeArg(raw string) (int32, error) {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" {
		return 0, errors.New("warning time must not be empty")
	}
	mult := 1.0
	numStr := raw
	switch {
	case strings.HasSuffix(raw, "d"):
		mult = 1200.0
		numStr = strings.TrimSuffix(raw, "d")
	case strings.HasSuffix(raw, "s"):
		mult = 1.0
		numStr = strings.TrimSuffix(raw, "s")
	case strings.HasSuffix(raw, "t"):
		mult = 0.05
		numStr = strings.TrimSuffix(raw, "t")
	}
	val, err := strconv.ParseFloat(numStr, 64)
	if err != nil || math.IsNaN(val) || math.IsInf(val, 0) || val < 0 {
		return 0, errors.New("World border warning time must not be less than 0")
	}
	return int32(math.Round(val * mult)), nil
}

// --- World Border NBT Persistence (<WorldDir>/data/world_border.dat) ---

// WorldBorderFilePath returns the path to <WorldDir>/data/world_border.dat, or "" if WorldDir is unset.
func (s *Server) WorldBorderFilePath() string {
	if s == nil || s.cfg.WorldDir == "" {
		return ""
	}
	return filepath.Join(s.cfg.WorldDir, "data", "world_border.dat")
}

// SaveWorldBorderFile writes the server's current WorldBorder state as gzipped NBT
// to <WorldDir>/data/world_border.dat.
func (s *Server) SaveWorldBorderFile() error {
	path := s.WorldBorderFilePath()
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	settings := s.WorldBorder().Settings()
	root := nbt.NewCompound()
	root.Set("data", settings.ToNBT())
	root.Set("DataVersion", nbt.Int(4763))

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(nbt.MarshalNamed("", root)); err != nil {
		_ = gz.Close()
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}

	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, buf.Bytes(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

// LoadWorldBorderFile loads the server's WorldBorder state from <WorldDir>/data/world_border.dat
// if the file exists.
func (s *Server) LoadWorldBorderFile() error {
	path := s.WorldBorderFilePath()
	if path == "" {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	nbtBytes := raw
	if len(raw) >= 2 && raw[0] == 0x1f && raw[1] == 0x8b {
		gz, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return err
		}
		decompressed, err := io.ReadAll(gz)
		_ = gz.Close()
		if err != nil {
			return err
		}
		nbtBytes = decompressed
	}
	_, tag, err := nbt.UnmarshalNamed(nbtBytes)
	if err != nil {
		tag, err = nbt.Unmarshal(nbtBytes)
		if err != nil {
			return err
		}
	}
	root, ok := tag.(*nbt.Compound)
	if !ok || root == nil {
		return errors.New("worldborder: root NBT tag is not a Compound")
	}
	settings := WorldBorderSettingsFromNBT(root)
	s.WorldBorder().ApplySettings(settings)
	return nil
}

