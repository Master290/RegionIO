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

	"regionio/internal/protocol"
	"regionio/internal/world"
)

// Stonecutter menu constants for Minecraft 26.1.2 (protocol 775).
const (
	// MenuTypeStonecutter is BuiltInRegistries.MENU index 24 ("minecraft:stonecutter").
	MenuTypeStonecutter int32 = 24

	StonecutterSlotInput          = 0
	StonecutterSlotResult         = 1
	StonecutterInputSlotCount     = 1
	StonecutterContainerSlotCount = 2
	StonecutterMenuSlotCount      = 38 // 2 stonecutter slots + 27 main inventory (2..28) + 9 hotbar (29..37)

	// StonecutterDataSelectedRecipe is ContainerData property 0 in StonecutterMenu
	// (-1 when no recipe is selected, 0..N-1 when a recipe is selected).
	StonecutterDataSelectedRecipe int16 = 0

	StonecutterBlockID = "minecraft:stonecutter"
)

// IsStonecutterBlock reports whether state is a `minecraft:stonecutter` block.
func IsStonecutterBlock(state uint16) bool {
	return world.BlockName(state) == StonecutterBlockID
}

// StonecuttingRecipe represents one parsed vanilla `minecraft:stonecutting` recipe.
type StonecuttingRecipe struct {
	Name       string
	Group      string
	Ingredient Ingredient
	Result     world.ItemStack
}

// StonecutterBook holds all `minecraft:stonecutting` recipes loaded from the
// embedded vanilla 26.1.2 datapack archive (`recipe_data.zip`).
type StonecutterBook struct {
	recipes []*StonecuttingRecipe
	byName  map[string]*StonecuttingRecipe
}

var (
	defaultStonecutterOnce sync.Once
	defaultStonecutterBook *StonecutterBook
)

// DefaultStonecutterBook returns the singleton StonecutterBook loaded from the
// embedded vanilla 26.1.2 datapack archive.
func DefaultStonecutterBook() *StonecutterBook {
	defaultStonecutterOnce.Do(func() {
		_ = DefaultRecipeBook()
		book, err := LoadStonecutterBookFromZip(embeddedRecipeData)
		if err != nil {
			panic(fmt.Sprintf("server: loading embedded stonecutting recipes: %v", err))
		}
		defaultStonecutterBook = book
	})
	return defaultStonecutterBook
}

type rawStonecuttingRecipeFile struct {
	Type       string          `json:"type"`
	Group      string          `json:"group"`
	Ingredient json.RawMessage `json:"ingredient"`
	Result     rawRecipeResult `json:"result"`
}

// LoadStonecutterBookFromZip parses all `minecraft:stonecutting` recipes from
// a zip archive structured like the vanilla 26.1.2 datapack.
func LoadStonecutterBookFromZip(zipBytes []byte) (*StonecutterBook, error) {
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

	book := &StonecutterBook{
		recipes: make([]*StonecuttingRecipe, 0, 275),
		byName:  make(map[string]*StonecuttingRecipe, 275),
	}

	for _, rf := range recipeFiles {
		rec, ok, err := parseStonecuttingRecipe(rf.name, rf.data, resolvedTags)
		if err != nil {
			return nil, fmt.Errorf("stonecutting recipe %s: %w", rf.name, err)
		}
		if !ok {
			continue
		}
		book.recipes = append(book.recipes, rec)
		book.byName[rec.Name] = rec
	}
	return book, nil
}

func parseStonecuttingRecipe(name string, data []byte, tags map[string][]string) (*StonecuttingRecipe, bool, error) {
	var header struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &header); err != nil {
		return nil, false, err
	}
	if canonicalResourceName(header.Type) != "minecraft:stonecutting" {
		return nil, false, nil
	}

	var raw rawStonecuttingRecipeFile
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

	return &StonecuttingRecipe{
		Name:       canonicalResourceName(name),
		Group:      raw.Group,
		Ingredient: ing,
		Result:     resultStack,
	}, true, nil
}

// Count returns the total number of loaded `minecraft:stonecutting` recipes.
func (b *StonecutterBook) Count() int {
	if b == nil {
		return 0
	}
	return len(b.recipes)
}

// All returns a copy of all loaded `minecraft:stonecutting` recipes in sorted order.
func (b *StonecutterBook) All() []*StonecuttingRecipe {
	if b == nil {
		return nil
	}
	out := make([]*StonecuttingRecipe, len(b.recipes))
	copy(out, b.recipes)
	return out
}

// Recipe looks up a `minecraft:stonecutting` recipe by its resource identifier.
func (b *StonecutterBook) Recipe(name string) (*StonecuttingRecipe, bool) {
	if b == nil {
		return nil, false
	}
	rec, ok := b.byName[canonicalResourceName(name)]
	return rec, ok
}

