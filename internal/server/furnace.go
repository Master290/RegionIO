package server

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"regionio/internal/nbt"
	"regionio/internal/protocol"
	"regionio/internal/world"
)

// Furnace ContainerData property indices in ClientboundContainerSetDataPacket (0x13),
// matching vanilla AbstractFurnaceBlockEntity.dataAccess.
const (
	FurnaceDataLitTimeRemaining int16 = 0
	FurnaceDataLitTotalTime     int16 = 1
	FurnaceDataCookingTimer     int16 = 2
	FurnaceDataCookingTotalTime int16 = 3
)

// Default cooking durations in game ticks (20 ticks = 1 second).
const (
	DefaultSmeltingCookTime int16 = 200
	DefaultBlastingCookTime int16 = 100
	DefaultSmokingCookTime  int16 = 100
	DefaultCampfireCookTime int16 = 600
)

// SmeltingRecipe represents one parsed vanilla smelting, blasting, smoking, or campfire_cooking recipe.
type SmeltingRecipe struct {
	Name string
	Type string // "minecraft:smelting", "minecraft:blasting", "minecraft:smoking", or "minecraft:campfire_cooking"
	Category    string
	Group       string
	Ingredient  Ingredient
	Result      world.ItemStack
	Experience  float32
	CookingTime int16
}

// SmeltingBook holds all smelting, blasting, and smoking recipes and fuel values
// loaded from the embedded 26.1.2 datapack.
type SmeltingBook struct {
	recipes []*SmeltingRecipe
	byName  map[string]*SmeltingRecipe
	byType  map[string][]*SmeltingRecipe
	fuels   map[string]int16
}

var (
	defaultSmeltingOnce sync.Once
	defaultSmeltingBook *SmeltingBook
)

// DefaultSmeltingBook returns the singleton SmeltingBook loaded from the
// embedded vanilla 26.1.2 datapack archive.
func DefaultSmeltingBook() *SmeltingBook {
	defaultSmeltingOnce.Do(func() {
		// Ensure DefaultRecipeBook has registered all item IDs from items.json.
		_ = DefaultRecipeBook()
		book, err := LoadSmeltingBookFromZip(embeddedRecipeData)
		if err != nil {
			panic(fmt.Sprintf("server: loading embedded smelting recipes: %v", err))
		}
		defaultSmeltingBook = book
	})
	return defaultSmeltingBook
}

type rawCookingRecipeFile struct {
	Type        string          `json:"type"`
	Category    string          `json:"category"`
	Group       string          `json:"group"`
	Ingredient  json.RawMessage `json:"ingredient"`
	Result      rawRecipeResult `json:"result"`
	Experience  float32         `json:"experience"`
	CookingTime int             `json:"cookingtime"`
}

// LoadSmeltingBookFromZip parses smelting, blasting, and smoking recipes and
// builds the vanilla 26.1.2 fuel burn-time table from a datapack zip archive.
func LoadSmeltingBookFromZip(zipBytes []byte) (*SmeltingBook, error) {
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		return nil, err
	}

	rawTags := make(map[string][]string, 256)
	type rawEntry struct {
		name string
		data []byte
	}
	var recipeFiles []rawEntry

	for _, f := range zr.File {
		if f.FileInfo().IsDir() || !strings.HasSuffix(f.Name, ".json") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, err
		}

		switch {
		case f.Name == "data/minecraft/items.json":
			var items map[string]int32
			if err := json.Unmarshal(data, &items); err != nil {
				return nil, fmt.Errorf("parsing items.json: %w", err)
			}
			world.RegisterItems(items)

		case strings.HasPrefix(f.Name, "data/minecraft/tags/item/"):
			rel := strings.TrimPrefix(f.Name, "data/minecraft/tags/item/")
			tagName := "minecraft:" + strings.TrimSuffix(rel, ".json")
			var tf rawTagFile
			if err := json.Unmarshal(data, &tf); err != nil {
				return nil, fmt.Errorf("parsing item tag %s: %w", f.Name, err)
			}
			rawTags[tagName] = tf.Values

		case strings.HasPrefix(f.Name, "data/minecraft/recipe/"):
			rel := strings.TrimPrefix(f.Name, "data/minecraft/recipe/")
			recName := "minecraft:" + strings.TrimSuffix(path.Clean(rel), ".json")
			recipeFiles = append(recipeFiles, rawEntry{name: recName, data: data})
		}
	}

	resolvedTags := resolveAllItemTags(rawTags)
	sort.Slice(recipeFiles, func(i, j int) bool { return recipeFiles[i].name < recipeFiles[j].name })

	book := &SmeltingBook{
		recipes: make([]*SmeltingRecipe, 0, 128),
		byName:  make(map[string]*SmeltingRecipe, 128),
		byType:  make(map[string][]*SmeltingRecipe, 3),
		fuels:   buildVanillaFuelTable(resolvedTags),
	}

	for _, rf := range recipeFiles {
		rec, ok, err := parseSmeltingRecipe(rf.name, rf.data, resolvedTags)
		if err != nil {
			return nil, fmt.Errorf("smelting recipe %s: %w", rf.name, err)
		}
		if !ok {
			continue
		}
		book.recipes = append(book.recipes, rec)
		book.byName[rec.Name] = rec
		book.byType[rec.Type] = append(book.byType[rec.Type], rec)
	}
	return book, nil
}

func parseSmeltingRecipe(name string, data []byte, tags map[string][]string) (*SmeltingRecipe, bool, error) {
	var header struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &header); err != nil {
		return nil, false, err
	}
	rtype := canonicalResourceName(header.Type)
	if rtype != "minecraft:smelting" && rtype != "minecraft:blasting" && rtype != "minecraft:smoking" && rtype != "minecraft:campfire_cooking" {
		return nil, false, nil
	}

	var raw rawCookingRecipeFile
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, false, err
	}
	if raw.Result.ID == "" || len(raw.Ingredient) == 0 {
		return nil, false, nil
	}
	ing, err := parseIngredientSpec(raw.Ingredient, tags)
	if err != nil || ing.IsEmpty() {
		return nil, false, err
	}
	count := raw.Result.Count
	if count <= 0 {
		count = 1
	}
	resultStack := world.NewNamedItemStack(raw.Result.ID, count)
	if resultStack.IsEmpty() {
		return nil, false, nil
	}

	cookTime := int16(raw.CookingTime)
	if cookTime <= 0 {
		cookTime = DefaultCookTimeForRecipeType(rtype)
	}

	return &SmeltingRecipe{
		Name:        canonicalResourceName(name),
		Type:        rtype,
		Category:    raw.Category,
		Group:       raw.Group,
		Ingredient:  ing,
		Result:      resultStack,
		Experience:  raw.Experience,
		CookingTime: cookTime,
	}, true, nil
}

