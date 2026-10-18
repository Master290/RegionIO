package server

import (
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"regionio/internal/nbt"
	"regionio/internal/protocol"
	"regionio/internal/world"
)

// --- Chiseled Bookshelf Block State Helpers ---

// ChiseledBookshelfBlockState returns the block state ID for
// `minecraft:chiseled_bookshelf` with the given horizontal facing ("north",
// "south", "west", "east") and 6 slot occupancy flags.
func ChiseledBookshelfBlockState(facing string, occupied [ChiseledBookshelfSlotCount]bool) uint16 {
	f := strings.ToLower(strings.TrimSpace(facing))
	switch f {
	case "north", "south", "west", "east":
	default:
		f = "north"
	}
	props := map[string]string{
		"facing": f,
	}
	for i := 0; i < ChiseledBookshelfSlotCount; i++ {
		val := "false"
		if occupied[i] {
			val = "true"
		}
		props["slot_"+strconv.Itoa(i)+"_occupied"] = val
	}
	if st, ok := world.BlockStateIDByProperties("minecraft:chiseled_bookshelf", props); ok {
		return st
	}
	return 0
}

// ChiseledBookshelfFacing returns the horizontal `facing` property ("north",
// "south", "west", or "east") of a `minecraft:chiseled_bookshelf` block state.
func ChiseledBookshelfFacing(state uint16) string {
	f := strings.ToLower(world.BlockProperties(state)["facing"])
	switch f {
	case "north", "south", "west", "east":
		return f
	default:
		return "north"
	}
}

// ChiseledBookshelfOccupiedMask returns the 6 boolean occupancy flags
// (`slot_0_occupied` .. `slot_5_occupied`) from a `minecraft:chiseled_bookshelf`
// block state.
func ChiseledBookshelfOccupiedMask(state uint16) [ChiseledBookshelfSlotCount]bool {
	var mask [ChiseledBookshelfSlotCount]bool
	props := world.BlockProperties(state)
	for i := 0; i < ChiseledBookshelfSlotCount; i++ {
		mask[i] = props["slot_"+strconv.Itoa(i)+"_occupied"] == "true"
	}
	return mask
}

// ChiseledBookshelfOccupiedCount returns the number of occupied book slots
// (0..6) on a `minecraft:chiseled_bookshelf` block state.
func ChiseledBookshelfOccupiedCount(state uint16) int {
	mask := ChiseledBookshelfOccupiedMask(state)
	cnt := 0
	for _, occ := range mask {
		if occ {
			cnt++
		}
	}
	return cnt
}

// --- Hit-Coordinate Slot Selection (0..5) ---

func normalizeBlockFaceCoord(v float64) float64 {
	if v < 0.0 || v > 1.0 {
		f := v - math.Floor(v)
		if f == 0.0 && v > 0.0 {
			return 1.0
		}
		return f
	}
	return v
}

// ChiseledBookshelfHitSlot resolves the book slot index (0..5) clicked on a
// `minecraft:chiseled_bookshelf` facing `bookshelfFacing`, given the clicked
// block face `clickedFace` and hit coordinates `(hitX, hitY, hitZ)` (either
// fractional `[0..1]` within the block or world coordinates):
//   - Top row (`localY >= 0.5`):    slots 0 (left), 1 (middle), 2 (right)
//   - Bottom row (`localY < 0.5`): slots 3 (left), 4 (middle), 5 (right)
//
// Following vanilla ChiseledBookshelfBlock.getHitSlot, only clicks on the front
// `facing` side of the bookshelf are valid (`ok == true`).
func ChiseledBookshelfHitSlot(bookshelfFacing, clickedFace string, hitX, hitY, hitZ float64) (slot int, ok bool) {
	front := strings.ToLower(strings.TrimSpace(bookshelfFacing))
	switch front {
	case "north", "south", "west", "east":
	default:
		front = "north"
	}
	if cf := strings.ToLower(strings.TrimSpace(clickedFace)); cf != "" && cf != front {
		return -1, false
	}

	lx := normalizeBlockFaceCoord(hitX)
	ly := normalizeBlockFaceCoord(hitY)
	lz := normalizeBlockFaceCoord(hitZ)

	var relX float64
	switch front {
	case "north":
		relX = 1.0 - lx
	case "south":
		relX = lx
	case "west":
		relX = lz
	case "east":
		relX = 1.0 - lz
	}

	col := 2
	if relX < 0.375 { // 6/16
		col = 0
	} else if relX < 0.6875 { // 11/16
		col = 1
	}

	row := 1
	if ly >= 0.5 {
		row = 0
	}
	return row*3 + col, true
}

