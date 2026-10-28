package server

import (
	"errors"
	"math"
	"math/rand"
	"strings"
	"sync"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

// Piglin, Piglin Brute, Zombified Piglin, Hoglin, Zoglin, and Strider entity types,
// registry IDs, health values, zombification timers, bartering constants, and
// SynchedEntityData indices for Minecraft 26.1.2 (protocol 775).
const (
	MobTypePiglin          = "minecraft:piglin"
	MobTypePiglinBrute     = "minecraft:piglin_brute"
	MobTypeZombifiedPiglin = "minecraft:zombified_piglin"
	MobTypeHoglin          = "minecraft:hoglin"
	MobTypeZoglin          = "minecraft:zoglin"
	MobTypeStrider         = "minecraft:strider"

	// BuiltInRegistries.ENTITY_TYPE numeric network IDs in Minecraft 26.1.2 (protocol 775).
	EntityTypeHoglinID          int = 64
	EntityTypePiglinID          int = 101
	EntityTypePiglinBruteID     int = 102
	EntityTypeStriderID         int = 129
	EntityTypeZoglinID          int = 149
	EntityTypeZombifiedPiglinID int = 154

	// Default MaxHealth values in vanilla 26.1.2.
	DefaultPiglinMaxHealth          float32 = 16.0
	DefaultPiglinBruteMaxHealth     float32 = 50.0
	DefaultZombifiedPiglinMaxHealth float32 = 20.0
	DefaultHoglinMaxHealth          float32 = 40.0
	DefaultZoglinMaxHealth          float32 = 40.0
	DefaultStriderMaxHealth         float32 = 20.0

	// Zombification, Bartering, Hunting & Anger timers (ticks at 20 TPS).
	PiglinZombificationTicks       = 300  // 15 seconds outside the Nether
	HoglinZombificationTicks       = 300  // 15 seconds outside the Nether
	PiglinBarterAdmireTicks        = 120  // 6 seconds admiring a gold ingot before throwing barter loot
	PiglinAngerDurationTicks       = 600  // 30 seconds anger when gold is mined / chest opened / attacked
	PiglinHuntCooldownTicks        = 1200 // 60 seconds cooldown between hoglin hunts
	PiglinCrossbowChargeTicks      = 25   // 1.25s crossbow draw time
	PiglinInventorySlots           = 8    // 8-slot pocket inventory for picked-up loved items
	ZombifiedPiglinAngerMinTicks   = 400  // 20 seconds minimum anger duration
	ZombifiedPiglinAngerMaxTicks   = 800  // 40 seconds maximum anger duration
	ZombifiedPiglinAlertRange      = 32.0 // 32-block group anger alert radius
	ZombifiedPiglinAttackDamage    = float32(8.0)
	ZombifiedPiglinChaseSpeed      = 0.16

	// Movement speeds & combat parameters.
	PiglinWanderSpeed      = 0.10
	PiglinChaseSpeed       = 0.16
	PiglinAttackDamage     = float32(5.0)
	PiglinBruteSpeed       = 0.17
	PiglinBruteAttackDmg   = float32(7.0)
	PiglinAggroRange       = 16.0
	PiglinGoldSearchRange  = 16.0
	PiglinRepellentRadius  = 8
	PiglinCelebrateTicks   = 60

	HoglinWanderSpeed     = 0.09
	HoglinChaseSpeed      = 0.16
	HoglinRetreatSpeed    = 0.18
	HoglinAttackDamage    = float32(6.0)
	HoglinBabyAttackDmg   = float32(0.5)
	HoglinFlingVelocityY  = 0.40
	HoglinRepellentRadius = 7

	ZoglinChaseSpeed   = 0.17
	ZoglinAttackDamage = float32(6.0)

	StriderLandSpeed                = 0.09
	StriderLavaSpeed                = 0.175
	StriderBoostSpeedMult           = 1.55
	StriderMinBoostTicks            = 140
	StriderMaxBoostTicks            = 420
	StriderWaterDamage              = float32(1.0)
	StriderWaterDamageIntervalTicks = 20
	WarpedFungusOnAStickMaxDamage   = 100

	// XP rewards on death (vanilla 26.1.2).
	PiglinAdultXPReward      = 5
	PiglinBabyXPReward       = 1
	PiglinBruteXPReward      = 20
	ZombifiedPiglinXPReward  = 5
	HoglinXPReward           = 5
	ZoglinXPReward           = 5
	StriderXPReward          = 2

	// SynchedEntityData indices for AbstractPiglin / Piglin / PiglinBrute in 26.1.2 (protocol 775).
	EntityDataPiglinImmuneZombificationID byte = 16 // AbstractPiglin.DATA_IMMUNE_TO_ZOMBIFICATION (BOOLEAN serializer 8)
	EntityDataPiglinIsBabyID              byte = 17 // Piglin.DATA_BABY_ID (BOOLEAN serializer 8)
	EntityDataPiglinChargingCrossbowID    byte = 18 // Piglin.DATA_IS_CHARGING_CROSSBOW (BOOLEAN serializer 8)
	EntityDataPiglinIsDancingID           byte = 19 // Piglin.DATA_IS_DANCING (BOOLEAN serializer 8)

	// SynchedEntityData index for Hoglin in 26.1.2 (protocol 775).
	EntityDataHoglinImmuneZombificationID byte = 17 // Hoglin.DATA_IMMUNE_TO_ZOMBIFICATION (BOOLEAN serializer 8)

	// SynchedEntityData index for Zoglin in 26.1.2 (protocol 775).
	EntityDataZoglinIsBabyID byte = 16 // Zoglin.DATA_BABY_ID (BOOLEAN serializer 8)

	// SynchedEntityData indices for Strider in 26.1.2 (protocol 775).
	EntityDataStriderBoostTimeID   byte = 17 // Strider.DATA_BOOST_TIME (INT serializer 1)
	EntityDataStriderSuffocatingID byte = 18 // Strider.DATA_SUFFOCATING (BOOLEAN serializer 8 — shivering outside lava)
	EntityDataStriderSaddleID      byte = 19 // Strider.DATA_SADDLE_ID (BOOLEAN serializer 8)

	// Total weight of vanilla 26.1.2 gameplay/piglin_bartering loot table.
	PiglinBarterTotalWeight = 459

	// Canonical sound effects for Piglin, Piglin Brute, Hoglin, Zoglin, and Strider.
	SoundPiglinAdmiringItem           = "minecraft:entity.piglin.admiring_item"
	SoundPiglinAngry                  = "minecraft:entity.piglin.angry"
	SoundPiglinCelebrate              = "minecraft:entity.piglin.celebrate"
	SoundPiglinConvertedToZombified   = "minecraft:entity.piglin.converted_to_zombified"
	SoundPiglinHurt                   = "minecraft:entity.piglin.hurt"
	SoundPiglinDeath                  = "minecraft:entity.piglin.death"
	SoundPiglinRetreat                = "minecraft:entity.piglin.retreat"
	SoundPiglinBruteAngry             = "minecraft:entity.piglin_brute.angry"
	SoundPiglinBruteConverted         = "minecraft:entity.piglin_brute.converted_to_zombified"
	SoundPiglinBruteHurt              = "minecraft:entity.piglin_brute.hurt"
	SoundPiglinBruteDeath             = "minecraft:entity.piglin_brute.death"
	SoundZombifiedPiglinAngry         = "minecraft:entity.zombified_piglin.angry"
	SoundZombifiedPiglinHurt          = "minecraft:entity.zombified_piglin.hurt"
	SoundZombifiedPiglinDeath         = "minecraft:entity.zombified_piglin.death"
	SoundHoglinAttack                 = "minecraft:entity.hoglin.attack"
	SoundHoglinConvertedToZombified   = "minecraft:entity.hoglin.converted_to_zombified"
	SoundHoglinHurt                   = "minecraft:entity.hoglin.hurt"
	SoundHoglinDeath                  = "minecraft:entity.hoglin.death"
	SoundHoglinRetreat                = "minecraft:entity.hoglin.retreat"
	SoundZoglinAttack                 = "minecraft:entity.zoglin.attack"
	SoundZoglinHurt                   = "minecraft:entity.zoglin.hurt"
	SoundZoglinDeath                  = "minecraft:entity.zoglin.death"
	SoundStriderAmbient               = "minecraft:entity.strider.ambient"
	SoundStriderHappy                 = "minecraft:entity.strider.happy"
	SoundStriderRetreat               = "minecraft:entity.strider.retreat"
	SoundStriderSaddle                = "minecraft:entity.strider.saddle"
	SoundStriderEat                   = "minecraft:entity.strider.eat"
	SoundStriderHurt                  = "minecraft:entity.strider.hurt"
	SoundStriderDeath                 = "minecraft:entity.strider.death"
)

// PiglinBarterEntry describes one weighted entry in the vanilla 26.1.2
// `gameplay/piglin_bartering` loot table (total weight = 459 across 18 entries).
type PiglinBarterEntry struct {
	ItemID        string
	Weight        int
	MinCount      int
	MaxCount      int
	SoulSpeedBook bool
	SoulSpeedBoot bool
	FireResPotion bool
	SplashFireRes bool
	WaterBottle   bool
}

var vanillaPiglinBarterTable = []PiglinBarterEntry{
	{ItemID: "minecraft:enchanted_book", Weight: 5, MinCount: 1, MaxCount: 1, SoulSpeedBook: true},
	{ItemID: "minecraft:iron_boots", Weight: 8, MinCount: 1, MaxCount: 1, SoulSpeedBoot: true},
	{ItemID: "minecraft:potion", Weight: 8, MinCount: 1, MaxCount: 1, FireResPotion: true},
	{ItemID: "minecraft:splash_potion", Weight: 8, MinCount: 1, MaxCount: 1, SplashFireRes: true},
	{ItemID: "minecraft:potion", Weight: 10, MinCount: 1, MaxCount: 1, WaterBottle: true},
	{ItemID: "minecraft:iron_nugget", Weight: 10, MinCount: 10, MaxCount: 36},
	{ItemID: "minecraft:ender_pearl", Weight: 10, MinCount: 2, MaxCount: 4},
	{ItemID: "minecraft:string", Weight: 20, MinCount: 3, MaxCount: 9},
	{ItemID: "minecraft:quartz", Weight: 20, MinCount: 5, MaxCount: 12},
	{ItemID: "minecraft:obsidian", Weight: 40, MinCount: 1, MaxCount: 1},
	{ItemID: "minecraft:crying_obsidian", Weight: 40, MinCount: 1, MaxCount: 3},
	{ItemID: "minecraft:fire_charge", Weight: 40, MinCount: 1, MaxCount: 1},
	{ItemID: "minecraft:leather", Weight: 40, MinCount: 2, MaxCount: 4},
	{ItemID: "minecraft:soul_sand", Weight: 40, MinCount: 2, MaxCount: 8},
	{ItemID: "minecraft:nether_brick", Weight: 40, MinCount: 2, MaxCount: 8},
	{ItemID: "minecraft:spectral_arrow", Weight: 40, MinCount: 6, MaxCount: 12},
	{ItemID: "minecraft:gravel", Weight: 40, MinCount: 8, MaxCount: 16},
	{ItemID: "minecraft:blackstone", Weight: 40, MinCount: 8, MaxCount: 16},
}

// PiglinBarterLootTable returns a copy of the 18-entry vanilla 26.1.2
// `gameplay/piglin_bartering` loot table (total weight 459).
func PiglinBarterLootTable() []PiglinBarterEntry {
	out := make([]PiglinBarterEntry, len(vanillaPiglinBarterTable))
	copy(out, vanillaPiglinBarterTable)
	return out
}

// RollPiglinBarterLootByWeightRoll selects and rolls the bartered item stack and
// ItemMeta for a deterministic weight roll in `[0, PiglinBarterTotalWeight)`.
func RollPiglinBarterLootByWeightRoll(weightRoll int, rng *rand.Rand) (stack world.ItemStack, meta ItemMeta, entry PiglinBarterEntry) {
	if weightRoll < 0 {
		weightRoll = 0
	}
	weightRoll %= PiglinBarterTotalWeight
	acc := 0
	chosen := vanillaPiglinBarterTable[len(vanillaPiglinBarterTable)-1]
	for _, e := range vanillaPiglinBarterTable {
		acc += e.Weight
		if weightRoll < acc {
			chosen = e
			break
		}
	}
	count := chosen.MinCount
	if chosen.MaxCount > chosen.MinCount && rng != nil {
		count += rng.Intn(chosen.MaxCount - chosen.MinCount + 1)
	}
	stack = world.NewNamedItemStack(chosen.ItemID, int32(count))
	if chosen.SoulSpeedBook || chosen.SoulSpeedBoot {
		lvl := 1
		if rng != nil {
			lvl = 1 + rng.Intn(3) // Soul Speed I..III
		}
		meta.SetEnchantment(EnchantSoulSpeed, lvl)
	}
	return stack, meta, chosen
}

// RollPiglinBarterLoot rolls one random outcome from the vanilla 26.1.2
// `gameplay/piglin_bartering` loot table.
func RollPiglinBarterLoot(rng *rand.Rand) (stack world.ItemStack, meta ItemMeta, entry PiglinBarterEntry) {
	roll := 0
	if rng != nil {
		roll = rng.Intn(PiglinBarterTotalWeight)
	}
	return RollPiglinBarterLootByWeightRoll(roll, rng)
}

// PiglinHoglinStriderXPReward returns the vanilla 26.1.2 experience reward when
// a Piglin (5 adult / 1 baby), Piglin Brute (20), Zombified Piglin (5),
// Hoglin (5), Zoglin (5), or Strider (2) dies.
func PiglinHoglinStriderXPReward(typeName string, isBaby bool) int {
	norm := canonicalResourceName(typeName)
	if isBaby {
		if norm == MobTypePiglin {
			return PiglinBabyXPReward
		}
		return 0
	}
	switch norm {
	case MobTypePiglinBrute:
		return PiglinBruteXPReward
	case MobTypePiglin:
		return PiglinAdultXPReward
	case MobTypeZombifiedPiglin:
		return ZombifiedPiglinXPReward
	case MobTypeHoglin:
		return HoglinXPReward
	case MobTypeZoglin:
		return ZoglinXPReward
	case MobTypeStrider:
		return StriderXPReward
	default:
		return 0
	}
}

// PiglinState holds per-entity AI, zombification, bartering, hunting, and combat
// state for a `minecraft:piglin` or `minecraft:piglin_brute`.
type PiglinState struct {
	EntityID              int32
	TypeName              string
	Dimension             string
	Baby                  bool
	ImmuneToZombification bool
	TimeInOverworld       int
	ChargingCrossbow      bool
	CrossbowChargeTicks   int
	Dancing               bool
	DanceTicks            int
	MainHandItem          world.ItemStack
	OffHandItem           world.ItemStack
	Inventory             []world.ItemStack
	AdmireTicks           int
	Bartering             bool
	BarterPlayer          *PlayerSession
	AngerTarget           *PlayerSession
	AngerMobID            int32
	AngerTicks            int
	AttackCooldown        int
	HuntCooldownTicks     int
	CannotHunt            bool
	Retreating            bool
	MountEntityID         int32
	HasHome               bool
	HomeX, HomeY, HomeZ   float64
}

// ZombifiedPiglinState holds per-entity group-anger and equipment state for a
// `minecraft:zombified_piglin`.
type ZombifiedPiglinState struct {
	EntityID       int32
	Dimension      string
	Baby           bool
	MainHandItem   world.ItemStack
	AngerTarget    *PlayerSession
	AngerMobID     int32
	AngerTicks     int
	AttackCooldown int
}

// HoglinState holds per-entity AI, zombification, panic, and combat state for a
// `minecraft:hoglin` or `minecraft:zoglin`.
type HoglinState struct {
	EntityID              int32
	TypeName              string
	Dimension             string
	Baby                  bool
	ImmuneToZombification bool
	TimeInOverworld       int
	CannotBeHunted        bool
	Panicking             bool
	RepellentX            float64
	RepellentZ            float64
	TargetPlayer          *PlayerSession
	TargetMobID           int32
	AttackCooldown        int
	RiderEntityIDs        []int32
}

// StriderState holds per-entity lava-walking, shivering, saddle, water-damage,
// and fungus-boost state for a `minecraft:strider`.
type StriderState struct {
	EntityID         int32
	Dimension        string
	Saddled          bool
	Shivering        bool
	BoostTicks       int32
	TotalBoostTime   int32
	RiderEntityID    int32
	WaterDamageTimer int
}

type netherMobManager struct {
	mu               sync.RWMutex
	piglins          map[int32]*PiglinState
	zombifiedPiglins map[int32]*ZombifiedPiglinState
	hoglins          map[int32]*HoglinState
	striders         map[int32]*StriderState
}

var (
	serverNetherMobManagers sync.Map // map[*Server]*netherMobManager
	piglinAdvancementsOnce  sync.Once
)

func init() {
	ensurePiglinAdvancementsRegistered()
}

func ensurePiglinAdvancementsRegistered() {
	piglinAdvancementsOnce.Do(func() {
		reg := DefaultAdvancementRegistry()
		if reg == nil {
			return
		}
		if _, ok := reg.byID["minecraft:nether/distract_piglin"]; !ok {
			cDistract, rDistract := allOfCriteria("distract_piglin")
			def := &AdvancementDef{
				ID:       "minecraft:nether/distract_piglin",
				ParentID: "minecraft:nether/root",
				Display: &AdvancementDisplay{
					Title:          "Oh Shiny",
					Description:    "Distract Piglins with gold",
					IconItem:       "minecraft:gold_ingot",
					Frame:          AdvancementFrameTask,
					ShowToast:      true,
					AnnounceToChat: true,
					X:              1,
					Y:              6,
				},
				Criteria:     cDistract,
				Requirements: rDistract,
			}
			reg.defs = append(reg.defs, def)
			reg.byID[def.ID] = def
			reg.children[def.ParentID] = append(reg.children[def.ParentID], def)
		}
	})
}

func (s *Server) netherMobs() *netherMobManager {
	if s == nil {
		return &netherMobManager{
			piglins:          make(map[int32]*PiglinState),
			zombifiedPiglins: make(map[int32]*ZombifiedPiglinState),
			hoglins:          make(map[int32]*HoglinState),
			striders:         make(map[int32]*StriderState),
		}
	}
	if v, ok := serverNetherMobManagers.Load(s); ok {
		return v.(*netherMobManager)
	}
	actual, _ := serverNetherMobManagers.LoadOrStore(s, &netherMobManager{
		piglins:          make(map[int32]*PiglinState),
		zombifiedPiglins: make(map[int32]*ZombifiedPiglinState),
		hoglins:          make(map[int32]*HoglinState),
		striders:         make(map[int32]*StriderState),
	})
	return actual.(*netherMobManager)
}

// --- Piglins, Piglin Brutes & Zombified Piglins ---

// IsGoldArmorItem reports whether itemID is a piece of golden player armor that
// pacifies standard piglins in vanilla 26.1.2.
func IsGoldArmorItem(itemID string) bool {
	switch canonicalResourceName(itemID) {
	case "minecraft:golden_helmet", "minecraft:golden_chestplate",
		"minecraft:golden_leggings", "minecraft:golden_boots":
		return true
	default:
		return false
	}
}

// IsPlayerWearingGoldArmor reports whether session is wearing at least one piece
// of golden armor in slots 5..8 (helmet, chestplate, leggings, boots).
func IsPlayerWearingGoldArmor(session *PlayerSession) bool {
	if session == nil {
		return false
	}
	for slot := 5; slot <= 8; slot++ {
		st := session.InventorySlot(slot)
		if !st.IsEmpty() && IsGoldArmorItem(st.ResolvedID()) {
			return true
		}
	}
	return false
}

// IsPiglinLovedGoldItem reports whether itemID belongs to `#minecraft:piglin_loved`
// (items that attract piglins and distract them on pickup).
func IsPiglinLovedGoldItem(itemID string) bool {
	switch canonicalResourceName(itemID) {
	case "minecraft:gold_ingot", "minecraft:gold_block", "minecraft:raw_gold",
		"minecraft:raw_gold_block", "minecraft:gold_nugget", "minecraft:gold_ore",
		"minecraft:deepslate_gold_ore", "minecraft:nether_gold_ore",
		"minecraft:gilded_blackstone", "minecraft:golden_apple",
		"minecraft:enchanted_golden_apple", "minecraft:golden_carrot",
		"minecraft:glistering_melon_slice", "minecraft:golden_helmet",
		"minecraft:golden_chestplate", "minecraft:golden_leggings",
		"minecraft:golden_boots", "minecraft:golden_sword",
		"minecraft:golden_pickaxe", "minecraft:golden_axe",
		"minecraft:golden_shovel", "minecraft:golden_hoe",
		"minecraft:golden_horse_armor", "minecraft:light_weighted_pressure_plate",
		"minecraft:bell", "minecraft:clock":
		return true
	default:
		return false
	}
}

// IsPiglinGuardedBlock reports whether blockName is a container or gold block
// guarded by Piglins in vanilla 26.1.2 (`#minecraft:guarded_by_piglins`).
func IsPiglinGuardedBlock(blockName string) bool {
	norm := canonicalResourceName(blockName)
	if strings.HasSuffix(norm, "shulker_box") || strings.Contains(norm, "copper_chest") {
		return true
	}
	switch norm {
	case "minecraft:chest", "minecraft:trapped_chest", "minecraft:ender_chest",
		"minecraft:barrel", "minecraft:gold_block", "minecraft:raw_gold_block",
		"minecraft:gold_ore", "minecraft:deepslate_gold_ore",
		"minecraft:nether_gold_ore", "minecraft:gilded_blackstone":
		return true
	default:
		return false
	}
}

// IsPiglinRepellentBlock reports whether blockName repels standard Piglins in
// vanilla 26.1.2 (`#minecraft:piglin_repellents`: `soul_fire`, `soul_torch`,
// `soul_wall_torch`, `soul_lantern`, `soul_campfire`).
func IsPiglinRepellentBlock(blockName string) bool {
	switch canonicalResourceName(blockName) {
	case "minecraft:soul_fire", "minecraft:soul_torch", "minecraft:soul_wall_torch",
		"minecraft:soul_lantern", "minecraft:soul_campfire":
		return true
	default:
		return false
	}
}

// HasPiglinRepellentNear checks whether any `#minecraft:piglin_repellents` block
// (`soul_fire`, `soul_torch`, `soul_lantern`, `soul_campfire`) or any
// `minecraft:zombified_piglin` / `minecraft:zoglin` entity is within `radius`
// (default 8) of (x, y, z) in dimension.
func (s *Server) HasPiglinRepellentNear(dimension string, x, y, z float64, radius int) (rx, ry, rz float64, found bool) {
	if s == nil {
		return 0, 0, 0, false
	}
	if radius <= 0 {
		radius = PiglinRepellentRadius
	}
	normDim := world.NormalizeDimension(dimension)
	cache := s.DimensionChunks(normDim)
	bestDistSq := float64(radius*radius) + 1.0

	if cache != nil {
		cx, cy, cz := int(math.Floor(x)), int(math.Floor(y)), int(math.Floor(z))
		for dy := -3; dy <= 3; dy++ {
			by := cy + dy
			if by < world.MinY || by >= world.MinY+world.WorldHeight {
				continue
			}
			for dx := -radius; dx <= radius; dx++ {
				bx := cx + dx
				for dz := -radius; dz <= radius; dz++ {
					bz := cz + dz
					if !IsPiglinRepellentBlock(world.BlockName(cache.GetBlock(bx, by, bz))) {
						continue
					}
					fx, fy, fz := float64(bx)+0.5, float64(by)+0.5, float64(bz)+0.5
					dSq := (fx-x)*(fx-x) + (fy-y)*(fy-y) + (fz-z)*(fz-z)
					if dSq <= float64(radius*radius) && dSq < bestDistSq {
						bestDistSq = dSq
						rx, ry, rz = fx, fy, fz
						found = true
					}
				}
			}
		}
	}

	if s.entities != nil {
		allEnts := s.entities.AppendAll(nil)
		maxDistSq := float64(radius * radius)
		for i := range allEnts {
			ent := &allEnts[i]
			if ent.EffectiveDimension() != normDim {
				continue
			}
			tName := canonicalResourceName(ent.TypeName)
			if tName != MobTypeZombifiedPiglin && tName != MobTypeZoglin {
				continue
			}
			dSq := (ent.X-x)*(ent.X-x) + (ent.Y-y)*(ent.Y-y) + (ent.Z-z)*(ent.Z-z)
			if dSq <= maxDistSq && dSq < bestDistSq {
				bestDistSq = dSq
				rx, ry, rz = ent.X, ent.Y, ent.Z
				found = true
			}
		}
	}
	return rx, ry, rz, found
}

// EncodePiglinEquipmentPacket encodes a ClientboundSetEquipmentPacket (0x66)
// for a Piglin's main-hand (slot 0) and off-hand (slot 1) items.
func EncodePiglinEquipmentPacket(entityID int32, mainHand, offHand world.ItemStack) []byte {
	w := protocol.NewWriter(32)
	w.VarInt(entityID)

	// Entry 0: MAINHAND (slot 0 | 0x80 continuation bit)
	w.Byte(0x80)
	mainID := mainHand.ResolvedItemID()
	if mainHand.IsEmpty() || mainID <= 0 {
		w.VarInt(0)
	} else {
		w.VarInt(mainHand.Count)
		w.VarInt(mainID)
		w.VarInt(0)
		w.VarInt(0)
	}

	// Entry 1: OFFHAND (slot 1, last entry bit 7 = 0)
	w.Byte(1)
	offID := offHand.ResolvedItemID()
	if offHand.IsEmpty() || offID <= 0 {
		w.VarInt(0)
	} else {
		w.VarInt(offHand.Count)
		w.VarInt(offID)
		w.VarInt(0)
		w.VarInt(0)
	}
	return w.Bytes()
}

// EncodePiglinEntityDataPacket encodes a ClientboundSetEntityDataPacket (0x63)
// updating a piglin's ImmuneToZombification (16), Baby (17), ChargingCrossbow (18),
// and Dancing (19) flags for protocol 775.
func EncodePiglinEntityDataPacket(entityID int32, immuneToZombification, baby, chargingCrossbow, dancing bool) []byte {
	w := protocol.NewWriter(24)
	w.VarInt(entityID)

	w.Byte(EntityDataPiglinImmuneZombificationID)
	w.VarInt(EntityDataSerializerBool)
	w.Bool(immuneToZombification)

	w.Byte(EntityDataPiglinIsBabyID)
	w.VarInt(EntityDataSerializerBool)
	w.Bool(baby)

	w.Byte(EntityDataPiglinChargingCrossbowID)
	w.VarInt(EntityDataSerializerBool)
	w.Bool(chargingCrossbow)

	w.Byte(EntityDataPiglinIsDancingID)
	w.VarInt(EntityDataSerializerBool)
	w.Bool(dancing)

	w.Byte(0xFF)
	return w.Bytes()
}

// DecodePiglinEntityDataPacket decodes a ClientboundSetEntityDataPacket (0x63)
// produced by EncodePiglinEntityDataPacket.
func DecodePiglinEntityDataPacket(data []byte) (entityID int32, immuneToZombification, baby, chargingCrossbow, dancing bool, err error) {
	r := protocol.NewReader(data)
	entityID, err = r.VarInt()
	if err != nil {
		return 0, false, false, false, false, err
	}
	for {
		idx, err := r.ReadByte()
		if err != nil {
			return 0, false, false, false, false, err
		}
		if idx == 0xFF {
			return entityID, immuneToZombification, baby, chargingCrossbow, dancing, nil
		}
		ser, err := r.VarInt()
		if err != nil || ser != EntityDataSerializerBool {
			return 0, false, false, false, false, errors.New("piglin: unexpected entity data serializer")
		}
		val, err := r.Bool()
		if err != nil {
			return 0, false, false, false, false, err
		}
		switch idx {
		case EntityDataPiglinImmuneZombificationID:
			immuneToZombification = val
		case EntityDataPiglinIsBabyID:
			baby = val
		case EntityDataPiglinChargingCrossbowID:
			chargingCrossbow = val
		case EntityDataPiglinIsDancingID:
			dancing = val
		default:
			return 0, false, false, false, false, errors.New("piglin: unexpected entity data index")
		}
	}
}

// EncodePiglinBruteEntityDataPacket encodes a ClientboundSetEntityDataPacket (0x63)
// updating a piglin brute's ImmuneToZombification (16) flag for protocol 775.
func EncodePiglinBruteEntityDataPacket(entityID int32, immuneToZombification bool) []byte {
	w := protocol.NewWriter(12)
	w.VarInt(entityID)
	w.Byte(EntityDataPiglinImmuneZombificationID)
	w.VarInt(EntityDataSerializerBool)
	w.Bool(immuneToZombification)
	w.Byte(0xFF)
	return w.Bytes()
}

// DecodePiglinBruteEntityDataPacket decodes a ClientboundSetEntityDataPacket (0x63)
// produced by EncodePiglinBruteEntityDataPacket.
func DecodePiglinBruteEntityDataPacket(data []byte) (entityID int32, immuneToZombification bool, err error) {
	r := protocol.NewReader(data)
	entityID, err = r.VarInt()
	if err != nil {
		return 0, false, err
	}
	idx, err := r.ReadByte()
	if err != nil || idx != EntityDataPiglinImmuneZombificationID {
		return 0, false, errors.New("piglin_brute: unexpected entity data index")
	}
	ser, err := r.VarInt()
	if err != nil || ser != EntityDataSerializerBool {
		return 0, false, errors.New("piglin_brute: unexpected entity data serializer")
	}
	immuneToZombification, err = r.Bool()
	if err != nil {
		return 0, false, err
	}
	term, err := r.ReadByte()
	if err != nil || term != 0xFF {
		return 0, false, errors.New("piglin_brute: missing 0xFF terminator")
	}
	return entityID, immuneToZombification, nil
}

// SpawnPiglin spawns a `minecraft:piglin` (ID 101, 16.0 HP) at (x, y, z) in dimension.
func (s *Server) SpawnPiglin(dimension string, x, y, z float64, baby bool) (int32, error) {
	return s.SpawnPiglinWithWeapon(dimension, x, y, z, baby, "minecraft:golden_sword")
}

// SpawnPiglinWithWeapon spawns a `minecraft:piglin` at (x, y, z) in dimension
// armed with `weaponID` (e.g. `minecraft:golden_sword` or `minecraft:crossbow`).
func (s *Server) SpawnPiglinWithWeapon(dimension string, x, y, z float64, baby bool, weaponID string) (int32, error) {
	if s == nil || s.entities == nil {
		return 0, errors.New("server: nil entity manager")
	}
	normDim := world.NormalizeDimension(dimension)
	typeID := ResolveEntityTypeID(MobTypePiglin)
	if typeID < 0 {
		typeID = EntityTypePiglinID
	}
	id := s.entities.Add(&world.Entity{
		TypeID:    typeID,
		TypeName:  MobTypePiglin,
		Dimension: normDim,
		X:         x,
		Y:         y,
		Z:         z,
		OnGround:  true,
	})

	tr := s.entityTracker()
	tr.mu.Lock()
	mem := tr.mobStateFor(id, tr.aiTickID)
	mem.healthInit = true
	mem.health = DefaultPiglinMaxHealth
	mem.lastHealth = DefaultPiglinMaxHealth
	if baby {
		mem.ageTicks = BabyMobGrowthTicks
	}
	tr.mu.Unlock()

	var weapon world.ItemStack
	if !baby && weaponID != "" {
		weapon = world.NewNamedItemStack(canonicalResourceName(weaponID), 1)
	}
	nm := s.netherMobs()
	nm.mu.Lock()
	nm.piglins[id] = &PiglinState{
		EntityID:     id,
		TypeName:     MobTypePiglin,
		Dimension:    normDim,
		Baby:         baby,
		MainHandItem: weapon,
		CannotHunt:   baby,
	}
	nm.mu.Unlock()

	if ent := s.entities.Get(id); ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, ent))
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, PlaySetEntityDataCB,
			EncodePiglinEntityDataPacket(id, false, baby, false, false))
		if !weapon.IsEmpty() {
			s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlaySetEquipment,
				EncodePiglinEquipmentPacket(id, weapon, world.ItemStack{}))
		}
	}
	return id, nil
}

