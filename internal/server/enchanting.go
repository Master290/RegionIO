package server

import (
	"errors"
	"math"
	"math/rand"
	"sort"
	"strings"
	"sync"

	"regionio/internal/protocol"
	"regionio/internal/registry"
	"regionio/internal/world"
)

// Enchanting table container layout constants (MenuTypeEnchantment = 13).
const (
	EnchantingSlotItem       = 0
	EnchantingSlotLapis      = 1
	EnchantingInputSlotCount = 2
	EnchantingMenuSlotCount  = 38 // 2 enchanting + 27 main inventory + 9 hotbar
	MaxEnchantingBookshelves = 15
)

// EnchantmentMenu ContainerData property indices in ClientboundContainerSetDataPacket (0x13).
const (
	EnchantDataCost0   int16 = 0
	EnchantDataCost1   int16 = 1
	EnchantDataCost2   int16 = 2
	EnchantDataXPSeed  int16 = 3
	EnchantDataClueID0 int16 = 4
	EnchantDataClueID1 int16 = 5
	EnchantDataClueID2 int16 = 6
	EnchantDataLevel0  int16 = 7
	EnchantDataLevel1  int16 = 8
	EnchantDataLevel2  int16 = 9
)

// ItemCategory classifies items for enchantment compatibility.
type ItemCategory string

const (
	CategoryArmor      ItemCategory = "armor"
	CategoryHelmet     ItemCategory = "helmet"
	CategoryChestplate ItemCategory = "chestplate"
	CategoryLeggings   ItemCategory = "leggings"
	CategoryBoots      ItemCategory = "boots"
	CategorySword      ItemCategory = "sword"
	CategoryAxe        ItemCategory = "axe"
	CategoryPickaxe    ItemCategory = "pickaxe"
	CategoryShovel     ItemCategory = "shovel"
	CategoryHoe        ItemCategory = "hoe"
	CategoryDigger     ItemCategory = "digger"
	CategoryWeapon     ItemCategory = "weapon"
	CategoryBow        ItemCategory = "bow"
	CategoryCrossbow   ItemCategory = "crossbow"
	CategoryTrident    ItemCategory = "trident"
	CategoryFishingRod ItemCategory = "fishing_rod"
	CategoryMace       ItemCategory = "mace"
	CategorySpear      ItemCategory = "spear"
	CategoryShears     ItemCategory = "shears"
	CategoryBreakable  ItemCategory = "breakable"
	CategoryVanishable ItemCategory = "vanishable"
)

// EnchantmentDef describes one vanilla 26.1.2 enchantment definition.
type EnchantmentDef struct {
	ID                string
	RegistryIndex     int
	Weight            int
	MaxLevel          int
	AnvilCost         int
	MinCostBase       int
	MinCostPerLevel   int
	MaxCostBase       int
	MaxCostPerLevel   int
	PrimaryCategories []ItemCategory
	AnvilCategories   []ItemCategory
	IncompatibleGroup string
	InTable           bool
	Curse             bool
}

// MinCost returns the minimum modified enchantment level required to roll level (1..MaxLevel).
func (d *EnchantmentDef) MinCost(level int) int {
	if d == nil || level < 1 {
		return 1
	}
	return d.MinCostBase + (level-1)*d.MinCostPerLevel
}

// MaxCost returns the maximum modified enchantment level at which level (1..MaxLevel) can be rolled.
func (d *EnchantmentDef) MaxCost(level int) int {
	if d == nil || level < 1 {
		return 50
	}
	return d.MaxCostBase + (level-1)*d.MaxCostPerLevel
}

// EnchantmentInstance pairs a canonical enchantment ID with its 1-based level.
type EnchantmentInstance struct {
	ID    string
	Level int
}

// EnchantmentOffer represents one of the three rows in an Enchanting Table UI.
type EnchantmentOffer struct {
	Slot          int
	Cost          int16
	Enchantments  []EnchantmentInstance
	ClueEnchantID int16
	ClueLevel     int16
}

// EnchantmentRegistry holds the ordered vanilla 26.1.2 enchantment definitions.
type EnchantmentRegistry struct {
	defs    []*EnchantmentDef
	byID    map[string]*EnchantmentDef
	byIndex map[int]*EnchantmentDef
}

var (
	defaultEnchantRegOnce sync.Once
	defaultEnchantReg     *EnchantmentRegistry
)

// DefaultEnchantmentRegistry returns the singleton vanilla 26.1.2 enchantment registry.
func DefaultEnchantmentRegistry() *EnchantmentRegistry {
	defaultEnchantRegOnce.Do(func() {
		defaultEnchantReg = buildVanillaEnchantmentRegistry()
	})
	return defaultEnchantReg
}

// All returns a copy of all registered enchantment definitions in registry order.
func (r *EnchantmentRegistry) All() []*EnchantmentDef {
	if r == nil {
		return nil
	}
	out := make([]*EnchantmentDef, len(r.defs))
	copy(out, r.defs)
	return out
}

// ByID looks up an enchantment definition by canonical or short name.
func (r *EnchantmentRegistry) ByID(id string) (*EnchantmentDef, bool) {
	if r == nil {
		return nil, false
	}
	def, ok := r.byID[ canonicalEnchantmentID(id)]
	return def, ok
}

// ByIndex looks up an enchantment definition by its numeric registry index (0..42).
func (r *EnchantmentRegistry) ByIndex(idx int) (*EnchantmentDef, bool) {
	if r == nil {
		return nil, false
	}
	def, ok := r.byIndex[idx]
	return def, ok
}

// EnchantmentByID looks up an enchantment in DefaultEnchantmentRegistry by ID.
func EnchantmentByID(id string) (*EnchantmentDef, bool) {
	return DefaultEnchantmentRegistry().ByID(id)
}

// EnchantmentByIndex looks up an enchantment in DefaultEnchantmentRegistry by numeric index.
func EnchantmentByIndex(idx int) (*EnchantmentDef, bool) {
	return DefaultEnchantmentRegistry().ByIndex(idx)
}

func canonicalEnchantmentID(id string) string {
	id = strings.TrimSpace(strings.ToLower(id))
	if id == "" {
		return ""
	}
	if !strings.Contains(id, ":") {
		return "minecraft:" + id
	}
	return id
}

