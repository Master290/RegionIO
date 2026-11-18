package server

import (
	"strconv"
	"strings"

	"regionio/internal/nbt"
	"regionio/internal/world"
)

// SetDropperContainerTransferHook registers an optional callback that Agent B
// can use to handle dropper-to-container transfers for custom container types.
func (s *Server) SetDropperContainerTransferHook(fn DropperContainerTransferFunc) {
	if s == nil {
		return
	}
	ds := s.dispenserState()
	ds.mu.Lock()
	ds.customTransferFn = fn
	ds.mu.Unlock()
}

// IsDropperTargetContainer reports whether the block state at the front of a
// dropper provides a vanilla Container / WorldlyContainer (matching
// HopperBlockEntity.getBlockContainer in 26.1.2) that intercepts dropper item
// ejection instead of dropping into air.
func IsDropperTargetContainer(state uint16) bool {
	if state == world.StateAir {
		return false
	}
	if world.IsCopperChestBlock(state) {
		return true
	}
	name := world.BlockName(state)
	switch name {
	case "minecraft:dispenser", "minecraft:dropper",
		"minecraft:chest", "minecraft:trapped_chest", "minecraft:barrel",
		"minecraft:furnace", "minecraft:blast_furnace", "minecraft:smoker",
		"minecraft:brewing_stand", "minecraft:hopper",
		"minecraft:chiseled_bookshelf", "minecraft:crafter", "minecraft:decorated_pot",
		"minecraft:composter", "minecraft:jukebox":
		return true
	}
	return IsShulkerBoxItemOrBlock(name)
}

// DispenseFromDropper executes a single activation of the `minecraft:dropper`
// at (dim, x, y, z) following vanilla 26.1.2 DropperBlock.dispenseFrom:
//   - Unpacks any pending LootTable and picks a uniformly random non-empty slot
//     (0..8). If empty, emits LevelEvent 1001 (dispenser fail) and returns false.
//   - If a block Container / WorldlyContainer or vehicle Container entity is at
//     (x+dx, y+dy, z+dz), attempts to transfer 1 item into that container
//     respecting WorldlyContainer face rules without playing dispense sound/smoke
//     (and keeping the item in the dropper if the target container rejects it).
//   - Otherwise, ejects 1 item as a dropped item entity with vanilla velocity
//     and spread, preserving ItemMeta / Potion / ShulkerBoxItemData, and emits
//     LevelEvent 1000 + smoke LevelEvent 2000.
func (s *Server) DispenseFromDropper(dim string, x, y, z int) bool {
	if s == nil {
		return false
	}
	normDim := normalizeDispenserDim(dim)
	cache := s.BindNoteBlockAndJukeboxRedstone(normDim)
	if cache == nil {
		return false
	}
	state := cache.GetBlock(x, y, z)
	if world.BlockName(state) != "minecraft:dropper" {
		return false
	}
	facing := s.DispenserFacingAt(normDim, x, y, z)

	ds := s.dispenserState()
	ds.mu.Lock()
	be := s.getOrCreateDispenserLocked(ds, normDim, x, y, z)
	be.IsDropper = true
	unpacked := s.unpackDispenserLootTableLocked(be)
	slot := ChooseRandomDispenserSlot(be.Slots, ds.rng)
	if slot < 0 {
		ds.mu.Unlock()
		if unpacked {
			_ = s.SaveChunkDispensers(normDim, int32(x>>4), int32(z>>4))
		}
		// Vanilla DropperBlock.dispenseFrom: levelEvent(1001, pos, 0) on empty dropper.
		s.broadcastDispenserEffects(normDim, x, y, z, facing, LevelEventSoundFailDispense, false)
		return false
	}
	item := be.Slots[slot]
	meta := be.SlotMeta[slot].Clone()
	potionType := be.SlotPotions[slot]
	shulkerData := s.dispenserSlotShulkerDataLocked(be, slot)
	rawComp := world.CloneNBTCompound(be.SlotComponents[slot])
	rng := ds.rng
	customFn := ds.customTransferFn
	ds.mu.Unlock()
	if unpacked {
		_ = s.SaveChunkDispensers(normDim, int32(x>>4), int32(z>>4))
	}

	single := world.NewItemStack(item.ResolvedItemID(), 1)
	if single.IsEmpty() {
		single = world.NewNamedItemStack(item.ResolvedID(), 1)
	}

	dx, dy, dz := DispenserFacingVector(facing)
	fx, fy, fz := x+dx, y+dy, z+dz
	frontState := cache.GetBlock(fx, fy, fz)

	// 1. Check custom hook first if registered.
	if customFn != nil {
		if customFn(normDim, fx, fy, fz, facing, single, meta, potionType) {
			s.consumeDropperSlotOne(normDim, x, y, z, slot)
			s.updateDispenserComparatorsAndBroadcast(normDim, fx, fy, fz)
			return true
		}
	}

	// 2. Check block Container / WorldlyContainer OR entity Container at (fx, fy, fz).
	hasBlockContainer := fy >= world.MinY && fy < world.MinY+world.WorldHeight && IsDropperTargetContainer(frontState)
	vehicleContainers := s.findVehicleContainersInBlock(normDim, fx, fy, fz)
	if hasBlockContainer || len(vehicleContainers) > 0 {
		var transferred bool
		if hasBlockContainer {
			transferred = s.tryDropperTransferIntoContainer(normDim, cache, fx, fy, fz, facing, frontState, single, meta, potionType, shulkerData, rawComp)
		} else {
			transferred = s.tryInsertIntoVehicleContainerAt(normDim, fx, fy, fz, single)
		}
		if transferred {
			s.consumeDropperSlotOne(normDim, x, y, z, slot)
			s.updateDispenserComparatorsAndBroadcast(normDim, fx, fy, fz)
		}
		// Vanilla DropperBlock: when facing a container, even if the container
		// cannot accept the item, the dropper does NOT drop the item into the world.
		return transferred
	}

	// 3. No container in front -> eject 1 item into the world as a dropped item entity.
	s.spawnDispensedItemEntityFull(normDim, x, y, z, facing, single, meta, potionType, shulkerData, rng)
	s.consumeDropperSlotOne(normDim, x, y, z, slot)
	s.broadcastDispenserEffects(normDim, x, y, z, facing, LevelEventSoundDispense, true)
	s.EmitGameEvent(normDim, GameEventBlockActivate, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, VibrationSource{})
	return true
}

