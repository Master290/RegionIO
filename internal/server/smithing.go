package server

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"path"
	"sort"
	"strings"
	"sync"

	"regionio/internal/nbt"
	"regionio/internal/protocol"
	"regionio/internal/world"
)

// Smithing Table menu and data component constants for Minecraft 26.1.2 (protocol 775).
const (
	// MenuTypeSmithing is BuiltInRegistries.MENU index 21 ("minecraft:smithing").
	MenuTypeSmithing int32 = 21

	SmithingSlotTemplate       = 0
	SmithingSlotBase           = 1
	SmithingSlotAddition       = 2
	SmithingSlotResult         = 3
	SmithingInputSlotCount     = 3
	SmithingContainerSlotCount = 4
	SmithingMenuSlotCount      = 40 // 4 smithing slots (0..3) + 27 main inventory (4..30) + 9 hotbar (31..39)

	// DataComponentTrim is BuiltInRegistries.DATA_COMPONENT_TYPE index 62 ("minecraft:trim").
	DataComponentTrim int32 = 62

	// DataComponentDamageID is BuiltInRegistries.DATA_COMPONENT_TYPE index 3 ("minecraft:damage").
	DataComponentDamageID int32 = 3

	// DataComponentEnchantmentsID is BuiltInRegistries.DATA_COMPONENT_TYPE index 13 ("minecraft:enchantments").
	DataComponentEnchantmentsID int32 = 13

	// DataComponentRepairCostID is BuiltInRegistries.DATA_COMPONENT_TYPE index 16 ("minecraft:repair_cost").
	DataComponentRepairCostID int32 = 16

	SmithingTableBlockID = "minecraft:smithing_table"
)

// IsSmithingTableBlock reports whether state is a `minecraft:smithing_table` block.
func IsSmithingTableBlock(state uint16) bool {
	return world.BlockName(state) == SmithingTableBlockID
}

// TrimPatternEntry describes one entry in the synced 26.1.2 `minecraft:trim_pattern` registry.
type TrimPatternEntry struct {
	Index          int32
	ID             string
	AssetID        string
	TemplateItemID string
	Decal          bool
}

// TrimMaterialEntry describes one entry in the synced 26.1.2 `minecraft:trim_material` registry.
type TrimMaterialEntry struct {
	Index            int32
	ID               string
	AssetName        string
	IngredientItemID string
}

// Vanilla 26.1.2 `minecraft:trim_pattern` registry (indices 0..17 in alphabetical order).
var vanillaTrimPatterns = []TrimPatternEntry{
	{Index: 0, ID: "minecraft:bolt", AssetID: "minecraft:bolt", TemplateItemID: "minecraft:bolt_armor_trim_smithing_template", Decal: true},
	{Index: 1, ID: "minecraft:coast", AssetID: "minecraft:coast", TemplateItemID: "minecraft:coast_armor_trim_smithing_template", Decal: false},
	{Index: 2, ID: "minecraft:dune", AssetID: "minecraft:dune", TemplateItemID: "minecraft:dune_armor_trim_smithing_template", Decal: false},
	{Index: 3, ID: "minecraft:eye", AssetID: "minecraft:eye", TemplateItemID: "minecraft:eye_armor_trim_smithing_template", Decal: false},
	{Index: 4, ID: "minecraft:flow", AssetID: "minecraft:flow", TemplateItemID: "minecraft:flow_armor_trim_smithing_template", Decal: true},
	{Index: 5, ID: "minecraft:host", AssetID: "minecraft:host", TemplateItemID: "minecraft:host_armor_trim_smithing_template", Decal: false},
	{Index: 6, ID: "minecraft:raiser", AssetID: "minecraft:raiser", TemplateItemID: "minecraft:raiser_armor_trim_smithing_template", Decal: false},
	{Index: 7, ID: "minecraft:rib", AssetID: "minecraft:rib", TemplateItemID: "minecraft:rib_armor_trim_smithing_template", Decal: false},
	{Index: 8, ID: "minecraft:sentry", AssetID: "minecraft:sentry", TemplateItemID: "minecraft:sentry_armor_trim_smithing_template", Decal: false},
	{Index: 9, ID: "minecraft:shaper", AssetID: "minecraft:shaper", TemplateItemID: "minecraft:shaper_armor_trim_smithing_template", Decal: false},
	{Index: 10, ID: "minecraft:silence", AssetID: "minecraft:silence", TemplateItemID: "minecraft:silence_armor_trim_smithing_template", Decal: false},
	{Index: 11, ID: "minecraft:snout", AssetID: "minecraft:snout", TemplateItemID: "minecraft:snout_armor_trim_smithing_template", Decal: false},
	{Index: 12, ID: "minecraft:spire", AssetID: "minecraft:spire", TemplateItemID: "minecraft:spire_armor_trim_smithing_template", Decal: false},
	{Index: 13, ID: "minecraft:tide", AssetID: "minecraft:tide", TemplateItemID: "minecraft:tide_armor_trim_smithing_template", Decal: false},
	{Index: 14, ID: "minecraft:vex", AssetID: "minecraft:vex", TemplateItemID: "minecraft:vex_armor_trim_smithing_template", Decal: false},
	{Index: 15, ID: "minecraft:ward", AssetID: "minecraft:ward", TemplateItemID: "minecraft:ward_armor_trim_smithing_template", Decal: false},
	{Index: 16, ID: "minecraft:wayfinder", AssetID: "minecraft:wayfinder", TemplateItemID: "minecraft:wayfinder_armor_trim_smithing_template", Decal: false},
	{Index: 17, ID: "minecraft:wild", AssetID: "minecraft:wild", TemplateItemID: "minecraft:wild_armor_trim_smithing_template", Decal: false},
}

// Vanilla 26.1.2 `minecraft:trim_material` registry (indices 0..10 in alphabetical order).
var vanillaTrimMaterials = []TrimMaterialEntry{
	{Index: 0, ID: "minecraft:amethyst", AssetName: "amethyst", IngredientItemID: "minecraft:amethyst_shard"},
	{Index: 1, ID: "minecraft:copper", AssetName: "copper", IngredientItemID: "minecraft:copper_ingot"},
	{Index: 2, ID: "minecraft:diamond", AssetName: "diamond", IngredientItemID: "minecraft:diamond"},
	{Index: 3, ID: "minecraft:emerald", AssetName: "emerald", IngredientItemID: "minecraft:emerald"},
	{Index: 4, ID: "minecraft:gold", AssetName: "gold", IngredientItemID: "minecraft:gold_ingot"},
	{Index: 5, ID: "minecraft:iron", AssetName: "iron", IngredientItemID: "minecraft:iron_ingot"},
	{Index: 6, ID: "minecraft:lapis", AssetName: "lapis", IngredientItemID: "minecraft:lapis_lazuli"},
	{Index: 7, ID: "minecraft:netherite", AssetName: "netherite", IngredientItemID: "minecraft:netherite_ingot"},
	{Index: 8, ID: "minecraft:quartz", AssetName: "quartz", IngredientItemID: "minecraft:quartz"},
	{Index: 9, ID: "minecraft:redstone", AssetName: "redstone", IngredientItemID: "minecraft:redstone"},
	{Index: 10, ID: "minecraft:resin", AssetName: "resin", IngredientItemID: "minecraft:resin_brick"},
}