func buildVanillaEnchantmentRegistry() *EnchantmentRegistry {
	defs := []*EnchantmentDef{
		{ID: "minecraft:aqua_affinity", Weight: 2, MaxLevel: 1, AnvilCost: 4, MinCostBase: 1, MinCostPerLevel: 0, MaxCostBase: 41, MaxCostPerLevel: 0, PrimaryCategories: []ItemCategory{CategoryHelmet}, InTable: true},
		{ID: "minecraft:bane_of_arthropods", Weight: 5, MaxLevel: 5, AnvilCost: 2, MinCostBase: 5, MinCostPerLevel: 8, MaxCostBase: 25, MaxCostPerLevel: 8, PrimaryCategories: []ItemCategory{CategorySword}, AnvilCategories: []ItemCategory{CategoryAxe}, IncompatibleGroup: "damage", InTable: true},
		{ID: "minecraft:binding_curse", Weight: 1, MaxLevel: 1, AnvilCost: 8, MinCostBase: 25, MinCostPerLevel: 0, MaxCostBase: 50, MaxCostPerLevel: 0, PrimaryCategories: []ItemCategory{CategoryArmor}, InTable: false, Curse: true},
		{ID: "minecraft:blast_protection", Weight: 2, MaxLevel: 4, AnvilCost: 4, MinCostBase: 5, MinCostPerLevel: 8, MaxCostBase: 13, MaxCostPerLevel: 8, PrimaryCategories: []ItemCategory{CategoryArmor}, IncompatibleGroup: "protection", InTable: true},
		{ID: "minecraft:breach", Weight: 2, MaxLevel: 4, AnvilCost: 4, MinCostBase: 15, MinCostPerLevel: 9, MaxCostBase: 65, MaxCostPerLevel: 9, PrimaryCategories: []ItemCategory{CategoryMace}, IncompatibleGroup: "damage", InTable: true},
		{ID: "minecraft:channeling", Weight: 1, MaxLevel: 1, AnvilCost: 8, MinCostBase: 25, MinCostPerLevel: 0, MaxCostBase: 50, MaxCostPerLevel: 0, PrimaryCategories: []ItemCategory{CategoryTrident}, IncompatibleGroup: "trident_riptide", InTable: true},
		{ID: "minecraft:density", Weight: 5, MaxLevel: 5, AnvilCost: 2, MinCostBase: 5, MinCostPerLevel: 8, MaxCostBase: 25, MaxCostPerLevel: 8, PrimaryCategories: []ItemCategory{CategoryMace}, IncompatibleGroup: "damage", InTable: true},
		{ID: "minecraft:depth_strider", Weight: 2, MaxLevel: 3, AnvilCost: 4, MinCostBase: 10, MinCostPerLevel: 10, MaxCostBase: 25, MaxCostPerLevel: 10, PrimaryCategories: []ItemCategory{CategoryBoots}, IncompatibleGroup: "boots_walk", InTable: true},
		{ID: "minecraft:efficiency", Weight: 10, MaxLevel: 5, AnvilCost: 1, MinCostBase: 1, MinCostPerLevel: 10, MaxCostBase: 51, MaxCostPerLevel: 10, PrimaryCategories: []ItemCategory{CategoryDigger}, AnvilCategories: []ItemCategory{CategoryShears}, InTable: true},
		{ID: "minecraft:feather_falling", Weight: 5, MaxLevel: 4, AnvilCost: 2, MinCostBase: 5, MinCostPerLevel: 6, MaxCostBase: 11, MaxCostPerLevel: 6, PrimaryCategories: []ItemCategory{CategoryBoots}, InTable: true},
		{ID: "minecraft:fire_aspect", Weight: 2, MaxLevel: 2, AnvilCost: 4, MinCostBase: 10, MinCostPerLevel: 20, MaxCostBase: 60, MaxCostPerLevel: 20, PrimaryCategories: []ItemCategory{CategorySword}, AnvilCategories: []ItemCategory{CategoryMace}, InTable: true},
		{ID: "minecraft:fire_protection", Weight: 5, MaxLevel: 4, AnvilCost: 2, MinCostBase: 10, MinCostPerLevel: 8, MaxCostBase: 18, MaxCostPerLevel: 8, PrimaryCategories: []ItemCategory{CategoryArmor}, IncompatibleGroup: "protection", InTable: true},
		{ID: "minecraft:flame", Weight: 2, MaxLevel: 1, AnvilCost: 4, MinCostBase: 20, MinCostPerLevel: 0, MaxCostBase: 50, MaxCostPerLevel: 0, PrimaryCategories: []ItemCategory{CategoryBow}, InTable: true},
		{ID: "minecraft:fortune", Weight: 2, MaxLevel: 3, AnvilCost: 4, MinCostBase: 15, MinCostPerLevel: 9, MaxCostBase: 65, MaxCostPerLevel: 9, PrimaryCategories: []ItemCategory{CategoryDigger}, IncompatibleGroup: "mining", InTable: true},
		{ID: "minecraft:frost_walker", Weight: 2, MaxLevel: 2, AnvilCost: 4, MinCostBase: 10, MinCostPerLevel: 10, MaxCostBase: 25, MaxCostPerLevel: 10, PrimaryCategories: []ItemCategory{CategoryBoots}, IncompatibleGroup: "boots_walk", InTable: false},
		{ID: "minecraft:impaling", Weight: 2, MaxLevel: 5, AnvilCost: 4, MinCostBase: 1, MinCostPerLevel: 8, MaxCostBase: 21, MaxCostPerLevel: 8, PrimaryCategories: []ItemCategory{CategoryTrident}, IncompatibleGroup: "damage", InTable: true},
		{ID: "minecraft:infinity", Weight: 1, MaxLevel: 1, AnvilCost: 8, MinCostBase: 20, MinCostPerLevel: 0, MaxCostBase: 50, MaxCostPerLevel: 0, PrimaryCategories: []ItemCategory{CategoryBow}, IncompatibleGroup: "bow_mending", InTable: true},
		{ID: "minecraft:knockback", Weight: 5, MaxLevel: 2, AnvilCost: 2, MinCostBase: 5, MinCostPerLevel: 20, MaxCostBase: 55, MaxCostPerLevel: 20, PrimaryCategories: []ItemCategory{CategorySword}, InTable: true},
		{ID: "minecraft:looting", Weight: 2, MaxLevel: 3, AnvilCost: 4, MinCostBase: 15, MinCostPerLevel: 9, MaxCostBase: 65, MaxCostPerLevel: 9, PrimaryCategories: []ItemCategory{CategorySword}, InTable: true},
		{ID: "minecraft:loyalty", Weight: 5, MaxLevel: 3, AnvilCost: 2, MinCostBase: 12, MinCostPerLevel: 7, MaxCostBase: 50, MaxCostPerLevel: 0, PrimaryCategories: []ItemCategory{CategoryTrident}, IncompatibleGroup: "trident_riptide", InTable: true},
		{ID: "minecraft:luck_of_the_sea", Weight: 2, MaxLevel: 3, AnvilCost: 4, MinCostBase: 15, MinCostPerLevel: 9, MaxCostBase: 65, MaxCostPerLevel: 9, PrimaryCategories: []ItemCategory{CategoryFishingRod}, InTable: true},
		{ID: "minecraft:lunge", Weight: 5, MaxLevel: 3, AnvilCost: 2, MinCostBase: 5, MinCostPerLevel: 8, MaxCostBase: 25, MaxCostPerLevel: 8, PrimaryCategories: []ItemCategory{CategorySpear}, InTable: true},
		{ID: "minecraft:lure", Weight: 2, MaxLevel: 3, AnvilCost: 4, MinCostBase: 15, MinCostPerLevel: 9, MaxCostBase: 65, MaxCostPerLevel: 9, PrimaryCategories: []ItemCategory{CategoryFishingRod}, InTable: true},
		{ID: "minecraft:mending", Weight: 2, MaxLevel: 1, AnvilCost: 4, MinCostBase: 25, MinCostPerLevel: 0, MaxCostBase: 75, MaxCostPerLevel: 0, PrimaryCategories: []ItemCategory{CategoryBreakable}, IncompatibleGroup: "bow_mending", InTable: false},
		{ID: "minecraft:multishot", Weight: 2, MaxLevel: 1, AnvilCost: 4, MinCostBase: 20, MinCostPerLevel: 0, MaxCostBase: 50, MaxCostPerLevel: 0, PrimaryCategories: []ItemCategory{CategoryCrossbow}, IncompatibleGroup: "crossbow", InTable: true},
		{ID: "minecraft:piercing", Weight: 10, MaxLevel: 4, AnvilCost: 1, MinCostBase: 1, MinCostPerLevel: 10, MaxCostBase: 50, MaxCostPerLevel: 0, PrimaryCategories: []ItemCategory{CategoryCrossbow}, IncompatibleGroup: "crossbow", InTable: true},
		{ID: "minecraft:power", Weight: 10, MaxLevel: 5, AnvilCost: 1, MinCostBase: 1, MinCostPerLevel: 10, MaxCostBase: 16, MaxCostPerLevel: 10, PrimaryCategories: []ItemCategory{CategoryBow}, InTable: true},
		{ID: "minecraft:projectile_protection", Weight: 5, MaxLevel: 4, AnvilCost: 2, MinCostBase: 3, MinCostPerLevel: 6, MaxCostBase: 9, MaxCostPerLevel: 6, PrimaryCategories: []ItemCategory{CategoryArmor}, IncompatibleGroup: "protection", InTable: true},
		{ID: "minecraft:protection", Weight: 10, MaxLevel: 4, AnvilCost: 1, MinCostBase: 1, MinCostPerLevel: 11, MaxCostBase: 12, MaxCostPerLevel: 11, PrimaryCategories: []ItemCategory{CategoryArmor}, IncompatibleGroup: "protection", InTable: true},
		{ID: "minecraft:punch", Weight: 2, MaxLevel: 2, AnvilCost: 4, MinCostBase: 12, MinCostPerLevel: 20, MaxCostBase: 37, MaxCostPerLevel: 20, PrimaryCategories: []ItemCategory{CategoryBow}, InTable: true},
		{ID: "minecraft:quick_charge", Weight: 5, MaxLevel: 3, AnvilCost: 2, MinCostBase: 12, MinCostPerLevel: 20, MaxCostBase: 50, MaxCostPerLevel: 0, PrimaryCategories: []ItemCategory{CategoryCrossbow}, InTable: true},
		{ID: "minecraft:respiration", Weight: 2, MaxLevel: 3, AnvilCost: 4, MinCostBase: 10, MinCostPerLevel: 10, MaxCostBase: 40, MaxCostPerLevel: 10, PrimaryCategories: []ItemCategory{CategoryHelmet}, InTable: true},
		{ID: "minecraft:riptide", Weight: 2, MaxLevel: 3, AnvilCost: 4, MinCostBase: 17, MinCostPerLevel: 7, MaxCostBase: 50, MaxCostPerLevel: 0, PrimaryCategories: []ItemCategory{CategoryTrident}, IncompatibleGroup: "trident_riptide", InTable: true},
		{ID: "minecraft:sharpness", Weight: 10, MaxLevel: 5, AnvilCost: 1, MinCostBase: 1, MinCostPerLevel: 11, MaxCostBase: 21, MaxCostPerLevel: 11, PrimaryCategories: []ItemCategory{CategorySword}, AnvilCategories: []ItemCategory{CategoryAxe}, IncompatibleGroup: "damage", InTable: true},
		{ID: "minecraft:silk_touch", Weight: 1, MaxLevel: 1, AnvilCost: 8, MinCostBase: 15, MinCostPerLevel: 0, MaxCostBase: 65, MaxCostPerLevel: 0, PrimaryCategories: []ItemCategory{CategoryDigger}, AnvilCategories: []ItemCategory{CategoryShears}, IncompatibleGroup: "mining", InTable: true},
		{ID: "minecraft:smite", Weight: 5, MaxLevel: 5, AnvilCost: 2, MinCostBase: 5, MinCostPerLevel: 8, MaxCostBase: 25, MaxCostPerLevel: 8, PrimaryCategories: []ItemCategory{CategorySword}, AnvilCategories: []ItemCategory{CategoryAxe}, IncompatibleGroup: "damage", InTable: true},
		{ID: "minecraft:soul_speed", Weight: 1, MaxLevel: 3, AnvilCost: 8, MinCostBase: 10, MinCostPerLevel: 10, MaxCostBase: 25, MaxCostPerLevel: 10, PrimaryCategories: []ItemCategory{CategoryBoots}, InTable: false},
		{ID: "minecraft:sweeping_edge", Weight: 2, MaxLevel: 3, AnvilCost: 4, MinCostBase: 5, MinCostPerLevel: 9, MaxCostBase: 20, MaxCostPerLevel: 9, PrimaryCategories: []ItemCategory{CategorySword}, InTable: true},
		{ID: "minecraft:swift_sneak", Weight: 1, MaxLevel: 3, AnvilCost: 8, MinCostBase: 25, MinCostPerLevel: 25, MaxCostBase: 75, MaxCostPerLevel: 25, PrimaryCategories: []ItemCategory{CategoryLeggings}, InTable: false},
		{ID: "minecraft:thorns", Weight: 1, MaxLevel: 3, AnvilCost: 8, MinCostBase: 10, MinCostPerLevel: 20, MaxCostBase: 60, MaxCostPerLevel: 20, PrimaryCategories: []ItemCategory{CategoryChestplate}, AnvilCategories: []ItemCategory{CategoryArmor}, InTable: true},
		{ID: "minecraft:unbreaking", Weight: 5, MaxLevel: 3, AnvilCost: 2, MinCostBase: 5, MinCostPerLevel: 8, MaxCostBase: 55, MaxCostPerLevel: 8, PrimaryCategories: []ItemCategory{CategoryBreakable}, InTable: true},
		{ID: "minecraft:vanishing_curse", Weight: 1, MaxLevel: 1, AnvilCost: 8, MinCostBase: 25, MinCostPerLevel: 0, MaxCostBase: 50, MaxCostPerLevel: 0, PrimaryCategories: []ItemCategory{CategoryVanishable}, InTable: false, Curse: true},
		{ID: "minecraft:wind_burst", Weight: 2, MaxLevel: 3, AnvilCost: 4, MinCostBase: 15, MinCostPerLevel: 9, MaxCostBase: 65, MaxCostPerLevel: 9, PrimaryCategories: []ItemCategory{CategoryMace}, InTable: false},
	}

	reg := &EnchantmentRegistry{
		defs:    defs,
		byID:    make(map[string]*EnchantmentDef, len(defs)),
		byIndex: make(map[int]*EnchantmentDef, len(defs)),
	}
	for i, d := range defs {
		idx := registry.Index("minecraft:enchantment", d.ID)
		if idx < 0 {
			idx = i
		}
		d.RegistryIndex = idx
		reg.byID[d.ID] = d
		reg.byIndex[idx] = d
	}
	return reg
}

