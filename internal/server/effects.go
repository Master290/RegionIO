package server

import (
	"math"
	"sort"
	"strings"
	"sync"

	"regionio/internal/nbt"
	"regionio/internal/protocol"
	"regionio/internal/world"
)

// Status effect protocol 775 (Minecraft 26.1.2) clientbound packet IDs.
const (
	PlayRemoveMobEffect int32 = 0x4e // clientbound remove_mob_effect
	PlayUpdateMobEffect int32 = 0x84 // clientbound update_mob_effect
)

// ClientboundUpdateMobEffectPacket bitflags.
const (
	MobEffectFlagAmbient  byte = 1 << 0 // 0x01
	MobEffectFlagVisible  byte = 1 << 1 // 0x02
	MobEffectFlagShowIcon byte = 1 << 2 // 0x04
	MobEffectFlagBlend    byte = 1 << 3 // 0x08
)

// InfiniteEffectDuration represents an infinite status effect duration (-1 ticks).
const InfiniteEffectDuration int32 = -1

// MobEffectCategory classifies whether an effect is beneficial, harmful, or neutral.
const (
	EffectCategoryBeneficial = "beneficial"
	EffectCategoryHarmful    = "harmful"
	EffectCategoryNeutral    = "neutral"
)

// Canonical vanilla 26.1.2 mob effect identifiers.
const (
	EffectSpeed               = "minecraft:speed"
	EffectSlowness            = "minecraft:slowness"
	EffectHaste               = "minecraft:haste"
	EffectMiningFatigue       = "minecraft:mining_fatigue"
	EffectStrength            = "minecraft:strength"
	EffectInstantHealth       = "minecraft:instant_health"
	EffectInstantDamage       = "minecraft:instant_damage"
	EffectJumpBoost           = "minecraft:jump_boost"
	EffectNausea              = "minecraft:nausea"
	EffectRegeneration        = "minecraft:regeneration"
	EffectResistance          = "minecraft:resistance"
	EffectFireResistance      = "minecraft:fire_resistance"
	EffectWaterBreathing      = "minecraft:water_breathing"
	EffectInvisibility        = "minecraft:invisibility"
	EffectBlindness           = "minecraft:blindness"
	EffectNightVision         = "minecraft:night_vision"
	EffectHunger              = "minecraft:hunger"
	EffectWeakness            = "minecraft:weakness"
	EffectPoison              = "minecraft:poison"
	EffectWither              = "minecraft:wither"
	EffectHealthBoost         = "minecraft:health_boost"
	EffectAbsorption          = "minecraft:absorption"
	EffectSaturation          = "minecraft:saturation"
	EffectGlowing             = "minecraft:glowing"
	EffectLevitation          = "minecraft:levitation"
	EffectLuck                = "minecraft:luck"
	EffectUnluck              = "minecraft:unluck"
	EffectSlowFalling         = "minecraft:slow_falling"
	EffectConduitPower        = "minecraft:conduit_power"
	EffectDolphinsGrace       = "minecraft:dolphins_grace"
	EffectBadOmen             = "minecraft:bad_omen"
	EffectHeroOfTheVillage    = "minecraft:hero_of_the_village"
	EffectDarkness            = "minecraft:darkness"
	EffectTrialOmen           = "minecraft:trial_omen"
	EffectRaidOmen            = "minecraft:raid_omen"
	EffectWindCharged         = "minecraft:wind_charged"
	EffectWeaving             = "minecraft:weaving"
	EffectOozing              = "minecraft:oozing"
	EffectInfested            = "minecraft:infested"
	EffectBreathOfTheNautilus = "minecraft:breath_of_the_nautilus"

	// Canonical enchantment identifiers used by combat/survival helpers.
	EnchantProtection           = "minecraft:protection"
	EnchantFireProtection       = "minecraft:fire_protection"
	EnchantFeatherFalling       = "minecraft:feather_falling"
	EnchantBlastProtection      = "minecraft:blast_protection"
	EnchantProjectileProtection = "minecraft:projectile_protection"
	EnchantRespiration          = "minecraft:respiration"
	EnchantAquaAffinity         = "minecraft:aqua_affinity"
	EnchantThorns               = "minecraft:thorns"
	EnchantDepthStrider         = "minecraft:depth_strider"
	EnchantFrostWalker          = "minecraft:frost_walker"
	EnchantBindingCurse         = "minecraft:binding_curse"
	EnchantSoulSpeed            = "minecraft:soul_speed"
	EnchantSwiftSneak           = "minecraft:swift_sneak"
	EnchantSharpness            = "minecraft:sharpness"
	EnchantSmite                = "minecraft:smite"
	EnchantBaneOfArthropods     = "minecraft:bane_of_arthropods"
	EnchantKnockback            = "minecraft:knockback"
	EnchantFireAspect           = "minecraft:fire_aspect"
	EnchantLooting              = "minecraft:looting"
	EnchantSweepingEdge         = "minecraft:sweeping_edge"
	EnchantEfficiency           = "minecraft:efficiency"
	EnchantSilkTouch            = "minecraft:silk_touch"
	EnchantUnbreaking           = "minecraft:unbreaking"
	EnchantFortune              = "minecraft:fortune"
	EnchantPower                = "minecraft:power"
	EnchantPunch                = "minecraft:punch"
	EnchantFlame                = "minecraft:flame"
	EnchantInfinity             = "minecraft:infinity"
	EnchantLuckOfTheSea         = "minecraft:luck_of_the_sea"
	EnchantLure                 = "minecraft:lure"
	EnchantLoyalty              = "minecraft:loyalty"
	EnchantImpaling             = "minecraft:impaling"
	EnchantRiptide              = "minecraft:riptide"
	EnchantChanneling           = "minecraft:channeling"
	EnchantMultishot            = "minecraft:multishot"
	EnchantQuickCharge          = "minecraft:quick_charge"
	EnchantPiercing             = "minecraft:piercing"
	EnchantDensity              = "minecraft:density"
	EnchantBreach               = "minecraft:breach"
	EnchantWindBurst            = "minecraft:wind_burst"
	EnchantMending              = "minecraft:mending"
	EnchantVanishingCurse       = "minecraft:vanishing_curse"
)

// MobEffectDef describes one vanilla 26.1.2 status effect in BuiltInRegistries.MOB_EFFECT.
type MobEffectDef struct {
	ID            string
	RegistryIndex int32
	Category      string
	Color         int32
	Instantaneous bool
}

// StatusEffectInstance represents one active or pending status effect with its
// amplifier (0 = level I), remaining duration in game ticks, and display flags.
type StatusEffectInstance struct {
	ID            string
	Amplifier     int32
	Duration      int32
	Ambient       bool
	ShowParticles bool
	ShowIcon      bool
}

