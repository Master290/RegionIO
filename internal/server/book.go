package server

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"regionio/internal/nbt"
	"regionio/internal/protocol"
	"regionio/internal/world"
)

// Book item resource identifiers.
const (
	WritableBookItemID = "minecraft:writable_book"
	WrittenBookItemID  = "minecraft:written_book"
	BookItemID         = "minecraft:book"
)

// Protocol 775 (26.1.2) book packet IDs.
const (
	// PlayEditBookSB is ServerboundEditBookPacket (0x18 in protocol 775).
	PlayEditBookSB int32 = 0x18
	// PlayOpenBookCB is ClientboundOpenBookPacket (0x3a in protocol 775).
	PlayOpenBookCB int32 = 0x3a
)

// Protocol 775 (26.1.2) BuiltInRegistries.DATA_COMPONENT_TYPE indices for books.
const (
	DataComponentWritableBookContent int32 = 60 // minecraft:writable_book_content
	DataComponentWrittenBookContent  int32 = 61 // minecraft:written_book_content
)

// Vanilla 26.1.2 book limits and generation constants.
const (
	MaxBookPages       = 100
	MaxBookPageRunes   = 1024
	MaxBookTitleRunes  = 32
	MinBookTitleRunes  = 1
	MaxBookAuthorRunes = 64

	BookGenerationOriginal       int32 = 0 // Original
	BookGenerationCopyOfOriginal int32 = 1 // Copy of original
	BookGenerationCopyOfCopy     int32 = 2 // Copy of a copy (cannot be copied further)
	BookGenerationTattered       int32 = 3 // Tattered
	MaxCopyableBookGeneration    int32 = 1
)

// FilterableString represents vanilla Filterable<String> (raw text + optional filtered text).
type FilterableString struct {
	Raw         string
	Filtered    string
	HasFiltered bool
}

// Effective returns Filtered when HasFiltered is true, or Raw otherwise.
func (f FilterableString) Effective() string {
	if f.HasFiltered {
		return f.Filtered
	}
	return f.Raw
}

// WritableBookContent represents the minecraft:writable_book_content component on a Book and Quill.
type WritableBookContent struct {
	Pages []FilterableString
}

// Clone returns a deep copy of c.
func (c WritableBookContent) Clone() WritableBookContent {
	if len(c.Pages) == 0 {
		return WritableBookContent{}
	}
	cp := make([]FilterableString, len(c.Pages))
	copy(cp, c.Pages)
	return WritableBookContent{Pages: cp}
}

// PlainPages returns the raw page strings of c.
func (c WritableBookContent) PlainPages() []string {
	out := make([]string, len(c.Pages))
	for i, p := range c.Pages {
		out[i] = p.Raw
	}
	return out
}

// FilterableComponent represents vanilla Filterable<Component> on a Written Book page.
type FilterableComponent struct {
	Raw         ChatComponent
	Filtered    ChatComponent
	HasFiltered bool
}

// WrittenBookContent represents the minecraft:written_book_content component on a signed Written Book.
type WrittenBookContent struct {
	Title      FilterableString
	Author     string
	Generation int32
	Pages      []FilterableComponent
	Resolved   bool
}

// Clone returns a deep copy of c.
func (c WrittenBookContent) Clone() WrittenBookContent {
	out := WrittenBookContent{
		Title:      c.Title,
		Author:     c.Author,
		Generation: c.Generation,
		Resolved:   c.Resolved,
	}
	if len(c.Pages) > 0 {
		out.Pages = make([]FilterableComponent, len(c.Pages))
		copy(out.Pages, c.Pages)
	}
	return out
}

// PlainPages returns the plain text of each page in c.
func (c WrittenBookContent) PlainPages() []string {
	out := make([]string, len(c.Pages))
	for i, p := range c.Pages {
		out[i] = p.Raw.PlainText()
	}
	return out
}