// --- Player Interaction (Insert / Remove Book by Hit Slot) ---

// InteractChiseledBookshelf handles a player right-clicking a
// `minecraft:chiseled_bookshelf` at (dim, x, y, z) on `clickedFace` at
// `(hitX, hitY, hitZ)`:
//   - If the targeted slot (0..5) is occupied, removes the book from that slot,
//     gives it to the player (or drops it in the world), updates
//     `slot_<N>_occupied=false` and `LastInteractedSlot = N`, plays the pickup
//     sound, emits a `block_change` vibration, and updates comparators.
//   - If the targeted slot (0..5) is empty and the player holds a valid book
//     (`#minecraft:bookshelf_books`), inserts 1 book into that slot, updates
//     `slot_<N>_occupied=true` and `LastInteractedSlot = N`, plays the insert
//     sound, emits a `block_change` vibration, and updates comparators.
func (s *Server) InteractChiseledBookshelf(
	session *PlayerSession,
	dim string,
	x, y, z int,
	clickedFace string,
	hitX, hitY, hitZ float64,
) (handled bool, res world.BlockUpdateResult) {
	if s == nil {
		return false, res
	}
	normDim := normalizeHopperDim(dim)
	if session != nil && dim == "" {
		if d := session.Dimension(); d != "" {
			normDim = normalizeHopperDim(d)
		}
	}
	cache := s.BindNoteBlockAndJukeboxRedstone(normDim)
	if cache == nil {
		return false, res
	}
	st := cache.GetBlock(x, y, z)
	if !IsChiseledBookshelfBlock(st) {
		return false, res
	}

	facing := ChiseledBookshelfFacing(st)
	slot, ok := ChiseledBookshelfHitSlot(facing, clickedFace, hitX, hitY, hitZ)
	if !ok || slot < 0 || slot >= ChiseledBookshelfSlotCount {
		return false, res
	}

	hs := s.hopperState()
	hs.mu.Lock()
	cb := s.getOrCreateChiseledBookshelfLocked(hs, normDim, cache, x, y, z)
	curBook := cb.Slots[slot]
	curMeta := cb.SlotMeta[slot].Clone()
	hs.mu.Unlock()

	cx, cz := int32(x>>4), int32(z>>4)

	// Case 1: Target slot is occupied -> remove book from slot.
	if !curBook.IsEmpty() && curBook.Count > 0 {
		s.SetChiseledBookshelfSlot(normDim, x, y, z, slot, world.ItemStack{}, ItemMeta{})
		if session != nil {
			s.giveChiseledBookshelfBookToPlayer(session, normDim, x, y, z, curBook, curMeta)
		} else {
			s.SpawnItemEntityInDimension(normDim, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, curBook)
		}
		soundName := "minecraft:block.chiseled_bookshelf.pickup"
		if canonicalResourceName(curBook.ResolvedID()) == "minecraft:enchanted_book" {
			soundName = "minecraft:block.chiseled_bookshelf.pickup.enchanted"
		}
		s.BroadcastDimensionChunk(normDim, cx, cz, PlaySoundPacketID,
			EncodeSoundPacket(soundName, SoundSourceBlocks, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, 1.0, 1.0, 0))
		s.EmitVibration(VibrationEvent{
			Dimension: normDim,
			Type:      GameEventBlockChange,
			X:         float64(x) + 0.5,
			Y:         float64(y) + 0.5,
			Z:         float64(z) + 0.5,
			Player:    session,
		})
		return true, res
	}

	// Case 2: Target slot is empty -> insert 1 held book if valid.
	if session == nil {
		return false, res
	}
	held := session.HeldItem()
	if !IsChiseledBookshelfBook(held) {
		return false, res
	}
	heldMeta := session.HeldItemMeta()
	singleBook := world.NewNamedItemStack(held.ResolvedID(), 1)
	if !s.SetChiseledBookshelfSlot(normDim, x, y, z, slot, singleBook, heldMeta) {
		return false, res
	}

	s.ConsumeHeldItem(session, 1)

	soundName := "minecraft:block.chiseled_bookshelf.insert"
	if canonicalResourceName(singleBook.ResolvedID()) == "minecraft:enchanted_book" {
		soundName = "minecraft:block.chiseled_bookshelf.insert.enchanted"
	}
	s.BroadcastDimensionChunk(normDim, cx, cz, PlaySoundPacketID,
		EncodeSoundPacket(soundName, SoundSourceBlocks, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, 1.0, 1.0, 0))
	s.EmitVibration(VibrationEvent{
		Dimension: normDim,
		Type:      GameEventBlockChange,
		X:         float64(x) + 0.5,
		Y:         float64(y) + 0.5,
		Z:         float64(z) + 0.5,
		Player:    session,
	})
	return true, res
}