// AllTrimPatterns returns a copy of all 18 vanilla 26.1.2 trim pattern registry entries.
func AllTrimPatterns() []TrimPatternEntry {
	out := make([]TrimPatternEntry, len(vanillaTrimPatterns))
	copy(out, vanillaTrimPatterns)
	return out
}

// AllTrimMaterials returns a copy of all 11 vanilla 26.1.2 trim material registry entries.
func AllTrimMaterials() []TrimMaterialEntry {
	out := make([]TrimMaterialEntry, len(vanillaTrimMaterials))
	copy(out, vanillaTrimMaterials)
	return out
}

// LookupTrimPattern finds a TrimPatternEntry by resource ID (e.g. "minecraft:silence" or "silence").
func LookupTrimPattern(id string) (TrimPatternEntry, bool) {
	canon := canonicalResourceName(id)
	for _, p := range vanillaTrimPatterns {
		if p.ID == canon {
			return p, true
		}
	}
	return TrimPatternEntry{}, false
}

// LookupTrimPatternByIndex finds a TrimPatternEntry by its registry index (0..17).
func LookupTrimPatternByIndex(idx int32) (TrimPatternEntry, bool) {
	if idx < 0 || int(idx) >= len(vanillaTrimPatterns) {
		return TrimPatternEntry{}, false
	}
	return vanillaTrimPatterns[idx], true
}

// LookupTrimPatternByTemplateItem finds a TrimPatternEntry by its smithing template item ID.
func LookupTrimPatternByTemplateItem(itemID string) (TrimPatternEntry, bool) {
	canon := canonicalResourceName(itemID)
	for _, p := range vanillaTrimPatterns {
		if p.TemplateItemID == canon {
			return p, true
		}
	}
	return TrimPatternEntry{}, false
}

// LookupTrimMaterial finds a TrimMaterialEntry by resource ID (e.g. "minecraft:amethyst" or "amethyst").
func LookupTrimMaterial(id string) (TrimMaterialEntry, bool) {
	canon := canonicalResourceName(id)
	for _, m := range vanillaTrimMaterials {
		if m.ID == canon {
			return m, true
		}
	}
	return TrimMaterialEntry{}, false
}

// LookupTrimMaterialByIndex finds a TrimMaterialEntry by its registry index (0..10).
func LookupTrimMaterialByIndex(idx int32) (TrimMaterialEntry, bool) {
	if idx < 0 || int(idx) >= len(vanillaTrimMaterials) {
		return TrimMaterialEntry{}, false
	}
	return vanillaTrimMaterials[idx], true
}

// LookupTrimMaterialByIngredientItem finds a TrimMaterialEntry by its addition ingredient item ID
// (e.g. "minecraft:amethyst_shard" -> "minecraft:amethyst").
func LookupTrimMaterialByIngredientItem(itemID string) (TrimMaterialEntry, bool) {
	canon := canonicalResourceName(itemID)
	for _, m := range vanillaTrimMaterials {
		if m.IngredientItemID == canon {
			return m, true
		}
	}
	return TrimMaterialEntry{}, false
}

// ArmorTrim represents the `minecraft:trim` data component (DataComponentTrim = 62).
type ArmorTrim struct {
	Material string // canonical `minecraft:trim_material` ID, e.g. "minecraft:amethyst"
	Pattern  string // canonical `minecraft:trim_pattern` ID, e.g. "minecraft:silence"
}

// IsValid reports whether both Material and Pattern resolve to valid 26.1.2 registry entries.
func (t ArmorTrim) IsValid() bool {
	if t.Material == "" || t.Pattern == "" {
		return false
	}
	_, okMat := LookupTrimMaterial(t.Material)
	_, okPat := LookupTrimPattern(t.Pattern)
	return okMat && okPat
}

// Equal reports whether t and other represent the exact same material and pattern.
func (t ArmorTrim) Equal(other ArmorTrim) bool {
	if !t.IsValid() || !other.IsValid() {
		return !t.IsValid() && !other.IsValid()
	}
	return canonicalResourceName(t.Material) == canonicalResourceName(other.Material) &&
		canonicalResourceName(t.Pattern) == canonicalResourceName(other.Pattern)
}

// EncodeArmorTrimComponent writes an ArmorTrim using the 26.1.2 network stream codec:
//   - Holder<TrimMaterial>: VarInt(materialIndex + 1)
//   - Holder<TrimPattern>:  VarInt(patternIndex + 1)
func EncodeArmorTrimComponent(w *protocol.Writer, trim ArmorTrim) error {
	mat, okMat := LookupTrimMaterial(trim.Material)
	if !okMat {
		return fmt.Errorf("smithing: unknown trim material %q", trim.Material)
	}
	pat, okPat := LookupTrimPattern(trim.Pattern)
	if !okPat {
		return fmt.Errorf("smithing: unknown trim pattern %q", trim.Pattern)
	}
	w.VarInt(mat.Index + 1)
	w.VarInt(pat.Index + 1)
	return nil
}

// EncodeArmorTrimPayload encodes an ArmorTrim into a byte slice using the 26.1.2 network stream codec.
func EncodeArmorTrimPayload(trim ArmorTrim) ([]byte, error) {
	w := protocol.NewWriter(8)
	if err := EncodeArmorTrimComponent(w, trim); err != nil {
		return nil, err
	}
	return w.Bytes(), nil
}

// DecodeArmorTrimComponent decodes an ArmorTrim from a protocol.Reader using the 26.1.2 network stream codec.
func DecodeArmorTrimComponent(r *protocol.Reader) (ArmorTrim, error) {
	matHolderID, err := r.VarInt()
	if err != nil {
		return ArmorTrim{}, err
	}
	if matHolderID <= 0 {
		return ArmorTrim{}, fmt.Errorf("smithing: inline trim_material holder not supported (got %d)", matHolderID)
	}
	mat, okMat := LookupTrimMaterialByIndex(matHolderID - 1)
	if !okMat {
		return ArmorTrim{}, fmt.Errorf("smithing: trim_material index %d out of range", matHolderID-1)
	}

	patHolderID, err := r.VarInt()
	if err != nil {
		return ArmorTrim{}, err
	}
	if patHolderID <= 0 {
		return ArmorTrim{}, fmt.Errorf("smithing: inline trim_pattern holder not supported (got %d)", patHolderID)
	}
	pat, okPat := LookupTrimPatternByIndex(patHolderID - 1)
	if !okPat {
		return ArmorTrim{}, fmt.Errorf("smithing: trim_pattern index %d out of range", patHolderID-1)
	}

	return ArmorTrim{
		Material: mat.ID,
		Pattern:  pat.ID,
	}, nil
}

// DecodeArmorTrimPayload decodes an ArmorTrim from raw bytes.
func DecodeArmorTrimPayload(raw []byte) (ArmorTrim, error) {
	return DecodeArmorTrimComponent(protocol.NewReader(raw))
}

