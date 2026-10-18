package server

import (
	"context"
	"errors"
	"math"
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

// Vanilla 26.1.2 brewing stand timing, fuel, sound, level event, and particle constants.
const (
	DefaultBrewingTime     int16 = 400 // 20 seconds at 20 TPS
	BlazePowderFuelCharges int16 = 20  // 1 blaze_powder = 20 fuel charges
	MaxBrewingFuelCharges  int16 = 20

	// LevelEventSoundBrewingStand is the vanilla LevelEvent (1035) broadcast in
	// BrewingStandBlockEntity.doBrew when a brewing cycle finishes.
	LevelEventSoundBrewingStand int32 = 1035

	// SoundBrewingStandBrew is the vanilla sound event played when a brewing cycle finishes.
	SoundBrewingStandBrew = "minecraft:block.brewing_stand.brew"

	// ParticleTypeBubbleID ("minecraft:bubble" = 4) and ParticleTypeSmokeID
	// ("minecraft:smoke" = 72) in BuiltInRegistries.PARTICLE_TYPE (protocol 775).
	ParticleTypeBubbleID int32 = 4
	ParticleTypeSmokeID  int32 = 72
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

// EncodeBrewingStandBubbleParticlePacket encodes a ClientboundLevelParticlesPacket
// (0x2f in protocol 775) for the brewing stand's rising bubble/smoke particles.
func EncodeBrewingStandBubbleParticlePacket(x, y, z int, brewTime int16) []byte {
	phase := float64(brewTime%20) / 20.0
	px := float64(x) + 0.4 + 0.2*phase
	py := float64(y) + 0.7 + 0.2*phase
	pz := float64(z) + 0.4 + 0.2*(1.0-phase)

	w := protocol.NewWriter(48)
	w.Bool(false) // overrideLimiter
	w.Bool(false) // alwaysShow
	w.Float64(px)
	w.Float64(py)
	w.Float64(pz)
	w.Float32(0.05) // xDist
	w.Float32(0.08) // yDist
	w.Float32(0.05) // zDist
	w.Float32(0.01) // maxSpeed
	w.Int32(2)      // count
	w.VarInt(ParticleTypeSmokeID)
	return w.Bytes()
}

// --- Brewing Recipe Registry (Vanilla 26.1.2 PotionBrewing) ---

// BrewingMix represents one potion or container conversion rule.
type BrewingMix struct {
	From    string
	Reagent string
	To      string
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

// ContainerMixes returns a copy of all registered container mixes.
func (r *BrewingRecipeRegistry) ContainerMixes() []BrewingMix {
	if r == nil {
		return nil
	}
	out := make([]BrewingMix, len(r.containerMixes))
	copy(out, r.containerMixes)
	return out
}

// PotionMixes returns a copy of all registered potion mixes.
func (r *BrewingRecipeRegistry) PotionMixes() []BrewingMix {
	if r == nil {
		return nil
	}
	out := make([]BrewingMix, len(r.potionMixes))
	copy(out, r.potionMixes)
	return out
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

// --- Hopper WorldlyContainer Rules & Redstone Comparator Signal ---

// BrewingStandSlotsForFace returns the slot indices accessible to a hopper for
// the given face of the brewing stand block ("up" / "top", "down" / "bottom",
// or any horizontal side "north" / "south" / "west" / "east" / "side"),
// matching vanilla BrewingStandBlockEntity.getSlotsForFace:
//   - "up": {3} (ingredient slot)
//   - "down": {0, 1, 2, 3} (3 bottle slots + ingredient slot for leftover glass_bottle)
//   - sides: {4, 0, 1, 2} (fuel slot 4 first, then bottle slots 0..2)
func BrewingStandSlotsForFace(face string) []int {
	switch strings.ToLower(strings.TrimSpace(face)) {
	case "up", "top":
		return []int{BrewingStandSlotIngredient}
	case "down", "bottom":
		return []int{
			BrewingStandSlotBottle0,
			BrewingStandSlotBottle1,
			BrewingStandSlotBottle2,
			BrewingStandSlotIngredient,
		}
	default:
		return []int{
			BrewingStandSlotFuel,
			BrewingStandSlotBottle0,
			BrewingStandSlotBottle1,
			BrewingStandSlotBottle2,
		}
	}
}

// CanPlaceInBrewingSlot reports whether stack can be inserted into slot (0..4)
// of a brewing stand given the current contents of that slot (existing),
// matching BrewingStandBlockEntity.canPlaceItem.
func CanPlaceInBrewingSlot(slot int, stack world.ItemStack, existing world.ItemStack) bool {
	if stack.IsEmpty() {
		return false
	}
	switch slot {
	case BrewingStandSlotIngredient:
		if !IsBrewingIngredient(stack) {
			return false
		}
		maxSt := brewingItemMaxStackSize(stack)
		return existing.IsEmpty() || (existing.ResolvedItemID() == stack.ResolvedItemID() && existing.Count < maxSt)
	case BrewingStandSlotFuel:
		if !IsBrewingFuel(stack) {
			return false
		}
		return existing.IsEmpty() || (existing.ResolvedItemID() == stack.ResolvedItemID() && existing.Count < MaxItemStackSize)
	case BrewingStandSlotBottle0, BrewingStandSlotBottle1, BrewingStandSlotBottle2:
		return CanPlaceInBrewingBottleSlot(stack) && existing.IsEmpty()
	default:
		return false
	}
}

// CanTakeFromBrewingSlot reports whether a hopper below a brewing stand can
// extract stack from slot (0..4), matching BrewingStandBlockEntity.canTakeItemThroughFace:
//   - Bottle slots (0..2): any non-empty item can be extracted.
//   - Ingredient slot (3): only a leftover `minecraft:glass_bottle` (left behind
//     after brewing with `minecraft:dragon_breath`) or non-ingredient remainder
//     can be extracted from below.
func CanTakeFromBrewingSlot(slot int, stack world.ItemStack) bool {
	if stack.IsEmpty() {
		return false
	}
	switch slot {
	case BrewingStandSlotBottle0, BrewingStandSlotBottle1, BrewingStandSlotBottle2:
		return true
	case BrewingStandSlotIngredient:
		return normalizeBrewingItem(stack.ResolvedID()) == "minecraft:glass_bottle" || !IsBrewingIngredient(stack)
	default:
		return false
	}
}

func brewingItemMaxStackSize(stack world.ItemStack) int32 {
	if stack.IsEmpty() {
		return MaxItemStackSize
	}
	if IsPotionItem(stack) {
		return 1
	}
	switch normalizeBrewingItem(stack.ResolvedID()) {
	case "minecraft:turtle_helmet":
		return 1
	default:
		return MaxItemStackSize
	}
}

// BrewingStandComparatorSignalFromItems computes the vanilla redstone comparator
// signal (0..15) for the 5 slots of a brewing stand, matching
// AbstractContainerMenu.getRedstoneSignalFromContainer:
//   - 0 when all 5 slots are empty
//   - floor((sum(count_i / maxStack_i) / 5.0) * 14.0) + 1 otherwise
func BrewingStandComparatorSignalFromItems(items [BrewingStandSlotCount]world.ItemStack) int {
	var totalRatio float64
	nonEmpty := false
	for slot, st := range items {
		if st.IsEmpty() || st.Count <= 0 {
			continue
		}
		nonEmpty = true
		maxStack := float64(brewingItemMaxStackSize(st))
		if slot < 3 && maxStack > 1 {
			// Bottle slots only hold 1 bottle at a time in BrewingStandMenu,
			// while potions themselves have maxStackSize == 1.
			maxStack = 1
		}
		ratio := float64(st.Count) / maxStack
		if ratio > 1.0 {
			ratio = 1.0
		}
		totalRatio += ratio
	}
	if !nonEmpty {
		return 0
	}
	sig := int(math.Floor((totalRatio/float64(BrewingStandSlotCount))*14.0)) + 1
	if sig > world.MaxRedstonePower {
		sig = world.MaxRedstonePower
	}
	return sig
}

// BrewingStandComparatorSignal returns the redstone comparator signal (0..15)
// for a BrewingStandBlockEntity.
func BrewingStandComparatorSignal(b BrewingStandBlockEntity) int {
	return BrewingStandComparatorSignalFromItems(b.Items)
}

// BrewingStandComparatorSignalAt returns the redstone comparator signal (0..15)
// for a brewing stand at (x, y, z) in dim.
func (s *Server) BrewingStandComparatorSignalAt(dim string, x, y, z int) int {
	if s == nil {
		return 0
	}
	normDim := world.NormalizeDimension(dim)
	cache := s.DimensionChunks(normDim)
	if cache != nil && !IsBrewingStandBlock(cache.GetBlock(x, y, z)) {
		return 0
	}
	b, ok := s.BrewingStandAtInDim(normDim, x, y, z)
	if !ok {
		return 0
	}
	return BrewingStandComparatorSignal(b)
}

// --- BrewingStandBlockEntity State & Persistence ---

// BrewingStandBlockEntity represents the persistent and runtime state of a
// minecraft:brewing_stand block entity at (Dimension, X, Y, Z).
type BrewingStandBlockEntity struct {
	ID          string
	Dimension   string
	X           int
	Y           int
	Z           int
	CustomName  string
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
	if cp.Dimension == "" {
		cp.Dimension = world.DimensionOverworld
	} else {
		cp.Dimension = world.NormalizeDimension(cp.Dimension)
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
		Set("dimension", nbt.String(b.Dimension)).
		Set("x", nbt.Int(b.X)).
		Set("y", nbt.Int(b.Y)).
		Set("z", nbt.Int(b.Z)).
		Set("BrewTime", nbt.Short(b.BrewTime)).
		Set("Fuel", nbt.Byte(b.Fuel))
	if b.CustomName != "" {
		c.Set("CustomName", nbt.String(b.CustomName))
	}

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
		ID:        "minecraft:brewing_stand",
		Dimension: world.DimensionOverworld,
	}
	if dimTag, ok := c.Get("dimension"); ok {
		if ds, ok := dimTag.(nbt.String); ok && string(ds) != "" {
			b.Dimension = world.NormalizeDimension(string(ds))
		}
	}
	if cnTag, ok := c.Get("CustomName"); ok {
		if cs, ok := cnTag.(nbt.String); ok {
			b.CustomName = string(cs)
		}
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
	dim     string
	x, y, z int
}

func makeBrewingKey(dim string, x, y, z int) brewingKey {
	return brewingKey{
		dim: world.NormalizeDimension(dim),
		x:   x,
		y:   y,
		z:   z,
	}
}

type serverBrewingState struct {
	mu         sync.Mutex
	loadedDisk bool
	stands     map[brewingKey]*BrewingStandBlockEntity
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
			key := makeBrewingKey(b.Dimension, b.X, b.Y, b.Z)
			if _, exists := bs.stands[key]; !exists {
				cp := b.Clone()
				bs.stands[key] = &cp
			}
		}
	}
}

// LoadBrewingStands loads all persisted brewing stand block entities from disk
// and synchronizes their `has_bottle_0..2` block state properties and comparators.
func (s *Server) LoadBrewingStands() error {
	if s == nil {
		return nil
	}
	bs := s.brewingState()
	bs.mu.Lock()
	bs.loadedDisk = false
	s.ensureBrewingLoadedLocked(bs)
	var loaded []BrewingStandBlockEntity
	for _, stand := range bs.stands {
		if stand != nil {
			loaded = append(loaded, stand.Clone())
		}
	}
	bs.mu.Unlock()

	for _, stand := range loaded {
		s.BindNoteBlockAndJukeboxRedstone(stand.Dimension)
		s.updateBrewingStandBottleBlockStateInDim(stand.Dimension, stand.X, stand.Y, stand.Z, stand.Items)
		s.updateHopperComparatorsAndBroadcast(stand.Dimension, stand.X, stand.Y, stand.Z)
	}
	return nil
}

// ChunkBrewingStandBlockEntitiesNBT returns vanilla `block_entities` NBT compounds
// for all brewing stands in chunk (cx, cz) of dim.
func (s *Server) ChunkBrewingStandBlockEntitiesNBT(dim string, cx, cz int32) []*nbt.Compound {
	if s == nil {
		return nil
	}
	normDim := world.NormalizeDimension(dim)
	bs := s.brewingState()
	bs.mu.Lock()
	s.ensureBrewingLoadedLocked(bs)
	var out []*nbt.Compound
	for k, stand := range bs.stands {
		if k.dim != normDim || stand == nil {
			continue
		}
		if int32(k.x>>4) == cx && int32(k.z>>4) == cz {
			out = append(out, EncodeBrewingStandBlockEntityNBT(stand.Clone()))
		}
	}
	bs.mu.Unlock()
	return out
}

// UnloadChunkBrewingStands removes all brewing stands in chunk (cx, cz) of dim
// from runtime memory and returns their serialized NBT compounds.
func (s *Server) UnloadChunkBrewingStands(dim string, cx, cz int32) []*nbt.Compound {
	if s == nil {
		return nil
	}
	normDim := world.NormalizeDimension(dim)
	bs := s.brewingState()
	bs.mu.Lock()
	defer bs.mu.Unlock()
	var out []*nbt.Compound
	for k, stand := range bs.stands {
		if k.dim != normDim {
			continue
		}
		if int32(k.x>>4) == cx && int32(k.z>>4) == cz {
			if stand != nil {
				out = append(out, EncodeBrewingStandBlockEntityNBT(stand.Clone()))
			}
			delete(bs.stands, k)
		}
	}
	return out
}

// LoadChunkBrewingStandBlockEntitiesNBT loads brewing stand block entities from
// a chunk's `block_entities` NBT list and syncs their `has_bottle_0..2` block states.
func (s *Server) LoadChunkBrewingStandBlockEntitiesNBT(dim string, list []*nbt.Compound) int {
	if s == nil || len(list) == 0 {
		return 0
	}
	normDim := world.NormalizeDimension(dim)
	bs := s.brewingState()
	bs.mu.Lock()
	var loaded []BrewingStandBlockEntity
	for _, c := range list {
		b, valid := DecodeBrewingStandBlockEntityNBT(c)
		if !valid {
			continue
		}
		if b.Dimension == "" {
			b.Dimension = normDim
		} else {
			b.Dimension = world.NormalizeDimension(b.Dimension)
		}
		key := makeBrewingKey(b.Dimension, b.X, b.Y, b.Z)
		cp := b.Clone()
		bs.stands[key] = &cp
		loaded = append(loaded, cp)
	}
	bs.mu.Unlock()

	for _, stand := range loaded {
		s.BindNoteBlockAndJukeboxRedstone(stand.Dimension)
		s.updateBrewingStandBottleBlockStateInDim(stand.Dimension, stand.X, stand.Y, stand.Z, stand.Items)
		s.updateHopperComparatorsAndBroadcast(stand.Dimension, stand.X, stand.Y, stand.Z)
	}
	return len(loaded)
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
		if list[i].Dimension != list[j].Dimension {
			return list[i].Dimension < list[j].Dimension
		}
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

func (s *Server) getOrCreateBrewingStandInDimLocked(bs *serverBrewingState, dim string, x, y, z int) *BrewingStandBlockEntity {
	s.ensureBrewingLoadedLocked(bs)
	key := makeBrewingKey(dim, x, y, z)
	b := bs.stands[key]
	if b == nil {
		b = &BrewingStandBlockEntity{
			ID:        "minecraft:brewing_stand",
			Dimension: key.dim,
			X:         x,
			Y:         y,
			Z:         z,
		}
		bs.stands[key] = b
	}
	return b
}

func (s *Server) getOrCreateBrewingStandLocked(bs *serverBrewingState, x, y, z int) *BrewingStandBlockEntity {
	return s.getOrCreateBrewingStandInDimLocked(bs, world.DimensionOverworld, x, y, z)
}

// BrewingStandAtInDim returns a snapshot of the brewing stand block entity at
// (x, y, z) in dim.
func (s *Server) BrewingStandAtInDim(dim string, x, y, z int) (BrewingStandBlockEntity, bool) {
	if s == nil {
		return BrewingStandBlockEntity{}, false
	}
	normDim := world.NormalizeDimension(dim)
	bs := s.brewingState()
	bs.mu.Lock()
	s.ensureBrewingLoadedLocked(bs)
	b := bs.stands[makeBrewingKey(normDim, x, y, z)]
	if b == nil {
		bs.mu.Unlock()
		cache := s.DimensionChunks(normDim)
		if cache != nil && IsBrewingStandBlock(cache.GetBlock(x, y, z)) {
			return BrewingStandBlockEntity{
				ID:        "minecraft:brewing_stand",
				Dimension: normDim,
				X:         x,
				Y:         y,
				Z:         z,
			}, true
		}
		return BrewingStandBlockEntity{}, false
	}
	cp := b.Clone()
	bs.mu.Unlock()
	return cp, true
}

// BrewingStandAt returns a snapshot of the brewing stand block entity at (x, y, z)
// in the Overworld.
func (s *Server) BrewingStandAt(x, y, z int) (BrewingStandBlockEntity, bool) {
	return s.BrewingStandAtInDim(world.DimensionOverworld, x, y, z)
}

// SetBrewingStandState replaces or inserts the brewing stand block entity at
// (b.Dimension, b.X, b.Y, b.Z) and persists it.
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
	key := makeBrewingKey(cp.Dimension, cp.X, cp.Y, cp.Z)
	bs.stands[key] = &cp
	bs.mu.Unlock()

	s.updateBrewingStandBottleBlockStateInDim(key.dim, cp.X, cp.Y, cp.Z, cp.Items)
	_ = s.SaveBrewingStands()
}

// OnBrewingStandPlacedInDim initializes a brewing stand block entity when a
// brewing stand is placed at (x, y, z) in dim.
func (s *Server) OnBrewingStandPlacedInDim(dim string, x, y, z int, customName string) {
	if s == nil {
		return
	}
	normDim := world.NormalizeDimension(dim)
	if cache := s.DimensionChunks(normDim); cache != nil && !IsBrewingStandBlock(cache.GetBlock(x, y, z)) {
		cache.SetBlock(x, y, z, BrewingStandBlockState(false, false, false))
	}
	bs := s.brewingState()
	bs.mu.Lock()
	b := s.getOrCreateBrewingStandInDimLocked(bs, normDim, x, y, z)
	if customName != "" {
		b.CustomName = customName
	}
	itemsCopy := b.Items
	bs.mu.Unlock()
	s.updateBrewingStandBottleBlockStateInDim(normDim, x, y, z, itemsCopy)
	_ = s.SaveBrewingStands()
}

// OnBrewingStandPlaced initializes a brewing stand block entity in the Overworld.
func (s *Server) OnBrewingStandPlaced(x, y, z int, customName string) {
	s.OnBrewingStandPlacedInDim(world.DimensionOverworld, x, y, z, customName)
}

// BrewingStandSlotsInDim returns a copy of the 5 slots in the brewing stand at
// (x, y, z) in dim.
func (s *Server) BrewingStandSlotsInDim(dim string, x, y, z int) [BrewingStandSlotCount]world.ItemStack {
	b, ok := s.BrewingStandAtInDim(dim, x, y, z)
	if !ok {
		return [BrewingStandSlotCount]world.ItemStack{}
	}
	return b.Items
}

// BrewingStandSlots returns a copy of the 5 slots in the brewing stand at (x, y, z).
func (s *Server) BrewingStandSlots(x, y, z int) [BrewingStandSlotCount]world.ItemStack {
	return s.BrewingStandSlotsInDim(world.DimensionOverworld, x, y, z)
}

// GetBrewingSlotPotionTypeInDim returns the canonical potion ID of bottle slot
// (0..2) in the brewing stand at (x, y, z) in dim.
func (s *Server) GetBrewingSlotPotionTypeInDim(dim string, x, y, z, slot int) string {
	if s == nil || slot < 0 || slot >= 3 {
		return ""
	}
	b, ok := s.BrewingStandAtInDim(dim, x, y, z)
	if !ok || !IsPotionItem(b.Items[slot]) {
		return ""
	}
	if b.PotionTypes[slot] == "" {
		return PotionWater
	}
	return canonicalPotionID(b.PotionTypes[slot])
}

// GetBrewingSlotPotionType returns the canonical potion ID of bottle slot (0..2)
// in the brewing stand at (x, y, z).
func (s *Server) GetBrewingSlotPotionType(x, y, z, slot int) string {
	return s.GetBrewingSlotPotionTypeInDim(world.DimensionOverworld, x, y, z, slot)
}

// SetBrewingSlotInDim sets slot (0..4) in the brewing stand at (x, y, z) in dim
// along with an optional potionType for bottle slots (0..2).
func (s *Server) SetBrewingSlotInDim(dim string, x, y, z, slot int, stack world.ItemStack, potionType string) bool {
	if s == nil || slot < 0 || slot >= BrewingStandSlotCount {
		return false
	}
	normDim := world.NormalizeDimension(dim)
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
	b := s.getOrCreateBrewingStandInDimLocked(bs, normDim, x, y, z)
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

	s.updateBrewingStandBottleBlockStateInDim(normDim, x, y, z, itemsCopy)
	_ = s.SaveBrewingStands()
	return true
}

// SetBrewingSlot sets slot (0..4) in the brewing stand at (x, y, z) along with
// an optional potionType for bottle slots (0..2).
func (s *Server) SetBrewingSlot(x, y, z, slot int, stack world.ItemStack, potionType string) bool {
	return s.SetBrewingSlotInDim(world.DimensionOverworld, x, y, z, slot, stack, potionType)
}

func (s *Server) updateBrewingStandBottleBlockStateInDim(dim string, x, y, z int, items [BrewingStandSlotCount]world.ItemStack) {
	if s == nil {
		return
	}
	normDim := world.NormalizeDimension(dim)
	cache := s.DimensionChunks(normDim)
	if cache == nil {
		return
	}
	curState := cache.GetBlock(x, y, z)
	if !IsBrewingStandBlock(curState) {
		cache.UpdateComparatorsAround(x, y, z)
		return
	}
	nextState := BrewingStandBlockState(!items[0].IsEmpty(), !items[1].IsEmpty(), !items[2].IsEmpty())
	if nextState != curState {
		if valid, lightChunks := cache.SetBlockWithLight(x, y, z, nextState); valid {
			s.broadcastBlockAndLightUpdate(x, y, z, nextState, lightChunks)
		} else {
			cache.SetBlock(x, y, z, nextState)
		}
	}
	cache.UpdateComparatorsAround(x, y, z)
}

func (s *Server) updateBrewingStandBottleBlockState(x, y, z int, items [BrewingStandSlotCount]world.ItemStack) {
	s.updateBrewingStandBottleBlockStateInDim(world.DimensionOverworld, x, y, z, items)
}

// DropBrewingStandContentsInDim removes the brewing stand block entity at
// (x, y, z) in dim, closes any open brewing stand screens targeting it, drops
// its non-empty slots as item entities in the world, and updates comparators.
func (s *Server) DropBrewingStandContentsInDim(dim string, x, y, z int) {
	if s == nil {
		return
	}
	normDim := world.NormalizeDimension(dim)
	bs := s.brewingState()
	bs.mu.Lock()
	s.ensureBrewingLoadedLocked(bs)
	key := makeBrewingKey(normDim, x, y, z)
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
				s.SpawnItemEntityInDimension(normDim, float64(x)+0.5, float64(y)+0.25, float64(z)+0.5, st)
			}
		}
		_ = s.SaveBrewingStands()
	}
	if cache := s.DimensionChunks(normDim); cache != nil {
		cache.UpdateComparatorsAround(x, y, z)
	}
}