// NewStatusEffectInstance constructs a standard visible status effect instance
// with particles and icon enabled.
func NewStatusEffectInstance(effectID string, duration, amplifier int32) StatusEffectInstance {
	if amplifier < 0 {
		amplifier = 0
	}
	return StatusEffectInstance{
		ID:            canonicalEffectID(effectID),
		Amplifier:     amplifier,
		Duration:      duration,
		Ambient:       false,
		ShowParticles: true,
		ShowIcon:      true,
	}
}

// Clone returns a copy of inst with a canonicalized ID.
func (inst StatusEffectInstance) Clone() StatusEffectInstance {
	inst.ID = canonicalEffectID(inst.ID)
	return inst
}

// IsInfinite reports whether the effect has an infinite duration (-1).
func (inst StatusEffectInstance) IsInfinite() bool {
	return inst.Duration == InfiniteEffectDuration
}

// MobEffectRegistry holds the ordered vanilla 26.1.2 BuiltInRegistries.MOB_EFFECT table.
type MobEffectRegistry struct {
	defs    []*MobEffectDef
	byID    map[string]*MobEffectDef
	byIndex map[int32]*MobEffectDef
}

var (
	defaultEffectRegOnce sync.Once
	defaultEffectReg     *MobEffectRegistry
)

// DefaultMobEffectRegistry returns the singleton 26.1.2 mob effect registry.
func DefaultMobEffectRegistry() *MobEffectRegistry {
	defaultEffectRegOnce.Do(func() {
		defaultEffectReg = buildVanillaMobEffectRegistry()
	})
	return defaultEffectReg
}

// All returns all registered mob effects in registry order.
func (r *MobEffectRegistry) All() []*MobEffectDef {
	if r == nil {
		return nil
	}
	out := make([]*MobEffectDef, len(r.defs))
	copy(out, r.defs)
	return out
}

// ByID looks up a mob effect definition by canonical or short identifier.
func (r *MobEffectRegistry) ByID(id string) (*MobEffectDef, bool) {
	if r == nil {
		return nil, false
	}
	def, ok := r.byID[canonicalEffectID(id)]
	return def, ok
}

// ByIndex looks up a mob effect definition by its 0-based protocol registry ID.
func (r *MobEffectRegistry) ByIndex(index int32) (*MobEffectDef, bool) {
	if r == nil {
		return nil, false
	}
	def, ok := r.byIndex[index]
	return def, ok
}

// MobEffectByID looks up a mob effect in DefaultMobEffectRegistry by ID.
func MobEffectByID(id string) (*MobEffectDef, bool) {
	return DefaultMobEffectRegistry().ByID(id)
}

// MobEffectByIndex looks up a mob effect in DefaultMobEffectRegistry by registry index.
func MobEffectByIndex(index int32) (*MobEffectDef, bool) {
	return DefaultMobEffectRegistry().ByIndex(index)
}

func canonicalEffectID(id string) string {
	id = strings.TrimSpace(strings.ToLower(id))
	if id == "" {
		return ""
	}
	if !strings.Contains(id, ":") {
		return "minecraft:" + id
	}
	return id
}

func buildVanillaMobEffectRegistry() *MobEffectRegistry {
	// Exact registration order from net.minecraft.world.effect.MobEffects (26.1.2).
	entries := []struct {
		id      string
		cat     string
		color   int32
		instant bool
	}{
		{EffectSpeed, EffectCategoryBeneficial, 0x33EBFF, false},               // 0
		{EffectSlowness, EffectCategoryHarmful, 0x8BAFE0, false},               // 1
		{EffectHaste, EffectCategoryBeneficial, 0xD9C043, false},               // 2
		{EffectMiningFatigue, EffectCategoryHarmful, 0x4A4217, false},          // 3
		{EffectStrength, EffectCategoryBeneficial, 0xFFC700, false},            // 4
		{EffectInstantHealth, EffectCategoryBeneficial, 0xF82423, true},        // 5
		{EffectInstantDamage, EffectCategoryHarmful, 0xA9656A, true},           // 6
		{EffectJumpBoost, EffectCategoryBeneficial, 0xFDFF84, false},           // 7
		{EffectNausea, EffectCategoryHarmful, 0x551D4A, false},                 // 8
		{EffectRegeneration, EffectCategoryBeneficial, 0xCD5CAB, false},        // 9
		{EffectResistance, EffectCategoryBeneficial, 0x9146F0, false},          // 10
		{EffectFireResistance, EffectCategoryBeneficial, 0xFF9900, false},      // 11
		{EffectWaterBreathing, EffectCategoryBeneficial, 0x98DAC0, false},      // 12
		{EffectInvisibility, EffectCategoryBeneficial, 0xF6F6F6, false},        // 13
		{EffectBlindness, EffectCategoryHarmful, 0x1F1F23, false},              // 14
		{EffectNightVision, EffectCategoryBeneficial, 0xC2FF66, false},         // 15
		{EffectHunger, EffectCategoryHarmful, 0x587653, false},                 // 16
		{EffectWeakness, EffectCategoryHarmful, 0x484D48, false},               // 17
		{EffectPoison, EffectCategoryHarmful, 0x87A363, false},                 // 18
		{EffectWither, EffectCategoryHarmful, 0x736156, false},                 // 19
		{EffectHealthBoost, EffectCategoryBeneficial, 0xF87D23, false},         // 20
		{EffectAbsorption, EffectCategoryBeneficial, 0x2552A5, false},          // 21
		{EffectSaturation, EffectCategoryBeneficial, 0xF82423, true},           // 22
		{EffectGlowing, EffectCategoryNeutral, 0x94A061, false},                // 23
		{EffectLevitation, EffectCategoryHarmful, 0xCEFFFF, false},             // 24
		{EffectLuck, EffectCategoryBeneficial, 0x59C106, false},                // 25
		{EffectUnluck, EffectCategoryHarmful, 0xC0A44D, false},                 // 26
		{EffectSlowFalling, EffectCategoryBeneficial, 0xF3CFB9, false},         // 27
		{EffectConduitPower, EffectCategoryBeneficial, 0x1DC2D1, false},        // 28
		{EffectDolphinsGrace, EffectCategoryBeneficial, 0x88A3BE, false},       // 29
		{EffectBadOmen, EffectCategoryNeutral, 0x0B6138, false},                // 30
		{EffectHeroOfTheVillage, EffectCategoryBeneficial, 0x44FF44, false},    // 31
		{EffectDarkness, EffectCategoryHarmful, 0x292721, false},               // 32
		{EffectTrialOmen, EffectCategoryNeutral, 0x16A6A6, false},              // 33
		{EffectRaidOmen, EffectCategoryNeutral, 0xDE4058, false},               // 34
		{EffectWindCharged, EffectCategoryHarmful, 0xBDC9FF, false},            // 35
		{EffectWeaving, EffectCategoryHarmful, 0x78695A, false},                // 36
		{EffectOozing, EffectCategoryHarmful, 0x99FFA3, false},                 // 37
		{EffectInfested, EffectCategoryHarmful, 0x8C9B8C, false},               // 38
		{EffectBreathOfTheNautilus, EffectCategoryBeneficial, 0x00FFFE, false}, // 39
	}

	reg := &MobEffectRegistry{
		defs:    make([]*MobEffectDef, 0, len(entries)),
		byID:    make(map[string]*MobEffectDef, len(entries)),
		byIndex: make(map[int32]*MobEffectDef, len(entries)),
	}
	for i, e := range entries {
		def := &MobEffectDef{
			ID:            e.id,
			RegistryIndex: int32(i),
			Category:      e.cat,
			Color:         e.color,
			Instantaneous: e.instant,
		}
		reg.defs = append(reg.defs, def)
		reg.byID[e.id] = def
		reg.byIndex[int32(i)] = def
	}
	return reg
}

