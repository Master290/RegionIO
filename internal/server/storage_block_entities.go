package server

import (
	"errors"
	"sort"
	"strings"
	"sync"

	"regionio/internal/nbt"
	"regionio/internal/protocol"
	"regionio/internal/world"
	"regionio/internal/worldgen"
)

// MenuTypeShulkerBox is BuiltInRegistries.MENU ID 20 (`minecraft:shulker_box`)
// in Minecraft 26.1.2 (protocol 775): 27 shulker slots + 36 player slots.
const MenuTypeShulkerBox int32 = 20

// StorageBlockEntity represents the authoritative server-side block entity state
// for 27-slot vanilla 26.1.2 storage blocks:
//   - `minecraft:chest` (including single/double chests and all copper_chest variants)
//   - `minecraft:trapped_chest`
//   - `minecraft:barrel`
//   - `minecraft:shulker_box` (undyed and all 16 colored `*_shulker_box` blocks)
type StorageBlockEntity struct {
	ID             string
	Dimension      string
	X, Y, Z        int
	Slots          [ChestSlotCount]world.ItemStack
	SlotMeta       [ChestSlotCount]ItemMeta
	SlotPotions    [ChestSlotCount]string
	SlotShulker    [ChestSlotCount]*[ChestSlotCount]world.ItemStack
	SlotComponents [ChestSlotCount]*nbt.Compound
	CustomName     string
	CustomNameTag  nbt.Tag
	Lock           string
	LockTag        nbt.Tag
	LootTable      string
	LootTableSeed  int64
	ComponentsTag  *nbt.Compound
}

// Clone returns a deep copy of the StorageBlockEntity.
func (be *StorageBlockEntity) Clone() *StorageBlockEntity {
	if be == nil {
		return nil
	}
	cp := *be
	cp.CustomNameTag = world.CloneNBTTag(be.CustomNameTag)
	cp.LockTag = world.CloneNBTTag(be.LockTag)
	cp.ComponentsTag = world.CloneNBTCompound(be.ComponentsTag)
	for i := 0; i < ChestSlotCount; i++ {
		cp.SlotMeta[i] = be.SlotMeta[i].Clone()
		cp.SlotComponents[i] = world.CloneNBTCompound(be.SlotComponents[i])
		if be.SlotShulker[i] != nil {
			arr := *be.SlotShulker[i]
			cp.SlotShulker[i] = &arr
		}
	}
	return &cp
}

// HasAnyItems reports whether any of the 27 slots contain a non-empty stack.
func (be *StorageBlockEntity) HasAnyItems() bool {
	if be == nil {
		return false
	}
	for i := 0; i < ChestSlotCount; i++ {
		if !be.Slots[i].IsEmpty() && be.Slots[i].Count > 0 {
			return true
		}
	}
	return false
}

// ShulkerBoxItemData holds the vanilla 26.1.2 item data components carried by a
// `shulker_box` (or `<color>_shulker_box`) item stack:
//   - `minecraft:container` (non-empty slots with `slot` and `item`)
//   - `minecraft:container_loot` (`loot_table` and optional non-zero `seed`)
//   - `minecraft:custom_name`
//   - `minecraft:lock`
type ShulkerBoxItemData struct {
	Slots          [ChestSlotCount]world.ItemStack
	SlotMeta       [ChestSlotCount]ItemMeta
	SlotPotions    [ChestSlotCount]string
	SlotShulker    [ChestSlotCount]*[ChestSlotCount]world.ItemStack
	SlotComponents [ChestSlotCount]*nbt.Compound
	LootTable      string
	LootTableSeed  int64
	CustomName     string
	CustomNameTag  nbt.Tag
	Lock           string
	LockTag        nbt.Tag
}

// Clone returns a deep copy of d.
func (d *ShulkerBoxItemData) Clone() *ShulkerBoxItemData {
	if d == nil {
		return nil
	}
	cp := *d
	cp.CustomNameTag = world.CloneNBTTag(d.CustomNameTag)
	cp.LockTag = world.CloneNBTTag(d.LockTag)
	for i := 0; i < ChestSlotCount; i++ {
		cp.SlotMeta[i] = d.SlotMeta[i].Clone()
		cp.SlotComponents[i] = world.CloneNBTCompound(d.SlotComponents[i])
		if d.SlotShulker[i] != nil {
			arr := *d.SlotShulker[i]
			cp.SlotShulker[i] = &arr
		}
	}
	return &cp
}

// HasAnyItems reports whether any of the 27 slots in d are non-empty.
func (d *ShulkerBoxItemData) HasAnyItems() bool {
	if d == nil {
		return false
	}
	for i := 0; i < ChestSlotCount; i++ {
		if !d.Slots[i].IsEmpty() && d.Slots[i].Count > 0 {
			return true
		}
	}
	return false
}

// IsEmpty reports whether d carries no items, loot table, custom name, or lock.
func (d *ShulkerBoxItemData) IsEmpty() bool {
	if d == nil {
		return true
	}
	return !d.HasAnyItems() &&
		d.LootTable == "" &&
		d.CustomName == "" &&
		d.CustomNameTag == nil &&
		d.Lock == "" &&
		d.LockTag == nil
}

// StorageBlockEntityIDForBlockName returns the canonical vanilla 26.1.2
// BlockEntityType registry ID for a chest, trapped_chest, copper_chest, barrel,
// or shulker_box (undyed or any of the 16 dyed variants).
// Note: In vanilla 26.1.2, ALL 17 shulker box blocks share `"minecraft:shulker_box"`,
// and all copper chest variants share `"minecraft:chest"`.
func StorageBlockEntityIDForBlockName(blockName string) (string, bool) {
	norm := canonicalResourceName(blockName)
	switch norm {
	case "minecraft:chest":
		return "minecraft:chest", true
	case "minecraft:trapped_chest":
		return "minecraft:trapped_chest", true
	case "minecraft:barrel":
		return "minecraft:barrel", true
	default:
		if IsShulkerBoxItemOrBlock(norm) {
			return "minecraft:shulker_box", true
		}
		if norm == "minecraft:copper_chest" || strings.HasSuffix(norm, "_copper_chest") {
			return "minecraft:chest", true
		}
		return "", false
	}
}

// IsStorageBlockEntityID reports whether id is one of the four 27-slot vanilla
// storage block entity IDs (`minecraft:chest`, `minecraft:trapped_chest`,
// `minecraft:barrel`, `minecraft:shulker_box`).
func IsStorageBlockEntityID(id string) bool {
	switch canonicalResourceName(id) {
	case "minecraft:chest", "minecraft:trapped_chest", "minecraft:barrel", "minecraft:shulker_box":
		return true
	default:
		return false
	}
}

func normalizeStorageDim(dim string) string {
	if strings.TrimSpace(dim) == "" {
		return world.DimensionOverworld
	}
	return world.NormalizeDimension(dim)
}

func (s *Server) loadedDimensionCache(dim string) *world.Cache {
	if s == nil {
		return nil
	}
	norm := world.NormalizeDimension(dim)
	if norm == world.DimensionOverworld && s.chunks != nil {
		return s.chunks
	}
	ds := s.dimState()
	ds.mu.Lock()
	c := ds.caches[norm]
	ds.mu.Unlock()
	return c
}

type storageBlockKey struct {
	dim     string
	x, y, z int
}

type storageChunkKey struct {
	dim    string
	cx, cz int32
}

type serverStorageState struct {
	mu          sync.Mutex
	loadedStore bool
	loadedChunk map[storageChunkKey]bool
	blocks      map[storageBlockKey]*StorageBlockEntity
}

var serverStorages sync.Map // map[*Server]*serverStorageState

func (s *Server) storageState() *serverStorageState {
	if v, ok := serverStorages.Load(s); ok {
		return v.(*serverStorageState)
	}
	st := &serverStorageState{
		loadedChunk: make(map[storageChunkKey]bool),
		blocks:      make(map[storageBlockKey]*StorageBlockEntity),
	}
	actual, _ := serverStorages.LoadOrStore(s, st)
	return actual.(*serverStorageState)
}

func (s *Server) resolveStorageIDAt(dim string, x, y, z int, fallback string) string {
	normDim := normalizeStorageDim(dim)
	if s != nil {
		if cache := s.DimensionChunks(normDim); cache != nil {
			st := cache.GetBlock(x, y, z)
			if id, ok := StorageBlockEntityIDForBlockName(world.BlockName(st)); ok {
				return id
			}
		}
	}
	if id, ok := StorageBlockEntityIDForBlockName(fallback); ok {
		return id
	}
	if IsStorageBlockEntityID(fallback) {
		return canonicalResourceName(fallback)
	}
	return "minecraft:chest"
}

