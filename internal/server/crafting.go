package server

import (
	"archive/zip"
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"sync"

	"regionio/internal/world"
)

//go:embed recipe_data.zip
var embeddedRecipeData []byte

// Ingredient represents one slot requirement in a shaped or shapeless recipe.
// An empty Allowed map means the slot must be empty (in a shaped pattern).
type Ingredient struct {
	Allowed map[string]bool
}

// IsEmpty reports whether this pattern cell requires an empty slot.
func (ing Ingredient) IsEmpty() bool {
	return len(ing.Allowed) == 0
}

// Matches reports whether stack satisfies this ingredient.
func (ing Ingredient) Matches(stack world.ItemStack) bool {
	if ing.IsEmpty() {
		return stack.IsEmpty()
	}
	if stack.IsEmpty() {
		return false
	}
	return ing.Allowed[stack.ResolvedID()]
}

// CraftingRecipe represents one parsed vanilla shaped or shapeless crafting recipe.
type CraftingRecipe struct {
	Name        string
	Type        string
	Category    string
	Group       string
	Width       int
	Height      int
	Pattern     []Ingredient // Row-major Width*Height for crafting_shaped
	Ingredients []Ingredient // Unordered list for crafting_shapeless
	Result      world.ItemStack
}

// RecipeBook holds the loaded item tags and crafting recipes from the datapack.
type RecipeBook struct {
	tags    map[string][]string
	recipes []*CraftingRecipe
	byName  map[string]*CraftingRecipe
}

var (
	defaultBookOnce sync.Once
	defaultBook     *RecipeBook
)

func init() {
	// Ensure vanilla item IDs from the embedded datapack are registered early
	// so world.NewNamedItemStack resolves tools, sticks, ingots, and food items.
	_ = DefaultRecipeBook()
}

// DefaultRecipeBook returns the singleton RecipeBook loaded from the embedded
// vanilla 26.1.2 datapack archive.
func DefaultRecipeBook() *RecipeBook {
	defaultBookOnce.Do(func() {
		book, err := LoadRecipeBookFromZip(embeddedRecipeData)
		if err != nil {
			panic(fmt.Sprintf("server: loading embedded recipe datapack: %v", err))
		}
		defaultBook = book
	})
	return defaultBook
}

type rawTagFile struct {
	Values []string `json:"values"`
}

type rawRecipeResult struct {
	ID    string `json:"id"`
	Count int32  `json:"count"`
}

type rawRecipeFile struct {
	Type        string                     `json:"type"`
	Category    string                     `json:"category"`
	Group       string                     `json:"group"`
	Key         map[string]json.RawMessage `json:"key"`
	Pattern     []string                   `json:"pattern"`
	Ingredients []json.RawMessage          `json:"ingredients"`
	Result      rawRecipeResult            `json:"result"`
}

// LoadRecipeBookFromZip parses item IDs, item tags, and shaped/shapeless
// crafting recipes from a zip archive structured like the vanilla datapack.
func LoadRecipeBookFromZip(zipBytes []byte) (*RecipeBook, error) {
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		return nil, err
	}

	rawTags := make(map[string][]string, 256)
	type rawRecipeEntry struct {
		name string
		data []byte
	}
	var recipeFiles []rawRecipeEntry

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
			recipeFiles = append(recipeFiles, rawRecipeEntry{name: recName, data: data})
		}
	}

	resolvedTags := resolveAllItemTags(rawTags)
	sort.Slice(recipeFiles, func(i, j int) bool { return recipeFiles[i].name < recipeFiles[j].name })

	book := &RecipeBook{
		tags:    resolvedTags,
		recipes: make([]*CraftingRecipe, 0, len(recipeFiles)),
		byName:  make(map[string]*CraftingRecipe, len(recipeFiles)),
	}

	for _, rf := range recipeFiles {
		rec, ok, err := parseCraftingRecipe(rf.name, rf.data, resolvedTags)
		if err != nil {
			return nil, fmt.Errorf("recipe %s: %w", rf.name, err)
		}
		if !ok {
			continue
		}
		book.recipes = append(book.recipes, rec)
		book.byName[rec.Name] = rec
	}
	return book, nil
}