// CanBeCopied reports whether a written book with this content can be cloned in a crafting grid
// (vanilla WrittenBookContent: generation < 2, i.e. Original or Copy of original).
func (c WrittenBookContent) CanBeCopied() bool {
	return c.Generation >= 0 && c.Generation <= MaxCopyableBookGeneration
}

// --- Validation & Filtering ---

// SanitizeBookPage validates and cleans a single book page string (max 1024 runes).
func SanitizeBookPage(raw string) (string, error) {
	if utf8.RuneCountInString(raw) > MaxBookPageRunes {
		return "", fmt.Errorf("book page exceeds maximum length of %d characters", MaxBookPageRunes)
	}
	var b strings.Builder
	b.Grow(len(raw))
	for _, r := range raw {
		if r == 0 || r == '§' || (unicode.IsControl(r) && r != '\n' && r != '\t') {
			continue
		}
		b.WriteRune(r)
	}
	return b.String(), nil
}

// SanitizeBookTitle validates and cleans a book title for signing (1..32 runes, no newlines or control chars).
func SanitizeBookTitle(raw string) (string, error) {
	var b strings.Builder
	b.Grow(len(raw))
	for _, r := range raw {
		if r == 0 || r == '§' || unicode.IsControl(r) {
			continue
		}
		b.WriteRune(r)
	}
	cleaned := strings.TrimSpace(b.String())
	runeCount := utf8.RuneCountInString(cleaned)
	if runeCount < MinBookTitleRunes {
		return "", errors.New("book title cannot be empty")
	}
	if runeCount > MaxBookTitleRunes {
		return "", fmt.Errorf("book title exceeds maximum length of %d characters", MaxBookTitleRunes)
	}
	return cleaned, nil
}

// --- Network Stream Codecs for Book Data Components ---

func writeFilterableString(w *protocol.Writer, fs FilterableString) {
	w.String(fs.Raw)
	if fs.HasFiltered {
		w.Bool(true)
		w.String(fs.Filtered)
	} else {
		w.Bool(false)
	}
}

func readFilterableString(r *protocol.Reader, maxRunes int) (FilterableString, error) {
	raw, err := r.String()
	if err != nil {
		return FilterableString{}, err
	}
	if maxRunes > 0 && utf8.RuneCountInString(raw) > maxRunes {
		return FilterableString{}, fmt.Errorf("server: string length %d exceeds max %d", utf8.RuneCountInString(raw), maxRunes)
	}
	hasFilt, err := r.Bool()
	if err != nil {
		return FilterableString{}, err
	}
	var filt string
	if hasFilt {
		filt, err = r.String()
		if err != nil {
			return FilterableString{}, err
		}
		if maxRunes > 0 && utf8.RuneCountInString(filt) > maxRunes {
			return FilterableString{}, fmt.Errorf("server: filtered string length %d exceeds max %d", utf8.RuneCountInString(filt), maxRunes)
		}
	}
	return FilterableString{Raw: raw, Filtered: filt, HasFiltered: hasFilt}, nil
}

// EncodeWritableBookContentComponent writes the payload of DataComponentWritableBookContent (60).
func EncodeWritableBookContentComponent(w *protocol.Writer, c WritableBookContent) {
	pages := c.Pages
	if len(pages) > MaxBookPages {
		pages = pages[:MaxBookPages]
	}
	w.VarInt(int32(len(pages)))
	for _, p := range pages {
		writeFilterableString(w, p)
	}
}

// DecodeWritableBookContentComponent reads the payload of DataComponentWritableBookContent (60).
func DecodeWritableBookContentComponent(r *protocol.Reader) (WritableBookContent, error) {
	count, err := r.VarInt()
	if err != nil {
		return WritableBookContent{}, err
	}
	if count < 0 || count > MaxBookPages {
		return WritableBookContent{}, fmt.Errorf("server: invalid writable book page count %d", count)
	}
	pages := make([]FilterableString, count)
	for i := int32(0); i < count; i++ {
		fs, err := readFilterableString(r, MaxBookPageRunes)
		if err != nil {
			return WritableBookContent{}, err
		}
		pages[i] = fs
	}
	return WritableBookContent{Pages: pages}, nil
}