// buildVanillaFuelTable mirrors FuelValues.vanillaBurnTimes(smeltTime=200) from
// Minecraft 26.1.2, expanding item tags and removing #minecraft:non_flammable_wood.
func buildVanillaFuelTable(tags map[string][]string) map[string]int16 {
	fuels := make(map[string]int16, 256)
	addItem := func(id string, ticks int16) {
		fuels[canonicalResourceName(id)] = ticks
	}
	addTag := func(tag string, ticks int16) {
		for _, item := range tags[canonicalResourceName(tag)] {
			fuels[canonicalResourceName(item)] = ticks
		}
	}

	addItem("minecraft:lava_bucket", 20000)
	addItem("minecraft:coal_block", 16000)
	addItem("minecraft:blaze_rod", 2400)
	addItem("minecraft:coal", 1600)
	addItem("minecraft:charcoal", 1600)
	addTag("minecraft:logs", 300)
	addTag("minecraft:bamboo_blocks", 300)
	addTag("minecraft:planks", 300)
	addItem("minecraft:bamboo_mosaic", 300)
	addTag("minecraft:wooden_stairs", 300)
	addItem("minecraft:bamboo_mosaic_stairs", 300)
	addTag("minecraft:wooden_slabs", 150)
	addItem("minecraft:bamboo_mosaic_slab", 150)
	addTag("minecraft:wooden_trapdoors", 300)
	addTag("minecraft:wooden_pressure_plates", 300)
	addTag("minecraft:wooden_shelves", 300)
	addTag("minecraft:wooden_fences", 300)
	addTag("minecraft:fence_gates", 300)
	addItem("minecraft:note_block", 300)
	addItem("minecraft:bookshelf", 300)
	addItem("minecraft:chiseled_bookshelf", 300)
	addItem("minecraft:lectern", 300)
	addItem("minecraft:jukebox", 300)
	addItem("minecraft:chest", 300)
	addItem("minecraft:trapped_chest", 300)
	addItem("minecraft:crafting_table", 300)
	addItem("minecraft:daylight_detector", 300)
	addTag("minecraft:banners", 300)
	addItem("minecraft:bow", 300)
	addItem("minecraft:fishing_rod", 300)
	addItem("minecraft:ladder", 300)
	addTag("minecraft:signs", 200)
	addTag("minecraft:hanging_signs", 800)
	addItem("minecraft:wooden_shovel", 200)
	addItem("minecraft:wooden_sword", 200)
	addItem("minecraft:wooden_spear", 200)
	addItem("minecraft:wooden_hoe", 200)
	addItem("minecraft:wooden_axe", 200)
	addItem("minecraft:wooden_pickaxe", 200)
	addTag("minecraft:wooden_doors", 200)
	addTag("minecraft:boats", 1200)
	addTag("minecraft:wool", 100)
	addTag("minecraft:wooden_buttons", 100)
	addItem("minecraft:stick", 100)
	addTag("minecraft:saplings", 100)
	addItem("minecraft:bowl", 100)
	addTag("minecraft:wool_carpets", 67)
	addItem("minecraft:dried_kelp_block", 4001)
	addItem("minecraft:crossbow", 300)
	addItem("minecraft:bamboo", 50)
	addItem("minecraft:dead_bush", 100)
	addItem("minecraft:short_dry_grass", 100)
	addItem("minecraft:tall_dry_grass", 100)
	addItem("minecraft:scaffolding", 50)
	addItem("minecraft:loom", 300)
	addItem("minecraft:barrel", 300)
	addItem("minecraft:cartography_table", 300)
	addItem("minecraft:fletching_table", 300)
	addItem("minecraft:smithing_table", 300)
	addItem("minecraft:composter", 300)
	addItem("minecraft:azalea", 100)
	addItem("minecraft:flowering_azalea", 100)
	addItem("minecraft:mangrove_roots", 300)
	addItem("minecraft:leaf_litter", 100)

	// FuelValues.Builder.remove(ItemTags.NON_FLAMMABLE_WOOD)
	for _, nonFlammable := range tags["minecraft:non_flammable_wood"] {
		delete(fuels, canonicalResourceName(nonFlammable))
	}
	return fuels
}

// DefaultCookTimeForRecipeType returns the default cooking duration in ticks for
// a cooking recipe type.
func DefaultCookTimeForRecipeType(recipeType string) int16 {
	switch canonicalResourceName(recipeType) {
	case "minecraft:blasting":
		return DefaultBlastingCookTime
	case "minecraft:smoking":
		return DefaultSmokingCookTime
	case "minecraft:campfire_cooking":
		return DefaultCampfireCookTime
	default:
		return DefaultSmeltingCookTime
	}
}

// RecipeTypeForFurnaceID returns the cooking recipe type corresponding to a
// furnace block entity ID ("minecraft:furnace", "minecraft:blast_furnace", or
// "minecraft:smoker").
func RecipeTypeForFurnaceID(blockEntityID string) string {
	switch canonicalResourceName(blockEntityID) {
	case "minecraft:blast_furnace":
		return "minecraft:blasting"
	case "minecraft:smoker":
		return "minecraft:smoking"
	default:
		return "minecraft:smelting"
	}
}

// Count returns the total number of loaded smelting, blasting, and smoking recipes.
func (b *SmeltingBook) Count() int {
	if b == nil {
		return 0
	}
	return len(b.recipes)
}

// Recipe looks up a cooking recipe by its resource identifier.
func (b *SmeltingBook) Recipe(name string) (*SmeltingRecipe, bool) {
	if b == nil {
		return nil, false
	}
	rec, ok := b.byName[canonicalResourceName(name)]
	return rec, ok
}

// Match finds the cooking recipe of recipeType ("minecraft:smelting",
// "minecraft:blasting", or "minecraft:smoking") that accepts input.
func (b *SmeltingBook) Match(recipeType string, input world.ItemStack) (*SmeltingRecipe, bool) {
	if b == nil || input.IsEmpty() {
		return nil, false
	}
	rtype := canonicalResourceName(recipeType)
	for _, rec := range b.byType[rtype] {
		if rec.Ingredient.Matches(input) {
			return rec, true
		}
	}
	return nil, false
}

// FuelBurnDuration returns the burn duration in ticks for stack in a standard
// furnace (200-tick base), or 0 if stack is not a fuel. Blast furnaces and
// smokers divide this duration by 2 (matching vanilla FuelValues.burnDuration /
// AbstractFurnaceBlockEntity.getBurnDuration).
func (b *SmeltingBook) FuelBurnDuration(stack world.ItemStack) int16 {
	if b == nil || stack.IsEmpty() {
		return 0
	}
	return b.fuels[stack.ResolvedID()]
}

// FuelBurnDuration returns the vanilla 26.1.2 burn time in ticks for stack in a
// standard furnace, or 0 if stack is not fuel.
func FuelBurnDuration(stack world.ItemStack) int16 {
	return DefaultSmeltingBook().FuelBurnDuration(stack)
}

