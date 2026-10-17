package server

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"regionio/internal/nbt"
	"regionio/internal/protocol"
	"regionio/internal/world"
)

// RecipeBookType enum ordinals (net.minecraft.world.inventory.RecipeBookType)
// in protocol 775 (Minecraft 26.1.2).
const (
	RecipeBookTypeCrafting     int32 = 0
	RecipeBookTypeFurnace      int32 = 1
	RecipeBookTypeBlastFurnace int32 = 2
	RecipeBookTypeSmoker       int32 = 3
)

// BuiltInRegistries.RECIPE_BOOK_CATEGORY IDs in protocol 775 (26.1.2).
const (
	RecipeCategoryCraftingBuildingBlocks int32 = 0
	RecipeCategoryCraftingRedstone       int32 = 1
	RecipeCategoryCraftingEquipment      int32 = 2
	RecipeCategoryCraftingMisc           int32 = 3
	RecipeCategoryFurnaceFood            int32 = 4
	RecipeCategoryFurnaceBlocks          int32 = 5
	RecipeCategoryFurnaceMisc            int32 = 6
	RecipeCategoryBlastFurnaceBlocks     int32 = 7
	RecipeCategoryBlastFurnaceMisc       int32 = 8
	RecipeCategorySmokerFood             int32 = 9
	RecipeCategoryStonecutter            int32 = 10
	RecipeCategorySmithing               int32 = 11
	RecipeCategoryCampfire               int32 = 12
)

// BuiltInRegistries.RECIPE_DISPLAY IDs in protocol 775 (26.1.2).
const (
	RecipeDisplayTypeCraftingShapeless int32 = 0
	RecipeDisplayTypeCraftingShaped    int32 = 1
	RecipeDisplayTypeFurnace           int32 = 2
	RecipeDisplayTypeStonecutter       int32 = 3
	RecipeDisplayTypeSmithing          int32 = 4
)

// BuiltInRegistries.SLOT_DISPLAY IDs in protocol 775 (26.1.2).
const (
	SlotDisplayTypeEmpty             int32 = 0
	SlotDisplayTypeAnyFuel           int32 = 1
	SlotDisplayTypeWithAnyPotion     int32 = 2
	SlotDisplayTypeOnlyWithComponent int32 = 3
	SlotDisplayTypeItem              int32 = 4
	SlotDisplayTypeItemStack         int32 = 5
	SlotDisplayTypeTag               int32 = 6
	SlotDisplayTypeDyed              int32 = 7
	SlotDisplayTypeSmithingTrim      int32 = 8
	SlotDisplayTypeWithRemainder     int32 = 9
	SlotDisplayTypeComposite         int32 = 10
)

// ClientboundRecipeBookAddPacket.Entry bitflags in protocol 775 (26.1.2).
const (
	RecipeBookAddFlagNotification byte = 1 << 0 // 0x01: show toast notification
	RecipeBookAddFlagHighlight    byte = 1 << 1 // 0x02: highlight in recipe book until seen
)

// RecipeBookTypeSettings holds the GUI open and craftable-filter flags for one
// RecipeBookType tab.
type RecipeBookTypeSettings struct {
	Open      bool
	Filtering bool
}

// RecipeBookSettings holds the four vanilla recipe book UI settings matching
// net.minecraft.stats.RecipeBookSettings.
type RecipeBookSettings struct {
	Crafting     RecipeBookTypeSettings
	Furnace      RecipeBookTypeSettings
	BlastFurnace RecipeBookTypeSettings
	Smoker       RecipeBookTypeSettings
}

// TypeSettings returns the settings for the given RecipeBookType ordinal.
func (s RecipeBookSettings) TypeSettings(bookType int32) RecipeBookTypeSettings {
	switch bookType {
	case RecipeBookTypeCrafting:
		return s.Crafting
	case RecipeBookTypeFurnace:
		return s.Furnace
	case RecipeBookTypeBlastFurnace:
		return s.BlastFurnace
	case RecipeBookTypeSmoker:
		return s.Smoker
	default:
		return RecipeBookTypeSettings{}
	}
}

// WithTypeSettings returns a copy of s with the specified bookType updated.
func (s RecipeBookSettings) WithTypeSettings(bookType int32, open, filtering bool) (RecipeBookSettings, bool) {
	ts := RecipeBookTypeSettings{Open: open, Filtering: filtering}
	switch bookType {
	case RecipeBookTypeCrafting:
		s.Crafting = ts
	case RecipeBookTypeFurnace:
		s.Furnace = ts
	case RecipeBookTypeBlastFurnace:
		s.BlastFurnace = ts
	case RecipeBookTypeSmoker:
		s.Smoker = ts
	default:
		return s, false
	}
	return s, true
}

// RecipeBookAddEntry represents one entry in ClientboundRecipeBookAddPacket.
type RecipeBookAddEntry struct {
	DisplayID    int32
	Recipe       *CraftingRecipe
	Notification bool
	Highlight    bool
}

// Flags returns the protocol 775 byte bitmask for this entry.
func (e RecipeBookAddEntry) Flags() byte {
	var flags byte
	if e.Notification {
		flags |= RecipeBookAddFlagNotification
	}
	if e.Highlight {
		flags |= RecipeBookAddFlagHighlight
	}
	return flags
}

type recipeDisplayIndex struct {
	byDisplayID []*CraftingRecipe
	toDisplayID map[string]int32
	groupToID   map[string]int32
	byItemID    map[string][]string // itemID -> sorted recipe names unlocked by acquiring itemID
}

var (
	defaultDisplayIdxOnce sync.Once
	defaultDisplayIdx     *recipeDisplayIndex
)

