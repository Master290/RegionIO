package server

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"

	"regionio/internal/nbt"
	"regionio/internal/protocol"
	"regionio/internal/world"
)

// Vanilla 26.1.2 splash and lingering potion constants, level events, and sounds.
const (
	SplashPotionRadius              float64 = 4.0
	WaterSplashSensitiveDamage      float32 = 1.0
	LingeringCloudInitialRadius     float32 = 3.0
	LingeringCloudMinRadius         float32 = 0.5
	LingeringCloudRadiusOnUse       float32 = -0.5
	LingeringCloudDefaultDuration   int32   = 600 // 30 seconds at 20 TPS
	LingeringCloudWaitTime          int32   = 10  // 0.5 seconds before first application
	LingeringCloudReapplyDelayTicks int32   = 20  // 1.0 second per-entity cooldown

	// DefaultPotionWaterColorRGB is the default water bottle RGB color (0x385DC6 = -13083194).
	DefaultPotionWaterColorRGB int32 = 0x385DC6

	// DataComponentPotionContents is the BuiltInRegistries.DATA_COMPONENT_TYPE index
	// for "minecraft:potion_contents" in Minecraft 26.1.2 (protocol 775).
	DataComponentPotionContents int32 = 51

	// Vanilla LevelEvent IDs for splash/lingering potion impact particles + glass break sound.
	LevelEventParticlesSpellPotionBreak   int32 = 2002
	LevelEventParticlesInstantPotionBreak int32 = 2007

	// Vanilla potion sound events.
	SoundSplashPotionBreak    = "minecraft:entity.splash_potion.break"
	SoundSplashPotionThrow    = "minecraft:entity.splash_potion.throw"
	SoundLingeringPotionThrow = "minecraft:entity.lingering_potion.throw"
	SoundGenericDrink         = "minecraft:entity.generic.drink"
)

// Canonical vanilla 26.1.2 potion identifiers (BuiltInRegistries.POTION).
const (
	PotionWater              = "minecraft:water"
	PotionMundane            = "minecraft:mundane"
	PotionThick              = "minecraft:thick"
	PotionAwkward            = "minecraft:awkward"
	PotionNightVision        = "minecraft:night_vision"
	PotionLongNightVision    = "minecraft:long_night_vision"
	PotionInvisibility       = "minecraft:invisibility"
	PotionLongInvisibility   = "minecraft:long_invisibility"
	PotionLeaping            = "minecraft:leaping"
	PotionLongLeaping        = "minecraft:long_leaping"
	PotionStrongLeaping      = "minecraft:strong_leaping"
	PotionFireResistance     = "minecraft:fire_resistance"
	PotionLongFireResistance = "minecraft:long_fire_resistance"
	PotionSwiftness          = "minecraft:swiftness"
	PotionLongSwiftness      = "minecraft:long_swiftness"
	PotionStrongSwiftness    = "minecraft:strong_swiftness"
	PotionSlowness           = "minecraft:slowness"
	PotionLongSlowness       = "minecraft:long_slowness"
	PotionStrongSlowness     = "minecraft:strong_slowness"
	PotionTurtleMaster       = "minecraft:turtle_master"
	PotionLongTurtleMaster   = "minecraft:long_turtle_master"
	PotionStrongTurtleMaster = "minecraft:strong_turtle_master"
	PotionWaterBreathing     = "minecraft:water_breathing"
	PotionLongWaterBreathing = "minecraft:long_water_breathing"
	PotionHealing            = "minecraft:healing"
	PotionStrongHealing      = "minecraft:strong_healing"
	PotionHarming            = "minecraft:harming"
	PotionStrongHarming      = "minecraft:strong_harming"
	PotionPoison             = "minecraft:poison"
	PotionLongPoison         = "minecraft:long_poison"
	PotionStrongPoison       = "minecraft:strong_poison"
	PotionRegeneration       = "minecraft:regeneration"
	PotionLongRegeneration   = "minecraft:long_regeneration"
	PotionStrongRegeneration = "minecraft:strong_regeneration"
	PotionStrength           = "minecraft:strength"
	PotionLongStrength       = "minecraft:long_strength"
	PotionStrongStrength     = "minecraft:strong_strength"
	PotionWeakness           = "minecraft:weakness"
	PotionLongWeakness       = "minecraft:long_weakness"
	PotionLuck               = "minecraft:luck"
	PotionSlowFalling        = "minecraft:slow_falling"
	PotionLongSlowFalling    = "minecraft:long_slow_falling"
	PotionWindCharged        = "minecraft:wind_charged"
	PotionWeaving            = "minecraft:weaving"
	PotionOozing             = "minecraft:oozing"
	PotionInfested           = "minecraft:infested"
)

// PotionDef describes one registered potion in BuiltInRegistries.POTION.
type PotionDef struct {
	ID            string
	BaseName      string
	RegistryIndex int32
	Effects       []StatusEffectInstance
}

// PotionRegistry holds all vanilla 26.1.2 potion types in registry order.
type PotionRegistry struct {
	defs    []*PotionDef
	byID    map[string]*PotionDef
	byIndex map[int32]*PotionDef
}

var (
	defaultPotionRegOnce sync.Once
	defaultPotionReg     *PotionRegistry
)

// DefaultPotionRegistry returns the singleton vanilla 26.1.2 potion registry.
func DefaultPotionRegistry() *PotionRegistry {
	defaultPotionRegOnce.Do(func() {
		defaultPotionReg = buildVanillaPotionRegistry()
	})
	return defaultPotionReg
}

// All returns all registered potion definitions in registry order.
func (r *PotionRegistry) All() []*PotionDef {
	if r == nil {
		return nil
	}
	out := make([]*PotionDef, len(r.defs))
	copy(out, r.defs)
	return out
}

// ByID looks up a potion definition by canonical or short name.
func (r *PotionRegistry) ByID(id string) (*PotionDef, bool) {
	if r == nil {
		return nil, false
	}
	def, ok := r.byID[canonicalPotionID(id)]
	return def, ok
}

// ByIndex looks up a potion definition by 0-based registry index.
func (r *PotionRegistry) ByIndex(index int32) (*PotionDef, bool) {
	if r == nil {
		return nil, false
	}
	def, ok := r.byIndex[index]
	return def, ok
}

// PotionByID looks up a potion in DefaultPotionRegistry by ID.
func PotionByID(id string) (*PotionDef, bool) {
	return DefaultPotionRegistry().ByID(id)
}

// PotionByIndex looks up a potion in DefaultPotionRegistry by 0-based registry index.
func PotionByIndex(index int32) (*PotionDef, bool) {
	return DefaultPotionRegistry().ByIndex(index)
}

// PotionEffects returns a copy of the status effects granted by potionID.
func PotionEffects(potionID string) []StatusEffectInstance {
	def, ok := PotionByID(potionID)
	if !ok || len(def.Effects) == 0 {
		return nil
	}
	out := make([]StatusEffectInstance, len(def.Effects))
	for i, e := range def.Effects {
		out[i] = e.Clone()
	}
	return out
}

