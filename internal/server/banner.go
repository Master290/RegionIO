package server

import (
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"regionio/internal/nbt"
	"regionio/internal/protocol"
	"regionio/internal/registry"
	"regionio/internal/world"
)

// Protocol 775 (Minecraft 26.1.2) registry & data component constants for banners.
const (
	// BlockEntityTypeBanner is BuiltInRegistries.BLOCK_ENTITY_TYPE index 20 ("minecraft:banner").
	BlockEntityTypeBanner int32 = 20

	// DataComponentBannerPatterns is BuiltInRegistries.DATA_COMPONENT_TYPE index 80 ("minecraft:banner_patterns").
	DataComponentBannerPatterns int32 = 80
	// DataComponentBaseColor is BuiltInRegistries.DATA_COMPONENT_TYPE index 81 ("minecraft:base_color").
	DataComponentBaseColor int32 = 81

	// MaxLoomBannerLayers is the maximum number of pattern layers a Loom allows on a banner (6).
	MaxLoomBannerLayers = 6
	// MaxBannerPatternLayers is the hard cap on decoded banner pattern layers (16).
	MaxBannerPatternLayers = 16
)

// VanillaBannerColors lists all 16 DyeColor names in exact vanilla ordinal order (0..15).
var VanillaBannerColors = [16]string{
	"white",      // 0
	"orange",     // 1
	"magenta",    // 2
	"light_blue", // 3
	"yellow",     // 4
	"lime",       // 5
	"pink",       // 6
	"gray",       // 7
	"light_gray", // 8
	"cyan",       // 9
	"purple",     // 10
	"blue",       // 11
	"brown",      // 12
	"green",      // 13
	"red",        // 14
	"black",      // 15
}

// NoItemRequiredBannerPatterns lists the vanilla 26.1.2 banner patterns in the
// `#minecraft:pattern_item/no_item_required` tag selectable in a Loom without a pattern item.
var NoItemRequiredBannerPatterns = []string{
	"minecraft:square_bottom_left",
	"minecraft:square_bottom_right",
	"minecraft:square_top_left",
	"minecraft:square_top_right",
	"minecraft:stripe_bottom",
	"minecraft:stripe_top",
	"minecraft:stripe_left",
	"minecraft:stripe_right",
	"minecraft:stripe_center",
	"minecraft:stripe_middle",
	"minecraft:stripe_downright",
	"minecraft:stripe_downleft",
	"minecraft:small_stripes",
	"minecraft:cross",
	"minecraft:straight_cross",
	"minecraft:triangle_bottom",
	"minecraft:triangle_top",
	"minecraft:triangles_bottom",
	"minecraft:triangles_top",
	"minecraft:diagonal_left",
	"minecraft:diagonal_up_right",
	"minecraft:diagonal_up_left",
	"minecraft:diagonal_right",
	"minecraft:circle",
	"minecraft:rhombus",
	"minecraft:half_vertical",
	"minecraft:half_horizontal",
	"minecraft:half_vertical_right",
	"minecraft:half_horizontal_bottom",
	"minecraft:border",
	"minecraft:curly_border",
	"minecraft:gradient",
	"minecraft:gradient_up",
	"minecraft:bricks",
}

// bannerPatternItemMap maps canonical `*_banner_pattern` item IDs to the
// canonical `minecraft:banner_pattern` registry entry they unlock in the Loom.
var bannerPatternItemMap = map[string]string{
	"minecraft:flower_banner_pattern":           "minecraft:flower",
	"minecraft:creeper_banner_pattern":          "minecraft:creeper",
	"minecraft:skull_banner_pattern":            "minecraft:skull",
	"minecraft:mojang_banner_pattern":           "minecraft:mojang",
	"minecraft:globe_banner_pattern":            "minecraft:globe",
	"minecraft:piglin_banner_pattern":           "minecraft:piglin",
	"minecraft:flow_banner_pattern":             "minecraft:flow",
	"minecraft:guster_banner_pattern":           "minecraft:guster",
	"minecraft:field_masoned_banner_pattern":    "minecraft:bricks",
	"minecraft:bordure_indented_banner_pattern": "minecraft:curly_border",
}

// BannerDyeColorOrdinal returns the 0..15 DyeColor ordinal for a color name.
func BannerDyeColorOrdinal(color string) (int32, bool) {
	c := strings.ToLower(strings.TrimSpace(color))
	for i, name := range VanillaBannerColors {
		if name == c {
			return int32(i), true
		}
	}
	return 0, false
}

// BannerDyeColorFromOrdinal returns the color name for a 0..15 DyeColor ordinal.
func BannerDyeColorFromOrdinal(ordinal int32) (string, bool) {
	if ordinal < 0 || int(ordinal) >= len(VanillaBannerColors) {
		return "", false
	}
	return VanillaBannerColors[ordinal], true
}

// CanonicalBannerPatternID normalizes a banner pattern identifier and verifies
// it exists in the synchronized 26.1.2 `minecraft:banner_pattern` registry.
func CanonicalBannerPatternID(pattern string) (string, int32, bool) {
	p := strings.ToLower(strings.TrimSpace(pattern))
	if p == "" {
		return "", -1, false
	}
	if !strings.Contains(p, ":") {
		p = "minecraft:" + p
	}
	idx := registry.Index("minecraft:banner_pattern", p)
	if idx < 0 {
		return "", -1, false
	}
	return p, int32(idx), true
}

// BannerPatternByRegistryIndex looks up the canonical `minecraft:banner_pattern`
// identifier at 0-based index idx in the synchronized registry.
func BannerPatternByRegistryIndex(idx int32) (string, bool) {
	if idx < 0 {
		return "", false
	}
	for _, reg := range registry.Synced() {
		if reg.Name == "minecraft:banner_pattern" {
			if int(idx) < len(reg.Entries) {
				return reg.Entries[idx], true
			}
			return "", false
		}
	}
	return "", false
}

// IsBannerPatternItem reports whether itemName is a `*_banner_pattern` item
// and returns the canonical `minecraft:banner_pattern` ID it unlocks.
func IsBannerPatternItem(itemName string) (string, bool) {
	canon := canonicalItemName(itemName)
	pat, ok := bannerPatternItemMap[canon]
	return pat, ok
}

