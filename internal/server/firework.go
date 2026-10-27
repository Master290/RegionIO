package server

import (
	"fmt"
	"math"
	"math/rand"
	"strings"
	"sync"

	"regionio/internal/nbt"
	"regionio/internal/protocol"
	"regionio/internal/registry"
	"regionio/internal/world"
)

// Firework Rocket & Firework Star constants for Minecraft 26.1.2 (protocol 775).
const (
	// EntityTypeFireworkRocket is the canonical entity type identifier.
	EntityTypeFireworkRocket = "minecraft:firework_rocket"
	// EntityTypeFireworkRocketID is BuiltInRegistries.ENTITY_TYPE index 54 in 26.1.2.
	EntityTypeFireworkRocketID int = 54

	// Canonical item identifiers.
	FireworkRocketItemID = "minecraft:firework_rocket"
	FireworkStarItemID   = "minecraft:firework_star"
	CrossbowItemID       = "minecraft:crossbow"

	// BuiltInRegistries.DATA_COMPONENT_TYPE indices in 26.1.2 (protocol 775).
	DataComponentChargedProjectiles int32 = 49 // "minecraft:charged_projectiles"
	DataComponentFireworkExplosion  int32 = 68 // "minecraft:firework_explosion"
	DataComponentFireworks          int32 = 69 // "minecraft:fireworks"

	// FireworkRocketEntity SynchedEntityData accessor IDs in 26.1.2.
	EntityDataFireworkItemID           byte = 8  // ITEM_STACK serializer (7)
	EntityDataFireworkAttachedTargetID byte = 9  // OPTIONAL_UNSIGNED_INT serializer (20)
	EntityDataFireworkShotAtAngleID    byte = 10 // BOOLEAN serializer (8)

	// EntityDataSerializers IDs in 26.1.2.
	EntityDataSerializerOptionalUnsignedInt int32 = 20

	// EntityEvent.FIREWORKS_EXPLODE (17) carried by ClientboundEntityEventPacket (0x22).
	EntityEventFireworkExplode byte = 17

	// Limits and gameplay parameters.
	MaxFireworkExplosions          int     = 256
	DefaultFireworkFlightDuration  int32   = 1
	MaxCraftFireworkFlightDuration int32   = 3
	FireworkExplosionDamageRadius  float64 = 5.0
	FireworkExplosionBaseDamage    float32 = 5.0
	FireworkExplosionDamagePerStar float32 = 2.0
	CrossbowFireworkShootPower     float64 = 1.6
	CrossbowArrowShootPower        float64 = 3.15
	CrossbowFireworkDurabilityCost int32   = 3
	CrossbowArrowDurabilityCost    int32   = 1
)

// FireworkShape represents net.minecraft.world.item.component.FireworkExplosion$Shape.
type FireworkShape int32

const (
	FireworkShapeSmallBall FireworkShape = 0 // "small_ball"
	FireworkShapeLargeBall FireworkShape = 1 // "large_ball"
	FireworkShapeStar      FireworkShape = 2 // "star"
	FireworkShapeCreeper   FireworkShape = 3 // "creeper"
	FireworkShapeBurst     FireworkShape = 4 // "burst"
)

// String returns the canonical vanilla serialized name of the firework shape.
func (sh FireworkShape) String() string {
	switch sh {
	case FireworkShapeSmallBall:
		return "small_ball"
	case FireworkShapeLargeBall:
		return "large_ball"
	case FireworkShapeStar:
		return "star"
	case FireworkShapeCreeper:
		return "creeper"
	case FireworkShapeBurst:
		return "burst"
	default:
		return "small_ball"
	}
}

// ParseFireworkShape parses a shape name ("small_ball", "large_ball", "star",
// "creeper", "burst") into a FireworkShape.
func ParseFireworkShape(name string) (FireworkShape, bool) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "small_ball":
		return FireworkShapeSmallBall, true
	case "large_ball":
		return FireworkShapeLargeBall, true
	case "star":
		return FireworkShapeStar, true
	case "creeper":
		return FireworkShapeCreeper, true
	case "burst":
		return FireworkShapeBurst, true
	default:
		return FireworkShapeSmallBall, false
	}
}

// VanillaDyeFireworkColors maps canonical dye item IDs and color names to their
// exact vanilla 26.1.2 DyeColor.getFireworkColor() 24-bit RGB values.
var VanillaDyeFireworkColors = map[string]int32{
	"white":      0xF0F0F0, // 15790320
	"orange":     0xEB8844, // 15435844
	"magenta":    0xC354CD, // 12801229
	"light_blue": 0x6689D3, // 6719955
	"yellow":     0xDECF2A, // 14602026
	"lime":       0x41CD34, // 4312372
	"pink":       0xD88198, // 14188952
	"gray":       0x434343, // 4408131
	"light_gray": 0xABABAB, // 11250603
	"cyan":       0x287697, // 2651799
	"purple":     0x7B2FBE, // 8073150
	"blue":       0x253192, // 2437522
	"brown":      0x51301A, // 5320730
	"green":      0x3B511A, // 3887386
	"red":        0xB3312C, // 11743532
	"black":      0x1E1B1B, // 1973019
}

// FireworkColorFromDyeItem returns the vanilla 24-bit RGB firework color for a
// dye item (e.g. "minecraft:red_dye" or "red"), or false if not a dye.
func FireworkColorFromDyeItem(itemName string) (int32, bool) {
	short := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(itemName)), "minecraft:")
	short = strings.TrimSuffix(short, "_dye")
	rgb, ok := VanillaDyeFireworkColors[short]
	return rgb, ok
}

// FireworkShapeModifierFromItem returns the FireworkShape modifier granted by
// a crafting ingredient in FireworkStarRecipe:
//   - fire_charge -> LARGE_BALL
//   - gold_nugget -> STAR
//   - feather     -> BURST
//   - *_head / *_skull -> CREEPER
func FireworkShapeModifierFromItem(itemName string) (FireworkShape, bool) {
	short := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(itemName)), "minecraft:")
	switch short {
	case "fire_charge":
		return FireworkShapeLargeBall, true
	case "gold_nugget":
		return FireworkShapeStar, true
	case "feather":
		return FireworkShapeBurst, true
	case "skeleton_skull", "wither_skeleton_skull", "player_head",
		"zombie_head", "creeper_head", "dragon_head", "piglin_head":
		return FireworkShapeCreeper, true
	default:
		return FireworkShapeSmallBall, false
	}
}

// FireworkExplosion represents the `minecraft:firework_explosion` data component
// stored on a `minecraft:firework_star` and inside `minecraft:fireworks`.
type FireworkExplosion struct {
	Shape      FireworkShape
	Colors     []int32
	FadeColors []int32
	HasTrail   bool
	HasTwinkle bool
}

// Clone returns a deep copy of e.
func (e FireworkExplosion) Clone() FireworkExplosion {
	out := FireworkExplosion{
		Shape:      e.Shape,
		HasTrail:   e.HasTrail,
		HasTwinkle: e.HasTwinkle,
	}
	if len(e.Colors) > 0 {
		out.Colors = make([]int32, len(e.Colors))
		copy(out.Colors, e.Colors)
	}
	if len(e.FadeColors) > 0 {
		out.FadeColors = make([]int32, len(e.FadeColors))
		copy(out.FadeColors, e.FadeColors)
	}
	return out
}

// FireworksComponent represents the `minecraft:fireworks` data component stored
// on a `minecraft:firework_rocket`.
type FireworksComponent struct {
	FlightDuration int32
	Explosions     []FireworkExplosion
}

// Clone returns a deep copy of f.
func (f FireworksComponent) Clone() FireworksComponent {
	dur := f.FlightDuration
	if dur <= 0 {
		dur = DefaultFireworkFlightDuration
	}
	out := FireworksComponent{
		FlightDuration: dur,
	}
	if len(f.Explosions) > 0 {
		out.Explosions = make([]FireworkExplosion, len(f.Explosions))
		for i, exp := range f.Explosions {
			out.Explosions[i] = exp.Clone()
		}
	}
	return out
}

// ChargedProjectile represents an ammo stack loaded into a `minecraft:crossbow`
// (`minecraft:charged_projectiles` component).
type ChargedProjectile struct {
	Stack     world.ItemStack
	Fireworks *FireworksComponent
}

