package world

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"regionio/internal/nbt"
)

// Furnace container slot indices matching vanilla AbstractFurnaceMenu.
const (
	FurnaceSlotInput  = 0
	FurnaceSlotFuel   = 1
	FurnaceSlotOutput = 2
	FurnaceSlotCount  = 3
)

// FurnaceBlockEntity represents the persistent and runtime state of a
// minecraft:furnace, minecraft:blast_furnace, or minecraft:smoker block entity.
type FurnaceBlockEntity struct {
	ID               string // "minecraft:furnace", "minecraft:blast_furnace", or "minecraft:smoker"
	X, Y, Z          int
	CustomName       string
	CustomNameTag    nbt.Tag
	Lock             string
	LockTag          nbt.Tag
	Items            [FurnaceSlotCount]ItemStack
	SlotComponents   [FurnaceSlotCount]*nbt.Compound
	LitTimeRemaining int16 // NBT "lit_time_remaining" (ContainerData index 0)
	LitTotalTime     int16 // NBT "lit_total_time"     (ContainerData index 1)
	CookingTimer     int16 // NBT "cooking_time_spent" (ContainerData index 2)
	CookingTotalTime int16 // NBT "cooking_total_time" (ContainerData index 3)
	RecipesUsed      map[string]int32
}

// Clone returns a deep copy of f.
func (f FurnaceBlockEntity) Clone() FurnaceBlockEntity {
	out := f
	out.CustomNameTag = CloneNBTTag(f.CustomNameTag)
	out.LockTag = CloneNBTTag(f.LockTag)
	for i := range f.SlotComponents {
		out.SlotComponents[i] = CloneNBTCompound(f.SlotComponents[i])
	}
	if len(f.RecipesUsed) > 0 {
		out.RecipesUsed = make(map[string]int32, len(f.RecipesUsed))
		for k, v := range f.RecipesUsed {
			out.RecipesUsed[k] = v
		}
	}
	return out
}

// CloneNBTTag returns a deep copy of tag.
func CloneNBTTag(tag nbt.Tag) nbt.Tag {
	if tag == nil {
		return nil
	}
	switch v := tag.(type) {
	case *nbt.Compound:
		return CloneNBTCompound(v)
	case nbt.List:
		elems := make([]nbt.Tag, len(v.Elems))
		for i, e := range v.Elems {
			elems[i] = CloneNBTTag(e)
		}
		return nbt.List{ElemID: v.ElemID, Elems: elems}
	case nbt.ByteArray:
		return append(nbt.ByteArray(nil), v...)
	case nbt.IntArray:
		return append(nbt.IntArray(nil), v...)
	case nbt.LongArray:
		return append(nbt.LongArray(nil), v...)
	default:
		return tag
	}
}

// CloneNBTCompound returns a deep copy of c.
func CloneNBTCompound(c *nbt.Compound) *nbt.Compound {
	if c == nil {
		return nil
	}
	out := nbt.NewCompound()
	for _, k := range c.Keys() {
		if v, ok := c.Get(k); ok {
			out.Set(k, CloneNBTTag(v))
		}
	}
	return out
}

// ExtractCustomNameFromNBTTag extracts a human-readable string from a vanilla
// ComponentSerialization.CODEC NBT tag (String or Compound).
func ExtractCustomNameFromNBTTag(tag nbt.Tag) string {
	switch v := tag.(type) {
	case nbt.String:
		return string(v)
	case *nbt.Compound:
		if t, ok := v.Get("text"); ok {
			if str, ok := t.(nbt.String); ok {
				return string(str)
			}
		}
	}
	return ""
}

// ExtractLockStringFromNBTTag extracts a human-readable lock string from a
// vanilla 26.1.2 LockCode NBT tag (ItemPredicate Compound or legacy String).
func ExtractLockStringFromNBTTag(tag nbt.Tag) string {
	switch v := tag.(type) {
	case nbt.String:
		return string(v)
	case *nbt.Compound:
		if compsTag, ok := v.Get("components"); ok {
			if comps, ok := compsTag.(*nbt.Compound); ok {
				if cnTag, ok := comps.Get("minecraft:custom_name"); ok {
					if s := ExtractCustomNameFromNBTTag(cnTag); s != "" {
						return s
					}
				}
			}
		}
		if itemsTag, ok := v.Get("items"); ok {
			if s, ok := itemsTag.(nbt.String); ok {
				return string(s)
			}
		}
	}
	return ""
}

// IsFurnaceBlockID reports whether id is one of the three furnace block/entity IDs.
func IsFurnaceBlockID(id string) bool {
	switch normalizeItemName(id) {
	case "minecraft:furnace", "minecraft:blast_furnace", "minecraft:smoker":
		return true
	default:
		return false
	}
}

// IsFurnaceBlockState reports whether state is a furnace, blast_furnace, or smoker.
func IsFurnaceBlockState(state uint16) bool {
	_, ok := FurnaceBlockEntityIDForState(state)
	return ok
}

// FurnaceBlockEntityIDForState returns the canonical block entity ID
// ("minecraft:furnace", "minecraft:blast_furnace", or "minecraft:smoker") for
// a block state.
func FurnaceBlockEntityIDForState(state uint16) (string, bool) {
	info, ok := stateByID(state)
	if !ok {
		return "", false
	}
	switch info.Name {
	case "minecraft:furnace", "minecraft:blast_furnace", "minecraft:smoker":
		return info.Name, true
	default:
		return "", false
	}
}