// EncodeWrittenBookContentComponent writes the payload of DataComponentWrittenBookContent (61).
func EncodeWrittenBookContentComponent(w *protocol.Writer, c WrittenBookContent) {
	writeFilterableString(w, c.Title)
	w.String(c.Author)
	gen := c.Generation
	if gen < 0 {
		gen = 0
	} else if gen > BookGenerationTattered {
		gen = BookGenerationTattered
	}
	w.VarInt(gen)
	pages := c.Pages
	if len(pages) > MaxBookPages {
		pages = pages[:MaxBookPages]
	}
	w.VarInt(int32(len(pages)))
	for _, p := range pages {
		w.Raw(p.Raw.MarshalNBT())
		if p.HasFiltered {
			w.Bool(true)
			w.Raw(p.Filtered.MarshalNBT())
		} else {
			w.Bool(false)
		}
	}
	w.Bool(c.Resolved)
}

// DecodeWrittenBookContentComponent reads the payload of DataComponentWrittenBookContent (61).
func DecodeWrittenBookContentComponent(r *protocol.Reader) (WrittenBookContent, error) {
	title, err := readFilterableString(r, MaxBookTitleRunes)
	if err != nil {
		return WrittenBookContent{}, err
	}
	author, err := r.String()
	if err != nil {
		return WrittenBookContent{}, err
	}
	if utf8.RuneCountInString(author) > MaxBookAuthorRunes {
		return WrittenBookContent{}, fmt.Errorf("server: author length exceeds max %d", MaxBookAuthorRunes)
	}
	gen, err := r.VarInt()
	if err != nil {
		return WrittenBookContent{}, err
	}
	if gen < 0 || gen > BookGenerationTattered {
		return WrittenBookContent{}, fmt.Errorf("server: invalid written book generation %d", gen)
	}
	count, err := r.VarInt()
	if err != nil {
		return WrittenBookContent{}, err
	}
	if count < 0 || count > MaxBookPages {
		return WrittenBookContent{}, fmt.Errorf("server: invalid written book page count %d", count)
	}
	pages := make([]FilterableComponent, count)
	for i := int32(0); i < count; i++ {
		rawComp, err := ReadChatComponentNBT(r)
		if err != nil {
			return WrittenBookContent{}, err
		}
		hasFilt, err := r.Bool()
		if err != nil {
			return WrittenBookContent{}, err
		}
		var filtComp ChatComponent
		if hasFilt {
			filtComp, err = ReadChatComponentNBT(r)
			if err != nil {
				return WrittenBookContent{}, err
			}
		}
		pages[i] = FilterableComponent{Raw: rawComp, Filtered: filtComp, HasFiltered: hasFilt}
	}
	resolved, err := r.Bool()
	if err != nil {
		return WrittenBookContent{}, err
	}
	return WrittenBookContent{
		Title:      title,
		Author:     author,
		Generation: gen,
		Pages:      pages,
		Resolved:   resolved,
	}, nil
}

// EncodeSlotWithBookContent writes a protocol 775 Slot carrying DataComponentWritableBookContent
// or DataComponentWrittenBookContent when present.
func EncodeSlotWithBookContent(w *protocol.Writer, stack world.ItemStack, writable *WritableBookContent, written *WrittenBookContent) {
	itemID := stack.ResolvedItemID()
	if stack.IsEmpty() || itemID <= 0 {
		w.VarInt(0)
		return
	}
	w.VarInt(stack.Count)
	w.VarInt(itemID)
	resolvedName := stack.ResolvedID()
	switch {
	case resolvedName == WritableBookItemID && writable != nil:
		w.VarInt(1) // 1 added component
		w.VarInt(0) // 0 removed components
		w.VarInt(DataComponentWritableBookContent)
		EncodeWritableBookContentComponent(w, *writable)
	case resolvedName == WrittenBookItemID && written != nil:
		w.VarInt(1) // 1 added component
		w.VarInt(0) // 0 removed components
		w.VarInt(DataComponentWrittenBookContent)
		EncodeWrittenBookContentComponent(w, *written)
	default:
		w.VarInt(0)
		w.VarInt(0)
	}
}