// PotionHasInstantEffect reports whether any status effect of potionID is
// instantaneous (e.g. instant_health or instant_damage), which selects
// LevelEvent 2007 instead of 2002 on splash/lingering impact.
func PotionHasInstantEffect(potionID string) bool {
	for _, eff := range PotionEffects(potionID) {
		if def, ok := MobEffectByID(eff.ID); ok && def.Instantaneous {
			return true
		}
	}
	return false
}

// PotionColor computes the blended 24-bit RGB color of potionID's effects
// (or DefaultPotionWaterColorRGB = 0x385DC6 when potionID has no effects),
// matching vanilla PotionContents.getColor.
func PotionColor(potionID string) int32 {
	effects := PotionEffects(potionID)
	if len(effects) == 0 {
		return DefaultPotionWaterColorRGB
	}
	var sumR, sumG, sumB, totalWeight int32
	for _, eff := range effects {
		def, ok := MobEffectByID(eff.ID)
		if !ok || !eff.ShowParticles {
			continue
		}
		weight := eff.Amplifier + 1
		if weight < 1 {
			weight = 1
		}
		c := def.Color & 0xFFFFFF
		sumR += ((c >> 16) & 0xFF) * weight
		sumG += ((c >> 8) & 0xFF) * weight
		sumB += (c & 0xFF) * weight
		totalWeight += weight
	}
	if totalWeight <= 0 {
		return DefaultPotionWaterColorRGB
	}
	r := (sumR / totalWeight) & 0xFF
	g := (sumG / totalWeight) & 0xFF
	b := (sumB / totalWeight) & 0xFF
	return (r << 16) | (g << 8) | b
}

func canonicalPotionID(id string) string {
	id = strings.TrimSpace(strings.ToLower(id))
	if id == "" {
		return PotionWater
	}
	if !strings.Contains(id, ":") {
		return "minecraft:" + id
	}
	return id
}

func buildVanillaPotionRegistry() *PotionRegistry {
	eff := NewStatusEffectInstance
	// Exact registration order from net.minecraft.world.item.alchemy.Potions (26.1.2).
	entries := []struct {
		id       string
		baseName string
		effects  []StatusEffectInstance
	}{
		{PotionWater, "water", nil},
		{PotionMundane, "mundane", nil},
		{PotionThick, "thick", nil},
		{PotionAwkward, "awkward", nil},
		{PotionNightVision, "night_vision", []StatusEffectInstance{eff(EffectNightVision, 3600, 0)}},
		{PotionLongNightVision, "night_vision", []StatusEffectInstance{eff(EffectNightVision, 9600, 0)}},
		{PotionInvisibility, "invisibility", []StatusEffectInstance{eff(EffectInvisibility, 3600, 0)}},
		{PotionLongInvisibility, "invisibility", []StatusEffectInstance{eff(EffectInvisibility, 9600, 0)}},
		{PotionLeaping, "leaping", []StatusEffectInstance{eff(EffectJumpBoost, 3600, 0)}},
		{PotionLongLeaping, "leaping", []StatusEffectInstance{eff(EffectJumpBoost, 9600, 0)}},
		{PotionStrongLeaping, "leaping", []StatusEffectInstance{eff(EffectJumpBoost, 1800, 1)}},
		{PotionFireResistance, "fire_resistance", []StatusEffectInstance{eff(EffectFireResistance, 3600, 0)}},
		{PotionLongFireResistance, "fire_resistance", []StatusEffectInstance{eff(EffectFireResistance, 9600, 0)}},
		{PotionSwiftness, "swiftness", []StatusEffectInstance{eff(EffectSpeed, 3600, 0)}},
		{PotionLongSwiftness, "swiftness", []StatusEffectInstance{eff(EffectSpeed, 9600, 0)}},
		{PotionStrongSwiftness, "swiftness", []StatusEffectInstance{eff(EffectSpeed, 1800, 1)}},
		{PotionSlowness, "slowness", []StatusEffectInstance{eff(EffectSlowness, 1800, 0)}},
		{PotionLongSlowness, "slowness", []StatusEffectInstance{eff(EffectSlowness, 4800, 0)}},
		{PotionStrongSlowness, "slowness", []StatusEffectInstance{eff(EffectSlowness, 400, 3)}},
		{PotionTurtleMaster, "turtle_master", []StatusEffectInstance{
			eff(EffectSlowness, 400, 3),
			eff(EffectResistance, 400, 2),
		}},
		{PotionLongTurtleMaster, "turtle_master", []StatusEffectInstance{
			eff(EffectSlowness, 800, 3),
			eff(EffectResistance, 800, 2),
		}},
		{PotionStrongTurtleMaster, "turtle_master", []StatusEffectInstance{
			eff(EffectSlowness, 400, 5),
			eff(EffectResistance, 400, 3),
		}},
		{PotionWaterBreathing, "water_breathing", []StatusEffectInstance{eff(EffectWaterBreathing, 3600, 0)}},
		{PotionLongWaterBreathing, "water_breathing", []StatusEffectInstance{eff(EffectWaterBreathing, 9600, 0)}},
		{PotionHealing, "healing", []StatusEffectInstance{eff(EffectInstantHealth, 1, 0)}},
		{PotionStrongHealing, "healing", []StatusEffectInstance{eff(EffectInstantHealth, 1, 1)}},
		{PotionHarming, "harming", []StatusEffectInstance{eff(EffectInstantDamage, 1, 0)}},
		{PotionStrongHarming, "harming", []StatusEffectInstance{eff(EffectInstantDamage, 1, 1)}},
		{PotionPoison, "poison", []StatusEffectInstance{eff(EffectPoison, 900, 0)}},
		{PotionLongPoison, "poison", []StatusEffectInstance{eff(EffectPoison, 1800, 0)}},
		{PotionStrongPoison, "poison", []StatusEffectInstance{eff(EffectPoison, 432, 1)}},
		{PotionRegeneration, "regeneration", []StatusEffectInstance{eff(EffectRegeneration, 900, 0)}},
		{PotionLongRegeneration, "regeneration", []StatusEffectInstance{eff(EffectRegeneration, 1800, 0)}},
		{PotionStrongRegeneration, "regeneration", []StatusEffectInstance{eff(EffectRegeneration, 450, 1)}},
		{PotionStrength, "strength", []StatusEffectInstance{eff(EffectStrength, 3600, 0)}},
		{PotionLongStrength, "strength", []StatusEffectInstance{eff(EffectStrength, 9600, 0)}},
		{PotionStrongStrength, "strength", []StatusEffectInstance{eff(EffectStrength, 1800, 1)}},
		{PotionWeakness, "weakness", []StatusEffectInstance{eff(EffectWeakness, 1800, 0)}},
		{PotionLongWeakness, "weakness", []StatusEffectInstance{eff(EffectWeakness, 4800, 0)}},
		{PotionLuck, "luck", []StatusEffectInstance{eff(EffectLuck, 6000, 0)}},
		{PotionSlowFalling, "slow_falling", []StatusEffectInstance{eff(EffectSlowFalling, 1800, 0)}},
		{PotionLongSlowFalling, "slow_falling", []StatusEffectInstance{eff(EffectSlowFalling, 4800, 0)}},
		{PotionWindCharged, "wind_charged", []StatusEffectInstance{eff(EffectWindCharged, 3600, 0)}},
		{PotionWeaving, "weaving", []StatusEffectInstance{eff(EffectWeaving, 3600, 0)}},
		{PotionOozing, "oozing", []StatusEffectInstance{eff(EffectOozing, 3600, 0)}},
		{PotionInfested, "infested", []StatusEffectInstance{eff(EffectInfested, 3600, 0)}},
	}

	reg := &PotionRegistry{
		defs:    make([]*PotionDef, 0, len(entries)),
		byID:    make(map[string]*PotionDef, len(entries)),
		byIndex: make(map[int32]*PotionDef, len(entries)),
	}
	for i, e := range entries {
		def := &PotionDef{
			ID:            e.id,
			BaseName:      e.baseName,
			RegistryIndex: int32(i),
			Effects:       e.effects,
		}
		reg.defs = append(reg.defs, def)
		reg.byID[e.id] = def
		reg.byIndex[int32(i)] = def
	}
	return reg
}