// FurnaceStateLit reports whether state has property lit="true".
func FurnaceStateLit(state uint16) bool {
	info, ok := stateByID(state)
	if !ok {
		return false
	}
	return info.Properties["lit"] == "true"
}

// SetFurnaceStateLit returns the block state ID for the furnace at state with
// its "lit" property set to lit, preserving "facing".
func SetFurnaceStateLit(state uint16, lit bool) (uint16, bool) {
	info, ok := stateByID(state)
	if !ok || !IsFurnaceBlockID(info.Name) {
		return state, false
	}
	facing := info.Properties["facing"]
	if facing == "" {
		facing = "north"
	}
	litStr := "false"
	if lit {
		litStr = "true"
	}
	return nameToStateID(info.Name, map[string]string{
		"facing": facing,
		"lit":    litStr,
	})
}

// EncodeFurnaceBlockEntityNBT serializes f into a vanilla 26.1.2 chunk
// block_entities compound tag.
func EncodeFurnaceBlockEntityNBT(f FurnaceBlockEntity) *nbt.Compound {
	id := normalizeItemName(f.ID)
	if !IsFurnaceBlockID(id) {
		id = "minecraft:furnace"
	}
	c := nbt.NewCompound().
		Set("id", nbt.String(id)).
		Set("x", nbt.Int(int32(f.X))).
		Set("y", nbt.Int(int32(f.Y))).
		Set("z", nbt.Int(int32(f.Z))).
		Set("keepPacked", nbt.Byte(0)).
		Set("cooking_time_spent", nbt.Short(f.CookingTimer)).
		Set("cooking_total_time", nbt.Short(f.CookingTotalTime)).
		Set("lit_time_remaining", nbt.Short(f.LitTimeRemaining)).
		Set("lit_total_time", nbt.Short(f.LitTotalTime))
	if f.CustomNameTag != nil && (f.CustomName == "" || ExtractCustomNameFromNBTTag(f.CustomNameTag) == f.CustomName) {
		c.Set("CustomName", CloneNBTTag(f.CustomNameTag))
	} else if f.CustomName != "" {
		c.Set("CustomName", nbt.String(f.CustomName))
	}
	if f.LockTag != nil && (f.Lock == "" || ExtractLockStringFromNBTTag(f.LockTag) == f.Lock) {
		c.Set("lock", CloneNBTTag(f.LockTag))
	} else if f.Lock != "" {
		c.Set("lock", nbt.String(f.Lock))
	}

	items := nbt.List{ElemID: nbt.TagCompound}
	for slot := 0; slot < FurnaceSlotCount; slot++ {
		st := f.Items[slot]
		if st.IsEmpty() {
			continue
		}
		itemName := st.ResolvedID()
		if itemName == "" || itemName == "minecraft:air" {
			continue
		}
		entry := nbt.NewCompound().
			Set("Slot", nbt.Byte(int8(slot))).
			Set("id", nbt.String(itemName)).
			Set("count", nbt.Int(st.Count))
		if f.SlotComponents[slot] != nil && f.SlotComponents[slot].Len() > 0 {
			entry.Set("components", CloneNBTCompound(f.SlotComponents[slot]))
		}
		items.Elems = append(items.Elems, entry)
	}
	c.Set("Items", items)

	recipesUsed := nbt.NewCompound()
	if len(f.RecipesUsed) > 0 {
		keys := make([]string, 0, len(f.RecipesUsed))
		for k, v := range f.RecipesUsed {
			if v > 0 {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		for _, k := range keys {
			recipesUsed.Set(normalizeItemName(k), nbt.Int(f.RecipesUsed[k]))
		}
	}
	c.Set("RecipesUsed", recipesUsed)
	return c
}

// DecodeFurnaceBlockEntityNBT decodes a vanilla 26.1.2 (or legacy) furnace
// block entity compound tag.
func DecodeFurnaceBlockEntityNBT(c *nbt.Compound) (FurnaceBlockEntity, bool) {
	if c == nil {
		return FurnaceBlockEntity{}, false
	}
	idTag, hasID := c.Get("id")
	if !hasID {
		return FurnaceBlockEntity{}, false
	}
	idStr, ok := idTag.(nbt.String)
	if !ok {
		return FurnaceBlockEntity{}, false
	}
	id := normalizeItemName(string(idStr))
	if !IsFurnaceBlockID(id) {
		return FurnaceBlockEntity{}, false
	}
	f := FurnaceBlockEntity{
		ID:               id,
		X:                int(nbtAsInt(c, "x")),
		Y:                int(nbtAsInt(c, "y")),
		Z:                int(nbtAsInt(c, "z")),
		CookingTimer:     nbtShortOrInt(c, "cooking_time_spent", "CookTime"),
		CookingTotalTime: nbtShortOrInt(c, "cooking_total_time", "CookTimeTotal"),
		LitTimeRemaining: nbtShortOrInt(c, "lit_time_remaining", "BurnTime"),
		LitTotalTime:     nbtShortOrInt(c, "lit_total_time", "BurnTimeTotal"),
	}
	if cnTag, ok := c.Get("CustomName"); ok {
		f.CustomNameTag = CloneNBTTag(cnTag)
		f.CustomName = ExtractCustomNameFromNBTTag(cnTag)
	}
	if lockTag, ok := c.Get("lock"); ok {
		f.LockTag = CloneNBTTag(lockTag)
		f.Lock = ExtractLockStringFromNBTTag(lockTag)
	} else if lockTag, ok := c.Get("Lock"); ok {
		f.LockTag = CloneNBTTag(lockTag)
		f.Lock = ExtractLockStringFromNBTTag(lockTag)
	}

	if itemsTag, ok := c.Get("Items"); ok {
		if list, ok := itemsTag.(nbt.List); ok && (list.ElemID == nbt.TagCompound || len(list.Elems) == 0) {
			for _, elem := range list.Elems {
				ic, ok := elem.(*nbt.Compound)
				if !ok {
					continue
				}
				slot, ok := nbtSlotIndex(ic, "Slot")
				if !ok || slot < 0 || slot >= FurnaceSlotCount {
					continue
				}
				itemIDStr := string(nbtAsString(ic, "id"))
				count := nbtItemCount(ic)
				if count <= 0 || itemIDStr == "" || itemIDStr == "minecraft:air" {
					continue
				}
				f.Items[slot] = NewNamedItemStack(itemIDStr, count)
				if compTag, ok := ic.Get("components"); ok {
					if comp, ok := compTag.(*nbt.Compound); ok && comp.Len() > 0 {
						f.SlotComponents[slot] = CloneNBTCompound(comp)
					}
				}
			}
		}
	}

	if ruTag, ok := c.Get("RecipesUsed"); ok {
		if ruComp, ok := ruTag.(*nbt.Compound); ok && ruComp.Len() > 0 {
			f.RecipesUsed = make(map[string]int32, ruComp.Len())
			for _, k := range ruComp.Keys() {
				if v := nbtAsInt(ruComp, k); v > 0 {
					f.RecipesUsed[normalizeItemName(k)] = v
				}
			}
		}
	}
	return f, true
}

func nbtShortOrInt(c *nbt.Compound, primary, fallback string) int16 {
	for _, key := range []string{primary, fallback} {
		if key == "" {
			continue
		}
		tag, ok := c.Get(key)
		if !ok {
			continue
		}
		switch v := tag.(type) {
		case nbt.Short:
			return int16(v)
		case nbt.Int:
			return int16(v)
		case nbt.Byte:
			return int16(v)
		}
	}
	return 0
}

func nbtItemCount(c *nbt.Compound) int32 {
	for _, key := range []string{"count", "Count"} {
		tag, ok := c.Get(key)
		if !ok {
			continue
		}
		switch v := tag.(type) {
		case nbt.Int:
			return int32(v)
		case nbt.Byte:
			return int32(v)
		case nbt.Short:
			return int32(v)
		}
	}
	return 0
}

func nbtSlotIndex(c *nbt.Compound, key string) (int, bool) {
	tag, ok := c.Get(key)
	if !ok {
		return 0, false
	}
	switch v := tag.(type) {
	case nbt.Byte:
		return int(uint8(v)), true
	case nbt.Short:
		return int(v), true
	case nbt.Int:
		return int(v), true
	default:
		return 0, false
	}
}

// RawBlockEntities returns a deep copy of all raw block_entities NBT compounds
// attached to this chunk.
func (c *Chunk) RawBlockEntities() []*nbt.Compound {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if len(c.rawBlockEntities) == 0 {
		return nil
	}
	out := make([]*nbt.Compound, 0, len(c.rawBlockEntities))
	for _, be := range c.rawBlockEntities {
		if be != nil {
			out = append(out, CloneNBTCompound(be))
		}
	}
	return out
}

// ReplaceRawBlockEntities replaces all raw block_entities compounds on c whose
// canonical "id" satisfies matchID with clones of replacements.
func (c *Chunk) ReplaceRawBlockEntities(matchID func(string) bool, replacements []*nbt.Compound) {
	if c == nil || matchID == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	kept := make([]*nbt.Compound, 0, len(c.rawBlockEntities)+len(replacements))
	for _, existing := range c.rawBlockEntities {
		if existing == nil {
			continue
		}
		id := normalizeItemName(string(nbtAsString(existing, "id")))
		if !matchID(id) {
			kept = append(kept, existing)
		}
	}
	for _, rep := range replacements {
		if rep != nil {
			kept = append(kept, CloneNBTCompound(rep))
		}
	}
	c.rawBlockEntities = kept
	c.revision.Add(1)
}

// Store returns the underlying persistent Store attached to c, or nil.
func (c *Cache) Store() *Store {
	if c == nil {
		return nil
	}
	return c.store
}

// UnloadChunk flushes any dirty state for chunk (cx, cz) to the backing Store
// and evicts the chunk from in-memory cache so the next access reloads from disk.
func (c *Cache) UnloadChunk(cx, cz int32) error {
	if c == nil {
		return nil
	}
	key := [2]int32{cx, cz}
	c.mu.Lock()
	ch := c.chunks[key]
	_, isDirty := c.dirty[key]
	isUnsaved := c.unsavedGen[key]
	c.mu.Unlock()

	if ch != nil && c.store != nil && (isDirty || isUnsaved) {
		snapshot, _ := ch.snapshot()
		if _, hasSched := cacheSchedulers.Load(c); hasSched {
			snapshot.persistedTicks = c.ExportChunkScheduledBlockTicks(cx, cz)
			snapshot.persistedFallingBlocks = c.ExportChunkFallingBlocks(cx, cz)
		}
		c.syncChunkPistonBlockEntitiesToSnapshot(cx, cz, snapshot)
		c.syncChunkSculkToSnapshot(cx, cz, snapshot)
		if err := c.store.saveSnapshot(snapshot); err != nil {
			return err
		}
	}

	c.mu.Lock()
	delete(c.chunks, key)
	delete(c.frames, key)
	delete(c.dirty, key)
	delete(c.unsavedGen, key)
	if c.order != nil && c.index != nil {
		if elem, ok := c.index[key]; ok {
			c.order.Remove(elem)
			delete(c.index, key)
		}
	}
	c.mu.Unlock()
	return nil
}

// LoadVanillaChunk loads chunk (cx, cz) from the backing Store without requiring
// RegionIO's generator stamp, caching and returning the loaded Chunk.
func (c *Cache) LoadVanillaChunk(cx, cz int32) (*Chunk, error) {
	if c == nil || c.store == nil {
		return nil, ErrChunkNotFound
	}
	ch, err := c.store.LoadVanillaChunk(cx, cz)
	if err != nil {
		return nil, err
	}
	key := [2]int32{cx, cz}
	c.mu.Lock()
	c.chunks[key] = ch
	delete(c.frames, key)
	c.touch(key)
	c.mu.Unlock()
	return ch, nil
}

// SaveChunkBlockEntities replaces all block_entities in chunk (cx, cz) whose
// canonical "id" satisfies matchID with list, updating both the in-memory Chunk
// and the on-disk Anvil chunk NBT.
func (c *Cache) SaveChunkBlockEntities(cx, cz int32, matchID func(string) bool, list []*nbt.Compound) error {
	if c == nil || matchID == nil {
		return nil
	}
	key := [2]int32{cx, cz}
	c.startUse(key)
	defer c.endUse(key)

	ch, err := c.chunkAtErr(cx, cz)
	if err != nil {
		return err
	}
	ch.ReplaceRawBlockEntities(matchID, list)
	c.mu.Lock()
	delete(c.frames, key)
	c.mu.Unlock()
	if c.store == nil {
		return nil
	}
	snapshot, savedRevision := ch.snapshot()
	if _, hasSched := cacheSchedulers.Load(c); hasSched {
		snapshot.persistedTicks = c.ExportChunkScheduledBlockTicks(cx, cz)
		snapshot.persistedFallingBlocks = c.ExportChunkFallingBlocks(cx, cz)
	}
	c.syncChunkPistonBlockEntitiesToSnapshot(cx, cz, snapshot)
	c.syncChunkSculkToSnapshot(cx, cz, snapshot)
	if err := c.store.SaveChunkBlockEntities(cx, cz, snapshot, matchID, list); err != nil {
		return err
	}
	c.mu.Lock()
	if dirtyRevision, ok := c.dirty[key]; ok && dirtyRevision <= savedRevision {
		delete(c.dirty, key)
	}
	c.evictIfNeeded()
	c.mu.Unlock()
	return nil
}

// LoadChunkBlockEntities returns all block_entities compounds in chunk (cx, cz)
// whose canonical "id" satisfies matchID, reading from the backing Store (if any)
// and falling back to the in-memory Chunk's rawBlockEntities.
func (c *Cache) LoadChunkBlockEntities(cx, cz int32, matchID func(string) bool) ([]*nbt.Compound, error) {
	if c == nil || matchID == nil {
		return nil, nil
	}
	byPos := make(map[[3]int]*nbt.Compound)
	c.mu.Lock()
	memChunk := c.chunks[[2]int32{cx, cz}]
	c.mu.Unlock()
	if memChunk != nil {
		for _, comp := range memChunk.RawBlockEntities() {
			id := normalizeItemName(string(nbtAsString(comp, "id")))
			if matchID(id) {
				pos := [3]int{int(nbtAsInt(comp, "x")), int(nbtAsInt(comp, "y")), int(nbtAsInt(comp, "z"))}
				byPos[pos] = comp
			}
		}
	}
	if c.store != nil {
		persisted, err := c.store.LoadChunkBlockEntities(cx, cz, matchID)
		if err != nil {
			return nil, err
		}
		for _, comp := range persisted {
			pos := [3]int{int(nbtAsInt(comp, "x")), int(nbtAsInt(comp, "y")), int(nbtAsInt(comp, "z"))}
			byPos[pos] = comp
		}
	}
	return sortedBlockEntityCompounds(byPos), nil
}

// SaveChunkBlockEntities persists all block_entities in chunk (cx, cz) matching
// matchID into the chunk's Anvil "block_entities" NBT list on disk, preserving
// all non-matching block entities already present in the chunk.
func (s *Store) SaveChunkBlockEntities(cx, cz int32, snapshot *Chunk, matchID func(string) bool, replacements []*nbt.Compound) error {
	if s == nil || matchID == nil {
		return nil
	}
	rf, err := s.regionFor(cx, cz)
	if err != nil {
		return err
	}
	_, _, lx, lz := regionIndex(cx, cz)

	byPos := make(map[[3]int]*nbt.Compound)
	if snapshot != nil {
		for _, comp := range snapshot.RawBlockEntities() {
			id := normalizeItemName(string(nbtAsString(comp, "id")))
			if !matchID(id) {
				pos := [3]int{int(nbtAsInt(comp, "x")), int(nbtAsInt(comp, "y")), int(nbtAsInt(comp, "z"))}
				byPos[pos] = comp
			}
		}
	}
	if existingRaw, err := rf.ReadChunk(lx, lz); err == nil {
		if _, tag, err := nbt.UnmarshalNamed(existingRaw); err == nil {
			if existingRoot, ok := tag.(*nbt.Compound); ok {
				if beTag, ok := existingRoot.Get("block_entities"); ok {
					if list, ok := beTag.(nbt.List); ok && list.ElemID == nbt.TagCompound {
						for _, elem := range list.Elems {
							ec, ok := elem.(*nbt.Compound)
							if !ok {
								continue
							}
							id := normalizeItemName(string(nbtAsString(ec, "id")))
							if !matchID(id) {
								pos := [3]int{int(nbtAsInt(ec, "x")), int(nbtAsInt(ec, "y")), int(nbtAsInt(ec, "z"))}
								byPos[pos] = CloneNBTCompound(ec)
							}
						}
					}
				}
			}
		}
	}
	for _, rep := range replacements {
		if rep == nil {
			continue
		}
		x := int(nbtAsInt(rep, "x"))
		y := int(nbtAsInt(rep, "y"))
		z := int(nbtAsInt(rep, "z"))
		if int32(x>>4) == cx && int32(z>>4) == cz {
			byPos[[3]int{x, y, z}] = CloneNBTCompound(rep)
		}
	}
	if snapshot != nil {
		snapshot.fillMissingDefaultShowcaseBlockEntities(byPos)
	}

	var root *nbt.Compound
	if snapshot != nil {
		root = chunkToNBT(snapshot)
	} else if existingRaw, err := rf.ReadChunk(lx, lz); err == nil {
		if _, tag, err := nbt.UnmarshalNamed(existingRaw); err == nil {
			root, _ = tag.(*nbt.Compound)
		}
	}
	if root == nil {
		return nil
	}

	sorted := sortedBlockEntityCompounds(byPos)
	beList := nbt.List{
		ElemID: nbt.TagCompound,
		Elems:  make([]nbt.Tag, 0, len(sorted)),
	}
	for _, comp := range sorted {
		beList.Elems = append(beList.Elems, comp)
	}
	root.Set("block_entities", beList)
	raw := nbt.MarshalNamed("", root)
	return rf.WriteChunk(lx, lz, raw)
}

// LoadChunkBlockEntities reads all block_entities compounds in chunk (cx, cz)
// whose canonical "id" satisfies matchID.
func (s *Store) LoadChunkBlockEntities(cx, cz int32, matchID func(string) bool) ([]*nbt.Compound, error) {
	if s == nil || matchID == nil {
		return nil, nil
	}
	rf, err := s.regionFor(cx, cz)
	if err != nil {
		return nil, nil
	}
	_, _, lx, lz := regionIndex(cx, cz)
	raw, err := rf.ReadChunk(lx, lz)
	if err != nil {
		return nil, nil
	}
	_, tag, err := nbt.UnmarshalNamed(raw)
	if err != nil {
		return nil, fmt.Errorf("world: decode chunk (%d,%d) block_entities NBT: %w", cx, cz, err)
	}
	root, ok := tag.(*nbt.Compound)
	if !ok {
		return nil, nil
	}
	return ExtractBlockEntitiesFromChunkRoot(root, matchID), nil
}

// LoadAllBlockEntities scans all region files in the store and returns every
// persisted block_entities compound whose canonical "id" satisfies matchID.
func (s *Store) LoadAllBlockEntities(matchID func(string) bool) ([]*nbt.Compound, error) {
	if s == nil || matchID == nil {
		return nil, nil
	}
	byPos := make(map[[3]int]*nbt.Compound)
	regionDir := filepath.Join(s.dir, "region")
	entries, err := os.ReadDir(regionDir)
	if err != nil {
		return nil, nil
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		rx, rz, ok := parseRegionFileName(entry.Name())
		if !ok {
			continue
		}
		rf, err := s.regionFor(int32(rx*32), int32(rz*32))
		if err != nil {
			continue
		}
		for lz := 0; lz < 32; lz++ {
			for lx := 0; lx < 32; lx++ {
				raw, err := rf.ReadChunk(lx, lz)
				if err != nil {
					continue
				}
				_, tag, err := nbt.UnmarshalNamed(raw)
				if err != nil {
					continue
				}
				root, ok := tag.(*nbt.Compound)
				if !ok {
					continue
				}
				for _, comp := range ExtractBlockEntitiesFromChunkRoot(root, matchID) {
					pos := [3]int{int(nbtAsInt(comp, "x")), int(nbtAsInt(comp, "y")), int(nbtAsInt(comp, "z"))}
					byPos[pos] = comp
				}
			}
		}
	}
	return sortedBlockEntityCompounds(byPos), nil
}

// ExtractBlockEntitiesFromChunkRoot returns deep copies of all compounds in
// root's "block_entities" list whose canonical "id" satisfies matchID (or all
// compounds if matchID is nil).
func ExtractBlockEntitiesFromChunkRoot(root *nbt.Compound, matchID func(string) bool) []*nbt.Compound {
	if root == nil {
		return nil
	}
	beTag, ok := root.Get("block_entities")
	if !ok {
		return nil
	}
	list, ok := beTag.(nbt.List)
	if !ok || list.ElemID != nbt.TagCompound {
		return nil
	}
	var out []*nbt.Compound
	for _, elem := range list.Elems {
		ec, ok := elem.(*nbt.Compound)
		if !ok {
			continue
		}
		idTag, hasID := ec.Get("id")
		if !hasID {
			continue
		}
		idStr, ok := idTag.(nbt.String)
		if !ok {
			continue
		}
		if matchID == nil || matchID(normalizeItemName(string(idStr))) {
			out = append(out, CloneNBTCompound(ec))
		}
	}
	return out
}

func sortedBlockEntityCompounds(byPos map[[3]int]*nbt.Compound) []*nbt.Compound {
	if len(byPos) == 0 {
		return nil
	}
	positions := make([][3]int, 0, len(byPos))
	for pos := range byPos {
		positions = append(positions, pos)
	}
	sort.Slice(positions, func(i, j int) bool {
		if positions[i][1] != positions[j][1] {
			return positions[i][1] < positions[j][1]
		}
		if positions[i][2] != positions[j][2] {
			return positions[i][2] < positions[j][2]
		}
		return positions[i][0] < positions[j][0]
	})
	out := make([]*nbt.Compound, 0, len(positions))
	for _, pos := range positions {
		out = append(out, CloneNBTCompound(byPos[pos]))
	}
	return out
}

// SaveChunkFurnaces persists all furnace block entities belonging to chunk
// (cx, cz) directly into the chunk's Anvil "block_entities" NBT list on disk,
// and also updates the world's furnace index so block edits saved via
// Cache.SaveAll do not drop furnace state.
func (c *Cache) SaveChunkFurnaces(cx, cz int32, furnaces []FurnaceBlockEntity) error {
	if c == nil {
		return nil
	}
	key := [2]int32{cx, cz}
	c.startUse(key)
	defer c.endUse(key)

	ch, err := c.chunkAtErr(cx, cz)
	if err != nil {
		return err
	}
	compounds := make([]*nbt.Compound, 0, len(furnaces))
	for _, f := range furnaces {
		if int32(f.X>>4) == cx && int32(f.Z>>4) == cz {
			compounds = append(compounds, EncodeFurnaceBlockEntityNBT(f))
		}
	}
	ch.ReplaceRawBlockEntities(IsFurnaceBlockID, compounds)
	if c.store == nil {
		return nil
	}
	snapshot, savedRevision := ch.snapshot()
	if err := c.store.saveChunkWithFurnaces(snapshot, furnaces); err != nil {
		return err
	}

	c.mu.Lock()
	if dirtyRevision, ok := c.dirty[key]; ok && dirtyRevision <= savedRevision {
		delete(c.dirty, key)
	}
	c.evictIfNeeded()
	c.mu.Unlock()
	return nil
}

// LoadChunkFurnaces returns all furnace block entities for chunk (cx, cz),
// combining in-memory Chunk rawBlockEntities and the backing Store.
func (c *Cache) LoadChunkFurnaces(cx, cz int32) ([]FurnaceBlockEntity, error) {
	if c == nil {
		return nil, nil
	}
	byPos := make(map[[3]int]FurnaceBlockEntity)
	c.mu.Lock()
	memChunk := c.chunks[[2]int32{cx, cz}]
	c.mu.Unlock()
	if memChunk != nil {
		for _, comp := range memChunk.RawBlockEntities() {
			if f, ok := DecodeFurnaceBlockEntityNBT(comp); ok {
				byPos[[3]int{f.X, f.Y, f.Z}] = f
			}
		}
	}
	if c.store != nil {
		persisted, err := c.store.LoadChunkFurnaces(cx, cz)
		if err != nil {
			return nil, err
		}
		for _, f := range persisted {
			byPos[[3]int{f.X, f.Y, f.Z}] = f
		}
	}
	return mapValuesSorted(byPos), nil
}

func (s *Store) saveChunkWithFurnaces(snapshot *Chunk, furnaces []FurnaceBlockEntity) error {
	if s == nil || snapshot == nil {
		return nil
	}
	rf, err := s.regionFor(snapshot.X, snapshot.Z)
	if err != nil {
		return err
	}
	_, _, lx, lz := regionIndex(snapshot.X, snapshot.Z)

	// Preserve any non-furnace block entities already stored in the chunk NBT.
	var preserved []nbt.Tag
	if existingRaw, err := rf.ReadChunk(lx, lz); err == nil {
		if _, tag, err := nbt.UnmarshalNamed(existingRaw); err == nil {
			if existingRoot, ok := tag.(*nbt.Compound); ok {
				if beTag, ok := existingRoot.Get("block_entities"); ok {
					if list, ok := beTag.(nbt.List); ok && list.ElemID == nbt.TagCompound {
						for _, elem := range list.Elems {
							ec, ok := elem.(*nbt.Compound)
							if !ok {
								continue
							}
							if _, isFurnace := DecodeFurnaceBlockEntityNBT(ec); !isFurnace {
								preserved = append(preserved, ec)
							}
						}
					}
				}
			}
		}
	}

	sorted := make([]FurnaceBlockEntity, 0, len(furnaces))
	for _, f := range furnaces {
		if int32(f.X>>4) == snapshot.X && int32(f.Z>>4) == snapshot.Z {
			sorted = append(sorted, f.Clone())
		}
	}
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Y != sorted[j].Y {
			return sorted[i].Y < sorted[j].Y
		}
		if sorted[i].Z != sorted[j].Z {
			return sorted[i].Z < sorted[j].Z
		}
		return sorted[i].X < sorted[j].X
	})

	beList := nbt.List{
		ElemID: nbt.TagCompound,
		Elems:  make([]nbt.Tag, 0, len(preserved)+len(sorted)),
	}
	beList.Elems = append(beList.Elems, preserved...)
	for _, f := range sorted {
		beList.Elems = append(beList.Elems, EncodeFurnaceBlockEntityNBT(f))
	}

	root := chunkToNBT(snapshot)
	root.Set("block_entities", beList)
	raw := nbt.MarshalNamed("", root)
	if err := rf.WriteChunk(lx, lz, raw); err != nil {
		return err
	}
	return s.updateSidecarChunkFurnaces(snapshot.X, snapshot.Z, sorted)
}

