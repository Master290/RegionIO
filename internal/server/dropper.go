package server

import (
	"strconv"
	"strings"

	"regionio/internal/world"
)

// SetDropperContainerTransferHook registers an optional callback that Agent B
// can use to handle dropper-to-container transfers for custom container types
// (such as hoppers, barrels, chiseled bookshelves, or crafters).
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
// dropper is a container (or hopper-like receiver such as a composter or empty
// jukebox) that intercepts dropper item ejection instead of dropping into air.
func IsDropperTargetContainer(state uint16) bool {
	if state == world.StateAir {
		return false
	}
	name := world.BlockName(state)
	switch name {
	case "minecraft:dispenser", "minecraft:dropper",
		"minecraft:chest", "minecraft:trapped_chest", "minecraft:barrel",
		"minecraft:furnace", "minecraft:blast_furnace", "minecraft:smoker",
		"minecraft:brewing_stand", "minecraft:hopper",
		"minecraft:chiseled_bookshelf", "minecraft:crafter", "minecraft:decorated_pot",
		"minecraft:composter":
		return true
	case "minecraft:jukebox":
		props := world.BlockProperties(state)
		return props["has_record"] != "true"
	}
	return IsShulkerBoxItemOrBlock(name)
}

// DispenseFromDropper executes a single activation of the `minecraft:dropper`
// at (dim, x, y, z) following vanilla 26.1.2 DropperBlock.dispenseFrom:
//   - Picks a uniformly random non-empty slot (0..8). If empty, emits
//     LevelEvent 1001 (dispenser fail) and returns false.
//   - If the block in front (x+dx, y+dy, z+dz) is a container, attempts to
//     transfer 1 item into that container without playing dispense sound/smoke
//     (and without dropping the item if the target container is full).
//   - Otherwise, ejects 1 item as a dropped item entity with vanilla velocity
//     and Gaussian spread, and emits LevelEvent 1000 + smoke LevelEvent 2000.
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
	slot := ChooseRandomDispenserSlot(be.Slots, ds.rng)
	if slot < 0 {
		ds.mu.Unlock()
		s.broadcastDispenserEffects(normDim, x, y, z, facing, LevelEventSoundFailDispense, false)
		s.EmitGameEvent(normDim, GameEventBlockActivate, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, VibrationSource{})
		return false
	}
	item := be.Slots[slot]
	meta := be.SlotMeta[slot].Clone()
	potionType := be.SlotPotions[slot]
	rng := ds.rng
	customFn := ds.customTransferFn
	ds.mu.Unlock()

	single := world.NewItemStack(item.ResolvedItemID(), 1)
	if single.IsEmpty() {
		single = world.NewNamedItemStack(item.ResolvedID(), 1)
	}

	dx, dy, dz := DispenserFacingVector(facing)
	fx, fy, fz := x+dx, y+dy, z+dz
	frontState := cache.GetBlock(fx, fy, fz)

	// 1. Check if the block in front is a container (or custom hook handles it).
	if customFn != nil {
		if customFn(normDim, fx, fy, fz, facing, single, meta, potionType) {
			s.consumeDropperSlotOne(normDim, x, y, z, slot)
			s.updateDispenserComparatorsAndBroadcast(normDim, fx, fy, fz)
			return true
		}
	}

	if IsDropperTargetContainer(frontState) {
		transferred := s.tryDropperTransferIntoContainer(normDim, cache, fx, fy, fz, facing, frontState, single, meta, potionType)
		if transferred {
			s.consumeDropperSlotOne(normDim, x, y, z, slot)
			s.updateDispenserComparatorsAndBroadcast(normDim, fx, fy, fz)
		}
		// Vanilla DropperBlock: when facing a container, even if the container
		// cannot accept the item, the dropper does NOT drop the item into the world.
		return transferred
	}

	// 2. No container in front -> eject 1 item into the world as a dropped item entity.
	s.spawnDispensedItemEntity(normDim, x, y, z, facing, single, rng)
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
) bool {
	frontName := world.BlockName(frontState)
	switch {
	case frontName == "minecraft:dispenser" || frontName == "minecraft:dropper":
		return s.HopperInsertIntoDispenser(dim, fx, fy, fz, single, meta, potionType)

	case IsChestBlock(frontState) || frontName == "minecraft:trapped_chest" || frontName == "minecraft:barrel":
		return s.transferIntoChestAt(fx, fy, fz, single)

	case IsShulkerBoxItemOrBlock(frontName):
		// Vanilla ShulkerBoxBlockEntity.canPlaceItemThroughFace: shulker boxes cannot be nested inside shulker boxes!
		if IsShulkerBoxItemOrBlock(single.ResolvedID()) {
			return false
		}
		return s.transferIntoShulkerBoxAt(dim, fx, fy, fz, single)

	case IsFurnaceBlock(frontState):
		return s.transferIntoFurnaceAt(fx, fy, fz, dropperFacing, single)

	case frontName == "minecraft:brewing_stand":
		entryFace := oppositeDropperDirection(dropperFacing)
		inserted, _ := s.HopperTryInsertIntoBrewingStand(dim, fx, fy, fz, entryFace, single, potionType)
		return inserted

	case frontName == "minecraft:composter":
		return s.transferIntoComposterAt(dim, cache, fx, fy, fz, frontState, single)

	case frontName == "minecraft:jukebox":
		if !IsMusicDiscItem(single) {
			return false
		}
		inserted, _ := s.InsertJukeboxDisc(dim, fx, fy, fz, single)
		return inserted

	case frontName == "minecraft:hopper":
		return s.HopperInsertIntoHopper(dim, fx, fy, fz, single, meta, potionType)

	case frontName == "minecraft:chiseled_bookshelf":
		return s.HopperInsertIntoChiseledBookshelf(dim, fx, fy, fz, single, meta)

	case frontName == "minecraft:crafter":
		return s.HopperInsertIntoCrafter(dim, fx, fy, fz, single, meta)

	default:
		return false
	}
}

