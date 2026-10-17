package server

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"regionio/internal/nbt"
	"regionio/internal/protocol"
	"regionio/internal/world"
)

// Hopper, Chiseled Bookshelf, and Crafter constants for Minecraft 26.1.2 (protocol 775).
const (
	HopperBlockSlotCount        = 5
	HopperBlockMenuSlotCount    = 41 // 5 hopper slots + 27 main inventory + 9 hotbar
	HopperBlockMenuType         = 16 // minecraft:hopper in BuiltInRegistries.MENU
	HopperTransferCooldownTicks = 8  // 8 game ticks (4 redstone ticks) per transfer

	ChiseledBookshelfSlotCount = 6 // slot_0_occupied .. slot_5_occupied
	CrafterSlotCount           = 9 // 3x3 auto-crafting grid

	LevelEventComposterFill int32 = 1500 // 1500: composter fill particles & sound (data=1 if level increased, 0 otherwise)
)

// HopperBlockEntity represents the server-side state of a `minecraft:hopper`
// block entity at (Dimension, X, Y, Z).
type HopperBlockEntity struct {
	Dimension        string
	X, Y, Z          int
	Slots            [HopperBlockSlotCount]world.ItemStack
	SlotMeta         [HopperBlockSlotCount]ItemMeta
	SlotPotions      [HopperBlockSlotCount]string
	SlotShulker      [HopperBlockSlotCount]*[ChestSlotCount]world.ItemStack
	TransferCooldown int
	CustomName       string
}

// Clone returns a deep copy of the HopperBlockEntity.
func (be *HopperBlockEntity) Clone() *HopperBlockEntity {
	if be == nil {
		return nil
	}
	cp := *be
	for i := 0; i < HopperBlockSlotCount; i++ {
		cp.SlotMeta[i] = be.SlotMeta[i].Clone()
		if be.SlotShulker[i] != nil {
			arr := *be.SlotShulker[i]
			cp.SlotShulker[i] = &arr
		}
	}
	return &cp
}

// IsEmpty reports whether all 5 slots of the hopper are empty.
func (be *HopperBlockEntity) IsEmpty() bool {
	if be == nil {
		return true
	}
	for i := 0; i < HopperBlockSlotCount; i++ {
		if !be.Slots[i].IsEmpty() && be.Slots[i].Count > 0 {
			return false
		}
	}
	return true
}

// IsFull reports whether all 5 slots of the hopper are at their maximum stack size.
func (be *HopperBlockEntity) IsFull() bool {
	if be == nil {
		return false
	}
	for i := 0; i < HopperBlockSlotCount; i++ {
		st := be.Slots[i]
		if st.IsEmpty() || st.Count <= 0 {
			return false
		}
		maxStack := DispenserItemMaxStackSize(st)
		if st.Count < maxStack {
			return false
		}
	}
	return true
}

// ChiseledBookshelfBlockEntity represents the server-side state of a
// `minecraft:chiseled_bookshelf` at (Dimension, X, Y, Z).
type ChiseledBookshelfBlockEntity struct {
	Dimension          string
	X, Y, Z            int
	Slots              [ChiseledBookshelfSlotCount]world.ItemStack
	SlotMeta           [ChiseledBookshelfSlotCount]ItemMeta
	LastInteractedSlot int // -1 if none, 0..5 after insert/extract (comparator output = LastInteractedSlot + 1)
}

// Clone returns a deep copy of the ChiseledBookshelfBlockEntity.
func (cb *ChiseledBookshelfBlockEntity) Clone() *ChiseledBookshelfBlockEntity {
	if cb == nil {
		return nil
	}
	cp := *cb
	for i := 0; i < ChiseledBookshelfSlotCount; i++ {
		cp.SlotMeta[i] = cb.SlotMeta[i].Clone()
	}
	return &cp
}

// CrafterBlockEntity represents the server-side state of a `minecraft:crafter`
// at (Dimension, X, Y, Z).
type CrafterBlockEntity struct {
	Dimension string
	X, Y, Z   int
	Slots     [CrafterSlotCount]world.ItemStack
	SlotMeta  [CrafterSlotCount]ItemMeta
	Disabled  [CrafterSlotCount]bool
}

// Clone returns a deep copy of the CrafterBlockEntity.
func (cr *CrafterBlockEntity) Clone() *CrafterBlockEntity {
	if cr == nil {
		return nil
	}
	cp := *cr
	for i := 0; i < CrafterSlotCount; i++ {
		cp.SlotMeta[i] = cr.SlotMeta[i].Clone()
	}
	return &cp
}

type hopperBlockKey struct {
	dim     string
	x, y, z int
}

type serverHopperState struct {
	mu               sync.Mutex
	hoppers          map[hopperBlockKey]*HopperBlockEntity
	bookshelves      map[hopperBlockKey]*ChiseledBookshelfBlockEntity
	crafters         map[hopperBlockKey]*CrafterBlockEntity
	composterPending map[hopperBlockKey]int // countdown ticks for level 7 -> 8 maturation
	minecartFallback map[int32]*[HopperBlockSlotCount]world.ItemStack
	minecartEnabled  map[int32]bool
	hookBound        bool
}

var serverHoppers sync.Map // map[*Server]*serverHopperState

func (s *Server) hopperState() *serverHopperState {
	if v, ok := serverHoppers.Load(s); ok {
		hs := v.(*serverHopperState)
		s.ensureHopperMinecartHookBound(hs)
		return hs
	}
	hs := &serverHopperState{
		hoppers:          make(map[hopperBlockKey]*HopperBlockEntity),
		bookshelves:      make(map[hopperBlockKey]*ChiseledBookshelfBlockEntity),
		crafters:         make(map[hopperBlockKey]*CrafterBlockEntity),
		composterPending: make(map[hopperBlockKey]int),
		minecartFallback: make(map[int32]*[HopperBlockSlotCount]world.ItemStack),
		minecartEnabled:  make(map[int32]bool),
	}
	actual, _ := serverHoppers.LoadOrStore(s, hs)
	res := actual.(*serverHopperState)
	s.ensureHopperMinecartHookBound(res)
	return res
}

// ensureHopperMinecartHookBound automatically registers HopperMinecartTransfer
// with Agent B's vehicle manager.
func (s *Server) ensureHopperMinecartHookBound(hs *serverHopperState) {
	if s == nil || hs == nil {
		return
	}
	hs.mu.Lock()
	if hs.hookBound {
		hs.mu.Unlock()
		return
	}
	hs.hookBound = true
	hs.mu.Unlock()

	s.RegisterHopperMinecartHook(HopperMinecartTransfer)
}

func normalizeHopperDim(dim string) string {
	if strings.TrimSpace(dim) == "" {
		return world.DimensionOverworld
	}
	return world.NormalizeDimension(dim)
}

// --- Hopper Block State Helpers ---

// IsHopperBlock reports whether state is a `minecraft:hopper` block.
func IsHopperBlock(state uint16) bool {
	return world.BlockName(state) == "minecraft:hopper"
}

// HopperFacing returns the output `facing` direction of a `minecraft:hopper`
// block state ("down", "north", "south", "west", or "east"). Note that hoppers
// never face "up"; invalid or missing values default to "down".
func HopperFacing(state uint16) string {
	props := world.BlockProperties(state)
	switch strings.ToLower(props["facing"]) {
	case "north", "south", "west", "east":
		return strings.ToLower(props["facing"])
	default:
		return "down"
	}
}

// HopperEnabled reports whether the `enabled` property of a `minecraft:hopper`
// block state is true.
func HopperEnabled(state uint16) bool {
	props := world.BlockProperties(state)
	if v, ok := props["enabled"]; ok {
		return v != "false"
	}
	return true
}

// HopperBlockState returns the block state ID for `minecraft:hopper` with the
// given output facing ("down", "north", "south", "west", "east") and enabled flag.
func HopperBlockState(facing string, enabled bool) uint16 {
	f := strings.ToLower(strings.TrimSpace(facing))
	switch f {
	case "north", "south", "west", "east":
	default:
		f = "down"
	}
	en := "true"
	if !enabled {
		en = "false"
	}
	if st, ok := world.BlockStateIDByProperties("minecraft:hopper", map[string]string{
		"facing":  f,
		"enabled": en,
	}); ok {
		return st
	}
	return 0
}

// HopperPlacementFacing computes the output `facing` property for a newly
// placed `minecraft:hopper` when clicking on `clickedFace` of a target block,
// matching vanilla HopperBlock.getStateForPlacement (opposite of clickedFace,
// or "down" if opposite would be "up").
func HopperPlacementFacing(clickedFace string) string {
	opp := oppositeDropperDirection(clickedFace)
	if opp == "up" {
		return "down"
	}
	switch opp {
	case "north", "south", "west", "east", "down":
		return opp
	default:
		return "down"
	}
}

// --- Hopper Block Entity CRUD & Slot Access ---

func (s *Server) getOrCreateHopperLocked(hs *serverHopperState, dim string, x, y, z int) *HopperBlockEntity {
	normDim := normalizeHopperDim(dim)
	key := hopperBlockKey{dim: normDim, x: x, y: y, z: z}
	be := hs.hoppers[key]
	if be == nil {
		be = &HopperBlockEntity{
			Dimension: normDim,
			X:         x,
			Y:         y,
			Z:         z,
		}
		hs.hoppers[key] = be
	}
	return be
}

// GetOrCreateHopper returns (or initializes) a snapshot of the HopperBlockEntity
// at (dim, x, y, z).
func (s *Server) GetOrCreateHopper(dim string, x, y, z int) *HopperBlockEntity {
	if s == nil {
		return nil
	}
	normDim := normalizeHopperDim(dim)
	s.BindNoteBlockAndJukeboxRedstone(normDim)
	hs := s.hopperState()
	hs.mu.Lock()
	be := s.getOrCreateHopperLocked(hs, normDim, x, y, z)
	cp := be.Clone()
	hs.mu.Unlock()
	return cp
}

// HopperAt returns a snapshot of the HopperBlockEntity at (dim, x, y, z) if present.
func (s *Server) HopperAt(dim string, x, y, z int) (*HopperBlockEntity, bool) {
	if s == nil {
		return nil, false
	}
	normDim := normalizeHopperDim(dim)
	hs := s.hopperState()
	hs.mu.Lock()
	be, ok := hs.hoppers[hopperBlockKey{dim: normDim, x: x, y: y, z: z}]
	var cp *HopperBlockEntity
	if ok && be != nil {
		cp = be.Clone()
	}
	hs.mu.Unlock()
	return cp, cp != nil
}

// HopperSlots returns the 5 item slots of the hopper at (x, y, z) in the Overworld.
func (s *Server) HopperSlots(x, y, z int) [HopperBlockSlotCount]world.ItemStack {
	return s.HopperSlotsInDimension(world.DimensionOverworld, x, y, z)
}

// HopperSlotsInDimension returns a copy of the 5 item slots of the hopper at (dim, x, y, z).
func (s *Server) HopperSlotsInDimension(dim string, x, y, z int) [HopperBlockSlotCount]world.ItemStack {
	if s == nil {
		return [HopperBlockSlotCount]world.ItemStack{}
	}
	normDim := normalizeHopperDim(dim)
	hs := s.hopperState()
	hs.mu.Lock()
	defer hs.mu.Unlock()
	be := hs.hoppers[hopperBlockKey{dim: normDim, x: x, y: y, z: z}]
	if be == nil {
		return [HopperBlockSlotCount]world.ItemStack{}
	}
	return be.Slots
}

// SetHopperSlot sets a single slot (0..4) in the hopper at (x, y, z) in the Overworld.
func (s *Server) SetHopperSlot(x, y, z, slot int, stack world.ItemStack) bool {
	return s.SetHopperSlotInDimension(world.DimensionOverworld, x, y, z, slot, stack)
}

// SetHopperSlotInDimension sets a single slot (0..4) in the hopper at (dim, x, y, z).
func (s *Server) SetHopperSlotInDimension(dim string, x, y, z, slot int, stack world.ItemStack) bool {
	return s.SetHopperSlotWithMeta(dim, x, y, z, slot, stack, ItemMeta{}, "")
}

// SetHopperSlotWithMeta sets a single slot (0..4) in the hopper at (dim, x, y, z)
// along with its ItemMeta and potionType, and updates adjacent comparators.
func (s *Server) SetHopperSlotWithMeta(dim string, x, y, z, slot int, stack world.ItemStack, meta ItemMeta, potionType string) bool {
	if s == nil || slot < 0 || slot >= HopperBlockSlotCount {
		return false
	}
	normDim := normalizeHopperDim(dim)
	s.BindNoteBlockAndJukeboxRedstone(normDim)
	hs := s.hopperState()
	hs.mu.Lock()
	be := s.getOrCreateHopperLocked(hs, normDim, x, y, z)
	if stack.IsEmpty() || stack.Count <= 0 {
		be.Slots[slot] = world.ItemStack{}
		be.SlotMeta[slot] = ItemMeta{}
		be.SlotPotions[slot] = ""
		be.SlotShulker[slot] = nil
	} else {
		if stack.ItemID <= 0 && stack.ID != "" {
			stack = world.NewNamedItemStack(stack.ID, stack.Count)
		} else {
			stack = world.NewItemStack(stack.ResolvedItemID(), stack.Count)
		}
		be.Slots[slot] = stack
		be.SlotMeta[slot] = meta.Clone()
		be.SlotPotions[slot] = potionType
	}
	hs.mu.Unlock()
	s.updateHopperComparatorsAndBroadcast(normDim, x, y, z)
	return true
}

// GetHopperSlotMeta returns the ItemMeta and potionType stored in slot (0..4)
// of the hopper at (dim, x, y, z).
func (s *Server) GetHopperSlotMeta(dim string, x, y, z, slot int) (ItemMeta, string) {
	if s == nil || slot < 0 || slot >= HopperBlockSlotCount {
		return ItemMeta{}, ""
	}
	normDim := normalizeHopperDim(dim)
	hs := s.hopperState()
	hs.mu.Lock()
	defer hs.mu.Unlock()
	be := hs.hoppers[hopperBlockKey{dim: normDim, x: x, y: y, z: z}]
	if be == nil {
		return ItemMeta{}, ""
	}
	return be.SlotMeta[slot].Clone(), be.SlotPotions[slot]
}

// HopperTransferCooldown returns the remaining transfer cooldown ticks (0..8)
// of the hopper at (dim, x, y, z).
func (s *Server) HopperTransferCooldown(dim string, x, y, z int) int {
	if s == nil {
		return 0
	}
	normDim := normalizeHopperDim(dim)
	hs := s.hopperState()
	hs.mu.Lock()
	defer hs.mu.Unlock()
	be := hs.hoppers[hopperBlockKey{dim: normDim, x: x, y: y, z: z}]
	if be == nil {
		return 0
	}
	return be.TransferCooldown
}

// SetHopperTransferCooldown sets the transfer cooldown ticks on the hopper at (dim, x, y, z).
func (s *Server) SetHopperTransferCooldown(dim string, x, y, z, cooldown int) {
	if s == nil {
		return
	}
	if cooldown < 0 {
		cooldown = 0
	}
	normDim := normalizeHopperDim(dim)
	hs := s.hopperState()
	hs.mu.Lock()
	be := s.getOrCreateHopperLocked(hs, normDim, x, y, z)
	be.TransferCooldown = cooldown
	hs.mu.Unlock()
}

// SetHopperCustomName sets the custom display name on the hopper at (dim, x, y, z).
func (s *Server) SetHopperCustomName(dim string, x, y, z int, name string) {
	if s == nil {
		return
	}
	normDim := normalizeHopperDim(dim)
	hs := s.hopperState()
	hs.mu.Lock()
	be := s.getOrCreateHopperLocked(hs, normDim, x, y, z)
	be.CustomName = name
	hs.mu.Unlock()
}

