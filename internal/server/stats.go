package server

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

// PlayAwardStatsCB is ClientboundAwardStatsPacket (0x03 in protocol 775 / 26.1.2).
const PlayAwardStatsCB int32 = protocol.PlayAwardStats

// VanillaStatsDataVersion is the Minecraft 26.1.2 DataVersion written to stats/<uuid>.json.
const VanillaStatsDataVersion = 4790

// StatType identifies one of the 9 vanilla BuiltInRegistries.STAT_TYPE categories.
type StatType int32

const (
	StatTypeMined    StatType = 0 // minecraft:mined (BuiltInRegistries.BLOCK)
	StatTypeCrafted  StatType = 1 // minecraft:crafted (BuiltInRegistries.ITEM)
	StatTypeUsed     StatType = 2 // minecraft:used (BuiltInRegistries.ITEM)
	StatTypeBroken   StatType = 3 // minecraft:broken (BuiltInRegistries.ITEM)
	StatTypePickedUp StatType = 4 // minecraft:picked_up (BuiltInRegistries.ITEM)
	StatTypeDropped  StatType = 5 // minecraft:dropped (BuiltInRegistries.ITEM)
	StatTypeKilled   StatType = 6 // minecraft:killed (BuiltInRegistries.ENTITY_TYPE)
	StatTypeKilledBy StatType = 7 // minecraft:killed_by (BuiltInRegistries.ENTITY_TYPE)
	StatTypeCustom   StatType = 8 // minecraft:custom (BuiltInRegistries.CUSTOM_STAT)
)

// String returns the canonical resource location of the stat category.
func (st StatType) String() string {
	switch st {
	case StatTypeMined:
		return "minecraft:mined"
	case StatTypeCrafted:
		return "minecraft:crafted"
	case StatTypeUsed:
		return "minecraft:used"
	case StatTypeBroken:
		return "minecraft:broken"
	case StatTypePickedUp:
		return "minecraft:picked_up"
	case StatTypeDropped:
		return "minecraft:dropped"
	case StatTypeKilled:
		return "minecraft:killed"
	case StatTypeKilledBy:
		return "minecraft:killed_by"
	case StatTypeCustom:
		return "minecraft:custom"
	default:
		return "minecraft:custom"
	}
}

// ParseStatType parses a stat category resource location (e.g. "minecraft:mined" or "mined").
func ParseStatType(raw string) (StatType, bool) {
	switch canonicalStatResource(raw) {
	case "minecraft:mined":
		return StatTypeMined, true
	case "minecraft:crafted":
		return StatTypeCrafted, true
	case "minecraft:used":
		return StatTypeUsed, true
	case "minecraft:broken":
		return StatTypeBroken, true
	case "minecraft:picked_up":
		return StatTypePickedUp, true
	case "minecraft:dropped":
		return StatTypeDropped, true
	case "minecraft:killed":
		return StatTypeKilled, true
	case "minecraft:killed_by":
		return StatTypeKilledBy, true
	case "minecraft:custom":
		return StatTypeCustom, true
	default:
		return StatTypeCustom, false
	}
}

