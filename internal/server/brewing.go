package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"regionio/internal/nbt"
	"regionio/internal/protocol"
	"regionio/internal/world"
)

// Brewing stand MenuType ID in BuiltInRegistries.MENU (protocol 775 / 26.1.2).
const (
	MenuTypeBrewingStand int32 = 11 // Brewing stand (5 stand slots + 36 player slots)
)

// Brewing stand container slot indices and layout constants.
const (
	BrewingStandSlotBottle0    = 0
	BrewingStandSlotBottle1    = 1
	BrewingStandSlotBottle2    = 2
	BrewingStandSlotIngredient = 3
	BrewingStandSlotFuel       = 4
	BrewingStandSlotCount      = 5
	BrewingStandMenuSlotCount  = 41 // 5 brewing stand + 27 main inventory + 9 hotbar
)

// BrewingStandBlockEntity ContainerData property indices in
// ClientboundContainerSetDataPacket (0x13), matching BrewingStandBlockEntity.dataAccess.
const (
	BrewingDataBrewTime int16 = 0 // remaining brew ticks (400..0)
	BrewingDataFuelUses int16 = 1 // remaining blaze powder fuel charges (0..20)
)

// Vanilla 26.1.2 brewing stand timing and fuel constants.
const (
	DefaultBrewingTime     int16 = 400 // 20 seconds at 20 TPS
	BlazePowderFuelCharges int16 = 20  // 1 blaze_powder = 20 fuel charges
	MaxBrewingFuelCharges  int16 = 20
)

// Block state IDs for minecraft:brewing_stand in blocks.json (9452..9459).
const (
	brewingStandStateAllTrue  uint16 = 9452 // has_bottle_0=true,  has_bottle_1=true,  has_bottle_2=true
	brewingStandStateAllFalse uint16 = 9459 // has_bottle_0=false, has_bottle_1=false, has_bottle_2=false (default)
)

// IsBrewingStandBlock reports whether state is a minecraft:brewing_stand block.
func IsBrewingStandBlock(state uint16) bool {
	if state >= brewingStandStateAllTrue && state <= brewingStandStateAllFalse {
		return true
	}
	return world.BlockName(state) == "minecraft:brewing_stand"
}

// BrewingStandBlockState returns the block state ID for minecraft:brewing_stand
// with the given has_bottle_0, has_bottle_1, and has_bottle_2 flags.
func BrewingStandBlockState(hasBottle0, hasBottle1, hasBottle2 bool) uint16 {
	var offset uint16
	if hasBottle0 {
		offset |= 4
	}
	if hasBottle1 {
		offset |= 2
	}
	if hasBottle2 {
		offset |= 1
	}
	return brewingStandStateAllFalse - offset
}

// BrewingStandStateBottles extracts the has_bottle_0, has_bottle_1, and
// has_bottle_2 flags from a minecraft:brewing_stand block state ID.
func BrewingStandStateBottles(state uint16) (b0, b1, b2, ok bool) {
	if state < brewingStandStateAllTrue || state > brewingStandStateAllFalse {
		return false, false, false, false
	}
	bits := brewingStandStateAllFalse - state
	return (bits & 4) != 0, (bits & 2) != 0, (bits & 1) != 0, true
}

// --- Brewing Recipe Registry (Vanilla 26.1.2 PotionBrewing) ---

// BrewingMix represents one potion or container conversion rule.
type BrewingMix struct {
	From   string
	Reagent string
	To     string
}

// BrewingRecipeRegistry holds the vanilla 26.1.2 container and potion brewing mixes.
type BrewingRecipeRegistry struct {
	containerMixes []BrewingMix
	potionMixes    []BrewingMix
	reagents       map[string]bool
}

var (
	defaultBrewingOnce sync.Once
	defaultBrewingReg  *BrewingRecipeRegistry
)

// DefaultBrewingRegistry returns the singleton vanilla 26.1.2 brewing recipe registry.
func DefaultBrewingRegistry() *BrewingRecipeRegistry {
	defaultBrewingOnce.Do(func() {
		defaultBrewingReg = buildVanillaBrewingRegistry()
	})
	return defaultBrewingReg
}

func normalizeBrewingItem(name string) string {
	name = strings.TrimSpace(strings.ToLower(name))
	if name == "" {
		return ""
	}
	if !strings.Contains(name, ":") {
		return "minecraft:" + name
	}
	return name
}