// AreEnchantmentsCompatible reports whether two enchantments can coexist on the
// same item, matching vanilla exclusive_set tag rules.
func AreEnchantmentsCompatible(idA, idB string) bool {
	a := canonicalEnchantmentID(idA)
	b := canonicalEnchantmentID(idB)
	if a == "" || b == "" {
		return false
	}
	if a == b {
		return false
	}
	defA, okA := EnchantmentByID(a)
	defB, okB := EnchantmentByID(b)
	if !okA || !okB {
		return false
	}
	// Riptide is mutually exclusive with both Loyalty and Channeling, while
	// Loyalty and Channeling ARE compatible with each other.
	if (a == "minecraft:riptide" && (b == "minecraft:loyalty" || b == "minecraft:channeling")) ||
		(b == "minecraft:riptide" && (a == "minecraft:loyalty" || a == "minecraft:channeling")) {
		return false
	}
	if defA.IncompatibleGroup != "" && defA.IncompatibleGroup == defB.IncompatibleGroup {
		if defA.IncompatibleGroup == "trident_riptide" {
			return true
		}
		return false
	}
	return true
}

// CanEnchantItem reports whether enchantID can be applied to itemName.
// When isAnvil is true, secondary anvil categories (such as Sharpness on an axe
// or Thorns on any armor piece) and books are also accepted.
func CanEnchantItem(enchantID, itemName string, isAnvil bool) bool {
	def, ok := EnchantmentByID(enchantID)
	if !ok {
		return false
	}
	itemName = canonicalEnchantmentID(itemName)
	if itemName == "minecraft:book" || (isAnvil && itemName == "minecraft:enchanted_book") {
		return true
	}
	cats := itemCategories(itemName)
	for _, want := range def.PrimaryCategories {
		if hasCategory(cats, want) {
			return true
		}
	}
	if isAnvil {
		for _, want := range def.AnvilCategories {
			if hasCategory(cats, want) {
				return true
			}
		}
	}
	return false
}

func hasCategory(cats []ItemCategory, want ItemCategory) bool {
	for _, c := range cats {
		if c == want {
			return true
		}
	}
	return false
}

func itemCategories(itemName string) []ItemCategory {
	name := strings.TrimPrefix(strings.ToLower(itemName), "minecraft:")
	var cats []ItemCategory
	switch {
	case strings.HasSuffix(name, "_helmet") || name == "turtle_helmet":
		cats = append(cats, CategoryHelmet, CategoryArmor, CategoryBreakable, CategoryVanishable)
	case strings.HasSuffix(name, "_chestplate"):
		cats = append(cats, CategoryChestplate, CategoryArmor, CategoryBreakable, CategoryVanishable)
	case strings.HasSuffix(name, "_leggings"):
		cats = append(cats, CategoryLeggings, CategoryArmor, CategoryBreakable, CategoryVanishable)
	case strings.HasSuffix(name, "_boots"):
		cats = append(cats, CategoryBoots, CategoryArmor, CategoryBreakable, CategoryVanishable)
	case strings.HasSuffix(name, "_sword"):
		cats = append(cats, CategorySword, CategoryWeapon, CategoryBreakable, CategoryVanishable)
	case strings.HasSuffix(name, "_axe"):
		cats = append(cats, CategoryAxe, CategoryDigger, CategoryWeapon, CategoryBreakable, CategoryVanishable)
	case strings.HasSuffix(name, "_pickaxe"):
		cats = append(cats, CategoryPickaxe, CategoryDigger, CategoryBreakable, CategoryVanishable)
	case strings.HasSuffix(name, "_shovel"):
		cats = append(cats, CategoryShovel, CategoryDigger, CategoryBreakable, CategoryVanishable)
	case strings.HasSuffix(name, "_hoe"):
		cats = append(cats, CategoryHoe, CategoryDigger, CategoryBreakable, CategoryVanishable)
	case strings.HasSuffix(name, "_spear"):
		cats = append(cats, CategorySpear, CategoryWeapon, CategoryBreakable, CategoryVanishable)
	case name == "bow":
		cats = append(cats, CategoryBow, CategoryBreakable, CategoryVanishable)
	case name == "crossbow":
		cats = append(cats, CategoryCrossbow, CategoryBreakable, CategoryVanishable)
	case name == "trident":
		cats = append(cats, CategoryTrident, CategoryWeapon, CategoryBreakable, CategoryVanishable)
	case name == "mace":
		cats = append(cats, CategoryMace, CategoryWeapon, CategoryBreakable, CategoryVanishable)
	case name == "fishing_rod":
		cats = append(cats, CategoryFishingRod, CategoryBreakable, CategoryVanishable)
	case name == "shears":
		cats = append(cats, CategoryShears, CategoryBreakable, CategoryVanishable)
	case MaxItemDurability("minecraft:"+name) > 0:
		cats = append(cats, CategoryBreakable, CategoryVanishable)
	}
	return cats
}

