package server

import (
	"math/rand"
	"strconv"
	"strings"
	"sync"

	"regionio/internal/world"
)

// CauldronKind identifies the contents of a cauldron block in Minecraft 26.1.2.
type CauldronKind string

const (
	CauldronKindEmpty      CauldronKind = "empty"
	CauldronKindWater      CauldronKind = "water"
	CauldronKindLava       CauldronKind = "lava"
	CauldronKindPowderSnow CauldronKind = "powder_snow"
)

// Vanilla 26.1.2 cauldron fill levels, precipitation chances, dripstone constants,
// data components, and sound event identifiers.
const (
	CauldronMinFillLevel = 1
	CauldronMaxFillLevel = 3

	// RainFillCauldronChance is the vanilla 5% (0.05) chance per precipitation
	// tick for rain to add 1 level to an empty or water cauldron.
	RainFillCauldronChance float32 = 0.05

	// SnowFillCauldronChance is the vanilla 10% (0.10) chance per precipitation
	// tick for snow to add 1 level to an empty or powder_snow cauldron.
	SnowFillCauldronChance float32 = 0.10

	// DripstoneWaterFillChance and DripstoneLavaFillChance match vanilla
	// PointedDripstoneBlock.DRIP_PROBABILITY_PER_RANDOM_TICK (~0.17578125 / 0.05859375).
	DripstoneWaterFillChance float32 = 0.17578125
	DripstoneLavaFillChance  float32 = 0.05859375

	// MaxDripstoneSearchDistance is the maximum vertical distance (10 blocks)
	// between a cauldron and the tip of a pointed_dripstone stalactite above it.
	MaxDripstoneSearchDistance = 10

	// DataComponentDyedColor is BuiltInRegistries.DATA_COMPONENT_TYPE index 38 ("minecraft:dyed_color").
	DataComponentDyedColor int32 = 38

	// DefaultLeatherColorRGB is the default undyed leather RGB color (0xA06540 = -6265536).
	DefaultLeatherColorRGB int32 = 0xA06540

	// LavaCauldronEntityDamage is the 4.0 lava damage dealt to entities inside a lava_cauldron.
	LavaCauldronEntityDamage float32 = 4.0

	// DefaultCauldronLavaFireTicks is 300 ticks (15 seconds) of fire applied by lava_cauldron.
	DefaultCauldronLavaFireTicks int = 300

	// Vanilla cauldron sound events.
	SoundBucketFillCauldron            = "minecraft:item.bucket.fill"
	SoundBucketFillLavaCauldron        = "minecraft:item.bucket.fill_lava"
	SoundBucketFillPowderSnowCauldron  = "minecraft:item.bucket.fill_powder_snow"
	SoundBucketEmptyCauldron           = "minecraft:item.bucket.empty"
	SoundBucketEmptyLavaCauldron       = "minecraft:item.bucket.empty_lava"
	SoundBucketEmptyPowderSnowCauldron = "minecraft:item.bucket.empty_powder_snow"
	SoundBottleFillCauldron            = "minecraft:item.bottle.fill"
	SoundBottleEmptyCauldron           = "minecraft:item.bottle.empty"
	SoundDripWaterIntoCauldron         = "minecraft:block.pointed_dripstone.drip_water_into_cauldron"
	SoundDripLavaIntoCauldron          = "minecraft:block.pointed_dripstone.drip_lava_into_cauldron"
	SoundGenericExtinguishFire         = "minecraft:entity.generic.extinguish_fire"
)

var (
	cauldronStatesOnce     sync.Once
	stateEmptyCauldron     uint16
	stateLavaCauldron      uint16
	stateWaterCauldron     [4]uint16 // index 1..3
	statePowderSnowCauld   [4]uint16 // index 1..3
	pointedDripstoneTipIDs map[uint16]bool
	pointedDripstoneDown   map[uint16]bool
)

func initCauldronStates() {
	cauldronStatesOnce.Do(func() {
		if st, ok := world.BlockStateIDByProperties("minecraft:cauldron", nil); ok {
			stateEmptyCauldron = st
		}
		if st, ok := world.BlockStateIDByProperties("minecraft:lava_cauldron", nil); ok {
			stateLavaCauldron = st
		}
		for lvl := 1; lvl <= 3; lvl++ {
			props := map[string]string{"level": strconv.Itoa(lvl)}
			if st, ok := world.BlockStateIDByProperties("minecraft:water_cauldron", props); ok {
				stateWaterCauldron[lvl] = st
			}
			if st, ok := world.BlockStateIDByProperties("minecraft:powder_snow_cauldron", props); ok {
				statePowderSnowCauld[lvl] = st
			}
		}

		pointedDripstoneTipIDs = make(map[uint16]bool)
		pointedDripstoneDown = make(map[uint16]bool)
		thicknesses := []string{"tip", "tip_merge", "frustum", "middle", "base"}
		waterloggedVals := []string{"false", "true"}
		for _, th := range thicknesses {
			for _, wl := range waterloggedVals {
				st, ok := world.BlockStateIDByProperties("minecraft:pointed_dripstone", map[string]string{
					"thickness":          th,
					"vertical_direction": "down",
					"waterlogged":        wl,
				})
				if ok && st != world.StateAir {
					pointedDripstoneDown[st] = true
					if th == "tip" || th == "tip_merge" {
						pointedDripstoneTipIDs[st] = true
					}
				}
			}
		}
	})
}

