package world

import (
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"regionio/internal/nbt"
)

// Player inventory container layout constants (container ID 0).
const (
	PlayerInventorySlots  = 46
	HotbarSlotCount       = 9
	HotbarContainerOffset = 36

	DefaultPlayerHealth     float32 = 20.0
	DefaultPlayerFoodLevel  int32   = 20
	DefaultPlayerSaturation float32 = 5.0
)

// ItemStack represents one inventory slot's item and stack size.
type ItemStack struct {
	ItemID int32  // Network item ID (> 0 when resolved)
	ID     string // Canonical item resource identifier (e.g. "minecraft:stone")
	Count  int32  // Stack count (<= 0 means empty)
}

// NewItemStack constructs an ItemStack from a network item ID and count.
func NewItemStack(itemID int32, count int32) ItemStack {
	if itemID < 0 || count <= 0 {
		return ItemStack{}
	}
	return ItemStack{
		ItemID: itemID,
		ID:     ItemIDToName(itemID),
		Count:  count,
	}
}

// NewNamedItemStack constructs an ItemStack from an item identifier and count.
func NewNamedItemStack(id string, count int32) ItemStack {
	id = normalizeItemName(id)
	if count <= 0 || id == "" || id == "minecraft:air" {
		return ItemStack{}
	}
	itemID, ok := ItemNameToID(id)
	if !ok {
		itemID = -1
	}
	return ItemStack{
		ItemID: itemID,
		ID:     id,
		Count:  count,
	}
}

// IsEmpty reports whether the stack holds no items.
func (s ItemStack) IsEmpty() bool {
	if s.Count <= 0 {
		return true
	}
	if s.ItemID < 0 && (s.ID == "" || s.ID == "minecraft:air") {
		return true
	}
	if s.ItemID == 0 && (s.ID == "" || s.ID == "minecraft:air") {
		return true
	}
	return false
}

// ResolvedID returns the canonical resource name for this stack.
func (s ItemStack) ResolvedID() string {
	if s.IsEmpty() {
		return "minecraft:air"
	}
	if s.ID != "" && s.ID != "minecraft:air" {
		return normalizeItemName(s.ID)
	}
	return ItemIDToName(s.ItemID)
}

// ResolvedItemID returns the network item ID for this stack, or -1 if empty/unknown.
func (s ItemStack) ResolvedItemID() int32 {
	if s.IsEmpty() {
		return -1
	}
	if s.ItemID > 0 {
		return s.ItemID
	}
	if s.ID != "" {
		if id, ok := ItemNameToID(s.ID); ok {
			return id
		}
	}
	return s.ItemID
}

var (
	itemTableOnce sync.Once
	itemTableMu   sync.RWMutex
	itemIDToName  map[int32]string
	itemNameToID  map[string]int32
)

func buildItemTable() {
	stateByIDOnce.Do(buildStateTable)
	itemIDToName = make(map[int32]string, len(itemToBlock)+1)
	itemNameToID = make(map[string]int32, len(itemToBlock)+1)
	itemIDToName[0] = "minecraft:air"
	itemNameToID["minecraft:air"] = 0

	for itemID, blockState := range itemToBlock {
		st, ok := stateByID(blockState)
		if !ok || st.Name == "" {
			continue
		}
		itemIDToName[itemID] = st.Name
		if prev, exists := itemNameToID[st.Name]; !exists || itemID < prev {
			itemNameToID[st.Name] = itemID
		}
	}
}

// RegisterItems merges a map of canonical item names to network IDs into the
// item lookup table so non-block items (sticks, tools, ingots, food, etc.)
// resolve to their protocol item IDs.
func RegisterItems(items map[string]int32) {
	itemTableOnce.Do(buildItemTable)
	itemTableMu.Lock()
	defer itemTableMu.Unlock()
	for rawName, id := range items {
		name := normalizeItemName(rawName)
		if name == "" || id < 0 {
			continue
		}
		itemIDToName[id] = name
		itemNameToID[name] = id
	}
}

func normalizeItemName(name string) string {
	name = strings.TrimSpace(strings.ToLower(name))
	if name == "" {
		return ""
	}
	if !strings.Contains(name, ":") {
		return "minecraft:" + name
	}
	return name
}