func buildVanillaBrewingRegistry() *BrewingRecipeRegistry {
	reg := &BrewingRecipeRegistry{
		reagents: make(map[string]bool, 24),
	}
	addContainer := func(fromItem, reagent, toItem string) {
		f := normalizeBrewingItem(fromItem)
		r := normalizeBrewingItem(reagent)
		t := normalizeBrewingItem(toItem)
		reg.containerMixes = append(reg.containerMixes, BrewingMix{From: f, Reagent: r, To: t})
		reg.reagents[r] = true
	}
	addPotion := func(fromPotion, reagent, toPotion string) {
		f := canonicalPotionID(fromPotion)
		r := normalizeBrewingItem(reagent)
		t := canonicalPotionID(toPotion)
		reg.potionMixes = append(reg.potionMixes, BrewingMix{From: f, Reagent: r, To: t})
		reg.reagents[r] = true
	}
	addStartMix := func(reagent, effectPotion string) {
		addPotion(PotionWater, reagent, PotionMundane)
		addPotion(PotionAwkward, reagent, effectPotion)
	}

	// 1. Container conversions: potion -> splash_potion -> lingering_potion
	addContainer("minecraft:potion", "minecraft:gunpowder", "minecraft:splash_potion")
	addContainer("minecraft:splash_potion", "minecraft:dragon_breath", "minecraft:lingering_potion")

	// 2. Base mixes from water
	addPotion(PotionWater, "minecraft:nether_wart", PotionAwkward)
	addPotion(PotionWater, "minecraft:redstone", PotionMundane)
	addPotion(PotionWater, "minecraft:glowstone_dust", PotionThick)
	addPotion(PotionWater, "minecraft:fermented_spider_eye", PotionWeakness)

	// 3. Awkward -> Effect potions (and Water -> Mundane for each reagent)
	addStartMix("minecraft:golden_carrot", PotionNightVision)
	addStartMix("minecraft:magma_cream", PotionFireResistance)
	addStartMix("minecraft:rabbit_foot", PotionLeaping)
	addStartMix("minecraft:sugar", PotionSwiftness)
	addStartMix("minecraft:turtle_helmet", PotionTurtleMaster)
	addStartMix("minecraft:pufferfish", PotionWaterBreathing)
	addStartMix("minecraft:glistering_melon_slice", PotionHealing)
	addStartMix("minecraft:spider_eye", PotionPoison)
	addStartMix("minecraft:ghast_tear", PotionRegeneration)
	addStartMix("minecraft:blaze_powder", PotionStrength)
	addStartMix("minecraft:phantom_membrane", PotionSlowFalling)
	addStartMix("minecraft:breeze_rod", PotionWindCharged)
	addStartMix("minecraft:cobweb", PotionWeaving)
	addStartMix("minecraft:slime_block", PotionOozing)
	addStartMix("minecraft:stone", PotionInfested)
	addPotion(PotionAwkward, "minecraft:fermented_spider_eye", PotionWeakness)

	// 4. Redstone duration extension (base -> long_*)
	addPotion(PotionNightVision, "minecraft:redstone", PotionLongNightVision)
	addPotion(PotionInvisibility, "minecraft:redstone", PotionLongInvisibility)
	addPotion(PotionLeaping, "minecraft:redstone", PotionLongLeaping)
	addPotion(PotionFireResistance, "minecraft:redstone", PotionLongFireResistance)
	addPotion(PotionSwiftness, "minecraft:redstone", PotionLongSwiftness)
	addPotion(PotionSlowness, "minecraft:redstone", PotionLongSlowness)
	addPotion(PotionTurtleMaster, "minecraft:redstone", PotionLongTurtleMaster)
	addPotion(PotionWaterBreathing, "minecraft:redstone", PotionLongWaterBreathing)
	addPotion(PotionPoison, "minecraft:redstone", PotionLongPoison)
	addPotion(PotionRegeneration, "minecraft:redstone", PotionLongRegeneration)
	addPotion(PotionStrength, "minecraft:redstone", PotionLongStrength)
	addPotion(PotionWeakness, "minecraft:redstone", PotionLongWeakness)
	addPotion(PotionSlowFalling, "minecraft:redstone", PotionLongSlowFalling)

	// 5. Glowstone amplification (base -> strong_*)
	addPotion(PotionLeaping, "minecraft:glowstone_dust", PotionStrongLeaping)
	addPotion(PotionSwiftness, "minecraft:glowstone_dust", PotionStrongSwiftness)
	addPotion(PotionSlowness, "minecraft:glowstone_dust", PotionStrongSlowness)
	addPotion(PotionTurtleMaster, "minecraft:glowstone_dust", PotionStrongTurtleMaster)
	addPotion(PotionHealing, "minecraft:glowstone_dust", PotionStrongHealing)
	addPotion(PotionHarming, "minecraft:glowstone_dust", PotionStrongHarming)
	addPotion(PotionPoison, "minecraft:glowstone_dust", PotionStrongPoison)
	addPotion(PotionRegeneration, "minecraft:glowstone_dust", PotionStrongRegeneration)
	addPotion(PotionStrength, "minecraft:glowstone_dust", PotionStrongStrength)

	// 6. Fermented spider eye corruption
	addPotion(PotionNightVision, "minecraft:fermented_spider_eye", PotionInvisibility)
	addPotion(PotionLongNightVision, "minecraft:fermented_spider_eye", PotionLongInvisibility)
	addPotion(PotionLeaping, "minecraft:fermented_spider_eye", PotionSlowness)
	addPotion(PotionLongLeaping, "minecraft:fermented_spider_eye", PotionLongSlowness)
	addPotion(PotionSwiftness, "minecraft:fermented_spider_eye", PotionSlowness)
	addPotion(PotionLongSwiftness, "minecraft:fermented_spider_eye", PotionLongSlowness)
	addPotion(PotionHealing, "minecraft:fermented_spider_eye", PotionHarming)
	addPotion(PotionStrongHealing, "minecraft:fermented_spider_eye", PotionStrongHarming)
	addPotion(PotionPoison, "minecraft:fermented_spider_eye", PotionHarming)
	addPotion(PotionLongPoison, "minecraft:fermented_spider_eye", PotionHarming)
	addPotion(PotionStrongPoison, "minecraft:fermented_spider_eye", PotionStrongHarming)

	return reg
}

// IsIngredient reports whether itemStack is a valid brewing reagent in any
// container or potion mix.
func (r *BrewingRecipeRegistry) IsIngredient(stack world.ItemStack) bool {
	if r == nil || stack.IsEmpty() {
		return false
	}
	return r.reagents[normalizeBrewingItem(stack.ResolvedID())]
}

// Mix evaluates brewing a single bottle (bottleStack with potionType) using
// ingredient. It returns the resulting bottle item stack, resulting canonical
// potion type, and true if a valid recipe exists.
func (r *BrewingRecipeRegistry) Mix(bottleStack world.ItemStack, potionType string, ingredient world.ItemStack) (outStack world.ItemStack, outPotion string, ok bool) {
	if r == nil || bottleStack.IsEmpty() || ingredient.IsEmpty() {
		return world.ItemStack{}, "", false
	}
	if !IsPotionItem(bottleStack) {
		return world.ItemStack{}, "", false
	}
	inItem := normalizeBrewingItem(bottleStack.ResolvedID())
	reagent := normalizeBrewingItem(ingredient.ResolvedID())
	inPotion := canonicalPotionID(potionType)

	// 1. Check potion mix first (e.g. water + nether_wart -> awkward).
	for _, pm := range r.potionMixes {
		if pm.From == inPotion && pm.Reagent == reagent {
			return world.NewNamedItemStack(inItem, 1), pm.To, true
		}
	}

	// 2. Check container mix (e.g. potion + gunpowder -> splash_potion).
	for _, cm := range r.containerMixes {
		if cm.From == inItem && cm.Reagent == reagent {
			return world.NewNamedItemStack(cm.To, 1), inPotion, true
		}
	}

	return world.ItemStack{}, "", false
}

// IsBrewingIngredient reports whether stack can be placed in the top ingredient
// slot (slot 3) of a brewing stand.
func IsBrewingIngredient(stack world.ItemStack) bool {
	return DefaultBrewingRegistry().IsIngredient(stack)
}