// EmptyCauldronBlockState returns the block state ID for an empty `minecraft:cauldron`.
func EmptyCauldronBlockState() uint16 {
	initCauldronStates()
	return stateEmptyCauldron
}

// CauldronLavaBlockState returns the block state ID for `minecraft:lava_cauldron`.
func CauldronLavaBlockState() uint16 {
	initCauldronStates()
	return stateLavaCauldron
}

// CauldronWaterBlockState returns the block state ID for `minecraft:water_cauldron`
// at level (1..3), or `minecraft:cauldron` when level <= 0.
func CauldronWaterBlockState(level int) uint16 {
	initCauldronStates()
	if level <= 0 {
		return stateEmptyCauldron
	}
	if level > CauldronMaxFillLevel {
		level = CauldronMaxFillLevel
	}
	return stateWaterCauldron[level]
}

// CauldronPowderSnowBlockState returns the block state ID for `minecraft:powder_snow_cauldron`
// at level (1..3), or `minecraft:cauldron` when level <= 0.
func CauldronPowderSnowBlockState(level int) uint16 {
	initCauldronStates()
	if level <= 0 {
		return stateEmptyCauldron
	}
	if level > CauldronMaxFillLevel {
		level = CauldronMaxFillLevel
	}
	return statePowderSnowCauld[level]
}

// CauldronBlockState returns the block state ID for the given CauldronKind and
// fill level (0..3).
func CauldronBlockState(kind CauldronKind, level int) (uint16, bool) {
	initCauldronStates()
	switch kind {
	case CauldronKindEmpty:
		return stateEmptyCauldron, stateEmptyCauldron != world.StateAir
	case CauldronKindWater:
		if level <= 0 {
			return stateEmptyCauldron, stateEmptyCauldron != world.StateAir
		}
		if level > CauldronMaxFillLevel {
			level = CauldronMaxFillLevel
		}
		return stateWaterCauldron[level], stateWaterCauldron[level] != world.StateAir
	case CauldronKindPowderSnow:
		if level <= 0 {
			return stateEmptyCauldron, stateEmptyCauldron != world.StateAir
		}
		if level > CauldronMaxFillLevel {
			level = CauldronMaxFillLevel
		}
		return statePowderSnowCauld[level], statePowderSnowCauld[level] != world.StateAir
	case CauldronKindLava:
		if level <= 0 {
			return stateEmptyCauldron, stateEmptyCauldron != world.StateAir
		}
		return stateLavaCauldron, stateLavaCauldron != world.StateAir
	default:
		return world.StateAir, false
	}
}

// ParseCauldronState inspects state and returns its CauldronKind, fill level
// (0 for empty, 1..3 for water/powder_snow, 3 for lava), and whether state is a
// cauldron block.
func ParseCauldronState(state uint16) (kind CauldronKind, level int, isCauldron bool) {
	if state == world.StateAir {
		return "", 0, false
	}
	initCauldronStates()
	if state == stateEmptyCauldron {
		return CauldronKindEmpty, 0, true
	}
	if state == stateLavaCauldron {
		return CauldronKindLava, CauldronMaxFillLevel, true
	}
	for lvl := 1; lvl <= 3; lvl++ {
		if state == stateWaterCauldron[lvl] {
			return CauldronKindWater, lvl, true
		}
		if state == statePowderSnowCauld[lvl] {
			return CauldronKindPowderSnow, lvl, true
		}
	}
	switch world.BlockName(state) {
	case "minecraft:cauldron":
		return CauldronKindEmpty, 0, true
	case "minecraft:lava_cauldron":
		return CauldronKindLava, CauldronMaxFillLevel, true
	case "minecraft:water_cauldron":
		return CauldronKindWater, 1, true
	case "minecraft:powder_snow_cauldron":
		return CauldronKindPowderSnow, 1, true
	default:
		return "", 0, false
	}
}

// IsCauldronBlock reports whether state is any cauldron block (`cauldron`,
// `water_cauldron`, `lava_cauldron`, or `powder_snow_cauldron`).
func IsCauldronBlock(state uint16) bool {
	_, _, ok := ParseCauldronState(state)
	return ok
}

// LowerCauldronFillLevel decrements the fill level of a layered cauldron
// (`water_cauldron` or `powder_snow_cauldron`) by 1, turning level 1 (or a
// `lava_cauldron`) into an empty `minecraft:cauldron`.
func LowerCauldronFillLevel(state uint16) (nextState uint16, lowered bool) {
	kind, lvl, ok := ParseCauldronState(state)
	if !ok || kind == CauldronKindEmpty || lvl <= 0 {
		return state, false
	}
	switch kind {
	case CauldronKindWater:
		return CauldronWaterBlockState(lvl - 1), true
	case CauldronKindPowderSnow:
		return CauldronPowderSnowBlockState(lvl - 1), true
	case CauldronKindLava:
		return EmptyCauldronBlockState(), true
	default:
		return state, false
	}
}