// ItemMeta holds per-stack durability damage, enchantments, custom anvil name,
// and prior-work penalty cost.
type ItemMeta struct {
	Damage       int32
	Enchantments map[string]int
	CustomName   string
	RepairCost   int32
}

// Clone returns a deep copy of m.
func (m ItemMeta) Clone() ItemMeta {
	cp := ItemMeta{
		Damage:     m.Damage,
		CustomName: m.CustomName,
		RepairCost: m.RepairCost,
	}
	if len(m.Enchantments) > 0 {
		cp.Enchantments = make(map[string]int, len(m.Enchantments))
		for k, v := range m.Enchantments {
			if v > 0 {
				cp.Enchantments[canonicalEnchantmentID(k)] = v
			}
		}
	}
	return cp
}

// IsEmpty reports whether m carries no damage, enchantments, custom name, or repair cost.
func (m ItemMeta) IsEmpty() bool {
	return m.Damage == 0 && len(m.Enchantments) == 0 && m.CustomName == "" && m.RepairCost == 0
}

// HasEnchantments reports whether m has at least one enchantment with level > 0.
func (m ItemMeta) HasEnchantments() bool {
	for _, lvl := range m.Enchantments {
		if lvl > 0 {
			return true
		}
	}
	return false
}

// EnchantmentLevel returns the level of enchantID on m, or 0 if not present.
func (m ItemMeta) EnchantmentLevel(enchantID string) int {
	if len(m.Enchantments) == 0 {
		return 0
	}
	return m.Enchantments[canonicalEnchantmentID(enchantID)]
}

// SetEnchantment sets or removes (when level <= 0) an enchantment on m.
func (m *ItemMeta) SetEnchantment(enchantID string, level int) {
	if m == nil {
		return
	}
	key := canonicalEnchantmentID(enchantID)
	if key == "" {
		return
	}
	if level <= 0 {
		if m.Enchantments != nil {
			delete(m.Enchantments, key)
		}
		return
	}
	if m.Enchantments == nil {
		m.Enchantments = make(map[string]int)
	}
	m.Enchantments[key] = level
}

// SortedEnchantments returns all active enchantments on m sorted by canonical ID.
func (m ItemMeta) SortedEnchantments() []EnchantmentInstance {
	if len(m.Enchantments) == 0 {
		return nil
	}
	out := make([]EnchantmentInstance, 0, len(m.Enchantments))
	for k, v := range m.Enchantments {
		if v > 0 {
			out = append(out, EnchantmentInstance{ID: canonicalEnchantmentID(k), Level: v})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	return out
}

type sessionItemMetaTracker struct {
	mu           sync.Mutex
	invMeta      [world.PlayerInventorySlots]ItemMeta
	carriedMeta  ItemMeta
	enchantMeta  [2]ItemMeta
	combinerMeta [2]ItemMeta
	resultMeta   ItemMeta
}

var playerItemMetas sync.Map // map[*PlayerSession]*sessionItemMetaTracker

func getItemMetaTracker(session *PlayerSession) *sessionItemMetaTracker {
	if session == nil {
		return nil
	}
	if v, ok := playerItemMetas.Load(session); ok {
		return v.(*sessionItemMetaTracker)
	}
	actual, _ := playerItemMetas.LoadOrStore(session, &sessionItemMetaTracker{})
	return actual.(*sessionItemMetaTracker)
}

// GetSlotItemMeta returns the ItemMeta associated with player inventory slot (0..45).
func GetSlotItemMeta(session *PlayerSession, slot int) ItemMeta {
	if session == nil || slot < 0 || slot >= world.PlayerInventorySlots {
		return ItemMeta{}
	}
	if session.InventorySlot(slot).IsEmpty() {
		return ItemMeta{}
	}
	tr := getItemMetaTracker(session)
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return tr.invMeta[slot].Clone()
}

// SetSlotItemMeta sets the ItemMeta associated with player inventory slot (0..45).
func SetSlotItemMeta(session *PlayerSession, slot int, meta ItemMeta) {
	if session == nil || slot < 0 || slot >= world.PlayerInventorySlots {
		return
	}
	tr := getItemMetaTracker(session)
	tr.mu.Lock()
	tr.invMeta[slot] = meta.Clone()
	tr.mu.Unlock()
}

// GetCarriedItemMeta returns the ItemMeta for the item on the player's cursor.
func GetCarriedItemMeta(session *PlayerSession) ItemMeta {
	if session == nil || session.CarriedItem().IsEmpty() {
		return ItemMeta{}
	}
	tr := getItemMetaTracker(session)
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return tr.carriedMeta.Clone()
}

// SetCarriedItemMeta sets the ItemMeta for the item on the player's cursor.
func SetCarriedItemMeta(session *PlayerSession, meta ItemMeta) {
	if session == nil {
		return
	}
	tr := getItemMetaTracker(session)
	tr.mu.Lock()
	tr.carriedMeta = meta.Clone()
	tr.mu.Unlock()
}

// MaxItemDurability returns the maximum durability of a vanilla 26.1.2 tool,
// weapon, or armor item, or 0 if the item does not have durability.
func MaxItemDurability(itemName string) int32 {
	name := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(itemName)), "minecraft:")
	if name == "" {
		return 0
	}

	// Tools & melee weapons by material tier.
	toolTiers := map[string]int32{
		"wooden_":    59,
		"stone_":     131,
		"copper_":    190,
		"iron_":      250,
		"golden_":    32,
		"diamond_":   1561,
		"netherite_": 2031,
	}
	for prefix, dur := range toolTiers {
		if strings.HasPrefix(name, prefix) {
			rest := strings.TrimPrefix(name, prefix)
			switch rest {
			case "sword", "pickaxe", "axe", "shovel", "hoe", "spear":
				return dur
			}
		}
	}

	// Armor pieces by material multiplier * base slot factor [11, 16, 15, 13].
	armorMultipliers := map[string]int32{
		"leather_":   5,
		"golden_":    7,
		"copper_":    11,
		"chainmail_": 15,
		"iron_":      15,
		"diamond_":   33,
		"netherite_": 37,
	}
	for prefix, mult := range armorMultipliers {
		if strings.HasPrefix(name, prefix) {
			switch strings.TrimPrefix(name, prefix) {
			case "helmet":
				return 11 * mult
			case "chestplate":
				return 16 * mult
			case "leggings":
				return 15 * mult
			case "boots":
				return 13 * mult
			}
		}
	}

	switch name {
	case "turtle_helmet":
		return 275
	case "bow":
		return 384
	case "crossbow":
		return 465
	case "trident":
		return 250
	case "mace":
		return 500
	case "shield":
		return 336
	case "elytra":
		return 432
	case "shears":
		return 238
	case "flint_and_steel", "fishing_rod", "brush":
		return 64
	case "carrot_on_a_stick":
		return 25
	case "warped_fungus_on_a_stick":
		return 100
	default:
		return 0
	}
}

// IsArmorItem reports whether itemName is a wearable armor piece.
func IsArmorItem(itemName string) bool {
	name := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(itemName)), "minecraft:")
	return name == "turtle_helmet" ||
		strings.HasSuffix(name, "_helmet") ||
		strings.HasSuffix(name, "_chestplate") ||
		strings.HasSuffix(name, "_leggings") ||
		strings.HasSuffix(name, "_boots")
}

// IsDamageableItem reports whether itemName has finite durability.
func IsDamageableItem(itemName string) bool {
	return MaxItemDurability(itemName) > 0
}

// DamageItem applies amount points of durability wear to stack/meta, respecting
// minecraft:unbreaking. When durability is exhausted, stack is cleared and
// broke is returned as true.
func DamageItem(stack world.ItemStack, meta ItemMeta, amount int32, rng *rand.Rand) (world.ItemStack, ItemMeta, bool) {
	if stack.IsEmpty() || amount <= 0 {
		return stack, meta, false
	}
	itemName := stack.ResolvedID()
	maxDur := MaxItemDurability(itemName)
	if maxDur <= 0 {
		return stack, meta, false
	}

	outMeta := meta.Clone()
	unbreaking := outMeta.EnchantmentLevel("minecraft:unbreaking")
	isArmor := IsArmorItem(itemName)

	var applied int32
	for i := int32(0); i < amount; i++ {
		if unbreaking > 0 && shouldIgnoreDurabilityDamage(unbreaking, isArmor, rng) {
			continue
		}
		applied++
	}
	if applied <= 0 {
		return stack, outMeta, false
	}

	outMeta.Damage += applied
	if outMeta.Damage >= maxDur {
		return world.ItemStack{}, ItemMeta{}, true
	}
	return stack, outMeta, false
}

