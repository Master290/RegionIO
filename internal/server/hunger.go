package server

import (
	"math"
	"strings"
	"sync"
	"sync/atomic"

	"regionio/internal/nbt"
	"regionio/internal/protocol"
	"regionio/internal/world"
)

// Difficulty represents the world difficulty setting.
type Difficulty uint8

const (
	DifficultyPeaceful Difficulty = 0
	DifficultyEasy     Difficulty = 1
	DifficultyNormal   Difficulty = 2
	DifficultyHard     Difficulty = 3
)

// Vanilla 26.1.2 FoodConstants and hunger/exhaustion parameters.
const (
	MaxFoodLevel            int32   = 20
	MaxSaturationLevel      float32 = 20.0
	StartSaturationLevel    float32 = 5.0
	SaturationFloor         float32 = 2.5
	ExhaustionDropThreshold float32 = 4.0
	MaxExhaustionLevel      float32 = 40.0

	HealthTickCountNormal    int32 = 80
	HealthTickCountSaturated int32 = 10

	HealFoodThreshold     int32 = 18
	FastHealFoodThreshold int32 = 20
	SprintFoodThreshold   int32 = 6
	StarveFoodLevel       int32 = 0

	FoodSaturationPoor         float32 = 0.1
	FoodSaturationLow          float32 = 0.3
	FoodSaturationNormal       float32 = 0.6
	FoodSaturationGood         float32 = 0.8
	FoodSaturationMax          float32 = 1.0
	FoodSaturationSupernatural float32 = 1.2

	ExhaustionHeal            float32 = 6.0
	ExhaustionJump            float32 = 0.05
	ExhaustionSprintJump      float32 = 0.2
	ExhaustionMine            float32 = 0.005
	ExhaustionAttack          float32 = 0.1
	ExhaustionDamage          float32 = 0.1
	ExhaustionWalkPerMeter    float32 = 0.0
	ExhaustionCrouchPerMeter  float32 = 0.0
	ExhaustionSprintPerMeter  float32 = 0.1
	ExhaustionSwimPerMeter    float32 = 0.01
	DefaultConsumeTicks       int     = 32 // 1.6s at 20 TPS
	DriedKelpConsumeTicks     int     = 16 // 0.8s at 20 TPS
	HoneyBottleConsumeTicks   int     = 40 // 2.0s at 20 TPS

	HandMain             int32 = 0
	HandOffHand          int32 = 1
	OffHandContainerSlot int   = 45
)

// Protocol 775 (26.1.2) packet and action IDs used by hunger and eating.
const (
	// PlayUseItemSB is ServerboundUseItemPacket (0x43 in protocol 775).
	PlayUseItemSB int32 = 0x43
	// PlayPlayerCommandSB is ServerboundPlayerCommandPacket (0x2a in protocol 775).
	PlayPlayerCommandSB int32 = 0x2a

	// ServerboundPlayerActionPacket$Action.RELEASE_USE_ITEM (5).
	PlayerActionReleaseUseItem int32 = 5

	// ServerboundPlayerCommandPacket$Action values.
	PlayerCommandStopSleeping   int32 = 0
	PlayerCommandStartSprinting int32 = 1
	PlayerCommandStopSprinting  int32 = 2

	// EntityEvent.USE_ITEM_COMPLETE (9) carried by PlayEntityEvent (0x22).
	EntityEventUseItemComplete byte = 9
)

// FoodProperties holds the vanilla 26.1.2 nutrition, saturation, and consume
// behaviour for an edible item.
type FoodProperties struct {
	Nutrition          int32
	SaturationModifier float32
	Saturation         float32
	AlwaysEdible       bool
	ConsumeTicks       int
	ConvertRemainder   string
	HungerEffectChance float32
}

// SaturationByModifier implements FoodConstants.saturationByModifier:
// nutrition * modifier * 2.0f.
func SaturationByModifier(nutrition int32, modifier float32) float32 {
	return float32(nutrition) * modifier * 2.0
}

func makeFood(nutrition int32, modifier float32, alwaysEdible bool, consumeTicks int, remainder string, hungerChance float32) FoodProperties {
	if consumeTicks <= 0 {
		consumeTicks = DefaultConsumeTicks
	}
	return FoodProperties{
		Nutrition:          nutrition,
		SaturationModifier: modifier,
		Saturation:         SaturationByModifier(nutrition, modifier),
		AlwaysEdible:       alwaysEdible,
		ConsumeTicks:       consumeTicks,
		ConvertRemainder:   remainder,
		HungerEffectChance: hungerChance,
	}
}