// CauldronComparatorSignal returns the vanilla redstone comparator signal (0..3)
// for a cauldron block state:
//   - `minecraft:cauldron`: 0
//   - `minecraft:water_cauldron[level=1..3]`: 1..3
//   - `minecraft:powder_snow_cauldron[level=1..3]`: 1..3
//   - `minecraft:lava_cauldron`: 3
func CauldronComparatorSignal(state uint16) int {
	kind, lvl, ok := ParseCauldronState(state)
	if !ok || kind == CauldronKindEmpty {
		return 0
	}
	return lvl
}

// CauldronComparatorSignalAt returns the redstone comparator signal (0..3) for
// a cauldron at (x, y, z) in dim.
func (s *Server) CauldronComparatorSignalAt(dim string, x, y, z int) int {
	if s == nil {
		return 0
	}
	cache := s.DimensionChunks(world.NormalizeDimension(dim))
	if cache == nil {
		return 0
	}
	return CauldronComparatorSignal(cache.GetBlock(x, y, z))
}

// CauldronContentHeight returns the relative Y height in [0, 1] of the fluid
// surface inside a cauldron at fill level (1..3): (6.0 + 3.0*level) / 16.0,
// matching vanilla AbstractCauldronBlock.getContentHeight.
func CauldronContentHeight(level int) float64 {
	if level <= 0 {
		return 0
	}
	if level > CauldronMaxFillLevel {
		level = CauldronMaxFillLevel
	}
	return (6.0 + 3.0*float64(level)) / 16.0
}

// --- Dyed Leather Armor Metadata & Shulker Box / Banner Washing Helpers ---

// IsDyeableLeatherItem reports whether itemName is in the vanilla 26.1.2
// `#minecraft:dyeable` tag (leather helmet/chestplate/leggings/boots,
// leather_horse_armor, wolf_armor).
func IsDyeableLeatherItem(itemName string) bool {
	switch canonicalBeaconResource(itemName) {
	case "minecraft:leather_helmet",
		"minecraft:leather_chestplate",
		"minecraft:leather_leggings",
		"minecraft:leather_boots",
		"minecraft:leather_horse_armor",
		"minecraft:wolf_armor":
		return true
	default:
		return false
	}
}

type sessionDyedColorTracker struct {
	mu           sync.Mutex
	slotColor    [world.PlayerInventorySlots]int32
	slotHasColor [world.PlayerInventorySlots]bool
	carriedColor int32
	carriedHas   bool
}

var playerDyedColors sync.Map // map[*PlayerSession]*sessionDyedColorTracker

func getDyedColorTracker(session *PlayerSession) *sessionDyedColorTracker {
	if session == nil {
		return nil
	}
	if v, ok := playerDyedColors.Load(session); ok {
		return v.(*sessionDyedColorTracker)
	}
	actual, _ := playerDyedColors.LoadOrStore(session, &sessionDyedColorTracker{})
	return actual.(*sessionDyedColorTracker)
}

// SetSlotDyedColor sets the custom RGB dye color (`minecraft:dyed_color`) on
// player inventory slot (0..45).
func SetSlotDyedColor(session *PlayerSession, slot int, rgb int32) {
	if session == nil || slot < 0 || slot >= world.PlayerInventorySlots {
		return
	}
	tr := getDyedColorTracker(session)
	tr.mu.Lock()
	tr.slotColor[slot] = rgb & 0xFFFFFF
	tr.slotHasColor[slot] = true
	tr.mu.Unlock()
}

// GetSlotDyedColor returns the custom RGB dye color on player inventory slot (0..45).
func GetSlotDyedColor(session *PlayerSession, slot int) (rgb int32, hasColor bool) {
	if session == nil || slot < 0 || slot >= world.PlayerInventorySlots {
		return 0, false
	}
	if session.InventorySlot(slot).IsEmpty() {
		return 0, false
	}
	tr := getDyedColorTracker(session)
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if !tr.slotHasColor[slot] {
		return 0, false
	}
	return tr.slotColor[slot], true
}

// HasSlotDyedColor reports whether player inventory slot (0..45) has a custom
// dyed leather color component.
func HasSlotDyedColor(session *PlayerSession, slot int) bool {
	_, ok := GetSlotDyedColor(session, slot)
	return ok
}

// ClearSlotDyedColor removes the custom `minecraft:dyed_color` component from
// player inventory slot (0..45), as performed when washing dyed leather armor
// in a water cauldron.
func ClearSlotDyedColor(session *PlayerSession, slot int) {
	if session == nil || slot < 0 || slot >= world.PlayerInventorySlots {
		return
	}
	tr := getDyedColorTracker(session)
	tr.mu.Lock()
	tr.slotColor[slot] = 0
	tr.slotHasColor[slot] = false
	tr.mu.Unlock()
}

// IsDyedShulkerBoxItem reports whether itemName is one of the 16 colored
// `minecraft:<color>_shulker_box` items (excluding undyed `minecraft:shulker_box`).
func IsDyedShulkerBoxItem(itemName string) bool {
	short := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(itemName)), "minecraft:")
	if short == "shulker_box" || !strings.HasSuffix(short, "_shulker_box") {
		return false
	}
	color := strings.TrimSuffix(short, "_shulker_box")
	_, ok := BannerDyeColorOrdinal(color)
	return ok
}