func shouldIgnoreDurabilityDamage(unbreakingLevel int, isArmor bool, rng *rand.Rand) bool {
	if unbreakingLevel <= 0 {
		return false
	}
	// Vanilla Unbreaking:
	// - Armor: 60% + 40%/(level+1) chance to take damage (i.e. ignored only if
	//   rng.Float64() >= 0.6 && rng.Intn(level+1) > 0).
	// - Tools/weapons: 1/(level+1) chance to take damage (ignored if rng.Intn(level+1) > 0).
	if rng == nil {
		// Deterministic fallback when nil rng is passed: reduce damage on odd levels.
		return !isArmor
	}
	if isArmor && rng.Float64() < 0.6 {
		return false
	}
	return rng.Intn(unbreakingLevel+1) > 0
}

// DamagePlayerItem applies durability damage to the item in session's inventory
// slot, breaking and removing the item when its durability reaches MaxItemDurability.
func (s *Server) DamagePlayerItem(session *PlayerSession, slot int, amount int32) bool {
	return s.DamagePlayerItemWithRand(session, slot, amount, nil)
}

// DamagePlayerItemWithRand applies durability damage using rng for Unbreaking rolls.
func (s *Server) DamagePlayerItemWithRand(session *PlayerSession, slot int, amount int32, rng *rand.Rand) bool {
	if session == nil || slot < 0 || slot >= world.PlayerInventorySlots || amount <= 0 {
		return false
	}
	if session.Snapshot().GameMode == GameModeCreative {
		return false
	}
	stack := session.InventorySlot(slot)
	if stack.IsEmpty() {
		return false
	}
	meta := GetSlotItemMeta(session, slot)
	newStack, newMeta, broke := DamageItem(stack, meta, amount, rng)
	session.SetInventorySlot(slot, newStack)
	SetSlotItemMeta(session, slot, newMeta)
	if s != nil && session.send != nil {
		stateID := session.NextContainerStateID()
		_ = session.send(protocol.PlayContainerSetSlot, EncodeContainerSlot(0, stateID, slot, newStack))
	}
	return broke
}

// EnchantmentAttackBonus returns the flat melee bonus damage added by weapon
// enchantments (Sharpness, Smite against undead, Bane of Arthropods against
// arthropods) against targetEntityType.
func EnchantmentAttackBonus(meta ItemMeta, targetEntityType string) float32 {
	var bonus float32
	if sharp := meta.EnchantmentLevel("minecraft:sharpness"); sharp > 0 {
		bonus += 0.5*float32(sharp) + 0.5
	}
	if smite := meta.EnchantmentLevel("minecraft:smite"); smite > 0 && isUndeadEntityType(targetEntityType) {
		bonus += 2.5 * float32(smite)
	}
	if bane := meta.EnchantmentLevel("minecraft:bane_of_arthropods"); bane > 0 && isArthropodEntityType(targetEntityType) {
		bonus += 2.5 * float32(bane)
	}
	return bonus
}

func isUndeadEntityType(entityType string) bool {
	name := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(entityType)), "minecraft:")
	switch name {
	case "zombie", "zombie_villager", "husk", "drowned", "zombified_piglin", "zoglin",
		"skeleton", "stray", "wither_skeleton", "bogged", "wither", "phantom",
		"skeleton_horse", "zombie_horse", "zombie_nautilus":
		return true
	default:
		return false
	}
}

func isArthropodEntityType(entityType string) bool {
	name := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(entityType)), "minecraft:")
	switch name {
	case "spider", "cave_spider", "silverfish", "endermite", "bee":
		return true
	default:
		return false
	}
}

// EnchantmentKnockbackBonus returns the Knockback level on meta.
func EnchantmentKnockbackBonus(meta ItemMeta) int {
	return meta.EnchantmentLevel("minecraft:knockback")
}

// EnchantmentFireAspectSeconds returns the burn duration in seconds inflicted
// by Fire Aspect on meta (4 seconds per level).
func EnchantmentFireAspectSeconds(meta ItemMeta) int {
	lvl := meta.EnchantmentLevel("minecraft:fire_aspect")
	if lvl <= 0 {
		return 0
	}
	return lvl * 4
}

// EnchantmentLootingLevel returns the Looting level on meta.
func EnchantmentLootingLevel(meta ItemMeta) int {
	return meta.EnchantmentLevel("minecraft:looting")
}

// EnchantmentArmorProtection computes the fractional damage reduction in [0, 0.80]
// granted by equipped armor pieces against damageType, matching vanilla
// EnchantmentProtectionFactor (4% per EPF point, capped at 20 EPF = 80%).
func EnchantmentArmorProtection(armorMetas []ItemMeta, damageType string) float32 {
	dt := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(damageType)), "minecraft:")
	if dt == "out_of_world" || dt == "generic_kill" || dt == "starve" {
		return 0
	}

	isFire := dt == "in_fire" || dt == "on_fire" || dt == "lava" || dt == "hot_floor" || dt == "fireball" || dt == "unattributed_fireball"
	isFall := dt == "fall" || dt == "stalagmite"
	isBlast := dt == "explosion" || dt == "player_explosion" || dt == "fireworks"
	isProj := dt == "arrow" || dt == "trident" || dt == "mob_projectile" || dt == "spit" || dt == "thrown" || dt == "fireball" || dt == "wither_skull" || dt == "wind_charge"

	epf := 0
	for _, m := range armorMetas {
		if p := m.EnchantmentLevel("minecraft:protection"); p > 0 {
			epf += p
		}
		if isFire {
			if fp := m.EnchantmentLevel("minecraft:fire_protection"); fp > 0 {
				epf += fp * 2
			}
		}
		if isFall {
			if ff := m.EnchantmentLevel("minecraft:feather_falling"); ff > 0 {
				epf += ff * 3
			}
		}
		if isBlast {
			if bp := m.EnchantmentLevel("minecraft:blast_protection"); bp > 0 {
				epf += bp * 2
			}
		}
		if isProj {
			if pp := m.EnchantmentLevel("minecraft:projectile_protection"); pp > 0 {
				epf += pp * 2
			}
		}
	}
	if epf > 20 {
		epf = 20
	}
	return float32(epf) * 0.04
}

// EnchantmentMiningSpeedBonus returns the additive mining speed bonus from
// Efficiency (level^2 + 1 when level > 0, or 0 otherwise).
func EnchantmentMiningSpeedBonus(meta ItemMeta) float32 {
	lvl := meta.EnchantmentLevel("minecraft:efficiency")
	if lvl <= 0 {
		return 0
	}
	return float32(lvl*lvl + 1)
}

// HasSilkTouch reports whether meta has the Silk Touch enchantment.
func HasSilkTouch(meta ItemMeta) bool {
	return meta.EnchantmentLevel("minecraft:silk_touch") > 0
}

// EnchantmentFortuneLevel returns the Fortune level on meta.
func EnchantmentFortuneLevel(meta ItemMeta) int {
	return meta.EnchantmentLevel("minecraft:fortune")
}

// ApplyBlockDropEnchantments computes the item stack dropped when breaking
// blockState with a tool carrying toolMeta (handling Silk Touch and Fortune).
func ApplyBlockDropEnchantments(blockState uint16, toolMeta ItemMeta, rng *rand.Rand) (world.ItemStack, bool) {
	blockName := world.BlockName(blockState)
	if blockName == "" {
		return world.ItemStack{}, false
	}

	if HasSilkTouch(toolMeta) && isSilkTouchEligibleBlock(blockName) {
		st := world.NewNamedItemStack(blockName, 1)
		if !st.IsEmpty() && st.ResolvedItemID() > 0 {
			return st, true
		}
	}

	baseDrop, ok := world.BlockDropItem(blockState)
	if !ok {
		return world.ItemStack{}, false
	}

	fortune := EnchantmentFortuneLevel(toolMeta)
	if fortune > 0 && isFortuneOreBlock(blockName) {
		mult := int32(1)
		if rng != nil {
			// Vanilla ore drops: roll in [0, fortune+1]; values > 0 multiply by (roll + 1).
			r := rng.Intn(fortune + 2)
			if r > 0 {
				mult = int32(r + 1)
			}
		} else {
			mult = int32(fortune + 1)
		}
		count := baseDrop.Count * mult
		if count > MaxItemStackSize {
			count = MaxItemStackSize
		}
		return world.NewItemStack(baseDrop.ResolvedItemID(), count), true
	}

	return baseDrop, true
}