// FurnaceFuelBurnDuration returns the burn duration in ticks for stack inside
// the given furnace type ("minecraft:furnace" uses full duration;
// "minecraft:blast_furnace" and "minecraft:smoker" halve the duration matching
// vanilla's getBurnDuration).
func FurnaceFuelBurnDuration(blockEntityID string, stack world.ItemStack) int16 {
	base := FuelBurnDuration(stack)
	if base <= 0 {
		return 0
	}
	switch canonicalResourceName(blockEntityID) {
	case "minecraft:blast_furnace", "minecraft:smoker":
		return base / 2
	default:
		return base
	}
}

// IsFurnaceFuel reports whether stack has a positive fuel burn duration.
func IsFurnaceFuel(stack world.ItemStack) bool {
	return FuelBurnDuration(stack) > 0
}

// CanPlaceInFurnaceFuelSlot reports whether stack is allowed in slot 1 (fuel
// slot), matching vanilla FurnaceFuelSlot.mayPlace (any fuel or empty bucket).
func CanPlaceInFurnaceFuelSlot(stack world.ItemStack) bool {
	if stack.IsEmpty() {
		return false
	}
	return IsFurnaceFuel(stack) || stack.ResolvedID() == "minecraft:bucket"
}

// MatchSmeltingRecipe finds the matching recipe for input in a furnace of type
// blockEntityID ("minecraft:furnace", "minecraft:blast_furnace", or "minecraft:smoker").
func MatchSmeltingRecipe(blockEntityID string, input world.ItemStack) (*SmeltingRecipe, bool) {
	return DefaultSmeltingBook().Match(RecipeTypeForFurnaceID(blockEntityID), input)
}

// IsFurnaceBlock reports whether state is a furnace, blast_furnace, or smoker.
func IsFurnaceBlock(state uint16) bool {
	return world.IsFurnaceBlockState(state)
}

// FurnaceMenuInfo returns the MenuType ID, canonical block entity ID, and
// default window title for a furnace, blast_furnace, or smoker block state.
func FurnaceMenuInfo(state uint16) (menuType int32, blockEntityID, title string, ok bool) {
	id, valid := world.FurnaceBlockEntityIDForState(state)
	if !valid {
		return 0, "", "", false
	}
	menuType, title = furnaceMenuAndTitleForID(id)
	return menuType, id, title, true
}

func furnaceMenuAndTitleForID(blockEntityID string) (int32, string) {
	switch canonicalResourceName(blockEntityID) {
	case "minecraft:blast_furnace":
		return MenuTypeBlastFurnace, "Blast Furnace"
	case "minecraft:smoker":
		return MenuTypeSmoker, "Smoker"
	default:
		return MenuTypeFurnace, "Furnace"
	}
}

func furnaceIDForMenuType(menuType int32) string {
	switch menuType {
	case MenuTypeBlastFurnace:
		return "minecraft:blast_furnace"
	case MenuTypeSmoker:
		return "minecraft:smoker"
	default:
		return "minecraft:furnace"
	}
}

type furnaceKey struct {
	x, y, z int
}

type serverFurnaceState struct {
	mu          sync.Mutex
	loadedStore bool
	loadedChunk map[[2]int32]bool
	furnaces    map[furnaceKey]*world.FurnaceBlockEntity
}

var serverFurnaces sync.Map // map[*Server]*serverFurnaceState

func (s *Server) furnaceState() *serverFurnaceState {
	if v, ok := serverFurnaces.Load(s); ok {
		return v.(*serverFurnaceState)
	}
	actual, _ := serverFurnaces.LoadOrStore(s, &serverFurnaceState{
		loadedChunk: make(map[[2]int32]bool),
		furnaces:    make(map[furnaceKey]*world.FurnaceBlockEntity),
	})
	return actual.(*serverFurnaceState)
}

func (s *Server) ensureFurnacesLoadedLocked(fs *serverFurnaceState) {
	if fs.loadedStore {
		return
	}
	fs.loadedStore = true
	if s == nil || s.store == nil {
		return
	}
	persisted, err := s.store.LoadAllFurnaces()
	if err != nil {
		return
	}
	for _, f := range persisted {
		key := furnaceKey{f.X, f.Y, f.Z}
		if _, exists := fs.furnaces[key]; !exists {
			cp := f.Clone()
			fs.furnaces[key] = &cp
		}
		fs.loadedChunk[[2]int32{int32(f.X >> 4), int32(f.Z >> 4)}] = true
	}
}

func (s *Server) ensureChunkFurnacesLoadedLocked(fs *serverFurnaceState, cx, cz int32) {
	s.ensureFurnacesLoadedLocked(fs)
	ck := [2]int32{cx, cz}
	if fs.loadedChunk[ck] {
		return
	}
	fs.loadedChunk[ck] = true
	if s == nil || s.store == nil {
		return
	}
	persisted, err := s.store.LoadChunkFurnaces(cx, cz)
	if err != nil {
		return
	}
	for _, f := range persisted {
		key := furnaceKey{f.X, f.Y, f.Z}
		if _, exists := fs.furnaces[key]; !exists {
			cp := f.Clone()
			fs.furnaces[key] = &cp
		}
	}
}

func (s *Server) resolveFurnaceIDAt(x, y, z int, fallback string) string {
	if s != nil && s.chunks != nil {
		if id, ok := world.FurnaceBlockEntityIDForState(s.chunks.GetBlock(x, y, z)); ok {
			return id
		}
	}
	if world.IsFurnaceBlockID(fallback) {
		return canonicalResourceName(fallback)
	}
	return "minecraft:furnace"
}

func (s *Server) getOrCreateFurnaceLocked(fs *serverFurnaceState, x, y, z int, fallbackID string) *world.FurnaceBlockEntity {
	s.ensureChunkFurnacesLoadedLocked(fs, int32(x>>4), int32(z>>4))
	key := furnaceKey{x, y, z}
	f := fs.furnaces[key]
	resolvedID := s.resolveFurnaceIDAt(x, y, z, fallbackID)
	if f == nil {
		f = &world.FurnaceBlockEntity{
			ID:          resolvedID,
			X:           x,
			Y:           y,
			Z:           z,
			RecipesUsed: make(map[string]int32),
		}
		fs.furnaces[key] = f
	} else if world.IsFurnaceBlockID(resolvedID) {
		f.ID = resolvedID
	}
	return f
}

func (s *Server) collectChunkFurnacesLocked(fs *serverFurnaceState, cx, cz int32) []world.FurnaceBlockEntity {
	var out []world.FurnaceBlockEntity
	for key, f := range fs.furnaces {
		if int32(key.x>>4) == cx && int32(key.z>>4) == cz && f != nil {
			out = append(out, f.Clone())
		}
	}
	return out
}