// SpawnPiglinBrute spawns a `minecraft:piglin_brute` (ID 102, 50.0 HP, armed with
// `minecraft:golden_axe`) at (x, y, z) in dimension, anchored to its spawn home.
func (s *Server) SpawnPiglinBrute(dimension string, x, y, z float64) (int32, error) {
	if s == nil || s.entities == nil {
		return 0, errors.New("server: nil entity manager")
	}
	normDim := world.NormalizeDimension(dimension)
	typeID := ResolveEntityTypeID(MobTypePiglinBrute)
	if typeID < 0 {
		typeID = EntityTypePiglinBruteID
	}
	id := s.entities.Add(&world.Entity{
		TypeID:    typeID,
		TypeName:  MobTypePiglinBrute,
		Dimension: normDim,
		X:         x,
		Y:         y,
		Z:         z,
		OnGround:  true,
	})

	tr := s.entityTracker()
	tr.mu.Lock()
	mem := tr.mobStateFor(id, tr.aiTickID)
	mem.healthInit = true
	mem.health = DefaultPiglinBruteMaxHealth
	mem.lastHealth = DefaultPiglinBruteMaxHealth
	tr.mu.Unlock()

	axe := world.NewNamedItemStack("minecraft:golden_axe", 1)
	nm := s.netherMobs()
	nm.mu.Lock()
	nm.piglins[id] = &PiglinState{
		EntityID:     id,
		TypeName:     MobTypePiglinBrute,
		Dimension:    normDim,
		MainHandItem: axe,
		CannotHunt:   true,
		HasHome:      true,
		HomeX:        x,
		HomeY:        y,
		HomeZ:        z,
	}
	nm.mu.Unlock()

	if ent := s.entities.Get(id); ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, ent))
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, PlaySetEntityDataCB,
			EncodePiglinBruteEntityDataPacket(id, false))
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlaySetEquipment,
			EncodePiglinEquipmentPacket(id, axe, world.ItemStack{}))
	}
	return id, nil
}

