package server

import (
	"math"
	"math/rand"
	"strings"
	"sync"

	"regionio/internal/protocol"
	"regionio/internal/registry"
	"regionio/internal/world"
)

// Wolf, Cat, and Parrot variants, wolf armor constants, and SynchedEntityData
// indices for Minecraft 26.1.2 (protocol 775).
const (
	// Wolf Armor item & durability constants (vanilla 26.1.2).
	WolfArmorItemID                  = "minecraft:wolf_armor"
	WolfArmorMaxDurability     int32 = 64
	WolfArmorScuteRepairAmount int32 = 8 // 12.5% of 64 = 8 durability restored per armadillo_scute
	DefaultWolfArmorRGB        int32 = -1

	// SynchedEntityData indices for Wolf, Cat, and Parrot in 26.1.2 (protocol 775).
	EntityDataWolfVariantID         byte  = 22 // Wolf.DATA_VARIANT_ID (WOLF_VARIANT serializer = 22)
	EntityDataSerializerWolfVariant int32 = 22

	EntityDataCatVariantID         byte  = 18 // Cat.DATA_VARIANT_ID (CAT_VARIANT serializer = 23)
	EntityDataCatIsLyingID         byte  = 19 // Cat.IS_LYING (BOOLEAN serializer = 8)
	EntityDataCatRelaxStateOneID   byte  = 20 // Cat.RELAX_STATE_ONE (BOOLEAN serializer = 8)
	EntityDataCatCollarColor26ID   byte  = 21 // Cat.DATA_COLLAR_COLOR (INT serializer = 1)
	EntityDataSerializerCatVariant int32 = 23

	EntityDataParrotVariantID byte = 19 // Parrot.DATA_VARIANT_ID (INT serializer = 1: 0..4)

	// Cat & Ocelot repulsion radii.
	CatCreeperRepelRange      = 6.0
	CatPhantomRepelRange      = 16.0
	CatMorningGiftSearchRange = 10.0
	CatMorningGiftChance      = 0.70

	// Parrot behavior radii.
	ParrotJukeboxDanceRadius = 3.46
	ParrotImitateMobRadius   = 20.0
	ParrotCookiePoisonTicks  = 900

	// Canonical 26.1.2 Wolf variants (minecraft:wolf_variant registry).
	WolfVariantAshen    = "minecraft:ashen"
	WolfVariantBlack    = "minecraft:black"
	WolfVariantChestnut = "minecraft:chestnut"
	WolfVariantPale     = "minecraft:pale"
	WolfVariantRusty    = "minecraft:rusty"
	WolfVariantSnowy    = "minecraft:snowy"
	WolfVariantSpotted  = "minecraft:spotted"
	WolfVariantStriped  = "minecraft:striped"
	WolfVariantWoods    = "minecraft:woods"

	// Canonical 26.1.2 Cat variants (minecraft:cat_variant registry).
	CatVariantAllBlack         = "minecraft:all_black"
	CatVariantBlack            = "minecraft:black"
	CatVariantBritishShorthair = "minecraft:british_shorthair"
	CatVariantCalico           = "minecraft:calico"
	CatVariantJellie           = "minecraft:jellie"
	CatVariantPersian          = "minecraft:persian"
	CatVariantRagdoll          = "minecraft:ragdoll"
	CatVariantRed              = "minecraft:red"
	CatVariantSiamese          = "minecraft:siamese"
	CatVariantTabby            = "minecraft:tabby"
	CatVariantWhite            = "minecraft:white"

	// Parrot variant ordinals (0..4).
	ParrotVariantRedBlue    int32 = 0
	ParrotVariantBlue       int32 = 1
	ParrotVariantGreen      int32 = 2
	ParrotVariantYellowBlue int32 = 3
	ParrotVariantCyan       int32 = 3
	ParrotVariantGray       int32 = 4

	// Canonical sounds for Wolf Armor, Cats, and Parrots.
	SoundWolfArmorEquip  = "minecraft:item.armor.equip_wolf"
	SoundWolfArmorUnequip = "minecraft:item.armor.unequip_wolf"
	SoundWolfArmorCrack  = "minecraft:item.wolf_armor.crack"
	SoundWolfArmorDamage = "minecraft:item.wolf_armor.damage"
	SoundWolfArmorBreak  = "minecraft:item.wolf_armor.break"
	SoundWolfArmorRepair = "minecraft:item.wolf_armor.repair"
	SoundCatHiss         = "minecraft:entity.cat.hiss"
	SoundCatPurr         = "minecraft:entity.cat.purr"
	SoundCatBegForFood   = "minecraft:entity.cat.beg_for_food"
	SoundParrotEat       = "minecraft:entity.parrot.eat"
)

var allWolfVariants = []string{
	WolfVariantAshen,
	WolfVariantBlack,
	WolfVariantChestnut,
	WolfVariantPale,
	WolfVariantRusty,
	WolfVariantSnowy,
	WolfVariantSpotted,
	WolfVariantStriped,
	WolfVariantWoods,
}

var allCatVariants = []string{
	CatVariantAllBlack,
	CatVariantBlack,
	CatVariantBritishShorthair,
	CatVariantCalico,
	CatVariantJellie,
	CatVariantPersian,
	CatVariantRagdoll,
	CatVariantRed,
	CatVariantSiamese,
	CatVariantTabby,
	CatVariantWhite,
}

var catMorningGiftLootItems = []string{
	"minecraft:rabbit_foot",
	"minecraft:rabbit_hide",
	"minecraft:string",
	"minecraft:rotten_flesh",
	"minecraft:feather",
	"minecraft:chicken",
	"minecraft:phantom_membrane",
}

// WolfArmorCrackLevel represents the crack stage of a wolf's equipped wolf_armor.
type WolfArmorCrackLevel uint8

const (
	WolfArmorCrackNone   WolfArmorCrackLevel = 0 // remaining durability > 60 (damage < 4)
	WolfArmorCrackLow    WolfArmorCrackLevel = 1 // remaining durability <= 60 (damage >= 4)
	WolfArmorCrackMedium WolfArmorCrackLevel = 2 // remaining durability <= 44 (damage >= 20)
	WolfArmorCrackHigh   WolfArmorCrackLevel = 3 // remaining durability <= 20 (damage >= 44)
)

