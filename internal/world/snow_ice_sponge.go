package world

import (
	"math/rand"
	"strconv"
	"strings"
)

// Vanilla 26.1.2 snow, ice, frosted ice, powder snow, and sponge constants.
const (
	// SnowAndIceMeltBlockLightThreshold is the minimum block light level (12, i.e. > 11)
	// at which `minecraft:snow` layers and `minecraft:ice` melt on a random tick.
	SnowAndIceMeltBlockLightThreshold = 12

	// MaxSnowLayers is the maximum number of layers (8) in `minecraft:snow`.
	MaxSnowLayers = 8

	// MaxFrostedIceAge is the maximum age (3) of `minecraft:frosted_ice` before it melts into water.
	MaxFrostedIceAge = 3

	// PowderSnowMinFreezeTicks is the vanilla TICKS_REQUIRED_TO_FREEZE (140 game ticks = 7s).
	PowderSnowMinFreezeTicks = 140
	// PowderSnowFreezeDamageIntervalTicks is the interval (40 game ticks = 2s) between freeze damage ticks.
	PowderSnowFreezeDamageIntervalTicks = 40
	// PowderSnowFreezeDamage is the vanilla freeze damage (1.0 = half a heart) dealt every 40 ticks.
	PowderSnowFreezeDamage float32 = 1.0

	// SpongeMaxAbsorbDepth is the maximum BFS depth (6) for sponge water absorption.
	SpongeMaxAbsorbDepth = 6
	// SpongeMaxAbsorbBlocks is the maximum number of water cells (64) absorbed by one sponge.
	SpongeMaxAbsorbBlocks = 64
)

// --- Snow Layers (`minecraft:snow`) ---

// IsSnowLayerBlock reports whether state is a `minecraft:snow` layer block.
func IsSnowLayerBlock(state uint16) bool {
	return isBlockNamed(state, "minecraft:snow")
}

// SnowLayerCount returns the `layers` property (1..8) of a `minecraft:snow` state.
func SnowLayerCount(state uint16) (int, bool) {
	info, ok := stateByID(state)
	if !ok || info.Name != "minecraft:snow" {
		return 0, false
	}
	n, err := strconv.Atoi(info.Properties["layers"])
	if err != nil || n < 1 || n > MaxSnowLayers {
		return 0, false
	}
	return n, true
}

// SnowLayerBlockState returns the block state ID for `minecraft:snow` with
// `layers` clamped to 1..8.
func SnowLayerBlockState(layers int) uint16 {
	if layers < 1 {
		layers = 1
	} else if layers > MaxSnowLayers {
		layers = MaxSnowLayers
	}
	if st, ok := nameToStateID("minecraft:snow", map[string]string{"layers": strconv.Itoa(layers)}); ok {
		return st
	}
	return StateSnow
}

// StackSnowLayerAt increments the `layers` of a `minecraft:snow` block at
// (x, y, z) by 1 (up to 8), or places `layers="1"` if (x, y, z) is air and has
// valid floor support below.
func (c *Cache) StackSnowLayerAt(x, y, z int) (newState uint16, res BlockUpdateResult, stacked bool) {
	if c == nil || y <= MinY || y >= MinY+WorldHeight {
		return StateAir, res, false
	}
	cur := c.GetBlock(x, y, z)
	if layers, ok := SnowLayerCount(cur); ok {
		if layers >= MaxSnowLayers {
			return cur, res, false
		}
		next := SnowLayerBlockState(layers + 1)
		applied, upd := c.ApplyBlockWithUpdates(x, y, z, next)
		return next, upd, applied
	}
	if isAirState(cur) && canSupportFloorBlock("minecraft:snow", c.GetBlock(x, y-1, z)) {
		next := SnowLayerBlockState(1)
		applied, upd := c.ApplyBlockWithUpdates(x, y, z, next)
		return next, upd, applied
	}
	return cur, res, false
}