// Clone returns a deep copy of cp.
func (cp ChargedProjectile) Clone() ChargedProjectile {
	out := ChargedProjectile{Stack: cp.Stack}
	if cp.Fireworks != nil {
		fw := cp.Fireworks.Clone()
		out.Fireworks = &fw
	}
	return out
}

// EncodeFireworkExplosionComponent writes the protocol 775 payload of
// DataComponentFireworkExplosion (68):
//   - Shape: VarInt (0..4)
//   - Colors: VarInt(len) + Int32[len]
//   - FadeColors: VarInt(len) + Int32[len]
//   - HasTrail: Bool
//   - HasTwinkle: Bool
func EncodeFireworkExplosionComponent(w *protocol.Writer, exp FireworkExplosion) {
	sh := exp.Shape
	if sh < FireworkShapeSmallBall || sh > FireworkShapeBurst {
		sh = FireworkShapeSmallBall
	}
	w.VarInt(int32(sh))
	w.VarInt(int32(len(exp.Colors)))
	for _, c := range exp.Colors {
		w.Int32(c)
	}
	w.VarInt(int32(len(exp.FadeColors)))
	for _, c := range exp.FadeColors {
		w.Int32(c)
	}
	w.Bool(exp.HasTrail)
	w.Bool(exp.HasTwinkle)
}

// DecodeFireworkExplosionComponent reads the protocol 775 payload of
// DataComponentFireworkExplosion (68).
func DecodeFireworkExplosionComponent(r *protocol.Reader) (FireworkExplosion, error) {
	shRaw, err := r.VarInt()
	if err != nil {
		return FireworkExplosion{}, err
	}
	if shRaw < 0 || shRaw > int32(FireworkShapeBurst) {
		return FireworkExplosion{}, fmt.Errorf("server: invalid firework shape %d", shRaw)
	}
	cLen, err := r.VarInt()
	if err != nil {
		return FireworkExplosion{}, err
	}
	if cLen < 0 || cLen > 256 {
		return FireworkExplosion{}, fmt.Errorf("server: invalid firework color count %d", cLen)
	}
	colors := make([]int32, cLen)
	for i := int32(0); i < cLen; i++ {
		v, err := r.Int32()
		if err != nil {
			return FireworkExplosion{}, err
		}
		colors[i] = v
	}
	fLen, err := r.VarInt()
	if err != nil {
		return FireworkExplosion{}, err
	}
	if fLen < 0 || fLen > 256 {
		return FireworkExplosion{}, fmt.Errorf("server: invalid firework fade color count %d", fLen)
	}
	fades := make([]int32, fLen)
	for i := int32(0); i < fLen; i++ {
		v, err := r.Int32()
		if err != nil {
			return FireworkExplosion{}, err
		}
		fades[i] = v
	}
	trail, err := r.Bool()
	if err != nil {
		return FireworkExplosion{}, err
	}
	twinkle, err := r.Bool()
	if err != nil {
		return FireworkExplosion{}, err
	}
	return FireworkExplosion{
		Shape:      FireworkShape(shRaw),
		Colors:     colors,
		FadeColors: fades,
		HasTrail:   trail,
		HasTwinkle: twinkle,
	}, nil
}

// EncodeFireworksComponent writes the protocol 775 payload of
// DataComponentFireworks (69):
//   - FlightDuration: VarInt
//   - Explosions: VarInt(len <= 256) + FireworkExplosion[len]
func EncodeFireworksComponent(w *protocol.Writer, fw FireworksComponent) {
	dur := fw.FlightDuration
	if dur < 0 {
		dur = 0
	}
	w.VarInt(dur)
	exps := fw.Explosions
	if len(exps) > MaxFireworkExplosions {
		exps = exps[:MaxFireworkExplosions]
	}
	w.VarInt(int32(len(exps)))
	for _, exp := range exps {
		EncodeFireworkExplosionComponent(w, exp)
	}
}

// DecodeFireworksComponent reads the protocol 775 payload of
// DataComponentFireworks (69).
func DecodeFireworksComponent(r *protocol.Reader) (FireworksComponent, error) {
	dur, err := r.VarInt()
	if err != nil {
		return FireworksComponent{}, err
	}
	if dur < 0 || dur > 255 {
		return FireworksComponent{}, fmt.Errorf("server: invalid firework flight duration %d", dur)
	}
	count, err := r.VarInt()
	if err != nil {
		return FireworksComponent{}, err
	}
	if count < 0 || int(count) > MaxFireworkExplosions {
		return FireworksComponent{}, fmt.Errorf("server: invalid firework explosion count %d", count)
	}
	exps := make([]FireworkExplosion, count)
	for i := int32(0); i < count; i++ {
		exp, err := DecodeFireworkExplosionComponent(r)
		if err != nil {
			return FireworksComponent{}, err
		}
		exps[i] = exp
	}
	return FireworksComponent{
		FlightDuration: dur,
		Explosions:     exps,
	}, nil
}

// EncodeSlotWithFireworkStar writes a protocol 775 ItemStack carrying a
// `minecraft:firework_explosion` (68) component.
func EncodeSlotWithFireworkStar(w *protocol.Writer, stack world.ItemStack, exp FireworkExplosion) {
	if stack.IsEmpty() {
		w.VarInt(0)
		return
	}
	itemID := stack.ResolvedItemID()
	if itemID <= 0 {
		w.VarInt(0)
		return
	}
	w.VarInt(stack.Count)
	w.VarInt(itemID)
	w.VarInt(1) // 1 added component
	w.VarInt(0) // 0 removed components
	w.VarInt(DataComponentFireworkExplosion)
	EncodeFireworkExplosionComponent(w, exp)
}

// EncodeSlotWithFireworks writes a protocol 775 ItemStack carrying a
// `minecraft:fireworks` (69) component.
func EncodeSlotWithFireworks(w *protocol.Writer, stack world.ItemStack, fw FireworksComponent) {
	if stack.IsEmpty() {
		w.VarInt(0)
		return
	}
	itemID := stack.ResolvedItemID()
	if itemID <= 0 {
		w.VarInt(0)
		return
	}
	w.VarInt(stack.Count)
	w.VarInt(itemID)
	w.VarInt(1) // 1 added component
	w.VarInt(0) // 0 removed components
	w.VarInt(DataComponentFireworks)
	EncodeFireworksComponent(w, fw)
}

// WriteFireworkExplosionNBT serializes a FireworkExplosion into an NBT Compound.
func WriteFireworkExplosionNBT(exp FireworkExplosion) *nbt.Compound {
	c := nbt.NewCompound()
	c.Set("shape", nbt.String(exp.Shape.String()))
	if len(exp.Colors) > 0 {
		cols := make([]int32, len(exp.Colors))
		copy(cols, exp.Colors)
		c.Set("colors", nbt.IntArray(cols))
	}
	if len(exp.FadeColors) > 0 {
		fades := make([]int32, len(exp.FadeColors))
		copy(fades, exp.FadeColors)
		c.Set("fade_colors", nbt.IntArray(fades))
	}
	if exp.HasTrail {
		c.Set("has_trail", nbt.Byte(1))
	}
	if exp.HasTwinkle {
		c.Set("has_twinkle", nbt.Byte(1))
	}
	return c
}

// ReadFireworkExplosionNBT deserializes a FireworkExplosion from an NBT Compound.
func ReadFireworkExplosionNBT(c *nbt.Compound) (FireworkExplosion, bool) {
	if c == nil {
		return FireworkExplosion{}, false
	}
	var exp FireworkExplosion
	if st, ok := c.Get("shape"); ok {
		if s, ok := st.(nbt.String); ok {
			if parsed, valid := ParseFireworkShape(string(s)); valid {
				exp.Shape = parsed
			}
		}
	}
	if ct, ok := c.Get("colors"); ok {
		if arr, ok := ct.(nbt.IntArray); ok {
			exp.Colors = append(exp.Colors, []int32(arr)...)
		}
	}
	if ft, ok := c.Get("fade_colors"); ok {
		if arr, ok := ft.(nbt.IntArray); ok {
			exp.FadeColors = append(exp.FadeColors, []int32(arr)...)
		}
	}
	if tr, ok := c.Get("has_trail"); ok {
		if b, ok := tr.(nbt.Byte); ok && b != 0 {
			exp.HasTrail = true
		}
	}
	if tw, ok := c.Get("has_twinkle"); ok {
		if b, ok := tw.(nbt.Byte); ok && b != 0 {
			exp.HasTwinkle = true
		}
	}
	return exp, true
}