// Vanilla 26.1.2 custom stat resource locations (net.minecraft.stats.Stats).
const (
	StatLeaveGame                    = "minecraft:leave_game"
	StatPlayTime                     = "minecraft:play_time"
	StatTotalWorldTime               = "minecraft:total_world_time"
	StatTimeSinceDeath               = "minecraft:time_since_death"
	StatTimeSinceRest                = "minecraft:time_since_rest"
	StatSneakTime                    = "minecraft:sneak_time"
	StatWalkOneCm                    = "minecraft:walk_one_cm"
	StatCrouchOneCm                  = "minecraft:crouch_one_cm"
	StatSprintOneCm                  = "minecraft:sprint_one_cm"
	StatWalkOnWaterOneCm             = "minecraft:walk_on_water_one_cm"
	StatFallOneCm                    = "minecraft:fall_one_cm"
	StatClimbOneCm                   = "minecraft:climb_one_cm"
	StatFlyOneCm                     = "minecraft:fly_one_cm"
	StatWalkUnderWaterOneCm          = "minecraft:walk_under_water_one_cm"
	StatMinecartOneCm                = "minecraft:minecart_one_cm"
	StatBoatOneCm                    = "minecraft:boat_one_cm"
	StatPigOneCm                     = "minecraft:pig_one_cm"
	StatHappyGhastOneCm              = "minecraft:happy_ghast_one_cm"
	StatHorseOneCm                   = "minecraft:horse_one_cm"
	StatAviateOneCm                  = "minecraft:aviate_one_cm"
	StatSwimOneCm                    = "minecraft:swim_one_cm"
	StatStriderOneCm                 = "minecraft:strider_one_cm"
	StatNautilusOneCm                = "minecraft:nautilus_one_cm"
	StatJump                         = "minecraft:jump"
	StatDrop                         = "minecraft:drop"
	StatDamageDealt                  = "minecraft:damage_dealt"
	StatDamageDealtAbsorbed          = "minecraft:damage_dealt_absorbed"
	StatDamageDealtResisted          = "minecraft:damage_dealt_resisted"
	StatDamageTaken                  = "minecraft:damage_taken"
	StatDamageBlockedByShield        = "minecraft:damage_blocked_by_shield"
	StatDamageAbsorbed               = "minecraft:damage_absorbed"
	StatDamageResisted               = "minecraft:damage_resisted"
	StatDeaths                       = "minecraft:deaths"
	StatMobKills                     = "minecraft:mob_kills"
	StatAnimalsBred                  = "minecraft:animals_bred"
	StatPlayerKills                  = "minecraft:player_kills"
	StatFishCaught                   = "minecraft:fish_caught"
	StatTalkedToVillager             = "minecraft:talked_to_villager"
	StatTradedWithVillager           = "minecraft:traded_with_villager"
	StatEatCakeSlice                 = "minecraft:eat_cake_slice"
	StatFillCauldron                 = "minecraft:fill_cauldron"
	StatUseCauldron                  = "minecraft:use_cauldron"
	StatCleanArmor                   = "minecraft:clean_armor"
	StatCleanBanner                  = "minecraft:clean_banner"
	StatCleanShulkerBox              = "minecraft:clean_shulker_box"
	StatInteractWithBrewingStand     = "minecraft:interact_with_brewingstand"
	StatInteractWithBeacon           = "minecraft:interact_with_beacon"
	StatInspectDropper               = "minecraft:inspect_dropper"
	StatInspectHopper                = "minecraft:inspect_hopper"
	StatInspectDispenser             = "minecraft:inspect_dispenser"
	StatPlayNoteBlock                = "minecraft:play_noteblock"
	StatTuneNoteBlock                = "minecraft:tune_noteblock"
	StatPotFlower                    = "minecraft:pot_flower"
	StatTriggerTrappedChest          = "minecraft:trigger_trapped_chest"
	StatOpenEnderChest               = "minecraft:open_enderchest"
	StatEnchantItem                  = "minecraft:enchant_item"
	StatPlayRecord                   = "minecraft:play_record"
	StatInteractWithFurnace          = "minecraft:interact_with_furnace"
	StatInteractWithCraftingTable    = "minecraft:interact_with_crafting_table"
	StatOpenChest                    = "minecraft:open_chest"
	StatSleepInBed                   = "minecraft:sleep_in_bed"
	StatOpenShulkerBox               = "minecraft:open_shulker_box"
	StatOpenBarrel                   = "minecraft:open_barrel"
	StatInteractWithBlastFurnace     = "minecraft:interact_with_blast_furnace"
	StatInteractWithSmoker           = "minecraft:interact_with_smoker"
	StatInteractWithLectern          = "minecraft:interact_with_lectern"
	StatInteractWithCampfire         = "minecraft:interact_with_campfire"
	StatInteractWithCartographyTable = "minecraft:interact_with_cartography_table"
	StatInteractWithLoom             = "minecraft:interact_with_loom"
	StatInteractWithStonecutter      = "minecraft:interact_with_stonecutter"
	StatBellRing                     = "minecraft:bell_ring"
	StatRaidTrigger                  = "minecraft:raid_trigger"
	StatRaidWin                      = "minecraft:raid_win"
	StatInteractWithAnvil            = "minecraft:interact_with_anvil"
	StatInteractWithGrindstone       = "minecraft:interact_with_grindstone"
	StatTargetHit                    = "minecraft:target_hit"
	StatInteractWithSmithingTable    = "minecraft:interact_with_smithing_table"
)

// vanillaCustomStats holds the exact registration order of BuiltInRegistries.CUSTOM_STAT
// in Minecraft 26.1.2 (indices 0..73).
var vanillaCustomStats = []string{
	StatLeaveGame,
	StatPlayTime,
	StatTotalWorldTime,
	StatTimeSinceDeath,
	StatTimeSinceRest,
	StatSneakTime,
	StatWalkOneCm,
	StatCrouchOneCm,
	StatSprintOneCm,
	StatWalkOnWaterOneCm,
	StatFallOneCm,
	StatClimbOneCm,
	StatFlyOneCm,
	StatWalkUnderWaterOneCm,
	StatMinecartOneCm,
	StatBoatOneCm,
	StatPigOneCm,
	StatHappyGhastOneCm,
	StatHorseOneCm,
	StatAviateOneCm,
	StatSwimOneCm,
	StatStriderOneCm,
	StatNautilusOneCm,
	StatJump,
	StatDrop,
	StatDamageDealt,
	StatDamageDealtAbsorbed,
	StatDamageDealtResisted,
	StatDamageTaken,
	StatDamageBlockedByShield,
	StatDamageAbsorbed,
	StatDamageResisted,
	StatDeaths,
	StatMobKills,
	StatAnimalsBred,
	StatPlayerKills,
	StatFishCaught,
	StatTalkedToVillager,
	StatTradedWithVillager,
	StatEatCakeSlice,
	StatFillCauldron,
	StatUseCauldron,
	StatCleanArmor,
	StatCleanBanner,
	StatCleanShulkerBox,
	StatInteractWithBrewingStand,
	StatInteractWithBeacon,
	StatInspectDropper,
	StatInspectHopper,
	StatInspectDispenser,
	StatPlayNoteBlock,
	StatTuneNoteBlock,
	StatPotFlower,
	StatTriggerTrappedChest,
	StatOpenEnderChest,
	StatEnchantItem,
	StatPlayRecord,
	StatInteractWithFurnace,
	StatInteractWithCraftingTable,
	StatOpenChest,
	StatSleepInBed,
	StatOpenShulkerBox,
	StatOpenBarrel,
	StatInteractWithBlastFurnace,
	StatInteractWithSmoker,
	StatInteractWithLectern,
	StatInteractWithCampfire,
	StatInteractWithCartographyTable,
	StatInteractWithLoom,
	StatInteractWithStonecutter,
	StatBellRing,
	StatRaidTrigger,
	StatRaidWin,
	StatInteractWithAnvil,
	StatInteractWithGrindstone,
	StatTargetHit,
	StatInteractWithSmithingTable,
}