// IsBannerItem reports whether itemName is one of the 16 `minecraft:<color>_banner` items.
func IsBannerItem(itemName string) bool {
	_, ok := BannerColorFromItem(itemName)
	return ok
}

// BannerColorFromItem extracts the dye color ("white".."black") from a banner item ID.
func BannerColorFromItem(itemName string) (string, bool) {
	short := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(itemName)), "minecraft:")
	if !strings.HasSuffix(short, "_banner") || strings.HasSuffix(short, "_wall_banner") {
		return "", false
	}
	col := strings.TrimSuffix(short, "_banner")
	if _, ok := BannerDyeColorOrdinal(col); ok {
		return col, true
	}
	return "", false
}

// BannerItemFromColor returns the canonical `minecraft:<color>_banner` item ID.
func BannerItemFromColor(color string) string {
	col := strings.ToLower(strings.TrimSpace(color))
	if _, ok := BannerDyeColorOrdinal(col); !ok {
		col = "white"
	}
	return "minecraft:" + col + "_banner"
}

// IsStandingBannerBlock reports whether state is a standing `minecraft:<color>_banner` block.
func IsStandingBannerBlock(state uint16) bool {
	name := strings.TrimPrefix(world.BlockName(state), "minecraft:")
	if !strings.HasSuffix(name, "_banner") || strings.HasSuffix(name, "_wall_banner") {
		return false
	}
	_, ok := BannerDyeColorOrdinal(strings.TrimSuffix(name, "_banner"))
	return ok
}

// IsWallBannerBlock reports whether state is a `minecraft:<color>_wall_banner` block.
func IsWallBannerBlock(state uint16) bool {
	name := strings.TrimPrefix(world.BlockName(state), "minecraft:")
	if !strings.HasSuffix(name, "_wall_banner") {
		return false
	}
	_, ok := BannerDyeColorOrdinal(strings.TrimSuffix(name, "_wall_banner"))
	return ok
}

// IsBannerBlock reports whether state is a standing or wall banner block.
func IsBannerBlock(state uint16) bool {
	return IsStandingBannerBlock(state) || IsWallBannerBlock(state)
}

// BannerColorFromBlockState extracts the dye color ("white".."black") from a
// standing or wall banner block state.
func BannerColorFromBlockState(state uint16) (string, bool) {
	name := strings.TrimPrefix(world.BlockName(state), "minecraft:")
	switch {
	case strings.HasSuffix(name, "_wall_banner"):
		col := strings.TrimSuffix(name, "_wall_banner")
		if _, ok := BannerDyeColorOrdinal(col); ok {
			return col, true
		}
	case strings.HasSuffix(name, "_banner"):
		col := strings.TrimSuffix(name, "_banner")
		if _, ok := BannerDyeColorOrdinal(col); ok {
			return col, true
		}
	}
	return "", false
}

// ComputeStandingBannerState computes the standing `minecraft:<color>_banner`
// block state with rotation 0..15 facing the placing player (`playerYaw`).
func ComputeStandingBannerState(color string, playerYaw float32) (uint16, bool) {
	if _, ok := BannerDyeColorOrdinal(color); !ok {
		return world.StateAir, false
	}
	blockName := "minecraft:" + strings.ToLower(strings.TrimSpace(color)) + "_banner"
	rot := int(math.Floor(float64((playerYaw+180.0)*16.0/360.0)+0.5)) & 15
	if st, ok := world.BlockStateByName(blockName, map[string]string{"rotation": strconv.Itoa(rot)}); ok && st != world.StateAir {
		return st, true
	}
	base := blockBaseStateByName(blockName)
	if base == world.StateAir {
		return world.StateAir, false
	}
	cand := base + uint16(rot)
	if world.BlockName(cand) == blockName {
		return cand, true
	}
	return base, true
}

// ComputeWallBannerState computes the `minecraft:<color>_wall_banner` block state
// for a horizontal block face (2=north, 3=south, 4=west, 5=east).
func ComputeWallBannerState(color string, clickedFace int32) (uint16, bool) {
	if _, ok := BannerDyeColorOrdinal(color); !ok {
		return world.StateAir, false
	}
	var facing string
	switch clickedFace {
	case 2:
		facing = "north"
	case 3:
		facing = "south"
	case 4:
		facing = "west"
	case 5:
		facing = "east"
	default:
		return world.StateAir, false
	}
	blockName := "minecraft:" + strings.ToLower(strings.TrimSpace(color)) + "_wall_banner"
	if st, ok := world.BlockStateByName(blockName, map[string]string{"facing": facing}); ok && st != world.StateAir {
		return st, true
	}
	base := blockBaseStateByName(blockName)
	if base == world.StateAir {
		return world.StateAir, false
	}
	offset := uint16(clickedFace - 2)
	cand := base + offset
	if world.BlockName(cand) == blockName {
		return cand, true
	}
	return base, true
}

// BannerPlacementState returns the block state to place when using a banner item
// on clickedFace (1=top -> standing banner with rotation from playerYaw;
// 2..5 -> wall banner facing north/south/west/east; 0=bottom -> invalid).
func BannerPlacementState(itemName string, clickedFace int32, playerYaw float32) (uint16, bool) {
	color, ok := BannerColorFromItem(itemName)
	if !ok {
		return world.StateAir, false
	}
	switch {
	case clickedFace == 1:
		return ComputeStandingBannerState(color, playerYaw)
	case clickedFace >= 2 && clickedFace <= 5:
		return ComputeWallBannerState(color, clickedFace)
	default:
		return world.StateAir, false
	}
}

// --- BannerPatternLayer & BannerPatternsComponent ---

// BannerPatternLayer represents a single pattern + dye color layer on a banner or shield.
type BannerPatternLayer struct {
	Pattern string // Canonical pattern ID, e.g. "minecraft:creeper", "minecraft:stripe_bottom"
	Color   string // Canonical DyeColor name, e.g. "red", "white", "black"
}

// BannerPatternsComponent represents the `minecraft:banner_patterns` data component (ID 80).
type BannerPatternsComponent struct {
	Layers []BannerPatternLayer
}

// Clone returns a deep copy of c.
func (c BannerPatternsComponent) Clone() BannerPatternsComponent {
	if len(c.Layers) == 0 {
		return BannerPatternsComponent{}
	}
	out := make([]BannerPatternLayer, len(c.Layers))
	copy(out, c.Layers)
	return BannerPatternsComponent{Layers: out}
}

