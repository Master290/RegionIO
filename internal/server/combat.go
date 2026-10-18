package server

import (
	"math"
	"math/rand"
	"sync"

	"regionio/internal/protocol"
	"regionio/internal/registry"
	"regionio/internal/world"
)

// Protocol 775 (26.1.2) packet IDs and constants for combat, animations, and sounds.
const (
	// PlayAttackSB is ServerboundAttackPacket (0x01 in protocol 775).
	PlayAttackSB int32 = 0x01
	// PlayInteractSB is ServerboundInteractPacket (0x1a in protocol 775).
	PlayInteractSB int32 = 0x1a
	// PlaySwingSB is ServerboundSwingPacket (0x3f in protocol 775).
	PlaySwingSB int32 = 0x3f

	// PlayAnimateCB is ClientboundAnimatePacket (0x02 in protocol 775).
	PlayAnimateCB int32 = 0x02
	// PlaySoundEntityPacketID is ClientboundSoundEntityPacket (0x74 in protocol 775).
	PlaySoundEntityPacketID int32 = 0x74
	// PlaySoundPacketID is ClientboundSoundPacket (0x75 in protocol 775).
	PlaySoundPacketID int32 = 0x75

	// Interact packet action types.
	InteractActionInteract   int32 = 0
	InteractActionAttack     int32 = 1
	InteractActionInteractAt int32 = 2

	// ClientboundEntityEventPacket (0x22) event IDs.
	EntityEventKineticHit byte = 2
	EntityEventDeath      byte = 3
	EntityEventPoof       byte = 60

	// ClientboundAnimatePacket (0x02) action IDs.
	AnimateSwingMainHand    byte = 0
	AnimateWakeUp           byte = 2
	AnimateSwingOffHand     byte = 3
	AnimateCriticalHit      byte = 4
	AnimateMagicCriticalHit byte = 5

	// SoundSource enum indices matching net.minecraft.sounds.SoundSource.
	SoundSourceMaster  int32 = 0
	SoundSourceMusic   int32 = 1
	SoundSourceRecords int32 = 2
	SoundSourceWeather int32 = 3
	SoundSourceBlocks  int32 = 4
	SoundSourceHostile int32 = 5
	SoundSourceNeutral int32 = 6
	SoundSourcePlayers int32 = 7
	SoundSourceAmbient int32 = 8
	SoundSourceVoice   int32 = 9
	SoundSourceUI      int32 = 10

	// Player inventory container armor slots (containerId 0).
	ArmorSlotHead  int = 5
	ArmorSlotChest int = 6
	ArmorSlotLegs  int = 7
	ArmorSlotFeet  int = 8

	// Vanilla 26.1.2 combat parameters.
	BasePlayerAttackDamage   float32 = 1.0
	BasePlayerAttackSpeed    float32 = 4.0
	MaxMeleeAttackReach      float64 = 6.0
	BaseKnockbackStrength    float64 = 0.4
	SprintKnockbackBonus     float64 = 0.4
	KnockbackVerticalImpulse float64 = 0.36
	CriticalHitMultiplier    float32 = 1.5

	// Default Mob Max Health values (vanilla 26.1.2).
	DefaultZombieMaxHealth float32 = 20.0
	DefaultPigMaxHealth    float32 = 10.0
	DefaultMobMaxHealth    float32 = 20.0
)

// WeaponStats holds the total attack damage and attack speed when an item is
// held in the main hand.
type WeaponStats struct {
	AttackDamage float32
	AttackSpeed  float32
}

// vanillaWeapons maps canonical weapon and tool identifiers to their vanilla
// 26.1.2 main-hand total attack damage (1.0 base + material bonus + tool bonus)
// and total attack speed (4.0 base + modifier).
var vanillaWeapons = map[string]WeaponStats{
	// Swords (speed = 4.0 - 2.4 = 1.6)
	"minecraft:wooden_sword":    {AttackDamage: 4.0, AttackSpeed: 1.6},
	"minecraft:golden_sword":    {AttackDamage: 4.0, AttackSpeed: 1.6},
	"minecraft:stone_sword":     {AttackDamage: 5.0, AttackSpeed: 1.6},
	"minecraft:copper_sword":    {AttackDamage: 5.0, AttackSpeed: 1.6},
	"minecraft:iron_sword":      {AttackDamage: 6.0, AttackSpeed: 1.6},
	"minecraft:diamond_sword":   {AttackDamage: 7.0, AttackSpeed: 1.6},
	"minecraft:netherite_sword": {AttackDamage: 8.0, AttackSpeed: 1.6},

	// Axes
	"minecraft:wooden_axe":    {AttackDamage: 7.0, AttackSpeed: 0.8},
	"minecraft:golden_axe":    {AttackDamage: 7.0, AttackSpeed: 1.0},
	"minecraft:stone_axe":     {AttackDamage: 9.0, AttackSpeed: 0.8},
	"minecraft:copper_axe":    {AttackDamage: 9.0, AttackSpeed: 0.8},
	"minecraft:iron_axe":      {AttackDamage: 9.0, AttackSpeed: 0.9},
	"minecraft:diamond_axe":   {AttackDamage: 9.0, AttackSpeed: 1.0},
	"minecraft:netherite_axe": {AttackDamage: 10.0, AttackSpeed: 1.0},

	// Pickaxes (speed = 4.0 - 2.8 = 1.2)
	"minecraft:wooden_pickaxe":    {AttackDamage: 2.0, AttackSpeed: 1.2},
	"minecraft:golden_pickaxe":    {AttackDamage: 2.0, AttackSpeed: 1.2},
	"minecraft:stone_pickaxe":     {AttackDamage: 3.0, AttackSpeed: 1.2},
	"minecraft:copper_pickaxe":    {AttackDamage: 3.0, AttackSpeed: 1.2},
	"minecraft:iron_pickaxe":      {AttackDamage: 4.0, AttackSpeed: 1.2},
	"minecraft:diamond_pickaxe":   {AttackDamage: 5.0, AttackSpeed: 1.2},
	"minecraft:netherite_pickaxe": {AttackDamage: 6.0, AttackSpeed: 1.2},

	// Shovels (speed = 4.0 - 3.0 = 1.0)
	"minecraft:wooden_shovel":    {AttackDamage: 2.5, AttackSpeed: 1.0},
	"minecraft:golden_shovel":    {AttackDamage: 2.5, AttackSpeed: 1.0},
	"minecraft:stone_shovel":     {AttackDamage: 3.5, AttackSpeed: 1.0},
	"minecraft:copper_shovel":    {AttackDamage: 3.5, AttackSpeed: 1.0},
	"minecraft:iron_shovel":      {AttackDamage: 4.5, AttackSpeed: 1.0},
	"minecraft:diamond_shovel":   {AttackDamage: 5.5, AttackSpeed: 1.0},
	"minecraft:netherite_shovel": {AttackDamage: 6.5, AttackSpeed: 1.0},

	// Hoes
	"minecraft:wooden_hoe":    {AttackDamage: 1.0, AttackSpeed: 1.0},
	"minecraft:golden_hoe":    {AttackDamage: 1.0, AttackSpeed: 1.0},
	"minecraft:stone_hoe":     {AttackDamage: 1.0, AttackSpeed: 2.0},
	"minecraft:copper_hoe":    {AttackDamage: 1.0, AttackSpeed: 2.0},
	"minecraft:iron_hoe":      {AttackDamage: 1.0, AttackSpeed: 3.0},
	"minecraft:diamond_hoe":   {AttackDamage: 1.0, AttackSpeed: 4.0},
	"minecraft:netherite_hoe": {AttackDamage: 1.0, AttackSpeed: 4.0},

	// Trident & Mace
	"minecraft:trident": {AttackDamage: 9.0, AttackSpeed: 1.1},
	"minecraft:mace":    {AttackDamage: 6.0, AttackSpeed: 0.6},
}

// ArmorStats holds the defense points, armor toughness, and equipment slot for
// a wearable armor item.
type ArmorStats struct {
	Defense   float32
	Toughness float32
	Slot      int
}