// RecipesForInput returns the ordered list of `minecraft:stonecutting` recipes
// that accept input, matching `StonecutterMenu.recipesForInput`.
func (b *StonecutterBook) RecipesForInput(input world.ItemStack) []*StonecuttingRecipe {
	if b == nil || input.IsEmpty() {
		return nil
	}
	var out []*StonecuttingRecipe
	for _, rec := range b.recipes {
		if rec.Ingredient.Matches(input) {
			out = append(out, rec)
		}
	}
	return out
}

// CanStonecut reports whether input matches at least one `minecraft:stonecutting` recipe.
func (b *StonecutterBook) CanStonecut(input world.ItemStack) bool {
	if b == nil || input.IsEmpty() {
		return false
	}
	for _, rec := range b.recipes {
		if rec.Ingredient.Matches(input) {
			return true
		}
	}
	return false
}

// AvailableStonecutterRecipes returns the ordered stonecutting recipes for input
// from DefaultStonecutterBook.
func AvailableStonecutterRecipes(input world.ItemStack) []*StonecuttingRecipe {
	return DefaultStonecutterBook().RecipesForInput(input)
}

// EvaluateStonecutterResult computes the output stack for input and selectedIndex
// in a StonecutterMenu.
func EvaluateStonecutterResult(input world.ItemStack, selectedIndex int) (result world.ItemStack, recipe *StonecuttingRecipe, validIndex int, ok bool) {
	recipes := AvailableStonecutterRecipes(input)
	if len(recipes) == 0 || selectedIndex < 0 || selectedIndex >= len(recipes) {
		return world.ItemStack{}, nil, -1, false
	}
	rec := recipes[selectedIndex]
	return rec.Result, rec, selectedIndex, true
}

// OpenStonecutter opens the Stonecutter screen (MenuTypeStonecutter = 24) for
// session at block position (bx, by, bz) and sends PlayOpenScreen,
// PlayContainerSetContent, and PlayContainerSetData (selected recipe = -1).
func (s *Server) OpenStonecutter(session *PlayerSession, bx, by, bz int) (int32, error) {
	if s == nil || session == nil {
		return 0, errors.New("server: nil session")
	}
	if session.IsDead() {
		return 0, errors.New("server: dead player cannot open stonecutter")
	}

	_ = s.CloseStonecutterContainer(session, 0, false)

	tr := getContainerTracker(session)
	tr.mu.Lock()
	tr.nextWindow = (tr.nextWindow % 100) + 1
	cid := tr.nextWindow
	tr.open = &OpenContainerState{
		ContainerID: cid,
		MenuType:    MenuTypeStonecutter,
		BlockX:      bx,
		BlockY:      by,
		BlockZ:      bz,
		AnvilCost:   -1, // stores selectedRecipeIndex (-1 when none selected)
	}
	carried := tr.carried
	tr.mu.Unlock()

	if session.send != nil {
		if err := session.send(protocol.PlayOpenScreen, EncodeOpenScreen(cid, MenuTypeStonecutter, "Stonecutter")); err != nil {
			return cid, err
		}
		slots := s.buildStonecutterMenuSlots(session, world.ItemStack{}, world.ItemStack{})
		stateID := session.NextContainerStateID()
		if err := session.send(protocol.PlayContainerSetContent, EncodeContainerContent(cid, stateID, slots, carried)); err != nil {
			return cid, err
		}
		if err := session.send(protocol.PlayContainerSetData, EncodeContainerSetData(cid, StonecutterDataSelectedRecipe, -1)); err != nil {
			return cid, err
		}
	}
	return cid, nil
}

// CloseStonecutterContainer returns any item in the Stonecutter input slot (0)
// and cursor back to session's inventory (or drops overflow in the world) and
// closes the window.
func (s *Server) CloseStonecutterContainer(session *PlayerSession, containerID int32, notifyClient bool) error {
	if s == nil || session == nil {
		return nil
	}
	tr := getContainerTracker(session)
	tr.mu.Lock()
	if tr.open == nil || tr.open.MenuType != MenuTypeStonecutter ||
		(containerID != 0 && tr.open.ContainerID != containerID) {
		tr.mu.Unlock()
		return s.CloseContainer(session, containerID, notifyClient)
	}
	inputStack := tr.open.Crafting[StonecutterSlotInput]
	tr.open.Crafting[StonecutterSlotInput] = world.ItemStack{}
	tr.open.Result = world.ItemStack{}
	tr.open.AnvilCost = -1
	tr.mu.Unlock()

	if !inputStack.IsEmpty() {
		snap := session.Snapshot()
		added, _ := session.AddItemToInventory(inputStack)
		if rem := inputStack.Count - added; rem > 0 {
			_, _ = s.SpawnItemEntityInDimension(snap.Dimension, snap.X, snap.Y+0.25, snap.Z, world.NewItemStack(inputStack.ResolvedItemID(), rem))
		}
	}

	return s.CloseContainer(session, containerID, notifyClient)
}