// --- Network Packet Encoders & Decoders ---

// EncodeUpdateMobEffectPacket encodes a protocol 775 clientbound
// update_mob_effect (0x84) packet body matching
// ClientboundUpdateMobEffectPacket.STREAM_CODEC.
func EncodeUpdateMobEffectPacket(entityID int32, inst StatusEffectInstance, blend bool) []byte {
	def, ok := MobEffectByID(inst.ID)
	var holderID int32
	if ok {
		holderID = def.RegistryIndex
	}
	var flags byte
	if inst.Ambient {
		flags |= MobEffectFlagAmbient
	}
	if inst.ShowParticles {
		flags |= MobEffectFlagVisible
	}
	if inst.ShowIcon {
		flags |= MobEffectFlagShowIcon
	}
	if blend {
		flags |= MobEffectFlagBlend
	}

	w := protocol.NewWriter(24)
	w.VarInt(entityID)
	w.VarInt(holderID)
	w.VarInt(inst.Amplifier)
	w.VarInt(inst.Duration)
	w.Byte(flags)
	return w.Bytes()
}

// DecodeUpdateMobEffectPacket decodes a protocol 775 clientbound
// update_mob_effect (0x84) packet body.
func DecodeUpdateMobEffectPacket(r *protocol.Reader) (entityID int32, inst StatusEffectInstance, blend bool, err error) {
	entityID, err = r.VarInt()
	if err != nil {
		return 0, StatusEffectInstance{}, false, err
	}
	holderID, err := r.VarInt()
	if err != nil {
		return 0, StatusEffectInstance{}, false, err
	}
	amp, err := r.VarInt()
	if err != nil {
		return 0, StatusEffectInstance{}, false, err
	}
	dur, err := r.VarInt()
	if err != nil {
		return 0, StatusEffectInstance{}, false, err
	}
	flags, err := r.ReadByte()
	if err != nil {
		return 0, StatusEffectInstance{}, false, err
	}
	effectID := EffectSpeed
	if def, ok := MobEffectByIndex(holderID); ok {
		effectID = def.ID
	}
	inst = StatusEffectInstance{
		ID:            effectID,
		Amplifier:     amp,
		Duration:      dur,
		Ambient:       (flags & MobEffectFlagAmbient) != 0,
		ShowParticles: (flags & MobEffectFlagVisible) != 0,
		ShowIcon:      (flags & MobEffectFlagShowIcon) != 0,
	}
	blend = (flags & MobEffectFlagBlend) != 0
	return entityID, inst, blend, nil
}

// EncodeRemoveMobEffectPacket encodes a protocol 775 clientbound
// remove_mob_effect (0x4e) packet body matching
// ClientboundRemoveMobEffectPacket.STREAM_CODEC.
func EncodeRemoveMobEffectPacket(entityID int32, effectID string) []byte {
	def, ok := MobEffectByID(effectID)
	var holderID int32
	if ok {
		holderID = def.RegistryIndex
	}
	w := protocol.NewWriter(12)
	w.VarInt(entityID)
	w.VarInt(holderID)
	return w.Bytes()
}

// DecodeRemoveMobEffectPacket decodes a protocol 775 clientbound
// remove_mob_effect (0x4e) packet body.
func DecodeRemoveMobEffectPacket(r *protocol.Reader) (entityID int32, effectID string, err error) {
	entityID, err = r.VarInt()
	if err != nil {
		return 0, "", err
	}
	holderID, err := r.VarInt()
	if err != nil {
		return 0, "", err
	}
	if def, ok := MobEffectByIndex(holderID); ok {
		return entityID, def.ID, nil
	}
	return entityID, "", nil
}

// --- Periodic Tick Schedule & Merge Rules ---

// ShouldApplyPeriodicEffect reports whether an effect with the given ID,
// remaining duration (ticks), and amplifier (0-based) should fire its periodic
// tick effect on the current tick, matching MobEffect.shouldApplyEffectTickThisTick.
func ShouldApplyPeriodicEffect(effectID string, duration, amplifier int32) bool {
	if duration <= 0 && duration != InfiniteEffectDuration {
		return false
	}
	if amplifier < 0 {
		amplifier = 0
	}
	tick := duration
	if tick < 0 {
		tick = 1
	}
	switch canonicalEffectID(effectID) {
	case EffectRegeneration:
		interval := int32(50) >> minInt32(amplifier, 30)
		if interval <= 0 {
			return true
		}
		return tick%interval == 0
	case EffectPoison:
		interval := int32(25) >> minInt32(amplifier, 30)
		if interval <= 0 {
			return true
		}
		return tick%interval == 0
	case EffectWither:
		interval := int32(40) >> minInt32(amplifier, 30)
		if interval <= 0 {
			return true
		}
		return tick%interval == 0
	case EffectHunger, EffectSaturation:
		return true
	default:
		return false
	}
}

func minInt32(a, b int32) int32 {
	if a < b {
		return a
	}
	return b
}