func isSilkTouchEligibleBlock(blockName string) bool {
	switch blockName {
	case "minecraft:stone", "minecraft:grass_block", "minecraft:Glass", "minecraft:glass",
		"minecraft:coal_ore", "minecraft:deepslate_coal_ore",
		"minecraft:iron_ore", "minecraft:deepslate_iron_ore",
		"minecraft:copper_ore", "minecraft:deepslate_copper_ore",
		"minecraft:gold_ore", "minecraft:deepslate_gold_ore",
		"minecraft:redstone_ore", "minecraft:deepslate_redstone_ore",
		"minecraft:emerald_ore", "minecraft:deepslate_emerald_ore",
		"minecraft:lapis_ore", "minecraft:deepslate_lapis_ore",
		"minecraft:diamond_ore", "minecraft:deepslate_diamond_ore",
		"minecraft:nether_gold_ore", "minecraft:nether_quartz_ore",
		"minecraft:bookshelf", "minecraft:ender_chest", "minecraft:glowstone",
		"minecraft:sea_lantern", "minecraft:ice", "minecraft:packed_ice", "minecraft:blue_ice":
		return true
	default:
		return strings.HasSuffix(blockName, "_ore") || strings.HasSuffix(blockName, "_glass")
	}
}

func isFortuneOreBlock(blockName string) bool {
	switch blockName {
	case "minecraft:coal_ore", "minecraft:deepslate_coal_ore",
		"minecraft:diamond_ore", "minecraft:deepslate_diamond_ore",
		"minecraft:emerald_ore", "minecraft:deepslate_emerald_ore",
		"minecraft:lapis_ore", "minecraft:deepslate_lapis_ore",
		"minecraft:redstone_ore", "minecraft:deepslate_redstone_ore",
		"minecraft:copper_ore", "minecraft:deepslate_copper_ore",
		"minecraft:iron_ore", "minecraft:deepslate_iron_ore",
		"minecraft:gold_ore", "minecraft:deepslate_gold_ore",
		"minecraft:nether_gold_ore", "minecraft:nether_quartz_ore",
		"minecraft:amethyst_cluster":
		return true
	default:
		return strings.HasSuffix(blockName, "_ore")
	}
}

// IsEnchantingTableBlock reports whether state is a minecraft:enchanting_table block.
func IsEnchantingTableBlock(state uint16) bool {
	return world.BlockName(state) == "minecraft:enchanting_table"
}

// CountEnchantingBookshelves counts the valid bookshelves around an enchanting
// table at (x, y, z) in cache, up to MaxEnchantingBookshelves (15).
// Following vanilla EnchantingTableBlock.BOOKSHELF_OFFSETS, bookshelves must lie
// at horizontal distance 2 (max(|dx|, |dz|) == 2) and dy in {0, 1}, with an
// unobstructed (air) intermediate block at (x + dx/2, y + dy, z + dz/2).
func CountEnchantingBookshelves(cache *world.Cache, x, y, z int) int {
	if cache == nil {
		return 0
	}
	count := 0
	for dy := 0; dy <= 1; dy++ {
		for dz := -2; dz <= 2; dz++ {
			for dx := -2; dx <= 2; dx++ {
				if absInt(dx) != 2 && absInt(dz) != 2 {
					continue
				}
				midX := x + dx/2
				midY := y + dy
				midZ := z + dz/2
				if !isEnchantingPathClear(cache.GetBlock(midX, midY, midZ)) {
					continue
				}
				if world.BlockName(cache.GetBlock(x+dx, y+dy, z+dz)) == "minecraft:bookshelf" {
					count++
					if count >= MaxEnchantingBookshelves {
						return MaxEnchantingBookshelves
					}
				}
			}
		}
	}
	return count
}

func isEnchantingPathClear(state uint16) bool {
	if state == world.StateAir {
		return true
	}
	switch world.BlockName(state) {
	case "", "minecraft:air", "minecraft:cave_air", "minecraft:void_air":
		return true
	default:
		return false
	}
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// ItemEnchantability returns the vanilla enchantment value for itemName, or 0
// if the item cannot be enchanted at an enchanting table.
func ItemEnchantability(itemName string) int {
	name := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(itemName)), "minecraft:")
	if name == "book" {
		return 1
	}

	toolEnch := map[string]int{
		"wooden_":    15,
		"stone_":     5,
		"copper_":    13,
		"iron_":      14,
		"diamond_":   10,
		"netherite_": 15,
		"golden_":    22,
	}
	for prefix, val := range toolEnch {
		if strings.HasPrefix(name, prefix) {
			switch strings.TrimPrefix(name, prefix) {
			case "sword", "pickaxe", "axe", "shovel", "hoe", "spear":
				return val
			}
		}
	}

	armorEnch := map[string]int{
		"leather_":   15,
		"chainmail_": 12,
		"copper_":    8,
		"iron_":      9,
		"diamond_":   10,
		"netherite_": 15,
		"golden_":    25,
	}
	for prefix, val := range armorEnch {
		if strings.HasPrefix(name, prefix) {
			switch strings.TrimPrefix(name, prefix) {
			case "helmet", "chestplate", "leggings", "boots":
				return val
			}
		}
	}

	switch name {
	case "turtle_helmet":
		return 9
	case "mace":
		return 15
	case "bow", "crossbow", "trident", "fishing_rod":
		return 1
	default:
		return 0
	}
}

// CalculateEnchantmentOffers deterministically computes the 3 enchanting table
// offers for stack/meta given bookshelves (0..15) and the player's enchantSeed.
func CalculateEnchantmentOffers(stack world.ItemStack, meta ItemMeta, bookshelves int, enchantSeed int32) [3]EnchantmentOffer {
	var offers [3]EnchantmentOffer
	for i := 0; i < 3; i++ {
		offers[i] = EnchantmentOffer{
			Slot:          i,
			Cost:          0,
			ClueEnchantID: -1,
			ClueLevel:     -1,
		}
	}
	if stack.IsEmpty() || stack.Count != 1 || meta.HasEnchantments() {
		return offers
	}
	itemName := stack.ResolvedID()
	enchantability := ItemEnchantability(itemName)
	if enchantability <= 0 {
		return offers
	}
	if bookshelves < 0 {
		bookshelves = 0
	} else if bookshelves > MaxEnchantingBookshelves {
		bookshelves = MaxEnchantingBookshelves
	}

	rng := rand.New(rand.NewSource(int64(uint32(enchantSeed))))
	for slot := 0; slot < 3; slot++ {
		cost := calcSlotEnchantCost(rng, slot, bookshelves)
		if cost <= 0 {
			continue
		}
		slotSeed := int64(uint32(enchantSeed)) + int64(slot)*0x9e3779b9
		enchs := selectTableEnchantments(itemName, cost, enchantability, rand.New(rand.NewSource(slotSeed)))
		if len(enchs) == 0 {
			continue
		}
		clueIdx := 0
		if len(enchs) > 1 {
			clueIdx = rand.New(rand.NewSource(slotSeed ^ 0x5f3759df)).Intn(len(enchs))
		}
		clue := enchs[clueIdx]
		clueRegID := int16(-1)
		if def, ok := EnchantmentByID(clue.ID); ok {
			clueRegID = int16(def.RegistryIndex)
		}
		offers[slot] = EnchantmentOffer{
			Slot:          slot,
			Cost:          int16(cost),
			Enchantments:  enchs,
			ClueEnchantID: clueRegID,
			ClueLevel:     int16(clue.Level),
		}
	}
	return offers
}

func calcSlotEnchantCost(rng *rand.Rand, slot, bookshelves int) int {
	base := rng.Intn(8) + 1 + (bookshelves >> 1) + rng.Intn(bookshelves+1)
	switch slot {
	case 0:
		c := base / 3
		if c < 1 {
			c = 1
		}
		return c
	case 1:
		return (base * 2) / 3 + 1
	default:
		minBottom := bookshelves * 2
		if base > minBottom {
			return base
		}
		return minBottom
	}
}

func selectTableEnchantments(itemName string, cost, enchantability int, rng *rand.Rand) []EnchantmentInstance {
	if enchantability <= 0 || cost <= 0 {
		return nil
	}
	// Vanilla modified enchantment level:
	// cost + 1 + rng.Intn(enchantability/4 + 1) + rng.Intn(enchantability/4 + 1),
	// scaled by a triangular bonus in [0.85, 1.15].
	q := enchantability/4 + 1
	modLevel := cost + 1 + rng.Intn(q) + rng.Intn(q)
	bonus := (rng.Float64() + rng.Float64() - 1.0) * 0.15
	finalLevel := int(math.Round(float64(modLevel) * (1.0 + bonus)))
	if finalLevel < 1 {
		finalLevel = 1
	}

	candidates := availableTableEnchantments(itemName, finalLevel)
	if len(candidates) == 0 {
		return nil
	}

	var chosen []EnchantmentInstance
	first, ok := pickWeightedEnchantment(candidates, rng)
	if !ok {
		return nil
	}
	chosen = append(chosen, first)

	// Chance for additional enchantments decreases as finalLevel is halved.
	for rng.Intn(50) <= finalLevel {
		// Filter out candidates incompatible with already-chosen enchantments.
		var filtered []EnchantmentInstance
		for _, cand := range candidates {
			compat := true
			for _, ex := range chosen {
				if !AreEnchantmentsCompatible(cand.ID, ex.ID) {
					compat = false
					break
				}
			}
			if compat {
				filtered = append(filtered, cand)
			}
		}
		if len(filtered) == 0 {
			break
		}
		next, ok := pickWeightedEnchantment(filtered, rng)
		if !ok {
			break
		}
		chosen = append(chosen, next)
		finalLevel /= 2
	}
	return chosen
}