// LoadChunkFurnaces reads all furnace block entities persisted in chunk (cx, cz).
func (s *Store) LoadChunkFurnaces(cx, cz int32) ([]FurnaceBlockEntity, error) {
	if s == nil {
		return nil, nil
	}
	byPos := make(map[[3]int]FurnaceBlockEntity)

	if sidecar, err := s.loadSidecarFurnaces(); err == nil {
		for _, f := range sidecar {
			if int32(f.X>>4) == cx && int32(f.Z>>4) == cz {
				byPos[[3]int{f.X, f.Y, f.Z}] = f
			}
		}
	}

	rf, err := s.regionFor(cx, cz)
	if err != nil {
		return mapValuesSorted(byPos), nil
	}
	_, _, lx, lz := regionIndex(cx, cz)
	raw, err := rf.ReadChunk(lx, lz)
	if err != nil {
		return mapValuesSorted(byPos), nil
	}
	_, tag, err := nbt.UnmarshalNamed(raw)
	if err != nil {
		return nil, fmt.Errorf("world: decode chunk (%d,%d) block_entities NBT: %w", cx, cz, err)
	}
	root, ok := tag.(*nbt.Compound)
	if !ok {
		return mapValuesSorted(byPos), nil
	}
	for _, f := range extractFurnacesFromChunkRoot(root) {
		byPos[[3]int{f.X, f.Y, f.Z}] = f
	}
	return mapValuesSorted(byPos), nil
}