// vanillaFoods maps canonical item identifiers to their 26.1.2 Foods.java +
// Consumables.java properties.
var vanillaFoods = map[string]FoodProperties{
	"minecraft:apple":                  makeFood(4, FoodSaturationLow, false, DefaultConsumeTicks, "", 0),
	"minecraft:baked_potato":           makeFood(5, FoodSaturationNormal, false, DefaultConsumeTicks, "", 0),
	"minecraft:beef":                   makeFood(3, FoodSaturationLow, false, DefaultConsumeTicks, "", 0),
	"minecraft:beetroot":               makeFood(1, FoodSaturationNormal, false, DefaultConsumeTicks, "", 0),
	"minecraft:beetroot_soup":          makeFood(6, FoodSaturationNormal, false, DefaultConsumeTicks, "minecraft:bowl", 0),
	"minecraft:bread":                  makeFood(5, FoodSaturationNormal, false, DefaultConsumeTicks, "", 0),
	"minecraft:carrot":                 makeFood(3, FoodSaturationNormal, false, DefaultConsumeTicks, "", 0),
	"minecraft:chicken":                makeFood(2, FoodSaturationLow, false, DefaultConsumeTicks, "", 0.3),
	"minecraft:chorus_fruit":           makeFood(4, FoodSaturationLow, true, DefaultConsumeTicks, "", 0),
	"minecraft:cod":                    makeFood(2, FoodSaturationPoor, false, DefaultConsumeTicks, "", 0),
	"minecraft:cooked_beef":            makeFood(8, FoodSaturationGood, false, DefaultConsumeTicks, "", 0),
	"minecraft:cooked_chicken":         makeFood(6, FoodSaturationNormal, false, DefaultConsumeTicks, "", 0),
	"minecraft:cooked_cod":             makeFood(5, FoodSaturationNormal, false, DefaultConsumeTicks, "", 0),
	"minecraft:cooked_mutton":          makeFood(6, FoodSaturationGood, false, DefaultConsumeTicks, "", 0),
	"minecraft:cooked_porkchop":        makeFood(8, FoodSaturationGood, false, DefaultConsumeTicks, "", 0),
	"minecraft:cooked_rabbit":          makeFood(5, FoodSaturationNormal, false, DefaultConsumeTicks, "", 0),
	"minecraft:cooked_salmon":          makeFood(6, FoodSaturationGood, false, DefaultConsumeTicks, "", 0),
	"minecraft:cookie":                 makeFood(2, FoodSaturationPoor, false, DefaultConsumeTicks, "", 0),
	"minecraft:dried_kelp":             makeFood(1, FoodSaturationLow, false, DriedKelpConsumeTicks, "", 0),
	"minecraft:enchanted_golden_apple": makeFood(4, FoodSaturationSupernatural, true, DefaultConsumeTicks, "", 0),
	"minecraft:golden_apple":           makeFood(4, FoodSaturationSupernatural, true, DefaultConsumeTicks, "", 0),
	"minecraft:golden_carrot":          makeFood(6, FoodSaturationSupernatural, false, DefaultConsumeTicks, "", 0),
	"minecraft:honey_bottle":           makeFood(6, FoodSaturationPoor, true, HoneyBottleConsumeTicks, "minecraft:glass_bottle", 0),
	"minecraft:melon_slice":            makeFood(2, FoodSaturationLow, false, DefaultConsumeTicks, "", 0),
	"minecraft:mushroom_stew":          makeFood(6, FoodSaturationNormal, false, DefaultConsumeTicks, "minecraft:bowl", 0),
	"minecraft:mutton":                 makeFood(2, FoodSaturationLow, false, DefaultConsumeTicks, "", 0),
	"minecraft:poisonous_potato":       makeFood(2, FoodSaturationLow, false, DefaultConsumeTicks, "", 0),
	"minecraft:porkchop":               makeFood(3, FoodSaturationLow, false, DefaultConsumeTicks, "", 0),
	"minecraft:potato":                 makeFood(1, FoodSaturationLow, false, DefaultConsumeTicks, "", 0),
	"minecraft:pufferfish":             makeFood(1, FoodSaturationPoor, false, DefaultConsumeTicks, "", 0),
	"minecraft:pumpkin_pie":            makeFood(8, FoodSaturationLow, false, DefaultConsumeTicks, "", 0),
	"minecraft:rabbit":                 makeFood(3, FoodSaturationLow, false, DefaultConsumeTicks, "", 0),
	"minecraft:rabbit_stew":            makeFood(10, FoodSaturationNormal, false, DefaultConsumeTicks, "minecraft:bowl", 0),
	"minecraft:rotten_flesh":           makeFood(4, FoodSaturationPoor, false, DefaultConsumeTicks, "", 0.8),
	"minecraft:salmon":                 makeFood(2, FoodSaturationPoor, false, DefaultConsumeTicks, "", 0),
	"minecraft:spider_eye":             makeFood(2, FoodSaturationGood, false, DefaultConsumeTicks, "", 0),
	"minecraft:suspicious_stew":        makeFood(6, FoodSaturationNormal, true, DefaultConsumeTicks, "minecraft:bowl", 0),
	"minecraft:sweet_berries":          makeFood(2, FoodSaturationPoor, false, DefaultConsumeTicks, "", 0),
	"minecraft:glow_berries":           makeFood(2, FoodSaturationPoor, false, DefaultConsumeTicks, "", 0),
	"minecraft:tropical_fish":          makeFood(1, FoodSaturationPoor, false, DefaultConsumeTicks, "", 0),
}

func normalizeFoodItemID(name string) string {
	name = strings.TrimSpace(strings.ToLower(name))
	if name == "" {
		return ""
	}
	if !strings.Contains(name, ":") {
		return "minecraft:" + name
	}
	return name
}

// FoodPropertiesForItem returns the vanilla 26.1.2 FoodProperties for itemName,
// or false if the item is not edible.
func FoodPropertiesForItem(itemName string) (FoodProperties, bool) {
	props, ok := vanillaFoods[normalizeFoodItemID(itemName)]
	return props, ok
}

// FoodPropertiesForStack returns the vanilla 26.1.2 FoodProperties for stack,
// or false if the stack is empty or not edible.
func FoodPropertiesForStack(stack world.ItemStack) (FoodProperties, bool) {
	if stack.IsEmpty() {
		return FoodProperties{}, false
	}
	return FoodPropertiesForItem(stack.ResolvedID())
}