// ItemIDToName converts a network item ID into its resource location string.
func ItemIDToName(itemID int32) string {
	if itemID <= 0 {
		return "minecraft:air"
	}
	itemTableOnce.Do(buildItemTable)
	itemTableMu.RLock()
	name, ok := itemIDToName[itemID]
	itemTableMu.RUnlock()
	if ok {
		return name
	}
	return fmt.Sprintf("minecraft:item_%d", itemID)
}

// ItemNameToID resolves an item resource location string to its network item ID.
func ItemNameToID(name string) (int32, bool) {
	name = normalizeItemName(name)
	if name == "" || name == "minecraft:air" {
		return 0, false
	}
	itemTableOnce.Do(buildItemTable)
	itemTableMu.RLock()
	id, ok := itemNameToID[name]
	itemTableMu.RUnlock()
	if ok {
		return id, true
	}
	if strings.HasPrefix(name, "minecraft:item_") {
		raw := strings.TrimPrefix(name, "minecraft:item_")
		if parsed, err := strconv.ParseInt(raw, 10, 32); err == nil && parsed > 0 {
			return int32(parsed), true
		}
	}
	return -1, false
}

// PlayerData holds the persistent state of a player stored in playerdata/<uuid>.dat.
type PlayerData struct {
	Dimension      string
	Pos            [3]float64
	Yaw            float32
	Pitch          float32
	OnGround       bool
	GameMode       uint8
	Health         float32
	FoodLevel      int32
	FoodSaturation float32
	FoodExhaustion float32
	FoodTickTimer  int32
	XpLevel        int32
	XpTotal        int32
	XpP            float32
	XpSeed         int32
	SelectedSlot   int32
	Inventory      [PlayerInventorySlots]ItemStack
	SpawnX         int32
	SpawnY         int32
	SpawnZ         int32
	SpawnYaw       float32
	SpawnPitch     float32
	SpawnForced    bool
	SpawnDimension   string
	HasSpawn         bool
	Advancements     *nbt.Compound
	ActiveEffects    *nbt.List
	AbsorptionAmount float32
	SlotPotions      *nbt.List
	SlotMaps         *nbt.List
}

// DefaultPlayerData returns the initial persistent state for a newly spawned player.
func DefaultPlayerData(x, y, z float64) PlayerData {
	sx := int32(math.Floor(x))
	sy := int32(math.Floor(y))
	sz := int32(math.Floor(z))
	return PlayerData{
		Dimension:      DimensionOverworld,
		Pos:            [3]float64{x, y, z},
		Yaw:            0,
		Pitch:          0,
		OnGround:       true,
		GameMode:       1, // Creative default
		Health:         DefaultPlayerHealth,
		FoodLevel:      DefaultPlayerFoodLevel,
		FoodSaturation: DefaultPlayerSaturation,
		SelectedSlot:   0,
		SpawnX:         sx,
		SpawnY:         sy,
		SpawnZ:         sz,
		SpawnDimension: DimensionOverworld,
		HasSpawn:       true,
	}
}