// LoadAllFurnaces scans all region files (and the sidecar index) in the store
// and returns every persisted furnace block entity in the world.
func (s *Store) LoadAllFurnaces() ([]FurnaceBlockEntity, error) {
	if s == nil {
		return nil, nil
	}
	byPos := make(map[[3]int]FurnaceBlockEntity)
	if sidecar, err := s.loadSidecarFurnaces(); err == nil {
		for _, f := range sidecar {
			byPos[[3]int{f.X, f.Y, f.Z}] = f
		}
	}

	regionDir := filepath.Join(s.dir, "region")
	entries, err := os.ReadDir(regionDir)
	if err != nil {
		return mapValuesSorted(byPos), nil
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		rx, rz, ok := parseRegionFileName(entry.Name())
		if !ok {
			continue
		}
		rf, err := s.regionFor(int32(rx*32), int32(rz*32))
		if err != nil {
			continue
		}
		for lz := 0; lz < 32; lz++ {
			for lx := 0; lx < 32; lx++ {
				raw, err := rf.ReadChunk(lx, lz)
				if err != nil {
					continue
				}
				_, tag, err := nbt.UnmarshalNamed(raw)
				if err != nil {
					continue
				}
				root, ok := tag.(*nbt.Compound)
				if !ok {
					continue
				}
				for _, f := range extractFurnacesFromChunkRoot(root) {
					byPos[[3]int{f.X, f.Y, f.Z}] = f
				}
			}
		}
	}
	return mapValuesSorted(byPos), nil
}