// DropBrewingStandContents removes the brewing stand block entity at (x, y, z)
// in the Overworld, closes any open brewing stand screens targeting it, and
// drops its non-empty slots as item entities in the world.
func (s *Server) DropBrewingStandContents(x, y, z int) {
	s.DropBrewingStandContentsInDim(world.DimensionOverworld, x, y, z)
}

// --- Hopper Insert & Extract Helpers (Vanilla 26.1.2 WorldlyContainer) ---

// HopperTryInsertIntoBrewingStand attempts to insert 1 item from stack (having
// optional potionType when stack is a potion) into the brewing stand at
// (x, y, z) in dim through fromFace ("up" = ingredient slot 3;
// "north"/"south"/"west"/"east"/"side" = fuel slot 4 or bottle slots 0..2).
// Returns (true, remainingStack) if 1 item was inserted, or (false, stack) otherwise.
func (s *Server) HopperTryInsertIntoBrewingStand(
	dim string,
	x, y, z int,
	fromFace string,
	stack world.ItemStack,
	potionType string,
) (inserted bool, remaining world.ItemStack) {
	if s == nil || stack.IsEmpty() || stack.Count <= 0 {
		return false, stack
	}
	normDim := world.NormalizeDimension(dim)
	candidateSlots := BrewingStandSlotsForFace(fromFace)
	if len(candidateSlots) == 0 {
		return false, stack
	}

	bs := s.brewingState()
	bs.mu.Lock()
	b := s.getOrCreateBrewingStandInDimLocked(bs, normDim, x, y, z)

	targetSlot := -1
	for _, slot := range candidateSlots {
		// Hoppers cannot insert through the bottom face ("down").
		if strings.EqualFold(strings.TrimSpace(fromFace), "down") || strings.EqualFold(strings.TrimSpace(fromFace), "bottom") {
			break
		}
		if CanPlaceInBrewingSlot(slot, stack, b.Items[slot]) {
			targetSlot = slot
			break
		}
	}
	if targetSlot < 0 {
		bs.mu.Unlock()
		return false, stack
	}

	cur := b.Items[targetSlot]
	if cur.IsEmpty() {
		b.Items[targetSlot] = world.NewItemStack(stack.ResolvedItemID(), 1)
	} else {
		b.Items[targetSlot] = world.NewItemStack(cur.ResolvedItemID(), cur.Count+1)
	}
	if targetSlot < 3 {
		if IsPotionItem(b.Items[targetSlot]) {
			if potionType == "" {
				b.PotionTypes[targetSlot] = PotionWater
			} else {
				b.PotionTypes[targetSlot] = canonicalPotionID(potionType)
			}
		} else {
			b.PotionTypes[targetSlot] = ""
		}
	} else if targetSlot == BrewingStandSlotIngredient {
		if b.ingredient == "" {
			b.ingredient = b.Items[targetSlot].ResolvedID()
		}
	}
	itemsCopy := b.Items
	bs.mu.Unlock()

	remCount := stack.Count - 1
	if remCount > 0 {
		remaining = world.NewItemStack(stack.ResolvedItemID(), remCount)
	}
	s.updateBrewingStandBottleBlockStateInDim(normDim, x, y, z, itemsCopy)
	s.broadcastBrewingStandProgressToViewers(x, y, z, true)
	_ = s.SaveBrewingStands()
	return true, remaining
}