// HandleStonecutterButtonClick handles ServerboundContainerButtonClickPacket (0x11)
// when session has a Stonecutter open (selecting recipe index buttonID).
// Returns (true, nil) if the open container was a Stonecutter.
func (s *Server) HandleStonecutterButtonClick(session *PlayerSession, containerID, buttonID int32) (bool, error) {
	if s == nil || session == nil || session.IsDead() {
		return false, nil
	}
	tr := getContainerTracker(session)
	tr.mu.Lock()
	if tr.open == nil || tr.open.MenuType != MenuTypeStonecutter ||
		(containerID != 0 && tr.open.ContainerID != containerID) {
		tr.mu.Unlock()
		return false, nil
	}

	input := tr.open.Crafting[StonecutterSlotInput]
	resStack, _, validIdx, ok := EvaluateStonecutterResult(input, int(buttonID))
	if !ok {
		tr.mu.Unlock()
		return true, nil
	}

	tr.open.AnvilCost = int16(validIdx)
	tr.open.Result = resStack
	cid := tr.open.ContainerID
	tr.mu.Unlock()

	if session.send != nil {
		_ = session.send(protocol.PlayContainerSetData, EncodeContainerSetData(cid, StonecutterDataSelectedRecipe, int16(validIdx)))
	}
	return true, s.syncStonecutterContainer(session)
}