func extractFurnacesFromChunkRoot(root *nbt.Compound) []FurnaceBlockEntity {
	if root == nil {
		return nil
	}
	beTag, ok := root.Get("block_entities")
	if !ok {
		return nil
	}
	list, ok := beTag.(nbt.List)
	if !ok || list.ElemID != nbt.TagCompound {
		return nil
	}
	var out []FurnaceBlockEntity
	for _, elem := range list.Elems {
		ec, ok := elem.(*nbt.Compound)
		if !ok {
			continue
		}
		if f, ok := DecodeFurnaceBlockEntityNBT(ec); ok {
			out = append(out, f)
		}
	}
	return out
}

func parseRegionFileName(name string) (rx, rz int, ok bool) {
	if !strings.HasPrefix(name, "r.") || !strings.HasSuffix(name, ".mca") {
		return 0, 0, false
	}
	trimmed := strings.TrimSuffix(strings.TrimPrefix(name, "r."), ".mca")
	parts := strings.Split(trimmed, ".")
	if len(parts) != 2 {
		return 0, 0, false
	}
	x, err1 := strconv.Atoi(parts[0])
	z, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return x, z, true
}

func mapValuesSorted(m map[[3]int]FurnaceBlockEntity) []FurnaceBlockEntity {
	if len(m) == 0 {
		return nil
	}
	out := make([]FurnaceBlockEntity, 0, len(m))
	for _, f := range m {
		out = append(out, f.Clone())
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Y != out[j].Y {
			return out[i].Y < out[j].Y
		}
		if out[i].Z != out[j].Z {
			return out[i].Z < out[j].Z
		}
		return out[i].X < out[j].X
	})
	return out
}