// vanillaArmor maps canonical armor item identifiers to their vanilla 26.1.2
// ArmorMaterials defense, toughness, and container slot.
var vanillaArmor = map[string]ArmorStats{
	// Leather
	"minecraft:leather_helmet":     {Defense: 1, Toughness: 0, Slot: ArmorSlotHead},
	"minecraft:leather_chestplate": {Defense: 3, Toughness: 0, Slot: ArmorSlotChest},
	"minecraft:leather_leggings":   {Defense: 2, Toughness: 0, Slot: ArmorSlotLegs},
	"minecraft:leather_boots":      {Defense: 1, Toughness: 0, Slot: ArmorSlotFeet},

	// Copper (26.1.2)
	"minecraft:copper_helmet":     {Defense: 2, Toughness: 0, Slot: ArmorSlotHead},
	"minecraft:copper_chestplate": {Defense: 4, Toughness: 0, Slot: ArmorSlotChest},
	"minecraft:copper_leggings":   {Defense: 3, Toughness: 0, Slot: ArmorSlotLegs},
	"minecraft:copper_boots":      {Defense: 1, Toughness: 0, Slot: ArmorSlotFeet},

	// Chainmail
	"minecraft:chainmail_helmet":     {Defense: 2, Toughness: 0, Slot: ArmorSlotHead},
	"minecraft:chainmail_chestplate": {Defense: 5, Toughness: 0, Slot: ArmorSlotChest},
	"minecraft:chainmail_leggings":   {Defense: 4, Toughness: 0, Slot: ArmorSlotLegs},
	"minecraft:chainmail_boots":      {Defense: 1, Toughness: 0, Slot: ArmorSlotFeet},

	// Iron
	"minecraft:iron_helmet":     {Defense: 2, Toughness: 0, Slot: ArmorSlotHead},
	"minecraft:iron_chestplate": {Defense: 6, Toughness: 0, Slot: ArmorSlotChest},
	"minecraft:iron_leggings":   {Defense: 5, Toughness: 0, Slot: ArmorSlotLegs},
	"minecraft:iron_boots":      {Defense: 2, Toughness: 0, Slot: ArmorSlotFeet},

	// Golden
	"minecraft:golden_helmet":     {Defense: 2, Toughness: 0, Slot: ArmorSlotHead},
	"minecraft:golden_chestplate": {Defense: 5, Toughness: 0, Slot: ArmorSlotChest},
	"minecraft:golden_leggings":   {Defense: 3, Toughness: 0, Slot: ArmorSlotLegs},
	"minecraft:golden_boots":      {Defense: 1, Toughness: 0, Slot: ArmorSlotFeet},

	// Diamond
	"minecraft:diamond_helmet":     {Defense: 3, Toughness: 2, Slot: ArmorSlotHead},
	"minecraft:diamond_chestplate": {Defense: 8, Toughness: 2, Slot: ArmorSlotChest},
	"minecraft:diamond_leggings":   {Defense: 6, Toughness: 2, Slot: ArmorSlotLegs},
	"minecraft:diamond_boots":      {Defense: 3, Toughness: 2, Slot: ArmorSlotFeet},

	// Netherite
	"minecraft:netherite_helmet":     {Defense: 3, Toughness: 3, Slot: ArmorSlotHead},
	"minecraft:netherite_chestplate": {Defense: 8, Toughness: 3, Slot: ArmorSlotChest},
	"minecraft:netherite_leggings":   {Defense: 6, Toughness: 3, Slot: ArmorSlotLegs},
	"minecraft:netherite_boots":      {Defense: 3, Toughness: 3, Slot: ArmorSlotFeet},

	// Turtle Helmet
	"minecraft:turtle_helmet": {Defense: 2, Toughness: 0, Slot: ArmorSlotHead},
}

// ItemAttackStats returns the total main-hand attack damage and attack speed for
// itemName (defaults to 1.0 damage and 4.0 attack speed for unarmed/non-weapons).
func ItemAttackStats(itemName string) (damage, attackSpeed float32) {
	if stats, ok := vanillaWeapons[normalizeFoodItemID(itemName)]; ok {
		return stats.AttackDamage, stats.AttackSpeed
	}
	return BasePlayerAttackDamage, BasePlayerAttackSpeed
}

// StackAttackStats returns the total main-hand attack damage and attack speed for stack.
func StackAttackStats(stack world.ItemStack) (damage, attackSpeed float32) {
	if stack.IsEmpty() {
		return BasePlayerAttackDamage, BasePlayerAttackSpeed
	}
	return ItemAttackStats(stack.ResolvedID())
}

// PlayerAttackDamageAndSpeed returns the base melee attack damage and attack
// speed for session's currently held item.
func PlayerAttackDamageAndSpeed(session *PlayerSession) (damage, attackSpeed float32) {
	if session == nil {
		return BasePlayerAttackDamage, BasePlayerAttackSpeed
	}
	return StackAttackStats(session.HeldItem())
}

// ArmorItemStats returns the defense, toughness, and armor slot for itemName,
// or false if the item is not wearable armor.
func ArmorItemStats(itemName string) (ArmorStats, bool) {
	stats, ok := vanillaArmor[normalizeFoodItemID(itemName)]
	return stats, ok
}

// ArmorDefense returns the total armor defense points (clamped to [0, 20]) and
// total armor toughness worn by session in container slots 5..8.
func (session *PlayerSession) ArmorDefense() (defense, toughness float32) {
	if session == nil {
		return 0, 0
	}
	session.mu.RLock()
	defer session.mu.RUnlock()
	for slot := ArmorSlotHead; slot <= ArmorSlotFeet; slot++ {
		stack := session.inventory[slot]
		if stack.IsEmpty() {
			continue
		}
		if stats, ok := ArmorItemStats(stack.ResolvedID()); ok {
			defense += stats.Defense
			toughness += stats.Toughness
		}
	}
	if defense > 20.0 {
		defense = 20.0
	}
	return defense, toughness
}

// DamageAfterArmor implements net.minecraft.world.damagesource.CombatRules.getDamageAfterAbsorb:
//
//	f = 2.0 + armorToughness / 4.0
//	g = clamp(armorDefense - damage / f, armorDefense * 0.2, 20.0)
//	return damage * (1.0 - g / 25.0)
func DamageAfterArmor(damage, armorDefense, armorToughness float32) float32 {
	if damage <= 0 {
		return 0
	}
	if armorDefense <= 0 {
		return damage
	}
	if armorDefense > 20.0 {
		armorDefense = 20.0
	}
	if armorToughness < 0 {
		armorToughness = 0
	}
	f := float32(2.0) + armorToughness/4.0
	g := armorDefense - damage/f
	minG := armorDefense * 0.2
	if g < minG {
		g = minG
	}
	if g > 20.0 {
		g = 20.0
	}
	return damage * (1.0 - g/25.0)
}

// ScaleDamageByDifficulty scales incoming mob/environment damage according to
// vanilla difficulty rules.
func ScaleDamageByDifficulty(damage float32, diff Difficulty) float32 {
	switch diff {
	case DifficultyPeaceful:
		return 0
	case DifficultyEasy:
		scaled := damage*0.5 + 1.0
		if scaled < damage {
			return scaled
		}
		return damage
	case DifficultyHard:
		return damage * 1.5
	default:
		return damage
	}
}

type playerCombatState struct {
	mu                   sync.Mutex
	attackStrengthTicker int
	lastSelectedSlot     int32
	shieldBlocking       bool
}

var playerCombatStates sync.Map // map[*PlayerSession]*playerCombatState

func getCombatState(session *PlayerSession) *playerCombatState {
	if session == nil {
		return nil
	}
	if v, ok := playerCombatStates.Load(session); ok {
		return v.(*playerCombatState)
	}
	sel := session.SelectedSlot()
	initState := &playerCombatState{
		attackStrengthTicker: 100, // Start fully charged before the first swing
		lastSelectedSlot:     sel,
	}
	actual, _ := playerCombatStates.LoadOrStore(session, initState)
	return actual.(*playerCombatState)
}

// SetBlockingWithShield sets whether session is actively raising a shield to block attacks/projectiles.
func (session *PlayerSession) SetBlockingWithShield(blocking bool) {
	if session == nil {
		return
	}
	st := getCombatState(session)
	st.mu.Lock()
	st.shieldBlocking = blocking
	st.mu.Unlock()
}