// BlockLightAt returns the effective block light level (0..15) at (x, y, z),
// combining stored chunk block light with direct Manhattan-distance emission
// from nearby light sources within 4 blocks.
func (c *Cache) BlockLightAt(x, y, z int) int {
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return 0
	}
	best := 0
	cx, cz := int32(x>>4), int32(z>>4)
	key := [2]int32{cx, cz}
	c.startUse(key)
	ch, err := c.chunkAtErr(cx, cz)
	if err == nil && ch != nil {
		_, blk, ready := ch.LightAt(x&15, y, z&15)
		if ready && int(blk) > best {
			best = int(blk)
		}
	}
	c.endUse(key)

	for dy := -4; dy <= 4; dy++ {
		ny := y + dy
		if ny < MinY || ny >= MinY+WorldHeight {
			continue
		}
		for dz := -4; dz <= 4; dz++ {
			for dx := -4; dx <= 4; dx++ {
				dist := absInt(dx) + absInt(dy) + absInt(dz)
				if dist > 4 {
					continue
				}
				em := int(lightEmission(c.GetBlock(x+dx, ny, z+dz)))
				if em-dist > best {
					best = em - dist
				}
			}
		}
	}
	return best
}

// RandomTickSnowLayerAt melts a `minecraft:snow` block at (x, y, z) into
// `StateAir` when block light at (x, y, z) is >= 12 (vanilla `light > 11`).
func (c *Cache) RandomTickSnowLayerAt(x, y, z int) (melted bool, res BlockUpdateResult) {
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return false, res
	}
	if !IsSnowLayerBlock(c.GetBlock(x, y, z)) {
		return false, res
	}
	if c.BlockLightAt(x, y, z) < SnowAndIceMeltBlockLightThreshold {
		return false, res
	}
	applied, upd := c.ApplyBlockWithUpdates(x, y, z, StateAir)
	return applied, upd
}

// --- Ice (`minecraft:ice`) & Frosted Ice (`minecraft:frosted_ice`) ---

// IsIceBlock reports whether state is `minecraft:ice`.
func IsIceBlock(state uint16) bool {
	return isBlockNamed(state, "minecraft:ice")
}

// IsFrostedIceBlock reports whether state is `minecraft:frosted_ice`.
func IsFrostedIceBlock(state uint16) bool {
	return isBlockNamed(state, "minecraft:frosted_ice")
}

// FrostedIceBlockState returns the block state ID for `minecraft:frosted_ice`
// with `age` clamped to 0..3.
func FrostedIceBlockState(age int) uint16 {
	if age < 0 {
		age = 0
	} else if age > MaxFrostedIceAge {
		age = MaxFrostedIceAge
	}
	if st, ok := nameToStateID("minecraft:frosted_ice", map[string]string{"age": strconv.Itoa(age)}); ok {
		return st
	}
	return StateAir
}

// FrostedIceAge returns the `age` property (0..3) of a `minecraft:frosted_ice` state.
func FrostedIceAge(state uint16) (int, bool) {
	info, ok := stateByID(state)
	if !ok || info.Name != "minecraft:frosted_ice" {
		return 0, false
	}
	age, err := strconv.Atoi(info.Properties["age"])
	if err != nil || age < 0 || age > MaxFrostedIceAge {
		return 0, false
	}
	return age, true
}

func isNetherDimName(dim string) bool {
	d := strings.ToLower(strings.TrimSpace(dim))
	return d == DimensionNether || d == "the_nether" || d == "nether"
}

// IceMeltTargetState returns the block state that `minecraft:ice` or
// `minecraft:frosted_ice` melts into in `dim` (`StateAir` in the Nether,
// `StateWater` in the Overworld/End).
func IceMeltTargetState(dim string) uint16 {
	if isNetherDimName(dim) {
		return StateAir
	}
	return StateWater
}

// BreakIceWithoutSilkTouchAt breaks a `minecraft:ice` block at (x, y, z)
// without Silk Touch, matching vanilla `IceBlock.playerDestroy`:
//   - In the Nether, always becomes `StateAir`.
//   - In the Overworld/End, becomes `StateWater` if the block at `(x, y-1, z)`
//     blocks motion or is a fluid; otherwise becomes `StateAir`.
func (c *Cache) BreakIceWithoutSilkTouchAt(dim string, x, y, z int) (replacement uint16, res BlockUpdateResult, broken bool) {
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return StateAir, res, false
	}
	if !IsIceBlock(c.GetBlock(x, y, z)) {
		return c.GetBlock(x, y, z), res, false
	}
	replacement = StateAir
	if !isNetherDimName(dim) && y > MinY {
		below := c.GetBlock(x, y-1, z)
		if isSolidState(below) || blocksMotionOrFluid(below) || hasWaterFluid(below) || isLavaBlock(below) {
			replacement = StateWater
		}
	}
	applied, upd := c.ApplyBlockWithUpdates(x, y, z, replacement)
	return replacement, upd, applied
}