// MergeEffectInstance merges an incoming StatusEffectInstance into an existing
// instance of the same effect ID according to vanilla MobEffectInstance.update:
//   - A higher amplifier upgrades the amplifier and adopts the incoming duration.
//   - The same amplifier extends the duration if the incoming duration is longer
//     (or infinite).
//   - A lower amplifier does not downgrade the active effect, returning upgraded=false.
func MergeEffectInstance(existing, incoming StatusEffectInstance) (merged StatusEffectInstance, changed bool) {
	merged = existing.Clone()
	incoming = incoming.Clone()
	if merged.ID == "" {
		return incoming, true
	}
	if incoming.Amplifier > merged.Amplifier {
		merged.Amplifier = incoming.Amplifier
		merged.Duration = incoming.Duration
		merged.Ambient = incoming.Ambient
		merged.ShowParticles = incoming.ShowParticles
		merged.ShowIcon = incoming.ShowIcon
		return merged, true
	}
	if incoming.Amplifier == merged.Amplifier {
		incomingLonger := incoming.IsInfinite() && !merged.IsInfinite() ||
			(!merged.IsInfinite() && incoming.Duration > merged.Duration)
		if incomingLonger {
			merged.Duration = incoming.Duration
			merged.Ambient = incoming.Ambient
			merged.ShowParticles = incoming.ShowParticles
			merged.ShowIcon = incoming.ShowIcon
			return merged, true
		}
	}
	return merged, false
}

// --- Per-Player & Per-Entity Status Effect State ---

type playerEffectTracker struct {
	mu         sync.Mutex
	effects    map[string]StatusEffectInstance
	absorption float32
}

type entityEffectTracker struct {
	mu      sync.Mutex
	effects map[int32]map[string]StatusEffectInstance
}

var (
	playerEffects sync.Map // map[*PlayerSession]*playerEffectTracker
	serverEffects sync.Map // map[*Server]*entityEffectTracker
)

func getPlayerEffectTracker(session *PlayerSession) *playerEffectTracker {
	if session == nil {
		return nil
	}
	if v, ok := playerEffects.Load(session); ok {
		return v.(*playerEffectTracker)
	}
	actual, _ := playerEffects.LoadOrStore(session, &playerEffectTracker{
		effects: make(map[string]StatusEffectInstance),
	})
	return actual.(*playerEffectTracker)
}

func (s *Server) entityEffectState() *entityEffectTracker {
	if v, ok := serverEffects.Load(s); ok {
		return v.(*entityEffectTracker)
	}
	actual, _ := serverEffects.LoadOrStore(s, &entityEffectTracker{
		effects: make(map[int32]map[string]StatusEffectInstance),
	})
	return actual.(*entityEffectTracker)
}

// AbsorptionHealth returns the player's current absorption hearts (golden hearts).
func (session *PlayerSession) AbsorptionHealth() float32 {
	if session == nil {
		return 0
	}
	tr := getPlayerEffectTracker(session)
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return tr.absorption
}

// SetAbsorptionHealth sets the player's current absorption hearts (clamped >= 0).
func (session *PlayerSession) SetAbsorptionHealth(amount float32) {
	if session == nil {
		return
	}
	if amount < 0 {
		amount = 0
	}
	tr := getPlayerEffectTracker(session)
	tr.mu.Lock()
	tr.absorption = amount
	tr.mu.Unlock()
}