// WriteSlotWithArmorTrim writes a protocol 775 ItemStack slot with optional ItemMeta
// (damage, enchantments, repair_cost) and `minecraft:trim` (component 62).
func WriteSlotWithArmorTrim(w *protocol.Writer, stack world.ItemStack, meta ItemMeta, trim ArmorTrim) {
	itemID := stack.ResolvedItemID()
	if stack.IsEmpty() || itemID <= 0 {
		w.VarInt(0)
		return
	}
	w.VarInt(stack.Count)
	w.VarInt(itemID)

	type rawComp struct {
		id   int32
		data []byte
	}
	var comps []rawComp

	if meta.Damage > 0 {
		cw := protocol.NewWriter(4)
		cw.VarInt(meta.Damage)
		comps = append(comps, rawComp{id: DataComponentDamageID, data: cw.Bytes()})
	}
	if meta.HasEnchantments() {
		sorted := meta.SortedEnchantments()
		cw := protocol.NewWriter(4 + len(sorted)*4)
		cw.VarInt(int32(len(sorted)))
		for _, inst := range sorted {
			def, ok := EnchantmentByID(inst.ID)
			idx := int32(0)
			if ok {
				idx = int32(def.RegistryIndex)
			}
			cw.VarInt(idx)
			cw.VarInt(int32(inst.Level))
		}
		comps = append(comps, rawComp{id: DataComponentEnchantmentsID, data: cw.Bytes()})
	}
	if meta.RepairCost > 0 {
		cw := protocol.NewWriter(4)
		cw.VarInt(meta.RepairCost)
		comps = append(comps, rawComp{id: DataComponentRepairCostID, data: cw.Bytes()})
	}
	if trim.IsValid() {
		if trimBytes, err := EncodeArmorTrimPayload(trim); err == nil {
			comps = append(comps, rawComp{id: DataComponentTrim, data: trimBytes})
		}
	}

	w.VarInt(int32(len(comps)))
	w.VarInt(0) // 0 removed components
	for _, c := range comps {
		w.VarInt(c.id)
		w.Raw(c.data)
	}
}

// EncodeSlotWithArmorTrim encodes a Slot (stack + ItemMeta + `minecraft:trim` component 62) into a byte slice.
func EncodeSlotWithArmorTrim(stack world.ItemStack, meta ItemMeta, trim ArmorTrim) []byte {
	w := protocol.NewWriter(32)
	WriteSlotWithArmorTrim(w, stack, meta, trim)
	return w.Bytes()
}

// ArmorTrimToNBT serializes an ArmorTrim to its vanilla NBT compound representation
// (`{material: "minecraft:...", pattern: "minecraft:..."}`).
func ArmorTrimToNBT(trim ArmorTrim) *nbt.Compound {
	if !trim.IsValid() {
		return nil
	}
	c := nbt.NewCompound()
	c.Set("material", nbt.String(canonicalResourceName(trim.Material)))
	c.Set("pattern", nbt.String(canonicalResourceName(trim.Pattern)))
	return c
}

// ArmorTrimFromNBT parses an ArmorTrim from a vanilla `minecraft:trim` NBT compound.
func ArmorTrimFromNBT(c *nbt.Compound) (ArmorTrim, bool) {
	if c == nil {
		return ArmorTrim{}, false
	}
	mat, okMat := nbtStringTag(c, "material")
	pat, okPat := nbtStringTag(c, "pattern")
	if !okMat || !okPat {
		return ArmorTrim{}, false
	}
	trim := ArmorTrim{
		Material: canonicalResourceName(mat),
		Pattern:  canonicalResourceName(pat),
	}
	if !trim.IsValid() {
		return ArmorTrim{}, false
	}
	return trim, true
}

// Per-player ArmorTrim tracking (keyed by PlayerSession pointer).
type playerArmorTrimState struct {
	mu      sync.RWMutex
	slots   map[int]ArmorTrim
	carried ArmorTrim
	// Open Smithing Table input slot metadata & trims while a Smithing window is open.
	smithingInputMeta [SmithingInputSlotCount]ItemMeta
	smithingInputTrim [SmithingInputSlotCount]ArmorTrim
}

var (
	playerArmorTrimMu sync.Mutex
	playerArmorTrims  = make(map[*PlayerSession]*playerArmorTrimState)
)

func getPlayerArmorTrimState(session *PlayerSession) *playerArmorTrimState {
	if session == nil {
		return nil
	}
	playerArmorTrimMu.Lock()
	defer playerArmorTrimMu.Unlock()
	st, ok := playerArmorTrims[session]
	if !ok {
		st = &playerArmorTrimState{
			slots: make(map[int]ArmorTrim),
		}
		playerArmorTrims[session] = st
	}
	return st
}

// GetSlotArmorTrim returns the ArmorTrim stored on player inventory slot `slot` (0..45).
func GetSlotArmorTrim(session *PlayerSession, slot int) (ArmorTrim, bool) {
	st := getPlayerArmorTrimState(session)
	if st == nil {
		return ArmorTrim{}, false
	}
	st.mu.RLock()
	defer st.mu.RUnlock()
	t, ok := st.slots[slot]
	if !ok || !t.IsValid() {
		return ArmorTrim{}, false
	}
	return t, true
}