// WolfArmorCrackLevelForDamage returns the vanilla Crackiness.WOLF_ARMOR stage
// for the given damage value out of WolfArmorMaxDurability (64).
func WolfArmorCrackLevelForDamage(damage int32) WolfArmorCrackLevel {
	rem := WolfArmorMaxDurability - damage
	switch {
	case rem <= 20:
		return WolfArmorCrackHigh
	case rem <= 44:
		return WolfArmorCrackMedium
	case rem <= 60:
		return WolfArmorCrackLow
	default:
		return WolfArmorCrackNone
	}
}

// WolfDetailState holds biome variant and wolf_armor state for a wolf entity.
type WolfDetailState struct {
	EntityID   int32
	Variant    string
	HasArmor   bool
	ArmorStack world.ItemStack
	ArmorMeta  ItemMeta
	DyedRGB    int32 // -1 if undyed, 0xRRGGBB if dyed
}

// CatDetailState holds variant, lying/relaxed state, and gift cooldown for a cat entity.
type CatDetailState struct {
	EntityID       int32
	Variant        string
	Lying          bool
	Relaxed        bool
	LastGiftDay    int64
}

// ParrotDetailState holds color variant, dancing state, and imitation cooldown for a parrot entity.
type ParrotDetailState struct {
	EntityID        int32
	Variant         int32
	Dancing         bool
	ImitateCooldown int
	LastImitatedMob string
}

type petDetailManager struct {
	mu                sync.RWMutex
	wolves            map[int32]*WolfDetailState
	cats              map[int32]*CatDetailState
	parrots           map[int32]*ParrotDetailState
	shoulderVariants  map[[16]byte][2]int32            // ownerUUID -> [leftVariant, rightVariant]
	tamedCatCatalogue map[[16]byte]map[string]struct{} // ownerUUID -> set of tamed cat variants
}

var serverPetDetailManagers sync.Map // map[*Server]*petDetailManager

func (s *Server) petDetails() *petDetailManager {
	if v, ok := serverPetDetailManagers.Load(s); ok {
		return v.(*petDetailManager)
	}
	actual, _ := serverPetDetailManagers.LoadOrStore(s, &petDetailManager{
		wolves:            make(map[int32]*WolfDetailState),
		cats:              make(map[int32]*CatDetailState),
		parrots:           make(map[int32]*ParrotDetailState),
		shoulderVariants:  make(map[[16]byte][2]int32),
		tamedCatCatalogue: make(map[[16]byte]map[string]struct{}),
	})
	return actual.(*petDetailManager)
}

// --- Wolf Variants & Wolf Armor ---

// AllWolfVariants returns a copy of all 9 vanilla 26.1.2 wolf variant identifiers.
func AllWolfVariants() []string {
	out := make([]string, len(allWolfVariants))
	copy(out, allWolfVariants)
	return out
}

// ResolveWolfVariantForBiome maps a biome identifier to its vanilla 26.1.2
// `minecraft:wolf_variant` identifier (defaulting to `minecraft:pale`).
func ResolveWolfVariantForBiome(biomeName string) string {
	switch canonicalResourceName(biomeName) {
	case "minecraft:snowy_taiga":
		return WolfVariantAshen
	case "minecraft:old_growth_pine_taiga":
		return WolfVariantBlack
	case "minecraft:old_growth_spruce_taiga":
		return WolfVariantChestnut
	case "minecraft:sparse_jungle", "minecraft:jungle", "minecraft:bamboo_jungle":
		return WolfVariantRusty
	case "minecraft:grove", "minecraft:snowy_plains", "minecraft:snowy_slopes", "minecraft:frozen_peaks", "minecraft:jagged_peaks":
		return WolfVariantSnowy
	case "minecraft:savanna", "minecraft:savanna_plateau", "minecraft:windswept_savanna":
		return WolfVariantSpotted
	case "minecraft:wooded_badlands", "minecraft:badlands", "minecraft:eroded_badlands":
		return WolfVariantStriped
	case "minecraft:forest", "minecraft:flower_forest", "minecraft:birch_forest", "minecraft:dark_forest":
		return WolfVariantWoods
	case "minecraft:taiga":
		return WolfVariantPale
	default:
		return WolfVariantPale
	}
}

// WolfVariantRegistryIndex returns the network registry index (0..8) for a
// `minecraft:wolf_variant` identifier in protocol 775.
func WolfVariantRegistryIndex(variant string) int32 {
	norm := canonicalResourceName(variant)
	if idx := registry.Index("minecraft:wolf_variant", norm); idx >= 0 {
		return int32(idx)
	}
	for i, v := range allWolfVariants {
		if v == norm {
			return int32(i)
		}
	}
	return 3 // minecraft:pale
}

// EncodeWolfVariantDataPacket encodes a ClientboundSetEntityDataPacket (0x63)
// updating Wolf.DATA_VARIANT_ID (index 22, WOLF_VARIANT serializer 22).
func EncodeWolfVariantDataPacket(entityID int32, variant string) []byte {
	w := protocol.NewWriter(12)
	w.VarInt(entityID)
	w.Byte(EntityDataWolfVariantID)
	w.VarInt(EntityDataSerializerWolfVariant)
	w.VarInt(WolfVariantRegistryIndex(variant))
	w.Byte(0xFF)
	return w.Bytes()
}

func inferBiomeAtPosition(cache *world.Cache, bx, by, bz int) string {
	if cache == nil {
		return "minecraft:taiga"
	}
	hasSnow := false
	hasPodzol := false
	hasSpruce := false
	hasJungle := false
	hasSavanna := false
	hasBadlands := false
	hasForest := false
	for dy := -2; dy <= 3; dy++ {
		y := by + dy
		if y < world.MinY || y >= world.MinY+world.WorldHeight {
			continue
		}
		for dx := -2; dx <= 2; dx++ {
			for dz := -2; dz <= 2; dz++ {
				name := world.BlockName(cache.GetBlock(bx+dx, y, bz+dz))
				switch name {
				case "minecraft:snow", "minecraft:snow_block", "minecraft:powder_snow", "minecraft:ice", "minecraft:packed_ice":
					hasSnow = true
				case "minecraft:podzol":
					hasPodzol = true
				case "minecraft:spruce_log", "minecraft:spruce_leaves":
					hasSpruce = true
				case "minecraft:jungle_log", "minecraft:jungle_leaves", "minecraft:bamboo":
					hasJungle = true
				case "minecraft:acacia_log", "minecraft:acacia_leaves":
					hasSavanna = true
				case "minecraft:red_sand", "minecraft:terracotta", "minecraft:red_terracotta", "minecraft:orange_terracotta":
					hasBadlands = true
				case "minecraft:oak_log", "minecraft:oak_leaves", "minecraft:birch_log", "minecraft:birch_leaves":
					hasForest = true
				}
			}
		}
	}
	switch {
	case hasSnow && hasSpruce:
		return "minecraft:snowy_taiga"
	case hasSnow:
		return "minecraft:grove"
	case hasPodzol && hasSpruce:
		return "minecraft:old_growth_spruce_taiga"
	case hasPodzol:
		return "minecraft:old_growth_pine_taiga"
	case hasJungle:
		return "minecraft:sparse_jungle"
	case hasSavanna:
		return "minecraft:savanna_plateau"
	case hasBadlands:
		return "minecraft:wooded_badlands"
	case hasForest:
		return "minecraft:forest"
	default:
		return "minecraft:taiga"
	}
}