// IsBlockingWithShield reports whether session has a shield raised (or is
// actively blocking while holding a minecraft:shield in main hand or offhand).
func (session *PlayerSession) IsBlockingWithShield() bool {
	if session == nil || session.IsDead() {
		return false
	}
	st := getCombatState(session)
	st.mu.Lock()
	blocking := st.shieldBlocking
	st.mu.Unlock()
	if !blocking {
		return false
	}
	mainItem := session.HeldItem()
	if !mainItem.IsEmpty() && normalizeFoodItemID(mainItem.ResolvedID()) == "minecraft:shield" {
		return true
	}
	offItem := session.InventorySlot(45)
	if !offItem.IsEmpty() && normalizeFoodItemID(offItem.ResolvedID()) == "minecraft:shield" {
		return true
	}
	return true
}

// UseShield starts blocking with a shield when session uses a minecraft:shield
// in hand (0 = main hand, 1 = offhand).
func (s *Server) UseShield(session *PlayerSession, hand int32) bool {
	if s == nil || session == nil || session.IsDead() || s.IsPlayerShieldDisabled(session) {
		return false
	}
	_, stack := session.handSlotAndStack(hand)
	if stack.IsEmpty() || normalizeFoodItemID(stack.ResolvedID()) != "minecraft:shield" {
		return false
	}
	session.SetBlockingWithShield(true)
	return true
}

// DeflectProjectileWithShield handles a shield deflecting an incoming projectile
// for session, playing the shield block sound and firing TriggerDeflectedProjectile.
func (s *Server) DeflectProjectileWithShield(session *PlayerSession) bool {
	if s == nil || session == nil || !session.IsBlockingWithShield() {
		return false
	}
	snap := session.Snapshot()
	cx := int32(int64(math.Floor(snap.X)) >> 4)
	cz := int32(int64(math.Floor(snap.Z)) >> 4)
	s.BroadcastChunk(cx, cz, PlaySoundPacketID,
		EncodeSoundPacket("minecraft:item.shield.block", SoundSourcePlayers, snap.X, snap.Y, snap.Z, 1.0, 1.0, 0))
	s.TriggerDeflectedProjectile(session)
	return true
}

func isProjectileDamageCause(cause string) bool {
	switch normalizeFoodItemID(cause) {
	case "minecraft:arrow", "minecraft:skeleton", "minecraft:trident",
		"minecraft:mob_projectile", "minecraft:projectile", "minecraft:spit",
		"minecraft:fireball", "minecraft:wind_charge", "minecraft:wither_skull":
		return true
	default:
		return false
	}
}

// AttackCooldownDelayTicks returns the number of ticks required for a full-strength
// swing at attackSpeed: 20.0 / attackSpeed.
func AttackCooldownDelayTicks(attackSpeed float32) float32 {
	if attackSpeed <= 0 {
		attackSpeed = BasePlayerAttackSpeed
	}
	return 20.0 / attackSpeed
}

// AttackStrengthScale implements Player.getAttackStrengthScale(adjustTicks):
// clamp((ticksSinceLastAttack + adjustTicks) / (20.0 / attackSpeed), 0.0, 1.0).
func AttackStrengthScale(ticksSinceLastAttack int, attackSpeed, adjustTicks float32) float32 {
	delay := AttackCooldownDelayTicks(attackSpeed)
	if delay <= 0 {
		return 1.0
	}
	scale := (float32(ticksSinceLastAttack) + adjustTicks) / delay
	if scale < 0 {
		return 0
	}
	if scale > 1 {
		return 1
	}
	return scale
}

// AttackDamageMultiplier implements Player.baseDamageScaleFactor:
// 0.2 + scale * scale * 0.8.
func AttackDamageMultiplier(scale float32) float32 {
	if scale < 0 {
		scale = 0
	}
	if scale > 1 {
		scale = 1
	}
	return 0.2 + scale*scale*0.8
}

// AttackStrengthTicks returns the number of ticks elapsed since session's last
// attack or hotbar slot switch.
func (session *PlayerSession) AttackStrengthTicks() int {
	if session == nil {
		return 0
	}
	st := getCombatState(session)
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.attackStrengthTicker
}

// SetAttackStrengthTicks sets session's attack cooldown tick counter.
func (session *PlayerSession) SetAttackStrengthTicks(ticks int) {
	if session == nil {
		return
	}
	if ticks < 0 {
		ticks = 0
	}
	st := getCombatState(session)
	st.mu.Lock()
	st.attackStrengthTicker = ticks
	st.lastSelectedSlot = session.SelectedSlot()
	st.mu.Unlock()
}

// ResetAttackCooldown resets session's attack strength ticker to 0.
func (session *PlayerSession) ResetAttackCooldown() {
	session.SetAttackStrengthTicks(0)
}

// CurrentAttackStrengthScale returns session's current attack strength scale in
// [0.0, 1.0] for the currently held item.
func (session *PlayerSession) CurrentAttackStrengthScale(adjustTicks float32) float32 {
	if session == nil {
		return 1.0
	}
	_, speed := PlayerAttackDamageAndSpeed(session)
	sel := session.SelectedSlot()
	st := getCombatState(session)
	st.mu.Lock()
	if st.lastSelectedSlot != sel {
		st.lastSelectedSlot = sel
		st.attackStrengthTicker = 0
	}
	ticks := st.attackStrengthTicker
	st.mu.Unlock()
	return AttackStrengthScale(ticks, speed, adjustTicks)
}

func (s *Server) tickPlayerCombatCooldown(session *PlayerSession) {
	if session == nil {
		return
	}
	sel := session.SelectedSlot()
	st := getCombatState(session)
	st.mu.Lock()
	if st.lastSelectedSlot != sel {
		st.lastSelectedSlot = sel
		st.attackStrengthTicker = 0
	} else if st.attackStrengthTicker < 10000 {
		st.attackStrengthTicker++
	}
	st.mu.Unlock()
}

// ComputeKnockback computes the horizontal (vx, vz) and vertical (vy) knockback
// velocity impulse applied to a target at (targetX, targetZ) hit by an attacker
// at (attackerX, attackerZ) with facing yaw attackerYaw and knockback strength.
func ComputeKnockback(attackerX, attackerZ float64, attackerYaw float32, targetX, targetZ, strength float64) (vx, vy, vz float64) {
	if strength <= 0 {
		return 0, 0, 0
	}
	dx := targetX - attackerX
	dz := targetZ - attackerZ
	dist := math.Hypot(dx, dz)
	if dist < 1e-4 {
		rad := float64(attackerYaw) * (math.Pi / 180.0)
		dx = -math.Sin(rad)
		dz = math.Cos(rad)
		dist = 1.0
	}
	nx := dx / dist
	nz := dz / dist
	return nx * strength, KnockbackVerticalImpulse, nz * strength
}

// MobMaxHealth returns the default maximum health for a mob entity type.
func MobMaxHealth(typeName string) float32 {
	return ResolveMobMaxHealth(typeName)
}