// SetSlotArmorTrim sets or clears the ArmorTrim stored on player inventory slot `slot` (0..45).
func SetSlotArmorTrim(session *PlayerSession, slot int, trim ArmorTrim) {
	st := getPlayerArmorTrimState(session)
	if st == nil {
		return
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if !trim.IsValid() {
		delete(st.slots, slot)
		return
	}
	st.slots[slot] = ArmorTrim{
		Material: canonicalResourceName(trim.Material),
		Pattern:  canonicalResourceName(trim.Pattern),
	}
}

// ClearSlotArmorTrim removes any ArmorTrim stored on player inventory slot `slot`.
func ClearSlotArmorTrim(session *PlayerSession, slot int) {
	SetSlotArmorTrim(session, slot, ArmorTrim{})
}

// GetCarriedArmorTrim returns the ArmorTrim on the player's cursor stack.
func GetCarriedArmorTrim(session *PlayerSession) (ArmorTrim, bool) {
	st := getPlayerArmorTrimState(session)
	if st == nil {
		return ArmorTrim{}, false
	}
	st.mu.RLock()
	defer st.mu.RUnlock()
	if !st.carried.IsValid() {
		return ArmorTrim{}, false
	}
	return st.carried, true
}

// SetCarriedArmorTrim sets or clears the ArmorTrim on the player's cursor stack.
func SetCarriedArmorTrim(session *PlayerSession, trim ArmorTrim) {
	st := getPlayerArmorTrimState(session)
	if st == nil {
		return
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if !trim.IsValid() {
		st.carried = ArmorTrim{}
		return
	}
	st.carried = ArmorTrim{
		Material: canonicalResourceName(trim.Material),
		Pattern:  canonicalResourceName(trim.Pattern),
	}
}

// SmithingTransformRecipe represents one `minecraft:smithing_transform` recipe (e.g. netherite upgrade).
type SmithingTransformRecipe struct {
	Name     string
	Template Ingredient
	Base     Ingredient
	Addition Ingredient
	Result   world.ItemStack
}

// SmithingTrimRecipe represents one `minecraft:smithing_trim` recipe (armor trim application).
type SmithingTrimRecipe struct {
	Name     string
	Template Ingredient
	Base     Ingredient
	Addition Ingredient
	Pattern  string // canonical `minecraft:trim_pattern` ID
}

// SmithingEvaluation holds the computed output of a Smithing Table recipe match.
type SmithingEvaluation struct {
	RecipeName string
	IsTrim     bool
	Result     world.ItemStack
	ResultMeta ItemMeta
	ResultTrim ArmorTrim
}

// SmithingBook holds all `minecraft:smithing_transform` and `minecraft:smithing_trim`
// recipes loaded from the embedded vanilla 26.1.2 datapack (`recipe_data.zip`).
type SmithingBook struct {
	transforms []*SmithingTransformRecipe
	trims      []*SmithingTrimRecipe
	byName     map[string]any
}

var (
	defaultSmithingOnce sync.Once
	defaultSmithingBook *SmithingBook
)

// DefaultSmithingBook returns the singleton SmithingBook loaded from the
// embedded vanilla 26.1.2 datapack archive.
func DefaultSmithingBook() *SmithingBook {
	defaultSmithingOnce.Do(func() {
		_ = DefaultRecipeBook()
		book, err := LoadSmithingBookFromZip(embeddedRecipeData)
		if err != nil {
			panic(fmt.Sprintf("server: loading embedded smithing recipes: %v", err))
		}
		defaultSmithingBook = book
	})
	return defaultSmithingBook
}

type rawSmithingTransformFile struct {
	Type     string          `json:"type"`
	Template json.RawMessage `json:"template"`
	Base     json.RawMessage `json:"base"`
	Addition json.RawMessage `json:"addition"`
	Result   rawRecipeResult `json:"result"`
}

type rawSmithingTrimFile struct {
	Type     string          `json:"type"`
	Template json.RawMessage `json:"template"`
	Base     json.RawMessage `json:"base"`
	Addition json.RawMessage `json:"addition"`
	Pattern  string          `json:"pattern"`
}

// LoadSmithingBookFromZip parses all `minecraft:smithing_transform` and
// `minecraft:smithing_trim` recipes from a zip archive structured like the
// vanilla 26.1.2 datapack.
func LoadSmithingBookFromZip(zipBytes []byte) (*SmithingBook, error) {
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

	book := &SmithingBook{
		transforms: make([]*SmithingTransformRecipe, 0, 16),
		trims:      make([]*SmithingTrimRecipe, 0, 20),
		byName:     make(map[string]any, 36),
	}

	for _, rf := range recipeFiles {
		var header struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(rf.data, &header); err != nil {
			return nil, fmt.Errorf("smithing recipe %s: %w", rf.name, err)
		}
		switch canonicalResourceName(header.Type) {
		case "minecraft:smithing_transform":
			rec, ok, err := parseSmithingTransformRecipe(rf.name, rf.data, resolvedTags)
			if err != nil {
				return nil, fmt.Errorf("smithing_transform %s: %w", rf.name, err)
			}
			if ok {
				book.transforms = append(book.transforms, rec)
				book.byName[rec.Name] = rec
			}
		case "minecraft:smithing_trim":
			rec, ok, err := parseSmithingTrimRecipe(rf.name, rf.data, resolvedTags)
			if err != nil {
				return nil, fmt.Errorf("smithing_trim %s: %w", rf.name, err)
			}
			if ok {
				book.trims = append(book.trims, rec)
				book.byName[rec.Name] = rec
			}
		}
	}

	return book, nil
}

func parseSmithingTransformRecipe(name string, data []byte, tags map[string][]string) (*SmithingTransformRecipe, bool, error) {
	var raw rawSmithingTransformFile
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, false, err
	}
	if raw.Result.ID == "" {
		return nil, false, nil
	}
	tpl, err := parseIngredientSpec(raw.Template, tags)
	if err != nil || tpl.IsEmpty() {
		return nil, false, err
	}
	base, err := parseIngredientSpec(raw.Base, tags)
	if err != nil || base.IsEmpty() {
		return nil, false, err
	}
	add, err := parseIngredientSpec(raw.Addition, tags)
	if err != nil || add.IsEmpty() {
		return nil, false, err
	}
	count := raw.Result.Count
	if count <= 0 {
		count = 1
	}
	resStack := world.NewNamedItemStack(raw.Result.ID, count)
	if resStack.IsEmpty() {
		return nil, false, nil
	}
	return &SmithingTransformRecipe{
		Name:     canonicalResourceName(name),
		Template: tpl,
		Base:     base,
		Addition: add,
		Result:   resStack,
	}, true, nil
}

func parseSmithingTrimRecipe(name string, data []byte, tags map[string][]string) (*SmithingTrimRecipe, bool, error) {
	var raw rawSmithingTrimFile
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, false, err
	}
	if raw.Pattern == "" {
		return nil, false, nil
	}
	tpl, err := parseIngredientSpec(raw.Template, tags)
	if err != nil || tpl.IsEmpty() {
		return nil, false, err
	}
	base, err := parseIngredientSpec(raw.Base, tags)
	if err != nil || base.IsEmpty() {
		return nil, false, err
	}
	add, err := parseIngredientSpec(raw.Addition, tags)
	if err != nil || add.IsEmpty() {
		return nil, false, err
	}
	return &SmithingTrimRecipe{
		Name:     canonicalResourceName(name),
		Template: tpl,
		Base:     base,
		Addition: add,
		Pattern:  canonicalResourceName(raw.Pattern),
	}, true, nil
}

// TransformCount returns the number of loaded `minecraft:smithing_transform` recipes.
func (b *SmithingBook) TransformCount() int {
	if b == nil {
		return 0
	}
	return len(b.transforms)
}

// TrimCount returns the number of loaded `minecraft:smithing_trim` recipes.
func (b *SmithingBook) TrimCount() int {
	if b == nil {
		return 0
	}
	return len(b.trims)
}

// TotalCount returns the total number of loaded smithing recipes (`smithing_transform` + `smithing_trim`).
func (b *SmithingBook) TotalCount() int {
	if b == nil {
		return 0
	}
	return len(b.transforms) + len(b.trims)
}

// TransformRecipes returns a copy of all loaded `minecraft:smithing_transform` recipes.
func (b *SmithingBook) TransformRecipes() []*SmithingTransformRecipe {
	if b == nil {
		return nil
	}
	out := make([]*SmithingTransformRecipe, len(b.transforms))
	copy(out, b.transforms)
	return out
}

// TrimRecipes returns a copy of all loaded `minecraft:smithing_trim` recipes.
func (b *SmithingBook) TrimRecipes() []*SmithingTrimRecipe {
	if b == nil {
		return nil
	}
	out := make([]*SmithingTrimRecipe, len(b.trims))
	copy(out, b.trims)
	return out
}

// IsTemplateItem reports whether stack is accepted in slot 0 (template) by any smithing recipe.
func (b *SmithingBook) IsTemplateItem(stack world.ItemStack) bool {
	if b == nil || stack.IsEmpty() {
		return false
	}
	for _, r := range b.transforms {
		if r.Template.Matches(stack) {
			return true
		}
	}
	for _, r := range b.trims {
		if r.Template.Matches(stack) {
			return true
		}
	}
	return false
}