// SetPiglinBruteHome sets the Bastion Remnant / Nether Fortress home patrol anchor
// for a `minecraft:piglin_brute` (or `minecraft:piglin`).
func (s *Server) SetPiglinBruteHome(entityID int32, homeX, homeY, homeZ float64) bool {
	if s == nil || entityID <= 0 {
		return false
	}
	ps := s.ensurePiglinState(entityID)
	if ps == nil {
		return false
	}
	nm := s.netherMobs()
	nm.mu.Lock()
	ps.HasHome = true
	ps.HomeX = homeX
	ps.HomeY = homeY
	ps.HomeZ = homeZ
	nm.mu.Unlock()
	return true
}

// SpawnZombifiedPiglin spawns a `minecraft:zombified_piglin` (ID 154, 20.0 HP,
// armed with `minecraft:golden_sword`) at (x, y, z) in dimension.
func (s *Server) SpawnZombifiedPiglin(dimension string, x, y, z float64, baby bool) (int32, error) {
	if s == nil || s.entities == nil {
		return 0, errors.New("server: nil entity manager")
	}
	normDim := world.NormalizeDimension(dimension)
	typeID := ResolveEntityTypeID(MobTypeZombifiedPiglin)
	if typeID < 0 {
		typeID = EntityTypeZombifiedPiglinID
	}
	id := s.entities.Add(&world.Entity{
		TypeID:    typeID,
		TypeName:  MobTypeZombifiedPiglin,
		Dimension: normDim,
		X:         x,
		Y:         y,
		Z:         z,
		OnGround:  true,
	})

	tr := s.entityTracker()
	tr.mu.Lock()
	mem := tr.mobStateFor(id, tr.aiTickID)
	mem.healthInit = true
	mem.health = DefaultZombifiedPiglinMaxHealth
	mem.lastHealth = DefaultZombifiedPiglinMaxHealth
	if baby {
		mem.ageTicks = BabyMobGrowthTicks
	}
	tr.mu.Unlock()

	sword := world.NewNamedItemStack("minecraft:golden_sword", 1)
	nm := s.netherMobs()
	nm.mu.Lock()
	nm.zombifiedPiglins[id] = &ZombifiedPiglinState{
		EntityID:     id,
		Dimension:    normDim,
		Baby:         baby,
		MainHandItem: sword,
	}
	nm.mu.Unlock()

	if ent := s.entities.Get(id); ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, ent))
		if baby {
			s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, PlaySetEntityDataCB, EncodeAgeableMobDataPacket(id, true))
		}
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlaySetEquipment,
			EncodePiglinEquipmentPacket(id, sword, world.ItemStack{}))
	}
	return id, nil
}

// GetZombifiedPiglinState returns a snapshot of the ZombifiedPiglinState for entityID.
func (s *Server) GetZombifiedPiglinState(entityID int32) (ZombifiedPiglinState, bool) {
	if s == nil || entityID <= 0 {
		return ZombifiedPiglinState{}, false
	}
	nm := s.netherMobs()
	nm.mu.RLock()
	st, ok := nm.zombifiedPiglins[entityID]
	var out ZombifiedPiglinState
	if ok && st != nil {
		out = *st
	}
	nm.mu.RUnlock()
	return out, ok && st != nil
}

// OnZombifiedPiglinDamaged provokes the attacked `minecraft:zombified_piglin`
// and alerts all `minecraft:zombified_piglin` within 32 blocks in the same
// dimension to attack `attacker` for 400..800 ticks (`SoundZombifiedPiglinAngry`).
func (s *Server) OnZombifiedPiglinDamaged(entityID int32, attacker *PlayerSession, attackerMobID int32) int {
	if s == nil || s.entities == nil || entityID <= 0 {
		return 0
	}
	ent := s.entities.Get(entityID)
	if ent == nil {
		return 0
	}
	dim := ent.EffectiveDimension()
	const maxDistSq = ZombifiedPiglinAlertRange * ZombifiedPiglinAlertRange

	allEnts := s.entities.AppendAll(nil)
	nm := s.netherMobs()
	nm.mu.Lock()
	alerted := 0
	for i := range allEnts {
		other := &allEnts[i]
		if other.EffectiveDimension() != dim || canonicalResourceName(other.TypeName) != MobTypeZombifiedPiglin {
			continue
		}
		dx := other.X - ent.X
		dy := other.Y - ent.Y
		dz := other.Z - ent.Z
		if dx*dx+dy*dy+dz*dz > maxDistSq {
			continue
		}
		zp, ok := nm.zombifiedPiglins[other.ID]
		if !ok || zp == nil {
			zp = &ZombifiedPiglinState{
				EntityID:     other.ID,
				Dimension:    dim,
				MainHandItem: world.NewNamedItemStack("minecraft:golden_sword", 1),
			}
			nm.zombifiedPiglins[other.ID] = zp
		}
		zp.AngerTarget = attacker
		zp.AngerMobID = attackerMobID
		zp.AngerTicks = ZombifiedPiglinAngerMinTicks
		alerted++
	}
	nm.mu.Unlock()

	if alerted > 0 {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
			EncodeSoundPacket(SoundZombifiedPiglinAngry, SoundSourceHostile, ent.X, ent.Y, ent.Z, 2.0, 1.0, 0))
	}
	return alerted
}

func (s *Server) ensurePiglinState(entityID int32) *PiglinState {
	if s == nil || entityID <= 0 {
		return nil
	}
	nm := s.netherMobs()
	nm.mu.Lock()
	defer nm.mu.Unlock()
	ps, ok := nm.piglins[entityID]
	if !ok || ps == nil {
		tName := MobTypePiglin
		dim := world.DimensionNether
		if s.entities != nil {
			if ent := s.entities.Get(entityID); ent != nil {
				tName = canonicalResourceName(ent.TypeName)
				dim = ent.EffectiveDimension()
			}
		}
		ps = &PiglinState{
			EntityID:  entityID,
			TypeName:  tName,
			Dimension: dim,
		}
		nm.piglins[entityID] = ps
	}
	return ps
}

// GetPiglinState returns a snapshot of the PiglinState for entityID.
func (s *Server) GetPiglinState(entityID int32) (PiglinState, bool) {
	if s == nil || entityID <= 0 {
		return PiglinState{}, false
	}
	nm := s.netherMobs()
	nm.mu.RLock()
	ps, ok := nm.piglins[entityID]
	var out PiglinState
	if ok && ps != nil {
		out = *ps
		if len(ps.Inventory) > 0 {
			out.Inventory = make([]world.ItemStack, len(ps.Inventory))
			copy(out.Inventory, ps.Inventory)
		}
	}
	nm.mu.RUnlock()
	return out, ok && ps != nil
}

// PiglinInventory returns a copy of the Piglin's 8-slot pocket inventory of
// admired/collected items.
func (s *Server) PiglinInventory(entityID int32) []world.ItemStack {
	ps, ok := s.GetPiglinState(entityID)
	if !ok {
		return nil
	}
	return ps.Inventory
}

// SetPiglinImmuneToZombification sets whether the piglin or piglin brute with
// entityID is immune to zombification outside the Nether.
func (s *Server) SetPiglinImmuneToZombification(entityID int32, immune bool) bool {
	if s == nil || s.entities == nil || entityID <= 0 {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil {
		return false
	}
	ps := s.ensurePiglinState(entityID)
	nm := s.netherMobs()
	nm.mu.Lock()
	ps.ImmuneToZombification = immune
	if immune {
		ps.TimeInOverworld = 0
	}
	snap := *ps
	nm.mu.Unlock()

	cp := ent.ChunkPos()
	dim := ent.EffectiveDimension()
	if snap.TypeName == MobTypePiglinBrute {
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
			EncodePiglinBruteEntityDataPacket(entityID, immune))
	} else {
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
			EncodePiglinEntityDataPacket(entityID, immune, snap.Baby, snap.ChargingCrossbow, snap.Dancing))
	}
	return true
}

// SetPiglinChargingCrossbow sets whether the Piglin with entityID is drawing a
// crossbow (`DATA_IS_CHARGING_CROSSBOW`, index 18) and broadcasts entity data.
func (s *Server) SetPiglinChargingCrossbow(entityID int32, charging bool) bool {
	if s == nil || s.entities == nil || entityID <= 0 {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypePiglin {
		return false
	}
	ps := s.ensurePiglinState(entityID)
	nm := s.netherMobs()
	nm.mu.Lock()
	ps.ChargingCrossbow = charging
	if !charging {
		ps.CrossbowChargeTicks = 0
	}
	snap := *ps
	nm.mu.Unlock()

	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, PlaySetEntityDataCB,
		EncodePiglinEntityDataPacket(entityID, snap.ImmuneToZombification, snap.Baby, snap.ChargingCrossbow, snap.Dancing))
	return true
}

// StartPiglinVictoryDance starts a celebratory victory dance (`DATA_IS_DANCING`,
// index 19) on a `minecraft:piglin` for `PiglinCelebrateTicks` (60 ticks),
// broadcasting entity metadata and `SoundPiglinCelebrate`.
func (s *Server) StartPiglinVictoryDance(entityID int32) bool {
	if s == nil || s.entities == nil || entityID <= 0 {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypePiglin {
		return false
	}
	ps := s.ensurePiglinState(entityID)
	nm := s.netherMobs()
	nm.mu.Lock()
	ps.Dancing = true
	ps.DanceTicks = PiglinCelebrateTicks
	ps.HuntCooldownTicks = PiglinHuntCooldownTicks
	snap := *ps
	nm.mu.Unlock()

	dim := ent.EffectiveDimension()
	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
		EncodePiglinEntityDataPacket(entityID, snap.ImmuneToZombification, snap.Baby, snap.ChargingCrossbow, true))
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundPiglinCelebrate, SoundSourceHostile, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
	return true
}