// IsFoodItem reports whether itemName is a vanilla edible item.
func IsFoodItem(itemName string) bool {
	_, ok := FoodPropertiesForItem(itemName)
	return ok
}

type serverHungerRules struct {
	difficultyPlusOne atomic.Uint32 // 0 = default (DifficultyNormal), otherwise Difficulty+1
	naturalRegenOff   atomic.Bool   // false = naturalRegeneration enabled (default)
}

var serverHungerRulesMap sync.Map // map[*Server]*serverHungerRules

func (s *Server) hungerRules() *serverHungerRules {
	if v, ok := serverHungerRulesMap.Load(s); ok {
		return v.(*serverHungerRules)
	}
	actual, _ := serverHungerRulesMap.LoadOrStore(s, &serverHungerRules{})
	return actual.(*serverHungerRules)
}

// Difficulty returns the server's active difficulty (defaults to DifficultyNormal).
func (s *Server) Difficulty() Difficulty {
	if s == nil {
		return DifficultyNormal
	}
	raw := s.hungerRules().difficultyPlusOne.Load()
	if raw == 0 {
		return DifficultyNormal
	}
	d := Difficulty(raw - 1)
	if d > DifficultyHard {
		return DifficultyNormal
	}
	return d
}

// SetDifficulty sets the server's active difficulty.
func (s *Server) SetDifficulty(d Difficulty) {
	if s == nil {
		return
	}
	if d > DifficultyHard {
		d = DifficultyNormal
	}
	s.hungerRules().difficultyPlusOne.Store(uint32(d) + 1)
}

// NaturalRegeneration reports whether the naturalRegeneration gamerule is active.
func (s *Server) NaturalRegeneration() bool {
	if s == nil {
		return true
	}
	return !s.hungerRules().naturalRegenOff.Load()
}

// SetNaturalRegeneration enables or disables natural health regeneration from food.
func (s *Server) SetNaturalRegeneration(enabled bool) {
	if s == nil {
		return
	}
	s.hungerRules().naturalRegenOff.Store(!enabled)
}

type playerHungerState struct {
	mu                  sync.Mutex
	exhaustion          float32
	tickTimer           int32
	sprinting           bool
	usingItem           bool
	usingHand           int32
	usingSlot           int
	usingItemName       string
	usingTicksRemaining int
	usingTotalTicks     int
}

var playerHungerStates sync.Map // map[*PlayerSession]*playerHungerState

func getHungerState(session *PlayerSession) *playerHungerState {
	if session == nil {
		return nil
	}
	if v, ok := playerHungerStates.Load(session); ok {
		return v.(*playerHungerState)
	}
	actual, _ := playerHungerStates.LoadOrStore(session, &playerHungerState{})
	return actual.(*playerHungerState)
}

// FoodLevel returns the player's current food level (0..20).
func (session *PlayerSession) FoodLevel() int32 {
	if session == nil {
		return 0
	}
	session.mu.RLock()
	defer session.mu.RUnlock()
	return session.foodLevel
}

// FoodSaturation returns the player's current saturation level (0..foodLevel).
func (session *PlayerSession) FoodSaturation() float32 {
	if session == nil {
		return 0
	}
	session.mu.RLock()
	defer session.mu.RUnlock()
	return session.foodSaturation
}

// FoodExhaustion returns the player's accumulated exhaustion (0..40).
func (session *PlayerSession) FoodExhaustion() float32 {
	if session == nil {
		return 0
	}
	st := getHungerState(session)
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.exhaustion
}

// SetFoodExhaustion sets the player's accumulated exhaustion level (clamped to [0, 40]).
func (session *PlayerSession) SetFoodExhaustion(exhaustion float32) {
	if session == nil {
		return
	}
	if exhaustion < 0 {
		exhaustion = 0
	}
	if exhaustion > MaxExhaustionLevel {
		exhaustion = MaxExhaustionLevel
	}
	st := getHungerState(session)
	st.mu.Lock()
	st.exhaustion = exhaustion
	st.mu.Unlock()
}

// FoodTickTimer returns the player's current regeneration/starvation tick counter.
func (session *PlayerSession) FoodTickTimer() int32 {
	if session == nil {
		return 0
	}
	st := getHungerState(session)
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.tickTimer
}

// SetFoodTickTimer sets the player's regeneration/starvation tick counter.
func (session *PlayerSession) SetFoodTickTimer(tickTimer int32) {
	if session == nil {
		return
	}
	if tickTimer < 0 {
		tickTimer = 0
	}
	st := getHungerState(session)
	st.mu.Lock()
	st.tickTimer = tickTimer
	st.mu.Unlock()
}

// IsSprinting reports whether the player is currently sprinting.
func (session *PlayerSession) IsSprinting() bool {
	if session == nil {
		return false
	}
	st := getHungerState(session)
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.sprinting
}

// SetSprinting sets whether the player is sprinting. In survival/adventure mode,
// sprinting requires FoodLevel > SprintFoodThreshold (6).
func (session *PlayerSession) SetSprinting(sprinting bool) {
	if session == nil {
		return
	}
	if sprinting {
		session.mu.RLock()
		gm := session.gameMode
		food := session.foodLevel
		session.mu.RUnlock()
		if (gm == GameModeSurvival || gm == GameModeAdventure) && food <= SprintFoodThreshold {
			sprinting = false
		}
	}
	st := getHungerState(session)
	st.mu.Lock()
	st.sprinting = sprinting
	st.mu.Unlock()
}