// WriteFireworksNBT serializes a FireworksComponent into an NBT Compound.
func WriteFireworksNBT(fw FireworksComponent) *nbt.Compound {
	c := nbt.NewCompound()
	dur := fw.FlightDuration
	if dur <= 0 {
		dur = DefaultFireworkFlightDuration
	}
	c.Set("flight_duration", nbt.Byte(byte(dur)))
	elems := make([]nbt.Tag, 0, len(fw.Explosions))
	for _, exp := range fw.Explosions {
		elems = append(elems, WriteFireworkExplosionNBT(exp))
	}
	c.Set("explosions", nbt.List{ElemID: nbt.TagCompound, Elems: elems})
	return c
}

// ReadFireworksNBT deserializes a FireworksComponent from an NBT Compound.
func ReadFireworksNBT(c *nbt.Compound) (FireworksComponent, bool) {
	if c == nil {
		return FireworksComponent{}, false
	}
	fw := FireworksComponent{FlightDuration: DefaultFireworkFlightDuration}
	if dt, ok := c.Get("flight_duration"); ok {
		switch v := dt.(type) {
		case nbt.Byte:
			fw.FlightDuration = int32(v)
		case nbt.Int:
			fw.FlightDuration = int32(v)
		}
	}
	if et, ok := c.Get("explosions"); ok {
		if list, ok := et.(nbt.List); ok {
			for _, elem := range list.Elems {
				if ec, ok := elem.(*nbt.Compound); ok {
					if exp, valid := ReadFireworkExplosionNBT(ec); valid {
						fw.Explosions = append(fw.Explosions, exp)
					}
				}
			}
		}
	}
	return fw, true
}

// Per-player slot tracking for firework stars, firework rockets, and charged crossbows.
type playerFireworkSlotState struct {
	mu          sync.Mutex
	stars       map[int]FireworkExplosion
	fireworks   map[int]FireworksComponent
	crossbows   map[int][]ChargedProjectile
	carriedStar *FireworkExplosion
	carriedFW   *FireworksComponent
}

var playerFireworkSlots sync.Map // map[*PlayerSession]*playerFireworkSlotState

func getFireworkSlotState(session *PlayerSession) *playerFireworkSlotState {
	if session == nil {
		return nil
	}
	if v, ok := playerFireworkSlots.Load(session); ok {
		return v.(*playerFireworkSlotState)
	}
	initSt := &playerFireworkSlotState{
		stars:     make(map[int]FireworkExplosion),
		fireworks: make(map[int]FireworksComponent),
		crossbows: make(map[int][]ChargedProjectile),
	}
	actual, _ := playerFireworkSlots.LoadOrStore(session, initSt)
	return actual.(*playerFireworkSlotState)
}

// GetSlotFireworkStar returns the FireworkExplosion stored in session's inventory slot.
func GetSlotFireworkStar(session *PlayerSession, slot int) (FireworkExplosion, bool) {
	if session == nil {
		return FireworkExplosion{}, false
	}
	st := getFireworkSlotState(session)
	st.mu.Lock()
	defer st.mu.Unlock()
	exp, ok := st.stars[slot]
	if !ok {
		return FireworkExplosion{}, false
	}
	return exp.Clone(), true
}

// SetSlotFireworkStar stores a FireworkExplosion for session's inventory slot.
func SetSlotFireworkStar(session *PlayerSession, slot int, exp FireworkExplosion) {
	if session == nil {
		return
	}
	st := getFireworkSlotState(session)
	st.mu.Lock()
	st.stars[slot] = exp.Clone()
	st.mu.Unlock()
}

// ClearSlotFireworkStar removes any FireworkExplosion from session's inventory slot.
func ClearSlotFireworkStar(session *PlayerSession, slot int) {
	if session == nil {
		return
	}
	st := getFireworkSlotState(session)
	st.mu.Lock()
	delete(st.stars, slot)
	st.mu.Unlock()
}

// GetSlotFireworks returns the FireworksComponent stored in session's inventory
// slot (or a default FlightDuration=1 component if the slot holds a firework_rocket).
func GetSlotFireworks(session *PlayerSession, slot int) (FireworksComponent, bool) {
	if session == nil {
		return FireworksComponent{}, false
	}
	st := getFireworkSlotState(session)
	st.mu.Lock()
	fw, ok := st.fireworks[slot]
	st.mu.Unlock()
	if ok {
		return fw.Clone(), true
	}
	stack := session.InventorySlot(slot)
	if !stack.IsEmpty() && canonicalItemName(stack.ResolvedID()) == FireworkRocketItemID {
		return FireworksComponent{FlightDuration: DefaultFireworkFlightDuration}, true
	}
	return FireworksComponent{}, false
}

// SetSlotFireworks stores a FireworksComponent for session's inventory slot.
func SetSlotFireworks(session *PlayerSession, slot int, fw FireworksComponent) {
	if session == nil {
		return
	}
	st := getFireworkSlotState(session)
	st.mu.Lock()
	st.fireworks[slot] = fw.Clone()
	st.mu.Unlock()
}

// ClearSlotFireworks removes any FireworksComponent from session's inventory slot.
func ClearSlotFireworks(session *PlayerSession, slot int) {
	if session == nil {
		return
	}
	st := getFireworkSlotState(session)
	st.mu.Lock()
	delete(st.fireworks, slot)
	st.mu.Unlock()
}

// GetSlotChargedProjectiles returns the charged projectiles loaded in a crossbow
// at session's inventory slot.
func GetSlotChargedProjectiles(session *PlayerSession, slot int) []ChargedProjectile {
	if session == nil {
		return nil
	}
	st := getFireworkSlotState(session)
	st.mu.Lock()
	defer st.mu.Unlock()
	projs := st.crossbows[slot]
	if len(projs) == 0 {
		return nil
	}
	out := make([]ChargedProjectile, len(projs))
	for i, p := range projs {
		out[i] = p.Clone()
	}
	return out
}

// SetSlotChargedProjectiles sets the charged projectiles loaded in a crossbow
// at session's inventory slot.
func SetSlotChargedProjectiles(session *PlayerSession, slot int, projs []ChargedProjectile) {
	if session == nil {
		return
	}
	st := getFireworkSlotState(session)
	st.mu.Lock()
	if len(projs) == 0 {
		delete(st.crossbows, slot)
	} else {
		copied := make([]ChargedProjectile, len(projs))
		for i, p := range projs {
			copied[i] = p.Clone()
		}
		st.crossbows[slot] = copied
	}
	st.mu.Unlock()
}

// ClearSlotChargedProjectiles clears any charged projectiles from session's inventory slot.
func ClearSlotChargedProjectiles(session *PlayerSession, slot int) {
	SetSlotChargedProjectiles(session, slot, nil)
}