// TryPiglinStartHoglinHunt checks whether the adult `minecraft:piglin` with
// `piglinID` can initiate a hunt against a nearby adult `minecraft:hoglin`
// within 16 blocks:
//   - If adult Hoglins outnumber adult Piglins within 16 blocks, the Piglin
//     retreats (`Retreating = true`, `SoundPiglinRetreat`).
//   - Otherwise (`adultPiglins >= adultHoglins`), all nearby adult Piglins
//     within 16 blocks join the hunt against the nearest adult Hoglin!
func (s *Server) TryPiglinStartHoglinHunt(piglinID int32) (targetHoglinID int32, started bool) {
	if s == nil || s.entities == nil || piglinID <= 0 {
		return 0, false
	}
	ent := s.entities.Get(piglinID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypePiglin {
		return 0, false
	}
	ps, ok := s.GetPiglinState(piglinID)
	if !ok || ps.Baby || ps.CannotHunt || ps.HuntCooldownTicks > 0 || ps.Bartering || ps.AdmireTicks > 0 {
		return 0, false
	}

	dim := ent.EffectiveDimension()
	const maxDistSq = PiglinAggroRange * PiglinAggroRange

	var (
		adultPiglins []int32
		adultHoglins []int32
		bestHoglinID int32
		bestDistSq   = maxDistSq + 1.0
	)
	nm := s.netherMobs()
	nm.mu.RLock()
	for id, p := range nm.piglins {
		if p == nil || p.Baby || s.IsMobBaby(id) || p.TypeName != MobTypePiglin {
			continue
		}
		oEnt := s.entities.Get(id)
		if oEnt == nil || oEnt.EffectiveDimension() != dim {
			continue
		}
		dx := oEnt.X - ent.X
		dy := oEnt.Y - ent.Y
		dz := oEnt.Z - ent.Z
		if dx*dx+dy*dy+dz*dz <= maxDistSq {
			adultPiglins = append(adultPiglins, id)
		}
	}
	for id, h := range nm.hoglins {
		if h == nil || h.Baby || s.IsMobBaby(id) || h.CannotBeHunted || h.TypeName != MobTypeHoglin {
			continue
		}
		hEnt := s.entities.Get(id)
		if hEnt == nil || hEnt.EffectiveDimension() != dim {
			continue
		}
		dx := hEnt.X - ent.X
		dy := hEnt.Y - ent.Y
		dz := hEnt.Z - ent.Z
		dSq := dx*dx + dy*dy + dz*dz
		if dSq <= maxDistSq {
			adultHoglins = append(adultHoglins, id)
			if dSq < bestDistSq {
				bestDistSq = dSq
				bestHoglinID = id
			}
		}
	}
	nm.mu.RUnlock()

	if bestHoglinID == 0 {
		return 0, false
	}
	cp := ent.ChunkPos()
	if len(adultHoglins) > len(adultPiglins) {
		nm.mu.Lock()
		if cur, ok := nm.piglins[piglinID]; ok && cur != nil {
			cur.Retreating = true
			cur.HuntCooldownTicks = 200
		}
		nm.mu.Unlock()
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
			EncodeSoundPacket(SoundPiglinRetreat, SoundSourceHostile, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
		return 0, false
	}

	nm.mu.Lock()
	for _, pid := range adultPiglins {
		if cur, ok := nm.piglins[pid]; ok && cur != nil && !cur.CannotHunt {
			cur.AngerMobID = bestHoglinID
			cur.AngerTicks = PiglinAngerDurationTicks
			cur.HuntCooldownTicks = PiglinHuntCooldownTicks
			cur.Retreating = false
		}
	}
	nm.mu.Unlock()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundPiglinAngry, SoundSourceHostile, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
	return bestHoglinID, true
}

// TryBabyPiglinMountBabyHoglin mounts a baby `minecraft:piglin` onto a nearby
// baby `minecraft:hoglin` within 8 blocks (up to 3 stacked baby piglin riders).
func (s *Server) TryBabyPiglinMountBabyHoglin(babyPiglinID int32) (hoglinID int32, mounted bool) {
	if s == nil || s.entities == nil || babyPiglinID <= 0 {
		return 0, false
	}
	ent := s.entities.Get(babyPiglinID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypePiglin {
		return 0, false
	}
	ps, ok := s.GetPiglinState(babyPiglinID)
	if !ok || (!ps.Baby && !s.IsMobBaby(babyPiglinID)) || ps.MountEntityID > 0 || s.EntityMountedVehicle(babyPiglinID) > 0 {
		return 0, false
	}
	dim := ent.EffectiveDimension()
	nm := s.netherMobs()
	nm.mu.RLock()
	var candidateHoglinID int32
	bestDistSq := 64.0
	for id, hs := range nm.hoglins {
		if hs == nil || (!hs.Baby && !s.IsMobBaby(id)) || hs.TypeName != MobTypeHoglin {
			continue
		}
		hEnt := s.entities.Get(id)
		if hEnt == nil || hEnt.EffectiveDimension() != dim {
			continue
		}
		dx := hEnt.X - ent.X
		dy := hEnt.Y - ent.Y
		dz := hEnt.Z - ent.Z
		dSq := dx*dx + dy*dy + dz*dz
		if dSq <= bestDistSq && len(hs.RiderEntityIDs) < 3 {
			bestDistSq = dSq
			candidateHoglinID = id
		}
	}
	nm.mu.RUnlock()

	if candidateHoglinID <= 0 {
		return 0, false
	}
	hEnt := s.entities.Get(candidateHoglinID)
	if hEnt == nil {
		return 0, false
	}

	nm.mu.Lock()
	hs := nm.hoglins[candidateHoglinID]
	curPS := nm.piglins[babyPiglinID]
	if hs == nil || curPS == nil || len(hs.RiderEntityIDs) >= 3 || curPS.MountEntityID > 0 {
		nm.mu.Unlock()
		return 0, false
	}
	hs.RiderEntityIDs = append(hs.RiderEntityIDs, babyPiglinID)
	curPS.MountEntityID = candidateHoglinID
	ridersCopy := make([]int32, len(hs.RiderEntityIDs))
	copy(ridersCopy, hs.RiderEntityIDs)
	nm.mu.Unlock()

	mgr := s.vehicleManager()
	mgr.mu.Lock()
	mgr.byPassengerEntity[babyPiglinID] = candidateHoglinID
	mgr.mu.Unlock()

	s.entities.Update(babyPiglinID, func(e *world.Entity) {
		e.X = hEnt.X
		e.Y = hEnt.Y + 0.75
		e.Z = hEnt.Z
	})
	cp := hEnt.ChunkPos()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetPassengersCB,
		EncodeSetPassengersPacket(candidateHoglinID, ridersCopy))
	return candidateHoglinID, true
}

// ZombifyPiglin converts a `minecraft:piglin` or `minecraft:piglin_brute` into a
// `minecraft:zombified_piglin` at the same position, preserving baby state and
// main-hand weapon (`golden_sword`, `crossbow`, `golden_axe`) and applying
// Nausea (`200` ticks) per vanilla `AbstractPiglin.finishConversion`.
func (s *Server) ZombifyPiglin(entityID int32) (zombifiedID int32, ok bool) {
	if s == nil || s.entities == nil || entityID <= 0 {
		return 0, false
	}
	ent := s.entities.Get(entityID)
	if ent == nil {
		return 0, false
	}
	norm := canonicalResourceName(ent.TypeName)
	if norm != MobTypePiglin && norm != MobTypePiglinBrute {
		return 0, false
	}
	ps, _ := s.GetPiglinState(entityID)
	dim := ent.EffectiveDimension()
	x, y, z := ent.X, ent.Y, ent.Z
	yaw, pitch := ent.Yaw, ent.Pitch
	cp := ent.ChunkPos()

	nm := s.netherMobs()
	nm.mu.Lock()
	delete(nm.piglins, entityID)
	nm.mu.Unlock()

	tr := s.entityTracker()
	tr.mu.Lock()
	delete(tr.mobStates, entityID)
	tr.mu.Unlock()

	s.entities.Remove(entityID)
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayRemoveEntities,
		encodeRemoveEntitiesPacket(nil, []int32{entityID}))

	typeID := ResolveEntityTypeID(MobTypeZombifiedPiglin)
	if typeID < 0 {
		typeID = EntityTypeZombifiedPiglinID
	}
	zombifiedID = s.entities.Add(&world.Entity{
		TypeID:    typeID,
		TypeName:  MobTypeZombifiedPiglin,
		Dimension: dim,
		X:         x,
		Y:         y,
		Z:         z,
		Yaw:       yaw,
		HeadYaw:   yaw,
		Pitch:     pitch,
		OnGround:  ent.OnGround,
	})

	tr.mu.Lock()
	mem := tr.mobStateFor(zombifiedID, tr.aiTickID)
	mem.healthInit = true
	mem.health = DefaultZombifiedPiglinMaxHealth
	mem.lastHealth = DefaultZombifiedPiglinMaxHealth
	if ps.Baby {
		mem.ageTicks = BabyMobGrowthTicks
	}
	tr.mu.Unlock()

	weapon := ps.MainHandItem
	if weapon.IsEmpty() && !ps.Baby {
		weapon = world.NewNamedItemStack("minecraft:golden_sword", 1)
	}
	nm.mu.Lock()
	nm.zombifiedPiglins[zombifiedID] = &ZombifiedPiglinState{
		EntityID:     zombifiedID,
		Dimension:    dim,
		Baby:         ps.Baby,
		MainHandItem: weapon,
	}
	nm.mu.Unlock()

	if zEnt := s.entities.Get(zombifiedID); zEnt != nil {
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, zEnt))
		if ps.Baby {
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB, EncodeAgeableMobDataPacket(zombifiedID, true))
		}
		if !weapon.IsEmpty() {
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlaySetEquipment,
				EncodePiglinEquipmentPacket(zombifiedID, weapon, world.ItemStack{}))
		}
	}
	s.ApplyEntityEffectScaled(zombifiedID, NewStatusEffectInstance(EffectNausea, 200, 0), 1.0)
	snd := SoundPiglinConvertedToZombified
	if norm == MobTypePiglinBrute {
		snd = SoundPiglinBruteConverted
	}
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(snd, SoundSourceHostile, x, y, z, 1.0, 1.0, 0))
	return zombifiedID, true
}

// StartPiglinBartering starts a piglin bartering (or admiring a gold item) for
// `PiglinBarterAdmireTicks` (120 ticks = 6s). Only adult `minecraft:piglin` can
// barter `minecraft:gold_ingot`; `minecraft:piglin_brute` never barters.
func (s *Server) StartPiglinBartering(entityID int32, player *PlayerSession, itemStack world.ItemStack) bool {
	if s == nil || s.entities == nil || entityID <= 0 || itemStack.IsEmpty() {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypePiglin {
		return false
	}
	itemID := canonicalResourceName(itemStack.ResolvedID())
	if !IsPiglinLovedGoldItem(itemID) {
		return false
	}
	ps := s.ensurePiglinState(entityID)
	nm := s.netherMobs()
	nm.mu.Lock()
	if ps.Baby || s.IsMobBaby(entityID) {
		if itemID != "minecraft:gold_ingot" && ps.OffHandItem.IsEmpty() {
			ps.OffHandItem = world.NewItemStack(itemStack.ResolvedItemID(), 1)
			ps.AdmireTicks = PiglinBarterAdmireTicks
			mainH := ps.MainHandItem
			offH := ps.OffHandItem
			nm.mu.Unlock()
			cp := ent.ChunkPos()
			s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, protocol.PlaySetEquipment,
				EncodePiglinEquipmentPacket(entityID, mainH, offH))
			return true
		}
		nm.mu.Unlock()
		return false
	}
	if !ps.OffHandItem.IsEmpty() && ps.AdmireTicks > 0 {
		nm.mu.Unlock()
		return false
	}
	wasAngryAtPlayer := player != nil && (ps.AngerTarget == player || !IsPlayerWearingGoldArmor(player))
	ps.OffHandItem = world.NewItemStack(itemStack.ResolvedItemID(), 1)
	ps.AdmireTicks = PiglinBarterAdmireTicks
	ps.Bartering = itemID == "minecraft:gold_ingot"
	ps.BarterPlayer = player
	// Distracting an angry piglin with gold calms its immediate aggro while admiring.
	if ps.AngerTarget == player {
		ps.AngerTarget = nil
		ps.AngerTicks = 0
	}
	mainH := ps.MainHandItem
	offH := ps.OffHandItem
	nm.mu.Unlock()

	if player != nil && wasAngryAtPlayer {
		ensurePiglinAdvancementsRegistered()
		s.GrantCriterion(player, "minecraft:nether/distract_piglin", "distract_piglin")
	}
	cp := ent.ChunkPos()
	dim := ent.EffectiveDimension()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlaySetEquipment,
		EncodePiglinEquipmentPacket(entityID, mainH, offH))
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundPiglinAdmiringItem, SoundSourceHostile, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
	return true
}

// CompletePiglinBarter finishes an active piglin barter (or forces immediate
// completion), consuming the held `minecraft:gold_ingot`, rolling an item from
// `gameplay/piglin_bartering`, and spawning the bartered item entity in the world
// (tossed toward the bartering player if present).
func (s *Server) CompletePiglinBarter(entityID int32, rng *rand.Rand) (barteredStack world.ItemStack, barteredMeta ItemMeta, droppedEntityID int32, ok bool) {
	if s == nil || s.entities == nil || entityID <= 0 {
		return world.ItemStack{}, ItemMeta{}, 0, false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypePiglin {
		return world.ItemStack{}, ItemMeta{}, 0, false
	}
	ps := s.ensurePiglinState(entityID)
	nm := s.netherMobs()
	nm.mu.Lock()
	if !ps.Bartering && canonicalResourceName(ps.OffHandItem.ResolvedID()) != "minecraft:gold_ingot" {
		nm.mu.Unlock()
		return world.ItemStack{}, ItemMeta{}, 0, false
	}
	barterPlayer := ps.BarterPlayer
	mainH := ps.MainHandItem
	ps.OffHandItem = world.ItemStack{}
	ps.AdmireTicks = 0
	ps.Bartering = false
	ps.BarterPlayer = nil
	nm.mu.Unlock()

	dim := ent.EffectiveDimension()
	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlaySetEquipment,
		EncodePiglinEquipmentPacket(entityID, mainH, world.ItemStack{}))

	barteredStack, barteredMeta, _ = RollPiglinBarterLoot(rng)
	spawnX, spawnY, spawnZ := ent.X, ent.Y+0.6, ent.Z
	droppedEntityID, _ = s.SpawnItemEntityInDimension(dim, spawnX, spawnY, spawnZ, barteredStack)
	if droppedEntityID > 0 && barterPlayer != nil && !barterPlayer.IsDead() {
		pSnap := barterPlayer.Snapshot()
		dx := pSnap.X - ent.X
		dz := pSnap.Z - ent.Z
		dist := math.Hypot(dx, dz)
		if dist > 1e-4 {
			vx := (dx / dist) * 0.2
			vz := (dz / dist) * 0.2
			s.entities.Update(droppedEntityID, func(e *world.Entity) {
				e.VelocityX = clampVelocityShort(vx)
				e.VelocityY = clampVelocityShort(0.15)
				e.VelocityZ = clampVelocityShort(vz)
			})
		}
	}
	return barteredStack, barteredMeta, droppedEntityID, droppedEntityID > 0
}

