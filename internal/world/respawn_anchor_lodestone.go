package world

import (
	"math"
	"strconv"
	"strings"

	"regionio/internal/nbt"
)

// Respawn anchor charge bounds matching vanilla 26.1.2 RespawnAnchorBlock.
const (
	RespawnAnchorMinCharges = 0
	RespawnAnchorMaxCharges = 4
)

// IsRespawnAnchorBlock reports whether state is a `minecraft:respawn_anchor` block.
func IsRespawnAnchorBlock(state uint16) bool {
	return isBlockNamed(state, "minecraft:respawn_anchor")
}

// RespawnAnchorCharges returns the charge level (0..4) of a `minecraft:respawn_anchor`
// block state, or 0 if state is not a respawn anchor.
func RespawnAnchorCharges(state uint16) int {
	info, ok := stateByID(state)
	if !ok || info.Name != "minecraft:respawn_anchor" {
		return 0
	}
	c, err := strconv.Atoi(info.Properties["charges"])
	if err != nil || c < RespawnAnchorMinCharges {
		return RespawnAnchorMinCharges
	}
	if c > RespawnAnchorMaxCharges {
		return RespawnAnchorMaxCharges
	}
	return c
}

// RespawnAnchorBlockState returns the block state ID for `minecraft:respawn_anchor`
// with charges clamped to [0, 4].
func RespawnAnchorBlockState(charges int) uint16 {
	if charges < RespawnAnchorMinCharges {
		charges = RespawnAnchorMinCharges
	}
	if charges > RespawnAnchorMaxCharges {
		charges = RespawnAnchorMaxCharges
	}
	if st, ok := nameToStateID("minecraft:respawn_anchor", map[string]string{
		"charges": strconv.Itoa(charges),
	}); ok {
		return st
	}
	return StateAir
}

// RespawnAnchorScaledChargeLevel computes `floor(charges / 4.0 * scale)` matching
// vanilla 26.1.2 RespawnAnchorBlock.getScaledChargeLevel(state, scale).
// For scale = 15 (light emission and comparator signal):
//   - charges=0 -> 0
//   - charges=1 -> 3
//   - charges=2 -> 7
//   - charges=3 -> 11
//   - charges=4 -> 15
func RespawnAnchorScaledChargeLevel(state uint16, scale int) int {
	if !IsRespawnAnchorBlock(state) || scale <= 0 {
		return 0
	}
	charges := RespawnAnchorCharges(state)
	return int(math.Floor(float64(charges) / float64(RespawnAnchorMaxCharges) * float64(scale)))
}

// RespawnAnchorLightEmission returns the runtime block light emission (0, 3, 7, 11, 15)
// for a `minecraft:respawn_anchor` with the given charge count (0..4).
func RespawnAnchorLightEmission(charges int) byte {
	st := RespawnAnchorBlockState(charges)
	if st == StateAir {
		return 0
	}
	return lightEmission(st)
}

// isWaterThatWouldFlowAt implements vanilla 26.1.2 RespawnAnchorBlock.isWaterThatWouldFlow(pos, level):
//   - If the fluid at (x, y, z) is not water, returns false.
//   - If the fluid at (x, y, z) is a water source (level 0, waterlogged, bubble column, kelp/seagrass), returns true.
//   - Otherwise, if the fluid amount is < 2 (i.e. flowing water level == 7), returns false.
//   - Otherwise, returns true iff the block below (x, y-1, z) does not contain water.
func (c *Cache) isWaterThatWouldFlowAt(x, y, z int) bool {
	if c == nil || y < c.minY() || y >= c.maxY() {
		return false
	}
	kind, _, isSource, _, amount, ok := ClassifyFluidState(c.GetBlock(x, y, z))
	if !ok || kind != FluidWater {
		return false
	}
	if isSource {
		return true
	}
	if amount < 2 {
		return false
	}
	if y-1 < c.minY() {
		return true
	}
	return !isWaterFluidState(c.GetBlock(x, y-1, z))
}

// IsRespawnAnchorExplosionInWater reports whether a respawn anchor exploding at
// (x, y, z) is considered "in water" by vanilla 26.1.2 RespawnAnchorBlock.explode:
// true when any of the 4 horizontal neighbors satisfies isWaterThatWouldFlow or
// the block at (x, y+1, z) has water fluid. When true, the explosion center gets
// water's blast resistance (100.0) and does not ignite fire or destroy surrounding blocks.
func (c *Cache) IsRespawnAnchorExplosionInWater(x, y, z int) bool {
	if c == nil {
		return false
	}
	for _, off := range horizontalOffsets {
		if c.isWaterThatWouldFlowAt(x+off[0], y+off[1], z+off[2]) {
			return true
		}
	}
	if y+1 < c.maxY() && isWaterFluidState(c.GetBlock(x, y+1, z)) {
		return true
	}
	return false
}

// --- Lodestone (minecraft:lodestone) & LodestoneTracker ---

// IsLodestoneBlock reports whether state is a `minecraft:lodestone` block.
func IsLodestoneBlock(state uint16) bool {
	return isBlockNamed(state, "minecraft:lodestone")
}

// LodestoneBlockState returns the block state ID for `minecraft:lodestone`.
func LodestoneBlockState() uint16 {
	if st, ok := nameToStateID("minecraft:lodestone", nil); ok {
		return st
	}
	return StateAir
}