func availableTableEnchantments(itemName string, modifiedLevel int) []EnchantmentInstance {
	reg := DefaultEnchantmentRegistry()
	var list []EnchantmentInstance
	for _, def := range reg.defs {
		if !def.InTable || def.Curse {
			continue
		}
		if !CanEnchantItem(def.ID, itemName, false) {
			continue
		}
		for lvl := def.MaxLevel; lvl >= 1; lvl-- {
			if modifiedLevel >= def.MinCost(lvl) && modifiedLevel <= def.MaxCost(lvl) {
				list = append(list, EnchantmentInstance{ID: def.ID, Level: lvl})
				break
			}
		}
	}
	return list
}

func pickWeightedEnchantment(candidates []EnchantmentInstance, rng *rand.Rand) (EnchantmentInstance, bool) {
	totalWeight := 0
	for _, c := range candidates {
		if def, ok := EnchantmentByID(c.ID); ok && def.Weight > 0 {
			totalWeight += def.Weight
		}
	}
	if totalWeight <= 0 {
		return EnchantmentInstance{}, false
	}
	roll := rng.Intn(totalWeight)
	for _, c := range candidates {
		def, ok := EnchantmentByID(c.ID)
		if !ok || def.Weight <= 0 {
			continue
		}
		roll -= def.Weight
		if roll < 0 {
			return c, true
		}
	}
	return candidates[0], true
}

// OpenEnchantingTable opens the Enchanting Table UI at (x, y, z) for session
// and sends open_screen, container_set_content, and the 10 enchantment data properties.
func (s *Server) OpenEnchantingTable(session *PlayerSession, x, y, z int) (int32, error) {
	if s == nil || session == nil {
		return 0, errors.New("server: nil session")
	}
	_ = s.CloseContainer(session, 0, false)

	metaTr := getItemMetaTracker(session)
	metaTr.mu.Lock()
	metaTr.enchantMeta = [2]ItemMeta{}
	metaTr.mu.Unlock()

	seed := session.EnchantmentSeed()
	tr := getContainerTracker(session)
	tr.mu.Lock()
	tr.nextWindow = (tr.nextWindow % 100) + 1
	cid := tr.nextWindow
	open := &OpenContainerState{
		ContainerID:  cid,
		MenuType:     MenuTypeEnchantment,
		BlockX:       x,
		BlockY:       y,
		BlockZ:       z,
		EnchantClues: [3]int16{-1, -1, -1},
		LevelClues:   [3]int16{-1, -1, -1},
	}
	tr.open = open
	carried := tr.carried
	tr.mu.Unlock()

	if session.send != nil {
		if err := session.send(protocol.PlayOpenScreen, EncodeOpenScreen(cid, MenuTypeEnchantment, "Enchant")); err != nil {
			return cid, err
		}
		slots := s.buildEnchantingMenuSlots(session, [2]world.ItemStack{})
		stateID := session.NextContainerStateID()
		if err := session.send(protocol.PlayContainerSetContent, EncodeContainerContent(cid, stateID, slots, carried)); err != nil {
			return cid, err
		}
		if err := s.sendEnchantingDataProperties(session, cid, [3]int16{}, int16(seed), [3]int16{-1, -1, -1}, [3]int16{-1, -1, -1}); err != nil {
			return cid, err
		}
	}
	return cid, nil
}

func (s *Server) buildEnchantingMenuSlots(session *PlayerSession, inputs [2]world.ItemStack) []world.ItemStack {
	inv := session.Inventory()
	slots := make([]world.ItemStack, EnchantingMenuSlotCount)
	slots[0] = inputs[0]
	slots[1] = inputs[1]
	copy(slots[2:29], inv[9:36])
	copy(slots[29:38], inv[36:45])
	return slots
}

func (s *Server) recomputeEnchantingOffersLocked(session *PlayerSession, tr *playerContainerTracker) {
	if tr == nil || tr.open == nil || tr.open.MenuType != MenuTypeEnchantment {
		return
	}
	metaTr := getItemMetaTracker(session)
	metaTr.mu.Lock()
	itemMeta := metaTr.enchantMeta[0].Clone()
	metaTr.mu.Unlock()

	bookshelves := 0
	if cache := s.PlayerChunks(session); cache != nil {
		bookshelves = CountEnchantingBookshelves(cache, tr.open.BlockX, tr.open.BlockY, tr.open.BlockZ)
	}
	offers := CalculateEnchantmentOffers(tr.open.EnchantSlots[0], itemMeta, bookshelves, session.EnchantmentSeed())
	for i := 0; i < 3; i++ {
		tr.open.EnchantCosts[i] = offers[i].Cost
		tr.open.EnchantClues[i] = offers[i].ClueEnchantID
		tr.open.LevelClues[i] = offers[i].ClueLevel
	}
}

