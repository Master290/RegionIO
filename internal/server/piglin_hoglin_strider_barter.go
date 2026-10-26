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

	// Zombification & Bartering timers (ticks at 20 TPS).
	PiglinZombificationTicks = 300 // 15 seconds outside the Nether
	HoglinZombificationTicks = 300 // 15 seconds outside the Nether
	PiglinBarterAdmireTicks  = 120 // 6 seconds admiring a gold ingot before throwing barter loot
	PiglinAngerDurationTicks = 600 // 30 seconds anger when gold is mined / chest opened / attacked

	// Movement speeds & combat parameters.
	PiglinWanderSpeed      = 0.10
	PiglinChaseSpeed       = 0.16
	PiglinAttackDamage     = float32(5.0)
	PiglinBruteSpeed       = 0.17
	PiglinBruteAttackDmg   = float32(7.0)
	PiglinAggroRange       = 16.0
	PiglinGoldSearchRange  = 16.0
	PiglinCelebrateTicks   = 60

	HoglinWanderSpeed     = 0.09
	HoglinChaseSpeed      = 0.16
	HoglinRetreatSpeed    = 0.18
	HoglinAttackDamage    = float32(6.0)
	HoglinFlingVelocityY  = 0.40
	HoglinRepellentRadius = 7

	ZoglinChaseSpeed   = 0.17
	ZoglinAttackDamage = float32(6.0)

	StriderLandSpeed      = 0.09
	StriderLavaSpeed      = 0.175
	StriderBoostSpeedMult = 1.55
	StriderMinBoostTicks  = 140
	StriderMaxBoostTicks  = 420

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

// PiglinState holds per-entity AI, zombification, bartering, and combat state
// for a `minecraft:piglin` or `minecraft:piglin_brute`.
type PiglinState struct {
	EntityID              int32
	TypeName              string
	Dimension             string
	Baby                  bool
	ImmuneToZombification bool
	TimeInOverworld       int
	ChargingCrossbow      bool
	Dancing               bool
	DanceTicks            int
	MainHandItem          world.ItemStack
	OffHandItem           world.ItemStack
	AdmireTicks           int
	Bartering             bool
	BarterPlayer          *PlayerSession
	AngerTarget           *PlayerSession
	AngerMobID            int32
	AngerTicks            int
	AttackCooldown        int
	CannotHunt            bool
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
}

// StriderState holds per-entity lava-walking, shivering, saddle, and fungus-boost
// state for a `minecraft:strider`.
type StriderState struct {
	EntityID       int32
	Dimension      string
	Saddled        bool
	Shivering      bool
	BoostTicks     int32
	TotalBoostTime int32
	RiderEntityID  int32
}

type netherMobManager struct {
	mu       sync.RWMutex
	piglins  map[int32]*PiglinState
	hoglins  map[int32]*HoglinState
	striders map[int32]*StriderState
}

var serverNetherMobManagers sync.Map // map[*Server]*netherMobManager

func (s *Server) netherMobs() *netherMobManager {
	if v, ok := serverNetherMobManagers.Load(s); ok {
		return v.(*netherMobManager)
	}
	actual, _ := serverNetherMobManagers.LoadOrStore(s, &netherMobManager{
		piglins:  make(map[int32]*PiglinState),
		hoglins:  make(map[int32]*HoglinState),
		striders: make(map[int32]*StriderState),
	})
	return actual.(*netherMobManager)
}

// --- Piglins & Piglin Brutes ---

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

// SpawnPiglin spawns a `minecraft:piglin` (ID 101, 16.0 HP) at (x, y, z) in dimension.
func (s *Server) SpawnPiglin(dimension string, x, y, z float64, baby bool) (int32, error) {
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

	weapon := world.NewNamedItemStack("minecraft:golden_sword", 1)
	if baby {
		weapon = world.ItemStack{}
	}
	nm := s.netherMobs()
	nm.mu.Lock()
	nm.piglins[id] = &PiglinState{
		EntityID:     id,
		TypeName:     MobTypePiglin,
		Dimension:    normDim,
		Baby:         baby,
		MainHandItem: weapon,
	}
	nm.mu.Unlock()

	if ent := s.entities.Get(id); ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, ent))
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, PlaySetEntityDataCB,
			EncodePiglinEntityDataPacket(id, false, baby, false, false))
	}
	return id, nil
}

// SpawnPiglinBrute spawns a `minecraft:piglin_brute` (ID 102, 50.0 HP, armed with
// `minecraft:golden_axe`) at (x, y, z) in dimension.
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

	nm := s.netherMobs()
	nm.mu.Lock()
	nm.piglins[id] = &PiglinState{
		EntityID:     id,
		TypeName:     MobTypePiglinBrute,
		Dimension:    normDim,
		MainHandItem: world.NewNamedItemStack("minecraft:golden_axe", 1),
		CannotHunt:   true,
	}
	nm.mu.Unlock()

	if ent := s.entities.Get(id); ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, ent))
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, PlaySetEntityDataCB,
			EncodePiglinBruteEntityDataPacket(id, false))
	}
	return id, nil
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
	}
	nm.mu.RUnlock()
	return out, ok && ps != nil
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