// IsBrewingFuel reports whether stack can be placed in the fuel slot (slot 4)
// of a brewing stand (minecraft:blaze_powder).
func IsBrewingFuel(stack world.ItemStack) bool {
	return !stack.IsEmpty() && normalizeBrewingItem(stack.ResolvedID()) == "minecraft:blaze_powder"
}

// CanPlaceInBrewingBottleSlot reports whether stack is allowed in one of the
// three bottle slots (0, 1, 2) of a brewing stand, matching
// BrewingStandMenu$PotionSlot.mayPlaceItem.
func CanPlaceInBrewingBottleSlot(stack world.ItemStack) bool {
	if stack.IsEmpty() {
		return false
	}
	switch normalizeBrewingItem(stack.ResolvedID()) {
	case "minecraft:potion", "minecraft:splash_potion", "minecraft:lingering_potion", "minecraft:glass_bottle":
		return true
	default:
		return false
	}
}

// BrewPotion computes the output bottle stack and potion ID when brewing
// bottleStack (having potionType) with ingredient.
func BrewPotion(bottleStack world.ItemStack, potionType string, ingredient world.ItemStack) (world.ItemStack, string, bool) {
	return DefaultBrewingRegistry().Mix(bottleStack, potionType, ingredient)
}

// --- BrewingStandBlockEntity State & Persistence ---

// BrewingStandBlockEntity represents the persistent and runtime state of a
// minecraft:brewing_stand block entity at (X, Y, Z).
type BrewingStandBlockEntity struct {
	ID          string
	X           int
	Y           int
	Z           int
	Items       [BrewingStandSlotCount]world.ItemStack
	PotionTypes [3]string // canonical potion IDs for bottle slots 0..2
	BrewTime    int16     // remaining ticks in current brew (400..0)
	Fuel        int16     // remaining fuel charges (0..20)
	ingredient  string    // item ID of ingredient when brewing started
	lastBrewer  *PlayerSession
}

// Clone returns a deep copy of b.
func (b BrewingStandBlockEntity) Clone() BrewingStandBlockEntity {
	cp := b
	if cp.ID == "" {
		cp.ID = "minecraft:brewing_stand"
	}
	for i := 0; i < 3; i++ {
		if IsPotionItem(cp.Items[i]) {
			cp.PotionTypes[i] = canonicalPotionID(cp.PotionTypes[i])
		} else {
			cp.PotionTypes[i] = ""
		}
	}
	return cp
}

// EncodeBrewingStandBlockEntityNBT serializes b into a vanilla 26.1.2
// brewing_stand block entity NBT compound.
func EncodeBrewingStandBlockEntityNBT(b BrewingStandBlockEntity) *nbt.Compound {
	b = b.Clone()
	c := nbt.NewCompound().
		Set("id", nbt.String("minecraft:brewing_stand")).
		Set("x", nbt.Int(b.X)).
		Set("y", nbt.Int(b.Y)).
		Set("z", nbt.Int(b.Z)).
		Set("BrewTime", nbt.Short(b.BrewTime)).
		Set("Fuel", nbt.Byte(b.Fuel))

	var items []nbt.Tag
	for slot, st := range b.Items {
		if st.IsEmpty() {
			continue
		}
		itemID := st.ResolvedID()
		if itemID == "" || itemID == "minecraft:air" {
			continue
		}
		itemComp := nbt.NewCompound().
			Set("Slot", nbt.Byte(slot)).
			Set("id", nbt.String(itemID)).
			Set("count", nbt.Int(st.Count))
		if slot < 3 && IsPotionItem(st) {
			pType := b.PotionTypes[slot]
			if pType == "" {
				pType = PotionWater
			}
			itemComp.Set("Potion", nbt.String(canonicalPotionID(pType)))
		}
		items = append(items, itemComp)
	}
	c.Set("Items", nbt.List{
		ElemID: nbt.TagCompound,
		Elems:  items,
	})
	return c
}

// DecodeBrewingStandBlockEntityNBT parses a vanilla 26.1.2 brewing_stand block
// entity NBT compound.
func DecodeBrewingStandBlockEntityNBT(c *nbt.Compound) (BrewingStandBlockEntity, bool) {
	if c == nil {
		return BrewingStandBlockEntity{}, false
	}
	idTag, ok := c.Get("id")
	if !ok {
		return BrewingStandBlockEntity{}, false
	}
	idStr, ok := idTag.(nbt.String)
	if !ok || normalizeBrewingItem(string(idStr)) != "minecraft:brewing_stand" {
		return BrewingStandBlockEntity{}, false
	}

	b := BrewingStandBlockEntity{
		ID: "minecraft:brewing_stand",
	}
	if xTag, ok := c.Get("x"); ok {
		if v, ok := xTag.(nbt.Int); ok {
			b.X = int(v)
		}
	}
	if yTag, ok := c.Get("y"); ok {
		if v, ok := yTag.(nbt.Int); ok {
			b.Y = int(v)
		}
	}
	if zTag, ok := c.Get("z"); ok {
		if v, ok := zTag.(nbt.Int); ok {
			b.Z = int(v)
		}
	}
	if btTag, ok := c.Get("BrewTime"); ok {
		switch v := btTag.(type) {
		case nbt.Short:
			b.BrewTime = int16(v)
		case nbt.Int:
			b.BrewTime = int16(v)
		}
	}
	if fuelTag, ok := c.Get("Fuel"); ok {
		switch v := fuelTag.(type) {
		case nbt.Byte:
			b.Fuel = int16(uint8(v))
		case nbt.Short:
			b.Fuel = int16(v)
		case nbt.Int:
			b.Fuel = int16(v)
		}
	}

	if itemsTag, ok := c.Get("Items"); ok {
		var listElems []nbt.Tag
		switch v := itemsTag.(type) {
		case nbt.List:
			listElems = v.Elems
		case *nbt.List:
			if v != nil {
				listElems = v.Elems
			}
		}
		for _, elem := range listElems {
			ic, ok := elem.(*nbt.Compound)
			if !ok {
				continue
			}
			slotTag, hasSlot := ic.Get("Slot")
			idTag, hasID := ic.Get("id")
			if !hasSlot || !hasID {
				continue
			}
			slotByte, ok1 := slotTag.(nbt.Byte)
			itemName, ok2 := idTag.(nbt.String)
			if !ok1 || !ok2 {
				continue
			}
			slot := int(uint8(slotByte))
			if slot < 0 || slot >= BrewingStandSlotCount {
				continue
			}
			count := int32(1)
			if ct, ok := ic.Get("count"); ok {
				switch v := ct.(type) {
				case nbt.Int:
					count = int32(v)
				case nbt.Byte:
					count = int32(v)
				}
			} else if ct, ok := ic.Get("Count"); ok {
				if v, ok := ct.(nbt.Byte); ok {
					count = int32(v)
				}
			}
			if count <= 0 {
				continue
			}
			st := world.NewNamedItemStack(string(itemName), count)
			if st.IsEmpty() {
				continue
			}
			b.Items[slot] = st
			if slot < 3 && IsPotionItem(st) {
				pType := PotionWater
				if pt, ok := ic.Get("Potion"); ok {
					if ps, ok := pt.(nbt.String); ok && string(ps) != "" {
						pType = canonicalPotionID(string(ps))
					}
				}
				b.PotionTypes[slot] = pType
			}
		}
	}
	if !b.Items[BrewingStandSlotIngredient].IsEmpty() {
		b.ingredient = b.Items[BrewingStandSlotIngredient].ResolvedID()
	}
	return b, true
}