// --- Player Right-Click Cauldron Interaction (Buckets, Bottles, Washing) ---

// InteractCauldronAtInDim handles a player right-clicking a cauldron block at
// (x, y, z) in dim using the item in inventorySlot (e.g. `world.HotbarContainerOffset + selectedHotbar`
// or `world.OffhandSlot`):
//   - Buckets: `water_bucket`, `lava_bucket`, `powder_snow_bucket` fill any cauldron to level 3;
//     empty `bucket` scoops a level-3 cauldron into the corresponding filled bucket.
//   - Bottles: `glass_bottle` scoops 1 water level into a water `minecraft:potion`;
//     a water `minecraft:potion` (`PotionWater`) adds 1 water level and leaves a `glass_bottle`.
//     Non-water potions (`awkward`, `healing`, `splash_potion`, `lingering_potion`, etc.) are rejected
//     (Java Edition has no potion cauldrons).
//   - Washing in `water_cauldron` (level >= 1):
//     1. Dyed leather armor (`#minecraft:dyeable` with `HasSlotDyedColor`): clears dye color, lowers water level by 1.
//     2. Patterned banner (`IsBannerItem` with >= 1 pattern layer): removes topmost layer (`WithoutTopLayer`), lowers water level by 1.
//     3. Dyed shulker box (`minecraft:<color>_shulker_box`): converts to undyed `minecraft:shulker_box`, lowers water level by 1.
func (s *Server) InteractCauldronAtInDim(
	session *PlayerSession,
	dim string,
	x, y, z int,
	inventorySlot int,
) (handled bool, nextState uint16) {
	if s == nil || session == nil || session.IsDead() {
		return false, world.StateAir
	}
	normDim := world.NormalizeDimension(dim)
	cache := s.DimensionChunks(normDim)
	if cache == nil {
		return false, world.StateAir
	}
	curState := cache.GetBlock(x, y, z)
	kind, lvl, isCauld := ParseCauldronState(curState)
	if !isCauld {
		return false, curState
	}

	if inventorySlot < 0 || inventorySlot >= world.PlayerInventorySlots {
		inventorySlot, _ = session.handSlotAndStack(0)
	}
	held := session.InventorySlot(inventorySlot)
	if held.IsEmpty() {
		return false, curState
	}
	itemName := canonicalBeaconResource(held.ResolvedID())
	creative := session.Snapshot().GameMode == GameModeCreative

	// 1. Filled buckets -> fill any cauldron to full (level 3).
	switch itemName {
	case "minecraft:water_bucket":
		targetState := CauldronWaterBlockState(CauldronMaxFillLevel)
		s.setCauldronBlockAndNotify(normDim, cache, x, y, z, targetState, SoundBucketEmptyCauldron)
		if !creative {
			replaceOrGiveBucketItem(session, inventorySlot, held, world.NewNamedItemStack("minecraft:bucket", 1))
		}
		return true, targetState

	case "minecraft:lava_bucket":
		targetState := CauldronLavaBlockState()
		s.setCauldronBlockAndNotify(normDim, cache, x, y, z, targetState, SoundBucketEmptyLavaCauldron)
		if !creative {
			replaceOrGiveBucketItem(session, inventorySlot, held, world.NewNamedItemStack("minecraft:bucket", 1))
		}
		return true, targetState

	case "minecraft:powder_snow_bucket":
		targetState := CauldronPowderSnowBlockState(CauldronMaxFillLevel)
		s.setCauldronBlockAndNotify(normDim, cache, x, y, z, targetState, SoundBucketEmptyPowderSnowCauldron)
		if !creative {
			replaceOrGiveBucketItem(session, inventorySlot, held, world.NewNamedItemStack("minecraft:bucket", 1))
		}
		return true, targetState

	case "minecraft:bucket":
		// Empty bucket scoops only from a full (level 3) cauldron.
		if kind == CauldronKindEmpty || lvl < CauldronMaxFillLevel {
			return false, curState
		}
		var filledBucket string
		var soundName string
		switch kind {
		case CauldronKindWater:
			filledBucket = "minecraft:water_bucket"
			soundName = SoundBucketFillCauldron
		case CauldronKindLava:
			filledBucket = "minecraft:lava_bucket"
			soundName = SoundBucketFillLavaCauldron
		case CauldronKindPowderSnow:
			filledBucket = "minecraft:powder_snow_bucket"
			soundName = SoundBucketFillPowderSnowCauldron
		default:
			return false, curState
		}
		targetState := EmptyCauldronBlockState()
		s.setCauldronBlockAndNotify(normDim, cache, x, y, z, targetState, soundName)
		if !creative {
			replaceOrGiveBucketItem(session, inventorySlot, held, world.NewNamedItemStack(filledBucket, 1))
		}
		return true, targetState

	case "minecraft:glass_bottle":
		// Glass bottle scoops 1 level from a water cauldron into a water potion bottle.
		if kind != CauldronKindWater || lvl < CauldronMinFillLevel {
			return false, curState
		}
		targetState, _ := LowerCauldronFillLevel(curState)
		s.setCauldronBlockAndNotify(normDim, cache, x, y, z, targetState, SoundBottleFillCauldron)
		if !creative {
			giveWaterBottleFromCauldron(session, inventorySlot, held)
		}
		return true, targetState

	case "minecraft:potion":
		// Only a pure water bottle (PotionWater = "minecraft:water") can fill a cauldron;
		// Java Edition has NO potion cauldrons!
		pType := GetSlotPotionType(session, inventorySlot)
		if pType == "" {
			pType = PotionWater
		}
		if canonicalPotionID(pType) != PotionWater {
			return false, curState
		}
		if kind != CauldronKindEmpty && (kind != CauldronKindWater || lvl >= CauldronMaxFillLevel) {
			return false, curState
		}
		nextLvl := 1
		if kind == CauldronKindWater {
			nextLvl = lvl + 1
		}
		targetState := CauldronWaterBlockState(nextLvl)
		s.setCauldronBlockAndNotify(normDim, cache, x, y, z, targetState, SoundBottleEmptyCauldron)
		if !creative {
			SetSlotPotionType(session, inventorySlot, "")
			replaceOrGiveBucketItem(session, inventorySlot, held, world.NewNamedItemStack("minecraft:glass_bottle", 1))
		}
		return true, targetState

	case "minecraft:splash_potion", "minecraft:lingering_potion":
		// Explicitly rejected: Java Edition has no potion cauldrons.
		return false, curState
	}

	// 2. Washing items in a water cauldron (requires kind == CauldronKindWater && lvl >= 1).
	if kind != CauldronKindWater || lvl < CauldronMinFillLevel {
		return false, curState
	}

	// 2a. Dyed leather armor washing (`#minecraft:dyeable` with custom dyed color).
	if IsDyeableLeatherItem(itemName) && HasSlotDyedColor(session, inventorySlot) {
		ClearSlotDyedColor(session, inventorySlot)
		targetState, _ := LowerCauldronFillLevel(curState)
		s.setCauldronBlockAndNotify(normDim, cache, x, y, z, targetState, "")
		return true, targetState
	}

	// 2b. Banner pattern washing (`IsBannerItem` with >= 1 pattern layer).
	if IsBannerItem(itemName) {
		patterns := GetSlotBannerPatterns(session, inventorySlot)
		if nextPatterns, ok := patterns.WithoutTopLayer(); ok {
			if held.Count <= 1 || creative {
				SetSlotBannerPatterns(session, inventorySlot, nextPatterns)
			} else {
				// Consume 1 from the stack in inventorySlot and give 1 washed banner.
				session.SetInventorySlot(inventorySlot, world.NewItemStack(held.ResolvedItemID(), held.Count-1))
				giveWashedBannerCopy(session, held.ResolvedItemID(), nextPatterns)
			}
			targetState, _ := LowerCauldronFillLevel(curState)
			s.setCauldronBlockAndNotify(normDim, cache, x, y, z, targetState, "")
			return true, targetState
		}
		return false, curState
	}

	// 2c. Dyed shulker box washing (`minecraft:<color>_shulker_box` -> `minecraft:shulker_box`).
	if IsDyedShulkerBoxItem(itemName) {
		undyed := world.NewNamedItemStack("minecraft:shulker_box", held.Count)
		if !undyed.IsEmpty() {
			meta := GetSlotItemMeta(session, inventorySlot)
			session.SetInventorySlot(inventorySlot, undyed)
			SetSlotItemMeta(session, inventorySlot, meta)
			targetState, _ := LowerCauldronFillLevel(curState)
			s.setCauldronBlockAndNotify(normDim, cache, x, y, z, targetState, "")
			return true, targetState
		}
	}

	return false, curState
}