// MatchFireworkStarRecipe implements vanilla 26.1.2 FireworkStarRecipe:
//   - Exactly 1x minecraft:gunpowder
//   - 1..8x dye items (minecraft:<color>_dye) -> Colors
//   - At most 1 shape modifier (fire_charge -> LARGE_BALL, gold_nugget -> STAR,
//     feather -> BURST, any skull/head -> CREEPER; default -> SMALL_BALL)
//   - Optional 1x minecraft:diamond -> HasTrail = true
//   - Optional 1x minecraft:glowstone_dust -> HasTwinkle = true
//   - Produces 1x minecraft:firework_star with the resulting FireworkExplosion
func MatchFireworkStarRecipe(ingredients []world.ItemStack) (world.ItemStack, FireworkExplosion, bool) {
	var (
		hasGunpowder  bool
		hasShapeMod   bool
		hasTrail      bool
		hasTwinkle    bool
		shape         = FireworkShapeSmallBall
		colors        []int32
	)
	for _, ing := range ingredients {
		if ing.IsEmpty() {
			continue
		}
		itemID := canonicalItemName(ing.ResolvedID())
		switch itemID {
		case "minecraft:gunpowder":
			if hasGunpowder {
				return world.ItemStack{}, FireworkExplosion{}, false
			}
			hasGunpowder = true
		case "minecraft:diamond":
			if hasTrail {
				return world.ItemStack{}, FireworkExplosion{}, false
			}
			hasTrail = true
		case "minecraft:glowstone_dust":
			if hasTwinkle {
				return world.ItemStack{}, FireworkExplosion{}, false
			}
			hasTwinkle = true
		default:
			if modShape, isMod := FireworkShapeModifierFromItem(itemID); isMod {
				if hasShapeMod {
					return world.ItemStack{}, FireworkExplosion{}, false
				}
				hasShapeMod = true
				shape = modShape
			} else if rgb, isDye := FireworkColorFromDyeItem(itemID); isDye {
				colors = append(colors, rgb)
			} else {
				return world.ItemStack{}, FireworkExplosion{}, false
			}
		}
	}
	if !hasGunpowder || len(colors) == 0 {
		return world.ItemStack{}, FireworkExplosion{}, false
	}
	outStack := world.NewNamedItemStack(FireworkStarItemID, 1)
	return outStack, FireworkExplosion{
		Shape:      shape,
		Colors:     colors,
		HasTrail:   hasTrail,
		HasTwinkle: hasTwinkle,
	}, true
}

// MatchFireworkStarFadeRecipe implements vanilla 26.1.2 FireworkStarFadeRecipe:
//   - Exactly 1x minecraft:firework_star
//   - 1..8x dye items (minecraft:<color>_dye)
//   - Produces 1x minecraft:firework_star preserving the input star's Shape,
//     Colors, HasTrail, and HasTwinkle while setting FadeColors to the dye RGBs.
func MatchFireworkStarFadeRecipe(ingredients []world.ItemStack, inputStars []FireworkExplosion) (world.ItemStack, FireworkExplosion, bool) {
	var (
		starCount  int
		starExp    FireworkExplosion
		fadeColors []int32
	)
	for i, ing := range ingredients {
		if ing.IsEmpty() {
			continue
		}
		itemID := canonicalItemName(ing.ResolvedID())
		if itemID == FireworkStarItemID {
			if starCount > 0 {
				return world.ItemStack{}, FireworkExplosion{}, false
			}
			starCount++
			if i < len(inputStars) {
				starExp = inputStars[i].Clone()
			}
			continue
		}
		if rgb, isDye := FireworkColorFromDyeItem(itemID); isDye {
			fadeColors = append(fadeColors, rgb)
			continue
		}
		return world.ItemStack{}, FireworkExplosion{}, false
	}
	if starCount != 1 || len(fadeColors) == 0 {
		return world.ItemStack{}, FireworkExplosion{}, false
	}
	starExp.FadeColors = fadeColors
	outStack := world.NewNamedItemStack(FireworkStarItemID, 1)
	return outStack, starExp, true
}

// MatchFireworkRocketRecipe implements vanilla 26.1.2 FireworkRocketRecipe:
//   - Exactly 1x minecraft:paper
//   - 1..3x minecraft:gunpowder -> FlightDuration = gunpowderCount
//   - 0..7x minecraft:firework_star -> Explosions copied in slot order
//   - Produces 3x minecraft:firework_rocket with FireworksComponent
func MatchFireworkRocketRecipe(ingredients []world.ItemStack, inputStars []FireworkExplosion) (world.ItemStack, FireworksComponent, bool) {
	var (
		hasPaper       bool
		gunpowderCount int32
		explosions     []FireworkExplosion
	)
	for i, ing := range ingredients {
		if ing.IsEmpty() {
			continue
		}
		itemID := canonicalItemName(ing.ResolvedID())
		switch itemID {
		case "minecraft:paper":
			if hasPaper {
				return world.ItemStack{}, FireworksComponent{}, false
			}
			hasPaper = true
		case "minecraft:gunpowder":
			gunpowderCount++
			if gunpowderCount > MaxCraftFireworkFlightDuration {
				return world.ItemStack{}, FireworksComponent{}, false
			}
		case FireworkStarItemID:
			var exp FireworkExplosion
			if i < len(inputStars) {
				exp = inputStars[i].Clone()
			}
			explosions = append(explosions, exp)
		default:
			return world.ItemStack{}, FireworksComponent{}, false
		}
	}
	if !hasPaper || gunpowderCount < 1 || gunpowderCount > MaxCraftFireworkFlightDuration {
		return world.ItemStack{}, FireworksComponent{}, false
	}
	outStack := world.NewNamedItemStack(FireworkRocketItemID, 3)
	return outStack, FireworksComponent{
		FlightDuration: gunpowderCount,
		Explosions:     explosions,
	}, true
}

// FireworkRocketState holds the server-side state of an active
// `minecraft:firework_rocket` entity in the world.
type FireworkRocketState struct {
	EntityID         int32
	Dimension        string
	X                float64
	Y                float64
	Z                float64
	VX               float64
	VY               float64
	VZ               float64
	Yaw              float32
	Pitch            float32
	Life             int
	Lifetime         int
	Fireworks        FireworksComponent
	AttachedPlayer   *PlayerSession
	AttachedEntityID int32
	ShooterPlayer    *PlayerSession
	ShooterEntityID  int32
	ShotAtAngle      bool
}

type fireworkRocketManager struct {
	mu      sync.Mutex
	rockets map[int32]*FireworkRocketState
}

var serverFireworkManagers sync.Map // map[*Server]*fireworkRocketManager

func (s *Server) fireworkManager() *fireworkRocketManager {
	if s == nil {
		return &fireworkRocketManager{rockets: make(map[int32]*FireworkRocketState)}
	}
	if v, ok := serverFireworkManagers.Load(s); ok {
		return v.(*fireworkRocketManager)
	}
	initMgr := &fireworkRocketManager{
		rockets: make(map[int32]*FireworkRocketState),
	}
	actual, _ := serverFireworkManagers.LoadOrStore(s, initMgr)
	return actual.(*fireworkRocketManager)
}

// FireworkRocketStateByID returns a snapshot of an active firework rocket entity.
func (s *Server) FireworkRocketStateByID(entityID int32) (FireworkRocketState, bool) {
	if s == nil || entityID == 0 {
		return FireworkRocketState{}, false
	}
	mgr := s.fireworkManager()
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	st, ok := mgr.rockets[entityID]
	if !ok || st == nil {
		return FireworkRocketState{}, false
	}
	cp := *st
	cp.Fireworks = st.Fireworks.Clone()
	return cp, true
}

// ComputeFireworkLifetime computes the vanilla 26.1.2 FireworkRocketEntity
// lifetime in ticks:
//
//	lifetime = 10 * (1 + flightDuration) + rng.Intn(6) + rng.Intn(7)
func ComputeFireworkLifetime(flightDuration int32, rng *rand.Rand) int {
	if flightDuration < 0 {
		flightDuration = 0
	}
	base := 10 * int(1+flightDuration)
	if rng == nil {
		return base + 5
	}
	return base + rng.Intn(6) + rng.Intn(7)
}

// EncodeFireworkRocketEntityDataPacket encodes the clientbound PlaySetEntityData
// (0x61) packet for a `minecraft:firework_rocket` entity in protocol 775:
//   - index 8 (DATA_ID_FIREWORKS_ITEM, ITEM_STACK serializer 7)
//   - index 9 (DATA_ATTACHED_TO_TARGET, OPTIONAL_UNSIGNED_INT serializer 20: 0 or entityID+1)
//   - index 10 (DATA_SHOT_AT_ANGLE, BOOLEAN serializer 8)
func EncodeFireworkRocketEntityDataPacket(entityID int32, fw FireworksComponent, attachedEntityID int32, shotAtAngle bool) []byte {
	w := protocol.NewWriter(48)
	w.VarInt(entityID)

	// Index 8: DATA_ID_FIREWORKS_ITEM (ITEM_STACK = 7).
	w.Byte(EntityDataFireworkItemID)
	w.VarInt(EntityDataSerializerItemStack)
	rocketStack := world.NewNamedItemStack(FireworkRocketItemID, 1)
	EncodeSlotWithFireworks(w, rocketStack, fw)

	// Index 9: DATA_ATTACHED_TO_TARGET (OPTIONAL_UNSIGNED_INT = 20).
	w.Byte(EntityDataFireworkAttachedTargetID)
	w.VarInt(EntityDataSerializerOptionalUnsignedInt)
	if attachedEntityID > 0 {
		w.VarInt(attachedEntityID + 1)
	} else {
		w.VarInt(0)
	}

	// Index 10: DATA_SHOT_AT_ANGLE (BOOLEAN = 8).
	w.Byte(EntityDataFireworkShotAtAngleID)
	w.VarInt(EntityDataSerializerBool)
	w.Bool(shotAtAngle)

	w.Byte(0xFF)
	return w.Bytes()
}