func (s *Store) furnaceSidecarPath() string {
	return filepath.Join(s.dir, "data", "furnaces.dat")
}

func (s *Store) updateSidecarChunkFurnaces(cx, cz int32, chunkFurnaces []FurnaceBlockEntity) error {
	s.metaMu.Lock()
	defer s.metaMu.Unlock()

	existing, _ := s.loadSidecarFurnacesLocked()
	byPos := make(map[[3]int]FurnaceBlockEntity, len(existing)+len(chunkFurnaces))
	for _, f := range existing {
		if int32(f.X>>4) == cx && int32(f.Z>>4) == cz {
			continue
		}
		byPos[[3]int{f.X, f.Y, f.Z}] = f
	}
	for _, f := range chunkFurnaces {
		byPos[[3]int{f.X, f.Y, f.Z}] = f.Clone()
	}
	return s.saveSidecarFurnacesLocked(mapValuesSorted(byPos))
}

func (s *Store) loadSidecarFurnaces() ([]FurnaceBlockEntity, error) {
	s.metaMu.Lock()
	defer s.metaMu.Unlock()
	return s.loadSidecarFurnacesLocked()
}

func (s *Store) loadSidecarFurnacesLocked() ([]FurnaceBlockEntity, error) {
	raw, err := os.ReadFile(s.furnaceSidecarPath())
	if err != nil {
		return nil, err
	}
	gr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	defer gr.Close()
	uncompressed, err := io.ReadAll(gr)
	if err != nil {
		return nil, err
	}
	_, tag, err := nbt.UnmarshalNamed(uncompressed)
	if err != nil {
		return nil, err
	}
	root, ok := tag.(*nbt.Compound)
	if !ok {
		return nil, nil
	}
	return extractFurnacesFromChunkRoot(root), nil
}

