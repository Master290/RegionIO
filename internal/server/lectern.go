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

	// LecternPageTurnPulseTicks is the 2-game-tick (1 redstone tick) pulse duration
	// during which `powered=true` is held on `minecraft:lectern` after a page turn.
	LecternPageTurnPulseTicks = 2
)

// LecternBlockEntity represents a minecraft:lectern block entity holding a book and current page.
type LecternBlockEntity struct {
	Dimension           string
	X                   int
	Y                   int
	Z                   int
	Book                world.ItemStack
	Writable            *WritableBookContent
	Written             *WrittenBookContent
	Page                int32
	PageCount           int32
	PulseTicksRemaining int
}

// Clone returns a deep copy of l.
func (l *LecternBlockEntity) Clone() *LecternBlockEntity {
	if l == nil {
		return nil
	}
	cp := &LecternBlockEntity{
		Dimension:           l.Dimension,
		X:                   l.X,
		Y:                   l.Y,
		Z:                   l.Z,
		Book:                l.Book,
		Page:                l.Page,
		PageCount:           l.PageCount,
		PulseTicksRemaining: l.PulseTicksRemaining,
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

// ParseLecternBlockState extracts `facing`, `has_book`, and `powered` from a
// `minecraft:lectern` block state ID.
func ParseLecternBlockState(state uint16) (facing string, hasBook, powered, ok bool) {
	if !IsLecternBlock(state) {
		return "", false, false, false
	}
	props := world.BlockProperties(state)
	facing = strings.ToLower(strings.TrimSpace(props["facing"]))
	if facing == "" {
		facing = "north"
	}
	return facing, props["has_book"] == "true", props["powered"] == "true", true
}

// MakeLecternBlockState constructs the block state ID for `minecraft:lectern`.
func MakeLecternBlockState(facing string, hasBook, powered bool) uint16 {
	switch strings.ToLower(strings.TrimSpace(facing)) {
	case "north", "south", "west", "east":
		facing = strings.ToLower(strings.TrimSpace(facing))
	default:
		facing = "north"
	}
	hbStr := "false"
	if hasBook {
		hbStr = "true"
	}
	pwStr := "false"
	if powered {
		pwStr = "true"
	}
	if id, ok := world.BlockStateIDByProperties("minecraft:lectern", map[string]string{
		"facing":   facing,
		"has_book": hbStr,
		"powered":  pwStr,
	}); ok {
		return id
	}
	return blockBaseStateByName("minecraft:lectern")
}

// ResolveLecternPlacementState returns the initial block state for a placed
// `minecraft:lectern`, orienting its front (`facing`) opposite to
// `playerHorizontalFacing` so it faces the player (`LecternBlock.getStateForPlacement`).
func ResolveLecternPlacementState(playerHorizontalFacing string, hasBook bool) uint16 {
	opp := "north"
	switch strings.ToLower(strings.TrimSpace(playerHorizontalFacing)) {
	case "north":
		opp = "south"
	case "south":
		opp = "north"
	case "west":
		opp = "east"
	case "east":
		opp = "west"
	}
	return MakeLecternBlockState(opp, hasBook, false)
}

// LecternStateWithBook returns the lectern block state with has_book adjusted
// while preserving its facing & powered offset.
// In vanilla 26.1.2 block states for minecraft:lectern:
// properties are facing (north/south/west/east, stride 4), has_book (true=0, false=1, stride 2),
// powered (true=0, false=1, stride 1). Default state (from ItemToBlock) has facing=north, has_book=false, powered=false.
func LecternStateWithBook(state uint16, hasBook bool) uint16 {
	facing, _, powered, ok := ParseLecternBlockState(state)
	if !ok {
		return state
	}
	if !hasBook {
		powered = false
	}
	return MakeLecternBlockState(facing, hasBook, powered)
}

// LecternStateWithPowered returns the lectern block state with `powered` adjusted
// while preserving its `facing` and `has_book` properties.
func LecternStateWithPowered(state uint16, powered bool) uint16 {
	facing, hasBook, _, ok := ParseLecternBlockState(state)
	if !ok {
		return state
	}
	return MakeLecternBlockState(facing, hasBook, powered)
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
// If the chunk has a generated `minecraft:lectern` StructureBlockEntity or a
// `minecraft:lectern[has_book=true]` block state that has not yet been hydrated,
// it initializes the lectern entity automatically.
func (s *Server) GetLecternAt(dimension string, x, y, z int) (*LecternBlockEntity, bool) {
	if s == nil {
		return nil, false
	}
	normDim := world.NormalizeDimension(dimension)
	key := blockPosDimKey{dim: normDim, x: x, y: y, z: z}
	st := s.lecternStore()
	st.mu.RLock()
	lec := st.lecterns[key].Clone()
	st.mu.RUnlock()
	if lec != nil {
		return lec, true
	}
	if hydrated := s.tryHydrateLecternAt(normDim, x, y, z); hydrated != nil {
		return hydrated, true
	}
	return nil, false
}

func (s *Server) tryHydrateLecternAt(normDim string, x, y, z int) *LecternBlockEntity {
	cache := s.chunksForDimension(normDim)
	if cache == nil || !cache.IsChunkLoaded(int32(x>>4), int32(z>>4)) {
		return nil
	}
	blockState := cache.GetBlock(x, y, z)
	_, hasBookProp, _, isLectern := ParseLecternBlockState(blockState)
	if !isLectern {
		return nil
	}

	var bookItem world.ItemStack
	for _, sbe := range cache.LoadedChunkStructureBlockEntities(int32(x>>4), int32(z>>4)) {
		if sbe.X == x && sbe.Y == y && sbe.Z == z && sbe.ID == "minecraft:lectern" {
			if len(sbe.Items) > 0 && sbe.Items[0].ItemID != "" {
				bookItem = world.NewNamedItemStack(sbe.Items[0].ItemID, 1)
			}
			break
		}
	}
	if bookItem.IsEmpty() && hasBookProp {
		bookItem = world.NewNamedItemStack(WritableBookItemID, 1)
	}
	if bookItem.IsEmpty() {
		return nil
	}

	lec := &LecternBlockEntity{
		Dimension: normDim,
		X:         x,
		Y:         y,
		Z:         z,
		Book:      bookItem,
		Page:      0,
		PageCount: 1,
	}
	if bookItem.ResolvedID() == WrittenBookItemID {
		defWritten := WrittenBookContent{
			Title:    FilterableString{Raw: "Book"},
			Author:   "Unknown",
			Pages:    []FilterableComponent{{Raw: TextComponent("")}},
			Resolved: true,
		}
		lec.Written = &defWritten
	} else {
		defWritable := WritableBookContent{
			Pages: []FilterableString{{Raw: ""}},
		}
		lec.Writable = &defWritable
	}

	key := blockPosDimKey{dim: normDim, x: x, y: y, z: z}
	st := s.lecternStore()
	st.mu.Lock()
	if existing := st.lecterns[key]; existing != nil {
		cp := existing.Clone()
		st.mu.Unlock()
		return cp
	}
	st.lecterns[key] = lec
	cp := lec.Clone()
	st.mu.Unlock()
	return cp
}

// HydrateChunkLecterns inspects all `world.StructureBlockEntity` entries on the
// loaded chunk (dim, cx, cz) and registers any `minecraft:lectern` block entities.
func (s *Server) HydrateChunkLecterns(dim string, cx, cz int32) int {
	if s == nil {
		return 0
	}
	normDim := world.NormalizeDimension(dim)
	cache := s.chunksForDimension(normDim)
	if cache == nil {
		return 0
	}
	sbes := cache.LoadedChunkStructureBlockEntities(cx, cz)
	count := 0
	for _, sbe := range sbes {
		if sbe.ID != "minecraft:lectern" {
			continue
		}
		if s.tryHydrateLecternAt(normDim, sbe.X, sbe.Y, sbe.Z) != nil {
			count++
		}
	}
	return count
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

func (s *Server) applyLecternBlockStateAndNotify(dim string, x, y, z int, hasBook, powered bool) {
	normDim := world.NormalizeDimension(dim)
	cache := s.BindNoteBlockAndJukeboxRedstone(normDim)
	if cache != nil {
		curState := cache.GetBlock(x, y, z)
		if IsLecternBlock(curState) {
			facing, _, _, _ := ParseLecternBlockState(curState)
			if !hasBook {
				powered = false
			}
			newState := MakeLecternBlockState(facing, hasBook, powered)
			if newState != curState {
				_, res := cache.ApplyBlockWithUpdates(x, y, z, newState)
				s.broadcastBlockUpdateInDim(normDim, x, y, z, newState)
				for _, ch := range res.Changes {
					s.broadcastBlockUpdateInDim(normDim, ch.Pos.X, ch.Pos.Y, ch.Pos.Z, ch.NewState)
				}
			}
		}
	}
	s.updateHopperComparatorsAndBroadcast(normDim, x, y, z)
}

// BroadcastLecternBlockEntityData broadcasts ClientboundBlockEntityDataPacket
// (0x06, BlockEntityTypeLectern = 31) for the lectern at (dim, x, y, z).
func (s *Server) BroadcastLecternBlockEntityData(dim string, x, y, z int, lec *LecternBlockEntity) {
	if s == nil {
		return
	}
	var comp *nbt.Compound
	if lec != nil {
		comp = lec.ToNBT()
	} else {
		comp = (&LecternBlockEntity{Dimension: dim, X: x, Y: y, Z: z}).ToNBT()
	}
	pkt := EncodeBlockEntityDataPacket(x, y, z, BlockEntityTypeLectern, comp)
	s.BroadcastDimensionChunk(world.NormalizeDimension(dim), int32(x>>4), int32(z>>4), PlayBlockEntityDataCB, pkt)
}

// TickLecterns advances any active 2-tick lectern page-turn redstone pulses by 1 game tick.
func (s *Server) TickLecterns() {
	s.AdvanceLecternPulses(1)
}

// AdvanceLecternPulses advances active lectern page-turn redstone pulses by `ticks`
// game ticks, resetting `powered=false` when a lectern's pulse timer expires.
func (s *Server) AdvanceLecternPulses(ticks int) int {
	if s == nil || ticks <= 0 {
		return 0
	}
	st := s.lecternStore()
	st.mu.Lock()
	var expired []blockPosDimKey
	for key, lec := range st.lecterns {
		if lec == nil || lec.PulseTicksRemaining <= 0 {
			continue
		}
		lec.PulseTicksRemaining -= ticks
		if lec.PulseTicksRemaining <= 0 {
			lec.PulseTicksRemaining = 0
			expired = append(expired, key)
		}
	}
	st.mu.Unlock()

	for _, key := range expired {
		s.applyLecternBlockStateAndNotify(key.dim, key.x, key.y, key.z, true, false)
	}
	return len(expired)
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
	cache := s.BindNoteBlockAndJukeboxRedstone(dim)
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
	lecCopy := lec.Clone()
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
		SyncPlayerSlotBookPacket(session, slot)
	}

	s.applyLecternBlockStateAndNotify(dim, x, y, z, true, false)
	s.BroadcastLecternBlockEntityData(dim, x, y, z, lecCopy)

	cx := int32(x >> 4)
	cz := int32(z >> 4)
	s.BroadcastDimensionChunk(dim, cx, cz, PlaySoundPacketID,
		EncodeSoundPacket("minecraft:item.book.put", SoundSourceBlocks, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, 1.0, 1.0, 0))
	s.EmitVibration(VibrationEvent{
		Dimension: dim,
		Type:      GameEventBlockChange,
		X:         float64(x) + 0.5,
		Y:         float64(y) + 0.5,
		Z:         float64(z) + 0.5,
		Player:    session,
	})
	return true
}

func (s *Server) broadcastBlockUpdateInDim(dim string, x, y, z int, state uint16) {
	w := protocol.NewWriter(16)
	w.Position(x, y, z)
	w.VarInt(int32(state))
	cx := int32(x >> 4)
	cz := int32(z >> 4)
	s.BroadcastDimensionChunk(world.NormalizeDimension(dim), cx, cz, protocol.PlayBlockUpdate, w.Bytes())
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
	_, _ = s.GetLecternAt(dim, x, y, z)
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
//   - buttonID == 1 (LecternButtonPrevPage): decrements Page, pulses `powered=true` (2 ticks), updates comparators
//   - buttonID == 2 (LecternButtonNextPage): increments Page, pulses `powered=true` (2 ticks), updates comparators
//   - buttonID == 3 (LecternButtonTakeBook): takes the book into player's inventory and closes the lectern UI
//   - buttonID >= 100 (LecternButtonPageJumpBase): jumps to page (buttonID - 100), pulses `powered=true` (2 ticks), updates comparators
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
		prevPage := lec.Page
		if lec.Page > 0 {
			lec.Page--
		}
		pageChanged := lec.Page != prevPage
		if pageChanged {
			lec.PulseTicksRemaining = LecternPageTurnPulseTicks
		}
		newPage := int16(lec.Page)
		lecCopy := lec.Clone()
		viewers := s.lecternViewersLocked(st, open.key)
		st.mu.Unlock()
		s.broadcastLecternPageToViewers(viewers, newPage, open.key)
		if pageChanged {
			s.onLecternPageChanged(open.key, lecCopy, session)
		}
		return true, nil

	case buttonID == LecternButtonNextPage:
		prevPage := lec.Page
		if lec.Page+1 < lec.PageCount {
			lec.Page++
		}
		pageChanged := lec.Page != prevPage
		if pageChanged {
			lec.PulseTicksRemaining = LecternPageTurnPulseTicks
		}
		newPage := int16(lec.Page)
		lecCopy := lec.Clone()
		viewers := s.lecternViewersLocked(st, open.key)
		st.mu.Unlock()
		s.broadcastLecternPageToViewers(viewers, newPage, open.key)
		if pageChanged {
			s.onLecternPageChanged(open.key, lecCopy, session)
		}
		return true, nil

	case buttonID >= LecternButtonPageJumpBase:
		targetPage := buttonID - LecternButtonPageJumpBase
		if targetPage < 0 {
			targetPage = 0
		}
		if lec.PageCount > 0 && targetPage >= lec.PageCount {
			targetPage = lec.PageCount - 1
		}
		pageChanged := lec.Page != targetPage
		lec.Page = targetPage
		if pageChanged {
			lec.PulseTicksRemaining = LecternPageTurnPulseTicks
		}
		newPage := int16(lec.Page)
		lecCopy := lec.Clone()
		viewers := s.lecternViewersLocked(st, open.key)
		st.mu.Unlock()
		s.broadcastLecternPageToViewers(viewers, newPage, open.key)
		if pageChanged {
			s.onLecternPageChanged(open.key, lecCopy, session)
		}
		return true, nil

	case buttonID == LecternButtonTakeBook:
		st.mu.Unlock()
		_, _, _, _ = s.TakeBookFromLectern(session, open.key.dim, open.key.x, open.key.y, open.key.z)
		return true, nil

	default:
		st.mu.Unlock()
		return true, nil
	}
}

func (s *Server) onLecternPageChanged(key blockPosDimKey, lecCopy *LecternBlockEntity, session *PlayerSession) {
	s.applyLecternBlockStateAndNotify(key.dim, key.x, key.y, key.z, true, true)
	s.BroadcastLecternBlockEntityData(key.dim, key.x, key.y, key.z, lecCopy)
	s.EmitVibration(VibrationEvent{
		Dimension: key.dim,
		Type:      GameEventBlockChange,
		X:         float64(key.x) + 0.5,
		Y:         float64(key.y) + 0.5,
		Z:         float64(key.z) + 0.5,
		Player:    session,
	})
}

// TakeBookFromLectern removes the book from the lectern at (dim, x, y, z),
// gives it to `session` (if non-nil) with its WritableBookContent / WrittenBookContent,
// updates the lectern block state (`has_book=false, powered=false`) and comparators,
// and closes the lectern screen for any open viewers.
func (s *Server) TakeBookFromLectern(session *PlayerSession, dim string, x, y, z int) (world.ItemStack, *WritableBookContent, *WrittenBookContent, bool) {
	if s == nil {
		return world.ItemStack{}, nil, nil, false
	}
	normDim := world.NormalizeDimension(dim)
	key := blockPosDimKey{dim: normDim, x: x, y: y, z: z}
	st := s.lecternStore()
	st.mu.Lock()
	lec := st.lecterns[key]
	if lec == nil || !lec.HasBook() {
		st.mu.Unlock()
		return world.ItemStack{}, nil, nil, false
	}
	takenBook := lec.Book
	var takenWritable *WritableBookContent
	if lec.Writable != nil {
		cp := lec.Writable.Clone()
		takenWritable = &cp
	}
	var takenWritten *WrittenBookContent
	if lec.Written != nil {
		cp := lec.Written.Clone()
		takenWritten = &cp
	}
	delete(st.lecterns, key)
	viewers := s.lecternViewersLocked(st, key)
	for _, v := range viewers {
		delete(st.openBy, v.session)
	}
	st.mu.Unlock()

	if session != nil {
		s.giveBookToPlayer(session, takenBook, takenWritable, takenWritten)
	} else {
		_, _ = s.SpawnBookItemEntityInDimension(normDim, float64(x)+0.5, float64(y)+1.0, float64(z)+0.5, takenBook, takenWritable, takenWritten)
	}

	s.applyLecternBlockStateAndNotify(normDim, x, y, z, false, false)
	s.BroadcastLecternBlockEntityData(normDim, x, y, z, nil)
	s.EmitVibration(VibrationEvent{
		Dimension: normDim,
		Type:      GameEventBlockChange,
		X:         float64(x) + 0.5,
		Y:         float64(y) + 0.5,
		Z:         float64(z) + 0.5,
		Player:    session,
	})

	for _, v := range viewers {
		if v.session != nil && v.session.send != nil {
			w := protocol.NewWriter(4)
			w.VarInt(v.containerID)
			_ = v.session.send(protocol.PlayContainerClose, w.Bytes())
		}
	}
	return takenBook, takenWritable, takenWritten, true
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
	s.BroadcastDimensionChunk(key.dim, cx, cz, PlaySoundPacketID,
		EncodeSoundPacket("minecraft:item.book.page_turn", SoundSourceBlocks, float64(key.x)+0.5, float64(key.y)+0.5, float64(key.z)+0.5, 1.0, 1.0, 0))
}

func (s *Server) giveBookToPlayer(session *PlayerSession, book world.ItemStack, writable *WritableBookContent, written *WrittenBookContent) {
	if session == nil || book.IsEmpty() {
		return
	}
	heldSlot := int(world.HotbarContainerOffset + session.Snapshot().SelectedSlot)
	if session.InventorySlot(heldSlot).IsEmpty() {
		session.SetInventorySlot(heldSlot, book)
		if writable != nil {
			SetSlotWritableBook(session, heldSlot, *writable)
		}
		if written != nil {
			SetSlotWrittenBook(session, heldSlot, *written)
		}
		if session.send != nil {
			stateID := session.NextContainerStateID()
			_ = session.send(protocol.PlayContainerSetSlot, encodeContainerSetSlotWithBook(0, stateID, heldSlot, book, writable, written))
		}
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
	_, _ = s.SpawnBookItemEntityInDimension(snap.Dimension, snap.X, snap.Y+0.25, snap.Z, book, writable, written)
}

// RemoveLecternAt removes any stored LecternBlockEntity at (dimension, x, y, z)
// when the lectern block is broken, returning the held book so it can be dropped.
func (s *Server) RemoveLecternAt(dimension string, x, y, z int) (world.ItemStack, *WritableBookContent, *WrittenBookContent, bool) {
	if s == nil {
		return world.ItemStack{}, nil, nil, false
	}
	normDim := world.NormalizeDimension(dimension)
	key := blockPosDimKey{dim: normDim, x: x, y: y, z: z}
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

	s.applyLecternBlockStateAndNotify(normDim, x, y, z, false, false)

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
