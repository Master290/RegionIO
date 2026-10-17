package server

import (
	"errors"
	"math"
	"strings"
	"sync"

	"regionio/internal/nbt"
	"regionio/internal/protocol"
	"regionio/internal/world"
)

// MenuTypeLectern is BuiltInRegistries.MENU index 17 ("minecraft:lectern" in protocol 775 / 26.1.2).
const MenuTypeLectern int32 = 17

// BlockEntityTypeLectern is BuiltInRegistries.BLOCK_ENTITY_TYPE index 31 ("minecraft:lectern" in protocol 775).
const BlockEntityTypeLectern int32 = 31

// LecternMenu button IDs in ServerboundContainerButtonClickPacket (0x11),
// matching vanilla net.minecraft.world.inventory.LecternMenu.
const (
	LecternButtonPrevPage     int32 = 1
	LecternButtonNextPage     int32 = 2
	LecternButtonTakeBook     int32 = 3
	LecternButtonPageJumpBase int32 = 100
)

// LecternBlockEntity represents a minecraft:lectern block entity holding a book and current page.
type LecternBlockEntity struct {
	Dimension string
	X         int
	Y         int
	Z         int
	Book      world.ItemStack
	Writable  *WritableBookContent
	Written   *WrittenBookContent
	Page      int32
	PageCount int32
}

// Clone returns a deep copy of l.
func (l *LecternBlockEntity) Clone() *LecternBlockEntity {
	if l == nil {
		return nil
	}
	cp := &LecternBlockEntity{
		Dimension: l.Dimension,
		X:         l.X,
		Y:         l.Y,
		Z:         l.Z,
		Book:      l.Book,
		Page:      l.Page,
		PageCount: l.PageCount,
	}
	if l.Writable != nil {
		w := l.Writable.Clone()
		cp.Writable = &w
	}
	if l.Written != nil {
		w := l.Written.Clone()
		cp.Written = &w
	}
	return cp
}

// HasBook reports whether this lectern currently holds a book.
func (l *LecternBlockEntity) HasBook() bool {
	return l != nil && !l.Book.IsEmpty()
}

// ComparatorSignal returns the redstone comparator signal (0..15) for this lectern.
func (l *LecternBlockEntity) ComparatorSignal() int {
	if !l.HasBook() {
		return 0
	}
	return CalculateLecternComparatorSignal(int(l.Page), int(l.PageCount))
}

// CalculateLecternComparatorSignal computes the vanilla redstone comparator output
// (0..15) for a lectern at (page, pageCount), matching LecternBlockEntity.getRedstoneSignal:
//   - 0 if pageCount <= 0 (no book)
//   - floor(float32(page) / float32(pageCount-1) * 14.0) + 1 (or 15 when pageCount == 1)
func CalculateLecternComparatorSignal(page, pageCount int) int {
	if pageCount <= 0 {
		return 0
	}
	if page < 0 {
		page = 0
	}
	if page >= pageCount {
		page = pageCount - 1
	}
	var f float32
	if pageCount > 1 {
		f = float32(page) / float32(pageCount-1)
	} else {
		f = 1.0
	}
	sig := int(math.Floor(float64(f*14.0))) + 1
	if sig < 1 {
		sig = 1
	}
	if sig > 15 {
		sig = 15
	}
	return sig
}

// IsLecternBlock reports whether state is a minecraft:lectern block.
func IsLecternBlock(state uint16) bool {
	return world.BlockName(state) == "minecraft:lectern"
}

// LecternStateWithBook returns the lectern block state with has_book adjusted
// while preserving its facing & powered offset.
// In vanilla 26.1.2 block states for minecraft:lectern:
// properties are facing (north/south/west/east, stride 4), has_book (true=0, false=1, stride 2),
// powered (true=0, false=1, stride 1). Default state (from ItemToBlock) has facing=north, has_book=false, powered=false.
func LecternStateWithBook(state uint16, hasBook bool) uint16 {
	if !IsLecternBlock(state) {
		return state
	}
	base := blockBaseStateByName("minecraft:lectern")
	if base == world.StateAir || state < base || state >= base+16 {
		return state
	}
	offset := state - base // 0..15: (facing*4) + (has_book?0:2) + (powered?0:1)
	facingAndPowered := offset &^ 2
	if !hasBook {
		facingAndPowered |= 2
	}
	cand := base + facingAndPowered
	if IsLecternBlock(cand) {
		return cand
	}
	return state
}