func encodeContainerSetSlotWithBook(containerID, stateID int32, slot int, stack world.ItemStack, writable *WritableBookContent, written *WrittenBookContent) []byte {
	w := protocol.NewWriter(64)
	w.VarInt(containerID)
	w.VarInt(stateID)
	w.Uint16(uint16(slot))
	EncodeSlotWithBookContent(w, stack, writable, written)
	return w.Bytes()
}

// --- NBT Serialization for Book Contents ---

// ToNBT serializes WritableBookContent to an NBT compound.
func (c WritableBookContent) ToNBT() *nbt.Compound {
	comp := nbt.NewCompound()
	elems := make([]nbt.Tag, 0, len(c.Pages))
	for _, p := range c.Pages {
		pc := nbt.NewCompound()
		pc.Set("raw", nbt.String(p.Raw))
		if p.HasFiltered {
			pc.Set("filtered", nbt.String(p.Filtered))
		}
		elems = append(elems, pc)
	}
	elemID := byte(nbt.TagCompound)
	if len(elems) == 0 {
		elemID = nbt.TagEnd
	}
	comp.Set("pages", nbt.List{ElemID: elemID, Elems: elems})
	return comp
}

// WritableBookContentFromNBT decodes a WritableBookContent from an NBT compound.
func WritableBookContentFromNBT(comp *nbt.Compound) WritableBookContent {
	if comp == nil {
		return WritableBookContent{}
	}
	list, ok := nbtListTag(comp, "pages")
	if !ok {
		return WritableBookContent{}
	}
	pages := make([]FilterableString, 0, len(list.Elems))
	for _, elem := range list.Elems {
		switch v := elem.(type) {
		case nbt.String:
			pages = append(pages, FilterableString{Raw: string(v)})
		case *nbt.Compound:
			raw, _ := nbtStringTag(v, "raw")
			filt, hasFilt := nbtStringTag(v, "filtered")
			pages = append(pages, FilterableString{Raw: raw, Filtered: filt, HasFiltered: hasFilt})
		}
	}
	return WritableBookContent{Pages: pages}
}

// ToNBT serializes WrittenBookContent to an NBT compound.
func (c WrittenBookContent) ToNBT() *nbt.Compound {
	comp := nbt.NewCompound()
	titleComp := nbt.NewCompound()
	titleComp.Set("raw", nbt.String(c.Title.Raw))
	if c.Title.HasFiltered {
		titleComp.Set("filtered", nbt.String(c.Title.Filtered))
	}
	comp.Set("title", titleComp)
	comp.Set("author", nbt.String(c.Author))
	comp.Set("generation", nbt.Int(c.Generation))
	comp.Set("resolved", boolToNBTByte(c.Resolved))

	elems := make([]nbt.Tag, 0, len(c.Pages))
	for _, p := range c.Pages {
		pc := nbt.NewCompound()
		pc.Set("raw", nbt.String(p.Raw.ToJSON()))
		if p.HasFiltered {
			pc.Set("filtered", nbt.String(p.Filtered.ToJSON()))
		}
		elems = append(elems, pc)
	}
	elemID := byte(nbt.TagCompound)
	if len(elems) == 0 {
		elemID = nbt.TagEnd
	}
	comp.Set("pages", nbt.List{ElemID: elemID, Elems: elems})
	return comp
}