// IsEating reports whether the player is currently consuming a food item.
func (session *PlayerSession) IsEating() bool {
	if session == nil {
		return false
	}
	st := getHungerState(session)
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.usingItem
}

// EatingTicksRemaining returns the number of ticks left before the current food
// item finishes being consumed, or 0 if not eating.
func (session *PlayerSession) EatingTicksRemaining() int {
	if session == nil {
		return 0
	}
	st := getHungerState(session)
	st.mu.Lock()
	defer st.mu.Unlock()
	if !st.usingItem {
		return 0
	}
	return st.usingTicksRemaining
}

// WriteHungerNBT writes foodLevel, foodSaturationLevel, foodExhaustionLevel,
// and foodTickTimer onto a playerdata NBT compound.
func (session *PlayerSession) WriteHungerNBT(root *nbt.Compound) {
	if session == nil || root == nil {
		return
	}
	session.mu.RLock()
	food := session.foodLevel
	sat := session.foodSaturation
	session.mu.RUnlock()

	st := getHungerState(session)
	st.mu.Lock()
	exhaustion := st.exhaustion
	tickTimer := st.tickTimer
	st.mu.Unlock()

	root.Set("foodLevel", nbt.Int(food))
	root.Set("foodSaturationLevel", nbt.Float(sat))
	root.Set("foodExhaustionLevel", nbt.Float(exhaustion))
	root.Set("foodTickTimer", nbt.Int(tickTimer))
}

// ReadHungerNBT restores foodLevel, foodSaturationLevel, foodExhaustionLevel,
// and foodTickTimer from a playerdata NBT compound onto session.
func (session *PlayerSession) ReadHungerNBT(root *nbt.Compound) {
	if session == nil || root == nil {
		return
	}
	session.mu.Lock()
	if t, ok := root.Get("foodLevel"); ok {
		if v, ok := t.(nbt.Int); ok {
			fl := int32(v)
			if fl < 0 {
				fl = 0
			}
			if fl > MaxFoodLevel {
				fl = MaxFoodLevel
			}
			session.foodLevel = fl
		}
	}
	if t, ok := root.Get("foodSaturationLevel"); ok {
		if f, ok := t.(nbt.Float); ok {
			sat := float32(f)
			if sat < 0 {
				sat = 0
			}
			if sat > float32(session.foodLevel) {
				sat = float32(session.foodLevel)
			}
			session.foodSaturation = sat
		}
	}
	session.mu.Unlock()

	st := getHungerState(session)
	st.mu.Lock()
	if t, ok := root.Get("foodExhaustionLevel"); ok {
		if f, ok := t.(nbt.Float); ok {
			ex := float32(f)
			if ex < 0 {
				ex = 0
			}
			if ex > MaxExhaustionLevel {
				ex = MaxExhaustionLevel
			}
			st.exhaustion = ex
		}
	}
	if t, ok := root.Get("foodTickTimer"); ok {
		if v, ok := t.(nbt.Int); ok && v >= 0 {
			st.tickTimer = int32(v)
		}
	}
	st.mu.Unlock()
}

// SendSetHealth sends a clientbound PlaySetHealth (0x68) packet with session's
// current health, food level, and saturation.
func (s *Server) SendSetHealth(session *PlayerSession) {
	if session == nil {
		return
	}
	session.mu.RLock()
	health := session.health
	food := session.foodLevel
	saturation := session.foodSaturation
	send := session.send
	session.mu.RUnlock()
	if send != nil {
		_ = send(protocol.PlaySetHealth, EncodeSetHealth(health, food, saturation))
	}
}

// AddExhaustion adds amount to session's food exhaustion in survival or
// adventure mode (clamped to MaxExhaustionLevel = 40.0).
func (s *Server) AddExhaustion(session *PlayerSession, amount float32) {
	if s == nil || session == nil || amount <= 0 || session.IsDead() {
		return
	}
	if s.Difficulty() == DifficultyPeaceful {
		return
	}
	session.mu.RLock()
	gm := session.gameMode
	session.mu.RUnlock()
	if gm != GameModeSurvival && gm != GameModeAdventure {
		return
	}
	st := getHungerState(session)
	st.mu.Lock()
	st.exhaustion += amount
	if st.exhaustion > MaxExhaustionLevel {
		st.exhaustion = MaxExhaustionLevel
	}
	st.mu.Unlock()
}

// RecordPlayerMovementExhaustion adds vanilla exhaustion for jumping, sprint-jumping,
// sprinting distance, and swimming distance when the player moves from
// (oldX, oldY, oldZ) to (newX, newY, newZ).
func (s *Server) RecordPlayerMovementExhaustion(
	session *PlayerSession,
	oldX, oldY, oldZ, newX, newY, newZ float64,
	wasOnGround, onGround bool,
) {
	if s == nil || session == nil || session.IsDead() {
		return
	}
	session.mu.RLock()
	gm := session.gameMode
	food := session.foodLevel
	session.mu.RUnlock()
	if gm != GameModeSurvival && gm != GameModeAdventure {
		return
	}

	sprinting := session.IsSprinting()
	if sprinting && food <= SprintFoodThreshold {
		session.SetSprinting(false)
		sprinting = false
	}

	var totalExhaustion float32
	// Jump exhaustion: transitioning from on-ground to airborne while ascending.
	if wasOnGround && !onGround && newY > oldY+1e-4 {
		if sprinting {
			totalExhaustion += ExhaustionSprintJump
		} else {
			totalExhaustion += ExhaustionJump
		}
	}

	horizDist := math.Hypot(newX-oldX, newZ-oldZ)
	if horizDist > 1e-4 && horizDist <= 16.0 {
		inWater := false
		if s.chunks != nil {
			bx := int(math.Floor(newX))
			by := int(math.Floor(newY))
			bz := int(math.Floor(newZ))
			inWater = s.chunks.GetBlock(bx, by, bz) == world.StateWater
		}
		if inWater {
			totalExhaustion += float32(horizDist) * ExhaustionSwimPerMeter
		} else if sprinting {
			totalExhaustion += float32(horizDist) * ExhaustionSprintPerMeter
		}
	}

	if totalExhaustion > 0 {
		s.AddExhaustion(session, totalExhaustion)
	}
}