func (s *Server) sendEnchantingDataProperties(
	session *PlayerSession,
	containerID int32,
	costs [3]int16,
	xpSeed int16,
	clues [3]int16,
	levels [3]int16,
) error {
	if session == nil || session.send == nil {
		return nil
	}
	props := [][2]int16{
		{EnchantDataCost0, costs[0]},
		{EnchantDataCost1, costs[1]},
		{EnchantDataCost2, costs[2]},
		{EnchantDataXPSeed, xpSeed},
		{EnchantDataClueID0, clues[0]},
		{EnchantDataClueID1, clues[1]},
		{EnchantDataClueID2, clues[2]},
		{EnchantDataLevel0, levels[0]},
		{EnchantDataLevel1, levels[1]},
		{EnchantDataLevel2, levels[2]},
	}
	for _, p := range props {
		if err := session.send(protocol.PlayContainerSetData, EncodeContainerSetData(containerID, p[0], p[1])); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) applyEnchantingTableClickLocked(session *PlayerSession, tr *playerContainerTracker, slot int, button int8, mode int32) {
	if slot < 0 || slot >= EnchantingMenuSlotCount || tr.open == nil {
		return
	}
	slots := s.buildEnchantingMenuSlots(session, tr.open.EnchantSlots)
	metas := buildEnchantingMenuMetas(session)

	switch mode {
	case ContainerInputPickup:
		carriedMeta := GetCarriedItemMeta(session)
		slots[slot], metas[slot], tr.carried, carriedMeta = applyPickupSlotWithMeta(slots[slot], metas[slot], tr.carried, carriedMeta, button)
		SetCarriedItemMeta(session, carriedMeta)
	case ContainerInputQuickMove:
		if slot < EnchantingInputSlotCount {
			slots[slot], metas[slot] = mergeIntoSlotSliceWithMeta(slots, metas, slots[slot], metas[slot], 29, 38)
			slots[slot], metas[slot] = mergeIntoSlotSliceWithMeta(slots, metas, slots[slot], metas[slot], 2, 29)
		} else {
			st := slots[slot]
			if st.ResolvedID() == "minecraft:lapis_lazuli" {
				slots[slot], metas[slot] = mergeIntoSlotSliceWithMeta(slots, metas, st, metas[slot], 1, 2)
			} else if slots[0].IsEmpty() && st.Count >= 1 {
				one := world.NewItemStack(st.ResolvedItemID(), 1)
				slots[0] = one
				metas[0] = metas[slot].Clone()
				if st.Count <= 1 {
					slots[slot] = world.ItemStack{}
					metas[slot] = ItemMeta{}
				} else {
					slots[slot] = world.NewItemStack(st.ResolvedItemID(), st.Count-1)
				}
			}
		}
	case ContainerInputSwap:
		if button >= 0 && button < 9 {
			hotbarIdx := 29 + int(button)
			slots[slot], slots[hotbarIdx] = slots[hotbarIdx], slots[slot]
			metas[slot], metas[hotbarIdx] = metas[hotbarIdx], metas[slot]
		}
	}

	tr.open.EnchantSlots[0] = slots[0]
	tr.open.EnchantSlots[1] = slots[1]
	writeBackEnchantingMenuMetas(session, slots, metas)
	s.recomputeEnchantingOffersLocked(session, tr)
}

func buildEnchantingMenuMetas(session *PlayerSession) []ItemMeta {
	metas := make([]ItemMeta, EnchantingMenuSlotCount)
	metaTr := getItemMetaTracker(session)
	metaTr.mu.Lock()
	metas[0] = metaTr.enchantMeta[0].Clone()
	metas[1] = metaTr.enchantMeta[1].Clone()
	for i := 0; i < 27; i++ {
		metas[2+i] = metaTr.invMeta[9+i].Clone()
	}
	for i := 0; i < 9; i++ {
		metas[29+i] = metaTr.invMeta[36+i].Clone()
	}
	metaTr.mu.Unlock()
	return metas
}

func writeBackEnchantingMenuMetas(session *PlayerSession, slots []world.ItemStack, metas []ItemMeta) {
	for i := 0; i < 27; i++ {
		session.SetInventorySlot(9+i, slots[2+i])
	}
	for i := 0; i < 9; i++ {
		session.SetInventorySlot(36+i, slots[29+i])
	}
	metaTr := getItemMetaTracker(session)
	metaTr.mu.Lock()
	for i := 0; i < 2; i++ {
		if slots[i].IsEmpty() {
			metaTr.enchantMeta[i] = ItemMeta{}
		} else {
			metaTr.enchantMeta[i] = metas[i].Clone()
		}
	}
	for i := 0; i < 27; i++ {
		if slots[2+i].IsEmpty() {
			metaTr.invMeta[9+i] = ItemMeta{}
		} else {
			metaTr.invMeta[9+i] = metas[2+i].Clone()
		}
	}
	for i := 0; i < 9; i++ {
		if slots[29+i].IsEmpty() {
			metaTr.invMeta[36+i] = ItemMeta{}
		} else {
			metaTr.invMeta[36+i] = metas[29+i].Clone()
		}
	}
	metaTr.mu.Unlock()
}

// DecodeContainerButtonClickPacket parses a protocol 775 serverbound
// container_button_click (0x11) packet body.
func DecodeContainerButtonClickPacket(r *protocol.Reader) (containerID, buttonID int32, err error) {
	containerID, err = r.VarInt()
	if err != nil {
		return 0, 0, err
	}
	buttonID, err = r.VarInt()
	if err != nil {
		return 0, 0, err
	}
	return containerID, buttonID, nil
}

// HandleContainerButtonClick processes a serverbound container_button_click
// packet (such as clicking enchantment row 0, 1, or 2 in an Enchanting Table).
func (s *Server) HandleContainerButtonClick(session *PlayerSession, containerID, buttonID int32) error {
	if s == nil || session == nil || session.IsDead() {
		return nil
	}
	if buttonID < 0 || buttonID > 2 {
		return nil
	}

	tr := getContainerTracker(session)
	tr.mu.Lock()
	if tr.open == nil || tr.open.ContainerID != containerID || tr.open.MenuType != MenuTypeEnchantment {
		tr.mu.Unlock()
		return nil
	}

	item := tr.open.EnchantSlots[0]
	lapis := tr.open.EnchantSlots[1]
	if item.IsEmpty() || item.Count != 1 {
		tr.mu.Unlock()
		return nil
	}

	metaTr := getItemMetaTracker(session)
	metaTr.mu.Lock()
	itemMeta := metaTr.enchantMeta[0].Clone()
	metaTr.mu.Unlock()
	if itemMeta.HasEnchantments() {
		tr.mu.Unlock()
		return nil
	}

	bookshelves := 0
	if cache := s.PlayerChunks(session); cache != nil {
		bookshelves = CountEnchantingBookshelves(cache, tr.open.BlockX, tr.open.BlockY, tr.open.BlockZ)
	}
	offers := CalculateEnchantmentOffers(item, itemMeta, bookshelves, session.EnchantmentSeed())
	offer := offers[buttonID]
	if offer.Cost <= 0 || len(offer.Enchantments) == 0 {
		tr.mu.Unlock()
		return nil
	}

	levelCost := buttonID + 1 // 1, 2, or 3 levels & lapis
	creative := session.Snapshot().GameMode == GameModeCreative
	if !creative {
		if lapis.IsEmpty() || lapis.ResolvedID() != "minecraft:lapis_lazuli" || lapis.Count < levelCost {
			tr.mu.Unlock()
			return nil
		}
		if session.ExperienceLevel() < int32(offer.Cost) {
			tr.mu.Unlock()
			return nil
		}
	}

	// Deduct lapis in survival mode.
	if !creative {
		remLapis := lapis.Count - levelCost
		if remLapis <= 0 {
			tr.open.EnchantSlots[1] = world.ItemStack{}
		} else {
			tr.open.EnchantSlots[1] = world.NewItemStack(lapis.ResolvedItemID(), remLapis)
		}
	}

	// Apply enchantments to item (converting book -> enchanted_book if needed).
	for _, ench := range offer.Enchantments {
		itemMeta.SetEnchantment(ench.ID, ench.Level)
	}
	if item.ResolvedID() == "minecraft:book" {
		if eb := world.NewNamedItemStack("minecraft:enchanted_book", 1); !eb.IsEmpty() {
			tr.open.EnchantSlots[0] = eb
		}
	}

	metaTr.mu.Lock()
	metaTr.enchantMeta[0] = itemMeta.Clone()
	metaTr.mu.Unlock()

	session.RollEnchantmentSeed()
	s.recomputeEnchantingOffersLocked(session, tr)
	tr.mu.Unlock()

	if !creative {
		_ = s.AddPlayerExperienceLevels(session, -levelCost)
	}
	return s.syncOpenContainer(session)
}

func applyPickupSlotWithMeta(
	slotStack world.ItemStack,
	slotMeta ItemMeta,
	carried world.ItemStack,
	carriedMeta ItemMeta,
	button int8,
) (newSlot world.ItemStack, newSlotMeta ItemMeta, newCarried world.ItemStack, newCarriedMeta ItemMeta) {
	if !slotStack.IsEmpty() && !carried.IsEmpty() &&
		slotStack.ResolvedItemID() == carried.ResolvedItemID() &&
		(!slotMeta.IsEmpty() || !carriedMeta.IsEmpty()) {
		// Items with distinct non-empty metadata cannot stack together; swap them on left-click.
		if button == 0 {
			return carried, carriedMeta.Clone(), slotStack, slotMeta.Clone()
		}
		return slotStack, slotMeta, carried, carriedMeta
	}

	newSlot, newCarried = applyPickupSlot(slotStack, carried, button)
	switch {
	case slotStack.IsEmpty() && !carried.IsEmpty():
		if !newSlot.IsEmpty() {
			newSlotMeta = carriedMeta.Clone()
		}
		if !newCarried.IsEmpty() {
			newCarriedMeta = carriedMeta.Clone()
		}
	case !slotStack.IsEmpty() && carried.IsEmpty():
		if !newSlot.IsEmpty() {
			newSlotMeta = slotMeta.Clone()
		}
		if !newCarried.IsEmpty() {
			newCarriedMeta = slotMeta.Clone()
		}
	case !slotStack.IsEmpty() && !carried.IsEmpty() && slotStack.ResolvedItemID() != carried.ResolvedItemID():
		newSlotMeta = carriedMeta.Clone()
		newCarriedMeta = slotMeta.Clone()
	default:
		if !newSlot.IsEmpty() {
			newSlotMeta = slotMeta.Clone()
		}
		if !newCarried.IsEmpty() {
			newCarriedMeta = carriedMeta.Clone()
		}
	}
	return newSlot, newSlotMeta, newCarried, newCarriedMeta
}

func mergeIntoSlotSliceWithMeta(
	slots []world.ItemStack,
	metas []ItemMeta,
	stack world.ItemStack,
	meta ItemMeta,
	start, end int,
) (world.ItemStack, ItemMeta) {
	if stack.IsEmpty() || start >= end {
		return stack, meta
	}
	itemID := stack.ResolvedItemID()
	maxStack := MaxItemStackSize
	if MaxItemDurability(stack.ResolvedID()) > 0 || !meta.IsEmpty() || stack.ResolvedID() == "minecraft:enchanted_book" {
		maxStack = 1
	}
	remaining := stack.Count

	if maxStack > 1 {
		for i := start; i < end && remaining > 0; i++ {
			cur := slots[i]
			if cur.IsEmpty() || cur.ResolvedItemID() != itemID || !metas[i].IsEmpty() || cur.Count >= maxStack {
				continue
			}
			space := maxStack - cur.Count
			take := remaining
			if take > space {
				take = space
			}
			slots[i] = world.NewItemStack(itemID, cur.Count+take)
			remaining -= take
		}
	}
	for i := start; i < end && remaining > 0; i++ {
		if !slots[i].IsEmpty() {
			continue
		}
		take := remaining
		if take > maxStack {
			take = maxStack
		}
		slots[i] = world.NewItemStack(itemID, take)
		metas[i] = meta.Clone()
		remaining -= take
	}
	if remaining <= 0 {
		return world.ItemStack{}, ItemMeta{}
	}
	return world.NewItemStack(itemID, remaining), meta
}