// HopperTryInsertIntoBrewingStandAt attempts to insert 1 item from stack into
// the Overworld brewing stand at (x, y, z) through fromFace.
func (s *Server) HopperTryInsertIntoBrewingStandAt(
	x, y, z int,
	fromFace string,
	stack world.ItemStack,
	potionType string,
) (inserted bool, remaining world.ItemStack) {
	return s.HopperTryInsertIntoBrewingStand(world.DimensionOverworld, x, y, z, fromFace, stack, potionType)
}

// HopperTryExtractFromBrewingStand attempts to extract 1 item from the bottom
// ("down" face: slots 0, 1, 2, 3) of the brewing stand at (x, y, z) in dim.
// Returns the extracted 1-item stack, its canonical potionType (if a potion),
// and true on success.
func (s *Server) HopperTryExtractFromBrewingStand(
	dim string,
	x, y, z int,
) (extracted world.ItemStack, potionType string, ok bool) {
	if s == nil {
		return world.ItemStack{}, "", false
	}
	normDim := world.NormalizeDimension(dim)
	downSlots := BrewingStandSlotsForFace("down")

	bs := s.brewingState()
	bs.mu.Lock()
	s.ensureBrewingLoadedLocked(bs)
	b := bs.stands[makeBrewingKey(normDim, x, y, z)]
	if b == nil {
		bs.mu.Unlock()
		return world.ItemStack{}, "", false
	}

	targetSlot := -1
	for _, slot := range downSlots {
		if CanTakeFromBrewingSlot(slot, b.Items[slot]) {
			targetSlot = slot
			break
		}
	}
	if targetSlot < 0 {
		bs.mu.Unlock()
		return world.ItemStack{}, "", false
	}

	cur := b.Items[targetSlot]
	extracted = world.NewItemStack(cur.ResolvedItemID(), 1)
	if targetSlot < 3 && IsPotionItem(cur) {
		potionType = b.PotionTypes[targetSlot]
		if potionType == "" {
			potionType = PotionWater
		} else {
			potionType = canonicalPotionID(potionType)
		}
	}
	rem := cur.Count - 1
	if rem <= 0 {
		b.Items[targetSlot] = world.ItemStack{}
		if targetSlot < 3 {
			b.PotionTypes[targetSlot] = ""
		} else if targetSlot == BrewingStandSlotIngredient {
			b.ingredient = ""
		}
	} else {
		b.Items[targetSlot] = world.NewItemStack(cur.ResolvedItemID(), rem)
	}
	itemsCopy := b.Items
	bs.mu.Unlock()

	s.updateBrewingStandBottleBlockStateInDim(normDim, x, y, z, itemsCopy)
	s.broadcastBrewingStandProgressToViewers(x, y, z, true)
	_ = s.SaveBrewingStands()
	return extracted, potionType, true
}