func (s *Server) consumeDropperSlotOne(dim string, x, y, z, slot int) {
	ds := s.dispenserState()
	ds.mu.Lock()
	be := s.getOrCreateDispenserLocked(ds, dim, x, y, z)
	decrementDispenserSlotLocked(be, slot)
	ds.mu.Unlock()
	_ = s.SaveChunkDispensers(dim, int32(x>>4), int32(z>>4))
	s.updateDispenserComparatorsAndBroadcast(dim, x, y, z)
	_ = s.SyncDispenserViewers(nil, x, y, z)
}

func (s *Server) tryDropperTransferIntoContainer(
	dim string,
	cache *world.Cache,
	fx, fy, fz int,
	dropperFacing string,
	frontState uint16,
	single world.ItemStack,
	meta ItemMeta,
	potionType string,
	shulkerData *ShulkerBoxItemData,
	rawComp *nbt.Compound,
) bool {
	frontName := world.BlockName(frontState)
	var shulkerArr *[ChestSlotCount]world.ItemStack
	if shulkerData != nil && shulkerData.HasAnyItems() {
		cp := shulkerData.Slots
		shulkerArr = &cp
	}

	switch {
	case frontName == "minecraft:dispenser" || frontName == "minecraft:dropper":
		return s.hopperInsertIntoDispenserFull(dim, fx, fy, fz, single, meta, potionType, shulkerData, rawComp)

	case IsChestBlock(frontState) || world.IsCopperChestBlock(frontState) || frontName == "minecraft:trapped_chest" || frontName == "minecraft:barrel":
		return s.hopperInsertIntoChestOrBarrelWithMeta(dim, cache, fx, fy, fz, frontState, single, meta, potionType, shulkerArr, rawComp)

	case IsShulkerBoxItemOrBlock(frontName):
		// Vanilla ShulkerBoxBlockEntity.canPlaceItemThroughFace: shulker boxes cannot be nested inside shulker boxes!
		if IsShulkerBoxItemOrBlock(single.ResolvedID()) {
			return false
		}
		return s.hopperInsertIntoChestOrBarrelWithMeta(dim, cache, fx, fy, fz, frontState, single, meta, potionType, nil, rawComp)

	case IsFurnaceBlock(frontState):
		return s.hopperInsertIntoFurnace(dim, fx, fy, fz, dropperFacing, single)

	case frontName == "minecraft:brewing_stand":
		entryFace := oppositeDropperDirection(dropperFacing)
		inserted, _ := s.HopperTryInsertIntoBrewingStand(dim, fx, fy, fz, entryFace, single, potionType)
		return inserted

	case frontName == "minecraft:composter":
		entryFace := oppositeDropperDirection(dropperFacing)
		return s.HopperInsertIntoComposter(dim, fx, fy, fz, entryFace, single)

	case frontName == "minecraft:jukebox":
		if !IsMusicDiscItem(single) {
			return false
		}
		inserted, _, _ := s.HopperTryInsertIntoJukebox(dim, fx, fy, fz, single)
		return inserted

	case frontName == "minecraft:hopper":
		return s.HopperInsertIntoHopper(dim, fx, fy, fz, single, meta, potionType)

	case frontName == "minecraft:chiseled_bookshelf":
		return s.HopperInsertIntoChiseledBookshelf(dim, fx, fy, fz, single, meta)

	case frontName == "minecraft:crafter":
		return s.HopperInsertIntoCrafter(dim, fx, fy, fz, single, meta)

	case frontName == "minecraft:decorated_pot":
		return s.HopperInsertIntoDecoratedPot(dim, fx, fy, fz, single, meta)

	default:
		return false
	}
}