// MobLootDrops returns the item stack(s) dropped when a mob of typeName dies.
// Zombies drop minecraft:rotten_flesh (1..2) and pigs drop minecraft:porkchop (1..3).
func MobLootDrops(typeName string, rng *rand.Rand) []world.ItemStack {
	switch normalizeFoodItemID(typeName) {
	case MobTypeZombie:
		count := int32(1)
		if rng != nil {
			count = int32(1 + rng.Intn(2)) // 1..2 rotten flesh
		}
		if stack := world.NewNamedItemStack("minecraft:rotten_flesh", count); !stack.IsEmpty() {
			return []world.ItemStack{stack}
		}
	case MobTypePig:
		count := int32(1)
		if rng != nil {
			count = int32(1 + rng.Intn(3)) // 1..3 raw porkchop
		}
		if stack := world.NewNamedItemStack("minecraft:porkchop", count); !stack.IsEmpty() {
			return []world.ItemStack{stack}
		}
	case "minecraft:cow":
		if stack := world.NewNamedItemStack("minecraft:beef", 1); !stack.IsEmpty() {
			return []world.ItemStack{stack}
		}
	case "minecraft:chicken":
		if stack := world.NewNamedItemStack("minecraft:chicken", 1); !stack.IsEmpty() {
			return []world.ItemStack{stack}
		}
	case "minecraft:sheep":
		if stack := world.NewNamedItemStack("minecraft:mutton", 1); !stack.IsEmpty() {
			return []world.ItemStack{stack}
		}
	case "minecraft:spider":
		if stack := world.NewNamedItemStack("minecraft:string", 1); !stack.IsEmpty() {
			return []world.ItemStack{stack}
		}
	case "minecraft:skeleton":
		if stack := world.NewNamedItemStack("minecraft:bone", 1); !stack.IsEmpty() {
			return []world.ItemStack{stack}
		}
	case "minecraft:creeper":
		if stack := world.NewNamedItemStack("minecraft:gunpowder", 1); !stack.IsEmpty() {
			return []world.ItemStack{stack}
		}
	case MobTypeIronGolem:
		ingots := int32(3)
		if rng != nil {
			ingots += int32(rng.Intn(3)) // 3..5 iron_ingot
		}
		var out []world.ItemStack
		if s1 := world.NewNamedItemStack("minecraft:iron_ingot", ingots); !s1.IsEmpty() {
			out = append(out, s1)
		}
		poppies := int32(1)
		if rng != nil {
			poppies = int32(rng.Intn(3)) // 0..2 poppy
		}
		if poppies > 0 {
			if s2 := world.NewNamedItemStack("minecraft:poppy", poppies); !s2.IsEmpty() {
				out = append(out, s2)
			}
		}
		return out
	case MobTypePhantom:
		if stack := world.NewNamedItemStack("minecraft:phantom_membrane", 1); !stack.IsEmpty() {
			return []world.ItemStack{stack}
		}
	case MobTypeParrot:
		feathers := int32(1)
		if rng != nil {
			feathers = int32(1 + rng.Intn(2))
		}
		if stack := world.NewNamedItemStack("minecraft:feather", feathers); !stack.IsEmpty() {
			return []world.ItemStack{stack}
		}
	case MobTypeHorse, MobTypeDonkey, MobTypeMule, MobTypeLlama, MobTypeTraderLlama:
		if stack := world.NewNamedItemStack("minecraft:leather", 1); !stack.IsEmpty() {
			return []world.ItemStack{stack}
		}
	case MobTypeSkeletonHorse:
		if stack := world.NewNamedItemStack("minecraft:bone", 1); !stack.IsEmpty() {
			return []world.ItemStack{stack}
		}
	case MobTypeZombieHorse:
		if stack := world.NewNamedItemStack("minecraft:rotten_flesh", 1); !stack.IsEmpty() {
			return []world.ItemStack{stack}
		}
	case MobTypeCat:
		if stack := world.NewNamedItemStack("minecraft:string", 1); !stack.IsEmpty() {
			return []world.ItemStack{stack}
		}
	case MobTypeWither:
		if stack := world.NewNamedItemStack(NetherStarItemID, 1); !stack.IsEmpty() {
			return []world.ItemStack{stack}
		}
	case MobTypeWitherSkeleton:
		if stack := world.NewNamedItemStack("minecraft:bone", 1); !stack.IsEmpty() {
			return []world.ItemStack{stack}
		}
	case MobTypeWarden:
		if stack := world.NewNamedItemStack(SculkCatalystItemID, 1); !stack.IsEmpty() {
			return []world.ItemStack{stack}
		}
	case MobTypeBreeze:
		if stack := RollBreezeRodDrops(rng, 0); !stack.IsEmpty() {
			return []world.ItemStack{stack}
		}
	case MobTypeBogged:
		return RollBoggedDeathLoot(rng, true)
	}
	return nil
}

func mobHurtSound(typeName string) (sound string, source int32) {
	switch normalizeFoodItemID(typeName) {
	case MobTypeZombie:
		return "minecraft:entity.zombie.hurt", SoundSourceHostile
	case MobTypeBogged:
		return SoundBoggedHurt, SoundSourceHostile
	case MobTypeBreeze:
		return SoundBreezeHurt, SoundSourceHostile
	case MobTypePig:
		return "minecraft:entity.pig.hurt", SoundSourceNeutral
	case MobTypeSniffer:
		return SoundSnifferHurt, SoundSourceNeutral
	case MobTypeArmadillo:
		return SoundArmadilloHurt, SoundSourceNeutral
	case MobTypeCamel:
		return SoundCamelHurt, SoundSourceNeutral
	case MobTypeAllay:
		return SoundAllayHurt, SoundSourceNeutral
	case MobTypeFrog:
		return SoundFrogHurt, SoundSourceNeutral
	case MobTypeTadpole:
		return SoundTadpoleHurt, SoundSourceNeutral
	case MobTypeAxolotl:
		return SoundAxolotlHurt, SoundSourceNeutral
	case MobTypeGoat:
		return SoundGoatHurt, SoundSourceNeutral
	case MobTypeBee:
		return SoundBeeHurt, SoundSourceNeutral
	case MobTypeVillager:
		return "minecraft:entity.villager.hurt", SoundSourceNeutral
	case MobTypeWanderingTrader:
		return SoundWanderingTraderHurt, SoundSourceNeutral
	case MobTypeWolf:
		return "minecraft:entity.wolf.hurt", SoundSourceNeutral
	case MobTypeCat:
		return "minecraft:entity.cat.hurt", SoundSourceNeutral
	case MobTypeParrot:
		return "minecraft:entity.parrot.hurt", SoundSourceNeutral
	case MobTypeHorse:
		return SoundHorseHurt, SoundSourceNeutral
	case MobTypeDonkey:
		return SoundDonkeyHurt, SoundSourceNeutral
	case MobTypeMule:
		return SoundMuleHurt, SoundSourceNeutral
	case MobTypeSkeletonHorse:
		return SoundSkeletonHorseHurt, SoundSourceNeutral
	case MobTypeZombieHorse:
		return SoundZombieHorseHurt, SoundSourceNeutral
	case MobTypeLlama, MobTypeTraderLlama:
		return SoundLlamaHurt, SoundSourceNeutral
	case MobTypeIronGolem:
		return "minecraft:entity.iron_golem.hurt", SoundSourceNeutral
	case MobTypePhantom:
		return "minecraft:entity.phantom.hurt", SoundSourceHostile
	case MobTypeWarden:
		return SoundWardenHurt, SoundSourceHostile
	case MobTypeWither:
		return "minecraft:entity.wither.hurt", SoundSourceHostile
	case MobTypeEnderDragon:
		return "minecraft:entity.ender_dragon.hurt", SoundSourceHostile
	default:
		return "minecraft:entity.generic.hurt", SoundSourceNeutral
	}
}

func mobDeathSound(typeName string) (sound string, source int32) {
	switch normalizeFoodItemID(typeName) {
	case MobTypeZombie:
		return "minecraft:entity.zombie.death", SoundSourceHostile
	case MobTypeBogged:
		return SoundBoggedDeath, SoundSourceHostile
	case MobTypeBreeze:
		return SoundBreezeDeath, SoundSourceHostile
	case MobTypePig:
		return "minecraft:entity.pig.death", SoundSourceNeutral
	case MobTypeSniffer:
		return SoundSnifferDeath, SoundSourceNeutral
	case MobTypeArmadillo:
		return SoundArmadilloDeath, SoundSourceNeutral
	case MobTypeCamel:
		return SoundCamelDeath, SoundSourceNeutral
	case MobTypeAllay:
		return SoundAllayDeath, SoundSourceNeutral
	case MobTypeFrog:
		return SoundFrogDeath, SoundSourceNeutral
	case MobTypeTadpole:
		return SoundTadpoleDeath, SoundSourceNeutral
	case MobTypeAxolotl:
		return SoundAxolotlDeath, SoundSourceNeutral
	case MobTypeGoat:
		return SoundGoatDeath, SoundSourceNeutral
	case MobTypeBee:
		return SoundBeeDeath, SoundSourceNeutral
	case MobTypeVillager:
		return "minecraft:entity.villager.death", SoundSourceNeutral
	case MobTypeWanderingTrader:
		return SoundWanderingTraderDeath, SoundSourceNeutral
	case MobTypeWolf:
		return "minecraft:entity.wolf.death", SoundSourceNeutral
	case MobTypeCat:
		return "minecraft:entity.cat.death", SoundSourceNeutral
	case MobTypeParrot:
		return "minecraft:entity.parrot.death", SoundSourceNeutral
	case MobTypeHorse:
		return SoundHorseDeath, SoundSourceNeutral
	case MobTypeDonkey:
		return SoundDonkeyDeath, SoundSourceNeutral
	case MobTypeMule:
		return SoundMuleDeath, SoundSourceNeutral
	case MobTypeSkeletonHorse:
		return SoundSkeletonHorseDeath, SoundSourceNeutral
	case MobTypeZombieHorse:
		return SoundZombieHorseDeath, SoundSourceNeutral
	case MobTypeLlama, MobTypeTraderLlama:
		return SoundLlamaDeath, SoundSourceNeutral
	case MobTypeIronGolem:
		return "minecraft:entity.iron_golem.death", SoundSourceNeutral
	case MobTypePhantom:
		return "minecraft:entity.phantom.death", SoundSourceHostile
	case MobTypeWarden:
		return SoundWardenDeath, SoundSourceHostile
	case MobTypeWither:
		return "minecraft:entity.wither.death", SoundSourceHostile
	case MobTypeEnderDragon:
		return "minecraft:entity.ender_dragon.death", SoundSourceHostile
	default:
		return "minecraft:entity.generic.death", SoundSourceNeutral
	}
}