// IsEmpty reports whether c has no pattern layers.
func (c BannerPatternsComponent) IsEmpty() bool {
	return len(c.Layers) == 0
}

// WithAddedLayer returns a copy of c with (pattern, color) appended.
func (c BannerPatternsComponent) WithAddedLayer(pattern, color string) BannerPatternsComponent {
	cp := c.Clone()
	canonPat, _, ok := CanonicalBannerPatternID(pattern)
	if !ok {
		canonPat = canonicalItemName(pattern)
	}
	col := strings.ToLower(strings.TrimSpace(color))
	if _, ok := BannerDyeColorOrdinal(col); !ok {
		col = "white"
	}
	cp.Layers = append(cp.Layers, BannerPatternLayer{
		Pattern: canonPat,
		Color:   col,
	})
	return cp
}

// WithoutTopLayer returns a copy of c with its last (topmost) layer removed,
// as performed when washing a banner in a water cauldron.
func (c BannerPatternsComponent) WithoutTopLayer() (BannerPatternsComponent, bool) {
	if len(c.Layers) == 0 {
		return BannerPatternsComponent{}, false
	}
	if len(c.Layers) == 1 {
		return BannerPatternsComponent{}, true
	}
	out := make([]BannerPatternLayer, len(c.Layers)-1)
	copy(out, c.Layers[:len(c.Layers)-1])
	return BannerPatternsComponent{Layers: out}, true
}

// EncodeBannerPatternsComponent writes the protocol 775 `BannerPatternLayers.STREAM_CODEC`
// payload for `DataComponentBannerPatterns` (80):
//   - VarInt(layerCount)
//   - For each layer:
//   - Holder<BannerPattern>: VarInt(registryIndex + 1)
//   - DyeColor: VarInt(colorOrdinal)
func EncodeBannerPatternsComponent(w *protocol.Writer, c BannerPatternsComponent) {
	layers := c.Layers
	if len(layers) > MaxBannerPatternLayers {
		layers = layers[:MaxBannerPatternLayers]
	}
	w.VarInt(int32(len(layers)))
	for _, l := range layers {
		_, regIdx, ok := CanonicalBannerPatternID(l.Pattern)
		if !ok {
			regIdx = 0
		}
		w.VarInt(regIdx + 1) // Holder reference ID = registryIndex + 1
		colOrd, ok := BannerDyeColorOrdinal(l.Color)
		if !ok {
			colOrd = 0
		}
		w.VarInt(colOrd)
	}
}

// DecodeBannerPatternsComponent reads a `BannerPatternLayers.STREAM_CODEC` payload.
func DecodeBannerPatternsComponent(r *protocol.Reader) (BannerPatternsComponent, error) {
	count, err := r.VarInt()
	if err != nil {
		return BannerPatternsComponent{}, err
	}
	if count < 0 || count > MaxBannerPatternLayers {
		return BannerPatternsComponent{}, fmt.Errorf("server: banner pattern layer count %d out of range [0..%d]", count, MaxBannerPatternLayers)
	}
	layers := make([]BannerPatternLayer, count)
	for i := int32(0); i < count; i++ {
		holderID, err := r.VarInt()
		if err != nil {
			return BannerPatternsComponent{}, err
		}
		var patID string
		if holderID == 0 {
			// Inline BannerPattern(assetId, translationKey)
			assetID, err := r.String()
			if err != nil {
				return BannerPatternsComponent{}, err
			}
			if _, err := r.String(); err != nil {
				return BannerPatternsComponent{}, err
			}
			patID = canonicalItemName(assetID)
		} else {
			var ok bool
			patID, ok = BannerPatternByRegistryIndex(holderID - 1)
			if !ok {
				return BannerPatternsComponent{}, fmt.Errorf("server: unknown banner pattern holder %d", holderID)
			}
		}
		colOrd, err := r.VarInt()
		if err != nil {
			return BannerPatternsComponent{}, err
		}
		colName, ok := BannerDyeColorFromOrdinal(colOrd)
		if !ok {
			return BannerPatternsComponent{}, fmt.Errorf("server: invalid banner dye color ordinal %d", colOrd)
		}
		layers[i] = BannerPatternLayer{Pattern: patID, Color: colName}
	}
	return BannerPatternsComponent{Layers: layers}, nil
}

// EncodeSlotWithBannerPatterns writes a protocol 775 ItemStack slot with optional
// `minecraft:banner_patterns` (80) and `minecraft:base_color` (81) data components.
func EncodeSlotWithBannerPatterns(w *protocol.Writer, stack world.ItemStack, patterns BannerPatternsComponent, baseColor string) {
	itemID := stack.ResolvedItemID()
	if stack.IsEmpty() || itemID <= 0 {
		w.VarInt(0)
		return
	}
	w.VarInt(stack.Count)
	w.VarInt(itemID)

	hasPatterns := !patterns.IsEmpty()
	colOrd, hasBaseColor := BannerDyeColorOrdinal(baseColor)
	addedCount := int32(0)
	if hasPatterns {
		addedCount++
	}
	if hasBaseColor {
		addedCount++
	}
	w.VarInt(addedCount)
	w.VarInt(0) // 0 removed components
	if hasPatterns {
		w.VarInt(DataComponentBannerPatterns)
		EncodeBannerPatternsComponent(w, patterns)
	}
	if hasBaseColor {
		w.VarInt(DataComponentBaseColor)
		w.VarInt(colOrd)
	}
}

func encodeContainerSetSlotWithBanner(containerID, stateID int32, slot int, stack world.ItemStack, patterns BannerPatternsComponent, baseColor string) []byte {
	w := protocol.NewWriter(48)
	w.VarInt(containerID)
	w.VarInt(stateID)
	w.Uint16(uint16(slot))
	EncodeSlotWithBannerPatterns(w, stack, patterns, baseColor)
	return w.Bytes()
}