// PlayerEffects returns a sorted slice of all active status effects on session.
func (session *PlayerSession) PlayerEffects() []StatusEffectInstance {
	if session == nil {
		return nil
	}
	tr := getPlayerEffectTracker(session)
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if len(tr.effects) == 0 {
		return nil
	}
	out := make([]StatusEffectInstance, 0, len(tr.effects))
	for _, inst := range tr.effects {
		out = append(out, inst.Clone())
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	return out
}

// PlayerEffect returns the active StatusEffectInstance for effectID on session, if present.
func (session *PlayerSession) PlayerEffect(effectID string) (StatusEffectInstance, bool) {
	if session == nil {
		return StatusEffectInstance{}, false
	}
	id := canonicalEffectID(effectID)
	tr := getPlayerEffectTracker(session)
	tr.mu.Lock()
	defer tr.mu.Unlock()
	inst, ok := tr.effects[id]
	return inst.Clone(), ok
}

// HasPlayerEffect reports whether session currently has effectID active.
func (session *PlayerSession) HasPlayerEffect(effectID string) bool {
	_, ok := session.PlayerEffect(effectID)
	return ok
}

// ApplyPlayerEffect applies inst to session with scale 1.0 (executing instant
// health/damage/saturation immediately or adding/upgrading timed effects,
// broadcasting UpdateMobEffect, and firing advancement triggers).
func (s *Server) ApplyPlayerEffect(session *PlayerSession, inst StatusEffectInstance) bool {
	return s.ApplyPlayerEffectScaled(session, inst, 1.0)
}

// ApplyPlayerEffectScaled applies inst to session scaled by distance attenuation
// (1.0 for direct drink/application, (0, 1] for splash/lingering potions).
func (s *Server) ApplyPlayerEffectScaled(session *PlayerSession, inst StatusEffectInstance, scale float64) bool {
	if session == nil || session.IsDead() {
		return false
	}
	inst = inst.Clone()
	def, ok := MobEffectByID(inst.ID)
	if !ok {
		return false
	}
	if inst.Amplifier < 0 {
		inst.Amplifier = 0
	}
	if scale <= 0 {
		return false
	}
	if scale > 1.0 {
		scale = 1.0
	}

	// Handle instantaneous effects (instant_health, instant_damage, saturation).
	switch def.ID {
	case EffectInstantHealth:
		base := float64(int(4) << minInt32(inst.Amplifier, 20))
		amount := float32(math.Floor(scale*base + 0.5))
		if amount < 1.0 {
			amount = 1.0
		}
		if s != nil {
			s.HealPlayer(session, amount)
		}
		return true

	case EffectInstantDamage:
		base := float64(int(6) << minInt32(inst.Amplifier, 20))
		amount := float32(math.Floor(scale*base + 0.5))
		if amount < 1.0 {
			amount = 1.0
		}
		if s != nil {
			s.DamagePlayerWithEffects(session, amount, "magic")
		}
		return true

	case EffectSaturation:
		if inst.Duration <= 1 {
			ticks := int32(1)
			s.applySaturationEffectTick(session, inst.Amplifier, ticks)
			return true
		}
	}

	if !inst.IsInfinite() {
		if scale < 1.0 {
			inst.Duration = int32(scale*float64(inst.Duration) + 0.5)
		}
		if inst.Duration <= 0 {
			return false
		}
	}

	tr := getPlayerEffectTracker(session)
	tr.mu.Lock()
	existing, exists := tr.effects[def.ID]
	var active StatusEffectInstance
	changed := false
	if !exists {
		active = inst
		tr.effects[def.ID] = active
		changed = true
	} else {
		active, changed = MergeEffectInstance(existing, inst)
		if changed {
			tr.effects[def.ID] = active
		}
	}
	if changed && def.ID == EffectAbsorption {
		absHearts := float32(4 * (active.Amplifier + 1))
		if absHearts > tr.absorption {
			tr.absorption = absHearts
		}
	}
	tr.mu.Unlock()

	if !changed {
		return false
	}

	s.sendUpdateMobEffect(session, active, true)
	if s != nil {
		s.TriggerEffectsChanged(session)
	}
	return true
}

// RemovePlayerEffect removes effectID from session and sends RemoveMobEffect (0x4e).
func (s *Server) RemovePlayerEffect(session *PlayerSession, effectID string) bool {
	if session == nil {
		return false
	}
	id := canonicalEffectID(effectID)
	tr := getPlayerEffectTracker(session)
	tr.mu.Lock()
	_, exists := tr.effects[id]
	if exists {
		delete(tr.effects, id)
		if id == EffectAbsorption {
			tr.absorption = 0
		}
	}
	tr.mu.Unlock()

	if !exists {
		return false
	}
	s.sendRemoveMobEffect(session, id)
	return true
}

// ClearPlayerEffects removes all active status effects from session (e.g. when
// drinking milk or dying) and sends RemoveMobEffect packets for each cleared effect.
func (s *Server) ClearPlayerEffects(session *PlayerSession) int {
	if session == nil {
		return 0
	}
	tr := getPlayerEffectTracker(session)
	tr.mu.Lock()
	if len(tr.effects) == 0 {
		tr.absorption = 0
		tr.mu.Unlock()
		return 0
	}
	removed := make([]string, 0, len(tr.effects))
	for id := range tr.effects {
		removed = append(removed, id)
	}
	tr.effects = make(map[string]StatusEffectInstance)
	tr.absorption = 0
	tr.mu.Unlock()

	sort.Strings(removed)
	for _, id := range removed {
		s.sendRemoveMobEffect(session, id)
	}
	return len(removed)
}

func (s *Server) sendUpdateMobEffect(session *PlayerSession, inst StatusEffectInstance, blend bool) {
	if session == nil {
		return
	}
	pkt := EncodeUpdateMobEffectPacket(session.EntityID, inst, blend)
	if session.send != nil {
		_ = session.send(PlayUpdateMobEffect, pkt)
	}
	if s != nil {
		snap := session.Snapshot()
		cx := int32(int64(math.Floor(snap.X)) >> 4)
		cz := int32(int64(math.Floor(snap.Z)) >> 4)
		s.BroadcastDimensionChunkExcept(snap.Dimension, cx, cz, session.Profile.UUID, PlayUpdateMobEffect, pkt)
	}
}

func (s *Server) sendRemoveMobEffect(session *PlayerSession, effectID string) {
	if session == nil {
		return
	}
	pkt := EncodeRemoveMobEffectPacket(session.EntityID, effectID)
	if session.send != nil {
		_ = session.send(PlayRemoveMobEffect, pkt)
	}
	if s != nil {
		snap := session.Snapshot()
		cx := int32(int64(math.Floor(snap.X)) >> 4)
		cz := int32(int64(math.Floor(snap.Z)) >> 4)
		s.BroadcastDimensionChunkExcept(snap.Dimension, cx, cz, session.Profile.UUID, PlayRemoveMobEffect, pkt)
	}
}

// SendPlayerActiveEffects sends UpdateMobEffect packets for all active status
// effects on session (useful upon login, dimension change, or respawn).
func (s *Server) SendPlayerActiveEffects(session *PlayerSession) error {
	if session == nil || session.send == nil {
		return nil
	}
	for _, inst := range session.PlayerEffects() {
		if err := session.send(PlayUpdateMobEffect, EncodeUpdateMobEffectPacket(session.EntityID, inst, false)); err != nil {
			return err
		}
	}
	return nil
}

// TickPlayerEffects advances all active status effects on session by one game
// tick: applying regeneration, poison, wither, hunger, and saturation tick
// effects, decrementing durations, and expiring finished effects.
func (s *Server) TickPlayerEffects(session *PlayerSession) {
	if session == nil {
		return
	}
	if session.IsDead() {
		s.ClearPlayerEffects(session)
		return
	}

	tr := getPlayerEffectTracker(session)
	tr.mu.Lock()
	if len(tr.effects) == 0 {
		tr.mu.Unlock()
		return
	}

	keys := make([]string, 0, len(tr.effects))
	for k := range tr.effects {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var (
		toFire   []StatusEffectInstance
		expired  []string
	)
	for _, id := range keys {
		inst := tr.effects[id]
		if ShouldApplyPeriodicEffect(inst.ID, inst.Duration, inst.Amplifier) {
			toFire = append(toFire, inst)
		}
		if !inst.IsInfinite() {
			inst.Duration--
			if inst.Duration <= 0 {
				delete(tr.effects, id)
				if id == EffectAbsorption {
					tr.absorption = 0
				}
				expired = append(expired, id)
			} else {
				tr.effects[id] = inst
			}
		}
	}
	tr.mu.Unlock()

	for _, inst := range toFire {
		s.applyPeriodicPlayerEffect(session, inst)
	}
	for _, id := range expired {
		s.sendRemoveMobEffect(session, id)
	}
}

// TickAllPlayerEffects ticks active status effects for all connected players
// and active lingering potion clouds.
func (s *Server) TickAllPlayerEffects() {
	if s == nil {
		return
	}
	s.playersMu.RLock()
	players := make([]*PlayerSession, 0, len(s.players))
	for _, p := range s.players {
		players = append(players, p)
	}
	s.playersMu.RUnlock()

	for _, p := range players {
		s.TickPlayerEffects(p)
	}
	s.TickAllEntityEffects()
	s.TickLingeringClouds()
}

func (s *Server) applyPeriodicPlayerEffect(session *PlayerSession, inst StatusEffectInstance) {
	if session == nil || session.IsDead() {
		return
	}
	switch inst.ID {
	case EffectRegeneration:
		if s != nil {
			s.HealPlayer(session, 1.0)
		}

	case EffectPoison:
		// Vanilla PoisonMobEffect: damages by 1.0 only while health > 1.0 (never fatal).
		hp := session.Health()
		if hp > 1.0 && s != nil {
			dmg := float32(1.0)
			if hp-dmg < 1.0 {
				dmg = hp - 1.0
			}
			if dmg > 0 {
				s.DamagePlayer(session, dmg, "magic")
			}
		}

	case EffectWither:
		// Vanilla WitherMobEffect: damages by 1.0 and can kill the player.
		if s != nil {
			s.DamagePlayer(session, 1.0, "wither")
		}

	case EffectHunger:
		// Vanilla HungerMobEffect: adds 0.005f * (amplifier + 1) exhaustion per tick.
		if s != nil {
			s.AddExhaustion(session, 0.005*float32(inst.Amplifier+1))
		}

	case EffectSaturation:
		s.applySaturationEffectTick(session, inst.Amplifier, 1)
	}
}

func (s *Server) applySaturationEffectTick(session *PlayerSession, amplifier, multiplier int32) {
	if session == nil || session.IsDead() {
		return
	}
	if amplifier < 0 {
		amplifier = 0
	}
	if multiplier <= 0 {
		multiplier = 1
	}
	addFood := (amplifier + 1) * multiplier
	addSat := float32(addFood) * 2.0

	session.mu.Lock()
	food := session.foodLevel + addFood
	if food > MaxFoodLevel {
		food = MaxFoodLevel
	}
	sat := session.foodSaturation + addSat
	if sat > float32(food) {
		sat = float32(food)
	}
	changed := food != session.foodLevel || sat != session.foodSaturation
	session.foodLevel = food
	session.foodSaturation = sat
	hp := session.health
	send := session.send
	session.mu.Unlock()

	if changed && send != nil {
		_ = send(protocol.PlaySetHealth, EncodeSetHealth(hp, food, sat))
	}
}

// DamagePlayerWithEffects applies damage to session after accounting for Fire
// Resistance (immunity to fire/lava/hot_floor), Resistance (20% reduction per
// level), and Absorption hearts. Returns true if the player died.
func (s *Server) DamagePlayerWithEffects(session *PlayerSession, amount float32, cause string) bool {
	if s == nil || session == nil || amount <= 0 || session.IsDead() {
		return false
	}
	switch cause {
	case "fire", "in_fire", "on_fire", "lava", "hot_floor", "fireball":
		if session.HasFireResistance() {
			return false
		}
	case "drown", "drowning":
		if session.HasWaterBreathing() || session.HasPlayerEffect(EffectConduitPower) {
			return false
		}
	case "fall":
		if session.HasSlowFalling() {
			return false
		}
		if jb := session.JumpBoostLevel(); jb > 0 {
			amount -= float32(jb)
			if amount <= 0 {
				return false
			}
		}
	}

	if mult := session.ResistanceDamageMultiplier(cause); mult < 1.0 {
		amount *= mult
		if amount <= 0 {
			return false
		}
	}

	// Drain Absorption hearts before real health.
	tr := getPlayerEffectTracker(session)
	tr.mu.Lock()
	if tr.absorption > 0 {
		absorbed := amount
		if absorbed > tr.absorption {
			absorbed = tr.absorption
		}
		tr.absorption -= absorbed
		amount -= absorbed
		if tr.absorption <= 0 {
			tr.absorption = 0
		}
	}
	tr.mu.Unlock()

	if amount <= 0 {
		return false
	}
	return s.DamagePlayer(session, amount, cause)
}

// --- Gameplay Modifier Helpers ---

// MovementSpeedMultiplier returns session's movement speed multiplier from
// Speed (+20% per level) and Slowness (-15% per level).
func (session *PlayerSession) MovementSpeedMultiplier() float32 {
	if session == nil {
		return 1.0
	}
	mult := float32(1.0)
	if sp, ok := session.PlayerEffect(EffectSpeed); ok {
		mult *= 1.0 + 0.20*float32(sp.Amplifier+1)
	}
	if sl, ok := session.PlayerEffect(EffectSlowness); ok {
		slowFactor := 1.0 - 0.15*float32(sl.Amplifier+1)
		if slowFactor < 0 {
			slowFactor = 0
		}
		mult *= slowFactor
	}
	return mult
}

// MiningSpeedMultiplier returns session's block break speed multiplier from
// Haste (+20% per level) and Mining Fatigue (0.3^level up to level 4).
func (session *PlayerSession) MiningSpeedMultiplier() float32 {
	if session == nil {
		return 1.0
	}
	mult := float32(1.0)
	if h, ok := session.PlayerEffect(EffectHaste); ok {
		mult *= 1.0 + 0.20*float32(h.Amplifier+1)
	}
	if cp, ok := session.PlayerEffect(EffectConduitPower); ok {
		conduitMult := 1.0 + 0.20*float32(cp.Amplifier+1)
		if conduitMult > mult {
			mult = conduitMult
		}
	}
	if mf, ok := session.PlayerEffect(EffectMiningFatigue); ok {
		lvl := mf.Amplifier + 1
		if lvl > 4 {
			lvl = 4
		}
		for i := int32(0); i < lvl; i++ {
			mult *= 0.3
		}
	}
	return mult
}

// MeleeDamageBonus returns session's flat melee attack damage modifier from
// Strength (+3 per level) and Weakness (-4 per level).
func (session *PlayerSession) MeleeDamageBonus() float32 {
	if session == nil {
		return 0
	}
	var bonus float32
	if st, ok := session.PlayerEffect(EffectStrength); ok {
		bonus += 3.0 * float32(st.Amplifier+1)
	}
	if wk, ok := session.PlayerEffect(EffectWeakness); ok {
		bonus -= 4.0 * float32(wk.Amplifier+1)
	}
	return bonus
}

// ResistanceDamageMultiplier returns the damage multiplier in [0.0, 1.0] from
// the Resistance effect (-20% damage per level, bypassed only by void/starve).
func (session *PlayerSession) ResistanceDamageMultiplier(cause string) float32 {
	if session == nil {
		return 1.0
	}
	switch cause {
	case "void", "out_of_world", "starve":
		return 1.0
	}
	if res, ok := session.PlayerEffect(EffectResistance); ok {
		mult := 1.0 - 0.20*float32(res.Amplifier+1)
		if mult < 0 {
			mult = 0
		}
		return mult
	}
	return 1.0
}

// JumpBoostLevel returns session's active Jump Boost level (1 = Jump Boost I,
// 2 = Jump Boost II, or 0 if not active).
func (session *PlayerSession) JumpBoostLevel() int32 {
	if session == nil {
		return 0
	}
	if jb, ok := session.PlayerEffect(EffectJumpBoost); ok {
		return jb.Amplifier + 1
	}
	return 0
}

// JumpBoostPower returns the extra vertical velocity (0.1 per level) granted by Jump Boost.
func (session *PlayerSession) JumpBoostPower() float32 {
	lvl := session.JumpBoostLevel()
	if lvl <= 0 {
		return 0
	}
	return 0.1 * float32(lvl)
}

// HasFireResistance reports whether session has Fire Resistance active.
func (session *PlayerSession) HasFireResistance() bool {
	return session.HasPlayerEffect(EffectFireResistance)
}

// HasWaterBreathing reports whether session has Water Breathing active.
func (session *PlayerSession) HasWaterBreathing() bool {
	return session.HasPlayerEffect(EffectWaterBreathing)
}

// HasSlowFalling reports whether session has Slow Falling active.
func (session *PlayerSession) HasSlowFalling() bool {
	return session.HasPlayerEffect(EffectSlowFalling)
}

// HasNightVision reports whether session has Night Vision active.
func (session *PlayerSession) HasNightVision() bool {
	return session.HasPlayerEffect(EffectNightVision)
}

// HasInvisibility reports whether session has Invisibility active.
func (session *PlayerSession) HasInvisibility() bool {
	return session.HasPlayerEffect(EffectInvisibility)
}

// --- Entity Status Effects (Mobs & Undead Inversion) ---

// IsUndeadEntityType reports whether entityType is an undead mob (where Instant
// Health deals damage, Instant Damage heals, and Poison/Regeneration are ignored).
func IsUndeadEntityType(entityType string) bool {
	name := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(entityType)), "minecraft:")
	switch name {
	case "zombie", "skeleton", "wither_skeleton", "zombified_piglin", "drowned",
		"husk", "stray", "bogged", "parched", "phantom", "wither",
		"zombie_villager", "zoglin", "skeleton_horse", "zombie_horse", "camel_husk", "zombie_nautilus":
		return true
	default:
		return false
	}
}