// RecordBlockBreakExhaustion adds vanilla mining exhaustion (0.005) when a
// survival player breaks a block.
func (s *Server) RecordBlockBreakExhaustion(session *PlayerSession) {
	s.AddExhaustion(session, ExhaustionMine)
}

// RecordAttackExhaustion adds vanilla melee attack exhaustion (0.1).
func (s *Server) RecordAttackExhaustion(session *PlayerSession) {
	s.AddExhaustion(session, ExhaustionAttack)
}

// RecordDamageExhaustion adds vanilla damage-taken exhaustion (0.1).
func (s *Server) RecordDamageExhaustion(session *PlayerSession) {
	s.AddExhaustion(session, ExhaustionDamage)
}

// HealPlayer increases session's health by up to amount (capped at
// DefaultPlayerHealth = 20.0) and sends PlaySetHealth if health changed.
func (s *Server) HealPlayer(session *PlayerSession, amount float32) float32 {
	if s == nil || session == nil || amount <= 0 || session.IsDead() {
		return 0
	}
	session.mu.Lock()
	if session.health <= 0 || session.health >= world.DefaultPlayerHealth {
		session.mu.Unlock()
		return 0
	}
	before := session.health
	after := before + amount
	if after > world.DefaultPlayerHealth {
		after = world.DefaultPlayerHealth
	}
	healed := after - before
	session.health = after
	food := session.foodLevel
	sat := session.foodSaturation
	send := session.send
	session.mu.Unlock()

	if healed > 0 && send != nil {
		_ = send(protocol.PlaySetHealth, EncodeSetHealth(after, food, sat))
	}
	return healed
}

// HasActiveHungerOrHealthEffects reports whether session currently has any of
// the periodic hunger or health status effects active (hunger, saturation,
// regeneration, poison, or wither).
func HasActiveHungerOrHealthEffects(session *PlayerSession) bool {
	if session == nil {
		return false
	}
	return session.HasPlayerEffect(EffectHunger) ||
		session.HasPlayerEffect(EffectSaturation) ||
		session.HasPlayerEffect(EffectRegeneration) ||
		session.HasPlayerEffect(EffectPoison) ||
		session.HasPlayerEffect(EffectWither)
}

// ActiveHungerExhaustionPerTick returns the exhaustion added per tick by an
// active Hunger status effect (0.005 * (amplifier + 1)), or 0 if not active.
func ActiveHungerExhaustionPerTick(session *PlayerSession) float32 {
	if session == nil {
		return 0
	}
	if inst, ok := session.PlayerEffect(EffectHunger); ok {
		return 0.005 * float32(inst.Amplifier+1)
	}
	return 0
}