// PlayerDataToCompound converts PlayerData into a vanilla playerdata NBT root compound.
func PlayerDataToCompound(d PlayerData) *nbt.Compound {
	root := nbt.NewCompound()
	root.Set("DataVersion", nbt.Int(dataVersion26))
	root.Set("Dimension", nbt.String(NormalizeDimension(d.Dimension)))
	root.Set("Pos", nbt.List{
		ElemID: nbt.TagDouble,
		Elems: []nbt.Tag{
			nbt.Double(d.Pos[0]),
			nbt.Double(d.Pos[1]),
			nbt.Double(d.Pos[2]),
		},
	})
	root.Set("Rotation", nbt.List{
		ElemID: nbt.TagFloat,
		Elems: []nbt.Tag{
			nbt.Float(d.Yaw),
			nbt.Float(d.Pitch),
		},
	})
	if d.OnGround {
		root.Set("OnGround", nbt.Byte(1))
	} else {
		root.Set("OnGround", nbt.Byte(0))
	}
	root.Set("playerGameType", nbt.Int(int32(d.GameMode)))

	health := d.Health
	if health <= 0 {
		health = DefaultPlayerHealth
	}
	root.Set("Health", nbt.Float(health))
	root.Set("foodLevel", nbt.Int(d.FoodLevel))
	root.Set("foodSaturationLevel", nbt.Float(d.FoodSaturation))
	root.Set("foodExhaustionLevel", nbt.Float(d.FoodExhaustion))
	root.Set("foodTickTimer", nbt.Int(d.FoodTickTimer))
	root.Set("XpLevel", nbt.Int(d.XpLevel))
	root.Set("XpTotal", nbt.Int(d.XpTotal))
	root.Set("XpP", nbt.Float(d.XpP))
	root.Set("XpSeed", nbt.Int(d.XpSeed))

	selected := d.SelectedSlot
	if selected < 0 || selected >= HotbarSlotCount {
		selected = 0
	}
	root.Set("SelectedItemSlot", nbt.Int(selected))

	invElems := make([]nbt.Tag, 0)
	for slot, stack := range d.Inventory {
		if stack.IsEmpty() {
			continue
		}
		itemComp := nbt.NewCompound()
		itemComp.Set("Slot", nbt.Byte(int8(slot)))
		itemComp.Set("id", nbt.String(stack.ResolvedID()))
		itemComp.Set("count", nbt.Int(stack.Count))
		itemComp.Set("Count", nbt.Byte(int8(stack.Count)))
		if itemID := stack.ResolvedItemID(); itemID > 0 {
			itemComp.Set("item_id", nbt.Int(itemID))
		}
		invElems = append(invElems, itemComp)
	}
	elemID := byte(nbt.TagCompound)
	if len(invElems) == 0 {
		elemID = nbt.TagEnd
	}
	root.Set("Inventory", nbt.List{
		ElemID: elemID,
		Elems:  invElems,
	})

	if d.HasSpawn || d.SpawnX != 0 || d.SpawnY != 0 || d.SpawnZ != 0 || d.SpawnForced {
		dim := NormalizeDimension(d.SpawnDimension)
		var forcedByte int8
		if d.SpawnForced {
			forcedByte = 1
		}
		root.Set("SpawnX", nbt.Int(d.SpawnX))
		root.Set("SpawnY", nbt.Int(d.SpawnY))
		root.Set("SpawnZ", nbt.Int(d.SpawnZ))
		root.Set("SpawnAngle", nbt.Float(d.SpawnYaw))
		root.Set("SpawnForced", nbt.Byte(forcedByte))
		root.Set("SpawnDimension", nbt.String(dim))

		respawn := nbt.NewCompound()
		respawn.Set("pos", nbt.IntArray([]int32{d.SpawnX, d.SpawnY, d.SpawnZ}))
		respawn.Set("dimension", nbt.String(dim))
		respawn.Set("yaw", nbt.Float(d.SpawnYaw))
		respawn.Set("pitch", nbt.Float(d.SpawnPitch))
		respawn.Set("forced", nbt.Byte(forcedByte))
		root.Set("respawn", respawn)
	}

	if d.Advancements != nil {
		root.Set("Advancements", d.Advancements)
	}
	if d.ActiveEffects != nil && len(d.ActiveEffects.Elems) > 0 {
		root.Set("active_effects", *d.ActiveEffects)
	}
	if d.AbsorptionAmount > 0 {
		root.Set("AbsorptionAmount", nbt.Float(d.AbsorptionAmount))
	}
	if d.SlotPotions != nil && len(d.SlotPotions.Elems) > 0 {
		root.Set("SlotPotions", *d.SlotPotions)
	}
	if d.SlotMaps != nil && len(d.SlotMaps.Elems) > 0 {
		root.Set("SlotMaps", *d.SlotMaps)
	}

	return root
}

func nbtGetList(c *nbt.Compound, key string) (*nbt.List, bool) {
	t, ok := c.Get(key)
	if !ok {
		return nil, false
	}
	switch v := t.(type) {
	case *nbt.List:
		return v, true
	case nbt.List:
		return &v, true
	default:
		return nil, false
	}
}

func nbtGetCompound(c *nbt.Compound, key string) (*nbt.Compound, bool) {
	t, ok := c.Get(key)
	if !ok {
		return nil, false
	}
	v, ok := t.(*nbt.Compound)
	return v, ok
}

func nbtGetByte(c *nbt.Compound, key string) (int8, bool) {
	t, ok := c.Get(key)
	if !ok {
		return 0, false
	}
	v, ok := t.(nbt.Byte)
	return int8(v), ok
}

func nbtGetShort(c *nbt.Compound, key string) (int16, bool) {
	t, ok := c.Get(key)
	if !ok {
		return 0, false
	}
	v, ok := t.(nbt.Short)
	return int16(v), ok
}