// ToNBT serializes LecternBlockEntity into a vanilla block_entities NBT compound.
func (l *LecternBlockEntity) ToNBT() *nbt.Compound {
	comp := nbt.NewCompound()
	if l == nil {
		return comp
	}
	comp.Set("id", nbt.String("minecraft:lectern"))
	comp.Set("x", nbt.Int(int32(l.X)))
	comp.Set("y", nbt.Int(int32(l.Y)))
	comp.Set("z", nbt.Int(int32(l.Z)))
	if l.Dimension != "" {
		comp.Set("dimension", nbt.String(world.NormalizeDimension(l.Dimension)))
	}
	if !l.Book.IsEmpty() {
		bookTag := nbt.NewCompound()
		bookTag.Set("id", nbt.String(l.Book.ResolvedID()))
		bookTag.Set("count", nbt.Int(l.Book.Count))
		if l.Writable != nil {
			bookTag.Set("writable_book_content", l.Writable.ToNBT())
		}
		if l.Written != nil {
			bookTag.Set("written_book_content", l.Written.ToNBT())
		}
		comp.Set("Book", bookTag)
		comp.Set("Page", nbt.Int(l.Page))
	}
	return comp
}

// LecternBlockEntityFromNBT decodes a LecternBlockEntity from a block_entities NBT compound.
func LecternBlockEntityFromNBT(comp *nbt.Compound) (*LecternBlockEntity, error) {
	if comp == nil {
		return nil, errors.New("server: nil lectern NBT")
	}
	x, _ := nbtIntTag(comp, "x")
	y, _ := nbtIntTag(comp, "y")
	z, _ := nbtIntTag(comp, "z")
	dim, _ := nbtStringTag(comp, "dimension")
	if dim == "" {
		dim = world.DimensionOverworld
	}
	out := &LecternBlockEntity{
		Dimension: world.NormalizeDimension(dim),
		X:         int(x),
		Y:         int(y),
		Z:         int(z),
	}
	if bookComp, ok := nbtCompoundTag(comp, "Book"); ok && bookComp != nil {
		itemID, _ := nbtStringTag(bookComp, "id")
		cnt, ok := nbtIntTag(bookComp, "count")
		if !ok || cnt <= 0 {
			cnt = 1
		}
		out.Book = world.NewNamedItemStack(itemID, cnt)
		if wc, ok := nbtCompoundTag(bookComp, "writable_book_content"); ok && wc != nil {
			parsed := WritableBookContentFromNBT(wc)
			out.Writable = &parsed
			out.PageCount = int32(len(parsed.Pages))
		}
		if sc, ok := nbtCompoundTag(bookComp, "written_book_content"); ok && sc != nil {
			parsed := WrittenBookContentFromNBT(sc)
			out.Written = &parsed
			out.PageCount = int32(len(parsed.Pages))
		}
		if out.PageCount < 1 && !out.Book.IsEmpty() {
			out.PageCount = 1
		}
		page, _ := nbtIntTag(comp, "Page")
		if page < 0 {
			page = 0
		}
		if out.PageCount > 0 && page >= out.PageCount {
			page = out.PageCount - 1
		}
		out.Page = page
	}
	return out, nil
}

// --- Server Lectern State & Open Viewer Tracking ---

type blockPosDimKey struct {
	dim     string
	x, y, z int
}

type openLecternSessionState struct {
	containerID int32
	key         blockPosDimKey
}

type serverLecternStore struct {
	mu       sync.RWMutex
	lecterns map[blockPosDimKey]*LecternBlockEntity
	openBy   map[*PlayerSession]openLecternSessionState
}

var lecternStores sync.Map // map[*Server]*serverLecternStore

func (s *Server) lecternStore() *serverLecternStore {
	if s == nil {
		return &serverLecternStore{
			lecterns: make(map[blockPosDimKey]*LecternBlockEntity),
			openBy:   make(map[*PlayerSession]openLecternSessionState),
		}
	}
	if v, ok := lecternStores.Load(s); ok {
		return v.(*serverLecternStore)
	}
	st := &serverLecternStore{
		lecterns: make(map[blockPosDimKey]*LecternBlockEntity),
		openBy:   make(map[*PlayerSession]openLecternSessionState),
	}
	actual, _ := lecternStores.LoadOrStore(s, st)
	return actual.(*serverLecternStore)
}

