package server

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

// MaxGiveItemCountMultiplier is the vanilla multiplier (100 stacks) for /give.
const MaxGiveItemCountMultiplier int32 = 100

// ParsedItemSpec holds a parsed item argument from /give, /clear, or /item
// (e.g. `minecraft:diamond_sword[damage=10,custom_name="Excalibur",enchantments={"minecraft:sharpness":5}]`).
type ParsedItemSpec struct {
	ItemID        string
	NumericID     int32
	Meta          ItemMeta
	PotionType    string
	MapID         int32
	HasMapID      bool
	WritableBook  *WritableBookContent
	WrittenBook   *WrittenBookContent
	HasDamageSpec bool
	HasNameSpec   bool
	HasEnchSpec   bool
	HasPotionSpec bool
}

// ParseItemArgument parses an item argument token (with optional `[k=v,...]`
// data component bracket syntax) into a ParsedItemSpec.
func ParseItemArgument(raw string) (ParsedItemSpec, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ParsedItemSpec{}, fmt.Errorf("empty item identifier")
	}

	// Ensure the default recipe book is initialized so all item IDs are registered.
	_ = DefaultRecipeBook()

	baseID := s
	var compPart string
	if openIdx := strings.IndexByte(s, '['); openIdx >= 0 {
		if !strings.HasSuffix(s, "]") {
			return ParsedItemSpec{}, fmt.Errorf("malformed item component bracket in %q", raw)
		}
		baseID = strings.TrimSpace(s[:openIdx])
		compPart = strings.TrimSpace(s[openIdx+1 : len(s)-1])
	}
	if idx := strings.IndexByte(baseID, '{'); idx >= 0 {
		baseID = strings.TrimSpace(baseID[:idx])
	}

	canonID := canonicalItemName(baseID)
	numID, ok := world.ItemNameToID(canonID)
	if !ok || numID <= 0 {
		return ParsedItemSpec{}, fmt.Errorf("unknown item %q", baseID)
	}

	spec := ParsedItemSpec{
		ItemID:    canonID,
		NumericID: numID,
	}

	if compPart != "" {
		if err := parseItemComponentsBracket(&spec, compPart); err != nil {
			return ParsedItemSpec{}, err
		}
	}
	return spec, nil
}

func canonicalItemName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return ""
	}
	if !strings.Contains(name, ":") {
		return "minecraft:" + name
	}
	return name
}

func parseItemComponentsBracket(spec *ParsedItemSpec, compPart string) error {
	entries := splitTopLevelCSV(compPart)
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		eq := strings.IndexByte(entry, '=')
		if eq <= 0 {
			return fmt.Errorf("invalid component assignment %q", entry)
		}
		key := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(entry[:eq])), "minecraft:")
		val := strings.TrimSpace(entry[eq+1:])

		switch key {
		case "damage":
			dmg, err := strconv.ParseInt(val, 10, 32)
			if err != nil || dmg < 0 {
				return fmt.Errorf("invalid damage component %q", val)
			}
			spec.Meta.Damage = int32(dmg)
			spec.HasDamageSpec = true

		case "repair_cost":
			rc, err := strconv.ParseInt(val, 10, 32)
			if err != nil || rc < 0 {
				return fmt.Errorf("invalid repair_cost component %q", val)
			}
			spec.Meta.RepairCost = int32(rc)

		case "custom_name", "item_name":
			unquoted := trimMatchingQuotes(val)
			if comp, err := ParseChatComponentJSON(unquoted); err == nil && !comp.IsEmpty() {
				spec.Meta.CustomName = comp.PlainText()
			} else {
				spec.Meta.CustomName = unquoted
			}
			spec.HasNameSpec = true

		case "potion_contents", "potion":
			pVal := trimMatchingQuotes(val)
			if strings.HasPrefix(pVal, "{") && strings.HasSuffix(pVal, "}") {
				var obj map[string]any
				if err := json.Unmarshal([]byte(pVal), &obj); err == nil {
					if pStr, ok := obj["potion"].(string); ok {
						pVal = pStr
					}
				}
			}
			pCanon := canonicalItemName(pVal)
			if _, ok := PotionByID(pCanon); !ok {
				return fmt.Errorf("unknown potion %q", pVal)
			}
			spec.PotionType = pCanon
			spec.HasPotionSpec = true

		case "map_id":
			mid, err := strconv.ParseInt(val, 10, 32)
			if err != nil {
				return fmt.Errorf("invalid map_id %q", val)
			}
			spec.MapID = int32(mid)
			spec.HasMapID = true

		case "enchantments", "stored_enchantments":
			if err := parseEnchantmentsComponentValue(&spec.Meta, val); err != nil {
				return err
			}
			spec.HasEnchSpec = true
		}
	}
	return nil
}

func parseEnchantmentsComponentValue(meta *ItemMeta, raw string) error {
	s := strings.TrimSpace(raw)
	if strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}") {
		var rawMap map[string]any
		if err := json.Unmarshal([]byte(s), &rawMap); err == nil {
			if levelsObj, ok := rawMap["levels"].(map[string]any); ok {
				rawMap = levelsObj
			}
			for k, v := range rawMap {
				def, ok := EnchantmentByID(k)
				if !ok {
					return fmt.Errorf("unknown enchantment %q", k)
				}
				lvlFloat, ok := v.(float64)
				if !ok || int(lvlFloat) < 1 {
					return fmt.Errorf("invalid level for enchantment %q", k)
				}
				meta.SetEnchantment(def.ID, int(lvlFloat))
			}
			return nil
		}
		inner := strings.TrimSpace(s[1 : len(s)-1])
		for _, pair := range splitTopLevelCSV(inner) {
			pair = strings.TrimSpace(pair)
			if pair == "" {
				continue
			}
			sep := strings.IndexAny(pair, ":=")
			if sep <= 0 {
				return fmt.Errorf("invalid enchantment entry %q", pair)
			}
			// Handle "minecraft:sharpness:5" where the last colon separates the level.
			lastColon := strings.LastIndexByte(pair, ':')
			if eqIdx := strings.IndexByte(pair, '='); eqIdx > 0 {
				sep = eqIdx
			} else if lastColon > 0 {
				sep = lastColon
			}
			enchKey := trimMatchingQuotes(strings.TrimSpace(pair[:sep]))
			lvlStr := strings.TrimSpace(pair[sep+1:])
			def, ok := EnchantmentByID(enchKey)
			if !ok {
				return fmt.Errorf("unknown enchantment %q", enchKey)
			}
			lvl, err := strconv.Atoi(lvlStr)
			if err != nil || lvl < 1 {
				return fmt.Errorf("invalid enchantment level %q", lvlStr)
			}
			meta.SetEnchantment(def.ID, lvl)
		}
		return nil
	}
	return fmt.Errorf("invalid enchantments component %q", raw)
}

func splitTopLevelCSV(s string) []string {
	var out []string
	depth := 0
	inQuotes := false
	var quoteChar byte
	start := 0
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if inQuotes {
			if ch == '\\' && i+1 < len(s) {
				i++
				continue
			}
			if ch == quoteChar {
				inQuotes = false
			}
			continue
		}
		switch ch {
		case '"', '\'':
			inQuotes = true
			quoteChar = ch
		case '{', '[':
			depth++
		case '}', ']':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				out = append(out, s[start:i])
				start = i + 1
			}
		}
	}
	out = append(out, s[start:])
	return out
}

func trimMatchingQuotes(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && ((s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'')) {
		return s[1 : len(s)-1]
	}
	return s
}