// IsBaseItem reports whether stack is accepted in slot 1 (base) by any smithing recipe.
func (b *SmithingBook) IsBaseItem(stack world.ItemStack) bool {
	if b == nil || stack.IsEmpty() {
		return false
	}
	for _, r := range b.transforms {
		if r.Base.Matches(stack) {
			return true
		}
	}
	for _, r := range b.trims {
		if r.Base.Matches(stack) {
			return true
		}
	}
	return false
}

// IsAdditionItem reports whether stack is accepted in slot 2 (addition) by any smithing recipe.
func (b *SmithingBook) IsAdditionItem(stack world.ItemStack) bool {
	if b == nil || stack.IsEmpty() {
		return false
	}
	for _, r := range b.transforms {
		if r.Addition.Matches(stack) {
			return true
		}
	}
	for _, r := range b.trims {
		if r.Addition.Matches(stack) {
			return true
		}
	}
	return false
}

// Evaluate matches (template, base, addition) against loaded `smithing_transform`
// and `smithing_trim` recipes, returning the output item, preserved `ItemMeta`,
// and resulting `ArmorTrim`.
func (b *SmithingBook) Evaluate(template, base, addition world.ItemStack, baseMeta ItemMeta, baseTrim ArmorTrim) (SmithingEvaluation, bool) {
	if b == nil || template.IsEmpty() || base.IsEmpty() || addition.IsEmpty() {
		return SmithingEvaluation{}, false
	}

	// 1. Check `minecraft:smithing_transform` recipes (e.g. netherite upgrade).
	for _, rec := range b.transforms {
		if rec.Template.Matches(template) && rec.Base.Matches(base) && rec.Addition.Matches(addition) {
			outStack := world.NewItemStack(rec.Result.ResolvedItemID(), rec.Result.Count)
			return SmithingEvaluation{
				RecipeName: rec.Name,
				IsTrim:     false,
				Result:     outStack,
				ResultMeta: baseMeta.Clone(),
				ResultTrim: baseTrim,
			}, true
		}
	}

	// 2. Check `minecraft:smithing_trim` recipes (armor trims).
	for _, rec := range b.trims {
		if rec.Template.Matches(template) && rec.Base.Matches(base) && rec.Addition.Matches(addition) {
			mat, okMat := LookupTrimMaterialByIngredientItem(addition.ResolvedID())
			if !okMat {
				return SmithingEvaluation{}, false
			}
			pat, okPat := LookupTrimPattern(rec.Pattern)
			if !okPat {
				return SmithingEvaluation{}, false
			}
			newTrim := ArmorTrim{
				Material: mat.ID,
				Pattern:  pat.ID,
			}
			// Vanilla SmithingTrimRecipe.assemble returns ItemStack.EMPTY if the base
			// item already has the identical ArmorTrim.
			if baseTrim.Equal(newTrim) {
				return SmithingEvaluation{}, false
			}
			return SmithingEvaluation{
				RecipeName: rec.Name,
				IsTrim:     true,
				Result:     world.NewItemStack(base.ResolvedItemID(), 1),
				ResultMeta: baseMeta.Clone(),
				ResultTrim: newTrim,
			}, true
		}
	}

	return SmithingEvaluation{}, false
}

// EvaluateSmithingResult evaluates (template, base, addition) using DefaultSmithingBook.
func EvaluateSmithingResult(template, base, addition world.ItemStack, baseMeta ItemMeta, baseTrim ArmorTrim) (SmithingEvaluation, bool) {
	return DefaultSmithingBook().Evaluate(template, base, addition, baseMeta, baseTrim)
}

// OpenSmithingTable opens the Smithing Table screen (MenuTypeSmithing = 21) for
// session at block position (bx, by, bz) and sends PlayOpenScreen and
// PlayContainerSetContent.
func (s *Server) OpenSmithingTable(session *PlayerSession, bx, by, bz int) (int32, error) {
	if s == nil || session == nil {
		return 0, errors.New("server: nil session")
	}
	if session.IsDead() {
		return 0, errors.New("server: dead player cannot open smithing table")
	}

	_ = s.CloseSmithingContainer(session, 0, false)

	st := getPlayerArmorTrimState(session)
	if st != nil {
		st.mu.Lock()
		st.smithingInputMeta = [SmithingInputSlotCount]ItemMeta{}
		st.smithingInputTrim = [SmithingInputSlotCount]ArmorTrim{}
		st.mu.Unlock()
	}

	tr := getContainerTracker(session)
	tr.mu.Lock()
	tr.nextWindow = (tr.nextWindow % 100) + 1
	cid := tr.nextWindow
	tr.open = &OpenContainerState{
		ContainerID: cid,
		MenuType:    MenuTypeSmithing,
		BlockX:      bx,
		BlockY:      by,
		BlockZ:      bz,
	}
	carried := tr.carried
	tr.mu.Unlock()

	if session.send != nil {
		if err := session.send(protocol.PlayOpenScreen, EncodeOpenScreen(cid, MenuTypeSmithing, "Upgrade Gear")); err != nil {
			return cid, err
		}
		var inputs [SmithingInputSlotCount]world.ItemStack
		slots := s.buildSmithingMenuSlots(session, inputs, world.ItemStack{})
		stateID := session.NextContainerStateID()
		if err := session.send(protocol.PlayContainerSetContent, EncodeContainerContent(cid, stateID, slots, carried)); err != nil {
			return cid, err
		}
	}
	return cid, nil
}

// CloseSmithingContainer returns any items in the Smithing Table input slots (0..2)
// and cursor back to session's inventory (preserving ItemMeta and ArmorTrim) and
// closes the window.
func (s *Server) CloseSmithingContainer(session *PlayerSession, containerID int32, notifyClient bool) error {
	if s == nil || session == nil {
		return nil
	}
	tr := getContainerTracker(session)
	tr.mu.Lock()
	if tr.open == nil || tr.open.MenuType != MenuTypeSmithing ||
		(containerID != 0 && tr.open.ContainerID != containerID) {
		tr.mu.Unlock()
		return s.CloseContainer(session, containerID, notifyClient)
	}
	var inputs [SmithingInputSlotCount]world.ItemStack
	for i := 0; i < SmithingInputSlotCount; i++ {
		inputs[i] = tr.open.Crafting[i]
		tr.open.Crafting[i] = world.ItemStack{}
	}
	tr.open.Result = world.ItemStack{}
	tr.mu.Unlock()

	var inputMeta [SmithingInputSlotCount]ItemMeta
	var inputTrim [SmithingInputSlotCount]ArmorTrim
	if st := getPlayerArmorTrimState(session); st != nil {
		st.mu.Lock()
		inputMeta = st.smithingInputMeta
		inputTrim = st.smithingInputTrim
		st.smithingInputMeta = [SmithingInputSlotCount]ItemMeta{}
		st.smithingInputTrim = [SmithingInputSlotCount]ArmorTrim{}
		st.mu.Unlock()
	}

	snap := session.Snapshot()
	for i := 0; i < SmithingInputSlotCount; i++ {
		st := inputs[i]
		if st.IsEmpty() {
			continue
		}
		placed := returnStackWithMetaAndTrimToInventory(session, st, inputMeta[i], inputTrim[i])
		if rem := st.Count - placed; rem > 0 {
			_, _ = s.SpawnItemEntityInDimension(snap.Dimension, snap.X, snap.Y+0.25, snap.Z, world.NewItemStack(st.ResolvedItemID(), rem))
		}
	}

	return s.CloseContainer(session, containerID, notifyClient)
}