// InteractWithPiglin handles a player right-clicking a `minecraft:piglin` with
// `minecraft:gold_ingot` to initiate bartering.
func (s *Server) InteractWithPiglin(session *PlayerSession, entityID int32) bool {
	if s == nil || s.entities == nil || session == nil || session.IsDead() {
		return false
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator || snap.Health <= 0 {
		return false
	}
	held := session.HeldItem()
	if held.IsEmpty() || canonicalResourceName(held.ResolvedID()) != "minecraft:gold_ingot" {
		return false
	}
	if !s.StartPiglinBartering(entityID, session, held) {
		return false
	}
	consumeOneHeldItemInSurvival(session)
	return true
}

// TryPiglinPickUpGoldItem attempts to make a piglin pick up a gold-related item
// stack from the ground, starting bartering if it is `minecraft:gold_ingot`.
func (s *Server) TryPiglinPickUpGoldItem(entityID int32, stack world.ItemStack, thrower *PlayerSession) bool {
	if s == nil || entityID <= 0 || stack.IsEmpty() {
		return false
	}
	if !IsPiglinLovedGoldItem(stack.ResolvedID()) {
		return false
	}
	return s.StartPiglinBartering(entityID, thrower, stack)
}

// NotifyPiglinsPlayerOpenedGuardedBlock alerts all adult piglins within 16 blocks
// in dimension when a player opens a guarded container (chest, barrel, shulker_box,
// ender_chest, trapped_chest) or breaks a gold block, making them angry at `player`
// for `PiglinAngerDurationTicks` (600 ticks) even if wearing gold armor!
func (s *Server) NotifyPiglinsPlayerOpenedGuardedBlock(dimension string, x, y, z int, player *PlayerSession) int {
	if s == nil || s.entities == nil || player == nil || player.IsDead() {
		return 0
	}
	pSnap := player.Snapshot()
	if pSnap.GameMode == GameModeCreative || pSnap.GameMode == GameModeSpectator {
		return 0
	}
	normDim := world.NormalizeDimension(dimension)
	const maxDistSq = PiglinAggroRange * PiglinAggroRange
	bx, by, bz := float64(x)+0.5, float64(y)+0.5, float64(z)+0.5
	alerted := 0

	nm := s.netherMobs()
	nm.mu.Lock()
	for id, ps := range nm.piglins {
		if ps == nil || ps.Baby {
			continue
		}
		ent := s.entities.Get(id)
		if ent == nil || ent.EffectiveDimension() != normDim {
			continue
		}
		dx := ent.X - bx
		dy := ent.Y - by
		dz := ent.Z - bz
		if dx*dx+dy*dy+dz*dz <= maxDistSq {
			ps.AngerTarget = player
			ps.AngerTicks = PiglinAngerDurationTicks
			alerted++
		}
	}
	nm.mu.Unlock()
	return alerted
}

// NotifyPiglinsPlayerMinedGoldBlock is an alias for NotifyPiglinsPlayerOpenedGuardedBlock
// when a player mines a gold-related block (`gold_block`, `nether_gold_ore`, `gilded_blackstone`, etc.).
func (s *Server) NotifyPiglinsPlayerMinedGoldBlock(dimension string, x, y, z int, player *PlayerSession) int {
	return s.NotifyPiglinsPlayerOpenedGuardedBlock(dimension, x, y, z, player)
}

// OnPiglinOrBruteDamaged provokes the hit piglin/brute and alerts nearby adult
// piglins and piglin brutes within 16 blocks to attack `attacker`.
func (s *Server) OnPiglinOrBruteDamaged(entityID int32, attacker *PlayerSession, attackerMobID int32) {
	if s == nil || s.entities == nil || entityID <= 0 {
		return
	}
	ent := s.entities.Get(entityID)
	if ent == nil {
		return
	}
	dim := ent.EffectiveDimension()
	const maxDistSq = PiglinAggroRange * PiglinAggroRange
	_ = s.ensurePiglinState(entityID)

	nm := s.netherMobs()
	nm.mu.Lock()
	for id, ps := range nm.piglins {
		if ps == nil || ps.Baby {
			continue
		}
		other := s.entities.Get(id)
		if other == nil || other.EffectiveDimension() != dim {
			continue
		}
		dx := other.X - ent.X
		dy := other.Y - ent.Y
		dz := other.Z - ent.Z
		if dx*dx+dy*dy+dz*dz <= maxDistSq {
			ps.AngerTarget = attacker
			ps.AngerMobID = attackerMobID
			ps.AngerTicks = PiglinAngerDurationTicks
		}
	}
	nm.mu.Unlock()
}

// OnPiglinOrBruteKilled cleans up state when a Piglin or Piglin Brute dies and
// returns any items held in its offhand (e.g. a gold ingot being admired) or
// collected in its pocket inventory so they drop on death.
func (s *Server) OnPiglinOrBruteKilled(entityID int32) []world.ItemStack {
	if s == nil || entityID <= 0 {
		return nil
	}
	nm := s.netherMobs()
	nm.mu.Lock()
	ps, ok := nm.piglins[entityID]
	delete(nm.piglins, entityID)
	var drops []world.ItemStack
	if ok && ps != nil {
		if !ps.OffHandItem.IsEmpty() {
			drops = append(drops, ps.OffHandItem)
		}
		for _, item := range ps.Inventory {
			if !item.IsEmpty() {
				drops = append(drops, item)
			}
		}
	}
	nm.mu.Unlock()
	return drops
}

// RollZombifiedPiglinDeathLoot rolls vanilla 26.1.2 `minecraft:zombified_piglin`
// death loot (`0..1` `minecraft:rotten_flesh` + `0..1` `minecraft:gold_nugget`).
func RollZombifiedPiglinDeathLoot(rng *rand.Rand, lootingLevel int) []world.ItemStack {
	if lootingLevel < 0 {
		lootingLevel = 0
	}
	flesh := int32(1)
	nuggets := int32(1)
	if rng != nil {
		flesh = int32(rng.Intn(2 + lootingLevel))
		nuggets = int32(rng.Intn(2 + lootingLevel))
	}
	var out []world.ItemStack
	if flesh > 0 {
		if s1 := world.NewNamedItemStack("minecraft:rotten_flesh", flesh); !s1.IsEmpty() {
			out = append(out, s1)
		}
	}
	if nuggets > 0 {
		if s2 := world.NewNamedItemStack("minecraft:gold_nugget", nuggets); !s2.IsEmpty() {
			out = append(out, s2)
		}
	}
	return out
}

// --- Hoglins & Zoglins ---

// EncodeHoglinEntityDataPacket encodes a ClientboundSetEntityDataPacket (0x63)
// updating a hoglin's ImmuneToZombification (17) flag for protocol 775.
func EncodeHoglinEntityDataPacket(entityID int32, immuneToZombification bool) []byte {
	w := protocol.NewWriter(12)
	w.VarInt(entityID)
	w.Byte(EntityDataHoglinImmuneZombificationID)
	w.VarInt(EntityDataSerializerBool)
	w.Bool(immuneToZombification)
	w.Byte(0xFF)
	return w.Bytes()
}

// DecodeHoglinEntityDataPacket decodes a ClientboundSetEntityDataPacket (0x63)
// produced by EncodeHoglinEntityDataPacket.
func DecodeHoglinEntityDataPacket(data []byte) (entityID int32, immuneToZombification bool, err error) {
	r := protocol.NewReader(data)
	entityID, err = r.VarInt()
	if err != nil {
		return 0, false, err
	}
	idx, err := r.ReadByte()
	if err != nil || idx != EntityDataHoglinImmuneZombificationID {
		return 0, false, errors.New("hoglin: unexpected entity data index")
	}
	ser, err := r.VarInt()
	if err != nil || ser != EntityDataSerializerBool {
		return 0, false, errors.New("hoglin: unexpected entity data serializer")
	}
	immuneToZombification, err = r.Bool()
	if err != nil {
		return 0, false, err
	}
	term, err := r.ReadByte()
	if err != nil || term != 0xFF {
		return 0, false, errors.New("hoglin: missing 0xFF terminator")
	}
	return entityID, immuneToZombification, nil
}

// EncodeZoglinEntityDataPacket encodes a ClientboundSetEntityDataPacket (0x63)
// updating a zoglin's Baby (16) flag for protocol 775.
func EncodeZoglinEntityDataPacket(entityID int32, baby bool) []byte {
	w := protocol.NewWriter(12)
	w.VarInt(entityID)
	w.Byte(EntityDataZoglinIsBabyID)
	w.VarInt(EntityDataSerializerBool)
	w.Bool(baby)
	w.Byte(0xFF)
	return w.Bytes()
}

// DecodeZoglinEntityDataPacket decodes a ClientboundSetEntityDataPacket (0x63)
// produced by EncodeZoglinEntityDataPacket.
func DecodeZoglinEntityDataPacket(data []byte) (entityID int32, baby bool, err error) {
	r := protocol.NewReader(data)
	entityID, err = r.VarInt()
	if err != nil {
		return 0, false, err
	}
	idx, err := r.ReadByte()
	if err != nil || idx != EntityDataZoglinIsBabyID {
		return 0, false, errors.New("zoglin: unexpected entity data index")
	}
	ser, err := r.VarInt()
	if err != nil || ser != EntityDataSerializerBool {
		return 0, false, errors.New("zoglin: unexpected entity data serializer")
	}
	baby, err = r.Bool()
	if err != nil {
		return 0, false, err
	}
	term, err := r.ReadByte()
	if err != nil || term != 0xFF {
		return 0, false, errors.New("zoglin: missing 0xFF terminator")
	}
	return entityID, baby, nil
}

// SpawnHoglin spawns a `minecraft:hoglin` (ID 64, 40.0 HP) at (x, y, z) in dimension.
func (s *Server) SpawnHoglin(dimension string, x, y, z float64, baby bool) (int32, error) {
	if s == nil || s.entities == nil {
		return 0, errors.New("server: nil entity manager")
	}
	normDim := world.NormalizeDimension(dimension)
	typeID := ResolveEntityTypeID(MobTypeHoglin)
	if typeID < 0 {
		typeID = EntityTypeHoglinID
	}
	id := s.entities.Add(&world.Entity{
		TypeID:    typeID,
		TypeName:  MobTypeHoglin,
		Dimension: normDim,
		X:         x,
		Y:         y,
		Z:         z,
		OnGround:  true,
	})

	tr := s.entityTracker()
	tr.mu.Lock()
	mem := tr.mobStateFor(id, tr.aiTickID)
	mem.healthInit = true
	mem.health = DefaultHoglinMaxHealth
	mem.lastHealth = DefaultHoglinMaxHealth
	if baby {
		mem.ageTicks = BabyMobGrowthTicks
	}
	tr.mu.Unlock()

	nm := s.netherMobs()
	nm.mu.Lock()
	nm.hoglins[id] = &HoglinState{
		EntityID:  id,
		TypeName:  MobTypeHoglin,
		Dimension: normDim,
		Baby:      baby,
	}
	nm.mu.Unlock()

	if ent := s.entities.Get(id); ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, ent))
		if baby {
			s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, PlaySetEntityDataCB, EncodeAgeableMobDataPacket(id, true))
		}
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, PlaySetEntityDataCB, EncodeHoglinEntityDataPacket(id, false))
	}
	return id, nil
}

// SpawnZoglin spawns a `minecraft:zoglin` (ID 149, 40.0 HP) at (x, y, z) in dimension.
func (s *Server) SpawnZoglin(dimension string, x, y, z float64, baby bool) (int32, error) {
	if s == nil || s.entities == nil {
		return 0, errors.New("server: nil entity manager")
	}
	normDim := world.NormalizeDimension(dimension)
	typeID := ResolveEntityTypeID(MobTypeZoglin)
	if typeID < 0 {
		typeID = EntityTypeZoglinID
	}
	id := s.entities.Add(&world.Entity{
		TypeID:    typeID,
		TypeName:  MobTypeZoglin,
		Dimension: normDim,
		X:         x,
		Y:         y,
		Z:         z,
		OnGround:  true,
	})

	tr := s.entityTracker()
	tr.mu.Lock()
	mem := tr.mobStateFor(id, tr.aiTickID)
	mem.healthInit = true
	mem.health = DefaultZoglinMaxHealth
	mem.lastHealth = DefaultZoglinMaxHealth
	if baby {
		mem.ageTicks = BabyMobGrowthTicks
	}
	tr.mu.Unlock()

	nm := s.netherMobs()
	nm.mu.Lock()
	nm.hoglins[id] = &HoglinState{
		EntityID:  id,
		TypeName:  MobTypeZoglin,
		Dimension: normDim,
		Baby:      baby,
	}
	nm.mu.Unlock()

	if ent := s.entities.Get(id); ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, ent))
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, PlaySetEntityDataCB, EncodeZoglinEntityDataPacket(id, baby))
	}
	return id, nil
}

func (s *Server) ensureHoglinState(entityID int32) *HoglinState {
	if s == nil || entityID <= 0 {
		return nil
	}
	nm := s.netherMobs()
	nm.mu.Lock()
	defer nm.mu.Unlock()
	hs, ok := nm.hoglins[entityID]
	if !ok || hs == nil {
		tName := MobTypeHoglin
		dim := world.DimensionNether
		if s.entities != nil {
			if ent := s.entities.Get(entityID); ent != nil {
				tName = canonicalResourceName(ent.TypeName)
				dim = ent.EffectiveDimension()
			}
		}
		hs = &HoglinState{
			EntityID:  entityID,
			TypeName:  tName,
			Dimension: dim,
		}
		nm.hoglins[entityID] = hs
	}
	return hs
}

// GetHoglinState returns a snapshot of the HoglinState for entityID.
func (s *Server) GetHoglinState(entityID int32) (HoglinState, bool) {
	if s == nil || entityID <= 0 {
		return HoglinState{}, false
	}
	nm := s.netherMobs()
	nm.mu.RLock()
	hs, ok := nm.hoglins[entityID]
	var out HoglinState
	if ok && hs != nil {
		out = *hs
	}
	nm.mu.RUnlock()
	return out, ok && hs != nil
}

// SetHoglinImmuneToZombification sets whether the hoglin with entityID is
// immune to zombification outside the Nether (`DATA_IMMUNE_TO_ZOMBIFICATION`).
func (s *Server) SetHoglinImmuneToZombification(entityID int32, immune bool) bool {
	if s == nil || s.entities == nil || entityID <= 0 {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeHoglin {
		return false
	}
	hs := s.ensureHoglinState(entityID)
	nm := s.netherMobs()
	nm.mu.Lock()
	hs.ImmuneToZombification = immune
	if immune {
		hs.TimeInOverworld = 0
	}
	nm.mu.Unlock()

	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, PlaySetEntityDataCB,
		EncodeHoglinEntityDataPacket(entityID, immune))
	return true
}

// ZombifyHoglin converts a `minecraft:hoglin` into a `minecraft:zoglin` at the
// same position, preserving baby state and applying Nausea (`200` ticks).
func (s *Server) ZombifyHoglin(entityID int32) (zoglinID int32, ok bool) {
	if s == nil || s.entities == nil || entityID <= 0 {
		return 0, false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeHoglin {
		return 0, false
	}
	hs, _ := s.GetHoglinState(entityID)
	baby := hs.Baby || s.IsMobBaby(entityID)
	dim := ent.EffectiveDimension()
	x, y, z := ent.X, ent.Y, ent.Z
	cp := ent.ChunkPos()

	nm := s.netherMobs()
	nm.mu.Lock()
	delete(nm.hoglins, entityID)
	nm.mu.Unlock()

	tr := s.entityTracker()
	tr.mu.Lock()
	delete(tr.mobStates, entityID)
	tr.mu.Unlock()

	s.entities.Remove(entityID)
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayRemoveEntities,
		encodeRemoveEntitiesPacket(nil, []int32{entityID}))

	zoglinID, err := s.SpawnZoglin(dim, x, y, z, baby)
	if err != nil || zoglinID <= 0 {
		return 0, false
	}
	s.ApplyEntityEffectScaled(zoglinID, NewStatusEffectInstance(EffectNausea, 200, 0), 1.0)
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundHoglinConvertedToZombified, SoundSourceHostile, x, y, z, 1.0, 1.0, 0))
	return zoglinID, true
}

// OnHoglinOrZoglinDamaged handles retaliation or baby-panic when a Hoglin or
// Zoglin takes damage.
func (s *Server) OnHoglinOrZoglinDamaged(entityID int32, attacker *PlayerSession, attackerMobID int32) {
	if s == nil || s.entities == nil || entityID <= 0 {
		return
	}
	hs := s.ensureHoglinState(entityID)
	if hs == nil {
		return
	}
	nm := s.netherMobs()
	nm.mu.Lock()
	hs.TargetPlayer = attacker
	hs.TargetMobID = attackerMobID
	if hs.TypeName == MobTypeHoglin && hs.Baby {
		hs.Panicking = true
	}
	nm.mu.Unlock()
}

// OnHoglinOrZoglinKilled cleans up state when a Hoglin or Zoglin dies and
// triggers `StartPiglinVictoryDance` on any Piglins that were hunting it!
func (s *Server) OnHoglinOrZoglinKilled(entityID int32) {
	if s == nil || entityID <= 0 {
		return
	}
	nm := s.netherMobs()
	nm.mu.Lock()
	delete(nm.hoglins, entityID)
	var celebratingPiglins []int32
	for pid, ps := range nm.piglins {
		if ps != nil && ps.TypeName == MobTypePiglin && ps.AngerMobID == entityID {
			ps.AngerMobID = 0
			ps.AngerTicks = 0
			celebratingPiglins = append(celebratingPiglins, pid)
		}
	}
	nm.mu.Unlock()

	for _, pid := range celebratingPiglins {
		s.StartPiglinVictoryDance(pid)
	}
}

// RollHoglinDeathLoot rolls vanilla 26.1.2 `minecraft:hoglin` death loot:
//   - `2..4` `minecraft:porkchop` (or `minecraft:cooked_porkchop` if `onFire == true`) + `0..lootingLevel`
//   - `0..1` `minecraft:leather` + `0..lootingLevel`
func RollHoglinDeathLoot(rng *rand.Rand, lootingLevel int, onFire bool) []world.ItemStack {
	if lootingLevel < 0 {
		lootingLevel = 0
	}
	porkCount := int32(2)
	leatherCount := int32(1)
	if rng != nil {
		porkCount = int32(2 + rng.Intn(3)) // 2..4
		leatherCount = int32(rng.Intn(2))  // 0..1
		if lootingLevel > 0 {
			porkCount += int32(rng.Intn(lootingLevel + 1))
			leatherCount += int32(rng.Intn(lootingLevel + 1))
		}
	}
	porkItem := "minecraft:porkchop"
	if onFire {
		porkItem = "minecraft:cooked_porkchop"
	}
	var out []world.ItemStack
	if s1 := world.NewNamedItemStack(porkItem, porkCount); !s1.IsEmpty() {
		out = append(out, s1)
	}
	if leatherCount > 0 {
		if s2 := world.NewNamedItemStack("minecraft:leather", leatherCount); !s2.IsEmpty() {
			out = append(out, s2)
		}
	}
	return out
}

// RollZoglinDeathLoot rolls vanilla 26.1.2 `minecraft:zoglin` death loot:
// `1..3` `minecraft:rotten_flesh` (+ `0..lootingLevel`).
func RollZoglinDeathLoot(rng *rand.Rand, lootingLevel int) []world.ItemStack {
	if lootingLevel < 0 {
		lootingLevel = 0
	}
	count := int32(1)
	if rng != nil {
		count = int32(1 + rng.Intn(3)) // 1..3
		if lootingLevel > 0 {
			count += int32(rng.Intn(lootingLevel + 1))
		}
	}
	if s1 := world.NewNamedItemStack("minecraft:rotten_flesh", count); !s1.IsEmpty() {
		return []world.ItemStack{s1}
	}
	return nil
}

// IsHoglinRepellentBlock reports whether blockName repels hoglins in vanilla 26.1.2
// (`minecraft:warped_fungus`, `minecraft:potted_warped_fungus`,
// `minecraft:nether_portal`, `minecraft:respawn_anchor`).
func IsHoglinRepellentBlock(blockName string) bool {
	switch canonicalResourceName(blockName) {
	case "minecraft:warped_fungus", "minecraft:potted_warped_fungus",
		"minecraft:nether_portal", "minecraft:respawn_anchor":
		return true
	default:
		return false
	}
}