// ToNBT serializes BannerPatternsComponent into the vanilla 26.1.2 `patterns` NBT list.
func (c BannerPatternsComponent) ToNBT() nbt.List {
	if len(c.Layers) == 0 {
		return nbt.List{ElemID: nbt.TagEnd}
	}
	elems := make([]nbt.Tag, 0, len(c.Layers))
	for _, l := range c.Layers {
		patID, _, ok := CanonicalBannerPatternID(l.Pattern)
		if !ok {
			patID = canonicalItemName(l.Pattern)
		}
		col := strings.ToLower(strings.TrimSpace(l.Color))
		if _, ok := BannerDyeColorOrdinal(col); !ok {
			col = "white"
		}
		lc := nbt.NewCompound()
		lc.Set("pattern", nbt.String(patID))
		lc.Set("color", nbt.String(col))
		elems = append(elems, lc)
	}
	return nbt.List{ElemID: nbt.TagCompound, Elems: elems}
}

// BannerPatternsFromNBT decodes a BannerPatternsComponent from a `patterns` NBT list.
func BannerPatternsFromNBT(list nbt.List) BannerPatternsComponent {
	if len(list.Elems) == 0 {
		return BannerPatternsComponent{}
	}
	var layers []BannerPatternLayer
	for _, elem := range list.Elems {
		lc, ok := elem.(*nbt.Compound)
		if !ok || lc == nil {
			continue
		}
		pat, okPat := nbtStringTag(lc, "pattern")
		col, okCol := nbtStringTag(lc, "color")
		if !okPat || !okCol {
			continue
		}
		canonPat, _, validPat := CanonicalBannerPatternID(pat)
		if !validPat {
			canonPat = canonicalItemName(pat)
		}
		colLower := strings.ToLower(strings.TrimSpace(col))
		if _, validCol := BannerDyeColorOrdinal(colLower); !validCol {
			continue
		}
		layers = append(layers, BannerPatternLayer{Pattern: canonPat, Color: colLower})
		if len(layers) >= MaxBannerPatternLayers {
			break
		}
	}
	return BannerPatternsComponent{Layers: layers}
}

// --- Per-Player Slot Banner & Shield Metadata Tracker ---

type sessionBannerMetaTracker struct {
	mu               sync.Mutex
	slotPatterns     [world.PlayerInventorySlots]BannerPatternsComponent
	slotShieldColors [world.PlayerInventorySlots]string
	carriedPatterns  BannerPatternsComponent
	carriedColor     string
	loomPatterns     BannerPatternsComponent
	loomResult       BannerPatternsComponent
}

var playerBannerMetas sync.Map // map[*PlayerSession]*sessionBannerMetaTracker

func getBannerMetaTracker(session *PlayerSession) *sessionBannerMetaTracker {
	if session == nil {
		return nil
	}
	if v, ok := playerBannerMetas.Load(session); ok {
		return v.(*sessionBannerMetaTracker)
	}
	actual, _ := playerBannerMetas.LoadOrStore(session, &sessionBannerMetaTracker{})
	return actual.(*sessionBannerMetaTracker)
}

// GetSlotBannerPatterns returns the BannerPatternsComponent stored on player inventory slot (0..45).
func GetSlotBannerPatterns(session *PlayerSession, slot int) BannerPatternsComponent {
	if session == nil || slot < 0 || slot >= world.PlayerInventorySlots {
		return BannerPatternsComponent{}
	}
	tr := getBannerMetaTracker(session)
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return tr.slotPatterns[slot].Clone()
}

// SetSlotBannerPatterns sets the BannerPatternsComponent on player inventory slot (0..45).
func SetSlotBannerPatterns(session *PlayerSession, slot int, patterns BannerPatternsComponent) {
	if session == nil || slot < 0 || slot >= world.PlayerInventorySlots {
		return
	}
	tr := getBannerMetaTracker(session)
	tr.mu.Lock()
	tr.slotPatterns[slot] = patterns.Clone()
	tr.mu.Unlock()
}

// GetSlotShieldBaseColor returns the banner base color applied to a shield in slot (0..45).
func GetSlotShieldBaseColor(session *PlayerSession, slot int) string {
	if session == nil || slot < 0 || slot >= world.PlayerInventorySlots {
		return ""
	}
	tr := getBannerMetaTracker(session)
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return tr.slotShieldColors[slot]
}

// SetSlotShieldBaseColor sets the banner base color applied to a shield in slot (0..45).
func SetSlotShieldBaseColor(session *PlayerSession, slot int, baseColor string) {
	if session == nil || slot < 0 || slot >= world.PlayerInventorySlots {
		return
	}
	tr := getBannerMetaTracker(session)
	tr.mu.Lock()
	tr.slotShieldColors[slot] = strings.ToLower(strings.TrimSpace(baseColor))
	tr.mu.Unlock()
}

// ClearSlotBannerMeta clears banner patterns and shield base color on slot (0..45).
func ClearSlotBannerMeta(session *PlayerSession, slot int) {
	if session == nil || slot < 0 || slot >= world.PlayerInventorySlots {
		return
	}
	tr := getBannerMetaTracker(session)
	tr.mu.Lock()
	tr.slotPatterns[slot] = BannerPatternsComponent{}
	tr.slotShieldColors[slot] = ""
	tr.mu.Unlock()
}

// MoveSlotBannerMeta moves banner metadata from fromSlot to toSlot.
func MoveSlotBannerMeta(session *PlayerSession, fromSlot, toSlot int) {
	if session == nil || fromSlot == toSlot ||
		fromSlot < 0 || fromSlot >= world.PlayerInventorySlots ||
		toSlot < 0 || toSlot >= world.PlayerInventorySlots {
		return
	}
	tr := getBannerMetaTracker(session)
	tr.mu.Lock()
	tr.slotPatterns[toSlot] = tr.slotPatterns[fromSlot]
	tr.slotShieldColors[toSlot] = tr.slotShieldColors[fromSlot]
	tr.slotPatterns[fromSlot] = BannerPatternsComponent{}
	tr.slotShieldColors[fromSlot] = ""
	tr.mu.Unlock()
}

// SwapSlotBannerMetas swaps banner metadata between slotA and slotB.
func SwapSlotBannerMetas(session *PlayerSession, slotA, slotB int) {
	if session == nil || slotA == slotB ||
		slotA < 0 || slotA >= world.PlayerInventorySlots ||
		slotB < 0 || slotB >= world.PlayerInventorySlots {
		return
	}
	tr := getBannerMetaTracker(session)
	tr.mu.Lock()
	tr.slotPatterns[slotA], tr.slotPatterns[slotB] = tr.slotPatterns[slotB], tr.slotPatterns[slotA]
	tr.slotShieldColors[slotA], tr.slotShieldColors[slotB] = tr.slotShieldColors[slotB], tr.slotShieldColors[slotA]
	tr.mu.Unlock()
}