// WrittenBookContentFromNBT decodes a WrittenBookContent from an NBT compound.
func WrittenBookContentFromNBT(comp *nbt.Compound) WrittenBookContent {
	if comp == nil {
		return WrittenBookContent{}
	}
	var out WrittenBookContent
	if tc, ok := nbtCompoundTag(comp, "title"); ok && tc != nil {
		raw, _ := nbtStringTag(tc, "raw")
		filt, hasFilt := nbtStringTag(tc, "filtered")
		out.Title = FilterableString{Raw: raw, Filtered: filt, HasFiltered: hasFilt}
	} else if ts, ok := nbtStringTag(comp, "title"); ok {
		out.Title = FilterableString{Raw: ts}
	}
	out.Author, _ = nbtStringTag(comp, "author")
	out.Generation, _ = nbtIntTag(comp, "generation")
	if rb, ok := nbtByteTag(comp, "resolved"); ok {
		out.Resolved = rb != 0
	}
	if list, ok := nbtListTag(comp, "pages"); ok {
		for _, elem := range list.Elems {
			switch v := elem.(type) {
			case nbt.String:
				out.Pages = append(out.Pages, FilterableComponent{Raw: ParseChatComponentOrText(string(v))})
			case *nbt.Compound:
				rawStr, _ := nbtStringTag(v, "raw")
				filtStr, hasFilt := nbtStringTag(v, "filtered")
				fc := FilterableComponent{
					Raw:         ParseChatComponentOrText(rawStr),
					HasFiltered: hasFilt,
				}
				if hasFilt {
					fc.Filtered = ParseChatComponentOrText(filtStr)
				}
				out.Pages = append(out.Pages, fc)
			}
		}
	}
	return out
}

// --- Per-Player Book Metadata Tracker ---

type sessionBookMetaTracker struct {
	mu          sync.Mutex
	writable    [world.PlayerInventorySlots]*WritableBookContent
	written     [world.PlayerInventorySlots]*WrittenBookContent
	carriedWrt  *WritableBookContent
	carriedSign *WrittenBookContent
}

var playerBookMetas sync.Map // map[*PlayerSession]*sessionBookMetaTracker

func getBookMetaTracker(session *PlayerSession) *sessionBookMetaTracker {
	if session == nil {
		return nil
	}
	if v, ok := playerBookMetas.Load(session); ok {
		return v.(*sessionBookMetaTracker)
	}
	actual, _ := playerBookMetas.LoadOrStore(session, &sessionBookMetaTracker{})
	return actual.(*sessionBookMetaTracker)
}

// GetSlotWritableBook returns the WritableBookContent stored on player inventory slot (0..45).
func GetSlotWritableBook(session *PlayerSession, slot int) (WritableBookContent, bool) {
	if session == nil || slot < 0 || slot >= world.PlayerInventorySlots {
		return WritableBookContent{}, false
	}
	tr := getBookMetaTracker(session)
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if tr.writable[slot] == nil {
		return WritableBookContent{}, false
	}
	return tr.writable[slot].Clone(), true
}

// SetSlotWritableBook stores a WritableBookContent on player inventory slot (0..45).
func SetSlotWritableBook(session *PlayerSession, slot int, content WritableBookContent) {
	if session == nil || slot < 0 || slot >= world.PlayerInventorySlots {
		return
	}
	cp := content.Clone()
	tr := getBookMetaTracker(session)
	tr.mu.Lock()
	tr.writable[slot] = &cp
	tr.written[slot] = nil
	tr.mu.Unlock()
}

// GetSlotWrittenBook returns the WrittenBookContent stored on player inventory slot (0..45).
func GetSlotWrittenBook(session *PlayerSession, slot int) (WrittenBookContent, bool) {
	if session == nil || slot < 0 || slot >= world.PlayerInventorySlots {
		return WrittenBookContent{}, false
	}
	tr := getBookMetaTracker(session)
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if tr.written[slot] == nil {
		return WrittenBookContent{}, false
	}
	return tr.written[slot].Clone(), true
}