func nbtGetInt(c *nbt.Compound, key string) (int32, bool) {
	t, ok := c.Get(key)
	if !ok {
		return 0, false
	}
	v, ok := t.(nbt.Int)
	return int32(v), ok
}

func nbtGetFloat(c *nbt.Compound, key string) (float32, bool) {
	t, ok := c.Get(key)
	if !ok {
		return 0, false
	}
	v, ok := t.(nbt.Float)
	return float32(v), ok
}

func nbtGetString(c *nbt.Compound, key string) (string, bool) {
	t, ok := c.Get(key)
	if !ok {
		return "", false
	}
	v, ok := t.(nbt.String)
	return string(v), ok
}

func nbtGetIntArray(c *nbt.Compound, key string) ([]int32, bool) {
	t, ok := c.Get(key)
	if !ok {
		return nil, false
	}
	v, ok := t.(nbt.IntArray)
	return []int32(v), ok
}

// PlayerDataFromCompound decodes PlayerData from a vanilla playerdata NBT root compound.
func PlayerDataFromCompound(root *nbt.Compound) (PlayerData, error) {
	if root == nil {
		return PlayerData{}, errors.New("world: nil playerdata NBT compound")
	}
	d := PlayerData{
		Dimension:      DimensionOverworld,
		OnGround:       true,
		GameMode:       1,
		Health:         DefaultPlayerHealth,
		FoodLevel:      DefaultPlayerFoodLevel,
		FoodSaturation: DefaultPlayerSaturation,
		SelectedSlot:   0,
		SpawnDimension: DimensionOverworld,
	}

	if dimStr, ok := nbtGetString(root, "Dimension"); ok && dimStr != "" {
		d.Dimension = NormalizeDimension(dimStr)
	} else if dimInt, ok := nbtGetInt(root, "Dimension"); ok {
		switch dimInt {
		case -1:
			d.Dimension = DimensionNether
		case 1:
			d.Dimension = DimensionEnd
		default:
			d.Dimension = DimensionOverworld
		}
	}

	if posList, ok := nbtGetList(root, "Pos"); ok && len(posList.Elems) == 3 {
		p0, ok0 := posList.Elems[0].(nbt.Double)
		p1, ok1 := posList.Elems[1].(nbt.Double)
		p2, ok2 := posList.Elems[2].(nbt.Double)
		if !ok0 || !ok1 || !ok2 {
			return PlayerData{}, errors.New("world: invalid Pos tag in playerdata")
		}
		d.Pos = [3]float64{float64(p0), float64(p1), float64(p2)}
	}

	if rotList, ok := nbtGetList(root, "Rotation"); ok && len(rotList.Elems) == 2 {
		yaw, ok0 := rotList.Elems[0].(nbt.Float)
		pitch, ok1 := rotList.Elems[1].(nbt.Float)
		if !ok0 || !ok1 {
			return PlayerData{}, errors.New("world: invalid Rotation tag in playerdata")
		}
		d.Yaw = float32(yaw)
		d.Pitch = float32(pitch)
	}

	if og, ok := nbtGetByte(root, "OnGround"); ok {
		d.OnGround = og != 0
	}

	if gm, ok := nbtGetInt(root, "playerGameType"); ok && gm >= 0 && gm <= 3 {
		d.GameMode = uint8(gm)
	} else if gmByte, ok := nbtGetByte(root, "playerGameType"); ok && gmByte >= 0 && gmByte <= 3 {
		d.GameMode = uint8(gmByte)
	}

	if h, ok := nbtGetFloat(root, "Health"); ok {
		d.Health = h
	}
	if food, ok := nbtGetInt(root, "foodLevel"); ok {
		d.FoodLevel = food
	} else if foodShort, ok := nbtGetShort(root, "foodLevel"); ok {
		d.FoodLevel = int32(foodShort)
	}
	if sat, ok := nbtGetFloat(root, "foodSaturationLevel"); ok {
		d.FoodSaturation = sat
	}
	if ex, ok := nbtGetFloat(root, "foodExhaustionLevel"); ok && ex >= 0 {
		d.FoodExhaustion = ex
	}
	if tt, ok := nbtGetInt(root, "foodTickTimer"); ok && tt >= 0 {
		d.FoodTickTimer = tt
	} else if ttShort, ok := nbtGetShort(root, "foodTickTimer"); ok && ttShort >= 0 {
		d.FoodTickTimer = int32(ttShort)
	}
	if xpLvl, ok := nbtGetInt(root, "XpLevel"); ok && xpLvl >= 0 {
		d.XpLevel = xpLvl
	}
	if xpTot, ok := nbtGetInt(root, "XpTotal"); ok && xpTot >= 0 {
		d.XpTotal = xpTot
	}
	if xpP, ok := nbtGetFloat(root, "XpP"); ok && xpP >= 0 {
		if xpP > 1 {
			xpP = 1
		}
		d.XpP = xpP
	}
	if xpSeed, ok := nbtGetInt(root, "XpSeed"); ok {
		d.XpSeed = xpSeed
	}

	if sel, ok := nbtGetInt(root, "SelectedItemSlot"); ok && sel >= 0 && sel < HotbarSlotCount {
		d.SelectedSlot = sel
	}

	if invList, ok := nbtGetList(root, "Inventory"); ok {
		for _, elem := range invList.Elems {
			comp, ok := elem.(*nbt.Compound)
			if !ok {
				continue
			}
			slotIdx := -1
			if sb, ok := nbtGetByte(comp, "Slot"); ok {
				slotIdx = mapNBTSlotToContainer(int(sb))
			} else if si, ok := nbtGetInt(comp, "Slot"); ok {
				slotIdx = mapNBTSlotToContainer(int(si))
			}
			if slotIdx < 0 || slotIdx >= PlayerInventorySlots {
				continue
			}

			var count int32
			if c, ok := nbtGetInt(comp, "count"); ok {
				count = c
			} else if cb, ok := nbtGetByte(comp, "count"); ok {
				count = int32(uint8(cb))
			} else if cb, ok := nbtGetByte(comp, "Count"); ok {
				count = int32(uint8(cb))
			} else if ci, ok := nbtGetInt(comp, "Count"); ok {
				count = ci
			}
			if count <= 0 {
				continue
			}

			idStr, _ := nbtGetString(comp, "id")
			idStr = normalizeItemName(idStr)
			var itemID int32 = -1
			if rawID, ok := nbtGetInt(comp, "item_id"); ok && rawID > 0 {
				itemID = rawID
			} else if idStr != "" {
				if resolved, ok := ItemNameToID(idStr); ok {
					itemID = resolved
				}
			}
			if idStr == "" && itemID > 0 {
				idStr = ItemIDToName(itemID)
			}
			if idStr == "minecraft:air" || (idStr == "" && itemID <= 0) {
				continue
			}
			d.Inventory[slotIdx] = ItemStack{
				ItemID: itemID,
				ID:     idStr,
				Count:  count,
			}
		}
	}

	if respawn, ok := nbtGetCompound(root, "respawn"); ok {
		if pos, ok := nbtGetIntArray(respawn, "pos"); ok && len(pos) == 3 {
			d.SpawnX = pos[0]
			d.SpawnY = pos[1]
			d.SpawnZ = pos[2]
			d.HasSpawn = true
		}
		if dim, ok := nbtGetString(respawn, "dimension"); ok && dim != "" {
			d.SpawnDimension = dim
		}
		if yaw, ok := nbtGetFloat(respawn, "yaw"); ok {
			d.SpawnYaw = yaw
		}
		if pitch, ok := nbtGetFloat(respawn, "pitch"); ok {
			d.SpawnPitch = pitch
		}
		if forced, ok := nbtGetByte(respawn, "forced"); ok {
			d.SpawnForced = forced != 0
		}
	}

	sx, okX := nbtGetInt(root, "SpawnX")
	sy, okY := nbtGetInt(root, "SpawnY")
	sz, okZ := nbtGetInt(root, "SpawnZ")
	if okX && okY && okZ {
		d.SpawnX = sx
		d.SpawnY = sy
		d.SpawnZ = sz
		d.HasSpawn = true
		if angle, ok := nbtGetFloat(root, "SpawnAngle"); ok {
			d.SpawnYaw = angle
		}
		if forced, ok := nbtGetByte(root, "SpawnForced"); ok {
			d.SpawnForced = forced != 0
		}
		if dim, ok := nbtGetString(root, "SpawnDimension"); ok && dim != "" {
			d.SpawnDimension = dim
		}
	}

	if adv, ok := nbtGetCompound(root, "Advancements"); ok && adv != nil {
		d.Advancements = adv
	}
	if eff, ok := nbtGetList(root, "active_effects"); ok && eff != nil {
		cp := *eff
		d.ActiveEffects = &cp
	}
	if abs, ok := nbtGetFloat(root, "AbsorptionAmount"); ok && abs > 0 {
		d.AbsorptionAmount = abs
	}
	if sp, ok := nbtGetList(root, "SlotPotions"); ok && sp != nil {
		cp := *sp
		d.SlotPotions = &cp
	}
	if sm, ok := nbtGetList(root, "SlotMaps"); ok && sm != nil {
		cp := *sm
		d.SlotMaps = &cp
	}

	return d, nil
}