func getRecipeDisplayIndex() *recipeDisplayIndex {
	defaultDisplayIdxOnce.Do(func() {
		book := DefaultRecipeBook()
		idx := &recipeDisplayIndex{
			byDisplayID: make([]*CraftingRecipe, len(book.recipes)),
			toDisplayID: make(map[string]int32, len(book.recipes)),
			groupToID:   make(map[string]int32, 64),
			byItemID:    make(map[string][]string, 512),
		}
		itemSet := make(map[string]map[string]bool, 512)
		addUnlock := func(itemID, recipeName string) {
			itemID = canonicalResourceName(itemID)
			recipeName = canonicalResourceName(recipeName)
			if itemID == "" || recipeName == "" {
				return
			}
			m := itemSet[itemID]
			if m == nil {
				m = make(map[string]bool)
				itemSet[itemID] = m
			}
			m[recipeName] = true
		}

		var nextGroupID int32 = 1
		for i, rec := range book.recipes {
			displayID := int32(i)
			idx.byDisplayID[i] = rec
			idx.toDisplayID[rec.Name] = displayID
			if g := strings.TrimSpace(rec.Group); g != "" {
				if _, exists := idx.groupToID[g]; !exists {
					idx.groupToID[g] = nextGroupID
					nextGroupID++
				}
			}

			// Unlock this recipe whenever any of its ingredient items or its result item is acquired.
			for _, ing := range rec.Pattern {
				for itemID := range ing.Allowed {
					addUnlock(itemID, rec.Name)
				}
			}
			for _, ing := range rec.Ingredients {
				for itemID := range ing.Allowed {
					addUnlock(itemID, rec.Name)
				}
			}
			if resID := rec.Result.ResolvedID(); resID != "" {
				addUnlock(resID, rec.Name)
			}
		}

		// Also link logs/stems/bamboo -> planks & crafting_table & stick so players
		// picking up their first log immediately unlock planks and basic wood recipes.
		for _, logItem := range book.TagItems("minecraft:logs") {
			addUnlock(logItem, "minecraft:crafting_table")
			addUnlock(logItem, "minecraft:stick")
		}
		for _, plankItem := range book.TagItems("minecraft:planks") {
			addUnlock(plankItem, "minecraft:crafting_table")
			addUnlock(plankItem, "minecraft:stick")
			addUnlock(plankItem, "minecraft:chest")
			addUnlock(plankItem, "minecraft:bowl")
			addUnlock(plankItem, "minecraft:wooden_pickaxe")
			addUnlock(plankItem, "minecraft:wooden_axe")
			addUnlock(plankItem, "minecraft:wooden_shovel")
			addUnlock(plankItem, "minecraft:wooden_hoe")
			addUnlock(plankItem, "minecraft:wooden_sword")
		}

		for itemID, recMap := range itemSet {
			list := make([]string, 0, len(recMap))
			for rName := range recMap {
				if _, ok := idx.toDisplayID[rName]; ok {
					list = append(list, rName)
				}
			}
			sort.Strings(list)
			idx.byItemID[itemID] = list
		}

		defaultDisplayIdx = idx
	})
	return defaultDisplayIdx
}

// RecipeByDisplayID looks up a CraftingRecipe by its protocol 775 RecipeDisplayId index.
func RecipeByDisplayID(displayID int32) (*CraftingRecipe, bool) {
	idx := getRecipeDisplayIndex()
	if displayID < 0 || int(displayID) >= len(idx.byDisplayID) {
		return nil, false
	}
	return idx.byDisplayID[displayID], true
}

// DisplayIDForRecipe returns the protocol 775 RecipeDisplayId index for a recipe name.
func DisplayIDForRecipe(name string) (int32, bool) {
	idx := getRecipeDisplayIndex()
	id, ok := idx.toDisplayID[canonicalResourceName(name)]
	return id, ok
}

// RecipeBookCategoryID maps a recipe's category string to its protocol 775
// BuiltInRegistries.RECIPE_BOOK_CATEGORY VarInt ID.
func RecipeBookCategoryID(category string) int32 {
	switch strings.ToLower(strings.TrimSpace(category)) {
	case "building", "crafting_building_blocks", "minecraft:crafting_building_blocks":
		return RecipeCategoryCraftingBuildingBlocks
	case "redstone", "crafting_redstone", "minecraft:crafting_redstone":
		return RecipeCategoryCraftingRedstone
	case "equipment", "crafting_equipment", "minecraft:crafting_equipment":
		return RecipeCategoryCraftingEquipment
	case "misc", "crafting_misc", "minecraft:crafting_misc", "":
		return RecipeCategoryCraftingMisc
	case "furnace_food", "minecraft:furnace_food":
		return RecipeCategoryFurnaceFood
	case "furnace_blocks", "minecraft:furnace_blocks":
		return RecipeCategoryFurnaceBlocks
	case "furnace_misc", "minecraft:furnace_misc":
		return RecipeCategoryFurnaceMisc
	case "blast_furnace_blocks", "minecraft:blast_furnace_blocks":
		return RecipeCategoryBlastFurnaceBlocks
	case "blast_furnace_misc", "minecraft:blast_furnace_misc":
		return RecipeCategoryBlastFurnaceMisc
	case "smoker_food", "minecraft:smoker_food":
		return RecipeCategorySmokerFood
	case "stonecutter", "minecraft:stonecutter":
		return RecipeCategoryStonecutter
	case "smithing", "minecraft:smithing":
		return RecipeCategorySmithing
	case "campfire", "minecraft:campfire":
		return RecipeCategoryCampfire
	default:
		return RecipeCategoryCraftingMisc
	}
}

// --- Per-Player Recipe Book Tracker ---

type playerRecipeBookTracker struct {
	mu        sync.Mutex
	settings  RecipeBookSettings
	known     map[string]bool
	highlight map[string]bool
}

var playerRecipeBooks sync.Map // map[*PlayerSession]*playerRecipeBookTracker

func getRecipeBookTracker(session *PlayerSession) *playerRecipeBookTracker {
	if session == nil {
		return nil
	}
	if v, ok := playerRecipeBooks.Load(session); ok {
		return v.(*playerRecipeBookTracker)
	}
	tr := &playerRecipeBookTracker{
		known:     make(map[string]bool),
		highlight: make(map[string]bool),
	}
	actual, _ := playerRecipeBooks.LoadOrStore(session, tr)
	return actual.(*playerRecipeBookTracker)
}

// RecipeBookSettings returns session's current recipe book UI settings.
func (session *PlayerSession) RecipeBookSettings() RecipeBookSettings {
	if session == nil {
		return RecipeBookSettings{}
	}
	tr := getRecipeBookTracker(session)
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return tr.settings
}

// SetRecipeBookSetting updates the open and filtering flags for bookType on session.
func (session *PlayerSession) SetRecipeBookSetting(bookType int32, open, filtering bool) bool {
	if session == nil {
		return false
	}
	tr := getRecipeBookTracker(session)
	tr.mu.Lock()
	defer tr.mu.Unlock()
	updated, ok := tr.settings.WithTypeSettings(bookType, open, filtering)
	if !ok {
		return false
	}
	tr.settings = updated
	return true
}

// HasUnlockedRecipe reports whether session has unlocked recipeName.
func (session *PlayerSession) HasUnlockedRecipe(recipeName string) bool {
	if session == nil {
		return false
	}
	name := canonicalResourceName(recipeName)
	tr := getRecipeBookTracker(session)
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return tr.known[name]
}

// IsRecipeHighlighted reports whether recipeName is currently marked as
// newly unlocked (unseen) in session's recipe book.
func (session *PlayerSession) IsRecipeHighlighted(recipeName string) bool {
	if session == nil {
		return false
	}
	name := canonicalResourceName(recipeName)
	tr := getRecipeBookTracker(session)
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return tr.highlight[name]
}