// RandomTickIceAt melts `minecraft:ice` at (x, y, z) into `StateWater` (or
// `StateAir` in the Nether) when block light at (x, y, z) is >= 12.
func (c *Cache) RandomTickIceAt(dim string, x, y, z int) (melted bool, res BlockUpdateResult) {
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return false, res
	}
	if !IsIceBlock(c.GetBlock(x, y, z)) {
		return false, res
	}
	if c.BlockLightAt(x, y, z) < SnowAndIceMeltBlockLightThreshold {
		return false, res
	}
	target := IceMeltTargetState(dim)
	applied, upd := c.ApplyBlockWithUpdates(x, y, z, target)
	return applied, upd
}

// CountAdjacentFrostedIce counts how many of the 6 direct neighbors of (x, y, z)
// are `minecraft:frosted_ice`.
func (c *Cache) CountAdjacentFrostedIce(x, y, z int) int {
	if c == nil {
		return 0
	}
	count := 0
	for _, off := range sixNeighborOffsets {
		ny := y + off[1]
		if ny < MinY || ny >= MinY+WorldHeight {
			continue
		}
		if IsFrostedIceBlock(c.GetBlock(x+off[0], ny, z+off[2])) {
			count++
		}
	}
	return count
}

func (c *Cache) slightlyMeltFrostedIceAt(dim string, x, y, z int, res *BlockUpdateResult) bool {
	state := c.GetBlock(x, y, z)
	age, ok := FrostedIceAge(state)
	if !ok {
		return false
	}
	if age < MaxFrostedIceAge {
		next := FrostedIceBlockState(age + 1)
		_, upd := c.ApplyBlockWithUpdates(x, y, z, next)
		res.merge(upd)
		return true
	}
	target := IceMeltTargetState(dim)
	_, upd := c.ApplyBlockWithUpdates(x, y, z, target)
	res.merge(upd)
	return true
}

// StepFrostedIceAt executes one melting step on a `minecraft:frosted_ice` block
// at (x, y, z), matching vanilla `FrostedIceBlock.tick`:
//   - Increments `age` (0 -> 1 -> 2 -> 3 -> water) when either a 1-in-3 roll
//     succeeds or fewer than 4 neighbors are `frosted_ice`.
//   - When a `frosted_ice` block reaches age 3 and melts into water, cascades
//     `slightlyMelt` to any adjacent `frosted_ice` with fewer than 2 `frosted_ice` neighbors.
func (c *Cache) StepFrostedIceAt(dim string, x, y, z int, rng *rand.Rand) (changed bool, res BlockUpdateResult) {
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return false, res
	}
	state := c.GetBlock(x, y, z)
	age, ok := FrostedIceAge(state)
	if !ok {
		return false, res
	}
	if rng == nil {
		rng = rand.New(rand.NewSource(int64(x*3129871 ^ z*116129781 ^ y)))
	}
	adj := c.CountAdjacentFrostedIce(x, y, z)
	if rng.Intn(3) != 0 && adj >= 4 {
		return false, res
	}
	wasMax := age >= MaxFrostedIceAge
	if !c.slightlyMeltFrostedIceAt(dim, x, y, z, &res) {
		return false, res
	}
	if wasMax {
		for _, off := range sixNeighborOffsets {
			nx, ny, nz := x+off[0], y+off[1], z+off[2]
			if ny < MinY || ny >= MinY+WorldHeight {
				continue
			}
			if IsFrostedIceBlock(c.GetBlock(nx, ny, nz)) && c.CountAdjacentFrostedIce(nx, ny, nz) < 2 {
				c.slightlyMeltFrostedIceAt(dim, nx, ny, nz, &res)
			}
		}
	}
	return len(res.Changes) > 0, res
}