func (s *Server) transferIntoChestAt(x, y, z int, single world.ItemStack) bool {
	return s.transferIntoChestAtInDim(s.resolveLegacyStorageDim(x, y, z), x, y, z, single)
}

func (s *Server) transferIntoChestAtInDim(dim string, x, y, z int, single world.ItemStack) bool {
	if s == nil || single.IsEmpty() {
		return false
	}
	cache := s.DimensionChunks(dim)
	var state uint16
	if cache != nil {
		state = cache.GetBlock(x, y, z)
	}
	return s.hopperInsertIntoChestOrBarrelWithMeta(dim, cache, x, y, z, state, single, ItemMeta{}, "", nil, nil)
}

func (s *Server) transferIntoShulkerBoxAt(dim string, x, y, z int, single world.ItemStack) bool {
	if s == nil || single.IsEmpty() || IsShulkerBoxItemOrBlock(single.ResolvedID()) {
		return false
	}
	cache := s.DimensionChunks(dim)
	var state uint16
	if cache != nil {
		state = cache.GetBlock(x, y, z)
	}
	return s.hopperInsertIntoChestOrBarrelWithMeta(dim, cache, x, y, z, state, single, ItemMeta{}, "", nil, nil)
}

// transferIntoFurnaceAt inserts 1 item into a furnace, blast_furnace, or smoker
// following vanilla WorldlyContainer side rules.
func (s *Server) transferIntoFurnaceAt(x, y, z int, dropperFacing string, single world.ItemStack) bool {
	return s.hopperInsertIntoFurnace(world.DimensionOverworld, x, y, z, dropperFacing, single)
}

func oppositeDropperDirection(dir string) string {
	switch strings.ToLower(dir) {
	case "down":
		return "up"
	case "up":
		return "down"
	case "north":
		return "south"
	case "south":
		return "north"
	case "west":
		return "east"
	case "east":
		return "west"
	default:
		return "up"
	}
}

func compostChanceForItem(itemID string) float32 {
	return world.CompostChanceForItem(itemID)
}

var _ = strconv.Itoa