// FireworkRocketEntityDataPacket returns the initial SynchedEntityData packet
// payload for an active firework rocket entity when a viewer starts tracking it.
func (s *Server) FireworkRocketEntityDataPacket(entityID int32) []byte {
	st, ok := s.FireworkRocketStateByID(entityID)
	if !ok {
		return nil
	}
	return EncodeFireworkRocketEntityDataPacket(entityID, st.Fireworks, st.AttachedEntityID, st.ShotAtAngle)
}

// StepElytraFireworkBoostVelocity computes one tick of firework rocket velocity
// boost on a fall-flying entity, matching vanilla 26.1.2 FireworkRocketEntity.tick():
//
//	v += look * 0.1 + (look * 1.5 - v) * 0.5
func StepElytraFireworkBoostVelocity(vx, vy, vz float64, yawDeg, pitchDeg float32) (nx, ny, nz float64) {
	lx, ly, lz := ElytraLookVector(yawDeg, pitchDeg)
	nx = vx + lx*0.1 + (lx*1.5-vx)*0.5
	ny = vy + ly*0.1 + (ly*1.5-vy)*0.5
	nz = vz + lz*0.1 + (lz*1.5-vz)*0.5
	return nx, ny, nz
}

// SpawnFireworkRocket spawns a `minecraft:firework_rocket` entity in dim at
// (x, y, z) with initial velocity (vx, vy, vz), FireworksComponent fw, optional
// attached fall-flying player, and shotAtAngle flag.
func (s *Server) SpawnFireworkRocket(
	dim string,
	x, y, z, vx, vy, vz float64,
	fw FireworksComponent,
	attachedPlayer *PlayerSession,
	shooter *PlayerSession,
	shotAtAngle bool,
) (int32, *FireworkRocketState) {
	if s == nil || s.entities == nil {
		return 0, nil
	}
	dim = world.NormalizeDimension(dim)
	if fw.FlightDuration <= 0 {
		fw.FlightDuration = DefaultFireworkFlightDuration
	}

	typeID := registry.EntityTypeIndex(EntityTypeFireworkRocket)
	if typeID < 0 {
		typeID = EntityTypeFireworkRocketID
	}

	var attachedID, shooterID int32
	if attachedPlayer != nil {
		attachedID = attachedPlayer.EntityID
	}
	if shooter != nil {
		shooterID = shooter.EntityID
	}

	ent := &world.Entity{
		TypeID:    typeID,
		TypeName:  EntityTypeFireworkRocket,
		Dimension: dim,
		X:         x,
		Y:         y,
		Z:         z,
		VelocityX: clampVelocityShort(vx),
		VelocityY: clampVelocityShort(vy),
		VelocityZ: clampVelocityShort(vz),
		OnGround:  false,
	}
	entityID := s.entities.Add(ent)

	st := &FireworkRocketState{
		EntityID:         entityID,
		Dimension:        dim,
		X:                x,
		Y:                y,
		Z:                z,
		VX:               vx,
		VY:               vy,
		VZ:               vz,
		Life:             0,
		Lifetime:         ComputeFireworkLifetime(fw.FlightDuration, nil),
		Fireworks:        fw.Clone(),
		AttachedPlayer:   attachedPlayer,
		AttachedEntityID: attachedID,
		ShooterPlayer:    shooter,
		ShooterEntityID:  shooterID,
		ShotAtAngle:      shotAtAngle,
	}

	mgr := s.fireworkManager()
	mgr.mu.Lock()
	mgr.rockets[entityID] = st
	mgr.mu.Unlock()

	cx := int32(int64(math.Floor(x)) >> 4)
	cz := int32(int64(math.Floor(z)) >> 4)
	if saved := s.entities.Get(entityID); saved != nil {
		s.BroadcastDimensionChunk(dim, cx, cz, protocol.PlayAddEntity, encodeAddEntityPacket(nil, saved))
		s.BroadcastDimensionChunk(dim, cx, cz, PlaySetEntityDataCB,
			EncodeFireworkRocketEntityDataPacket(entityID, st.Fireworks, attachedID, shotAtAngle))
	}
	s.BroadcastDimensionChunk(dim, cx, cz, PlaySoundPacketID,
		EncodeSoundPacket("minecraft:entity.firework_rocket.launch", SoundSourceAmbient, x, y, z, 3.0, 1.0, 0))
	return entityID, st
}