// InteractCauldronAt handles a player right-clicking a cauldron at (x, y, z)
// in session's dimension using the item in inventorySlot.
func (s *Server) InteractCauldronAt(session *PlayerSession, x, y, z int, inventorySlot int) (handled bool, nextState uint16) {
	dim := world.DimensionOverworld
	if session != nil && session.Dimension() != "" {
		dim = session.Dimension()
	}
	return s.InteractCauldronAtInDim(session, dim, x, y, z, inventorySlot)
}

func (s *Server) setCauldronBlockAndNotify(dim string, cache *world.Cache, x, y, z int, nextState uint16, soundName string) {
	if cache == nil {
		return
	}
	if valid, lightChunks := cache.SetBlockWithLight(x, y, z, nextState); valid {
		s.broadcastBlockAndLightUpdate(x, y, z, nextState, lightChunks)
	} else {
		cache.SetBlock(x, y, z, nextState)
	}
	cache.UpdateComparatorsAround(x, y, z)
	if soundName != "" {
		s.playBeaconSound(dim, x, y, z, soundName)
	}
}

func replaceOrGiveBucketItem(session *PlayerSession, slot int, curStack, resultStack world.ItemStack) {
	if session == nil || resultStack.IsEmpty() {
		return
	}
	if curStack.Count <= 1 {
		session.SetInventorySlot(slot, resultStack)
		return
	}
	session.SetInventorySlot(slot, world.NewItemStack(curStack.ResolvedItemID(), curStack.Count-1))
	_, _ = session.AddItemToInventory(resultStack)
}