// GetLecternAt returns a copy of the LecternBlockEntity at (dimension, x, y, z), if present.
func (s *Server) GetLecternAt(dimension string, x, y, z int) (*LecternBlockEntity, bool) {
	if s == nil {
		return nil, false
	}
	key := blockPosDimKey{dim: world.NormalizeDimension(dimension), x: x, y: y, z: z}
	st := s.lecternStore()
	st.mu.RLock()
	lec := st.lecterns[key].Clone()
	st.mu.RUnlock()
	if lec == nil {
		return nil, false
	}
	return lec, true
}

// SetLecternAt stores or updates the LecternBlockEntity at (lec.Dimension, lec.X, lec.Y, lec.Z).
func (s *Server) SetLecternAt(lec *LecternBlockEntity) {
	if s == nil || lec == nil {
		return
	}
	cp := lec.Clone()
	cp.Dimension = world.NormalizeDimension(cp.Dimension)
	key := blockPosDimKey{dim: cp.Dimension, x: cp.X, y: cp.Y, z: cp.Z}
	st := s.lecternStore()
	st.mu.Lock()
	st.lecterns[key] = cp
	st.mu.Unlock()
}

// GetLecternComparatorOutput returns the redstone comparator signal (0..15)
// for a lectern at (dimension, x, y, z). Exposed for redstone comparator logic.
func (s *Server) GetLecternComparatorOutput(dimension string, x, y, z int) int {
	lec, ok := s.GetLecternAt(dimension, x, y, z)
	if !ok || lec == nil {
		return 0
	}
	return lec.ComparatorSignal()
}

// LecternComparatorSignalAt returns the redstone comparator signal (0..15)
// for an Overworld lectern at (x, y, z).
func (s *Server) LecternComparatorSignalAt(x, y, z int) int {
	return s.GetLecternComparatorOutput(world.DimensionOverworld, x, y, z)
}

// PlaceBookOnLectern places a writable_book or written_book from session's hand
// onto an empty lectern at (x, y, z). Returns true if the book was placed.
func (s *Server) PlaceBookOnLectern(session *PlayerSession, hand int32, x, y, z int) bool {
	if s == nil || session == nil || session.IsDead() {
		return false
	}
	slot, held := session.handSlotAndStack(hand)
	if held.IsEmpty() {
		return false
	}
	itemID := held.ResolvedID()
	if itemID != WritableBookItemID && itemID != WrittenBookItemID {
		return false
	}

	snap := session.Snapshot()
	dim := world.NormalizeDimension(snap.Dimension)
	cache := s.chunksForDimension(dim)
	if cache != nil {
		blockState := cache.GetBlock(x, y, z)
		if blockState != world.StateAir && !IsLecternBlock(blockState) {
			return false
		}
	}

	key := blockPosDimKey{dim: dim, x: x, y: y, z: z}
	st := s.lecternStore()
	st.mu.Lock()
	if existing := st.lecterns[key]; existing != nil && existing.HasBook() {
		st.mu.Unlock()
		return false
	}

	lec := &LecternBlockEntity{
		Dimension: dim,
		X:         x,
		Y:         y,
		Z:         z,
		Book:      world.NewItemStack(held.ResolvedItemID(), 1),
		Page:      0,
		PageCount: 1,
	}
	if itemID == WritableBookItemID {
		if wb, ok := GetSlotWritableBook(session, slot); ok {
			lec.Writable = &wb
			if len(wb.Pages) > 0 {
				lec.PageCount = int32(len(wb.Pages))
			}
		} else {
			empty := WritableBookContent{Pages: []FilterableString{{Raw: ""}}}
			lec.Writable = &empty
		}
	} else {
		if sb, ok := GetSlotWrittenBook(session, slot); ok {
			sb.Resolved = true
			lec.Written = &sb
			if len(sb.Pages) > 0 {
				lec.PageCount = int32(len(sb.Pages))
			}
		} else {
			empty := WrittenBookContent{
				Title:    FilterableString{Raw: "Book"},
				Author:   snap.Profile.Name,
				Pages:    []FilterableComponent{{Raw: TextComponent("")}},
				Resolved: true,
			}
			lec.Written = &empty
		}
	}
	st.lecterns[key] = lec
	st.mu.Unlock()

	// Consume 1 book from player's hand in survival/adventure mode.
	if snap.GameMode != GameModeCreative {
		rem := held.Count - 1
		if rem <= 0 {
			session.SetInventorySlot(slot, world.ItemStack{})
			ClearSlotBookMeta(session, slot)
		} else {
			session.SetInventorySlot(slot, world.NewItemStack(held.ResolvedItemID(), rem))
		}
		if session.send != nil {
			stateID := session.NextContainerStateID()
			_ = session.send(protocol.PlayContainerSetSlot, encodeContainerSetSlotPacket(stateID, slot, session.InventorySlot(slot)))
		}
	}

	if cache != nil {
		curState := cache.GetBlock(x, y, z)
		if IsLecternBlock(curState) {
			newState := LecternStateWithBook(curState, true)
			if newState != curState {
				cache.SetBlock(x, y, z, newState)
				s.broadcastBlockUpdateInDim(dim, x, y, z, newState)
			}
		}
	}

	cx := int32(x >> 4)
	cz := int32(z >> 4)
	s.BroadcastChunk(cx, cz, PlaySoundPacketID,
		EncodeSoundPacket("minecraft:item.book.put", SoundSourceBlocks, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, 1.0, 1.0, 0))
	return true
}