// RemoveHopper deletes the HopperBlockEntity at (dim, x, y, z) without dropping items.
func (s *Server) RemoveHopper(dim string, x, y, z int) {
	if s == nil {
		return
	}
	normDim := normalizeHopperDim(dim)
	hs := s.hopperState()
	hs.mu.Lock()
	delete(hs.hoppers, hopperBlockKey{dim: normDim, x: x, y: y, z: z})
	hs.mu.Unlock()
	s.updateHopperComparatorsAndBroadcast(normDim, x, y, z)
}

// BreakHopperAndDropItems removes the hopper block entity at (dim, x, y, z),
// drops all non-empty slot contents as item entities in the world, and updates
// adjacent comparators.
func (s *Server) BreakHopperAndDropItems(dim string, x, y, z int) {
	if s == nil {
		return
	}
	normDim := normalizeHopperDim(dim)
	hs := s.hopperState()
	hs.mu.Lock()
	key := hopperBlockKey{dim: normDim, x: x, y: y, z: z}
	be := hs.hoppers[key]
	delete(hs.hoppers, key)
	hs.mu.Unlock()

	if be != nil {
		for i := 0; i < HopperBlockSlotCount; i++ {
			st := be.Slots[i]
			if !st.IsEmpty() && st.Count > 0 {
				s.SpawnItemEntityInDimension(normDim, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, st)
			}
		}
	}
	s.updateHopperComparatorsAndBroadcast(normDim, x, y, z)
}

// DropHopperContents removes the hopper at (x, y, z) in the Overworld and drops its items.
func (s *Server) DropHopperContents(x, y, z int) {
	s.BreakHopperAndDropItems(world.DimensionOverworld, x, y, z)
}

// DropHopperContentsInDimension removes the hopper at (dim, x, y, z) and drops its items.
func (s *Server) DropHopperContentsInDimension(dim string, x, y, z int) {
	s.BreakHopperAndDropItems(dim, x, y, z)
}

func (s *Server) updateHopperComparatorsAndBroadcast(dim string, x, y, z int) {
	cache := s.BindNoteBlockAndJukeboxRedstone(dim)
	if cache == nil {
		return
	}
	res := cache.UpdateComparatorsAround(x, y, z)
	for _, ch := range res.Changes {
		s.broadcastBlockUpdateInDim(dim, ch.Pos.X, ch.Pos.Y, ch.Pos.Z, ch.NewState)
	}
}

// --- Hopper Comparator Signal & Redstone Locking ---

// HopperComparatorSignalAt returns the vanilla redstone comparator signal (0..15)
// for a `minecraft:hopper` at (dim, x, y, z), matching
// AbstractContainerMenu.getRedstoneSignalFromContainer over the 5 hopper slots.
func (s *Server) HopperComparatorSignalAt(dim string, x, y, z int) int {
	if s == nil {
		return 0
	}
	normDim := normalizeHopperDim(dim)
	if cache := s.DimensionChunks(normDim); cache != nil {
		st := cache.GetBlock(x, y, z)
		if st != world.StateAir && !IsHopperBlock(st) {
			return 0
		}
	}
	hs := s.hopperState()
	hs.mu.Lock()
	be := hs.hoppers[hopperBlockKey{dim: normDim, x: x, y: y, z: z}]
	if be == nil {
		hs.mu.Unlock()
		return 0
	}
	slots := be.Slots
	hs.mu.Unlock()
	return CalculateContainerComparatorSignal(slots[:])
}

// IsHopperBlockEnabled reports whether the `minecraft:hopper` at (dim, x, y, z)
// is unlocked (its block state `enabled` is true and it is not receiving redstone power).
func (s *Server) IsHopperBlockEnabled(dim string, x, y, z int) bool {
	if s == nil {
		return false
	}
	normDim := normalizeHopperDim(dim)
	cache := s.DimensionChunks(normDim)
	if cache == nil {
		return true
	}
	state := cache.GetBlock(x, y, z)
	if !IsHopperBlock(state) {
		return false
	}
	if cache.IsBlockPowered(x, y, z) {
		return false
	}
	return HopperEnabled(state)
}

// UpdateHopperRedstone evaluates redstone power at (dim, x, y, z) and updates
// the `enabled` block state property of a `minecraft:hopper` ("false" when
// powered, "true" when unpowered), matching vanilla HopperBlock.checkPoweredState.
func (s *Server) UpdateHopperRedstone(dim string, x, y, z int) bool {
	if s == nil {
		return false
	}
	normDim := normalizeHopperDim(dim)
	cache := s.BindNoteBlockAndJukeboxRedstone(normDim)
	if cache == nil {
		return false
	}
	state := cache.GetBlock(x, y, z)
	if !IsHopperBlock(state) {
		return false
	}
	powered := cache.IsBlockPowered(x, y, z)
	wantEnabled := "true"
	if powered {
		wantEnabled = "false"
	}
	props := world.BlockProperties(state)
	if props["enabled"] == wantEnabled {
		return !powered
	}
	if nextSt, ok := world.WithBlockProperty(state, "enabled", wantEnabled); ok {
		_, res := cache.ApplyBlockWithUpdates(x, y, z, nextSt)
		s.broadcastBlockUpdateInDim(normDim, x, y, z, nextSt)
		for _, ch := range res.Changes {
			s.broadcastBlockUpdateInDim(normDim, ch.Pos.X, ch.Pos.Y, ch.Pos.Z, ch.NewState)
		}
	}
	return !powered
}

// --- Chiseled Bookshelf (`minecraft:chiseled_bookshelf`) State & Hopper API ---

// IsChiseledBookshelfBlock reports whether state is a `minecraft:chiseled_bookshelf`.
func IsChiseledBookshelfBlock(state uint16) bool {
	return world.BlockName(state) == "minecraft:chiseled_bookshelf"
}

// IsChiseledBookshelfBook reports whether stack is in the vanilla 26.1.2
// `#minecraft:bookshelf_books` item tag (book, enchanted_book, writable_book,
// written_book, knowledge_book).
func IsChiseledBookshelfBook(stack world.ItemStack) bool {
	if stack.IsEmpty() || stack.Count <= 0 {
		return false
	}
	switch canonicalResourceName(stack.ResolvedID()) {
	case "minecraft:book",
		"minecraft:enchanted_book",
		"minecraft:writable_book",
		"minecraft:written_book",
		"minecraft:knowledge_book":
		return true
	default:
		return false
	}
}

func (s *Server) getOrCreateChiseledBookshelfLocked(hs *serverHopperState, dim string, cache *world.Cache, x, y, z int) *ChiseledBookshelfBlockEntity {
	normDim := normalizeHopperDim(dim)
	key := hopperBlockKey{dim: normDim, x: x, y: y, z: z}
	cb := hs.bookshelves[key]
	if cb == nil {
		cb = &ChiseledBookshelfBlockEntity{
			Dimension:          normDim,
			X:                  x,
			Y:                  y,
			Z:                  z,
			LastInteractedSlot: -1,
		}
		if cache != nil {
			st := cache.GetBlock(x, y, z)
			if IsChiseledBookshelfBlock(st) {
				props := world.BlockProperties(st)
				for i := 0; i < ChiseledBookshelfSlotCount; i++ {
					propName := "slot_" + strconv.Itoa(i) + "_occupied"
					if props[propName] == "true" {
						cb.Slots[i] = world.NewNamedItemStack("minecraft:book", 1)
						cb.LastInteractedSlot = i
					}
				}
			}
		}
		hs.bookshelves[key] = cb
	}
	return cb
}

// GetChiseledBookshelfAt returns a snapshot of the ChiseledBookshelfBlockEntity at (dim, x, y, z).
func (s *Server) GetChiseledBookshelfAt(dim string, x, y, z int) (*ChiseledBookshelfBlockEntity, bool) {
	if s == nil {
		return nil, false
	}
	normDim := normalizeHopperDim(dim)
	cache := s.DimensionChunks(normDim)
	hs := s.hopperState()
	hs.mu.Lock()
	cb, ok := hs.bookshelves[hopperBlockKey{dim: normDim, x: x, y: y, z: z}]
	if !ok && cache != nil && IsChiseledBookshelfBlock(cache.GetBlock(x, y, z)) {
		cb = s.getOrCreateChiseledBookshelfLocked(hs, normDim, cache, x, y, z)
		ok = true
	}
	var cp *ChiseledBookshelfBlockEntity
	if ok && cb != nil {
		cp = cb.Clone()
	}
	hs.mu.Unlock()
	return cp, cp != nil
}

// SetChiseledBookshelfSlot sets slot (0..5) of a `minecraft:chiseled_bookshelf`
// at (dim, x, y, z), updates `slot_<i>_occupied` on the block state, sets
// `LastInteractedSlot = slot`, and updates adjacent comparators.
func (s *Server) SetChiseledBookshelfSlot(dim string, x, y, z, slot int, stack world.ItemStack, meta ItemMeta) bool {
	if s == nil || slot < 0 || slot >= ChiseledBookshelfSlotCount {
		return false
	}
	if !stack.IsEmpty() && !IsChiseledBookshelfBook(stack) {
		return false
	}
	normDim := normalizeHopperDim(dim)
	cache := s.BindNoteBlockAndJukeboxRedstone(normDim)
	hs := s.hopperState()
	hs.mu.Lock()
	cb := s.getOrCreateChiseledBookshelfLocked(hs, normDim, cache, x, y, z)
	occupiedStr := "false"
	if stack.IsEmpty() || stack.Count <= 0 {
		cb.Slots[slot] = world.ItemStack{}
		cb.SlotMeta[slot] = ItemMeta{}
	} else {
		cb.Slots[slot] = world.NewNamedItemStack(stack.ResolvedID(), 1)
		cb.SlotMeta[slot] = meta.Clone()
		occupiedStr = "true"
	}
	cb.LastInteractedSlot = slot
	hs.mu.Unlock()

	if cache != nil {
		st := cache.GetBlock(x, y, z)
		if IsChiseledBookshelfBlock(st) {
			propName := "slot_" + strconv.Itoa(slot) + "_occupied"
			if nextSt, ok := world.WithBlockProperty(st, propName, occupiedStr); ok && nextSt != st {
				_, res := cache.ApplyBlockWithUpdates(x, y, z, nextSt)
				s.broadcastBlockUpdateInDim(normDim, x, y, z, nextSt)
				for _, ch := range res.Changes {
					s.broadcastBlockUpdateInDim(normDim, ch.Pos.X, ch.Pos.Y, ch.Pos.Z, ch.NewState)
				}
			}
		}
	}
	s.updateHopperComparatorsAndBroadcast(normDim, x, y, z)
	return true
}

// ChiseledBookshelfComparatorSignalAt returns the redstone comparator output
// (0..6, equal to LastInteractedSlot + 1) for a `minecraft:chiseled_bookshelf`
// at (dim, x, y, z), matching vanilla ChiseledBookshelfBlock.getAnalogOutputSignal.
func (s *Server) ChiseledBookshelfComparatorSignalAt(dim string, x, y, z int) int {
	if s == nil {
		return 0
	}
	normDim := normalizeHopperDim(dim)
	if cache := s.DimensionChunks(normDim); cache != nil {
		st := cache.GetBlock(x, y, z)
		if st != world.StateAir && !IsChiseledBookshelfBlock(st) {
			return 0
		}
	}
	hs := s.hopperState()
	hs.mu.Lock()
	cb := hs.bookshelves[hopperBlockKey{dim: normDim, x: x, y: y, z: z}]
	if cb == nil {
		hs.mu.Unlock()
		return 0
	}
	last := cb.LastInteractedSlot
	hs.mu.Unlock()
	if last < 0 || last >= ChiseledBookshelfSlotCount {
		return 0
	}
	return last + 1
}

// HopperInsertIntoChiseledBookshelf inserts 1 book item into the first unoccupied
// slot (0..5) of the `minecraft:chiseled_bookshelf` at (dim, x, y, z).
func (s *Server) HopperInsertIntoChiseledBookshelf(dim string, x, y, z int, stack world.ItemStack, meta ItemMeta) bool {
	if s == nil || !IsChiseledBookshelfBook(stack) {
		return false
	}
	normDim := normalizeHopperDim(dim)
	cache := s.BindNoteBlockAndJukeboxRedstone(normDim)
	hs := s.hopperState()
	hs.mu.Lock()
	cb := s.getOrCreateChiseledBookshelfLocked(hs, normDim, cache, x, y, z)
	targetSlot := -1
	for i := 0; i < ChiseledBookshelfSlotCount; i++ {
		if cb.Slots[i].IsEmpty() {
			targetSlot = i
			break
		}
	}
	hs.mu.Unlock()
	if targetSlot < 0 {
		return false
	}
	return s.SetChiseledBookshelfSlot(normDim, x, y, z, targetSlot, world.NewNamedItemStack(stack.ResolvedID(), 1), meta)
}

// HopperExtractFromChiseledBookshelf extracts 1 book item from the first occupied
// slot (0..5) of the `minecraft:chiseled_bookshelf` at (dim, x, y, z).
func (s *Server) HopperExtractFromChiseledBookshelf(dim string, x, y, z int) (world.ItemStack, ItemMeta, bool) {
	if s == nil {
		return world.ItemStack{}, ItemMeta{}, false
	}
	normDim := normalizeHopperDim(dim)
	cache := s.BindNoteBlockAndJukeboxRedstone(normDim)
	hs := s.hopperState()
	hs.mu.Lock()
	cb := s.getOrCreateChiseledBookshelfLocked(hs, normDim, cache, x, y, z)
	targetSlot := -1
	var extracted world.ItemStack
	var meta ItemMeta
	for i := 0; i < ChiseledBookshelfSlotCount; i++ {
		if !cb.Slots[i].IsEmpty() && cb.Slots[i].Count > 0 {
			targetSlot = i
			extracted = cb.Slots[i]
			meta = cb.SlotMeta[i].Clone()
			break
		}
	}
	hs.mu.Unlock()
	if targetSlot < 0 {
		return world.ItemStack{}, ItemMeta{}, false
	}
	s.SetChiseledBookshelfSlot(normDim, x, y, z, targetSlot, world.ItemStack{}, ItemMeta{})
	return extracted, meta, true
}

// --- Crafter (`minecraft:crafter`) State & Hopper API ---

// IsCrafterBlock reports whether state is a `minecraft:crafter`.
func IsCrafterBlock(state uint16) bool {
	return world.BlockName(state) == "minecraft:crafter"
}

func (s *Server) getOrCreateCrafterLocked(hs *serverHopperState, dim string, x, y, z int) *CrafterBlockEntity {
	normDim := normalizeHopperDim(dim)
	key := hopperBlockKey{dim: normDim, x: x, y: y, z: z}
	cr := hs.crafters[key]
	if cr == nil {
		cr = &CrafterBlockEntity{
			Dimension: normDim,
			X:         x,
			Y:         y,
			Z:         z,
		}
		hs.crafters[key] = cr
	}
	return cr
}

// GetCrafterAt returns a snapshot of the CrafterBlockEntity at (dim, x, y, z).
func (s *Server) GetCrafterAt(dim string, x, y, z int) (*CrafterBlockEntity, bool) {
	if s == nil {
		return nil, false
	}
	normDim := normalizeHopperDim(dim)
	hs := s.hopperState()
	hs.mu.Lock()
	cr, ok := hs.crafters[hopperBlockKey{dim: normDim, x: x, y: y, z: z}]
	var cp *CrafterBlockEntity
	if ok && cr != nil {
		cp = cr.Clone()
	}
	hs.mu.Unlock()
	return cp, cp != nil
}