// HasHoglinRepellentNear checks whether any hoglin repellent block
// (`warped_fungus`, `potted_warped_fungus`, `nether_portal`, `respawn_anchor`)
// or `minecraft:armor_stand` entity is within `radius` (default 7) of (x, y, z)
// in dimension, returning the nearest repellent coordinates.
func (s *Server) HasHoglinRepellentNear(dimension string, x, y, z float64, radius int) (rx, ry, rz float64, found bool) {
	if s == nil {
		return 0, 0, 0, false
	}
	if radius <= 0 {
		radius = HoglinRepellentRadius
	}
	normDim := world.NormalizeDimension(dimension)
	cache := s.DimensionChunks(normDim)
	bestDistSq := float64(radius*radius) + 1.0

	if cache != nil {
		cx, cy, cz := int(math.Floor(x)), int(math.Floor(y)), int(math.Floor(z))
		for dy := -3; dy <= 3; dy++ {
			by := cy + dy
			if by < world.MinY || by >= world.MinY+world.WorldHeight {
				continue
			}
			for dx := -radius; dx <= radius; dx++ {
				bx := cx + dx
				for dz := -radius; dz <= radius; dz++ {
					bz := cz + dz
					if !IsHoglinRepellentBlock(world.BlockName(cache.GetBlock(bx, by, bz))) {
						continue
					}
					fx, fy, fz := float64(bx)+0.5, float64(by)+0.5, float64(bz)+0.5
					dSq := (fx-x)*(fx-x) + (fy-y)*(fy-y) + (fz-z)*(fz-z)
					if dSq <= float64(radius*radius) && dSq < bestDistSq {
						bestDistSq = dSq
						rx, ry, rz = fx, fy, fz
						found = true
					}
				}
			}
		}
	}

	if s.entities != nil {
		allEnts := s.entities.AppendAll(nil)
		maxDistSq := float64(radius * radius)
		for i := range allEnts {
			ent := &allEnts[i]
			if ent.EffectiveDimension() != normDim || canonicalResourceName(ent.TypeName) != EntityTypeArmorStand {
				continue
			}
			dSq := (ent.X-x)*(ent.X-x) + (ent.Y-y)*(ent.Y-y) + (ent.Z-z)*(ent.Z-z)
			if dSq <= maxDistSq && dSq < bestDistSq {
				bestDistSq = dSq
				rx, ry, rz = ent.X, ent.Y, ent.Z
				found = true
			}
		}
	}
	return rx, ry, rz, found
}

// InteractWithHoglin handles feeding a `minecraft:hoglin` with
// `minecraft:crimson_fungus` to breed or accelerate baby growth (only when not
// panicking from a nearby repellent).
func (s *Server) InteractWithHoglin(session *PlayerSession, entityID int32, rng *rand.Rand) (handled bool, offspringID int32) {
	if s == nil || s.entities == nil || session == nil || session.IsDead() {
		return false, 0
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeHoglin {
		return false, 0
	}
	held := session.HeldItem()
	if held.IsEmpty() || canonicalResourceName(held.ResolvedID()) != "minecraft:crimson_fungus" {
		return false, 0
	}
	if _, _, _, repelled := s.HasHoglinRepellentNear(ent.EffectiveDimension(), ent.X, ent.Y, ent.Z, HoglinRepellentRadius); repelled {
		return false, 0
	}
	if s.IsMobBaby(entityID) {
		fed, _ := s.FeedBabyMob(session, entityID, "minecraft:crimson_fungus")
		return fed, 0
	}
	if s.MobBreedCooldown(entityID) <= 0 && s.MobInLoveTicks(entityID) <= 0 {
		consumeOneHeldItemInSurvival(session)
		s.SetMobInLove(entityID, AnimalLoveDurationTicks, session)
		offspringID, _ = s.TryBreedMobWithNearbyMate(entityID, AnimalBreedRange, rng)
		return true, offspringID
	}
	return false, 0
}

// --- Striders (minecraft:strider) ---

// EncodeStriderEntityDataPacket encodes a ClientboundSetEntityDataPacket (0x63)
// updating Strider.DATA_BOOST_TIME (17, INT), Strider.DATA_SUFFOCATING (18, BOOL),
// and Strider.DATA_SADDLE_ID (19, BOOL) for protocol 775.
func EncodeStriderEntityDataPacket(entityID int32, boostTime int32, shivering, saddled bool) []byte {
	w := protocol.NewWriter(24)
	w.VarInt(entityID)

	w.Byte(EntityDataStriderBoostTimeID)
	w.VarInt(EntityDataSerializerInt)
	w.VarInt(boostTime)

	w.Byte(EntityDataStriderSuffocatingID)
	w.VarInt(EntityDataSerializerBool)
	w.Bool(shivering)

	w.Byte(EntityDataStriderSaddleID)
	w.VarInt(EntityDataSerializerBool)
	w.Bool(saddled)

	w.Byte(0xFF)
	return w.Bytes()
}

// DecodeStriderEntityDataPacket decodes a ClientboundSetEntityDataPacket (0x63)
// produced by EncodeStriderEntityDataPacket.
func DecodeStriderEntityDataPacket(data []byte) (entityID, boostTime int32, shivering, saddled bool, err error) {
	r := protocol.NewReader(data)
	entityID, err = r.VarInt()
	if err != nil {
		return 0, 0, false, false, err
	}
	for {
		idx, err := r.ReadByte()
		if err != nil {
			return 0, 0, false, false, err
		}
		if idx == 0xFF {
			return entityID, boostTime, shivering, saddled, nil
		}
		ser, err := r.VarInt()
		if err != nil {
			return 0, 0, false, false, err
		}
		switch idx {
		case EntityDataStriderBoostTimeID:
			if ser != EntityDataSerializerInt {
				return 0, 0, false, false, errors.New("strider: unexpected boost_time serializer")
			}
			boostTime, err = r.VarInt()
		case EntityDataStriderSuffocatingID:
			if ser != EntityDataSerializerBool {
				return 0, 0, false, false, errors.New("strider: unexpected suffocating serializer")
			}
			shivering, err = r.Bool()
		case EntityDataStriderSaddleID:
			if ser != EntityDataSerializerBool {
				return 0, 0, false, false, errors.New("strider: unexpected saddle serializer")
			}
			saddled, err = r.Bool()
		default:
			return 0, 0, false, false, errors.New("strider: unexpected entity data index")
		}
		if err != nil {
			return 0, 0, false, false, err
		}
	}
}

// IsStriderOnLava reports whether the strider at (x, y, z) in cache is standing
// on or inside `minecraft:lava` (or `minecraft:lava_cauldron`).
func IsStriderOnLava(cache *world.Cache, x, y, z float64) bool {
	if cache == nil {
		return false
	}
	bx := int(math.Floor(x))
	by := int(math.Floor(y))
	bz := int(math.Floor(z))
	for _, checkY := range []int{by, int(math.Floor(y - 0.2)), by - 1} {
		if checkY < world.MinY || checkY >= world.MinY+world.WorldHeight {
			continue
		}
		name := world.BlockName(cache.GetBlock(bx, checkY, bz))
		if name == "minecraft:lava" || name == "minecraft:lava_cauldron" {
			return true
		}
	}
	return false
}

// SpawnStrider spawns a `minecraft:strider` (ID 129, 20.0 HP) at (x, y, z) in dimension.
func (s *Server) SpawnStrider(dimension string, x, y, z float64, baby bool) (int32, error) {
	if s == nil || s.entities == nil {
		return 0, errors.New("server: nil entity manager")
	}
	normDim := world.NormalizeDimension(dimension)
	typeID := ResolveEntityTypeID(MobTypeStrider)
	if typeID < 0 {
		typeID = EntityTypeStriderID
	}
	id := s.entities.Add(&world.Entity{
		TypeID:    typeID,
		TypeName:  MobTypeStrider,
		Dimension: normDim,
		X:         x,
		Y:         y,
		Z:         z,
		OnGround:  true,
	})

	tr := s.entityTracker()
	tr.mu.Lock()
	mem := tr.mobStateFor(id, tr.aiTickID)
	mem.healthInit = true
	mem.health = DefaultStriderMaxHealth
	mem.lastHealth = DefaultStriderMaxHealth
	if baby {
		mem.ageTicks = BabyMobGrowthTicks
	}
	tr.mu.Unlock()

	cache := s.DimensionChunks(normDim)
	shivering := !IsStriderOnLava(cache, x, y, z)

	nm := s.netherMobs()
	nm.mu.Lock()
	nm.striders[id] = &StriderState{
		EntityID:  id,
		Dimension: normDim,
		Shivering: shivering,
	}
	nm.mu.Unlock()

	if ent := s.entities.Get(id); ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, ent))
		if baby {
			s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, PlaySetEntityDataCB, EncodeAgeableMobDataPacket(id, true))
		}
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, PlaySetEntityDataCB,
			EncodeStriderEntityDataPacket(id, 0, shivering, false))
	}
	return id, nil
}

// SpawnStriderJockey spawns a `minecraft:strider` with a vanilla passenger
// (`riderType == MobTypeZombifiedPiglin` holding `minecraft:warped_fungus_on_a_stick`
// on a saddled Strider, or `riderType == MobTypeStrider` baby Strider passenger).
func (s *Server) SpawnStriderJockey(dimension string, x, y, z float64, riderType string, _ *rand.Rand) (striderID, riderID int32, err error) {
	striderID, err = s.SpawnStrider(dimension, x, y, z, false)
	if err != nil || striderID <= 0 {
		return 0, 0, err
	}
	normRider := canonicalResourceName(riderType)
	switch normRider {
	case MobTypeZombifiedPiglin:
		s.EquipStriderSaddle(striderID, true)
		riderID, err = s.SpawnZombifiedPiglin(dimension, x, y+1.1, z, false)
		if err != nil {
			return striderID, 0, err
		}
		stick := world.NewNamedItemStack("minecraft:warped_fungus_on_a_stick", 1)
		nm := s.netherMobs()
		nm.mu.Lock()
		if zp, ok := nm.zombifiedPiglins[riderID]; ok && zp != nil {
			zp.MainHandItem = stick
		}
		if st, ok := nm.striders[striderID]; ok && st != nil {
			st.RiderEntityID = riderID
		}
		nm.mu.Unlock()
		if rEnt := s.entities.Get(riderID); rEnt != nil {
			cp := rEnt.ChunkPos()
			s.BroadcastDimensionChunk(rEnt.EffectiveDimension(), cp.X, cp.Z, protocol.PlaySetEquipment,
				EncodePiglinEquipmentPacket(riderID, stick, world.ItemStack{}))
			s.BroadcastDimensionChunk(rEnt.EffectiveDimension(), cp.X, cp.Z, PlaySetPassengersCB,
				EncodeSetPassengersPacket(striderID, []int32{riderID}))
		}
		mgr := s.vehicleManager()
		mgr.mu.Lock()
		mgr.byPassengerEntity[riderID] = striderID
		mgr.mu.Unlock()
	case MobTypeStrider:
		riderID, err = s.SpawnStrider(dimension, x, y+1.1, z, true)
		if err != nil {
			return striderID, 0, err
		}
		nm := s.netherMobs()
		nm.mu.Lock()
		if st, ok := nm.striders[striderID]; ok && st != nil {
			st.RiderEntityID = riderID
		}
		nm.mu.Unlock()
		mgr := s.vehicleManager()
		mgr.mu.Lock()
		mgr.byPassengerEntity[riderID] = striderID
		mgr.mu.Unlock()
		if rEnt := s.entities.Get(riderID); rEnt != nil {
			cp := rEnt.ChunkPos()
			s.BroadcastDimensionChunk(rEnt.EffectiveDimension(), cp.X, cp.Z, PlaySetPassengersCB,
				EncodeSetPassengersPacket(striderID, []int32{riderID}))
		}
		s.UpdateStriderEnvironment(riderID)
	default:
		return striderID, 0, errors.New("strider: unsupported jockey rider type " + riderType)
	}
	return striderID, riderID, nil
}

func (s *Server) ensureStriderState(entityID int32) *StriderState {
	if s == nil || entityID <= 0 {
		return nil
	}
	nm := s.netherMobs()
	nm.mu.Lock()
	defer nm.mu.Unlock()
	st, ok := nm.striders[entityID]
	if !ok || st == nil {
		dim := world.DimensionNether
		if s.entities != nil {
			if ent := s.entities.Get(entityID); ent != nil {
				dim = ent.EffectiveDimension()
			}
		}
		st = &StriderState{
			EntityID:  entityID,
			Dimension: dim,
		}
		nm.striders[entityID] = st
	}
	return st
}

// GetStriderState returns a snapshot of the StriderState for entityID,
// synchronizing with any saddle equipped via a dispenser.
func (s *Server) GetStriderState(entityID int32) (StriderState, bool) {
	if s == nil || entityID <= 0 {
		return StriderState{}, false
	}
	st := s.ensureStriderState(entityID)
	if st == nil {
		return StriderState{}, false
	}
	dispSaddled := s.IsEntitySaddled(entityID)
	nm := s.netherMobs()
	nm.mu.Lock()
	if dispSaddled && !st.Saddled {
		st.Saddled = true
	}
	out := *st
	nm.mu.Unlock()
	return out, true
}

// IsStriderSaddled reports whether the strider with entityID has a saddle equipped.
func (s *Server) IsStriderSaddled(entityID int32) bool {
	st, ok := s.GetStriderState(entityID)
	return ok && st.Saddled
}

// IsStriderShivering reports whether the strider with entityID is currently
// cold / shivering outside lava (`Strider.DATA_SUFFOCATING`).
func (s *Server) IsStriderShivering(entityID int32) bool {
	st, ok := s.GetStriderState(entityID)
	return ok && st.Shivering
}