func (s *Server) broadcastBlockUpdateInDim(dim string, x, y, z int, state uint16) {
	w := protocol.NewWriter(16)
	w.Position(x, y, z)
	w.VarInt(int32(state))
	cx := int32(x >> 4)
	cz := int32(z >> 4)
	s.BroadcastChunk(cx, cz, protocol.PlayBlockUpdate, w.Bytes())
}

// OpenLecternScreen opens the MenuTypeLectern (17) UI for a lectern holding a book at (x, y, z),
// sending open_screen (0x3b), container_set_slot (0x14) for slot 0 with the book component,
// and container_set_data (0x13) property 0 = current page.
func (s *Server) OpenLecternScreen(session *PlayerSession, x, y, z int) bool {
	if s == nil || session == nil || session.IsDead() {
		return false
	}
	snap := session.Snapshot()
	dim := world.NormalizeDimension(snap.Dimension)
	key := blockPosDimKey{dim: dim, x: x, y: y, z: z}

	st := s.lecternStore()
	st.mu.Lock()
	lec := st.lecterns[key]
	if lec == nil || !lec.HasBook() {
		st.mu.Unlock()
		return false
	}
	lecCopy := lec.Clone()
	tr := getContainerTracker(session)
	tr.mu.Lock()
	tr.nextWindow = (tr.nextWindow % 100) + 1
	cid := tr.nextWindow
	tr.mu.Unlock()
	st.openBy[session] = openLecternSessionState{
		containerID: cid,
		key:         key,
	}
	st.mu.Unlock()

	if session.send != nil {
		_ = session.send(protocol.PlayOpenScreen, EncodeOpenScreen(cid, MenuTypeLectern, "Lectern"))
		stateID := session.NextContainerStateID()
		_ = session.send(protocol.PlayContainerSetSlot, encodeContainerSetSlotWithBook(cid, stateID, 0, lecCopy.Book, lecCopy.Writable, lecCopy.Written))
		_ = session.send(protocol.PlayContainerSetData, encodeLecternPageSetData(cid, int16(lecCopy.Page)))
	}
	return true
}

func encodeLecternPageSetData(containerID int32, page int16) []byte {
	w := protocol.NewWriter(12)
	w.VarInt(containerID)
	w.Uint16(0) // property 0 = page index in LecternMenu
	w.Uint16(uint16(page))
	return w.Bytes()
}

// InteractWithLectern handles right-clicking (use_item_on) a lectern block at (x, y, z):
//   - If the lectern already holds a book, opens the lectern reading UI.
//   - If the lectern is empty and the player holds a writable_book or written_book, places it on the lectern.
func (s *Server) InteractWithLectern(session *PlayerSession, hand int32, x, y, z int) bool {
	if s == nil || session == nil || session.IsDead() {
		return false
	}
	snap := session.Snapshot()
	dim := world.NormalizeDimension(snap.Dimension)
	if cache := s.chunksForDimension(dim); cache != nil {
		st := cache.GetBlock(x, y, z)
		if st != world.StateAir && !IsLecternBlock(st) {
			return false
		}
	}
	if lec, ok := s.GetLecternAt(dim, x, y, z); ok && lec.HasBook() {
		return s.OpenLecternScreen(session, x, y, z)
	}
	return s.PlaceBookOnLectern(session, hand, x, y, z)
}