// SetCrafterSlot sets slot (0..8) in the `minecraft:crafter` at (dim, x, y, z).
func (s *Server) SetCrafterSlot(dim string, x, y, z, slot int, stack world.ItemStack, meta ItemMeta) bool {
	if s == nil || slot < 0 || slot >= CrafterSlotCount {
		return false
	}
	normDim := normalizeHopperDim(dim)
	s.BindNoteBlockAndJukeboxRedstone(normDim)
	hs := s.hopperState()
	hs.mu.Lock()
	cr := s.getOrCreateCrafterLocked(hs, normDim, x, y, z)
	if stack.IsEmpty() || stack.Count <= 0 {
		cr.Slots[slot] = world.ItemStack{}
		cr.SlotMeta[slot] = ItemMeta{}
	} else {
		if stack.ItemID <= 0 && stack.ID != "" {
			stack = world.NewNamedItemStack(stack.ID, stack.Count)
		} else {
			stack = world.NewItemStack(stack.ResolvedItemID(), stack.Count)
		}
		cr.Slots[slot] = stack
		cr.SlotMeta[slot] = meta.Clone()
	}
	hs.mu.Unlock()
	s.updateHopperComparatorsAndBroadcast(normDim, x, y, z)
	return true
}

// SetCrafterSlotDisabled sets whether slot (0..8) of the `minecraft:crafter` at
// (dim, x, y, z) is disabled (locked against item insertion), and updates
// adjacent comparators.
func (s *Server) SetCrafterSlotDisabled(dim string, x, y, z, slot int, disabled bool) bool {
	if s == nil || slot < 0 || slot >= CrafterSlotCount {
		return false
	}
	normDim := normalizeHopperDim(dim)
	s.BindNoteBlockAndJukeboxRedstone(normDim)
	hs := s.hopperState()
	hs.mu.Lock()
	cr := s.getOrCreateCrafterLocked(hs, normDim, x, y, z)
	cr.Disabled[slot] = disabled
	hs.mu.Unlock()
	s.updateHopperComparatorsAndBroadcast(normDim, x, y, z)
	return true
}

// CrafterComparatorSignalAt returns the redstone comparator signal (0..9) for
// a `minecraft:crafter` at (dim, x, y, z), equal to the number of slots (0..8)
// that are either non-empty or disabled (matching vanilla CrafterBlockEntity.getRedstoneStrength).
func (s *Server) CrafterComparatorSignalAt(dim string, x, y, z int) int {
	if s == nil {
		return 0
	}
	normDim := normalizeHopperDim(dim)
	if cache := s.DimensionChunks(normDim); cache != nil {
		st := cache.GetBlock(x, y, z)
		if st != world.StateAir && !IsCrafterBlock(st) {
			return 0
		}
	}
	hs := s.hopperState()
	hs.mu.Lock()
	cr := hs.crafters[hopperBlockKey{dim: normDim, x: x, y: y, z: z}]
	if cr == nil {
		hs.mu.Unlock()
		return 0
	}
	sig := 0
	for i := 0; i < CrafterSlotCount; i++ {
		if (!cr.Slots[i].IsEmpty() && cr.Slots[i].Count > 0) || cr.Disabled[i] {
			sig++
		}
	}
	hs.mu.Unlock()
	return sig
}

// HopperInsertIntoCrafter inserts 1 item into the `minecraft:crafter` at
// (dim, x, y, z) following vanilla CrafterBlockEntity.canPlaceItem:
//   - Disabled slots are skipped.
//   - If any enabled slot is empty, inserts into the first empty enabled slot.
//   - Otherwise, inserts into the enabled slot containing the same item (with
//     count < maxStackSize) that currently has the smallest stack count.
func (s *Server) HopperInsertIntoCrafter(dim string, x, y, z int, stack world.ItemStack, meta ItemMeta) bool {
	if s == nil || stack.IsEmpty() || stack.Count <= 0 {
		return false
	}
	normDim := normalizeHopperDim(dim)
	s.BindNoteBlockAndJukeboxRedstone(normDim)
	maxStack := DispenserItemMaxStackSize(stack)
	itemID := stack.ResolvedItemID()
	single := world.NewItemStack(itemID, 1)
	if single.IsEmpty() {
		single = world.NewNamedItemStack(stack.ResolvedID(), 1)
	}

	hs := s.hopperState()
	hs.mu.Lock()
	cr := s.getOrCreateCrafterLocked(hs, normDim, x, y, z)

	// 1. Prefer the first empty enabled slot.
	for i := 0; i < CrafterSlotCount; i++ {
		if cr.Disabled[i] {
			continue
		}
		if cr.Slots[i].IsEmpty() || cr.Slots[i].Count <= 0 {
			cr.Slots[i] = single
			cr.SlotMeta[i] = meta.Clone()
			hs.mu.Unlock()
			s.updateHopperComparatorsAndBroadcast(normDim, x, y, z)
			return true
		}
	}

	// 2. Otherwise, merge into the matching enabled slot with the smallest stack count.
	bestSlot := -1
	bestCount := int32(maxStack)
	if maxStack > 1 && meta.IsEmpty() {
		for i := 0; i < CrafterSlotCount; i++ {
			if cr.Disabled[i] {
				continue
			}
			cur := cr.Slots[i]
			if cur.ResolvedItemID() == itemID && cr.SlotMeta[i].IsEmpty() && cur.Count < bestCount {
				bestCount = cur.Count
				bestSlot = i
			}
		}
	}
	if bestSlot >= 0 {
		cr.Slots[bestSlot] = world.NewItemStack(itemID, cr.Slots[bestSlot].Count+1)
		hs.mu.Unlock()
		s.updateHopperComparatorsAndBroadcast(normDim, x, y, z)
		return true
	}
	hs.mu.Unlock()
	return false
}

// HopperExtractFromCrafter extracts 1 item from the first non-empty slot (0..8)
// of the `minecraft:crafter` at (dim, x, y, z).
func (s *Server) HopperExtractFromCrafter(dim string, x, y, z int) (world.ItemStack, ItemMeta, bool) {
	if s == nil {
		return world.ItemStack{}, ItemMeta{}, false
	}
	normDim := normalizeHopperDim(dim)
	s.BindNoteBlockAndJukeboxRedstone(normDim)
	hs := s.hopperState()
	hs.mu.Lock()
	cr := hs.crafters[hopperBlockKey{dim: normDim, x: x, y: y, z: z}]
	if cr == nil {
		hs.mu.Unlock()
		return world.ItemStack{}, ItemMeta{}, false
	}
	for i := 0; i < CrafterSlotCount; i++ {
		cur := cr.Slots[i]
		if cur.IsEmpty() || cur.Count <= 0 {
			continue
		}
		out := world.NewItemStack(cur.ResolvedItemID(), 1)
		if out.IsEmpty() {
			out = world.NewNamedItemStack(cur.ResolvedID(), 1)
		}
		meta := cr.SlotMeta[i].Clone()
		if cur.Count <= 1 {
			cr.Slots[i] = world.ItemStack{}
			cr.SlotMeta[i] = ItemMeta{}
		} else {
			cr.Slots[i] = world.NewItemStack(cur.ResolvedItemID(), cur.Count-1)
		}
		hs.mu.Unlock()
		s.updateHopperComparatorsAndBroadcast(normDim, x, y, z)
		return out, meta, true
	}
	hs.mu.Unlock()
	return world.ItemStack{}, ItemMeta{}, false
}

// --- Composter (`minecraft:composter`) Hopper & Dropper API ---

// CompostChanceForItem returns the vanilla 26.1.2 composting probability
// (0.30, 0.50, 0.65, 0.85, or 1.00) for itemID, or 0 if not compostable.
func CompostChanceForItem(itemID string) float32 {
	norm := canonicalResourceName(itemID)
	if norm == "" {
		return 0
	}
	if strings.HasSuffix(norm, "_leaves") || strings.HasSuffix(norm, "_sapling") || strings.HasSuffix(norm, "_seeds") {
		return 0.30
	}
	switch norm {
	case "minecraft:short_grass", "minecraft:kelp", "minecraft:moss_carpet", "minecraft:pale_moss_carpet",
		"minecraft:seagrass", "minecraft:small_dripleaf", "minecraft:sweet_berries", "minecraft:glow_berries",
		"minecraft:hanging_roots", "minecraft:pale_hanging_moss", "minecraft:mangrove_propagule",
		"minecraft:Mangrove_roots", "minecraft:mangrove_roots", "minecraft:pink_petals", "minecraft:pitcher_pod",
		"minecraft:torchflower_seeds", "minecraft:short_dry_grass", "minecraft:bush", "minecraft:firefly_bush",
		"minecraft:leaf_litter", "minecraft:wildflowers":
		return 0.30
	case "minecraft:dried_kelp", "minecraft:tall_grass", "minecraft:cactus", "minecraft:sugar_cane",
		"minecraft:vine", "minecraft:nether_sprouts", "minecraft:weeping_vines", "minecraft:twisting_vines",
		"minecraft:melon_slice", "minecraft:glow_lichen", "minecraft:tall_dry_grass", "minecraft:cactus_flower":
		return 0.50
	case "minecraft:apple", "minecraft:beetroot", "minecraft:carrot", "minecraft:cocoa_beans",
		"minecraft:potato", "minecraft:wheat", "minecraft:melon", "minecraft:pumpkin", "minecraft:carved_pumpkin",
		"minecraft:sea_pickle", "minecraft:brown_mushroom", "minecraft:red_mushroom",
		"minecraft:crimson_fungus", "minecraft:warped_fungus", "minecraft:lily_pad",
		"minecraft:fern", "minecraft:large_fern", "minecraft:dandelion", "minecraft:poppy",
		"minecraft:blue_orchid", "minecraft:allium", "minecraft:azure_bluet", "minecraft:oxeye_daisy",
		"minecraft:cornflower", "minecraft:lily_of_the_valley", "minecraft:wither_rose",
		"minecraft:sunflower", "minecraft:lilac", "minecraft:rose_bush", "minecraft:peony",
		"minecraft:moss_block", "minecraft:pale_moss_block", "minecraft:azalea", "minecraft:big_dripleaf",
		"minecraft:shroomlight", "minecraft:spore_blossom", "minecraft:open_eyeblossom", "minecraft:closed_eyeblossom":
		return 0.65
	case "minecraft:baked_potato", "minecraft:bread", "minecraft:cookie", "minecraft:hay_block",
		"minecraft:nether_wart_block", "minecraft:warped_wart_block", "minecraft:flowering_azalea",
		"minecraft:red_mushroom_block", "minecraft:brown_mushroom_block",
		"minecraft:torchflower", "minecraft:pitcher_plant", "minecraft:dried_kelp_block":
		return 0.85
	case "minecraft:cake", "minecraft:pumpkin_pie":
		return 1.00
	default:
		return 0
	}
}

// HopperInsertIntoComposter attempts to insert 1 compostable item into the
// `minecraft:composter` at (dim, x, y, z) from `entryFace` ("up" when pushed by
// a hopper above facing "down").
// In vanilla ComposterBlock, hoppers can only insert from the top ("up"), and
// only when level < 7. Reaching level 7 schedules maturation to level 8.
func (s *Server) HopperInsertIntoComposter(dim string, x, y, z int, entryFace string, stack world.ItemStack) bool {
	if s == nil || stack.IsEmpty() || stack.Count <= 0 {
		return false
	}
	if strings.ToLower(entryFace) != "up" {
		return false
	}
	chance := CompostChanceForItem(stack.ResolvedID())
	if chance <= 0 {
		return false
	}
	normDim := normalizeHopperDim(dim)
	cache := s.BindNoteBlockAndJukeboxRedstone(normDim)
	if cache == nil {
		return false
	}
	state := cache.GetBlock(x, y, z)
	if world.BlockName(state) != "minecraft:composter" {
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
		if nextSt, ok := world.WithBlockProperty(state, "level", strconv.Itoa(nextLvl)); ok {
			s.setDispenserWorldBlock(normDim, cache, x, y, z, nextSt)
		}
		if nextLvl == 7 {
			hs := s.hopperState()
			hs.mu.Lock()
			hs.composterPending[hopperBlockKey{dim: normDim, x: x, y: y, z: z}] = 1
			hs.mu.Unlock()
		}
	}
	s.BroadcastDimensionChunk(normDim, int32(x>>4), int32(z>>4), PlayLevelEventCB,
		EncodeLevelEventPacket(LevelEventComposterFill, x, y, z, boolToInt32(nextLvl > lvl), false))
	return true
}

func boolToInt32(b bool) int32 {
	if b {
		return 1
	}
	return 0
}

// HopperExtractFromComposter attempts to extract 1 `minecraft:bone_meal` from
// the bottom of a ready `minecraft:composter` (level == 8, or level == 7 whose
// maturation timer has elapsed) at (dim, x, y, z), resetting its level to 0.
func (s *Server) HopperExtractFromComposter(dim string, x, y, z int) (world.ItemStack, bool) {
	if s == nil {
		return world.ItemStack{}, false
	}
	normDim := normalizeHopperDim(dim)
	cache := s.BindNoteBlockAndJukeboxRedstone(normDim)
	if cache == nil {
		return world.ItemStack{}, false
	}
	state := cache.GetBlock(x, y, z)
	if world.BlockName(state) != "minecraft:composter" {
		return world.ItemStack{}, false
	}
	props := world.BlockProperties(state)
	lvl, _ := strconv.Atoi(props["level"])
	if lvl == 7 {
		hs := s.hopperState()
		hs.mu.Lock()
		key := hopperBlockKey{dim: normDim, x: x, y: y, z: z}
		rem, exists := hs.composterPending[key]
		if !exists || rem <= 0 {
			delete(hs.composterPending, key)
			lvl = 8
		}
		hs.mu.Unlock()
	}
	if lvl != 8 {
		return world.ItemStack{}, false
	}
	if nextSt, ok := world.WithBlockProperty(state, "level", "0"); ok {
		s.setDispenserWorldBlock(normDim, cache, x, y, z, nextSt)
	}
	return world.NewNamedItemStack("minecraft:bone_meal", 1), true
}

// --- Hopper <-> Hopper Direct Transfer ---

// HopperInsertIntoHopper attempts to insert 1 item into the `minecraft:hopper`
// at (dim, x, y, z). If the target hopper was completely empty before insertion,
// its TransferCooldown is set to 8 ticks (matching vanilla HopperBlockEntity.tryTakeInItemFromSlot).
func (s *Server) HopperInsertIntoHopper(dim string, x, y, z int, stack world.ItemStack, meta ItemMeta, potionType string) bool {
	if s == nil || stack.IsEmpty() || stack.Count <= 0 {
		return false
	}
	normDim := normalizeHopperDim(dim)
	s.BindNoteBlockAndJukeboxRedstone(normDim)
	maxStack := DispenserItemMaxStackSize(stack)
	itemID := stack.ResolvedItemID()
	single := world.NewItemStack(itemID, 1)
	if single.IsEmpty() {
		single = world.NewNamedItemStack(stack.ResolvedID(), 1)
	}

	hs := s.hopperState()
	hs.mu.Lock()
	be := s.getOrCreateHopperLocked(hs, normDim, x, y, z)
	wasEmpty := be.IsEmpty()

	// 1. Try merging into an existing matching slot.
	if maxStack > 1 && meta.IsEmpty() && potionType == "" {
		for i := 0; i < HopperBlockSlotCount; i++ {
			cur := be.Slots[i]
			if !cur.IsEmpty() && cur.ResolvedItemID() == itemID &&
				be.SlotMeta[i].IsEmpty() && be.SlotPotions[i] == "" &&
				cur.Count < maxStack {
				be.Slots[i] = world.NewItemStack(itemID, cur.Count+1)
				if wasEmpty && be.TransferCooldown <= 0 {
					be.TransferCooldown = HopperTransferCooldownTicks
				}
				hs.mu.Unlock()
				s.updateHopperComparatorsAndBroadcast(normDim, x, y, z)
				_ = s.SyncHopperViewers(nil, x, y, z)
				return true
			}
		}
	}

	// 2. Place into the first empty slot.
	for i := 0; i < HopperBlockSlotCount; i++ {
		if be.Slots[i].IsEmpty() || be.Slots[i].Count <= 0 {
			be.Slots[i] = single
			be.SlotMeta[i] = meta.Clone()
			be.SlotPotions[i] = potionType
			if wasEmpty && be.TransferCooldown <= 0 {
				be.TransferCooldown = HopperTransferCooldownTicks
			}
			hs.mu.Unlock()
			s.updateHopperComparatorsAndBroadcast(normDim, x, y, z)
			_ = s.SyncHopperViewers(nil, x, y, z)
			return true
		}
	}
	hs.mu.Unlock()
	return false
}