// ZombifyPiglin converts a `minecraft:piglin` or `minecraft:piglin_brute` into a
// `minecraft:zombified_piglin` at the same position, preserving baby state and
// applying Nausea (`200` ticks) per vanilla `AbstractPiglin.finishConversion`.
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

	if zEnt := s.entities.Get(zombifiedID); zEnt != nil {
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, zEnt))
		if ps.Baby {
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB, EncodeAgeableMobDataPacket(zombifiedID, true))
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
			nm.mu.Unlock()
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
	nm.mu.Unlock()

	if player != nil && wasAngryAtPlayer {
		s.GrantCriterion(player, "minecraft:nether/distract_piglin", "distract_piglin")
	}
	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundPiglinAdmiringItem, SoundSourceHostile, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
	return true
}

// CompletePiglinBarter finishes an active piglin barter (or forces immediate
// completion), consuming the held `minecraft:gold_ingot`, rolling an item from
// `gameplay/piglin_bartering`, and spawning the bartered item entity in the world.
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
	ps.OffHandItem = world.ItemStack{}
	ps.AdmireTicks = 0
	ps.Bartering = false
	ps.BarterPlayer = nil
	nm.mu.Unlock()

	barteredStack, barteredMeta, _ = RollPiglinBarterLoot(rng)
	dim := ent.EffectiveDimension()
	droppedEntityID, _ = s.SpawnItemEntityInDimension(dim, ent.X, ent.Y+0.5, ent.Z, barteredStack)
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
// (`DATA_SUFFOCATING`) flag based on the blocks at its current position.
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
	shivering = !onLava

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
// `minecraft:warped_fungus_on_a_stick`, setting `DATA_BOOST_TIME` (`140..420` ticks)
// and granting `minecraft:nether/ride_strider`.
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

// --- Per-Tick AI for Piglins, Piglin Brutes, Hoglins, Zoglins & Striders ---

// TickPiglinsHoglinsAndStriders steps all active piglins, piglin brutes,
// hoglins, zoglins, and striders each server tick.
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
	if ps.AngerTicks > 0 {
		ps.AngerTicks--
		if ps.AngerTicks == 0 {
			ps.AngerTarget = nil
			ps.AngerMobID = 0
		}
	}
	if ps.DanceTicks > 0 {
		ps.DanceTicks--
		if ps.DanceTicks == 0 {
			ps.Dancing = false
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
			wasBarter := ps.Bartering
			ps.OffHandItem = world.ItemStack{}
			ps.Bartering = false
			ps.BarterPlayer = nil
			nm.mu.Unlock()
			if wasBarter {
				barteredStack, _, _, _ := s.CompletePiglinBarter(entityID, rng)
				if barteredStack.IsEmpty() {
					bStack, _, _ := RollPiglinBarterLoot(rng)
					s.SpawnItemEntityInDimension(dim, ent.X, ent.Y+0.5, ent.Z, bStack)
				}
			}
			return
		}
		nm.mu.Unlock()
		return
	}
	snap := *ps
	nm.mu.Unlock()

	cache := s.DimensionChunks(dim)
	feetY := int(math.Floor(ent.Y + 0.01))

	// 3. Standard piglins seek and pick up dropped gold items within 16 blocks.
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
		return
	}

	// 4. Target acquisition:
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
	}

	if targetPlayer != nil && !targetPlayer.IsDead() {
		pSnap := targetPlayer.Snapshot()
		speed := PiglinChaseSpeed
		dmg := PiglinAttackDamage
		if snap.TypeName == MobTypePiglinBrute {
			speed = PiglinBruteSpeed
			dmg = PiglinBruteAttackDmg
		}
		s.stepEntityTowardPoint(ent, cache, feetY, pSnap.X, pSnap.Y, pSnap.Z, speed, 1.2)
		dx := pSnap.X - ent.X
		dy := pSnap.Y - ent.Y
		dz := pSnap.Z - ent.Z
		if dx*dx+dy*dy+dz*dz <= 4.0 && snap.AttackCooldown <= 0 {
			nm.mu.Lock()
			ps.AttackCooldown = 20
			nm.mu.Unlock()
			s.AttackPlayerFromMob(entityID, snap.TypeName, ent.X, ent.Z, targetPlayer, dmg)
		}
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
					s.applyDamageToMob(other, ZoglinAttackDamage, nil, 0.5, false, false)
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
				dealt := s.AttackPlayerFromMob(entityID, snap.TypeName, ent.X, ent.Z, p, HoglinAttackDamage)
				if dealt > 0 && p.send != nil {
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
	riderID := st.RiderEntityID
	nm.mu.Unlock()

	s.UpdateStriderEnvironment(entityID)
	if riderID > 0 {
		if rider := s.playerByEntityID(riderID); rider != nil && !rider.IsDead() {
			s.SteerStriderWithWarpedFungusOnAStick(rider, entityID)
		}
	}
}