func (s *Server) ensureWolfDetailState(entityID int32) *WolfDetailState {
	if s == nil || entityID <= 0 {
		return nil
	}
	pm := s.petDetails()
	pm.mu.Lock()
	defer pm.mu.Unlock()
	ws, ok := pm.wolves[entityID]
	if !ok || ws == nil {
		variant := WolfVariantPale
		if s.entities != nil {
			if ent := s.entities.Get(entityID); ent != nil {
				if cache := s.DimensionChunks(ent.EffectiveDimension()); cache != nil {
					biome := inferBiomeAtPosition(cache, int(math.Floor(ent.X)), int(math.Floor(ent.Y)), int(math.Floor(ent.Z)))
					variant = ResolveWolfVariantForBiome(biome)
				}
			}
		}
		ws = &WolfDetailState{
			EntityID: entityID,
			Variant:  variant,
			DyedRGB:  DefaultWolfArmorRGB,
		}
		pm.wolves[entityID] = ws
	}
	return ws
}

// GetWolfDetailState returns a snapshot of the WolfDetailState for entityID,
// synchronizing with any wolf_armor equipped via a dispenser.
func (s *Server) GetWolfDetailState(entityID int32) (WolfDetailState, bool) {
	if s == nil || entityID <= 0 {
		return WolfDetailState{}, false
	}
	ws := s.ensureWolfDetailState(entityID)
	if ws == nil {
		return WolfDetailState{}, false
	}
	dispArmor := s.WolfArmor(entityID)
	pm := s.petDetails()
	pm.mu.Lock()
	if !ws.HasArmor && !dispArmor.IsEmpty() {
		ws.HasArmor = true
		ws.ArmorStack = dispArmor
	}
	out := *ws
	out.ArmorMeta = ws.ArmorMeta.Clone()
	pm.mu.Unlock()
	return out, true
}

// WolfVariant returns the `minecraft:wolf_variant` identifier for entityID.
func (s *Server) WolfVariant(entityID int32) string {
	ws, ok := s.GetWolfDetailState(entityID)
	if !ok || ws.Variant == "" {
		return WolfVariantPale
	}
	return ws.Variant
}

// SetWolfVariant sets the `minecraft:wolf_variant` identifier for entityID and
// broadcasts the updated SynchedEntityData packet.
func (s *Server) SetWolfVariant(entityID int32, variant string) bool {
	if s == nil || s.entities == nil || entityID <= 0 {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeWolf {
		return false
	}
	norm := canonicalResourceName(variant)
	valid := false
	for _, v := range allWolfVariants {
		if v == norm {
			valid = true
			break
		}
	}
	if !valid {
		norm = ResolveWolfVariantForBiome(norm)
	}
	ws := s.ensureWolfDetailState(entityID)
	pm := s.petDetails()
	pm.mu.Lock()
	ws.Variant = norm
	pm.mu.Unlock()

	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, PlaySetEntityDataCB,
		EncodeWolfVariantDataPacket(entityID, norm))
	return true
}

// HasWolfArmor reports whether the wolf with entityID is currently wearing wolf_armor.
func (s *Server) HasWolfArmor(entityID int32) bool {
	ws, ok := s.GetWolfDetailState(entityID)
	return ok && ws.HasArmor && !ws.ArmorStack.IsEmpty()
}

const wolfArmorDyedColorKey = "minecraft:dyed_color"

// WolfArmorDyedRGB extracts the dyed RGB color from a `minecraft:wolf_armor` ItemMeta,
// returning (rgb, true) if dyed or (-1, false) if undyed.
func WolfArmorDyedRGB(meta ItemMeta) (int32, bool) {
	if len(meta.Enchantments) == 0 {
		return DefaultWolfArmorRGB, false
	}
	if v, ok := meta.Enchantments[wolfArmorDyedColorKey]; ok && v > 0 {
		return int32(v - 1), true
	}
	return DefaultWolfArmorRGB, false
}

func setWolfArmorDyedRGBOnMeta(meta *ItemMeta, rgb int32) {
	if meta == nil {
		return
	}
	if rgb < 0 {
		if meta.Enchantments != nil {
			delete(meta.Enchantments, wolfArmorDyedColorKey)
		}
		return
	}
	if meta.Enchantments == nil {
		meta.Enchantments = make(map[string]int)
	}
	meta.Enchantments[wolfArmorDyedColorKey] = int(rgb) + 1
}