// vanillaEntityTypes holds the exact registration order of BuiltInRegistries.ENTITY_TYPE
// in Minecraft 26.1.2 (indices 0..156).
var vanillaEntityTypes = []string{
	"minecraft:acacia_boat",
	"minecraft:acacia_chest_boat",
	"minecraft:allay",
	"minecraft:area_effect_cloud",
	"minecraft:armadillo",
	"minecraft:armor_stand",
	"minecraft:arrow",
	"minecraft:axolotl",
	"minecraft:bamboo_chest_raft",
	"minecraft:bamboo_raft",
	"minecraft:bat",
	"minecraft:bee",
	"minecraft:birch_boat",
	"minecraft:birch_chest_boat",
	"minecraft:blaze",
	"minecraft:block_display",
	"minecraft:bogged",
	"minecraft:breeze",
	"minecraft:breeze_wind_charge",
	"minecraft:camel",
	"minecraft:camel_husk",
	"minecraft:cat",
	"minecraft:cave_spider",
	"minecraft:cherry_boat",
	"minecraft:cherry_chest_boat",
	"minecraft:chest_minecart",
	"minecraft:chicken",
	"minecraft:cod",
	"minecraft:copper_golem",
	"minecraft:command_block_minecart",
	"minecraft:cow",
	"minecraft:creaking",
	"minecraft:creeper",
	"minecraft:dark_oak_boat",
	"minecraft:dark_oak_chest_boat",
	"minecraft:dolphin",
	"minecraft:donkey",
	"minecraft:dragon_fireball",
	"minecraft:drowned",
	"minecraft:egg",
	"minecraft:elder_guardian",
	"minecraft:enderman",
	"minecraft:endermite",
	"minecraft:ender_dragon",
	"minecraft:ender_pearl",
	"minecraft:end_crystal",
	"minecraft:evoker",
	"minecraft:evoker_fangs",
	"minecraft:experience_bottle",
	"minecraft:experience_orb",
	"minecraft:eye_of_ender",
	"minecraft:falling_block",
	"minecraft:fireball",
	"minecraft:firework_rocket",
	"minecraft:fox",
	"minecraft:frog",
	"minecraft:furnace_minecart",
	"minecraft:ghast",
	"minecraft:happy_ghast",
	"minecraft:giant",
	"minecraft:glow_item_frame",
	"minecraft:glow_squid",
	"minecraft:goat",
	"minecraft:guardian",
	"minecraft:hoglin",
	"minecraft:hopper_minecart",
	"minecraft:horse",
	"minecraft:husk",
	"minecraft:illusioner",
	"minecraft:interaction",
	"minecraft:iron_golem",
	"minecraft:item",
	"minecraft:item_display",
	"minecraft:item_frame",
	"minecraft:jungle_boat",
	"minecraft:jungle_chest_boat",
	"minecraft:leash_knot",
	"minecraft:lightning_bolt",
	"minecraft:llama",
	"minecraft:llama_spit",
	"minecraft:magma_cube",
	"minecraft:mangrove_boat",
	"minecraft:mangrove_chest_boat",
	"minecraft:mannequin",
	"minecraft:marker",
	"minecraft:minecart",
	"minecraft:mooshroom",
	"minecraft:mule",
	"minecraft:nautilus",
	"minecraft:oak_boat",
	"minecraft:oak_chest_boat",
	"minecraft:ocelot",
	"minecraft:ominous_item_spawner",
	"minecraft:painting",
	"minecraft:pale_oak_boat",
	"minecraft:pale_oak_chest_boat",
	"minecraft:panda",
	"minecraft:parched",
	"minecraft:parrot",
	"minecraft:phantom",
	"minecraft:pig",
	"minecraft:piglin",
	"minecraft:piglin_brute",
	"minecraft:pillager",
	"minecraft:polar_bear",
	"minecraft:splash_potion",
	"minecraft:lingering_potion",
	"minecraft:pufferfish",
	"minecraft:rabbit",
	"minecraft:ravager",
	"minecraft:salmon",
	"minecraft:sheep",
	"minecraft:shulker",
	"minecraft:shulker_bullet",
	"minecraft:silverfish",
	"minecraft:skeleton",
	"minecraft:skeleton_horse",
	"minecraft:slime",
	"minecraft:small_fireball",
	"minecraft:sniffer",
	"minecraft:snowball",
	"minecraft:snow_golem",
	"minecraft:spawner_minecart",
	"minecraft:spectral_arrow",
	"minecraft:spider",
	"minecraft:spruce_boat",
	"minecraft:spruce_chest_boat",
	"minecraft:squid",
	"minecraft:stray",
	"minecraft:strider",
	"minecraft:tadpole",
	"minecraft:text_display",
	"minecraft:tnt",
	"minecraft:tnt_minecart",
	"minecraft:trader_llama",
	"minecraft:trident",
	"minecraft:tropical_fish",
	"minecraft:turtle",
	"minecraft:vex",
	"minecraft:villager",
	"minecraft:vindicator",
	"minecraft:wandering_trader",
	"minecraft:warden",
	"minecraft:wind_charge",
	"minecraft:witch",
	"minecraft:wither",
	"minecraft:wither_skeleton",
	"minecraft:wither_skull",
	"minecraft:wolf",
	"minecraft:zoglin",
	"minecraft:zombie",
	"minecraft:zombie_horse",
	"minecraft:zombie_nautilus",
	"minecraft:zombie_villager",
	"minecraft:zombified_piglin",
	"minecraft:player",
	"minecraft:fishing_bobber",
}