// ApplyFrostWalkerAround freezes exposed source water blocks at `y-1` within
// horizontal Euclidean radius `min(16, 2 + level)` around `(x, y, z)` into
// `minecraft:frosted_ice[age=0]`, matching the vanilla Frost Walker enchantment.
func (c *Cache) ApplyFrostWalkerAround(x, y, z int, level int) (placed int, res BlockUpdateResult) {
	if c == nil || level <= 0 || y <= MinY || y >= MinY+WorldHeight {
		return 0, res
	}
	radius := 2 + level
	if radius > 16 {
		radius = 16
	}
	r2 := radius * radius
	frosted0 := FrostedIceBlockState(0)
	if frosted0 == StateAir {
		return 0, res
	}
	for dz := -radius; dz <= radius; dz++ {
		for dx := -radius; dx <= radius; dx++ {
			if dx*dx+dz*dz > r2 {
				continue
			}
			nx, nz := x+dx, z+dz
			if !isAirState(c.GetBlock(nx, y, nz)) {
				continue
			}
			below := c.GetBlock(nx, y-1, nz)
			if kind, lvl, ok := isStandaloneFluidBlock(below); ok && kind == fluidWater && lvl == 0 {
				if applied, upd := c.ApplyBlockWithUpdates(nx, y-1, nz, frosted0); applied {
					res.merge(upd)
					placed++
				}
			}
		}
	}
	return placed, res
}

// --- Powder Snow (`minecraft:powder_snow`) ---

// IsPowderSnowBlock reports whether state is `minecraft:powder_snow`.
func IsPowderSnowBlock(state uint16) bool {
	return state == StatePowderSnow || isBlockNamed(state, "minecraft:powder_snow")
}

// CanEntityWalkOnPowderSnow reports whether an entity wearing `bootsItemID` on
// its feet can walk on top of `minecraft:powder_snow` without sinking (true for
// `minecraft:leather_boots`).
func CanEntityWalkOnPowderSnow(bootsItemID string) bool {
	id := strings.ToLower(strings.TrimSpace(bootsItemID))
	if !strings.Contains(id, ":") && id != "" {
		id = "minecraft:" + id
	}
	return id == "minecraft:leather_boots"
}

// IsEntityFreezeImmuneByArmor reports whether wearing any of the 4 armor slots
// grants immunity to `minecraft:powder_snow` freezing (true if any piece is
// leather armor).
func IsEntityFreezeImmuneByArmor(helmetID, chestplateID, leggingsID, bootsID string) bool {
	for _, raw := range []string{helmetID, chestplateID, leggingsID, bootsID} {
		id := strings.ToLower(strings.TrimSpace(raw))
		if !strings.Contains(id, ":") && id != "" {
			id = "minecraft:" + id
		}
		switch id {
		case "minecraft:leather_helmet", "minecraft:leather_chestplate",
			"minecraft:leather_leggings", "minecraft:leather_boots":
			return true
		}
	}
	return false
}

// StepPowderSnowFreezing computes the next `TicksFrozen` (0..140) for an entity
// on a server tick and reports whether the entity is fully frozen (`>= 140`
// ticks, dealing `PowderSnowFreezeDamage` every 40 ticks):
//   - Inside `powder_snow` without leather armor: `min(140, currentTicksFrozen + 1)`.
//   - Outside `powder_snow` or wearing leather armor: `max(0, currentTicksFrozen - 2)`.
func StepPowderSnowFreezing(inPowderSnow, hasLeatherArmor bool, currentTicksFrozen int) (nextTicksFrozen int, fullyFrozen bool) {
	if currentTicksFrozen < 0 {
		currentTicksFrozen = 0
	}
	if inPowderSnow && !hasLeatherArmor {
		nextTicksFrozen = currentTicksFrozen + 1
		if nextTicksFrozen > PowderSnowMinFreezeTicks {
			nextTicksFrozen = PowderSnowMinFreezeTicks
		}
	} else {
		nextTicksFrozen = currentTicksFrozen - 2
		if nextTicksFrozen < 0 {
			nextTicksFrozen = 0
		}
	}
	return nextTicksFrozen, inPowderSnow && !hasLeatherArmor && nextTicksFrozen >= PowderSnowMinFreezeTicks
}

// ExtinguishBurningEntityInPowderSnowAt melts a `minecraft:powder_snow` block
// at (x, y, z) into `StateAir` when a burning entity enters it (matching
// vanilla `PowderSnowBlock.entityInside`).
func (c *Cache) ExtinguishBurningEntityInPowderSnowAt(x, y, z int, entityOnFire, canDestroyBlock bool) (melted bool, res BlockUpdateResult) {
	if c == nil || !entityOnFire || !canDestroyBlock || y < MinY || y >= MinY+WorldHeight {
		return false, res
	}
	if !IsPowderSnowBlock(c.GetBlock(x, y, z)) {
		return false, res
	}
	applied, upd := c.ApplyBlockWithUpdates(x, y, z, StateAir)
	return applied, upd
}