// MobHealth returns the current health of the mob with entityID, or 0 if the
// entity does not exist or is an item entity.
func (s *Server) MobHealth(entityID int32) float32 {
	if s == nil || s.entities == nil {
		return 0
	}
	ent := s.entities.Get(entityID)
	if ent == nil || ent.IsItem() {
		return 0
	}
	if IsDecorationEntity(ent.TypeName) {
		return 1000.0
	}
	tr := s.entityTracker()
	tr.mu.Lock()
	defer tr.mu.Unlock()
	mem := tr.mobStates[entityID]
	if mem == nil || !mem.healthInit {
		return MobMaxHealth(ent.TypeName)
	}
	return mem.health
}

// SetMobHealth sets the current health of the mob with entityID.
func (s *Server) SetMobHealth(entityID int32, health float32) {
	if s == nil || s.entities == nil {
		return
	}
	ent := s.entities.Get(entityID)
	if ent == nil || ent.IsItem() {
		return
	}
	if health < 0 {
		health = 0
	}
	tr := s.entityTracker()
	tr.mu.Lock()
	if tr.mobStates == nil {
		tr.mobStates = make(map[int32]*mobMemory)
	}
	mem := tr.mobStates[entityID]
	if mem == nil {
		mem = &mobMemory{seenTick: tr.aiTickID}
		tr.mobStates[entityID] = mem
	}
	mem.healthInit = true
	mem.health = health
	tr.mu.Unlock()
}