func giveWaterBottleFromCauldron(session *PlayerSession, slot int, curStack world.ItemStack) {
	if session == nil {
		return
	}
	waterBottle := world.NewNamedItemStack("minecraft:potion", 1)
	if curStack.Count <= 1 {
		session.SetInventorySlot(slot, waterBottle)
		SetSlotPotionType(session, slot, PotionWater)
		return
	}
	session.SetInventorySlot(slot, world.NewItemStack(curStack.ResolvedItemID(), curStack.Count-1))
	// Find an empty slot for the unstackable water potion bottle so we can tag its PotionWater metadata.
	for dst := world.HotbarContainerOffset; dst < world.HotbarContainerOffset+9; dst++ {
		if session.InventorySlot(dst).IsEmpty() {
			session.SetInventorySlot(dst, waterBottle)
			SetSlotPotionType(session, dst, PotionWater)
			return
		}
	}
	for dst := 9; dst < world.HotbarContainerOffset; dst++ {
		if session.InventorySlot(dst).IsEmpty() {
			session.SetInventorySlot(dst, waterBottle)
			SetSlotPotionType(session, dst, PotionWater)
			return
		}
	}
	_, _ = session.AddItemToInventory(waterBottle)
}

func giveWashedBannerCopy(session *PlayerSession, bannerItemID int32, patterns BannerPatternsComponent) {
	if session == nil {
		return
	}
	oneBanner := world.NewItemStack(bannerItemID, 1)
	for dst := world.HotbarContainerOffset; dst < world.HotbarContainerOffset+9; dst++ {
		if session.InventorySlot(dst).IsEmpty() {
			session.SetInventorySlot(dst, oneBanner)
			SetSlotBannerPatterns(session, dst, patterns)
			return
		}
	}
	for dst := 9; dst < world.HotbarContainerOffset; dst++ {
		if session.InventorySlot(dst).IsEmpty() {
			session.SetInventorySlot(dst, oneBanner)
			SetSlotBannerPatterns(session, dst, patterns)
			return
		}
	}
	_, _ = session.AddItemToInventory(oneBanner)
}

// --- Filling by Rain / Snow (Precipitation) & Pointed Dripstone ---

// TryFillCauldronFromPrecipitation attempts to fill the cauldron at (x, y, z)
// in dim by 1 level from active rain (`precipitation == "rain"`) or snow
// (`precipitation == "snow"`), checking sky clearance above `(x, y, z)` and
// rolling vanilla probabilities (5% for rain, 10% for snow) unless force is true.
func (s *Server) TryFillCauldronFromPrecipitation(
	dim string,
	x, y, z int,
	precipitation string,
	rng *rand.Rand,
	force bool,
) (filled bool, nextState uint16) {
	if s == nil {
		return false, world.StateAir
	}
	normDim := world.NormalizeDimension(dim)
	cache := s.DimensionChunks(normDim)
	if cache == nil {
		return false, world.StateAir
	}
	curState := cache.GetBlock(x, y, z)
	kind, lvl, isCauld := ParseCauldronState(curState)
	if !isCauld {
		return false, curState
	}

	// Must have open sky above the cauldron.
	for sy := y + 1; sy <= BeaconDefaultMaxBuild; sy++ {
		if cache.GetBlock(x, sy, z) != world.StateAir {
			return false, curState
		}
	}

	prec := strings.ToLower(strings.TrimSpace(precipitation))
	switch prec {
	case "rain":
		if kind != CauldronKindEmpty && (kind != CauldronKindWater || lvl >= CauldronMaxFillLevel) {
			return false, curState
		}
		if !force && rng != nil && rng.Float32() >= RainFillCauldronChance {
			return false, curState
		}
		nextLvl := 1
		if kind == CauldronKindWater {
			nextLvl = lvl + 1
		}
		nextState = CauldronWaterBlockState(nextLvl)
		s.setCauldronBlockAndNotify(normDim, cache, x, y, z, nextState, "")
		return true, nextState

	case "snow":
		if kind != CauldronKindEmpty && (kind != CauldronKindPowderSnow || lvl >= CauldronMaxFillLevel) {
			return false, curState
		}
		if !force && rng != nil && rng.Float32() >= SnowFillCauldronChance {
			return false, curState
		}
		nextLvl := 1
		if kind == CauldronKindPowderSnow {
			nextLvl = lvl + 1
		}
		nextState = CauldronPowderSnowBlockState(nextLvl)
		s.setCauldronBlockAndNotify(normDim, cache, x, y, z, nextState, "")
		return true, nextState

	default:
		return false, curState
	}
}