// IsLecternOpen reports whether session currently has a lectern reading screen open.
func (s *Server) IsLecternOpen(session *PlayerSession) bool {
	if s == nil || session == nil {
		return false
	}
	st := s.lecternStore()
	st.mu.RLock()
	_, ok := st.openBy[session]
	st.mu.RUnlock()
	return ok
}

// CloseLecternIfOpen removes session from open lectern tracking.
func (s *Server) CloseLecternIfOpen(session *PlayerSession) {
	if s == nil || session == nil {
		return
	}
	st := s.lecternStore()
	st.mu.Lock()
	delete(st.openBy, session)
	st.mu.Unlock()
}

// HandleLecternButtonClick processes ServerboundContainerButtonClickPacket (0x11)
// when session has a lectern open:
//   - buttonID == 1 (LecternButtonPrevPage): decrements Page
//   - buttonID == 2 (LecternButtonNextPage): increments Page
//   - buttonID == 3 (LecternButtonTakeBook): takes the book into player's inventory and closes the lectern UI
//   - buttonID >= 100 (LecternButtonPageJumpBase): jumps to page (buttonID - 100)
func (s *Server) HandleLecternButtonClick(session *PlayerSession, containerID, buttonID int32) (bool, error) {
	if s == nil || session == nil || session.IsDead() {
		return false, nil
	}
	st := s.lecternStore()
	st.mu.Lock()
	open, ok := st.openBy[session]
	if !ok || (containerID != 0 && open.containerID != containerID) {
		st.mu.Unlock()
		return false, nil
	}
	lec := st.lecterns[open.key]
	if lec == nil || !lec.HasBook() {
		delete(st.openBy, session)
		st.mu.Unlock()
		return true, nil
	}

	switch {
	case buttonID == LecternButtonPrevPage:
		if lec.Page > 0 {
			lec.Page--
		}
		newPage := int16(lec.Page)
		viewers := s.lecternViewersLocked(st, open.key)
		st.mu.Unlock()
		s.broadcastLecternPageToViewers(viewers, newPage, open.key)
		return true, nil

	case buttonID == LecternButtonNextPage:
		if lec.Page+1 < lec.PageCount {
			lec.Page++
		}
		newPage := int16(lec.Page)
		viewers := s.lecternViewersLocked(st, open.key)
		st.mu.Unlock()
		s.broadcastLecternPageToViewers(viewers, newPage, open.key)
		return true, nil

	case buttonID >= LecternButtonPageJumpBase:
		targetPage := buttonID - LecternButtonPageJumpBase
		if targetPage < 0 {
			targetPage = 0
		}
		if lec.PageCount > 0 && targetPage >= lec.PageCount {
			targetPage = lec.PageCount - 1
		}
		lec.Page = targetPage
		newPage := int16(lec.Page)
		viewers := s.lecternViewersLocked(st, open.key)
		st.mu.Unlock()
		s.broadcastLecternPageToViewers(viewers, newPage, open.key)
		return true, nil

	case buttonID == LecternButtonTakeBook:
		takenBook := lec.Book
		takenWritable := lec.Writable
		takenWritten := lec.Written
		delete(st.lecterns, open.key)
		viewers := s.lecternViewersLocked(st, open.key)
		for _, v := range viewers {
			delete(st.openBy, v.session)
		}
		st.mu.Unlock()

		// Give the book back to session.
		s.giveBookToPlayer(session, takenBook, takenWritable, takenWritten)

		// Update block state to has_book=false.
		if cache := s.chunksForDimension(open.key.dim); cache != nil {
			curState := cache.GetBlock(open.key.x, open.key.y, open.key.z)
			if IsLecternBlock(curState) {
				newState := LecternStateWithBook(curState, false)
				if newState != curState {
					cache.SetBlock(open.key.x, open.key.y, open.key.z, newState)
					s.broadcastBlockUpdateInDim(open.key.dim, open.key.x, open.key.y, open.key.z, newState)
				}
			}
		}

		// Close lectern screen for all viewers of this lectern.
		for _, v := range viewers {
			if v.session != nil && v.session.send != nil {
				w := protocol.NewWriter(4)
				w.VarInt(v.containerID)
				_ = v.session.send(protocol.PlayContainerClose, w.Bytes())
			}
		}
		return true, nil

	default:
		st.mu.Unlock()
		return true, nil
	}
}