// --- Sponge & Wet Sponge (`minecraft:sponge`, `minecraft:wet_sponge`) ---

// IsSpongeBlock reports whether state is `minecraft:sponge`.
func IsSpongeBlock(state uint16) bool {
	return isBlockNamed(state, "minecraft:sponge")
}

// IsWetSpongeBlock reports whether state is `minecraft:wet_sponge`.
func IsWetSpongeBlock(state uint16) bool {
	return isBlockNamed(state, "minecraft:wet_sponge")
}

func isRemovableAquaticPlant(state uint16) bool {
	info, ok := stateByID(state)
	if !ok {
		return false
	}
	switch info.Name {
	case "minecraft:kelp", "minecraft:kelp_plant",
		"minecraft:seagrass", "minecraft:tall_seagrass":
		return true
	default:
		return false
	}
}

// DryWetSpongeInNetherAt immediately converts a `minecraft:wet_sponge` at
// (x, y, z) into a dry `minecraft:sponge` when placed in the Nether.
func (c *Cache) DryWetSpongeInNetherAt(dim string, x, y, z int) (dried bool, res BlockUpdateResult) {
	if c == nil || !isNetherDimName(dim) || y < MinY || y >= MinY+WorldHeight {
		return false, res
	}
	if !IsWetSpongeBlock(c.GetBlock(x, y, z)) {
		return false, res
	}
	drySponge, ok := nameToStateID("minecraft:sponge", nil)
	if !ok {
		return false, res
	}
	applied, upd := c.ApplyBlockWithUpdates(x, y, z, drySponge)
	return applied, upd
}

// --- Weather Snowfall & Exposed Water Freezing ---

// CanBiomeSnowOrFreeze reports whether `biomeName` is a cold/snowy biome where
// precipitation falls as snow and exposed source water freezes into ice.
func CanBiomeSnowOrFreeze(biomeName string, y int) bool {
	b := strings.ToLower(strings.TrimSpace(biomeName))
	b = strings.TrimPrefix(b, "minecraft:")
	if strings.Contains(b, "snowy") || strings.Contains(b, "frozen") ||
		strings.Contains(b, "ice_spikes") || strings.Contains(b, "jagged_peaks") ||
		strings.Contains(b, "grove") || strings.Contains(b, "cold_ocean") {
		return true
	}
	if (strings.Contains(b, "windswept") || strings.Contains(b, "taiga") || strings.Contains(b, "stony_peaks")) && y >= 120 {
		return true
	}
	return false
}

// TryWeatherSnowAndFreezeAt applies one weather precipitation tick at surface
// position (x, y, z) in `biomeName`:
//   - If `CanBiomeSnowOrFreeze(biomeName, y)` is true:
//   - Freezes exposed source water at `(x, y-1, z)` into `minecraft:ice` when
//     block light at `(x, y-1, z)` is < 10.
//   - Places `minecraft:snow[layers=1]` at `(x, y, z)` when `(x, y, z)` is air,
//     block light at `(x, y, z)` is < 10, and `(x, y-1, z)` can support snow.
func (c *Cache) TryWeatherSnowAndFreezeAt(x, y, z int, biomeName string) (changed bool, res BlockUpdateResult) {
	if c == nil || y <= MinY || y >= MinY+WorldHeight {
		return false, res
	}
	if !CanBiomeSnowOrFreeze(biomeName, y) {
		return false, res
	}
	below := c.GetBlock(x, y-1, z)
	if kind, lvl, ok := isStandaloneFluidBlock(below); ok && kind == fluidWater && lvl == 0 {
		if c.BlockLightAt(x, y-1, z) < 10 {
			if iceState, ok := nameToStateID("minecraft:ice", nil); ok {
				applied, upd := c.ApplyBlockWithUpdates(x, y-1, z, iceState)
				return applied, upd
			}
		}
	}
	if isAirState(c.GetBlock(x, y, z)) && c.BlockLightAt(x, y, z) < 10 {
		if canSupportFloorBlock("minecraft:snow", c.GetBlock(x, y-1, z)) {
			applied, upd := c.ApplyBlockWithUpdates(x, y, z, SnowLayerBlockState(1))
			return applied, upd
		}
	}
	return false, res
}
