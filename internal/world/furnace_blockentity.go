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
	Items            [FurnaceSlotCount]ItemStack
	LitTimeRemaining int16 // NBT "lit_time_remaining" (ContainerData index 0)
	LitTotalTime     int16 // NBT "lit_total_time"     (ContainerData index 1)
	CookingTimer     int16 // NBT "cooking_time_spent" (ContainerData index 2)
	CookingTotalTime int16 // NBT "cooking_total_time" (ContainerData index 3)
	RecipesUsed      map[string]int32
}

// Clone returns a deep copy of f.
func (f FurnaceBlockEntity) Clone() FurnaceBlockEntity {
	out := f
	if len(f.RecipesUsed) > 0 {
		out.RecipesUsed = make(map[string]int32, len(f.RecipesUsed))
		for k, v := range f.RecipesUsed {
			out.RecipesUsed[k] = v
		}
	}
	return out
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
	id := normalizeItemName(string(nbtAsString(c, "id")))
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

	if itemsTag, ok := c.Get("Items"); ok {
		if list, ok := itemsTag.(nbt.List); ok && list.ElemID == nbt.TagCompound {
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

// SaveChunkFurnaces persists all furnace block entities belonging to chunk
// (cx, cz) directly into the chunk's Anvil "block_entities" NBT list on disk,
// and also updates the world's furnace index so block edits saved via
// Cache.SaveAll do not drop furnace state.
func (c *Cache) SaveChunkFurnaces(cx, cz int32, furnaces []FurnaceBlockEntity) error {
	if c == nil || c.store == nil {
		return nil
	}
	key := [2]int32{cx, cz}
	c.startUse(key)
	defer c.endUse(key)

	ch, err := c.chunkAtErr(cx, cz)
	if err != nil {
		return err
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