// EncodeStorageBlockEntityNBT serializes a StorageBlockEntity into an exact
// vanilla 26.1.2 chunk `block_entities` compound:
//   - `id` (`"minecraft:chest"`, `"minecraft:trapped_chest"`, `"minecraft:barrel"`, or `"minecraft:shulker_box"`),
//     `x`, `y`, `z`, `keepPacked: 0b`
//   - optional `CustomName` and `lock`
//   - when `LootTable != ""`: `LootTable` and (only when `!= 0`) `LootTableSeed`, omitting `Items`
//   - otherwise: `Items` with `Slot` (Byte), `id` (String), `count` (Int), and optional 26.1.2 `components`
//     (omitted when all slots are empty on `minecraft:shulker_box`, matching
//     `ContainerHelper.saveAllItems(output, itemStacks, false)` in 26.1.2).
func EncodeStorageBlockEntityNBT(be *StorageBlockEntity) *nbt.Compound {
	c := nbt.NewCompound()
	if be == nil {
		return c
	}
	id := canonicalResourceName(be.ID)
	if mapped, ok := StorageBlockEntityIDForBlockName(id); ok {
		id = mapped
	} else if !IsStorageBlockEntityID(id) {
		id = "minecraft:chest"
	}
	c.Set("id", nbt.String(id))
	c.Set("x", nbt.Int(int32(be.X)))
	c.Set("y", nbt.Int(int32(be.Y)))
	c.Set("z", nbt.Int(int32(be.Z)))
	c.Set("keepPacked", nbt.Byte(0))

	if be.CustomNameTag != nil {
		c.Set("CustomName", world.CloneNBTTag(be.CustomNameTag))
	} else if be.CustomName != "" {
		c.Set("CustomName", nbt.String(be.CustomName))
	}
	if be.LockTag != nil {
		c.Set("lock", world.CloneNBTTag(be.LockTag))
	} else if be.Lock != "" {
		c.Set("lock", nbt.String(be.Lock))
	}

	if be.LootTable != "" {
		c.Set("LootTable", nbt.String(canonicalResourceName(be.LootTable)))
		if be.LootTableSeed != 0 {
			c.Set("LootTableSeed", nbt.Long(be.LootTableSeed))
		}
	} else {
		var items []nbt.Tag
		for slot := 0; slot < ChestSlotCount; slot++ {
			st := be.Slots[slot]
			if st.IsEmpty() || st.Count <= 0 {
				continue
			}
			it := nbt.NewCompound()
			it.Set("Slot", nbt.Byte(int8(slot)))
			it.Set("id", nbt.String(st.ResolvedID()))
			it.Set("count", nbt.Int(st.Count))
			if comp := encodeHopperItemComponents(be.SlotComponents[slot], be.SlotMeta[slot], be.SlotPotions[slot], be.SlotShulker[slot]); comp != nil {
				it.Set("components", comp)
			}
			items = append(items, it)
		}
		if id != "minecraft:shulker_box" || len(items) > 0 {
			elemID := byte(nbt.TagEnd)
			if len(items) > 0 {
				elemID = nbt.TagCompound
			}
			c.Set("Items", nbt.List{ElemID: elemID, Elems: items})
		}
	}
	if be.ComponentsTag != nil && be.ComponentsTag.Len() > 0 {
		c.Set("components", world.CloneNBTCompound(be.ComponentsTag))
	}
	return c
}

// DecodeStorageBlockEntityNBT deserializes a chest, trapped_chest, barrel, or
// shulker_box block entity from a vanilla 26.1.2 `block_entities` NBT compound.
func DecodeStorageBlockEntityNBT(c *nbt.Compound, fallbackDim string) (*StorageBlockEntity, bool) {
	if c == nil {
		return nil, false
	}
	rawID, _ := nbtGetString(c, "id")
	id := canonicalResourceName(rawID)
	if mapped, ok := StorageBlockEntityIDForBlockName(id); ok {
		id = mapped
	} else if !IsStorageBlockEntityID(id) {
		return nil, false
	}

	dim, _ := nbtGetString(c, "Dimension")
	if dim == "" {
		dim, _ = nbtGetString(c, "dimension")
	}
	if dim == "" {
		dim = fallbackDim
	}
	normDim := normalizeStorageDim(dim)
	x, _ := nbtGetInt(c, "x")
	y, _ := nbtGetInt(c, "y")
	z, _ := nbtGetInt(c, "z")

	var customName string
	var customNameTag nbt.Tag
	if rawName, ok := c.Get("CustomName"); ok && rawName != nil {
		customNameTag = world.CloneNBTTag(rawName)
		customName = world.ExtractCustomNameFromNBTTag(rawName)
	}
	var lockStr string
	var lockTag nbt.Tag
	if rawLock, ok := c.Get("lock"); ok && rawLock != nil {
		lockTag = world.CloneNBTTag(rawLock)
		lockStr = world.ExtractLockStringFromNBTTag(rawLock)
	} else if rawLockUpper, ok := c.Get("Lock"); ok && rawLockUpper != nil {
		lockTag = world.CloneNBTTag(rawLockUpper)
		lockStr = world.ExtractLockStringFromNBTTag(rawLockUpper)
	}

	lootTable, _ := nbtGetString(c, "LootTable")
	if lootTable != "" {
		lootTable = canonicalResourceName(lootTable)
	}
	var lootTableSeed int64
	if rawSeed, ok := c.Get("LootTableSeed"); ok {
		switch sv := rawSeed.(type) {
		case nbt.Long:
			lootTableSeed = int64(sv)
		case nbt.Int:
			lootTableSeed = int64(sv)
		}
	}

	var componentsTag *nbt.Compound
	if rawComp, ok := c.Get("components"); ok {
		if cc, isComp := rawComp.(*nbt.Compound); isComp && cc != nil && cc.Len() > 0 {
			componentsTag = world.CloneNBTCompound(cc)
		}
	}

	be := &StorageBlockEntity{
		ID:            id,
		Dimension:     normDim,
		X:             int(x),
		Y:             int(y),
		Z:             int(z),
		CustomName:    customName,
		CustomNameTag: customNameTag,
		Lock:          lockStr,
		LockTag:       lockTag,
		LootTable:     lootTable,
		LootTableSeed: lootTableSeed,
		ComponentsTag: componentsTag,
	}

	if lootTable == "" {
		_ = DefaultRecipeBook()
		if itemElems, ok := nbtGetListElems(c, "Items"); ok {
			for _, itElem := range itemElems {
				itComp, ok := itElem.(*nbt.Compound)
				if !ok || itComp == nil {
					continue
				}
				slotByte, ok := nbtGetByte(itComp, "Slot")
				if !ok {
					continue
				}
				slotIdx := int(uint8(slotByte))
				if slotIdx < 0 || slotIdx >= ChestSlotCount {
					continue
				}
				itemID, _ := nbtGetString(itComp, "id")
				count, _ := nbtGetInt(itComp, "count")
				if count <= 0 {
					if bCount, ok := nbtGetByte(itComp, "Count"); ok {
						count = int32(bCount)
					}
				}
				if itemID == "" || count <= 0 {
					continue
				}
				be.Slots[slotIdx] = world.NewNamedItemStack(itemID, count)
				comp, meta, potion, shulker := decodeHopperItemComponents(itComp)
				be.SlotComponents[slotIdx] = comp
				be.SlotMeta[slotIdx] = meta
				be.SlotPotions[slotIdx] = potion
				be.SlotShulker[slotIdx] = shulker
			}
		}
	}
	return be, true
}

func (s *Server) ensureStorageLoadedLocked(ss *serverStorageState) {
	if ss.loadedStore {
		return
	}
	ss.loadedStore = true
	if s == nil {
		return
	}
	if store := s.activeFurnaceStore(); store != nil {
		if comps, err := store.LoadAllBlockEntities(IsStorageBlockEntityID); err == nil {
			for _, comp := range comps {
				be, ok := DecodeStorageBlockEntityNBT(comp, world.DimensionOverworld)
				if !ok || be == nil {
					continue
				}
				key := storageBlockKey{dim: be.Dimension, x: be.X, y: be.Y, z: be.Z}
				if _, exists := ss.blocks[key]; !exists {
					ss.blocks[key] = be
				}
				ss.loadedChunk[storageChunkKey{dim: be.Dimension, cx: int32(be.X >> 4), cz: int32(be.Z >> 4)}] = true
			}
		}
	}
}