// TickPlayerHunger advances one player's eating timer, active status effects
// (hunger, saturation, regeneration, poison, wither via TickPlayerEffects),
// exhaustion drain, natural regeneration (fast saturated regen at food 20,
// normal regen at food >= 18), and starvation damage at food 0 according to
// vanilla 26.1.2 FoodData.tick rules.
func (s *Server) TickPlayerHunger(session *PlayerSession) {
	if s == nil || session == nil {
		return
	}
	s.tickPlayerCombatCooldown(session)
	if session.IsDead() {
		s.CancelEating(session)
		s.ClearPlayerEffects(session)
		return
	}

	// Advance active item consumption first.
	s.tickPlayerEating(session)

	// Delegate periodic status effect ticking (hunger, saturation, regeneration,
	// poison, wither, and duration countdown) to TickPlayerEffects so logic is
	// never duplicated.
	if HasActiveHungerOrHealthEffects(session) || len(session.PlayerEffects()) > 0 {
		s.TickPlayerEffects(session)
		if session.IsDead() {
			s.CancelEating(session)
			return
		}
	}

	session.mu.RLock()
	gm := session.gameMode
	health := session.health
	food := session.foodLevel
	sat := session.foodSaturation
	session.mu.RUnlock()

	if gm == GameModeCreative || gm == GameModeSpectator || health <= 0 {
		return
	}

	diff := s.Difficulty()
	naturalRegen := s.NaturalRegeneration()

	st := getHungerState(session)
	st.mu.Lock()
	exhaustion := st.exhaustion
	tickTimer := st.tickTimer

	stateChanged := false

	// 1. Drain exhaustion in 4.0-unit steps (FoodData.tick lines 10..78).
	for exhaustion > ExhaustionDropThreshold {
		exhaustion -= ExhaustionDropThreshold
		if sat > 0 {
			newSat := sat - 1.0
			if newSat < 0 {
				newSat = 0
			}
			if newSat != sat {
				sat = newSat
				stateChanged = true
			}
		} else if diff != DifficultyPeaceful {
			if food > 0 {
				food--
				stateChanged = true
			}
		}
	}

	if food <= SprintFoodThreshold && st.sprinting {
		st.sprinting = false
	}

	isHurt := health > 0 && health < world.DefaultPlayerHealth
	var (
		healDelta  float32
		starveTick bool
	)

	// 2. Natural regeneration and starvation (FoodData.tick lines 78..324).
	if naturalRegen && sat > 0 && isHurt && food >= FastHealFoodThreshold {
		// Fast saturation-boosted regeneration every 10 ticks (0.5s).
		tickTimer++
		if tickTimer >= HealthTickCountSaturated {
			amt := sat
			if amt > ExhaustionHeal {
				amt = ExhaustionHeal
			}
			healDelta = amt / ExhaustionHeal
			exhaustion += amt
			if exhaustion > MaxExhaustionLevel {
				exhaustion = MaxExhaustionLevel
			}
			tickTimer = 0
		}
	} else if naturalRegen && food >= HealFoodThreshold && isHurt {
		// Standard regeneration: 1.0 HP every 80 ticks (4.0s), +6.0 exhaustion.
		tickTimer++
		if tickTimer >= HealthTickCountNormal {
			healDelta = 1.0
			exhaustion += ExhaustionHeal
			if exhaustion > MaxExhaustionLevel {
				exhaustion = MaxExhaustionLevel
			}
			tickTimer = 0
		}
	} else if food <= StarveFoodLevel {
		// Starvation every 80 ticks (4.0s) respecting difficulty Floor:
		// Easy stops at 10.0 HP, Normal stops at 1.0 HP, Hard can kill.
		tickTimer++
		if tickTimer >= HealthTickCountNormal {
			if health > 10.0 || diff == DifficultyHard || (health > 1.0 && diff == DifficultyNormal) {
				starveTick = true
			}
			tickTimer = 0
		}
	} else {
		tickTimer = 0
	}

	st.exhaustion = exhaustion
	st.tickTimer = tickTimer
	st.mu.Unlock()

	if healDelta > 0 {
		newHealth := health + healDelta
		if newHealth > world.DefaultPlayerHealth {
			newHealth = world.DefaultPlayerHealth
		}
		if newHealth != health {
			health = newHealth
			stateChanged = true
		}
	}

	if stateChanged {
		session.mu.Lock()
		session.health = health
		session.foodLevel = food
		session.foodSaturation = sat
		send := session.send
		session.mu.Unlock()
		if send != nil {
			_ = send(protocol.PlaySetHealth, EncodeSetHealth(health, food, sat))
		}
	}

	if starveTick {
		s.DamagePlayer(session, 1.0, "starve")
		s.RecordDamageExhaustion(session)
	}
}

// TickAllPlayersHunger steps hunger, eating, status effects, and combat cooldowns
// by one tick for all connected players (plus entity effects and lingering clouds).
func (s *Server) TickAllPlayersHunger() {
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
		s.tickPlayerCombatCooldown(p)
		s.TickPlayerHunger(p)
	}
	s.TickAllEntityEffects()
	s.TickLingeringClouds()
}

func (session *PlayerSession) handSlotAndStack(hand int32) (slot int, stack world.ItemStack) {
	session.mu.RLock()
	defer session.mu.RUnlock()
	if hand == HandOffHand {
		return OffHandContainerSlot, session.inventory[OffHandContainerSlot]
	}
	sel := int(session.selectedSlot)
	if sel < 0 || sel >= world.HotbarSlotCount {
		sel = 0
	}
	slot = world.HotbarContainerOffset + sel
	stack = session.inventory[slot]
	if stack.IsEmpty() && !session.inventory[sel].IsEmpty() {
		slot = sel
		stack = session.inventory[sel]
	}
	return slot, stack
}

// CanEat reports whether session can currently eat a food item with props.
func (session *PlayerSession) CanEat(props FoodProperties) bool {
	if session == nil || session.IsDead() {
		return false
	}
	session.mu.RLock()
	gm := session.gameMode
	food := session.foodLevel
	session.mu.RUnlock()
	if gm == GameModeSpectator {
		return false
	}
	if gm == GameModeCreative || props.AlwaysEdible {
		return true
	}
	return food < MaxFoodLevel
}

// StartEating begins consuming the food item in the specified hand (HandMain or
// HandOffHand). Returns true if the item is edible and the player can eat it now.
func (s *Server) StartEating(session *PlayerSession, hand int32) bool {
	if s == nil || session == nil || session.IsDead() {
		return false
	}
	slot, stack := session.handSlotAndStack(hand)
	props, ok := FoodPropertiesForStack(stack)
	if !ok || !session.CanEat(props) {
		return false
	}

	st := getHungerState(session)
	st.mu.Lock()
	st.usingItem = true
	st.usingHand = hand
	st.usingSlot = slot
	st.usingItemName = stack.ResolvedID()
	st.usingTicksRemaining = props.ConsumeTicks
	st.usingTotalTicks = props.ConsumeTicks
	st.mu.Unlock()
	return true
}

// CancelEating aborts any in-progress food consumption for session.
func (s *Server) CancelEating(session *PlayerSession) {
	if session == nil {
		return
	}
	st := getHungerState(session)
	st.mu.Lock()
	st.usingItem = false
	st.usingTicksRemaining = 0
	st.usingTotalTicks = 0
	st.usingItemName = ""
	st.mu.Unlock()
}