type brewingKey struct {
	x, y, z int
}

type serverBrewingState struct {
	mu          sync.Mutex
	loadedDisk  bool
	stands      map[brewingKey]*BrewingStandBlockEntity
}

var serverBrewingStands sync.Map // map[*Server]*serverBrewingState

func (s *Server) brewingState() *serverBrewingState {
	if v, ok := serverBrewingStands.Load(s); ok {
		return v.(*serverBrewingState)
	}
	actual, _ := serverBrewingStands.LoadOrStore(s, &serverBrewingState{
		stands: make(map[brewingKey]*BrewingStandBlockEntity),
	})
	return actual.(*serverBrewingState)
}

func (s *Server) brewingSidecarPath() string {
	if s == nil || s.cfg.WorldDir == "" {
		return ""
	}
	return filepath.Join(s.cfg.WorldDir, "region", "brewing_stands.nbt")
}

func (s *Server) ensureBrewingLoadedLocked(bs *serverBrewingState) {
	if bs.loadedDisk {
		return
	}
	bs.loadedDisk = true
	path := s.brewingSidecarPath()
	if path == "" {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	_, parsedTag, err := nbt.UnmarshalNamed(data)
	if err != nil {
		return
	}
	root, ok := parsedTag.(*nbt.Compound)
	if !ok || root == nil {
		return
	}
	tag, ok := root.Get("block_entities")
	if !ok {
		return
	}
	var listElems []nbt.Tag
	switch v := tag.(type) {
	case nbt.List:
		listElems = v.Elems
	case *nbt.List:
		if v != nil {
			listElems = v.Elems
		}
	default:
		return
	}
	for _, elem := range listElems {
		comp, ok := elem.(*nbt.Compound)
		if !ok {
			continue
		}
		if b, valid := DecodeBrewingStandBlockEntityNBT(comp); valid {
			key := brewingKey{b.X, b.Y, b.Z}
			if _, exists := bs.stands[key]; !exists {
				cp := b.Clone()
				bs.stands[key] = &cp
			}
		}
	}
}

// SaveBrewingStands persists all tracked brewing stand block entities to disk
// when world persistence is enabled.
func (s *Server) SaveBrewingStands() error {
	if s == nil {
		return nil
	}
	path := s.brewingSidecarPath()
	if path == "" {
		return nil
	}
	bs := s.brewingState()
	bs.mu.Lock()
	s.ensureBrewingLoadedLocked(bs)
	list := make([]BrewingStandBlockEntity, 0, len(bs.stands))
	for _, stand := range bs.stands {
		if stand != nil {
			list = append(list, stand.Clone())
		}
	}
	bs.mu.Unlock()

	sort.Slice(list, func(i, j int) bool {
		if list[i].Y != list[j].Y {
			return list[i].Y < list[j].Y
		}
		if list[i].Z != list[j].Z {
			return list[i].Z < list[j].Z
		}
		return list[i].X < list[j].X
	})

	elems := make([]nbt.Tag, 0, len(list))
	for _, stand := range list {
		elems = append(elems, EncodeBrewingStandBlockEntityNBT(stand))
	}
	root := nbt.NewCompound().Set("block_entities", nbt.List{
		ElemID: nbt.TagCompound,
		Elems:  elems,
	})
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, nbt.MarshalNamed("", root), 0o644)
}

func (s *Server) getOrCreateBrewingStandLocked(bs *serverBrewingState, x, y, z int) *BrewingStandBlockEntity {
	s.ensureBrewingLoadedLocked(bs)
	key := brewingKey{x, y, z}
	b := bs.stands[key]
	if b == nil {
		b = &BrewingStandBlockEntity{
			ID: "minecraft:brewing_stand",
			X:  x,
			Y:  y,
			Z:  z,
		}
		bs.stands[key] = b
	}
	return b
}

// BrewingStandAt returns a snapshot of the brewing stand block entity at (x, y, z).
func (s *Server) BrewingStandAt(x, y, z int) (BrewingStandBlockEntity, bool) {
	if s == nil {
		return BrewingStandBlockEntity{}, false
	}
	bs := s.brewingState()
	bs.mu.Lock()
	s.ensureBrewingLoadedLocked(bs)
	b := bs.stands[brewingKey{x, y, z}]
	if b == nil {
		bs.mu.Unlock()
		if s.chunks != nil && IsBrewingStandBlock(s.chunks.GetBlock(x, y, z)) {
			return BrewingStandBlockEntity{ID: "minecraft:brewing_stand", X: x, Y: y, Z: z}, true
		}
		return BrewingStandBlockEntity{}, false
	}
	cp := b.Clone()
	bs.mu.Unlock()
	return cp, true
}

// SetBrewingStandState replaces or inserts the brewing stand block entity at
// (b.X, b.Y, b.Z) and persists it.
func (s *Server) SetBrewingStandState(b BrewingStandBlockEntity) {
	if s == nil {
		return
	}
	bs := s.brewingState()
	bs.mu.Lock()
	s.ensureBrewingLoadedLocked(bs)
	cp := b.Clone()
	if !cp.Items[BrewingStandSlotIngredient].IsEmpty() {
		cp.ingredient = cp.Items[BrewingStandSlotIngredient].ResolvedID()
	}
	bs.stands[brewingKey{b.X, b.Y, b.Z}] = &cp
	bs.mu.Unlock()

	s.updateBrewingStandBottleBlockState(b.X, b.Y, b.Z, cp.Items)
	_ = s.SaveBrewingStands()
}