// MaxStackSizeForSpec returns the maximum stack size (1, 16, or 64) for an item spec.
func MaxStackSizeForSpec(spec ParsedItemSpec) int32 {
	if MaxItemDurability(spec.ItemID) > 0 || !spec.Meta.IsEmpty() {
		return 1
	}
	name := strings.TrimPrefix(spec.ItemID, "minecraft:")
	switch name {
	case "writable_book", "enchanted_book", "potion", "splash_potion", "lingering_potion",
		"milk_bucket", "water_bucket", "lava_bucket", "powder_snow_bucket",
		"axolotl_bucket", "cod_bucket", "salmon_bucket", "tropical_fish_bucket", "pufferfish_bucket", "tadpole_bucket",
		"mushroom_stew", "rabbit_stew", "beetroot_soup", "suspicious_stew",
		"saddle", "minecart", "chest_minecart", "furnace_minecart", "tnt_minecart", "hopper_minecart",
		"totem_of_undying", "spyglass", "goat_horn", "Recovery_compass":
		return 1
	case "written_book", "ender_pearl", "snowball", "egg", "honey_bottle", "bucket", "armor_stand":
		return 16
	}
	if strings.HasSuffix(name, "_boat") || strings.HasSuffix(name, "_raft") ||
		strings.HasSuffix(name, "_horse_armor") || strings.HasPrefix(name, "music_disc_") ||
		strings.HasSuffix(name, "_banner_pattern") || strings.HasSuffix(name, "_shulker_box") || name == "shulker_box" {
		return 1
	}
	if strings.HasSuffix(name, "_sign") || strings.HasSuffix(name, "_hanging_sign") || strings.HasSuffix(name, "_banner") {
		return 16
	}
	return MaxItemStackSize
}

func applySpecMetadataToSlot(session *PlayerSession, slot int, spec ParsedItemSpec) {
	if session == nil || slot < 0 || slot >= world.PlayerInventorySlots {
		return
	}
	SetSlotItemMeta(session, slot, spec.Meta)
	if spec.PotionType != "" {
		SetSlotPotionType(session, slot, spec.PotionType)
	} else {
		SetSlotPotionType(session, slot, "")
	}
	if spec.HasMapID {
		SetSlotMapID(session, slot, spec.MapID)
	} else {
		ClearSlotMapID(session, slot)
	}
	if spec.WritableBook != nil {
		SetSlotWritableBook(session, slot, *spec.WritableBook)
	} else if spec.WrittenBook != nil {
		SetSlotWrittenBook(session, slot, *spec.WrittenBook)
	} else {
		ClearSlotBookMeta(session, slot)
	}
}

func clearAllSlotMetadata(session *PlayerSession, slot int) {
	if session == nil || slot < 0 || slot >= world.PlayerInventorySlots {
		return
	}
	SetSlotItemMeta(session, slot, ItemMeta{})
	SetSlotPotionType(session, slot, "")
	ClearSlotMapID(session, slot)
	ClearSlotBookMeta(session, slot)
}

func slotHasCustomMetadata(session *PlayerSession, slot int) bool {
	if session == nil || slot < 0 || slot >= world.PlayerInventorySlots {
		return false
	}
	if !GetSlotItemMeta(session, slot).IsEmpty() {
		return true
	}
	if GetSlotPotionType(session, slot) != "" {
		return true
	}
	if _, ok := GetSlotMapID(session, slot); ok {
		return true
	}
	if _, ok := GetSlotWritableBook(session, slot); ok {
		return true
	}
	if _, ok := GetSlotWrittenBook(session, slot); ok {
		return true
	}
	return false
}

func sendPlayerSlotUpdate(session *PlayerSession, slot int) {
	if session == nil || session.send == nil || slot < 0 || slot >= world.PlayerInventorySlots {
		return
	}
	st := session.InventorySlot(slot)
	stateID := session.NextContainerStateID()
	if wb, ok := GetSlotWritableBook(session, slot); ok {
		_ = session.send(protocol.PlayContainerSetSlot, encodeContainerSetSlotWithBook(0, stateID, slot, st, &wb, nil))
		return
	}
	if wr, ok := GetSlotWrittenBook(session, slot); ok {
		_ = session.send(protocol.PlayContainerSetSlot, encodeContainerSetSlotWithBook(0, stateID, slot, st, nil, &wr))
		return
	}
	if mapID, ok := GetSlotMapID(session, slot); ok {
		_ = session.send(protocol.PlayContainerSetSlot, encodeContainerSetSlotWithMapID(0, stateID, slot, st, mapID, true))
		return
	}
	_ = session.send(protocol.PlayContainerSetSlot, EncodeContainerSlot(0, stateID, slot, st))
}

// GivePlayerItemSpec adds count items described by spec to session's inventory,
// spawning any overflow as dropped item entities at the player's location.
func (s *Server) GivePlayerItemSpec(session *PlayerSession, spec ParsedItemSpec, count int32) int32 {
	if s == nil || session == nil || count <= 0 || spec.NumericID <= 0 {
		return 0
	}
	maxStack := MaxStackSizeForSpec(spec)
	hasCustomMeta := !spec.Meta.IsEmpty() || spec.PotionType != "" || spec.HasMapID || spec.WritableBook != nil || spec.WrittenBook != nil

	remaining := count
	var changedSlots []int

	// 1. Merge into existing compatible stacks (hotbar 36..44, then main 9..35) when stackable and no custom metadata.
	if maxStack > 1 && !hasCustomMeta {
		for _, r := range [][2]int{{world.HotbarContainerOffset, world.HotbarContainerOffset + world.HotbarSlotCount}, {9, world.HotbarContainerOffset}} {
			for slot := r[0]; slot < r[1] && remaining > 0; slot++ {
				cur := session.InventorySlot(slot)
				if cur.IsEmpty() || cur.ResolvedItemID() != spec.NumericID || cur.Count >= maxStack || slotHasCustomMetadata(session, slot) {
					continue
				}
				space := maxStack - cur.Count
				take := remaining
				if take > space {
					take = space
				}
				session.SetInventorySlot(slot, world.NewItemStack(spec.NumericID, cur.Count+take))
				remaining -= take
				changedSlots = append(changedSlots, slot)
			}
		}
	}

	// 2. Place into empty slots (hotbar 36..44, then main 9..35).
	for _, r := range [][2]int{{world.HotbarContainerOffset, world.HotbarContainerOffset + world.HotbarSlotCount}, {9, world.HotbarContainerOffset}} {
		for slot := r[0]; slot < r[1] && remaining > 0; slot++ {
			if !session.InventorySlot(slot).IsEmpty() {
				continue
			}
			take := remaining
			if take > maxStack {
				take = maxStack
			}
			session.SetInventorySlot(slot, world.NewItemStack(spec.NumericID, take))
			applySpecMetadataToSlot(session, slot, spec)
			remaining -= take
			changedSlots = append(changedSlots, slot)
		}
	}

	for _, slot := range changedSlots {
		sendPlayerSlotUpdate(session, slot)
	}

	snap := session.Snapshot()
	// 3. Drop any remaining overflow at the player's feet.
	for remaining > 0 {
		dropCount := remaining
		if dropCount > maxStack {
			dropCount = maxStack
		}
		remaining -= dropCount
		entID, _ := s.SpawnItemEntityInDimension(snap.Dimension, snap.X, snap.Y+0.25, snap.Z, world.NewItemStack(spec.NumericID, dropCount))
		if entID != 0 && spec.HasMapID {
			s.AssociateDroppedMapEntity(entID, spec.MapID)
		}
	}

	if len(changedSlots) > 0 {
		cx := int32(int64(math.Floor(snap.X)) >> 4)
		cz := int32(int64(math.Floor(snap.Z)) >> 4)
		s.BroadcastDimensionChunk(snap.Dimension, cx, cz, PlaySoundPacketID,
			EncodeSoundPacket("minecraft:entity.item.pickup", SoundSourcePlayers, snap.X, snap.Y, snap.Z, 0.2, 1.4, 0))
	}
	s.TriggerInventoryChanged(session)
	return count
}