func (s *Server) ensureChunkStorageLoadedLocked(ss *serverStorageState, dim string, cx, cz int32) {
	s.ensureStorageLoadedLocked(ss)
	normDim := normalizeStorageDim(dim)
	ck := storageChunkKey{dim: normDim, cx: cx, cz: cz}
	if ss.loadedChunk[ck] {
		return
	}
	ss.loadedChunk[ck] = true
	if s == nil {
		return
	}
	cache := s.DimensionChunks(normDim)
	if cache == nil {
		return
	}
	if comps, err := cache.LoadChunkBlockEntities(cx, cz, IsStorageBlockEntityID); err == nil {
		for _, comp := range comps {
			be, ok := DecodeStorageBlockEntityNBT(comp, normDim)
			if !ok || be == nil {
				continue
			}
			key := storageBlockKey{dim: normDim, x: be.X, y: be.Y, z: be.Z}
			if _, exists := ss.blocks[key]; !exists {
				ss.blocks[key] = be
			}
		}
	}
	// Also hydrate any worldgen StructureBlockEntity in (cx, cz) without unpacking
	// its LootTable yet so LootTable/LootTableSeed survive until first open.
	for _, sbe := range cache.LoadedChunkStructureBlockEntities(cx, cz) {
		id, ok := StorageBlockEntityIDForBlockName(sbe.ID)
		if !ok && IsStorageBlockEntityID(sbe.ID) {
			id = canonicalResourceName(sbe.ID)
			ok = true
		}
		if !ok {
			continue
		}
		key := storageBlockKey{dim: normDim, x: sbe.X, y: sbe.Y, z: sbe.Z}
		if _, exists := ss.blocks[key]; exists {
			continue
		}
		be := &StorageBlockEntity{
			ID:            id,
			Dimension:     normDim,
			X:             sbe.X,
			Y:             sbe.Y,
			Z:             sbe.Z,
			LootTable:     canonicalResourceName(sbe.LootTable),
			LootTableSeed: sbe.LootTableSeed,
		}
		if sbe.LootTable == "" && len(sbe.Items) > 0 {
			_ = DefaultRecipeBook()
			for _, it := range sbe.Items {
				if it.Slot < 0 || it.Slot >= ChestSlotCount || it.ItemID == "" || it.Count <= 0 {
					continue
				}
				be.Slots[it.Slot] = world.NewNamedItemStack(it.ItemID, int32(it.Count))
				if it.PotionID != "" {
					be.SlotPotions[it.Slot] = strings.TrimPrefix(canonicalResourceName(it.PotionID), "minecraft:")
				}
			}
		}
		ss.blocks[key] = be
	}
}

func (s *Server) unpackStorageLootTableLocked(be *StorageBlockEntity) bool {
	if be == nil || be.LootTable == "" {
		return false
	}
	lootTable := be.LootTable
	seed := be.LootTableSeed
	be.LootTable = ""
	be.LootTableSeed = 0
	if seed == 0 {
		seed = (int64(be.X)&0x3FFFFFF)<<38 | (int64(be.Y) & 0xFFF) | ((int64(be.Z) & 0x3FFFFFF) << 12)
		if s != nil && s.cfg.WorldSeed != 0 {
			seed ^= s.cfg.WorldSeed
		}
	}
	_ = DefaultRecipeBook()
	if reg, err := worldgen.LoadLootAndSpawnerRegistry(); err == nil && reg != nil {
		items := reg.FillContainer(lootTable, seed, ChestSlotCount)
		for _, it := range items {
			if it.Slot < 0 || it.Slot >= ChestSlotCount || it.ItemID == "" || it.Count <= 0 {
				continue
			}
			be.Slots[it.Slot] = world.NewNamedItemStack(it.ItemID, int32(it.Count))
			if it.PotionID != "" {
				be.SlotPotions[it.Slot] = strings.TrimPrefix(canonicalResourceName(it.PotionID), "minecraft:")
			}
		}
	}
	return true
}

func (s *Server) getOrCreateStorageLocked(ss *serverStorageState, dim string, x, y, z int, fallbackID string) *StorageBlockEntity {
	normDim := normalizeStorageDim(dim)
	s.ensureChunkStorageLoadedLocked(ss, normDim, int32(x>>4), int32(z>>4))
	key := storageBlockKey{dim: normDim, x: x, y: y, z: z}
	be := ss.blocks[key]
	resolvedID := s.resolveStorageIDAt(normDim, x, y, z, fallbackID)
	if be == nil {
		be = &StorageBlockEntity{
			ID:        resolvedID,
			Dimension: normDim,
			X:         x,
			Y:         y,
			Z:         z,
		}
		ss.blocks[key] = be
	} else if IsStorageBlockEntityID(resolvedID) {
		be.ID = resolvedID
	}
	return be
}

// StorageBlockEntityAt returns a deep-copied snapshot of the StorageBlockEntity
// at (dim, x, y, z) WITHOUT unpacking its `LootTable` (if still packed).
func (s *Server) StorageBlockEntityAt(dim string, x, y, z int) (*StorageBlockEntity, bool) {
	if s == nil {
		return nil, false
	}
	normDim := normalizeStorageDim(dim)
	ss := s.storageState()
	ss.mu.Lock()
	s.ensureChunkStorageLoadedLocked(ss, normDim, int32(x>>4), int32(z>>4))
	be := ss.blocks[storageBlockKey{dim: normDim, x: x, y: y, z: z}]
	if be == nil {
		if cache := s.DimensionChunks(normDim); cache != nil {
			if id, ok := StorageBlockEntityIDForBlockName(world.BlockName(cache.GetBlock(x, y, z))); ok {
				be = &StorageBlockEntity{
					ID:        id,
					Dimension: normDim,
					X:         x,
					Y:         y,
					Z:         z,
				}
				ss.blocks[storageBlockKey{dim: normDim, x: x, y: y, z: z}] = be
			}
		}
	}
	var cp *StorageBlockEntity
	if be != nil {
		be.ID = s.resolveStorageIDAt(normDim, x, y, z, be.ID)
		cp = be.Clone()
	}
	ss.mu.Unlock()
	return cp, cp != nil
}

// SetStorageBlockEntity stores or replaces a StorageBlockEntity at
// (be.Dimension, be.X, be.Y, be.Z) and persists it to the chunk's `block_entities`.
func (s *Server) SetStorageBlockEntity(be *StorageBlockEntity) {
	if s == nil || be == nil {
		return
	}
	normDim := normalizeStorageDim(be.Dimension)
	s.BindNoteBlockAndJukeboxRedstone(normDim)
	ss := s.storageState()
	ss.mu.Lock()
	s.ensureChunkStorageLoadedLocked(ss, normDim, int32(be.X>>4), int32(be.Z>>4))
	cp := be.Clone()
	cp.Dimension = normDim
	cp.ID = s.resolveStorageIDAt(normDim, cp.X, cp.Y, cp.Z, cp.ID)
	ss.blocks[storageBlockKey{dim: normDim, x: cp.X, y: cp.Y, z: cp.Z}] = cp
	ss.mu.Unlock()
	_ = s.SaveChunkStorageContainers(normDim, int32(cp.X>>4), int32(cp.Z>>4))
	s.notifyStorageComparators(normDim, cp.X, cp.Y, cp.Z)
}

// SetStorageCustomName sets the CustomName on a storage block entity at (dim, x, y, z).
func (s *Server) SetStorageCustomName(dim string, x, y, z int, customName string) {
	if s == nil || customName == "" {
		return
	}
	normDim := normalizeStorageDim(dim)
	ss := s.storageState()
	ss.mu.Lock()
	be := s.getOrCreateStorageLocked(ss, normDim, x, y, z, "")
	be.CustomName = customName
	be.CustomNameTag = nil
	ss.mu.Unlock()
	_ = s.SaveChunkStorageContainers(normDim, int32(x>>4), int32(z>>4))
}

func (s *Server) notifyStorageComparators(dim string, x, y, z int) {
	if s == nil {
		return
	}
	normDim := normalizeStorageDim(dim)
	s.updateDispenserComparatorsAndBroadcast(normDim, x, y, z)
	if cache := s.DimensionChunks(normDim); cache != nil {
		if px, py, pz, _, ok := cache.DoubleChestPartnerAt(x, y, z); ok {
			s.updateDispenserComparatorsAndBroadcast(normDim, px, py, pz)
		}
	}
}

// ChestSlotsInDim returns a copy of the 27 slots in the chest, barrel, or
// shulker_box at (dim, x, y, z), unpacking `LootTable` on first access just like
// vanilla `RandomizableContainerBlockEntity.getItem` / `unpackLootTable`.
func (s *Server) ChestSlotsInDim(dim string, x, y, z int) [ChestSlotCount]world.ItemStack {
	if s == nil {
		return [ChestSlotCount]world.ItemStack{}
	}
	normDim := normalizeStorageDim(dim)
	ss := s.storageState()
	ss.mu.Lock()
	s.ensureChunkStorageLoadedLocked(ss, normDim, int32(x>>4), int32(z>>4))
	be := ss.blocks[storageBlockKey{dim: normDim, x: x, y: y, z: z}]
	if be == nil {
		ss.mu.Unlock()
		return [ChestSlotCount]world.ItemStack{}
	}
	unpacked := s.unpackStorageLootTableLocked(be)
	slots := be.Slots
	ss.mu.Unlock()
	if unpacked {
		_ = s.SaveChunkStorageContainers(normDim, int32(x>>4), int32(z>>4))
	}
	return slots
}