// BrewingStandSlots returns a copy of the 5 slots in the brewing stand at (x, y, z).
func (s *Server) BrewingStandSlots(x, y, z int) [BrewingStandSlotCount]world.ItemStack {
	b, ok := s.BrewingStandAt(x, y, z)
	if !ok {
		return [BrewingStandSlotCount]world.ItemStack{}
	}
	return b.Items
}

// GetBrewingSlotPotionType returns the canonical potion ID of bottle slot (0..2)
// in the brewing stand at (x, y, z).
func (s *Server) GetBrewingSlotPotionType(x, y, z, slot int) string {
	if s == nil || slot < 0 || slot >= 3 {
		return ""
	}
	b, ok := s.BrewingStandAt(x, y, z)
	if !ok || !IsPotionItem(b.Items[slot]) {
		return ""
	}
	if b.PotionTypes[slot] == "" {
		return PotionWater
	}
	return canonicalPotionID(b.PotionTypes[slot])
}

// SetBrewingSlot sets slot (0..4) in the brewing stand at (x, y, z) along with
// an optional potionType for bottle slots (0..2).
func (s *Server) SetBrewingSlot(x, y, z, slot int, stack world.ItemStack, potionType string) bool {
	if s == nil || slot < 0 || slot >= BrewingStandSlotCount {
		return false
	}
	var normalized world.ItemStack
	if !stack.IsEmpty() {
		count := stack.Count
		if slot < 3 && count > 1 {
			count = 1
		}
		normalized = world.NewItemStack(stack.ResolvedItemID(), count)
	}

	bs := s.brewingState()
	bs.mu.Lock()
	b := s.getOrCreateBrewingStandLocked(bs, x, y, z)
	b.Items[slot] = normalized
	if slot < 3 {
		if IsPotionItem(normalized) {
			if potionType == "" {
				if b.PotionTypes[slot] == "" {
					b.PotionTypes[slot] = PotionWater
				}
			} else {
				b.PotionTypes[slot] = canonicalPotionID(potionType)
			}
		} else {
			b.PotionTypes[slot] = ""
		}
	} else if slot == BrewingStandSlotIngredient {
		if normalized.IsEmpty() || normalized.ResolvedID() != b.ingredient {
			b.BrewTime = 0
			b.ingredient = normalized.ResolvedID()
		}
	}
	itemsCopy := b.Items
	bs.mu.Unlock()

	s.updateBrewingStandBottleBlockState(x, y, z, itemsCopy)
	_ = s.SaveBrewingStands()
	return true
}

func (s *Server) updateBrewingStandBottleBlockState(x, y, z int, items [BrewingStandSlotCount]world.ItemStack) {
	if s == nil || s.chunks == nil {
		return
	}
	curState := s.chunks.GetBlock(x, y, z)
	if !IsBrewingStandBlock(curState) {
		return
	}
	nextState := BrewingStandBlockState(!items[0].IsEmpty(), !items[1].IsEmpty(), !items[2].IsEmpty())
	if nextState != curState {
		if valid, lightChunks := s.chunks.SetBlockWithLight(x, y, z, nextState); valid {
			s.broadcastBlockAndLightUpdate(x, y, z, nextState, lightChunks)
		}
	}
}

// DropBrewingStandContents removes the brewing stand block entity at (x, y, z),
// closes any open brewing stand screens targeting it, and drops its non-empty
// slots as item entities in the world.
func (s *Server) DropBrewingStandContents(x, y, z int) {
	if s == nil {
		return
	}
	bs := s.brewingState()
	bs.mu.Lock()
	s.ensureBrewingLoadedLocked(bs)
	key := brewingKey{x, y, z}
	b := bs.stands[key]
	delete(bs.stands, key)
	bs.mu.Unlock()

	s.playersMu.RLock()
	sessions := make([]*PlayerSession, 0, len(s.players))
	for _, sess := range s.players {
		sessions = append(sessions, sess)
	}
	s.playersMu.RUnlock()
	for _, sess := range sessions {
		open := sess.OpenContainer()
		if open != nil && open.MenuType == MenuTypeBrewingStand &&
			open.BlockX == x && open.BlockY == y && open.BlockZ == z {
			_ = s.CloseContainer(sess, open.ContainerID, true)
		}
	}

	if b != nil {
		for _, st := range b.Items {
			if !st.IsEmpty() {
				s.SpawnItemEntity(float64(x)+0.5, float64(y)+0.25, float64(z)+0.5, st)
			}
		}
		_ = s.SaveBrewingStands()
	}
}

// --- Brewing Tick Logic (Vanilla 26.1.2 BrewingStandBlockEntity.serverTick) ---

// TickBrewingStandAt advances the brewing stand at (x, y, z) by one game tick:
//   - Consumes 1 blaze_powder in slot 4 to add 20 fuel charges when fuel == 0.
//   - Starts a 400-tick brew when fuel > 0 and canBrew is true (consuming 1 fuel charge).
//   - Decrements BrewTime each tick while canBrew remains valid; on reaching 0,
//     applies the brewing recipe to bottle slots 0..2, consumes 1 ingredient,
//     and triggers minecraft:nether/brew_potion.
func (s *Server) TickBrewingStandAt(x, y, z int) bool {
	if s == nil {
		return false
	}
	if s.chunks != nil {
		state := s.chunks.GetBlock(x, y, z)
		if !IsBrewingStandBlock(state) {
			s.DropBrewingStandContents(x, y, z)
			return false
		}
	}

	bs := s.brewingState()
	bs.mu.Lock()
	b := s.getOrCreateBrewingStandLocked(bs, x, y, z)
	changed, slotsChanged, brewedPotions, brewer, dropDragonBottle := stepBrewingStandTickLocked(b)
	itemsCopy := b.Items
	bs.mu.Unlock()

	if dropDragonBottle {
		s.SpawnItemEntity(float64(x)+0.5, float64(y)+0.25, float64(z)+0.5, world.NewNamedItemStack("minecraft:glass_bottle", 1))
	}
	if slotsChanged {
		s.updateBrewingStandBottleBlockState(x, y, z, itemsCopy)
	}
	if len(brewedPotions) > 0 {
		s.notifyBrewedPotionAdvancement(brewer, x, y, z, brewedPotions)
	}
	if changed {
		_ = s.SaveBrewingStands()
	}
	if changed || slotsChanged {
		s.broadcastBrewingStandProgressToViewers(x, y, z, slotsChanged)
	}
	return changed
}