// HandleStonecutterClick processes a ServerboundContainerClickPacket on an open
// Stonecutter window (MenuTypeStonecutter = 24).
func (s *Server) HandleStonecutterClick(session *PlayerSession, click ContainerClick) error {
	if s == nil || session == nil || session.IsDead() {
		return nil
	}

	tr := getContainerTracker(session)
	tr.mu.Lock()
	if tr.open == nil || tr.open.MenuType != MenuTypeStonecutter ||
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
			} else {
				tr.carried = world.NewItemStack(tr.carried.ResolvedItemID(), rem)
			}
			bx, by, bz := tr.open.BlockX, tr.open.BlockY, tr.open.BlockZ
			 _ = bx
			_ = by
			_ = bz
			tr.mu.Unlock()
			snap := session.Snapshot()
			_, _ = s.SpawnItemEntityInDimension(snap.Dimension, snap.X, snap.Y+0.25, snap.Z, dropStack)
			return s.syncStonecutterContainer(session)
		}
		tr.mu.Unlock()
		return nil
	}

	if slot < 0 || slot >= StonecutterMenuSlotCount {
		tr.mu.Unlock()
		return nil
	}

	prevInput := tr.open.Crafting[StonecutterSlotInput]
	selectedIdx := int(tr.open.AnvilCost)
	resStack, _, validIdx, hasResult := EvaluateStonecutterResult(prevInput, selectedIdx)
	if hasResult {
		tr.open.Result = resStack
		tr.open.AnvilCost = int16(validIdx)
	} else {
		tr.open.Result = world.ItemStack{}
	}

	slots := s.buildStonecutterMenuSlots(session, prevInput, tr.open.Result)
	tookResult := false
	var craftedStack world.ItemStack
	bx, by, bz := tr.open.BlockX, tr.open.BlockY, tr.open.BlockZ

	if slot == StonecutterSlotResult {
		if !hasResult || resStack.IsEmpty() || slots[StonecutterSlotInput].IsEmpty() {
			tr.mu.Unlock()
			return s.syncStonecutterContainer(session)
		}

		switch mode {
		case ContainerInputPickup:
			canTake := false
			if tr.carried.IsEmpty() {
				tr.carried = resStack
				SetCarriedItemMeta(session, ItemMeta{})
				canTake = true
			} else if tr.carried.ResolvedItemID() == resStack.ResolvedItemID() &&
				GetCarriedItemMeta(session).IsEmpty() &&
				tr.carried.Count+resStack.Count <= MaxItemStackSize {
				tr.carried = world.NewItemStack(resStack.ResolvedItemID(), tr.carried.Count+resStack.Count)
				canTake = true
			}
			if canTake {
				remIn := slots[StonecutterSlotInput].Count - 1
				if remIn <= 0 {
					slots[StonecutterSlotInput] = world.ItemStack{}
				} else {
					slots[StonecutterSlotInput] = world.NewItemStack(slots[StonecutterSlotInput].ResolvedItemID(), remIn)
				}
				tookResult = true
				craftedStack = resStack
			}

		case ContainerInputQuickMove:
			// Shift-clicking result crafts as many times as input count and player inventory space allow.
			for !slots[StonecutterSlotInput].IsEmpty() {
				if !canFitInSlice(slots, resStack, 2, StonecutterMenuSlotCount) {
					break
				}
				rem := mergeIntoSlotSlice(slots, resStack, 29, 38)
				rem = mergeIntoSlotSlice(slots, rem, 2, 29)
				if !rem.IsEmpty() {
					break
				}
				remIn := slots[StonecutterSlotInput].Count - 1
				if remIn <= 0 {
					slots[StonecutterSlotInput] = world.ItemStack{}
				} else {
					slots[StonecutterSlotInput] = world.NewItemStack(slots[StonecutterSlotInput].ResolvedItemID(), remIn)
				}
				tookResult = true
				if craftedStack.IsEmpty() {
					craftedStack = resStack
				} else {
					craftedStack.Count += resStack.Count
				}
			}
		}
	} else {
		switch mode {
		case ContainerInputPickup:
			slots[slot], tr.carried = applyPickupSlot(slots[slot], tr.carried, button)

		case ContainerInputQuickMove:
			if slot == StonecutterSlotInput {
				slots[slot] = mergeIntoSlotSlice(slots, slots[slot], 29, 38)
				slots[slot] = mergeIntoSlotSlice(slots, slots[slot], 2, 29)
			} else if slot >= 2 {
				clicked := slots[slot]
				if DefaultStonecutterBook().CanStonecut(clicked) {
					slots[slot] = mergeIntoSlotSlice(slots, clicked, StonecutterSlotInput, StonecutterSlotInput+1)
				} else if slot >= 2 && slot < 29 {
					slots[slot] = mergeIntoSlotSlice(slots, clicked, 29, 38)
				} else if slot >= 29 && slot < 38 {
					slots[slot] = mergeIntoSlotSlice(slots, clicked, 2, 29)
				}
			}

		case ContainerInputSwap:
			if button >= 0 && button < 9 {
				hotbarIdx := 29 + int(button)
				slots[slot], slots[hotbarIdx] = slots[hotbarIdx], slots[slot]
			}
		}
	}

	// Write back player inventory slots (2..28 -> 9..35, 29..37 -> 36..44).
	for i := 0; i < 27; i++ {
		session.SetInventorySlot(9+i, slots[2+i])
	}
	for i := 0; i < 9; i++ {
		session.SetInventorySlot(36+i, slots[29+i])
	}

	newInput := slots[StonecutterSlotInput]
	tr.open.Crafting[StonecutterSlotInput] = newInput

	// If the input item type changed or became empty, reset selected recipe to -1.
	if newInput.IsEmpty() || newInput.ResolvedItemID() != prevInput.ResolvedItemID() {
		tr.open.AnvilCost = -1
		tr.open.Result = world.ItemStack{}
	} else {
		nextRes, _, nextValidIdx, ok := EvaluateStonecutterResult(newInput, int(tr.open.AnvilCost))
		if ok {
			tr.open.Result = nextRes
			tr.open.AnvilCost = int16(nextValidIdx)
		} else {
			tr.open.Result = world.ItemStack{}
			tr.open.AnvilCost = -1
		}
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
			EncodeSoundPacket("minecraft:ui.stonecutter.take_result", SoundSourceBlocks, fx, fy, fz, 1.0, 1.0, 0))
		if !craftedStack.IsEmpty() {
			s.TriggerRecipeCrafted(session, craftedStack)
		}
	}

	return s.syncStonecutterContainer(session)
}

func (s *Server) buildStonecutterMenuSlots(session *PlayerSession, input, result world.ItemStack) []world.ItemStack {
	inv := session.Inventory()
	slots := make([]world.ItemStack, StonecutterMenuSlotCount)
	slots[StonecutterSlotInput] = input
	slots[StonecutterSlotResult] = result
	copy(slots[2:29], inv[9:36])
	copy(slots[29:38], inv[36:45])
	return slots
}

func (s *Server) syncStonecutterContainer(session *PlayerSession) error {
	if s == nil || session == nil || session.send == nil {
		return nil
	}
	tr := getContainerTracker(session)
	tr.mu.Lock()
	if tr.open == nil || tr.open.MenuType != MenuTypeStonecutter {
		tr.mu.Unlock()
		return nil
	}
	cid := tr.open.ContainerID
	input := tr.open.Crafting[StonecutterSlotInput]
	result := tr.open.Result
	selectedIdx := tr.open.AnvilCost
	carried := tr.carried
	tr.mu.Unlock()

	slots := s.buildStonecutterMenuSlots(session, input, result)
	stateID := session.NextContainerStateID()
	if err := session.send(protocol.PlayContainerSetContent, EncodeContainerContent(cid, stateID, slots, carried)); err != nil {
		return err
	}
	return session.send(protocol.PlayContainerSetData, EncodeContainerSetData(cid, StonecutterDataSelectedRecipe, selectedIdx))
}