func (s *Server) persistChunkFurnaces(cx, cz int32) {
	if s == nil || s.chunks == nil || s.store == nil {
		return
	}
	fs := s.furnaceState()
	fs.mu.Lock()
	s.ensureChunkFurnacesLoadedLocked(fs, cx, cz)
	list := s.collectChunkFurnacesLocked(fs, cx, cz)
	fs.mu.Unlock()
	_ = s.chunks.SaveChunkFurnaces(cx, cz, list)
}

// SaveFurnaces flushes all chunks containing furnace block entities to the
// underlying world store.
func (s *Server) SaveFurnaces() error {
	if s == nil || s.chunks == nil || s.store == nil {
		return nil
	}
	fs := s.furnaceState()
	fs.mu.Lock()
	s.ensureFurnacesLoadedLocked(fs)
	byChunk := make(map[[2]int32][]world.FurnaceBlockEntity)
	for ck := range fs.loadedChunk {
		byChunk[ck] = nil
	}
	for key, f := range fs.furnaces {
		if f == nil {
			continue
		}
		ck := [2]int32{int32(key.x >> 4), int32(key.z >> 4)}
		byChunk[ck] = append(byChunk[ck], f.Clone())
	}
	fs.mu.Unlock()

	var firstErr error
	for ck, list := range byChunk {
		if err := s.chunks.SaveChunkFurnaces(ck[0], ck[1], list); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// FurnaceAt returns a snapshot of the furnace block entity at (x, y, z).
func (s *Server) FurnaceAt(x, y, z int) (world.FurnaceBlockEntity, bool) {
	if s == nil {
		return world.FurnaceBlockEntity{}, false
	}
	fs := s.furnaceState()
	fs.mu.Lock()
	s.ensureChunkFurnacesLoadedLocked(fs, int32(x>>4), int32(z>>4))
	f := fs.furnaces[furnaceKey{x, y, z}]
	if f == nil {
		fs.mu.Unlock()
		if s.chunks != nil {
			if id, ok := world.FurnaceBlockEntityIDForState(s.chunks.GetBlock(x, y, z)); ok {
				return world.FurnaceBlockEntity{ID: id, X: x, Y: y, Z: z}, true
			}
		}
		return world.FurnaceBlockEntity{}, false
	}
	cp := f.Clone()
	fs.mu.Unlock()
	return cp, true
}

// SetFurnaceState replaces or inserts the furnace block entity at (f.X, f.Y, f.Z)
// and persists it to the chunk's block_entities list.
func (s *Server) SetFurnaceState(f world.FurnaceBlockEntity) {
	if s == nil {
		return
	}
	s.BindNoteBlockAndJukeboxRedstone(world.DimensionOverworld)
	fs := s.furnaceState()
	fs.mu.Lock()
	s.ensureChunkFurnacesLoadedLocked(fs, int32(f.X>>4), int32(f.Z>>4))
	cp := f.Clone()
	cp.ID = s.resolveFurnaceIDAt(f.X, f.Y, f.Z, f.ID)
	if cp.RecipesUsed == nil {
		cp.RecipesUsed = make(map[string]int32)
	}
	if cp.CookingTotalTime <= 0 && !cp.Items[world.FurnaceSlotInput].IsEmpty() {
		if rec, ok := MatchSmeltingRecipe(cp.ID, cp.Items[world.FurnaceSlotInput]); ok {
			cp.CookingTotalTime = rec.CookingTime
		}
	}
	fs.furnaces[furnaceKey{f.X, f.Y, f.Z}] = &cp
	fs.mu.Unlock()
	s.syncFurnaceLitBlockStateAt(f.X, f.Y, f.Z, cp.LitTimeRemaining > 0)
	s.persistChunkFurnaces(int32(f.X>>4), int32(f.Z>>4))
	s.updateHopperComparatorsAndBroadcast(world.DimensionOverworld, f.X, f.Y, f.Z)
}

func (s *Server) syncFurnaceLitBlockStateAt(x, y, z int, wantLit bool) {
	if s == nil || s.chunks == nil {
		return
	}
	curState := s.chunks.GetBlock(x, y, z)
	if !world.IsFurnaceBlockState(curState) {
		return
	}
	if world.FurnaceStateLit(curState) == wantLit {
		return
	}
	if nextState, ok := world.SetFurnaceStateLit(curState, wantLit); ok && nextState != curState {
		_, _ = s.chunks.SetBlockWithLight(x, y, z, nextState)
		s.broadcastBlockUpdateInDim(world.DimensionOverworld, x, y, z, nextState)
	}
}

// FurnaceComparatorSignal returns the redstone comparator output (0..15) for a
// furnace block entity snapshot.
func FurnaceComparatorSignal(f world.FurnaceBlockEntity) int {
	return CalculateContainerComparatorSignal(f.Items[:])
}

// FurnaceComparatorSignalAt returns the redstone comparator output (0..15) for
// the furnace, blast_furnace, or smoker at (x, y, z).
func (s *Server) FurnaceComparatorSignalAt(x, y, z int) int {
	if s == nil {
		return 0
	}
	slots := s.FurnaceSlots(x, y, z)
	return CalculateContainerComparatorSignal(slots[:])
}

// FurnaceSlots returns a copy of the 3 slots (input, fuel, output) in the
// furnace at (x, y, z).
func (s *Server) FurnaceSlots(x, y, z int) [FurnaceSlotCount]world.ItemStack {
	f, ok := s.FurnaceAt(x, y, z)
	if !ok {
		return [FurnaceSlotCount]world.ItemStack{}
	}
	return f.Items
}

// SetFurnaceSlot updates slot (0=input, 1=fuel, 2=output) in the furnace at
// (x, y, z), resetting cooking progress when the input item changes (matching
// vanilla AbstractFurnaceBlockEntity.setItem).
func (s *Server) SetFurnaceSlot(x, y, z, slot int, stack world.ItemStack) bool {
	if s == nil || slot < 0 || slot >= FurnaceSlotCount {
		return false
	}
	s.BindNoteBlockAndJukeboxRedstone(world.DimensionOverworld)
	var normalized world.ItemStack
	if !stack.IsEmpty() {
		normalized = world.NewItemStack(stack.ResolvedItemID(), stack.Count)
	}

	fs := s.furnaceState()
	fs.mu.Lock()
	f := s.getOrCreateFurnaceLocked(fs, x, y, z, "")
	prev := f.Items[slot]
	f.Items[slot] = normalized
	if slot == world.FurnaceSlotInput {
		sameItem := !prev.IsEmpty() && !normalized.IsEmpty() && prev.ResolvedItemID() == normalized.ResolvedItemID()
		if !sameItem {
			f.CookingTimer = 0
			if rec, ok := MatchSmeltingRecipe(f.ID, normalized); ok {
				f.CookingTotalTime = rec.CookingTime
			} else {
				f.CookingTotalTime = 0
			}
		}
	}
	fs.mu.Unlock()

	s.persistChunkFurnaces(int32(x>>4), int32(z>>4))
	s.updateHopperComparatorsAndBroadcast(world.DimensionOverworld, x, y, z)
	return true
}

// DropFurnaceContents removes any stored furnace block entity at (x, y, z),
// closes any open furnace screens targeting that position, spawns non-empty
// input/fuel/output stacks as item entities in the world, spawns stored
// smelting XP orbs from RecipesUsed, and updates disk state.
func (s *Server) DropFurnaceContents(x, y, z int) {
	if s == nil {
		return
	}
	fs := s.furnaceState()
	fs.mu.Lock()
	s.ensureChunkFurnacesLoadedLocked(fs, int32(x>>4), int32(z>>4))
	key := furnaceKey{x, y, z}
	f := fs.furnaces[key]
	delete(fs.furnaces, key)
	fs.mu.Unlock()

	// Close any viewer screens open on this furnace.
	s.playersMu.RLock()
	sessions := make([]*PlayerSession, 0, len(s.players))
	for _, sess := range s.players {
		sessions = append(sessions, sess)
	}
	s.playersMu.RUnlock()
	for _, sess := range sessions {
		open := sess.OpenContainer()
		if open != nil && isFurnaceMenuType(open.MenuType) &&
			open.BlockX == x && open.BlockY == y && open.BlockZ == z {
			_ = s.CloseContainer(sess, open.ContainerID, true)
		}
	}

	if f != nil {
		for _, st := range f.Items {
			if !st.IsEmpty() {
				s.SpawnItemEntity(float64(x)+0.5, float64(y)+0.25, float64(z)+0.5, st)
			}
		}
		if totalXP := calculateFurnaceRecipesXP(f.RecipesUsed); totalXP > 0 {
			s.SpawnExperienceOrbs(float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, int32(totalXP))
		}
		s.persistChunkFurnaces(int32(x>>4), int32(z>>4))
	}
	s.updateHopperComparatorsAndBroadcast(world.DimensionOverworld, x, y, z)
}

func isFurnaceMenuType(menuType int32) bool {
	return menuType == MenuTypeFurnace || menuType == MenuTypeBlastFurnace || menuType == MenuTypeSmoker
}

// OpenFurnace opens the furnace, blast furnace, or smoker screen at (x, y, z)
// for session and sends open_screen, container_set_content, and the four
// container_set_data progress properties.
func (s *Server) OpenFurnace(session *PlayerSession, x, y, z int) (int32, error) {
	if s == nil || session == nil {
		return 0, errors.New("server: nil session")
	}
	_ = s.CloseContainer(session, 0, false)

	menuType := MenuTypeFurnace
	blockEntityID := "minecraft:furnace"
	title := "Furnace"
	if s.chunks != nil {
		if mt, id, t, ok := FurnaceMenuInfo(s.chunks.GetBlock(x, y, z)); ok {
			menuType, blockEntityID, title = mt, id, t
		}
	}

	fs := s.furnaceState()
	fs.mu.Lock()
	f := s.getOrCreateFurnaceLocked(fs, x, y, z, blockEntityID)
	menuType, title = furnaceMenuAndTitleForID(f.ID)
	if f.CustomName != "" {
		title = f.CustomName
	}
	fs.mu.Unlock()

	tr := getContainerTracker(session)
	tr.mu.Lock()
	tr.nextWindow = (tr.nextWindow % 100) + 1
	cid := tr.nextWindow
	tr.open = &OpenContainerState{
		ContainerID: cid,
		MenuType:    menuType,
		BlockX:      x,
		BlockY:      y,
		BlockZ:      z,
	}
	carried := tr.carried
	tr.mu.Unlock()

	if session.send != nil {
		if err := session.send(protocol.PlayOpenScreen, EncodeOpenScreen(cid, menuType, title)); err != nil {
			return cid, err
		}
		slots := s.buildFurnaceMenuSlots(session, x, y, z)
		stateID := session.NextContainerStateID()
		if err := session.send(protocol.PlayContainerSetContent, EncodeContainerContent(cid, stateID, slots, carried)); err != nil {
			return cid, err
		}
		if err := s.sendFurnaceDataProperties(session, cid, x, y, z); err != nil {
			return cid, err
		}
	}
	return cid, nil
}

func (s *Server) buildFurnaceMenuSlots(session *PlayerSession, x, y, z int) []world.ItemStack {
	furnaceSlots := s.FurnaceSlots(x, y, z)
	inv := session.Inventory()
	slots := make([]world.ItemStack, FurnaceMenuSlotCount)
	copy(slots[0:3], furnaceSlots[:])
	copy(slots[3:30], inv[9:36])
	copy(slots[30:39], inv[36:45])
	return slots
}

func (s *Server) sendFurnaceDataProperties(session *PlayerSession, containerID int32, x, y, z int) error {
	if session == nil || session.send == nil {
		return nil
	}
	f, _ := s.FurnaceAt(x, y, z)
	props := [4]struct {
		id  int16
		val int16
	}{
		{FurnaceDataLitTimeRemaining, f.LitTimeRemaining},
		{FurnaceDataLitTotalTime, f.LitTotalTime},
		{FurnaceDataCookingTimer, f.CookingTimer},
		{FurnaceDataCookingTotalTime, f.CookingTotalTime},
	}
	for _, p := range props {
		if err := session.send(protocol.PlayContainerSetData, EncodeContainerSetData(containerID, p.id, p.val)); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) applyFurnaceClickLocked(session *PlayerSession, tr *playerContainerTracker, menuType int32, bx, by, bz, slot int, button int8, mode int32) {
	if slot < 0 || slot >= FurnaceMenuSlotCount {
		return
	}
	slots := s.buildFurnaceMenuSlots(session, bx, by, bz)
	prevOutput := slots[world.FurnaceSlotOutput]
	prevOutputCount := prevOutput.Count
	furnaceID := s.resolveFurnaceIDAt(bx, by, bz, furnaceIDForMenuType(menuType))

	switch mode {
	case ContainerInputPickup:
		switch slot {
		case world.FurnaceSlotOutput:
			// FurnaceResultSlot: players can only take items out, never place into slot 2.
			if !slots[slot].IsEmpty() {
				if tr.carried.IsEmpty() {
					if button == 1 {
						take := (slots[slot].Count + 1) / 2
						leave := slots[slot].Count - take
						tr.carried = world.NewItemStack(slots[slot].ResolvedItemID(), take)
						if leave <= 0 {
							slots[slot] = world.ItemStack{}
						} else {
							slots[slot] = world.NewItemStack(slots[slot].ResolvedItemID(), leave)
						}
					} else {
						tr.carried = slots[slot]
						slots[slot] = world.ItemStack{}
					}
				} else if tr.carried.ResolvedItemID() == slots[slot].ResolvedItemID() {
					space := MaxItemStackSize - tr.carried.Count
					if space > 0 {
						take := slots[slot].Count
						if take > space {
							take = space
						}
						tr.carried = world.NewItemStack(tr.carried.ResolvedItemID(), tr.carried.Count+take)
						rem := slots[slot].Count - take
						if rem <= 0 {
							slots[slot] = world.ItemStack{}
						} else {
							slots[slot] = world.NewItemStack(slots[slot].ResolvedItemID(), rem)
						}
					}
				}
			}
		case world.FurnaceSlotFuel:
			// FurnaceFuelSlot: taking out is always allowed; placing requires fuel or empty bucket.
			if tr.carried.IsEmpty() || CanPlaceInFurnaceFuelSlot(tr.carried) {
				if !tr.carried.IsEmpty() && tr.carried.ResolvedID() == "minecraft:bucket" && !slots[slot].IsEmpty() {
					break
				}
				slots[slot], tr.carried = applyPickupSlot(slots[slot], tr.carried, button)
			}
		default:
			slots[slot], tr.carried = applyPickupSlot(slots[slot], tr.carried, button)
		}

	case ContainerInputQuickMove:
		if slot < FurnaceSlotCount {
			// Furnace slot (0..2) -> Player inventory (prefer hotbar 30..38, then main 3..29).
			slots[slot] = mergeIntoSlotSlice(slots, slots[slot], 30, 39)
			slots[slot] = mergeIntoSlotSlice(slots, slots[slot], 3, 30)
		} else {
			// Player inventory (3..38) -> Furnace input (0), fuel (1), or toggle main/hotbar.
			clicked := slots[slot]
			if !clicked.IsEmpty() {
				if _, canSmelt := MatchSmeltingRecipe(furnaceID, clicked); canSmelt {
					slots[slot] = mergeIntoSlotSlice(slots, clicked, 0, 1)
				} else if CanPlaceInFurnaceFuelSlot(clicked) {
					slots[slot] = mergeIntoSlotSlice(slots, clicked, 1, 2)
				} else if slot >= 3 && slot < 30 {
					slots[slot] = mergeIntoSlotSlice(slots, clicked, 30, 39)
				} else if slot >= 30 && slot < 39 {
					slots[slot] = mergeIntoSlotSlice(slots, clicked, 3, 30)
				}
			}
		}

	case ContainerInputSwap:
		if button >= 0 && button < 9 && slot != world.FurnaceSlotOutput {
			hotbarIdx := 30 + int(button)
			if slot == world.FurnaceSlotFuel && !slots[hotbarIdx].IsEmpty() && !CanPlaceInFurnaceFuelSlot(slots[hotbarIdx]) {
				break
			}
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
			s.SpawnItemEntity(snap.X, snap.Y+0.5, snap.Z, dropStack)
		}
	}

	for i := 0; i < FurnaceSlotCount; i++ {
		s.SetFurnaceSlot(bx, by, bz, i, slots[i])
	}
	if slots[world.FurnaceSlotOutput].Count < prevOutputCount {
		if xp := s.AwardFurnaceExperience(bx, by, bz); xp > 0 && session != nil {
			_ = s.AddPlayerExperience(session, int32(xp))
		}
		s.TriggerItemSmelted(session, prevOutput)
	}
	for i := 0; i < 27; i++ {
		session.SetInventorySlot(9+i, slots[3+i])
	}
	for i := 0; i < 9; i++ {
		session.SetInventorySlot(36+i, slots[30+i])
	}
}

func (s *Server) syncFurnaceViewers(actor *PlayerSession, bx, by, bz int) error {
	s.playersMu.RLock()
	sessions := make([]*PlayerSession, 0, len(s.players))
	for _, sess := range s.players {
		sessions = append(sessions, sess)
	}
	s.playersMu.RUnlock()

	var firstErr error
	for _, sess := range sessions {
		open := sess.OpenContainer()
		if open != nil && isFurnaceMenuType(open.MenuType) &&
			open.BlockX == bx && open.BlockY == by && open.BlockZ == bz {
			if err := s.syncOpenContainer(sess); err != nil && firstErr == nil {
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

func (s *Server) broadcastFurnaceProgressToViewers(bx, by, bz int, slotsChanged bool) {
	s.playersMu.RLock()
	sessions := make([]*PlayerSession, 0, len(s.players))
	for _, sess := range s.players {
		sessions = append(sessions, sess)
	}
	s.playersMu.RUnlock()

	for _, sess := range sessions {
		open := sess.OpenContainer()
		if open == nil || !isFurnaceMenuType(open.MenuType) ||
			open.BlockX != bx || open.BlockY != by || open.BlockZ != bz {
			continue
		}
		if slotsChanged {
			_ = s.syncOpenContainer(sess)
		} else {
			_ = s.sendFurnaceDataProperties(sess, open.ContainerID, bx, by, bz)
		}
	}
}

// TickFurnaceAt advances the furnace block entity at (x, y, z) by one game tick,
// matching vanilla 26.1.2 AbstractFurnaceBlockEntity.serverTick. It returns true
// if the furnace state, inventory, or lit block state changed.
func (s *Server) TickFurnaceAt(x, y, z int) bool {
	if s == nil {
		return false
	}
	if s.chunks != nil {
		state := s.chunks.GetBlock(x, y, z)
		if !IsFurnaceBlock(state) {
			// If a furnace block was removed from the world, drop its contents.
			s.DropFurnaceContents(x, y, z)
			return false
		}
	}

	fs := s.furnaceState()
	fs.mu.Lock()
	f := s.getOrCreateFurnaceLocked(fs, x, y, z, "")
	changed, slotsChanged, litChanged, isLit := stepFurnaceTickLocked(f)
	fs.mu.Unlock()

	if litChanged && s.chunks != nil {
		curState := s.chunks.GetBlock(x, y, z)
		if newState, ok := world.SetFurnaceStateLit(curState, isLit); ok && newState != curState {
			if valid, lightChunks := s.chunks.SetBlockWithLight(x, y, z, newState); valid {
				s.broadcastBlockAndLightUpdate(x, y, z, newState, lightChunks)
			}
		}
	}

	if slotsChanged {
		s.BindNoteBlockAndJukeboxRedstone(world.DimensionOverworld)
		s.updateHopperComparatorsAndBroadcast(world.DimensionOverworld, x, y, z)
	}
	if changed {
		s.persistChunkFurnaces(int32(x>>4), int32(z>>4))
	}
	if changed || slotsChanged {
		s.broadcastFurnaceProgressToViewers(x, y, z, slotsChanged)
	}
	return changed
}

// TickFurnaces advances all tracked furnace block entities by one game tick and
// returns the number of furnaces whose state changed.
func (s *Server) TickFurnaces() int {
	if s == nil {
		return 0
	}
	fs := s.furnaceState()
	fs.mu.Lock()
	s.ensureFurnacesLoadedLocked(fs)
	keys := make([]furnaceKey, 0, len(fs.furnaces))
	for k := range fs.furnaces {
		keys = append(keys, k)
	}
	fs.mu.Unlock()

	sort.Slice(keys, func(i, j int) bool {
		if keys[i].y != keys[j].y {
			return keys[i].y < keys[j].y
		}
		if keys[i].z != keys[j].z {
			return keys[i].z < keys[j].z
		}
		return keys[i].x < keys[j].x
	})

	changedCount := 0
	for _, k := range keys {
		if s.TickFurnaceAt(k.x, k.y, k.z) {
			changedCount++
		}
	}
	return changedCount
}

// StartFurnaceTicker runs a 20 TPS (50ms) background goroutine ticking all
// furnaces until ctx is cancelled, then flushes furnace block entities.
func (s *Server) StartFurnaceTicker(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	if s == nil {
		close(done)
		return done
	}
	go func() {
		defer close(done)
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				_ = s.SaveFurnaces()
				return
			case <-ticker.C:
				s.TickFurnaces()
			}
		}
	}()
	return done
}

func stepFurnaceTickLocked(f *world.FurnaceBlockEntity) (changed, slotsChanged, litChanged, isLit bool) {
	wasLit := f.LitTimeRemaining > 0
	if f.LitTimeRemaining > 0 {
		f.LitTimeRemaining--
		changed = true
	}
	isLit = f.LitTimeRemaining > 0

	input := f.Items[world.FurnaceSlotInput]
	fuel := f.Items[world.FurnaceSlotFuel]
	hasInput := !input.IsEmpty()
	hasFuel := !fuel.IsEmpty()

	if isLit || (hasFuel && hasInput) {
		var rec *SmeltingRecipe
		if hasInput {
			rec, _ = MatchSmeltingRecipe(f.ID, input)
		}
		if rec != nil && canFurnaceBurnOutput(f.Items[world.FurnaceSlotOutput], rec.Result) {
			if !isLit {
				burnDur := FurnaceFuelBurnDuration(f.ID, fuel)
				f.LitTimeRemaining = burnDur
				f.LitTotalTime = burnDur
				if burnDur > 0 {
					isLit = true
					changed = true
					slotsChanged = true
					consumeFurnaceFuel(&f.Items)
				}
			}
			if isLit {
				if f.CookingTotalTime <= 0 {
					f.CookingTotalTime = rec.CookingTime
				}
				f.CookingTimer++
				changed = true
				if f.CookingTimer >= f.CookingTotalTime {
					f.CookingTimer = 0
					f.CookingTotalTime = rec.CookingTime
					completeFurnaceBurn(&f.Items, rec.Result)
					if f.RecipesUsed == nil {
						f.RecipesUsed = make(map[string]int32)
					}
					f.RecipesUsed[rec.Name]++
					// If input became empty, reset CookingTotalTime to 0.
					if f.Items[world.FurnaceSlotInput].IsEmpty() {
						f.CookingTotalTime = 0
					}
					slotsChanged = true
				}
			} else if f.CookingTimer != 0 {
				f.CookingTimer = 0
				changed = true
			}
		} else if f.CookingTimer != 0 {
			f.CookingTimer = 0
			changed = true
		}
	} else if !isLit && f.CookingTimer > 0 {
		// Cool down by 2 ticks per tick when fuel runs out mid-smelt.
		next := f.CookingTimer - 2
		if next < 0 {
			next = 0
		}
		if f.CookingTotalTime > 0 && next > f.CookingTotalTime {
			next = f.CookingTotalTime
		}
		f.CookingTimer = next
		changed = true
	}

	litChanged = wasLit != isLit
	if litChanged {
		changed = true
	}
	return changed, slotsChanged, litChanged, isLit
}

func canFurnaceBurnOutput(currentOutput, recipeResult world.ItemStack) bool {
	if recipeResult.IsEmpty() {
		return false
	}
	if currentOutput.IsEmpty() {
		return true
	}
	if currentOutput.ResolvedItemID() != recipeResult.ResolvedItemID() {
		return false
	}
	return currentOutput.Count+recipeResult.Count <= MaxItemStackSize
}

func consumeFurnaceFuel(items *[FurnaceSlotCount]world.ItemStack) {
	fuel := items[world.FurnaceSlotFuel]
	if fuel.IsEmpty() {
		return
	}
	wasLavaBucket := fuel.ResolvedID() == "minecraft:lava_bucket"
	rem := fuel.Count - 1
	if rem <= 0 {
		if wasLavaBucket {
			items[world.FurnaceSlotFuel] = world.NewNamedItemStack("minecraft:bucket", 1)
		} else {
			items[world.FurnaceSlotFuel] = world.ItemStack{}
		}
	} else {
		items[world.FurnaceSlotFuel] = world.NewItemStack(fuel.ResolvedItemID(), rem)
	}
}

func completeFurnaceBurn(items *[FurnaceSlotCount]world.ItemStack, result world.ItemStack) {
	input := items[world.FurnaceSlotInput]
	out := items[world.FurnaceSlotOutput]
	if out.IsEmpty() {
		items[world.FurnaceSlotOutput] = world.NewItemStack(result.ResolvedItemID(), result.Count)
	} else {
		items[world.FurnaceSlotOutput] = world.NewItemStack(out.ResolvedItemID(), out.Count+result.Count)
	}

	// Vanilla wet_sponge -> sponge special case: fills an empty bucket in the fuel slot with water.
	if input.ResolvedID() == "minecraft:wet_sponge" &&
		!items[world.FurnaceSlotFuel].IsEmpty() &&
		items[world.FurnaceSlotFuel].ResolvedID() == "minecraft:bucket" &&
		items[world.FurnaceSlotFuel].Count == 1 {
		items[world.FurnaceSlotFuel] = world.NewNamedItemStack("minecraft:water_bucket", 1)
	}

	rem := input.Count - 1
	if rem <= 0 {
		items[world.FurnaceSlotInput] = world.ItemStack{}
	} else {
		items[world.FurnaceSlotInput] = world.NewItemStack(input.ResolvedItemID(), rem)
	}
}

func calculateFurnaceRecipesXP(used map[string]int32) int {
	if len(used) == 0 {
		return 0
	}
	book := DefaultSmeltingBook()
	totalXP := 0
	for recName, count := range used {
		if count <= 0 {
			continue
		}
		if rec, ok := book.Recipe(recName); ok && rec.Experience > 0 {
			raw := float64(count) * float64(rec.Experience)
			whole := int(math.Floor(raw))
			frac := raw - float64(whole)
			if frac >= 0.5 || (whole == 0 && frac > 0) {
				whole++
			}
			totalXP += whole
		}
	}
	return totalXP
}

// AwardFurnaceExperience computes the total stored XP from a furnace's
// RecipesUsed map, clears the map, and spawns experience orbs at the furnace
// (matching AbstractFurnaceBlockEntity.getRecipesToAwardAndPopExperience).
func (s *Server) AwardFurnaceExperience(x, y, z int) int {
	if s == nil {
		return 0
	}
	fs := s.furnaceState()
	fs.mu.Lock()
	f := fs.furnaces[furnaceKey{x, y, z}]
	if f == nil || len(f.RecipesUsed) == 0 {
		fs.mu.Unlock()
		return 0
	}
	used := f.RecipesUsed
	f.RecipesUsed = make(map[string]int32)
	fs.mu.Unlock()

	totalXP := calculateFurnaceRecipesXP(used)
	if totalXP > 0 {
		s.SpawnExperienceOrbs(float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, int32(totalXP))
	}
	s.persistChunkFurnaces(int32(x>>4), int32(z>>4))
	return totalXP
}

// LoadFurnaces loads all persisted furnace block entities from disk and
// synchronizes their `lit` block states, cooking total times, and comparators.
func (s *Server) LoadFurnaces() error {
	if s == nil {
		return nil
	}
	s.BindNoteBlockAndJukeboxRedstone(world.DimensionOverworld)
	fs := s.furnaceState()
	fs.mu.Lock()
	fs.loadedStore = false
	s.ensureFurnacesLoadedLocked(fs)
	var loaded []world.FurnaceBlockEntity
	for _, f := range fs.furnaces {
		if f == nil {
			continue
		}
		if f.CookingTotalTime <= 0 && !f.Items[world.FurnaceSlotInput].IsEmpty() {
			if rec, ok := MatchSmeltingRecipe(f.ID, f.Items[world.FurnaceSlotInput]); ok {
				f.CookingTotalTime = rec.CookingTime
			}
		}
		loaded = append(loaded, f.Clone())
	}
	fs.mu.Unlock()

	for _, f := range loaded {
		s.syncFurnaceLitBlockStateAt(f.X, f.Y, f.Z, f.LitTimeRemaining > 0)
		s.updateHopperComparatorsAndBroadcast(world.DimensionOverworld, f.X, f.Y, f.Z)
	}
	return nil
}

// UnloadChunkFurnaces persists and removes all furnace block entities in chunk
// (cx, cz) from runtime memory when the chunk is unloaded.
func (s *Server) UnloadChunkFurnaces(cx, cz int32) error {
	if s == nil {
		return nil
	}
	fs := s.furnaceState()
	fs.mu.Lock()
	list := s.collectChunkFurnacesLocked(fs, cx, cz)
	for key := range fs.furnaces {
		if int32(key.x>>4) == cx && int32(key.z>>4) == cz {
			delete(fs.furnaces, key)
		}
	}
	delete(fs.loadedChunk, [2]int32{cx, cz})
	fs.mu.Unlock()

	if s.chunks != nil && s.store != nil {
		return s.chunks.SaveChunkFurnaces(cx, cz, list)
	}
	return nil
}

// ReloadChunkFurnaces reloads furnace block entities for chunk (cx, cz) from
// disk and synchronizes their `lit` block states and comparator outputs.
func (s *Server) ReloadChunkFurnaces(cx, cz int32) error {
	if s == nil {
		return nil
	}
	s.BindNoteBlockAndJukeboxRedstone(world.DimensionOverworld)
	fs := s.furnaceState()
	fs.mu.Lock()
	delete(fs.loadedChunk, [2]int32{cx, cz})
	s.ensureChunkFurnacesLoadedLocked(fs, cx, cz)
	var chunkFurnaces []world.FurnaceBlockEntity
	for key, f := range fs.furnaces {
		if int32(key.x>>4) == cx && int32(key.z>>4) == cz && f != nil {
			if f.CookingTotalTime <= 0 && !f.Items[world.FurnaceSlotInput].IsEmpty() {
				if rec, ok := MatchSmeltingRecipe(f.ID, f.Items[world.FurnaceSlotInput]); ok {
					f.CookingTotalTime = rec.CookingTime
				}
			}
			chunkFurnaces = append(chunkFurnaces, f.Clone())
		}
	}
	fs.mu.Unlock()

	for _, f := range chunkFurnaces {
		s.syncFurnaceLitBlockStateAt(f.X, f.Y, f.Z, f.LitTimeRemaining > 0)
		s.updateHopperComparatorsAndBroadcast(world.DimensionOverworld, f.X, f.Y, f.Z)
	}
	return nil
}

// ChunkFurnaceBlockEntitiesNBT returns vanilla `block_entities` NBT compounds
// for all furnaces, blast furnaces, and smokers in chunk (cx, cz).
func (s *Server) ChunkFurnaceBlockEntitiesNBT(dim string, cx, cz int32) []*nbt.Compound {
	if s == nil {
		return nil
	}
	fs := s.furnaceState()
	fs.mu.Lock()
	s.ensureChunkFurnacesLoadedLocked(fs, cx, cz)
	list := s.collectChunkFurnacesLocked(fs, cx, cz)
	fs.mu.Unlock()

	out := make([]*nbt.Compound, 0, len(list))
	for _, f := range list {
		out = append(out, world.EncodeFurnaceBlockEntityNBT(f))
	}
	return out
}

// LoadChunkFurnaceBlockEntitiesNBT decodes and loads furnace block entities
// from a chunk's `block_entities` NBT list.
func (s *Server) LoadChunkFurnaceBlockEntitiesNBT(dim string, list []*nbt.Compound) int {
	if s == nil || len(list) == 0 {
		return 0
	}
	s.BindNoteBlockAndJukeboxRedstone(world.DimensionOverworld)
	fs := s.furnaceState()
	fs.mu.Lock()
	var loaded []world.FurnaceBlockEntity
	for _, c := range list {
		f, ok := world.DecodeFurnaceBlockEntityNBT(c)
		if !ok {
			continue
		}
		f.ID = s.resolveFurnaceIDAt(f.X, f.Y, f.Z, f.ID)
		if f.RecipesUsed == nil {
			f.RecipesUsed = make(map[string]int32)
		}
		if f.CookingTotalTime <= 0 && !f.Items[world.FurnaceSlotInput].IsEmpty() {
			if rec, ok := MatchSmeltingRecipe(f.ID, f.Items[world.FurnaceSlotInput]); ok {
				f.CookingTotalTime = rec.CookingTime
			}
		}
		cp := f.Clone()
		fs.furnaces[furnaceKey{f.X, f.Y, f.Z}] = &cp
		fs.loadedChunk[[2]int32{int32(f.X >> 4), int32(f.Z >> 4)}] = true
		loaded = append(loaded, cp)
	}
	fs.mu.Unlock()

	for _, f := range loaded {
		s.syncFurnaceLitBlockStateAt(f.X, f.Y, f.Z, f.LitTimeRemaining > 0)
		s.updateHopperComparatorsAndBroadcast(world.DimensionOverworld, f.X, f.Y, f.Z)
	}
	return len(loaded)
}