// --- Potion Item Classification & Per-Slot Potion Metadata ---

// IsPotionItem reports whether stack is a potion, splash_potion, or lingering_potion.
func IsPotionItem(stack world.ItemStack) bool {
	if stack.IsEmpty() {
		return false
	}
	switch stack.ResolvedID() {
	case "minecraft:potion", "minecraft:splash_potion", "minecraft:lingering_potion":
		return true
	default:
		return false
	}
}

// IsDrinkablePotionItem reports whether stack is a drinkable minecraft:potion.
func IsDrinkablePotionItem(stack world.ItemStack) bool {
	return !stack.IsEmpty() && stack.ResolvedID() == "minecraft:potion"
}

// IsSplashPotionItem reports whether stack is a minecraft:splash_potion.
func IsSplashPotionItem(stack world.ItemStack) bool {
	return !stack.IsEmpty() && stack.ResolvedID() == "minecraft:splash_potion"
}

// IsLingeringPotionItem reports whether stack is a minecraft:lingering_potion.
func IsLingeringPotionItem(stack world.ItemStack) bool {
	return !stack.IsEmpty() && stack.ResolvedID() == "minecraft:lingering_potion"
}

// IsMilkBucketItem reports whether stack is a minecraft:milk_bucket.
func IsMilkBucketItem(stack world.ItemStack) bool {
	return !stack.IsEmpty() && stack.ResolvedID() == "minecraft:milk_bucket"
}

// IsPotionOrTippedArrowItem reports whether stack carries a `minecraft:potion_contents`
// component (`minecraft:potion`, `minecraft:splash_potion`, `minecraft:lingering_potion`,
// or `minecraft:tipped_arrow`).
func IsPotionOrTippedArrowItem(stack world.ItemStack) bool {
	if stack.IsEmpty() {
		return false
	}
	switch stack.ResolvedID() {
	case "minecraft:potion", "minecraft:splash_potion", "minecraft:lingering_potion", "minecraft:tipped_arrow":
		return true
	default:
		return false
	}
}

// PotionContentsComponent represents the protocol 775 `minecraft:potion_contents`
// (DataComponentPotionContents = 51) data component payload.
type PotionContentsComponent struct {
	PotionID      string
	HasColor      bool
	CustomColor   int32
	CustomEffects []StatusEffectInstance
	CustomName    string
}

// EncodePotionContentsComponent writes the protocol 775 `PotionContents.STREAM_CODEC`
// payload for DataComponentPotionContents (51):
//   - Bool(hasPotion) + [if true: VarInt(potionRegistryIndex)]
//   - Bool(hasCustomColor) + [if true: Int32(customColor)]
//   - VarInt(len(customEffects)) + MobEffectInstance[len]
//   - Bool(hasCustomName) + [if true: String(customName)]
func EncodePotionContentsComponent(w *protocol.Writer, pc PotionContentsComponent) {
	potID := strings.TrimSpace(pc.PotionID)
	if potID != "" {
		if def, ok := PotionByID(potID); ok {
			w.Bool(true)
			w.VarInt(def.RegistryIndex)
		} else {
			w.Bool(true)
			w.VarInt(0) // fallback to water (index 0)
		}
	} else {
		w.Bool(false)
	}
	if pc.HasColor {
		w.Bool(true)
		w.Int32(pc.CustomColor)
	} else {
		w.Bool(false)
	}
	w.VarInt(int32(len(pc.CustomEffects)))
	for _, eff := range pc.CustomEffects {
		effIdx := int32(0)
		if def, ok := MobEffectByID(eff.ID); ok {
			effIdx = def.RegistryIndex
		}
		w.VarInt(effIdx)
		w.VarInt(eff.Amplifier)
		w.VarInt(eff.Duration)
		w.Bool(eff.Ambient)
		w.Bool(eff.ShowParticles)
		w.Bool(eff.ShowIcon)
		w.Bool(false) // no hiddenEffect
	}
	if pc.CustomName != "" {
		w.Bool(true)
		w.String(pc.CustomName)
	} else {
		w.Bool(false)
	}
}

// DecodePotionContentsComponent reads the protocol 775 `PotionContents.STREAM_CODEC`
// payload for DataComponentPotionContents (51).
func DecodePotionContentsComponent(r *protocol.Reader) (PotionContentsComponent, error) {
	var pc PotionContentsComponent
	hasPotion, err := r.Bool()
	if err != nil {
		return pc, err
	}
	if hasPotion {
		regIdx, err := r.VarInt()
		if err != nil {
			return pc, err
		}
		if def, ok := PotionByIndex(regIdx); ok {
			pc.PotionID = def.ID
		} else {
			pc.PotionID = PotionWater
		}
	}
	hasColor, err := r.Bool()
	if err != nil {
		return pc, err
	}
	if hasColor {
		col, err := r.Int32()
		if err != nil {
			return pc, err
		}
		pc.HasColor = true
		pc.CustomColor = col
	}
	effCount, err := r.VarInt()
	if err != nil {
		return pc, err
	}
	if effCount < 0 || effCount > 64 {
		return pc, fmt.Errorf("server: invalid potion custom effect count %d", effCount)
	}
	for i := int32(0); i < effCount; i++ {
		effIdx, err := r.VarInt()
		if err != nil {
			return pc, err
		}
		amp, err := r.VarInt()
		if err != nil {
			return pc, err
		}
		dur, err := r.VarInt()
		if err != nil {
			return pc, err
		}
		ambient, err := r.Bool()
		if err != nil {
			return pc, err
		}
		showPart, err := r.Bool()
		if err != nil {
			return pc, err
		}
		showIcon, err := r.Bool()
		if err != nil {
			return pc, err
		}
		hasHidden, err := r.Bool()
		if err != nil {
			return pc, err
		}
		if hasHidden {
			return pc, fmt.Errorf("server: nested hiddenEffect not supported in DecodePotionContentsComponent")
		}
		effID := EffectSpeed
		if def, ok := MobEffectByIndex(effIdx); ok {
			effID = def.ID
		}
		pc.CustomEffects = append(pc.CustomEffects, StatusEffectInstance{
			ID:            effID,
			Amplifier:     amp,
			Duration:      dur,
			Ambient:       ambient,
			ShowParticles: showPart,
			ShowIcon:      showIcon,
		})
	}
	hasName, err := r.Bool()
	if err != nil {
		return pc, err
	}
	if hasName {
		name, err := r.String()
		if err != nil {
			return pc, err
		}
		pc.CustomName = name
	}
	return pc, nil
}