// --- 1. /give <targets> <item> [<count>] ---

// HandleGiveCommand implements the vanilla 26.1.2 /give command:
//
//	/give <targets> <item> [<count>]
func (s *Server) HandleGiveCommand(sender *PlayerSession, args []string) string {
	if s == nil {
		return "Server unavailable"
	}
	if !s.HasAdminPermission(sender) {
		return "You do not have permission to use this command"
	}
	if len(args) < 2 || len(args) > 3 {
		return "Usage: /give <targets> <item> [<count>]"
	}
	targets := s.ResolveCommandTargets(sender, args[0])
	if len(targets) == 0 {
		return fmt.Sprintf("No player was found matching %q", args[0])
	}
	spec, err := ParseItemArgument(args[1])
	if err != nil {
		return fmt.Sprintf("Invalid item: %v", err)
	}

	maxAllowed := MaxStackSizeForSpec(spec) * MaxGiveItemCountMultiplier
	count := int32(1)
	if len(args) == 3 {
		parsedCount, err := strconv.ParseInt(args[2], 10, 32)
		if err != nil || parsedCount < 1 {
			return fmt.Sprintf("Invalid count %q: must be at least 1", args[2])
		}
		if int32(parsedCount) > maxAllowed {
			return fmt.Sprintf("Can't give more than %d of %s", maxAllowed, spec.ItemID)
		}
		count = int32(parsedCount)
	}

	for _, target := range targets {
		s.GivePlayerItemSpec(target, spec, count)
	}

	if len(targets) == 1 {
		return fmt.Sprintf("Gave %d [%s] to %s", count, spec.ItemID, targets[0].Profile.Name)
	}
	return fmt.Sprintf("Gave %d [%s] to %d players", count, spec.ItemID, len(targets))
}

// --- 2. /clear [<targets>] [<item>] [<maxCount>] ---

func slotMatchesClearFilter(session *PlayerSession, slot int, st world.ItemStack, filter *ParsedItemSpec) bool {
	if st.IsEmpty() {
		return false
	}
	if filter == nil {
		return true
	}
	if st.ResolvedItemID() != filter.NumericID {
		return false
	}
	meta := GetSlotItemMeta(session, slot)
	if filter.HasDamageSpec && meta.Damage != filter.Meta.Damage {
		return false
	}
	if filter.HasNameSpec && meta.CustomName != filter.Meta.CustomName {
		return false
	}
	if filter.HasEnchSpec {
		for k, wantLvl := range filter.Meta.Enchantments {
			if meta.EnchantmentLevel(k) != wantLvl {
				return false
			}
		}
	}
	if filter.HasPotionSpec {
		if GetSlotPotionType(session, slot) != filter.PotionType {
			return false
		}
	}
	if filter.HasMapID {
		if mid, ok := GetSlotMapID(session, slot); !ok || mid != filter.MapID {
			return false
		}
	}
	return true
}

// ClearPlayerInventory clears or counts matching items in session's inventory.
// When maxCount == 0, no items are removed and the total matching item count is returned.
// When maxCount < 0, all matching items are removed.
func (s *Server) ClearPlayerInventory(session *PlayerSession, filter *ParsedItemSpec, maxCount int32) int32 {
	if session == nil {
		return 0
	}

	// Standard clear order: main inventory + hotbar (9..44), armor (5..8), offhand (45), crafting grid (1..4), cursor.
	slotOrder := make([]int, 0, world.PlayerInventorySlots)
	for i := 9; i < 45; i++ {
		slotOrder = append(slotOrder, i)
	}
	for i := 5; i <= 8; i++ {
		slotOrder = append(slotOrder, i)
	}
	slotOrder = append(slotOrder, 45)
	for i := 1; i <= 4; i++ {
		slotOrder = append(slotOrder, i)
	}

	if maxCount == 0 {
		var total int32
		for _, slot := range slotOrder {
			st := session.InventorySlot(slot)
			if slotMatchesClearFilter(session, slot, st, filter) {
				total += st.Count
			}
		}
		carried := session.CarriedItem()
		if !carried.IsEmpty() && (filter == nil || carried.ResolvedItemID() == filter.NumericID) {
			total += carried.Count
		}
		return total
	}

	remaining := maxCount // negative means unlimited
	var removed int32
	var changedSlots []int

	for _, slot := range slotOrder {
		if remaining == 0 {
			break
		}
		st := session.InventorySlot(slot)
		if !slotMatchesClearFilter(session, slot, st, filter) {
			continue
		}
		take := st.Count
		if remaining > 0 && take > remaining {
			take = remaining
		}
		newCount := st.Count - take
		if newCount <= 0 {
			session.SetInventorySlot(slot, world.ItemStack{})
			clearAllSlotMetadata(session, slot)
		} else {
			session.SetInventorySlot(slot, world.NewItemStack(st.ResolvedItemID(), newCount))
		}
		removed += take
		if remaining > 0 {
			remaining -= take
		}
		changedSlots = append(changedSlots, slot)
	}

	if remaining != 0 {
		carried := session.CarriedItem()
		if !carried.IsEmpty() && (filter == nil || carried.ResolvedItemID() == filter.NumericID) {
			take := carried.Count
			if remaining > 0 && take > remaining {
				take = remaining
			}
			newCount := carried.Count - take
			if newCount <= 0 {
				session.SetCarriedItem(world.ItemStack{})
			} else {
				session.SetCarriedItem(world.NewItemStack(carried.ResolvedItemID(), newCount))
			}
			removed += take
		}
	}

	for _, slot := range changedSlots {
		sendPlayerSlotUpdate(session, slot)
	}
	if s != nil && removed > 0 {
		s.TriggerInventoryChanged(session)
	}
	return removed
}

// HandleClearCommand implements the vanilla 26.1.2 /clear command:
//
//	/clear [<targets>] [<item>] [<maxCount>]
func (s *Server) HandleClearCommand(sender *PlayerSession, args []string) string {
	if s == nil {
		return "Server unavailable"
	}
	if !s.HasAdminPermission(sender) {
		return "You do not have permission to use this command"
	}
	if len(args) > 3 {
		return "Usage: /clear [<targets>] [<item>] [<maxCount>]"
	}

	var targets []*PlayerSession
	if len(args) == 0 {
		if sender == nil {
			return "Usage: /clear <targets> [<item>] [<maxCount>]"
		}
		targets = []*PlayerSession{sender}
	} else {
		targets = s.ResolveCommandTargets(sender, args[0])
		if len(targets) == 0 {
			return fmt.Sprintf("No player was found matching %q", args[0])
		}
	}

	var filter *ParsedItemSpec
	if len(args) >= 2 && args[1] != "*" {
		parsed, err := ParseItemArgument(args[1])
		if err != nil {
			return fmt.Sprintf("Invalid item: %v", err)
		}
		filter = &parsed
	}

	maxCount := int32(-1)
	if len(args) == 3 {
		n, err := strconv.ParseInt(args[2], 10, 32)
		if err != nil || n < -1 {
			return fmt.Sprintf("Invalid maxCount %q: must be >= -1", args[2])
		}
		maxCount = int32(n)
	}

	var total int32
	for _, t := range targets {
		total += s.ClearPlayerInventory(t, filter, maxCount)
	}

	if total == 0 {
		if len(targets) == 1 {
			return fmt.Sprintf("No items were found on player %s", targets[0].Profile.Name)
		}
		return fmt.Sprintf("No items were found on %d players", len(targets))
	}
	if maxCount == 0 {
		if len(targets) == 1 {
			return fmt.Sprintf("Found %d matching item(s) on player %s", total, targets[0].Profile.Name)
		}
		return fmt.Sprintf("Found %d matching item(s) on %d players", total, len(targets))
	}
	if len(targets) == 1 {
		return fmt.Sprintf("Removed %d item(s) from player %s", total, targets[0].Profile.Name)
	}
	return fmt.Sprintf("Removed %d item(s) from %d players", total, len(targets))
}