func (s *Server) transferIntoChestAt(x, y, z int, single world.ItemStack) bool {
	if s == nil || single.IsEmpty() {
		return false
	}
	slots := s.ChestSlots(x, y, z)
	maxStack := DispenserItemMaxStackSize(single)
	itemID := single.ResolvedItemID()

	if maxStack > 1 {
		for i := 0; i < ChestSlotCount; i++ {
			cur := slots[i]
			if !cur.IsEmpty() && cur.ResolvedItemID() == itemID && cur.Count < maxStack {
				s.SetChestSlot(x, y, z, i, world.NewItemStack(itemID, cur.Count+1))
				_ = s.syncChestViewers(nil, x, y, z)
				return true
			}
		}
	}
	for i := 0; i < ChestSlotCount; i++ {
		if slots[i].IsEmpty() {
			s.SetChestSlot(x, y, z, i, single)
			_ = s.syncChestViewers(nil, x, y, z)
			return true
		}
	}
	return false
}

func (s *Server) transferIntoShulkerBoxAt(dim string, x, y, z int, single world.ItemStack) bool {
	if s == nil || single.IsEmpty() {
		return false
	}
	slots := s.ShulkerBoxSlots(dim, x, y, z)
	maxStack := DispenserItemMaxStackSize(single)
	itemID := single.ResolvedItemID()

	if maxStack > 1 {
		for i := 0; i < ChestSlotCount; i++ {
			cur := slots[i]
			if !cur.IsEmpty() && cur.ResolvedItemID() == itemID && cur.Count < maxStack {
				slots[i] = world.NewItemStack(itemID, cur.Count+1)
				s.SetShulkerBoxSlots(dim, x, y, z, slots)
				if dim == world.DimensionOverworld {
					s.SetChestSlot(x, y, z, i, slots[i])
				}
				return true
			}
		}
	}
	for i := 0; i < ChestSlotCount; i++ {
		if slots[i].IsEmpty() {
			slots[i] = single
			s.SetShulkerBoxSlots(dim, x, y, z, slots)
			if dim == world.DimensionOverworld {
				s.SetChestSlot(x, y, z, i, single)
			}
			return true
		}
	}
	return false
}