// EncodeSlotWithPotionContents writes a protocol 775 ItemStack carrying a
// `minecraft:potion_contents` (DataComponentPotionContents = 51) component.
func EncodeSlotWithPotionContents(w *protocol.Writer, stack world.ItemStack, potionID string) {
	if stack.IsEmpty() {
		w.VarInt(0)
		return
	}
	itemID := stack.ResolvedItemID()
	if itemID <= 0 {
		w.VarInt(0)
		return
	}
	pot := canonicalPotionID(potionID)
	if pot == "" {
		pot = PotionWater
	}
	w.VarInt(stack.Count)
	w.VarInt(itemID)
	w.VarInt(1) // 1 added component
	w.VarInt(0) // 0 removed components
	w.VarInt(DataComponentPotionContents)
	EncodePotionContentsComponent(w, PotionContentsComponent{PotionID: pot})
}

// DecodeSlotWithPotionContents reads a protocol 775 ItemStack carrying a
// `minecraft:potion_contents` (51) component.
func DecodeSlotWithPotionContents(r *protocol.Reader) (world.ItemStack, PotionContentsComponent, error) {
	count, err := r.VarInt()
	if err != nil {
		return world.ItemStack{}, PotionContentsComponent{}, err
	}
	if count <= 0 {
		return world.ItemStack{}, PotionContentsComponent{}, nil
	}
	itemID, err := r.VarInt()
	if err != nil {
		return world.ItemStack{}, PotionContentsComponent{}, err
	}
	added, err := r.VarInt()
	if err != nil {
		return world.ItemStack{}, PotionContentsComponent{}, err
	}
	removed, err := r.VarInt()
	if err != nil {
		return world.ItemStack{}, PotionContentsComponent{}, err
	}
	var pc PotionContentsComponent
	for i := int32(0); i < added; i++ {
		compType, err := r.VarInt()
		if err != nil {
			return world.ItemStack{}, PotionContentsComponent{}, err
		}
		if compType != DataComponentPotionContents {
			return world.ItemStack{}, PotionContentsComponent{}, fmt.Errorf("server: unexpected component %d in DecodeSlotWithPotionContents", compType)
		}
		pc, err = DecodePotionContentsComponent(r)
		if err != nil {
			return world.ItemStack{}, PotionContentsComponent{}, err
		}
	}
	for i := int32(0); i < removed; i++ {
		if _, err := r.VarInt(); err != nil {
			return world.ItemStack{}, PotionContentsComponent{}, err
		}
	}
	return world.NewItemStack(itemID, count), pc, nil
}

func encodeContainerSetSlotWithPotion(containerID, stateID int32, slot int, stack world.ItemStack, potionID string) []byte {
	w := protocol.NewWriter(32)
	w.VarInt(containerID)
	w.VarInt(stateID)
	w.Uint16(uint16(int16(slot)))
	if IsPotionOrTippedArrowItem(stack) {
		EncodeSlotWithPotionContents(w, stack, potionID)
	} else {
		writeContainerItemStack(w, stack)
	}
	return w.Bytes()
}

// MatchTippedArrowRecipe implements vanilla 26.1.2 TippedArrowRecipe:
//   - 3x3 crafting grid (len(grid) == 9)
//   - Slots 0, 1, 2, 3, 5, 6, 7, 8 must each contain minecraft:arrow
//   - Center slot 4 must contain minecraft:lingering_potion
//   - Produces 8x minecraft:tipped_arrow inheriting the lingering potion's PotionType.
func MatchTippedArrowRecipe(grid []world.ItemStack, centerPotionType string) (world.ItemStack, string, bool) {
	if len(grid) != 9 {
		return world.ItemStack{}, "", false
	}
	if grid[4].IsEmpty() || grid[4].ResolvedID() != "minecraft:lingering_potion" {
		return world.ItemStack{}, "", false
	}
	for i := 0; i < 9; i++ {
		if i == 4 {
			continue
		}
		if grid[i].IsEmpty() || grid[i].ResolvedID() != "minecraft:arrow" {
			return world.ItemStack{}, "", false
		}
	}
	pot := canonicalPotionID(centerPotionType)
	if pot == "" {
		pot = PotionWater
	}
	return world.NewNamedItemStack("minecraft:tipped_arrow", 8), pot, true
}

type serverDroppedPotionTracker struct {
	mu      sync.Mutex
	potions map[int32]string
}

var serverDroppedPotions sync.Map // map[*Server]*serverDroppedPotionTracker

func (s *Server) droppedPotionTracker() *serverDroppedPotionTracker {
	if s == nil {
		return nil
	}
	if v, ok := serverDroppedPotions.Load(s); ok {
		return v.(*serverDroppedPotionTracker)
	}
	actual, _ := serverDroppedPotions.LoadOrStore(s, &serverDroppedPotionTracker{
		potions: make(map[int32]string),
	})
	return actual.(*serverDroppedPotionTracker)
}

// AssociateDroppedPotionEntity records the canonical potion ID (e.g. "minecraft:poison")
// for a spawned potion or tipped_arrow item entity so picking it up restores its PotionType.
func (s *Server) AssociateDroppedPotionEntity(itemEntityID int32, potionID string) {
	if s == nil || itemEntityID <= 0 || potionID == "" {
		return
	}
	tr := s.droppedPotionTracker()
	tr.mu.Lock()
	tr.potions[itemEntityID] = canonicalPotionID(potionID)
	tr.mu.Unlock()
}

// DroppedPotionEntityType returns the canonical potion ID associated with a
// dropped potion or tipped_arrow item entity, if any.
func (s *Server) DroppedPotionEntityType(itemEntityID int32) (string, bool) {
	if s == nil || itemEntityID <= 0 {
		return "", false
	}
	tr := s.droppedPotionTracker()
	tr.mu.Lock()
	pot, ok := tr.potions[itemEntityID]
	tr.mu.Unlock()
	return pot, ok
}

// TakeDroppedPotionEntityType removes and returns the canonical potion ID
// associated with a dropped potion or tipped_arrow item entity, if any.
func (s *Server) TakeDroppedPotionEntityType(itemEntityID int32) (string, bool) {
	if s == nil || itemEntityID <= 0 {
		return "", false
	}
	tr := s.droppedPotionTracker()
	tr.mu.Lock()
	pot, ok := tr.potions[itemEntityID]
	if ok {
		delete(tr.potions, itemEntityID)
	}
	tr.mu.Unlock()
	return pot, ok
}

type playerPotionMetaTracker struct {
	mu          sync.Mutex
	invPotions  [world.PlayerInventorySlots]string
	carriedType string
}

var playerPotionMetas sync.Map // map[*PlayerSession]*playerPotionMetaTracker

func getPotionMetaTracker(session *PlayerSession) *playerPotionMetaTracker {
	if session == nil {
		return nil
	}
	if v, ok := playerPotionMetas.Load(session); ok {
		return v.(*playerPotionMetaTracker)
	}
	actual, _ := playerPotionMetas.LoadOrStore(session, &playerPotionMetaTracker{})
	return actual.(*playerPotionMetaTracker)
}