var (
	customStatByName map[string]int32
	entityTypeByName map[string]int32
)

func init() {
	customStatByName = make(map[string]int32, len(vanillaCustomStats))
	for i, name := range vanillaCustomStats {
		customStatByName[name] = int32(i)
	}
	entityTypeByName = make(map[string]int32, len(vanillaEntityTypes))
	for i, name := range vanillaEntityTypes {
		entityTypeByName[name] = int32(i)
	}
}

func canonicalStatResource(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" {
		return ""
	}
	if !strings.Contains(s, ":") {
		return "minecraft:" + s
	}
	return s
}

// CustomStatRegistryID returns the 0-based BuiltInRegistries.CUSTOM_STAT numeric ID
// for a custom stat resource location (e.g. "minecraft:jump" -> 23).
func CustomStatRegistryID(name string) (int32, bool) {
	id, ok := customStatByName[canonicalStatResource(name)]
	return id, ok
}

// CustomStatRegistryName returns the canonical resource location for a 0-based
// BuiltInRegistries.CUSTOM_STAT numeric ID.
func CustomStatRegistryName(id int32) (string, bool) {
	if id < 0 || int(id) >= len(vanillaCustomStats) {
		return "", false
	}
	return vanillaCustomStats[id], true
}

// EntityTypeRegistryID returns the 0-based BuiltInRegistries.ENTITY_TYPE numeric ID
// for an entity type resource location (e.g. "minecraft:zombie" -> 150).
func EntityTypeRegistryID(name string) (int32, bool) {
	id, ok := entityTypeByName[canonicalStatResource(name)]
	return id, ok
}

// EntityTypeRegistryName returns the canonical resource location for a 0-based
// BuiltInRegistries.ENTITY_TYPE numeric ID.
func EntityTypeRegistryName(id int32) (string, bool) {
	if id < 0 || int(id) >= len(vanillaEntityTypes) {
		return "", false
	}
	return vanillaEntityTypes[id], true
}

// StatRegistryID resolves the numeric statID within category's registry for key.
func StatRegistryID(category StatType, key string) (int32, bool) {
	key = canonicalStatResource(key)
	switch category {
	case StatTypeMined:
		return world.BlockRegistryID(key)
	case StatTypeCrafted, StatTypeUsed, StatTypeBroken, StatTypePickedUp, StatTypeDropped:
		return world.ItemNameToID(key)
	case StatTypeKilled, StatTypeKilledBy:
		return EntityTypeRegistryID(key)
	case StatTypeCustom:
		return CustomStatRegistryID(key)
	default:
		return 0, false
	}
}

// StatRegistryName resolves the canonical resource location for (category, statID).
func StatRegistryName(category StatType, statID int32) (string, bool) {
	switch category {
	case StatTypeMined:
		return world.BlockRegistryName(statID)
	case StatTypeCrafted, StatTypeUsed, StatTypeBroken, StatTypePickedUp, StatTypeDropped:
		name := world.ItemIDToName(statID)
		if name == "" || (statID != 0 && name == "minecraft:air") {
			return "", false
		}
		return name, true
	case StatTypeKilled, StatTypeKilledBy:
		return EntityTypeRegistryName(statID)
	case StatTypeCustom:
		return CustomStatRegistryName(statID)
	default:
		return "", false
	}
}