// transferIntoFurnaceAt inserts 1 item into a furnace, blast_furnace, or smoker
// following vanilla WorldlyContainer side rules:
//   - Dropper facing "down" (entering top face of furnace) -> Slot 0 (input)
//   - Dropper facing horizontal or "up" (entering side or bottom of furnace) ->
//     Slot 1 (fuel), only if the item is valid furnace fuel or an empty bucket.
func (s *Server) transferIntoFurnaceAt(x, y, z int, dropperFacing string, single world.ItemStack) bool {
	if s == nil || single.IsEmpty() {
		return false
	}
	slots := s.FurnaceSlots(x, y, z)
	targetSlot := world.FurnaceSlotFuel
	if strings.EqualFold(dropperFacing, "down") {
		targetSlot = world.FurnaceSlotInput
	} else {
		if !CanPlaceInFurnaceFuelSlot(single) {
			return false
		}
	}
	cur := slots[targetSlot]
	maxStack := DispenserItemMaxStackSize(single)
	if cur.IsEmpty() {
		return s.SetFurnaceSlot(x, y, z, targetSlot, single)
	}
	if cur.ResolvedItemID() == single.ResolvedItemID() && cur.Count < maxStack {
		return s.SetFurnaceSlot(x, y, z, targetSlot, world.NewItemStack(cur.ResolvedItemID(), cur.Count+1))
	}
	return false
}

func (s *Server) transferIntoComposterAt(dim string, cache *world.Cache, x, y, z int, state uint16, single world.ItemStack) bool {
	if cache == nil || single.IsEmpty() {
		return false
	}
	chance := compostChanceForItem(single.ResolvedID())
	if chance <= 0 {
		return false
	}
	props := world.BlockProperties(state)
	lvl, _ := strconv.Atoi(props["level"])
	if lvl >= 7 {
		return false
	}
	ds := s.dispenserState()
	ds.mu.Lock()
	roll := ds.rng.Float64()
	ds.mu.Unlock()

	nextLvl := lvl
	if lvl == 0 || roll < float64(chance) {
		nextLvl = lvl + 1
	}
	if nextLvl > lvl {
		nextStr := strconv.Itoa(nextLvl)
		if nextSt, ok := world.WithBlockProperty(state, "level", nextStr); ok {
			s.setDispenserWorldBlock(dim, cache, x, y, z, nextSt)
		}
	}
	return true
}

func compostChanceForItem(itemID string) float32 {
	norm := canonicalResourceName(itemID)
	if strings.HasSuffix(norm, "_leaves") || strings.HasSuffix(norm, "_sapling") || strings.HasSuffix(norm, "_seeds") {
		return 0.3
	}
	switch norm {
	case "minecraft:short_grass", "minecraft:kelp", "minecraft:moss_carpet", "minecraft:seagrass",
		"minecraft:small_dripleaf", "minecraft:sweet_berries", "minecraft:glow_berries", "minecraft:hanging_roots":
		return 0.3
	case "minecraft:dried_kelp", "minecraft:tall_grass", "minecraft:cactus", "minecraft:sugar_cane",
		"minecraft:vine", "minecraft:nether_sprouts", "minecraft:weeping_vines", "minecraft:twisting_vines",
		"minecraft:melon_slice", "minecraft:glow_lichen":
		return 0.5
	case "minecraft:apple", "minecraft:beetroot", "minecraft:carrot", "minecraft:cocoa_beans",
		"minecraft:potato", "minecraft:wheat", "minecraft:melon", "minecraft:pumpkin", "minecraft:carved_pumpkin",
		"minecraft:sea_pickle", "minecraft:brown_mushroom", "minecraft:red_mushroom",
		"minecraft:crimson_fungus", "minecraft:warped_fungus", "minecraft:lily_pad",
		"minecraft:fern", "minecraft:large_fern", "minecraft:dandelion", "minecraft:poppy",
		"minecraft:blue_orchid", "minecraft:allium", "minecraft:azure_bluet", "minecraft:oxeye_daisy",
		"minecraft:cornflower", "minecraft:lily_of_the_valley", "minecraft:wither_rose",
		"minecraft:sunflower", "minecraft:lilac", "minecraft:rose_bush", "minecraft:peony",
		"minecraft:moss_block", "minecraft:azalea", "minecraft:big_dripleaf", "minecraft:shroomlight":
		return 0.65
	case "minecraft:baked_potato", "minecraft:bread", "minecraft:cookie", "minecraft:hay_block",
		"minecraft:nether_wart_block", "minecraft:warped_wart_block", "minecraft:flowering_azalea",
		"minecraft:red_mushroom_block", "minecraft:brown_mushroom_block":
		return 0.85
	case "minecraft:cake", "minecraft:pumpkin_pie":
		return 1.0
	default:
		return 0
	}
}

func oppositeDropperDirection(facing string) string {
	switch strings.ToLower(facing) {
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
		return "south"
	}
}