func returnStackWithMetaAndTrimToInventory(session *PlayerSession, stack world.ItemStack, meta ItemMeta, trim ArmorTrim) int32 {
	if session == nil || stack.IsEmpty() {
		return 0
	}
	// If the item has custom metadata or an armor trim, place it into an empty slot so we can attach its metadata/trim.
	if !meta.IsEmpty() || trim.IsValid() {
		inv := session.Inventory()
		// Prefer hotbar (36..44), then main inventory (9..35).
		order := make([]int, 0, 36)
		for i := 36; i < 45; i++ {
			order = append(order, i)
		}
		for i := 9; i < 36; i++ {
			order = append(order, i)
		}
		for _, idx := range order {
			if inv[idx].IsEmpty() {
				session.SetInventorySlot(idx, stack)
				if !meta.IsEmpty() {
					SetSlotItemMeta(session, idx, meta)
				}
				if trim.IsValid() {
					SetSlotArmorTrim(session, idx, trim)
				}
				return stack.Count
			}
		}
	}
	added, _ := session.AddItemToInventory(stack)
	return added
}

// HandleSmithingClick processes a ServerboundContainerClickPacket on an open
// Smithing Table window (MenuTypeSmithing = 21).
func (s *Server) HandleSmithingClick(session *PlayerSession, click ContainerClick) error {
	if s == nil || session == nil || session.IsDead() {
		return nil
	}

	tr := getContainerTracker(session)
	tr.mu.Lock()
	if tr.open == nil || tr.open.MenuType != MenuTypeSmithing ||
		(click.ContainerID != 0 && tr.open.ContainerID != click.ContainerID) {
		tr.mu.Unlock()
		return nil
	}

	slot := int(click.Slot)
	mode := click.Mode
	button := click.Button

	// Drop cursor outside window (-999).
	if slot == -999 && mode == ContainerInputPickup {
		if !tr.carried.IsEmpty() {
			dropCount := tr.carried.Count
			if button == 1 {
				dropCount = 1
			}
			dropStack := world.NewItemStack(tr.carried.ResolvedItemID(), dropCount)
			rem := tr.carried.Count - dropCount
			if rem <= 0 {
				tr.carried = world.ItemStack{}
				SetCarriedItemMeta(session, ItemMeta{})
				SetCarriedArmorTrim(session, ArmorTrim{})
			} else {
				tr.carried = world.NewItemStack(tr.carried.ResolvedItemID(), rem)
			}
			tr.mu.Unlock()
			snap := session.Snapshot()
			_, _ = s.SpawnItemEntityInDimension(snap.Dimension, snap.X, snap.Y+0.25, snap.Z, dropStack)
			return s.syncSmithingContainer(session)
		}
		tr.mu.Unlock()
		return nil
	}

	if slot < 0 || slot >= SmithingMenuSlotCount {
		tr.mu.Unlock()
		return nil
	}

	var inputs [SmithingInputSlotCount]world.ItemStack
	for i := 0; i < SmithingInputSlotCount; i++ {
		inputs[i] = tr.open.Crafting[i]
	}

	trimState := getPlayerArmorTrimState(session)
	var inputMeta [SmithingInputSlotCount]ItemMeta
	var inputTrim [SmithingInputSlotCount]ArmorTrim
	if trimState != nil {
		trimState.mu.RLock()
		for i := 0; i < SmithingInputSlotCount; i++ {
			inputMeta[i] = trimState.smithingInputMeta[i].Clone()
			inputTrim[i] = trimState.smithingInputTrim[i]
		}
		trimState.mu.RUnlock()
	}

	eval, hasResult := EvaluateSmithingResult(
		inputs[SmithingSlotTemplate],
		inputs[SmithingSlotBase],
		inputs[SmithingSlotAddition],
		inputMeta[SmithingSlotBase],
		inputTrim[SmithingSlotBase],
	)
	if hasResult {
		tr.open.Result = eval.Result
	} else {
		tr.open.Result = world.ItemStack{}
	}

	slots := s.buildSmithingMenuSlots(session, inputs, tr.open.Result)
	slotMeta := make([]ItemMeta, SmithingMenuSlotCount)
	slotTrim := make([]ArmorTrim, SmithingMenuSlotCount)
	for i := 0; i < SmithingInputSlotCount; i++ {
		slotMeta[i] = inputMeta[i].Clone()
		slotTrim[i] = inputTrim[i]
	}
	if hasResult {
		slotMeta[SmithingSlotResult] = eval.ResultMeta.Clone()
		slotTrim[SmithingSlotResult] = eval.ResultTrim
	}
	for i := 0; i < 27; i++ {
		slotMeta[4+i] = GetSlotItemMeta(session, 9+i)
		if t, ok := GetSlotArmorTrim(session, 9+i); ok {
			slotTrim[4+i] = t
		}
	}
	for i := 0; i < 9; i++ {
		slotMeta[31+i] = GetSlotItemMeta(session, 36+i)
		if t, ok := GetSlotArmorTrim(session, 36+i); ok {
			slotTrim[31+i] = t
		}
	}

	tookResult := false
	var craftedStack world.ItemStack
	bx, by, bz := tr.open.BlockX, tr.open.BlockY, tr.open.BlockZ

	if slot == SmithingSlotResult {
		if !hasResult || eval.Result.IsEmpty() {
			tr.mu.Unlock()
			return s.syncSmithingContainer(session)
		}

		switch mode {
		case ContainerInputPickup:
			canTake := false
			carriedTrim, _ := GetCarriedArmorTrim(session)
			if tr.carried.IsEmpty() {
				tr.carried = eval.Result
				SetCarriedItemMeta(session, eval.ResultMeta)
				SetCarriedArmorTrim(session, eval.ResultTrim)
				canTake = true
			} else if tr.carried.ResolvedItemID() == eval.Result.ResolvedItemID() &&
				GetCarriedItemMeta(session).IsEmpty() &&
				eval.ResultMeta.IsEmpty() &&
				!carriedTrim.IsValid() &&
				!eval.ResultTrim.IsValid() &&
				tr.carried.Count+eval.Result.Count <= MaxItemStackSize {
				tr.carried = world.NewItemStack(eval.Result.ResolvedItemID(), tr.carried.Count+eval.Result.Count)
				canTake = true
			}
			if canTake {
				consumeSmithingInputs(slots, slotMeta, slotTrim)
				tookResult = true
				craftedStack = eval.Result
			}

		case ContainerInputQuickMove:
			placed := placeSmithingResultInMenuSlots(slots, slotMeta, slotTrim, eval.Result, eval.ResultMeta, eval.ResultTrim)
			if placed {
				consumeSmithingInputs(slots, slotMeta, slotTrim)
				tookResult = true
				craftedStack = eval.Result
			}
		}
	} else {
		switch mode {
		case ContainerInputPickup:
			carriedMeta := GetCarriedItemMeta(session)
			carriedTrim, _ := GetCarriedArmorTrim(session)
			prevSlotStack := slots[slot]
			prevSlotMeta := slotMeta[slot].Clone()
			prevSlotTrim := slotTrim[slot]

			nextSlotStack, nextCarriedStack := applyPickupSlot(prevSlotStack, tr.carried, button)
			slots[slot] = nextSlotStack
			tr.carried = nextCarriedStack

			// Update metadata & trim when swapping or placing/picking up stacks.
			if prevSlotStack.IsEmpty() && !nextSlotStack.IsEmpty() {
				slotMeta[slot] = carriedMeta.Clone()
				slotTrim[slot] = carriedTrim
				if nextCarriedStack.IsEmpty() {
					SetCarriedItemMeta(session, ItemMeta{})
					SetCarriedArmorTrim(session, ArmorTrim{})
				}
			} else if !prevSlotStack.IsEmpty() && nextSlotStack.IsEmpty() {
				SetCarriedItemMeta(session, prevSlotMeta)
				SetCarriedArmorTrim(session, prevSlotTrim)
				slotMeta[slot] = ItemMeta{}
				slotTrim[slot] = ArmorTrim{}
			} else if !prevSlotStack.IsEmpty() && !nextSlotStack.IsEmpty() &&
				prevSlotStack.ResolvedItemID() != nextSlotStack.ResolvedItemID() {
				slotMeta[slot] = carriedMeta.Clone()
				slotTrim[slot] = carriedTrim
				SetCarriedItemMeta(session, prevSlotMeta)
				SetCarriedArmorTrim(session, prevSlotTrim)
			}

		case ContainerInputQuickMove:
			if slot >= 0 && slot < SmithingInputSlotCount {
				moveSmithingSlotToSlice(slots, slotMeta, slotTrim, slot, 31, 40)
				moveSmithingSlotToSlice(slots, slotMeta, slotTrim, slot, 4, 31)
			} else if slot >= 4 {
				clicked := slots[slot]
				book := DefaultSmithingBook()
				switch {
				case book.IsTemplateItem(clicked) && canMergeIntoSmithingSlot(slots, slotMeta, slotTrim, slot, SmithingSlotTemplate):
					moveSmithingSlotToSlice(slots, slotMeta, slotTrim, slot, SmithingSlotTemplate, SmithingSlotTemplate+1)
				case book.IsBaseItem(clicked) && canMergeIntoSmithingSlot(slots, slotMeta, slotTrim, slot, SmithingSlotBase):
					moveSmithingSlotToSlice(slots, slotMeta, slotTrim, slot, SmithingSlotBase, SmithingSlotBase+1)
				case book.IsAdditionItem(clicked) && canMergeIntoSmithingSlot(slots, slotMeta, slotTrim, slot, SmithingSlotAddition):
					moveSmithingSlotToSlice(slots, slotMeta, slotTrim, slot, SmithingSlotAddition, SmithingSlotAddition+1)
				case slot >= 4 && slot < 31:
					moveSmithingSlotToSlice(slots, slotMeta, slotTrim, slot, 31, 40)
				case slot >= 31 && slot < 40:
					moveSmithingSlotToSlice(slots, slotMeta, slotTrim, slot, 4, 31)
				}
			}

		case ContainerInputSwap:
			if button >= 0 && button < 9 {
				hotbarIdx := 31 + int(button)
				slots[slot], slots[hotbarIdx] = slots[hotbarIdx], slots[slot]
				slotMeta[slot], slotMeta[hotbarIdx] = slotMeta[hotbarIdx], slotMeta[slot]
				slotTrim[slot], slotTrim[hotbarIdx] = slotTrim[hotbarIdx], slotTrim[slot]
			}
		}
	}

	// Write back player inventory slots (4..30 -> 9..35, 31..39 -> 36..44) along with ItemMeta and ArmorTrim.
	for i := 0; i < 27; i++ {
		invIdx := 9 + i
		menuIdx := 4 + i
		session.SetInventorySlot(invIdx, slots[menuIdx])
		if slots[menuIdx].IsEmpty() {
			SetSlotItemMeta(session, invIdx, ItemMeta{})
			ClearSlotArmorTrim(session, invIdx)
		} else {
			SetSlotItemMeta(session, invIdx, slotMeta[menuIdx])
			SetSlotArmorTrim(session, invIdx, slotTrim[menuIdx])
		}
	}
	for i := 0; i < 9; i++ {
		invIdx := 36 + i
		menuIdx := 31 + i
		session.SetInventorySlot(invIdx, slots[menuIdx])
		if slots[menuIdx].IsEmpty() {
			SetSlotItemMeta(session, invIdx, ItemMeta{})
			ClearSlotArmorTrim(session, invIdx)
		} else {
			SetSlotItemMeta(session, invIdx, slotMeta[menuIdx])
			SetSlotArmorTrim(session, invIdx, slotTrim[menuIdx])
		}
	}

	// Persist Smithing input slots (0..2) and their metadata/trims.
	for i := 0; i < SmithingInputSlotCount; i++ {
		tr.open.Crafting[i] = slots[i]
		if slots[i].IsEmpty() {
			slotMeta[i] = ItemMeta{}
			slotTrim[i] = ArmorTrim{}
		}
	}
	if trimState != nil {
		trimState.mu.Lock()
		for i := 0; i < SmithingInputSlotCount; i++ {
			trimState.smithingInputMeta[i] = slotMeta[i].Clone()
			trimState.smithingInputTrim[i] = slotTrim[i]
		}
		trimState.mu.Unlock()
	}

	nextEval, nextOK := EvaluateSmithingResult(
		slots[SmithingSlotTemplate],
		slots[SmithingSlotBase],
		slots[SmithingSlotAddition],
		slotMeta[SmithingSlotBase],
		slotTrim[SmithingSlotBase],
	)
	if nextOK {
		tr.open.Result = nextEval.Result
	} else {
		tr.open.Result = world.ItemStack{}
	}
	tr.mu.Unlock()

	if tookResult {
		snap := session.Snapshot()
		dim := world.NormalizeDimension(snap.Dimension)
		fx, fy, fz := float64(bx)+0.5, float64(by)+0.5, float64(bz)+0.5
		if bx == 0 && by == 0 && bz == 0 {
			fx, fy, fz = snap.X, snap.Y, snap.Z
		}
		cx := int32(int(math.Floor(fx)) >> 4)
		cz := int32(int(math.Floor(fz)) >> 4)
		s.BroadcastDimensionChunk(dim, cx, cz, PlaySoundPacketID,
			EncodeSoundPacket("minecraft:block.smithing_table.use", SoundSourceBlocks, fx, fy, fz, 1.0, 1.0, 0))
		if !craftedStack.IsEmpty() {
			s.TriggerRecipeCrafted(session, craftedStack)
		}
	}

	return s.syncSmithingContainer(session)
}