// GetSlotPotionType returns the canonical potion ID (e.g. "minecraft:healing")
// stored on player inventory slot (0..45) for a potion or tipped_arrow item.
// With no explicit type set, it defaults to "minecraft:water".
func GetSlotPotionType(session *PlayerSession, slot int) string {
	if session == nil || slot < 0 || slot >= world.PlayerInventorySlots {
		return ""
	}
	st := session.InventorySlot(slot)
	if !IsPotionOrTippedArrowItem(st) {
		return ""
	}
	tr := getPotionMetaTracker(session)
	tr.mu.Lock()
	raw := tr.invPotions[slot]
	tr.mu.Unlock()
	if raw == "" {
		return PotionWater
	}
	return canonicalPotionID(raw)
}

// SetSlotPotionType sets the canonical potion ID associated with player
// inventory slot (0..45).
func SetSlotPotionType(session *PlayerSession, slot int, potionID string) {
	if session == nil || slot < 0 || slot >= world.PlayerInventorySlots {
		return
	}
	tr := getPotionMetaTracker(session)
	tr.mu.Lock()
	if potionID == "" {
		tr.invPotions[slot] = ""
	} else {
		tr.invPotions[slot] = canonicalPotionID(potionID)
	}
	tr.mu.Unlock()
}

// GetCarriedPotionType returns the canonical potion ID for the potion or
// tipped_arrow on the player's cursor.
func GetCarriedPotionType(session *PlayerSession) string {
	if session == nil || !IsPotionOrTippedArrowItem(session.CarriedItem()) {
		return ""
	}
	tr := getPotionMetaTracker(session)
	tr.mu.Lock()
	raw := tr.carriedType
	tr.mu.Unlock()
	if raw == "" {
		return PotionWater
	}
	return canonicalPotionID(raw)
}

// SetCarriedPotionType sets the canonical potion ID for the potion on the
// player's cursor.
func SetCarriedPotionType(session *PlayerSession, potionID string) {
	if session == nil {
		return
	}
	tr := getPotionMetaTracker(session)
	tr.mu.Lock()
	if potionID == "" {
		tr.carriedType = ""
	} else {
		tr.carriedType = canonicalPotionID(potionID)
	}
	tr.mu.Unlock()
}

// --- Drinking Potions & Milk Bucket ---

// DrinkPotion consumes the drinkable minecraft:potion in session's specified
// hand (HandMain or HandOffHand), applies all status effects of its potion
// type, replaces the bottle with minecraft:glass_bottle in survival/adventure
// mode, and fires advancement triggers.
func (s *Server) DrinkPotion(session *PlayerSession, hand int32) bool {
	if s == nil || session == nil || session.IsDead() {
		return false
	}
	slot, stack := session.handSlotAndStack(hand)
	if !IsDrinkablePotionItem(stack) {
		return false
	}
	potionID := GetSlotPotionType(session, slot)
	if potionID == "" {
		potionID = PotionWater
	}

	effects := PotionEffects(potionID)
	for _, eff := range effects {
		s.ApplyPlayerEffect(session, eff)
	}

	snap := session.Snapshot()
	cx := int32(int64(math.Floor(snap.X)) >> 4)
	cz := int32(int64(math.Floor(snap.Z)) >> 4)
	s.BroadcastDimensionChunk(snap.Dimension, cx, cz, PlaySoundPacketID,
		EncodeSoundPacket(SoundGenericDrink, SoundSourcePlayers, snap.X, snap.Y, snap.Z, 0.5, 0.9, 0))

	s.consumeBeverageContainer(session, slot, stack, "minecraft:glass_bottle")
	s.TriggerConsumeItem(session, stack)
	return true
}

// DrinkPotionByType applies the effects of potionID directly to session (as if
// drunk from a bottle) and fires the consume_item advancement trigger.
func (s *Server) DrinkPotionByType(session *PlayerSession, potionID string) bool {
	if s == nil || session == nil || session.IsDead() {
		return false
	}
	def, ok := PotionByID(potionID)
	if !ok {
		return false
	}
	for _, eff := range def.Effects {
		s.ApplyPlayerEffect(session, eff)
	}
	s.TriggerConsumeItem(session, world.NewNamedItemStack("minecraft:potion", 1))
	return true
}

// DrinkMilkBucket consumes a minecraft:milk_bucket from session's specified
// hand, clears all active status effects on session, replaces the milk bucket
// with an empty minecraft:bucket in survival/adventure mode, and fires
// consume_item advancements. Returns the number of cleared effects (or >= 0
// when milk was successfully consumed).
func (s *Server) DrinkMilkBucket(session *PlayerSession, hand int32) (cleared int, consumed bool) {
	if s == nil || session == nil || session.IsDead() {
		return 0, false
	}
	slot, stack := session.handSlotAndStack(hand)
	if !IsMilkBucketItem(stack) {
		return 0, false
	}
	cleared = s.ClearPlayerEffects(session)
	snap := session.Snapshot()
	cx := int32(int64(math.Floor(snap.X)) >> 4)
	cz := int32(int64(math.Floor(snap.Z)) >> 4)
	s.BroadcastDimensionChunk(snap.Dimension, cx, cz, PlaySoundPacketID,
		EncodeSoundPacket(SoundGenericDrink, SoundSourcePlayers, snap.X, snap.Y, snap.Z, 0.5, 0.9, 0))
	s.consumeBeverageContainer(session, slot, stack, "minecraft:bucket")
	s.TriggerConsumeItem(session, stack)
	return cleared, true
}

// UsePotionOrMilkItem handles right-clicking (use_item) with a potion, milk
// bucket, splash potion, or lingering potion in the given hand.
func (s *Server) UsePotionOrMilkItem(session *PlayerSession, hand int32) bool {
	if s == nil || session == nil || session.IsDead() {
		return false
	}
	_, stack := session.handSlotAndStack(hand)
	if stack.IsEmpty() {
		return false
	}
	switch stack.ResolvedID() {
	case "minecraft:potion":
		return s.DrinkPotion(session, hand)
	case "minecraft:milk_bucket":
		_, ok := s.DrinkMilkBucket(session, hand)
		return ok
	case "minecraft:splash_potion":
		return s.ThrowSplashPotion(session, hand)
	case "minecraft:lingering_potion":
		_, ok := s.ThrowLingeringPotion(session, hand)
		return ok
	case "minecraft:ominous_bottle":
		return s.DrinkOminousBottle(session, hand)
	default:
		return false
	}
}