// TickBrewingStands advances all tracked brewing stands by one game tick and
// returns the number of stands whose state changed.
func (s *Server) TickBrewingStands() int {
	if s == nil {
		return 0
	}
	bs := s.brewingState()
	bs.mu.Lock()
	s.ensureBrewingLoadedLocked(bs)
	keys := make([]brewingKey, 0, len(bs.stands))
	for k := range bs.stands {
		keys = append(keys, k)
	}
	bs.mu.Unlock()

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
		if s.TickBrewingStandAt(k.x, k.y, k.z) {
			changedCount++
		}
	}
	return changedCount
}

// StartBrewingStandTicker runs a 20 TPS (50ms) background goroutine ticking all
// brewing stands until ctx is cancelled, then saves brewing stand state.
func (s *Server) StartBrewingStandTicker(ctx context.Context) <-chan struct{} {
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
				_ = s.SaveBrewingStands()
				return
			case <-ticker.C:
				s.TickBrewingStands()
			}
		}
	}()
	return done
}

func canBrewLocked(b *BrewingStandBlockEntity) bool {
	ing := b.Items[BrewingStandSlotIngredient]
	if ing.IsEmpty() || !IsBrewingIngredient(ing) {
		return false
	}
	reg := DefaultBrewingRegistry()
	for i := 0; i < 3; i++ {
		bottle := b.Items[i]
		if bottle.IsEmpty() {
			continue
		}
		pType := b.PotionTypes[i]
		if pType == "" {
			pType = PotionWater
		}
		if _, _, ok := reg.Mix(bottle, pType, ing); ok {
			return true
		}
	}
	return false
}

func stepBrewingStandTickLocked(b *BrewingStandBlockEntity) (
	changed, slotsChanged bool,
	brewedPotions []string,
	brewer *PlayerSession,
	dropDragonBottle bool,
) {
	// 1. Refill fuel from blaze_powder in slot 4 when fuel == 0.
	fuelStack := b.Items[BrewingStandSlotFuel]
	if b.Fuel <= 0 && IsBrewingFuel(fuelStack) {
		b.Fuel = BlazePowderFuelCharges
		rem := fuelStack.Count - 1
		if rem <= 0 {
			b.Items[BrewingStandSlotFuel] = world.ItemStack{}
		} else {
			b.Items[BrewingStandSlotFuel] = world.NewItemStack(fuelStack.ResolvedItemID(), rem)
		}
		changed = true
		slotsChanged = true
	}

	 brewable := canBrewLocked(b)
	isBrewing := b.BrewTime > 0
	ing := b.Items[BrewingStandSlotIngredient]

	if isBrewing {
		b.BrewTime--
		changed = true
		if b.BrewTime == 0 && brewable {
			brewedPotions, dropDragonBottle = performBrewLocked(b)
			brewer = b.lastBrewer
			slotsChanged = true
		} else if !brewable || ing.IsEmpty() || ing.ResolvedID() != b.ingredient {
			b.BrewTime = 0
		}
	} else if brewable && b.Fuel > 0 {
		b.Fuel--
		b.BrewTime = DefaultBrewingTime
		b.ingredient = ing.ResolvedID()
		changed = true
	}

	return changed, slotsChanged, brewedPotions, brewer, dropDragonBottle
}

func performBrewLocked(b *BrewingStandBlockEntity) (brewedPotions []string, dropDragonBottle bool) {
	ing := b.Items[BrewingStandSlotIngredient]
	if ing.IsEmpty() {
		return nil, false
	}
	reg := DefaultBrewingRegistry()
	for i := 0; i < 3; i++ {
		bottle := b.Items[i]
		if bottle.IsEmpty() {
			continue
		}
		pType := b.PotionTypes[i]
		if pType == "" {
			pType = PotionWater
		}
		if outStack, outPotion, ok := reg.Mix(bottle, pType, ing); ok {
			b.Items[i] = outStack
			b.PotionTypes[i] = outPotion
			brewedPotions = append(brewedPotions, outPotion)
		}
	}

	wasDragonBreath := normalizeBrewingItem(ing.ResolvedID()) == "minecraft:dragon_breath"
	rem := ing.Count - 1
	if rem <= 0 {
		if wasDragonBreath {
			b.Items[BrewingStandSlotIngredient] = world.NewNamedItemStack("minecraft:glass_bottle", 1)
		} else {
			b.Items[BrewingStandSlotIngredient] = world.ItemStack{}
		}
	} else {
		b.Items[BrewingStandSlotIngredient] = world.NewItemStack(ing.ResolvedItemID(), rem)
		if wasDragonBreath {
			dropDragonBottle = true
		}
	}
	b.ingredient = b.Items[BrewingStandSlotIngredient].ResolvedID()
	return brewedPotions, dropDragonBottle
}

func (s *Server) notifyBrewedPotionAdvancement(brewer *PlayerSession, bx, by, bz int, potions []string) {
	if s == nil || len(potions) == 0 {
		return
	}
	notified := make(map[*PlayerSession]bool)
	if brewer != nil {
		for _, pID := range potions {
			s.TriggerBrewedPotion(brewer, pID)
		}
		notified[brewer] = true
	}

	s.playersMu.RLock()
	sessions := make([]*PlayerSession, 0, len(s.players))
	for _, sess := range s.players {
		sessions = append(sessions, sess)
	}
	s.playersMu.RUnlock()

	for _, sess := range sessions {
		if notified[sess] {
			continue
		}
		open := sess.OpenContainer()
		if open != nil && open.MenuType == MenuTypeBrewingStand &&
			open.BlockX == bx && open.BlockY == by && open.BlockZ == bz {
			for _, pID := range potions {
				s.TriggerBrewedPotion(sess, pID)
			}
			notified[sess] = true
		}
	}
}

// --- Brewing Stand Container UI (Open / Click / Sync) ---