// HopperExtractFromHopper extracts 1 item from the first non-empty slot (0..4)
// of the `minecraft:hopper` at (dim, x, y, z).
func (s *Server) HopperExtractFromHopper(dim string, x, y, z int) (world.ItemStack, ItemMeta, string, bool) {
	if s == nil {
		return world.ItemStack{}, ItemMeta{}, "", false
	}
	normDim := normalizeHopperDim(dim)
	hs := s.hopperState()
	hs.mu.Lock()
	be := hs.hoppers[hopperBlockKey{dim: normDim, x: x, y: y, z: z}]
	if be == nil {
		hs.mu.Unlock()
		return world.ItemStack{}, ItemMeta{}, "", false
	}
	for i := 0; i < HopperBlockSlotCount; i++ {
		cur := be.Slots[i]
		if cur.IsEmpty() || cur.Count <= 0 {
			continue
		}
		out := world.NewItemStack(cur.ResolvedItemID(), 1)
		if out.IsEmpty() {
			out = world.NewNamedItemStack(cur.ResolvedID(), 1)
		}
		meta := be.SlotMeta[i].Clone()
		pot := be.SlotPotions[i]
		if cur.Count <= 1 {
			be.Slots[i] = world.ItemStack{}
			be.SlotMeta[i] = ItemMeta{}
			be.SlotPotions[i] = ""
			be.SlotShulker[i] = nil
		} else {
			be.Slots[i] = world.NewItemStack(cur.ResolvedItemID(), cur.Count-1)
		}
		hs.mu.Unlock()
		s.updateHopperComparatorsAndBroadcast(normDim, x, y, z)
		_ = s.SyncHopperViewers(nil, x, y, z)
		return out, meta, pot, true
	}
	hs.mu.Unlock()
	return world.ItemStack{}, ItemMeta{}, "", false
}

// --- Generic Container Push & Pull (All 26.1.2 Containers) ---

// IsHopperContainerBlock reports whether state is any container block that a
// hopper can push items into or pull items from.
func IsHopperContainerBlock(state uint16) bool {
	if state == world.StateAir {
		return false
	}
	name := world.BlockName(state)
	switch name {
	case "minecraft:hopper",
		"minecraft:chest", "minecraft:trapped_chest", "minecraft:barrel",
		"minecraft:furnace", "minecraft:blast_furnace", "minecraft:smoker",
		"minecraft:brewing_stand",
		"minecraft:dispenser", "minecraft:dropper",
		"minecraft:jukebox",
		"minecraft:composter",
		"minecraft:chiseled_bookshelf",
		"minecraft:crafter":
		return true
	}
	return IsShulkerBoxItemOrBlock(name)
}

// doubleChestPartnerPos returns the coordinates of the paired second chest block
// when state is a `type="left"` or `type="right"` double chest, and whether this
// half is the first (upper) 27 slots of the combined 54-slot double chest.
func doubleChestPartnerPos(cache *world.Cache, x, y, z int, state uint16) (px, py, pz int, thisFirst bool, ok bool) {
	if cache == nil {
		return 0, 0, 0, false, false
	}
	name := world.BlockName(state)
	if name != "minecraft:chest" && name != "minecraft:trapped_chest" {
		return 0, 0, 0, false, false
	}
	props := world.BlockProperties(state)
	chestType := props["type"]
	if chestType != "left" && chestType != "right" {
		return 0, 0, 0, false, false
	}
	facing := props["facing"]
	var dx, dz int
	// In vanilla ChestBlock.getConnectedDirection:
	// "left" connects clockwise of facing; "right" connects counter-clockwise.
	switch facing {
	case "north":
		if chestType == "left" {
			dx = 1 // east
		} else {
			dx = -1 // west
		}
	case "south":
		if chestType == "left" {
			dx = -1 // west
		} else {
			dx = 1 // east
		}
	case "west":
		if chestType == "left" {
			dz = -1 // north
		} else {
			dz = 1 // south
		}
	case "east":
		if chestType == "left" {
			dz = 1 // south
		} else {
			dz = -1 // north
		}
	default:
		return 0, 0, 0, false, false
	}
	nx, ny, nz := x+dx, y, z+dz
	nState := cache.GetBlock(nx, ny, nz)
	if world.BlockName(nState) != name {
		return 0, 0, 0, false, false
	}
	nProps := world.BlockProperties(nState)
	if nProps["facing"] != facing || nProps["type"] == chestType || nProps["type"] == "single" {
		return 0, 0, 0, false, false
	}
	// In vanilla ChestBlock.DOUBLE_COMBINER, "right" is the first (top 27 slots) container.
	return nx, ny, nz, chestType == "right", true
}

func (s *Server) hopperInsertIntoChestOrBarrel(dim string, cache *world.Cache, x, y, z int, state uint16, single world.ItemStack) bool {
	if pX, pY, pZ, thisFirst, isDouble := doubleChestPartnerPos(cache, x, y, z, state); isDouble {
		type chestCoord struct{ x, y, z int }
		halves := [2]chestCoord{{x, y, z}, {pX, pY, pZ}}
		if !thisFirst {
			halves = [2]chestCoord{{pX, pY, pZ}, {x, y, z}}
		}
		maxStack := DispenserItemMaxStackSize(single)
		itemID := single.ResolvedItemID()
		if maxStack > 1 {
			for _, h := range halves {
				slots := s.ChestSlots(h.x, h.y, h.z)
				for i := 0; i < ChestSlotCount; i++ {
					cur := slots[i]
					if !cur.IsEmpty() && cur.ResolvedItemID() == itemID && cur.Count < maxStack {
						s.SetChestSlot(h.x, h.y, h.z, i, world.NewItemStack(itemID, cur.Count+1))
						s.updateHopperComparatorsAndBroadcast(dim, h.x, h.y, h.z)
						_ = s.syncChestViewers(nil, h.x, h.y, h.z)
						return true
					}
				}
			}
		}
		for _, h := range halves {
			slots := s.ChestSlots(h.x, h.y, h.z)
			for i := 0; i < ChestSlotCount; i++ {
				if slots[i].IsEmpty() {
					s.SetChestSlot(h.x, h.y, h.z, i, single)
					s.updateHopperComparatorsAndBroadcast(dim, h.x, h.y, h.z)
					_ = s.syncChestViewers(nil, h.x, h.y, h.z)
					return true
				}
			}
		}
		return false
	}

	if s.transferIntoChestAt(x, y, z, single) {
		s.updateHopperComparatorsAndBroadcast(dim, x, y, z)
		return true
	}
	return false
}

func (s *Server) hopperExtractFromChestOrBarrel(dim string, cache *world.Cache, x, y, z int, state uint16, canAccept func(world.ItemStack, ItemMeta, string) bool) (world.ItemStack, bool) {
	if pX, pY, pZ, thisFirst, isDouble := doubleChestPartnerPos(cache, x, y, z, state); isDouble {
		type chestCoord struct{ x, y, z int }
		halves := [2]chestCoord{{x, y, z}, {pX, pY, pZ}}
		if !thisFirst {
			halves = [2]chestCoord{{pX, pY, pZ}, {x, y, z}}
		}
		for _, h := range halves {
			slots := s.ChestSlots(h.x, h.y, h.z)
			for i := 0; i < ChestSlotCount; i++ {
				cur := slots[i]
				if cur.IsEmpty() || cur.Count <= 0 {
					continue
				}
				single := world.NewItemStack(cur.ResolvedItemID(), 1)
				if canAccept != nil && !canAccept(single, ItemMeta{}, "") {
					continue
				}
				if cur.Count <= 1 {
					s.SetChestSlot(h.x, h.y, h.z, i, world.ItemStack{})
				} else {
					s.SetChestSlot(h.x, h.y, h.z, i, world.NewItemStack(cur.ResolvedItemID(), cur.Count-1))
				}
				s.updateHopperComparatorsAndBroadcast(dim, h.x, h.y, h.z)
				_ = s.syncChestViewers(nil, h.x, h.y, h.z)
				return single, true
			}
		}
		return world.ItemStack{}, false
	}

	slots := s.ChestSlots(x, y, z)
	for i := 0; i < ChestSlotCount; i++ {
		cur := slots[i]
		if cur.IsEmpty() || cur.Count <= 0 {
			continue
		}
		single := world.NewItemStack(cur.ResolvedItemID(), 1)
		if canAccept != nil && !canAccept(single, ItemMeta{}, "") {
			continue
		}
		if cur.Count <= 1 {
			s.SetChestSlot(x, y, z, i, world.ItemStack{})
		} else {
			s.SetChestSlot(x, y, z, i, world.NewItemStack(cur.ResolvedItemID(), cur.Count-1))
		}
		s.updateHopperComparatorsAndBroadcast(dim, x, y, z)
		_ = s.syncChestViewers(nil, x, y, z)
		return single, true
	}
	return world.ItemStack{}, false
}

func (s *Server) hopperExtractFromShulkerBox(dim string, x, y, z int, canAccept func(world.ItemStack, ItemMeta, string) bool) (world.ItemStack, bool) {
	normDim := normalizeHopperDim(dim)
	slots := s.ShulkerBoxSlots(normDim, x, y, z)
	hasAny := false
	for i := 0; i < ChestSlotCount; i++ {
		if !slots[i].IsEmpty() && slots[i].Count > 0 {
			hasAny = true
			break
		}
	}
	if !hasAny && normDim == world.DimensionOverworld {
		slots = s.ChestSlots(x, y, z)
	}
	for i := 0; i < ChestSlotCount; i++ {
		cur := slots[i]
		if cur.IsEmpty() || cur.Count <= 0 {
			continue
		}
		single := world.NewItemStack(cur.ResolvedItemID(), 1)
		if canAccept != nil && !canAccept(single, ItemMeta{}, "") {
			continue
		}
		if cur.Count <= 1 {
			slots[i] = world.ItemStack{}
		} else {
			slots[i] = world.NewItemStack(cur.ResolvedItemID(), cur.Count-1)
		}
		s.SetShulkerBoxSlots(normDim, x, y, z, slots)
		if normDim == world.DimensionOverworld {
			s.SetChestSlot(x, y, z, i, slots[i])
		}
		return single, true
	}
	return world.ItemStack{}, false
}

// hopperInsertIntoFurnace inserts 1 item into a furnace, blast_furnace, or smoker
// following vanilla AbstractFurnaceBlockEntity WorldlyContainer rules:
//   - Hopper facing "down" (entering UP face of furnace) -> Slot 0 (input).
//   - Hopper facing horizontal (entering side of furnace) -> Slot 1 (fuel),
//     only if CanPlaceInFurnaceFuelSlot(single) is true (fuel items, or 1 empty bucket).
func (s *Server) hopperInsertIntoFurnace(dim string, x, y, z int, hopperFacing string, single world.ItemStack) bool {
	if s == nil || single.IsEmpty() {
		return false
	}
	slots := s.FurnaceSlots(x, y, z)
	targetSlot := world.FurnaceSlotFuel
	maxStack := DispenserItemMaxStackSize(single)
	if strings.EqualFold(hopperFacing, "down") {
		targetSlot = world.FurnaceSlotInput
	} else {
		if !CanPlaceInFurnaceFuelSlot(single) {
			return false
		}
		// Vanilla AbstractFurnaceBlockEntity.canPlaceItem(1, stack):
		// An empty bucket can only be placed into the fuel slot if the fuel slot
		// does not already contain a bucket (max 1 empty bucket).
		if canonicalResourceName(single.ResolvedID()) == "minecraft:bucket" {
			maxStack = 1
		}
	}
	cur := slots[targetSlot]
	if cur.IsEmpty() {
		if s.SetFurnaceSlot(x, y, z, targetSlot, single) {
			s.updateHopperComparatorsAndBroadcast(dim, x, y, z)
			_ = s.syncFurnaceViewers(nil, x, y, z)
			return true
		}
		return false
	}
	if cur.ResolvedItemID() == single.ResolvedItemID() && cur.Count < maxStack {
		if s.SetFurnaceSlot(x, y, z, targetSlot, world.NewItemStack(cur.ResolvedItemID(), cur.Count+1)) {
			s.updateHopperComparatorsAndBroadcast(dim, x, y, z)
			_ = s.syncFurnaceViewers(nil, x, y, z)
			return true
		}
	}
	return false
}

// hopperExtractFromFurnace extracts 1 item from the bottom (DOWN face) of a
// furnace, blast_furnace, or smoker at (x, y, z) following vanilla
// AbstractFurnaceBlockEntity.SLOTS_FOR_DOWN = {2, 1} and canTakeItemThroughFace:
//   - Slot 2 (output): any item can be extracted.
//   - Slot 1 (fuel): ONLY an empty bucket (`minecraft:bucket`, left over after
//     burning a lava_bucket) can be extracted from the bottom!
func (s *Server) hopperExtractFromFurnace(dim string, x, y, z int, canAccept func(world.ItemStack, ItemMeta, string) bool) (world.ItemStack, bool) {
	slots := s.FurnaceSlots(x, y, z)
	// 1. Check Slot 2 (FurnaceSlotOutput).
	out := slots[world.FurnaceSlotOutput]
	if !out.IsEmpty() && out.Count > 0 {
		single := world.NewItemStack(out.ResolvedItemID(), 1)
		if canAccept == nil || canAccept(single, ItemMeta{}, "") {
			if out.Count <= 1 {
				s.SetFurnaceSlot(x, y, z, world.FurnaceSlotOutput, world.ItemStack{})
			} else {
				s.SetFurnaceSlot(x, y, z, world.FurnaceSlotOutput, world.NewItemStack(out.ResolvedItemID(), out.Count-1))
			}
			s.updateHopperComparatorsAndBroadcast(dim, x, y, z)
			_ = s.syncFurnaceViewers(nil, x, y, z)
			return single, true
		}
	}
	// 2. Check Slot 1 (FurnaceSlotFuel) for an empty bucket ("minecraft:bucket").
	fuel := slots[world.FurnaceSlotFuel]
	if !fuel.IsEmpty() && fuel.Count > 0 && canonicalResourceName(fuel.ResolvedID()) == "minecraft:bucket" {
		single := world.NewItemStack(fuel.ResolvedItemID(), 1)
		if canAccept == nil || canAccept(single, ItemMeta{}, "") {
			if fuel.Count <= 1 {
				s.SetFurnaceSlot(x, y, z, world.FurnaceSlotFuel, world.ItemStack{})
			} else {
				s.SetFurnaceSlot(x, y, z, world.FurnaceSlotFuel, world.NewItemStack(fuel.ResolvedItemID(), fuel.Count-1))
			}
			s.updateHopperComparatorsAndBroadcast(dim, x, y, z)
			_ = s.syncFurnaceViewers(nil, x, y, z)
			return single, true
		}
	}
	return world.ItemStack{}, false
}