// --- 3. /effect (give | clear) ---

// HandleEffectCommand implements the vanilla 26.1.2 /effect command:
//   - /effect clear [<targets>] [<effect>]
//   - /effect give <targets> <effect> [<seconds>|infinite] [<amplifier>] [<hideParticles>]
func (s *Server) HandleEffectCommand(sender *PlayerSession, args []string) string {
	if s == nil {
		return "Server unavailable"
	}
	if !s.HasAdminPermission(sender) {
		return "You do not have permission to use this command"
	}
	if len(args) == 0 {
		return "Usage: /effect (clear|give) ..."
	}

	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "clear":
		if len(args) > 3 {
			return "Usage: /effect clear [<targets>] [<effect>]"
		}
		var targets []*PlayerSession
		if len(args) == 1 {
			if sender == nil {
				return "Usage: /effect clear <targets> [<effect>]"
			}
			targets = []*PlayerSession{sender}
		} else {
			targets = s.ResolveCommandTargets(sender, args[1])
			if len(targets) == 0 {
				return fmt.Sprintf("No target was found matching %q", args[1])
			}
		}

		if len(args) == 3 {
			def, ok := MobEffectByID(args[2])
			if !ok {
				return fmt.Sprintf("Unknown status effect %q", args[2])
			}
			removed := 0
			for _, t := range targets {
				if s.RemovePlayerEffect(t, def.ID) {
					removed++
				}
			}
			if removed == 0 {
				return fmt.Sprintf("Target(s) do not have effect %s", def.ID)
			}
			if len(targets) == 1 {
				return fmt.Sprintf("Removed effect %s from %s", def.ID, targets[0].Profile.Name)
			}
			return fmt.Sprintf("Removed effect %s from %d targets", def.ID, removed)
		}

		totalCleared := 0
		for _, t := range targets {
			totalCleared += s.ClearPlayerEffects(t)
		}
		if totalCleared == 0 {
			return "Target has no effects to remove"
		}
		if len(targets) == 1 {
			return fmt.Sprintf("Removed every effect from %s", targets[0].Profile.Name)
		}
		return fmt.Sprintf("Removed every effect from %d targets", len(targets))

	case "give":
		if len(args) < 3 || len(args) > 6 {
			return "Usage: /effect give <targets> <effect> [<seconds>|infinite] [<amplifier>] [<hideParticles>]"
		}
		targets := s.ResolveCommandTargets(sender, args[1])
		if len(targets) == 0 {
			return fmt.Sprintf("No target was found matching %q", args[1])
		}
		def, ok := MobEffectByID(args[2])
		if !ok {
			return fmt.Sprintf("Unknown status effect %q", args[2])
		}

		// Default duration: 30 seconds (600 ticks) for normal effects, 1 tick for instantaneous effects.
		durationTicks := int32(600)
		if def.Instantaneous {
			durationTicks = 1
		}
		if len(args) >= 4 {
			durArg := strings.ToLower(strings.TrimSpace(args[3]))
			if durArg == "infinite" {
				durationTicks = InfiniteEffectDuration
			} else {
				sec, err := strconv.ParseInt(durArg, 10, 32)
				if err != nil || sec < 0 || sec > 1000000 {
					return fmt.Errorf("Invalid duration %q: must be 'infinite' or 0..1000000 seconds", args[3]).Error()
				}
				if def.Instantaneous {
					if sec == 0 {
						durationTicks = 0
					} else {
						durationTicks = int32(sec)
					}
				} else {
					durationTicks = int32(sec) * 20
				}
			}
		}

		amplifier := int32(0)
		if len(args) >= 5 {
			amp, err := strconv.ParseInt(args[4], 10, 32)
			if err != nil || amp < 0 || amp > 255 {
				return fmt.Sprintf("Invalid amplifier %q: must be between 0 and 255", args[4])
			}
			amplifier = int32(amp)
		}

		hideParticles := false
		if len(args) == 6 {
			hp, err := strconv.ParseBool(strings.ToLower(strings.TrimSpace(args[5])))
			if err != nil {
				return fmt.Sprintf("Invalid hideParticles flag %q: expected true or false", args[5])
			}
			hideParticles = hp
		}

		inst := StatusEffectInstance{
			ID:            def.ID,
			Amplifier:     amplifier,
			Duration:      durationTicks,
			Ambient:       false,
			ShowParticles: !hideParticles,
			ShowIcon:      true,
		}

		applied := 0
		for _, t := range targets {
			if durationTicks == 0 {
				if s.RemovePlayerEffect(t, def.ID) {
					applied++
				}
				continue
			}
			if s.ApplyPlayerEffect(t, inst) {
				applied++
			}
		}
		if applied == 0 {
			return fmt.Sprintf("Unable to apply effect %s (target is immune or has a stronger effect)", def.ID)
		}
		if len(targets) == 1 {
			return fmt.Sprintf("Applied effect %s to %s", def.ID, targets[0].Profile.Name)
		}
		return fmt.Sprintf("Applied effect %s to %d targets", def.ID, applied)

	default:
		return "Usage: /effect (clear|give) ..."
	}
}

// --- 4. /enchant <targets> <enchantment> [<level>] ---

// EnchantPlayerHeldItem enchants the item in session's main hand with
// (enchantID, level), enforcing vanilla /enchant rules (item compatibility,
// level <= MaxLevel, and mutual-exclusivity with existing enchantments).
func (s *Server) EnchantPlayerHeldItem(session *PlayerSession, enchantID string, level int) error {
	if session == nil {
		return fmt.Errorf("no target player")
	}
	def, ok := EnchantmentByID(enchantID)
	if !ok {
		return fmt.Errorf("unknown enchantment %q", enchantID)
	}
	if level < 1 {
		return fmt.Errorf("level %d must be at least 1", level)
	}
	if level > def.MaxLevel {
		return fmt.Errorf("%d is higher than the maximum level of %d supported by %s", level, def.MaxLevel, def.ID)
	}

	sel := int(session.SelectedSlot())
	if sel < 0 || sel >= world.HotbarSlotCount {
		sel = 0
	}
	slot := world.HotbarContainerOffset + sel
	held := session.InventorySlot(slot)
	if held.IsEmpty() {
		return fmt.Errorf("%s is not holding any item", session.Profile.Name)
	}

	itemName := held.ResolvedID()
	if !CanEnchantItem(def.ID, itemName, true) {
		return fmt.Errorf("%s cannot support enchantment %s", itemName, def.ID)
	}
	if held.Count != 1 {
		return fmt.Errorf("%s cannot be enchanted when stacked", itemName)
	}

	meta := GetSlotItemMeta(session, slot)
	for existingID, existingLvl := range meta.Enchantments {
		if existingLvl <= 0 {
			continue
		}
		if canonicalEnchantmentID(existingID) == def.ID {
			continue
		}
		if !AreEnchantmentsCompatible(def.ID, existingID) {
			return fmt.Errorf("%s is incompatible with %s", def.ID, existingID)
		}
	}

	meta.SetEnchantment(def.ID, level)
	if itemName == "minecraft:book" {
		if eb := world.NewNamedItemStack("minecraft:enchanted_book", 1); !eb.IsEmpty() {
			session.SetInventorySlot(slot, eb)
		}
	}
	SetSlotItemMeta(session, slot, meta)
	sendPlayerSlotUpdate(session, slot)
	if s != nil {
		s.TriggerEnchantedItem(session)
	}
	return nil
}