func resolveAllItemTags(raw map[string][]string) map[string][]string {
	resolved := make(map[string][]string, len(raw))
	visiting := make(map[string]bool)

	var expand func(tag string) []string
	expand = func(tag string) []string {
		tag = canonicalResourceName(tag)
		if items, ok := resolved[tag]; ok {
			return items
		}
		if visiting[tag] {
			return nil
		}
		visiting[tag] = true
		defer delete(visiting, tag)

		seen := make(map[string]bool)
		var out []string
		for _, entry := range raw[tag] {
			entry = strings.TrimSpace(entry)
			if strings.HasPrefix(entry, "#") {
				for _, sub := range expand(strings.TrimPrefix(entry, "#")) {
					if !seen[sub] {
						seen[sub] = true
						out = append(out, sub)
					}
				}
			} else {
				norm := canonicalResourceName(entry)
				if norm != "" && !seen[norm] {
					seen[norm] = true
					out = append(out, norm)
				}
			}
		}
		resolved[tag] = out
		return out
	}

	for tag := range raw {
		expand(tag)
	}
	return resolved
}

func canonicalResourceName(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return ""
	}
	if !strings.Contains(s, ":") {
		return "minecraft:" + s
	}
	return s
}

func parseIngredientSpec(raw json.RawMessage, tags map[string][]string) (Ingredient, error) {
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		return buildIngredientFromTokens([]string{single}, tags), nil
	}
	var list []string
	if err := json.Unmarshal(raw, &list); err == nil {
		return buildIngredientFromTokens(list, tags), nil
	}
	return Ingredient{}, fmt.Errorf("unsupported ingredient JSON: %s", string(raw))
}

func buildIngredientFromTokens(tokens []string, tags map[string][]string) Ingredient {
	allowed := make(map[string]bool)
	for _, tok := range tokens {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		if strings.HasPrefix(tok, "#") {
			tagName := canonicalResourceName(strings.TrimPrefix(tok, "#"))
			for _, item := range tags[tagName] {
				allowed[item] = true
			}
		} else {
			allowed[canonicalResourceName(tok)] = true
		}
	}
	return Ingredient{Allowed: allowed}
}

func parseCraftingRecipe(name string, data []byte, tags map[string][]string) (*CraftingRecipe, bool, error) {
	var header struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &header); err != nil {
		return nil, false, err
	}
	rtype := canonicalResourceName(header.Type)
	if rtype != "minecraft:crafting_shaped" && rtype != "minecraft:crafting_shapeless" {
		return nil, false, nil
	}
	var raw rawRecipeFile
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, false, err
	}
	if raw.Result.ID == "" {
		return nil, false, nil
	}
	count := raw.Result.Count
	if count <= 0 {
		count = 1
	}
	resultStack := world.NewNamedItemStack(raw.Result.ID, count)
	if resultStack.IsEmpty() {
		return nil, false, nil
	}

	rec := &CraftingRecipe{
		Name:     canonicalResourceName(name),
		Type:     rtype,
		Category: raw.Category,
		Group:    raw.Group,
		Result:   resultStack,
	}

	if rtype == "minecraft:crafting_shaped" {
		if len(raw.Pattern) == 0 || len(raw.Pattern) > 3 {
			return nil, false, nil
		}
		keyMap := make(map[rune]Ingredient, len(raw.Key))
		for k, v := range raw.Key {
			runes := []rune(k)
			if len(runes) != 1 {
				continue
			}
			ing, err := parseIngredientSpec(v, tags)
			if err != nil || ing.IsEmpty() {
				return nil, false, err
			}
			keyMap[runes[0]] = ing
		}
		height := len(raw.Pattern)
		width := 0
		rowRunes := make([][]rune, height)
		for r, line := range raw.Pattern {
			rr := []rune(line)
			if len(rr) > width {
				width = len(rr)
			}
			rowRunes[r] = rr
		}
		if width == 0 || width > 3 {
			return nil, false, nil
		}
		pattern := make([]Ingredient, width*height)
		for r := 0; r < height; r++ {
			for c := 0; c < width; c++ {
				ch := ' '
				if c < len(rowRunes[r]) {
					ch = rowRunes[r][c]
				}
				if ch == ' ' {
					continue
				}
				ing, ok := keyMap[ch]
				if !ok {
					return nil, false, nil
				}
				pattern[r*width+c] = ing
			}
		}
		rec.Width = width
		rec.Height = height
		rec.Pattern = pattern
		return rec, true, nil
	}

	// minecraft:crafting_shapeless
	if len(raw.Ingredients) == 0 || len(raw.Ingredients) > 9 {
		return nil, false, nil
	}
	ings := make([]Ingredient, 0, len(raw.Ingredients))
	for _, rawIng := range raw.Ingredients {
		ing, err := parseIngredientSpec(rawIng, tags)
		if err != nil || ing.IsEmpty() {
			return nil, false, err
		}
		ings = append(ings, ing)
	}
	rec.Ingredients = ings
	return rec, true, nil
}