// AwardStatEntry represents one entry in ClientboundAwardStatsPacket (0x03).
type AwardStatEntry struct {
	CategoryID StatType
	StatID     int32
	Value      int32
}

// EncodeAwardStatsPacket encodes ClientboundAwardStatsPacket (0x03 in protocol 775):
// VarInt(count), followed by count * (VarInt(categoryID), VarInt(statID), VarInt(value)).
func EncodeAwardStatsPacket(entries []AwardStatEntry) []byte {
	w := protocol.NewWriter(4 + len(entries)*6)
	w.VarInt(int32(len(entries)))
	for _, e := range entries {
		w.VarInt(int32(e.CategoryID))
		w.VarInt(e.StatID)
		w.VarInt(e.Value)
	}
	return w.Bytes()
}

// DecodeAwardStatsPacket decodes ClientboundAwardStatsPacket (0x03 in protocol 775).
func DecodeAwardStatsPacket(r *protocol.Reader) ([]AwardStatEntry, error) {
	count, err := r.VarInt()
	if err != nil {
		return nil, err
	}
	if count < 0 || count > 65536 {
		return nil, fmt.Errorf("stats: invalid award_stats entry count %d", count)
	}
	entries := make([]AwardStatEntry, count)
	for i := int32(0); i < count; i++ {
		cat, err := r.VarInt()
		if err != nil {
			return nil, err
		}
		statID, err := r.VarInt()
		if err != nil {
			return nil, err
		}
		val, err := r.VarInt()
		if err != nil {
			return nil, err
		}
		entries[i] = AwardStatEntry{
			CategoryID: StatType(cat),
			StatID:     statID,
			Value:      val,
		}
	}
	return entries, nil
}

type statMapKey struct {
	category StatType
	key      string
}

type playerStatsState struct {
	mu       sync.RWMutex
	values   map[statMapKey]int32
	sneaking bool
}

var playerStatsMap sync.Map // map[*PlayerSession]*playerStatsState

func getPlayerStatsState(session *PlayerSession) *playerStatsState {
	if session == nil {
		return nil
	}
	if v, ok := playerStatsMap.Load(session); ok {
		return v.(*playerStatsState)
	}
	created := &playerStatsState{
		values: make(map[statMapKey]int32),
	}
	actual, _ := playerStatsMap.LoadOrStore(session, created)
	return actual.(*playerStatsState)
}

// IsSneaking reports whether session is currently sneaking (crouching).
func (session *PlayerSession) IsSneaking() bool {
	st := getPlayerStatsState(session)
	if st == nil {
		return false
	}
	st.mu.RLock()
	v := st.sneaking
	st.mu.RUnlock()
	return v
}

// SetSneaking updates whether session is currently sneaking (crouching).
func (session *PlayerSession) SetSneaking(sneaking bool) {
	st := getPlayerStatsState(session)
	if st == nil {
		return
	}
	st.mu.Lock()
	st.sneaking = sneaking
	st.mu.Unlock()
}

// GetPlayerStat returns the current value of (category, key) for session.
func (s *Server) GetPlayerStat(session *PlayerSession, category StatType, key string) int32 {
	st := getPlayerStatsState(session)
	if st == nil {
		return 0
	}
	canon := canonicalStatResource(key)
	st.mu.RLock()
	val := st.values[statMapKey{category: category, key: canon}]
	st.mu.RUnlock()
	return val
}

// SetPlayerStat sets the exact value of (category, key) for session (clamped to >= 0).
func (s *Server) SetPlayerStat(session *PlayerSession, category StatType, key string, value int32) {
	st := getPlayerStatsState(session)
	if st == nil {
		return
	}
	canon := canonicalStatResource(key)
	if canon == "" || canon == "minecraft:air" {
		return
	}
	if value < 0 {
		value = 0
	}
	st.mu.Lock()
	if value == 0 {
		delete(st.values, statMapKey{category: category, key: canon})
	} else {
		st.values[statMapKey{category: category, key: canon}] = value
	}
	st.mu.Unlock()
}

// AwardPlayerStat adds delta to (category, key) for session and returns the new value.
func (s *Server) AwardPlayerStat(session *PlayerSession, category StatType, key string, delta int32) int32 {
	if delta <= 0 {
		return s.GetPlayerStat(session, category, key)
	}
	st := getPlayerStatsState(session)
	if st == nil {
		return 0
	}
	canon := canonicalStatResource(key)
	if canon == "" || canon == "minecraft:air" {
		return 0
	}
	st.mu.Lock()
	mk := statMapKey{category: category, key: canon}
	next := st.values[mk] + delta
	if next < 0 {
		next = math.MaxInt32
	}
	st.values[mk] = next
	st.mu.Unlock()
	return next
}

// AwardCustomStat adds delta to a custom stat (StatTypeCustom) for session.
func (s *Server) AwardCustomStat(session *PlayerSession, customStat string, delta int32) int32 {
	return s.AwardPlayerStat(session, StatTypeCustom, customStat, delta)
}