// EquipWolfArmor equips `minecraft:wolf_armor` onto a tamed adult wolf that does
// not already have armor equipped.
func (s *Server) EquipWolfArmor(entityID int32, stack world.ItemStack, meta ItemMeta) bool {
	if s == nil || s.entities == nil || entityID <= 0 {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeWolf {
		return false
	}
	if !s.IsMobTamed(entityID) || s.IsMobBaby(entityID) || s.HasWolfArmor(entityID) {
		return false
	}
	if stack.IsEmpty() || canonicalResourceName(stack.ResolvedID()) != WolfArmorItemID {
		stack = world.NewNamedItemStack(WolfArmorItemID, 1)
	} else {
		stack = world.NewItemStack(stack.ResolvedItemID(), 1)
	}
	rgb, _ := WolfArmorDyedRGB(meta)
	ws := s.ensureWolfDetailState(entityID)
	pm := s.petDetails()
	pm.mu.Lock()
	ws.HasArmor = true
	ws.ArmorStack = stack
	ws.ArmorMeta = meta.Clone()
	ws.DyedRGB = rgb
	pm.mu.Unlock()

	s.SetWolfArmor(entityID, stack)
	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundWolfArmorEquip, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
	return true
}

// RemoveWolfArmorWithShears removes equipped `minecraft:wolf_armor` from a tamed
// wolf using `minecraft:shears`, dropping the armor item with its current Damage
// and DyedRGB, damaging the shears by 1 in Survival, and granting the
// `minecraft:husbandry/remove_wolf_armor` advancement criterion.
func (s *Server) RemoveWolfArmorWithShears(session *PlayerSession, entityID int32) (droppedStack world.ItemStack, droppedMeta ItemMeta, ok bool) {
	if s == nil || s.entities == nil || entityID <= 0 {
		return world.ItemStack{}, ItemMeta{}, false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeWolf {
		return world.ItemStack{}, ItemMeta{}, false
	}
	if session != nil {
		if ownerUUID, tamed := s.MobOwnerUUID(entityID); !tamed || ownerUUID != session.Profile.UUID {
			return world.ItemStack{}, ItemMeta{}, false
		}
	}
	ws := s.ensureWolfDetailState(entityID)
	dispArmor := s.WolfArmor(entityID)
	pm := s.petDetails()
	pm.mu.Lock()
	if !ws.HasArmor && !dispArmor.IsEmpty() {
		ws.HasArmor = true
		ws.ArmorStack = dispArmor
	}
	if !ws.HasArmor || ws.ArmorStack.IsEmpty() {
		pm.mu.Unlock()
		return world.ItemStack{}, ItemMeta{}, false
	}
	droppedStack = ws.ArmorStack
	droppedMeta = ws.ArmorMeta.Clone()
	if ws.DyedRGB >= 0 {
		setWolfArmorDyedRGBOnMeta(&droppedMeta, ws.DyedRGB)
	}
	ws.HasArmor = false
	ws.ArmorStack = world.ItemStack{}
	ws.ArmorMeta = ItemMeta{}
	ws.DyedRGB = DefaultWolfArmorRGB
	pm.mu.Unlock()

	s.SetWolfArmor(entityID, world.ItemStack{})
	dim := ent.EffectiveDimension()
	cp := ent.ChunkPos()
	s.SpawnItemEntityInDimension(dim, ent.X, ent.Y+0.4, ent.Z, droppedStack)
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundWolfArmorUnequip, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
	if session != nil {
		s.GrantCriterion(session, "minecraft:husbandry/remove_wolf_armor", "remove_wolf_armor")
	}
	return droppedStack, droppedMeta, true
}

// RepairWolfArmorWithScute repairs damaged `minecraft:wolf_armor` on a tamed wolf
// by `WolfArmorScuteRepairAmount` (8 durability) when right-clicked with
// `minecraft:armadillo_scute`, consuming 1 scute in Survival and granting
// `minecraft:husbandry/repair_wolf_armor`.
func (s *Server) RepairWolfArmorWithScute(session *PlayerSession, entityID int32) (newDamage int32, repaired bool) {
	if s == nil || s.entities == nil || entityID <= 0 {
		return 0, false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeWolf {
		return 0, false
	}
	if session != nil {
		if ownerUUID, tamed := s.MobOwnerUUID(entityID); !tamed || ownerUUID != session.Profile.UUID {
			return 0, false
		}
	}
	ws := s.ensureWolfDetailState(entityID)
	pm := s.petDetails()
	pm.mu.Lock()
	if !ws.HasArmor || ws.ArmorStack.IsEmpty() || ws.ArmorMeta.Damage <= 0 {
		curDmg := int32(0)
		if ws != nil {
			curDmg = ws.ArmorMeta.Damage
		}
		pm.mu.Unlock()
		return curDmg, false
	}
	ws.ArmorMeta.Damage -= WolfArmorScuteRepairAmount
	if ws.ArmorMeta.Damage < 0 {
		ws.ArmorMeta.Damage = 0
	}
	newDamage = ws.ArmorMeta.Damage
	pm.mu.Unlock()

	if session != nil {
		consumeOneHeldItemInSurvival(session)
		s.GrantCriterion(session, "minecraft:husbandry/repair_wolf_armor", "repair_wolf_armor")
	}
	dim := ent.EffectiveDimension()
	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundWolfArmorRepair, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
	return newDamage, true
}

// DyeColorRGB returns the vanilla 24-bit RGB hex value for a 0..15 DyeColor index.
func DyeColorRGB(dyeIndex int32) (int32, bool) {
	switch dyeIndex {
	case 0: // white
		return 0xF9FFFE, true
	case 1: // orange
		return 0xF9801D, true
	case 2: // magenta
		return 0xC74EBD, true
	case 3: // light_blue
		return 0x3AB3DA, true
	case 4: // yellow
		return 0xFED83D, true
	case 5: // lime
		return 0x80C71F, true
	case 6: // pink
		return 0xF38BAA, true
	case 7: // gray
		return 0x474F52, true
	case 8: // light_gray
		return 0x9D9D97, true
	case 9: // cyan
		return 0x169C9C, true
	case 10: // purple
		return 0x8932B8, true
	case 11: // blue
		return 0x3C44AA, true
	case 12: // brown
		return 0x835432, true
	case 13: // green
		return 0x5E7C16, true
	case 14: // red
		return 0xB02E26, true
	case 15: // black
		return 0x1D1D21, true
	default:
		return -1, false
	}
}

// BlendWolfArmorDyeRGB blends an existing RGB color (-1 if undyed) with one or
// more 0..15 DyeColor indices using vanilla's `DyedItemColor.applyDyes` averaging.
func BlendWolfArmorDyeRGB(existingRGB int32, dyeIndices ...int32) (int32, bool) {
	if len(dyeIndices) == 0 {
		return existingRGB, false
	}
	var totalR, totalG, totalB, maxCompSum, count int32
	if existingRGB >= 0 {
		r := (existingRGB >> 16) & 0xFF
		g := (existingRGB >> 8) & 0xFF
		b := existingRGB & 0xFF
		totalR += r
		totalG += g
		totalB += b
		m := r
		if g > m {
			m = g
		}
		if b > m {
			m = b
		}
		maxCompSum += m
		count++
	}
	for _, idx := range dyeIndices {
		rgb, ok := DyeColorRGB(idx)
		if !ok {
			continue
		}
		r := (rgb >> 16) & 0xFF
		g := (rgb >> 8) & 0xFF
		b := rgb & 0xFF
		totalR += r
		totalG += g
		totalB += b
		m := r
		if g > m {
			m = g
		}
		if b > m {
			m = b
		}
		maxCompSum += m
		count++
	}
	if count == 0 {
		return existingRGB, false
	}
	avgR := totalR / count
	avgG := totalG / count
	avgB := totalB / count
	avgMax := float64(maxCompSum) / float64(count)
	curMax := float64(avgR)
	if float64(avgG) > curMax {
		curMax = float64(avgG)
	}
	if float64(avgB) > curMax {
		curMax = float64(avgB)
	}
	if curMax > 0 {
		avgR = int32(float64(avgR) * avgMax / curMax)
		avgG = int32(float64(avgG) * avgMax / curMax)
		avgB = int32(float64(avgB) * avgMax / curMax)
	}
	return (avgR << 16) | (avgG << 8) | avgB, true
}

// DyeWolfArmorItem applies one or more dye items to a `minecraft:wolf_armor`
// ItemMeta, updating its dyed RGB color.
func DyeWolfArmorItem(meta ItemMeta, dyeItemNames ...string) (ItemMeta, int32, bool) {
	var indices []int32
	for _, item := range dyeItemNames {
		if idx, ok := DyeColorIndexByItemName(item); ok {
			indices = append(indices, idx)
		}
	}
	if len(indices) == 0 {
		return meta, -1, false
	}
	existing, _ := WolfArmorDyedRGB(meta)
	blended, ok := BlendWolfArmorDyeRGB(existing, indices...)
	if !ok {
		return meta, existing, false
	}
	out := meta.Clone()
	setWolfArmorDyedRGBOnMeta(&out, blended)
	return out, blended, true
}

// DyeEquippedWolfArmor dyes the `minecraft:wolf_armor` currently worn by entityID.
func (s *Server) DyeEquippedWolfArmor(entityID int32, dyeColor int32) (int32, bool) {
	if s == nil || entityID <= 0 {
		return -1, false
	}
	ws := s.ensureWolfDetailState(entityID)
	pm := s.petDetails()
	pm.mu.Lock()
	defer pm.mu.Unlock()
	if !ws.HasArmor || ws.ArmorStack.IsEmpty() {
		return -1, false
	}
	blended, ok := BlendWolfArmorDyeRGB(ws.DyedRGB, dyeColor)
	if !ok {
		return ws.DyedRGB, false
	}
	ws.DyedRGB = blended
	setWolfArmorDyedRGBOnMeta(&ws.ArmorMeta, blended)
	return blended, true
}

// AbsorbWolfDamageWithArmor absorbs incoming damage on a tamed wolf wearing
// `minecraft:wolf_armor` in vanilla 26.1.2:
//   - Absorbs 100% of non-bypassing damage (`remainingHP = 0`).
//   - Applies `ceil(damage)` durability wear to the equipped `wolf_armor`.
//   - Plays `item.wolf_armor.crack` when crossing a crack stage (60 / 44 / 20 remaining).
//   - Shatters and removes the armor when `Damage >= 64` (`WolfArmorMaxDurability`).
func (s *Server) AbsorbWolfDamageWithArmor(entityID int32, rawDamage float32, bypassesWolfArmor bool) (absorbed bool, armorBroke bool, newArmorDamage int32) {
	if s == nil || entityID <= 0 || rawDamage <= 0 || bypassesWolfArmor {
		return false, false, 0
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeWolf {
		return false, false, 0
	}
	ws := s.ensureWolfDetailState(entityID)
	dispArmor := s.WolfArmor(entityID)

	pm := s.petDetails()
	pm.mu.Lock()
	if !ws.HasArmor && !dispArmor.IsEmpty() {
		ws.HasArmor = true
		ws.ArmorStack = dispArmor
	}
	if !ws.HasArmor || ws.ArmorStack.IsEmpty() {
		pm.mu.Unlock()
		return false, false, 0
	}
	wear := int32(math.Ceil(float64(rawDamage)))
	if wear < 1 {
		wear = 1
	}
	prevCrack := WolfArmorCrackLevelForDamage(ws.ArmorMeta.Damage)
	ws.ArmorMeta.Damage += wear
	newArmorDamage = ws.ArmorMeta.Damage
	nextCrack := WolfArmorCrackLevelForDamage(newArmorDamage)

	if newArmorDamage >= WolfArmorMaxDurability {
		ws.HasArmor = false
		ws.ArmorStack = world.ItemStack{}
		ws.ArmorMeta = ItemMeta{}
		ws.DyedRGB = DefaultWolfArmorRGB
		armorBroke = true
	}
	pm.mu.Unlock()

	dim := ent.EffectiveDimension()
	cp := ent.ChunkPos()
	if armorBroke {
		s.SetWolfArmor(entityID, world.ItemStack{})
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
			EncodeSoundPacket(SoundWolfArmorBreak, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
	} else {
		if nextCrack > prevCrack {
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
				EncodeSoundPacket(SoundWolfArmorCrack, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
		}
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
			EncodeSoundPacket(SoundWolfArmorDamage, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
	}
	return true, armorBroke, newArmorDamage
}

// --- Cat Variants, Morning Gifts & Repulsion ---

// AllCatVariants returns a copy of all 11 vanilla 26.1.2 cat variant identifiers.
func AllCatVariants() []string {
	out := make([]string, len(allCatVariants))
	copy(out, allCatVariants)
	return out
}

// CatVariantRegistryIndex returns the network registry index (0..10) for a
// `minecraft:cat_variant` identifier in protocol 775.
func CatVariantRegistryIndex(variant string) int32 {
	norm := canonicalResourceName(variant)
	if idx := registry.Index("minecraft:cat_variant", norm); idx >= 0 {
		return int32(idx)
	}
	for i, v := range allCatVariants {
		if v == norm {
			return int32(i)
		}
	}
	return 9 // minecraft:tabby
}

// RollCatSpawnVariant selects a cat variant according to vanilla 26.1.2 spawn rules:
//   - Swamp/witch hut spawns -> always `minecraft:all_black`.
//   - Full moon -> 50% chance `minecraft:all_black`, otherwise uniform across all 11 variants.
//   - Normal -> uniform across the first 10 non-all_black variants (or all 11 on full moon).
func RollCatSpawnVariant(inSwampHut, isFullMoon bool, rng *rand.Rand) string {
	if inSwampHut {
		return CatVariantAllBlack
	}
	if isFullMoon && (rng == nil || rng.Intn(2) == 0) {
		return CatVariantAllBlack
	}
	if rng == nil {
		return CatVariantTabby
	}
	if isFullMoon {
		return allCatVariants[rng.Intn(len(allCatVariants))]
	}
	// Vanilla non-full-moon village spawns exclude all_black (index 0).
	return allCatVariants[1+rng.Intn(len(allCatVariants)-1)]
}

// EncodeCatEntityDataPacket encodes a ClientboundSetEntityDataPacket (0x63)
// updating a cat's Tamable flags (17), CatVariant (18), Lying (19), Relaxed (20),
// and CollarColor (21) for protocol 775.
func EncodeCatEntityDataPacket(entityID int32, tamed, sitting bool, variant string, lying, relaxed bool, collarColor int32) []byte {
	var flags byte
	if sitting {
		flags |= TamableFlagSitting
	}
	if tamed {
		flags |= TamableFlagTamed
	}
	w := protocol.NewWriter(36)
	w.VarInt(entityID)

	w.Byte(EntityDataTamableFlagsID)
	w.VarInt(EntityDataSerializerByte)
	w.Byte(flags)

	w.Byte(EntityDataCatVariantID)
	w.VarInt(EntityDataSerializerCatVariant)
	w.VarInt(CatVariantRegistryIndex(variant))

	w.Byte(EntityDataCatIsLyingID)
	w.VarInt(EntityDataSerializerBool)
	w.Bool(lying)

	w.Byte(EntityDataCatRelaxStateOneID)
	w.VarInt(EntityDataSerializerBool)
	w.Bool(relaxed)

	w.Byte(EntityDataCatCollarColor26ID)
	w.VarInt(EntityDataSerializerInt)
	w.VarInt(collarColor)

	w.Byte(0xFF)
	return w.Bytes()
}

func (s *Server) ensureCatDetailState(entityID int32, rng *rand.Rand) *CatDetailState {
	if s == nil || entityID <= 0 {
		return nil
	}
	pm := s.petDetails()
	pm.mu.Lock()
	defer pm.mu.Unlock()
	cs, ok := pm.cats[entityID]
	if !ok || cs == nil {
		cs = &CatDetailState{
			EntityID:    entityID,
			Variant:     RollCatSpawnVariant(false, false, rng),
			LastGiftDay: -1,
		}
		pm.cats[entityID] = cs
	}
	return cs
}

// GetCatDetailState returns a snapshot of the CatDetailState for entityID.
func (s *Server) GetCatDetailState(entityID int32) (CatDetailState, bool) {
	if s == nil || entityID <= 0 {
		return CatDetailState{}, false
	}
	cs := s.ensureCatDetailState(entityID, nil)
	if cs == nil {
		return CatDetailState{}, false
	}
	pm := s.petDetails()
	pm.mu.RLock()
	out := *cs
	pm.mu.RUnlock()
	return out, true
}

// CatVariant returns the `minecraft:cat_variant` identifier for entityID.
func (s *Server) CatVariant(entityID int32) string {
	cs, ok := s.GetCatDetailState(entityID)
	if !ok || cs.Variant == "" {
		return CatVariantTabby
	}
	return cs.Variant
}

// SetCatVariant sets the `minecraft:cat_variant` identifier for entityID and
// broadcasts the updated SynchedEntityData packet.
func (s *Server) SetCatVariant(entityID int32, variant string) bool {
	if s == nil || s.entities == nil || entityID <= 0 {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeCat {
		return false
	}
	norm := canonicalResourceName(variant)
	valid := false
	for _, v := range allCatVariants {
		if v == norm {
			valid = true
			break
		}
	}
	if !valid {
		norm = CatVariantTabby
	}
	cs := s.ensureCatDetailState(entityID, nil)
	pm := s.petDetails()
	pm.mu.Lock()
	cs.Variant = norm
	lying := cs.Lying
	relaxed := cs.Relaxed
	pm.mu.Unlock()

	tst, _ := s.GetTamedMobState(entityID)
	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, PlaySetEntityDataCB,
		EncodeCatEntityDataPacket(entityID, tst.Tamed, tst.Sitting, norm, lying, relaxed, tst.CollarColor))
	return true
}

// SetCatLyingOnBed sets whether a tamed cat is lying down / relaxing near a sleeping owner.
func (s *Server) SetCatLyingOnBed(entityID int32, lying, relaxed bool) bool {
	if s == nil || s.entities == nil || entityID <= 0 {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeCat {
		return false
	}
	cs := s.ensureCatDetailState(entityID, nil)
	pm := s.petDetails()
	pm.mu.Lock()
	cs.Lying = lying
	cs.Relaxed = relaxed
	variant := cs.Variant
	pm.mu.Unlock()

	tst, _ := s.GetTamedMobState(entityID)
	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, PlaySetEntityDataCB,
		EncodeCatEntityDataPacket(entityID, tst.Tamed, tst.Sitting, variant, lying, relaxed, tst.CollarColor))
	return true
}

func (s *Server) recordCatVariantTamedByOwner(owner *PlayerSession, catEntityID int32) {
	if s == nil || owner == nil || catEntityID <= 0 {
		return
	}
	variant := s.CatVariant(catEntityID)
	short := strings.TrimPrefix(variant, "minecraft:")
	s.GrantCriterion(owner, "minecraft:husbandry/complete_catalogue", short)

	pm := s.petDetails()
	pm.mu.Lock()
	set := pm.tamedCatCatalogue[owner.Profile.UUID]
	if set == nil {
		set = make(map[string]struct{})
		pm.tamedCatCatalogue[owner.Profile.UUID] = set
	}
	set[variant] = struct{}{}
	count := len(set)
	pm.mu.Unlock()

	if count >= len(allCatVariants) {
		s.GrantCriterion(owner, "minecraft:husbandry/complete_catalogue", "all_cats")
	}
}

// TryDeliverCatMorningGift checks for a tamed, non-sitting cat belonging to
// owner within `CatMorningGiftSearchRange` (10 blocks) when the player wakes up
// in the morning. With a 70% probability (or 100% if `forceGift == true`), the
// cat drops 1 item from `gameplay/cat_morning_gift` at the player's position.
func (s *Server) TryDeliverCatMorningGift(owner *PlayerSession, forceGift bool, rng *rand.Rand) (giftItem world.ItemStack, droppedEntityID int32, delivered bool) {
	if s == nil || s.entities == nil || owner == nil || owner.IsDead() {
		return world.ItemStack{}, 0, false
	}
	oSnap := owner.Snapshot()
	dim := world.NormalizeDimension(oSnap.Dimension)
	maxDistSq := CatMorningGiftSearchRange * CatMorningGiftSearchRange

	allEnts := s.entities.AppendAll(nil)
	var chosenCat *world.Entity
	for i := range allEnts {
		ent := &allEnts[i]
		if ent.EffectiveDimension() != dim || canonicalResourceName(ent.TypeName) != MobTypeCat {
			continue
		}
		tst, ok := s.GetTamedMobState(ent.ID)
		if !ok || !tst.Tamed || tst.Sitting || tst.OwnerUUID != owner.Profile.UUID {
			continue
		}
		dx := ent.X - oSnap.X
		dy := ent.Y - oSnap.Y
		dz := ent.Z - oSnap.Z
		if dx*dx+dy*dy+dz*dz <= maxDistSq {
			chosenCat = ent
			break
		}
	}
	if chosenCat == nil {
		return world.ItemStack{}, 0, false
	}
	if !forceGift && rng != nil && rng.Float64() >= CatMorningGiftChance {
		return world.ItemStack{}, 0, false
	}

	itemIdx := 0
	if rng != nil {
		itemIdx = rng.Intn(len(catMorningGiftLootItems))
	}
	giftItem = world.NewNamedItemStack(catMorningGiftLootItems[itemIdx], 1)
	droppedEntityID, _ = s.SpawnItemEntityInDimension(dim, oSnap.X, oSnap.Y+0.3, oSnap.Z, giftItem)

	s.SetCatLyingOnBed(chosenCat.ID, false, false)
	cp := chosenCat.ChunkPos()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundCatPurr, SoundSourceNeutral, chosenCat.X, chosenCat.Y, chosenCat.Z, 1.0, 1.0, 0))
	return giftItem, droppedEntityID, droppedEntityID > 0
}

// HasCatOrOcelotNearPosition reports whether any living `minecraft:cat` or
// `minecraft:ocelot` is within `radius` blocks of (x, y, z) in dimension,
// returning the nearest feline's entity ID and coordinates.
func (s *Server) HasCatOrOcelotNearPosition(dimension string, x, y, z, radius float64) (felineID int32, fx, fy, fz float64, found bool) {
	if s == nil || s.entities == nil || radius <= 0 {
		return 0, 0, 0, 0, false
	}
	normDim := world.NormalizeDimension(dimension)
	maxDistSq := radius * radius
	bestDistSq := maxDistSq + 1.0

	allEnts := s.entities.AppendAll(nil)
	for i := range allEnts {
		ent := &allEnts[i]
		if ent.EffectiveDimension() != normDim {
			continue
		}
		norm := canonicalResourceName(ent.TypeName)
		if norm != MobTypeCat && norm != MobTypeOcelot {
			continue
		}
		dx := ent.X - x
		dy := ent.Y - y
		dz := ent.Z - z
		dSq := dx*dx + dy*dy + dz*dz
		if dSq <= maxDistSq && dSq < bestDistSq {
			bestDistSq = dSq
			felineID = ent.ID
			fx, fy, fz = ent.X, ent.Y, ent.Z
			found = true
		}
	}
	return felineID, fx, fy, fz, found
}

// --- Parrot Variants, Imitation & Jukebox Dancing ---

// PlayerShoulderParrots returns the tamed parrots perched on owner's left and
// right shoulders along with their color variants (0..4).
func (s *Server) PlayerShoulderParrots(owner *PlayerSession) (left, right *TamedMobState, leftVariant, rightVariant int32) {
	if s == nil || owner == nil {
		return nil, nil, 0, 0
	}
	mgr := s.tamedManager()
	mgr.mu.RLock()
	sh := mgr.shoulders[owner.Profile.UUID]
	if sh != nil {
		if sh.LeftParrot != nil {
			cp := *sh.LeftParrot
			left = &cp
		}
		if sh.RightParrot != nil {
			cp := *sh.RightParrot
			right = &cp
		}
	}
	mgr.mu.RUnlock()

	pm := s.petDetails()
	pm.mu.RLock()
	sv := pm.shoulderVariants[owner.Profile.UUID]
	pm.mu.RUnlock()
	return left, right, sv[0], sv[1]
}

// EncodeParrotVariantDataPacket encodes a ClientboundSetEntityDataPacket (0x63)
// updating a parrot's Tamable flags (17) and Parrot.DATA_VARIANT_ID (19, INT 0..4).
func EncodeParrotVariantDataPacket(entityID int32, tamed, sitting bool, variant int32) []byte {
	var flags byte
	if sitting {
		flags |= TamableFlagSitting
	}
	if tamed {
		flags |= TamableFlagTamed
	}
	w := protocol.NewWriter(20)
	w.VarInt(entityID)

	w.Byte(EntityDataTamableFlagsID)
	w.VarInt(EntityDataSerializerByte)
	w.Byte(flags)

	w.Byte(EntityDataParrotVariantID)
	w.VarInt(EntityDataSerializerInt)
	w.VarInt(variant)

	w.Byte(0xFF)
	return w.Bytes()
}

func (s *Server) ensureParrotDetailState(entityID int32, rng *rand.Rand) *ParrotDetailState {
	if s == nil || entityID <= 0 {
		return nil
	}
	pm := s.petDetails()
	pm.mu.Lock()
	defer pm.mu.Unlock()
	ps, ok := pm.parrots[entityID]
	if !ok || ps == nil {
		variant := ParrotVariantRedBlue
		if rng != nil {
			variant = int32(rng.Intn(5))
		}
		ps = &ParrotDetailState{
			EntityID: entityID,
			Variant:  variant,
		}
		pm.parrots[entityID] = ps
	}
	return ps
}

// GetParrotDetailState returns a snapshot of the ParrotDetailState for entityID.
func (s *Server) GetParrotDetailState(entityID int32) (ParrotDetailState, bool) {
	if s == nil || entityID <= 0 {
		return ParrotDetailState{}, false
	}
	ps := s.ensureParrotDetailState(entityID, nil)
	if ps == nil {
		return ParrotDetailState{}, false
	}
	pm := s.petDetails()
	pm.mu.RLock()
	out := *ps
	pm.mu.RUnlock()
	return out, true
}

// ParrotVariant returns the color variant ordinal (0..4) for a parrot entity.
func (s *Server) ParrotVariant(entityID int32) int32 {
	ps, ok := s.GetParrotDetailState(entityID)
	if !ok {
		return ParrotVariantRedBlue
	}
	return ps.Variant
}

// SetParrotVariant sets the color variant ordinal (0..4) for a parrot entity and
// broadcasts the updated SynchedEntityData packet.
func (s *Server) SetParrotVariant(entityID int32, variant int32) bool {
	if s == nil || s.entities == nil || entityID <= 0 {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeParrot {
		return false
	}
	if variant < 0 || variant > 4 {
		variant = ParrotVariantRedBlue
	}
	ps := s.ensureParrotDetailState(entityID, nil)
	pm := s.petDetails()
	pm.mu.Lock()
	ps.Variant = variant
	pm.mu.Unlock()

	tst, _ := s.GetTamedMobState(entityID)
	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, PlaySetEntityDataCB,
		EncodeParrotVariantDataPacket(entityID, tst.Tamed, tst.Sitting, variant))
	return true
}

// IsParrotDancing reports whether the parrot with entityID is currently dancing
// near a playing jukebox.
func (s *Server) IsParrotDancing(entityID int32) bool {
	ps, ok := s.GetParrotDetailState(entityID)
	return ok && ps.Dancing
}

// ParrotImitationSoundForMob maps a hostile or neutral mob type to its vanilla
// 26.1.2 `minecraft:entity.parrot.imitate.<mob>` sound event identifier.
func ParrotImitationSoundForMob(mobType string) (string, bool) {
	norm := strings.TrimPrefix(canonicalResourceName(mobType), "minecraft:")
	switch norm {
	case "blaze", "bogged", "breeze", "creaking", "creeper", "drowned",
		"elder_guardian", "ender_dragon", "endermite", "evoker", "ghast",
		"guardian", "hoglin", "husk", "illusioner", "magma_cube", "phantom",
		"piglin", "piglin_brute", "pillager", "ravager", "shulker",
		"silverfish", "skeleton", "slime", "spider", "stray", "vex",
		"vindicator", "warden", "witch", "wither", "wither_skeleton",
		"zoglin", "zombie", "zombie_villager":
		return "minecraft:entity.parrot.imitate." + norm, true
	case "cave_spider":
		return "minecraft:entity.parrot.imitate.spider", true
	default:
		return "", false
	}
}

// TryParrotImitateNearbyMob scans within `ParrotImitateMobRadius` (20 blocks) of
// the parrot with entityID for a hostile/imitable mob and broadcasts the
// corresponding `minecraft:entity.parrot.imitate.<mob>` sound at high pitch.
func (s *Server) TryParrotImitateNearbyMob(entityID int32, rng *rand.Rand) (soundEvent string, imitated bool) {
	if s == nil || s.entities == nil || entityID <= 0 {
		return "", false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeParrot {
		return "", false
	}
	dim := ent.EffectiveDimension()
	maxDistSq := ParrotImitateMobRadius * ParrotImitateMobRadius

	var candidates []string
	allEnts := s.entities.AppendAll(nil)
	for i := range allEnts {
		other := &allEnts[i]
		if other.ID == entityID || other.EffectiveDimension() != dim {
			continue
		}
		snd, ok := ParrotImitationSoundForMob(other.TypeName)
		if !ok {
			continue
		}
		dx := other.X - ent.X
		dy := other.Y - ent.Y
		dz := other.Z - ent.Z
		if dx*dx+dy*dy+dz*dz <= maxDistSq {
			candidates = append(candidates, snd)
		}
	}
	if len(candidates) == 0 {
		return "", false
	}
	idx := 0
	if rng != nil {
		idx = rng.Intn(len(candidates))
	}
	soundEvent = candidates[idx]

	ps := s.ensureParrotDetailState(entityID, rng)
	pm := s.petDetails()
	pm.mu.Lock()
	ps.LastImitatedMob = soundEvent
	ps.ImitateCooldown = 40
	pm.mu.Unlock()

	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(soundEvent, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 0.7, 1.5, 0))
	return soundEvent, true
}

// UpdateParrotDancingState checks whether an active playing `minecraft:jukebox`
// is within `ParrotJukeboxDanceRadius` (3.46 blocks) of the parrot with entityID
// and updates `ParrotDetailState.Dancing`.
func (s *Server) UpdateParrotDancingState(entityID int32) bool {
	if s == nil || s.entities == nil || entityID <= 0 {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeParrot {
		return false
	}
	dim := ent.EffectiveDimension()
	cache := s.DimensionChunks(dim)
	bx0 := int(math.Floor(ent.X))
	by0 := int(math.Floor(ent.Y))
	bz0 := int(math.Floor(ent.Z))
	maxDistSq := ParrotJukeboxDanceRadius * ParrotJukeboxDanceRadius

	dancing := false
	for dy := -3; dy <= 3 && !dancing; dy++ {
		by := by0 + dy
		if by < world.MinY || by >= world.MinY+world.WorldHeight {
			continue
		}
		for dx := -4; dx <= 4 && !dancing; dx++ {
			bx := bx0 + dx
			for dz := -4; dz <= 4; dz++ {
				bz := bz0 + dz
				distSq := (float64(bx)+0.5-ent.X)*(float64(bx)+0.5-ent.X) +
					(float64(by)+0.5-ent.Y)*(float64(by)+0.5-ent.Y) +
					(float64(bz)+0.5-ent.Z)*(float64(bz)+0.5-ent.Z)
				if distSq > maxDistSq {
					continue
				}
				if jb, ok := s.GetJukeboxAt(dim, bx, by, bz); ok && jb != nil && jb.IsPlaying() {
					dancing = true
					break
				}
				if cache != nil {
					st := cache.GetBlock(bx, by, bz)
					if IsJukeboxBlock(st) && world.BlockProperties(st)["has_record"] == "true" {
						dancing = true
						break
					}
				}
			}
		}
	}

	ps := s.ensureParrotDetailState(entityID, nil)
	pm := s.petDetails()
	pm.mu.Lock()
	ps.Dancing = dancing
	pm.mu.Unlock()
	return dancing
}

func (s *Server) tickParrotDetails(entityID int32, rng *rand.Rand) {
	s.UpdateParrotDancingState(entityID)
	ps := s.ensureParrotDetailState(entityID, rng)
	if ps == nil {
		return
	}
	pm := s.petDetails()
	pm.mu.Lock()
	if ps.ImitateCooldown > 0 {
		ps.ImitateCooldown--
		pm.mu.Unlock()
		return
	}
	pm.mu.Unlock()
	if rng != nil && rng.Intn(50) == 0 {
		_, _ = s.TryParrotImitateNearbyMob(entityID, rng)
	}
}