// HandleEnchantCommand implements the vanilla 26.1.2 /enchant command:
//
//	/enchant <targets> <enchantment> [<level>]
func (s *Server) HandleEnchantCommand(sender *PlayerSession, args []string) string {
	if s == nil {
		return "Server unavailable"
	}
	if !s.HasAdminPermission(sender) {
		return "You do not have permission to use this command"
	}
	if len(args) < 2 || len(args) > 3 {
		return "Usage: /enchant <targets> <enchantment> [<level>]"
	}
	targets := s.ResolveCommandTargets(sender, args[0])
	if len(targets) == 0 {
		return fmt.Sprintf("No target was found matching %q", args[0])
	}
	def, ok := EnchantmentByID(args[1])
	if !ok {
		return fmt.Sprintf("Unknown enchantment %q", args[1])
	}
	level := 1
	if len(args) == 3 {
		lvl, err := strconv.Atoi(args[2])
		if err != nil || lvl < 1 {
			return fmt.Sprintf("Invalid enchantment level %q: must be >= 1", args[2])
		}
		if lvl > def.MaxLevel {
			return fmt.Sprintf("%d is higher than the maximum level of %d supported by that enchantment", lvl, def.MaxLevel)
		}
		level = lvl
	}

	var succeeded int
	var lastErr error
	for _, t := range targets {
		if err := s.EnchantPlayerHeldItem(t, def.ID, level); err != nil {
			lastErr = err
		} else {
			succeeded++
		}
	}
	if succeeded == 0 {
		if lastErr != nil {
			return fmt.Sprintf("Could not enchant item: %v", lastErr)
		}
		return "Nothing changed; targets either hold no item or the enchantment could not be applied"
	}
	if len(targets) == 1 {
		return fmt.Sprintf("Applied enchantment %s %d to %s's item", def.ID, level, targets[0].Profile.Name)
	}
	return fmt.Sprintf("Applied enchantment %s %d to %d entities", def.ID, level, succeeded)
}

// --- 5. /item (replace | modify) (block | entity) ---

// ResolvePlayerInventorySlot resolves a vanilla slot identifier
// (e.g. "weapon", "weapon.mainhand", "weapon.offhand", "armor.head",
// "armor.chest", "armor.legs", "armor.feet", "hotbar.0".."hotbar.8",
// "inventory.0".."inventory.26", "container.0".."container.45") to a
// PlayerSession 46-slot container index.
func ResolvePlayerInventorySlot(session *PlayerSession, slotName string) (int, error) {
	s := strings.ToLower(strings.TrimSpace(slotName))
	switch s {
	case "weapon", "weapon.mainhand":
		sel := 0
		if session != nil {
			sel = int(session.SelectedSlot())
			if sel < 0 || sel >= world.HotbarSlotCount {
				sel = 0
			}
		}
		return world.HotbarContainerOffset + sel, nil
	case "weapon.offhand":
		return 45, nil
	case "armor.head":
		return ArmorSlotHead, nil
	case "armor.chest":
		return ArmorSlotChest, nil
	case "armor.legs":
		return ArmorSlotLegs, nil
	case "armor.feet":
		return ArmorSlotFeet, nil
	}

	if strings.HasPrefix(s, "hotbar.") {
		idx, err := strconv.Atoi(strings.TrimPrefix(s, "hotbar."))
		if err != nil || idx < 0 || idx >= world.HotbarSlotCount {
			return -1, fmt.Errorf("invalid hotbar slot %q (expected hotbar.0..hotbar.8)", slotName)
		}
		return world.HotbarContainerOffset + idx, nil
	}
	if strings.HasPrefix(s, "inventory.") {
		idx, err := strconv.Atoi(strings.TrimPrefix(s, "inventory."))
		if err != nil || idx < 0 || idx >= 27 {
			return -1, fmt.Errorf("invalid inventory slot %q (expected inventory.0..inventory.26)", slotName)
		}
		return 9 + idx, nil
	}
	if strings.HasPrefix(s, "container.") {
		idx, err := strconv.Atoi(strings.TrimPrefix(s, "container."))
		if err != nil || idx < 0 || idx >= world.PlayerInventorySlots {
			return -1, fmt.Errorf("invalid player container slot %q", slotName)
		}
		if idx < 9 {
			return world.HotbarContainerOffset + idx, nil
		}
		return idx, nil
	}
	return -1, fmt.Errorf("unknown entity slot %q", slotName)
}

// ParseBlockContainerSlot parses a block container slot name ("container.0".."container.26").
func ParseBlockContainerSlot(slotName string) (int, error) {
	s := strings.ToLower(strings.TrimSpace(slotName))
	if !strings.HasPrefix(s, "container.") {
		return -1, fmt.Errorf("invalid block slot %q (expected container.<index>)", slotName)
	}
	idx, err := strconv.Atoi(strings.TrimPrefix(s, "container."))
	if err != nil || idx < 0 || idx >= ChestSlotCount {
		return -1, fmt.Errorf("block container slot %q out of range", slotName)
	}
	return idx, nil
}

func parseCommandBlockCoord(raw string, origin int) (int, error) {
	s := strings.TrimSpace(raw)
	if strings.HasPrefix(s, "~") {
		rest := strings.TrimSpace(s[1:])
		if rest == "" {
			return origin, nil
		}
		off, err := strconv.ParseFloat(rest, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid relative coordinate %q", raw)
		}
		return origin + int(math.Floor(off)), nil
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("invalid block coordinate %q", raw)
	}
	return v, nil
}

func parseCommandBlockPos(sender *PlayerSession, args []string) (x, y, z int, err error) {
	if len(args) < 3 {
		return 0, 0, 0, fmt.Errorf("missing block coordinates <x> <y> <z>")
	}
	var ox, oy, oz int
	if sender != nil {
		snap := sender.Snapshot()
		ox = int(math.Floor(snap.X))
		oy = int(math.Floor(snap.Y))
		oz = int(math.Floor(snap.Z))
	}
	x, err = parseCommandBlockCoord(args[0], ox)
	if err != nil {
		return 0, 0, 0, err
	}
	y, err = parseCommandBlockCoord(args[1], oy)
	if err != nil {
		return 0, 0, 0, err
	}
	z, err = parseCommandBlockCoord(args[2], oz)
	if err != nil {
		return 0, 0, 0, err
	}
	return x, y, z, nil
}