func (s *Server) consumeBeverageContainer(session *PlayerSession, slot int, stack world.ItemStack, remainderItem string) {
	if session == nil || slot < 0 || slot >= world.PlayerInventorySlots {
		return
	}
	snap := session.Snapshot()
	if snap.GameMode != GameModeCreative {
		remCount := stack.Count - 1
		remainderStack := world.NewNamedItemStack(remainderItem, 1)
		if remCount <= 0 {
			SetSlotPotionType(session, slot, "")
			session.SetInventorySlot(slot, remainderStack)
		} else {
			session.SetInventorySlot(slot, world.NewItemStack(stack.ResolvedItemID(), remCount))
			added, _ := session.AddItemToInventory(remainderStack)
			if added < 1 && s != nil {
				s.SpawnItemEntityInDimension(snap.Dimension, snap.X, snap.Y+0.25, snap.Z, remainderStack)
			}
		}
	}
	if session.send != nil {
		_ = session.send(protocol.PlayEntityEvent, EncodeEntityEventPacket(session.EntityID, EntityEventUseItemComplete))
		stateID := session.NextContainerStateID()
		updated := session.InventorySlot(slot)
		_ = session.send(protocol.PlayContainerSetSlot, encodeContainerSetSlotWithPotion(0, stateID, slot, updated, GetSlotPotionType(session, slot)))
	}
}

// --- Splash Potions ---

// ThrowSplashPotion consumes one minecraft:splash_potion from session's hand
// and splashes its effects around the player's position.
func (s *Server) ThrowSplashPotion(session *PlayerSession, hand int32) bool {
	if s == nil || session == nil || session.IsDead() {
		return false
	}
	slot, stack := session.handSlotAndStack(hand)
	if !IsSplashPotionItem(stack) {
		return false
	}
	potionID := GetSlotPotionType(session, slot)
	if potionID == "" {
		potionID = PotionWater
	}
	snap := session.Snapshot()
	cx := int32(int64(math.Floor(snap.X)) >> 4)
	cz := int32(int64(math.Floor(snap.Z)) >> 4)
	s.BroadcastDimensionChunk(snap.Dimension, cx, cz, PlaySoundPacketID,
		EncodeSoundPacket(SoundSplashPotionThrow, SoundSourcePlayers, snap.X, snap.Y, snap.Z, 0.5, 0.4, 0))
	s.consumeThrownPotionSlot(session, slot, stack)
	s.ApplySplashPotionAt(snap.Dimension, snap.X, snap.Y, snap.Z, potionID, session.EntityID)
	return true
}

func (s *Server) broadcastPotionBreakEffect(dim string, x, y, z float64, potionID string) {
	if s == nil {
		return
	}
	normDim := world.NormalizeDimension(dim)
	bx := int(math.Floor(x))
	by := int(math.Floor(y))
	bz := int(math.Floor(z))
	cx := int32(bx >> 4)
	cz := int32(bz >> 4)

	evID := LevelEventParticlesSpellPotionBreak
	if PotionHasInstantEffect(potionID) {
		evID = LevelEventParticlesInstantPotionBreak
	}
	color := PotionColor(potionID)
	s.BroadcastDimensionChunk(normDim, cx, cz, PlayLevelEventCB,
		EncodeLevelEventPacket(evID, bx, by, bz, color, false))
	s.BroadcastDimensionChunk(normDim, cx, cz, PlaySoundPacketID,
		EncodeSoundPacket(SoundSplashPotionBreak, SoundSourceNeutral, x, y, z, 1.0, 1.0, 0))
}

// ApplySplashPotionAt applies a splash potion detonation at (x, y, z) in dim
// with vanilla's 4.0-block radius and distance attenuation (1.0 - dist/4.0).
// If directHitEntityID > 0, that entity receives full scale (1.0).
func (s *Server) ApplySplashPotionAt(dim string, x, y, z float64, potionID string, directHitEntityID int32) int {
	if s == nil {
		return 0
	}
	dim = world.NormalizeDimension(dim)
	potionID = canonicalPotionID(potionID)
	s.broadcastPotionBreakEffect(dim, x, y, z, potionID)
	effects := PotionEffects(potionID)
	affected := 0

	// Water splash potion damages water-sensitive mobs (blaze, enderman) and extinguishes burning entities/players.
	if len(effects) == 0 && (potionID == PotionWater || potionID == PotionMundane || potionID == PotionThick || potionID == PotionAwkward) {
		s.playersMu.RLock()
		players := make([]*PlayerSession, 0, len(s.players))
		for _, p := range s.players {
			players = append(players, p)
		}
		s.playersMu.RUnlock()
		for _, p := range players {
			if p == nil || p.IsDead() {
				continue
			}
			ps := p.Snapshot()
			if world.NormalizeDimension(ps.Dimension) != dim {
				continue
			}
			dist := math.Sqrt((ps.X-x)*(ps.X-x) + (ps.Y-y)*(ps.Y-y) + (ps.Z-z)*(ps.Z-z))
			if dist <= SplashPotionRadius && s.IsPlayerOnFire(p) {
				s.SetPlayerFireTicks(p, 0)
				affected++
			}
		}

		if s.entities != nil {
			cx := int32(int64(math.Floor(x)) >> 4)
			cz := int32(int64(math.Floor(z)) >> 4)
			ents := s.entities.AppendInChunkRange(nil, cx, cz, 1)
			for i := range ents {
				ent := &ents[i]
				if ent.EffectiveDimension() != dim || ent.IsItem() || ent.IsExperienceOrb() {
					continue
				}
				dist := math.Sqrt((ent.X-x)*(ent.X-x) + (ent.Y-y)*(ent.Y-y) + (ent.Z-z)*(ent.Z-z))
				if dist <= SplashPotionRadius {
					if s.IsEntityOnFire(ent.ID) {
						s.SetEntityFireTicks(ent.ID, 0)
						affected++
					}
					if isWaterSensitiveMob(ent.TypeName) {
						live := s.entities.Get(ent.ID)
						if live != nil {
							s.damageOrKillMobFromEffect(live, WaterSplashSensitiveDamage)
							affected++
						}
					}
				}
			}
		}
		return affected
	}

	if len(effects) == 0 {
		return 0
	}

	// 1. Affect players within SplashPotionRadius (4.0 blocks).
	s.playersMu.RLock()
	players := make([]*PlayerSession, 0, len(s.players))
	for _, p := range s.players {
		players = append(players, p)
	}
	s.playersMu.RUnlock()

	for _, p := range players {
		if p == nil || p.IsDead() {
			continue
		}
		ps := p.Snapshot()
		if world.NormalizeDimension(ps.Dimension) != dim {
			continue
		}
		dist := math.Sqrt((ps.X-x)*(ps.X-x) + (ps.Y-y)*(ps.Y-y) + (ps.Z-z)*(ps.Z-z))
		if dist > SplashPotionRadius {
			continue
		}
		scale := 1.0 - dist/SplashPotionRadius
		if p.EntityID == directHitEntityID {
			scale = 1.0
		}
		if scale <= 0 {
			continue
		}
		appliedAny := false
		for _, eff := range effects {
			if s.ApplyPlayerEffectScaled(p, eff, scale) {
				appliedAny = true
			}
		}
		if appliedAny {
			affected++
		}
	}

	// 2. Affect living non-player entities within SplashPotionRadius (4.0 blocks).
	if s.entities != nil {
		cx := int32(int64(math.Floor(x)) >> 4)
		cz := int32(int64(math.Floor(z)) >> 4)
		ents := s.entities.AppendInChunkRange(nil, cx, cz, 1)
		for i := range ents {
			ent := &ents[i]
			if ent.EffectiveDimension() != dim || ent.IsItem() || ent.IsExperienceOrb() {
				continue
			}
			dist := math.Sqrt((ent.X-x)*(ent.X-x) + (ent.Y-y)*(ent.Y-y) + (ent.Z-z)*(ent.Z-z))
			if dist > SplashPotionRadius {
				continue
			}
			scale := 1.0 - dist/SplashPotionRadius
			if ent.ID == directHitEntityID {
				scale = 1.0
			}
			if scale <= 0 {
				continue
			}
			appliedAny := false
			for _, eff := range effects {
				if s.ApplyEntityEffectScaled(ent.ID, eff, scale) {
					appliedAny = true
				}
			}
			if appliedAny {
				affected++
			}
		}
	}

	return affected
}