// TryHopperInsertIntoBlock attempts to push 1 item (`single`, `meta`, `potionType`)
// from a hopper facing `hopperFacing` ("down", "north", "south", "west", "east")
// into the target block (or vehicle container entity) at (dim, tx, ty, tz).
func (s *Server) TryHopperInsertIntoBlock(
	dim string,
	tx, ty, tz int,
	hopperFacing string,
	single world.ItemStack,
	meta ItemMeta,
	potionType string,
) bool {
	if s == nil || single.IsEmpty() || single.Count <= 0 {
		return false
	}
	normDim := normalizeHopperDim(dim)
	cache := s.BindNoteBlockAndJukeboxRedstone(normDim)
	if cache == nil {
		return false
	}
	if single.Count > 1 {
		single = world.NewItemStack(single.ResolvedItemID(), 1)
	}

	targetState := cache.GetBlock(tx, ty, tz)
	targetName := world.BlockName(targetState)

	switch {
	case targetName == "minecraft:hopper":
		return s.HopperInsertIntoHopper(normDim, tx, ty, tz, single, meta, potionType)

	case IsChestBlock(targetState) || targetName == "minecraft:trapped_chest" || targetName == "minecraft:barrel":
		return s.hopperInsertIntoChestOrBarrel(normDim, cache, tx, ty, tz, targetState, single)

	case IsFurnaceBlock(targetState):
		return s.hopperInsertIntoFurnace(normDim, tx, ty, tz, hopperFacing, single)

	case targetName == "minecraft:brewing_stand":
		entryFace := oppositeDropperDirection(hopperFacing)
		inserted, _ := s.HopperTryInsertIntoBrewingStand(normDim, tx, ty, tz, entryFace, single, potionType)
		return inserted

	case IsShulkerBoxItemOrBlock(targetName):
		if IsShulkerBoxItemOrBlock(single.ResolvedID()) {
			return false
		}
		if s.transferIntoShulkerBoxAt(normDim, tx, ty, tz, single) {
			s.updateHopperComparatorsAndBroadcast(normDim, tx, ty, tz)
			return true
		}
		return false

	case targetName == "minecraft:jukebox":
		if !IsMusicDiscItem(single) {
			return false
		}
		inserted, _, _ := s.HopperTryInsertIntoJukebox(normDim, tx, ty, tz, single)
		return inserted

	case targetName == "minecraft:composter":
		entryFace := oppositeDropperDirection(hopperFacing)
		return s.HopperInsertIntoComposter(normDim, tx, ty, tz, entryFace, single)

	case targetName == "minecraft:chiseled_bookshelf":
		return s.HopperInsertIntoChiseledBookshelf(normDim, tx, ty, tz, single, meta)

	case targetName == "minecraft:crafter":
		return s.HopperInsertIntoCrafter(normDim, tx, ty, tz, single, meta)

	case targetName == "minecraft:dispenser" || targetName == "minecraft:dropper":
		return s.HopperInsertIntoDispenser(normDim, tx, ty, tz, single, meta, potionType)
	}

	// If no block container is at (tx, ty, tz), check for a container vehicle
	// entity (chest_minecart, hopper_minecart, *_chest_boat) in that block box.
	return s.tryInsertIntoVehicleContainerAt(normDim, tx, ty, tz, single)
}

// TryHopperExtractFromBlockAbove attempts to pull 1 item from the container
// block (or container vehicle entity) at (dim, sx, sy, sz) into a hopper or
// hopper minecart below it, using `canAccept` to verify that the extracted item
// can fit into the destination slots before mutating the source container.
func (s *Server) TryHopperExtractFromBlockAbove(
	dim string,
	sx, sy, sz int,
	canAccept func(stack world.ItemStack, meta ItemMeta, potionType string) bool,
) (stack world.ItemStack, meta ItemMeta, potionType string, isContainerBlock bool, ok bool) {
	if s == nil {
		return world.ItemStack{}, ItemMeta{}, "", false, false
	}
	normDim := normalizeHopperDim(dim)
	cache := s.BindNoteBlockAndJukeboxRedstone(normDim)
	if cache == nil {
		return world.ItemStack{}, ItemMeta{}, "", false, false
	}
	srcState := cache.GetBlock(sx, sy, sz)
	srcName := world.BlockName(srcState)

	if IsHopperContainerBlock(srcState) {
		switch {
		case srcName == "minecraft:hopper":
			// Peek slots first without holding hs.mu across canAccept, so a hopper
			// pulling from another hopper above it never self-deadlocks.
			hs := s.hopperState()
			hs.mu.Lock()
			be := hs.hoppers[hopperBlockKey{dim: normDim, x: sx, y: sy, z: sz}]
			if be == nil {
				hs.mu.Unlock()
				return world.ItemStack{}, ItemMeta{}, "", true, false
			}
			snapSlots := be.Slots
			var snapMeta [HopperBlockSlotCount]ItemMeta
			for i := 0; i < HopperBlockSlotCount; i++ {
				snapMeta[i] = be.SlotMeta[i].Clone()
			}
			snapPotions := be.SlotPotions
			hs.mu.Unlock()

			for i := 0; i < HopperBlockSlotCount; i++ {
				cur := snapSlots[i]
				if cur.IsEmpty() || cur.Count <= 0 {
					continue
				}
				single := world.NewItemStack(cur.ResolvedItemID(), 1)
				if single.IsEmpty() {
					single = world.NewNamedItemStack(cur.ResolvedID(), 1)
				}
				m := snapMeta[i]
				p := snapPotions[i]
				if canAccept != nil && !canAccept(single, m, p) {
					continue
				}
				hs.mu.Lock()
				live := hs.hoppers[hopperBlockKey{dim: normDim, x: sx, y: sy, z: sz}]
				if live == nil || live.Slots[i].IsEmpty() || live.Slots[i].Count <= 0 {
					hs.mu.Unlock()
					continue
				}
				if live.Slots[i].Count <= 1 {
					live.Slots[i] = world.ItemStack{}
					live.SlotMeta[i] = ItemMeta{}
					live.SlotPotions[i] = ""
					live.SlotShulker[i] = nil
				} else {
					live.Slots[i] = world.NewItemStack(live.Slots[i].ResolvedItemID(), live.Slots[i].Count-1)
				}
				hs.mu.Unlock()
				s.updateHopperComparatorsAndBroadcast(normDim, sx, sy, sz)
				_ = s.SyncHopperViewers(nil, sx, sy, sz)
				return single, m, p, true, true
			}
			return world.ItemStack{}, ItemMeta{}, "", true, false

		case IsChestBlock(srcState) || srcName == "minecraft:trapped_chest" || srcName == "minecraft:barrel":
			st, extracted := s.hopperExtractFromChestOrBarrel(normDim, cache, sx, sy, sz, srcState, canAccept)
			return st, ItemMeta{}, "", true, extracted

		case IsFurnaceBlock(srcState):
			st, extracted := s.hopperExtractFromFurnace(normDim, sx, sy, sz, canAccept)
			return st, ItemMeta{}, "", true, extracted

		case srcName == "minecraft:brewing_stand":
			if stand, exists := s.BrewingStandAtInDim(normDim, sx, sy, sz); exists {
				canAny := false
				for _, slotIdx := range []int{0, 1, 2, BrewingStandSlotIngredient} {
					cur := stand.Items[slotIdx]
					if cur.IsEmpty() {
						continue
					}
					if slotIdx == BrewingStandSlotIngredient && (IsBrewingIngredient(cur) || IsBrewingFuel(cur)) {
						continue
					}
					pType := ""
					if slotIdx < 3 {
						pType = stand.PotionTypes[slotIdx]
					}
					single := world.NewItemStack(cur.ResolvedItemID(), 1)
					if canAccept == nil || canAccept(single, ItemMeta{}, pType) {
						canAny = true
						break
					}
				}
				if !canAny {
					return world.ItemStack{}, ItemMeta{}, "", true, false
				}
			}
			st, pot, extracted := s.HopperTryExtractFromBrewingStand(normDim, sx, sy, sz)
			return st, ItemMeta{}, pot, true, extracted

		case IsShulkerBoxItemOrBlock(srcName):
			st, extracted := s.hopperExtractFromShulkerBox(normDim, sx, sy, sz, canAccept)
			return st, ItemMeta{}, "", true, extracted

		case srcName == "minecraft:jukebox":
			if jb, exists := s.GetJukeboxAt(normDim, sx, sy, sz); exists && jb.HasRecord() {
				if canAccept != nil && !canAccept(jb.RecordItem, ItemMeta{}, "") {
					return world.ItemStack{}, ItemMeta{}, "", true, false
				}
			}
			st, extracted, _ := s.HopperTryExtractFromJukebox(normDim, sx, sy, sz)
			return st, ItemMeta{}, "", true, extracted

		case srcName == "minecraft:composter":
			bm := world.NewNamedItemStack("minecraft:bone_meal", 1)
			if canAccept != nil && !canAccept(bm, ItemMeta{}, "") {
				return world.ItemStack{}, ItemMeta{}, "", true, false
			}
			st, extracted := s.HopperExtractFromComposter(normDim, sx, sy, sz)
			return st, ItemMeta{}, "", true, extracted

		case srcName == "minecraft:chiseled_bookshelf":
			if cb, exists := s.GetChiseledBookshelfAt(normDim, sx, sy, sz); exists {
				canAny := false
				for i := 0; i < ChiseledBookshelfSlotCount; i++ {
					if !cb.Slots[i].IsEmpty() {
						if canAccept == nil || canAccept(cb.Slots[i], cb.SlotMeta[i], "") {
							canAny = true
							break
						}
					}
				}
				if !canAny {
					return world.ItemStack{}, ItemMeta{}, "", true, false
				}
			}
			st, m, extracted := s.HopperExtractFromChiseledBookshelf(normDim, sx, sy, sz)
			return st, m, "", true, extracted

		case srcName == "minecraft:crafter":
			if cr, exists := s.GetCrafterAt(normDim, sx, sy, sz); exists {
				canAny := false
				for i := 0; i < CrafterSlotCount; i++ {
					if !cr.Slots[i].IsEmpty() {
						single := world.NewItemStack(cr.Slots[i].ResolvedItemID(), 1)
						if canAccept == nil || canAccept(single, cr.SlotMeta[i], "") {
							canAny = true
							break
						}
					}
				}
				if !canAny {
					return world.ItemStack{}, ItemMeta{}, "", true, false
				}
			}
			st, m, extracted := s.HopperExtractFromCrafter(normDim, sx, sy, sz)
			return st, m, "", true, extracted

		case srcName == "minecraft:dispenser" || srcName == "minecraft:dropper":
			ds := s.dispenserState()
			ds.mu.Lock()
			be := ds.blocks[dispenserBlockKey{dim: normDim, x: sx, y: sy, z: sz}]
			if be == nil {
				ds.mu.Unlock()
				return world.ItemStack{}, ItemMeta{}, "", true, false
			}
			for i := 0; i < DispenserSlotCount; i++ {
				cur := be.Slots[i]
				if cur.IsEmpty() || cur.Count <= 0 {
					continue
				}
				single := world.NewItemStack(cur.ResolvedItemID(), 1)
				if single.IsEmpty() {
					single = world.NewNamedItemStack(cur.ResolvedID(), 1)
				}
				m := be.SlotMeta[i].Clone()
				p := be.SlotPotions[i]
				if canAccept != nil && !canAccept(single, m, p) {
					continue
				}
				decrementDispenserSlotLocked(be, i)
				ds.mu.Unlock()
				s.updateDispenserComparatorsAndBroadcast(normDim, sx, sy, sz)
				_ = s.SyncDispenserViewers(nil, sx, sy, sz)
				return single, m, p, true, true
			}
			ds.mu.Unlock()
			return world.ItemStack{}, ItemMeta{}, "", true, false
		}
	}

	// No container block at (sx, sy, sz); check for a container vehicle entity
	// (chest_minecart, hopper_minecart, *_chest_boat) at (sx, sy, sz).
	if st, extracted := s.tryExtractFromVehicleContainerAt(normDim, sx, sy, sz, canAccept); extracted {
		return st, ItemMeta{}, "", false, true
	}
	return world.ItemStack{}, ItemMeta{}, "", false, false
}

// --- Hopper Tick & Item Entity Suction ---

func canFitIntoHopperSlots(
	slots *[HopperBlockSlotCount]world.ItemStack,
	metas *[HopperBlockSlotCount]ItemMeta,
	potions *[HopperBlockSlotCount]string,
	stack world.ItemStack,
	meta ItemMeta,
	potionType string,
) bool {
	if stack.IsEmpty() || stack.Count <= 0 {
		return false
	}
	maxStack := DispenserItemMaxStackSize(stack)
	itemID := stack.ResolvedItemID()
	for i := 0; i < HopperBlockSlotCount; i++ {
		cur := slots[i]
		if cur.IsEmpty() || cur.Count <= 0 {
			return true
		}
		if maxStack > 1 && cur.ResolvedItemID() == itemID && cur.Count < maxStack {
			if (metas == nil || metas[i].IsEmpty()) && meta.IsEmpty() &&
				(potions == nil || potions[i] == "") && potionType == "" {
				return true
			}
		}
	}
	return false
}

func insertOneIntoHopperSlots(
	slots *[HopperBlockSlotCount]world.ItemStack,
	metas *[HopperBlockSlotCount]ItemMeta,
	potions *[HopperBlockSlotCount]string,
	stack world.ItemStack,
	meta ItemMeta,
	potionType string,
) bool {
	if stack.IsEmpty() || stack.Count <= 0 {
		return false
	}
	maxStack := DispenserItemMaxStackSize(stack)
	itemID := stack.ResolvedItemID()
	single := world.NewItemStack(itemID, 1)
	if single.IsEmpty() {
		single = world.NewNamedItemStack(stack.ResolvedID(), 1)
	}

	if maxStack > 1 && meta.IsEmpty() && potionType == "" {
		for i := 0; i < HopperBlockSlotCount; i++ {
			cur := slots[i]
			if !cur.IsEmpty() && cur.ResolvedItemID() == itemID && cur.Count < maxStack {
				if (metas == nil || metas[i].IsEmpty()) && (potions == nil || potions[i] == "") {
					slots[i] = world.NewItemStack(itemID, cur.Count+1)
					return true
				}
			}
		}
	}
	for i := 0; i < HopperBlockSlotCount; i++ {
		if slots[i].IsEmpty() || slots[i].Count <= 0 {
			slots[i] = single
			if metas != nil {
				metas[i] = meta.Clone()
			}
			if potions != nil {
				potions[i] = potionType
			}
			return true
		}
	}
	return false
}

// mergeStackIntoHopperSlots merges as many items from stack as will fit into
// the 5 hopper slots (used when sucking up a dropped ItemEntity from the world),
// returning the number of items absorbed.
func mergeStackIntoHopperSlots(
	slots *[HopperBlockSlotCount]world.ItemStack,
	metas *[HopperBlockSlotCount]ItemMeta,
	potions *[HopperBlockSlotCount]string,
	stack world.ItemStack,
) int32 {
	if stack.IsEmpty() || stack.Count <= 0 {
		return 0
	}
	maxStack := DispenserItemMaxStackSize(stack)
	itemID := stack.ResolvedItemID()
	remaining := stack.Count

	if maxStack > 1 {
		for i := 0; i < HopperBlockSlotCount && remaining > 0; i++ {
			cur := slots[i]
			if cur.IsEmpty() || cur.ResolvedItemID() != itemID || cur.Count >= maxStack {
				continue
			}
			if (metas != nil && !metas[i].IsEmpty()) || (potions != nil && potions[i] != "") {
				continue
			}
			space := maxStack - cur.Count
			take := remaining
			if take > space {
				take = space
			}
			slots[i] = world.NewItemStack(itemID, cur.Count+take)
			remaining -= take
		}
	}
	for i := 0; i < HopperBlockSlotCount && remaining > 0; i++ {
		if !slots[i].IsEmpty() && slots[i].Count > 0 {
			continue
		}
		take := remaining
		if take > maxStack {
			take = maxStack
		}
		slots[i] = world.NewItemStack(itemID, take)
		if metas != nil {
			metas[i] = ItemMeta{}
		}
		if potions != nil {
			potions[i] = ""
		}
		remaining -= take
	}
	return stack.Count - remaining
}