// --- BannerBlockEntity & Server Banner Store ---

// BannerBlockEntity represents a placed `minecraft:banner` block entity.
type BannerBlockEntity struct {
	Dimension  string
	X          int
	Y          int
	Z          int
	BaseColor  string
	Patterns   BannerPatternsComponent
	CustomName *ChatComponent
}

// Clone returns a deep copy of b.
func (b *BannerBlockEntity) Clone() *BannerBlockEntity {
	if b == nil {
		return nil
	}
	cp := &BannerBlockEntity{
		Dimension: b.Dimension,
		X:         b.X,
		Y:         b.Y,
		Z:         b.Z,
		BaseColor: b.BaseColor,
		Patterns:  b.Patterns.Clone(),
	}
	if b.CustomName != nil {
		cn := *b.CustomName
		cp.CustomName = &cn
	}
	return cp
}

// ToNBT serializes BannerBlockEntity into a vanilla 26.1.2 `minecraft:banner` block entity compound.
func (b *BannerBlockEntity) ToNBT() *nbt.Compound {
	comp := nbt.NewCompound()
	if b == nil {
		return comp
	}
	comp.Set("id", nbt.String("minecraft:banner"))
	comp.Set("x", nbt.Int(int32(b.X)))
	comp.Set("y", nbt.Int(int32(b.Y)))
	comp.Set("z", nbt.Int(int32(b.Z)))
	if b.Dimension != "" {
		comp.Set("dimension", nbt.String(world.NormalizeDimension(b.Dimension)))
	}
	if b.BaseColor != "" {
		comp.Set("base_color", nbt.String(b.BaseColor))
	}
	if !b.Patterns.IsEmpty() {
		comp.Set("patterns", b.Patterns.ToNBT())
	}
	if b.CustomName != nil && !b.CustomName.IsEmpty() {
		comp.Set("CustomName", nbt.String(b.CustomName.ToJSON()))
	}
	return comp
}

// BannerBlockEntityFromNBT decodes a BannerBlockEntity from an NBT compound.
func BannerBlockEntityFromNBT(comp *nbt.Compound) (*BannerBlockEntity, error) {
	if comp == nil {
		return nil, errors.New("server: nil banner block entity NBT")
	}
	id, _ := nbtStringTag(comp, "id")
	if id != "" && id != "minecraft:banner" {
		return nil, fmt.Errorf("server: unexpected banner block entity id %q", id)
	}
	x, _ := nbtIntTag(comp, "x")
	y, _ := nbtIntTag(comp, "y")
	z, _ := nbtIntTag(comp, "z")
	dim, hasDim := nbtStringTag(comp, "dimension")
	if !hasDim || dim == "" {
		dim = world.DimensionOverworld
	}
	baseCol, _ := nbtStringTag(comp, "base_color")
	if _, ok := BannerDyeColorOrdinal(baseCol); !ok {
		baseCol = "white"
	}
	out := &BannerBlockEntity{
		Dimension: world.NormalizeDimension(dim),
		X:         int(x),
		Y:         int(y),
		Z:         int(z),
		BaseColor: baseCol,
	}
	if patList, ok := nbtListTag(comp, "patterns"); ok {
		out.Patterns = BannerPatternsFromNBT(patList)
	}
	if cnRaw, ok := nbtStringTag(comp, "CustomName"); ok && cnRaw != "" {
		cn := ParseChatComponentOrText(cnRaw)
		out.CustomName = &cn
	}
	return out, nil
}

// EncodeBannerBlockEntityPacket encodes ClientboundBlockEntityDataPacket (0x06)
// for a BannerBlockEntity (BlockEntityTypeBanner = 20).
func EncodeBannerBlockEntityPacket(b *BannerBlockEntity) []byte {
	if b == nil {
		return nil
	}
	return EncodeBlockEntityDataPacket(b.X, b.Y, b.Z, BlockEntityTypeBanner, b.ToNBT())
}

type serverBannerStore struct {
	mu      sync.RWMutex
	banners map[blockPosDimKey]*BannerBlockEntity
}

var bannerStores sync.Map // map[*Server]*serverBannerStore

func (s *Server) bannerStore() *serverBannerStore {
	if s == nil {
		return &serverBannerStore{banners: make(map[blockPosDimKey]*BannerBlockEntity)}
	}
	if v, ok := bannerStores.Load(s); ok {
		return v.(*serverBannerStore)
	}
	st := &serverBannerStore{banners: make(map[blockPosDimKey]*BannerBlockEntity)}
	actual, _ := bannerStores.LoadOrStore(s, st)
	return actual.(*serverBannerStore)
}

// GetBannerAt returns a copy of the BannerBlockEntity at (dimension, x, y, z), if present.
func (s *Server) GetBannerAt(dimension string, x, y, z int) (*BannerBlockEntity, bool) {
	if s == nil {
		return nil, false
	}
	key := blockPosDimKey{dim: world.NormalizeDimension(dimension), x: x, y: y, z: z}
	st := s.bannerStore()
	st.mu.RLock()
	b := st.banners[key].Clone()
	st.mu.RUnlock()
	if b == nil {
		return nil, false
	}
	return b, true
}

// SetBannerAt stores or updates the BannerBlockEntity at (b.Dimension, b.X, b.Y, b.Z)
// and broadcasts its BlockEntityData packet to nearby players.
func (s *Server) SetBannerAt(b *BannerBlockEntity) {
	if s == nil || b == nil {
		return
	}
	cp := b.Clone()
	cp.Dimension = world.NormalizeDimension(cp.Dimension)
	if _, ok := BannerDyeColorOrdinal(cp.BaseColor); !ok {
		cp.BaseColor = "white"
	}
	key := blockPosDimKey{dim: cp.Dimension, x: cp.X, y: cp.Y, z: cp.Z}
	st := s.bannerStore()
	st.mu.Lock()
	st.banners[key] = cp
	st.mu.Unlock()

	pkt := EncodeBannerBlockEntityPacket(cp)
	cx := int32(cp.X >> 4)
	cz := int32(cp.Z >> 4)
	s.BroadcastDimensionChunk(cp.Dimension, cx, cz, PlayBlockEntityDataCB, pkt)
}