// SetChestSlotInDim updates a single slot (0..26) in the storage container at (dim, x, y, z).
func (s *Server) SetChestSlotInDim(dim string, x, y, z, slot int, stack world.ItemStack) bool {
	if s == nil || slot < 0 || slot >= ChestSlotCount {
		return false
	}
	normDim := normalizeStorageDim(dim)
	ss := s.storageState()
	ss.mu.Lock()
	be := s.getOrCreateStorageLocked(ss, normDim, x, y, z, "")
	_ = s.unpackStorageLootTableLocked(be)
	prevID := be.Slots[slot].ResolvedItemID()
	if stack.IsEmpty() || stack.Count <= 0 {
		be.Slots[slot] = world.ItemStack{}
		be.SlotMeta[slot] = ItemMeta{}
		be.SlotPotions[slot] = ""
		be.SlotShulker[slot] = nil
		be.SlotComponents[slot] = nil
	} else {
		if stack.ItemID <= 0 && stack.ID != "" {
			stack = world.NewNamedItemStack(stack.ID, stack.Count)
		} else {
			stack = world.NewItemStack(stack.ResolvedItemID(), stack.Count)
		}
		be.Slots[slot] = stack
		if prevID != stack.ResolvedItemID() {
			be.SlotMeta[slot] = ItemMeta{}
			be.SlotPotions[slot] = ""
			be.SlotShulker[slot] = nil
			be.SlotComponents[slot] = nil
		}
	}
	ss.mu.Unlock()
	s.notifyStorageComparators(normDim, x, y, z)
	return true
}

// SetChestSlotWithMeta updates a single slot (0..26) in the storage container at
// (dim, x, y, z) along with its ItemMeta, potionType, and raw 26.1.2 components.
func (s *Server) SetChestSlotWithMeta(dim string, x, y, z, slot int, stack world.ItemStack, meta ItemMeta, potionType string, rawComp *nbt.Compound) bool {
	if s == nil || slot < 0 || slot >= ChestSlotCount {
		return false
	}
	normDim := normalizeStorageDim(dim)
	ss := s.storageState()
	ss.mu.Lock()
	be := s.getOrCreateStorageLocked(ss, normDim, x, y, z, "")
	_ = s.unpackStorageLootTableLocked(be)
	if stack.IsEmpty() || stack.Count <= 0 {
		be.Slots[slot] = world.ItemStack{}
		be.SlotMeta[slot] = ItemMeta{}
		be.SlotPotions[slot] = ""
		be.SlotShulker[slot] = nil
		be.SlotComponents[slot] = nil
	} else {
		if stack.ItemID <= 0 && stack.ID != "" {
			stack = world.NewNamedItemStack(stack.ID, stack.Count)
		} else {
			stack = world.NewItemStack(stack.ResolvedItemID(), stack.Count)
		}
		be.Slots[slot] = stack
		be.SlotMeta[slot] = meta.Clone()
		be.SlotPotions[slot] = potionType
		be.SlotComponents[slot] = world.CloneNBTCompound(rawComp)
	}
	ss.mu.Unlock()
	s.notifyStorageComparators(normDim, x, y, z)
	return true
}

// GetChestSlotMeta returns the ItemMeta, potionType, and raw 26.1.2 components
// stored in slot (0..26) of the storage container at (dim, x, y, z).
func (s *Server) GetChestSlotMeta(dim string, x, y, z, slot int) (ItemMeta, string, *nbt.Compound) {
	if s == nil || slot < 0 || slot >= ChestSlotCount {
		return ItemMeta{}, "", nil
	}
	normDim := normalizeStorageDim(dim)
	ss := s.storageState()
	ss.mu.Lock()
	s.ensureChunkStorageLoadedLocked(ss, normDim, int32(x>>4), int32(z>>4))
	be := ss.blocks[storageBlockKey{dim: normDim, x: x, y: y, z: z}]
	if be == nil {
		ss.mu.Unlock()
		return ItemMeta{}, "", nil
	}
	meta := be.SlotMeta[slot].Clone()
	pot := be.SlotPotions[slot]
	comp := world.CloneNBTCompound(be.SlotComponents[slot])
	ss.mu.Unlock()
	return meta, pot, comp
}

func (s *Server) getStorageSlotMetaAndComponents(dim string, x, y, z, slot int) (world.ItemStack, ItemMeta, ArmorTrim, string, *[ChestSlotCount]world.ItemStack, *nbt.Compound) {
	if s == nil || slot < 0 || slot >= ChestSlotCount {
		return world.ItemStack{}, ItemMeta{}, ArmorTrim{}, "", nil, nil
	}
	normDim := normalizeStorageDim(dim)
	ss := s.storageState()
	ss.mu.Lock()
	s.ensureChunkStorageLoadedLocked(ss, normDim, int32(x>>4), int32(z>>4))
	be := ss.blocks[storageBlockKey{dim: normDim, x: x, y: y, z: z}]
	if be == nil {
		ss.mu.Unlock()
		return world.ItemStack{}, ItemMeta{}, ArmorTrim{}, "", nil, nil
	}
	st := be.Slots[slot]
	if st.IsEmpty() {
		ss.mu.Unlock()
		return world.ItemStack{}, ItemMeta{}, ArmorTrim{}, "", nil, nil
	}
	meta := be.SlotMeta[slot].Clone()
	pot := be.SlotPotions[slot]
	var sh *[ChestSlotCount]world.ItemStack
	if be.SlotShulker[slot] != nil {
		arr := *be.SlotShulker[slot]
		sh = &arr
	}
	comp := world.CloneNBTCompound(be.SlotComponents[slot])
	ss.mu.Unlock()
	var trim ArmorTrim
	if comp != nil {
		if tag, ok := comp.Get("minecraft:trim"); ok {
			if tc, ok2 := tag.(*nbt.Compound); ok2 && tc != nil {
				if tr, ok3 := ArmorTrimFromNBT(tc); ok3 {
					trim = tr
				}
			}
		}
	}
	return st, meta, trim, pot, sh, comp
}

func (s *Server) setStorageSlotMetaAndComponents(dim string, x, y, z, slot int, stack world.ItemStack, meta ItemMeta, trim ArmorTrim, potionType string, shulker *[ChestSlotCount]world.ItemStack, rawComp *nbt.Compound) bool {
	if s == nil || slot < 0 || slot >= ChestSlotCount {
		return false
	}
	normDim := normalizeStorageDim(dim)
	ss := s.storageState()
	ss.mu.Lock()
	s.ensureChunkStorageLoadedLocked(ss, normDim, int32(x>>4), int32(z>>4))
	key := storageBlockKey{dim: normDim, x: x, y: y, z: z}
	be := ss.blocks[key]
	if be == nil && (stack.IsEmpty() || stack.Count <= 0) {
		ss.mu.Unlock()
		return true
	}
	if be == nil {
		be = s.getOrCreateStorageLocked(ss, normDim, x, y, z, "")
	}
	_ = s.unpackStorageLootTableLocked(be)
	if stack.IsEmpty() || stack.Count <= 0 {
		be.Slots[slot] = world.ItemStack{}
		be.SlotMeta[slot] = ItemMeta{}
		be.SlotPotions[slot] = ""
		be.SlotShulker[slot] = nil
		be.SlotComponents[slot] = nil
	} else {
		if stack.ItemID <= 0 && stack.ID != "" {
			stack = world.NewNamedItemStack(stack.ID, stack.Count)
		} else {
			stack = world.NewItemStack(stack.ResolvedItemID(), stack.Count)
		}
		be.Slots[slot] = stack
		be.SlotMeta[slot] = meta.Clone()
		be.SlotPotions[slot] = potionType
		if shulker != nil {
			arr := *shulker
			be.SlotShulker[slot] = &arr
		} else {
			be.SlotShulker[slot] = nil
		}
		var nextComp *nbt.Compound
		if rawComp != nil {
			cloned := world.CloneNBTCompound(rawComp)
			for _, k := range cloned.Keys() {
				if !trim.IsValid() && k == "minecraft:trim" {
					continue
				}
				if v, ok := cloned.Get(k); ok {
					if nextComp == nil {
						nextComp = nbt.NewCompound()
					}
					nextComp.Set(k, v)
				}
			}
		}
		if trim.IsValid() {
			if tc := ArmorTrimToNBT(trim); tc != nil {
				if nextComp == nil {
					nextComp = nbt.NewCompound()
				}
				nextComp.Set("minecraft:trim", tc)
			}
		}
		be.SlotComponents[slot] = nextComp
	}
	ss.mu.Unlock()
	s.notifyStorageComparators(normDim, x, y, z)
	return true
}