func (s *Server) giveChiseledBookshelfBookToPlayer(session *PlayerSession, dim string, bx, by, bz int, book world.ItemStack, meta ItemMeta) {
	if session == nil || book.IsEmpty() {
		return
	}
	heldSlot := int(world.HotbarContainerOffset + session.Snapshot().SelectedSlot)
	if session.InventorySlot(heldSlot).IsEmpty() {
		session.SetInventorySlot(heldSlot, book)
		if !meta.IsEmpty() {
			SetSlotItemMeta(session, heldSlot, meta)
		}
		if session.send != nil {
			stateID := session.NextContainerStateID()
			_ = session.send(protocol.PlayContainerSetSlot, EncodeContainerSlot(0, stateID, heldSlot, book))
		}
		return
	}
	inv := session.Inventory()
	for _, r := range [][2]int{{36, 45}, {9, 36}} {
		for i := r[0]; i < r[1]; i++ {
			if inv[i].IsEmpty() {
				session.SetInventorySlot(i, book)
				if !meta.IsEmpty() {
					SetSlotItemMeta(session, i, meta)
				}
				if session.send != nil {
					stateID := session.NextContainerStateID()
					_ = session.send(protocol.PlayContainerSetSlot, EncodeContainerSlot(0, stateID, i, book))
				}
				return
			}
		}
	}
	s.SpawnItemEntityInDimension(dim, float64(bx)+0.5, float64(by)+0.5, float64(bz)+0.5, book)
}

// --- Breaking & NBT Persistence ---

// RemoveChiseledBookshelfAt removes the `ChiseledBookshelfBlockEntity` at
// (dim, x, y, z) when the block is broken and returns all stored books (each of
// which drops in vanilla even without Silk Touch).
func (s *Server) RemoveChiseledBookshelfAt(dim string, x, y, z int) (drops []world.ItemStack, metas []ItemMeta, existed bool) {
	if s == nil {
		return nil, nil, false
	}
	normDim := normalizeHopperDim(dim)
	key := hopperBlockKey{dim: normDim, x: x, y: y, z: z}
	hs := s.hopperState()
	hs.mu.Lock()
	cb, ok := hs.bookshelves[key]
	if ok {
		delete(hs.bookshelves, key)
	}
	hs.mu.Unlock()
	if !ok || cb == nil {
		return nil, nil, false
	}
	for i := 0; i < ChiseledBookshelfSlotCount; i++ {
		if !cb.Slots[i].IsEmpty() && cb.Slots[i].Count > 0 {
			drops = append(drops, cb.Slots[i])
			metas = append(metas, cb.SlotMeta[i].Clone())
		}
	}
	s.updateHopperComparatorsAndBroadcast(normDim, x, y, z)
	return drops, metas, true
}

// ToNBT serializes the ChiseledBookshelfBlockEntity into a vanilla-compatible NBT compound.
func (cb *ChiseledBookshelfBlockEntity) ToNBT() *nbt.Compound {
	if cb == nil {
		return nbt.NewCompound()
	}
	comp := nbt.NewCompound()
	comp.Set("id", nbt.String("minecraft:chiseled_bookshelf"))
	comp.Set("Dimension", nbt.String(normalizeHopperDim(cb.Dimension)))
	comp.Set("x", nbt.Int(int32(cb.X)))
	comp.Set("y", nbt.Int(int32(cb.Y)))
	comp.Set("z", nbt.Int(int32(cb.Z)))
	comp.Set("last_interacted_slot", nbt.Int(int32(cb.LastInteractedSlot)))

	var itemEntries []nbt.Tag
	for i := 0; i < ChiseledBookshelfSlotCount; i++ {
		st := cb.Slots[i]
		if st.IsEmpty() || st.Count <= 0 {
			continue
		}
		entry := nbt.NewCompound()
		entry.Set("Slot", nbt.Byte(int8(i)))
		entry.Set("id", nbt.String(st.ResolvedID()))
		entry.Set("count", nbt.Int(st.Count))
		if !cb.SlotMeta[i].IsEmpty() {
			metaComp := nbt.NewCompound()
			encodeItemMetaToNBT(metaComp, cb.SlotMeta[i])
			entry.Set("Meta", metaComp)
		}
		itemEntries = append(itemEntries, entry)
	}
	comp.Set("Items", nbt.List{ElemID: nbt.TagCompound, Elems: itemEntries})
	return comp
}