// SetSlotWrittenBook stores a WrittenBookContent on player inventory slot (0..45).
func SetSlotWrittenBook(session *PlayerSession, slot int, content WrittenBookContent) {
	if session == nil || slot < 0 || slot >= world.PlayerInventorySlots {
		return
	}
	cp := content.Clone()
	tr := getBookMetaTracker(session)
	tr.mu.Lock()
	tr.written[slot] = &cp
	tr.writable[slot] = nil
	tr.mu.Unlock()
}

// ClearSlotBookMeta clears any stored book content metadata for player inventory slot (0..45).
func ClearSlotBookMeta(session *PlayerSession, slot int) {
	if session == nil || slot < 0 || slot >= world.PlayerInventorySlots {
		return
	}
	tr := getBookMetaTracker(session)
	tr.mu.Lock()
	tr.writable[slot] = nil
	tr.written[slot] = nil
	tr.mu.Unlock()
}

// MoveSlotBookMeta moves book metadata from fromSlot to toSlot.
func MoveSlotBookMeta(session *PlayerSession, fromSlot, toSlot int) {
	if session == nil || fromSlot == toSlot ||
		fromSlot < 0 || fromSlot >= world.PlayerInventorySlots ||
		toSlot < 0 || toSlot >= world.PlayerInventorySlots {
		return
	}
	tr := getBookMetaTracker(session)
	tr.mu.Lock()
	tr.writable[toSlot] = tr.writable[fromSlot]
	tr.written[toSlot] = tr.written[fromSlot]
	tr.writable[fromSlot] = nil
	tr.written[fromSlot] = nil
	tr.mu.Unlock()
}

// SwapSlotBookMetas swaps book metadata between slotA and slotB.
func SwapSlotBookMetas(session *PlayerSession, slotA, slotB int) {
	if session == nil || slotA == slotB ||
		slotA < 0 || slotA >= world.PlayerInventorySlots ||
		slotB < 0 || slotB >= world.PlayerInventorySlots {
		return
	}
	tr := getBookMetaTracker(session)
	tr.mu.Lock()
	tr.writable[slotA], tr.writable[slotB] = tr.writable[slotB], tr.writable[slotA]
	tr.written[slotA], tr.written[slotB] = tr.written[slotB], tr.written[slotA]
	tr.mu.Unlock()
}

// --- ServerboundEditBookPacket (0x18) & ClientboundOpenBookPacket (0x3a) ---

// EncodeEditBookPacket encodes a ServerboundEditBookPacket (0x18, protocol 775):
//   - VarInt(slot)
//   - VarInt(len(pages)) + String(page, max=1024)...
//   - Bool(hasTitle) + [String(title, max=32)]
func EncodeEditBookPacket(slot int32, pages []string, title *string) []byte {
	w := protocol.NewWriter(64 + len(pages)*32)
	w.VarInt(slot)
	w.VarInt(int32(len(pages)))
	for _, p := range pages {
		w.String(p)
	}
	if title != nil {
		w.Bool(true)
		w.String(*title)
	} else {
		w.Bool(false)
	}
	return w.Bytes()
}

// DecodeEditBookPacket decodes a ServerboundEditBookPacket (0x18, protocol 775).
func DecodeEditBookPacket(r *protocol.Reader) (slot int32, pages []string, title *string, err error) {
	slot, err = r.VarInt()
	if err != nil {
		return 0, nil, nil, err
	}
	count, err := r.VarInt()
	if err != nil {
		return 0, nil, nil, err
	}
	if count < 0 || count > MaxBookPages {
		return 0, nil, nil, fmt.Errorf("server: edit_book page count %d out of range [0..%d]", count, MaxBookPages)
	}
	pages = make([]string, count)
	for i := int32(0); i < count; i++ {
		p, err := r.String()
		if err != nil {
			return 0, nil, nil, err
		}
		if utf8.RuneCountInString(p) > MaxBookPageRunes {
			return 0, nil, nil, fmt.Errorf("server: edit_book page length %d exceeds %d", utf8.RuneCountInString(p), MaxBookPageRunes)
		}
		pages[i] = p
	}
	hasTitle, err := r.Bool()
	if err != nil {
		return 0, nil, nil, err
	}
	if hasTitle {
		t, err := r.String()
		if err != nil {
			return 0, nil, nil, err
		}
		if utf8.RuneCountInString(t) > MaxBookTitleRunes {
			return 0, nil, nil, fmt.Errorf("server: edit_book title length %d exceeds %d", utf8.RuneCountInString(t), MaxBookTitleRunes)
		}
		title = &t
	}
	return slot, pages, title, nil
}