// RemoveBannerAt removes any BannerBlockEntity at (dimension, x, y, z) and returns
// the dropped banner ItemStack and its BannerPatternsComponent.
func (s *Server) RemoveBannerAt(dimension string, x, y, z int) (world.ItemStack, BannerPatternsComponent, bool) {
	if s == nil {
		return world.ItemStack{}, BannerPatternsComponent{}, false
	}
	dim := world.NormalizeDimension(dimension)
	key := blockPosDimKey{dim: dim, x: x, y: y, z: z}
	st := s.bannerStore()
	st.mu.Lock()
	b, exists := st.banners[key]
	if exists {
		delete(st.banners, key)
	}
	st.mu.Unlock()

	baseColor := ""
	var patterns BannerPatternsComponent
	if b != nil {
		baseColor = b.BaseColor
		patterns = b.Patterns.Clone()
	}
	if baseColor == "" {
		if cache := s.chunksForDimension(dim); cache != nil {
			if col, ok := BannerColorFromBlockState(cache.GetBlock(x, y, z)); ok {
				baseColor = col
			}
		}
	}
	if baseColor == "" && !exists {
		return world.ItemStack{}, BannerPatternsComponent{}, false
	}
	return world.NewNamedItemStack(BannerItemFromColor(baseColor), 1), patterns, true
}

// SendBannerBlockEntityPacket sends the BlockEntityData (0x06) packet for the banner
// at (x, y, z) to session if present.
func (s *Server) SendBannerBlockEntityPacket(session *PlayerSession, x, y, z int) bool {
	if s == nil || session == nil || session.send == nil {
		return false
	}
	snap := session.Snapshot()
	b, ok := s.GetBannerAt(snap.Dimension, x, y, z)
	if !ok || b == nil {
		return false
	}
	_ = session.send(PlayBlockEntityDataCB, EncodeBannerBlockEntityPacket(b))
	return true
}

// OnBannerPlaced creates and broadcasts the BannerBlockEntity when session places
// a banner block at (x, y, z) from inventory slot `slot`.
func (s *Server) OnBannerPlaced(session *PlayerSession, slot int, x, y, z int) bool {
	if s == nil || session == nil {
		return false
	}
	snap := session.Snapshot()
	dim := world.NormalizeDimension(snap.Dimension)

	baseColor := "white"
	if cache := s.chunksForDimension(dim); cache != nil {
		if col, ok := BannerColorFromBlockState(cache.GetBlock(x, y, z)); ok {
			baseColor = col
		}
	}
	if slot >= 0 && slot < world.PlayerInventorySlots {
		if col, ok := BannerColorFromItem(session.InventorySlot(slot).ResolvedID()); ok {
			baseColor = col
		}
	}

	patterns := GetSlotBannerPatterns(session, slot)
	meta := GetSlotItemMeta(session, slot)
	var customName *ChatComponent
	if meta.CustomName != "" {
		cn := TextComponent(meta.CustomName)
		customName = &cn
	}

	s.SetBannerAt(&BannerBlockEntity{
		Dimension:  dim,
		X:          x,
		Y:          y,
		Z:          z,
		BaseColor:  baseColor,
		Patterns:   patterns,
		CustomName: customName,
	})
	return true
}

// PlaceBannerBlock handles placing a banner from session's hand onto block face
// `clickedFace` so the banner occupies target block `(x, y, z)`, setting the
// standing or wall banner block state, creating the BannerBlockEntity with any
// `banner_patterns` component, and consuming 1 banner in survival mode.
func (s *Server) PlaceBannerBlock(session *PlayerSession, hand, clickedFace int32, x, y, z int) bool {
	if s == nil || session == nil || session.IsDead() {
		return false
	}
	slot, held := session.handSlotAndStack(hand)
	if held.IsEmpty() || !IsBannerItem(held.ResolvedID()) {
		return false
	}
	snap := session.Snapshot()
	state, ok := BannerPlacementState(held.ResolvedID(), clickedFace, snap.Yaw)
	if !ok {
		return false
	}
	dim := world.NormalizeDimension(snap.Dimension)
	cache := s.chunksForDimension(dim)
	if cache != nil {
		cur := cache.GetBlock(x, y, z)
		if cur != world.StateAir && cur != world.StateWater {
			return false
		}
		cache.SetBlock(x, y, z, state)
		s.broadcastBlockUpdateInDim(dim, x, y, z, state)
	}

	s.OnBannerPlaced(session, slot, x, y, z)

	if snap.GameMode != GameModeCreative {
		rem := held.Count - 1
		if rem <= 0 {
			session.SetInventorySlot(slot, world.ItemStack{})
			ClearSlotBannerMeta(session, slot)
			SetSlotItemMeta(session, slot, ItemMeta{})
		} else {
			session.SetInventorySlot(slot, world.NewItemStack(held.ResolvedItemID(), rem))
		}
		if session.send != nil {
			stateID := session.NextContainerStateID()
			_ = session.send(protocol.PlayContainerSetSlot, encodeContainerSetSlotWithBanner(0, stateID, slot, session.InventorySlot(slot), GetSlotBannerPatterns(session, slot), ""))
		}
	}
	return true
}

// --- Crafting Grid Recipes (Banner Cloning & Shield Decoration) and Cauldron Washing ---