func (s *Server) tickPlayerEating(session *PlayerSession) {
	st := getHungerState(session)
	st.mu.Lock()
	if !st.usingItem {
		st.mu.Unlock()
		return
	}
	hand := st.usingHand
	expectedItem := st.usingItemName
	st.mu.Unlock()

	_, currentStack := session.handSlotAndStack(hand)
	if currentStack.IsEmpty() || currentStack.ResolvedID() != expectedItem {
		s.CancelEating(session)
		return
	}

	st.mu.Lock()
	if !st.usingItem {
		st.mu.Unlock()
		return
	}
	st.usingTicksRemaining--
	rem := st.usingTicksRemaining
	st.mu.Unlock()

	if rem <= 0 {
		s.CompleteEating(session)
	}
}

// CompleteEating finishes the active food consumption for session (or consumes
// the currently held food item if called directly), updating food Level,
// saturation, inventory slot, and sending EntityEvent(9) + SetHealth + SetSlot.
func (s *Server) CompleteEating(session *PlayerSession) bool {
	if s == nil || session == nil || session.IsDead() {
		return false
	}
	st := getHungerState(session)
	st.mu.Lock()
	hand := st.usingHand
	st.usingItem = false
	st.usingTicksRemaining = 0
	st.usingTotalTicks = 0
	st.usingItemName = ""
	st.mu.Unlock()

	return s.consumeFoodFromHand(session, hand)
}

// EatHeldItem immediately consumes one food item from session's main hand if
// edible and allowed by hunger rules.
func (s *Server) EatHeldItem(session *PlayerSession) bool {
	if s == nil || session == nil {
		return false
	}
	s.CancelEating(session)
	return s.consumeFoodFromHand(session, HandMain)
}

func (s *Server) consumeFoodFromHand(session *PlayerSession, hand int32) bool {
	slot, stack := session.handSlotAndStack(hand)
	props, ok := FoodPropertiesForStack(stack)
	if !ok || !session.CanEat(props) {
		return false
	}

	session.mu.Lock()
	if session.health <= 0 || session.gameMode == GameModeSpectator {
		session.mu.Unlock()
		return false
	}
	cur := session.inventory[slot]
	if cur.IsEmpty() || cur.ResolvedID() != stack.ResolvedID() {
		session.mu.Unlock()
		return false
	}

	newFood := session.foodLevel + props.Nutrition
	if newFood > MaxFoodLevel {
		newFood = MaxFoodLevel
	}
	if newFood < 0 {
		newFood = 0
	}
	newSat := session.foodSaturation + props.Saturation
	if newSat > float32(newFood) {
		newSat = float32(newFood)
	}
	if newSat < 0 {
		newSat = 0
	}
	session.foodLevel = newFood
	session.foodSaturation = newSat

	gm := session.gameMode
	health := session.health
	entityID := session.EntityID
	pos := session.position
	send := session.send

	var (
		slotModified bool
		updatedStack world.ItemStack
		stateID      int32
	)
	if gm != GameModeCreative {
		slotModified = true
		remaining := cur.Count - 1
		if remaining <= 0 {
			if props.ConvertRemainder != "" {
				updatedStack = world.NewNamedItemStack(props.ConvertRemainder, 1)
			} else {
				updatedStack = world.ItemStack{}
			}
		} else {
			updatedStack = world.NewItemStack(cur.ResolvedItemID(), remaining)
		}
		session.inventory[slot] = updatedStack
		session.containerStateID++
		if session.containerStateID <= 0 {
			session.containerStateID = 1
		}
		stateID = session.containerStateID
	}
	session.mu.Unlock()

	// If the stack had > 1 items and produced a container remainder (e.g. stacked
	// honey bottles), add the remainder to the player's inventory or drop it.
	if slotModified && !updatedStack.IsEmpty() && props.ConvertRemainder != "" && updatedStack.ResolvedID() != props.ConvertRemainder {
		remStack := world.NewNamedItemStack(props.ConvertRemainder, 1)
		if !remStack.IsEmpty() {
			added, changed := session.AddItemToInventory(remStack)
			if send != nil {
				for _, chSlot := range changed {
					stID := session.NextContainerStateID()
					_ = send(protocol.PlayContainerSetSlot, encodeContainerSetSlotPacket(stID, chSlot, session.InventorySlot(chSlot)))
				}
			}
			if added < remStack.Count {
				s.SpawnItemEntity(pos[0], pos[1]+0.5, pos[2], world.NewNamedItemStack(props.ConvertRemainder, remStack.Count-added))
			}
		}
	}

	if send != nil {
		_ = send(protocol.PlayEntityEvent, EncodeEntityEventPacket(entityID, EntityEventUseItemComplete))
		_ = send(protocol.PlaySetHealth, EncodeSetHealth(health, newFood, newSat))
		if slotModified {
			_ = send(protocol.PlayContainerSetSlot, encodeContainerSetSlotPacket(stateID, slot, updatedStack))
		}
	}

	cx := int32(int64(math.Floor(pos[0])) >> 4)
	cz := int32(int64(math.Floor(pos[2])) >> 4)
	s.BroadcastChunk(cx, cz, PlaySoundPacketID, EncodeSoundPacket("minecraft:entity.player.burp", SoundSourcePlayers, pos[0], pos[1], pos[2], 0.5, 1.0, 0))
	s.applyConsumedFoodEffects(session, stack, props)
	s.TriggerConsumeItem(session, stack)
	return true
}