func resolveEditBookInventorySlot(rawSlot int32) (int, bool) {
	switch {
	case rawSlot >= 0 && rawSlot < world.HotbarSlotCount:
		return world.HotbarContainerOffset + int(rawSlot), true
	case rawSlot >= world.HotbarContainerOffset && rawSlot < world.HotbarContainerOffset+world.HotbarSlotCount:
		return int(rawSlot), true
	case rawSlot == 40 || rawSlot == 45:
		return 45, true
	case rawSlot >= 9 && rawSlot < 36:
		return int(rawSlot), true
	default:
		return -1, false
	}
}

// HandleEditBookPacket processes a ServerboundEditBookPacket (0x18):
//   - Validates the held slot contains a minecraft:writable_book
//   - Sanitizes pages (up to 100 pages, 1024 runes each)
//   - If title is nil: updates WritableBookContent on the slot
//   - If title is non-nil: signs the book into a minecraft:written_book with
//     Author = player's name, Title = sanitized title (1..32 runes), Generation = 0 (Original),
//     and Pages converted to ChatComponents
func (s *Server) HandleEditBookPacket(session *PlayerSession, r *protocol.Reader) error {
	if s == nil || session == nil || session.IsDead() {
		return nil
	}
	rawSlot, rawPages, rawTitle, err := DecodeEditBookPacket(r)
	if err != nil {
		return err
	}
	invSlot, ok := resolveEditBookInventorySlot(rawSlot)
	if !ok {
		return nil
	}
	held := session.InventorySlot(invSlot)
	if held.IsEmpty() || held.ResolvedID() != WritableBookItemID {
		return nil
	}

	filteredPages := make([]FilterableString, 0, len(rawPages))
	for _, rp := range rawPages {
		clean, err := SanitizeBookPage(rp)
		if err != nil {
			return err
		}
		filteredPages = append(filteredPages, FilterableString{Raw: clean})
	}

	if rawTitle == nil {
		// Editing pages without signing.
		content := WritableBookContent{Pages: filteredPages}
		SetSlotWritableBook(session, invSlot, content)
		if session.send != nil {
			stateID := session.NextContainerStateID()
			_ = session.send(protocol.PlayContainerSetSlot, encodeContainerSetSlotWithBook(0, stateID, invSlot, held, &content, nil))
		}
		return nil
	}

	// Signing the book into a minecraft:written_book.
	cleanTitle, err := SanitizeBookTitle(*rawTitle)
	if err != nil {
		return nil
	}
	snap := session.Snapshot()
	compPages := make([]FilterableComponent, len(filteredPages))
	for i, fp := range filteredPages {
		compPages[i] = FilterableComponent{Raw: TextComponent(fp.Raw)}
	}
	written := WrittenBookContent{
		Title:      FilterableString{Raw: cleanTitle},
		Author:     snap.Profile.Name,
		Generation: BookGenerationOriginal,
		Pages:      compPages,
		Resolved:   true,
	}
	writtenStack := world.NewNamedItemStack(WrittenBookItemID, 1)
	session.SetInventorySlot(invSlot, writtenStack)
	SetSlotWrittenBook(session, invSlot, written)

	if session.send != nil {
		stateID := session.NextContainerStateID()
		_ = session.send(protocol.PlayContainerSetSlot, encodeContainerSetSlotWithBook(0, stateID, invSlot, writtenStack, nil, &written))
	}
	return nil
}