// FindDripstoneFluidAboveCauldron searches up to 10 blocks of air above a
// cauldron at (x, y, z) for a downward-pointing `minecraft:pointed_dripstone`
// stalactite tip, traces the stalactite column up to its ceiling support block,
// and returns the fluid above the ceiling (`"water"`, `"lava"`, or `""`).
func FindDripstoneFluidAboveCauldron(getBlock func(x, y, z int) uint16, x, y, z int) string {
	if getBlock == nil {
		return ""
	}
	initCauldronStates()

	tipY := -1000
	for dy := 1; dy <= MaxDripstoneSearchDistance; dy++ {
		cy := y + dy
		st := getBlock(x, cy, z)
		if st == world.StateAir {
			continue
		}
		if pointedDripstoneTipIDs[st] {
			tipY = cy
		}
		break
	}
	if tipY == -1000 {
		return ""
	}

	topStalactiteY := tipY
	for cy := tipY + 1; cy <= tipY+64; cy++ {
		st := getBlock(x, cy, z)
		if pointedDripstoneDown[st] {
			topStalactiteY = cy
			continue
		}
		break
	}

	ceilingY := topStalactiteY + 1
	ceilingState := getBlock(x, ceilingY, z)
	if ceilingState == world.StateAir || world.BlockName(ceilingState) == "minecraft:pointed_dripstone" {
		return ""
	}

	fluidState := getBlock(x, ceilingY+1, z)
	if fluidState == world.StateWater || world.BlockName(fluidState) == "minecraft:water" {
		return "water"
	}
	if fluidState == world.StateLava || world.BlockName(fluidState) == "minecraft:lava" {
		return "lava"
	}
	return ""
}

// TryFillCauldronFromDripstone checks if a pointed_dripstone stalactite with a
// water or lava source above its ceiling block is dripping into the cauldron at
// (x, y, z) in dim, and advances the cauldron's fill state accordingly:
//   - `"water"`: fills `cauldron` -> `water_cauldron[level=1]` or increments `water_cauldron[level=1..2]`
//   - `"lava"`: fills empty `cauldron` -> `lava_cauldron`
func (s *Server) TryFillCauldronFromDripstone(
	dim string,
	x, y, z int,
	rng *rand.Rand,
	force bool,
) (filled bool, nextState uint16) {
	if s == nil {
		return false, world.StateAir
	}
	normDim := world.NormalizeDimension(dim)
	cache := s.DimensionChunks(normDim)
	if cache == nil {
		return false, world.StateAir
	}
	curState := cache.GetBlock(x, y, z)
	kind, lvl, isCauld := ParseCauldronState(curState)
	if !isCauld {
		return false, curState
	}

	fluid := FindDripstoneFluidAboveCauldron(cache.GetBlock, x, y, z)
	switch fluid {
	case "water":
		if kind != CauldronKindEmpty && (kind != CauldronKindWater || lvl >= CauldronMaxFillLevel) {
			return false, curState
		}
		if !force && rng != nil && rng.Float32() >= DripstoneWaterFillChance {
			return false, curState
		}
		nextLvl := 1
		if kind == CauldronKindWater {
			nextLvl = lvl + 1
		}
		nextState = CauldronWaterBlockState(nextLvl)
		s.setCauldronBlockAndNotify(normDim, cache, x, y, z, nextState, SoundDripWaterIntoCauldron)
		return true, nextState

	case "lava":
		if kind != CauldronKindEmpty {
			return false, curState
		}
		if !force && rng != nil && rng.Float32() >= DripstoneLavaFillChance {
			return false, curState
		}
		nextState = CauldronLavaBlockState()
		s.setCauldronBlockAndNotify(normDim, cache, x, y, z, nextState, SoundDripLavaIntoCauldron)
		return true, nextState

	default:
		return false, curState
	}
}

// --- Extinguishing Burning Entities & Lava Cauldron Contact ---

type serverFireState struct {
	mu          sync.Mutex
	playerTicks map[*PlayerSession]int
	entityTicks map[int32]int
}

var serverFireStates sync.Map // map[*Server]*serverFireState

func (s *Server) fireState() *serverFireState {
	if v, ok := serverFireStates.Load(s); ok {
		return v.(*serverFireState)
	}
	actual, _ := serverFireStates.LoadOrStore(s, &serverFireState{
		playerTicks: make(map[*PlayerSession]int),
		entityTicks: make(map[int32]int),
	})
	return actual.(*serverFireState)
}

// SetPlayerFireTicks sets the remaining burning ticks on session (0 extinguishes).
func (s *Server) SetPlayerFireTicks(session *PlayerSession, ticks int) {
	if s == nil || session == nil {
		return
	}
	fs := s.fireState()
	fs.mu.Lock()
	if ticks <= 0 {
		delete(fs.playerTicks, session)
	} else {
		fs.playerTicks[session] = ticks
	}
	fs.mu.Unlock()
}

// PlayerFireTicks returns the remaining burning ticks on session.
func (s *Server) PlayerFireTicks(session *PlayerSession) int {
	if s == nil || session == nil {
		return 0
	}
	fs := s.fireState()
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return fs.playerTicks[session]
}

// IsPlayerOnFire reports whether session currently has fire ticks > 0.
func (s *Server) IsPlayerOnFire(session *PlayerSession) bool {
	return s.PlayerFireTicks(session) > 0
}

// SetEntityFireTicks sets the remaining burning ticks on entityID (0 extinguishes).
func (s *Server) SetEntityFireTicks(entityID int32, ticks int) {
	if s == nil || entityID <= 0 {
		return
	}
	fs := s.fireState()
	fs.mu.Lock()
	if ticks <= 0 {
		delete(fs.entityTicks, entityID)
	} else {
		fs.entityTicks[entityID] = ticks
	}
	fs.mu.Unlock()
}