// DropChestContentsInDim removes the chest or barrel block entity at (dim, x, y, z),
// closes any open screens viewing it, drops all non-empty items (unpacking `LootTable`
// first if needed, matching vanilla `Containers.dropContents`), and updates comparators.
func (s *Server) DropChestContentsInDim(dim string, x, y, z int) {
	if s == nil {
		return
	}
	normDim := normalizeStorageDim(dim)
	ss := s.storageState()
	ss.mu.Lock()
	s.ensureChunkStorageLoadedLocked(ss, normDim, int32(x>>4), int32(z>>4))
	key := storageBlockKey{dim: normDim, x: x, y: y, z: z}
	be := ss.blocks[key]
	if be != nil {
		_ = s.unpackStorageLootTableLocked(be)
	}
	delete(ss.blocks, key)
	ss.mu.Unlock()

	if cache := s.DimensionChunks(normDim); cache != nil {
		cache.RemoveStructureBlockEntity(x, y, z)
	}
	s.closeStorageViewersAt(normDim, x, y, z)

	if be != nil {
		for i := 0; i < ChestSlotCount; i++ {
			st := be.Slots[i]
			if !st.IsEmpty() && st.Count > 0 {
				entID, _ := s.SpawnItemEntityInDimension(normDim, float64(x)+0.5, float64(y)+0.25, float64(z)+0.5, st)
				if entID > 0 {
					if !be.SlotMeta[i].IsEmpty() {
						ft := s.fishingTracker()
						ft.mu.Lock()
						ft.droppedMeta[entID] = be.SlotMeta[i].Clone()
						ft.mu.Unlock()
					}
					if be.SlotPotions[i] != "" && IsPotionOrTippedArrowItem(st) {
						s.AssociateDroppedPotionEntity(entID, be.SlotPotions[i])
					}
					if be.SlotShulker[i] != nil && IsShulkerBoxItemOrBlock(st.ResolvedID()) {
						s.AssociateDroppedShulkerBoxEntity(entID, &ShulkerBoxItemData{
							Slots: *be.SlotShulker[i],
						})
					}
				}
			}
		}
		_ = s.SaveChunkStorageContainers(normDim, int32(x>>4), int32(z>>4))
	}
	s.notifyStorageComparators(normDim, x, y, z)
}

func (s *Server) closeStorageViewersAt(dim string, x, y, z int) {
	normDim := normalizeStorageDim(dim)
	s.playersMu.RLock()
	sessions := make([]*PlayerSession, 0, len(s.players))
	for _, sess := range s.players {
		sessions = append(sessions, sess)
	}
	s.playersMu.RUnlock()
	for _, sess := range sessions {
		open := sess.OpenContainer()
		if open == nil || open.IsEnderChest || open.VehicleEntityID != 0 {
			continue
		}
		if open.MenuType != MenuTypeGeneric9x3 && open.MenuType != MenuTypeGeneric9x6 && open.MenuType != MenuTypeShulkerBox {
			continue
		}
		openDim := normalizeStorageDim(open.Dimension)
		if openDim != normDim {
			continue
		}
		if (open.BlockX == x && open.BlockY == y && open.BlockZ == z) ||
			(open.HasPartner && open.PartnerX == x && open.PartnerY == y && open.PartnerZ == z) {
			_ = s.CloseContainer(sess, open.ContainerID, true)
		}
	}
}

func isStorageBlockState(st uint16) bool {
	if st == world.StateAir {
		return false
	}
	if IsChestBlock(st) || IsBarrelBlock(st) {
		return true
	}
	return IsShulkerBoxItemOrBlock(world.BlockName(st))
}

func (s *Server) scanLoadedChunkStorageBlocksLocked(ss *serverStorageState, dim string, cx, cz int32) {
	cache := s.DimensionChunks(dim)
	if cache == nil || !cache.IsChunkLoaded(cx, cz) {
		return
	}
	for _, entry := range cache.LoadedChunkMatchingBlocks(cx, cz, isStorageBlockState) {
		wx, y, wz, st := entry[0], entry[1], entry[2], uint16(entry[3])
		id, ok := StorageBlockEntityIDForBlockName(world.BlockName(st))
		if !ok {
			continue
		}
		key := storageBlockKey{dim: dim, x: wx, y: y, z: wz}
		if existing, exists := ss.blocks[key]; !exists || existing == nil {
			ss.blocks[key] = &StorageBlockEntity{
				ID:        id,
				Dimension: dim,
				X:         wx,
				Y:         y,
				Z:         wz,
			}
		} else {
			existing.ID = id
		}
	}
}

func (s *Server) loadedChunkHasStorageBlock(cache *world.Cache, cx, cz int32) bool {
	if cache == nil || !cache.IsChunkLoaded(cx, cz) {
		return false
	}
	return len(cache.LoadedChunkMatchingBlocks(cx, cz, isStorageBlockState)) > 0
}

// ChunkStorageBlockEntitiesNBT returns vanilla 26.1.2 `block_entities` NBT
// compounds for all chest, trapped_chest, barrel, and shulker_box block entities
// in chunk (cx, cz) of dim without unpacking any `LootTable`.
func (s *Server) ChunkStorageBlockEntitiesNBT(dim string, cx, cz int32) []*nbt.Compound {
	if s == nil {
		return nil
	}
	normDim := normalizeStorageDim(dim)
	ss := s.storageState()
	ss.mu.Lock()
	defer ss.mu.Unlock()
	s.ensureChunkStorageLoadedLocked(ss, normDim, cx, cz)
	s.scanLoadedChunkStorageBlocksLocked(ss, normDim, cx, cz)

	cache := s.DimensionChunks(normDim)
	chunkLoaded := cache != nil && cache.IsChunkLoaded(cx, cz)

	var keys []storageBlockKey
	for k, be := range ss.blocks {
		if k.dim != normDim || be == nil {
			continue
		}
		if int32(k.x>>4) != cx || int32(k.z>>4) != cz {
			continue
		}
		if chunkLoaded {
			st := cache.GetBlock(k.x, k.y, k.z)
			if st != world.StateAir {
				if id, ok := StorageBlockEntityIDForBlockName(world.BlockName(st)); ok {
					be.ID = id
				} else {
					continue
				}
			}
		}
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].y != keys[j].y {
			return keys[i].y < keys[j].y
		}
		if keys[i].x != keys[j].x {
			return keys[i].x < keys[j].x
		}
		return keys[i].z < keys[j].z
	})
	out := make([]*nbt.Compound, 0, len(keys))
	for _, k := range keys {
		out = append(out, EncodeStorageBlockEntityNBT(ss.blocks[k]))
	}
	return out
}

// SaveChunkStorageContainers persists all chest, trapped_chest, barrel, and
// shulker_box block entities in chunk (cx, cz) of dim to the chunk's `block_entities`.
func (s *Server) SaveChunkStorageContainers(dim string, cx, cz int32) error {
	if s == nil {
		return nil
	}
	normDim := normalizeStorageDim(dim)
	cache := s.DimensionChunks(normDim)
	if cache == nil {
		return nil
	}
	comps := s.ChunkStorageBlockEntitiesNBT(normDim, cx, cz)
	return cache.SaveChunkBlockEntities(cx, cz, IsStorageBlockEntityID, comps)
}

// UnloadChunkStorageContainers saves all storage block entities in chunk (cx, cz)
// of dim to the chunk's `block_entities` list, removes them from runtime memory,
// and returns their serialized NBT compounds.
func (s *Server) UnloadChunkStorageContainers(dim string, cx, cz int32) []*nbt.Compound {
	if s == nil {
		return nil
	}
	normDim := normalizeStorageDim(dim)
	out := s.ChunkStorageBlockEntitiesNBT(normDim, cx, cz)
	if cache := s.DimensionChunks(normDim); cache != nil {
		_ = cache.SaveChunkBlockEntities(cx, cz, IsStorageBlockEntityID, out)
	}
	ss := s.storageState()
	ss.mu.Lock()
	for k := range ss.blocks {
		if k.dim == normDim && int32(k.x>>4) == cx && int32(k.z>>4) == cz {
			delete(ss.blocks, k)
		}
	}
	delete(ss.loadedChunk, storageChunkKey{dim: normDim, cx: cx, cz: cz})
	ss.mu.Unlock()
	return out
}

// ReloadChunkStorageContainers reloads storage block entities in chunk (cx, cz)
// of dim from the chunk's persisted `block_entities` NBT list.
func (s *Server) ReloadChunkStorageContainers(dim string, cx, cz int32) error {
	if s == nil {
		return nil
	}
	normDim := normalizeStorageDim(dim)
	ss := s.storageState()
	ss.mu.Lock()
	defer ss.mu.Unlock()
	for k := range ss.blocks {
		if k.dim == normDim && int32(k.x>>4) == cx && int32(k.z>>4) == cz {
			delete(ss.blocks, k)
		}
	}
	delete(ss.loadedChunk, storageChunkKey{dim: normDim, cx: cx, cz: cz})
	s.ensureChunkStorageLoadedLocked(ss, normDim, cx, cz)
	return nil
}

// LoadChunkStorageBlockEntitiesNBT loads chest, trapped_chest, barrel, and
// shulker_box block entities from a chunk's `block_entities` NBT list.
func (s *Server) LoadChunkStorageBlockEntitiesNBT(dim string, list []*nbt.Compound) int {
	if s == nil || len(list) == 0 {
		return 0
	}
	normDim := normalizeStorageDim(dim)
	ss := s.storageState()
	ss.mu.Lock()
	loaded := 0
	for _, c := range list {
		be, ok := DecodeStorageBlockEntityNBT(c, normDim)
		if !ok || be == nil {
			continue
		}
		ss.blocks[storageBlockKey{dim: be.Dimension, x: be.X, y: be.Y, z: be.Z}] = be
		ss.loadedChunk[storageChunkKey{dim: be.Dimension, cx: int32(be.X >> 4), cz: int32(be.Z >> 4)}] = true
		loaded++
	}
	ss.mu.Unlock()
	if loaded > 0 {
		s.BindNoteBlockAndJukeboxRedstone(normDim)
	}
	return loaded
}