// Count returns the number of loaded crafting recipes in the book.
func (b *RecipeBook) Count() int {
	if b == nil {
		return 0
	}
	return len(b.recipes)
}

// Recipe looks up a loaded crafting recipe by its resource identifier.
func (b *RecipeBook) Recipe(name string) (*CraftingRecipe, bool) {
	if b == nil {
		return nil, false
	}
	r, ok := b.byName[canonicalResourceName(name)]
	return r, ok
}

// TagItems returns the expanded list of item identifiers belonging to an item tag.
func (b *RecipeBook) TagItems(tag string) []string {
	if b == nil {
		return nil
	}
	tag = strings.TrimPrefix(strings.TrimSpace(tag), "#")
	items := b.tags[canonicalResourceName(tag)]
	out := make([]string, len(items))
	copy(out, items)
	return out
}

// MatchGrid finds the first recipe matching the given width x height crafting
// grid (e.g. 2x2 for player inventory or 3x3 for a crafting table).
func (b *RecipeBook) MatchGrid(grid []world.ItemStack, width, height int) (world.ItemStack, *CraftingRecipe, bool) {
	if b == nil || width <= 0 || height <= 0 || len(grid) < width*height {
		return world.ItemStack{}, nil, false
	}

	minR, maxR := height, -1
	minC, maxC := width, -1
	var nonEmpty []world.ItemStack

	for r := 0; r < height; r++ {
		for c := 0; c < width; c++ {
			st := grid[r*width+c]
			if st.IsEmpty() {
				continue
			}
			nonEmpty = append(nonEmpty, st)
			if r < minR {
				minR = r
			}
			if r > maxR {
				maxR = r
			}
			if c < minC {
				minC = c
			}
			if c > maxC {
				maxC = c
			}
		}
	}
	if len(nonEmpty) == 0 {
		return world.ItemStack{}, nil, false
	}

	boxW := maxC - minC + 1
	boxH := maxR - minR + 1

	for _, rec := range b.recipes {
		if rec.Type == "minecraft:crafting_shaped" {
			if rec.Width != boxW || rec.Height != boxH {
				continue
			}
			if matchShapedBox(rec, grid, width, minR, minC, false) ||
				(boxW > 1 && matchShapedBox(rec, grid, width, minR, minC, true)) {
				return rec.Result, rec, true
			}
		} else if rec.Type == "minecraft:crafting_shapeless" {
			if len(rec.Ingredients) != len(nonEmpty) {
				continue
			}
			if matchShapeless(rec.Ingredients, nonEmpty) {
				return rec.Result, rec, true
			}
		}
	}
	return world.ItemStack{}, nil, false
}

func matchShapedBox(rec *CraftingRecipe, grid []world.ItemStack, gridW, minR, minC int, mirror bool) bool {
	for r := 0; r < rec.Height; r++ {
		for c := 0; c < rec.Width; c++ {
			patCol := c
			if mirror {
				patCol = rec.Width - 1 - c
			}
			ing := rec.Pattern[r*rec.Width+patCol]
			st := grid[(minR+r)*gridW+(minC+c)]
			if !ing.Matches(st) {
				return false
			}
		}
	}
	return true
}

func matchShapeless(ings []Ingredient, stacks []world.ItemStack) bool {
	var used [9]bool
	var dfs func(idx int) bool
	dfs = func(idx int) bool {
		if idx == len(ings) {
			return true
		}
		ing := ings[idx]
		for i := 0; i < len(stacks); i++ {
			if used[i] || !ing.Matches(stacks[i]) {
				continue
			}
			used[i] = true
			if dfs(idx + 1) {
				return true
			}
			used[i] = false
		}
		return false
	}
	return dfs(0)
}

// MatchCraftingGrid matches a 2x2 or 3x3 crafting grid against DefaultRecipeBook().
func MatchCraftingGrid(grid []world.ItemStack, width, height int) (world.ItemStack, *CraftingRecipe, bool) {
	return DefaultRecipeBook().MatchGrid(grid, width, height)
}