// GetBlockContainerSlotItem reads an item and its spec from a block container
// (chest, furnace/blast_furnace/smoker, brewing stand, or lectern) at (x, y, z).
func (s *Server) GetBlockContainerSlotItem(dim string, x, y, z, slot int) (world.ItemStack, ParsedItemSpec, error) {
	if s == nil {
		return world.ItemStack{}, ParsedItemSpec{}, fmt.Errorf("server unavailable")
	}
	dim = world.NormalizeDimension(dim)
	var state uint16
	if cache := s.chunksForDimension(dim); cache != nil {
		state = cache.GetBlock(x, y, z)
	}

	switch {
	case IsChestBlock(state):
		if slot < 0 || slot >= ChestSlotCount {
			return world.ItemStack{}, ParsedItemSpec{}, fmt.Errorf("chest slot %d out of range [0..26]", slot)
		}
		st := s.ChestSlots(x, y, z)[slot]
		return st, specFromStack(st), nil

	case IsFurnaceBlock(state):
		if slot < 0 || slot >= FurnaceSlotCount {
			return world.ItemStack{}, ParsedItemSpec{}, fmt.Errorf("furnace slot %d out of range [0..2]", slot)
		}
		st := s.FurnaceSlots(x, y, z)[slot]
		return st, specFromStack(st), nil

	case IsBrewingStandBlock(state):
		if slot < 0 || slot >= BrewingStandSlotCount {
			return world.ItemStack{}, ParsedItemSpec{}, fmt.Errorf("brewing stand slot %d out of range [0..4]", slot)
		}
		st := s.BrewingStandSlots(x, y, z)[slot]
		spec := specFromStack(st)
		if slot < 3 {
			spec.PotionType = s.GetBrewingSlotPotionType(x, y, z, slot)
		}
		return st, spec, nil

	case IsLecternBlock(state):
		if slot != 0 {
			return world.ItemStack{}, ParsedItemSpec{}, fmt.Errorf("lectern only supports container.0")
		}
		lec, ok := s.GetLecternAt(dim, x, y, z)
		if !ok || lec == nil || !lec.HasBook() {
			return world.ItemStack{}, ParsedItemSpec{}, nil
		}
		spec := specFromStack(lec.Book)
		spec.WritableBook = lec.Writable
		spec.WrittenBook = lec.Written
		return lec.Book, spec, nil

	default:
		// Fallback if a chest or furnace state was populated directly in tests without placing the block state.
		if f, ok := s.FurnaceAt(x, y, z); ok && slot >= 0 && slot < FurnaceSlotCount {
			st := f.Items[slot]
			return st, specFromStack(st), nil
		}
		if b, ok := s.BrewingStandAt(x, y, z); ok && slot >= 0 && slot < BrewingStandSlotCount {
			st := b.Items[slot]
			spec := specFromStack(st)
			if slot < 3 {
				spec.PotionType = s.GetBrewingSlotPotionType(x, y, z, slot)
			}
			return st, spec, nil
		}
		if lec, ok := s.GetLecternAt(dim, x, y, z); ok && lec != nil && slot == 0 {
			spec := specFromStack(lec.Book)
			spec.WritableBook = lec.Writable
			spec.WrittenBook = lec.Written
			return lec.Book, spec, nil
		}
		if slot >= 0 && slot < ChestSlotCount {
			st := s.ChestSlots(x, y, z)[slot]
			return st, specFromStack(st), nil
		}
		return world.ItemStack{}, ParsedItemSpec{}, fmt.Errorf("block at (%d, %d, %d) is not a container", x, y, z)
	}
}

// SetBlockContainerSlotItem writes stack and spec into a block container
// (chest, furnace/blast_furnace/smoker, brewing stand, or lectern) at (x, y, z).
func (s *Server) SetBlockContainerSlotItem(dim string, x, y, z, slot int, stack world.ItemStack, spec ParsedItemSpec) error {
	if s == nil {
		return fmt.Errorf("server unavailable")
	}
	dim = world.NormalizeDimension(dim)
	var state uint16
	if cache := s.chunksForDimension(dim); cache != nil {
		state = cache.GetBlock(x, y, z)
	}

	switch {
	case IsChestBlock(state):
		if slot < 0 || slot >= ChestSlotCount {
			return fmt.Errorf("chest slot %d out of range [0..26]", slot)
		}
		s.SetChestSlot(x, y, z, slot, stack)
		return nil

	case IsFurnaceBlock(state):
		if slot < 0 || slot >= FurnaceSlotCount {
			return fmt.Errorf("furnace slot %d out of range [0..2]", slot)
		}
		s.SetFurnaceSlot(x, y, z, slot, stack)
		return nil

	case IsBrewingStandBlock(state):
		if slot < 0 || slot >= BrewingStandSlotCount {
			return fmt.Errorf("brewing stand slot %d out of range [0..4]", slot)
		}
		s.SetBrewingSlot(x, y, z, slot, stack, spec.PotionType)
		return nil

	case IsLecternBlock(state):
		if slot != 0 {
			return fmt.Errorf("lectern only supports container.0")
		}
		if stack.IsEmpty() {
			_, _, _, _ = s.RemoveLecternAt(dim, x, y, z)
			return nil
		}
		pageCount := int32(1)
		if spec.WrittenBook != nil && len(spec.WrittenBook.Pages) > 0 {
			pageCount = int32(len(spec.WrittenBook.Pages))
		} else if spec.WritableBook != nil && len(spec.WritableBook.Pages) > 0 {
			pageCount = int32(len(spec.WritableBook.Pages))
		}
		s.SetLecternAt(&LecternBlockEntity{
			Dimension: dim,
			X:         x,
			Y:         y,
			Z:         z,
			Book:      world.NewItemStack(stack.ResolvedItemID(), 1),
			Writable:  spec.WritableBook,
			Written:   spec.WrittenBook,
			Page:      0,
			PageCount: pageCount,
		})
		return nil

	default:
		if _, ok := s.FurnaceAt(x, y, z); ok && slot >= 0 && slot < FurnaceSlotCount {
			s.SetFurnaceSlot(x, y, z, slot, stack)
			return nil
		}
		if _, ok := s.BrewingStandAt(x, y, z); ok && slot >= 0 && slot < BrewingStandSlotCount {
			s.SetBrewingSlot(x, y, z, slot, stack, spec.PotionType)
			return nil
		}
		if slot >= 0 && slot < ChestSlotCount {
			s.SetChestSlot(x, y, z, slot, stack)
			return nil
		}
		return fmt.Errorf("block at (%d, %d, %d) is not a container", x, y, z)
	}
}

func specFromStack(st world.ItemStack) ParsedItemSpec {
	if st.IsEmpty() {
		return ParsedItemSpec{}
	}
	return ParsedItemSpec{
		ItemID:    st.ResolvedID(),
		NumericID: st.ResolvedItemID(),
	}
}

func specFromPlayerSlot(session *PlayerSession, slot int) (world.ItemStack, ParsedItemSpec) {
	if session == nil || slot < 0 || slot >= world.PlayerInventorySlots {
		return world.ItemStack{}, ParsedItemSpec{}
	}
	st := session.InventorySlot(slot)
	if st.IsEmpty() {
		return world.ItemStack{}, ParsedItemSpec{}
	}
	spec := ParsedItemSpec{
		ItemID:     st.ResolvedID(),
		NumericID:  st.ResolvedItemID(),
		Meta:       GetSlotItemMeta(session, slot),
		PotionType: GetSlotPotionType(session, slot),
	}
	if mid, ok := GetSlotMapID(session, slot); ok {
		spec.MapID = mid
		spec.HasMapID = true
	}
	if wb, ok := GetSlotWritableBook(session, slot); ok {
		spec.WritableBook = &wb
	}
	if wr, ok := GetSlotWrittenBook(session, slot); ok {
		spec.WrittenBook = &wr
	}
	return st, spec
}

func setPlayerSlotFromSpec(s *Server, session *PlayerSession, slot int, stack world.ItemStack, spec ParsedItemSpec) {
	if session == nil || slot < 0 || slot >= world.PlayerInventorySlots {
		return
	}
	if stack.IsEmpty() || stack.Count <= 0 {
		session.SetInventorySlot(slot, world.ItemStack{})
		clearAllSlotMetadata(session, slot)
	} else {
		session.SetInventorySlot(slot, world.NewItemStack(stack.ResolvedItemID(), stack.Count))
		applySpecMetadataToSlot(session, slot, spec)
	}
	sendPlayerSlotUpdate(session, slot)
	if s != nil {
		s.TriggerInventoryChanged(session)
	}
}