// EntityFireTicks returns the remaining burning ticks on entityID.
func (s *Server) EntityFireTicks(entityID int32) int {
	if s == nil || entityID <= 0 {
		return 0
	}
	fs := s.fireState()
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return fs.entityTicks[entityID]
}

// IsEntityOnFire reports whether entityID currently has fire ticks > 0.
func (s *Server) IsEntityOnFire(entityID int32) bool {
	return s.EntityFireTicks(entityID) > 0
}

// HandlePlayerInsideCauldron checks if session is standing inside the fluid of
// a cauldron at (x, y, z) in session's dimension:
//   - For `water_cauldron` or `powder_snow_cauldron` (`level >= 1`): if session
//     is burning (`IsPlayerOnFire(session)` or `wasOnFire == true`), extinguishes
//     session (`SetPlayerFireTicks(session, 0)`), lowers the cauldron fill level
//     by 1, updates comparators, and plays `entity.generic.extinguish_fire`.
//   - For `lava_cauldron`: sets session on fire (`DefaultCauldronLavaFireTicks`)
//     and applies `LavaCauldronEntityDamage` (4.0) via `DamagePlayerWithArmor`.
func (s *Server) HandlePlayerInsideCauldron(session *PlayerSession, x, y, z int, wasOnFire bool) (extinguished bool, nextState uint16) {
	if s == nil || session == nil || session.IsDead() {
		return false, world.StateAir
	}
	dim := session.Dimension()
	if dim == "" {
		dim = world.DimensionOverworld
	}
	normDim := world.NormalizeDimension(dim)
	cache := s.DimensionChunks(normDim)
	if cache == nil {
		return false, world.StateAir
	}
	curState := cache.GetBlock(x, y, z)
	kind, lvl, isCauld := ParseCauldronState(curState)
	if !isCauld || kind == CauldronKindEmpty {
		return false, curState
	}

	snap := session.Snapshot()
	if snap.Y >= float64(y)+CauldronContentHeight(lvl) {
		return false, curState
	}

	switch kind {
	case CauldronKindWater, CauldronKindPowderSnow:
		if !wasOnFire && !s.IsPlayerOnFire(session) {
			return false, curState
		}
		s.SetPlayerFireTicks(session, 0)
		nextState, _ = LowerCauldronFillLevel(curState)
		s.setCauldronBlockAndNotify(normDim, cache, x, y, z, nextState, SoundGenericExtinguishFire)
		return true, nextState

	case CauldronKindLava:
		if !session.HasFireResistance() && snap.GameMode != GameModeCreative && snap.GameMode != GameModeSpectator {
			s.SetPlayerFireTicks(session, DefaultCauldronLavaFireTicks)
			s.DamagePlayerWithArmor(session, LavaCauldronEntityDamage, "lava")
		}
		return false, curState

	default:
		return false, curState
	}
}

// HandleEntityInsideCauldron checks if the mob/entity `entityID` is inside the
// fluid of a cauldron at (x, y, z) in dim:
//   - For `water_cauldron` or `powder_snow_cauldron` (`level >= 1`): if the
//     entity is burning (`IsEntityOnFire(entityID)` or `wasOnFire == true`),
//     extinguishes it (`SetEntityFireTicks(entityID, 0)`), lowers the cauldron
//     fill level by 1, updates comparators, and plays `entity.generic.extinguish_fire`.
//   - For `lava_cauldron`: sets the entity on fire and deals `4.0` lava damage.
func (s *Server) HandleEntityInsideCauldron(dim string, entityID int32, x, y, z int, wasOnFire bool) (extinguished bool, nextState uint16) {
	if s == nil || entityID <= 0 {
		return false, world.StateAir
	}
	normDim := world.NormalizeDimension(dim)
	cache := s.DimensionChunks(normDim)
	if cache == nil {
		return false, world.StateAir
	}
	curState := cache.GetBlock(x, y, z)
	kind, lvl, isCauld := ParseCauldronState(curState)
	if !isCauld || kind == CauldronKindEmpty {
		return false, curState
	}

	if s.entities != nil {
		if ent := s.entities.Get(entityID); ent != nil {
			if ent.Y >= float64(y)+CauldronContentHeight(lvl) {
				return false, curState
			}
		}
	}

	switch kind {
	case CauldronKindWater, CauldronKindPowderSnow:
		if !wasOnFire && !s.IsEntityOnFire(entityID) {
			return false, curState
		}
		s.SetEntityFireTicks(entityID, 0)
		nextState, _ = LowerCauldronFillLevel(curState)
		s.setCauldronBlockAndNotify(normDim, cache, x, y, z, nextState, SoundGenericExtinguishFire)
		return true, nextState

	case CauldronKindLava:
		s.SetEntityFireTicks(entityID, DefaultCauldronLavaFireTicks)
		if s.entities != nil {
			if ent := s.entities.Get(entityID); ent != nil && !ent.IsItem() {
				killed := s.applyDamageToMob(ent, LavaCauldronEntityDamage, nil, 0, false, false)
				if killed {
					s.entities.Remove(entityID)
				}
			}
		}
		return false, curState

	default:
		return false, curState
	}
}