// OpenBrewingStand opens the brewing stand UI at (x, y, z) for session and
// sends open_screen (MenuTypeBrewingStand = 11), container_set_content, and
// the two container_set_data properties (brew time and fuel charges).
func (s *Server) OpenBrewingStand(session *PlayerSession, x, y, z int) (int32, error) {
	if s == nil || session == nil {
		return 0, errors.New("server: nil session")
	}
	_ = s.CloseContainer(session, 0, false)

	bs := s.brewingState()
	bs.mu.Lock()
	b := s.getOrCreateBrewingStandLocked(bs, x, y, z)
	b.lastBrewer = session
	bs.mu.Unlock()

	tr := getContainerTracker(session)
	tr.mu.Lock()
	tr.nextWindow = (tr.nextWindow % 100) + 1
	cid := tr.nextWindow
	tr.open = &OpenContainerState{
		ContainerID: cid,
		MenuType:    MenuTypeBrewingStand,
		BlockX:      x,
		BlockY:      y,
		BlockZ:      z,
	}
	carried := tr.carried
	tr.mu.Unlock()

	if session.send != nil {
		if err := session.send(protocol.PlayOpenScreen, EncodeOpenScreen(cid, MenuTypeBrewingStand, "Brewing Stand")); err != nil {
			return cid, err
		}
		slots := s.buildBrewingStandMenuSlots(session, x, y, z)
		stateID := session.NextContainerStateID()
		if err := session.send(protocol.PlayContainerSetContent, EncodeContainerContent(cid, stateID, slots, carried)); err != nil {
			return cid, err
		}
		if err := s.sendBrewingStandDataProperties(session, cid, x, y, z); err != nil {
			return cid, err
		}
	}
	return cid, nil
}

func (s *Server) buildBrewingStandMenuSlots(session *PlayerSession, x, y, z int) []world.ItemStack {
	standSlots := s.BrewingStandSlots(x, y, z)
	inv := session.Inventory()
	slots := make([]world.ItemStack, BrewingStandMenuSlotCount)
	copy(slots[0:5], standSlots[:])
	copy(slots[5:32], inv[9:36])
	copy(slots[32:41], inv[36:45])
	return slots
}

func (s *Server) sendBrewingStandDataProperties(session *PlayerSession, containerID int32, x, y, z int) error {
	if session == nil || session.send == nil {
		return nil
	}
	b, _ := s.BrewingStandAt(x, y, z)
	if err := session.send(protocol.PlayContainerSetData, EncodeContainerSetData(containerID, BrewingDataBrewTime, b.BrewTime)); err != nil {
		return err
	}
	return session.send(protocol.PlayContainerSetData, EncodeContainerSetData(containerID, BrewingDataFuelUses, b.Fuel))
}

// HandleBrewingStandClick processes a ContainerClick on an open brewing stand
// screen, enforcing bottle/ingredient/fuel slot constraints, preserving potion
// type metadata across bottle moves, and syncing viewers.
func (s *Server) HandleBrewingStandClick(session *PlayerSession, click ContainerClick) error {
	if s == nil || session == nil || session.IsDead() {
		return nil
	}
	tr := getContainerTracker(session)
	tr.mu.Lock()
	if tr.open == nil || tr.open.ContainerID != click.ContainerID || tr.open.MenuType != MenuTypeBrewingStand {
		tr.mu.Unlock()
		return nil
	}
	bx, by, bz := tr.open.BlockX, tr.open.BlockY, tr.open.BlockZ
	slot := int(click.Slot)

	if slot == -999 {
		if !tr.carried.IsEmpty() {
			dropCount := tr.carried.Count
			if click.Button == 1 {
				dropCount = 1
			}
			dropStack := world.NewItemStack(tr.carried.ResolvedItemID(), dropCount)
			rem := tr.carried.Count - dropCount
			if rem <= 0 {
				tr.carried = world.ItemStack{}
				SetCarriedPotionType(session, "")
			} else {
				tr.carried = world.NewItemStack(tr.carried.ResolvedItemID(), rem)
			}
			snap := session.Snapshot()
			s.SpawnItemEntity(snap.X, snap.Y+0.5, snap.Z, dropStack)
		}
		tr.mu.Unlock()
		return s.syncBrewingStandViewers(session, bx, by, bz)
	}

	s.applyBrewingStandClickLocked(session, tr, bx, by, bz, slot, click.Button, click.Mode)
	tr.mu.Unlock()
	return s.syncBrewingStandViewers(session, bx, by, bz)
}