// ApplyEntityEffectScaled applies inst to a non-player living entity in s.entities,
// respecting undead inversion for Instant Health / Instant Damage and immunity
// to Poison / Regeneration.
func (s *Server) ApplyEntityEffectScaled(entityID int32, inst StatusEffectInstance, scale float64) bool {
	if s == nil || s.entities == nil {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || ent.IsItem() || ent.IsExperienceOrb() {
		return false
	}
	inst = inst.Clone()
	def, ok := MobEffectByID(inst.ID)
	if !ok || scale <= 0 {
		return false
	}
	if scale > 1.0 {
		scale = 1.0
	}
	if inst.Amplifier < 0 {
		inst.Amplifier = 0
	}

	undead := IsUndeadEntityType(ent.TypeName)

	switch def.ID {
	case EffectInstantHealth:
		if undead {
			base := float64(int(6) << minInt32(inst.Amplifier, 20))
			dmg := float32(math.Floor(scale*base + 0.5))
			s.damageOrKillMobFromEffect(ent, dmg)
		} else {
			base := float64(int(4) << minInt32(inst.Amplifier, 20))
			heal := float32(math.Floor(scale*base + 0.5))
			s.healMobFromEffect(ent, heal)
		}
		return true

	case EffectInstantDamage:
		if undead {
			base := float64(int(4) << minInt32(inst.Amplifier, 20))
			heal := float32(math.Floor(scale*base + 0.5))
			s.healMobFromEffect(ent, heal)
		} else {
			base := float64(int(6) << minInt32(inst.Amplifier, 20))
			dmg := float32(math.Floor(scale*base + 0.5))
			s.damageOrKillMobFromEffect(ent, dmg)
		}
		return true

	case EffectRegeneration, EffectPoison:
		if undead {
			return false
		}
	}

	if !inst.IsInfinite() {
		if scale < 1.0 {
			inst.Duration = int32(scale*float64(inst.Duration) + 0.5)
		}
		if inst.Duration <= 0 {
			return false
		}
	}

	es := s.entityEffectState()
	es.mu.Lock()
	m := es.effects[entityID]
	if m == nil {
		m = make(map[string]StatusEffectInstance)
		es.effects[entityID] = m
	}
	existing, exists := m[def.ID]
	var active StatusEffectInstance
	changed := false
	if !exists {
		active = inst
		m[def.ID] = active
		changed = true
	} else {
		active, changed = MergeEffectInstance(existing, inst)
		if changed {
			m[def.ID] = active
		}
	}
	es.mu.Unlock()

	if changed {
		pos := ent.ChunkPos()
		s.BroadcastDimensionChunk(ent.EffectiveDimension(), pos.X, pos.Z, PlayUpdateMobEffect,
			EncodeUpdateMobEffectPacket(entityID, active, true))
	}
	return changed
}

// EntityEffects returns all active status effects on entityID.
func (s *Server) EntityEffects(entityID int32) []StatusEffectInstance {
	if s == nil {
		return nil
	}
	es := s.entityEffectState()
	es.mu.Lock()
	defer es.mu.Unlock()
	m := es.effects[entityID]
	if len(m) == 0 {
		return nil
	}
	out := make([]StatusEffectInstance, 0, len(m))
	for _, inst := range m {
		out = append(out, inst.Clone())
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	return out
}

// TickAllEntityEffects steps active status effects on all non-player entities.
func (s *Server) TickAllEntityEffects() {
	if s == nil || s.entities == nil {
		return
	}
	es := s.entityEffectState()
	es.mu.Lock()
	if len(es.effects) == 0 {
		es.mu.Unlock()
		return
	}
	type pendingTick struct {
		entityID int32
		inst     StatusEffectInstance
	}
	type pendingRemove struct {
		entityID int32
		effectID string
	}
	var (
		toFire  []pendingTick
		expired []pendingRemove
	)
	for entityID, m := range es.effects {
		ent := s.entities.Get(entityID)
		if ent == nil {
			delete(es.effects, entityID)
			continue
		}
		for id, inst := range m {
			if ShouldApplyPeriodicEffect(inst.ID, inst.Duration, inst.Amplifier) {
				toFire = append(toFire, pendingTick{entityID: entityID, inst: inst})
			}
			if !inst.IsInfinite() {
				inst.Duration--
				if inst.Duration <= 0 {
					delete(m, id)
					expired = append(expired, pendingRemove{entityID: entityID, effectID: id})
				} else {
					m[id] = inst
				}
			}
		}
		if len(m) == 0 {
			delete(es.effects, entityID)
		}
	}
	es.mu.Unlock()

	for _, item := range toFire {
		ent := s.entities.Get(item.entityID)
		if ent == nil {
			continue
		}
		switch item.inst.ID {
		case EffectRegeneration:
			s.healMobFromEffect(ent, 1.0)
		case EffectPoison:
			hp := s.MobHealth(ent.ID)
			if hp > 1.0 {
				dmg := float32(1.0)
				if hp-dmg < 1.0 {
					dmg = hp - 1.0
				}
				s.SetMobHealth(ent.ID, hp-dmg)
			}
		case EffectWither:
			s.damageOrKillMobFromEffect(ent, 1.0)
		}
	}
	for _, rem := range expired {
		if ent := s.entities.Get(rem.entityID); ent != nil {
			pos := ent.ChunkPos()
			s.BroadcastDimensionChunk(ent.EffectiveDimension(), pos.X, pos.Z, PlayRemoveMobEffect,
				EncodeRemoveMobEffectPacket(rem.entityID, rem.effectID))
		}
	}
}

func (s *Server) healMobFromEffect(ent *world.Entity, amount float32) {
	if s == nil || ent == nil || amount <= 0 {
		return
	}
	maxHP := MobMaxHealth(ent.TypeName)
	cur := s.MobHealth(ent.ID)
	if cur <= 0 || cur >= maxHP {
		return
	}
	next := cur + amount
	if next > maxHP {
		next = maxHP
	}
	s.SetMobHealth(ent.ID, next)
}

func (s *Server) damageOrKillMobFromEffect(ent *world.Entity, amount float32) {
	if s == nil || s.entities == nil || ent == nil || amount <= 0 {
		return
	}
	cur := s.MobHealth(ent.ID)
	if cur <= 0 {
		return
	}
	rem := cur - amount
	pos := ent.ChunkPos()
	dim := ent.EffectiveDimension()
	if rem > 0 {
		s.SetMobHealth(ent.ID, rem)
		s.BroadcastDimensionChunk(dim, pos.X, pos.Z, protocol.PlayHurtAnimation, EncodeHurtAnimation(ent.ID, 0))
		return
	}
	s.SetMobHealth(ent.ID, 0)
	s.BroadcastDimensionChunk(dim, pos.X, pos.Z, protocol.PlayEntityEvent, EncodeEntityEventPacket(ent.ID, EntityEventDeath))
	s.entities.Remove(ent.ID)
	s.BroadcastDimensionChunk(dim, pos.X, pos.Z, protocol.PlayRemoveEntities, encodeRemoveEntitiesPacket(nil, []int32{ent.ID}))
}

// --- NBT Serialization (Vanilla 26.1.2 "active_effects" Format) ---

// EncodeEffectInstanceNBT serializes one StatusEffectInstance into a vanilla
// 26.1.2 MobEffectInstance NBT compound.
func EncodeEffectInstanceNBT(inst StatusEffectInstance) *nbt.Compound {
	inst = inst.Clone()
	var ambientByte, particlesByte, iconByte byte
	if inst.Ambient {
		ambientByte = 1
	}
	if inst.ShowParticles {
		particlesByte = 1
	}
	if inst.ShowIcon {
		iconByte = 1
	}
	return nbt.NewCompound().
		Set("id", nbt.String(inst.ID)).
		Set("amplifier", nbt.Byte(inst.Amplifier)).
		Set("duration", nbt.Int(inst.Duration)).
		Set("ambient", nbt.Byte(ambientByte)).
		Set("show_particles", nbt.Byte(particlesByte)).
		Set("show_icon", nbt.Byte(iconByte))
}

// DecodeEffectInstanceNBT parses a vanilla 26.1.2 MobEffectInstance NBT compound.
func DecodeEffectInstanceNBT(comp *nbt.Compound) (StatusEffectInstance, bool) {
	if comp == nil {
		return StatusEffectInstance{}, false
	}
	idTag, ok := comp.Get("id")
	if !ok {
		return StatusEffectInstance{}, false
	}
	idStr, ok := idTag.(nbt.String)
	if !ok {
		return StatusEffectInstance{}, false
	}
	id := canonicalEffectID(string(idStr))
	if _, valid := MobEffectByID(id); !valid {
		return StatusEffectInstance{}, false
	}

	inst := StatusEffectInstance{
		ID:            id,
		Duration:      600,
		ShowParticles: true,
		ShowIcon:      true,
	}
	if ampTag, ok := comp.Get("amplifier"); ok {
		switch v := ampTag.(type) {
		case nbt.Byte:
			inst.Amplifier = int32(uint8(v))
		case nbt.Int:
			inst.Amplifier = int32(v)
		}
	}
	if durTag, ok := comp.Get("duration"); ok {
		if v, ok := durTag.(nbt.Int); ok {
			inst.Duration = int32(v)
		}
	}
	if ambTag, ok := comp.Get("ambient"); ok {
		if v, ok := ambTag.(nbt.Byte); ok {
			inst.Ambient = v != 0
		}
	}
	if partTag, ok := comp.Get("show_particles"); ok {
		if v, ok := partTag.(nbt.Byte); ok {
			inst.ShowParticles = v != 0
		}
	}
	if iconTag, ok := comp.Get("show_icon"); ok {
		if v, ok := iconTag.(nbt.Byte); ok {
			inst.ShowIcon = v != 0
		}
	}
	return inst, true
}

// EncodePlayerEffectsNBT serializes all active status effects on session as a
// vanilla 26.1.2 "active_effects" NBT list of compounds.
func EncodePlayerEffectsNBT(session *PlayerSession) nbt.List {
	effects := session.PlayerEffects()
	elems := make([]nbt.Tag, 0, len(effects))
	for _, inst := range effects {
		elems = append(elems, EncodeEffectInstanceNBT(inst))
	}
	return nbt.List{
		ElemID: nbt.TagCompound,
		Elems:  elems,
	}
}

// DecodePlayerEffectsNBT restores session's active status effects from a
// vanilla 26.1.2 "active_effects" NBT list.
func DecodePlayerEffectsNBT(session *PlayerSession, list nbt.List) {
	if session == nil {
		return
	}
	tr := getPlayerEffectTracker(session)
	tr.mu.Lock()
	tr.effects = make(map[string]StatusEffectInstance)
	for _, elem := range list.Elems {
		comp, ok := elem.(*nbt.Compound)
		if !ok {
			continue
		}
		if inst, valid := DecodeEffectInstanceNBT(comp); valid {
			tr.effects[inst.ID] = inst
			if inst.ID == EffectAbsorption {
				abs := float32(4 * (inst.Amplifier + 1))
				if abs > tr.absorption {
					tr.absorption = abs
				}
			}
		}
	}
	tr.mu.Unlock()
}

// SaveEffectsToPlayerCompound writes session's active status effects and
// AbsorptionAmount onto a playerdata root NBT compound.
func SaveEffectsToPlayerCompound(session *PlayerSession, root *nbt.Compound) {
	if session == nil || root == nil {
		return
	}
	list := EncodePlayerEffectsNBT(session)
	if len(list.Elems) > 0 {
		root.Set("active_effects", list)
	}
	if abs := session.AbsorptionHealth(); abs > 0 {
		root.Set("AbsorptionAmount", nbt.Float(abs))
	}
}

// LoadEffectsFromPlayerCompound restores session's active status effects and
// AbsorptionAmount from a playerdata root NBT compound.
func LoadEffectsFromPlayerCompound(session *PlayerSession, root *nbt.Compound) {
	if session == nil || root == nil {
		return
	}
	if tag, ok := root.Get("active_effects"); ok {
		switch list := tag.(type) {
		case nbt.List:
			DecodePlayerEffectsNBT(session, list)
		case *nbt.List:
			if list != nil {
				DecodePlayerEffectsNBT(session, *list)
			}
		}
	}
	if tag, ok := root.Get("AbsorptionAmount"); ok {
		if f, ok := tag.(nbt.Float); ok && f >= 0 {
			session.SetAbsorptionHealth(float32(f))
		}
	}
}