// EncodeOpenBookPacket encodes ClientboundOpenBookPacket (0x3a, protocol 775):
//   - VarInt(hand): 0 = main_hand, 1 = off_hand
func EncodeOpenBookPacket(hand int32) []byte {
	w := protocol.NewWriter(4)
	if hand != 1 {
		hand = 0
	}
	w.VarInt(hand)
	return w.Bytes()
}

// DecodeOpenBookPacket decodes ClientboundOpenBookPacket (0x3a, protocol 775).
func DecodeOpenBookPacket(r *protocol.Reader) (int32, error) {
	return r.VarInt()
}

// UseBookItem handles right-clicking (use_item) with a written_book in hand:
// syncs the slot's WrittenBookContent component to the client and sends
// ClientboundOpenBookPacket (0x3a) so the client opens the book reader UI.
func (s *Server) UseBookItem(session *PlayerSession, hand int32) bool {
	if s == nil || session == nil || session.IsDead() {
		return false
	}
	slot, held := session.handSlotAndStack(hand)
	if held.IsEmpty() {
		return false
	}
	switch held.ResolvedID() {
	case WrittenBookItemID:
		written, ok := GetSlotWrittenBook(session, slot)
		if ok && !written.Resolved {
			written.Resolved = true
			SetSlotWrittenBook(session, slot, written)
		}
		if session.send != nil {
			stateID := session.NextContainerStateID()
			var ptr *WrittenBookContent
			if ok {
				ptr = &written
			}
			_ = session.send(protocol.PlayContainerSetSlot, encodeContainerSetSlotWithBook(0, stateID, slot, held, nil, ptr))
			_ = session.send(PlayOpenBookCB, EncodeOpenBookPacket(hand))
		}
		return true
	default:
		return false
	}
}

// --- Written Book Copying in the Crafting Grid (BookCloningRecipe) ---

// MatchWrittenBookCloneRecipe checks whether a crafting grid (2x2 or 3x3)
// contains exactly 1 copyable minecraft:written_book (Generation <= 1) and
// N >= 1 minecraft:writable_book items (with all other slots empty).
// Returns:
//   - resultStack: minecraft:written_book with Count = N
//   - resultContent: cloned WrittenBookContent with Generation = source.Generation + 1
//   - writtenSlotIdx: index of the original written_book in grid (which remains in the grid as a crafting remainder!)
//   - matched: true if the grid matches BookCloningRecipe
func MatchWrittenBookCloneRecipe(grid []world.ItemStack, gridWritten []*WrittenBookContent) (resultStack world.ItemStack, resultContent WrittenBookContent, writtenSlotIdx int, matched bool) {
	writtenSlotIdx = -1
	writableCount := int32(0)
	var srcWritten WrittenBookContent

	for i, st := range grid {
		if st.IsEmpty() {
			continue
		}
		switch st.ResolvedID() {
		case WrittenBookItemID:
			if writtenSlotIdx != -1 {
				// More than one written_book in the grid is invalid.
				return world.ItemStack{}, WrittenBookContent{}, -1, false
			}
			if i < len(gridWritten) && gridWritten[i] != nil {
				srcWritten = gridWritten[i].Clone()
			}
			if !srcWritten.CanBeCopied() {
				// Generation >= 2 ("Copy of a copy") cannot be copied.
				return world.ItemStack{}, WrittenBookContent{}, -1, false
			}
			writtenSlotIdx = i
		case WritableBookItemID:
			writableCount++
		default:
			return world.ItemStack{}, WrittenBookContent{}, -1, false
		}
	}

	if writtenSlotIdx == -1 || writableCount < 1 {
		return world.ItemStack{}, WrittenBookContent{}, -1, false
	}

	outContent := srcWritten.Clone()
	outContent.Generation = srcWritten.Generation + 1
	outStack := world.NewNamedItemStack(WrittenBookItemID, writableCount)
	return outStack, outContent, writtenSlotIdx, true
}