// MatchBannerCloneRecipe ports vanilla `BannerDuplicateRecipe`:
//   - Requires exactly 2 banners of the same base color in the crafting grid
//   - One banner must have 1..6 pattern layers; the other must be blank (0 layers)
//   - Returns 1 copy of the patterned banner and the grid index of the original
//     patterned banner so it remains in the crafting grid as a remainder.
func MatchBannerCloneRecipe(grid []world.ItemStack, gridPatterns []BannerPatternsComponent) (resultStack world.ItemStack, resultPatterns BannerPatternsComponent, patternedSlot int, ok bool) {
	patternedSlot = -1
	blankSlot := -1
	var baseColor string

	for i, st := range grid {
		if st.IsEmpty() {
			continue
		}
		col, isBanner := BannerColorFromItem(st.ResolvedID())
		if !isBanner {
			return world.ItemStack{}, BannerPatternsComponent{}, -1, false
		}
		if baseColor == "" {
			baseColor = col
		} else if baseColor != col {
			return world.ItemStack{}, BannerPatternsComponent{}, -1, false
		}

		var pat BannerPatternsComponent
		if i < len(gridPatterns) {
			pat = gridPatterns[i]
		}
		if !pat.IsEmpty() {
			if patternedSlot >= 0 || len(pat.Layers) > MaxLoomBannerLayers {
				return world.ItemStack{}, BannerPatternsComponent{}, -1, false
			}
			patternedSlot = i
			resultPatterns = pat.Clone()
		} else {
			if blankSlot >= 0 {
				return world.ItemStack{}, BannerPatternsComponent{}, -1, false
			}
			blankSlot = i
		}
	}

	if patternedSlot < 0 || blankSlot < 0 {
		return world.ItemStack{}, BannerPatternsComponent{}, -1, false
	}
	return world.NewNamedItemStack(BannerItemFromColor(baseColor), 1), resultPatterns, patternedSlot, true
}

// MatchShieldDecorationRecipe ports vanilla `ShieldDecorationRecipe`:
//   - Requires exactly 1 `minecraft:shield` (with no existing banner patterns) and 1 `minecraft:<color>_banner`
//   - Produces 1 `minecraft:shield` preserving the shield's durability/enchantments (`ItemMeta`),
//     with `base_color` set to the banner's color and `banner_patterns` copied from the banner.
func MatchShieldDecorationRecipe(
	grid []world.ItemStack,
	gridMetas []ItemMeta,
	gridPatterns []BannerPatternsComponent,
	gridShieldColors []string,
) (resultStack world.ItemStack, resultMeta ItemMeta, baseColor string, resultPatterns BannerPatternsComponent, ok bool) {
	shieldSlot := -1
	bannerSlot := -1

	for i, st := range grid {
		if st.IsEmpty() {
			continue
		}
		switch {
		case st.ResolvedID() == "minecraft:shield":
			if shieldSlot >= 0 {
				return world.ItemStack{}, ItemMeta{}, "", BannerPatternsComponent{}, false
			}
			if i < len(gridPatterns) && !gridPatterns[i].IsEmpty() {
				return world.ItemStack{}, ItemMeta{}, "", BannerPatternsComponent{}, false
			}
			if i < len(gridShieldColors) && gridShieldColors[i] != "" {
				return world.ItemStack{}, ItemMeta{}, "", BannerPatternsComponent{}, false
			}
			shieldSlot = i
		case IsBannerItem(st.ResolvedID()):
			if bannerSlot >= 0 {
				return world.ItemStack{}, ItemMeta{}, "", BannerPatternsComponent{}, false
			}
			bannerSlot = i
		default:
			return world.ItemStack{}, ItemMeta{}, "", BannerPatternsComponent{}, false
		}
	}

	if shieldSlot < 0 || bannerSlot < 0 {
		return world.ItemStack{}, ItemMeta{}, "", BannerPatternsComponent{}, false
	}
	col, _ := BannerColorFromItem(grid[bannerSlot].ResolvedID())
	if shieldSlot < len(gridMetas) {
		resultMeta = gridMetas[shieldSlot].Clone()
	}
	if bannerSlot < len(gridPatterns) {
		resultPatterns = gridPatterns[bannerSlot].Clone()
	}
	return world.NewNamedItemStack("minecraft:shield", 1), resultMeta, col, resultPatterns, true
}

// CleanBannerInCauldron handles right-clicking a `minecraft:water_cauldron` at
// (x, y, z) while holding a patterned banner, removing the topmost pattern layer
// and lowering the cauldron water level by 1 (matching `LayeredCauldronBlock.CLEAN_BANNER`).
func (s *Server) CleanBannerInCauldron(session *PlayerSession, hand int32, x, y, z int) bool {
	if s == nil || session == nil || session.IsDead() {
		return false
	}
	slot, held := session.handSlotAndStack(hand)
	if held.IsEmpty() || !IsBannerItem(held.ResolvedID()) {
		return false
	}
	patterns := GetSlotBannerPatterns(session, slot)
	cleaned, ok := patterns.WithoutTopLayer()
	if !ok {
		return false
	}

	snap := session.Snapshot()
	dim := world.NormalizeDimension(snap.Dimension)
	cache := s.chunksForDimension(dim)
	if cache != nil {
		curState := cache.GetBlock(x, y, z)
		if world.BlockName(curState) != "minecraft:water_cauldron" {
			return false
		}
		newState := decrementWaterCauldronLevel(curState)
		cache.SetBlock(x, y, z, newState)
		s.broadcastBlockUpdateInDim(dim, x, y, z, newState)
	}

	if held.Count == 1 {
		SetSlotBannerPatterns(session, slot, cleaned)
		if session.send != nil {
			stateID := session.NextContainerStateID()
			_ = session.send(protocol.PlayContainerSetSlot, encodeContainerSetSlotWithBanner(0, stateID, slot, held, cleaned, ""))
		}
	} else {
		// Split 1 washed banner off the stack.
		if snap.GameMode != GameModeCreative {
			session.SetInventorySlot(slot, world.NewItemStack(held.ResolvedItemID(), held.Count-1))
			if session.send != nil {
				stateID := session.NextContainerStateID()
				_ = session.send(protocol.PlayContainerSetSlot, encodeContainerSetSlotWithBanner(0, stateID, slot, session.InventorySlot(slot), patterns, ""))
			}
		}
		s.giveBannerToPlayer(session, world.NewItemStack(held.ResolvedItemID(), 1), cleaned)
	}
	return true
}

func decrementWaterCauldronLevel(state uint16) uint16 {
	if _, props, ok := world.BlockStateProperties(state); ok {
		switch props["level"] {
		case "3":
			if st, ok := world.BlockStateByName("minecraft:water_cauldron", map[string]string{"level": "2"}); ok {
				return st
			}
		case "2":
			if st, ok := world.BlockStateByName("minecraft:water_cauldron", map[string]string{"level": "1"}); ok {
				return st
			}
		}
	}
	if emptyCauldron, ok := world.BlockStateByName("minecraft:cauldron", nil); ok && emptyCauldron != world.StateAir {
		return emptyCauldron
	}
	if emptyCauldron := blockBaseStateByName("minecraft:cauldron"); emptyCauldron != world.StateAir {
		return emptyCauldron
	}
	return state
}