// SaveStorageContainers flushes all tracked storage block entities across all
// dimensions into their chunks' Anvil `block_entities` lists.
func (s *Server) SaveStorageContainers() error {
	if s == nil {
		return nil
	}
	ss := s.storageState()
	ss.mu.Lock()
	s.ensureStorageLoadedLocked(ss)
	chunks := make(map[storageChunkKey]struct{})
	for ck := range ss.loadedChunk {
		chunks[ck] = struct{}{}
	}
	for k := range ss.blocks {
		chunks[storageChunkKey{dim: k.dim, cx: int32(k.x >> 4), cz: int32(k.z >> 4)}] = struct{}{}
	}
	ss.mu.Unlock()
	for _, dim := range []string{world.DimensionOverworld, world.DimensionNether, world.DimensionEnd} {
		if cache := s.loadedDimensionCache(dim); cache != nil {
			for _, coord := range cache.LoadedChunkCoords() {
				ck := storageChunkKey{dim: dim, cx: coord[0], cz: coord[1]}
				if _, ok := chunks[ck]; ok {
					continue
				}
				if s.loadedChunkHasStorageBlock(cache, coord[0], coord[1]) {
					chunks[ck] = struct{}{}
				}
			}
		}
	}
	var firstErr error
	for ck := range chunks {
		if err := s.SaveChunkStorageContainers(ck.dim, ck.cx, ck.cz); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// LoadStorageContainers loads persisted storage block entities from the world store.
func (s *Server) LoadStorageContainers() error {
	if s == nil {
		return nil
	}
	ss := s.storageState()
	ss.mu.Lock()
	ss.loadedStore = false
	s.ensureStorageLoadedLocked(ss)
	ss.mu.Unlock()
	return nil
}

// --- Shulker Box 26.1.2 Item Components & Lifecycle ---

// EncodeShulkerBoxItemComponentsNBT encodes the vanilla 26.1.2 `components`
// compound for a `shulker_box` item:
//   - `minecraft:container` (`[{slot: Int, item: {id, count, optional components}}, ...]`)
//   - `minecraft:container_loot` (`{loot_table: String, optional seed: Long}`)
//   - `minecraft:custom_name`
//   - `minecraft:lock`
func EncodeShulkerBoxItemComponentsNBT(data *ShulkerBoxItemData) *nbt.Compound {
	if data == nil {
		return nil
	}
	comp := nbt.NewCompound()
	if data.CustomNameTag != nil {
		comp.Set("minecraft:custom_name", world.CloneNBTTag(data.CustomNameTag))
	} else if data.CustomName != "" {
		comp.Set("minecraft:custom_name", nbt.String(data.CustomName))
	}
	if data.LockTag != nil {
		comp.Set("minecraft:lock", world.CloneNBTTag(data.LockTag))
	} else if data.Lock != "" {
		comp.Set("minecraft:lock", nbt.String(data.Lock))
	}
	if data.LootTable != "" {
		cl := nbt.NewCompound()
		cl.Set("loot_table", nbt.String(canonicalResourceName(data.LootTable)))
		if data.LootTableSeed != 0 {
			cl.Set("seed", nbt.Long(data.LootTableSeed))
		}
		comp.Set("minecraft:container_loot", cl)
	} else {
		var containerSlots []nbt.Tag
		for slot := 0; slot < ChestSlotCount; slot++ {
			st := data.Slots[slot]
			if st.IsEmpty() || st.Count <= 0 {
				continue
			}
			entry := nbt.NewCompound()
			entry.Set("slot", nbt.Int(int32(slot)))
			itemComp := nbt.NewCompound()
			itemComp.Set("id", nbt.String(st.ResolvedID()))
			itemComp.Set("count", nbt.Int(st.Count))
			if subComp := encodeHopperItemComponents(data.SlotComponents[slot], data.SlotMeta[slot], data.SlotPotions[slot], data.SlotShulker[slot]); subComp != nil {
				itemComp.Set("components", subComp)
			}
			entry.Set("item", itemComp)
			containerSlots = append(containerSlots, entry)
		}
		if len(containerSlots) > 0 {
			comp.Set("minecraft:container", nbt.List{ElemID: nbt.TagCompound, Elems: containerSlots})
		}
	}
	if comp.Len() == 0 {
		return nil
	}
	return comp
}

// EncodeShulkerBoxItemNBT encodes a complete vanilla 26.1.2 `ItemStack` NBT
// compound (`id`, `count`, optional `components`) for a shulker box item.
func EncodeShulkerBoxItemNBT(itemID string, count int32, data *ShulkerBoxItemData) *nbt.Compound {
	if count <= 0 {
		count = 1
	}
	normID := canonicalResourceName(itemID)
	if !IsShulkerBoxItemOrBlock(normID) {
		normID = "minecraft:shulker_box"
	}
	c := nbt.NewCompound()
	c.Set("id", nbt.String(normID))
	c.Set("count", nbt.Int(count))
	if comp := EncodeShulkerBoxItemComponentsNBT(data); comp != nil && comp.Len() > 0 {
		c.Set("components", comp)
	}
	return c
}

// DecodeShulkerBoxItemComponentsNBT decodes a `ShulkerBoxItemData` from a
// vanilla 26.1.2 item `components` compound.
func DecodeShulkerBoxItemComponentsNBT(comp *nbt.Compound) (*ShulkerBoxItemData, bool) {
	if comp == nil {
		return nil, false
	}
	_ = DefaultRecipeBook()
	data := &ShulkerBoxItemData{}
	found := false

	if rawName, ok := comp.Get("minecraft:custom_name"); ok && rawName != nil {
		data.CustomNameTag = world.CloneNBTTag(rawName)
		data.CustomName = world.ExtractCustomNameFromNBTTag(rawName)
		found = true
	}
	if rawLock, ok := comp.Get("minecraft:lock"); ok && rawLock != nil {
		data.LockTag = world.CloneNBTTag(rawLock)
		data.Lock = world.ExtractLockStringFromNBTTag(rawLock)
		found = true
	}
	if rawLoot, ok := comp.Get("minecraft:container_loot"); ok {
		if lc, isComp := rawLoot.(*nbt.Compound); isComp && lc != nil {
			if lt, ok := nbtGetString(lc, "loot_table"); ok && lt != "" {
				data.LootTable = canonicalResourceName(lt)
				found = true
			}
			if rawSeed, ok := lc.Get("seed"); ok {
				switch sv := rawSeed.(type) {
				case nbt.Long:
					data.LootTableSeed = int64(sv)
				case nbt.Int:
					data.LootTableSeed = int64(sv)
				}
			}
		}
	}
	if contElems, ok := nbtGetListElems(comp, "minecraft:container"); ok {
		for _, elem := range contElems {
			sc, ok := elem.(*nbt.Compound)
			if !ok || sc == nil {
				continue
			}
			slotIdx32, ok := nbtGetInt(sc, "slot")
			if !ok {
				if sb, okB := nbtGetByte(sc, "slot"); okB {
					slotIdx32 = int32(uint8(sb))
					ok = true
				}
			}
			if !ok || slotIdx32 < 0 || int(slotIdx32) >= ChestSlotCount {
				continue
			}
			itemTag, ok := sc.Get("item")
			if !ok {
				continue
			}
			itemComp, ok := itemTag.(*nbt.Compound)
			if !ok || itemComp == nil {
				continue
			}
			itemID, _ := nbtGetString(itemComp, "id")
			cnt, _ := nbtGetInt(itemComp, "count")
			if cnt <= 0 {
				if bc, okB := nbtGetByte(itemComp, "Count"); okB {
					cnt = int32(bc)
				}
			}
			if itemID == "" || cnt <= 0 {
				continue
			}
			slotIdx := int(slotIdx32)
			data.Slots[slotIdx] = world.NewNamedItemStack(itemID, cnt)
			subComp, subMeta, subPot, subSh := decodeHopperItemComponents(itemComp)
			data.SlotComponents[slotIdx] = subComp
			data.SlotMeta[slotIdx] = subMeta
			data.SlotPotions[slotIdx] = subPot
			data.SlotShulker[slotIdx] = subSh
			found = true
		}
	}
	return data, found
}

// DecodeShulkerBoxItemNBT decodes a complete vanilla 26.1.2 shulker box `ItemStack`
// NBT compound (`id`, `count`, optional `components`).
func DecodeShulkerBoxItemNBT(itemTag *nbt.Compound) (world.ItemStack, *ShulkerBoxItemData, bool) {
	if itemTag == nil {
		return world.ItemStack{}, nil, false
	}
	_ = DefaultRecipeBook()
	itemID, _ := nbtGetString(itemTag, "id")
	if !IsShulkerBoxItemOrBlock(itemID) {
		return world.ItemStack{}, nil, false
	}
	cnt, _ := nbtGetInt(itemTag, "count")
	if cnt <= 0 {
		cnt = 1
	}
	stack := world.NewNamedItemStack(itemID, cnt)
	var data *ShulkerBoxItemData
	if rawComp, ok := itemTag.Get("components"); ok {
		if cc, isComp := rawComp.(*nbt.Compound); isComp && cc != nil {
			if parsed, has := DecodeShulkerBoxItemComponentsNBT(cc); has {
				data = parsed
			}
		}
	}
	return stack, data, true
}

// ShulkerBoxDataFromBlockEntity extracts the `ShulkerBoxItemData` carried when
// breaking `be` (matching `ShulkerBoxBlock.playerWillDestroy` + `copy_components`).
func ShulkerBoxDataFromBlockEntity(be *StorageBlockEntity) *ShulkerBoxItemData {
	if be == nil {
		return nil
	}
	cp := be.Clone()
	data := &ShulkerBoxItemData{
		Slots:          cp.Slots,
		SlotMeta:       cp.SlotMeta,
		SlotPotions:    cp.SlotPotions,
		SlotShulker:    cp.SlotShulker,
		SlotComponents: cp.SlotComponents,
		LootTable:      cp.LootTable,
		LootTableSeed:  cp.LootTableSeed,
		CustomName:     cp.CustomName,
		CustomNameTag:  cp.CustomNameTag,
		Lock:           cp.Lock,
		LockTag:        cp.LockTag,
	}
	if data.IsEmpty() {
		return nil
	}
	return data
}

type serverDroppedShulkerStore struct {
	mu      sync.Mutex
	dropped map[int32]*ShulkerBoxItemData
}

type sessionShulkerSlotTracker struct {
	mu      sync.Mutex
	slots   [world.PlayerInventorySlots]*ShulkerBoxItemData
	carried *ShulkerBoxItemData
}

var (
	serverDroppedShulkers sync.Map // map[*Server]*serverDroppedShulkerStore
	playerShulkerSlots    sync.Map // map[*PlayerSession]*sessionShulkerSlotTracker
)

func (s *Server) droppedShulkerStore() *serverDroppedShulkerStore {
	if s == nil {
		return nil
	}
	if v, ok := serverDroppedShulkers.Load(s); ok {
		return v.(*serverDroppedShulkerStore)
	}
	actual, _ := serverDroppedShulkers.LoadOrStore(s, &serverDroppedShulkerStore{
		dropped: make(map[int32]*ShulkerBoxItemData),
	})
	return actual.(*serverDroppedShulkerStore)
}

func getShulkerSlotTracker(session *PlayerSession) *sessionShulkerSlotTracker {
	if session == nil {
		return nil
	}
	if v, ok := playerShulkerSlots.Load(session); ok {
		return v.(*sessionShulkerSlotTracker)
	}
	actual, _ := playerShulkerSlots.LoadOrStore(session, &sessionShulkerSlotTracker{})
	return actual.(*sessionShulkerSlotTracker)
}

// AssociateDroppedShulkerBoxEntity attaches vanilla 26.1.2 `ShulkerBoxItemData`
// to a spawned `minecraft:item` entity ID.
func (s *Server) AssociateDroppedShulkerBoxEntity(entityID int32, data *ShulkerBoxItemData) {
	if s == nil || entityID <= 0 || data == nil || data.IsEmpty() {
		return
	}
	st := s.droppedShulkerStore()
	st.mu.Lock()
	st.dropped[entityID] = data.Clone()
	st.mu.Unlock()
}

// DroppedShulkerBoxItemData returns a clone of the `ShulkerBoxItemData` attached
// to a dropped `minecraft:item` entity, if any.
func (s *Server) DroppedShulkerBoxItemData(entityID int32) (*ShulkerBoxItemData, bool) {
	if s == nil || entityID <= 0 {
		return nil, false
	}
	st := s.droppedShulkerStore()
	st.mu.Lock()
	defer st.mu.Unlock()
	d, ok := st.dropped[entityID]
	if !ok || d == nil || d.IsEmpty() {
		return nil, false
	}
	return d.Clone(), true
}

// TakeDroppedShulkerBoxItemData removes and returns the `ShulkerBoxItemData`
// attached to a dropped `minecraft:item` entity.
func (s *Server) TakeDroppedShulkerBoxItemData(entityID int32) (*ShulkerBoxItemData, bool) {
	if s == nil || entityID <= 0 {
		return nil, false
	}
	st := s.droppedShulkerStore()
	st.mu.Lock()
	defer st.mu.Unlock()
	d, ok := st.dropped[entityID]
	delete(st.dropped, entityID)
	if !ok || d == nil || d.IsEmpty() {
		return nil, false
	}
	return d, true
}

// DroppedShulkerBoxItemNBT returns the full vanilla 26.1.2 `ItemStack` NBT
// compound (`id`, `count`, `components`) for a dropped shulker box item entity.
func (s *Server) DroppedShulkerBoxItemNBT(entityID int32) (*nbt.Compound, bool) {
	if s == nil || s.entities == nil || entityID <= 0 {
		return nil, false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || !ent.IsItem() || ent.Item.IsEmpty() || !IsShulkerBoxItemOrBlock(ent.Item.ResolvedID()) {
		return nil, false
	}
	data, _ := s.DroppedShulkerBoxItemData(entityID)
	return EncodeShulkerBoxItemNBT(ent.Item.ResolvedID(), ent.Item.Count, data), true
}

// SetSlotShulkerBoxData sets or clears the `ShulkerBoxItemData` on player inventory slot (0..45).
func SetSlotShulkerBoxData(session *PlayerSession, slot int, data *ShulkerBoxItemData) {
	if session == nil || slot < 0 || slot >= world.PlayerInventorySlots {
		return
	}
	tr := getShulkerSlotTracker(session)
	tr.mu.Lock()
	if data == nil || data.IsEmpty() {
		tr.slots[slot] = nil
	} else {
		tr.slots[slot] = data.Clone()
	}
	tr.mu.Unlock()
}

// GetSlotShulkerBoxData returns a clone of the `ShulkerBoxItemData` on player inventory slot (0..45).
func GetSlotShulkerBoxData(session *PlayerSession, slot int) (*ShulkerBoxItemData, bool) {
	if session == nil || slot < 0 || slot >= world.PlayerInventorySlots {
		return nil, false
	}
	tr := getShulkerSlotTracker(session)
	tr.mu.Lock()
	defer tr.mu.Unlock()
	d := tr.slots[slot]
	if d == nil || d.IsEmpty() {
		return nil, false
	}
	return d.Clone(), true
}

// ClearSlotShulkerBoxData clears any `ShulkerBoxItemData` on player inventory slot (0..45).
func ClearSlotShulkerBoxData(session *PlayerSession, slot int) {
	SetSlotShulkerBoxData(session, slot, nil)
}

// SetCarriedShulkerBoxData sets or clears the `ShulkerBoxItemData` on the player's cursor.
func SetCarriedShulkerBoxData(session *PlayerSession, data *ShulkerBoxItemData) {
	if session == nil {
		return
	}
	tr := getShulkerSlotTracker(session)
	tr.mu.Lock()
	if data == nil || data.IsEmpty() {
		tr.carried = nil
	} else {
		tr.carried = data.Clone()
	}
	tr.mu.Unlock()
}

// GetCarriedShulkerBoxData returns a clone of the `ShulkerBoxItemData` on the player's cursor.
func GetCarriedShulkerBoxData(session *PlayerSession) (*ShulkerBoxItemData, bool) {
	if session == nil {
		return nil, false
	}
	tr := getShulkerSlotTracker(session)
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if tr.carried == nil || tr.carried.IsEmpty() {
		return nil, false
	}
	return tr.carried.Clone(), true
}

// TakeShulkerBoxDropData removes the shulker box block entity at (dim, x, y, z)
// WITHOUT unpacking its `LootTable`, closes any open screens viewing it, updates
// comparators, and returns its `ShulkerBoxItemData` for the dropped item entity.
func (s *Server) TakeShulkerBoxDropData(dim string, x, y, z int) *ShulkerBoxItemData {
	if s == nil {
		return nil
	}
	normDim := normalizeStorageDim(dim)
	ss := s.storageState()
	ss.mu.Lock()
	s.ensureChunkStorageLoadedLocked(ss, normDim, int32(x>>4), int32(z>>4))
	key := storageBlockKey{dim: normDim, x: x, y: y, z: z}
	be := ss.blocks[key]
	delete(ss.blocks, key)
	ss.mu.Unlock()

	if cache := s.DimensionChunks(normDim); cache != nil {
		cache.RemoveStructureBlockEntity(x, y, z)
	}
	ds := s.dispenserState()
	ds.mu.Lock()
	if legacyArr := ds.shulkerBoxes[dispenserBlockKey{dim: normDim, x: x, y: y, z: z}]; legacyArr != nil && be == nil {
		be = &StorageBlockEntity{
			ID:        "minecraft:shulker_box",
			Dimension: normDim,
			X:         x,
			Y:         y,
			Z:         z,
			Slots:     *legacyArr,
		}
	}
	delete(ds.shulkerBoxes, dispenserBlockKey{dim: normDim, x: x, y: y, z: z})
	ds.mu.Unlock()

	s.closeStorageViewersAt(normDim, x, y, z)
	_ = s.SaveChunkStorageContainers(normDim, int32(x>>4), int32(z>>4))
	s.notifyStorageComparators(normDim, x, y, z)
	return ShulkerBoxDataFromBlockEntity(be)
}

// PlaceShulkerBoxFromItem places `blockState` at (dim, x, y, z) and initializes
// its `minecraft:shulker_box` block entity from `data` (`minecraft:container` /
// `minecraft:container_loot`, `minecraft:custom_name`, `minecraft:lock`),
// preserving all 27 slots and component metadata without loss.
func (s *Server) PlaceShulkerBoxFromItem(dim string, x, y, z int, blockState uint16, data *ShulkerBoxItemData) bool {
	if s == nil {
		return false
	}
	normDim := normalizeStorageDim(dim)
	cache := s.BindNoteBlockAndJukeboxRedstone(normDim)
	if cache == nil {
		return false
	}
	if blockState == 0 || !IsShulkerBoxItemOrBlock(world.BlockName(blockState)) {
		st, ok := world.BlockStateByName("minecraft:shulker_box", map[string]string{"facing": "up"})
		if !ok {
			return false
		}
		blockState = st
	}
	applied, res := cache.ApplyBlockWithUpdates(x, y, z, blockState)
	if !applied {
		cache.SetBlock(x, y, z, blockState)
	} else if len(res.Changes) > 0 || len(res.LightChanged) > 0 {
		s.broadcastDimensionBlockChanges(normDim, cache, res.Changes, res.LightChanged)
	}
	s.ApplyShulkerBoxItemDataAt(normDim, x, y, z, data, "")
	return true
}

// ApplyShulkerBoxItemDataAt populates the `minecraft:shulker_box` block entity
// at (dim, x, y, z) from `data` (and optional fallback `customName` from `ItemMeta`).
func (s *Server) ApplyShulkerBoxItemDataAt(dim string, x, y, z int, data *ShulkerBoxItemData, fallbackCustomName string) {
	if s == nil {
		return
	}
	normDim := normalizeStorageDim(dim)
	be := &StorageBlockEntity{
		ID:        "minecraft:shulker_box",
		Dimension: normDim,
		X:         x,
		Y:         y,
		Z:         z,
	}
	if data != nil {
		cp := data.Clone()
		be.Slots = cp.Slots
		be.SlotMeta = cp.SlotMeta
		be.SlotPotions = cp.SlotPotions
		be.SlotShulker = cp.SlotShulker
		be.SlotComponents = cp.SlotComponents
		be.LootTable = cp.LootTable
		be.LootTableSeed = cp.LootTableSeed
		be.CustomName = cp.CustomName
		be.CustomNameTag = cp.CustomNameTag
		be.Lock = cp.Lock
		be.LockTag = cp.LockTag
	}
	if be.CustomName == "" && be.CustomNameTag == nil && fallbackCustomName != "" {
		be.CustomName = fallbackCustomName
	}
	s.SetStorageBlockEntity(be)
}

// OnShulkerBoxPlacedFromSlot transfers `ShulkerBoxItemData` and `CustomName` from
// `session`'s inventory `slot` onto the newly placed shulker box block at (dim, x, y, z).
func (s *Server) OnShulkerBoxPlacedFromSlot(session *PlayerSession, slot int, dim string, x, y, z int) {
	if s == nil {
		return
	}
	var data *ShulkerBoxItemData
	var customName string
	if session != nil && slot >= 0 && slot < world.PlayerInventorySlots {
		data, _ = GetSlotShulkerBoxData(session, slot)
		customName = GetSlotItemMeta(session, slot).CustomName
	}
	s.ApplyShulkerBoxItemDataAt(dim, x, y, z, data, customName)
}

// PlaceShulkerBoxBlock places the held shulker box item from `session` at
// (x, y, z) on `face`, restoring all `ShulkerBoxItemData` onto the placed
// `minecraft:shulker_box` block entity and consuming 1 item in Survival mode.
func (s *Server) PlaceShulkerBoxBlock(session *PlayerSession, x, y, z int, face uint8) (uint16, bool) {
	if s == nil || session == nil {
		return 0, false
	}
	invSlot := session.HeldInventorySlot()
	if invSlot < 0 || invSlot >= world.PlayerInventorySlots {
		return 0, false
	}
	held := session.InventorySlot(invSlot)
	if held.IsEmpty() || !IsShulkerBoxItemOrBlock(held.ResolvedID()) {
		return 0, false
	}
	baseState, ok := world.ItemToBlock(held.ResolvedItemID())
	if !ok {
		return 0, false
	}
	placedState := s.ResolvePlacedBlockState(session, baseState, int32(face))
	dim := normalizeStorageDim(session.Dimension())
	cache := s.DimensionChunks(dim)
	if cache == nil {
		return 0, false
	}
	applied, res := cache.ApplyBlockWithUpdates(x, y, z, placedState)
	if !applied {
		return 0, false
	}
	s.broadcastDimensionBlockChanges(dim, cache, res.Changes, res.LightChanged)
	s.OnShulkerBoxPlacedFromSlot(session, invSlot, dim, x, y, z)
	if session.Snapshot().GameMode == GameModeSurvival {
		_, _, _ = s.ConsumeHeldItem(session, 1)
	}
	return placedState, true
}

// CheckStorageUnlocked reports whether `session` can open a container guarded by `lockStr`
// (matching vanilla `BaseContainerBlockEntity.canUnlock`: empty lock allows everyone;
// non-empty lock requires the main-hand item's CustomName to equal `lockStr`).
func (s *Server) CheckStorageUnlocked(session *PlayerSession, lockStr string) bool {
	if lockStr == "" {
		return true
	}
	if session == nil {
		return false
	}
	invSlot := session.HeldInventorySlot()
	if invSlot < 0 || invSlot >= world.PlayerInventorySlots {
		return false
	}
	if session.InventorySlot(invSlot).IsEmpty() {
		return false
	}
	return GetSlotItemMeta(session, invSlot).CustomName == lockStr
}

// OpenShulkerBox opens the 27-slot shulker box screen (`MenuTypeShulkerBox = 20`)
// at (x, y, z) for `session`, unpacking any `LootTable` on first open.
func (s *Server) OpenShulkerBox(session *PlayerSession, x, y, z int) (int32, error) {
	if s == nil || session == nil {
		return 0, errors.New("server: nil session")
	}
	dim := normalizeStorageDim(session.Dimension())
	if be, ok := s.StorageBlockEntityAt(dim, x, y, z); ok && be != nil && !s.CheckStorageUnlocked(session, be.Lock) {
		return 0, errors.New("server: container is locked")
	}

	_ = s.CloseContainer(session, 0, false)

	titleComp := TranslatableComponent("container.shulkerBox")
	if be, ok := s.StorageBlockEntityAt(dim, x, y, z); ok && be != nil && be.CustomName != "" {
		titleComp = TextComponent(be.CustomName)
	}

	tr := getContainerTracker(session)
	tr.syncMu.Lock()
	defer tr.syncMu.Unlock()

	tr.mu.Lock()
	tr.nextWindow = (tr.nextWindow % 100) + 1
	cid := tr.nextWindow
	tr.open = &OpenContainerState{
		ContainerID: cid,
		MenuType:    MenuTypeShulkerBox,
		Dimension:   dim,
		BlockX:      x,
		BlockY:      y,
		BlockZ:      z,
	}
	carriedData := getCarriedSlotDataLocked(session, tr)
	tr.mu.Unlock()

	// Accessing ChestSlotsInDim unpacks LootTable if present.
	_ = s.ChestSlotsInDim(dim, x, y, z)
	s.updateContainerViewerState(dim, x, y, z)

	if session.send != nil {
		if err := session.send(protocol.PlayOpenScreen, EncodeOpenScreenComponent(cid, MenuTypeShulkerBox, titleComp)); err != nil {
			return cid, err
		}
		slotData := s.buildChestMenuSlotDataInDim(session, dim, x, y, z)
		stateID := session.NextContainerStateID()
		if err := session.send(protocol.PlayContainerSetContent, encodeContainerContentFromSlotData(cid, stateID, slotData, carriedData)); err != nil {
			return cid, err
		}
		setTrackerRemoteSnapshot(tr, cid, slotData, carriedData)
	}
	return cid, nil
}