// ChiseledBookshelfBlockEntityFromNBT deserializes a ChiseledBookshelfBlockEntity from an NBT compound.
func ChiseledBookshelfBlockEntityFromNBT(comp *nbt.Compound) (*ChiseledBookshelfBlockEntity, bool) {
	if comp == nil {
		return nil, false
	}
	if idStr, hasID := nbtGetString(comp, "id"); hasID && idStr != "" && canonicalResourceName(idStr) != "minecraft:chiseled_bookshelf" {
		return nil, false
	}
	dim, _ := nbtGetString(comp, "Dimension")
	x, okX := nbtGetInt(comp, "x")
	y, okY := nbtGetInt(comp, "y")
	z, okZ := nbtGetInt(comp, "z")
	if !okX || !okY || !okZ {
		return nil, false
	}
	lastSlot, okLast := nbtGetInt(comp, "last_interacted_slot")
	if !okLast {
		lastSlot = -1
	}
	cb := &ChiseledBookshelfBlockEntity{
		Dimension:          normalizeHopperDim(dim),
		X:                  int(x),
		Y:                  int(y),
		Z:                  int(z),
		LastInteractedSlot: int(lastSlot),
	}
	items, _ := nbtGetListElems(comp, "Items")
	for _, raw := range items {
		entry, ok := raw.(*nbt.Compound)
		if !ok || entry == nil {
			continue
		}
		slotB, okSlot := nbtGetByte(entry, "Slot")
		if !okSlot || slotB < 0 || int(slotB) >= ChiseledBookshelfSlotCount {
			continue
		}
		slot := int(slotB)
		itemID, _ := nbtGetString(entry, "id")
		if itemID == "" {
			continue
		}
		cb.Slots[slot] = world.NewNamedItemStack(itemID, 1)
		if rawMeta, ok := entry.Get("Meta"); ok {
			if metaComp, ok := rawMeta.(*nbt.Compound); ok && metaComp != nil {
				cb.SlotMeta[slot] = decodeItemMetaFromNBT(metaComp)
			}
		}
	}
	return cb, true
}

func (s *Server) syncLoadedChiseledBookshelfBlockState(cb *ChiseledBookshelfBlockEntity) {
	if s == nil || cb == nil {
		return
	}
	normDim := normalizeHopperDim(cb.Dimension)
	cache := s.BindNoteBlockAndJukeboxRedstone(normDim)
	if cache != nil {
		curState := cache.GetBlock(cb.X, cb.Y, cb.Z)
		if IsChiseledBookshelfBlock(curState) {
			var occ [ChiseledBookshelfSlotCount]bool
			for i := 0; i < ChiseledBookshelfSlotCount; i++ {
				occ[i] = !cb.Slots[i].IsEmpty() && cb.Slots[i].Count > 0
			}
			nextState := ChiseledBookshelfBlockState(ChiseledBookshelfFacing(curState), occ)
			if nextState != 0 && nextState != curState {
				_, _ = cache.SetBlockWithLight(cb.X, cb.Y, cb.Z, nextState)
				s.broadcastBlockUpdateInDim(normDim, cb.X, cb.Y, cb.Z, nextState)
			}
		}
	}
	s.updateHopperComparatorsAndBroadcast(normDim, cb.X, cb.Y, cb.Z)
}

func (s *Server) chiseledBookshelvesFilePath() string {
	if s == nil {
		return ""
	}
	dir := strings.TrimSpace(s.cfg.WorldDir)
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "chiseled_bookshelves.dat")
}