func isWaterSensitiveMob(typeName string) bool {
	switch strings.TrimPrefix(strings.ToLower(strings.TrimSpace(typeName)), "minecraft:") {
	case "blaze", "enderman", "snow_golem", "strider":
		return true
	default:
		return false
	}
}

func (s *Server) consumeThrownPotionSlot(session *PlayerSession, slot int, stack world.ItemStack) {
	if session == nil || slot < 0 || slot >= world.PlayerInventorySlots {
		return
	}
	if session.Snapshot().GameMode != GameModeCreative {
		rem := stack.Count - 1
		if rem <= 0 {
			SetSlotPotionType(session, slot, "")
			session.SetInventorySlot(slot, world.ItemStack{})
		} else {
			session.SetInventorySlot(slot, world.NewItemStack(stack.ResolvedItemID(), rem))
		}
	}
	if session.send != nil {
		stateID := session.NextContainerStateID()
		updated := session.InventorySlot(slot)
		_ = session.send(protocol.PlayContainerSetSlot, encodeContainerSetSlotWithPotion(0, stateID, slot, updated, GetSlotPotionType(session, slot)))
	}
}

// --- Lingering Potion Area Effect Clouds ---

// LingeringPotionCloud represents an active area_effect_cloud spawned by a
// lingering potion.
type LingeringPotionCloud struct {
	ID            int32
	Dimension     string
	X, Y, Z       float64
	PotionID      string
	Effects       []StatusEffectInstance
	Radius        float32
	RadiusPerTick float32
	RadiusOnUse   float32
	Age           int32
	Duration      int32
	WaitTime      int32
	victimNextUse map[int32]int32
}

// Clone returns a deep copy of c.
func (c LingeringPotionCloud) Clone() LingeringPotionCloud {
	cp := c
	if len(c.Effects) > 0 {
		cp.Effects = make([]StatusEffectInstance, len(c.Effects))
		for i, e := range c.Effects {
			cp.Effects[i] = e.Clone()
		}
	}
	if len(c.victimNextUse) > 0 {
		cp.victimNextUse = make(map[int32]int32, len(c.victimNextUse))
		for k, v := range c.victimNextUse {
			cp.victimNextUse[k] = v
		}
	}
	return cp
}

type serverLingeringCloudState struct {
	mu     sync.Mutex
	nextID int32
	clouds map[int32]*LingeringPotionCloud
}

var serverLingeringClouds sync.Map // map[*Server]*serverLingeringCloudState

func (s *Server) lingeringCloudState() *serverLingeringCloudState {
	if v, ok := serverLingeringClouds.Load(s); ok {
		return v.(*serverLingeringCloudState)
	}
	actual, _ := serverLingeringClouds.LoadOrStore(s, &serverLingeringCloudState{
		clouds: make(map[int32]*LingeringPotionCloud),
	})
	return actual.(*serverLingeringCloudState)
}

// ThrowLingeringPotion consumes one minecraft:lingering_potion from session's
// hand and spawns a lingering area effect cloud at the player's position.
func (s *Server) ThrowLingeringPotion(session *PlayerSession, hand int32) (int32, bool) {
	if s == nil || session == nil || session.IsDead() {
		return 0, false
	}
	slot, stack := session.handSlotAndStack(hand)
	if !IsLingeringPotionItem(stack) {
		return 0, false
	}
	potionID := GetSlotPotionType(session, slot)
	if potionID == "" {
		potionID = PotionWater
	}
	snap := session.Snapshot()
	cx := int32(int64(math.Floor(snap.X)) >> 4)
	cz := int32(int64(math.Floor(snap.Z)) >> 4)
	s.BroadcastDimensionChunk(snap.Dimension, cx, cz, PlaySoundPacketID,
		EncodeSoundPacket(SoundLingeringPotionThrow, SoundSourcePlayers, snap.X, snap.Y, snap.Z, 0.5, 0.4, 0))
	s.consumeThrownPotionSlot(session, slot, stack)
	cloudID := s.SpawnLingeringPotionCloud(snap.Dimension, snap.X, snap.Y, snap.Z, potionID)
	return cloudID, cloudID > 0
}

// SpawnLingeringPotionCloud spawns a vanilla 26.1.2 lingering potion cloud at
// (x, y, z) in dim and returns its cloud ID.
func (s *Server) SpawnLingeringPotionCloud(dim string, x, y, z float64, potionID string) int32 {
	if s == nil {
		return 0
	}
	dim = world.NormalizeDimension(dim)
	potionID = canonicalPotionID(potionID)
	s.broadcastPotionBreakEffect(dim, x, y, z, potionID)
	effects := PotionEffects(potionID)

	st := s.lingeringCloudState()
	st.mu.Lock()
	st.nextID++
	id := st.nextID
	cloud := &LingeringPotionCloud{
		ID:            id,
		Dimension:     dim,
		X:             x,
		Y:             y,
		Z:             z,
		PotionID:      potionID,
		Effects:       effects,
		Radius:        LingeringCloudInitialRadius,
		RadiusPerTick: -LingeringCloudInitialRadius / float32(LingeringCloudDefaultDuration),
		RadiusOnUse:   LingeringCloudRadiusOnUse,
		Age:           0,
		Duration:      LingeringCloudDefaultDuration,
		WaitTime:      LingeringCloudWaitTime,
		victimNextUse: make(map[int32]int32),
	}
	st.clouds[id] = cloud
	st.mu.Unlock()
	return id
}