func (s *Server) applyBrewingStandClickLocked(
	session *PlayerSession,
	tr *playerContainerTracker,
	bx, by, bz, slot int,
	button int8,
	mode int32,
) {
	if slot < 0 || slot >= BrewingStandMenuSlotCount {
		return
	}

	bs := s.brewingState()
	bs.mu.Lock()
	stand := s.getOrCreateBrewingStandLocked(bs, bx, by, bz)
	stand.lastBrewer = session
	slots := s.buildBrewingStandMenuSlots(session, bx, by, bz)
	var slotPotions [BrewingStandMenuSlotCount]string
	for i := 0; i < 3; i++ {
		slotPotions[i] = stand.PotionTypes[i]
	}
	bs.mu.Unlock()

	for i := 0; i < 27; i++ {
		slotPotions[5+i] = GetSlotPotionType(session, 9+i)
	}
	for i := 0; i < 9; i++ {
		slotPotions[32+i] = GetSlotPotionType(session, 36+i)
	}
	carriedPotion := GetCarriedPotionType(session)

	switch mode {
	case ContainerInputPickup:
		switch {
		case slot >= 0 && slot < 3:
			// Bottle slot (0..2): max stack size = 1, only potions / glass_bottle.
			if tr.carried.IsEmpty() {
				if !slots[slot].IsEmpty() {
					takenPotion := slotPotions[slot]
					tr.carried = slots[slot]
					carriedPotion = takenPotion
					slots[slot] = world.ItemStack{}
					slotPotions[slot] = ""
					if IsPotionItem(tr.carried) && takenPotion != "" && takenPotion != PotionWater {
						s.TriggerBrewedPotion(session, takenPotion)
					}
				}
			} else if CanPlaceInBrewingBottleSlot(tr.carried) {
				if slots[slot].IsEmpty() {
					slots[slot] = world.NewItemStack(tr.carried.ResolvedItemID(), 1)
					slotPotions[slot] = carriedPotion
					rem := tr.carried.Count - 1
					if rem <= 0 {
						tr.carried = world.ItemStack{}
						carriedPotion = ""
					} else {
						tr.carried = world.NewItemStack(tr.carried.ResolvedItemID(), rem)
					}
				} else if tr.carried.Count == 1 {
					slots[slot], tr.carried = tr.carried, slots[slot]
					slotPotions[slot], carriedPotion = carriedPotion, slotPotions[slot]
				}
			}

		case slot == BrewingStandSlotIngredient:
			if tr.carried.IsEmpty() || IsBrewingIngredient(tr.carried) {
				slots[slot], tr.carried = applyPickupSlot(slots[slot], tr.carried, button)
			}

		case slot == BrewingStandSlotFuel:
			if tr.carried.IsEmpty() || IsBrewingFuel(tr.carried) {
				slots[slot], tr.carried = applyPickupSlot(slots[slot], tr.carried, button)
			}

		default:
			prevSlotEmpty := slots[slot].IsEmpty()
			prevCarriedEmpty := tr.carried.IsEmpty()
			slots[slot], tr.carried = applyPickupSlot(slots[slot], tr.carried, button)
			if prevSlotEmpty && !slots[slot].IsEmpty() {
				slotPotions[slot] = carriedPotion
				if tr.carried.IsEmpty() {
					carriedPotion = ""
				}
			} else if prevCarriedEmpty && !tr.carried.IsEmpty() {
				carriedPotion = slotPotions[slot]
				if slots[slot].IsEmpty() {
					slotPotions[slot] = ""
				}
			} else if !slots[slot].IsEmpty() && !tr.carried.IsEmpty() {
				slotPotions[slot], carriedPotion = carriedPotion, slotPotions[slot]
			}
		}

	case ContainerInputQuickMove:
		if slot < BrewingStandSlotCount {
			// Move from brewing stand slot (0..4) -> player inventory (5..40).
			movedPotion := slotPotions[slot]
			wasBottleSlot := slot < 3 && IsPotionItem(slots[slot])
			for dst := 5; dst < BrewingStandMenuSlotCount && !slots[slot].IsEmpty(); dst++ {
				if slots[dst].IsEmpty() {
					slots[dst] = slots[slot]
					slotPotions[dst] = movedPotion
					slots[slot] = world.ItemStack{}
					slotPotions[slot] = ""
					if wasBottleSlot && movedPotion != "" && movedPotion != PotionWater {
						s.TriggerBrewedPotion(session, movedPotion)
					}
					break
				}
			}
		} else {
			// Move from player inventory (5..40) -> brewing stand (fuel, ingredient, or bottle).
			clicked := slots[slot]
			clickedPotion := slotPotions[slot]
			if !clicked.IsEmpty() {
				switch {
				case IsBrewingFuel(clicked):
					slots[slot] = mergeIntoSlotSlice(slots, clicked, BrewingStandSlotFuel, BrewingStandSlotFuel+1)
				case IsBrewingIngredient(clicked):
					slots[slot] = mergeIntoSlotSlice(slots, clicked, BrewingStandSlotIngredient, BrewingStandSlotIngredient+1)
				case CanPlaceInBrewingBottleSlot(clicked):
					for bSlot := 0; bSlot < 3 && !slots[slot].IsEmpty(); bSlot++ {
						if slots[bSlot].IsEmpty() {
							slots[bSlot] = world.NewItemStack(slots[slot].ResolvedItemID(), 1)
							slotPotions[bSlot] = clickedPotion
							rem := slots[slot].Count - 1
							if rem <= 0 {
								slots[slot] = world.ItemStack{}
								slotPotions[slot] = ""
							} else {
								slots[slot] = world.NewItemStack(slots[slot].ResolvedItemID(), rem)
							}
						}
					}
				}
			}
		}
	}

	// Write updated slots back to the brewing stand and player inventory.
	for i := 0; i < BrewingStandSlotCount; i++ {
		pType := ""
		if i < 3 {
			pType = slotPotions[i]
		}
		s.SetBrewingSlot(bx, by, bz, i, slots[i], pType)
	}
	for i := 0; i < 27; i++ {
		session.SetInventorySlot(9+i, slots[5+i])
		SetSlotPotionType(session, 9+i, slotPotions[5+i])
	}
	for i := 0; i < 9; i++ {
		session.SetInventorySlot(36+i, slots[32+i])
		SetSlotPotionType(session, 36+i, slotPotions[32+i])
	}
	SetCarriedPotionType(session, carriedPotion)
}

func (s *Server) syncBrewingStandViewers(actor *PlayerSession, bx, by, bz int) error {
	s.playersMu.RLock()
	sessions := make([]*PlayerSession, 0, len(s.players))
	for _, sess := range s.players {
		sessions = append(sessions, sess)
	}
	s.playersMu.RUnlock()

	var firstErr error
	for _, sess := range sessions {
		open := sess.OpenContainer()
		if open != nil && open.MenuType == MenuTypeBrewingStand &&
			open.BlockX == bx && open.BlockY == by && open.BlockZ == bz {
			if err := s.syncOpenBrewingStand(sess); err != nil && firstErr == nil {
				firstErr = err
			}
		} else if sess == actor {
			if err := s.syncOpenBrewingStand(sess); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

func (s *Server) syncOpenBrewingStand(session *PlayerSession) error {
	if s == nil || session == nil || session.send == nil {
		return nil
	}
	open := session.OpenContainer()
	if open == nil || open.MenuType != MenuTypeBrewingStand {
		return nil
	}
	slots := s.buildBrewingStandMenuSlots(session, open.BlockX, open.BlockY, open.BlockZ)
	carried := session.CarriedItem()
	stateID := session.NextContainerStateID()
	if err := session.send(protocol.PlayContainerSetContent, EncodeContainerContent(open.ContainerID, stateID, slots, carried)); err != nil {
		return err
	}
	return s.sendBrewingStandDataProperties(session, open.ContainerID, open.BlockX, open.BlockY, open.BlockZ)
}

func (s *Server) broadcastBrewingStandProgressToViewers(bx, by, bz int, slotsChanged bool) {
	s.playersMu.RLock()
	sessions := make([]*PlayerSession, 0, len(s.players))
	for _, sess := range s.players {
		sessions = append(sessions, sess)
	}
	s.playersMu.RUnlock()

	for _, sess := range sessions {
		open := sess.OpenContainer()
		if open == nil || open.MenuType != MenuTypeBrewingStand ||
			open.BlockX != bx || open.BlockY != by || open.BlockZ != bz {
			continue
		}
		if slotsChanged {
			_ = s.syncOpenBrewingStand(sess)
		} else {
			_ = s.sendBrewingStandDataProperties(sess, open.ContainerID, bx, by, bz)
		}
	}
}