// EquipStriderSaddle equips or removes a saddle on an adult `minecraft:strider`,
// synchronizing with `taming.go` so `IsEntitySaddled` also reflects the change.
func (s *Server) EquipStriderSaddle(entityID int32, saddled bool) bool {
	if s == nil || s.entities == nil || entityID <= 0 {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeStrider {
		return false
	}
	if saddled && s.IsMobBaby(entityID) {
		return false
	}
	st := s.ensureStriderState(entityID)
	nm := s.netherMobs()
	nm.mu.Lock()
	st.Saddled = saddled
	boost := st.BoostTicks
	shiver := st.Shivering
	nm.mu.Unlock()

	tst := s.ensureTamedMobState(entityID, MobTypeStrider)
	mgr := s.tamedManager()
	mgr.mu.Lock()
	tst.Saddled = saddled
	mgr.mu.Unlock()

	dim := ent.EffectiveDimension()
	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
		EncodeStriderEntityDataPacket(entityID, boost, shiver, saddled))
	if saddled {
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
			EncodeSoundPacket(SoundStriderSaddle, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
	}
	return true
}

// UpdateStriderEnvironment updates a strider's lava-walking support and shivering
// (`DATA_SUFFOCATING`) flag based on the blocks at its current position (or any
// warm strider vehicle it is riding).
func (s *Server) UpdateStriderEnvironment(entityID int32) (onLava bool, shivering bool) {
	if s == nil || s.entities == nil || entityID <= 0 {
		return false, false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeStrider {
		return false, false
	}
	dim := ent.EffectiveDimension()
	cache := s.DimensionChunks(dim)
	onLava = IsStriderOnLava(cache, ent.X, ent.Y, ent.Z)

	// A baby strider riding on top of a warm adult strider does not shiver!
	ridingWarmStrider := false
	if vehID := s.EntityMountedVehicle(entityID); vehID > 0 {
		if vEnt := s.entities.Get(vehID); vEnt != nil && canonicalResourceName(vEnt.TypeName) == MobTypeStrider {
			if !s.IsStriderShivering(vehID) {
				ridingWarmStrider = true
			}
		}
	}
	shivering = !onLava && !ridingWarmStrider

	if onLava {
		// Float/stand on top of the lava surface.
		by := int(math.Floor(ent.Y))
		if cache != nil && world.BlockName(cache.GetBlock(int(math.Floor(ent.X)), by, int(math.Floor(ent.Z)))) == "minecraft:lava" {
			s.entities.Update(entityID, func(e *world.Entity) {
				e.Y = float64(by + 1)
				e.VelocityY = 0
				e.OnGround = true
			})
		}
	}

	st := s.ensureStriderState(entityID)
	nm := s.netherMobs()
	nm.mu.Lock()
	changed := st.Shivering != shivering
	st.Shivering = shivering
	boost := st.BoostTicks
	saddled := st.Saddled
	nm.mu.Unlock()

	if changed {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
			EncodeStriderEntityDataPacket(entityID, boost, shivering, saddled))
	}
	return onLava, shivering
}

// MountPlayerOnStrider mounts `session` onto a saddled adult strider.
func (s *Server) MountPlayerOnStrider(session *PlayerSession, striderID int32) bool {
	if s == nil || s.entities == nil || session == nil || session.IsDead() || striderID <= 0 {
		return false
	}
	ent := s.entities.Get(striderID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeStrider || s.IsMobBaby(striderID) || !s.IsStriderSaddled(striderID) {
		return false
	}
	st := s.ensureStriderState(striderID)
	nm := s.netherMobs()
	nm.mu.Lock()
	if st.RiderEntityID > 0 && st.RiderEntityID != session.EntityID {
		nm.mu.Unlock()
		return false
	}
	st.RiderEntityID = session.EntityID
	nm.mu.Unlock()

	_, _ = s.MountEntityOnVehicle(session.EntityID, striderID)
	return true
}

// SteerStriderWithWarpedFungusOnAStick moves a mounted, saddled strider in the
// rider's look direction when the rider is holding `minecraft:warped_fungus_on_a_stick`.
func (s *Server) SteerStriderWithWarpedFungusOnAStick(rider *PlayerSession, striderID int32) (moved bool, nextX, nextY, nextZ float64) {
	if s == nil || s.entities == nil || rider == nil || rider.IsDead() || striderID <= 0 {
		return false, 0, 0, 0
	}
	ent := s.entities.Get(striderID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeStrider || !s.IsStriderSaddled(striderID) {
		return false, 0, 0, 0
	}
	held := rider.HeldItem()
	off := rider.InventorySlot(45)
	holdingStick := (!held.IsEmpty() && canonicalResourceName(held.ResolvedID()) == "minecraft:warped_fungus_on_a_stick") ||
		(!off.IsEmpty() && canonicalResourceName(off.ResolvedID()) == "minecraft:warped_fungus_on_a_stick")
	if !holdingStick {
		return false, ent.X, ent.Y, ent.Z
	}

	onLava, _ := s.UpdateStriderEnvironment(striderID)
	st, _ := s.GetStriderState(striderID)
	speed := StriderLandSpeed
	if onLava {
		speed = StriderLavaSpeed
	}
	if st.BoostTicks > 0 {
		speed *= StriderBoostSpeedMult
	}

	rSnap := rider.Snapshot()
	yawRad := float64(rSnap.Yaw) * (math.Pi / 180.0)
	dx := -math.Sin(yawRad) * speed
	dz := math.Cos(yawRad) * speed
	nextX = ent.X + dx
	nextY = ent.Y
	nextZ = ent.Z + dz

	s.entities.Update(striderID, func(e *world.Entity) {
		e.X = nextX
		e.Y = nextY
		e.Z = nextZ
		e.Yaw = rSnap.Yaw
		e.HeadYaw = rSnap.Yaw
		e.OnGround = true
	})

	dim := ent.EffectiveDimension()
	distCm := int32(math.Round(math.Hypot(dx, dz) * 100.0))
	if distCm > 0 {
		s.AwardCustomStat(rider, StatStriderOneCm, distCm)
	}
	if dim == world.DimensionOverworld && onLava {
		s.GrantCriterion(rider, "minecraft:nether/ride_strider_in_overworld_lava", "ride_entity_distance")
	}
	return true, nextX, nextY, nextZ
}

// BoostStrider triggers a speed boost on a saddled strider when its rider uses
// `minecraft:warped_fungus_on_a_stick`, setting `DATA_BOOST_TIME` (`140..420` ticks),
// damaging the stick by 1 durability in Survival (reverting to `minecraft:fishing_rod`
// if broken), and granting `minecraft:nether/ride_strider`.
func (s *Server) BoostStrider(rider *PlayerSession, striderID int32, rng *rand.Rand) (boostTicks int32, ok bool) {
	if s == nil || s.entities == nil || striderID <= 0 {
		return 0, false
	}
	ent := s.entities.Get(striderID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeStrider || !s.IsStriderSaddled(striderID) {
		return 0, false
	}
	boostTicks = StriderMinBoostTicks
	if rng != nil {
		boostTicks += int32(rng.Intn(int(StriderMaxBoostTicks - StriderMinBoostTicks + 1)))
	}
	st := s.ensureStriderState(striderID)
	nm := s.netherMobs()
	nm.mu.Lock()
	st.BoostTicks = boostTicks
	st.TotalBoostTime = boostTicks
	shiver := st.Shivering
	saddled := st.Saddled
	nm.mu.Unlock()

	dim := ent.EffectiveDimension()
	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
		EncodeStriderEntityDataPacket(striderID, boostTicks, shiver, saddled))
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundStriderHappy, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
	if rider != nil {
		slot := rider.HeldInventorySlot()
		if slot >= 0 {
			held := rider.InventorySlot(slot)
			if !held.IsEmpty() && canonicalResourceName(held.ResolvedID()) == "minecraft:warped_fungus_on_a_stick" {
				if s.DamageHeldItem(rider, 1) {
					rider.SetInventorySlot(slot, world.NewNamedItemStack("minecraft:fishing_rod", 1))
				}
			}
		}
		s.GrantCriterion(rider, "minecraft:nether/ride_strider", "used_warped_fungus_on_a_stick")
	}
	return boostTicks, true
}

// InteractWithStrider handles right-clicking a `minecraft:strider`:
//   - Holding `minecraft:warped_fungus` -> baby growth or love mode (`minecraft:husbandry/breed_an_animal`).
//   - Holding `minecraft:saddle` on unsaddled adult -> equips saddle.
//   - Holding `minecraft:shears` on saddled unmounted strider -> removes and drops saddle.
//   - Holding `minecraft:warped_fungus_on_a_stick` while riding -> triggers speed boost.
//   - Otherwise on a saddled adult strider -> mounts the player onto the strider.
func (s *Server) InteractWithStrider(session *PlayerSession, entityID int32, rng *rand.Rand) (handled bool, offspringID int32) {
	if s == nil || s.entities == nil || session == nil || session.IsDead() {
		return false, 0
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeStrider {
		return false, 0
	}
	held := session.HeldItem()
	heldID := ""
	if !held.IsEmpty() {
		heldID = canonicalResourceName(held.ResolvedID())
	}

	// 1. Breeding or feeding baby strider with warped_fungus.
	if heldID == "minecraft:warped_fungus" {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, PlaySoundPacketID,
			EncodeSoundPacket(SoundStriderEat, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
		if s.IsMobBaby(entityID) {
			fed, _ := s.FeedBabyMob(session, entityID, heldID)
			return fed, 0
		}
		if s.MobBreedCooldown(entityID) <= 0 && s.MobInLoveTicks(entityID) <= 0 {
			consumeOneHeldItemInSurvival(session)
			s.SetMobInLove(entityID, AnimalLoveDurationTicks, session)
			offspringID, _ = s.TryBreedMobWithNearbyMate(entityID, AnimalBreedRange, rng)
			return true, offspringID
		}
		return true, 0
	}

	// 2. Equipping saddle on adult strider.
	if heldID == "minecraft:saddle" && !s.IsMobBaby(entityID) && !s.IsStriderSaddled(entityID) {
		if s.EquipStriderSaddle(entityID, true) {
			consumeOneHeldItemInSurvival(session)
			return true, 0
		}
	}

	// 3. Removing saddle with shears.
	if heldID == "minecraft:shears" && s.IsStriderSaddled(entityID) {
		if s.EquipStriderSaddle(entityID, false) {
			s.SpawnItemEntityInDimension(ent.EffectiveDimension(), ent.X, ent.Y+0.5, ent.Z, world.NewNamedItemStack("minecraft:saddle", 1))
			return true, 0
		}
	}

	// 4. Boosting strider with warped_fungus_on_a_stick.
	if heldID == "minecraft:warped_fungus_on_a_stick" && s.IsStriderSaddled(entityID) {
		if _, ok := s.BoostStrider(session, entityID, rng); ok {
			return true, 0
		}
	}

	// 5. Mounting a saddled adult strider.
	if s.IsStriderSaddled(entityID) && !s.IsMobBaby(entityID) {
		if s.MountPlayerOnStrider(session, entityID) {
			return true, 0
		}
	}
	return false, 0
}

// OnStriderKilled cleans up state when a Strider dies, dismounting any passenger
// and returning `minecraft:saddle` if the Strider was saddled.
func (s *Server) OnStriderKilled(entityID int32) []world.ItemStack {
	if s == nil || entityID <= 0 {
		return nil
	}
	saddled := s.IsStriderSaddled(entityID)
	nm := s.netherMobs()
	nm.mu.Lock()
	st, ok := nm.striders[entityID]
	var riderID int32
	if ok && st != nil {
		riderID = st.RiderEntityID
		if st.Saddled {
			saddled = true
		}
	}
	delete(nm.striders, entityID)
	nm.mu.Unlock()

	if riderID > 0 {
		_ = s.DismountEntityFromVehicle(riderID)
	}
	if saddled {
		if stack := world.NewNamedItemStack("minecraft:saddle", 1); !stack.IsEmpty() {
			return []world.ItemStack{stack}
		}
	}
	return nil
}

// RollStriderDeathLoot rolls vanilla 26.1.2 `minecraft:strider` death loot:
// `2..5` `minecraft:string` (+ `0..lootingLevel` bonus) + `1` `minecraft:saddle` if `saddled == true`.
func RollStriderDeathLoot(rng *rand.Rand, lootingLevel int, saddled bool) []world.ItemStack {
	if lootingLevel < 0 {
		lootingLevel = 0
	}
	stringsCount := int32(2)
	if rng != nil {
		stringsCount = int32(2 + rng.Intn(4)) // 2..5
		if lootingLevel > 0 {
			stringsCount += int32(rng.Intn(lootingLevel + 1))
		}
	}
	var out []world.ItemStack
	if s1 := world.NewNamedItemStack("minecraft:string", stringsCount); !s1.IsEmpty() {
		out = append(out, s1)
	}
	if saddled {
		if s2 := world.NewNamedItemStack("minecraft:saddle", 1); !s2.IsEmpty() {
			out = append(out, s2)
		}
	}
	return out
}

// --- Per-Tick AI for Piglins, Piglin Brutes, Zombified Piglins, Hoglins, Zoglins & Striders ---

// TickPiglinsHoglinsAndStriders steps all active piglins, piglin brutes,
// zombified piglins, hoglins, zoglins, and striders each server tick.
func (s *Server) TickPiglinsHoglinsAndStriders(rng *rand.Rand) {
	if s == nil || s.entities == nil {
		return
	}
	nm := s.netherMobs()
	nm.mu.RLock()
	piglinIDs := make([]int32, 0, len(nm.piglins))
	for id := range nm.piglins {
		piglinIDs = append(piglinIDs, id)
	}
	zombifiedIDs := make([]int32, 0, len(nm.zombifiedPiglins))
	for id := range nm.zombifiedPiglins {
		zombifiedIDs = append(zombifiedIDs, id)
	}
	hoglinIDs := make([]int32, 0, len(nm.hoglins))
	for id := range nm.hoglins {
		hoglinIDs = append(hoglinIDs, id)
	}
	striderIDs := make([]int32, 0, len(nm.striders))
	for id := range nm.striders {
		striderIDs = append(striderIDs, id)
	}
	nm.mu.RUnlock()

	for _, id := range piglinIDs {
		s.tickSinglePiglinOrBrute(id, rng)
	}
	for _, id := range zombifiedIDs {
		s.tickSingleZombifiedPiglin(id)
	}
	for _, id := range hoglinIDs {
		s.tickSingleHoglinOrZoglin(id, rng)
	}
	for _, id := range striderIDs {
		s.tickSingleStrider(id, rng)
	}
}

func (s *Server) tickSinglePiglinOrBrute(entityID int32, rng *rand.Rand) {
	ent := s.entities.Get(entityID)
	nm := s.netherMobs()
	nm.mu.Lock()
	ps, ok := nm.piglins[entityID]
	if !ok || ps == nil || ent == nil {
		delete(nm.piglins, entityID)
		nm.mu.Unlock()
		return
	}
	if ps.AttackCooldown > 0 {
		ps.AttackCooldown--
	}
	if ps.HuntCooldownTicks > 0 {
		ps.HuntCooldownTicks--
	}
	if ps.AngerTicks > 0 {
		ps.AngerTicks--
		if ps.AngerTicks == 0 {
			ps.AngerTarget = nil
			ps.AngerMobID = 0
		}
	}
	danceEnded := false
	if ps.DanceTicks > 0 {
		ps.DanceTicks--
		if ps.DanceTicks == 0 {
			ps.Dancing = false
			danceEnded = true
		}
	}

	dim := ent.EffectiveDimension()
	ps.Dimension = dim

	// 1. Zombification outside the Nether (Overworld / End) after 300 ticks (15s).
	if dim != world.DimensionNether && !ps.ImmuneToZombification {
		ps.TimeInOverworld++
		if ps.TimeInOverworld >= PiglinZombificationTicks {
			nm.mu.Unlock()
			s.ZombifyPiglin(entityID)
			return
		}
	} else {
		ps.TimeInOverworld = 0
	}

	// 2. Admiring / Bartering timer.
	if ps.AdmireTicks > 0 {
		ps.AdmireTicks--
		if ps.AdmireTicks == 0 {
			wasBarter := ps.Bartering || canonicalResourceName(ps.OffHandItem.ResolvedID()) == "minecraft:gold_ingot"
			if wasBarter {
				nm.mu.Unlock()
				_, _, _, _ = s.CompletePiglinBarter(entityID, rng)
				return
			}
			// Non-ingot loved gold item: pocket into Piglin's 8-slot inventory.
			if !ps.OffHandItem.IsEmpty() && len(ps.Inventory) < PiglinInventorySlots {
				ps.Inventory = append(ps.Inventory, ps.OffHandItem)
			}
			mainH := ps.MainHandItem
			ps.OffHandItem = world.ItemStack{}
			ps.Bartering = false
			ps.BarterPlayer = nil
			nm.mu.Unlock()
			cp := ent.ChunkPos()
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlaySetEquipment,
				EncodePiglinEquipmentPacket(entityID, mainH, world.ItemStack{}))
			return
		}
		nm.mu.Unlock()
		return
	}
	snap := *ps
	nm.mu.Unlock()

	if danceEnded {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
			EncodePiglinEntityDataPacket(entityID, snap.ImmuneToZombification, snap.Baby, snap.ChargingCrossbow, false))
	}
	if snap.Dancing {
		return
	}

	cache := s.DimensionChunks(dim)
	feetY := int(math.Floor(ent.Y + 0.01))

	// 3. Standard piglins retreat from soul fire/torches/lanterns/campfires and zombified piglins/zoglins.
	if snap.TypeName == MobTypePiglin && s.EntityMountedVehicle(entityID) == 0 {
		if rx, _, rz, repelled := s.HasPiglinRepellentNear(dim, ent.X, ent.Y, ent.Z, PiglinRepellentRadius); repelled {
			nm.mu.Lock()
			if cur, ok := nm.piglins[entityID]; ok && cur != nil {
				cur.Retreating = true
			}
			nm.mu.Unlock()
			awayX := ent.X - rx
			awayZ := ent.Z - rz
			dist := math.Hypot(awayX, awayZ)
			if dist < 1e-4 {
				awayX = 1.0
				dist = 1.0
			}
			goalX := ent.X + (awayX/dist)*4.0
			goalZ := ent.Z + (awayZ/dist)*4.0
			s.stepEntityTowardPoint(ent, cache, feetY, goalX, ent.Y, goalZ, PiglinChaseSpeed, 0.2)
			return
		}
		nm.mu.Lock()
		if cur, ok := nm.piglins[entityID]; ok && cur != nil {
			cur.Retreating = false
		}
		nm.mu.Unlock()
	}

	// 4. Standard piglins seek and pick up dropped gold items within 16 blocks.
	if snap.TypeName == MobTypePiglin {
		allEnts := s.entities.AppendAll(nil)
		for i := range allEnts {
			it := &allEnts[i]
			if !it.IsItem() || it.EffectiveDimension() != dim || it.PickupDelay > 0 || !IsPiglinLovedGoldItem(it.Item.ResolvedID()) {
				continue
			}
			dx := it.X - ent.X
			dy := it.Y - ent.Y
			dz := it.Z - ent.Z
			distSq := dx*dx + dy*dy + dz*dz
			if distSq <= 2.25 {
				if s.TryPiglinPickUpGoldItem(entityID, it.Item, nil) {
					if it.Item.Count <= 1 {
						s.entities.Remove(it.ID)
						cp := it.ChunkPos()
						s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayRemoveEntities,
							encodeRemoveEntitiesPacket(nil, []int32{it.ID}))
					} else {
						s.entities.Update(it.ID, func(e *world.Entity) {
							e.Item.Count--
						})
					}
					return
				}
			} else if distSq <= PiglinGoldSearchRange*PiglinGoldSearchRange {
				s.stepEntityTowardPoint(ent, cache, feetY, it.X, it.Y, it.Z, PiglinChaseSpeed, 1.0)
				return
			}
		}
	}

	if snap.Baby {
		_, _ = s.TryBabyPiglinMountBabyHoglin(entityID)
		return
	}

	// 5. Mob target acquisition (Hunted Hoglins, Wither Skeletons, Withers):
	targetMobID := snap.AngerMobID
	if targetMobID > 0 {
		if mEnt := s.entities.Get(targetMobID); mEnt == nil || mEnt.EffectiveDimension() != dim {
			targetMobID = 0
			nm.mu.Lock()
			if cur, ok := nm.piglins[entityID]; ok && cur != nil {
				cur.AngerMobID = 0
			}
			nm.mu.Unlock()
		}
	}
	if targetMobID == 0 {
		// Check for Wither Skeleton or Wither within 16 blocks.
		allEnts := s.entities.AppendAll(nil)
		bestDistSq := PiglinAggroRange*PiglinAggroRange + 1.0
		for i := range allEnts {
			other := &allEnts[i]
			if other.ID == entityID || other.EffectiveDimension() != dim {
				continue
			}
			oNorm := canonicalResourceName(other.TypeName)
			if oNorm != MobTypeWitherSkeleton && oNorm != MobTypeWither {
				continue
			}
			dx := other.X - ent.X
			dy := other.Y - ent.Y
			dz := other.Z - ent.Z
			dSq := dx*dx + dy*dy + dz*dz
			if dSq <= PiglinAggroRange*PiglinAggroRange && dSq < bestDistSq {
				bestDistSq = dSq
				targetMobID = other.ID
			}
		}
	}
	if targetMobID == 0 && snap.TypeName == MobTypePiglin && !snap.CannotHunt && snap.HuntCooldownTicks <= 0 {
		if huntedID, ok := s.TryPiglinStartHoglinHunt(entityID); ok {
			targetMobID = huntedID
		}
	}

	if targetMobID > 0 {
		if targetMob := s.entities.Get(targetMobID); targetMob != nil {
			speed := PiglinChaseSpeed
			dmg := PiglinAttackDamage
			if snap.TypeName == MobTypePiglinBrute {
				speed = PiglinBruteSpeed
				dmg = PiglinBruteAttackDmg
			}
			s.stepEntityTowardPoint(ent, cache, feetY, targetMob.X, targetMob.Y, targetMob.Z, speed, 1.3)
			dx := targetMob.X - ent.X
			dy := targetMob.Y - ent.Y
			dz := targetMob.Z - ent.Z
			if dx*dx+dy*dy+dz*dz <= 4.5 && snap.AttackCooldown <= 0 {
				nm.mu.Lock()
				if cur, ok := nm.piglins[entityID]; ok && cur != nil {
					cur.AttackCooldown = 20
				}
				nm.mu.Unlock()
				s.applyDamageToMob(targetMob, dmg, nil, 0.4, false, false)
			}
			return
		}
	}

	// 6. Player target acquisition:
	//    - Piglin Brutes attack any survival/adventure player within 16 blocks regardless of gold armor.
	//    - Standard Piglins attack players within 16 blocks who are NOT wearing gold armor, OR who provoked them (AngerTarget).
	targetPlayer := snap.AngerTarget
	if targetPlayer == nil || targetPlayer.IsDead() {
		bestDistSq := PiglinAggroRange*PiglinAggroRange + 1.0
		for _, p := range s.onlinePlayersSnapshot() {
			if p == nil || p.IsDead() {
				continue
			}
			pSnap := p.Snapshot()
			if pSnap.GameMode == GameModeCreative || pSnap.GameMode == GameModeSpectator || world.NormalizeDimension(pSnap.Dimension) != dim {
				continue
			}
			if snap.TypeName == MobTypePiglin && IsPlayerWearingGoldArmor(p) {
				continue
			}
			dx := pSnap.X - ent.X
			dy := pSnap.Y - ent.Y
			dz := pSnap.Z - ent.Z
			dSq := dx*dx + dy*dy + dz*dz
			if dSq <= PiglinAggroRange*PiglinAggroRange && dSq < bestDistSq {
				bestDistSq = dSq
				targetPlayer = p
			}
		}
		// When a Piglin Brute spots an intruder in its Bastion/Fortress, it rallies nearby Piglins!
		if targetPlayer != nil && snap.TypeName == MobTypePiglinBrute {
			s.OnPiglinOrBruteDamaged(entityID, targetPlayer, 0)
		}
	}

	if targetPlayer != nil && !targetPlayer.IsDead() {
		pSnap := targetPlayer.Snapshot()
		hasCrossbow := canonicalResourceName(snap.MainHandItem.ResolvedID()) == "minecraft:crossbow"
		dx := pSnap.X - ent.X
		dy := pSnap.Y - ent.Y
		dz := pSnap.Z - ent.Z
		distSq := dx*dx + dy*dy + dz*dz

		if hasCrossbow && snap.TypeName == MobTypePiglin {
			if distSq > 64.0 {
				s.stepEntityTowardPoint(ent, cache, feetY, pSnap.X, pSnap.Y, pSnap.Z, PiglinChaseSpeed, 6.0)
			}
			if !snap.ChargingCrossbow && snap.AttackCooldown <= 0 {
				s.SetPiglinChargingCrossbow(entityID, true)
			} else if snap.ChargingCrossbow {
				nm.mu.Lock()
				chargeTicks := 0
				if cur, ok := nm.piglins[entityID]; ok && cur != nil {
					cur.CrossbowChargeTicks++
					chargeTicks = cur.CrossbowChargeTicks
				}
				nm.mu.Unlock()
				if chargeTicks >= PiglinCrossbowChargeTicks {
					s.SetPiglinChargingCrossbow(entityID, false)
					nm.mu.Lock()
					if cur, ok := nm.piglins[entityID]; ok && cur != nil {
						cur.AttackCooldown = 30
					}
					nm.mu.Unlock()
					s.AttackPlayerFromMob(entityID, MobTypePiglin, ent.X, ent.Z, targetPlayer, 4.0)
				}
			}
			return
		}

		speed := PiglinChaseSpeed
		dmg := PiglinAttackDamage
		if snap.TypeName == MobTypePiglinBrute {
			speed = PiglinBruteSpeed
			dmg = PiglinBruteAttackDmg
		}
		s.stepEntityTowardPoint(ent, cache, feetY, pSnap.X, pSnap.Y, pSnap.Z, speed, 1.2)
		if distSq <= 4.0 && snap.AttackCooldown <= 0 {
			nm.mu.Lock()
			if cur, ok := nm.piglins[entityID]; ok && cur != nil {
				cur.AttackCooldown = 20
			}
			nm.mu.Unlock()
			s.AttackPlayerFromMob(entityID, snap.TypeName, ent.X, ent.Z, targetPlayer, dmg)
		}
		return
	}

	// 7. Idle Piglin Brute returns toward its Bastion/Fortress home position if displaced.
	if snap.HasHome {
		hdx := snap.HomeX - ent.X
		hdz := snap.HomeZ - ent.Z
		if hdx*hdx+hdz*hdz > 36.0 {
			s.stepEntityTowardPoint(ent, cache, feetY, snap.HomeX, snap.HomeY, snap.HomeZ, PiglinWanderSpeed, 1.5)
		}
	}
}

func (s *Server) tickSingleZombifiedPiglin(entityID int32) {
	ent := s.entities.Get(entityID)
	nm := s.netherMobs()
	nm.mu.Lock()
	zp, ok := nm.zombifiedPiglins[entityID]
	if !ok || zp == nil || ent == nil {
		delete(nm.zombifiedPiglins, entityID)
		nm.mu.Unlock()
		return
	}
	if zp.AttackCooldown > 0 {
		zp.AttackCooldown--
	}
	if zp.AngerTicks > 0 {
		zp.AngerTicks--
		if zp.AngerTicks == 0 {
			zp.AngerTarget = nil
			zp.AngerMobID = 0
		}
	}
	snap := *zp
	nm.mu.Unlock()

	if snap.AngerTarget == nil || snap.AngerTarget.IsDead() || snap.AngerTicks <= 0 {
		return
	}
	dim := ent.EffectiveDimension()
	pSnap := snap.AngerTarget.Snapshot()
	if pSnap.GameMode == GameModeCreative || pSnap.GameMode == GameModeSpectator || world.NormalizeDimension(pSnap.Dimension) != dim {
		return
	}
	cache := s.DimensionChunks(dim)
	feetY := int(math.Floor(ent.Y + 0.01))
	s.stepEntityTowardPoint(ent, cache, feetY, pSnap.X, pSnap.Y, pSnap.Z, ZombifiedPiglinChaseSpeed, 1.2)
	dx := pSnap.X - ent.X
	dy := pSnap.Y - ent.Y
	dz := pSnap.Z - ent.Z
	if dx*dx+dy*dy+dz*dz <= 4.0 && snap.AttackCooldown <= 0 {
		nm.mu.Lock()
		if cur, ok := nm.zombifiedPiglins[entityID]; ok && cur != nil {
			cur.AttackCooldown = 20
		}
		nm.mu.Unlock()
		s.AttackPlayerFromMob(entityID, MobTypeZombifiedPiglin, ent.X, ent.Z, snap.AngerTarget, ZombifiedPiglinAttackDamage)
	}
}

func (s *Server) tickSingleHoglinOrZoglin(entityID int32, _ *rand.Rand) {
	ent := s.entities.Get(entityID)
	nm := s.netherMobs()
	nm.mu.Lock()
	hs, ok := nm.hoglins[entityID]
	if !ok || hs == nil || ent == nil {
		delete(nm.hoglins, entityID)
		nm.mu.Unlock()
		return
	}
	if hs.AttackCooldown > 0 {
		hs.AttackCooldown--
	}

	dim := ent.EffectiveDimension()
	hs.Dimension = dim

	// 1. Hoglin zombification into Zoglin outside the Nether after 300 ticks (15s).
	if hs.TypeName == MobTypeHoglin && dim != world.DimensionNether && !hs.ImmuneToZombification {
		hs.TimeInOverworld++
		if hs.TimeInOverworld >= HoglinZombificationTicks {
			nm.mu.Unlock()
			s.ZombifyHoglin(entityID)
			return
		}
	} else {
		hs.TimeInOverworld = 0
	}
	snap := *hs
	nm.mu.Unlock()

	cache := s.DimensionChunks(dim)
	feetY := int(math.Floor(ent.Y + 0.01))

	// 2. Hoglin panic / retreat from warped_fungus, nether_portal, respawn_anchor, or armor_stand within 7 blocks.
	if snap.TypeName == MobTypeHoglin {
		if rx, _, rz, repelled := s.HasHoglinRepellentNear(dim, ent.X, ent.Y, ent.Z, HoglinRepellentRadius); repelled {
			nm.mu.Lock()
			hs.Panicking = true
			hs.RepellentX = rx
			hs.RepellentZ = rz
			nm.mu.Unlock()

			awayX := ent.X - rx
			awayZ := ent.Z - rz
			dist := math.Hypot(awayX, awayZ)
			if dist < 1e-4 {
				awayX = 1.0
				dist = 1.0
			}
			goalX := ent.X + (awayX/dist)*4.0
			goalZ := ent.Z + (awayZ/dist)*4.0
			s.stepEntityTowardPoint(ent, cache, feetY, goalX, ent.Y, goalZ, HoglinRetreatSpeed, 0.2)
			return
		}
		nm.mu.Lock()
		hs.Panicking = false
		nm.mu.Unlock()
	}

	// 3. Zoglins attack nearby non-zoglin/non-creeper mobs as well as players.
	if snap.TypeName == MobTypeZoglin {
		allEnts := s.entities.AppendAll(nil)
		for i := range allEnts {
			other := &allEnts[i]
			if other.ID == entityID || other.IsItem() || other.IsExperienceOrb() || other.EffectiveDimension() != dim {
				continue
			}
			oNorm := canonicalResourceName(other.TypeName)
			if oNorm == MobTypeZoglin || oNorm == MobTypeCreeper || IsDecorationEntity(oNorm) {
				continue
			}
			dx := other.X - ent.X
			dy := other.Y - ent.Y
			dz := other.Z - ent.Z
			if dx*dx+dy*dy+dz*dz <= 256.0 {
				s.stepEntityTowardPoint(ent, cache, feetY, other.X, other.Y, other.Z, ZoglinChaseSpeed, 1.3)
				if dx*dx+dy*dy+dz*dz <= 4.5 && snap.AttackCooldown <= 0 {
					nm.mu.Lock()
					hs.AttackCooldown = 20
					nm.mu.Unlock()
					dmg := ZoglinAttackDamage
					if snap.Baby {
						dmg = HoglinBabyAttackDmg
					}
					s.applyDamageToMob(other, dmg, nil, 0.5, false, false)
				}
				return
			}
		}
	}

	// 4. Hoglins & Zoglins chase and fling survival/adventure players within 16 blocks.
	for _, p := range s.onlinePlayersSnapshot() {
		if p == nil || p.IsDead() {
			continue
		}
		pSnap := p.Snapshot()
		if pSnap.GameMode == GameModeCreative || pSnap.GameMode == GameModeSpectator || world.NormalizeDimension(pSnap.Dimension) != dim {
			continue
		}
		dx := pSnap.X - ent.X
		dy := pSnap.Y - ent.Y
		dz := pSnap.Z - ent.Z
		if dx*dx+dy*dy+dz*dz <= 256.0 {
			s.stepEntityTowardPoint(ent, cache, feetY, pSnap.X, pSnap.Y, pSnap.Z, HoglinChaseSpeed, 1.3)
			if dx*dx+dy*dy+dz*dz <= 4.5 && snap.AttackCooldown <= 0 {
				nm.mu.Lock()
				hs.AttackCooldown = 20
				nm.mu.Unlock()
				dmg := HoglinAttackDamage
				if snap.Baby {
					dmg = HoglinBabyAttackDmg
				}
				dealt := s.AttackPlayerFromMob(entityID, snap.TypeName, ent.X, ent.Z, p, dmg)
				if dealt > 0 && !snap.Baby && p.send != nil {
					_ = p.send(protocol.PlaySetEntityMotion, EncodeSetEntityMotionPacket(p.EntityID, 0, HoglinFlingVelocityY, 0))
				}
			}
			return
		}
	}
}

func (s *Server) tickSingleStrider(entityID int32, _ *rand.Rand) {
	ent := s.entities.Get(entityID)
	nm := s.netherMobs()
	nm.mu.Lock()
	st, ok := nm.striders[entityID]
	if !ok || st == nil || ent == nil {
		delete(nm.striders, entityID)
		nm.mu.Unlock()
		return
	}
	if st.BoostTicks > 0 {
		st.BoostTicks--
	}
	if st.WaterDamageTimer > 0 {
		st.WaterDamageTimer--
	}
	waterTimer := st.WaterDamageTimer
	riderID := st.RiderEntityID
	nm.mu.Unlock()

	dim := ent.EffectiveDimension()
	// Striders are sensitive to water and rain (1.0 HP damage every 20 ticks).
	if s.IsEndermanInWaterOrRain(dim, ent.X, ent.Y, ent.Z) && waterTimer <= 0 {
		nm.mu.Lock()
		if cur, ok := nm.striders[entityID]; ok && cur != nil {
			cur.WaterDamageTimer = StriderWaterDamageIntervalTicks
		}
		nm.mu.Unlock()
		if s.applyDamageToMob(ent, StriderWaterDamage, nil, 0, false, false) {
			return
		}
	}

	s.UpdateStriderEnvironment(entityID)
	if riderID > 0 {
		if rider := s.playerByEntityID(riderID); rider != nil && !rider.IsDead() {
			s.SteerStriderWithWarpedFungusOnAStick(rider, entityID)
		}
	}
}