func mapNBTSlotToContainer(slot int) int {
	if slot >= 0 && slot < PlayerInventorySlots {
		return slot
	}
	if slot == -106 || uint8(slot) == 150 {
		return 45 // offhand
	}
	if slot >= 100 && slot <= 103 {
		return 8 - (slot - 100) // feet..head -> 8..5
	}
	return -1
}

// MarshalPlayerDataNBT serializes PlayerData as gzip-compressed named NBT
// matching vanilla's playerdata/<uuid>.dat file format.
func MarshalPlayerDataNBT(d PlayerData) ([]byte, error) {
	rawNBT := nbt.MarshalNamed("", PlayerDataToCompound(d))
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(rawNBT); err != nil {
		_ = gz.Close()
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// UnmarshalPlayerDataNBT parses PlayerData from gzip-compressed or uncompressed
// named NBT bytes.
func UnmarshalPlayerDataNBT(raw []byte) (PlayerData, error) {
	if len(raw) == 0 {
		return PlayerData{}, errors.New("world: empty playerdata bytes")
	}
	nbtBytes := raw
	if len(raw) >= 2 && raw[0] == 0x1f && raw[1] == 0x8b {
		gr, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return PlayerData{}, fmt.Errorf("world: gunzip playerdata: %w", err)
		}
		defer gr.Close()
		nbtBytes, err = io.ReadAll(gr)
		if err != nil {
			return PlayerData{}, fmt.Errorf("world: read gunzipped playerdata: %w", err)
		}
	}
	_, root, err := nbt.UnmarshalNamed(nbtBytes)
	if err != nil {
		return PlayerData{}, fmt.Errorf("world: decode playerdata NBT: %w", err)
	}
	rootComp, ok := root.(*nbt.Compound)
	if !ok {
		return PlayerData{}, errors.New("world: playerdata root tag is not a compound")
	}
	return PlayerDataFromCompound(rootComp)
}

// FormatPlayerUUID formats a 16-byte UUID in canonical hyphenated lowercase form.
func FormatPlayerUUID(uuid [16]byte) string {
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		uuid[0:4], uuid[4:6], uuid[6:8], uuid[8:10], uuid[10:16])
}