// SaveChiseledBookshelves persists all ChiseledBookshelfBlockEntities to `<WorldDir>/chiseled_bookshelves.dat`.
func (s *Server) SaveChiseledBookshelves() error {
	path := s.chiseledBookshelvesFilePath()
	if path == "" {
		return nil
	}
	hs := s.hopperState()
	hs.mu.Lock()
	keys := make([]hopperBlockKey, 0, len(hs.bookshelves))
	for k := range hs.bookshelves {
		keys = append(keys, k)
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
	list := make([]nbt.Tag, 0, len(keys))
	for _, k := range keys {
		if cb := hs.bookshelves[k]; cb != nil {
			list = append(list, cb.ToNBT())
		}
	}
	hs.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	root := nbt.NewCompound()
	root.Set("ChiseledBookshelves", nbt.List{ElemID: nbt.TagCompound, Elems: list})
	data := nbt.MarshalNamed("", root)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// LoadChiseledBookshelves loads persisted ChiseledBookshelfBlockEntities from `<WorldDir>/chiseled_bookshelves.dat`.
func (s *Server) LoadChiseledBookshelves() error {
	path := s.chiseledBookshelvesFilePath()
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
	root, ok := parsedTag.(*nbt.Compound)
	if !ok || root == nil {
		return nil
	}
	entries, _ := nbtGetListElems(root, "ChiseledBookshelves")
	var loaded []*ChiseledBookshelfBlockEntity
	hs := s.hopperState()
	hs.mu.Lock()
	for _, raw := range entries {
		comp, ok := raw.(*nbt.Compound)
		if !ok {
			continue
		}
		if cb, ok := ChiseledBookshelfBlockEntityFromNBT(comp); ok && cb != nil {
			hs.bookshelves[hopperBlockKey{dim: cb.Dimension, x: cb.X, y: cb.Y, z: cb.Z}] = cb
			loaded = append(loaded, cb.Clone())
		}
	}
	hs.mu.Unlock()

	for _, cb := range loaded {
		s.syncLoadedChiseledBookshelfBlockState(cb)
	}
	return nil
}

// ChunkChiseledBookshelfBlockEntitiesNBT returns vanilla `block_entities` NBT
// compounds for all chiseled bookshelves in chunk (cx, cz) of dim.
func (s *Server) ChunkChiseledBookshelfBlockEntitiesNBT(dim string, cx, cz int32) []*nbt.Compound {
	if s == nil {
		return nil
	}
	normDim := normalizeHopperDim(dim)
	hs := s.hopperState()
	hs.mu.Lock()
	defer hs.mu.Unlock()
	var out []*nbt.Compound
	for k, cb := range hs.bookshelves {
		if k.dim != normDim || cb == nil {
			continue
		}
		if int32(k.x>>4) == cx && int32(k.z>>4) == cz {
			out = append(out, cb.ToNBT())
		}
	}
	return out
}

// UnloadChunkChiseledBookshelves removes all chiseled bookshelves in chunk
// (cx, cz) of dim from runtime memory and returns their serialized NBT compounds.
func (s *Server) UnloadChunkChiseledBookshelves(dim string, cx, cz int32) []*nbt.Compound {
	if s == nil {
		return nil
	}
	normDim := normalizeHopperDim(dim)
	hs := s.hopperState()
	hs.mu.Lock()
	defer hs.mu.Unlock()
	var out []*nbt.Compound
	for k, cb := range hs.bookshelves {
		if k.dim != normDim {
			continue
		}
		if int32(k.x>>4) == cx && int32(k.z>>4) == cz {
			if cb != nil {
				out = append(out, cb.ToNBT())
			}
			delete(hs.bookshelves, k)
		}
	}
	return out
}

// LoadChunkChiseledBookshelfBlockEntitiesNBT loads chiseled bookshelf block
// entities from a chunk's `block_entities` NBT list and syncs their slot occupancy.
func (s *Server) LoadChunkChiseledBookshelfBlockEntitiesNBT(dim string, list []*nbt.Compound) int {
	if s == nil || len(list) == 0 {
		return 0
	}
	normDim := normalizeHopperDim(dim)
	var loaded []*ChiseledBookshelfBlockEntity
	hs := s.hopperState()
	hs.mu.Lock()
	for _, comp := range list {
		cb, ok := ChiseledBookshelfBlockEntityFromNBT(comp)
		if !ok || cb == nil {
			continue
		}
		if cb.Dimension == "" {
			cb.Dimension = normDim
		}
		hs.bookshelves[hopperBlockKey{dim: cb.Dimension, x: cb.X, y: cb.Y, z: cb.Z}] = cb
		loaded = append(loaded, cb.Clone())
	}
	hs.mu.Unlock()

	for _, cb := range loaded {
		s.syncLoadedChiseledBookshelfBlockState(cb)
	}
	return len(loaded)
}