// DamagePlayerWithArmor reduces rawDamage by session's worn armor defense,
// toughness, Fire Resistance, Resistance status effect, and Absorption hearts,
// records damage exhaustion (0.1), and applies the resulting damage via
// DamagePlayerWithEffects. Returns the post-armor, post-resistance damage.
func (s *Server) DamagePlayerWithArmor(session *PlayerSession, rawDamage float32, cause string) float32 {
	if s == nil || session == nil || rawDamage <= 0 || session.IsDead() {
		return 0
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeCreative || snap.GameMode == GameModeSpectator || snap.Health <= 0 {
		return 0
	}
	if isProjectileDamageCause(cause) && s.DeflectProjectileWithShield(session) {
		return 0
	}
	switch cause {
	case "fire", "in_fire", "on_fire", "lava", "hot_floor", "fireball":
		if session.HasFireResistance() {
			return 0
		}
	}
	def, tough := session.ArmorDefense()
	afterArmor := DamageAfterArmor(rawDamage, def, tough)
	if afterArmor <= 0 {
		return 0
	}
	effective := afterArmor * session.ResistanceDamageMultiplier(cause)
	if effective <= 0 {
		return 0
	}
	s.DamagePlayerWithEffects(session, afterArmor, cause)
	s.RecordDamageExhaustion(session)
	s.SyncHealthObjectives(session)
	s.EmitVibration(VibrationEvent{
		Dimension:      snap.Dimension,
		Type:           GameEventEntityDamage,
		X:              snap.X,
		Y:              snap.Y,
		Z:              snap.Z,
		Player:         session,
		SourceEntityID: snap.EntityID,
	})
	if session.IsDead() {
		s.IncrementCriteriaObjectives(session.Profile.Name, "deathCount", 1)
		s.EmitVibration(VibrationEvent{
			Dimension:      snap.Dimension,
			Type:           GameEventEntityDie,
			X:              snap.X,
			Y:              snap.Y,
			Z:              snap.Z,
			Player:         session,
			SourceEntityID: snap.EntityID,
		})
	}
	return effective
}

func (s *Server) playerByEntityID(entityID int32) *PlayerSession {
	if s == nil || entityID == 0 {
		return nil
	}
	for _, p := range s.onlinePlayersSnapshot() {
		if p != nil && p.EntityID == entityID {
			return p
		}
	}
	return nil
}

// AttackPlayerFromMob applies a melee attack from a mob onto target, scaling
// damage by world difficulty, reducing it by target's armor and status effects,
// applying knockback velocity, broadcasting hurt sound, and firing
// TriggerKilledByEntity if the player dies.
func (s *Server) AttackPlayerFromMob(mobID int32, mobType string, mobX, mobZ float64, target *PlayerSession, baseDamage float32) float32 {
	if s == nil || target == nil || target.IsDead() {
		return 0
	}
	scaled := ScaleDamageByDifficulty(baseDamage, s.Difficulty())
	if scaled <= 0 {
		return 0
	}
	dealt := s.DamagePlayerWithArmor(target, scaled, mobAttackCauseName(mobType))
	if dealt <= 0 {
		return 0
	}
	if target.IsDead() {
		s.TriggerKilledByEntity(target, mobType)
		s.BroadcastDeathMessage(target, mobAttackCauseName(mobType), mobType)
	} else if mobID > 0 {
		s.NotifyOwnerDamagedByMob(target, mobID)
	}
	snap := target.Snapshot()
	vx, vy, vz := ComputeKnockback(mobX, mobZ, 0, snap.X, snap.Z, BaseKnockbackStrength)
	if target.send != nil {
		_ = target.send(protocol.PlaySetEntityMotion, EncodeSetEntityMotionPacket(target.EntityID, vx, vy, vz))
	}
	cx := int32(int64(math.Floor(snap.X)) >> 4)
	cz := int32(int64(math.Floor(snap.Z)) >> 4)
	s.BroadcastChunk(cx, cz, PlaySoundPacketID, EncodeSoundPacket("minecraft:entity.player.hurt", SoundSourcePlayers, snap.X, snap.Y, snap.Z, 1.0, 1.0, 0))
	return dealt
}

// AttackEntity performs a player melee attack against targetEntityID using
// session's held weapon, Strength/Weakness melee bonus, attack cooldown scaling,
// critical hit and sprint-knockback modifiers, hurt animation/sound, and mob
// death with item drops and advancement triggers.
func (s *Server) AttackEntity(session *PlayerSession, targetEntityID int32) (dealtDamage float32, killed bool) {
	if s == nil || s.entities == nil || session == nil || session.IsDead() {
		return 0, false
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator || snap.Health <= 0 {
		return 0, false
	}

	if victim := s.playerByEntityID(targetEntityID); victim != nil {
		if victim == session || !s.CanAttackPlayer(session, victim) {
			return 0, false
		}
		vSnap := victim.Snapshot()
		dx := vSnap.X - snap.X
		dy := vSnap.Y - snap.Y
		dz := vSnap.Z - snap.Z
		if snap.GameMode != GameModeCreative && (dx*dx+dy*dy+dz*dz) > MaxMeleeAttackReach*MaxMeleeAttackReach {
			return 0, false
		}
		baseDamage, _ := StackAttackStats(session.HeldItem())
		baseDamage += session.MeleeDamageBonus()
		scale := session.CurrentAttackStrengthScale(0.5)
		session.ResetAttackCooldown()
		if baseDamage <= 0 {
			return 0, false
		}
		damage := baseDamage * AttackDamageMultiplier(scale)
		if scale > 0.9 && !snap.OnGround && session.FallDistance() > 0 && !session.IsSprinting() {
			damage *= CriticalHitMultiplier
		}
		s.RecordAttackExhaustion(session)
		dealt := s.DamagePlayerWithArmor(victim, damage, "player")
		if dealt <= 0 {
			return 0, false
		}
		if victim.IsDead() {
			s.IncrementCriteriaObjectives(session.Profile.Name, "playerKillCount", 1)
			s.IncrementCriteriaObjectives(session.Profile.Name, "totalKillCount", 1)
			s.BroadcastDeathMessage(victim, "player", session.Profile.Name)
			return dealt, true
		}
		return dealt, false
	}

	target := s.entities.Get(targetEntityID)
	if target == nil || target.IsItem() {
		return 0, false
	}
	if IsDecorationEntity(target.TypeName) {
		handled, broken := s.AttackDecoration(session, targetEntityID)
		if !handled {
			return 0, false
		}
		return 1.0, broken
	}

	// Enforce vanilla 6-block interaction reach in survival and adventure modes.
	dx := target.X - snap.X
	dy := target.Y - snap.Y
	dz := target.Z - snap.Z
	if snap.GameMode != GameModeCreative && (dx*dx+dy*dy+dz*dz) > MaxMeleeAttackReach*MaxMeleeAttackReach {
		return 0, false
	}

	if target.TypeName == EntityTypeEndCrystal {
		if s.DestroyEndCrystal(targetEntityID, session) {
			return 1.0, true
		}
		return 0, false
	}
	if IsWindChargeEntityType(target.TypeName) {
		yawRad := float64(snap.Yaw) * (math.Pi / 180.0)
		pitchRad := float64(snap.Pitch) * (math.Pi / 180.0)
		cosPitch := math.Cos(pitchRad)
		dirX := -math.Sin(yawRad) * cosPitch
		dirY := -math.Sin(pitchRad)
		dirZ := math.Cos(yawRad) * cosPitch
		if s.DeflectWindCharge(target.ID, dirX, dirY, dirZ, snap.EntityID) {
			return 1.0, false
		}
		return 0, false
	}

	baseDamage, _ := StackAttackStats(session.HeldItem())
	baseDamage += session.MeleeDamageBonus()
	scale := session.CurrentAttackStrengthScale(0.5)
	session.ResetAttackCooldown()
	if baseDamage <= 0 {
		return 0, false
	}

	damage := baseDamage * AttackDamageMultiplier(scale)

	isStrong := scale > 0.9
	isSprinting := session.IsSprinting()
	isCrit := isStrong && !snap.OnGround && session.FallDistance() > 0 && !isSprinting

	if isCrit {
		damage *= CriticalHitMultiplier
	}

	kbStrength := BaseKnockbackStrength
	if isStrong && isSprinting {
		kbStrength += SprintKnockbackBonus
		session.SetSprinting(false)
	}

	s.RecordAttackExhaustion(session)

	if target.TypeName == MobTypeWither {
		return s.DamageWither(target.ID, damage, session, false)
	}
	if target.TypeName == MobTypeEnderDragon {
		return s.DamageEnderDragon(target.ID, damage, session, false)
	}
	if target.TypeName == MobTypeWarden {
		return s.DamageWarden(target.ID, damage, session)
	}
	if canonicalResourceName(target.TypeName) == MobTypeAllay && s.IsAllayImmuneToAttacker(target.ID, session) {
		return 0, false
	}
	if canonicalResourceName(target.TypeName) == MobTypeArmadillo && s.IsArmadilloRolledUp(target.ID) {
		damage = ReduceArmadilloRolledDamage(damage)
		kbStrength = 0
		if damage <= 0 {
			return 0, false
		}
	}

	killed = s.applyDamageToMob(target, damage, session, kbStrength, isCrit, isStrong)
	return damage, killed
}

func (s *Server) applyDamageToMob(
	target *world.Entity,
	damage float32,
	attacker *PlayerSession,
	kbStrength float64,
	isCrit bool,
	isStrong bool,
) (killed bool) {
	if target == nil || damage <= 0 {
		return false
	}
	if canonicalResourceName(target.TypeName) == MobTypeWarden {
		_, killed = s.DamageWarden(target.ID, damage, attacker)
		return killed
	}
	if canonicalResourceName(target.TypeName) == MobTypeHorse {
		damage = s.ReduceHorseDamageByArmor(target.ID, damage)
		if damage <= 0 {
			return false
		}
	}

	var (
		attackerID  int32
		attackerX   = target.X
		attackerY   = target.Y
		attackerZ   = target.Z
		attackerYaw float32
	)
	if attacker != nil {
		snap := attacker.Snapshot()
		attackerID = snap.EntityID
		attackerX = snap.X
		attackerY = snap.Y
		attackerZ = snap.Z
		attackerYaw = snap.Yaw
	}

	vx, vy, vz := ComputeKnockback(attackerX, attackerZ, attackerYaw, target.X, target.Z, kbStrength)
	velX := clampVelocityShort(vx)
	velY := clampVelocityShort(vy)
	velZ := clampVelocityShort(vz)

	tr := s.entityTracker()
	tr.mu.Lock()
	if tr.mobStates == nil {
		tr.mobStates = make(map[int32]*mobMemory)
	}
	mem := tr.mobStates[target.ID]
	if mem == nil {
		mem = &mobMemory{seenTick: tr.aiTickID}
		tr.mobStates[target.ID] = mem
	}
	if !mem.healthInit {
		mem.healthInit = true
		if IsEquineOrLlamaMob(target.TypeName) {
			mem.health = s.EquineMaxHealth(target.ID)
		} else {
			mem.health = MobMaxHealth(target.TypeName)
		}
	}
	wasBaby := mem.ageTicks < 0
	mem.health -= damage
	if mem.health <= 0 {
		mem.health = 0
		killed = true
		delete(tr.mobStates, target.ID)
	}
	tr.mu.Unlock()

	dim := target.EffectiveDimension()
	chunkPos := target.ChunkPos()
	hurtYaw := yawToward(attackerX-target.X, attackerZ-target.Z)

	// Broadcast player attack sound + hurt animation + damage event.
	attackSound := "minecraft:entity.player.attack.weak"
	if isCrit {
		attackSound = "minecraft:entity.player.attack.crit"
	} else if kbStrength > BaseKnockbackStrength {
		attackSound = "minecraft:entity.player.attack.knockback"
	} else if isStrong {
		attackSound = "minecraft:entity.player.attack.strong"
	}
	s.BroadcastChunk(chunkPos.X, chunkPos.Z, PlaySoundPacketID,
		EncodeSoundPacket(attackSound, SoundSourcePlayers, attackerX, attackerY, attackerZ, 1.0, 1.0, 0))
	s.BroadcastChunk(chunkPos.X, chunkPos.Z, protocol.PlayHurtAnimation,
		EncodeHurtAnimation(target.ID, hurtYaw))
	s.BroadcastChunk(chunkPos.X, chunkPos.Z, protocol.PlayDamageEvent,
		EncodeDamageEventPacket(target.ID, attackerID))
	if isCrit {
		s.BroadcastChunk(chunkPos.X, chunkPos.Z, PlayAnimateCB,
			EncodeAnimatePacket(target.ID, AnimateCriticalHit))
	}

	if !killed {
		s.OnEntityDamagedWithEffects(target.ID, false)
		normTarget := canonicalResourceName(target.TypeName)
		if normTarget == MobTypeArmadillo {
			s.OnArmadilloDamaged(target.ID)
		} else if normTarget == MobTypeCamel {
			s.OnCamelDamaged(target.ID)
		} else if normTarget == MobTypeAxolotl {
			s.OnAxolotlDamaged(target.ID, damage, nil)
		} else if normTarget == MobTypeLlama || normTarget == MobTypeTraderLlama {
			s.OnLlamaDamaged(target.ID, attacker, 0)
		} else if normTarget == MobTypeWanderingTrader {
			s.OnWanderingTraderDamaged(target.ID, attacker, 0)
		}
		s.EmitVibration(VibrationEvent{
			Dimension:      dim,
			Type:           GameEventEntityDamage,
			X:              target.X,
			Y:              target.Y,
			Z:              target.Z,
			Player:         attacker,
			SourceEntityID: target.ID,
		})
		if attacker != nil {
			if normTarget == MobTypeWolf {
				s.ProvokeWildWolf(target.ID, attacker)
			}
			if normTarget == MobTypeBee {
				s.AngerBeesNear(dim, target.X, target.Y, target.Z, BeeSwarmAlertRadius, attacker, nil)
			}
			if normTarget == MobTypeVillager {
				s.OnVillagerHurtByPlayer(target.ID, attacker)
			}
			s.NotifyOwnerAttackedTarget(attacker, target.ID)
		}
		// Apply horizontal knockback step if walkable, and set velocity on entity.
		curFeetY := int(math.Floor(target.Y + 0.01))
		nextX := target.X + vx
		nextY := target.Y
		nextZ := target.Z + vz
		if s.chunks != nil {
			if nx, ny, nz, ok := tryApplyHorizontalStep(s.chunks, target.X, curFeetY, target.Z, nextX, nextZ); ok {
				nextX, nextY, nextZ = nx, float64(ny), nz
			} else {
				nextX, nextZ = target.X, target.Z
			}
		}
		s.entities.Update(target.ID, func(e *world.Entity) {
			e.X = nextX
			e.Y = nextY
			e.Z = nextZ
			e.VelocityX = velX
			e.VelocityY = velY
			e.VelocityZ = velZ
		})
		s.BroadcastChunk(chunkPos.X, chunkPos.Z, protocol.PlaySetEntityMotion,
			EncodeSetEntityMotionPacket(target.ID, vx, vy, vz))
		hurtSnd, hurtSrc := mobHurtSound(target.TypeName)
		s.BroadcastChunk(chunkPos.X, chunkPos.Z, PlaySoundPacketID,
			EncodeSoundPacket(hurtSnd, hurtSrc, nextX, nextY, nextZ, 1.0, 1.0, 0))
		return false
	}

	// Mob died: broadcast death event + sound, trigger death status effects, notify raid manager, remove entity, and spawn loot drops (adults only).
	deathX, deathY, deathZ := target.X, target.Y, target.Z
	deathSnd, deathSrc := mobDeathSound(target.TypeName)
	s.BroadcastChunk(chunkPos.X, chunkPos.Z, PlaySoundPacketID,
		EncodeSoundPacket(deathSnd, deathSrc, target.X, target.Y, target.Z, 1.0, 1.0, 0))
	s.BroadcastChunk(chunkPos.X, chunkPos.Z, protocol.PlayEntityEvent,
		EncodeEntityEventPacket(target.ID, EntityEventDeath))
	s.OnEntityDiedWithEffects(target.ID)
	s.OnRaiderKilled(target.ID, attacker)
	_ = s.DismountEntityFromVehicle(target.ID)
	if canonicalResourceName(target.TypeName) == MobTypeBee {
		s.RemoveBeeState(target.ID)
		if attacker != nil {
			s.AngerBeesNear(dim, deathX, deathY, deathZ, BeeSwarmAlertRadius, attacker, nil)
		}
	}
	if canonicalResourceName(target.TypeName) == MobTypeWanderingTrader && attacker != nil {
		s.OnWanderingTraderDamaged(target.ID, attacker, 0)
	}
	if attacker != nil && canonicalResourceName(target.TypeName) == MobTypeVillager {
		s.OnVillagerKilledByPlayer(target.ID, dim, deathX, deathY, deathZ, attacker)
	}
	eqDrops := s.OnEquineOrLlamaKilled(target.ID)
	for _, eqDrop := range eqDrops {
		s.SpawnItemEntityInDimension(dim, deathX, deathY+0.25, deathZ, eqDrop)
	}
	s.entities.Remove(target.ID)
	s.BroadcastChunk(chunkPos.X, chunkPos.Z, protocol.PlayRemoveEntities,
		encodeRemoveEntitiesPacket(nil, []int32{target.ID}))

	xpReward := MobXPReward(target.TypeName, wasBaby)
	consumed := false
	if xpReward > 0 {
		consumed = s.TriggerSculkCatalystOnMobDeath(dim, target.X, target.Y, target.Z, xpReward, attacker)
	}
	if !consumed && xpReward > 0 && attacker != nil {
		s.SpawnExperienceOrbsInDimension(dim, target.X, target.Y+0.25, target.Z, int32(xpReward))
	}
	s.EmitVibration(VibrationEvent{
		Dimension:      dim,
		Type:           GameEventEntityDie,
		X:              target.X,
		Y:              target.Y,
		Z:              target.Z,
		Player:         attacker,
		SourceEntityID: target.ID,
	})

	if !wasBaby {
		drops := MobLootDrops(target.TypeName, nil)
		for _, drop := range drops {
			s.SpawnItemEntity(target.X, target.Y+0.25, target.Z, drop)
		}
	}
	if attacker != nil {
		s.TriggerEntityKilled(attacker, target.TypeName)
		s.IncrementCriteriaObjectives(attacker.Profile.Name, "totalKillCount", 1)
	}
	return true
}

func clampVelocityShort(v float64) int16 {
	scaled := math.Round(v * 8000.0)
	if scaled > math.MaxInt16 {
		return math.MaxInt16
	}
	if scaled < math.MinInt16 {
		return math.MinInt16
	}
	return int16(scaled)
}

// InteractPacket represents a decoded serverbound interact (0x1a) or attack (0x01) packet.
type InteractPacket struct {
	EntityID             int32
	Action               int32
	Hand                 int32
	TargetX              float32
	TargetY              float32
	TargetZ              float32
	UsingSecondaryAction bool
}

// DecodeInteractPacket decodes either a 26.1.2 ServerboundAttackPacket (0x01:
// VarInt(entityID)), a classic ServerboundInteractPacket (VarInt(entityID),
// VarInt(action), ...), or a 26.1.2 ServerboundInteractPacket (0x1a).
func DecodeInteractPacket(r *protocol.Reader) (InteractPacket, error) {
	entityID, err := r.VarInt()
	if err != nil {
		return InteractPacket{}, err
	}
	// 26.1.2 ServerboundAttackPacket (0x01) contains only the target entity ID.
	if r.Remaining() == 0 {
		return InteractPacket{
			EntityID: entityID,
			Action:   InteractActionAttack,
		}, nil
	}

	actionOrHand, err := r.VarInt()
	if err != nil {
		return InteractPacket{}, err
	}

	// Classic interact packet with action == ATTACK (1) followed by optional sneaking bool.
	if actionOrHand == InteractActionAttack && r.Remaining() <= 1 {
		var sneaking bool
		if r.Remaining() == 1 {
			sneaking, _ = r.Bool()
		}
		return InteractPacket{
			EntityID:             entityID,
			Action:               InteractActionAttack,
			UsingSecondaryAction: sneaking,
		}, nil
	}

	// Classic interact_at (action == 2) with 3 float32 coordinates + hand + sneaking.
	if actionOrHand == InteractActionInteractAt && r.Remaining() >= 13 {
		tx, err := r.Float32()
		if err != nil {
			return InteractPacket{}, err
		}
		ty, err := r.Float32()
		if err != nil {
			return InteractPacket{}, err
		}
		tz, err := r.Float32()
		if err != nil {
			return InteractPacket{}, err
		}
		hand, err := r.VarInt()
		if err != nil {
			return InteractPacket{}, err
		}
		var sneaking bool
		if r.Remaining() > 0 {
			sneaking, _ = r.Bool()
		}
		return InteractPacket{
			EntityID:             entityID,
			Action:               InteractActionInteractAt,
			Hand:                 hand,
			TargetX:              tx,
			TargetY:              ty,
			TargetZ:              tz,
			UsingSecondaryAction: sneaking,
		}, nil
	}

	// Classic interact (action == 0): VarInt(hand) + Bool(sneaking).
	if actionOrHand == InteractActionInteract && r.Remaining() <= 2 {
		var hand int32
		if r.Remaining() > 1 {
			hand, _ = r.VarInt()
		}
		var sneaking bool
		if r.Remaining() > 0 {
			sneaking, _ = r.Bool()
		}
		return InteractPacket{
			EntityID:             entityID,
			Action:               InteractActionInteract,
			Hand:                 hand,
			UsingSecondaryAction: sneaking,
		}, nil
	}

	// Drain any remaining bytes from 26.1.2 LPVec3 interact location + bool.
	var sneaking bool
	for r.Remaining() > 0 {
		b, _ := r.ReadByte()
		sneaking = b != 0
	}
	return InteractPacket{
		EntityID:             entityID,
		Action:               InteractActionInteract,
		Hand:                 actionOrHand,
		UsingSecondaryAction: sneaking,
	}, nil
}

// EncodeInteractAttackPacket encodes a serverbound interact (attack) packet body:
// VarInt(entityID), VarInt(InteractActionAttack=1), Bool(sneaking).
func EncodeInteractAttackPacket(entityID int32, sneaking bool) []byte {
	w := protocol.NewWriter(12)
	w.VarInt(entityID)
	w.VarInt(InteractActionAttack)
	w.Bool(sneaking)
	return w.Bytes()
}

// EncodeAttackPacket encodes a 26.1.2 serverbound attack (0x01) packet body:
// VarInt(entityID).
func EncodeAttackPacket(entityID int32) []byte {
	w := protocol.NewWriter(8)
	w.VarInt(entityID)
	return w.Bytes()
}

// HandleInteractPacket decodes a serverbound interact or attack packet and
// executes a melee attack when Action == InteractActionAttack, or right-click
// entity interaction (e.g. opening villager trading UI, rotating/inserting
// item frames, or equipping armor stands) when interacting with the main hand.
func (s *Server) HandleInteractPacket(session *PlayerSession, r *protocol.Reader) error {
	pkt, err := DecodeInteractPacket(r)
	if err != nil {
		return err
	}
	if s != nil && session != nil {
		if pkt.Action == InteractActionAttack {
			s.AttackEntity(session, pkt.EntityID)
		} else {
			if pkt.Action == InteractActionInteractAt {
				s.recordInteractAtY(session, pkt.EntityID, float64(pkt.TargetY))
			}
			if pkt.Hand == 0 {
				_, _, _ = s.InteractWithEntity(session, pkt.EntityID)
			}
		}
	}
	return nil
}

// HandleAttackPacket decodes a 26.1.2 serverbound attack (0x01) packet and
// executes a melee attack against the target entity.
func (s *Server) HandleAttackPacket(session *PlayerSession, r *protocol.Reader) error {
	entityID, err := r.VarInt()
	if err != nil {
		return err
	}
	if s != nil && session != nil {
		s.AttackEntity(session, entityID)
	}
	return nil
}

// EncodeEntityEventPacket encodes a clientbound entity_event (0x22) packet body:
// Int32(entityID), Byte(eventID).
func EncodeEntityEventPacket(entityID int32, eventID byte) []byte {
	w := protocol.NewWriter(8)
	w.Int32(entityID)
	w.Byte(eventID)
	return w.Bytes()
}

// EncodeDamageEventPacket encodes a clientbound damage_event (0x19) packet body
// for protocol 775.
func EncodeDamageEventPacket(entityID, attackerEntityID int32) []byte {
	damageTypeID := registry.Index("minecraft:damage_type", "minecraft:player_attack")
	if damageTypeID < 0 {
		damageTypeID = 0
	}
	var causeID int32
	if attackerEntityID > 0 {
		causeID = attackerEntityID + 1 // OptionalEntityId: entityID + 1 (0 = absent)
	}
	w := protocol.NewWriter(20)
	w.VarInt(entityID)
	w.VarInt(int32(damageTypeID))
	w.VarInt(causeID)
	w.VarInt(causeID)
	w.Bool(false) // no explicit source position Vec3
	return w.Bytes()
}

// EncodeAnimatePacket encodes a clientbound animate (0x02) packet body:
// VarInt(entityID), Byte(action).
func EncodeAnimatePacket(entityID int32, action byte) []byte {
	w := protocol.NewWriter(8)
	w.VarInt(entityID)
	w.Byte(action)
	return w.Bytes()
}

// EncodeSetEntityMotionPacket encodes a clientbound set_entity_motion (0x65)
// packet body in protocol 775 (VarInt entityID + LPVec3 velocity).
func EncodeSetEntityMotionPacket(entityID int32, vx, vy, vz float64) []byte {
	w := protocol.NewWriter(16)
	w.VarInt(entityID)
	w.LPVec3(vx, vy, vz)
	return w.Bytes()
}

// EncodeSoundPacket encodes a clientbound sound (0x75) packet body for protocol
// 775 using an inline Holder<SoundEvent> (id=0 + resource identifier + no fixed range).
func EncodeSoundPacket(soundName string, source int32, x, y, z float64, volume, pitch float32, seed int64) []byte {
	soundName = normalizeFoodItemID(soundName)
	w := protocol.NewWriter(len(soundName) + 36)
	w.VarInt(0) // inline Holder<SoundEvent>
	w.String(soundName)
	w.Bool(false) //fixedRange = Optional.empty()
	w.VarInt(source)
	w.Int32(int32(x * 8.0))
	w.Int32(int32(y * 8.0))
	w.Int32(int32(z * 8.0))
	w.Float32(volume)
	w.Float32(pitch)
	w.Int64(seed)
	return w.Bytes()
}

// IsStandardProjectileEntityType reports whether typeName is a standard thrown
// or shot projectile entity (`arrow`, `spectral_arrow`, `trident`, `snowball`,
// `egg`, `ender_pearl`, `fireball`, `small_fireball`).
func IsStandardProjectileEntityType(typeName string) bool {
	switch canonicalResourceName(typeName) {
	case "minecraft:arrow", "minecraft:spectral_arrow", "minecraft:trident",
		"minecraft:snowball", "minecraft:egg", "minecraft:ender_pearl",
		"minecraft:fireball", "minecraft:small_fireball":
		return true
	default:
		return false
	}
}

func projectileImpactBlockFace(prevX, prevY, prevZ, hitX, hitY, hitZ float64, bx, by, bz int) string {
	prevBX := int(math.Floor(prevX))
	prevBY := int(math.Floor(prevY))
	prevBZ := int(math.Floor(prevZ))
	if prevBY > by {
		return "up"
	}
	if prevBY < by {
		return "down"
	}
	if prevBX < bx {
		return "west"
	}
	if prevBX > bx {
		return "east"
	}
	if prevBZ < bz {
		return "north"
	}
	if prevBZ > bz {
		return "south"
	}
	fx := hitX - float64(bx)
	fy := hitY - float64(by)
	fz := hitZ - float64(bz)
	dx := math.Abs(fx - 0.5)
	dy := math.Abs(fy - 0.5)
	dz := math.Abs(fz - 0.5)
	if dy >= dx && dy >= dz {
		if fy >= 0.5 {
			return "up"
		}
		return "down"
	}
	if dx >= dz {
		if fx >= 0.5 {
			return "east"
		}
		return "west"
	}
	if fz >= 0.5 {
		return "south"
	}
	return "north"
}

// OnProjectileBlockImpact handles any projectile striking block (bx, by, bz) in
// dim at impact coordinates (hitX, hitY, hitZ) from (prevX, prevY, prevZ):
//   - Shatters `minecraft:decorated_pot` via HitDecoratedPotWithProjectile
//   - Powers `minecraft:target` (1..15) via HitTargetBlockWithProjectile
func (s *Server) OnProjectileBlockImpact(
	dim string,
	bx, by, bz int,
	prevX, prevY, prevZ float64,
	hitX, hitY, hitZ float64,
	projectileEntityID int32,
	projectileType string,
) (shatteredPot bool, targetPower int) {
	if s == nil {
		return false, 0
	}
	normDim := world.NormalizeDimension(dim)
	cache := s.DimensionChunks(normDim)
	if cache == nil || by < world.MinY || by >= world.MinY+world.WorldHeight {
		return false, 0
	}
	blk := cache.GetBlock(bx, by, bz)
	if IsDecoratedPotBlock(blk) {
		shattered, _ := s.HitDecoratedPotWithProjectile(normDim, bx, by, bz, projectileEntityID)
		return shattered, 0
	}
	if world.BlockName(blk) == "minecraft:target" {
		face := projectileImpactBlockFace(prevX, prevY, prevZ, hitX, hitY, hitZ, bx, by, bz)
		power, _ := s.HitTargetBlockWithProjectile(normDim, bx, by, bz, face, hitX, hitY, hitZ, projectileType)
		return false, power
	}
	return false, 0
}