// LodestoneTracker represents the vanilla 26.1.2 `minecraft:lodestone_tracker`
// item data component (`net.minecraft.world.item.component.LodestoneTracker`),
// holding an optional GlobalPos target (`dimension` + `pos`) and a `tracked` boolean.
type LodestoneTracker struct {
	HasTarget bool
	Dimension string
	X, Y, Z   int32
	Tracked   bool
}

// NewLodestoneTracker constructs a LodestoneTracker pointing to (dim, x, y, z)
// with the given tracked flag.
func NewLodestoneTracker(dim string, x, y, z int, tracked bool) LodestoneTracker {
	return LodestoneTracker{
		HasTarget: true,
		Dimension: NormalizeDimension(dim),
		X:         int32(x),
		Y:         int32(y),
		Z:         int32(z),
		Tracked:   tracked,
	}
}

// ToNBT serializes the LodestoneTracker into its vanilla 26.1.2 NBT compound
// representation (`target: {dimension: "...", pos: [I; x, y, z]}, tracked: 1b`).
func (lt LodestoneTracker) ToNBT() *nbt.Compound {
	c := nbt.NewCompound()
	if lt.HasTarget {
		target := nbt.NewCompound()
		dim := strings.TrimSpace(lt.Dimension)
		if dim == "" {
			dim = DimensionOverworld
		} else {
			dim = NormalizeDimension(dim)
		}
		target.Set("dimension", nbt.String(dim))
		target.Set("pos", nbt.IntArray([]int32{lt.X, lt.Y, lt.Z}))
		c.Set("target", target)
	}
	if lt.Tracked {
		c.Set("tracked", nbt.Byte(1))
	} else {
		c.Set("tracked", nbt.Byte(0))
	}
	return c
}

// LodestoneTrackerFromNBT decodes a LodestoneTracker from its vanilla 26.1.2
// NBT compound representation. Per LodestoneTracker.CODEC, `tracked` defaults
// to true when omitted.
func LodestoneTrackerFromNBT(c *nbt.Compound) (LodestoneTracker, bool) {
	if c == nil {
		return LodestoneTracker{}, false
	}
	lt := LodestoneTracker{
		Tracked: true,
	}
	if b, ok := nbtGetByte(c, "tracked"); ok {
		lt.Tracked = b != 0
	}
	if target, ok := nbtGetCompound(c, "target"); ok && target != nil {
		dim, okDim := nbtGetString(target, "dimension")
		if posArr, okPos := nbtGetIntArray(target, "pos"); okDim && okPos && len(posArr) == 3 {
			lt.HasTarget = true
			lt.Dimension = NormalizeDimension(dim)
			lt.X = posArr[0]
			lt.Y = posArr[1]
			lt.Z = posArr[2]
		} else if posList, okList := nbtGetList(target, "pos"); okDim && okList && len(posList.Elems) == 3 {
			p0, ok0 := posList.Elems[0].(nbt.Int)
			p1, ok1 := posList.Elems[1].(nbt.Int)
			p2, ok2 := posList.Elems[2].(nbt.Int)
			if ok0 && ok1 && ok2 {
				lt.HasTarget = true
				lt.Dimension = NormalizeDimension(dim)
				lt.X = int32(p0)
				lt.Y = int32(p1)
				lt.Z = int32(p2)
			}
		}
	}
	return lt, true
}

// Tick validates the LodestoneTracker against the world cache `c` for dimension
// `dim`, matching vanilla 26.1.2 `LodestoneTracker.tick(ServerLevel)`:
//   - If `!lt.Tracked || !lt.HasTarget`, returns `(lt, false)` (never modified).
//   - If `lt.Dimension` does not match `dim`, returns `(lt, false)`.
//   - If the target position is outside world bounds or the block at `(X, Y, Z)`
//     is no longer `minecraft:lodestone`, clears `HasTarget` while keeping
//     `Tracked: true` and returns `(updated, true)`.
func (lt LodestoneTracker) Tick(dim string, c *Cache) (LodestoneTracker, bool) {
	if !lt.Tracked || !lt.HasTarget || c == nil {
		return lt, false
	}
	if NormalizeDimension(lt.Dimension) != NormalizeDimension(dim) {
		return lt, false
	}
	x, y, z := int(lt.X), int(lt.Y), int(lt.Z)
	if y < c.minY() || y >= c.maxY() || !IsLodestoneBlock(c.GetBlock(x, y, z)) {
		return LodestoneTracker{
			HasTarget: false,
			Tracked:   true,
		}, true
	}
	return lt, false
}

// BlockEntityAt returns the structural block entity stored on the chunk at
// world coordinates (x, y, z), if present.
func (c *Cache) BlockEntityAt(x, y, z int) (StructureBlockEntity, bool) {
	if c == nil || y < c.minY() || y >= c.maxY() {
		return StructureBlockEntity{}, false
	}
	cx := int32(x >> 4)
	cz := int32(z >> 4)
	key := [2]int32{cx, cz}
	c.startUse(key)
	defer c.endUse(key)
	ch, err := c.chunkAtErr(cx, cz)
	if err != nil || ch == nil {
		return StructureBlockEntity{}, false
	}
	return ch.StructureBlockEntityAt(x, y, z)
}