// RecordBlockMined increments minecraft:mined for blockName by 1.
func (s *Server) RecordBlockMined(session *PlayerSession, blockName string) {
	if s == nil || session == nil || blockName == "" || blockName == "minecraft:air" {
		return
	}
	s.AwardPlayerStat(session, StatTypeMined, blockName, 1)
}

// RecordItemCrafted increments minecraft:crafted for itemName by count.
func (s *Server) RecordItemCrafted(session *PlayerSession, itemName string, count int32) {
	if s == nil || session == nil || itemName == "" || itemName == "minecraft:air" || count <= 0 {
		return
	}
	s.AwardPlayerStat(session, StatTypeCrafted, itemName, count)
}

// RecordItemUsed increments minecraft:used for itemName by count.
func (s *Server) RecordItemUsed(session *PlayerSession, itemName string, count int32) {
	if s == nil || session == nil || itemName == "" || itemName == "minecraft:air" || count <= 0 {
		return
	}
	s.AwardPlayerStat(session, StatTypeUsed, itemName, count)
}

// RecordItemBroken increments minecraft:broken for itemName by 1.
func (s *Server) RecordItemBroken(session *PlayerSession, itemName string) {
	if s == nil || session == nil || itemName == "" || itemName == "minecraft:air" {
		return
	}
	s.AwardPlayerStat(session, StatTypeBroken, itemName, 1)
}

// RecordItemPickedUp increments minecraft:picked_up for itemName by count.
func (s *Server) RecordItemPickedUp(session *PlayerSession, itemName string, count int32) {
	if s == nil || session == nil || itemName == "" || itemName == "minecraft:air" || count <= 0 {
		return
	}
	s.AwardPlayerStat(session, StatTypePickedUp, itemName, count)
}

// RecordItemDropped increments minecraft:dropped for itemName and minecraft:custom/minecraft:drop by count.
func (s *Server) RecordItemDropped(session *PlayerSession, itemName string, count int32) {
	if s == nil || session == nil || itemName == "" || itemName == "minecraft:air" || count <= 0 {
		return
	}
	s.AwardPlayerStat(session, StatTypeDropped, itemName, count)
	s.AwardCustomStat(session, StatDrop, count)
}

// RecordEntityKilled increments minecraft:killed for entityType and updates
// minecraft:mob_kills (or minecraft:player_kills when entityType is minecraft:player).
func (s *Server) RecordEntityKilled(session *PlayerSession, entityType string) {
	if s == nil || session == nil || entityType == "" {
		return
	}
	canon := canonicalStatResource(entityType)
	s.AwardPlayerStat(session, StatTypeKilled, canon, 1)
	if canon == "minecraft:player" {
		s.AwardCustomStat(session, StatPlayerKills, 1)
	} else {
		s.AwardCustomStat(session, StatMobKills, 1)
	}
}

// RecordKilledByEntity increments minecraft:killed_by for entityType by 1.
func (s *Server) RecordKilledByEntity(session *PlayerSession, entityType string) {
	if s == nil || session == nil || entityType == "" {
		return
	}
	s.AwardPlayerStat(session, StatTypeKilledBy, entityType, 1)
}

// RecordPlayerDamageDealt increments minecraft:damage_dealt in tenths of a health point.
func (s *Server) RecordPlayerDamageDealt(session *PlayerSession, damagePoints float32) {
	if s == nil || session == nil || damagePoints <= 0 {
		return
	}
	tenths := int32(math.Round(float64(damagePoints) * 10.0))
	if tenths > 0 {
		s.AwardCustomStat(session, StatDamageDealt, tenths)
	}
}

// RecordPlayerDamageTaken increments minecraft:damage_taken in tenths of a health point.
func (s *Server) RecordPlayerDamageTaken(session *PlayerSession, damagePoints float32) {
	if s == nil || session == nil || damagePoints <= 0 {
		return
	}
	tenths := int32(math.Round(float64(damagePoints) * 10.0))
	if tenths > 0 {
		s.AwardCustomStat(session, StatDamageTaken, tenths)
	}
}

// RecordPlayerDeath increments minecraft:deaths by 1 and resets minecraft:time_since_death to 0.
func (s *Server) RecordPlayerDeath(session *PlayerSession) {
	if s == nil || session == nil {
		return
	}
	s.AwardCustomStat(session, StatDeaths, 1)
	s.SetPlayerStat(session, StatTypeCustom, StatTimeSinceDeath, 0)
}

// RecordPlayerSleepInBed increments minecraft:sleep_in_bed by 1 and resets minecraft:time_since_rest to 0.
func (s *Server) RecordPlayerSleepInBed(session *PlayerSession) {
	if s == nil || session == nil {
		return
	}
	s.AwardCustomStat(session, StatSleepInBed, 1)
	s.SetPlayerStat(session, StatTypeCustom, StatTimeSinceRest, 0)
}