// SuckDroppedItemsAboveHopper collects dropped item entities in the hopper
// collection region above (dim, x, y, z) into the hopper at (dim, x, y, z),
// matching vanilla HopperBlockEntity.suckInItems.
func (s *Server) SuckDroppedItemsAboveHopper(dim string, x, y, z int) bool {
	if s == nil || s.entities == nil {
		return false
	}
	normDim := normalizeHopperDim(dim)
	cx, cz := int32(x>>4), int32(z>>4)
	nearby := s.entities.AppendInChunkRange(nil, cx, cz, 1)
	if len(nearby) == 0 {
		return false
	}

	minX, maxX := float64(x), float64(x+1)
	minY, maxY := float64(y)+0.5, float64(y)+2.05
	minZ, maxZ := float64(z), float64(z+1)

	collectedAny := false
	for i := range nearby {
		cand := &nearby[i]
		if !cand.IsItem() || cand.IsExperienceOrb() || cand.Item.IsEmpty() || cand.EffectiveDimension() != normDim {
			continue
		}
		if cand.X < minX || cand.X > maxX || cand.Y < minY || cand.Y > maxY || cand.Z < minZ || cand.Z > maxZ {
			continue
		}
		live := s.entities.Get(cand.ID)
		if live == nil || live.Item.IsEmpty() || live.Item.Count <= 0 {
			continue
		}

		hs := s.hopperState()
		hs.mu.Lock()
		be := s.getOrCreateHopperLocked(hs, normDim, x, y, z)
		wasEmpty := be.IsEmpty()
		added := mergeStackIntoHopperSlots(&be.Slots, &be.SlotMeta, &be.SlotPotions, live.Item)
		if added > 0 && wasEmpty && be.TransferCooldown <= 0 {
			be.TransferCooldown = HopperTransferCooldownTicks
		}
		hs.mu.Unlock()

		if added > 0 {
			collectedAny = true
			if added >= live.Item.Count {
				s.entities.Remove(live.ID)
				cp := live.ChunkPos()
				s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayRemoveEntities,
					encodeRemoveEntitiesPacket(nil, []int32{live.ID}))
			} else {
				rem := world.NewItemStack(live.Item.ResolvedItemID(), live.Item.Count-added)
				s.entities.Update(live.ID, func(e *world.Entity) {
					e.Item = rem
				})
			}
		}
	}

	if collectedAny {
		s.updateHopperComparatorsAndBroadcast(normDim, x, y, z)
		_ = s.SyncHopperViewers(nil, x, y, z)
	}
	return collectedAny
}

// TickHopperAt advances the `minecraft:hopper` at (dim, x, y, z) by 1 game tick
// (50ms), matching vanilla 26.1.2 HopperBlockEntity.pushItemsTick / tryMoveItems:
//   - Decrements TransferCooldown by 1.
//   - If TransferCooldown <= 0 and the hopper is enabled (not redstone-locked):
//     1. Attempts to push 1 item out to the container in its `facing` direction.
//     2. If not full, attempts to pull 1 item from a container at (x, y+1, z),
//        or if no container block is above, sucks in dropped item entities above.
//     3. If either push or pull succeeded, sets TransferCooldown = 8.
func (s *Server) TickHopperAt(dim string, x, y, z int) bool {
	return s.tickHopperAtInternal(dim, x, y, z, true)
}

func (s *Server) tickHopperAtInternal(dim string, x, y, z int, decrementCooldown bool) bool {
	if s == nil {
		return false
	}
	normDim := normalizeHopperDim(dim)
	cache := s.BindNoteBlockAndJukeboxRedstone(normDim)
	if cache == nil {
		return false
	}
	state := cache.GetBlock(x, y, z)
	if !IsHopperBlock(state) {
		return false
	}

	// Synchronize redstone enabled state.
	enabled := s.UpdateHopperRedstone(normDim, x, y, z)

	hs := s.hopperState()
	hs.mu.Lock()
	be := s.getOrCreateHopperLocked(hs, normDim, x, y, z)
	if decrementCooldown && be.TransferCooldown > 0 {
		be.TransferCooldown--
	}
	onCooldown := be.TransferCooldown > 0
	isEmpty := be.IsEmpty()
	isFull := be.IsFull()
	hs.mu.Unlock()

	if onCooldown || !enabled {
		return false
	}

	moved := false

	// 1. Push (ejectItems): push 1 item into the container at (x+dx, y+dy, z+dz).
	if !isEmpty {
		facing := HopperFacing(state)
		dx, dy, dz := DispenserFacingVector(facing)
		tx, ty, tz := x+dx, y+dy, z+dz

		hs.mu.Lock()
		be = s.getOrCreateHopperLocked(hs, normDim, x, y, z)
		slotsCopy := be.Slots
		metasCopy := be.SlotMeta
		potionsCopy := be.SlotPotions
		hs.mu.Unlock()

		for i := 0; i < HopperBlockSlotCount; i++ {
			cur := slotsCopy[i]
			if cur.IsEmpty() || cur.Count <= 0 {
				continue
			}
			single := world.NewItemStack(cur.ResolvedItemID(), 1)
			if single.IsEmpty() {
				single = world.NewNamedItemStack(cur.ResolvedID(), 1)
			}
			if s.TryHopperInsertIntoBlock(normDim, tx, ty, tz, facing, single, metasCopy[i], potionsCopy[i]) {
				hs.mu.Lock()
				be = s.getOrCreateHopperLocked(hs, normDim, x, y, z)
				if be.Slots[i].Count <= 1 {
					be.Slots[i] = world.ItemStack{}
					be.SlotMeta[i] = ItemMeta{}
					be.SlotPotions[i] = ""
					be.SlotShulker[i] = nil
				} else {
					be.Slots[i] = world.NewItemStack(be.Slots[i].ResolvedItemID(), be.Slots[i].Count-1)
				}
				isFull = be.IsFull()
				hs.mu.Unlock()
				s.updateHopperComparatorsAndBroadcast(normDim, x, y, z)
				_ = s.SyncHopperViewers(nil, x, y, z)
				moved = true
				break
			}
		}
	}

	// 2. Pull (suckInItems): if not full, pull 1 item from container at (x, y+1, z)
	//    or suck in dropped item entities above.
	if !isFull {
		canAccept := func(st world.ItemStack, m ItemMeta, p string) bool {
			hs.mu.Lock()
			defer hs.mu.Unlock()
			be := s.getOrCreateHopperLocked(hs, normDim, x, y, z)
			return canFitIntoHopperSlots(&be.Slots, &be.SlotMeta, &be.SlotPotions, st, m, p)
		}
		pulled, pulledMeta, pulledPotion, hadContainerBlock, ok := s.TryHopperExtractFromBlockAbove(normDim, x, y+1, z, canAccept)
		if ok {
			hs.mu.Lock()
			be := s.getOrCreateHopperLocked(hs, normDim, x, y, z)
			insertOneIntoHopperSlots(&be.Slots, &be.SlotMeta, &be.SlotPotions, pulled, pulledMeta, pulledPotion)
			hs.mu.Unlock()
			s.updateHopperComparatorsAndBroadcast(normDim, x, y, z)
			_ = s.SyncHopperViewers(nil, x, y, z)
			moved = true
		} else if !hadContainerBlock {
			if s.SuckDroppedItemsAboveHopper(normDim, x, y, z) {
				moved = true
			}
		}
	}

	if moved {
		hs.mu.Lock()
		be := s.getOrCreateHopperLocked(hs, normDim, x, y, z)
		be.TransferCooldown = HopperTransferCooldownTicks
		hs.mu.Unlock()
	}
	return moved
}

// TickHoppers advances all tracked hoppers (and pending composter maturations)
// by 1 server tick (20 TPS).
func (s *Server) TickHoppers() int {
	if s == nil {
		return 0
	}
	hs := s.hopperState()

	// 1. Advance any pending composter level 7 -> 8 maturations and decrement
	//    hopper transfer cooldowns before any hopper transfers in this tick.
	var readyComposters []hopperBlockKey
	hs.mu.Lock()
	for k, rem := range hs.composterPending {
		if rem <= 1 {
			delete(hs.composterPending, k)
			readyComposters = append(readyComposters, k)
		} else {
			hs.composterPending[k] = rem - 1
		}
	}
	keys := make([]hopperBlockKey, 0, len(hs.hoppers))
	for k, be := range hs.hoppers {
		if be != nil && be.TransferCooldown > 0 {
			be.TransferCooldown--
		}
		keys = append(keys, k)
	}
	hs.mu.Unlock()

	for _, ck := range readyComposters {
		if cache := s.BindNoteBlockAndJukeboxRedstone(ck.dim); cache != nil {
			st := cache.GetBlock(ck.x, ck.y, ck.z)
			if world.BlockName(st) == "minecraft:composter" && world.BlockProperties(st)["level"] == "7" {
				if nextSt, ok := world.WithBlockProperty(st, "level", "8"); ok {
					s.setDispenserWorldBlock(ck.dim, cache, ck.x, ck.y, ck.z, nextSt)
				}
			}
		}
	}

	// Sort hopper keys deterministically (top-to-bottom Y, then X, then Z).
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].dim != keys[j].dim {
			return keys[i].dim < keys[j].dim
		}
		if keys[i].y != keys[j].y {
			return keys[i].y > keys[j].y
		}
		if keys[i].x != keys[j].x {
			return keys[i].x < keys[j].x
		}
		return keys[i].z < keys[j].z
	})

	movedCount := 0
	for _, k := range keys {
		if s.tickHopperAtInternal(k.dim, k.x, k.y, k.z, false) {
			movedCount++
		}
	}
	return movedCount
}

// --- Hopper Minecart & Vehicle Container Transfer Logic (Hook for B) ---

type hopperVehicleInventoryAccess interface {
	VehicleContainerSlotCount(entityID int32) int
	VehicleContainerSlot(entityID int32, slot int) world.ItemStack
	SetVehicleContainerSlot(entityID int32, slot int, stack world.ItemStack) bool
}

type hopperMinecartEnabledChecker interface {
	IsHopperMinecartEnabled(entityID int32) bool
}

func (s *Server) getVehicleInventorySlotCount(entityID int32) int {
	if s == nil || entityID <= 0 {
		return 0
	}
	if cnt := s.VehicleContainerSlotCount(entityID); cnt > 0 {
		return cnt
	}
	if s.ensureVehicleState(entityID) != nil {
		if cnt := s.VehicleContainerSlotCount(entityID); cnt > 0 {
			return cnt
		}
	}
	hs := s.hopperState()
	hs.mu.Lock()
	_, hasFallback := hs.minecartFallback[entityID]
	hs.mu.Unlock()
	if hasFallback {
		return HopperBlockSlotCount
	}
	return 0
}

func (s *Server) getVehicleInventorySlot(entityID int32, slot int) world.ItemStack {
	if s == nil || entityID <= 0 {
		return world.ItemStack{}
	}
	if s.VehicleContainerSlotCount(entityID) > 0 || s.ensureVehicleState(entityID) != nil {
		if s.VehicleContainerSlotCount(entityID) > 0 {
			return s.VehicleContainerSlot(entityID, slot)
		}
	}
	if slot < 0 || slot >= HopperBlockSlotCount {
		return world.ItemStack{}
	}
	hs := s.hopperState()
	hs.mu.Lock()
	defer hs.mu.Unlock()
	if arr := hs.minecartFallback[entityID]; arr != nil {
		return arr[slot]
	}
	return world.ItemStack{}
}

func (s *Server) setVehicleInventorySlot(entityID int32, slot int, stack world.ItemStack) bool {
	if s == nil || entityID <= 0 {
		return false
	}
	if s.SetVehicleContainerSlot(entityID, slot, stack) {
		return true
	}
	if slot < 0 || slot >= HopperBlockSlotCount {
		return false
	}
	hs := s.hopperState()
	hs.mu.Lock()
	arr := hs.minecartFallback[entityID]
	if arr == nil {
		arr = &[HopperBlockSlotCount]world.ItemStack{}
		hs.minecartFallback[entityID] = arr
	}
	if stack.IsEmpty() || stack.Count <= 0 {
		arr[slot] = world.ItemStack{}
	} else {
		arr[slot] = world.NewItemStack(stack.ResolvedItemID(), stack.Count)
	}
	hs.mu.Unlock()
	return true
}

func (s *Server) isHopperMinecartEntityEnabled(entityID int32) bool {
	if s == nil || entityID <= 0 {
		return false
	}
	if _, ok := s.VehicleStateByID(entityID); ok {
		return s.IsHopperMinecartEnabled(entityID)
	}
	if s.ensureVehicleState(entityID) != nil {
		return s.IsHopperMinecartEnabled(entityID)
	}
	hs := s.hopperState()
	hs.mu.Lock()
	enabled, exists := hs.minecartEnabled[entityID]
	hs.mu.Unlock()
	if exists {
		return enabled
	}
	return true
}

// HasMinecartOnRail reports whether any minecart entity is currently on the rail
// at (dimension, x, y, z) (or at y+1 above a sloped rail).
func (s *Server) HasMinecartOnRail(dimension string, x, y, z int) bool {
	if s == nil {
		return false
	}
	return s.hasMinecartOnRailAt(dimension, x, y, z)
}

// DetectorRailComparatorSignal returns the redstone comparator signal (0..15)
// for a `minecraft:detector_rail` at (dimension, x, y, z):
//   - 0 if the block is not a powered detector_rail or has no minecart on it.
//   - Container fullness signal (0..15) for chest_minecart / hopper_minecart.
//   - 15 for a rideable minecart with a passenger.
func (s *Server) DetectorRailComparatorSignal(dimension string, x, y, z int) int {
	if s == nil {
		return 0
	}
	normDim := normalizeHopperDim(dimension)
	if cache := s.DimensionChunks(normDim); cache != nil {
		st := cache.GetBlock(x, y, z)
		if st != world.StateAir && world.BlockName(st) != "minecraft:detector_rail" {
			return 0
		}
	}
	return s.DetectorRailComparatorOutput(normDim, x, y, z)
}

// NudgeMinecartsOnPoweredRail launches or boosts any minecart resting on a
// newly powered `minecraft:powered_rail` at (dimension, x, y, z) away from an
// adjacent solid block along the rail's axis.
func (s *Server) NudgeMinecartsOnPoweredRail(dimension string, x, y, z int) {
	if s == nil || s.entities == nil {
		return
	}
	normDim := normalizeHopperDim(dimension)
	cache := s.DimensionChunks(normDim)
	if cache == nil {
		return
	}
	railState := cache.GetBlock(x, y, z)
	if world.BlockName(railState) != "minecraft:powered_rail" || !railPoweredProperty(railState) {
		return
	}
	shape := railShapeProperty(railState)

	ids := s.FindVehiclesInBox(normDim, float64(x)-0.49, float64(y)-0.49, float64(z)-0.49, float64(x+1)+0.49, float64(y+2)+0.49, float64(z+1)+0.49)
	for _, id := range ids {
		ent := s.entities.Get(id)
		if ent == nil || !IsMinecartEntity(ent.TypeName) {
			continue
		}
		vx := float64(ent.VelocityX) / 8000.0
		vz := float64(ent.VelocityZ) / 8000.0
		speed := math.Hypot(vx, vz)
		if speed > 0.01 {
			vx += (vx / speed) * MinecartPoweredRailBoost
			vz += (vz / speed) * MinecartPoweredRailBoost
		} else {
			switch shape {
			case "east_west", "ascending_east", "ascending_west":
				if !isPassableBlock(cache.GetBlock(x-1, y, z)) {
					vx = MinecartPoweredRailLaunch
				} else if !isPassableBlock(cache.GetBlock(x+1, y, z)) {
					vx = -MinecartPoweredRailLaunch
				}
			case "north_south", "ascending_north", "ascending_south":
				if !isPassableBlock(cache.GetBlock(x, y, z-1)) {
					vz = MinecartPoweredRailLaunch
				} else if !isPassableBlock(cache.GetBlock(x, y, z+1)) {
					vz = -MinecartPoweredRailLaunch
				}
			}
		}
		if vx != 0 || vz != 0 {
			s.entities.Update(id, func(e *world.Entity) {
				e.VelocityX = clampVelocityShort(vx)
				e.VelocityZ = clampVelocityShort(vz)
			})
		}
	}
}