// UnlockedRecipes returns a sorted slice of all recipe IDs unlocked by session.
func (session *PlayerSession) UnlockedRecipes() []string {
	if session == nil {
		return nil
	}
	tr := getRecipeBookTracker(session)
	tr.mu.Lock()
	defer tr.mu.Unlock()
	out := make([]string, 0, len(tr.known))
	for k := range tr.known {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// HighlightedRecipes returns a sorted slice of all recipe IDs highlighted in
// session's recipe book.
func (session *PlayerSession) HighlightedRecipes() []string {
	if session == nil {
		return nil
	}
	tr := getRecipeBookTracker(session)
	tr.mu.Lock()
	defer tr.mu.Unlock()
	out := make([]string, 0, len(tr.highlight))
	for k := range tr.highlight {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// --- NBT Persistence (Vanilla 26.1.2 ServerRecipeBook$Packed) ---

// EncodePlayerRecipeBookNBT serializes session's recipe book state into an NBT
// compound matching vanilla 26.1.2 ServerRecipeBook$Packed ("recipeBook").
func EncodePlayerRecipeBookNBT(session *PlayerSession) *nbt.Compound {
	root := nbt.NewCompound()
	if session == nil {
		return root
	}
	tr := getRecipeBookTracker(session)
	tr.mu.Lock()
	settings := tr.settings
	known := make([]string, 0, len(tr.known))
	for k := range tr.known {
		known = append(known, k)
	}
	highlight := make([]string, 0, len(tr.highlight))
	for k := range tr.highlight {
		highlight = append(highlight, k)
	}
	tr.mu.Unlock()

	sort.Strings(known)
	sort.Strings(highlight)

	boolByte := func(b bool) nbt.Byte {
		if b {
			return 1
		}
		return 0
	}
	root.Set("isGuiOpen", boolByte(settings.Crafting.Open))
	root.Set("isFilteringCraftable", boolByte(settings.Crafting.Filtering))
	root.Set("isFurnaceGuiOpen", boolByte(settings.Furnace.Open))
	root.Set("isFurnaceFilteringCraftable", boolByte(settings.Furnace.Filtering))
	root.Set("isBlastingFurnaceGuiOpen", boolByte(settings.BlastFurnace.Open))
	root.Set("isBlastingFurnaceFilteringCraftable", boolByte(settings.BlastFurnace.Filtering))
	root.Set("isSmokerGuiOpen", boolByte(settings.Smoker.Open))
	root.Set("isSmokerFilteringCraftable", boolByte(settings.Smoker.Filtering))

	recipesElems := make([]nbt.Tag, len(known))
	for i, name := range known {
		recipesElems[i] = nbt.String(name)
	}
	root.Set("recipes", nbt.List{
		ElemID: nbt.TagString,
		Elems:  recipesElems,
	})

	displayElems := make([]nbt.Tag, len(highlight))
	for i, name := range highlight {
		displayElems[i] = nbt.String(name)
	}
	root.Set("toBeDisplayed", nbt.List{
		ElemID: nbt.TagString,
		Elems:  displayElems,
	})
	return root
}

// DecodePlayerRecipeBookNBT restores session's recipe book state from a
// vanilla 26.1.2 "recipeBook" NBT compound.
func DecodePlayerRecipeBookNBT(session *PlayerSession, comp *nbt.Compound) {
	if session == nil || comp == nil {
		return
	}
	readBool := func(key string) bool {
		tag, ok := comp.Get(key)
		if !ok {
			return false
		}
		if b, ok := tag.(nbt.Byte); ok {
			return b != 0
		}
		return false
	}

	settings := RecipeBookSettings{
		Crafting: RecipeBookTypeSettings{
			Open:      readBool("isGuiOpen"),
			Filtering: readBool("isFilteringCraftable"),
		},
		Furnace: RecipeBookTypeSettings{
			Open:      readBool("isFurnaceGuiOpen"),
			Filtering: readBool("isFurnaceFilteringCraftable"),
		},
		BlastFurnace: RecipeBookTypeSettings{
			Open:      readBool("isBlastingFurnaceGuiOpen"),
			Filtering: readBool("isBlastingFurnaceFilteringCraftable"),
		},
		Smoker: RecipeBookTypeSettings{
			Open:      readBool("isSmokerGuiOpen"),
			Filtering: readBool("isSmokerFilteringCraftable"),
		},
	}

	extractStrings := func(key string) []string {
		tag, ok := comp.Get(key)
		if !ok {
			return nil
		}
		var elems []nbt.Tag
		switch v := tag.(type) {
		case *nbt.List:
			if v != nil && (v.ElemID == nbt.TagString || len(v.Elems) == 0) {
				elems = v.Elems
			}
		case nbt.List:
			if v.ElemID == nbt.TagString || len(v.Elems) == 0 {
				elems = v.Elems
			}
		}
		var out []string
		for _, el := range elems {
			if s, ok := el.(nbt.String); ok {
				out = append(out, string(s))
			}
		}
		return out
	}

	idx := getRecipeDisplayIndex()
	known := make(map[string]bool)
	for _, raw := range extractStrings("recipes") {
		name := canonicalResourceName(raw)
		if _, valid := idx.toDisplayID[name]; valid {
			known[name] = true
		}
	}

	highlight := make(map[string]bool)
	for _, raw := range extractStrings("toBeDisplayed") {
		name := canonicalResourceName(raw)
		if _, valid := idx.toDisplayID[name]; valid && known[name] {
			highlight[name] = true
		}
	}

	tr := getRecipeBookTracker(session)
	tr.mu.Lock()
	tr.settings = settings
	tr.known = known
	tr.highlight = highlight
	tr.mu.Unlock()
}

// SaveRecipeBookToPlayerCompound writes session's recipe book state under the
// "recipeBook" key of a playerdata root NBT compound.
func SaveRecipeBookToPlayerCompound(session *PlayerSession, root *nbt.Compound) {
	if session == nil || root == nil {
		return
	}
	root.Set("recipeBook", EncodePlayerRecipeBookNBT(session))
}

// LoadRecipeBookFromPlayerCompound restores session's recipe book state from
// the "recipeBook" key of a playerdata root NBT compound, if present.
func LoadRecipeBookFromPlayerCompound(session *PlayerSession, root *nbt.Compound) {
	if session == nil || root == nil {
		return
	}
	if tag, ok := root.Get("recipeBook"); ok {
		if comp, ok := tag.(*nbt.Compound); ok {
			DecodePlayerRecipeBookNBT(session, comp)
		}
	}
}

// --- Protocol 775 Packet Codecs ---

// EncodeRecipeBookSettingsPacket encodes a protocol 775 clientbound
// recipe_book_settings (0x4c) packet body matching
// ClientboundRecipeBookSettingsPacket.STREAM_CODEC.
func EncodeRecipeBookSettingsPacket(settings RecipeBookSettings) []byte {
	w := protocol.NewWriter(8)
	w.Bool(settings.Crafting.Open)
	w.Bool(settings.Crafting.Filtering)
	w.Bool(settings.Furnace.Open)
	w.Bool(settings.Furnace.Filtering)
	w.Bool(settings.BlastFurnace.Open)
	w.Bool(settings.BlastFurnace.Filtering)
	w.Bool(settings.Smoker.Open)
	w.Bool(settings.Smoker.Filtering)
	return w.Bytes()
}

// DecodeRecipeBookSettingsPacket decodes a protocol 775 clientbound
// recipe_book_settings (0x4c) packet body.
func DecodeRecipeBookSettingsPacket(r *protocol.Reader) (RecipeBookSettings, error) {
	var s RecipeBookSettings
	var err error
	if s.Crafting.Open, err = r.Bool(); err != nil {
		return s, err
	}
	if s.Crafting.Filtering, err = r.Bool(); err != nil {
		return s, err
	}
	if s.Furnace.Open, err = r.Bool(); err != nil {
		return s, err
	}
	if s.Furnace.Filtering, err = r.Bool(); err != nil {
		return s, err
	}
	if s.BlastFurnace.Open, err = r.Bool(); err != nil {
		return s, err
	}
	if s.BlastFurnace.Filtering, err = r.Bool(); err != nil {
		return s, err
	}
	if s.Smoker.Open, err = r.Bool(); err != nil {
		return s, err
	}
	if s.Smoker.Filtering, err = r.Bool(); err != nil {
		return s, err
	}
	return s, nil
}

// EncodeRecipeBookRemovePacket encodes a protocol 775 clientbound
// recipe_book_remove (0x4b) packet body matching
// ClientboundRecipeBookRemovePacket.STREAM_CODEC.
func EncodeRecipeBookRemovePacket(displayIDs []int32) []byte {
	w := protocol.NewWriter(4 + len(displayIDs)*4)
	w.VarInt(int32(len(displayIDs)))
	for _, id := range displayIDs {
		w.VarInt(id)
	}
	return w.Bytes()
}

// DecodeRecipeBookRemovePacket decodes a protocol 775 clientbound
// recipe_book_remove (0x4b) packet body.
func DecodeRecipeBookRemovePacket(r *protocol.Reader) ([]int32, error) {
	n, err := r.VarInt()
	if err != nil {
		return nil, err
	}
	if n < 0 || n > 65536 {
		return nil, fmt.Errorf("invalid recipe_book_remove count %d", n)
	}
	ids := make([]int32, n)
	for i := int32(0); i < n; i++ {
		id, err := r.VarInt()
		if err != nil {
			return nil, err
		}
		ids[i] = id
	}
	return ids, nil
}

// EncodeRecipeBookAddPacket encodes a protocol 775 clientbound
// recipe_book_add (0x4a) packet body matching
// ClientboundRecipeBookAddPacket.STREAM_CODEC.
func EncodeRecipeBookAddPacket(entries []RecipeBookAddEntry, replace bool) []byte {
	w := protocol.NewWriter(64 + len(entries)*32)
	w.VarInt(int32(len(entries)))
	for _, entry := range entries {
		writeRecipeDisplayEntry(w, entry.DisplayID, entry.Recipe)
		w.Byte(entry.Flags())
	}
	w.Bool(replace)
	return w.Bytes()
}

// EncodePlaceGhostRecipePacket encodes a protocol 775 clientbound
// place_ghost_recipe (0x3f) packet body matching
// ClientboundPlaceGhostRecipePacket.STREAM_CODEC.
func EncodePlaceGhostRecipePacket(containerID int32, rec *CraftingRecipe) []byte {
	w := protocol.NewWriter(64)
	w.VarInt(containerID)
	writeCraftingRecipeDisplay(w, rec)
	return w.Bytes()
}

// EncodeUpdateRecipesPacket encodes a protocol 775 clientbound
// update_recipes (0x85) packet body matching
// ClientboundUpdateRecipesPacket.STREAM_CODEC.
func EncodeUpdateRecipesPacket() []byte {
	w := protocol.NewWriter(256)

	// 1. itemSets: Map<ResourceKey<RecipePropertySet>, RecipePropertySet>
	// Populate the 7 vanilla property sets so client container slots accept inputs.
	type propSet struct {
		key   string
		items []int32
	}
	collectIDs := func(names []string) []int32 {
		seen := make(map[int32]bool, len(names))
		var out []int32
		for _, name := range names {
			if id, ok := world.ItemNameToID(name); ok && id > 0 && !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
		sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
		return out
	}

	var furnaceInputs, blastInputs, smokerInputs []string
	sb := DefaultSmeltingBook()
	if sb != nil {
		for _, rec := range sb.byType["minecraft:smelting"] {
			for inName := range rec.Ingredient.Allowed {
				furnaceInputs = append(furnaceInputs, inName)
			}
		}
		for _, rec := range sb.byType["minecraft:blasting"] {
			for inName := range rec.Ingredient.Allowed {
				blastInputs = append(blastInputs, inName)
			}
		}
		for _, rec := range sb.byType["minecraft:smoking"] {
			for inName := range rec.Ingredient.Allowed {
				smokerInputs = append(smokerInputs, inName)
			}
		}
	}

	sets := []propSet{
		{key: "minecraft:furnace_input", items: collectIDs(furnaceInputs)},
		{key: "minecraft:blast_furnace_input", items: collectIDs(blastInputs)},
		{key: "minecraft:smoker_input", items: collectIDs(smokerInputs)},
		{key: "minecraft:campfire_input", items: collectIDs(smokerInputs)},
		{key: "minecraft:smithing_base", items: nil},
		{key: "minecraft:smithing_template", items: nil},
		{key: "minecraft:smithing_addition", items: nil},
	}

	w.VarInt(int32(len(sets)))
	for _, ps := range sets {
		w.String(ps.key)
		w.VarInt(int32(len(ps.items)))
		for _, id := range ps.items {
			w.VarInt(id)
		}
	}

	// 2. stonecutterRecipes: SelectableRecipe.SingleInputSet<StonecutterRecipe>
	scRecipes := DefaultStonecutterBook().All()
	w.VarInt(int32(len(scRecipes)))
	for _, sc := range scRecipes {
		writeIngredientContents(w, sc.Ingredient)
		writeItemStackSlotDisplay(w, sc.Result)
	}
	return w.Bytes()
}

func writeRecipeDisplayEntry(w *protocol.Writer, displayID int32, rec *CraftingRecipe) {
	// 1. id: RecipeDisplayId (VarInt)
	w.VarInt(displayID)

	// 2. display: RecipeDisplay
	writeCraftingRecipeDisplay(w, rec)

	// 3. group: OptionalInt (ByteBufCodecs.OPTIONAL_VAR_INT: 0 = empty, v+1 = present)
	idx := getRecipeDisplayIndex()
	var groupOpt int32
	if rec != nil && rec.Group != "" {
		if gid, ok := idx.groupToID[strings.TrimSpace(rec.Group)]; ok && gid > 0 {
			groupOpt = gid + 1
		}
	}
	w.VarInt(groupOpt)

	// 4. category: RecipeBookCategory (VarInt registry ID)
	cat := RecipeCategoryCraftingMisc
	if rec != nil {
		cat = RecipeBookCategoryID(rec.Category)
	}
	w.VarInt(cat)

	// 5. craftingRequirements: Optional<List<Ingredient>>
	reqs := collectCraftingRequirements(rec)
	if len(reqs) == 0 {
		w.Bool(false)
		return
	}
	w.Bool(true)
	w.VarInt(int32(len(reqs)))
	for _, ing := range reqs {
		writeIngredientContents(w, ing)
	}
}

func collectCraftingRequirements(rec *CraftingRecipe) []Ingredient {
	if rec == nil {
		return nil
	}
	if rec.Type == "minecraft:crafting_shaped" {
		var out []Ingredient
		for _, ing := range rec.Pattern {
			if !ing.IsEmpty() {
				out = append(out, ing)
			}
		}
		return out
	}
	var out []Ingredient
	for _, ing := range rec.Ingredients {
		if !ing.IsEmpty() {
			out = append(out, ing)
		}
	}
	return out
}

func writeCraftingRecipeDisplay(w *protocol.Writer, rec *CraftingRecipe) {
	craftingTableID, ok := world.ItemNameToID("minecraft:crafting_table")
	if !ok || craftingTableID <= 0 {
		craftingTableID = 1
	}

	if rec != nil && rec.Type == "minecraft:crafting_shaped" {
		w.VarInt(RecipeDisplayTypeCraftingShaped)
		w.VarInt(int32(rec.Width))
		w.VarInt(int32(rec.Height))
		w.VarInt(int32(len(rec.Pattern)))
		for _, ing := range rec.Pattern {
			writeIngredientSlotDisplay(w, ing)
		}
		writeItemStackSlotDisplay(w, rec.Result)
		// craftingStation: SlotDisplay$ItemSlotDisplay(minecraft:crafting_table)
		w.VarInt(SlotDisplayTypeItem)
		w.VarInt(craftingTableID)
		return
	}

	// Default / minecraft:crafting_shapeless
	w.VarInt(RecipeDisplayTypeCraftingShapeless)
	if rec == nil {
		w.VarInt(0)
		w.VarInt(SlotDisplayTypeEmpty)
	} else {
		w.VarInt(int32(len(rec.Ingredients)))
		for _, ing := range rec.Ingredients {
			writeIngredientSlotDisplay(w, ing)
		}
		writeItemStackSlotDisplay(w, rec.Result)
	}
	// craftingStation: SlotDisplay$ItemSlotDisplay(minecraft:crafting_table)
	w.VarInt(SlotDisplayTypeItem)
	w.VarInt(craftingTableID)
}

func ingredientItemIDs(ing Ingredient) []int32 {
	if len(ing.Allowed) == 0 {
		return nil
	}
	ids := make([]int32, 0, len(ing.Allowed))
	for name := range ing.Allowed {
		if id, ok := world.ItemNameToID(name); ok && id > 0 {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func writeIngredientSlotDisplay(w *protocol.Writer, ing Ingredient) {
	ids := ingredientItemIDs(ing)
	switch len(ids) {
	case 0:
		w.VarInt(SlotDisplayTypeEmpty)
	case 1:
		w.VarInt(SlotDisplayTypeItem)
		w.VarInt(ids[0])
	default:
		w.VarInt(SlotDisplayTypeComposite)
		w.VarInt(int32(len(ids)))
		for _, id := range ids {
			w.VarInt(SlotDisplayTypeItem)
			w.VarInt(id)
		}
	}
}

func writeItemStackSlotDisplay(w *protocol.Writer, stack world.ItemStack) {
	itemID := stack.ResolvedItemID()
	if itemID <= 0 {
		itemID = 1 // fallback to non-air stone so ItemStackTemplate never sees air
	}
	count := stack.Count
	if count <= 0 {
		count = 1
	}
	if count > 99 {
		count = 99
	}
	w.VarInt(SlotDisplayTypeItemStack)
	w.VarInt(itemID)
	w.VarInt(count)
	w.VarInt(0) // added components
	w.VarInt(0) // removed components
}

func writeIngredientContents(w *protocol.Writer, ing Ingredient) {
	// ByteBufCodecs.holderSet(Registries.ITEM):
	// Direct HolderSet writes VarInt(size + 1) followed by each item's VarInt registry ID.
	ids := ingredientItemIDs(ing)
	if len(ids) == 0 {
		ids = []int32{1}
	}
	w.VarInt(int32(len(ids) + 1))
	for _, id := range ids {
		w.VarInt(id)
	}
}

// DecodePlaceRecipePacket parses a protocol 775 serverbound place_recipe (0x27)
// packet body matching ServerboundPlaceRecipePacket.STREAM_CODEC.
func DecodePlaceRecipePacket(r *protocol.Reader) (containerID int32, recipeDisplayID int32, useMaxItems bool, err error) {
	containerID, err = r.VarInt()
	if err != nil {
		return 0, 0, false, err
	}
	recipeDisplayID, err = r.VarInt()
	if err != nil {
		return containerID, 0, false, err
	}
	useMaxItems, err = r.Bool()
	if err != nil {
		return containerID, recipeDisplayID, false, err
	}
	return containerID, recipeDisplayID, useMaxItems, nil
}

// DecodeRecipeBookChangeSettingsPacket parses a protocol 775 serverbound
// recipe_book_change_settings (0x2e) packet body matching
// ServerboundRecipeBookChangeSettingsPacket.STREAM_CODEC.
func DecodeRecipeBookChangeSettingsPacket(r *protocol.Reader) (bookType int32, isOpen bool, isFiltering bool, err error) {
	bookType, err = r.VarInt()
	if err != nil {
		return 0, false, false, err
	}
	isOpen, err = r.Bool()
	if err != nil {
		return bookType, false, false, err
	}
	isFiltering, err = r.Bool()
	if err != nil {
		return bookType, isOpen, false, err
	}
	return bookType, isOpen, isFiltering, nil
}

// DecodeRecipeBookSeenRecipePacket parses a protocol 775 serverbound
// recipe_book_seen_recipe (0x2f) packet body matching
// ServerboundRecipeBookSeenRecipePacket.STREAM_CODEC.
func DecodeRecipeBookSeenRecipePacket(r *protocol.Reader) (recipeDisplayID int32, err error) {
	return r.VarInt()
}

// --- Server Synchronization & Unlocking ---

// SendInitialRecipeBook sends ClientboundUpdateRecipesPacket (0x85),
// ClientboundRecipeBookSettingsPacket (0x4c), and a full replace
// ClientboundRecipeBookAddPacket (0x4a) to session upon login.
func (s *Server) SendInitialRecipeBook(session *PlayerSession) error {
	if session == nil {
		return errors.New("server: nil player session")
	}
	if session.send == nil {
		return nil
	}

	if err := session.send(protocol.PlayUpdateRecipes, EncodeUpdateRecipesPacket()); err != nil {
		return err
	}

	settings := session.RecipeBookSettings()
	if err := session.send(protocol.PlayRecipeBookSettings, EncodeRecipeBookSettingsPacket(settings)); err != nil {
		return err
	}

	idx := getRecipeDisplayIndex()
	tr := getRecipeBookTracker(session)
	tr.mu.Lock()
	names := make([]string, 0, len(tr.known))
	for name := range tr.known {
		names = append(names, name)
	}
	sort.Strings(names)

	entries := make([]RecipeBookAddEntry, 0, len(names))
	for _, name := range names {
		displayID, ok := idx.toDisplayID[name]
		if !ok {
			continue
		}
		entries = append(entries, RecipeBookAddEntry{
			DisplayID:    displayID,
			Recipe:       idx.byDisplayID[displayID],
			Notification: false,
			Highlight:    tr.highlight[name],
		})
	}
	tr.mu.Unlock()

	return session.send(protocol.PlayRecipeBookAdd, EncodeRecipeBookAddPacket(entries, true))
}

// UnlockRecipesByNames unlocks the specified recipe names for session, sending
// an incremental ClientboundRecipeBookAddPacket (replace=false) for any newly
// unlocked recipes. Returns the count of newly unlocked recipes.
func (s *Server) UnlockRecipesByNames(session *PlayerSession, names []string) int {
	if session == nil || len(names) == 0 {
		return 0
	}
	idx := getRecipeDisplayIndex()
	tr := getRecipeBookTracker(session)

	tr.mu.Lock()
	var added []RecipeBookAddEntry
	for _, rawName := range names {
		name := canonicalResourceName(rawName)
		displayID, ok := idx.toDisplayID[name]
		if !ok || tr.known[name] {
			continue
		}
		tr.known[name] = true
		tr.highlight[name] = true
		added = append(added, RecipeBookAddEntry{
			DisplayID:    displayID,
			Recipe:       idx.byDisplayID[displayID],
			Notification: true,
			Highlight:    true,
		})
	}
	tr.mu.Unlock()

	if len(added) > 0 && session.send != nil {
		_ = session.send(protocol.PlayRecipeBookAdd, EncodeRecipeBookAddPacket(added, false))
	}
	return len(added)
}

// LockRecipesByNames removes the specified recipe names from session's recipe
// book and sends ClientboundRecipeBookRemovePacket for any removed recipes.
// Returns the count of removed recipes.
func (s *Server) LockRecipesByNames(session *PlayerSession, names []string) int {
	if session == nil || len(names) == 0 {
		return 0
	}
	idx := getRecipeDisplayIndex()
	tr := getRecipeBookTracker(session)

	tr.mu.Lock()
	var removedIDs []int32
	for _, rawName := range names {
		name := canonicalResourceName(rawName)
		if !tr.known[name] {
			continue
		}
		delete(tr.known, name)
		delete(tr.highlight, name)
		if displayID, ok := idx.toDisplayID[name]; ok {
			removedIDs = append(removedIDs, displayID)
		}
	}
	tr.mu.Unlock()

	if len(removedIDs) > 0 && session.send != nil {
		_ = session.send(protocol.PlayRecipeBookRemove, EncodeRecipeBookRemovePacket(removedIDs))
	}
	return len(removedIDs)
}

// UnlockAllRecipes unlocks every registered crafting recipe for session.
func (s *Server) UnlockAllRecipes(session *PlayerSession) int {
	if session == nil {
		return 0
	}
	idx := getRecipeDisplayIndex()
	names := make([]string, len(idx.byDisplayID))
	for i, rec := range idx.byDisplayID {
		names[i] = rec.Name
	}
	return s.UnlockRecipesByNames(session, names)
}

// LockAllRecipes removes every unlocked recipe from session's recipe book.
func (s *Server) LockAllRecipes(session *PlayerSession) int {
	if session == nil {
		return 0
	}
	return s.LockRecipesByNames(session, session.UnlockedRecipes())
}

// UnlockRecipesForItem unlocks all crafting recipes associated with stack's item ID.
func (s *Server) UnlockRecipesForItem(session *PlayerSession, stack world.ItemStack) int {
	if session == nil || stack.IsEmpty() {
		return 0
	}
	itemID := canonicalResourceName(stack.ResolvedID())
	if itemID == "" {
		return 0
	}
	idx := getRecipeDisplayIndex()
	names := idx.byItemID[itemID]
	if len(names) == 0 {
		return 0
	}
	return s.UnlockRecipesByNames(session, names)
}

// UnlockRecipesForInventory scans session's inventory and cursor item and unlocks
// all recipes associated with the items currently held by the player.
func (s *Server) UnlockRecipesForInventory(session *PlayerSession) int {
	if session == nil {
		return 0
	}
	inv := session.Inventory()
	idx := getRecipeDisplayIndex()
	seenItems := make(map[string]bool, len(inv)+1)
	var toUnlock []string
	collect := func(st world.ItemStack) {
		if st.IsEmpty() {
			return
		}
		id := canonicalResourceName(st.ResolvedID())
		if id == "" || seenItems[id] {
			return
		}
		seenItems[id] = true
		toUnlock = append(toUnlock, idx.byItemID[id]...)
	}
	for _, st := range inv {
		collect(st)
	}
	collect(session.CarriedItem())
	return s.UnlockRecipesByNames(session, toUnlock)
}

// UnlockRecipeOnCraft unlocks the crafted recipe itself as well as any recipes
// unlocked by acquiring the crafted result stack.
func (s *Server) UnlockRecipeOnCraft(session *PlayerSession, recipeName string, result world.ItemStack) int {
	if session == nil {
		return 0
	}
	count := 0
	if recipeName != "" {
		count += s.UnlockRecipesByNames(session, []string{recipeName})
	}
	if !result.IsEmpty() {
		count += s.UnlockRecipesForItem(session, result)
	}
	return count
}

// HandleRecipeBookChangeSettings processes a serverbound
// recipe_book_change_settings (0x2e) packet.
func (s *Server) HandleRecipeBookChangeSettings(session *PlayerSession, bookType int32, isOpen, isFiltering bool) error {
	if session == nil {
		return nil
	}
	session.SetRecipeBookSetting(bookType, isOpen, isFiltering)
	return nil
}

// HandleRecipeBookSeenRecipe processes a serverbound recipe_book_seen_recipe
// (0x2f) packet by clearing the highlight flag on the specified recipe.
func (s *Server) HandleRecipeBookSeenRecipe(session *PlayerSession, recipeDisplayID int32) error {
	if session == nil {
		return nil
	}
	rec, ok := RecipeByDisplayID(recipeDisplayID)
	if !ok || rec == nil {
		return nil
	}
	tr := getRecipeBookTracker(session)
	tr.mu.Lock()
	delete(tr.highlight, rec.Name)
	tr.mu.Unlock()
	return nil
}

// HandlePlaceRecipe processes a serverbound place_recipe (0x27) packet,
// implementing auto-crafting for both the 2x2 player inventory crafting grid
// (containerID == 0) and the 3x3 crafting table menu (MenuTypeCrafting).
// If the player lacks the required ingredients, a ClientboundPlaceGhostRecipePacket
// (0x3f) is sent instead.
func (s *Server) HandlePlaceRecipe(session *PlayerSession, containerID int32, recipeDisplayID int32, useMaxItems bool) error {
	if s == nil || session == nil {
		return nil
	}
	if session.Snapshot().GameMode == GameModeSpectator || session.IsDead() {
		return nil
	}
	rec, ok := RecipeByDisplayID(recipeDisplayID)
	if !ok || rec == nil {
		return nil
	}
	if !session.HasUnlockedRecipe(rec.Name) {
		return nil
	}

	tr := getContainerTracker(session)
	tr.mu.Lock()

	gridW, gridH := 2, 2
	isTable := false
	if containerID == 0 {
		gridW, gridH = 2, 2
	} else if tr.open != nil && tr.open.ContainerID == containerID && tr.open.MenuType == MenuTypeCrafting {
		gridW, gridH = 3, 3
		isTable = true
	} else {
		tr.mu.Unlock()
		return nil
	}

	// Verify the recipe fits inside the active crafting grid dimensions.
	if rec.Type == "minecraft:crafting_shaped" {
		if rec.Width > gridW || rec.Height > gridH {
			tr.mu.Unlock()
			return nil
		}
	} else if len(rec.Ingredients) > gridW*gridH {
		tr.mu.Unlock()
		return nil
	}

	// Build the target ingredient pattern across the gridW x gridH grid.
	gridCells := gridW * gridH
	targetPattern := make([]Ingredient, gridCells)
	if rec.Type == "minecraft:crafting_shaped" {
		for r := 0; r < rec.Height; r++ {
			for c := 0; c < rec.Width; c++ {
				targetPattern[r*gridW+c] = rec.Pattern[r*rec.Width+c]
			}
		}
	} else {
		for i, ing := range rec.Ingredients {
			targetPattern[i] = ing
		}
	}

	// Return any existing items in the crafting grid back to the player's inventory first.
	if isTable {
		returnStacksToInventory(session, tr.open.Crafting[:9])
		for i := range tr.open.Crafting {
			tr.open.Crafting[i] = world.ItemStack{}
		}
		tr.open.Result = world.ItemStack{}
	} else {
		var existing [4]world.ItemStack
		for i := 0; i < 4; i++ {
			st := tr.invCraft[i]
			if st.IsEmpty() {
				st = session.InventorySlot(1 + i)
			}
			existing[i] = st
			tr.invCraft[i] = world.ItemStack{}
			session.SetInventorySlot(1+i, world.ItemStack{})
		}
		tr.invResult = world.ItemStack{}
		session.SetInventorySlot(0, world.ItemStack{})
		returnStacksToInventory(session, existing[:])
	}

	// Work on a mutable copy of player storage slots (9..44: main inventory + hotbar).
	inv := session.Inventory()
	storage := make([]world.ItemStack, 36)
	for i := 0; i < 36; i++ {
		storage[i] = inv[9+i]
	}

	// Determine whether we can satisfy at least one craft of targetPattern from storage.
	newGrid := make([]world.ItemStack, gridCells)
	canCraftOnce := placeOneCraftFromStorage(storage, targetPattern, newGrid)
	if !canCraftOnce {
		// Ingredients missing: leave returned items in player inventory and send ghost recipe.
		tr.mu.Unlock()
		if isTable {
			_ = s.syncOpenContainer(session)
		} else {
			_ = s.syncPlayerInventoryContainer(session)
		}
		if session.send != nil {
			return session.send(protocol.PlayPlaceGhostRecipe, EncodePlaceGhostRecipePacket(containerID, rec))
		}
		return nil
	}

	// If useMaxItems (Shift-click in recipe book), stack additional crafts into newGrid.
	if useMaxItems {
		for {
			if !stackAdditionalCraftFromStorage(storage, newGrid) {
				break
			}
		}
	}

	// Commit updated player storage (9..44) and crafting grid slots.
	for i := 0; i < 36; i++ {
		session.SetInventorySlot(9+i, storage[i])
	}
	result, _, _ := MatchCraftingGrid(newGrid, gridW, gridH)
	if isTable {
		for i := 0; i < 9; i++ {
			tr.open.Crafting[i] = newGrid[i]
		}
		tr.open.Result = result
		tr.mu.Unlock()
		return s.syncOpenContainer(session)
	}

	for i := 0; i < 4; i++ {
		tr.invCraft[i] = newGrid[i]
		session.SetInventorySlot(1+i, newGrid[i])
	}
	tr.invResult = result
	session.SetInventorySlot(0, result)
	tr.mu.Unlock()
	return s.syncPlayerInventoryContainer(session)
}

func returnStacksToInventory(session *PlayerSession, stacks []world.ItemStack) {
	if session == nil {
		return
	}
	for _, st := range stacks {
		if !st.IsEmpty() {
			_, _ = session.AddItemToInventory(st)
		}
	}
}

func placeOneCraftFromStorage(storage []world.ItemStack, pattern []Ingredient, gridOut []world.ItemStack) bool {
	// Work on a trial copy so partial matches don't mutate storage on failure.
	trial := make([]world.ItemStack, len(storage))
	copy(trial, storage)

	for cellIdx, ing := range pattern {
		if ing.IsEmpty() {
			gridOut[cellIdx] = world.ItemStack{}
			continue
		}
		found := -1
		// Prefer picking the same item ID already chosen for an identical ingredient if available.
		for sIdx := range trial {
			if !trial[sIdx].IsEmpty() && ing.Matches(trial[sIdx]) {
				found = sIdx
				break
			}
		}
		if found < 0 {
			return false
		}
		picked := trial[found]
		gridOut[cellIdx] = world.NewItemStack(picked.ResolvedItemID(), 1)
		trial[found].Count--
		if trial[found].Count <= 0 {
			trial[found] = world.ItemStack{}
		}
	}
	copy(storage, trial)
	return true
}

func stackAdditionalCraftFromStorage(storage []world.ItemStack, grid []world.ItemStack) bool {
	trial := make([]world.ItemStack, len(storage))
	copy(trial, storage)

	for _, cur := range grid {
		if cur.IsEmpty() {
			continue
		}
		if cur.Count >= MaxItemStackSize {
			return false
		}
		found := -1
		for sIdx := range trial {
			if !trial[sIdx].IsEmpty() && trial[sIdx].ResolvedItemID() == cur.ResolvedItemID() {
				found = sIdx
				break
			}
		}
		if found < 0 {
			return false
		}
		trial[found].Count--
		if trial[found].Count <= 0 {
			trial[found] = world.ItemStack{}
		}
	}

	for cellIdx := range grid {
		if !grid[cellIdx].IsEmpty() {
			grid[cellIdx].Count++
		}
	}
	copy(storage, trial)
	return true
}

// HandleRecipeCommand executes the vanilla 26.1.2 /recipe command:
//   - /recipe give <targets> (<recipe>|*)
//   - /recipe take <targets> (<recipe>|*)
func (s *Server) HandleRecipeCommand(sender *PlayerSession, args []string) string {
	if s == nil {
		return "Server unavailable"
	}
	if !s.HasAdminPermission(sender) {
		return "You do not have permission to use this command"
	}
	if len(args) < 3 {
		return "Usage: /recipe <give|take> <targets> (<recipe>|*)"
	}
	action := strings.ToLower(strings.TrimSpace(args[0]))
	if action != "give" && action != "take" {
		return "Usage: /recipe <give|take> <targets> (<recipe>|*)"
	}
	targets := s.resolveCommandPlayerTargets(sender, args[1])
	if len(targets) == 0 {
		return fmt.Sprintf("No player was found for %q", args[1])
	}
	spec := strings.TrimSpace(args[2])
	isWildcard := spec == "*"

	var recName string
	if !isWildcard {
		recName = canonicalResourceName(spec)
		if _, ok := DisplayIDForRecipe(recName); !ok {
			return fmt.Sprintf("Unknown recipe: %s", spec)
		}
	}

	totalChanged := 0
	for _, target := range targets {
		if action == "give" {
			if isWildcard {
				totalChanged += s.UnlockAllRecipes(target)
			} else {
				totalChanged += s.UnlockRecipesByNames(target, []string{recName})
			}
		} else {
			if isWildcard {
				totalChanged += s.LockAllRecipes(target)
			} else {
				totalChanged += s.LockRecipesByNames(target, []string{recName})
			}
		}
	}

	if action == "give" {
		if len(targets) == 1 {
			if isWildcard {
				return fmt.Sprintf("Unlocked %d recipes for %s", totalChanged, targets[0].Profile.Name)
			}
			return fmt.Sprintf("Unlocked recipe %s for %s", recName, targets[0].Profile.Name)
		}
		return fmt.Sprintf("Unlocked recipes for %d players", len(targets))
	}
	if len(targets) == 1 {
		if isWildcard {
			return fmt.Sprintf("Taken %d recipes from %s", totalChanged, targets[0].Profile.Name)
		}
		return fmt.Sprintf("Taken recipe %s from %s", recName, targets[0].Profile.Name)
	}
	return fmt.Sprintf("Taken recipes from %d players", len(targets))
}

// HandleAdvancementCommand executes the vanilla 26.1.2 /advancement command:
//   - /advancement <grant|revoke> <targets> everything
//   - /advancement <grant|revoke> <targets> only <advancement> [<criterion>]
func (s *Server) HandleAdvancementCommand(sender *PlayerSession, args []string) string {
	if s == nil {
		return "Server unavailable"
	}
	if !s.HasAdminPermission(sender) {
		return "You do not have permission to use this command"
	}
	if len(args) < 3 {
		return "Usage: /advancement <grant|revoke> <targets> <everything|only> [<advancement> [<criterion>]]"
	}
	action := strings.ToLower(strings.TrimSpace(args[0]))
	if action != "grant" && action != "revoke" {
		return "Usage: /advancement <grant|revoke> <targets> <everything|only> [<advancement> [<criterion>]]"
	}
	targets := s.resolveCommandPlayerTargets(sender, args[1])
	if len(targets) == 0 {
		return fmt.Sprintf("No player was found for %q", args[1])
	}
	mode := strings.ToLower(strings.TrimSpace(args[2]))
	switch mode {
	case "everything":
		for _, t := range targets {
			if action == "grant" {
				s.GrantAllAdvancements(t)
			} else {
				s.RevokeAllAdvancements(t)
			}
		}
		if action == "grant" {
			return fmt.Sprintf("Granted every advancement to %d player(s)", len(targets))
		}
		return fmt.Sprintf("Revoked every advancement from %d player(s)", len(targets))

	case "only", "from", "through", "until":
		if len(args) < 4 {
			return fmt.Sprintf("Usage: /advancement %s <targets> %s <advancement> [<criterion>]", action, mode)
		}
		advID := canonicalAdvancementID(args[3])
		def, ok := AdvancementByID(advID)
		if !ok {
			return fmt.Sprintf("Unknown advancement: %s", args[3])
		}
		if len(args) >= 5 && mode == "only" {
			crit := args[4]
			for _, t := range targets {
				if action == "grant" {
					s.GrantCriterion(t, def.ID, crit)
				} else {
					s.RevokeCriterion(t, def.ID, crit)
				}
			}
			return fmt.Sprintf("Updated criterion %s on %s for %d player(s)", crit, def.ID, len(targets))
		}
		for _, t := range targets {
			if action == "grant" {
				s.GrantAdvancement(t, def.ID)
			} else {
				s.RevokeAdvancement(t, def.ID)
			}
		}
		if action == "grant" {
			return fmt.Sprintf("Granted advancement [%s] to %d player(s)", def.ID, len(targets))
		}
		return fmt.Sprintf("Revoked advancement [%s] from %d player(s)", def.ID, len(targets))

	default:
		return "Usage: /advancement <grant|revoke> <targets> <everything|only> [<advancement> [<criterion>]]"
	}
}

func (s *Server) resolveCommandPlayerTargets(sender *PlayerSession, selector string) []*PlayerSession {
	sel := strings.TrimSpace(selector)
	switch strings.ToLower(sel) {
	case "@s", "@p":
		if sender != nil {
			return []*PlayerSession{sender}
		}
		s.playersMu.RLock()
		defer s.playersMu.RUnlock()
		for _, p := range s.players {
			return []*PlayerSession{p}
		}
		return nil
	case "@a":
		s.playersMu.RLock()
		out := make([]*PlayerSession, 0, len(s.players))
		for _, p := range s.players {
			out = append(out, p)
		}
		s.playersMu.RUnlock()
		sort.Slice(out, func(i, j int) bool {
			return strings.ToLower(out[i].Profile.Name) < strings.ToLower(out[j].Profile.Name)
		})
		return out
	default:
		if p := s.PlayerByName(sel); p != nil {
			return []*PlayerSession{p}
		}
		return nil
	}
}