// RecordPlayerMovementStats updates movement distance stats (in centimeters) and jump count.
func (s *Server) RecordPlayerMovementStats(
	session *PlayerSession,
	prevX, prevY, prevZ, newX, newY, newZ float64,
	wasOnGround, onGround bool,
) {
	if s == nil || session == nil || session.IsDead() {
		return
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator {
		return
	}
	dx := newX - prevX
	dy := newY - prevY
	dz := newZ - prevZ
	horizDist := math.Hypot(dx, dz)
	dist3D := math.Sqrt(dx*dx + dy*dy + dz*dz)
	if dist3D > 10.0 {
		// Ignore teleports.
		return
	}

	if wasOnGround && !onGround && dy > 0.05 {
		s.AwardCustomStat(session, StatJump, 1)
	}

	horizCm := int32(math.Round(horizDist * 100.0))
	if horizCm > 0 {
		if onGround || wasOnGround {
			switch {
			case session.IsSprinting():
				s.AwardCustomStat(session, StatSprintOneCm, horizCm)
			case session.IsSneaking():
				s.AwardCustomStat(session, StatCrouchOneCm, horizCm)
			default:
				s.AwardCustomStat(session, StatWalkOneCm, horizCm)
			}
		} else if horizCm >= 1 {
			s.AwardCustomStat(session, StatFlyOneCm, horizCm)
		}
	}

	if (!onGround || !wasOnGround) && dy < 0 {
		fallCm := int32(math.Round(-dy * 100.0))
		if fallCm > 0 {
			s.AwardCustomStat(session, StatFallOneCm, fallCm)
		}
	}
}

// RecordContainerOpenStat increments the appropriate custom interaction stat when a
// player opens a block container.
func (s *Server) RecordContainerOpenStat(session *PlayerSession, blockName string) {
	if s == nil || session == nil {
		return
	}
	name := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(blockName)), "minecraft:")
	switch {
	case name == "crafting_table":
		s.AwardCustomStat(session, StatInteractWithCraftingTable, 1)
	case name == "furnace" || name == "lit_furnace":
		s.AwardCustomStat(session, StatInteractWithFurnace, 1)
	case name == "blast_furnace":
		s.AwardCustomStat(session, StatInteractWithBlastFurnace, 1)
	case name == "smoker":
		s.AwardCustomStat(session, StatInteractWithSmoker, 1)
	case name == "brewing_stand":
		s.AwardCustomStat(session, StatInteractWithBrewingStand, 1)
	case name == "beacon":
		s.AwardCustomStat(session, StatInteractWithBeacon, 1)
	case name == "dropper":
		s.AwardCustomStat(session, StatInspectDropper, 1)
	case name == "hopper":
		s.AwardCustomStat(session, StatInspectHopper, 1)
	case name == "dispenser":
		s.AwardCustomStat(session, StatInspectDispenser, 1)
	case name == "ender_chest":
		s.AwardCustomStat(session, StatOpenEnderChest, 1)
	case name == "trapped_chest":
		s.AwardCustomStat(session, StatTriggerTrappedChest, 1)
		s.AwardCustomStat(session, StatOpenChest, 1)
	case name == "chest" || strings.HasSuffix(name, "_chest"):
		s.AwardCustomStat(session, StatOpenChest, 1)
	case name == "barrel":
		s.AwardCustomStat(session, StatOpenBarrel, 1)
	case name == "shulker_box" || strings.HasSuffix(name, "_shulker_box"):
		s.AwardCustomStat(session, StatOpenShulkerBox, 1)
	case name == "lectern":
		s.AwardCustomStat(session, StatInteractWithLectern, 1)
	case name == "cartography_table":
		s.AwardCustomStat(session, StatInteractWithCartographyTable, 1)
	case name == "loom":
		s.AwardCustomStat(session, StatInteractWithLoom, 1)
	case name == "stonecutter":
		s.AwardCustomStat(session, StatInteractWithStonecutter, 1)
	case name == "anvil" || name == "chipped_anvil" || name == "damaged_anvil":
		s.AwardCustomStat(session, StatInteractWithAnvil, 1)
	case name == "grindstone":
		s.AwardCustomStat(session, StatInteractWithGrindstone, 1)
	case name == "smithing_table":
		s.AwardCustomStat(session, StatInteractWithSmithingTable, 1)
	}
}

// TickAllPlayerStats advances per-tick time statistics (play_time, total_world_time,
// time_since_death, time_since_rest, sneak_time) for all online players.
func (s *Server) TickAllPlayerStats() {
	if s == nil {
		return
	}
	for _, p := range s.onlinePlayersSnapshot() {
		if p == nil {
			continue
		}
		s.AwardCustomStat(p, StatPlayTime, 1)
		s.AwardCustomStat(p, StatTotalWorldTime, 1)
		if !p.IsDead() {
			s.AwardCustomStat(p, StatTimeSinceDeath, 1)
			if !s.IsPlayerSleeping(p) {
				s.AwardCustomStat(p, StatTimeSinceRest, 1)
			}
			if p.IsSneaking() {
				s.AwardCustomStat(p, StatSneakTime, 1)
			}
		}
	}
}