// PlayerDataPath returns the path to <world>/playerdata/<uuid>.dat.
func (s *Store) PlayerDataPath(uuid [16]byte) string {
	if s == nil || s.dir == "" {
		return ""
	}
	return filepath.Join(s.dir, "playerdata", FormatPlayerUUID(uuid)+".dat")
}

// SavePlayerData writes a player's state atomically to <world>/playerdata/<uuid>.dat.
// It is a no-op when the store is nil.
func (s *Store) SavePlayerData(uuid [16]byte, data PlayerData) error {
	if s == nil || s.dir == "" {
		return nil
	}
	playerDir := filepath.Join(s.dir, "playerdata")
	if err := mkdirAll(playerDir); err != nil {
		return err
	}
	encoded, err := MarshalPlayerDataNBT(data)
	if err != nil {
		return err
	}
	target := filepath.Join(playerDir, FormatPlayerUUID(uuid)+".dat")
	tmp, err := os.CreateTemp(playerDir, ".playerdata-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(encoded); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, target)
}

// LoadPlayerData reads a player's state from <world>/playerdata/<uuid>.dat.
// If the file does not exist yet, it returns (PlayerData{}, false, nil).
func (s *Store) LoadPlayerData(uuid [16]byte) (PlayerData, bool, error) {
	if s == nil || s.dir == "" {
		return PlayerData{}, false, nil
	}
	target := s.PlayerDataPath(uuid)
	raw, err := os.ReadFile(target)
	if err != nil {
		if os.IsNotExist(err) {
			return PlayerData{}, false, nil
		}
		return PlayerData{}, false, err
	}
	data, err := UnmarshalPlayerDataNBT(raw)
	if err != nil {
		return PlayerData{}, false, err
	}
	return data, true, nil
}