func (s *Server) giveBannerToPlayer(session *PlayerSession, stack world.ItemStack, patterns BannerPatternsComponent) {
	if session == nil || stack.IsEmpty() {
		return
	}
	inv := session.Inventory()
	for _, r := range [][2]int{{36, 45}, {9, 36}} {
		for i := r[0]; i < r[1]; i++ {
			if inv[i].IsEmpty() {
				session.SetInventorySlot(i, stack)
				SetSlotBannerPatterns(session, i, patterns)
				if session.send != nil {
					stateID := session.NextContainerStateID()
					_ = session.send(protocol.PlayContainerSetSlot, encodeContainerSetSlotWithBanner(0, stateID, i, stack, patterns, ""))
				}
				return
			}
		}
	}
	snap := session.Snapshot()
	_, _ = s.SpawnItemEntityInDimension(snap.Dimension, snap.X, snap.Y+0.25, snap.Z, stack)
}

// --- Chunk Block-Entity NBT & Disk Persistence (<WorldDir>/data/banners.dat) ---

// ChunkBannerBlockEntitiesNBT returns all BannerBlockEntity NBT compounds in
// chunk (cx, cz) of dimension.
func (s *Server) ChunkBannerBlockEntitiesNBT(dimension string, cx, cz int32) []*nbt.Compound {
	if s == nil {
		return nil
	}
	normDim := world.NormalizeDimension(dimension)
	var out []*nbt.Compound
	st := s.bannerStore()
	st.mu.RLock()
	for key, b := range st.banners {
		if b != nil && key.dim == normDim && int32(key.x>>4) == cx && int32(key.z>>4) == cz {
			out = append(out, b.ToNBT())
		}
	}
	st.mu.RUnlock()
	return out
}

// LoadChunkBannerBlockEntitiesNBT loads any `minecraft:banner` entries from a
// chunk's `block_entities` NBT list into the server banner store.
func (s *Server) LoadChunkBannerBlockEntitiesNBT(dimension string, list nbt.List) {
	if s == nil {
		return
	}
	normDim := world.NormalizeDimension(dimension)
	for _, elem := range list.Elems {
		comp, ok := elem.(*nbt.Compound)
		if !ok || comp == nil {
			continue
		}
		id, _ := nbtStringTag(comp, "id")
		if id != "minecraft:banner" {
			continue
		}
		if b, err := BannerBlockEntityFromNBT(comp); err == nil && b != nil {
			if _, hasDim := nbtStringTag(comp, "dimension"); !hasDim {
				b.Dimension = normDim
			}
			key := blockPosDimKey{dim: b.Dimension, x: b.X, y: b.Y, z: b.Z}
			st := s.bannerStore()
			st.mu.Lock()
			st.banners[key] = b
			st.mu.Unlock()
		}
	}
}

// SaveBannersToNBT serializes all placed BannerBlockEntities into an NBT compound.
func (s *Server) SaveBannersToNBT() *nbt.Compound {
	root := nbt.NewCompound()
	root.Set("DataVersion", nbt.Int(mapDataVersion26_1_2))
	if s == nil {
		return root
	}
	st := s.bannerStore()
	st.mu.RLock()
	keys := make([]blockPosDimKey, 0, len(st.banners))
	for k := range st.banners {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].dim != keys[j].dim {
			return keys[i].dim < keys[j].dim
		}
		if keys[i].x != keys[j].x {
			return keys[i].x < keys[j].x
		}
		if keys[i].y != keys[j].y {
			return keys[i].y < keys[j].y
		}
		return keys[i].z < keys[j].z
	})
	var elems []nbt.Tag
	for _, k := range keys {
		if b := st.banners[k]; b != nil {
			elems = append(elems, b.ToNBT())
		}
	}
	st.mu.RUnlock()

	elemID := byte(nbt.TagCompound)
	if len(elems) == 0 {
		elemID = nbt.TagEnd
	}
	root.Set("block_entities", nbt.List{ElemID: elemID, Elems: elems})
	return root
}

// LoadBannersFromNBT restores BannerBlockEntities from a saved NBT compound.
func (s *Server) LoadBannersFromNBT(root *nbt.Compound) error {
	if s == nil || root == nil {
		return nil
	}
	if list, ok := nbtListTag(root, "block_entities"); ok {
		s.LoadChunkBannerBlockEntitiesNBT(world.DimensionOverworld, list)
	}
	return nil
}

// SaveBannersFile writes `<WorldDir>/data/banners.dat` as gzipped NBT.
func (s *Server) SaveBannersFile() error {
	if s == nil || s.cfg.WorldDir == "" {
		return nil
	}
	dir := filepath.Join(s.cfg.WorldDir, "data")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	raw := nbt.MarshalNamed("", s.SaveBannersToNBT())
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(raw); err != nil {
		_ = gz.Close()
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "banners.dat"), buf.Bytes(), 0o644)
}

// LoadBannersFile loads `<WorldDir>/data/banners.dat` if it exists.
func (s *Server) LoadBannersFile() error {
	if s == nil || s.cfg.WorldDir == "" {
		return nil
	}
	path := filepath.Join(s.cfg.WorldDir, "data", "banners.dat")
	rawFile, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var nbtBytes []byte
	if len(rawFile) >= 2 && rawFile[0] == 0x1f && rawFile[1] == 0x8b {
		gz, err := gzip.NewReader(bytes.NewReader(rawFile))
		if err != nil {
			return err
		}
		nbtBytes, err = io.ReadAll(gz)
		_ = gz.Close()
		if err != nil {
			return err
		}
	} else {
		nbtBytes = rawFile
	}
	_, root, err := nbt.UnmarshalNamed(nbtBytes)
	if err != nil {
		return err
	}
	rootComp, ok := root.(*nbt.Compound)
	if !ok || rootComp == nil {
		return errors.New("server: banners.dat root is not Compound")
	}
	return s.LoadBannersFromNBT(rootComp)
}