// BuildAwardStatsEntries returns a deterministically sorted slice of all non-zero
// AwardStatEntry items for session that map to valid protocol 775 registry IDs.
func (s *Server) BuildAwardStatsEntries(session *PlayerSession) []AwardStatEntry {
	st := getPlayerStatsState(session)
	if st == nil {
		return nil
	}
	st.mu.RLock()
	entries := make([]AwardStatEntry, 0, len(st.values))
	for mk, val := range st.values {
		if val <= 0 {
			continue
		}
		statID, ok := StatRegistryID(mk.category, mk.key)
		if !ok || statID < 0 {
			continue
		}
		entries = append(entries, AwardStatEntry{
			CategoryID: mk.category,
			StatID:     statID,
			Value:      val,
		})
	}
	st.mu.RUnlock()

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].CategoryID != entries[j].CategoryID {
			return entries[i].CategoryID < entries[j].CategoryID
		}
		return entries[i].StatID < entries[j].StatID
	})
	return entries
}

// SendPlayerStats sends ClientboundAwardStatsPacket (0x03) with all current stats to session.
func (s *Server) SendPlayerStats(session *PlayerSession) error {
	if s == nil || session == nil || session.send == nil {
		return nil
	}
	entries := s.BuildAwardStatsEntries(session)
	return session.send(PlayAwardStatsCB, EncodeAwardStatsPacket(entries))
}

// HandleRequestStats handles ServerboundClientCommandPacket action 1 (REQUEST_STATS)
// by sending the player's current statistics via ClientboundAwardStatsPacket (0x03).
func (s *Server) HandleRequestStats(session *PlayerSession) error {
	return s.SendPlayerStats(session)
}

type vanillaStatsFile struct {
	Stats       map[string]map[string]int32 `json:"stats"`
	DataVersion int                         `json:"DataVersion"`
}

// MarshalPlayerStatsJSON serializes session's statistics in the vanilla
// <WorldDir>/stats/<uuid>.json format.
func (s *Server) MarshalPlayerStatsJSON(session *PlayerSession) ([]byte, error) {
	file := vanillaStatsFile{
		Stats:       make(map[string]map[string]int32),
		DataVersion: VanillaStatsDataVersion,
	}
	st := getPlayerStatsState(session)
	if st != nil {
		st.mu.RLock()
		for mk, val := range st.values {
			if val <= 0 {
				continue
			}
			catKey := mk.category.String()
			catMap := file.Stats[catKey]
			if catMap == nil {
				catMap = make(map[string]int32)
				file.Stats[catKey] = catMap
			}
			catMap[mk.key] = val
		}
		st.mu.RUnlock()
	}
	return json.MarshalIndent(file, "", "  ")
}

// UnmarshalPlayerStatsJSON loads session's statistics from a vanilla
// <WorldDir>/stats/<uuid>.json payload.
func (s *Server) UnmarshalPlayerStatsJSON(session *PlayerSession, data []byte) error {
	if session == nil {
		return nil
	}
	var file vanillaStatsFile
	if err := json.Unmarshal(data, &file); err != nil {
		return err
	}
	st := getPlayerStatsState(session)
	st.mu.Lock()
	st.values = make(map[statMapKey]int32)
	for catStr, catMap := range file.Stats {
		cat, ok := ParseStatType(catStr)
		if !ok {
			continue
		}
		for statName, val := range catMap {
			if val <= 0 {
				continue
			}
			canon := canonicalStatResource(statName)
			if canon == "" || canon == "minecraft:air" {
				continue
			}
			st.values[statMapKey{category: cat, key: canon}] = val
		}
	}
	st.mu.Unlock()
	return nil
}

func (s *Server) playerStatsFilePath(session *PlayerSession) string {
	if s == nil || session == nil || s.cfg.WorldDir == "" {
		return ""
	}
	uuidStr := FormatUUID(session.Profile.UUID)
	return filepath.Join(s.cfg.WorldDir, "stats", uuidStr+".json")
}

// SavePlayerStatsFile writes session's statistics to <WorldDir>/stats/<uuid>.json.
func (s *Server) SavePlayerStatsFile(session *PlayerSession) error {
	path := s.playerStatsFilePath(session)
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := s.MarshalPlayerStatsJSON(session)
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o644)
}

// LoadPlayerStatsFile loads session's statistics from <WorldDir>/stats/<uuid>.json if present.
func (s *Server) LoadPlayerStatsFile(session *PlayerSession) error {
	path := s.playerStatsFilePath(session)
	if path == "" {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return s.UnmarshalPlayerStatsJSON(session, raw)
}

// SaveAllPlayerStats saves statistics files for all currently connected players.
func (s *Server) SaveAllPlayerStats() error {
	if s == nil {
		return nil
	}
	var firstErr error
	for _, p := range s.onlinePlayersSnapshot() {
		if err := s.SavePlayerStatsFile(p); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