func consumeSmithingInputs(slots []world.ItemStack, slotMeta []ItemMeta, slotTrim []ArmorTrim) {
	for i := 0; i < SmithingInputSlotCount; i++ {
		if slots[i].IsEmpty() {
			continue
		}
		rem := slots[i].Count - 1
		if rem <= 0 {
			slots[i] = world.ItemStack{}
			slotMeta[i] = ItemMeta{}
			slotTrim[i] = ArmorTrim{}
		} else {
			slots[i] = world.NewItemStack(slots[i].ResolvedItemID(), rem)
		}
	}
}

func canMergeIntoSmithingSlot(slots []world.ItemStack, slotMeta []ItemMeta, slotTrim []ArmorTrim, srcIdx, dstIdx int) bool {
	src := slots[srcIdx]
	if src.IsEmpty() {
		return false
	}
	dst := slots[dstIdx]
	if dst.IsEmpty() {
		return true
	}
	return dst.ResolvedItemID() == src.ResolvedItemID() &&
		dst.Count < MaxItemStackSize &&
		slotMeta[srcIdx].IsEmpty() &&
		slotMeta[dstIdx].IsEmpty() &&
		!slotTrim[srcIdx].IsValid() &&
		!slotTrim[dstIdx].IsValid()
}

func moveSmithingSlotToSlice(slots []world.ItemStack, slotMeta []ItemMeta, slotTrim []ArmorTrim, srcIdx, start, end int) {
	src := slots[srcIdx]
	if src.IsEmpty() {
		return
	}
	srcMeta := slotMeta[srcIdx].Clone()
	srcTrim := slotTrim[srcIdx]

	// If stack has no custom metadata or trim, first try merging with existing stacks.
	if srcMeta.IsEmpty() && !srcTrim.IsValid() {
		for i := start; i < end && !src.IsEmpty(); i++ {
			if !slots[i].IsEmpty() &&
				slots[i].ResolvedItemID() == src.ResolvedItemID() &&
				slotMeta[i].IsEmpty() &&
				!slotTrim[i].IsValid() &&
				slots[i].Count < MaxItemStackSize {
				space := MaxItemStackSize - slots[i].Count
				move := src.Count
				if move > space {
					move = space
				}
				slots[i] = world.NewItemStack(slots[i].ResolvedItemID(), slots[i].Count+move)
				src.Count -= move
				if src.Count <= 0 {
					src = world.ItemStack{}
				}
			}
		}
	}

	// Place remaining into first empty slot.
	for i := start; i < end && !src.IsEmpty(); i++ {
		if slots[i].IsEmpty() {
			slots[i] = src
			slotMeta[i] = srcMeta.Clone()
			slotTrim[i] = srcTrim
			src = world.ItemStack{}
			srcMeta = ItemMeta{}
			srcTrim = ArmorTrim{}
			break
		}
	}

	slots[srcIdx] = src
	if src.IsEmpty() {
		slotMeta[srcIdx] = ItemMeta{}
		slotTrim[srcIdx] = ArmorTrim{}
	}
}