// ApplyItemModifier applies an item modifier function (either JSON object/array
// or shorthand `set_count:<n>`, `set_damage:<d>`, `set_name:<name>`,
// `set_potion:<id>`, `filtered`) to (stack, spec).
func ApplyItemModifier(stack world.ItemStack, spec ParsedItemSpec, modifierRaw string) (world.ItemStack, ParsedItemSpec, error) {
	if stack.IsEmpty() {
		return world.ItemStack{}, ParsedItemSpec{}, fmt.Errorf("cannot modify an empty slot")
	}
	modStr := strings.TrimSpace(modifierRaw)
	if modStr == "" {
		return stack, spec, nil
	}

	// JSON item modifier object or array of modifiers.
	if strings.HasPrefix(modStr, "[") {
		var list []json.RawMessage
		if err := json.Unmarshal([]byte(modStr), &list); err != nil {
			return stack, spec, fmt.Errorf("invalid item modifier JSON array: %v", err)
		}
		var err error
		for _, elem := range list {
			stack, spec, err = ApplyItemModifier(stack, spec, string(elem))
			if err != nil {
				return stack, spec, err
			}
		}
		return stack, spec, nil
	}

	if strings.HasPrefix(modStr, "{") {
		var obj map[string]any
		if err := json.Unmarshal([]byte(modStr), &obj); err != nil {
			return stack, spec, fmt.Errorf("invalid item modifier JSON: %v", err)
		}
		fnName, _ := obj["function"].(string)
		fnCanon := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(fnName)), "minecraft:")
		switch fnCanon {
		case "set_count":
			if cntFloat, ok := obj["count"].(float64); ok {
				add, _ := obj["add"].(bool)
				newCount := int32(cntFloat)
				if add {
					newCount = stack.Count + newCount
				}
				maxSt := MaxStackSizeForSpec(spec)
				if newCount <= 0 {
					return world.ItemStack{}, ParsedItemSpec{}, nil
				}
				if newCount > maxSt {
					newCount = maxSt
				}
				stack.Count = newCount
			}
		case "set_damage":
			if dmgFloat, ok := obj["damage"].(float64); ok {
				maxDur := MaxItemDurability(spec.ItemID)
				if maxDur > 0 {
					add, _ := obj["add"].(bool)
					var newDmg int32
					if dmgFloat >= 0 && dmgFloat <= 1.0 && strings.Contains(modStr, ".") {
						// Vanilla set_damage fraction of remaining durability: (1 - fraction) * maxDur.
						remFrac := dmgFloat
						if add {
							curRem := 1.0 - float64(spec.Meta.Damage)/float64(maxDur)
							remFrac = curRem + dmgFloat
						}
						if remFrac < 0 {
							remFrac = 0
						} else if remFrac > 1 {
							remFrac = 1
						}
						newDmg = int32(math.Round((1.0 - remFrac) * float64(maxDur)))
					} else {
						newDmg = int32(dmgFloat)
						if add {
							newDmg += spec.Meta.Damage
						}
					}
					if newDmg < 0 {
						newDmg = 0
					} else if newDmg >= maxDur {
						newDmg = maxDur - 1
					}
					spec.Meta.Damage = newDmg
				}
			}
		case "set_name", "set_custom_name":
			if nameRaw, ok := obj["name"]; ok {
				switch v := nameRaw.(type) {
				case string:
					spec.Meta.CustomName = v
				case map[string]any:
					if b, err := json.Marshal(v); err == nil {
						if comp, err := ParseChatComponentJSON(string(b)); err == nil {
							spec.Meta.CustomName = comp.PlainText()
						}
					}
				}
			}
		case "set_potion":
			if idStr, ok := obj["id"].(string); ok {
				pCanon := canonicalItemName(idStr)
				if _, valid := PotionByID(pCanon); !valid {
					return stack, spec, fmt.Errorf("unknown potion %q", idStr)
				}
				spec.PotionType = pCanon
			}
		case "set_enchantments":
			if enchMap, ok := obj["enchantments"].(map[string]any); ok {
				add, _ := obj["add"].(bool)
				if !add {
					spec.Meta.Enchantments = nil
				}
				for k, v := range enchMap {
					def, valid := EnchantmentByID(k)
					if !valid {
						return stack, spec, fmt.Errorf("unknown enchantment %q", k)
					}
					if lf, ok := v.(float64); ok {
						lvl := int(lf)
						if add {
							lvl += spec.Meta.EnchantmentLevel(def.ID)
						}
						spec.Meta.SetEnchantment(def.ID, lvl)
					}
				}
			}
		case "set_components":
			if comps, ok := obj["components"].(map[string]any); ok {
				var parts []string
				for k, v := range comps {
					rawBytes, _ := json.Marshal(v)
					parts = append(parts, fmt.Sprintf("%s=%s", k, string(rawBytes)))
				}
				if err := parseItemComponentsBracket(&spec, strings.Join(parts, ",")); err != nil {
					return stack, spec, err
				}
			}
		case "filtered", "reference", "sequence":
			// No-op pass-through when sub-modifier is omitted.
		default:
			return stack, spec, fmt.Errorf("unknown item modifier function %q", fnName)
		}
		return stack, spec, nil
	}

	// Shorthand/resource modifier names (e.g. "minecraft:set_count:5", "set_damage:12", "set_name:Hero").
	short := strings.TrimPrefix(modStr, "minecraft:")
	switch {
	case strings.HasPrefix(short, "set_count:"):
		n, err := strconv.ParseInt(strings.TrimPrefix(short, "set_count:"), 10, 32)
		if err != nil || n < 0 {
			return stack, spec, fmt.Errorf("invalid set_count value in %q", modifierRaw)
		}
		if n == 0 {
			return world.ItemStack{}, ParsedItemSpec{}, nil
		}
		maxSt := MaxStackSizeForSpec(spec)
		if int32(n) > maxSt {
			n = int64(maxSt)
		}
		stack.Count = int32(n)
		return stack, spec, nil

	case strings.HasPrefix(short, "set_damage:"):
		d, err := strconv.ParseInt(strings.TrimPrefix(short, "set_damage:"), 10, 32)
		if err != nil || d < 0 {
			return stack, spec, fmt.Errorf("invalid set_damage value in %q", modifierRaw)
		}
		spec.Meta.Damage = int32(d)
		return stack, spec, nil

	case strings.HasPrefix(short, "set_name:"):
		spec.Meta.CustomName = trimMatchingQuotes(strings.TrimPrefix(short, "set_name:"))
		return stack, spec, nil

	case strings.HasPrefix(short, "set_potion:"):
		pCanon := canonicalItemName(strings.TrimPrefix(short, "set_potion:"))
		if _, ok := PotionByID(pCanon); !ok {
			return stack, spec, fmt.Errorf("unknown potion %q", pCanon)
		}
		spec.PotionType = pCanon
		return stack, spec, nil
	}

	return stack, spec, fmt.Errorf("unknown item modifier %q", modifierRaw)
}