// HopperTryExtractFromBrewingStandAt attempts to extract 1 item from the bottom
// of the Overworld brewing stand at (x, y, z).
func (s *Server) HopperTryExtractFromBrewingStandAt(x, y, z int) (extracted world.ItemStack, potionType string, ok bool) {
	return s.HopperTryExtractFromBrewingStand(world.DimensionOverworld, x, y, z)
}

// --- Brewing Tick Logic (Vanilla 26.1.2 BrewingStandBlockEntity.serverTick) ---

// TickBrewingStandAtInDim advances the brewing stand at (x, y, z) in dim by one
// game tick:
//   - Consumes 1 blaze_powder in slot 4 to add 20 fuel charges when fuel == 0.
//   - Starts a 400-tick brew when fuel > 0 and canBrew is true (consuming 1 fuel charge).
//   - Emits periodic bubble/smoke particles while actively brewing.
//   - Decrements BrewTime each tick while canBrew remains valid; on reaching 0,
//     applies the brewing recipe to bottle slots 0..2, consumes 1 ingredient,
//     broadcasts LevelEvent 1035 and block.brewing_stand.brew sound, updates
//     comparators, and triggers minecraft:nether/brew_potion.
func (s *Server) TickBrewingStandAtInDim(dim string, x, y, z int) bool {
	if s == nil {
		return false
	}
	normDim := world.NormalizeDimension(dim)
	cache := s.DimensionChunks(normDim)
	if cache != nil {
		state := cache.GetBlock(x, y, z)
		if !IsBrewingStandBlock(state) {
			s.DropBrewingStandContentsInDim(normDim, x, y, z)
			return false
		}
	}

	bs := s.brewingState()
	bs.mu.Lock()
	b := s.getOrCreateBrewingStandInDimLocked(bs, normDim, x, y, z)
	changed, slotsChanged, brewedPotions, brewer, dropDragonBottle := stepBrewingStandTickLocked(b)
	itemsCopy := b.Items
	curBrewTime := b.BrewTime
	bs.mu.Unlock()

	cx := int32(x >> 4)
	cz := int32(z >> 4)

	if curBrewTime > 0 && curBrewTime%10 == 0 {
		s.BroadcastDimensionChunk(normDim, cx, cz, PlayLevelParticlesCB,
			EncodeBrewingStandBubbleParticlePacket(x, y, z, curBrewTime))
	}

	if dropDragonBottle {
		s.SpawnItemEntityInDimension(normDim, float64(x)+0.5, float64(y)+0.25, float64(z)+0.5, world.NewNamedItemStack("minecraft:glass_bottle", 1))
	}
	if slotsChanged {
		s.updateBrewingStandBottleBlockStateInDim(normDim, x, y, z, itemsCopy)
	}
	if len(brewedPotions) > 0 {
		// Vanilla BrewingStandBlockEntity.doBrew: level.levelEvent(1035, pos, 0) + sound.
		s.BroadcastDimensionChunk(normDim, cx, cz, PlayLevelEventCB,
			EncodeLevelEventPacket(LevelEventSoundBrewingStand, x, y, z, 0, false))
		s.BroadcastDimensionChunk(normDim, cx, cz, PlaySoundPacketID,
			EncodeSoundPacket(SoundBrewingStandBrew, SoundSourceBlocks, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, 1.0, 1.0, 0))
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

// TickBrewingStandAt advances the Overworld brewing stand at (x, y, z) by one game tick.
func (s *Server) TickBrewingStandAt(x, y, z int) bool {
	return s.TickBrewingStandAtInDim(world.DimensionOverworld, x, y, z)
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
		if keys[i].dim != keys[j].dim {
			return keys[i].dim < keys[j].dim
		}
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
		if s.TickBrewingStandAtInDim(k.dim, k.x, k.y, k.z) {
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

	dim := session.Dimension()
	if dim == "" {
		dim = world.DimensionOverworld
	}
	normDim := world.NormalizeDimension(dim)

	bs := s.brewingState()
	bs.mu.Lock()
	b := s.getOrCreateBrewingStandInDimLocked(bs, normDim, x, y, z)
	b.lastBrewer = session
	title := "Brewing Stand"
	if b.CustomName != "" {
		title = b.CustomName
	}
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
		if err := session.send(protocol.PlayOpenScreen, EncodeOpenScreen(cid, MenuTypeBrewingStand, title)); err != nil {
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
	dim := world.DimensionOverworld
	if session != nil && session.Dimension() != "" {
		dim = session.Dimension()
	}
	standSlots := s.BrewingStandSlotsInDim(dim, x, y, z)
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
	dim := session.Dimension()
	if dim == "" {
		dim = world.DimensionOverworld
	}
	b, _ := s.BrewingStandAtInDim(dim, x, y, z)
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
			s.SpawnItemEntityInDimension(snap.Dimension, snap.X, snap.Y+0.5, snap.Z, dropStack)
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
	dim := session.Dimension()
	if dim == "" {
		dim = world.DimensionOverworld
	}
	normDim := world.NormalizeDimension(dim)

	bs := s.brewingState()
	bs.mu.Lock()
	stand := s.getOrCreateBrewingStandInDimLocked(bs, normDim, bx, by, bz)
	stand.lastBrewer = session
	var slotPotions [BrewingStandMenuSlotCount]string
	for i := 0; i < 3; i++ {
		slotPotions[i] = stand.PotionTypes[i]
	}
	bs.mu.Unlock()
	slots := s.buildBrewingStandMenuSlots(session, bx, by, bz)

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

	case ContainerInputSwap:
		if button >= 0 && button < 9 {
			hotbarIdx := 32 + int(button)
			hbStack := slots[hotbarIdx]
			canSwap := true
			switch {
			case slot >= 0 && slot < 3:
				canSwap = hbStack.IsEmpty() || (CanPlaceInBrewingBottleSlot(hbStack) && hbStack.Count == 1)
			case slot == BrewingStandSlotIngredient:
				canSwap = hbStack.IsEmpty() || IsBrewingIngredient(hbStack)
			case slot == BrewingStandSlotFuel:
				canSwap = hbStack.IsEmpty() || IsBrewingFuel(hbStack)
			}
			if canSwap {
				if slot < 3 && IsPotionItem(slots[slot]) && slotPotions[slot] != "" && slotPotions[slot] != PotionWater {
					s.TriggerBrewedPotion(session, slotPotions[slot])
				}
				slots[slot], slots[hotbarIdx] = slots[hotbarIdx], slots[slot]
				slotPotions[slot], slotPotions[hotbarIdx] = slotPotions[hotbarIdx], slotPotions[slot]
			}
		}

	case ContainerInputClone:
		if session.Snapshot().GameMode == GameModeCreative && tr.carried.IsEmpty() && !slots[slot].IsEmpty() {
			maxSt := brewingItemMaxStackSize(slots[slot])
			tr.carried = world.NewItemStack(slots[slot].ResolvedItemID(), maxSt)
			carriedPotion = slotPotions[slot]
		}

	case ContainerInputThrow:
		if !slots[slot].IsEmpty() {
			dropCount := int32(1)
			if button == 1 {
				dropCount = slots[slot].Count
			}
			dropStack := world.NewItemStack(slots[slot].ResolvedItemID(), dropCount)
			droppedPotion := slotPotions[slot]
			rem := slots[slot].Count - dropCount
			if rem <= 0 {
				slots[slot] = world.ItemStack{}
				slotPotions[slot] = ""
			} else {
				slots[slot] = world.NewItemStack(slots[slot].ResolvedItemID(), rem)
			}
			if slot < 3 && IsPotionItem(dropStack) && droppedPotion != "" && droppedPotion != PotionWater {
				s.TriggerBrewedPotion(session, droppedPotion)
			}
			snap := session.Snapshot()
			s.SpawnItemEntityInDimension(snap.Dimension, snap.X, snap.Y+0.5, snap.Z, dropStack)
		}
	}

	// Write updated slots back to the brewing stand and player inventory.
	for i := 0; i < BrewingStandSlotCount; i++ {
		pType := ""
		if i < 3 {
			pType = slotPotions[i]
		}
		s.SetBrewingSlotInDim(normDim, bx, by, bz, i, slots[i], pType)
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