func (s *Server) consumeHandItemOne(session *PlayerSession, hand int32) bool {
	if session == nil {
		return false
	}
	slot, stack := session.handSlotAndStack(hand)
	if stack.IsEmpty() {
		return false
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeCreative {
		return true
	}
	if snap.GameMode != GameModeSurvival && snap.GameMode != GameModeAdventure {
		return false
	}
	var updated world.ItemStack
	if stack.Count > 1 {
		updated = world.NewItemStack(stack.ResolvedItemID(), stack.Count-1)
	} else {
		ClearSlotFireworks(session, slot)
		ClearSlotFireworkStar(session, slot)
	}
	session.SetInventorySlot(slot, updated)
	if session.send != nil {
		stateID := session.NextContainerStateID()
		_ = session.send(protocol.PlayContainerSetSlot, EncodeContainerSlot(0, stateID, slot, updated))
	}
	return true
}

// UseFireworkRocketOnBlock handles right-clicking a block while holding a
// `minecraft:firework_rocket`, spawning an upward-flying firework rocket at the
// clicked position and consuming 1 rocket in survival/adventure mode.
func (s *Server) UseFireworkRocketOnBlock(
	session *PlayerSession,
	hand int32,
	blockX, blockY, blockZ int,
	face int32,
	cursorX, cursorY, cursorZ float32,
) (int32, bool) {
	if s == nil || session == nil || session.IsDead() {
		return 0, false
	}
	slot, stack := session.handSlotAndStack(hand)
	if stack.IsEmpty() || canonicalItemName(stack.ResolvedID()) != FireworkRocketItemID {
		return 0, false
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator {
		return 0, false
	}

	fw, ok := GetSlotFireworks(session, slot)
	if !ok {
		fw = FireworksComponent{FlightDuration: DefaultFireworkFlightDuration}
	}

	var offX, offY, offZ float64
	switch face {
	case 0:
		offY = -0.15
	case 1:
		offY = 0.15
	case 2:
		offZ = -0.15
	case 3:
		offZ = 0.15
	case 4:
		offX = -0.15
	case 5:
		offX = 0.15
	}
	spawnX := float64(blockX) + float64(cursorX) + offX
	spawnY := float64(blockY) + float64(cursorY) + offY
	spawnZ := float64(blockZ) + float64(cursorZ) + offZ

	if !s.consumeHandItemOne(session, hand) {
		return 0, false
	}
	id, _ := s.SpawnFireworkRocket(snap.Dimension, spawnX, spawnY, spawnZ, 0.001, 0.05, 0.001, fw, nil, session, false)
	if id != 0 {
		s.RecordItemUsed(session, FireworkRocketItemID, 1)
	}
	return id, id != 0
}

// UseFireworkRocketInAir handles right-clicking in air while holding a
// `minecraft:firework_rocket`. When the player is fall-flying with an Elytra
// (`session.IsFallFlying()`), it spawns an attached rocket that boosts the
// player's glide velocity along their look vector and sends `PlaySetEntityMotion`.
func (s *Server) UseFireworkRocketInAir(session *PlayerSession, hand int32) (int32, bool) {
	if s == nil || session == nil || session.IsDead() || !session.IsFallFlying() {
		return 0, false
	}
	slot, stack := session.handSlotAndStack(hand)
	if stack.IsEmpty() || canonicalItemName(stack.ResolvedID()) != FireworkRocketItemID {
		return 0, false
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator {
		return 0, false
	}

	fw, ok := GetSlotFireworks(session, slot)
	if !ok {
		fw = FireworksComponent{FlightDuration: DefaultFireworkFlightDuration}
	}
	if !s.consumeHandItemOne(session, hand) {
		return 0, false
	}

	vx, vy, vz := session.ElytraVelocity()
	nvx, nvy, nvz := StepElytraFireworkBoostVelocity(vx, vy, vz, snap.Yaw, snap.Pitch)
	session.SetElytraVelocity(nvx, nvy, nvz)
	if session.send != nil {
		_ = session.send(protocol.PlaySetEntityMotion, EncodeSetEntityMotionPacket(snap.EntityID, nvx, nvy, nvz))
	}

	id, _ := s.SpawnFireworkRocket(snap.Dimension, snap.X, snap.Y, snap.Z, nvx, nvy, nvz, fw, session, session, false)
	if id != 0 {
		s.RecordItemUsed(session, FireworkRocketItemID, 1)
	}
	return id, id != 0
}

// ComputeFireworkExplosionDamage returns the vanilla 26.1.2 explosion damage
// dealt by a firework rocket with starCount > 0 explosions at distance dist
// (in [0, 5.0]):
//   - If attached (dist == 0, attached == true): 5.0 + float32(starCount*2)
//   - At distance 0 < dist <= 5.0: (5.0 + float32(starCount*2)) * sqrt((5.0 - dist) / 5.0)
func ComputeFireworkExplosionDamage(starCount int, dist float64, attached bool) float32 {
	if starCount <= 0 {
		return 0
	}
	base := FireworkExplosionBaseDamage + float32(starCount)*FireworkExplosionDamagePerStar
	if attached {
		return base
	}
	if dist < 0 || dist > FireworkExplosionDamageRadius {
		return 0
	}
	scale := float32(math.Sqrt((FireworkExplosionDamageRadius - dist) / FireworkExplosionDamageRadius))
	return base * scale
}

func (s *Server) hasFireworkLineOfSight(dim string, x0, y0, z0, x1, y1, z1 float64) bool {
	if s == nil {
		return true
	}
	cache := s.DimensionChunks(dim)
	if cache == nil {
		return true
	}
	dx := x1 - x0
	dy := y1 - y0
	dz := z1 - z0
	dist := math.Sqrt(dx*dx + dy*dy + dz*dz)
	if dist < 0.25 {
		return true
	}
	steps := int(math.Ceil(dist * 2.0))
	for i := 1; i < steps; i++ {
		t := float64(i) / float64(steps)
		bx := int(math.Floor(x0 + dx*t))
		by := int(math.Floor(y0 + dy*t))
		bz := int(math.Floor(z0 + dz*t))
		if !isPassableBlock(cache.GetBlock(bx, by, bz)) {
			return false
		}
	}
	return true
}

// DetonateFireworkRocket explodes the firework rocket entity, broadcasting
// EntityEvent 17 (FIREWORKS_EXPLODE) + explosion sounds, applying star explosion
// damage (`5 + 2*stars`) to the attached entity and radial damage within 5 blocks,
// and removing the entity from the world.
func (s *Server) DetonateFireworkRocket(entityID int32) (playersHit, mobsHit int) {
	if s == nil || entityID == 0 {
		return 0, 0
	}
	mgr := s.fireworkManager()
	mgr.mu.Lock()
	st, ok := mgr.rockets[entityID]
	if ok {
		delete(mgr.rockets, entityID)
	}
	mgr.mu.Unlock()
	if !ok || st == nil {
		return 0, 0
	}

	cx := int32(int64(math.Floor(st.X)) >> 4)
	cz := int32(int64(math.Floor(st.Z)) >> 4)

	// Broadcast EntityEvent 17 (FIREWORKS_EXPLODE) before removing the entity so
	// clients render the particle burst from the rocket's synced Fireworks item.
	s.BroadcastDimensionChunk(st.Dimension, cx, cz, protocol.PlayEntityEvent,
		EncodeEntityEventPacket(entityID, EntityEventFireworkExplode))
	s.EmitVibration(VibrationEvent{
		Dimension:      st.Dimension,
		Type:           GameEventExplode,
		X:              st.X,
		Y:              st.Y,
		Z:              st.Z,
		Player:         st.ShooterPlayer,
		SourceEntityID: entityID,
	})

	starCount := len(st.Fireworks.Explosions)
	if starCount > 0 {
		blastSound := "minecraft:entity.firework_rocket.blast"
		for _, exp := range st.Fireworks.Explosions {
			if exp.Shape == FireworkShapeLargeBall {
				blastSound = "minecraft:entity.firework_rocket.large_blast"
				break
			}
		}
		s.BroadcastDimensionChunk(st.Dimension, cx, cz, PlaySoundPacketID,
			EncodeSoundPacket(blastSound, SoundSourceAmbient, st.X, st.Y, st.Z, 20.0, 1.0, 0))
		for _, exp := range st.Fireworks.Explosions {
			if exp.HasTwinkle {
				s.BroadcastDimensionChunk(st.Dimension, cx, cz, PlaySoundPacketID,
					EncodeSoundPacket("minecraft:entity.firework_rocket.twinkle", SoundSourceAmbient, st.X, st.Y, st.Z, 20.0, 1.0, 0))
				break
			}
		}

		// Deal explosion damage (FireworkRocketEntity.dealExplosionDamage).
		if st.AttachedPlayer != nil && !st.AttachedPlayer.IsDead() {
			dmg := ComputeFireworkExplosionDamage(starCount, 0, true)
			if dmg > 0 {
				s.DamagePlayerWithEffects(st.AttachedPlayer, dmg, "fireworks")
				playersHit++
			}
		}

		for _, p := range s.onlinePlayersSnapshot() {
			if p == nil || p.IsDead() || p == st.AttachedPlayer {
				continue
			}
			psnap := p.Snapshot()
			if world.NormalizeDimension(psnap.Dimension) != st.Dimension || psnap.GameMode == GameModeSpectator {
				continue
			}
			dist := math.Sqrt(sqFloat(psnap.X-st.X) + sqFloat(psnap.Y-st.Y) + sqFloat(psnap.Z-st.Z))
			if dist > FireworkExplosionDamageRadius {
				continue
			}
			if !s.hasFireworkLineOfSight(st.Dimension, st.X, st.Y, st.Z, psnap.X, psnap.Y+0.8, psnap.Z) {
				continue
			}
			dmg := ComputeFireworkExplosionDamage(starCount, dist, false)
			if dmg > 0 {
				s.DamagePlayerWithEffects(p, dmg, "fireworks")
				playersHit++
			}
		}

		if s.entities != nil {
			nearby := s.entities.AppendInChunkRange(nil, cx, cz, 1)
			for i := range nearby {
				ent := &nearby[i]
				if ent.ID == entityID || ent.IsItem() || ent.IsExperienceOrb() ||
					ent.TypeName == EntityTypeFireworkRocket || IsHangingEntity(ent.TypeName) {
					continue
				}
				if ent.EffectiveDimension() != st.Dimension {
					continue
				}
				dist := math.Sqrt(sqFloat(ent.X-st.X) + sqFloat(ent.Y-st.Y) + sqFloat(ent.Z-st.Z))
				if dist > FireworkExplosionDamageRadius {
					continue
				}
				if !s.hasFireworkLineOfSight(st.Dimension, st.X, st.Y, st.Z, ent.X, ent.Y+0.5, ent.Z) {
					continue
				}
				dmg := ComputeFireworkExplosionDamage(starCount, dist, false)
				if dmg <= 0 {
					continue
				}
				switch ent.TypeName {
				case EntityTypeArmorStand:
					if handled, _ := s.DamageArmorStand(ent.ID, dmg, "firework", st.ShooterPlayer); handled {
						mobsHit++
					}
					continue
				case MobTypeWither:
					s.DamageWither(ent.ID, dmg, st.ShooterPlayer, true)
				case MobTypeEnderDragon:
					s.DamageEnderDragon(ent.ID, dmg, st.ShooterPlayer, true)
				default:
					s.applyDamageToMob(ent, dmg, st.ShooterPlayer, 0.2, false, false)
				}
				mobsHit++
			}
		}
	}

	if s.entities != nil {
		s.entities.Remove(entityID)
	}
	s.BroadcastDimensionChunk(st.Dimension, cx, cz, protocol.PlayRemoveEntities,
		encodeRemoveEntitiesPacket(nil, []int32{entityID}))
	return playersHit, mobsHit
}

func sqFloat(v float64) float64 {
	return v * v
}

// TickFireworkRockets advances all active `minecraft:firework_rocket` entities
// by one server tick:
//   - Attached Elytra-boost rockets follow the attached player and accelerate
//     their glide velocity along their look vector (`v += look*0.1 + (look*1.5-v)*0.5`),
//     detonating immediately if the player stops fall-flying
//   - Free-flying upward rockets accelerate vertically (`vx *= 1.15, vz *= 1.15, vy += 0.04`)
//   - Crossbow-shot angled rockets (`ShotAtAngle == true`) travel along their
//     projectile vector and detonate on entity or block impact
//   - Rockets whose `Life > Lifetime` detonate automatically
func (s *Server) TickFireworkRockets() {
	if s == nil {
		return
	}
	mgr := s.fireworkManager()
	mgr.mu.Lock()
	if len(mgr.rockets) == 0 {
		mgr.mu.Unlock()
		return
	}
	ids := make([]int32, 0, len(mgr.rockets))
	for id := range mgr.rockets {
		ids = append(ids, id)
	}
	mgr.mu.Unlock()

	for _, id := range ids {
		s.tickSingleFireworkRocket(id)
	}
}

func (s *Server) tickSingleFireworkRocket(entityID int32) {
	mgr := s.fireworkManager()
	mgr.mu.Lock()
	st, ok := mgr.rockets[entityID]
	if !ok || st == nil {
		mgr.mu.Unlock()
		return
	}

	var explodeNow bool
	if st.AttachedPlayer != nil {
		if st.AttachedPlayer.IsDead() || !st.AttachedPlayer.IsFallFlying() {
			explodeNow = true
		} else {
			psnap := st.AttachedPlayer.Snapshot()
			vx, vy, vz := st.AttachedPlayer.ElytraVelocity()
			nvx, nvy, nvz := StepElytraFireworkBoostVelocity(vx, vy, vz, psnap.Yaw, psnap.Pitch)
			st.AttachedPlayer.SetElytraVelocity(nvx, nvy, nvz)
			st.X = psnap.X
			st.Y = psnap.Y
			st.Z = psnap.Z
			st.VX = nvx
			st.VY = nvy
			st.VZ = nvz
			if st.AttachedPlayer.send != nil {
				_ = st.AttachedPlayer.send(protocol.PlaySetEntityMotion,
					EncodeSetEntityMotionPacket(psnap.EntityID, nvx, nvy, nvz))
			}
		}
	} else {
		if !st.ShotAtAngle {
			// Upward rocket acceleration: horizontal *= 1.15, vertical += 0.04.
			st.VX *= 1.15
			st.VZ *= 1.15
			st.VY += 0.04
		}
		nextX := st.X + st.VX
		nextY := st.Y + st.VY
		nextZ := st.Z + st.VZ

		// Check block or entity collision along step.
		if s.checkFireworkCollision(st.Dimension, nextX, nextY, nextZ, st.ShooterEntityID, st.ShotAtAngle) {
			explodeNow = true
		}
		st.X = nextX
		st.Y = nextY
		st.Z = nextZ
	}

	st.Life++
	if st.Life > st.Lifetime {
		explodeNow = true
	}
	newX, newY, newZ := st.X, st.Y, st.Z
	vx, vy, vz := st.VX, st.VY, st.VZ
	dim := st.Dimension
	isAttached := st.AttachedPlayer != nil
	mgr.mu.Unlock()

	if s.entities != nil {
		s.entities.Update(entityID, func(e *world.Entity) {
			e.X = newX
			e.Y = newY
			e.Z = newZ
			e.VelocityX = clampVelocityShort(vx)
			e.VelocityY = clampVelocityShort(vy)
			e.VelocityZ = clampVelocityShort(vz)
		})
	}

	if explodeNow {
		s.DetonateFireworkRocket(entityID)
		return
	}

	cx := int32(int64(math.Floor(newX)) >> 4)
	cz := int32(int64(math.Floor(newZ)) >> 4)
	s.BroadcastDimensionChunk(dim, cx, cz, protocol.PlaySetEntityMotion,
		EncodeSetEntityMotionPacket(entityID, vx, vy, vz))
	if !isAttached && s.entities != nil {
		if ent := s.entities.Get(entityID); ent != nil {
			s.BroadcastDimensionChunk(dim, cx, cz, protocol.PlayTeleportEntity,
				encodeTeleportEntityPacket(nil, ent))
		}
	}
}

func (s *Server) checkFireworkCollision(dim string, x, y, z float64, shooterID int32, checkEntities bool) bool {
	if cache := s.DimensionChunks(dim); cache != nil {
		bx := int(math.Floor(x))
		by := int(math.Floor(y))
		bz := int(math.Floor(z))
		if !isPassableBlock(cache.GetBlock(bx, by, bz)) {
			_, _ = s.OnProjectileBlockImpact(dim, bx, by, bz, x, y, z, x, y, z, 0, EntityTypeFireworkRocket)
			return true
		}
	}
	if !checkEntities || s.entities == nil {
		return false
	}
	cx := int32(int64(math.Floor(x)) >> 4)
	cz := int32(int64(math.Floor(z)) >> 4)
	nearby := s.entities.AppendInChunkRange(nil, cx, cz, 1)
	for i := range nearby {
		ent := &nearby[i]
		if ent.ID == shooterID || ent.IsItem() || ent.IsExperienceOrb() ||
			ent.TypeName == EntityTypeFireworkRocket || IsDecorationEntity(ent.TypeName) {
			continue
		}
		if ent.EffectiveDimension() != dim {
			continue
		}
		if math.Abs(ent.X-x) <= 0.8 && math.Abs((ent.Y+0.9)-y) <= 1.1 && math.Abs(ent.Z-z) <= 0.8 {
			return true
		}
	}
	return false
}

// LoadCrossbowProjectile loads an ammo item into the `minecraft:crossbow` held
// in hand (0 = main hand, 1 = offhand):
//   - Follows vanilla CrossbowItem.getSupportedHeldProjectiles() = ARROW_OR_FIREWORK:
//     checks the opposite hand first for `minecraft:firework_rocket` or arrows,
//     then searches the inventory for arrows (or firework rockets)
//   - If the crossbow has `minecraft:multishot` > 0, stores 3 copies of the
//     charged projectile while consuming only 1 ammo item
//   - Returns true if the crossbow was charged
func (s *Server) LoadCrossbowProjectile(session *PlayerSession, hand int32) bool {
	if s == nil || session == nil || session.IsDead() {
		return false
	}
	crossbowSlot, crossbowStack := session.handSlotAndStack(hand)
	if crossbowStack.IsEmpty() || canonicalItemName(crossbowStack.ResolvedID()) != CrossbowItemID {
		return false
	}
	if len(GetSlotChargedProjectiles(session, crossbowSlot)) > 0 {
		return false
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator {
		return false
	}

	otherHand := HandOffHand
	if hand == HandOffHand {
		otherHand = HandMain
	}
	otherSlot, otherStack := session.handSlotAndStack(otherHand)

	ammoSlot := -1
	var ammoStack world.ItemStack
	if isCrossbowHeldProjectile(otherStack) {
		ammoSlot = otherSlot
		ammoStack = otherStack
	} else {
		// Search hotbar + main inventory for arrows.
		for idx := world.HotbarContainerOffset; idx < world.HotbarContainerOffset+world.HotbarSlotCount; idx++ {
			st := session.InventorySlot(idx)
			if isCrossbowArrowProjectile(st) {
				ammoSlot = idx
				ammoStack = st
				break
			}
		}
		if ammoSlot < 0 {
			for idx := 9; idx < world.HotbarContainerOffset; idx++ {
				st := session.InventorySlot(idx)
				if isCrossbowArrowProjectile(st) {
					ammoSlot = idx
					ammoStack = st
					break
				}
			}
		}
		if ammoSlot < 0 && snap.GameMode == GameModeCreative {
			ammoStack = world.NewNamedItemStack("minecraft:arrow", 1)
		}
	}

	if ammoStack.IsEmpty() {
		return false
	}

	projItem := world.NewItemStack(ammoStack.ResolvedItemID(), 1)
	if projItem.ID == "" {
		projItem.ID = ammoStack.ResolvedID()
	}
	proj := ChargedProjectile{Stack: projItem}
	if canonicalItemName(projItem.ResolvedID()) == FireworkRocketItemID && ammoSlot >= 0 {
		if fw, ok := GetSlotFireworks(session, ammoSlot); ok {
			fwCopy := fw.Clone()
			proj.Fireworks = &fwCopy
		} else {
			proj.Fireworks = &FireworksComponent{FlightDuration: DefaultFireworkFlightDuration}
		}
	}

	// Consume 1 ammo item in survival/adventure mode.
	if ammoSlot >= 0 && snap.GameMode != GameModeCreative {
		var updated world.ItemStack
		if ammoStack.Count > 1 {
			updated = world.NewItemStack(ammoStack.ResolvedItemID(), ammoStack.Count-1)
		} else {
			ClearSlotFireworks(session, ammoSlot)
		}
		session.SetInventorySlot(ammoSlot, updated)
		if session.send != nil {
			stID := session.NextContainerStateID()
			_ = session.send(protocol.PlayContainerSetSlot, EncodeContainerSlot(0, stID, ammoSlot, updated))
		}
	}

	cbMeta := GetSlotItemMeta(session, crossbowSlot)
	count := 1
	if cbMeta.EnchantmentLevel("minecraft:multishot") > 0 {
		count = 3
	}
	charged := make([]ChargedProjectile, count)
	for i := 0; i < count; i++ {
		charged[i] = proj.Clone()
	}
	SetSlotChargedProjectiles(session, crossbowSlot, charged)

	cx := int32(int64(math.Floor(snap.X)) >> 4)
	cz := int32(int64(math.Floor(snap.Z)) >> 4)
	s.BroadcastDimensionChunk(snap.Dimension, cx, cz, PlaySoundPacketID,
		EncodeSoundPacket("minecraft:item.crossbow.loading_end", SoundSourcePlayers, snap.X, snap.Y, snap.Z, 1.0, 1.0, 0))
	return true
}

func isCrossbowHeldProjectile(stack world.ItemStack) bool {
	if stack.IsEmpty() {
		return false
	}
	if canonicalItemName(stack.ResolvedID()) == FireworkRocketItemID {
		return true
	}
	return isCrossbowArrowProjectile(stack)
}

func isCrossbowArrowProjectile(stack world.ItemStack) bool {
	if stack.IsEmpty() {
		return false
	}
	switch canonicalItemName(stack.ResolvedID()) {
	case "minecraft:arrow", "minecraft:spectral_arrow", "minecraft:tipped_arrow":
		return true
	default:
		return false
	}
}

// ShootCrossbow fires all charged projectiles loaded in the `minecraft:crossbow`
// held in hand, spawning angled firework rockets (`ShotAtAngle = true`, speed `1.6`)
// or arrows (speed `3.15`), clearing `ChargedProjectiles`, and damaging the
// crossbow (`3` durability points for firework rockets, `1` for arrows, matching
// vanilla CrossbowItem.getDurabilityUse).
func (s *Server) ShootCrossbow(session *PlayerSession, hand int32) ([]int32, bool) {
	if s == nil || session == nil || session.IsDead() {
		return nil, false
	}
	crossbowSlot, crossbowStack := session.handSlotAndStack(hand)
	if crossbowStack.IsEmpty() || canonicalItemName(crossbowStack.ResolvedID()) != CrossbowItemID {
		return nil, false
	}
	charged := GetSlotChargedProjectiles(session, crossbowSlot)
	if len(charged) == 0 {
		return nil, false
	}
	ClearSlotChargedProjectiles(session, crossbowSlot)

	snap := session.Snapshot()
	yawOffsets := []float32{0}
	if len(charged) == 3 {
		yawOffsets = []float32{0, -10.0, 10.0}
	}

	var spawnedIDs []int32
	durabilityCost := int32(0)

	for i, proj := range charged {
		yawOff := float32(0)
		if i < len(yawOffsets) {
			yawOff = yawOffsets[i]
		}
		shotYaw := snap.Yaw + yawOff
		lx, ly, lz := ElytraLookVector(shotYaw, snap.Pitch)

		if canonicalItemName(proj.Stack.ResolvedID()) == FireworkRocketItemID {
			if i == 0 {
				durabilityCost = CrossbowFireworkDurabilityCost
			}
			fw := FireworksComponent{FlightDuration: DefaultFireworkFlightDuration}
			if proj.Fireworks != nil {
				fw = proj.Fireworks.Clone()
			}
			vx := lx * CrossbowFireworkShootPower
			vy := ly * CrossbowFireworkShootPower
			vz := lz * CrossbowFireworkShootPower
			id, _ := s.SpawnFireworkRocket(snap.Dimension, snap.X, snap.Y+1.5, snap.Z, vx, vy, vz, fw, nil, session, true)
			if id != 0 {
				spawnedIDs = append(spawnedIDs, id)
			}
		} else {
			if i == 0 {
				durabilityCost = CrossbowArrowDurabilityCost
			}
			vx := lx * CrossbowArrowShootPower
			vy := ly * CrossbowArrowShootPower
			vz := lz * CrossbowArrowShootPower
			typeID := registry.EntityTypeIndex(MobTypeArrow)
			if typeID < 0 {
				typeID = EntityTypeArrowID
			}
			if s.entities != nil {
				id := s.entities.Add(&world.Entity{
					TypeID:    typeID,
					TypeName:  MobTypeArrow,
					Dimension: world.NormalizeDimension(snap.Dimension),
					X:         snap.X,
					Y:         snap.Y + 1.5,
					Z:         snap.Z,
					Yaw:       shotYaw,
					Pitch:     snap.Pitch,
					VelocityX: clampVelocityShort(vx),
					VelocityY: clampVelocityShort(vy),
					VelocityZ: clampVelocityShort(vz),
				})
				spawnedIDs = append(spawnedIDs, id)
			}
		}
	}

	if durabilityCost > 0 && (snap.GameMode == GameModeSurvival || snap.GameMode == GameModeAdventure) {
		s.DamagePlayerItem(session, crossbowSlot, durabilityCost)
	}

	cx := int32(int64(math.Floor(snap.X)) >> 4)
	cz := int32(int64(math.Floor(snap.Z)) >> 4)
	s.BroadcastDimensionChunk(snap.Dimension, cx, cz, PlaySoundPacketID,
		EncodeSoundPacket("minecraft:item.crossbow.shoot", SoundSourcePlayers, snap.X, snap.Y, snap.Z, 1.0, 1.0, 0))
	return spawnedIDs, len(spawnedIDs) > 0
}

// UseCrossbow handles right-clicking with a `minecraft:crossbow` in hand:
// if already charged with a firework rocket or arrow, fires the projectile(s);
// otherwise loads a firework rocket (from offhand) or arrow into the crossbow.
func (s *Server) UseCrossbow(session *PlayerSession, hand int32) bool {
	if s == nil || session == nil || session.IsDead() {
		return false
	}
	crossbowSlot, crossbowStack := session.handSlotAndStack(hand)
	if crossbowStack.IsEmpty() || canonicalItemName(crossbowStack.ResolvedID()) != CrossbowItemID {
		return false
	}
	if len(GetSlotChargedProjectiles(session, crossbowSlot)) > 0 {
		_, ok := s.ShootCrossbow(session, hand)
		return ok
	}
	return s.LoadCrossbowProjectile(session, hand)
}