// ActiveLingeringClouds returns snapshots of all currently active lingering
// potion clouds sorted by ID.
func (s *Server) ActiveLingeringClouds() []LingeringPotionCloud {
	if s == nil {
		return nil
	}
	st := s.lingeringCloudState()
	st.mu.Lock()
	defer st.mu.Unlock()
	if len(st.clouds) == 0 {
		return nil
	}
	out := make([]LingeringPotionCloud, 0, len(st.clouds))
	for _, c := range st.clouds {
		out = append(out, c.Clone())
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	return out
}

// TickLingeringClouds advances all active lingering potion clouds by one tick:
// shrinking radius, applying 0.25x duration / 0.5x instant potency to entities
// inside the cloud after WaitTime, and removing expired clouds.
func (s *Server) TickLingeringClouds() int {
	if s == nil {
		return 0
	}
	st := s.lingeringCloudState()
	st.mu.Lock()
	if len(st.clouds) == 0 {
		st.mu.Unlock()
		return 0
	}
	ids := make([]int32, 0, len(st.clouds))
	for id := range st.clouds {
		ids = append(ids, id)
	}
	st.mu.Unlock()
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	appliedCount := 0
	for _, id := range ids {
		st.mu.Lock()
		c, ok := st.clouds[id]
		if !ok {
			st.mu.Unlock()
			continue
		}
		c.Age++
		if c.Age >= c.WaitTime+c.Duration {
			delete(st.clouds, id)
			st.mu.Unlock()
			continue
		}
		if c.RadiusPerTick != 0 {
			c.Radius += c.RadiusPerTick
			if c.Radius < LingeringCloudMinRadius {
				delete(st.clouds, id)
				st.mu.Unlock()
				continue
			}
		}
		if c.Age < c.WaitTime || len(c.Effects) == 0 {
			st.mu.Unlock()
			continue
		}
		// Clean up expired victim cooldowns.
		for vid, due := range c.victimNextUse {
			if c.Age >= due {
				delete(c.victimNextUse, vid)
			}
		}
		snapCloud := c.Clone()
		st.mu.Unlock()

		// Check every 5 ticks matching AreaEffectCloud.serverTick.
		if snapCloud.Age%5 != 0 {
			continue
		}

		// Find players inside the horizontal radius and vertical bounds.
		s.playersMu.RLock()
		players := make([]*PlayerSession, 0, len(s.players))
		for _, p := range s.players {
			players = append(players, p)
		}
		s.playersMu.RUnlock()

		radiusSq := float64(snapCloud.Radius * snapCloud.Radius)
		for _, p := range players {
			if p == nil || p.IsDead() {
				continue
			}
			ps := p.Snapshot()
			if world.NormalizeDimension(ps.Dimension) != snapCloud.Dimension {
				continue
			}
			if _, onCooldown := snapCloud.victimNextUse[p.EntityID]; onCooldown {
				continue
			}
			dx := ps.X - snapCloud.X
			dz := ps.Z - snapCloud.Z
			dy := ps.Y - snapCloud.Y
			if dx*dx+dz*dz > radiusSq || dy < -1.0 || dy > 2.0 {
				continue
			}

			victimHit := false
			for _, eff := range snapCloud.Effects {
				def, valid := MobEffectByID(eff.ID)
				if !valid {
					continue
				}
				if def.Instantaneous {
					// Lingering clouds apply instant effects at 0.5x potency.
					if s.ApplyPlayerEffectScaled(p, eff, 0.5) {
						victimHit = true
					}
				} else {
					// Lingering clouds apply timed effects at 1/4 duration.
					lingeringEff := eff.Clone()
					lingeringEff.Duration = eff.Duration / 4
					if lingeringEff.Duration > 0 && s.ApplyPlayerEffect(p, lingeringEff) {
						victimHit = true
					}
				}
			}

			if victimHit {
				appliedCount++
				st.mu.Lock()
				if live, exists := st.clouds[id]; exists {
					live.victimNextUse[p.EntityID] = live.Age + LingeringCloudReapplyDelayTicks
					if live.RadiusOnUse != 0 {
						live.Radius += live.RadiusOnUse
						if live.Radius < LingeringCloudMinRadius {
							delete(st.clouds, id)
						}
					}
				}
				st.mu.Unlock()
			}
		}

		// Also affect living non-player entities inside the lingering cloud.
		if s.entities != nil {
			cx := int32(int64(math.Floor(snapCloud.X)) >> 4)
			cz := int32(int64(math.Floor(snapCloud.Z)) >> 4)
			ents := s.entities.AppendInChunkRange(nil, cx, cz, 1)
			for i := range ents {
				ent := &ents[i]
				if ent.EffectiveDimension() != snapCloud.Dimension || ent.IsItem() || ent.IsExperienceOrb() {
					continue
				}
				if _, onCooldown := snapCloud.victimNextUse[ent.ID]; onCooldown {
					continue
				}
				dx := ent.X - snapCloud.X
				dz := ent.Z - snapCloud.Z
				dy := ent.Y - snapCloud.Y
				if dx*dx+dz*dz > radiusSq || dy < -1.0 || dy > 2.0 {
					continue
				}

				victimHit := false
				for _, eff := range snapCloud.Effects {
					def, valid := MobEffectByID(eff.ID)
					if !valid {
						continue
					}
					if def.Instantaneous {
						if s.ApplyEntityEffectScaled(ent.ID, eff, 0.5) {
							victimHit = true
						}
					} else {
						lingeringEff := eff.Clone()
						lingeringEff.Duration = eff.Duration / 4
						if lingeringEff.Duration > 0 && s.ApplyEntityEffectScaled(ent.ID, lingeringEff, 1.0) {
							victimHit = true
						}
					}
				}

				if victimHit {
					appliedCount++
					st.mu.Lock()
					if live, exists := st.clouds[id]; exists {
						live.victimNextUse[ent.ID] = live.Age + LingeringCloudReapplyDelayTicks
						if live.RadiusOnUse != 0 {
							live.Radius += live.RadiusOnUse
							if live.Radius < LingeringCloudMinRadius {
								delete(st.clouds, id)
							}
						}
					}
					st.mu.Unlock()
				}
			}
		}
	}
	return appliedCount
}

// --- Player Potion Metadata NBT Persistence ---

// SavePotionMetaToPlayerCompound writes non-empty slot potion types onto a
// playerdata root NBT compound under "SlotPotions".
func SavePotionMetaToPlayerCompound(session *PlayerSession, root *nbt.Compound) {
	if session == nil || root == nil {
		return
	}
	var elems []nbt.Tag
	for slot := 0; slot < world.PlayerInventorySlots; slot++ {
		st := session.InventorySlot(slot)
		if !IsPotionOrTippedArrowItem(st) {
			continue
		}
		pType := GetSlotPotionType(session, slot)
		if pType == "" {
			continue
		}
		entry := nbt.NewCompound().
			Set("Slot", nbt.Byte(slot)).
			Set("Potion", nbt.String(pType))
		elems = append(elems, entry)
	}
	if len(elems) > 0 {
		root.Set("SlotPotions", nbt.List{
			ElemID: nbt.TagCompound,
			Elems:  elems,
		})
	}
}

// LoadPotionMetaFromPlayerCompound restores per-slot potion types from a
// playerdata root NBT compound.
func LoadPotionMetaFromPlayerCompound(session *PlayerSession, root *nbt.Compound) {
	if session == nil || root == nil {
		return
	}
	tag, ok := root.Get("SlotPotions")
	if !ok {
		return
	}
	var elems []nbt.Tag
	switch v := tag.(type) {
	case nbt.List:
		elems = v.Elems
	case *nbt.List:
		if v != nil {
			elems = v.Elems
		}
	default:
		return
	}
	for _, elem := range elems {
		comp, ok := elem.(*nbt.Compound)
		if !ok {
			continue
		}
		slotTag, ok1 := comp.Get("Slot")
		potTag, ok2 := comp.Get("Potion")
		if !ok1 || !ok2 {
			continue
		}
		sb, ok1 := slotTag.(nbt.Byte)
		ps, ok2 := potTag.(nbt.String)
		if !ok1 || !ok2 {
			continue
		}
		slot := int(uint8(sb))
		if slot >= 0 && slot < world.PlayerInventorySlots {
			SetSlotPotionType(session, slot, string(ps))
		}
	}
}