func (s *Store) saveSidecarFurnacesLocked(all []FurnaceBlockEntity) error {
	dataDir := filepath.Join(s.dir, "data")
	if err := mkdirAll(dataDir); err != nil {
		return err
	}
	beList := nbt.List{
		ElemID: nbt.TagCompound,
		Elems:  make([]nbt.Tag, 0, len(all)),
	}
	for _, f := range all {
		beList.Elems = append(beList.Elems, EncodeFurnaceBlockEntityNBT(f))
	}
	root := nbt.NewCompound().
		Set("DataVersion", nbt.Int(dataVersion26)).
		Set("block_entities", beList)

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	if _, err := gw.Write(nbt.MarshalNamed("", root)); err != nil {
		_ = gw.Close()
		return err
	}
	if err := gw.Close(); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dataDir, ".furnaces-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(buf.Bytes()); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, s.furnaceSidecarPath())
}

// LoadedChunkCoords returns a snapshot of all currently loaded chunk coordinates
// in c.
func (c *Cache) LoadedChunkCoords() [][2]int32 {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.chunks) == 0 {
		return nil
	}
	out := make([][2]int32, 0, len(c.chunks))
	for k := range c.chunks {
		out = append(out, k)
	}
	return out
}

// LoadedChunkMatchingBlocks scans only non-empty sections of an already-loaded
// chunk (cx, cz) under a single read lock and returns [4]int{worldX, worldY, worldZ, int(state)}
// for every non-air block satisfying match.
func (c *Cache) LoadedChunkMatchingBlocks(cx, cz int32, match func(state uint16) bool) [][4]int {
	if c == nil || match == nil {
		return nil
	}
	c.mu.Lock()
	ch := c.chunks[[2]int32{cx, cz}]
	c.mu.Unlock()
	if ch == nil {
		return nil
	}
	baseX, baseZ := int(cx)<<4, int(cz)<<4
	var out [][4]int
	ch.mu.RLock()
	for si, sec := range ch.sections {
		if sec == nil {
			continue
		}
		secBaseY := MinY + si*16
		for idx, st := range sec {
			if st == StateAir || !match(st) {
				continue
			}
			lx := idx & 15
			lz := (idx >> 4) & 15
			ly := (idx >> 8) & 15
			out = append(out, [4]int{baseX + lx, secBaseY + ly, baseZ + lz, int(st)})
		}
	}
	ch.mu.RUnlock()
	return out
}