func placeSmithingResultInMenuSlots(slots []world.ItemStack, slotMeta []ItemMeta, slotTrim []ArmorTrim, res world.ItemStack, resMeta ItemMeta, resTrim ArmorTrim) bool {
	if res.IsEmpty() {
		return false
	}
	// Check hotbar (31..40) then main inventory (4..31) for an empty slot if metadata/trim is present.
	ranges := [][2]int{{31, 40}, {4, 31}}
	if resMeta.IsEmpty() && !resTrim.IsValid() {
		for _, rg := range ranges {
			for i := rg[0]; i < rg[1]; i++ {
				if !slots[i].IsEmpty() &&
					slots[i].ResolvedItemID() == res.ResolvedItemID() &&
					slotMeta[i].IsEmpty() &&
					!slotTrim[i].IsValid() &&
					slots[i].Count+res.Count <= MaxItemStackSize {
					slots[i] = world.NewItemStack(res.ResolvedItemID(), slots[i].Count+res.Count)
					return true
				}
			}
		}
	}
	for _, rg := range ranges {
		for i := rg[0]; i < rg[1]; i++ {
			if slots[i].IsEmpty() {
				slots[i] = res
				slotMeta[i] = resMeta.Clone()
				slotTrim[i] = resTrim
				return true
			}
		}
	}
	return false
}

func (s *Server) buildSmithingMenuSlots(session *PlayerSession, inputs [SmithingInputSlotCount]world.ItemStack, result world.ItemStack) []world.ItemStack {
	inv := session.Inventory()
	slots := make([]world.ItemStack, SmithingMenuSlotCount)
	slots[SmithingSlotTemplate] = inputs[SmithingSlotTemplate]
	slots[SmithingSlotBase] = inputs[SmithingSlotBase]
	slots[SmithingSlotAddition] = inputs[SmithingSlotAddition]
	slots[SmithingSlotResult] = result
	copy(slots[4:31], inv[9:36])
	copy(slots[31:40], inv[36:45])
	return slots
}

func (s *Server) syncSmithingContainer(session *PlayerSession) error {
	if s == nil || session == nil || session.send == nil {
		return nil
	}
	tr := getContainerTracker(session)
	tr.mu.Lock()
	if tr.open == nil || tr.open.MenuType != MenuTypeSmithing {
		tr.mu.Unlock()
		return nil
	}
	cid := tr.open.ContainerID
	var inputs [SmithingInputSlotCount]world.ItemStack
	for i := 0; i < SmithingInputSlotCount; i++ {
		inputs[i] = tr.open.Crafting[i]
	}
	result := tr.open.Result
	carried := tr.carried
	tr.mu.Unlock()

	var inputMeta [SmithingInputSlotCount]ItemMeta
	var inputTrim [SmithingInputSlotCount]ArmorTrim
	if st := getPlayerArmorTrimState(session); st != nil {
		st.mu.RLock()
		for i := 0; i < SmithingInputSlotCount; i++ {
			inputMeta[i] = st.smithingInputMeta[i].Clone()
			inputTrim[i] = st.smithingInputTrim[i]
		}
		st.mu.RUnlock()
	}

	eval, hasResult := EvaluateSmithingResult(
		inputs[SmithingSlotTemplate],
		inputs[SmithingSlotBase],
		inputs[SmithingSlotAddition],
		inputMeta[SmithingSlotBase],
		inputTrim[SmithingSlotBase],
	)
	if hasResult {
		result = eval.Result
	}

	slots := s.buildSmithingMenuSlots(session, inputs, result)
	stateID := session.NextContainerStateID()

	// Encode container content including ItemMeta and ArmorTrim components on any slot or cursor item.
	w := protocol.NewWriter(32 + len(slots)*12)
	w.VarInt(cid)
	w.VarInt(stateID)
	w.VarInt(int32(len(slots)))
	for i, st := range slots {
		var meta ItemMeta
		var trim ArmorTrim
		switch {
		case i >= 0 && i < SmithingInputSlotCount:
			meta = inputMeta[i]
			trim = inputTrim[i]
		case i == SmithingSlotResult && hasResult:
			meta = eval.ResultMeta
			trim = eval.ResultTrim
		case i >= 4 && i < 31:
			meta = GetSlotItemMeta(session, 9+(i-4))
			trim, _ = GetSlotArmorTrim(session, 9+(i-4))
		case i >= 31 && i < 40:
			meta = GetSlotItemMeta(session, 36+(i-31))
			trim, _ = GetSlotArmorTrim(session, 36+(i-31))
		}
		WriteSlotWithArmorTrim(w, st, meta, trim)
	}
	carriedMeta := GetCarriedItemMeta(session)
	carriedTrim, _ := GetCarriedArmorTrim(session)
	WriteSlotWithArmorTrim(w, carried, carriedMeta, carriedTrim)

	return session.send(protocol.PlayContainerSetContent, w.Bytes())
}