// HandleItemCommand implements the vanilla 26.1.2 /item command:
//   - /item replace block <x> <y> <z> <slot> with <item> [<count>]
//   - /item replace entity <targets> <slot> with <item> [<count>]
//   - /item replace (block <x> <y> <z>|entity <targets>) <slot> from (block <srcX> <srcY> <srcZ>|entity <srcTargets>) <srcSlot> [<modifier>]
//   - /item modify (block <x> <y> <z>|entity <targets>) <slot> <modifier>
func (s *Server) HandleItemCommand(sender *PlayerSession, args []string) string {
	if s == nil {
		return "Server unavailable"
	}
	if !s.HasAdminPermission(sender) {
		return "You do not have permission to use this command"
	}
	if len(args) < 4 {
		return "Usage: /item (replace|modify) (block <x> <y> <z>|entity <targets>) <slot> ..."
	}

	dim := world.DimensionOverworld
	if sender != nil {
		dim = world.NormalizeDimension(sender.Dimension())
	}

	action := strings.ToLower(strings.TrimSpace(args[0]))
	targetKind := strings.ToLower(strings.TrimSpace(args[1]))

	var (
		isBlockTarget bool
		bx, by, bz    int
		entityTargets []*PlayerSession
		slotToken     string
		rest          []string
	)

	switch targetKind {
	case "block":
		if len(args) < 6 {
			return "Usage: /item (replace|modify) block <x> <y> <z> <slot> ..."
		}
		var err error
		bx, by, bz, err = parseCommandBlockPos(sender, args[2:5])
		if err != nil {
			return fmt.Sprintf("Invalid block position: %v", err)
		}
		isBlockTarget = true
		slotToken = args[5]
		rest = args[6:]

	case "entity":
		if len(args) < 4 {
			return "Usage: /item (replace|modify) entity <targets> <slot> ..."
		}
		entityTargets = s.ResolveCommandTargets(sender, args[2])
		if len(entityTargets) == 0 {
			return fmt.Sprintf("No target was found matching %q", args[2])
		}
		slotToken = args[3]
		rest = args[4:]

	default:
		return "Usage: /item (replace|modify) (block|entity) ..."
	}

	switch action {
	case "modify":
		if len(rest) == 0 {
			return "Usage: /item modify (block <x> <y> <z>|entity <targets>) <slot> <modifier>"
		}
		modRaw := strings.Join(rest, " ")
		if isBlockTarget {
			slotIdx, err := ParseBlockContainerSlot(slotToken)
			if err != nil {
				return err.Error()
			}
			curStack, curSpec, err := s.GetBlockContainerSlotItem(dim, bx, by, bz, slotIdx)
			if err != nil {
				return err.Error()
			}
			newStack, newSpec, err := ApplyItemModifier(curStack, curSpec, modRaw)
			if err != nil {
				return fmt.Sprintf("Failed to modify block slot: %v", err)
			}
			if err := s.SetBlockContainerSlotItem(dim, bx, by, bz, slotIdx, newStack, newSpec); err != nil {
				return err.Error()
			}
			return fmt.Sprintf("Modified item in (%d, %d, %d) slot %s", bx, by, bz, slotToken)
		}

		modified := 0
		var lastErr error
		for _, target := range entityTargets {
			slotIdx, err := ResolvePlayerInventorySlot(target, slotToken)
			if err != nil {
				return err.Error()
			}
			curStack, curSpec := specFromPlayerSlot(target, slotIdx)
			newStack, newSpec, err := ApplyItemModifier(curStack, curSpec, modRaw)
			if err != nil {
				lastErr = err
				continue
			}
			setPlayerSlotFromSpec(s, target, slotIdx, newStack, newSpec)
			modified++
		}
		if modified == 0 {
			if lastErr != nil {
				return fmt.Sprintf("Failed to modify slot: %v", lastErr)
			}
			return "No items were modified"
		}
		return fmt.Sprintf("Modified slot %s on %d entity(s)", slotToken, modified)

	case "replace":
		if len(rest) == 0 {
			return "Usage: /item replace ... (with <item> [<count>] | from (block|entity) ...)"
		}
		subMode := strings.ToLower(strings.TrimSpace(rest[0]))
		var (
			srcStack world.ItemStack
			srcSpec  ParsedItemSpec
		)

		switch subMode {
		case "with":
			if len(rest) < 2 || len(rest) > 3 {
				return "Usage: /item replace ... with <item> [<count>]"
			}
			parsed, err := ParseItemArgument(rest[1])
			if err != nil {
				return fmt.Sprintf("Invalid item: %v", err)
			}
			maxSt := MaxStackSizeForSpec(parsed)
			count := int32(1)
			if len(rest) == 3 {
				n, err := strconv.ParseInt(rest[2], 10, 32)
				if err != nil || n < 1 || int32(n) > maxSt {
					return fmt.Sprintf("Invalid count %q: must be between 1 and %d", rest[2], maxSt)
				}
				count = int32(n)
			}
			srcSpec = parsed
			srcStack = world.NewItemStack(parsed.NumericID, count)

		case "from":
			if len(rest) < 3 {
				return "Usage: /item replace ... from (block <x> <y> <z>|entity <source>) <slot> [<modifier>]"
			}
			srcKind := strings.ToLower(strings.TrimSpace(rest[1]))
			var modTail []string
			switch srcKind {
			case "block":
				if len(rest) < 6 {
					return "Usage: /item replace ... from block <x> <y> <z> <slot> [<modifier>]"
				}
				sx, sy, sz, err := parseCommandBlockPos(sender, rest[2:5])
				if err != nil {
					return fmt.Sprintf("Invalid source block position: %v", err)
				}
				sSlot, err := ParseBlockContainerSlot(rest[5])
				if err != nil {
					return err.Error()
				}
				srcStack, srcSpec, err = s.GetBlockContainerSlotItem(dim, sx, sy, sz, sSlot)
				if err != nil {
					return err.Error()
				}
				modTail = rest[6:]

			case "entity":
				if len(rest) < 4 {
					return "Usage: /item replace ... from entity <source> <slot> [<modifier>]"
				}
				srcs := s.ResolveCommandTargets(sender, rest[2])
				if len(srcs) != 1 {
					return fmt.Sprintf("Source selector %q must match exactly one entity", rest[2])
				}
				sSlot, err := ResolvePlayerInventorySlot(srcs[0], rest[3])
				if err != nil {
					return err.Error()
				}
				srcStack, srcSpec = specFromPlayerSlot(srcs[0], sSlot)
				modTail = rest[4:]

			default:
				return "Usage: /item replace ... from (block|entity) ..."
			}

			if len(modTail) > 0 && !srcStack.IsEmpty() {
				var err error
				srcStack, srcSpec, err = ApplyItemModifier(srcStack, srcSpec, strings.Join(modTail, " "))
				if err != nil {
					return fmt.Sprintf("Failed to apply item modifier: %v", err)
				}
			}

		default:
			return "Usage: /item replace ... (with|from) ..."
		}

		if isBlockTarget {
			slotIdx, err := ParseBlockContainerSlot(slotToken)
			if err != nil {
				return err.Error()
			}
			if err := s.SetBlockContainerSlotItem(dim, bx, by, bz, slotIdx, srcStack, srcSpec); err != nil {
				return err.Error()
			}
			itemName := "minecraft:air"
			if !srcStack.IsEmpty() {
				itemName = srcStack.ResolvedID()
			}
			return fmt.Sprintf("Replaced slot %s at (%d, %d, %d) with [%s]", slotToken, bx, by, bz, itemName)
		}

		for _, target := range entityTargets {
			slotIdx, err := ResolvePlayerInventorySlot(target, slotToken)
			if err != nil {
				return err.Error()
			}
			setPlayerSlotFromSpec(s, target, slotIdx, srcStack, srcSpec)
		}
		itemName := "minecraft:air"
		if !srcStack.IsEmpty() {
			itemName = srcStack.ResolvedID()
		}
		if len(entityTargets) == 1 {
			return fmt.Sprintf("Replaced slot %s on %s with [%s]", slotToken, entityTargets[0].Profile.Name, itemName)
		}
		return fmt.Sprintf("Replaced slot %s on %d entities with [%s]", slotToken, len(entityTargets), itemName)

	default:
		return "Usage: /item (replace|modify) ..."
	}
}

// HandleItemAndStatusCommand dispatches /give, /clear, /effect, /enchant, and
// /item, returning handled=true when cmdName matches one of those commands.
func (s *Server) HandleItemAndStatusCommand(sender *PlayerSession, cmdName string, args []string) (reply string, handled bool) {
	switch strings.ToLower(strings.TrimSpace(cmdName)) {
	case "give":
		return s.HandleGiveCommand(sender, args), true
	case "clear":
		return s.HandleClearCommand(sender, args), true
	case "effect":
		return s.HandleEffectCommand(sender, args), true
	case "enchant":
		return s.HandleEnchantCommand(sender, args), true
	case "item":
		return s.HandleItemCommand(sender, args), true
	default:
		return "", false
	}
}