type lecternViewerEntry struct {
	session     *PlayerSession
	containerID int32
}

func (s *Server) lecternViewersLocked(st *serverLecternStore, key blockPosDimKey) []lecternViewerEntry {
	var out []lecternViewerEntry
	for sess, open := range st.openBy {
		if open.key == key {
			out = append(out, lecternViewerEntry{session: sess, containerID: open.containerID})
		}
	}
	return out
}

func (s *Server) broadcastLecternPageToViewers(viewers []lecternViewerEntry, page int16, key blockPosDimKey) {
	for _, v := range viewers {
		if v.session != nil && v.session.send != nil {
			_ = v.session.send(protocol.PlayContainerSetData, encodeLecternPageSetData(v.containerID, page))
		}
	}
	cx := int32(key.x >> 4)
	cz := int32(key.z >> 4)
	s.BroadcastChunk(cx, cz, PlaySoundPacketID,
		EncodeSoundPacket("minecraft:item.book.page_turn", SoundSourceBlocks, float64(key.x)+0.5, float64(key.y)+0.5, float64(key.z)+0.5, 1.0, 1.0, 0))
}

func (s *Server) giveBookToPlayer(session *PlayerSession, book world.ItemStack, writable *WritableBookContent, written *WrittenBookContent) {
	if session == nil || book.IsEmpty() {
		return
	}
	inv := session.Inventory()
	targetSlot := -1
	for _, r := range [][2]int{{36, 45}, {9, 36}} {
		for i := r[0]; i < r[1]; i++ {
			if inv[i].IsEmpty() {
				targetSlot = i
				break
			}
		}
		if targetSlot >= 0 {
			break
		}
	}
	if targetSlot >= 0 {
		session.SetInventorySlot(targetSlot, book)
		if writable != nil {
			SetSlotWritableBook(session, targetSlot, *writable)
		}
		if written != nil {
			SetSlotWrittenBook(session, targetSlot, *written)
		}
		if session.send != nil {
			stateID := session.NextContainerStateID()
			_ = session.send(protocol.PlayContainerSetSlot, encodeContainerSetSlotWithBook(0, stateID, targetSlot, book, writable, written))
		}
		return
	}
	snap := session.Snapshot()
	_, _ = s.SpawnItemEntityInDimension(snap.Dimension, snap.X, snap.Y+0.25, snap.Z, book)
}

// RemoveLecternAt removes any stored LecternBlockEntity at (dimension, x, y, z)
// when the lectern block is broken, returning the held book so it can be dropped.
func (s *Server) RemoveLecternAt(dimension string, x, y, z int) (world.ItemStack, *WritableBookContent, *WrittenBookContent, bool) {
	if s == nil {
		return world.ItemStack{}, nil, nil, false
	}
	key := blockPosDimKey{dim: world.NormalizeDimension(dimension), x: x, y: y, z: z}
	st := s.lecternStore()
	st.mu.Lock()
	lec, exists := st.lecterns[key]
	if !exists || lec == nil {
		st.mu.Unlock()
		return world.ItemStack{}, nil, nil, false
	}
	delete(st.lecterns, key)
	viewers := s.lecternViewersLocked(st, key)
	for _, v := range viewers {
		delete(st.openBy, v.session)
	}
	st.mu.Unlock()

	for _, v := range viewers {
		if v.session != nil && v.session.send != nil {
			w := protocol.NewWriter(4)
			w.VarInt(v.containerID)
			_ = v.session.send(protocol.PlayContainerClose, w.Bytes())
		}
	}
	if !lec.HasBook() {
		return world.ItemStack{}, nil, nil, false
	}
	return lec.Book, lec.Writable, lec.Written, true
}

func isBookOrLecternRelatedItem(name string) bool {
	short := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(name)), "minecraft:")
	return short == "writable_book" || short == "written_book" || short == "lectern"
}