func (s *Server) applyConsumedFoodEffects(session *PlayerSession, stack world.ItemStack, props FoodProperties) {
	if s == nil || session == nil || stack.IsEmpty() {
		return
	}
	itemID := normalizeFoodItemID(stack.ResolvedID())
	switch itemID {
	case "minecraft:golden_apple":
		s.ApplyPlayerEffect(session, NewStatusEffectInstance(EffectRegeneration, 100, 1))
		s.ApplyPlayerEffect(session, NewStatusEffectInstance(EffectAbsorption, 2400, 0))
	case "minecraft:enchanted_golden_apple":
		s.ApplyPlayerEffect(session, NewStatusEffectInstance(EffectRegeneration, 400, 1))
		s.ApplyPlayerEffect(session, NewStatusEffectInstance(EffectResistance, 6000, 0))
		s.ApplyPlayerEffect(session, NewStatusEffectInstance(EffectFireResistance, 6000, 0))
		s.ApplyPlayerEffect(session, NewStatusEffectInstance(EffectAbsorption, 2400, 3))
	case "minecraft:honey_bottle":
		if session.HasPlayerEffect(EffectPoison) {
			s.RemovePlayerEffect(session, EffectPoison)
		}
	case "minecraft:spider_eye", "minecraft:poisonous_potato":
		s.ApplyPlayerEffect(session, NewStatusEffectInstance(EffectPoison, 100, 0))
	case "minecraft:pufferfish":
		s.ApplyPlayerEffect(session, NewStatusEffectInstance(EffectPoison, 1200, 1))
		s.ApplyPlayerEffect(session, NewStatusEffectInstance(EffectHunger, 300, 2))
		s.ApplyPlayerEffect(session, NewStatusEffectInstance(EffectNausea, 300, 0))
	default:
		if props.HungerEffectChance >= 0.8 {
			s.ApplyPlayerEffect(session, NewStatusEffectInstance(EffectHunger, 600, 0))
		}
	}
}

// DecodeUseItemPacket decodes a serverbound use_item (0x43) packet body:
// VarInt(hand), VarInt(sequence), Float32(yaw), Float32(pitch).
func DecodeUseItemPacket(r *protocol.Reader) (hand, sequence int32, yaw, pitch float32, err error) {
	hand, err = r.VarInt()
	if err != nil {
		return 0, 0, 0, 0, err
	}
	if r.Remaining() > 0 {
		sequence, err = r.VarInt()
		if err != nil {
			return hand, 0, 0, 0, err
		}
	}
	if r.Remaining() >= 8 {
		yaw, err = r.Float32()
		if err != nil {
			return hand, sequence, 0, 0, err
		}
		pitch, err = r.Float32()
		if err != nil {
			return hand, sequence, yaw, 0, err
		}
	}
	return hand, sequence, yaw, pitch, nil
}

func (s *Server) finishEating(session *PlayerSession) bool {
	return s.CompleteEating(session)
}

// HandleUseItemPacket handles a serverbound use_item (0x43) packet for session,
// starting food consumption when holding an edible item (or drinking/throwing a
// potion/milk bucket, activating an empty map, casting/reeling a fishing rod,
// or raising a shield) and acknowledging the block/item sequence number.
func (s *Server) HandleUseItemPacket(session *PlayerSession, r *protocol.Reader) error {
	hand, seq, _, _, err := DecodeUseItemPacket(r)
	if err != nil {
		return err
	}
	if s != nil && session != nil {
		if !s.StartEating(session, hand) && !s.UsePotionOrMilkItem(session, hand) {
			if !s.UseEmptyMap(session, hand) {
				if cast, wear := s.UseFishingRod(session, hand); !cast && wear == 0 {
					s.UseShield(session, hand)
				}
			}
		}
		if session.send != nil {
			ack := protocol.NewWriter(4)
			ack.VarInt(seq)
			_ = session.send(protocol.PlayBlockChangedAck, ack.Bytes())
		}
	}
	return nil
}

// DecodePlayerCommandPacket decodes a serverbound player_command (0x2a) packet:
// VarInt(entityID), VarInt(action), VarInt(data).
func DecodePlayerCommandPacket(r *protocol.Reader) (entityID, action, data int32, err error) {
	entityID, err = r.VarInt()
	if err != nil {
		return 0, 0, 0, err
	}
	action, err = r.VarInt()
	if err != nil {
		return entityID, 0, 0, err
	}
	if r.Remaining() > 0 {
		data, err = r.VarInt()
		if err != nil {
			return entityID, action, 0, err
		}
	}
	return entityID, action, data, nil
}

// HandlePlayerCommandPacket handles a serverbound player_command (0x2a) packet
// (START_SPRINTING, STOP_SPRINTING, STOP_SLEEPING).
func (s *Server) HandlePlayerCommandPacket(session *PlayerSession, r *protocol.Reader) error {
	_, action, _, err := DecodePlayerCommandPacket(r)
	if err != nil {
		return err
	}
	if s == nil || session == nil {
		return nil
	}
	switch action {
	case PlayerCommandStopSleeping:
		s.SetPlayerSleeping(session, false)
	case PlayerCommandStartSprinting:
		session.SetSprinting(true)
	case PlayerCommandStopSprinting:
		session.SetSprinting(false)
	}
	return nil
}