// OnActivatorRailPowerChange evaluates activator rail effects (dismounting
// passengers on rideable minecarts, priming TNT minecarts, or toggling
// hopper_minecart enabled state) for all minecarts on the activator rail at
// (dimension, x, y, z) when its redstone powered state changes.
func (s *Server) OnActivatorRailPowerChange(dimension string, x, y, z int, powered bool) {
	if s == nil || s.entities == nil {
		return
	}
	normDim := normalizeHopperDim(dimension)
	ids := s.FindVehiclesInBox(normDim, float64(x)-0.49, float64(y)-0.49, float64(z)-0.49, float64(x+1)+0.49, float64(y+2)+0.49, float64(z+1)+0.49)
	for _, id := range ids {
		ent := s.entities.Get(id)
		if ent == nil || !IsMinecartEntity(ent.TypeName) {
			continue
		}
		if canonicalResourceName(ent.TypeName) == EntityTypeHopperMinecart {
			s.SetHopperMinecartEnabled(id, !powered)
		}
		s.evaluateMinecartRailTriggers(id)
	}
}

// FindVehiclesInBox returns the entity IDs of all vehicle entities (minecarts
// and boats/rafts) in dimension whose position lies within
// [minX, maxX] x [minY, maxY] x [minZ, maxZ].
func (s *Server) FindVehiclesInBox(dimension string, minX, minY, minZ, maxX, maxY, maxZ float64) []int32 {
	if s == nil || s.entities == nil {
		return nil
	}
	normDim := normalizeHopperDim(dimension)
	minCX := int32(int(math.Floor(minX)) >> 4)
	maxCX := int32(int(math.Floor(maxX)) >> 4)
	minCZ := int32(int(math.Floor(minZ)) >> 4)
	maxCZ := int32(int(math.Floor(maxZ)) >> 4)
	centerCX := (minCX + maxCX) / 2
	centerCZ := (minCZ + maxCZ) / 2
	rad := maxCX - minCX + 1
	if rz := maxCZ - minCZ + 1; rz > rad {
		rad = rz
	}
	if rad < 1 {
		rad = 1
	}

	nearby := s.entities.AppendInChunkRange(nil, centerCX, centerCZ, rad)
	if len(nearby) == 0 {
		return nil
	}
	var ids []int32
	for i := range nearby {
		ent := &nearby[i]
		if ent.EffectiveDimension() != normDim {
			continue
		}
		if !IsVehicleEntity(ent.TypeName) && s.getVehicleInventorySlotCount(ent.ID) <= 0 {
			continue
		}
		if ent.X < minX || ent.X > maxX || ent.Y < minY || ent.Y > maxY || ent.Z < minZ || ent.Z > maxZ {
			continue
		}
		ids = append(ids, ent.ID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// HopperInsertIntoVehicle inserts items from stack into the container vehicle
// identified by vehicleID (chest_minecart, hopper_minecart, or *_chest_boat),
// returning the uninserted remainder stack and whether any item was inserted.
func (s *Server) HopperInsertIntoVehicle(vehicleID int32, stack world.ItemStack) (remaining world.ItemStack, changed bool) {
	if s == nil || vehicleID <= 0 || stack.IsEmpty() || stack.Count <= 0 {
		return stack, false
	}
	slotCount := s.getVehicleInventorySlotCount(vehicleID)
	if slotCount <= 0 {
		return stack, false
	}
	maxStack := DispenserItemMaxStackSize(stack)
	itemID := stack.ResolvedItemID()
	remCount := stack.Count

	// Pass 1: stack onto existing matching slots.
	if maxStack > 1 {
		for i := 0; i < slotCount && remCount > 0; i++ {
			cur := s.getVehicleInventorySlot(vehicleID, i)
			if cur.IsEmpty() || cur.ResolvedItemID() != itemID || cur.Count >= maxStack {
				continue
			}
			space := maxStack - cur.Count
			take := remCount
			if take > space {
				take = space
			}
			if s.setVehicleInventorySlot(vehicleID, i, world.NewItemStack(itemID, cur.Count+take)) {
				remCount -= take
				changed = true
			}
		}
	}

	// Pass 2: place into empty slots.
	for i := 0; i < slotCount && remCount > 0; i++ {
		cur := s.getVehicleInventorySlot(vehicleID, i)
		if !cur.IsEmpty() && cur.Count > 0 {
			continue
		}
		take := remCount
		if take > maxStack {
			take = maxStack
		}
		placed := world.NewItemStack(itemID, take)
		if placed.IsEmpty() {
			placed = world.NewNamedItemStack(stack.ResolvedID(), take)
		}
		if s.setVehicleInventorySlot(vehicleID, i, placed) {
			remCount -= take
			changed = true
		}
	}

	if changed {
		_ = s.syncVehicleContainerViewers(nil, vehicleID)
	}
	if remCount <= 0 {
		return world.ItemStack{}, changed
	}
	out := world.NewItemStack(itemID, remCount)
	if out.IsEmpty() {
		out = world.NewNamedItemStack(stack.ResolvedID(), remCount)
	}
	return out, changed
}

// HopperExtractFromVehicle extracts 1 item from the first non-empty slot of
// the container vehicle identified by vehicleID.
func (s *Server) HopperExtractFromVehicle(vehicleID int32) (extracted world.ItemStack, ok bool) {
	if s == nil || vehicleID <= 0 {
		return world.ItemStack{}, false
	}
	slotCount := s.getVehicleInventorySlotCount(vehicleID)
	for i := 0; i < slotCount; i++ {
		cur := s.getVehicleInventorySlot(vehicleID, i)
		if cur.IsEmpty() || cur.Count <= 0 {
			continue
		}
		single := world.NewItemStack(cur.ResolvedItemID(), 1)
		if single.IsEmpty() {
			single = world.NewNamedItemStack(cur.ResolvedID(), 1)
		}
		if cur.Count <= 1 {
			s.setVehicleInventorySlot(vehicleID, i, world.ItemStack{})
		} else {
			s.setVehicleInventorySlot(vehicleID, i, world.NewItemStack(cur.ResolvedItemID(), cur.Count-1))
		}
		_ = s.syncVehicleContainerViewers(nil, vehicleID)
		return single, true
	}
	return world.ItemStack{}, false
}

// ActiveHopperMinecarts returns the entity IDs of all active `minecraft:hopper_minecart`
// entities tracked by the server.
func (s *Server) ActiveHopperMinecarts() []int32 {
	if s == nil {
		return nil
	}
	seen := make(map[int32]bool)
	var ids []int32
	mgr := s.vehicleManager()
	mgr.mu.Lock()
	for id, st := range mgr.vehicles {
		if st != nil && canonicalResourceName(st.TypeName) == EntityTypeHopperMinecart {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	mgr.mu.Unlock()

	hs := s.hopperState()
	hs.mu.Lock()
	for id := range hs.minecartFallback {
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	hs.mu.Unlock()

	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func (s *Server) findVehicleContainersInBlock(dim string, bx, by, bz int) []int32 {
	candidates := s.FindVehiclesInBox(dim,
		float64(bx)-0.49, float64(by)-0.49, float64(bz)-0.49,
		float64(bx+1)+0.49, float64(by+1)+0.49, float64(bz+1)+0.49,
	)
	var ids []int32
	for _, id := range candidates {
		if s.getVehicleInventorySlotCount(id) > 0 {
			ids = append(ids, id)
		}
	}
	return ids
}

func (s *Server) tryInsertIntoVehicleContainerAt(dim string, bx, by, bz int, single world.ItemStack) bool {
	ids := s.findVehicleContainersInBlock(dim, bx, by, bz)
	for _, vehID := range ids {
		if _, changed := s.HopperInsertIntoVehicle(vehID, single); changed {
			return true
		}
	}
	return false
}

func (s *Server) tryExtractFromVehicleContainerAt(dim string, bx, by, bz int, canAccept func(world.ItemStack, ItemMeta, string) bool) (world.ItemStack, bool) {
	ids := s.findVehicleContainersInBlock(dim, bx, by, bz)
	for _, vehID := range ids {
		if canAccept == nil {
			if st, ok := s.HopperExtractFromVehicle(vehID); ok {
				return st, true
			}
			continue
		}
		slotCount := s.getVehicleInventorySlotCount(vehID)
		for i := 0; i < slotCount; i++ {
			cur := s.getVehicleInventorySlot(vehID, i)
			if cur.IsEmpty() || cur.Count <= 0 {
				continue
			}
			single := world.NewItemStack(cur.ResolvedItemID(), 1)
			if !canAccept(single, ItemMeta{}, "") {
				continue
			}
			if cur.Count <= 1 {
				s.setVehicleInventorySlot(vehID, i, world.ItemStack{})
			} else {
				s.setVehicleInventorySlot(vehID, i, world.NewItemStack(cur.ResolvedItemID(), cur.Count-1))
			}
			_ = s.syncVehicleContainerViewers(nil, vehID)
			return single, true
		}
	}
	return world.ItemStack{}, false
}

// HopperMinecartTransfer is the exported hook function for Agent B's
// `minecraft:hopper_minecart` tick (matching `HopperMinecartTransferHook`:
// `func(s *Server, entityID int32, dim string, x, y, z float64) bool`).
//
// When invoked for an enabled hopper minecart at (dim, x, y, z):
//  1. Pulls 1 item from any container block (or container entity) at (bx, by+1, bz)
//     above the minecart into the minecart's 5 hopper slots.
//  2. If no container block is at (bx, by+1, bz), sucks in dropped item entities
//     within the hopper minecart's collection box into its 5 slots.
//  3. If an enabled `minecraft:hopper` block is directly below at (bx, by-1, bz)
//     and not on cooldown, transfers 1 item from the hopper minecart into that
//     hopper block and sets the hopper block's TransferCooldown to 8.
func HopperMinecartTransfer(s *Server, entityID int32, dim string, x, y, z float64) bool {
	if s == nil || entityID <= 0 {
		return false
	}
	return s.TickHopperMinecartTransfer(entityID, dim, x, y, z)
}

// TickHopperMinecartTransfer executes one tick of item transfer for the
// `minecraft:hopper_minecart` identified by entityID at (dim, x, y, z).
func (s *Server) TickHopperMinecartTransfer(entityID int32, dim string, x, y, z float64) bool {
	if s == nil || entityID <= 0 || !s.isHopperMinecartEntityEnabled(entityID) {
		return false
	}
	normDim := normalizeHopperDim(dim)
	var slots [HopperBlockSlotCount]world.ItemStack
	for i := 0; i < HopperBlockSlotCount; i++ {
		slots[i] = s.getVehicleInventorySlot(entityID, i)
	}

	transferred := s.TransferHopperMinecartSlots(normDim, x, y, z, true, &slots)
	if transferred {
		for i := 0; i < HopperBlockSlotCount; i++ {
			s.setVehicleInventorySlot(entityID, i, slots[i])
		}
		_ = s.syncVehicleContainerViewers(nil, entityID)
	}
	return transferred
}

// TransferHopperMinecartSlots performs hopper minecart item pulling (from
// containers or dropped item entities above) and pushing into a hopper block
// directly below, mutating `slots` (the 5 slots of the hopper minecart) in place.
func (s *Server) TransferHopperMinecartSlots(
	dim string,
	x, y, z float64,
	enabled bool,
	slots *[HopperBlockSlotCount]world.ItemStack,
) bool {
	if s == nil || slots == nil || !enabled {
		return false
	}
	normDim := normalizeHopperDim(dim)
	bx := int(math.Floor(x))
	by := int(math.Floor(y))
	bz := int(math.Floor(z))

	transferred := false

	// 1. Pull from container at (bx, by+1, bz) if the minecart has space.
	isFull := true
	for i := 0; i < HopperBlockSlotCount; i++ {
		if slots[i].IsEmpty() || slots[i].Count < DispenserItemMaxStackSize(slots[i]) {
			isFull = false
			break
		}
	}
	if !isFull {
		canAccept := func(st world.ItemStack, m ItemMeta, p string) bool {
			return canFitIntoHopperSlots(slots, nil, nil, st, m, p)
		}
		pulled, _, _, hadContainerBlock, ok := s.TryHopperExtractFromBlockAbove(normDim, bx, by+1, bz, canAccept)
		if ok {
			if insertOneIntoHopperSlots(slots, nil, nil, pulled, ItemMeta{}, "") {
				transferred = true
			}
		} else if !hadContainerBlock && s.entities != nil {
			// Suck dropped item entities in [x-0.85..x+0.85, y-0.5..y+1.75, z-0.85..z+0.85].
			cx, cz := int32(bx>>4), int32(bz>>4)
			nearby := s.entities.AppendInChunkRange(nil, cx, cz, 1)
			for i := range nearby {
				cand := &nearby[i]
				if !cand.IsItem() || cand.IsExperienceOrb() || cand.Item.IsEmpty() || cand.EffectiveDimension() != normDim {
					continue
				}
				if math.Abs(cand.X-x) <= 0.85 && cand.Y >= y-0.5 && cand.Y <= y+1.75 && math.Abs(cand.Z-z) <= 0.85 {
					live := s.entities.Get(cand.ID)
					if live == nil || live.Item.IsEmpty() || live.Item.Count <= 0 {
						continue
					}
					added := mergeStackIntoHopperSlots(slots, nil, nil, live.Item)
					if added > 0 {
						transferred = true
						if added >= live.Item.Count {
							s.entities.Remove(live.ID)
							cp := live.ChunkPos()
							s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayRemoveEntities,
								encodeRemoveEntitiesPacket(nil, []int32{live.ID}))
						} else {
							rem := world.NewItemStack(live.Item.ResolvedItemID(), live.Item.Count-added)
							s.entities.Update(live.ID, func(e *world.Entity) {
								e.Item = rem
							})
						}
					}
				}
			}
		}
	}

	// 2. If there is an enabled block hopper directly below at (bx, by-1, bz)
	//    with TransferCooldown <= 0, transfer 1 item into it and set its cooldown to 8.
	if cache := s.DimensionChunks(normDim); cache != nil && by > world.MinY {
		belowState := cache.GetBlock(bx, by-1, bz)
		if IsHopperBlock(belowState) && s.IsHopperBlockEnabled(normDim, bx, by-1, bz) &&
			s.HopperTransferCooldown(normDim, bx, by-1, bz) <= 0 {
			for i := 0; i < HopperBlockSlotCount; i++ {
				cur := slots[i]
				if cur.IsEmpty() || cur.Count <= 0 {
					continue
				}
				single := world.NewItemStack(cur.ResolvedItemID(), 1)
				if single.IsEmpty() {
					single = world.NewNamedItemStack(cur.ResolvedID(), 1)
				}
				if s.HopperInsertIntoHopper(normDim, bx, by-1, bz, single, ItemMeta{}, "") {
					if cur.Count <= 1 {
						slots[i] = world.ItemStack{}
					} else {
						slots[i] = world.NewItemStack(cur.ResolvedItemID(), cur.Count-1)
					}
					s.SetHopperTransferCooldown(normDim, bx, by-1, bz, HopperTransferCooldownTicks)
					transferred = true
					break
				}
			}
		}
	}

	return transferred
}

// --- Hopper Container UI (`MenuTypeHopper` = 16) Hooks for B ---

// OpenHopper opens the 5-slot `minecraft:hopper` container screen at (x, y, z)
// for session, sends `open_screen` + `container_set_content`, awards
// `minecraft:inspect_hopper`, and emits `GameEventContainerOpen`.
func (s *Server) OpenHopper(session *PlayerSession, x, y, z int) (int32, error) {
	if s == nil || session == nil {
		return 0, errors.New("server: nil session")
	}
	_ = s.CloseContainer(session, 0, false)

	dim := normalizeHopperDim(session.Dimension())
	be := s.GetOrCreateHopper(dim, x, y, z)
	title := "Item Hopper"
	if be != nil && be.CustomName != "" {
		title = be.CustomName
	}

	s.AwardCustomStat(session, StatInspectHopper, 1)
	s.EmitPlayerBlockVibration(session, GameEventContainerOpen, dim, x, y, z, "minecraft:hopper")

	tr := getContainerTracker(session)
	tr.mu.Lock()
	tr.nextWindow = (tr.nextWindow % 100) + 1
	cid := tr.nextWindow
	tr.open = &OpenContainerState{
		ContainerID: cid,
		MenuType:    HopperBlockMenuType,
		BlockX:      x,
		BlockY:      y,
		BlockZ:      z,
	}
	carried := tr.carried
	tr.mu.Unlock()

	if session.send != nil {
		if err := session.send(protocol.PlayOpenScreen, EncodeOpenScreen(cid, HopperBlockMenuType, title)); err != nil {
			return cid, err
		}
		slots := s.BuildHopperWindowSlots(session, x, y, z)
		stateID := session.NextContainerStateID()
		if err := session.send(protocol.PlayContainerSetContent, EncodeContainerContent(cid, stateID, slots, carried)); err != nil {
			return cid, err
		}
	}
	return cid, nil
}

// BuildHopperWindowSlots constructs the 41-slot window slice for an open block
// hopper at (x, y, z): slots 0..4 are the hopper's 5 slots, 5..31 are main
// inventory (9..35), and 32..40 are hotbar (36..44).
func (s *Server) BuildHopperWindowSlots(session *PlayerSession, x, y, z int) []world.ItemStack {
	dim := world.DimensionOverworld
	if session != nil && session.Dimension() != "" {
		dim = normalizeHopperDim(session.Dimension())
	}
	hSlots := s.HopperSlotsInDimension(dim, x, y, z)
	slots := make([]world.ItemStack, HopperBlockMenuSlotCount)
	copy(slots[0:HopperBlockSlotCount], hSlots[:])
	if session != nil {
		inv := session.Inventory()
		copy(slots[5:32], inv[9:36])
		copy(slots[32:41], inv[36:45])
	}
	return slots
}

// HandleHopperClick processes a container_click packet on an open block hopper
// (`MenuTypeHopper`), updating the hopper's 5 slots and the player's inventory.
func (s *Server) HandleHopperClick(session *PlayerSession, open *OpenContainerState, slot int, button int8, mode int32) error {
	if s == nil || session == nil || open == nil {
		return nil
	}
	if slot < 0 || slot >= HopperBlockMenuSlotCount {
		return nil
	}
	dim := normalizeHopperDim(session.Dimension())
	bx, by, bz := open.BlockX, open.BlockY, open.BlockZ

	tr := getContainerTracker(session)
	tr.mu.Lock()
	slots := s.BuildHopperWindowSlots(session, bx, by, bz)

	switch mode {
	case ContainerInputPickup:
		slots[slot], tr.carried = applyPickupSlot(slots[slot], tr.carried, button)
	case ContainerInputQuickMove:
		if slot < HopperBlockSlotCount {
			slots[slot] = mergeIntoSlotSlice(slots, slots[slot], 32, 41)
			slots[slot] = mergeIntoSlotSlice(slots, slots[slot], 5, 32)
		} else {
			slots[slot] = mergeIntoSlotSlice(slots, slots[slot], 0, HopperBlockSlotCount)
		}
	case ContainerInputSwap:
		if button >= 0 && button < 9 {
			hotbarIdx := 32 + int(button)
			slots[slot], slots[hotbarIdx] = slots[hotbarIdx], slots[slot]
		}
	case ContainerInputClone:
		if session.Snapshot().GameMode == GameModeCreative && tr.carried.IsEmpty() && !slots[slot].IsEmpty() {
			tr.carried = world.NewItemStack(slots[slot].ResolvedItemID(), MaxItemStackSize)
		}
	case ContainerInputThrow:
		if !slots[slot].IsEmpty() {
			dropCount := int32(1)
			if button == 1 {
				dropCount = slots[slot].Count
			}
			dropStack := world.NewItemStack(slots[slot].ResolvedItemID(), dropCount)
			rem := slots[slot].Count - dropCount
			if rem <= 0 {
				slots[slot] = world.ItemStack{}
			} else {
				slots[slot] = world.NewItemStack(slots[slot].ResolvedItemID(), rem)
			}
			snap := session.Snapshot()
			s.SpawnItemEntityInDimension(dim, snap.X, snap.Y+0.5, snap.Z, dropStack)
			s.RecordItemDropped(session, dropStack.ResolvedID(), dropStack.Count)
		}
	}

	for i := 0; i < HopperBlockSlotCount; i++ {
		s.SetHopperSlotInDimension(dim, bx, by, bz, i, slots[i])
	}
	for i := 0; i < 27; i++ {
		session.SetInventorySlot(9+i, slots[5+i])
	}
	for i := 0; i < 9; i++ {
		session.SetInventorySlot(36+i, slots[32+i])
	}
	tr.mu.Unlock()

	s.TriggerInventoryChanged(session)
	s.UnlockRecipesForInventory(session)
	return s.SyncHopperViewers(session, bx, by, bz)
}

// SyncHopperViewers synchronizes the open hopper container screen for all
// players currently viewing the block hopper at (bx, by, bz).
func (s *Server) SyncHopperViewers(actor *PlayerSession, bx, by, bz int) error {
	if s == nil {
		return nil
	}
	s.playersMu.RLock()
	sessions := make([]*PlayerSession, 0, len(s.players))
	for _, sess := range s.players {
		sessions = append(sessions, sess)
	}
	s.playersMu.RUnlock()

	var firstErr error
	for _, sess := range sessions {
		if sess == nil || sess.send == nil {
			continue
		}
		open := sess.OpenContainer()
		if open != nil && open.MenuType == HopperBlockMenuType &&
			open.BlockX == bx && open.BlockY == by && open.BlockZ == bz {
			slots := s.BuildHopperWindowSlots(sess, bx, by, bz)
			stateID := sess.NextContainerStateID()
			if err := sess.send(protocol.PlayContainerSetContent, EncodeContainerContent(open.ContainerID, stateID, slots, sess.CarriedItem())); err != nil && firstErr == nil {
				firstErr = err
			}
		} else if sess == actor {
			if err := s.syncOpenContainer(sess); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// --- Hopper Persistence (`<WorldDir>/hoppers.nbt` & Chunk Block Entities NBT) ---

func (s *Server) hoppersFilePath() string {
	if s == nil || s.cfg.WorldDir == "" {
		return ""
	}
	return filepath.Join(s.cfg.WorldDir, "hoppers.nbt")
}

// EncodeHopperBlockEntityNBT serializes a HopperBlockEntity into a vanilla
// block_entities NBT compound (`id: "minecraft:hopper"`, `Items`, `TransferCooldown`).
func EncodeHopperBlockEntityNBT(be *HopperBlockEntity) *nbt.Compound {
	c := nbt.NewCompound()
	if be == nil {
		return c
	}
	c.Set("id", nbt.String("minecraft:hopper"))
	c.Set("Dimension", nbt.String(normalizeHopperDim(be.Dimension)))
	c.Set("x", nbt.Int(int32(be.X)))
	c.Set("y", nbt.Int(int32(be.Y)))
	c.Set("z", nbt.Int(int32(be.Z)))
	c.Set("TransferCooldown", nbt.Int(int32(be.TransferCooldown)))
	if be.CustomName != "" {
		c.Set("CustomName", nbt.String(be.CustomName))
	}

	var items []nbt.Tag
	for slot := 0; slot < HopperBlockSlotCount; slot++ {
		st := be.Slots[slot]
		if st.IsEmpty() || st.Count <= 0 {
			continue
		}
		it := nbt.NewCompound()
		it.Set("Slot", nbt.Byte(int8(slot)))
		it.Set("id", nbt.String(st.ResolvedID()))
		it.Set("count", nbt.Int(st.Count))
		if be.SlotPotions[slot] != "" {
			it.Set("Potion", nbt.String(be.SlotPotions[slot]))
		}
		if be.SlotMeta[slot].Damage > 0 {
			it.Set("Damage", nbt.Int(be.SlotMeta[slot].Damage))
		}
		if be.SlotMeta[slot].CustomName != "" {
			it.Set("CustomName", nbt.String(be.SlotMeta[slot].CustomName))
		}
		items = append(items, it)
	}
	c.Set("Items", nbt.List{ElemID: nbt.TagCompound, Elems: items})
	return c
}

// DecodeHopperBlockEntityNBT deserializes a HopperBlockEntity from an NBT compound.
func DecodeHopperBlockEntityNBT(c *nbt.Compound, fallbackDim string) (*HopperBlockEntity, bool) {
	if c == nil {
		return nil, false
	}
	idStr, _ := nbtGetString(c, "id")
	if idStr != "" && canonicalResourceName(idStr) != "minecraft:hopper" {
		return nil, false
	}
	dim, _ := nbtGetString(c, "Dimension")
	if dim == "" {
		dim = fallbackDim
	}
	normDim := normalizeHopperDim(dim)
	x, _ := nbtGetInt(c, "x")
	y, _ := nbtGetInt(c, "y")
	z, _ := nbtGetInt(c, "z")
	cd, _ := nbtGetInt(c, "TransferCooldown")
	customName, _ := nbtGetString(c, "CustomName")

	be := &HopperBlockEntity{
		Dimension:        normDim,
		X:                int(x),
		Y:                int(y),
		Z:                int(z),
		TransferCooldown: int(cd),
		CustomName:       customName,
	}
	if itemElems, ok := nbtGetListElems(c, "Items"); ok {
		for _, itElem := range itemElems {
			itComp, ok := itElem.(*nbt.Compound)
			if !ok || itComp == nil {
				continue
			}
			slotByte, _ := nbtGetByte(itComp, "Slot")
			slotIdx := int(slotByte)
			if slotIdx < 0 || slotIdx >= HopperBlockSlotCount {
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
			_ = DefaultRecipeBook()
			be.Slots[slotIdx] = world.NewNamedItemStack(itemID, count)
			if pot, ok := nbtGetString(itComp, "Potion"); ok {
				be.SlotPotions[slotIdx] = pot
			}
			var meta ItemMeta
			if dmg, ok := nbtGetInt(itComp, "Damage"); ok {
				meta.Damage = dmg
			}
			if cname, ok := nbtGetString(itComp, "CustomName"); ok {
				meta.CustomName = cname
			}
			be.SlotMeta[slotIdx] = meta
		}
	}
	return be, true
}

// SaveHoppersToNBT serializes all tracked hoppers into an NBT root compound.
func (s *Server) SaveHoppersToNBT() *nbt.Compound {
	root := nbt.NewCompound()
	if s == nil {
		return root
	}
	hs := s.hopperState()
	hs.mu.Lock()
	keys := make([]hopperBlockKey, 0, len(hs.hoppers))
	for k, be := range hs.hoppers {
		if be != nil && (!be.IsEmpty() || be.TransferCooldown > 0 || be.CustomName != "") {
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].dim != keys[j].dim {
			return keys[i].dim < keys[j].dim
		}
		if keys[i].x != keys[j].x {
			return keys[i].x < keys[j].x
		}
		if keys[i].y != keys[j].y {
			return keys[i].y < keys[j].y
		}
		return keys[i].z < keys[j].z
	})
	elems := make([]nbt.Tag, 0, len(keys))
	for _, k := range keys {
		elems = append(elems, EncodeHopperBlockEntityNBT(hs.hoppers[k]))
	}
	hs.mu.Unlock()
	root.Set("Hoppers", nbt.List{ElemID: nbt.TagCompound, Elems: elems})
	return root
}

// LoadHoppersFromNBT restores hoppers from an NBT compound produced by SaveHoppersToNBT.
func (s *Server) LoadHoppersFromNBT(root *nbt.Compound) int {
	if s == nil || root == nil {
		return 0
	}
	listElems, ok := nbtGetListElems(root, "Hoppers")
	if !ok {
		return 0
	}
	hs := s.hopperState()
	hs.mu.Lock()
	loaded := 0
	var dims []string
	for _, elem := range listElems {
		c, ok := elem.(*nbt.Compound)
		if !ok || c == nil {
			continue
		}
		be, ok := DecodeHopperBlockEntityNBT(c, world.DimensionOverworld)
		if !ok || be == nil {
			continue
		}
		hs.hoppers[hopperBlockKey{dim: be.Dimension, x: be.X, y: be.Y, z: be.Z}] = be
		dims = append(dims, be.Dimension)
		loaded++
	}
	hs.mu.Unlock()
	for _, d := range dims {
		s.BindNoteBlockAndJukeboxRedstone(d)
	}
	return loaded
}

// SaveHoppers persists all non-empty hoppers to `<WorldDir>/hoppers.nbt`.
func (s *Server) SaveHoppers() error {
	if s == nil {
		return nil
	}
	path := s.hoppersFilePath()
	if path == "" {
		return nil
	}
	root := s.SaveHoppersToNBT()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, nbt.MarshalNamed("", root), 0o644)
}

// LoadHoppers loads all persisted hoppers from `<WorldDir>/hoppers.nbt`.
func (s *Server) LoadHoppers() error {
	if s == nil {
		return nil
	}
	path := s.hoppersFilePath()
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	_, parsedTag, err := nbt.UnmarshalNamed(data)
	if err != nil {
		return err
	}
	if root, ok := parsedTag.(*nbt.Compound); ok {
		s.LoadHoppersFromNBT(root)
	}
	return nil
}

// ChunkHopperBlockEntitiesNBT returns vanilla `block_entities` NBT compounds
// for all hoppers in chunk (cx, cz) of dim.
func (s *Server) ChunkHopperBlockEntitiesNBT(dim string, cx, cz int32) []*nbt.Compound {
	if s == nil {
		return nil
	}
	normDim := normalizeHopperDim(dim)
	hs := s.hopperState()
	hs.mu.Lock()
	defer hs.mu.Unlock()
	var out []*nbt.Compound
	for k, be := range hs.hoppers {
		if k.dim != normDim || be == nil {
			continue
		}
		if int32(k.x>>4) == cx && int32(k.z>>4) == cz {
			out = append(out, EncodeHopperBlockEntityNBT(be))
		}
	}
	return out
}

// LoadChunkHopperBlockEntitiesNBT loads hopper block entities from a chunk's
// `block_entities` NBT list.
func (s *Server) LoadChunkHopperBlockEntitiesNBT(dim string, list []*nbt.Compound) int {
	if s == nil || len(list) == 0 {
		return 0
	}
	normDim := normalizeHopperDim(dim)
	hs := s.hopperState()
	hs.mu.Lock()
	loaded := 0
	for _, c := range list {
		be, ok := DecodeHopperBlockEntityNBT(c, normDim)
		if !ok || be == nil {
			continue
		}
		hs.hoppers[hopperBlockKey{dim: be.Dimension, x: be.X, y: be.Y, z: be.Z}] = be
		loaded++
	}
	hs.mu.Unlock()
	if loaded > 0 {
		s.BindNoteBlockAndJukeboxRedstone(normDim)
	}
	return loaded
}
