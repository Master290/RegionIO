package server

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"math"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"unicode/utf16"

	"regionio/internal/nbt"
	"regionio/internal/protocol"
	"regionio/internal/world"
)

// MenuType registry IDs in BuiltInRegistries.MENU (protocol 775 / 26.1.2).
const (
	MenuTypeHorse        int32 = -2 // Equine/llama inventory screen (opened via PlayHorseScreenOpen 0x29)
	MenuTypeGeneric9x3   int32 = 2  // Single chest / barrel / ender chest (27 chest slots + 36 player slots)
	MenuTypeGeneric9x6   int32 = 5  // Double chest (54 chest slots + 36 player slots)
	MenuTypeAnvil        int32 = 8  // Anvil (2 input + 1 result + 36 player slots)
	MenuTypeBlastFurnace int32 = 10 // Blast furnace (3 furnace slots + 36 player slots)
	MenuTypeCrafting     int32 = 12 // Crafting table (1 result + 9 grid + 36 player slots)
	MenuTypeEnchantment  int32 = 13 // Enchanting table (2 input + 36 player slots)
	MenuTypeFurnace      int32 = 14 // Furnace (3 furnace slots + 36 player slots)
	MenuTypeGrindstone   int32 = 15 // Grindstone (2 input + 1 result + 36 player slots)
	MenuTypeHopper       int32 = 16 // Hopper / Hopper Minecart (5 hopper slots + 36 player slots)
	MenuTypeSmoker       int32 = 22 // Smoker (3 furnace slots + 36 player slots)
)

// ContainerInput click mode IDs in ServerboundContainerClickPacket (protocol 775).
const (
	ContainerInputPickup     int32 = 0
	ContainerInputQuickMove  int32 = 1
	ContainerInputSwap       int32 = 2
	ContainerInputClone      int32 = 3
	ContainerInputThrow      int32 = 4
	ContainerInputQuickCraft int32 = 5
	ContainerInputPickupAll  int32 = 6
)

// Container slot layout constants.
const (
	ChestSlotCount           = 27
	ChestMenuSlotCount       = 63 // 27 chest + 27 main inventory + 9 hotbar
	DoubleChestSlotCount     = 54
	DoubleChestMenuSlotCount = 90 // 54 chest + 27 main inventory + 9 hotbar
	HopperSlotCount          = 5
	HopperMenuSlotCount      = 41 // 5 hopper + 27 main inventory + 9 hotbar
	CraftingGridSlotCount    = 9
	CraftingMenuSlotCount    = 46 // 1 result + 9 grid + 27 main inventory + 9 hotbar
	FurnaceSlotCount         = world.FurnaceSlotCount
	FurnaceMenuSlotCount     = 39 // 3 furnace + 27 main inventory + 9 hotbar
)

// HashedPatchMap represents vanilla 26.1.2 `net.minecraft.network.HashedPatchMap`.
type HashedPatchMap struct {
	Added   map[int32]int32
	Removed []int32
}

// HashedStack represents vanilla 26.1.2 `net.minecraft.network.HashedStack`.
type HashedStack struct {
	Present    bool
	ItemID     int32
	Count      int32
	Components HashedPatchMap
}

// Matches reports whether h equals actual under RemoteSlot$Synchronized semantics.
func (h HashedStack) Matches(actual HashedStack) bool {
	if !h.Present || h.Count <= 0 || h.ItemID <= 0 {
		return !actual.Present || actual.Count <= 0 || actual.ItemID <= 0
	}
	if !actual.Present || h.ItemID != actual.ItemID || h.Count != actual.Count {
		return false
	}
	if len(h.Components.Added) != len(actual.Components.Added) {
		return false
	}
	for k, v := range h.Components.Added {
		if av, ok := actual.Components.Added[k]; !ok || av != v {
			return false
		}
	}
	if len(h.Components.Removed) != len(actual.Components.Removed) {
		return false
	}
	if len(h.Components.Removed) > 0 {
		remSet := make(map[int32]struct{}, len(actual.Components.Removed))
		for _, id := range actual.Components.Removed {
			remSet[id] = struct{}{}
		}
		for _, id := range h.Components.Removed {
			if _, ok := remSet[id]; !ok {
				return false
			}
		}
	}
	return true
}

// ContainerClick holds the decoded fields of a serverbound container_click packet.
type ContainerClick struct {
	ContainerID       int32
	StateID           int32
	Slot              int16
	Button            int8
	Mode              int32
	ChangedSlots      map[int]HashedStack
	Carried           HashedStack
	HasWirePrediction bool
}

// OpenContainerState represents the currently open screen on a PlayerSession.
type OpenContainerState struct {
	ContainerID     int32
	MenuType        int32
	Dimension       string
	BlockX          int
	BlockY          int
	BlockZ          int
	PartnerX        int
	PartnerY        int
	PartnerZ        int
	HasPartner      bool
	IsEnderChest    bool
	IsBarrel        bool
	VehicleEntityID int32
	Crafting        [CraftingGridSlotCount]world.ItemStack
	CraftingSlots   [CraftingGridSlotCount]containerSlotData
	CraftingStars   [CraftingGridSlotCount]FireworkExplosion
	CraftingPotions [CraftingGridSlotCount]string
	Result          world.ItemStack
	EnchantSlots    [2]world.ItemStack
	CombinerInputs  [2]world.ItemStack
	AnvilRename     string
	EnchantCosts    [3]int16
	EnchantClues    [3]int16
	LevelClues      [3]int16
	AnvilCost       int16
}

type chestKey struct {
	x, y, z int
}

type serverChestState struct {
	mu     sync.Mutex
	chests map[chestKey]*[ChestSlotCount]world.ItemStack
}

// QuickCraft (drag mode 5) type and header constants matching AbstractContainerMenu.
const (
	QuickCraftTypeCharitable int8 = 0 // Left-drag: split carried stack evenly across dragged slots
	QuickCraftTypeGreedy     int8 = 1 // Right-drag: place 1 item per dragged slot
	QuickCraftTypeClone      int8 = 2 // Middle-drag (Creative): place max-stack clone per dragged slot

	QuickCraftHeaderStart    int8 = 0 // Start drag
	QuickCraftHeaderContinue int8 = 1 // Add slot to drag
	QuickCraftHeaderEnd      int8 = 2 // End drag and distribute items
)

type playerContainerTracker struct {
	syncMu            sync.Mutex // serializes outbound container state/slot sync per session; always acquire before mu
	mu                sync.Mutex
	nextWindow        int32
	carried           world.ItemStack
	open              *OpenContainerState
	invCraft          [4]world.ItemStack
	invCraftStars     [4]FireworkExplosion
	invResult         world.ItemStack
	enderChest        [ChestSlotCount]world.ItemStack
	dragStatus        int8
	dragType          int8
	dragSlots         []int
	remoteContainerID int32
	remoteSlots       []HashedStack
	remoteCarried     HashedStack
	hasRemoteCarried  bool
}

var (
	serverChests     sync.Map // map[*Server]*serverChestState
	playerContainers sync.Map // map[*PlayerSession]*playerContainerTracker
)

func (s *Server) chestState() *serverChestState {
	if v, ok := serverChests.Load(s); ok {
		return v.(*serverChestState)
	}
	actual, _ := serverChests.LoadOrStore(s, &serverChestState{
		chests: make(map[chestKey]*[ChestSlotCount]world.ItemStack),
	})
	return actual.(*serverChestState)
}

func getContainerTracker(session *PlayerSession) *playerContainerTracker {
	if session == nil {
		return nil
	}
	if v, ok := playerContainers.Load(session); ok {
		return v.(*playerContainerTracker)
	}
	actual, _ := playerContainers.LoadOrStore(session, &playerContainerTracker{})
	return actual.(*playerContainerTracker)
}

// OpenContainer returns a snapshot of the player's currently open container,
// or nil if only the default player inventory (container ID 0) is active.
func (session *PlayerSession) OpenContainer() *OpenContainerState {
	if session == nil {
		return nil
	}
	tr := getContainerTracker(session)
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if tr.open == nil {
		return nil
	}
	cp := *tr.open
	return &cp
}

// CarriedItem returns the item stack currently held on the player's cursor.
func (session *PlayerSession) CarriedItem() world.ItemStack {
	if session == nil {
		return world.ItemStack{}
	}
	tr := getContainerTracker(session)
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return tr.carried
}

// SetCarriedItem sets the item stack currently held on the player's cursor.
func (session *PlayerSession) SetCarriedItem(stack world.ItemStack) {
	if session == nil {
		return
	}
	tr := getContainerTracker(session)
	tr.mu.Lock()
	tr.carried = stack
	tr.mu.Unlock()
	if stack.IsEmpty() {
		SetCarriedItemMeta(session, ItemMeta{})
		SetCarriedPotionType(session, "")
		SetCarriedArmorTrim(session, ArmorTrim{})
		SetCarriedMapID(session, 0, false)
		SetCarriedWritableBook(session, WritableBookContent{}, false)
		SetCarriedWrittenBook(session, WrittenBookContent{}, false)
		SetCarriedGoatHornInstrument(session, "")
		SetCarriedBundleContents(session, nil)
		SetCarriedLodestoneTracker(session, LodestoneTracker{}, false)
		SetCarriedShulkerBoxData(session, nil)
		setCarriedFireworkStar(session, nil)
		setCarriedFireworks(session, nil)
	}
}

func (s *Server) resolveLegacyStorageDim(x, y, z int) string {
	if s == nil {
		return world.DimensionOverworld
	}
	if ow := s.loadedDimensionCache(world.DimensionOverworld); ow != nil && ow.IsChunkLoaded(int32(x>>4), int32(z>>4)) {
		if _, ok := StorageBlockEntityIDForBlockName(world.BlockName(ow.GetBlock(x, y, z))); ok {
			return world.DimensionOverworld
		}
	}
	for _, dim := range []string{world.DimensionNether, world.DimensionEnd} {
		cache := s.loadedDimensionCache(dim)
		if cache != nil && cache.IsChunkLoaded(int32(x>>4), int32(z>>4)) {
			if _, ok := StorageBlockEntityIDForBlockName(world.BlockName(cache.GetBlock(x, y, z))); ok {
				return dim
			}
		}
	}
	return world.DimensionOverworld
}

// ChestSlots returns a copy of the 27 slots stored in the chest at (x, y, z).
func (s *Server) ChestSlots(x, y, z int) [ChestSlotCount]world.ItemStack {
	if s == nil {
		return [ChestSlotCount]world.ItemStack{}
	}
	return s.ChestSlotsInDim(s.resolveLegacyStorageDim(x, y, z), x, y, z)
}

// SetChestSlot updates a single slot (0..26) in the chest at (x, y, z).
func (s *Server) SetChestSlot(x, y, z, slot int, stack world.ItemStack) bool {
	if s == nil || slot < 0 || slot >= ChestSlotCount {
		return false
	}
	return s.SetChestSlotInDim(s.resolveLegacyStorageDim(x, y, z), x, y, z, slot, stack)
}

// EnderChestSlots returns a copy of the player's personal 27-slot ender chest inventory.
func (session *PlayerSession) EnderChestSlots() [ChestSlotCount]world.ItemStack {
	if session == nil {
		return [ChestSlotCount]world.ItemStack{}
	}
	tr := getContainerTracker(session)
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return tr.enderChest
}

// EnderChestSlot returns a single slot (0..26) from the player's personal ender chest inventory.
func (session *PlayerSession) EnderChestSlot(slot int) world.ItemStack {
	if session == nil || slot < 0 || slot >= ChestSlotCount {
		return world.ItemStack{}
	}
	tr := getContainerTracker(session)
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return tr.enderChest[slot]
}

// SetEnderChestSlot updates a single slot (0..26) in the player's personal ender chest inventory.
func (session *PlayerSession) SetEnderChestSlot(slot int, stack world.ItemStack) bool {
	if session == nil || slot < 0 || slot >= ChestSlotCount {
		return false
	}
	tr := getContainerTracker(session)
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if stack.IsEmpty() {
		tr.enderChest[slot] = world.ItemStack{}
	} else {
		tr.enderChest[slot] = world.NewItemStack(stack.ResolvedItemID(), stack.Count)
	}
	return true
}

// SetEnderChestSlots replaces the player's personal 27-slot ender chest inventory.
func (session *PlayerSession) SetEnderChestSlots(slots [ChestSlotCount]world.ItemStack) {
	if session == nil {
		return
	}
	tr := getContainerTracker(session)
	tr.mu.Lock()
	tr.enderChest = slots
	tr.mu.Unlock()
}

// DropChestContents removes any stored chest inventory at (x, y, z) and spawns
// its non-empty stacks as item entities in the world.
func (s *Server) DropChestContents(x, y, z int) {
	if s == nil {
		return
	}
	s.DropChestContentsInDim(s.resolveLegacyStorageDim(x, y, z), x, y, z)
}

// IsChestBlock reports whether state is a chest block (chest, trapped_chest, or copper_chest).
func IsChestBlock(state uint16) bool {
	switch world.BlockName(state) {
	case "minecraft:chest", "minecraft:trapped_chest":
		return true
	default:
		return world.IsCopperChestBlock(state)
	}
}

// IsBarrelBlock reports whether state is a barrel block.
func IsBarrelBlock(state uint16) bool {
	return world.IsBarrelBlock(state)
}

// IsEnderChestBlock reports whether state is an ender_chest block.
func IsEnderChestBlock(state uint16) bool {
	return world.IsEnderChestBlock(state)
}

// PlaceChestAt places a chest, trapped_chest, or copper_chest at (x, y, z) for
// session, pairing it into a double chest ("left" / "right") when placed
// without sneaking adjacent to a matching single chest with the same facing.
func (s *Server) PlaceChestAt(session *PlayerSession, x, y, z int, baseState uint16) (uint16, bool) {
	if s == nil || session == nil {
		return 0, false
	}
	dim := world.NormalizeDimension(session.Dimension())
	cache := s.DimensionChunks(dim)
	if cache == nil {
		return 0, false
	}
	snap := session.Snapshot()
	horizLookDir := HorizontalFacingFromYaw(snap.Yaw)
	placedState, partnerPos, partnerState, hasPartner := cache.ResolveChestPlacementStateAt(
		x, y, z, baseState, horizLookDir, session.IsSneaking(),
	)
	applied, res := cache.ApplyBlockWithUpdates(x, y, z, placedState)
	if !applied {
		return 0, false
	}
	s.broadcastDimensionBlockChanges(dim, cache, res.Changes, res.LightChanged)
	if hasPartner {
		if pApplied, pRes := cache.ApplyBlockWithUpdates(partnerPos.X, partnerPos.Y, partnerPos.Z, partnerState); pApplied {
			s.broadcastDimensionBlockChanges(dim, cache, pRes.Changes, pRes.LightChanged)
		}
	}
	if invSlot := session.HeldInventorySlot(); invSlot >= 0 {
		if customName := GetSlotItemMeta(session, invSlot).CustomName; customName != "" {
			s.SetStorageCustomName(dim, x, y, z, customName)
		}
	}
	s.notifyStorageComparators(dim, x, y, z)
	return placedState, true
}

// IsCraftingTableBlock reports whether state is a crafting_table block.
func IsCraftingTableBlock(state uint16) bool {
	return world.BlockName(state) == "minecraft:crafting_table"
}

// EncodeOpenScreen encodes a clientbound open_screen (0x3b) packet body.
func EncodeOpenScreen(containerID, menuType int32, title string) []byte {
	w := protocol.NewWriter(len(title) + 16)
	w.VarInt(containerID)
	w.VarInt(menuType)
	w.Raw(nbt.Marshal(nbt.String(title)))
	return w.Bytes()
}

// EncodeOpenScreenComponent encodes a clientbound open_screen (0x3b) packet
// body using a full vanilla ChatComponent title (e.g. TranslatableComponent("container.chest")).
func EncodeOpenScreenComponent(containerID, menuType int32, title ChatComponent) []byte {
	nbtBytes := title.MarshalNBT()
	w := protocol.NewWriter(len(nbtBytes) + 16)
	w.VarInt(containerID)
	w.VarInt(menuType)
	w.Raw(nbtBytes)
	return w.Bytes()
}

// DecodeOpenScreen decodes a clientbound open_screen (0x3b) packet body.
func DecodeOpenScreen(r *protocol.Reader) (containerID, menuType int32, title ChatComponent, err error) {
	if r == nil {
		return 0, 0, ChatComponent{}, errors.New("server: nil reader")
	}
	containerID, err = r.VarInt()
	if err != nil {
		return 0, 0, ChatComponent{}, err
	}
	menuType, err = r.VarInt()
	if err != nil {
		return 0, 0, ChatComponent{}, err
	}
	title, err = ReadChatComponentNBT(r)
	if err != nil {
		return 0, 0, ChatComponent{}, err
	}
	return containerID, menuType, title, nil
}

// EncodeContainerClose encodes a clientbound container_close (0x11) packet body.
func EncodeContainerClose(containerID int32) []byte {
	w := protocol.NewWriter(4)
	w.VarInt(containerID)
	return w.Bytes()
}

// EncodeContainerContent encodes a clientbound container_set_content (0x12) packet body.
func EncodeContainerContent(containerID, stateID int32, slots []world.ItemStack, carried world.ItemStack) []byte {
	w := protocol.NewWriter(32 + len(slots)*8)
	w.VarInt(containerID)
	w.VarInt(stateID)
	w.VarInt(int32(len(slots)))
	for _, st := range slots {
		writeContainerItemStack(w, st)
	}
	writeContainerItemStack(w, carried)
	return w.Bytes()
}

// EncodeContainerContentWithMetaAndTrim encodes a clientbound container_set_content (0x12)
// packet body including ItemMeta (damage, custom_name, enchantments, stored_enchantments,
// repair_cost) and ArmorTrim components for each slot and the carried stack.
func EncodeContainerContentWithMetaAndTrim(containerID, stateID int32, slots []world.ItemStack, metas []ItemMeta, trims []ArmorTrim, carried world.ItemStack, carriedMeta ItemMeta, carriedTrim ArmorTrim) []byte {
	w := protocol.NewWriter(32 + len(slots)*16)
	w.VarInt(containerID)
	w.VarInt(stateID)
	w.VarInt(int32(len(slots)))
	for i, st := range slots {
		var meta ItemMeta
		if i < len(metas) {
			meta = metas[i]
		}
		var trim ArmorTrim
		if i < len(trims) {
			trim = trims[i]
		}
		WriteSlotWithArmorTrim(w, st, meta, trim)
	}
	WriteSlotWithArmorTrim(w, carried, carriedMeta, carriedTrim)
	return w.Bytes()
}

// EncodeContainerSlot encodes a clientbound container_set_slot (0x14) packet body.
func EncodeContainerSlot(containerID, stateID int32, slot int, stack world.ItemStack) []byte {
	w := protocol.NewWriter(24)
	w.VarInt(containerID)
	w.VarInt(stateID)
	w.Uint16(uint16(int16(slot)))
	writeContainerItemStack(w, stack)
	return w.Bytes()
}

// EncodeContainerSetData encodes a clientbound container_set_data (0x13) packet body.
func EncodeContainerSetData(containerID int32, propID, value int16) []byte {
	w := protocol.NewWriter(8)
	w.VarInt(containerID)
	w.Uint16(uint16(propID))
	w.Uint16(uint16(value))
	return w.Bytes()
}

func writeContainerItemStack(w *protocol.Writer, stack world.ItemStack) {
	itemID := stack.ResolvedItemID()
	if stack.IsEmpty() || itemID <= 0 {
		w.VarInt(0)
		return
	}
	w.VarInt(stack.Count)
	w.VarInt(itemID)
	w.VarInt(0) // added components
	w.VarInt(0) // removed components
}

// DecodeContainerClickPacket parses a protocol 775 serverbound container_click packet.
func DecodeContainerClickPacket(r *protocol.Reader) (ContainerClick, error) {
	var click ContainerClick
	cid, err := r.VarInt()
	if err != nil {
		return click, err
	}
	stateID, err := r.VarInt()
	if err != nil {
		return click, err
	}
	slotRaw, err := r.Uint16()
	if err != nil {
		return click, err
	}
	btnRaw, err := r.ReadByte()
	if err != nil {
		return click, err
	}
	mode, err := r.VarInt()
	if err != nil {
		return click, err
	}
	click = ContainerClick{
		ContainerID: cid,
		StateID:     stateID,
		Slot:        int16(slotRaw),
		Button:      int8(btnRaw),
		Mode:        mode,
	}

	if r.Remaining() > 0 {
		click.HasWirePrediction = true
		changedCount, err := r.VarInt()
		if err != nil {
			return click, err
		}
		if changedCount < 0 || changedCount > 128 {
			return click, errors.New("server: container_click changedSlots count out of range")
		}
		if changedCount > 0 {
			click.ChangedSlots = make(map[int]HashedStack, changedCount)
		}
		for i := int32(0); i < changedCount; i++ {
			slRaw, err := r.Uint16()
			if err != nil {
				return click, err
			}
			hs, err := readHashedStack(r)
			if err != nil {
				return click, err
			}
			click.ChangedSlots[int(int16(slRaw))] = hs
		}
		carried, err := readHashedStack(r)
		if err != nil {
			return click, err
		}
		click.Carried = carried
	}
	return click, nil
}

// EncodeContainerClickPacket encodes a protocol 775 serverbound container_click packet body.
func EncodeContainerClickPacket(click ContainerClick) []byte {
	w := protocol.NewWriter(64)
	w.VarInt(click.ContainerID)
	w.VarInt(click.StateID)
	w.Uint16(uint16(click.Slot))
	w.Byte(byte(click.Button))
	w.VarInt(click.Mode)
	slots := make([]int, 0, len(click.ChangedSlots))
	for s := range click.ChangedSlots {
		slots = append(slots, s)
	}
	sort.Ints(slots)
	w.VarInt(int32(len(slots)))
	for _, s := range slots {
		w.Uint16(uint16(int16(s)))
		writeHashedStack(w, click.ChangedSlots[s])
	}
	writeHashedStack(w, click.Carried)
	return w.Bytes()
}

func readHashedStack(r *protocol.Reader) (HashedStack, error) {
	present, err := r.Bool()
	if err != nil || !present {
		return HashedStack{}, err
	}
	itemID, err := r.VarInt()
	if err != nil {
		return HashedStack{}, err
	}
	count, err := r.VarInt()
	if err != nil {
		return HashedStack{}, err
	}
	addedCount, err := r.VarInt()
	if err != nil {
		return HashedStack{}, err
	}
	if addedCount < 0 || addedCount > 256 {
		return HashedStack{}, errors.New("server: HashedPatchMap added count out of range")
	}
	var added map[int32]int32
	if addedCount > 0 {
		added = make(map[int32]int32, addedCount)
	}
	for i := int32(0); i < addedCount; i++ {
		compID, err := r.VarInt()
		if err != nil {
			return HashedStack{}, err
		}
		hashVal, err := r.Int32()
		if err != nil {
			return HashedStack{}, err
		}
		added[compID] = hashVal
	}
	removedCount, err := r.VarInt()
	if err != nil {
		return HashedStack{}, err
	}
	if removedCount < 0 || removedCount > 256 {
		return HashedStack{}, errors.New("server: HashedPatchMap removed count out of range")
	}
	var removed []int32
	if removedCount > 0 {
		removed = make([]int32, 0, removedCount)
	}
	for i := int32(0); i < removedCount; i++ {
		compID, err := r.VarInt()
		if err != nil {
			return HashedStack{}, err
		}
		removed = append(removed, compID)
	}
	return HashedStack{
		Present: present,
		ItemID:  itemID,
		Count:   count,
		Components: HashedPatchMap{
			Added:   added,
			Removed: removed,
		},
	}, nil
}

func writeHashedStack(w *protocol.Writer, hs HashedStack) {
	if !hs.Present || hs.Count <= 0 || hs.ItemID <= 0 {
		w.Bool(false)
		return
	}
	w.Bool(true)
	w.VarInt(hs.ItemID)
	w.VarInt(hs.Count)
	keys := make([]int32, 0, len(hs.Components.Added))
	for k := range hs.Components.Added {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	w.VarInt(int32(len(keys)))
	for _, k := range keys {
		w.VarInt(k)
		w.Int32(hs.Components.Added[k])
	}
	w.VarInt(int32(len(hs.Components.Removed)))
	for _, k := range hs.Components.Removed {
		w.VarInt(k)
	}
}

var containerCRC32CTable = crc32.MakeTable(crc32.Castagnoli)

func hashOpsInt(v int32) uint32 {
	var buf [5]byte
	buf[0] = 8
	binary.LittleEndian.PutUint32(buf[1:], uint32(v))
	return crc32.Checksum(buf[:], containerCRC32CTable)
}

func hashOpsString(s string) uint32 {
	units := utf16.Encode([]rune(s))
	buf := make([]byte, 5+len(units)*2)
	buf[0] = 12
	binary.LittleEndian.PutUint32(buf[1:5], uint32(len(units)))
	for i, u := range units {
		binary.LittleEndian.PutUint16(buf[5+i*2:], u)
	}
	return crc32.Checksum(buf, containerCRC32CTable)
}

func hashOpsMap(entries [][2]uint32) uint32 {
	if len(entries) == 0 {
		return crc32.Checksum([]byte{2, 3}, containerCRC32CTable)
	}
	sorted := append([][2]uint32(nil), entries...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i][0] != sorted[j][0] {
			return sorted[i][0] < sorted[j][0]
		}
		return sorted[i][1] < sorted[j][1]
	})
	buf := make([]byte, 2+len(sorted)*8)
	buf[0] = 2
	off := 1
	for _, e := range sorted {
		binary.LittleEndian.PutUint32(buf[off:], e[0])
		binary.LittleEndian.PutUint32(buf[off+4:], e[1])
		off += 8
	}
	buf[off] = 3
	return crc32.Checksum(buf, containerCRC32CTable)
}

// containerSlotData bundles an ItemStack together with all associated 26.1.2
// data components so slot moves, swaps, drops, packets, and HashedStack hashes
// preserve components uniformly.
type containerSlotData struct {
	Stack       world.ItemStack
	Meta        ItemMeta
	Trim        ArmorTrim
	Potion      string
	MapID       int32
	HasMap      bool
	Shulker     *ShulkerBoxItemData
	Bundle      []world.ItemStack
	Horn        string
	Writable    *WritableBookContent
	Written     *WrittenBookContent
	Fireworks   *FireworksComponent
	Star        *FireworkExplosion
	Lodestone   *LodestoneTracker
	Banner      BannerPatternsComponent
	ShieldColor string
	RawComp     *nbt.Compound
}

func (d containerSlotData) IsEmpty() bool {
	return d.Stack.IsEmpty() || d.Stack.Count <= 0 || d.Stack.ResolvedItemID() <= 0
}

func (d containerSlotData) MaxStackSize() int32 {
	if d.IsEmpty() {
		return MaxItemStackSize
	}
	return MaxStackSizeForBundleWeight(d.Stack.ResolvedID())
}

func (d containerSlotData) WithCount(count int32) containerSlotData {
	if count <= 0 || d.IsEmpty() {
		return containerSlotData{}
	}
	cp := d.Clone()
	cp.Stack = world.NewItemStack(d.Stack.ResolvedItemID(), count)
	return cp
}

func (d containerSlotData) Clone() containerSlotData {
	cp := d
	cp.Meta = d.Meta.Clone()
	if d.Shulker != nil {
		cp.Shulker = d.Shulker.Clone()
	}
	if len(d.Bundle) > 0 {
		cp.Bundle = append([]world.ItemStack(nil), d.Bundle...)
	}
	if d.Writable != nil {
		w := d.Writable.Clone()
		cp.Writable = &w
	}
	if d.Written != nil {
		w := d.Written.Clone()
		cp.Written = &w
	}
	if d.Fireworks != nil {
		f := d.Fireworks.Clone()
		cp.Fireworks = &f
	}
	if d.Star != nil {
		s := d.Star.Clone()
		cp.Star = &s
	}
	if d.Lodestone != nil {
		l := *d.Lodestone
		cp.Lodestone = &l
	}
	if !d.Banner.IsEmpty() {
		cp.Banner = d.Banner.Clone()
	}
	if d.RawComp != nil {
		cp.RawComp = world.CloneNBTCompound(d.RawComp)
	}
	return cp
}

// HasCustomComponents reports whether d carries any non-default item metadata or data components.
func (d containerSlotData) HasCustomComponents() bool {
	if d.IsEmpty() {
		return false
	}
	return !d.Meta.IsEmpty() ||
		d.Trim.IsValid() ||
		d.Potion != "" ||
		d.HasMap ||
		d.Shulker != nil ||
		len(d.Bundle) > 0 ||
		d.Horn != "" ||
		d.Writable != nil ||
		d.Written != nil ||
		d.Fireworks != nil ||
		d.Star != nil ||
		d.Lodestone != nil ||
		!d.Banner.IsEmpty() ||
		d.ShieldColor != "" ||
		d.RawComp != nil
}

func (d containerSlotData) SameItemSameComponents(other containerSlotData) bool {
	if d.IsEmpty() || other.IsEmpty() {
		return false
	}
	if d.Stack.ResolvedItemID() != other.Stack.ResolvedItemID() {
		return false
	}
	if d.Meta.Damage != other.Meta.Damage ||
		d.Meta.CustomName != other.Meta.CustomName ||
		d.Meta.RepairCost != other.Meta.RepairCost ||
		!intMapEqual(d.Meta.Enchantments, other.Meta.Enchantments) ||
		!intMapEqual(d.Meta.StoredEnchantments, other.Meta.StoredEnchantments) ||
		!stringSliceEqual(d.Meta.CanPlaceOn, other.Meta.CanPlaceOn) ||
		!stringSliceEqual(d.Meta.CanBreak, other.Meta.CanBreak) {
		return false
	}
	if d.Trim != other.Trim || d.Potion != other.Potion || d.HasMap != other.HasMap || (d.HasMap && d.MapID != other.MapID) {
		return false
	}
	if d.Horn != other.Horn || d.ShieldColor != other.ShieldColor {
		return false
	}
	if (d.Shulker != nil) != (other.Shulker != nil) || len(d.Bundle) != len(other.Bundle) {
		return false
	}
	if (d.Writable != nil) != (other.Writable != nil) || (d.Written != nil) != (other.Written != nil) {
		return false
	}
	if (d.Fireworks != nil) != (other.Fireworks != nil) || (d.Star != nil) != (other.Star != nil) || (d.Lodestone != nil) != (other.Lodestone != nil) {
		return false
	}
	if (d.Banner.IsEmpty()) != (other.Banner.IsEmpty()) {
		return false
	}
	return d.ToHashedStack().WithCount1().Matches(other.ToHashedStack().WithCount1())
}

func (h HashedStack) WithCount1() HashedStack {
	if !h.Present {
		return h
	}
	h.Count = 1
	return h
}

func intMapEqual(a, b map[string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func stringSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func hashOpsList(hashes []uint32) uint32 {
	buf := make([]byte, 2+len(hashes)*4)
	buf[0] = 4
	off := 1
	for _, h := range hashes {
		binary.LittleEndian.PutUint32(buf[off:], h)
		off += 4
	}
	buf[off] = 5
	return crc32.Checksum(buf, containerCRC32CTable)
}

func (d containerSlotData) effectiveEnchantments() ([]EnchantmentInstance, int32) {
	if d.Stack.ResolvedID() == "minecraft:enchanted_book" && len(d.Meta.StoredEnchantments) > 0 {
		out := make([]EnchantmentInstance, 0, len(d.Meta.StoredEnchantments))
		for k, v := range d.Meta.StoredEnchantments {
			if v > 0 {
				out = append(out, EnchantmentInstance{ID: canonicalEnchantmentID(k), Level: v})
			}
		}
		sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
		return out, DataComponentStoredEnchantmentsID
	}
	if d.Meta.HasEnchantments() {
		compID := DataComponentEnchantmentsID
		if d.Stack.ResolvedID() == "minecraft:enchanted_book" {
			compID = DataComponentStoredEnchantmentsID
		}
		return d.Meta.SortedEnchantments(), compID
	}
	return nil, 0
}

func (d containerSlotData) ToHashedStack() HashedStack {
	if d.IsEmpty() {
		return HashedStack{}
	}
	added := make(map[int32]int32)
	if d.Meta.Damage > 0 {
		added[DataComponentDamageID] = int32(hashOpsInt(d.Meta.Damage))
	}
	if d.Meta.CustomName != "" {
		added[DataComponentCustomNameID] = int32(hashOpsString(d.Meta.CustomName))
	}
	if sorted, enchCompID := d.effectiveEnchantments(); len(sorted) > 0 {
		entries := make([][2]uint32, 0, len(sorted))
		for _, inst := range sorted {
			entries = append(entries, [2]uint32{hashOpsString(canonicalResourceName(inst.ID)), hashOpsInt(int32(inst.Level))})
		}
		added[enchCompID] = int32(hashOpsMap(entries))
	}
	if d.Meta.RepairCost > 0 {
		added[DataComponentRepairCostID] = int32(hashOpsInt(d.Meta.RepairCost))
	}
	if len(d.Meta.CanPlaceOn) > 0 {
		hashes := make([]uint32, 0, len(d.Meta.CanPlaceOn))
		for _, e := range d.Meta.CanPlaceOn {
			hashes = append(hashes, hashOpsMap([][2]uint32{{hashOpsString("blocks"), hashOpsString(e)}}))
		}
		added[DataComponentCanPlaceOnID] = int32(hashOpsList(hashes))
	}
	if len(d.Meta.CanBreak) > 0 {
		hashes := make([]uint32, 0, len(d.Meta.CanBreak))
		for _, e := range d.Meta.CanBreak {
			hashes = append(hashes, hashOpsMap([][2]uint32{{hashOpsString("blocks"), hashOpsString(e)}}))
		}
		added[DataComponentCanBreakID] = int32(hashOpsList(hashes))
	}
	if d.Trim.IsValid() {
		added[DataComponentTrim] = int32(hashOpsMap([][2]uint32{
			{hashOpsString("material"), hashOpsString(canonicalResourceName(d.Trim.Material))},
			{hashOpsString("pattern"), hashOpsString(canonicalResourceName(d.Trim.Pattern))},
		}))
	}
	if d.Potion != "" && IsPotionOrTippedArrowItem(d.Stack) {
		added[DataComponentPotionContents] = int32(hashOpsMap([][2]uint32{
			{hashOpsString("potion"), hashOpsString(canonicalPotionID(d.Potion))},
		}))
	}
	if d.HasMap && d.Stack.ResolvedID() == FilledMapItemID {
		added[DataComponentMapID] = int32(hashOpsInt(d.MapID))
	}
	if len(added) == 0 {
		added = nil
	}
	return HashedStack{
		Present: true,
		ItemID:  d.Stack.ResolvedItemID(),
		Count:   d.Stack.Count,
		Components: HashedPatchMap{
			Added: added,
		},
	}
}

func (d containerSlotData) WriteProtocolSlot(w *protocol.Writer) {
	if d.IsEmpty() {
		w.VarInt(0)
		return
	}
	id := d.Stack.ResolvedID()
	if IsBundleItem(id) && len(d.Bundle) > 0 {
		EncodeSlotWithBundleContents(w, d.Stack, d.Bundle)
		return
	}
	if id == FireworkRocketItemID && d.Fireworks != nil {
		EncodeSlotWithFireworks(w, d.Stack, *d.Fireworks)
		return
	}
	if id == FireworkStarItemID && d.Star != nil {
		EncodeSlotWithFireworkStar(w, d.Stack, *d.Star)
		return
	}
	if id == CompassItemID && d.Lodestone != nil {
		EncodeSlotWithLodestoneTracker(w, d.Stack, d.Lodestone)
		return
	}
	if (id == WritableBookItemID && d.Writable != nil) || (id == WrittenBookItemID && d.Written != nil) {
		EncodeSlotWithBookContent(w, d.Stack, d.Writable, d.Written)
		return
	}
	if (IsBannerItem(id) || id == "minecraft:shield") && (!d.Banner.IsEmpty() || d.ShieldColor != "") {
		EncodeSlotWithBannerPatterns(w, d.Stack, d.Banner, d.ShieldColor)
		return
	}

	type rawComp struct {
		id   int32
		data []byte
	}
	var comps []rawComp
	if d.Meta.Damage > 0 {
		cw := protocol.NewWriter(4)
		cw.VarInt(d.Meta.Damage)
		comps = append(comps, rawComp{id: DataComponentDamageID, data: cw.Bytes()})
	}
	if d.Meta.CustomName != "" {
		comps = append(comps, rawComp{id: DataComponentCustomNameID, data: TextComponent(d.Meta.CustomName).MarshalNBT()})
	}
	if sorted, enchCompID := d.effectiveEnchantments(); len(sorted) > 0 {
		cw := protocol.NewWriter(4 + len(sorted)*4)
		cw.VarInt(int32(len(sorted)))
		for _, inst := range sorted {
			def, ok := EnchantmentByID(inst.ID)
			idx := int32(0)
			if ok {
				idx = int32(def.RegistryIndex)
			}
			cw.VarInt(idx)
			cw.VarInt(int32(inst.Level))
		}
		comps = append(comps, rawComp{id: enchCompID, data: cw.Bytes()})
	}
	if len(d.Meta.CanPlaceOn) > 0 {
		comps = append(comps, rawComp{id: DataComponentCanPlaceOnID, data: EncodeAdventurePredicatePayload(d.Meta.CanPlaceOn)})
	}
	if len(d.Meta.CanBreak) > 0 {
		comps = append(comps, rawComp{id: DataComponentCanBreakID, data: EncodeAdventurePredicatePayload(d.Meta.CanBreak)})
	}
	if d.Meta.RepairCost > 0 {
		cw := protocol.NewWriter(4)
		cw.VarInt(d.Meta.RepairCost)
		comps = append(comps, rawComp{id: DataComponentRepairCostID, data: cw.Bytes()})
	}
	if d.HasMap && id == FilledMapItemID {
		cw := protocol.NewWriter(4)
		cw.VarInt(d.MapID)
		comps = append(comps, rawComp{id: DataComponentMapID, data: cw.Bytes()})
	}
	if IsPotionOrTippedArrowItem(d.Stack) && d.Potion != "" {
		cw := protocol.NewWriter(16)
		EncodePotionContentsComponent(cw, PotionContentsComponent{PotionID: canonicalPotionID(d.Potion)})
		comps = append(comps, rawComp{id: DataComponentPotionContents, data: cw.Bytes()})
	}
	if d.Trim.IsValid() {
		if trimBytes, err := EncodeArmorTrimPayload(d.Trim); err == nil {
			comps = append(comps, rawComp{id: DataComponentTrim, data: trimBytes})
		}
	}

	w.VarInt(d.Stack.Count)
	w.VarInt(d.Stack.ResolvedItemID())
	w.VarInt(int32(len(comps)))
	w.VarInt(0)
	for _, c := range comps {
		w.VarInt(c.id)
		w.Raw(c.data)
	}
}

func encodeContainerContentFromSlotData(containerID, stateID int32, slots []containerSlotData, carried containerSlotData) []byte {
	w := protocol.NewWriter(32 + len(slots)*16)
	w.VarInt(containerID)
	w.VarInt(stateID)
	w.VarInt(int32(len(slots)))
	for _, sl := range slots {
		sl.WriteProtocolSlot(w)
	}
	carried.WriteProtocolSlot(w)
	return w.Bytes()
}

func encodeContainerSlotFromSlotData(containerID, stateID int32, slot int, d containerSlotData) []byte {
	w := protocol.NewWriter(32)
	w.VarInt(containerID)
	w.VarInt(stateID)
	w.Uint16(uint16(int16(slot)))
	d.WriteProtocolSlot(w)
	return w.Bytes()
}

func encodeSetCursorItemFromSlotData(d containerSlotData) []byte {
	w := protocol.NewWriter(32)
	d.WriteProtocolSlot(w)
	return w.Bytes()
}

func (s *Server) getStorageSlotData(dim string, x, y, z, slot int) containerSlotData {
	st, meta, trim, pot, sh, comp := s.getStorageSlotMetaAndComponents(dim, x, y, z, slot)
	if st.IsEmpty() {
		return containerSlotData{}
	}
	d := containerSlotData{
		Stack:   st,
		Meta:    meta,
		Trim:    trim,
		Potion:  pot,
		RawComp: comp,
	}
	if sh != nil {
		d.Shulker = &ShulkerBoxItemData{Slots: *sh}
	}
	if st.ResolvedID() == FilledMapItemID {
		if id, ok := s.GetChestSlotMapID(x, y, z, slot); ok {
			d.MapID = id
			d.HasMap = true
		}
	}
	return d
}

func (s *Server) setStorageSlotData(dim string, x, y, z, slot int, d containerSlotData) {
	if d.IsEmpty() {
		s.setStorageSlotMetaAndComponents(dim, x, y, z, slot, world.ItemStack{}, ItemMeta{}, ArmorTrim{}, "", nil, nil)
		s.ClearChestSlotMapID(x, y, z, slot)
		return
	}
	var sh *[ChestSlotCount]world.ItemStack
	if d.Shulker != nil {
		arr := d.Shulker.Slots
		sh = &arr
	}
	s.setStorageSlotMetaAndComponents(dim, x, y, z, slot, d.Stack, d.Meta, d.Trim, d.Potion, sh, d.RawComp)
	if d.Stack.ResolvedID() == FilledMapItemID && d.HasMap {
		s.SetChestSlotMapID(x, y, z, slot, d.MapID)
	} else {
		s.ClearChestSlotMapID(x, y, z, slot)
	}
}

func getPlayerSlotData(session *PlayerSession, slot int) containerSlotData {
	if session == nil || slot < 0 || slot >= world.PlayerInventorySlots {
		return containerSlotData{}
	}
	st := session.InventorySlot(slot)
	if st.IsEmpty() {
		return containerSlotData{}
	}
	d := containerSlotData{
		Stack:  st,
		Meta:   GetSlotItemMeta(session, slot),
		Potion: GetSlotPotionType(session, slot),
		Horn:   GetSlotGoatHornInstrument(session, slot),
	}
	if tr, ok := GetSlotArmorTrim(session, slot); ok {
		d.Trim = tr
	}
	if st.ResolvedID() == FilledMapItemID {
		if mid, ok := GetSlotMapID(session, slot); ok {
			d.MapID = mid
			d.HasMap = true
		}
	}
	if sh, ok := GetSlotShulkerBoxData(session, slot); ok && sh != nil {
		d.Shulker = sh
	}
	if IsBundleItem(st.ResolvedID()) {
		if items := GetSlotBundleContents(session, slot); len(items) > 0 {
			d.Bundle = items
		}
	}
	if st.ResolvedID() == WritableBookItemID {
		if wb, ok := GetSlotWritableBook(session, slot); ok {
			d.Writable = &wb
		}
	}
	if st.ResolvedID() == WrittenBookItemID {
		if wrb, ok := GetSlotWrittenBook(session, slot); ok {
			d.Written = &wrb
		}
	}
	if st.ResolvedID() == FireworkRocketItemID {
		if fw, ok := GetSlotFireworks(session, slot); ok {
			d.Fireworks = &fw
		}
	}
	if st.ResolvedID() == FireworkStarItemID {
		if exp, ok := GetSlotFireworkStar(session, slot); ok {
			d.Star = &exp
		}
	}
	if IsCompassItem(st) {
		if lt, ok := GetSlotLodestoneTracker(session, slot); ok {
			d.Lodestone = &lt
		}
	}
	if id := st.ResolvedID(); IsBannerItem(id) || id == "minecraft:shield" {
		d.Banner = GetSlotBannerPatterns(session, slot)
		d.ShieldColor = GetSlotShieldBaseColor(session, slot)
	}
	return d
}

func setPlayerSlotData(session *PlayerSession, slot int, d containerSlotData) {
	if session == nil || slot < 0 || slot >= world.PlayerInventorySlots {
		return
	}
	if d.IsEmpty() {
		session.SetInventorySlot(slot, world.ItemStack{})
		SetSlotItemMeta(session, slot, ItemMeta{})
		SetSlotArmorTrim(session, slot, ArmorTrim{})
		SetSlotPotionType(session, slot, "")
		ClearSlotMapID(session, slot)
		ClearSlotShulkerBoxData(session, slot)
		ClearSlotBundleContents(session, slot)
		ClearSlotGoatHornInstrument(session, slot)
		ClearSlotWritableBook(session, slot)
		ClearSlotWrittenBook(session, slot)
		ClearSlotFireworks(session, slot)
		ClearSlotFireworkStar(session, slot)
		ClearSlotLodestoneTracker(session, slot)
		ClearSlotBannerMeta(session, slot)
		return
	}
	id := d.Stack.ResolvedID()
	session.SetInventorySlot(slot, world.NewItemStack(d.Stack.ResolvedItemID(), d.Stack.Count))
	SetSlotItemMeta(session, slot, d.Meta)
	if d.Trim.IsValid() {
		SetSlotArmorTrim(session, slot, d.Trim)
	} else {
		ClearSlotArmorTrim(session, slot)
	}
	SetSlotPotionType(session, slot, d.Potion)
	if d.HasMap && id == FilledMapItemID {
		SetSlotMapID(session, slot, d.MapID)
	} else {
		ClearSlotMapID(session, slot)
	}
	if d.Shulker != nil && IsShulkerBoxItemOrBlock(id) {
		SetSlotShulkerBoxData(session, slot, d.Shulker)
	} else {
		ClearSlotShulkerBoxData(session, slot)
	}
	if len(d.Bundle) > 0 && IsBundleItem(id) {
		SetSlotBundleContents(session, slot, d.Bundle)
	} else {
		ClearSlotBundleContents(session, slot)
	}
	SetSlotGoatHornInstrument(session, slot, d.Horn)
	if d.Writable != nil && id == WritableBookItemID {
		SetSlotWritableBook(session, slot, *d.Writable)
	} else {
		ClearSlotWritableBook(session, slot)
	}
	if d.Written != nil && id == WrittenBookItemID {
		SetSlotWrittenBook(session, slot, *d.Written)
	} else {
		ClearSlotWrittenBook(session, slot)
	}
	if d.Fireworks != nil && id == FireworkRocketItemID {
		SetSlotFireworks(session, slot, *d.Fireworks)
	} else {
		ClearSlotFireworks(session, slot)
	}
	if d.Star != nil && id == FireworkStarItemID {
		SetSlotFireworkStar(session, slot, *d.Star)
	} else {
		ClearSlotFireworkStar(session, slot)
	}
	if d.Lodestone != nil && IsCompassItem(d.Stack) {
		SetSlotLodestoneTracker(session, slot, *d.Lodestone)
	} else {
		ClearSlotLodestoneTracker(session, slot)
	}
	if (!d.Banner.IsEmpty() || d.ShieldColor != "") && (IsBannerItem(id) || id == "minecraft:shield") {
		SetSlotBannerPatterns(session, slot, d.Banner)
		SetSlotShieldBaseColor(session, slot, d.ShieldColor)
	} else {
		ClearSlotBannerMeta(session, slot)
	}
}

func getCarriedSlotDataLocked(session *PlayerSession, tr *playerContainerTracker) containerSlotData {
	if session == nil || tr == nil || tr.carried.IsEmpty() {
		return containerSlotData{}
	}
	d := containerSlotData{
		Stack:  tr.carried,
		Meta:   GetCarriedItemMeta(session),
		Potion: getCarriedPotionTypeForStack(session, tr.carried),
		Horn:   GetCarriedGoatHornInstrument(session),
	}
	if trm, ok := GetCarriedArmorTrim(session); ok {
		d.Trim = trm
	}
	if mid, ok := GetCarriedMapID(session); ok && tr.carried.ResolvedID() == FilledMapItemID {
		d.MapID = mid
		d.HasMap = true
	}
	if sh, ok := GetCarriedShulkerBoxData(session); ok && sh != nil {
		d.Shulker = sh
	}
	if IsBundleItem(tr.carried.ResolvedID()) {
		d.Bundle = GetCarriedBundleContents(session)
	}
	if tr.carried.ResolvedID() == WritableBookItemID {
		if wb, ok := GetCarriedWritableBook(session); ok {
			d.Writable = &wb
		}
	}
	if tr.carried.ResolvedID() == WrittenBookItemID {
		if wrb, ok := GetCarriedWrittenBook(session); ok {
			d.Written = &wrb
		}
	}
	if tr.carried.ResolvedID() == FireworkRocketItemID {
		if fw, ok := getCarriedFireworks(session); ok {
			d.Fireworks = &fw
		}
	}
	if tr.carried.ResolvedID() == FireworkStarItemID {
		if exp, ok := getCarriedFireworkStar(session); ok {
			d.Star = &exp
		}
	}
	if IsCompassItem(tr.carried) {
		if lt, ok := GetCarriedLodestoneTracker(session); ok {
			d.Lodestone = &lt
		}
	}
	return d
}

func setCarriedSlotDataLocked(session *PlayerSession, tr *playerContainerTracker, d containerSlotData) {
	if session == nil || tr == nil {
		return
	}
	if d.IsEmpty() {
		tr.carried = world.ItemStack{}
		SetCarriedItemMeta(session, ItemMeta{})
		SetCarriedPotionType(session, "")
		SetCarriedArmorTrim(session, ArmorTrim{})
		SetCarriedMapID(session, 0, false)
		SetCarriedWritableBook(session, WritableBookContent{}, false)
		SetCarriedWrittenBook(session, WrittenBookContent{}, false)
		SetCarriedGoatHornInstrument(session, "")
		SetCarriedBundleContents(session, nil)
		SetCarriedLodestoneTracker(session, LodestoneTracker{}, false)
		SetCarriedShulkerBoxData(session, nil)
		setCarriedFireworkStar(session, nil)
		setCarriedFireworks(session, nil)
		return
	}
	id := d.Stack.ResolvedID()
	tr.carried = world.NewItemStack(d.Stack.ResolvedItemID(), d.Stack.Count)
	SetCarriedItemMeta(session, d.Meta)
	SetCarriedPotionType(session, d.Potion)
	if d.Trim.IsValid() {
		SetCarriedArmorTrim(session, d.Trim)
	} else {
		SetCarriedArmorTrim(session, ArmorTrim{})
	}
	SetCarriedMapID(session, d.MapID, d.HasMap && id == FilledMapItemID)
	if d.Writable != nil && id == WritableBookItemID {
		SetCarriedWritableBook(session, *d.Writable, true)
	} else {
		SetCarriedWritableBook(session, WritableBookContent{}, false)
	}
	if d.Written != nil && id == WrittenBookItemID {
		SetCarriedWrittenBook(session, *d.Written, true)
	} else {
		SetCarriedWrittenBook(session, WrittenBookContent{}, false)
	}
	SetCarriedGoatHornInstrument(session, d.Horn)
	if len(d.Bundle) > 0 && IsBundleItem(id) {
		SetCarriedBundleContents(session, d.Bundle)
	} else {
		SetCarriedBundleContents(session, nil)
	}
	if d.Lodestone != nil && IsCompassItem(d.Stack) {
		SetCarriedLodestoneTracker(session, *d.Lodestone, true)
	} else {
		SetCarriedLodestoneTracker(session, LodestoneTracker{}, false)
	}
	if d.Shulker != nil && IsShulkerBoxItemOrBlock(id) {
		SetCarriedShulkerBoxData(session, d.Shulker)
	} else {
		SetCarriedShulkerBoxData(session, nil)
	}
	if d.Star != nil && id == FireworkStarItemID {
		setCarriedFireworkStar(session, d.Star)
	} else {
		setCarriedFireworkStar(session, nil)
	}
	if d.Fireworks != nil && id == FireworkRocketItemID {
		setCarriedFireworks(session, d.Fireworks)
	} else {
		setCarriedFireworks(session, nil)
	}
}

func (s *Server) buildChestMenuSlotDataInDim(session *PlayerSession, dim string, x, y, z int) []containerSlotData {
	slots := make([]containerSlotData, ChestMenuSlotCount)
	for i := 0; i < ChestSlotCount; i++ {
		slots[i] = s.getStorageSlotData(dim, x, y, z, i)
	}
	for i := 0; i < 27; i++ {
		slots[27+i] = getPlayerSlotData(session, 9+i)
	}
	for i := 0; i < 9; i++ {
		slots[54+i] = getPlayerSlotData(session, 36+i)
	}
	return slots
}

func (s *Server) buildDoubleChestMenuSlotDataInDim(session *PlayerSession, dim string, x1, y1, z1, x2, y2, z2 int) []containerSlotData {
	slots := make([]containerSlotData, DoubleChestMenuSlotCount)
	for i := 0; i < ChestSlotCount; i++ {
		slots[i] = s.getStorageSlotData(dim, x1, y1, z1, i)
		slots[ChestSlotCount+i] = s.getStorageSlotData(dim, x2, y2, z2, i)
	}
	for i := 0; i < 27; i++ {
		slots[54+i] = getPlayerSlotData(session, 9+i)
	}
	for i := 0; i < 9; i++ {
		slots[81+i] = getPlayerSlotData(session, 36+i)
	}
	return slots
}

func (s *Server) buildEnderChestMenuSlotData(session *PlayerSession, tr *playerContainerTracker) []containerSlotData {
	slots := make([]containerSlotData, ChestMenuSlotCount)
	for i := 0; i < ChestSlotCount; i++ {
		if !tr.enderChest[i].IsEmpty() {
			slots[i] = containerSlotData{Stack: tr.enderChest[i]}
		}
	}
	for i := 0; i < 27; i++ {
		slots[27+i] = getPlayerSlotData(session, 9+i)
	}
	for i := 0; i < 9; i++ {
		slots[54+i] = getPlayerSlotData(session, 36+i)
	}
	return slots
}

func (s *Server) buildCraftingMenuSlotData(session *PlayerSession, open *OpenContainerState) []containerSlotData {
	slots := make([]containerSlotData, CraftingMenuSlotCount)
	if !open.Result.IsEmpty() {
		d := containerSlotData{Stack: open.Result}
		if IsPotionOrTippedArrowItem(open.Result) {
			d.Potion = open.CraftingPotions[4]
		}
		if exp, ok := GetSlotFireworkStar(session, 0); ok && open.Result.ResolvedID() == FireworkStarItemID {
			d.Star = &exp
		}
		if fw, ok := GetSlotFireworks(session, 0); ok && open.Result.ResolvedID() == FireworkRocketItemID {
			d.Fireworks = &fw
		}
		slots[0] = d
	}
	for i := 0; i < CraftingGridSlotCount; i++ {
		if !open.Crafting[i].IsEmpty() {
			var d containerSlotData
			if !open.CraftingSlots[i].IsEmpty() && open.CraftingSlots[i].Stack.ResolvedItemID() == open.Crafting[i].ResolvedItemID() {
				d = open.CraftingSlots[i].WithCount(open.Crafting[i].Count)
			} else {
				d = containerSlotData{Stack: open.Crafting[i], Potion: open.CraftingPotions[i]}
				if len(open.CraftingStars[i].Colors) > 0 || len(open.CraftingStars[i].FadeColors) > 0 {
					stCopy := open.CraftingStars[i].Clone()
					d.Star = &stCopy
				}
			}
			slots[1+i] = d
		}
	}
	for i := 0; i < 27; i++ {
		slots[10+i] = getPlayerSlotData(session, 9+i)
	}
	for i := 0; i < 9; i++ {
		slots[37+i] = getPlayerSlotData(session, 36+i)
	}
	return slots
}

func (s *Server) buildPlayerMenuSlotData(session *PlayerSession, tr *playerContainerTracker) []containerSlotData {
	slots := make([]containerSlotData, world.PlayerInventorySlots)
	for i := 0; i < world.PlayerInventorySlots; i++ {
		slots[i] = getPlayerSlotData(session, i)
	}
	if !tr.invResult.IsEmpty() {
		d := containerSlotData{Stack: tr.invResult}
		if exp, ok := GetSlotFireworkStar(session, 0); ok && tr.invResult.ResolvedID() == FireworkStarItemID {
			d.Star = &exp
		}
		if fw, ok := GetSlotFireworks(session, 0); ok && tr.invResult.ResolvedID() == FireworkRocketItemID {
			d.Fireworks = &fw
		}
		slots[0] = d
	}
	for i := 0; i < 4; i++ {
		if !tr.invCraft[i].IsEmpty() && slots[1+i].IsEmpty() {
			slots[1+i] = containerSlotData{Stack: tr.invCraft[i]}
			if len(tr.invCraftStars[i].Colors) > 0 || len(tr.invCraftStars[i].FadeColors) > 0 {
				stCopy := tr.invCraftStars[i].Clone()
				slots[1+i].Star = &stCopy
			}
		}
	}
	return slots
}

func (s *Server) buildActiveMenuSlotDataLocked(session *PlayerSession, tr *playerContainerTracker) (cid int32, slots []containerSlotData, carried containerSlotData, supported bool) {
	if session == nil || tr == nil {
		return 0, nil, containerSlotData{}, false
	}
	carried = getCarriedSlotDataLocked(session, tr)
	if tr.open == nil {
		return 0, s.buildPlayerMenuSlotData(session, tr), carried, true
	}
	open := tr.open
	switch open.MenuType {
	case MenuTypeGeneric9x3, MenuTypeShulkerBox:
		if open.VehicleEntityID != 0 {
			raw := s.buildVehicleChestMenuSlots(session, open.VehicleEntityID)
			slots = make([]containerSlotData, len(raw))
			for i, st := range raw {
				slots[i] = containerSlotData{Stack: st}
			}
			return open.ContainerID, slots, carried, true
		}
		if open.IsEnderChest {
			return open.ContainerID, s.buildEnderChestMenuSlotData(session, tr), carried, true
		}
		dim := open.Dimension
		if dim == "" {
			dim = session.Dimension()
		}
		return open.ContainerID, s.buildChestMenuSlotDataInDim(session, dim, open.BlockX, open.BlockY, open.BlockZ), carried, true
	case MenuTypeGeneric9x6:
		dim := open.Dimension
		if dim == "" {
			dim = session.Dimension()
		}
		return open.ContainerID, s.buildDoubleChestMenuSlotDataInDim(session, dim, open.BlockX, open.BlockY, open.BlockZ, open.PartnerX, open.PartnerY, open.PartnerZ), carried, true
	case MenuTypeCrafting:
		return open.ContainerID, s.buildCraftingMenuSlotData(session, open), carried, true
	default:
		return open.ContainerID, nil, carried, false
	}
}

func setTrackerRemoteSnapshot(tr *playerContainerTracker, cid int32, slots []containerSlotData, carried containerSlotData) {
	if tr == nil {
		return
	}
	tr.remoteContainerID = cid
	if cap(tr.remoteSlots) < len(slots) {
		tr.remoteSlots = make([]HashedStack, len(slots))
	} else {
		tr.remoteSlots = tr.remoteSlots[:len(slots)]
	}
	for i, d := range slots {
		tr.remoteSlots[i] = d.ToHashedStack()
	}
	tr.remoteCarried = carried.ToHashedStack()
	tr.hasRemoteCarried = true
}

// InitPlayerInventoryRemoteState initializes remoteSlots/remoteCarried for container 0 after initial login content sync.
func (s *Server) InitPlayerInventoryRemoteState(session *PlayerSession) {
	if s == nil || session == nil {
		return
	}
	tr := getContainerTracker(session)
	tr.syncMu.Lock()
	defer tr.syncMu.Unlock()
	tr.mu.Lock()
	if tr.open == nil {
		slots := s.buildPlayerMenuSlotData(session, tr)
		carried := getCarriedSlotDataLocked(session, tr)
		setTrackerRemoteSnapshot(tr, 0, slots, carried)
	}
	tr.mu.Unlock()
}

func syncRemotePlayerInventorySlot(_ *Server, session *PlayerSession, slot int) {
	if session == nil || slot < 0 || slot >= world.PlayerInventorySlots {
		return
	}
	tr := getContainerTracker(session)
	tr.syncMu.Lock()
	defer tr.syncMu.Unlock()
	tr.mu.Lock()
	if tr.open == nil && tr.remoteContainerID == 0 && slot < len(tr.remoteSlots) {
		tr.remoteSlots[slot] = getPlayerSlotData(session, slot).ToHashedStack()
	}
	tr.mu.Unlock()
}

// WritePlayerInventorySlot encodes player inventory slot including all 26.1.2 item components.
func (s *Server) WritePlayerInventorySlot(w *protocol.Writer, session *PlayerSession, slot int) {
	if s == nil || session == nil {
		w.VarInt(0)
		return
	}
	getPlayerSlotData(session, slot).WriteProtocolSlot(w)
}

// SpawnPlayerDroppedItem spawns a minecraft:item entity dropped by session matching
// vanilla LivingEntity.createItemStackToDrop(stack, false, true):
// pickupDelay = 40 ticks, spawned at eyeY - 0.3 with look-direction velocity vector,
// and preserving all 26.1.2 item components on server tracking and broadcast packets.
func (s *Server) SpawnPlayerDroppedItem(session *PlayerSession, d containerSlotData) (int32, *world.Entity) {
	if s == nil || session == nil || s.entities == nil || d.IsEmpty() {
		return 0, nil
	}
	itemID := d.Stack.ResolvedItemID()
	if itemID <= 0 {
		return 0, nil
	}
	snap := session.Snapshot()
	dim := world.NormalizeDimension(snap.Dimension)
	eyeHeight := 1.62
	if session.IsSneaking() {
		eyeHeight = 1.27
	}
	spawnX := snap.X
	spawnY := snap.Y + eyeHeight - 0.3
	spawnZ := snap.Z

	pitchRad := float64(snap.Pitch) * (math.Pi / 180.0)
	yawRad := float64(snap.Yaw) * (math.Pi / 180.0)
	sinPitch, cosPitch := math.Sin(pitchRad), math.Cos(pitchRad)
	sinYaw, cosYaw := math.Sin(yawRad), math.Cos(yawRad)
	randAngle := rand.Float64() * 2.0 * math.Pi
	randMag := 0.02 * rand.Float64()
	vx := -sinYaw*cosPitch*0.3 + math.Cos(randAngle)*randMag
	vy := -sinPitch*0.3 + 0.1 + (rand.Float64()-rand.Float64())*0.1
	vz := cosYaw*cosPitch*0.3 + math.Sin(randAngle)*randMag

	normalized := world.NewItemStack(itemID, d.Stack.Count)
	typeID := ResolveEntityTypeID(world.ItemEntityTypeName)
	if typeID < 0 {
		typeID = 0
	}
	ent := world.NewItemEntity(spawnX, spawnY, spawnZ, normalized, typeID)
	ent.Dimension = dim
	ent.OnGround = false
	ent.PickupDelay = 40
	ent.VelocityX = int16(math.Round(vx * 8000.0))
	ent.VelocityY = int16(math.Round(vy * 8000.0))
	ent.VelocityZ = int16(math.Round(vz * 8000.0))
	if ent.VelocityX == 0 && ent.VelocityY == 0 && ent.VelocityZ == 0 {
		ent.VelocityY = 800
	}

	id := s.entities.Add(ent)
	saved := s.entities.Get(id)
	if saved == nil || id <= 0 {
		return id, saved
	}
	if !d.Meta.IsEmpty() {
		ft := s.fishingTracker()
		ft.mu.Lock()
		ft.droppedMeta[id] = d.Meta.Clone()
		ft.mu.Unlock()
	}
	canonID := canonicalItemName(normalized.ResolvedID())
	if d.Horn != "" && canonID == GoatHornItemID {
		s.AssociateDroppedGoatHornEntity(id, d.Horn)
	}
	if d.HasMap && canonID == FilledMapItemID {
		s.AssociateDroppedMapEntity(id, d.MapID)
	}
	if len(d.Bundle) > 0 && IsBundleItem(normalized.ResolvedID()) {
		s.AssociateDroppedBundleEntity(id, d.Bundle)
	}
	if d.Potion != "" && IsPotionOrTippedArrowItem(normalized) {
		s.AssociateDroppedPotionEntity(id, d.Potion)
	}
	if d.Shulker != nil && IsShulkerBoxItemOrBlock(normalized.ResolvedID()) {
		s.AssociateDroppedShulkerBoxEntity(id, d.Shulker)
	}

	dw := protocol.NewWriter(32)
	dw.VarInt(id)
	dw.Byte(8)   // ItemEntity.DATA_ITEM accessor ID
	dw.VarInt(7) // EntityDataSerializers.ITEM_STACK
	d.WithCount(normalized.Count).WriteProtocolSlot(dw)
	dw.Byte(0xFF)

	pos := saved.ChunkPos()
	s.BroadcastDimensionChunk(dim, pos.X, pos.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, saved))
	s.BroadcastDimensionChunk(dim, pos.X, pos.Z, protocol.PlaySetEntityData, dw.Bytes())
	return id, saved
}

// InteractBlock checks whether the block at (x, y, z) is an interactive
// container (chest, crafting table, furnace/blast_furnace/smoker, enchanting
// table, anvil, grindstone, or brewing stand) and opens its UI for session.
func (s *Server) InteractBlock(session *PlayerSession, x, y, z int) (opened bool, containerID int32, err error) {
	if s == nil || session == nil || session.IsDead() {
		return false, 0, nil
	}
	cache := s.PlayerChunks(session)
	if cache == nil {
		return false, 0, nil
	}
	state := cache.GetBlock(x, y, z)
	blockName := world.BlockName(state)
	switch {
	case IsChestBlock(state):
		if session.IsSneaking() && !session.HeldItem().IsEmpty() {
			return false, 0, nil
		}
		chestDim := world.NormalizeDimension(session.Dimension())
		if cache.IsChestBlockedAbove(x, y, z) || s.IsCatSittingOnChestAt(chestDim, x, y, z) {
			return true, 0, nil
		}
		if px, py, pz, _, ok := cache.DoubleChestPartnerAt(x, y, z); ok && (cache.IsChestBlockedAbove(px, py, pz) || s.IsCatSittingOnChestAt(chestDim, px, py, pz)) {
			return true, 0, nil
		}
		s.RecordContainerOpenStat(session, blockName)
		cid, err := s.OpenChest(session, x, y, z)
		if err == nil {
			s.EmitPlayerBlockVibration(session, GameEventContainerOpen, session.Dimension(), x, y, z, blockName)
		}
		return err == nil, cid, err
	case IsBarrelBlock(state):
		if session.IsSneaking() && !session.HeldItem().IsEmpty() {
			return false, 0, nil
		}
		s.RecordContainerOpenStat(session, blockName)
		cid, err := s.OpenBarrel(session, x, y, z)
		if err == nil {
			s.EmitPlayerBlockVibration(session, GameEventContainerOpen, session.Dimension(), x, y, z, blockName)
		}
		return err == nil, cid, err
	case IsShulkerBoxItemOrBlock(blockName):
		if session.IsSneaking() && !session.HeldItem().IsEmpty() {
			return false, 0, nil
		}
		s.RecordContainerOpenStat(session, blockName)
		cid, err := s.OpenShulkerBox(session, x, y, z)
		if err == nil {
			s.EmitPlayerBlockVibration(session, GameEventContainerOpen, session.Dimension(), x, y, z, blockName)
		}
		return err == nil, cid, err
	case IsEnderChestBlock(state):
		if session.IsSneaking() && !session.HeldItem().IsEmpty() {
			return false, 0, nil
		}
		if cache.IsChestBlockedAbove(x, y, z) {
			return true, 0, nil
		}
		s.RecordContainerOpenStat(session, blockName)
		cid, err := s.OpenEnderChest(session, x, y, z)
		if err == nil {
			s.EmitPlayerBlockVibration(session, GameEventContainerOpen, session.Dimension(), x, y, z, blockName)
		}
		return err == nil, cid, err
	case IsCraftingTableBlock(state):
		if session.IsSneaking() && !session.HeldItem().IsEmpty() {
			return false, 0, nil
		}
		s.RecordContainerOpenStat(session, blockName)
		cid, err := s.OpenCraftingTable(session, x, y, z)
		if err == nil {
			s.EmitPlayerBlockVibration(session, GameEventContainerOpen, session.Dimension(), x, y, z, blockName)
		}
		return err == nil, cid, err
	case IsFurnaceBlock(state):
		if session.IsSneaking() && !session.HeldItem().IsEmpty() {
			return false, 0, nil
		}
		s.RecordContainerOpenStat(session, blockName)
		cid, err := s.OpenFurnace(session, x, y, z)
		if err == nil {
			s.EmitPlayerBlockVibration(session, GameEventContainerOpen, session.Dimension(), x, y, z, blockName)
		}
		return err == nil, cid, err
	case IsEnchantingTableBlock(state):
		cid, err := s.OpenEnchantingTable(session, x, y, z)
		if err == nil {
			s.EmitPlayerBlockVibration(session, GameEventContainerOpen, session.Dimension(), x, y, z, blockName)
		}
		return err == nil, cid, err
	case IsAnvilBlock(state):
		s.RecordContainerOpenStat(session, blockName)
		cid, err := s.OpenAnvil(session, x, y, z)
		if err == nil {
			s.EmitPlayerBlockVibration(session, GameEventContainerOpen, session.Dimension(), x, y, z, blockName)
		}
		return err == nil, cid, err
	case IsGrindstoneBlock(state):
		s.RecordContainerOpenStat(session, blockName)
		cid, err := s.OpenGrindstone(session, x, y, z)
		if err == nil {
			s.EmitPlayerBlockVibration(session, GameEventContainerOpen, session.Dimension(), x, y, z, blockName)
		}
		return err == nil, cid, err
	case IsBrewingStandBlock(state):
		s.RecordContainerOpenStat(session, blockName)
		cid, err := s.OpenBrewingStand(session, x, y, z)
		if err == nil {
			s.EmitPlayerBlockVibration(session, GameEventContainerOpen, session.Dimension(), x, y, z, blockName)
		}
		return err == nil, cid, err
	case IsCartographyTableBlock(state):
		s.RecordContainerOpenStat(session, blockName)
		cid, err := s.OpenCartographyTable(session, x, y, z)
		if err == nil {
			s.EmitPlayerBlockVibration(session, GameEventContainerOpen, session.Dimension(), x, y, z, blockName)
		}
		return err == nil, cid, err
	case IsLoomBlock(state):
		s.RecordContainerOpenStat(session, blockName)
		cid, err := s.OpenLoom(session, x, y, z)
		if err == nil {
			s.EmitPlayerBlockVibration(session, GameEventContainerOpen, session.Dimension(), x, y, z, blockName)
		}
		return err == nil, cid, err
	case IsStonecutterBlock(state):
		s.RecordContainerOpenStat(session, blockName)
		cid, err := s.OpenStonecutter(session, x, y, z)
		if err == nil {
			s.EmitPlayerBlockVibration(session, GameEventContainerOpen, session.Dimension(), x, y, z, blockName)
		}
		return err == nil, cid, err
	case IsSmithingTableBlock(state):
		s.RecordContainerOpenStat(session, blockName)
		cid, err := s.OpenSmithingTable(session, x, y, z)
		if err == nil {
			s.EmitPlayerBlockVibration(session, GameEventContainerOpen, session.Dimension(), x, y, z, blockName)
		}
		return err == nil, cid, err
	case IsBeaconBlock(state):
		s.RecordContainerOpenStat(session, blockName)
		cid, err := s.OpenBeacon(session, x, y, z)
		if err == nil {
			s.EmitPlayerBlockVibration(session, GameEventContainerOpen, session.Dimension(), x, y, z, blockName)
		}
		return err == nil, cid, err
	case IsJukeboxBlock(state):
		handled, _ := s.InteractJukebox(session, 0, x, y, z)
		return handled, 0, nil
	case world.IsNoteBlock(state):
		handled, _ := s.InteractNoteBlock(session, x, y, z)
		if handled {
			s.AwardCustomStat(session, StatTuneNoteBlock, 1)
		}
		return handled, 0, nil
	case IsCauldronBlock(state):
		invSlot := session.HeldInventorySlot()
		var prevItem string
		var prevHasDyed bool
		if invSlot >= 0 {
			prevItem = session.InventorySlot(invSlot).ResolvedID()
			prevHasDyed = HasSlotDyedColor(session, invSlot)
		}
		if handled, _ := s.InteractCauldronAt(session, x, y, z, invSlot); handled {
			s.RecordCauldronInteractionStat(session, prevItem, prevHasDyed, state)
			return true, 0, nil
		}
		if blockName == "minecraft:water_cauldron" {
			if cleaned := s.CleanBannerInCauldron(session, 0, x, y, z); cleaned {
				s.AwardCustomStat(session, StatCleanBanner, 1)
				return true, 0, nil
			}
		}
		return false, 0, nil
	case IsBedBlock(state):
		dim := world.NormalizeDimension(session.Dimension())
		headPos, footPos, hasPartner := ResolveBedPositions(cache, x, y, z)
		if dt, _ := world.LookupDimensionType(dim); !dt.BedWorks {
			s.WakePlayersInBedAt(dim, headPos.X, headPos.Y, headPos.Z)
			if broken, upd := cache.BreakBlockWithUpdates(headPos.X, headPos.Y, headPos.Z); broken {
				s.broadcastDimensionBlockChanges(dim, cache, upd.Changes, upd.LightChanged)
			}
			if hasPartner {
				if broken, upd := cache.BreakBlockWithUpdates(footPos.X, footPos.Y, footPos.Z); broken {
					s.broadcastDimensionBlockChanges(dim, cache, upd.Changes, upd.LightChanged)
				}
			}
			s.ExplodeBlock(dim, headPos.X, headPos.Y, headPos.Z, 5.0, true)
			return true, 0, nil
		}
		if vid, ok := s.BedOwnerAt(dim, headPos.X, headPos.Y, headPos.Z); ok && s.IsVillagerSleeping(vid) {
			s.WakeSleepingVillager(vid)
		} else if hasPartner {
			if vid, ok := s.BedOwnerAt(dim, footPos.X, footPos.Y, footPos.Z); ok && s.IsVillagerSleeping(vid) {
				s.WakeSleepingVillager(vid)
			}
		}
		s.InteractBed(session, x, y, z)
		return true, 0, nil
	case IsRespawnAnchorBlock(state):
		dim := world.NormalizeDimension(session.Dimension())
		charges := RespawnAnchorCharges(state)
		if !(IsGlowstoneItem(session.HeldItem()) && charges < MaxRespawnAnchorCharges) && charges > 0 {
			if dt, _ := world.LookupDimensionType(dim); !dt.RespawnAnchorWorks {
				inWater := cache.IsRespawnAnchorExplosionInWater(x, y, z)
				if broken, upd := cache.BreakBlockWithUpdates(x, y, z); broken {
					s.broadcastDimensionBlockChanges(dim, cache, upd.Changes, upd.LightChanged)
				}
				breakBlocks := !inWater
				s.DetonateExplosion(ExplosionOptions{
					Dimension:   dim,
					X:           float64(x) + 0.5,
					Y:           float64(y) + 0.5,
					Z:           float64(z) + 0.5,
					Radius:      5.0,
					Category:    ExplosionSourceBlock,
					DamageCause: "bad_respawn_point",
					Activator:   session,
					CausesFire:  !inWater,
					BreakBlocks: &breakBlocks,
				})
				return true, 0, nil
			}
		}
		return s.InteractRespawnAnchor(session, x, y, z), 0, nil
	case world.IsLodestoneBlock(state):
		return s.UseCompassOnLodestone(session, 0, x, y, z), 0, nil
	case world.IsBellBlock(state):
		if session.IsSneaking() && !session.HeldItem().IsEmpty() {
			return false, 0, nil
		}
		if s.InteractBell(session, session.Dimension(), x, y, z, world.DefaultBellRingDirection(state), 0.5) {
			return true, 0, nil
		}
		return false, 0, nil
	case IsDispenserOrDropperBlock(state):
		if session.IsSneaking() && !session.HeldItem().IsEmpty() {
			return false, 0, nil
		}
		s.RecordContainerOpenStat(session, blockName)
		cid, err := s.OpenDispenserOrDropper(session, x, y, z)
		return err == nil, cid, err
	case IsHopperBlock(state):
		if session.IsSneaking() && !session.HeldItem().IsEmpty() {
			return false, 0, nil
		}
		cid, err := s.OpenHopper(session, x, y, z)
		return err == nil, cid, err
	case IsCrafterBlock(state):
		if session.IsSneaking() && !session.HeldItem().IsEmpty() {
			return false, 0, nil
		}
		cid, err := s.OpenCrafter(session, x, y, z)
		return err == nil, cid, err
	case IsDecoratedPotBlock(state):
		if session.IsSneaking() && !session.HeldItem().IsEmpty() {
			return false, 0, nil
		}
		_, wobbled := s.InteractDecoratedPot(session, session.Dimension(), x, y, z)
		return wobbled, 0, nil
	case IsCampfireBlock(state):
		_, ok := s.InteractCampfire(session, x, y, z)
		return ok, 0, nil
	case blockName == "minecraft:daylight_detector":
		if session.IsSneaking() && !session.HeldItem().IsEmpty() {
			return false, 0, nil
		}
		ok, _ := s.InteractDaylightDetector(session, x, y, z)
		return ok, 0, nil
	case IsLecternBlock(state):
		s.RecordContainerOpenStat(session, blockName)
		return s.InteractWithLectern(session, 0, x, y, z), 0, nil
	case IsSignBlock(state):
		return s.InteractWithSign(session, 0, x, y, z, true), 0, nil
	case world.IsDragonEggBlock(state):
		if session.IsSneaking() && !session.HeldItem().IsEmpty() {
			return false, 0, nil
		}
		_, ok := s.TeleportDragonEggBlock(session.Dimension(), x, y, z, nil)
		return ok, 0, nil
	case world.IsMovingPistonBlock(state):
		if session.IsSneaking() && !session.HeldItem().IsEmpty() {
			return false, 0, nil
		}
		ok, res := cache.InteractMovingPistonBlockAt(x, y, z)
		if len(res.Changes) > 0 || len(res.LightChanged) > 0 {
			s.broadcastDimensionBlockChanges(world.NormalizeDimension(session.Dimension()), cache, res.Changes, res.LightChanged)
		}
		return ok, 0, nil
	case world.IsFlowerPotBlock(state):
		if session.IsSneaking() && !session.HeldItem().IsEmpty() {
			return false, 0, nil
		}
		_, _, ok := s.InteractWithFlowerPot(session, session.Dimension(), x, y, z, 0)
		return ok, 0, nil
	case world.IsCakeBlock(state) || world.IsCandleCakeBlock(state) || world.IsCandleBlock(state) || blockName == "minecraft:composter":
		if session.IsSneaking() && !session.HeldItem().IsEmpty() {
			return false, 0, nil
		}
		_, ok := s.InteractWithCakeCandleOrComposter(session, session.Dimension(), x, y, z, 0)
		return ok, 0, nil
	default:
		return false, 0, nil
	}
}

// OpenChest opens the 27-slot single chest or 54-slot double chest screen at
// (x, y, z) for session and sends open_screen + container_set_content.
func (s *Server) OpenChest(session *PlayerSession, x, y, z int) (int32, error) {
	if s == nil || session == nil {
		return 0, errors.New("server: nil session")
	}
	dim := world.NormalizeDimension(session.Dimension())
	cache := s.DimensionChunks(dim)
	if (cache != nil && cache.IsChestBlockedAbove(x, y, z)) || s.IsCatSittingOnChestAt(dim, x, y, z) {
		return 0, errors.New("server: chest is blocked above")
	}
	firstX, firstY, firstZ := x, y, z
	secondX, secondY, secondZ := 0, 0, 0
	hasPartner := false
	if cache != nil {
		if px, py, pz, thisFirst, ok := cache.DoubleChestPartnerAt(x, y, z); ok {
			if cache.IsChestBlockedAbove(px, py, pz) || s.IsCatSittingOnChestAt(dim, px, py, pz) {
				return 0, errors.New("server: double chest partner is blocked above")
			}
			hasPartner = true
			if thisFirst {
				secondX, secondY, secondZ = px, py, pz
			} else {
				firstX, firstY, firstZ = px, py, pz
				secondX, secondY, secondZ = x, y, z
			}
		}
	}

	if be, ok := s.StorageBlockEntityAt(dim, firstX, firstY, firstZ); ok && be != nil && !s.CheckStorageUnlocked(session, be.Lock) {
		return 0, errors.New("server: container is locked")
	}
	if hasPartner {
		if pbe, ok := s.StorageBlockEntityAt(dim, secondX, secondY, secondZ); ok && pbe != nil && !s.CheckStorageUnlocked(session, pbe.Lock) {
			return 0, errors.New("server: container is locked")
		}
	}

	_ = s.CloseContainer(session, 0, false)

	menuType := MenuTypeGeneric9x3
	titleComp := TranslatableComponent("container.chest")
	if hasPartner {
		menuType = MenuTypeGeneric9x6
		titleComp = TranslatableComponent("container.chestDouble")
		if be1, ok := s.StorageBlockEntityAt(dim, firstX, firstY, firstZ); ok && be1 != nil && be1.CustomName != "" {
			titleComp = TextComponent(be1.CustomName)
		} else if be2, ok := s.StorageBlockEntityAt(dim, secondX, secondY, secondZ); ok && be2 != nil && be2.CustomName != "" {
			titleComp = TextComponent(be2.CustomName)
		}
	} else if be, ok := s.StorageBlockEntityAt(dim, firstX, firstY, firstZ); ok && be != nil && be.CustomName != "" {
		titleComp = TextComponent(be.CustomName)
	}

	tr := getContainerTracker(session)
	tr.syncMu.Lock()
	defer tr.syncMu.Unlock()

	tr.mu.Lock()
	tr.nextWindow = (tr.nextWindow % 100) + 1
	cid := tr.nextWindow
	tr.open = &OpenContainerState{
		ContainerID: cid,
		MenuType:    menuType,
		Dimension:   dim,
		BlockX:      firstX,
		BlockY:      firstY,
		BlockZ:      firstZ,
		PartnerX:    secondX,
		PartnerY:    secondY,
		PartnerZ:    secondZ,
		HasPartner:  hasPartner,
	}
	carriedData := getCarriedSlotDataLocked(session, tr)
	tr.mu.Unlock()

	_ = s.ChestSlotsInDim(dim, firstX, firstY, firstZ)
	if hasPartner {
		_ = s.ChestSlotsInDim(dim, secondX, secondY, secondZ)
	}
	s.HydrateChestExplorerMaps(firstX, firstY, firstZ, "buried_treasure")
	if hasPartner {
		s.HydrateChestExplorerMaps(secondX, secondY, secondZ, "buried_treasure")
	}
	s.updateContainerViewerState(dim, firstX, firstY, firstZ)

	if session.send != nil {
		if err := session.send(protocol.PlayOpenScreen, EncodeOpenScreenComponent(cid, menuType, titleComp)); err != nil {
			return cid, err
		}
		var slotData []containerSlotData
		if hasPartner {
			slotData = s.buildDoubleChestMenuSlotDataInDim(session, dim, firstX, firstY, firstZ, secondX, secondY, secondZ)
		} else {
			slotData = s.buildChestMenuSlotDataInDim(session, dim, firstX, firstY, firstZ)
		}
		stateID := session.NextContainerStateID()
		if err := session.send(protocol.PlayContainerSetContent, encodeContainerContentFromSlotData(cid, stateID, slotData, carriedData)); err != nil {
			return cid, err
		}
		setTrackerRemoteSnapshot(tr, cid, slotData, carriedData)
		if !hasPartner {
			for i := 0; i < ChestSlotCount; i++ {
				if slotData[i].HasMap && slotData[i].Stack.ResolvedID() == FilledMapItemID {
					if data, exists := s.GetMapData(slotData[i].MapID); exists && data != nil {
						s.refreshMapDecorations(data)
						_ = session.send(PlayMapItemDataCB, EncodeMapItemDataPacket(slotData[i].MapID, data.Scale, data.Locked, data.DecorationList(), data.FullMapColorPatch()))
					}
				}
			}
		}
	}
	return cid, nil
}

// OpenBarrel opens the 27-slot barrel screen at (x, y, z) for session,
// sets open="true" on the barrel block state, and sends open_screen +
// container_set_content.
func (s *Server) OpenBarrel(session *PlayerSession, x, y, z int) (int32, error) {
	if s == nil || session == nil {
		return 0, errors.New("server: nil session")
	}
	dim := world.NormalizeDimension(session.Dimension())
	if be, ok := s.StorageBlockEntityAt(dim, x, y, z); ok && be != nil && !s.CheckStorageUnlocked(session, be.Lock) {
		return 0, errors.New("server: container is locked")
	}
	_ = s.CloseContainer(session, 0, false)

	titleComp := TranslatableComponent("container.barrel")
	if be, ok := s.StorageBlockEntityAt(dim, x, y, z); ok && be != nil && be.CustomName != "" {
		titleComp = TextComponent(be.CustomName)
	}

	tr := getContainerTracker(session)
	tr.syncMu.Lock()
	defer tr.syncMu.Unlock()

	tr.mu.Lock()
	tr.nextWindow = (tr.nextWindow % 100) + 1
	cid := tr.nextWindow
	tr.open = &OpenContainerState{
		ContainerID: cid,
		MenuType:    MenuTypeGeneric9x3,
		Dimension:   dim,
		BlockX:      x,
		BlockY:      y,
		BlockZ:      z,
		IsBarrel:    true,
	}
	carriedData := getCarriedSlotDataLocked(session, tr)
	tr.mu.Unlock()

	_ = s.ChestSlotsInDim(dim, x, y, z)
	s.updateContainerViewerState(dim, x, y, z)

	if session.send != nil {
		if err := session.send(protocol.PlayOpenScreen, EncodeOpenScreenComponent(cid, MenuTypeGeneric9x3, titleComp)); err != nil {
			return cid, err
		}
		slotData := s.buildChestMenuSlotDataInDim(session, dim, x, y, z)
		stateID := session.NextContainerStateID()
		if err := session.send(protocol.PlayContainerSetContent, encodeContainerContentFromSlotData(cid, stateID, slotData, carriedData)); err != nil {
			return cid, err
		}
		setTrackerRemoteSnapshot(tr, cid, slotData, carriedData)
	}
	return cid, nil
}

// OpenEnderChest opens the player's personal 27-slot ender chest screen at
// (x, y, z) for session and sends open_screen + container_set_content.
func (s *Server) OpenEnderChest(session *PlayerSession, x, y, z int) (int32, error) {
	if s == nil || session == nil {
		return 0, errors.New("server: nil session")
	}
	dim := world.NormalizeDimension(session.Dimension())
	if cache := s.DimensionChunks(dim); cache != nil && cache.IsChestBlockedAbove(x, y, z) {
		return 0, errors.New("server: ender chest is blocked above")
	}
	_ = s.CloseContainer(session, 0, false)

	tr := getContainerTracker(session)
	tr.syncMu.Lock()
	defer tr.syncMu.Unlock()

	tr.mu.Lock()
	tr.nextWindow = (tr.nextWindow % 100) + 1
	cid := tr.nextWindow
	tr.open = &OpenContainerState{
		ContainerID:  cid,
		MenuType:     MenuTypeGeneric9x3,
		Dimension:    dim,
		BlockX:       x,
		BlockY:       y,
		BlockZ:       z,
		IsEnderChest: true,
	}
	carriedData := getCarriedSlotDataLocked(session, tr)
	slotData := s.buildEnderChestMenuSlotData(session, tr)
	tr.mu.Unlock()

	s.updateContainerViewerState(dim, x, y, z)

	if session.send != nil {
		if err := session.send(protocol.PlayOpenScreen, EncodeOpenScreenComponent(cid, MenuTypeGeneric9x3, TranslatableComponent("container.enderchest"))); err != nil {
			return cid, err
		}
		stateID := session.NextContainerStateID()
		if err := session.send(protocol.PlayContainerSetContent, encodeContainerContentFromSlotData(cid, stateID, slotData, carriedData)); err != nil {
			return cid, err
		}
		setTrackerRemoteSnapshot(tr, cid, slotData, carriedData)
	}
	return cid, nil
}

// OpenCraftingTable opens the 3x3 crafting table screen at (x, y, z) for
// session and sends open_screen + container_set_content.
func (s *Server) OpenCraftingTable(session *PlayerSession, x, y, z int) (int32, error) {
	if s == nil || session == nil {
		return 0, errors.New("server: nil session")
	}
	_ = s.CloseContainer(session, 0, false)
	dim := world.NormalizeDimension(session.Dimension())

	tr := getContainerTracker(session)
	tr.syncMu.Lock()
	defer tr.syncMu.Unlock()

	tr.mu.Lock()
	tr.nextWindow = (tr.nextWindow % 100) + 1
	cid := tr.nextWindow
	openState := &OpenContainerState{
		ContainerID: cid,
		MenuType:    MenuTypeCrafting,
		Dimension:   dim,
		BlockX:      x,
		BlockY:      y,
		BlockZ:      z,
	}
	tr.open = openState
	carriedData := getCarriedSlotDataLocked(session, tr)
	slotData := s.buildCraftingMenuSlotData(session, openState)
	tr.mu.Unlock()

	if session.send != nil {
		if err := session.send(protocol.PlayOpenScreen, EncodeOpenScreenComponent(cid, MenuTypeCrafting, TranslatableComponent("container.crafting"))); err != nil {
			return cid, err
		}
		stateID := session.NextContainerStateID()
		if err := session.send(protocol.PlayContainerSetContent, encodeContainerContentFromSlotData(cid, stateID, slotData, carriedData)); err != nil {
			return cid, err
		}
		setTrackerRemoteSnapshot(tr, cid, slotData, carriedData)
	}
	return cid, nil
}

func (s *Server) buildChestMenuSlots(session *PlayerSession, x, y, z int) []world.ItemStack {
	dim := world.DimensionOverworld
	if session != nil && session.Dimension() != "" {
		dim = session.Dimension()
	}
	return s.buildChestMenuSlotsInDim(session, dim, x, y, z)
}

func (s *Server) buildChestMenuSlotsInDim(session *PlayerSession, dim string, x, y, z int) []world.ItemStack {
	chest := s.ChestSlotsInDim(dim, x, y, z)
	inv := session.Inventory()
	slots := make([]world.ItemStack, ChestMenuSlotCount)
	copy(slots[0:27], chest[:])
	copy(slots[27:54], inv[9:36])
	copy(slots[54:63], inv[36:45])
	return slots
}

func (s *Server) buildDoubleChestMenuSlots(session *PlayerSession, x1, y1, z1, x2, y2, z2 int) []world.ItemStack {
	dim := world.DimensionOverworld
	if session != nil && session.Dimension() != "" {
		dim = session.Dimension()
	}
	return s.buildDoubleChestMenuSlotsInDim(session, dim, x1, y1, z1, x2, y2, z2)
}

func (s *Server) buildDoubleChestMenuSlotsInDim(session *PlayerSession, dim string, x1, y1, z1, x2, y2, z2 int) []world.ItemStack {
	first := s.ChestSlotsInDim(dim, x1, y1, z1)
	second := s.ChestSlotsInDim(dim, x2, y2, z2)
	inv := session.Inventory()
	slots := make([]world.ItemStack, DoubleChestMenuSlotCount)
	copy(slots[0:27], first[:])
	copy(slots[27:54], second[:])
	copy(slots[54:81], inv[9:36])
	copy(slots[81:90], inv[36:45])
	return slots
}

func (s *Server) buildEnderChestMenuSlots(session *PlayerSession) []world.ItemStack {
	ec := session.EnderChestSlots()
	inv := session.Inventory()
	slots := make([]world.ItemStack, ChestMenuSlotCount)
	copy(slots[0:27], ec[:])
	copy(slots[27:54], inv[9:36])
	copy(slots[54:63], inv[36:45])
	return slots
}

// ContainerViewerCountAt returns the number of active non-spectator players
// currently viewing the container block at (dim, x, y, z) (or its paired
// double-chest half).
func (s *Server) ContainerViewerCountAt(dim string, x, y, z int) int {
	if s == nil {
		return 0
	}
	normDim := world.NormalizeDimension(dim)
	px, py, pz := 0, 0, 0
	hasPartner := false
	if cache := s.DimensionChunks(normDim); cache != nil {
		if partnerX, partnerY, partnerZ, _, ok := cache.DoubleChestPartnerAt(x, y, z); ok {
			px, py, pz = partnerX, partnerY, partnerZ
			hasPartner = true
		}
	}
	s.playersMu.RLock()
	sessions := make([]*PlayerSession, 0, len(s.players))
	for _, sess := range s.players {
		sessions = append(sessions, sess)
	}
	s.playersMu.RUnlock()

	count := 0
	for _, sess := range sessions {
		if sess == nil || sess.IsDead() || sess.Snapshot().GameMode == GameModeSpectator {
			continue
		}
		open := sess.OpenContainer()
		if open == nil || open.VehicleEntityID != 0 {
			continue
		}
		openDim := world.NormalizeDimension(open.Dimension)
		if openDim == "" {
			openDim = world.NormalizeDimension(sess.Dimension())
		}
		if openDim != normDim {
			continue
		}
		if open.MenuType != MenuTypeGeneric9x3 && open.MenuType != MenuTypeGeneric9x6 && open.MenuType != MenuTypeShulkerBox {
			continue
		}
		matchesThis := open.BlockX == x && open.BlockY == y && open.BlockZ == z
		matchesPartner := (open.HasPartner && open.PartnerX == x && open.PartnerY == y && open.PartnerZ == z) ||
			(hasPartner && open.BlockX == px && open.BlockY == py && open.BlockZ == pz)
		if matchesThis || matchesPartner {
			count++
		}
	}
	return count
}

func (s *Server) updateContainerViewerState(dim string, x, y, z int) {
	if s == nil {
		return
	}
	normDim := world.NormalizeDimension(dim)
	cache := s.DimensionChunks(normDim)
	if cache == nil || y < world.MinY || y >= world.MinY+world.WorldHeight {
		return
	}
	state := cache.GetBlock(x, y, z)
	name := world.BlockName(state)
	openCount := s.ContainerViewerCountAt(normDim, x, y, z)
	switch {
	case name == "minecraft:barrel":
		wantOpen := "false"
		if openCount > 0 {
			wantOpen = "true"
		}
		if nextSt, ok := world.WithBlockProperty(state, "open", wantOpen); ok && nextSt != state {
			if applied, res := cache.ApplyBlockWithUpdates(x, y, z, nextSt); applied {
				s.broadcastDimensionBlockChanges(normDim, cache, res.Changes, res.LightChanged)
			}
		}
	case IsChestBlock(state) || name == "minecraft:ender_chest" || IsShulkerBoxItemOrBlock(name):
		actionParam := byte(0)
		if openCount > 255 {
			actionParam = 255
		} else if openCount > 0 {
			actionParam = byte(openCount)
		}
		blockID, _ := world.BlockRegistryID(name)
		s.BroadcastDimensionChunk(normDim, int32(x>>4), int32(z>>4), PlayBlockEventCB,
			EncodeBlockEventPacket(x, y, z, blockID, 1, actionParam))
		if px, py, pz, _, hasPartner := cache.DoubleChestPartnerAt(x, y, z); hasPartner {
			pState := cache.GetBlock(px, py, pz)
			pBlockID, _ := world.BlockRegistryID(world.BlockName(pState))
			s.BroadcastDimensionChunk(normDim, int32(px>>4), int32(pz>>4), PlayBlockEventCB,
				EncodeBlockEventPacket(px, py, pz, pBlockID, 1, actionParam))
		}
		if name == "minecraft:trapped_chest" {
			res := cache.SetTrappedChestOpenCount(x, y, z, openCount)
			if len(res.Changes) > 0 || len(res.LightChanged) > 0 {
				s.broadcastDimensionBlockChanges(normDim, cache, res.Changes, res.LightChanged)
			}
		} else if world.IsCopperChestBlock(state) {
			cache.SetCopperChestOpenCount(x, y, z, openCount)
		}
	}
}

func (s *Server) buildCraftingMenuSlots(session *PlayerSession, grid [CraftingGridSlotCount]world.ItemStack, result world.ItemStack) []world.ItemStack {
	inv := session.Inventory()
	slots := make([]world.ItemStack, CraftingMenuSlotCount)
	slots[0] = result
	copy(slots[1:10], grid[:])
	copy(slots[10:37], inv[9:36])
	copy(slots[37:46], inv[36:45])
	return slots
}

func (s *Server) buildPlayerMenuSlots(session *PlayerSession, grid [4]world.ItemStack, result world.ItemStack) []world.ItemStack {
	inv := session.Inventory()
	slots := make([]world.ItemStack, world.PlayerInventorySlots)
	copy(slots, inv[:])
	slots[0] = result
	for i := 0; i < 4; i++ {
		slots[1+i] = grid[i]
	}
	return slots
}

// CloseContainer closes session's open container (or clears player inventory
// crafting slots), returning any crafting grid and cursor items to the player's
// inventory (or dropping overflow in the world).
func (s *Server) CloseContainer(session *PlayerSession, containerID int32, notifyClient bool) error {
	return s.closeContainerInternal(session, containerID, notifyClient, false)
}

func (s *Server) closeContainerInternal(session *PlayerSession, containerID int32, notifyClient, dropAllOnDisconnect bool) error {
	if s == nil || session == nil {
		return nil
	}
	s.CloseLecternIfOpen(session)
	tr := getContainerTracker(session)
	tr.syncMu.Lock()
	defer tr.syncMu.Unlock()
	tr.mu.Lock()
	var returnItems []containerSlotData
	if !tr.carried.IsEmpty() {
		returnItems = append(returnItems, getCarriedSlotDataLocked(session, tr))
		setCarriedSlotDataLocked(session, tr, containerSlotData{})
	}
	closedID := containerID
	hadOpen := false
	var closedX, closedY, closedZ int
	var partnerX, partnerY, partnerZ int
	var hasPartner bool
	var closedDim string
	if tr.open != nil && (containerID == 0 || containerID == tr.open.ContainerID) {
		hadOpen = true
		closedID = tr.open.ContainerID
		closedX, closedY, closedZ = tr.open.BlockX, tr.open.BlockY, tr.open.BlockZ
		partnerX, partnerY, partnerZ = tr.open.PartnerX, tr.open.PartnerY, tr.open.PartnerZ
		hasPartner = tr.open.HasPartner
		closedDim = tr.open.Dimension
		var smithingMetas [3]ItemMeta
		var smithingTrims [3]ArmorTrim
		if tr.open.MenuType == MenuTypeSmithing {
			if ts := getPlayerArmorTrimState(session); ts != nil {
				ts.mu.Lock()
				smithingMetas = ts.smithingInputMeta
				smithingTrims = ts.smithingInputTrim
				ts.smithingInputMeta = [SmithingInputSlotCount]ItemMeta{}
				ts.smithingInputTrim = [SmithingInputSlotCount]ArmorTrim{}
				ts.mu.Unlock()
			}
		}
		for i := range tr.open.Crafting {
			if !tr.open.Crafting[i].IsEmpty() {
				var entry containerSlotData
				if (tr.open.MenuType == MenuTypeCrafting || tr.open.MenuType == MenuTypeMerchant || tr.open.MenuType == MenuTypeStonecutter) &&
					!tr.open.CraftingSlots[i].IsEmpty() &&
					tr.open.CraftingSlots[i].Stack.ResolvedItemID() == tr.open.Crafting[i].ResolvedItemID() {
					entry = tr.open.CraftingSlots[i].WithCount(tr.open.Crafting[i].Count)
				} else {
					entry = containerSlotData{
						Stack:  tr.open.Crafting[i],
						Potion: tr.open.CraftingPotions[i],
					}
					if tr.open.MenuType == MenuTypeSmithing && i < 3 {
						entry.Meta = smithingMetas[i].Clone()
						entry.Trim = smithingTrims[i]
					}
					if len(tr.open.CraftingStars[i].Colors) > 0 || len(tr.open.CraftingStars[i].FadeColors) > 0 {
						cp := tr.open.CraftingStars[i].Clone()
						entry.Star = &cp
					}
				}
				returnItems = append(returnItems, entry)
			}
		}
		metaTr := getItemMetaTracker(session)
		metaTr.mu.Lock()
		for i := range tr.open.EnchantSlots {
			if !tr.open.EnchantSlots[i].IsEmpty() {
				returnItems = append(returnItems, containerSlotData{
					Stack: tr.open.EnchantSlots[i],
					Meta:  metaTr.enchantMeta[i].Clone(),
					Trim:  metaTr.enchantTrim[i],
				})
				metaTr.enchantMeta[i] = ItemMeta{}
				metaTr.enchantTrim[i] = ArmorTrim{}
			}
		}
		for i := range tr.open.CombinerInputs {
			if !tr.open.CombinerInputs[i].IsEmpty() {
				returnItems = append(returnItems, containerSlotData{
					Stack: tr.open.CombinerInputs[i],
					Meta:  metaTr.combinerMeta[i].Clone(),
					Trim:  metaTr.combinerTrim[i],
				})
				metaTr.combinerMeta[i] = ItemMeta{}
				metaTr.combinerTrim[i] = ArmorTrim{}
			}
		}
		metaTr.resultMeta = ItemMeta{}
		metaTr.resultTrim = ArmorTrim{}
		metaTr.mu.Unlock()
		tr.open = nil
	}
	for i := range tr.invCraft {
		if !tr.invCraft[i].IsEmpty() {
			returnItems = append(returnItems, containerSlotData{Stack: tr.invCraft[i]})
			tr.invCraft[i] = world.ItemStack{}
		}
	}
	tr.invResult = world.ItemStack{}
	tr.dragStatus = 0
	tr.dragType = 0
	tr.dragSlots = nil
	session.SetInventorySlot(0, world.ItemStack{})
	for i := 1; i <= 4; i++ {
		session.SetInventorySlot(i, world.ItemStack{})
	}
	tr.remoteContainerID = 0
	tr.remoteSlots = nil
	tr.hasRemoteCarried = false
	tr.mu.Unlock()

	for _, entry := range returnItems {
		if entry.IsEmpty() {
			continue
		}
		if dropAllOnDisconnect {
			_, _ = s.SpawnPlayerDroppedItem(session, entry)
			continue
		}
		if entry.Shulker == nil && entry.Potion == "" && !entry.HasMap && len(entry.Bundle) == 0 && entry.Horn == "" && entry.Writable == nil && entry.Written == nil && entry.Fireworks == nil && entry.Star == nil && entry.Lodestone == nil && entry.Banner.IsEmpty() && (!entry.Meta.IsEmpty() || entry.Trim.IsValid()) {
			placed := returnStackWithMetaAndTrimToInventory(session, entry.Stack, entry.Meta, entry.Trim)
			if rem := entry.Stack.Count - placed; rem > 0 {
				_, _ = s.SpawnPlayerDroppedItem(session, entry.WithCount(rem))
			}
			continue
		}
		added, changedSlots := session.AddItemToInventory(entry.Stack)
		if len(changedSlots) > 0 {
			dst := changedSlots[0]
			if !entry.Meta.IsEmpty() {
				SetSlotItemMeta(session, dst, entry.Meta)
			}
			if entry.Potion != "" {
				SetSlotPotionType(session, dst, entry.Potion)
			}
			if entry.Trim.IsValid() {
				SetSlotArmorTrim(session, dst, entry.Trim)
			}
			if entry.Shulker != nil {
				SetSlotShulkerBoxData(session, dst, entry.Shulker)
			}
			if entry.HasMap && entry.Stack.ResolvedID() == FilledMapItemID {
				SetSlotMapID(session, dst, entry.MapID)
			}
			if len(entry.Bundle) > 0 && IsBundleItem(entry.Stack.ResolvedID()) {
				SetSlotBundleContents(session, dst, entry.Bundle)
			}
			if entry.Horn != "" {
				SetSlotGoatHornInstrument(session, dst, entry.Horn)
			}
			if entry.Writable != nil {
				SetSlotWritableBook(session, dst, *entry.Writable)
			}
			if entry.Written != nil {
				SetSlotWrittenBook(session, dst, *entry.Written)
			}
			if entry.Fireworks != nil {
				SetSlotFireworks(session, dst, *entry.Fireworks)
			}
			if entry.Star != nil {
				SetSlotFireworkStar(session, dst, *entry.Star)
			}
			if entry.Lodestone != nil {
				SetSlotLodestoneTracker(session, dst, *entry.Lodestone)
			}
		}
		if rem := entry.Stack.Count - added; rem > 0 {
			_, _ = s.SpawnPlayerDroppedItem(session, entry.WithCount(rem))
		}
	}

	if hadOpen {
		if closedDim == "" {
			closedDim = session.Dimension()
		}
		s.updateContainerViewerState(closedDim, closedX, closedY, closedZ)
		if hasPartner {
			s.updateContainerViewerState(closedDim, partnerX, partnerY, partnerZ)
		}
		s.EmitPlayerBlockVibration(session, GameEventContainerClose, closedDim, closedX, closedY, closedZ, "")
	}

	if session.send != nil && (notifyClient || hadOpen || len(returnItems) > 0) && !dropAllOnDisconnect {
		if notifyClient && closedID > 0 {
			_ = session.send(protocol.PlayContainerClose, EncodeContainerClose(closedID))
		}
		_ = s.syncPlayerInventoryContainerLocked(session, tr)
	}
	return nil
}

// CloseCraftingTableViewersAt closes the open crafting table screen (and returns
// grid + cursor items) for all sessions currently viewing the crafting_table at (dim, x, y, z).
func (s *Server) CloseCraftingTableViewersAt(dim string, x, y, z int) {
	if s == nil {
		return
	}
	normDim := world.NormalizeDimension(dim)
	s.playersMu.RLock()
	sessions := make([]*PlayerSession, 0, len(s.players))
	for _, sess := range s.players {
		sessions = append(sessions, sess)
	}
	s.playersMu.RUnlock()
	for _, sess := range sessions {
		open := sess.OpenContainer()
		if open == nil || open.MenuType != MenuTypeCrafting {
			continue
		}
		openDim := world.NormalizeDimension(open.Dimension)
		if open.Dimension == "" {
			openDim = world.NormalizeDimension(sess.Dimension())
		}
		if openDim == normDim && open.BlockX == x && open.BlockY == y && open.BlockZ == z {
			_ = s.CloseContainer(sess, open.ContainerID, true)
		}
	}
}

// HandleContainerClickWithState processes a slot click while checking whether the
// client's stateID matches the server's current ContainerStateID. On mismatch, a
// full container + cursor item resync is guaranteed after applying the click
// (matching ServerGamePacketListenerImpl.handleContainerClick in 26.1.2).
func (s *Server) HandleContainerClickWithState(session *PlayerSession, containerID, stateID int32, slot int, button int8, mode int32) error {
	if s == nil || session == nil || session.IsDead() {
		return nil
	}
	stateMismatch := stateID != session.ContainerStateID()
	prevStateID := session.ContainerStateID()
	err := s.HandleContainerClick(session, containerID, slot, button, mode)
	if err != nil {
		return err
	}
	if stateMismatch {
		if session.ContainerStateID() == prevStateID {
			if err := s.syncOpenContainer(session); err != nil {
				return err
			}
		}
		return s.SendSetCursorItem(session)
	}
	return nil
}

// HandleContainerClickPacket processes a protocol 775 serverbound container_click
// packet with HashedStack prediction verification and incremental/full remote state sync.
func (s *Server) HandleContainerClickPacket(session *PlayerSession, click ContainerClick) error {
	if s == nil || session == nil || session.IsDead() {
		return nil
	}
	tr := getContainerTracker(session)
	tr.syncMu.Lock()
	tr.mu.Lock()
	if click.ContainerID != 0 && (tr.open == nil || tr.open.ContainerID != click.ContainerID) {
		tr.mu.Unlock()
		tr.syncMu.Unlock()
		return nil
	}
	if click.ContainerID == 0 && tr.open != nil {
		tr.mu.Unlock()
		tr.syncMu.Unlock()
		return s.HandleContainerClickWithState(session, click.ContainerID, click.StateID, int(click.Slot), click.Button, click.Mode)
	}
	_, _, _, supported := s.buildActiveMenuSlotDataLocked(session, tr)
	if tr.open != nil && tr.open.VehicleEntityID != 0 {
		supported = false
	}
	if !supported {
		tr.mu.Unlock()
		tr.syncMu.Unlock()
		return s.HandleContainerClickWithState(session, click.ContainerID, click.StateID, int(click.Slot), click.Button, click.Mode)
	}
	if session.Snapshot().GameMode == GameModeSpectator {
		tr.mu.Unlock()
		err := s.syncActiveContainerLocked(session, tr)
		tr.syncMu.Unlock()
		return err
	}

	stateMismatch := click.StateID != session.ContainerStateID()
	activeCID, preSlots, preCarried, _ := s.buildActiveMenuSlotDataLocked(session, tr)
	if tr.remoteContainerID != activeCID || len(tr.remoteSlots) != len(preSlots) || !tr.hasRemoteCarried {
		setTrackerRemoteSnapshot(tr, activeCID, preSlots, preCarried)
	}

	pendingDrop, mutated, sharedStorage, sharedDim, sharedX, sharedY, sharedZ := s.mutateSupportedClickLocked(session, tr, click.ContainerID, int(click.Slot), click.Button, click.Mode)
	postCID, postSlots, postCarried, _ := s.buildActiveMenuSlotDataLocked(session, tr)

	if click.HasWirePrediction {
		for sl, hs := range click.ChangedSlots {
			if sl >= 0 && sl < len(tr.remoteSlots) {
				tr.remoteSlots[sl] = hs
			}
		}
		tr.remoteCarried = click.Carried
		tr.hasRemoteCarried = true
	}
	tr.mu.Unlock()

	if !pendingDrop.IsEmpty() {
		_, _ = s.SpawnPlayerDroppedItem(session, pendingDrop)
		s.RecordItemDropped(session, pendingDrop.Stack.ResolvedID(), pendingDrop.Stack.Count)
	}
	if mutated {
		s.TriggerInventoryChanged(session)
		s.UnlockRecipesForInventory(session)
	}

	var sendErr error
	if stateMismatch || !click.HasWirePrediction {
		setTrackerRemoteSnapshot(tr, postCID, postSlots, postCarried)
		if session.send != nil {
			stateID := session.NextContainerStateID()
			if err := session.send(protocol.PlayContainerSetContent, encodeContainerContentFromSlotData(postCID, stateID, postSlots, postCarried)); err != nil {
				sendErr = err
			}
			if err := session.send(protocol.PlaySetCursorItem, encodeSetCursorItemFromSlotData(postCarried)); err != nil && sendErr == nil {
				sendErr = err
			}
		}
	} else {
		for i := 0; i < len(postSlots); i++ {
			actualHS := postSlots[i].ToHashedStack()
			if i >= len(tr.remoteSlots) || !actualHS.Matches(tr.remoteSlots[i]) {
				if session.send != nil {
					stateID := session.NextContainerStateID()
					if err := session.send(protocol.PlayContainerSetSlot, encodeContainerSlotFromSlotData(postCID, stateID, i, postSlots[i])); err != nil && sendErr == nil {
						sendErr = err
					}
				}
				if i < len(tr.remoteSlots) {
					tr.remoteSlots[i] = actualHS
				}
			}
		}
		actualCarriedHS := postCarried.ToHashedStack()
		if !tr.hasRemoteCarried || !actualCarriedHS.Matches(tr.remoteCarried) {
			if session.send != nil {
				if err := session.send(protocol.PlaySetCursorItem, encodeSetCursorItemFromSlotData(postCarried)); err != nil && sendErr == nil {
					sendErr = err
				}
			}
			tr.remoteCarried = actualCarriedHS
			tr.hasRemoteCarried = true
		}
	}
	tr.syncMu.Unlock()

	if sharedStorage {
		if err := s.syncOtherStorageViewers(session, sharedDim, sharedX, sharedY, sharedZ); err != nil && sendErr == nil {
			sendErr = err
		}
	}
	return sendErr
}

func (s *Server) mutateSupportedClickLocked(
	session *PlayerSession,
	tr *playerContainerTracker,
	containerID int32,
	slot int,
	button int8,
	mode int32,
) (pendingDrop containerSlotData, mutated bool, sharedStorage bool, sharedDim string, sharedX, sharedY, sharedZ int) {
	if mode == ContainerInputQuickCraft {
		var qcShared bool
		mutated, sharedDim, sharedX, sharedY, sharedZ, qcShared = s.handleQuickCraftMutateLocked(session, tr, containerID, slot, button)
		if qcShared && mutated {
			sharedStorage = true
		}
		return containerSlotData{}, mutated, sharedStorage, sharedDim, sharedX, sharedY, sharedZ
	}
	if tr.dragStatus != 0 {
		tr.dragStatus = 0
		tr.dragType = 0
		tr.dragSlots = nil
	}

	if slot == -999 && (mode == ContainerInputPickup || mode == ContainerInputThrow) {
		carried := getCarriedSlotDataLocked(session, tr)
		if !carried.IsEmpty() {
			dropCount := carried.Stack.Count
			if button == 1 {
				dropCount = 1
			}
			pendingDrop = carried.WithCount(dropCount)
			setCarriedSlotDataLocked(session, tr, carried.WithCount(carried.Stack.Count-dropCount))
			mutated = true
		}
		return pendingDrop, mutated, false, "", 0, 0, 0
	}

	if containerID == 0 {
		pendingDrop, mutated = s.applyPlayerInventoryClickDataLocked(session, tr, slot, button, mode)
		return pendingDrop, mutated, false, "", 0, 0, 0
	}
	if tr.open == nil || tr.open.ContainerID != containerID {
		return containerSlotData{}, false, false, "", 0, 0, 0
	}
	switch tr.open.MenuType {
	case MenuTypeGeneric9x3, MenuTypeShulkerBox:
		if tr.open.IsEnderChest {
			pendingDrop, mutated = s.applyEnderChestClickDataLocked(session, tr, slot, button, mode)
			return pendingDrop, mutated, false, "", 0, 0, 0
		}
		bx, by, bz := tr.open.BlockX, tr.open.BlockY, tr.open.BlockZ
		openDim := tr.open.Dimension
		if openDim == "" {
			openDim = session.Dimension()
		}
		pendingDrop, mutated = s.applyChestClickDataLocked(session, tr, bx, by, bz, slot, button, mode)
		return pendingDrop, mutated, mutated, openDim, bx, by, bz
	case MenuTypeGeneric9x6:
		bx, by, bz := tr.open.BlockX, tr.open.BlockY, tr.open.BlockZ
		px, py, pz := tr.open.PartnerX, tr.open.PartnerY, tr.open.PartnerZ
		openDim := tr.open.Dimension
		if openDim == "" {
			openDim = session.Dimension()
		}
		pendingDrop, mutated = s.applyDoubleChestClickDataLocked(session, tr, bx, by, bz, px, py, pz, slot, button, mode)
		return pendingDrop, mutated, mutated, openDim, bx, by, bz
	case MenuTypeCrafting:
		pendingDrop, mutated = s.applyCraftingTableClickDataLocked(session, tr, slot, button, mode)
		return pendingDrop, mutated, false, "", 0, 0, 0
	}
	return containerSlotData{}, false, false, "", 0, 0, 0
}

// HandleContainerClick processes a slot click on either the player's inventory
// container (containerID == 0) or the currently open chest / crafting table
// container, updating authoritative slot contents and synchronizing the client.
func (s *Server) HandleContainerClick(session *PlayerSession, containerID int32, slot int, button int8, mode int32) error {
	if s == nil || session == nil || session.IsDead() {
		return nil
	}
	if session.Snapshot().GameMode == GameModeSpectator {
		return s.syncOpenContainer(session)
	}
	tr := getContainerTracker(session)
	tr.mu.Lock()

	if mode == ContainerInputQuickCraft {
		return s.handleQuickCraftClickLocked(session, tr, containerID, slot, button)
	}
	if tr.dragStatus != 0 {
		tr.dragStatus = 0
		tr.dragType = 0
		tr.dragSlots = nil
	}

	// Clicking outside the window (-999) with Pickup or Throw drops carried item(s).
	if slot == -999 && (mode == ContainerInputPickup || mode == ContainerInputThrow) {
		carried := getCarriedSlotDataLocked(session, tr)
		if !carried.IsEmpty() {
			dropCount := carried.Stack.Count
			if button == 1 {
				dropCount = 1
			}
			pendingDrop := carried.WithCount(dropCount)
			setCarriedSlotDataLocked(session, tr, carried.WithCount(carried.Stack.Count-dropCount))
			tr.mu.Unlock()
			_, _ = s.SpawnPlayerDroppedItem(session, pendingDrop)
			s.RecordItemDropped(session, pendingDrop.Stack.ResolvedID(), pendingDrop.Stack.Count)
			return s.syncOpenContainer(session)
		}
		tr.mu.Unlock()
		return s.syncOpenContainer(session)
	}

	if containerID == 0 {
		s.applyPlayerInventoryClickLocked(session, tr, slot, button, mode)
		tr.mu.Unlock()
		s.TriggerInventoryChanged(session)
		s.UnlockRecipesForInventory(session)
		return s.syncPlayerInventoryContainer(session)
	}

	if tr.open == nil || tr.open.ContainerID != containerID {
		tr.mu.Unlock()
		return nil
	}

	switch tr.open.MenuType {
	case MenuTypeGeneric9x3, MenuTypeShulkerBox:
		if tr.open.VehicleEntityID != 0 {
			vehID := tr.open.VehicleEntityID
			s.applyVehicleChestClickLocked(session, tr, vehID, slot, button, mode)
			tr.mu.Unlock()
			s.TriggerInventoryChanged(session)
			s.UnlockRecipesForInventory(session)
			return s.syncVehicleContainerViewers(session, vehID)
		}
		if tr.open.IsEnderChest {
			s.applyEnderChestClickLocked(session, tr, slot, button, mode)
			tr.mu.Unlock()
			s.TriggerInventoryChanged(session)
			s.UnlockRecipesForInventory(session)
			return s.syncOpenContainer(session)
		}
		bx, by, bz := tr.open.BlockX, tr.open.BlockY, tr.open.BlockZ
		s.applyChestClickLocked(session, tr, bx, by, bz, slot, button, mode)
		tr.mu.Unlock()
		s.TriggerInventoryChanged(session)
		s.UnlockRecipesForInventory(session)
		return s.syncChestViewers(session, bx, by, bz)
	case MenuTypeGeneric9x6:
		bx, by, bz := tr.open.BlockX, tr.open.BlockY, tr.open.BlockZ
		px, py, pz := tr.open.PartnerX, tr.open.PartnerY, tr.open.PartnerZ
		s.applyDoubleChestClickLocked(session, tr, bx, by, bz, px, py, pz, slot, button, mode)
		tr.mu.Unlock()
		s.TriggerInventoryChanged(session)
		s.UnlockRecipesForInventory(session)
		return s.syncChestViewers(session, bx, by, bz)
	case MenuTypeHopper:
		if tr.open.VehicleEntityID != 0 {
			vehID := tr.open.VehicleEntityID
			s.applyVehicleHopperClickLocked(session, tr, vehID, slot, button, mode)
			tr.mu.Unlock()
			s.TriggerInventoryChanged(session)
			s.UnlockRecipesForInventory(session)
			return s.syncVehicleContainerViewers(session, vehID)
		}
		openCopy := *tr.open
		tr.mu.Unlock()
		return s.HandleHopperClick(session, &openCopy, slot, button, mode)
	case CrafterMenuType:
		bx, by, bz := tr.open.BlockX, tr.open.BlockY, tr.open.BlockZ
		s.applyCrafterClickLocked(session, tr, bx, by, bz, slot, button, mode)
		tr.mu.Unlock()
		s.TriggerInventoryChanged(session)
		s.UnlockRecipesForInventory(session)
		return s.SyncCrafterViewers(session, bx, by, bz)
	case MenuTypeGeneric3x3:
		bx, by, bz := tr.open.BlockX, tr.open.BlockY, tr.open.BlockZ
		s.ApplyDispenserOrDropperClickLocked(session, tr, bx, by, bz, int(slot), button, mode)
		tr.mu.Unlock()
		s.TriggerInventoryChanged(session)
		s.UnlockRecipesForInventory(session)
		return s.SyncDispenserViewers(session, bx, by, bz)
	case MenuTypeCrafting:
		s.applyCraftingTableClickLocked(session, tr, slot, button, mode)
		tr.mu.Unlock()
		s.TriggerInventoryChanged(session)
		s.UnlockRecipesForInventory(session)
		return s.syncOpenContainer(session)
	case MenuTypeFurnace, MenuTypeBlastFurnace, MenuTypeSmoker:
		menuType := tr.open.MenuType
		bx, by, bz := tr.open.BlockX, tr.open.BlockY, tr.open.BlockZ
		prevOut := s.FurnaceSlots(bx, by, bz)[world.FurnaceSlotOutput]
		s.applyFurnaceClickLocked(session, tr, menuType, bx, by, bz, slot, button, mode)
		tr.mu.Unlock()
		if slot == world.FurnaceSlotOutput && !prevOut.IsEmpty() {
			newOut := s.FurnaceSlots(bx, by, bz)[world.FurnaceSlotOutput]
			if taken := prevOut.Count - newOut.Count; taken > 0 {
				s.RecordItemCrafted(session, prevOut.ResolvedID(), taken)
			}
		}
		s.TriggerInventoryChanged(session)
		s.UnlockRecipesForInventory(session)
		return s.syncFurnaceViewers(session, bx, by, bz)
	case MenuTypeEnchantment:
		s.applyEnchantingTableClickLocked(session, tr, slot, button, mode)
		tr.mu.Unlock()
		s.TriggerInventoryChanged(session)
		s.UnlockRecipesForInventory(session)
		return s.syncOpenContainer(session)
	case MenuTypeAnvil:
		bx, by, bz := tr.open.BlockX, tr.open.BlockY, tr.open.BlockZ
		tookResult, costPaid, _ := s.applyAnvilClickLocked(session, tr, slot, button, mode)
		tr.mu.Unlock()
		if costPaid > 0 {
			_ = s.AddPlayerExperienceLevels(session, -costPaid)
		}
		if tookResult {
			s.TriggerInventoryChanged(session)
			s.UnlockRecipesForInventory(session)
			if s.DamageAnvilBlockOnUse(session, bx, by, bz, nil) {
				return nil
			}
		} else {
			s.TriggerInventoryChanged(session)
			s.UnlockRecipesForInventory(session)
		}
		return s.syncOpenContainer(session)
	case MenuTypeGrindstone:
		bx, by, bz := tr.open.BlockX, tr.open.BlockY, tr.open.BlockZ
		tookResult, xpAward := s.applyGrindstoneClickLocked(session, tr, slot, button, mode)
		tr.mu.Unlock()
		if tookResult && xpAward > 0 {
			dim := world.NormalizeDimension(session.Snapshot().Dimension)
			s.SpawnExperienceOrbsInDimension(dim, float64(bx)+0.5, float64(by)+0.5, float64(bz)+0.5, xpAward)
			_ = s.AddPlayerExperience(session, xpAward)
		}
		s.TriggerInventoryChanged(session)
		s.UnlockRecipesForInventory(session)
		return s.syncOpenContainer(session)
	case MenuTypeBrewingStand:
		bx, by, bz := tr.open.BlockX, tr.open.BlockY, tr.open.BlockZ
		s.applyBrewingStandClickLocked(session, tr, bx, by, bz, slot, button, mode)
		tr.mu.Unlock()
		s.TriggerInventoryChanged(session)
		s.UnlockRecipesForInventory(session)
		return s.syncBrewingStandViewers(session, bx, by, bz)
	case MenuTypeCartographyTable:
		tr.mu.Unlock()
		if err := s.HandleCartographyContainerClick(session, containerID, slot, button, mode); err != nil {
			return err
		}
		s.TriggerInventoryChanged(session)
		s.UnlockRecipesForInventory(session)
		return nil
	case MenuTypeLoom:
		tr.mu.Unlock()
		err := s.HandleLoomClick(session, ContainerClick{
			ContainerID: containerID,
			Slot:        int16(slot),
			Button:      button,
			Mode:        mode,
		})
		s.TriggerInventoryChanged(session)
		s.UnlockRecipesForInventory(session)
		return err
	case MenuTypeStonecutter:
		tr.mu.Unlock()
		err := s.HandleStonecutterClick(session, ContainerClick{
			ContainerID: containerID,
			Slot:        int16(slot),
			Button:      button,
			Mode:        mode,
		})
		s.TriggerInventoryChanged(session)
		s.UnlockRecipesForInventory(session)
		return err
	case MenuTypeSmithing:
		tr.mu.Unlock()
		err := s.HandleSmithingClick(session, ContainerClick{
			ContainerID: containerID,
			Slot:        int16(slot),
			Button:      button,
			Mode:        mode,
		})
		s.TriggerInventoryChanged(session)
		s.UnlockRecipesForInventory(session)
		return err
	case MenuTypeBeacon:
		tr.mu.Unlock()
		err := s.HandleBeaconClick(session, ContainerClick{
			ContainerID: containerID,
			Slot:        int16(slot),
			Button:      button,
			Mode:        mode,
		})
		s.TriggerInventoryChanged(session)
		s.UnlockRecipesForInventory(session)
		return err
	case MenuTypeHorse:
		equineID := tr.open.VehicleEntityID
		s.applyEquineContainerClickLocked(session, tr, equineID, slot, button, mode)
		tr.mu.Unlock()
		s.TriggerInventoryChanged(session)
		s.UnlockRecipesForInventory(session)
		return s.syncOpenContainer(session)
	case MenuTypeMerchant:
		tr.mu.Unlock()
		err := s.handleMerchantClick(session, containerID, slot, button, mode)
		s.TriggerInventoryChanged(session)
		s.UnlockRecipesForInventory(session)
		return err
	default:
		tr.mu.Unlock()
		return nil
	}
}

func (s *Server) totalVillagerOfferUses(entityID int32) int32 {
	if s == nil || entityID <= 0 {
		return 0
	}
	vs, ok := s.VillagerState(entityID)
	if !ok {
		return 0
	}
	var total int32
	for _, off := range vs.Offers {
		total += off.Uses
	}
	return total
}

func (s *Server) applyChestClickDataLocked(session *PlayerSession, tr *playerContainerTracker, bx, by, bz, slot int, button int8, mode int32) (pendingDrop containerSlotData, mutated bool) {
	if slot < 0 || slot >= ChestMenuSlotCount {
		return containerSlotData{}, false
	}
	openDim := world.DimensionOverworld
	isShulker := false
	if tr.open != nil {
		if tr.open.Dimension != "" {
			openDim = tr.open.Dimension
		}
		isShulker = tr.open.MenuType == MenuTypeShulkerBox
	} else if session != nil && session.Dimension() != "" {
		openDim = session.Dimension()
	}
	if !isShulker {
		s.HydrateChestExplorerMaps(bx, by, bz, "buried_treasure")
	}
	slots := s.buildChestMenuSlotDataInDim(session, openDim, bx, by, bz)
	orig := append([]containerSlotData(nil), slots...)
	carried := getCarriedSlotDataLocked(session, tr)

	switch mode {
	case ContainerInputPickup:
		// ShulkerBoxSlot.mayPlace: prevent placing a shulker box inside a shulker box.
		if isShulker && slot < ChestSlotCount && !carried.IsEmpty() && IsShulkerBoxItemOrBlock(carried.Stack.ResolvedID()) {
			break
		}
		slots[slot], carried = applyPickupSlotData(slots[slot], carried, button, MaxItemStackSize)
		mutated = true
	case ContainerInputQuickMove:
		if slot < ChestSlotCount {
			slots[slot], mutated = moveSlotDataSlice(slots, slots[slot], ChestSlotCount, ChestMenuSlotCount, true, nil)
		} else if !isShulker || !IsShulkerBoxItemOrBlock(slots[slot].Stack.ResolvedID()) {
			slots[slot], mutated = moveSlotDataSlice(slots, slots[slot], 0, ChestSlotCount, false, nil)
		}
	case ContainerInputSwap:
		if button >= 0 && button < 9 {
			hotbarIdx := 54 + int(button)
			if isShulker && slot < ChestSlotCount && !slots[hotbarIdx].IsEmpty() && IsShulkerBoxItemOrBlock(slots[hotbarIdx].Stack.ResolvedID()) {
				break
			}
			slots[slot], slots[hotbarIdx] = slots[hotbarIdx], slots[slot]
			mutated = true
		} else if button == 40 {
			offData := getPlayerSlotData(session, OffHandContainerSlot)
			if isShulker && slot < ChestSlotCount && !offData.IsEmpty() && IsShulkerBoxItemOrBlock(offData.Stack.ResolvedID()) {
				break
			}
			slots[slot], offData = offData, slots[slot]
			setPlayerSlotData(session, OffHandContainerSlot, offData)
			mutated = true
		}
	case ContainerInputClone:
		if session.Snapshot().GameMode == GameModeCreative && carried.IsEmpty() && !slots[slot].IsEmpty() {
			carried = slots[slot].WithCount(slots[slot].MaxStackSize())
			mutated = true
		}
	case ContainerInputThrow:
		if carried.IsEmpty() && !slots[slot].IsEmpty() {
			dropCount := int32(1)
			if button == 1 {
				dropCount = slots[slot].Stack.Count
			}
			pendingDrop = slots[slot].WithCount(dropCount)
			slots[slot] = slots[slot].WithCount(slots[slot].Stack.Count - dropCount)
			mutated = true
		}
	case ContainerInputPickupAll:
		if !carried.IsEmpty() {
			applyPickupAllSlotData(slots, &carried, 0, nil)
			mutated = true
		}
	}

	for i := 0; i < ChestSlotCount; i++ {
		if !slots[i].SameItemSameComponents(orig[i]) || slots[i].Stack.Count != orig[i].Stack.Count {
			s.setStorageSlotData(openDim, bx, by, bz, i, slots[i])
		}
	}
	for i := 0; i < 27; i++ {
		idx := 27 + i
		if !slots[idx].SameItemSameComponents(orig[idx]) || slots[idx].Stack.Count != orig[idx].Stack.Count {
			setPlayerSlotData(session, 9+i, slots[idx])
		}
	}
	for i := 0; i < 9; i++ {
		idx := 54 + i
		if !slots[idx].SameItemSameComponents(orig[idx]) || slots[idx].Stack.Count != orig[idx].Stack.Count {
			setPlayerSlotData(session, 36+i, slots[idx])
		}
	}
	setCarriedSlotDataLocked(session, tr, carried)
	return pendingDrop, mutated
}

func (s *Server) applyChestClickLocked(session *PlayerSession, tr *playerContainerTracker, bx, by, bz, slot int, button int8, mode int32) {
	pendingDrop, _ := s.applyChestClickDataLocked(session, tr, bx, by, bz, slot, button, mode)
	if !pendingDrop.IsEmpty() {
		_, _ = s.SpawnPlayerDroppedItem(session, pendingDrop)
		s.RecordItemDropped(session, pendingDrop.Stack.ResolvedID(), pendingDrop.Stack.Count)
	}
}

// WearableArmorSlotForItem returns the player inventory armor slot index
// (ArmorSlotHead..ArmorSlotFeet = 5..8) where itemID can be worn, or false if
// itemID is not wearable in an armor slot.
func WearableArmorSlotForItem(itemID string) (int, bool) {
	norm := canonicalResourceName(itemID)
	if stats, ok := ArmorItemStats(norm); ok {
		return stats.Slot, true
	}
	switch norm {
	case "minecraft:carved_pumpkin",
		"minecraft:skeleton_skull",
		"minecraft:wither_skeleton_skull",
		"minecraft:player_head",
		"minecraft:zombie_head",
		"minecraft:creeper_head",
		"minecraft:dragon_head",
		"minecraft:piglin_head":
		return ArmorSlotHead, true
	case "minecraft:elytra":
		return ArmorSlotChest, true
	default:
		return -1, false
	}
}

// HasBindingCurseOnSlot reports whether session is in a non-Creative game mode
// and the armor slot (5..8) holds an item enchanted with minecraft:binding_curse.
func HasBindingCurseOnSlot(session *PlayerSession, slot int) bool {
	if session == nil || slot < ArmorSlotHead || slot > ArmorSlotFeet {
		return false
	}
	if session.Snapshot().GameMode == GameModeCreative {
		return false
	}
	if session.InventorySlot(slot).IsEmpty() {
		return false
	}
	return GetSlotItemMeta(session, slot).EnchantmentLevel("minecraft:binding_curse") > 0
}

func applyPickupAllSlice(slots []world.ItemStack, carried *world.ItemStack, minSlot int) {
	if carried == nil || carried.IsEmpty() {
		return
	}
	itemID := carried.ResolvedItemID()
	maxStack := MaxItemStackSize
	if MaxItemDurability(carried.ResolvedID()) > 0 || carried.ResolvedID() == "minecraft:enchanted_book" {
		maxStack = 1
	}
	if carried.Count >= maxStack {
		return
	}
	// Vanilla AbstractContainerMenu.doClick (PICKUP_ALL) performs 2 passes:
	// Pass 0 collects only from partial stacks (st.Count < maxStack);
	// Pass 1 collects from any remaining matching stacks (including full stacks).
	for pass := 0; pass < 2 && carried.Count < maxStack; pass++ {
		for i := minSlot; i < len(slots) && carried.Count < maxStack; i++ {
			st := slots[i]
			if st.IsEmpty() || st.ResolvedItemID() != itemID {
				continue
			}
			if pass == 0 && st.Count >= maxStack {
				continue
			}
			space := maxStack - carried.Count
			take := st.Count
			if take > space {
				take = space
			}
			*carried = world.NewItemStack(itemID, carried.Count+take)
			if st.Count-take <= 0 {
				slots[i] = world.ItemStack{}
			} else {
				slots[i] = world.NewItemStack(itemID, st.Count-take)
			}
		}
	}
}

func getCarriedFireworkStar(session *PlayerSession) (FireworkExplosion, bool) {
	if session == nil {
		return FireworkExplosion{}, false
	}
	st := getFireworkSlotState(session)
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.carriedStar == nil {
		return FireworkExplosion{}, false
	}
	return st.carriedStar.Clone(), true
}

func setCarriedFireworkStar(session *PlayerSession, exp *FireworkExplosion) {
	if session == nil {
		return
	}
	st := getFireworkSlotState(session)
	st.mu.Lock()
	if exp == nil {
		st.carriedStar = nil
	} else {
		cp := exp.Clone()
		st.carriedStar = &cp
	}
	st.mu.Unlock()
}

func getCarriedFireworks(session *PlayerSession) (FireworksComponent, bool) {
	if session == nil {
		return FireworksComponent{}, false
	}
	st := getFireworkSlotState(session)
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.carriedFW == nil {
		return FireworksComponent{}, false
	}
	return st.carriedFW.Clone(), true
}

func setCarriedFireworks(session *PlayerSession, fw *FireworksComponent) {
	if session == nil {
		return
	}
	st := getFireworkSlotState(session)
	st.mu.Lock()
	if fw == nil {
		st.carriedFW = nil
	} else {
		cp := fw.Clone()
		st.carriedFW = &cp
	}
	st.mu.Unlock()
}

func matchCraftingWithFireworks(
	grid []world.ItemStack,
	width, height int,
	gridStars []FireworkExplosion,
) (res world.ItemStack, starExp *FireworkExplosion, fwComp *FireworksComponent, ok bool) {
	if width == 3 && height == 3 && len(grid) >= 9 {
		if potOut, _, matched := MatchDecoratedPotRecipe(grid); matched {
			return potOut, nil, nil, true
		}
		if tippedOut, _, matched := MatchTippedArrowRecipe(grid[:9], ""); matched {
			return tippedOut, nil, nil, true
		}
	}
	if res, _, ok := MatchCraftingGrid(grid, width, height); ok {
		return res, nil, nil, true
	}
	if out, exp, matched := MatchFireworkStarRecipe(grid); matched {
		cp := exp.Clone()
		return out, &cp, nil, true
	}
	if out, exp, matched := MatchFireworkStarFadeRecipe(grid, gridStars); matched {
		cp := exp.Clone()
		return out, &cp, nil, true
	}
	if out, fw, matched := MatchFireworkRocketRecipe(grid, gridStars); matched {
		cp := fw.Clone()
		return out, nil, &cp, true
	}
	return world.ItemStack{}, nil, nil, false
}

func (s *Server) applyCraftingTableClickDataLocked(session *PlayerSession, tr *playerContainerTracker, slot int, button int8, mode int32) (pendingDrop containerSlotData, mutated bool) {
	if slot < 0 || slot >= CraftingMenuSlotCount || tr.open == nil {
		return containerSlotData{}, false
	}
	slots := s.buildCraftingMenuSlotData(session, tr.open)
	orig := append([]containerSlotData(nil), slots...)
	carried := getCarriedSlotDataLocked(session, tr)

	gridStacks := make([]world.ItemStack, CraftingGridSlotCount)
	gridStars := make([]FireworkExplosion, CraftingGridSlotCount)
	for i := 0; i < CraftingGridSlotCount; i++ {
		gridStacks[i] = slots[1+i].Stack
		if slots[1+i].Star != nil {
			gridStars[i] = slots[1+i].Star.Clone()
		} else if len(tr.open.CraftingStars[i].Colors) > 0 || len(tr.open.CraftingStars[i].FadeColors) > 0 {
			gridStars[i] = tr.open.CraftingStars[i].Clone()
		}
	}

	if slot == 0 {
		// Crafting output slot.
		_, matchedRec, _ := MatchCraftingGrid(gridStacks, 3, 3)
		_, tippedPotion, isTippedArrow := MatchTippedArrowRecipe(gridStacks, slots[5].Potion)
		if isTippedArrow && mode == ContainerInputPickup && !carried.IsEmpty() {
			if carried.Stack.ResolvedID() == "minecraft:tipped_arrow" && carried.Potion != "" && carried.Potion != tippedPotion {
				return containerSlotData{}, false
			}
		}
		crafted := s.clickCraftingOutputData(session, slots, 1, 9, 3, 3, 10, 46, gridStars, func(menuSlot int) int {
			return menuSlot - 1
		}, &carried, mode, func(res containerSlotData) containerSlotData {
			if isTippedArrow {
				res.Potion = tippedPotion
			}
			return res
		})
		if !crafted.IsEmpty() {
			mutated = true
			s.RecordItemCrafted(session, crafted.ResolvedID(), crafted.Count)
			s.TriggerRecipeCrafted(session, crafted)
			recName := ""
			if matchedRec != nil {
				recName = matchedRec.Name
			} else if isTippedArrow {
				recName = "minecraft:tipped_arrow"
			}
			s.UnlockRecipeOnCraft(session, recName, crafted)
		}
	} else {
		switch mode {
		case ContainerInputPickup:
			slots[slot], carried = applyPickupSlotData(slots[slot], carried, button, MaxItemStackSize)
			mutated = true
		case ContainerInputQuickMove:
			if slot >= 1 && slot <= 9 {
				slots[slot], mutated = moveSlotDataSlice(slots, slots[slot], 10, 46, false, nil)
			} else if slot >= 10 && slot < 46 {
				var moved bool
				slots[slot], moved = moveSlotDataSlice(slots, slots[slot], 1, 10, false, nil)
				if !moved {
					if slot < 37 {
						slots[slot], moved = moveSlotDataSlice(slots, slots[slot], 37, 46, false, nil)
					} else {
						slots[slot], moved = moveSlotDataSlice(slots, slots[slot], 10, 37, false, nil)
					}
				}
				mutated = moved
			}
		case ContainerInputSwap:
			if button >= 0 && button < 9 {
				hotbarIdx := 37 + int(button)
				slots[slot], slots[hotbarIdx] = slots[hotbarIdx], slots[slot]
				mutated = true
			} else if button == 40 {
				offData := getPlayerSlotData(session, OffHandContainerSlot)
				slots[slot], offData = offData, slots[slot]
				setPlayerSlotData(session, OffHandContainerSlot, offData)
				mutated = true
			}
		case ContainerInputClone:
			if session.Snapshot().GameMode == GameModeCreative && carried.IsEmpty() && !slots[slot].IsEmpty() {
				carried = slots[slot].WithCount(slots[slot].MaxStackSize())
				mutated = true
			}
		case ContainerInputThrow:
			if carried.IsEmpty() && !slots[slot].IsEmpty() {
				dropCount := int32(1)
				if button == 1 {
					dropCount = slots[slot].Stack.Count
				}
				pendingDrop = slots[slot].WithCount(dropCount)
				slots[slot] = slots[slot].WithCount(slots[slot].Stack.Count - dropCount)
				mutated = true
			}
		case ContainerInputPickupAll:
			if !carried.IsEmpty() {
				applyPickupAllSlotData(slots, &carried, 1, nil)
				mutated = true
			}
		}
	}

	// Write back crafting grid (1..9), recompute result (0), and write back changed player inventory slots (10..45).
	newGrid := make([]world.ItemStack, CraftingGridSlotCount)
	newStars := make([]FireworkExplosion, CraftingGridSlotCount)
	for i := 0; i < CraftingGridSlotCount; i++ {
		d := slots[1+i]
		tr.open.CraftingSlots[i] = d.Clone()
		tr.open.Crafting[i] = d.Stack
		newGrid[i] = d.Stack
		if d.IsEmpty() || !IsPotionOrTippedArrowItem(d.Stack) {
			tr.open.CraftingPotions[i] = ""
		} else {
			tr.open.CraftingPotions[i] = d.Potion
		}
		if d.Star != nil && d.Stack.ResolvedID() == FireworkStarItemID {
			cp := d.Star.Clone()
			tr.open.CraftingStars[i] = cp
			newStars[i] = cp
		} else {
			tr.open.CraftingStars[i] = FireworkExplosion{}
		}
	}
	if res, starExp, fwComp, ok := matchCraftingWithFireworks(newGrid, 3, 3, newStars); ok {
		tr.open.Result = res
		if starExp != nil {
			SetSlotFireworkStar(session, 0, *starExp)
			ClearSlotFireworks(session, 0)
		} else if fwComp != nil {
			SetSlotFireworks(session, 0, *fwComp)
			ClearSlotFireworkStar(session, 0)
		} else {
			ClearSlotFireworkStar(session, 0)
			ClearSlotFireworks(session, 0)
		}
	} else {
		tr.open.Result = world.ItemStack{}
		ClearSlotFireworkStar(session, 0)
		ClearSlotFireworks(session, 0)
	}
	for i := 0; i < 27; i++ {
		idx := 10 + i
		if !slots[idx].SameItemSameComponents(orig[idx]) || slots[idx].Stack.Count != orig[idx].Stack.Count {
			setPlayerSlotData(session, 9+i, slots[idx])
		}
	}
	for i := 0; i < 9; i++ {
		idx := 37 + i
		if !slots[idx].SameItemSameComponents(orig[idx]) || slots[idx].Stack.Count != orig[idx].Stack.Count {
			setPlayerSlotData(session, 36+i, slots[idx])
		}
	}
	setCarriedSlotDataLocked(session, tr, carried)
	return pendingDrop, mutated
}

func (s *Server) applyCraftingTableClickLocked(session *PlayerSession, tr *playerContainerTracker, slot int, button int8, mode int32) {
	pendingDrop, _ := s.applyCraftingTableClickDataLocked(session, tr, slot, button, mode)
	if !pendingDrop.IsEmpty() {
		_, _ = s.SpawnPlayerDroppedItem(session, pendingDrop)
		s.RecordItemDropped(session, pendingDrop.Stack.ResolvedID(), pendingDrop.Stack.Count)
	}
}

func (s *Server) applyPlayerInventoryClickDataLocked(session *PlayerSession, tr *playerContainerTracker, slot int, button int8, mode int32) (pendingDrop containerSlotData, mutated bool) {
	if slot < 0 || slot >= world.PlayerInventorySlots {
		return containerSlotData{}, false
	}
	for i := 0; i < 4; i++ {
		if tr.invCraft[i].IsEmpty() {
			tr.invCraft[i] = session.InventorySlot(1 + i)
		}
		if len(tr.invCraftStars[i].Colors) == 0 && len(tr.invCraftStars[i].FadeColors) == 0 {
			if exp, ok := GetSlotFireworkStar(session, 1+i); ok {
				tr.invCraftStars[i] = exp
			}
		}
	}
	if res, _, _, ok := matchCraftingWithFireworks(tr.invCraft[:], 2, 2, tr.invCraftStars[:]); ok {
		tr.invResult = res
	} else {
		tr.invResult = world.ItemStack{}
	}
	slots := s.buildPlayerMenuSlots(session, tr.invCraft, tr.invResult)
	metas := make([]ItemMeta, world.PlayerInventorySlots)
	trims := make([]ArmorTrim, world.PlayerInventorySlots)
	for i := 0; i < world.PlayerInventorySlots; i++ {
		metas[i] = GetSlotItemMeta(session, i)
		if t, ok := GetSlotArmorTrim(session, i); ok {
			trims[i] = t
		}
	}

	if slot == 0 {
		_, matchedRec, _ := MatchCraftingGrid(slots[1:5], 2, 2)
		if crafted := s.clickCraftingOutput(session, slots, 1, 4, 2, 2, 9, 45, tr.invCraftStars[:], func(menuSlot int) int {
			return menuSlot
		}, &tr.carried, mode); !crafted.IsEmpty() {
			mutated = true
			s.RecordItemCrafted(session, crafted.ResolvedID(), crafted.Count)
			s.TriggerRecipeCrafted(session, crafted)
			recName := ""
			if matchedRec != nil {
				recName = matchedRec.Name
			}
			s.UnlockRecipeOnCraft(session, recName, crafted)
		}
	} else {
		isArmorSlot := slot >= ArmorSlotHead && slot <= ArmorSlotFeet
		switch mode {
		case ContainerInputPickup:
			if button == 1 && s.tryApplyBundlePickupClick(session, slot, slots, metas, &tr.carried) {
				mutated = true
				break
			}
			if isArmorSlot {
				if HasBindingCurseOnSlot(session, slot) && !slots[slot].IsEmpty() {
					return containerSlotData{}, false
				}
				if !tr.carried.IsEmpty() {
					if targetSlot, ok := WearableArmorSlotForItem(tr.carried.ResolvedID()); !ok || targetSlot != slot {
						return containerSlotData{}, false
					}
					if !slots[slot].IsEmpty() && tr.carried.Count > 1 {
						return containerSlotData{}, false
					}
				}
			}
			prevSlotEmpty := slots[slot].IsEmpty()
			prevCarriedEmpty := tr.carried.IsEmpty()
			var slotStar *FireworkExplosion
			if exp, ok := GetSlotFireworkStar(session, slot); ok {
				slotStar = &exp
			} else if slot >= 1 && slot <= 4 && (len(tr.invCraftStars[slot-1].Colors) > 0 || len(tr.invCraftStars[slot-1].FadeColors) > 0) {
				cp := tr.invCraftStars[slot-1].Clone()
				slotStar = &cp
			}
			var slotFW *FireworksComponent
			if fw, ok := GetSlotFireworks(session, slot); ok && slots[slot].ResolvedID() == FireworkRocketItemID {
				slotFW = &fw
			}
			var cStar *FireworkExplosion
			if exp, ok := getCarriedFireworkStar(session); ok {
				cStar = &exp
			}
			var cFW *FireworksComponent
			if fw, ok := getCarriedFireworks(session); ok {
				cFW = &fw
			}
			carriedMeta := GetCarriedItemMeta(session)
			carriedTrim, _ := GetCarriedArmorTrim(session)
			slotBundle := GetSlotBundleContents(session, slot)
			carriedBundle := GetCarriedBundleContents(session)
			slotMapID, hasSlotMap := GetSlotMapID(session, slot)
			carriedMapID, hasCarriedMap := GetCarriedMapID(session)
			slotHorn := GetSlotGoatHornInstrument(session, slot)
			carriedHorn := GetCarriedGoatHornInstrument(session)
			slotPotion := GetSlotPotionType(session, slot)
			carriedPotion := getCarriedPotionTypeForStack(session, tr.carried)
			slotWritable, hasSlotWritable := GetSlotWritableBook(session, slot)
			carriedWritable, hasCarriedWritable := GetCarriedWritableBook(session)
			slotWritten, hasSlotWritten := GetSlotWrittenBook(session, slot)
			carriedWritten, hasCarriedWritten := GetCarriedWrittenBook(session)

			if isArmorSlot && slots[slot].IsEmpty() && !tr.carried.IsEmpty() && tr.carried.Count > 1 {
				slots[slot] = world.NewItemStack(tr.carried.ResolvedItemID(), 1)
				metas[slot] = carriedMeta.Clone()
				trims[slot] = carriedTrim
				tr.carried = world.NewItemStack(tr.carried.ResolvedItemID(), tr.carried.Count-1)
			} else {
				slotTrim := trims[slot]
				slots[slot], metas[slot], tr.carried, carriedMeta = applyPickupSlotWithMeta(slots[slot], metas[slot], tr.carried, carriedMeta, button)
				switch {
				case prevSlotEmpty && !slots[slot].IsEmpty():
					trims[slot] = carriedTrim
					SetSlotBundleContents(session, slot, carriedBundle)
					SetSlotGoatHornInstrument(session, slot, carriedHorn)
					SetSlotPotionType(session, slot, carriedPotion)
					if hasCarriedMap {
						SetSlotMapID(session, slot, carriedMapID)
					} else {
						ClearSlotMapID(session, slot)
					}
					if hasCarriedWritable {
						SetSlotWritableBook(session, slot, carriedWritable)
					} else {
						ClearSlotWritableBook(session, slot)
					}
					if hasCarriedWritten {
						SetSlotWrittenBook(session, slot, carriedWritten)
					} else {
						ClearSlotWrittenBook(session, slot)
					}
					if tr.carried.IsEmpty() {
						carriedTrim = ArmorTrim{}
						SetCarriedBundleContents(session, nil)
						SetCarriedGoatHornInstrument(session, "")
						SetCarriedPotionType(session, "")
						SetCarriedMapID(session, 0, false)
						SetCarriedWritableBook(session, WritableBookContent{}, false)
						SetCarriedWrittenBook(session, WrittenBookContent{}, false)
					}
				case prevCarriedEmpty && !tr.carried.IsEmpty():
					carriedTrim = slotTrim
					SetCarriedBundleContents(session, slotBundle)
					SetCarriedGoatHornInstrument(session, slotHorn)
					SetCarriedPotionType(session, slotPotion)
					SetCarriedMapID(session, slotMapID, hasSlotMap)
					SetCarriedWritableBook(session, slotWritable, hasSlotWritable)
					SetCarriedWrittenBook(session, slotWritten, hasSlotWritten)
					if slots[slot].IsEmpty() {
						trims[slot] = ArmorTrim{}
						ClearSlotBundleContents(session, slot)
						ClearSlotGoatHornInstrument(session, slot)
						SetSlotPotionType(session, slot, "")
						ClearSlotMapID(session, slot)
						ClearSlotWritableBook(session, slot)
						ClearSlotWrittenBook(session, slot)
					}
				case !slots[slot].IsEmpty() && !tr.carried.IsEmpty() && slots[slot].ResolvedItemID() != tr.carried.ResolvedItemID():
					trims[slot], carriedTrim = carriedTrim, slotTrim
					SetSlotBundleContents(session, slot, carriedBundle)
					SetCarriedBundleContents(session, slotBundle)
					SetSlotGoatHornInstrument(session, slot, carriedHorn)
					SetCarriedGoatHornInstrument(session, slotHorn)
					SetSlotPotionType(session, slot, carriedPotion)
					SetCarriedPotionType(session, slotPotion)
					if hasCarriedMap {
						SetSlotMapID(session, slot, carriedMapID)
					} else {
						ClearSlotMapID(session, slot)
					}
					SetCarriedMapID(session, slotMapID, hasSlotMap)
					if hasCarriedWritable {
						SetSlotWritableBook(session, slot, carriedWritable)
					} else {
						ClearSlotWritableBook(session, slot)
					}
					SetCarriedWritableBook(session, slotWritable, hasSlotWritable)
					if hasCarriedWritten {
						SetSlotWrittenBook(session, slot, carriedWritten)
					} else {
						ClearSlotWrittenBook(session, slot)
					}
					SetCarriedWrittenBook(session, slotWritten, hasSlotWritten)
				}
				SetCarriedItemMeta(session, carriedMeta)
				SetCarriedArmorTrim(session, carriedTrim)
			}

			var newSlotStar *FireworkExplosion
			var newSlotFW *FireworksComponent
			var newCStar *FireworkExplosion
			var newCFW *FireworksComponent
			switch {
			case prevSlotEmpty && !slots[slot].IsEmpty():
				newSlotStar, newSlotFW = cStar, cFW
				if !tr.carried.IsEmpty() {
					newCStar, newCFW = cStar, cFW
				}
			case prevCarriedEmpty && !tr.carried.IsEmpty():
				newCStar, newCFW = slotStar, slotFW
				if !slots[slot].IsEmpty() {
					newSlotStar, newSlotFW = slotStar, slotFW
				}
			case !slots[slot].IsEmpty() && !tr.carried.IsEmpty():
				if slots[slot].ResolvedItemID() == tr.carried.ResolvedItemID() {
					newSlotStar, newSlotFW = slotStar, slotFW
					newCStar, newCFW = cStar, cFW
				} else {
					newSlotStar, newSlotFW = cStar, cFW
					newCStar, newCFW = slotStar, slotFW
				}
			}
			if newSlotStar != nil && slots[slot].ResolvedID() == FireworkStarItemID {
				SetSlotFireworkStar(session, slot, *newSlotStar)
				if slot >= 1 && slot <= 4 {
					tr.invCraftStars[slot-1] = newSlotStar.Clone()
				}
			} else if slots[slot].IsEmpty() || slots[slot].ResolvedID() != FireworkStarItemID {
				ClearSlotFireworkStar(session, slot)
				if slot >= 1 && slot <= 4 {
					tr.invCraftStars[slot-1] = FireworkExplosion{}
				}
			}
			if newSlotFW != nil && slots[slot].ResolvedID() == FireworkRocketItemID {
				SetSlotFireworks(session, slot, *newSlotFW)
			} else if slots[slot].IsEmpty() || slots[slot].ResolvedID() != FireworkRocketItemID {
				ClearSlotFireworks(session, slot)
			}
			setCarriedFireworkStar(session, newCStar)
			setCarriedFireworks(session, newCFW)

		case ContainerInputQuickMove:
			mutated = true
			if slot >= 1 && slot <= 4 {
				slots[slot], metas[slot] = mergeIntoSlotSliceWithMeta(slots, metas, slots[slot], metas[slot], 36, 45)
				slots[slot], metas[slot] = mergeIntoSlotSliceWithMeta(slots, metas, slots[slot], metas[slot], 9, 36)
			} else if isArmorSlot {
				if HasBindingCurseOnSlot(session, slot) {
					return containerSlotData{}, false
				}
				movedTrim := trims[slot]
				slots[slot], metas[slot] = mergeIntoSlotSliceWithMeta(slots, metas, slots[slot], metas[slot], 9, 36)
				slots[slot], metas[slot] = mergeIntoSlotSliceWithMeta(slots, metas, slots[slot], metas[slot], 36, 45)
				if slots[slot].IsEmpty() {
					trims[slot] = ArmorTrim{}
					if movedTrim.IsValid() {
						for dst := 9; dst < 45; dst++ {
							if !slots[dst].IsEmpty() && !trims[dst].IsValid() {
								trims[dst] = movedTrim
								break
							}
						}
					}
				}
			} else if slot == OffHandContainerSlot {
				slots[slot], metas[slot] = mergeIntoSlotSliceWithMeta(slots, metas, slots[slot], metas[slot], 9, 36)
				slots[slot], metas[slot] = mergeIntoSlotSliceWithMeta(slots, metas, slots[slot], metas[slot], 36, 45)
			} else if slot >= 9 && slot < 45 && !slots[slot].IsEmpty() {
				if targetArmor, ok := WearableArmorSlotForItem(slots[slot].ResolvedID()); ok && slots[targetArmor].IsEmpty() {
					slots[targetArmor] = world.NewItemStack(slots[slot].ResolvedItemID(), 1)
					metas[targetArmor] = metas[slot].Clone()
					trims[targetArmor] = trims[slot]
					if slots[slot].Count <= 1 {
						slots[slot] = world.ItemStack{}
						metas[slot] = ItemMeta{}
						trims[slot] = ArmorTrim{}
					} else {
						slots[slot] = world.NewItemStack(slots[slot].ResolvedItemID(), slots[slot].Count-1)
					}
				} else if canonicalResourceName(slots[slot].ResolvedID()) == "minecraft:shield" && slots[OffHandContainerSlot].IsEmpty() {
					slots[OffHandContainerSlot] = world.NewItemStack(slots[slot].ResolvedItemID(), 1)
					metas[OffHandContainerSlot] = metas[slot].Clone()
					if slots[slot].Count <= 1 {
						slots[slot] = world.ItemStack{}
						metas[slot] = ItemMeta{}
					} else {
						slots[slot] = world.NewItemStack(slots[slot].ResolvedItemID(), slots[slot].Count-1)
					}
				} else if slot >= 9 && slot < 36 {
					slots[slot], metas[slot] = mergeIntoSlotSliceWithMeta(slots, metas, slots[slot], metas[slot], 36, 45)
				} else if slot >= 36 && slot < 45 {
					slots[slot], metas[slot] = mergeIntoSlotSliceWithMeta(slots, metas, slots[slot], metas[slot], 9, 36)
				}
			}
		case ContainerInputSwap:
			targetIdx := -1
			if button >= 0 && button < 9 {
				targetIdx = 36 + int(button)
			} else if button == 40 {
				targetIdx = OffHandContainerSlot
			}
			if targetIdx >= 0 && targetIdx < world.PlayerInventorySlots {
				if isArmorSlot {
					if HasBindingCurseOnSlot(session, slot) {
						return containerSlotData{}, false
					}
					if !slots[targetIdx].IsEmpty() {
						if ws, ok := WearableArmorSlotForItem(slots[targetIdx].ResolvedID()); !ok || ws != slot || slots[targetIdx].Count > 1 {
							return containerSlotData{}, false
						}
					}
				}
				slots[slot], slots[targetIdx] = slots[targetIdx], slots[slot]
				metas[slot], metas[targetIdx] = metas[targetIdx], metas[slot]
				trims[slot], trims[targetIdx] = trims[targetIdx], trims[slot]
				swapPlayerSlotExtendedComponents(session, slot, targetIdx)
				mutated = true
			}
		case ContainerInputClone:
			if session.Snapshot().GameMode == GameModeCreative && tr.carried.IsEmpty() && !slots[slot].IsEmpty() {
				maxStack := MaxItemStackSize
				if IsBundleItem(slots[slot].ResolvedID()) || MaxItemDurability(slots[slot].ResolvedID()) > 0 {
					maxStack = 1
				}
				tr.carried = world.NewItemStack(slots[slot].ResolvedItemID(), maxStack)
				SetCarriedItemMeta(session, metas[slot].Clone())
				SetCarriedArmorTrim(session, trims[slot])
				SetCarriedPotionType(session, GetSlotPotionType(session, slot))
				if mapID, ok := GetSlotMapID(session, slot); ok {
					SetCarriedMapID(session, mapID, true)
				}
				if wb, ok := GetSlotWritableBook(session, slot); ok {
					SetCarriedWritableBook(session, wb, true)
				}
				if wrb, ok := GetSlotWrittenBook(session, slot); ok {
					SetCarriedWrittenBook(session, wrb, true)
				}
				SetCarriedGoatHornInstrument(session, GetSlotGoatHornInstrument(session, slot))
				SetCarriedBundleContents(session, GetSlotBundleContents(session, slot))
			}
		case ContainerInputThrow:
			if isArmorSlot && HasBindingCurseOnSlot(session, slot) {
				return containerSlotData{}, false
			}
			if tr.carried.IsEmpty() && !slots[slot].IsEmpty() {
				dropCount := int32(1)
				if button == 1 {
					dropCount = slots[slot].Count
				}
				fullSlot := getPlayerSlotData(session, slot)
				fullSlot.Stack = slots[slot]
				fullSlot.Meta = metas[slot].Clone()
				fullSlot.Trim = trims[slot]
				pendingDrop = fullSlot.WithCount(dropCount)
				rem := slots[slot].Count - dropCount
				if rem <= 0 {
					slots[slot] = world.ItemStack{}
					metas[slot] = ItemMeta{}
					trims[slot] = ArmorTrim{}
					setPlayerSlotData(session, slot, containerSlotData{})
				} else {
					slots[slot] = world.NewItemStack(slots[slot].ResolvedItemID(), rem)
				}
				mutated = true
			}
		case ContainerInputPickupAll:
			if !tr.carried.IsEmpty() {
				carriedData := getCarriedSlotDataLocked(session, tr)
				maxStack := carriedData.MaxStackSize()
				for pass := 0; pass < 2 && tr.carried.Count < maxStack; pass++ {
					for i := 1; i < world.PlayerInventorySlots && tr.carried.Count < maxStack; i++ {
						if i >= ArmorSlotHead && i <= ArmorSlotFeet && HasBindingCurseOnSlot(session, i) {
							continue
						}
						st := slots[i]
						if st.IsEmpty() {
							continue
						}
						sd := getPlayerSlotData(session, i)
						sd.Stack = st
						sd.Meta = metas[i]
						sd.Trim = trims[i]
						if !sd.SameItemSameComponents(carriedData) {
							continue
						}
						if pass == 0 && st.Count >= maxStack {
							continue
						}
						space := maxStack - tr.carried.Count
						take := st.Count
						if take > space {
							take = space
						}
						tr.carried = world.NewItemStack(carriedData.Stack.ResolvedItemID(), tr.carried.Count+take)
						carriedData = carriedData.WithCount(tr.carried.Count)
						if st.Count-take <= 0 {
							slots[i] = world.ItemStack{}
							metas[i] = ItemMeta{}
							trims[i] = ArmorTrim{}
							setPlayerSlotData(session, i, containerSlotData{})
						} else {
							slots[i] = world.NewItemStack(st.ResolvedItemID(), st.Count-take)
						}
					}
				}
				mutated = true
			}
		}
	}
	if mode == ContainerInputPickup || mode == ContainerInputClone {
		mutated = true
	}

	for i := 0; i < 4; i++ {
		tr.invCraft[i] = slots[1+i]
		if tr.invCraft[i].IsEmpty() {
			tr.invCraftStars[i] = FireworkExplosion{}
			ClearSlotFireworkStar(session, 1+i)
		} else if len(tr.invCraftStars[i].Colors) == 0 && len(tr.invCraftStars[i].FadeColors) == 0 {
			if exp, ok := GetSlotFireworkStar(session, 1+i); ok {
				tr.invCraftStars[i] = exp
			}
		}
	}
	if res, starExp, fwComp, ok := matchCraftingWithFireworks(tr.invCraft[:], 2, 2, tr.invCraftStars[:]); ok {
		tr.invResult = res
		if starExp != nil {
			SetSlotFireworkStar(session, 0, *starExp)
			ClearSlotFireworks(session, 0)
		} else if fwComp != nil {
			SetSlotFireworks(session, 0, *fwComp)
			ClearSlotFireworkStar(session, 0)
		} else {
			ClearSlotFireworkStar(session, 0)
			ClearSlotFireworks(session, 0)
		}
	} else {
		tr.invResult = world.ItemStack{}
		ClearSlotFireworkStar(session, 0)
		ClearSlotFireworks(session, 0)
	}
	slots[0] = tr.invResult
	for i := 0; i < world.PlayerInventorySlots; i++ {
		session.SetInventorySlot(i, slots[i])
		if slots[i].IsEmpty() {
			SetSlotItemMeta(session, i, ItemMeta{})
			ClearSlotArmorTrim(session, i)
		} else {
			SetSlotItemMeta(session, i, metas[i])
			if trims[i].IsValid() {
				SetSlotArmorTrim(session, i, trims[i])
			} else {
				ClearSlotArmorTrim(session, i)
			}
		}
	}
	return pendingDrop, mutated
}

func (s *Server) applyPlayerInventoryClickLocked(session *PlayerSession, tr *playerContainerTracker, slot int, button int8, mode int32) {
	pendingDrop, _ := s.applyPlayerInventoryClickDataLocked(session, tr, slot, button, mode)
	if !pendingDrop.IsEmpty() {
		_, _ = s.SpawnPlayerDroppedItem(session, pendingDrop)
		s.RecordItemDropped(session, pendingDrop.Stack.ResolvedID(), pendingDrop.Stack.Count)
	}
}

// CraftingRemainderItem returns the vanilla 26.1.2 Items.craftRemainder item (count 1)
// left behind when one unit of itemID is consumed in a crafting grid.
func CraftingRemainderItem(itemID string) (world.ItemStack, bool) {
	switch canonicalResourceName(itemID) {
	case "minecraft:water_bucket", "minecraft:lava_bucket", "minecraft:milk_bucket":
		st := world.NewNamedItemStack("minecraft:bucket", 1)
		return st, !st.IsEmpty()
	case "minecraft:dragon_breath", "minecraft:honey_bottle":
		st := world.NewNamedItemStack("minecraft:glass_bottle", 1)
		return st, !st.IsEmpty()
	default:
		return world.ItemStack{}, false
	}
}

func (s *Server) clickCraftingOutputData(
	session *PlayerSession,
	slots []containerSlotData,
	gridStart, gridCount, width, height int,
	invStart, invEnd int,
	gridStars []FireworkExplosion,
	menuSlotToSessionSlot func(menuSlot int) int,
	carried *containerSlotData,
	mode int32,
	resultDecorator func(res containerSlotData) containerSlotData,
) world.ItemStack {
	grid := make([]world.ItemStack, gridCount)
	for i := 0; i < gridCount; i++ {
		grid[i] = slots[gridStart+i].Stack
	}
	var potSherds *[4]string
	if width == 3 && height == 3 && len(grid) >= 9 {
		if _, sh, matchedPot := MatchDecoratedPotRecipe(grid); matchedPot {
			shCopy := sh
			potSherds = &shCopy
		}
	}
	res, starExp, fwComp, ok := matchCraftingWithFireworks(grid, width, height, gridStars)
	if !ok || res.IsEmpty() {
		return world.ItemStack{}
	}
	resID := res.ResolvedItemID()
	if resID <= 0 {
		return world.ItemStack{}
	}

	buildResultData := func(curRes world.ItemStack, curStar *FireworkExplosion, curFW *FireworksComponent) containerSlotData {
		d := containerSlotData{Stack: curRes}
		if curStar != nil {
			cp := curStar.Clone()
			d.Star = &cp
		} else if curFW != nil {
			cp := curFW.Clone()
			d.Fireworks = &cp
		}
		if resultDecorator != nil {
			d = resultDecorator(d)
		}
		return d
	}

	consumeGridOnce := func() {
		for i := 0; i < gridCount; i++ {
			cur := slots[gridStart+i]
			if cur.IsEmpty() {
				continue
			}
			remStack, hasRem := CraftingRemainderItem(cur.Stack.ResolvedID())
			next := cur.WithCount(cur.Stack.Count - 1)
			slots[gridStart+i] = next
			if hasRem && !remStack.IsEmpty() {
				remData := containerSlotData{Stack: remStack}
				switch {
				case next.IsEmpty():
					slots[gridStart+i] = remData
				case next.SameItemSameComponents(remData) && next.Stack.Count+remStack.Count <= next.MaxStackSize():
					slots[gridStart+i] = next.WithCount(next.Stack.Count + remStack.Count)
				default:
					leftover, _ := moveSlotDataSlice(slots, remData, invStart, invEnd, false, nil)
					if !leftover.IsEmpty() && s != nil && session != nil {
						snap := session.Snapshot()
						_, _ = s.SpawnItemEntityInDimension(world.NormalizeDimension(snap.Dimension), snap.X, snap.Y+0.25, snap.Z, leftover.Stack)
					}
				}
			}
		}
	}

	var crafted world.ItemStack
	switch mode {
	case ContainerInputPickup:
		resData := buildResultData(res, starExp, fwComp)
		resMax := resData.MaxStackSize()
		if carried.IsEmpty() {
			*carried = resData
			consumeGridOnce()
			crafted = res
			if potSherds != nil {
				s.SetLastCraftedDecoratedPotSherds(*potSherds)
			}
		} else if carried.SameItemSameComponents(resData) && carried.Stack.Count+res.Count <= resMax {
			*carried = carried.WithCount(carried.Stack.Count + res.Count)
			consumeGridOnce()
			crafted = res
			if potSherds != nil {
				s.SetLastCraftedDecoratedPotSherds(*potSherds)
			}
		}
	case ContainerInputQuickMove:
		var totalCrafted int32
		curGrid := make([]world.ItemStack, gridCount)
		for iter := 0; iter < 64; iter++ {
			for i := 0; i < gridCount; i++ {
				curGrid[i] = slots[gridStart+i].Stack
			}
			var curPotSherds *[4]string
			if width == 3 && height == 3 && gridCount >= 9 {
				if _, sh, matchedPot := MatchDecoratedPotRecipe(curGrid); matchedPot {
					shCopy := sh
					curPotSherds = &shCopy
				}
			}
			curRes, curStarExp, curFwComp, match := matchCraftingWithFireworks(curGrid, width, height, gridStars)
			if !match || curRes.IsEmpty() || curRes.ResolvedItemID() != resID {
				break
			}
			resData := buildResultData(curRes, curStarExp, curFwComp)
			if !canFitSlotDataInSlice(slots, resData, invStart, invEnd) {
				break
			}
			prevCounts := make([]int32, invEnd)
			for idx := invStart; idx < invEnd; idx++ {
				prevCounts[idx] = slots[idx].Stack.Count
			}
			rem, _ := moveSlotDataSlice(slots, resData, invStart, invEnd, true, nil)
			if curPotSherds != nil {
				s.SetLastCraftedDecoratedPotSherds(*curPotSherds)
				if menuSlotToSessionSlot != nil {
					for idx := invStart; idx < invEnd; idx++ {
						if slots[idx].Stack.Count > prevCounts[idx] {
							SetSlotDecoratedPotSherds(s, session, menuSlotToSessionSlot(idx), *curPotSherds)
						}
					}
				}
			}
			consumeGridOnce()
			totalCrafted += curRes.Count
			if !rem.IsEmpty() {
				break
			}
		}
		if totalCrafted > 0 {
			crafted = world.NewItemStack(resID, totalCrafted)
		}
	}
	return crafted
}

func (s *Server) clickCraftingOutput(
	session *PlayerSession,
	slots []world.ItemStack,
	gridStart, gridCount, width, height int,
	invStart, invEnd int,
	gridStars []FireworkExplosion,
	menuSlotToSessionSlot func(menuSlot int) int,
	carried *world.ItemStack,
	mode int32,
) world.ItemStack {
	grid := slots[gridStart : gridStart+gridCount]
	var potSherds *[4]string
	if width == 3 && height == 3 && len(grid) >= 9 {
		if _, sh, matchedPot := MatchDecoratedPotRecipe(grid); matchedPot {
			shCopy := sh
			potSherds = &shCopy
		}
	}
	res, starExp, fwComp, ok := matchCraftingWithFireworks(grid, width, height, gridStars)
	if !ok || res.IsEmpty() {
		return world.ItemStack{}
	}
	resID := res.ResolvedItemID()
	if resID <= 0 {
		return world.ItemStack{}
	}

	consumeGridOnce := func() {
		for i := 0; i < gridCount; i++ {
			cur := slots[gridStart+i]
			if cur.IsEmpty() {
				continue
			}
			remStack, hasRem := CraftingRemainderItem(cur.ResolvedID())
			if cur.Count <= 1 {
				slots[gridStart+i] = world.ItemStack{}
			} else {
				slots[gridStart+i] = world.NewItemStack(cur.ResolvedItemID(), cur.Count-1)
			}
			if hasRem && !remStack.IsEmpty() {
				remMax := MaxStackSizeForBundleWeight(remStack.ResolvedID())
				after := slots[gridStart+i]
				switch {
				case after.IsEmpty():
					slots[gridStart+i] = remStack
				case after.ResolvedItemID() == remStack.ResolvedItemID() && after.Count+remStack.Count <= remMax:
					slots[gridStart+i] = world.NewItemStack(remStack.ResolvedItemID(), after.Count+remStack.Count)
				default:
					leftover := mergeIntoSlotSlice(slots, remStack, invStart, invEnd)
					if !leftover.IsEmpty() && s != nil && session != nil {
						snap := session.Snapshot()
						_, _ = s.SpawnItemEntityInDimension(world.NormalizeDimension(snap.Dimension), snap.X, snap.Y+0.25, snap.Z, leftover)
					}
				}
			}
		}
	}

	var crafted world.ItemStack
	switch mode {
	case ContainerInputPickup:
		resMax := (containerSlotData{Stack: res}).MaxStackSize()
		if carried.IsEmpty() {
			*carried = world.NewItemStack(resID, res.Count)
			consumeGridOnce()
			crafted = res
			if potSherds != nil {
				s.SetLastCraftedDecoratedPotSherds(*potSherds)
			}
			if starExp != nil {
				setCarriedFireworkStar(session, starExp)
				setCarriedFireworks(session, nil)
			} else if fwComp != nil {
				setCarriedFireworks(session, fwComp)
				setCarriedFireworkStar(session, nil)
			}
		} else if carried.ResolvedItemID() == resID && carried.Count+res.Count <= resMax {
			*carried = world.NewItemStack(resID, carried.Count+res.Count)
			consumeGridOnce()
			crafted = res
			if potSherds != nil {
				s.SetLastCraftedDecoratedPotSherds(*potSherds)
			}
			if starExp != nil {
				setCarriedFireworkStar(session, starExp)
				setCarriedFireworks(session, nil)
			} else if fwComp != nil {
				setCarriedFireworks(session, fwComp)
				setCarriedFireworkStar(session, nil)
			}
		}
	case ContainerInputQuickMove:
		var totalCrafted int32
		for iter := 0; iter < 64; iter++ {
			var curPotSherds *[4]string
			if width == 3 && height == 3 && gridCount >= 9 {
				if _, sh, matchedPot := MatchDecoratedPotRecipe(slots[gridStart : gridStart+gridCount]); matchedPot {
					shCopy := sh
					curPotSherds = &shCopy
				}
			}
			curRes, curStarExp, curFwComp, match := matchCraftingWithFireworks(slots[gridStart:gridStart+gridCount], width, height, gridStars)
			if !match || curRes.IsEmpty() || curRes.ResolvedItemID() != resID {
				break
			}
			if !canFitInSlice(slots, curRes, invStart, invEnd) {
				break
			}
			prevCounts := make([]int32, invEnd)
			for idx := invStart; idx < invEnd; idx++ {
				prevCounts[idx] = slots[idx].Count
			}
			// Match vanilla AbstractContainerMenu.moveItemStackTo(res, invStart, invEnd, true): fill right-to-left (hotbar 8..0 then main).
			rem := curRes
			itemMax := MaxStackSizeForBundleWeight(curRes.ResolvedID())
			if curRes.Count <= itemMax && itemMax > 1 {
				for idx := invEnd - 1; idx >= invStart && !rem.IsEmpty(); idx-- {
					cur := slots[idx]
					if cur.IsEmpty() || cur.ResolvedItemID() != resID || cur.Count >= itemMax {
						continue
					}
					space := itemMax - cur.Count
					take := rem.Count
					if take > space {
						take = space
					}
					slots[idx] = world.NewItemStack(resID, cur.Count+take)
					if rem.Count-take <= 0 {
						rem = world.ItemStack{}
					} else {
						rem = world.NewItemStack(resID, rem.Count-take)
					}
				}
			}
			for idx := invEnd - 1; idx >= invStart && !rem.IsEmpty(); idx-- {
				if !slots[idx].IsEmpty() {
					continue
				}
				take := rem.Count
				if take > itemMax {
					take = itemMax
				}
				slots[idx] = world.NewItemStack(resID, take)
				if rem.Count-take <= 0 {
					rem = world.ItemStack{}
				} else {
					rem = world.NewItemStack(resID, rem.Count-take)
				}
			}
			if curPotSherds != nil {
				s.SetLastCraftedDecoratedPotSherds(*curPotSherds)
			}
			if menuSlotToSessionSlot != nil && (curStarExp != nil || curFwComp != nil || curPotSherds != nil) {
				for idx := invStart; idx < invEnd; idx++ {
					if slots[idx].Count > prevCounts[idx] {
						dstSlot := menuSlotToSessionSlot(idx)
						if curStarExp != nil {
							SetSlotFireworkStar(session, dstSlot, *curStarExp)
						}
						if curFwComp != nil {
							SetSlotFireworks(session, dstSlot, *curFwComp)
						}
						if curPotSherds != nil {
							SetSlotDecoratedPotSherds(s, session, dstSlot, *curPotSherds)
						}
					}
				}
			}
			consumeGridOnce()
			totalCrafted += curRes.Count
			if !rem.IsEmpty() {
				break
			}
		}
		if totalCrafted > 0 {
			crafted = world.NewItemStack(resID, totalCrafted)
		}
	}
	return crafted
}

func applyPickupSlot(slotStack, carried world.ItemStack, button int8) (newSlot, newCarried world.ItemStack) {
	if button == 1 {
		// Right-click
		if carried.IsEmpty() {
			if slotStack.IsEmpty() {
				return slotStack, carried
			}
			take := (slotStack.Count + 1) / 2
			leave := slotStack.Count - take
			newCarried = world.NewItemStack(slotStack.ResolvedItemID(), take)
			if leave <= 0 {
				newSlot = world.ItemStack{}
			} else {
				newSlot = world.NewItemStack(slotStack.ResolvedItemID(), leave)
			}
			return newSlot, newCarried
		}
		if slotStack.IsEmpty() {
			newSlot = world.NewItemStack(carried.ResolvedItemID(), 1)
			if carried.Count <= 1 {
				newCarried = world.ItemStack{}
			} else {
				newCarried = world.NewItemStack(carried.ResolvedItemID(), carried.Count-1)
			}
			return newSlot, newCarried
		}
		if slotStack.ResolvedItemID() == carried.ResolvedItemID() {
			if slotStack.Count < MaxItemStackSize {
				newSlot = world.NewItemStack(slotStack.ResolvedItemID(), slotStack.Count+1)
				if carried.Count <= 1 {
					newCarried = world.ItemStack{}
				} else {
					newCarried = world.NewItemStack(carried.ResolvedItemID(), carried.Count-1)
				}
				return newSlot, newCarried
			}
			return slotStack, carried
		}
		return carried, slotStack
	}

	// Left-click (button == 0)
	if carried.IsEmpty() {
		return world.ItemStack{}, slotStack
	}
	if slotStack.IsEmpty() {
		return carried, world.ItemStack{}
	}
	if slotStack.ResolvedItemID() == carried.ResolvedItemID() {
		space := MaxItemStackSize - slotStack.Count
		if space <= 0 {
			return slotStack, carried
		}
		move := carried.Count
		if move > space {
			move = space
		}
		newSlot = world.NewItemStack(slotStack.ResolvedItemID(), slotStack.Count+move)
		rem := carried.Count - move
		if rem <= 0 {
			newCarried = world.ItemStack{}
		} else {
			newCarried = world.NewItemStack(carried.ResolvedItemID(), rem)
		}
		return newSlot, newCarried
	}
	return carried, slotStack
}

func mergeIntoSlotSlice(slots []world.ItemStack, stack world.ItemStack, start, end int) world.ItemStack {
	if stack.IsEmpty() || start >= end {
		return stack
	}
	itemID := stack.ResolvedItemID()
	remaining := stack.Count

	for i := start; i < end && remaining > 0; i++ {
		cur := slots[i]
		if cur.IsEmpty() || cur.ResolvedItemID() != itemID || cur.Count >= MaxItemStackSize {
			continue
		}
		space := MaxItemStackSize - cur.Count
		take := remaining
		if take > space {
			take = space
		}
		slots[i] = world.NewItemStack(itemID, cur.Count+take)
		remaining -= take
	}
	for i := start; i < end && remaining > 0; i++ {
		if !slots[i].IsEmpty() {
			continue
		}
		take := remaining
		if take > MaxItemStackSize {
			take = MaxItemStackSize
		}
		slots[i] = world.NewItemStack(itemID, take)
		remaining -= take
	}
	if remaining <= 0 {
		return world.ItemStack{}
	}
	return world.NewItemStack(itemID, remaining)
}

func canFitInSlice(slots []world.ItemStack, stack world.ItemStack, start, end int) bool {
	if stack.IsEmpty() {
		return true
	}
	itemID := stack.ResolvedItemID()
	needed := stack.Count
	for i := start; i < end && needed > 0; i++ {
		cur := slots[i]
		if cur.IsEmpty() {
			needed -= MaxItemStackSize
		} else if cur.ResolvedItemID() == itemID && cur.Count < MaxItemStackSize {
			needed -= MaxItemStackSize - cur.Count
		}
	}
	return needed <= 0
}

func moveSlotDataSlice(slots []containerSlotData, d containerSlotData, start, end int, reverse bool, slotMax func(slot int, item containerSlotData) int32) (containerSlotData, bool) {
	if d.IsEmpty() || start >= end {
		return d, false
	}
	if start < 0 {
		start = 0
	}
	if end > len(slots) {
		end = len(slots)
	}
	if start >= end {
		return d, false
	}
	remaining := d.Stack.Count
	moved := false
	limitFor := func(i int) int32 {
		lim := d.MaxStackSize()
		if slotMax != nil {
			if sm := slotMax(i, d); sm < lim {
				lim = sm
			}
		}
		return lim
	}
	stepMerge := func(i int) bool {
		if remaining <= 0 {
			return false
		}
		cur := slots[i]
		lim := limitFor(i)
		if cur.IsEmpty() || lim <= 1 || cur.Stack.Count >= lim || !cur.SameItemSameComponents(d) {
			return true
		}
		space := lim - cur.Stack.Count
		take := remaining
		if take > space {
			take = space
		}
		if take > 0 {
			slots[i] = cur.WithCount(cur.Stack.Count + take)
			remaining -= take
			moved = true
		}
		return remaining > 0
	}
	if d.MaxStackSize() > 1 {
		if reverse {
			for i := end - 1; i >= start; i-- {
				if !stepMerge(i) {
					break
				}
			}
		} else {
			for i := start; i < end; i++ {
				if !stepMerge(i) {
					break
				}
			}
		}
	}
	stepEmpty := func(i int) bool {
		if remaining <= 0 {
			return false
		}
		if !slots[i].IsEmpty() {
			return true
		}
		lim := limitFor(i)
		if lim <= 0 {
			return true
		}
		take := remaining
		if take > lim {
			take = lim
		}
		if take > 0 {
			slots[i] = d.WithCount(take)
			remaining -= take
			moved = true
		}
		return remaining > 0
	}
	if remaining > 0 {
		if reverse {
			for i := end - 1; i >= start; i-- {
				if !stepEmpty(i) {
					break
				}
			}
		} else {
			for i := start; i < end; i++ {
				if !stepEmpty(i) {
					break
				}
			}
		}
	}
	return d.WithCount(remaining), moved
}

func canFitSlotDataInSlice(slots []containerSlotData, d containerSlotData, start, end int) bool {
	if d.IsEmpty() {
		return true
	}
	needed := d.Stack.Count
	lim := d.MaxStackSize()
	for i := start; i < end && needed > 0; i++ {
		cur := slots[i]
		if cur.IsEmpty() {
			needed -= lim
		} else if cur.SameItemSameComponents(d) && cur.Stack.Count < lim {
			needed -= lim - cur.Stack.Count
		}
	}
	return needed <= 0
}

func applyPickupSlotData(slotData, carried containerSlotData, button int8, slotLimit int32) (newSlot, newCarried containerSlotData) {
	if slotLimit <= 0 {
		slotLimit = MaxItemStackSize
	}
	maxFor := func(d containerSlotData) int32 {
		m := d.MaxStackSize()
		if slotLimit < m {
			m = slotLimit
		}
		if m < 1 {
			m = 1
		}
		return m
	}
	if button == 1 {
		if carried.IsEmpty() {
			if slotData.IsEmpty() {
				return slotData, carried
			}
			take := (slotData.Stack.Count + 1) / 2
			leave := slotData.Stack.Count - take
			return slotData.WithCount(leave), slotData.WithCount(take)
		}
		if slotData.IsEmpty() {
			return carried.WithCount(1), carried.WithCount(carried.Stack.Count - 1)
		}
		if slotData.SameItemSameComponents(carried) {
			maxStack := maxFor(slotData)
			if slotData.Stack.Count < maxStack {
				return slotData.WithCount(slotData.Stack.Count + 1), carried.WithCount(carried.Stack.Count - 1)
			}
			return slotData, carried
		}
		if carried.Stack.Count > slotLimit {
			return slotData, carried
		}
		return carried, slotData
	}

	if carried.IsEmpty() {
		return containerSlotData{}, slotData
	}
	if slotData.IsEmpty() {
		if carried.Stack.Count > slotLimit {
			return carried.WithCount(slotLimit), carried.WithCount(carried.Stack.Count - slotLimit)
		}
		return carried, containerSlotData{}
	}
	if slotData.SameItemSameComponents(carried) {
		maxStack := maxFor(slotData)
		space := maxStack - slotData.Stack.Count
		if space <= 0 {
			return slotData, carried
		}
		move := carried.Stack.Count
		if move > space {
			move = space
		}
		return slotData.WithCount(slotData.Stack.Count + move), carried.WithCount(carried.Stack.Count - move)
	}
	if carried.Stack.Count > slotLimit {
		return slotData, carried
	}
	return carried, slotData
}

func applyPickupAllSlotData(slots []containerSlotData, carried *containerSlotData, minSlot int, skipSlot func(i int) bool) {
	if carried == nil || carried.IsEmpty() {
		return
	}
	maxStack := carried.MaxStackSize()
	if carried.Stack.Count >= maxStack {
		return
	}
	for pass := 0; pass < 2 && carried.Stack.Count < maxStack; pass++ {
		for i := minSlot; i < len(slots) && carried.Stack.Count < maxStack; i++ {
			if skipSlot != nil && skipSlot(i) {
				continue
			}
			st := slots[i]
			if st.IsEmpty() || !st.SameItemSameComponents(*carried) {
				continue
			}
			if pass == 0 && st.Stack.Count >= maxStack {
				continue
			}
			space := maxStack - carried.Stack.Count
			take := st.Stack.Count
			if take > space {
				take = space
			}
			*carried = carried.WithCount(carried.Stack.Count + take)
			slots[i] = st.WithCount(st.Stack.Count - take)
		}
	}
}

func distributeQuickCraftOnSlotData(slots []containerSlotData, carried *containerSlotData, dragSlots []int, dragType int8, slotMaxStack func(slot int, item containerSlotData) int32) {
	if carried == nil || carried.IsEmpty() || len(dragSlots) == 0 {
		return
	}
	origCarried := carried.Clone()
	defaultMax := origCarried.MaxStackSize()
	placePerSlot := QuickCraftPlaceCount(len(dragSlots), dragType, origCarried.Stack)
	if placePerSlot <= 0 {
		return
	}
	remaining := origCarried.Stack.Count
	for _, sl := range dragSlots {
		if sl < 0 || sl >= len(slots) {
			continue
		}
		cur := slots[sl]
		if !cur.IsEmpty() && !cur.SameItemSameComponents(origCarried) {
			continue
		}
		maxForSlot := defaultMax
		if slotMaxStack != nil {
			if m := slotMaxStack(sl, origCarried); m < maxForSlot {
				maxForSlot = m
			}
		}
		curCount := int32(0)
		if !cur.IsEmpty() {
			curCount = cur.Stack.Count
		}
		if curCount >= maxForSlot {
			continue
		}
		add := placePerSlot
		if curCount+add > maxForSlot {
			add = maxForSlot - curCount
		}
		if dragType != QuickCraftTypeClone {
			if add > remaining {
				add = remaining
			}
			remaining -= add
		}
		if add > 0 {
			slots[sl] = origCarried.WithCount(curCount + add)
		}
	}
	if dragType != QuickCraftTypeClone {
		*carried = origCarried.WithCount(remaining)
	}
}

func (s *Server) syncActiveContainerLocked(session *PlayerSession, tr *playerContainerTracker) error {
	if s == nil || session == nil || tr == nil {
		return nil
	}
	tr.mu.Lock()
	cid, slots, carried, supported := s.buildActiveMenuSlotDataLocked(session, tr)
	var openMenuType int32
	var isEnderChest bool
	var vehID int32
	if tr.open != nil {
		openMenuType = tr.open.MenuType
		isEnderChest = tr.open.IsEnderChest
		vehID = tr.open.VehicleEntityID
	}
	tr.mu.Unlock()
	if !supported {
		return nil
	}
	setTrackerRemoteSnapshot(tr, cid, slots, carried)
	if session.send == nil {
		return nil
	}
	stateID := session.NextContainerStateID()
	if err := session.send(protocol.PlayContainerSetContent, encodeContainerContentFromSlotData(cid, stateID, slots, carried)); err != nil {
		return err
	}
	if openMenuType == MenuTypeGeneric9x3 && vehID == 0 && !isEnderChest {
		for i := 0; i < ChestSlotCount && i < len(slots); i++ {
			if slots[i].HasMap && slots[i].Stack.ResolvedID() == FilledMapItemID {
				if data, exists := s.GetMapData(slots[i].MapID); exists && data != nil {
					s.refreshMapDecorations(data)
					_ = session.send(PlayMapItemDataCB, EncodeMapItemDataPacket(slots[i].MapID, data.Scale, data.Locked, data.DecorationList(), data.FullMapColorPatch()))
				}
			}
		}
	}
	return nil
}

func (s *Server) syncOpenContainer(session *PlayerSession) error {
	if session == nil || session.send == nil {
		return nil
	}
	tr := getContainerTracker(session)
	tr.syncMu.Lock()
	tr.mu.Lock()
	if tr.open == nil {
		tr.mu.Unlock()
		err := s.syncPlayerInventoryContainerLocked(session, tr)
		tr.syncMu.Unlock()
		return err
	}
	_, _, _, supported := s.buildActiveMenuSlotDataLocked(session, tr)
	if supported {
		tr.mu.Unlock()
		err := s.syncActiveContainerLocked(session, tr)
		tr.syncMu.Unlock()
		return err
	}
	carried := tr.carried
	cp := *tr.open
	openCopy := &cp
	tr.mu.Unlock()
	tr.syncMu.Unlock()
	var slots []world.ItemStack
	var slotMetas []ItemMeta
	var slotTrims []ArmorTrim
	hasMetaAndTrim := false
	isFurnace := false
	isEnchant := false
	isAnvil := false
	isBrewingStand := false
	isLoom := false
	isCrafter := false
	switch openCopy.MenuType {
	case MenuTypeGeneric9x3, MenuTypeShulkerBox:
		if openCopy.VehicleEntityID != 0 {
			slots = s.buildVehicleChestMenuSlots(session, openCopy.VehicleEntityID)
		} else if openCopy.IsEnderChest {
			slots = s.buildEnderChestMenuSlots(session)
		} else {
			slots = s.buildChestMenuSlotsInDim(session, openCopy.Dimension, openCopy.BlockX, openCopy.BlockY, openCopy.BlockZ)
		}
	case MenuTypeGeneric9x6:
		slots = s.buildDoubleChestMenuSlotsInDim(
			session,
			openCopy.Dimension,
			openCopy.BlockX, openCopy.BlockY, openCopy.BlockZ,
			openCopy.PartnerX, openCopy.PartnerY, openCopy.PartnerZ,
		)
	case MenuTypeHopper:
		if openCopy.VehicleEntityID != 0 {
			slots = s.buildVehicleHopperMenuSlots(session, openCopy.VehicleEntityID)
		} else {
			slots = s.BuildHopperWindowSlots(session, openCopy.BlockX, openCopy.BlockY, openCopy.BlockZ)
		}
	case CrafterMenuType:
		slots = s.BuildCrafterMenuSlots(session, session.Dimension(), openCopy.BlockX, openCopy.BlockY, openCopy.BlockZ)
		isCrafter = true
	case MenuTypeHorse:
		slots = s.BuildEquineMenuSlots(session, openCopy.VehicleEntityID)
	case MenuTypeGeneric3x3:
		slots = s.BuildDispenserMenuSlots(session, openCopy.BlockX, openCopy.BlockY, openCopy.BlockZ)
	case MenuTypeCrafting:
		slots = s.buildCraftingMenuSlots(session, openCopy.Crafting, openCopy.Result)
	case MenuTypeFurnace, MenuTypeBlastFurnace, MenuTypeSmoker:
		slots = s.buildFurnaceMenuSlots(session, openCopy.BlockX, openCopy.BlockY, openCopy.BlockZ)
		isFurnace = true
	case MenuTypeEnchantment:
		slots = s.buildEnchantingMenuSlots(session, openCopy.EnchantSlots)
		slotMetas = buildEnchantingMenuMetas(session)
		slotTrims = buildEnchantingMenuTrims(session)
		hasMetaAndTrim = true
		isEnchant = true
	case MenuTypeAnvil:
		slots = s.buildCombinerMenuSlots(session, openCopy.CombinerInputs, openCopy.Result)
		slotMetas = buildCombinerMenuMetas(session)
		slotTrims = buildCombinerMenuTrims(session)
		hasMetaAndTrim = true
		isAnvil = true
	case MenuTypeGrindstone:
		slots = s.buildCombinerMenuSlots(session, openCopy.CombinerInputs, openCopy.Result)
		slotMetas = buildCombinerMenuMetas(session)
		slotTrims = buildCombinerMenuTrims(session)
		hasMetaAndTrim = true
	case MenuTypeBrewingStand:
		return s.syncOpenBrewingStand(session)
	case MenuTypeLoom:
		slots = s.buildLoomMenuSlots(
			session,
			openCopy.Crafting[LoomSlotBanner],
			openCopy.Crafting[LoomSlotDye],
			openCopy.Crafting[LoomSlotPattern],
			openCopy.Result,
		)
		isLoom = true
	case MenuTypeCartographyTable:
		return s.syncCartographyContainer(session)
	case MenuTypeStonecutter:
		return s.syncStonecutterContainer(session)
	case MenuTypeSmithing:
		return s.syncSmithingContainer(session)
	case MenuTypeBeacon:
		return s.syncOpenBeacon(session)
	case MenuTypeMerchant:
		slots = s.buildMerchantMenuSlots(
			session,
			openCopy.Crafting[MerchantSlotPayment1],
			openCopy.Crafting[MerchantSlotPayment2],
			openCopy.Result,
		)
		slotMetas, slotTrims = buildMerchantMenuMetasAndTrims(session, openCopy)
		hasMetaAndTrim = true
	default:
		return nil
	}
	stateID := session.NextContainerStateID()
	var contentPkt []byte
	if hasMetaAndTrim {
		carriedTrim, _ := GetCarriedArmorTrim(session)
		contentPkt = EncodeContainerContentWithMetaAndTrim(
			openCopy.ContainerID,
			stateID,
			slots,
			slotMetas,
			slotTrims,
			carried,
			GetCarriedItemMeta(session),
			carriedTrim,
		)
	} else {
		contentPkt = EncodeContainerContent(openCopy.ContainerID, stateID, slots, carried)
	}
	if err := session.send(protocol.PlayContainerSetContent, contentPkt); err != nil {
		return err
	}
	if openCopy.MenuType == MenuTypeCrafting && !openCopy.Result.IsEmpty() && IsPotionOrTippedArrowItem(openCopy.Result) {
		_ = session.send(protocol.PlayContainerSetSlot, encodeContainerSetSlotWithPotion(openCopy.ContainerID, session.NextContainerStateID(), 0, openCopy.Result, openCopy.CraftingPotions[4]))
	}
	if openCopy.MenuType == MenuTypeGeneric9x3 && openCopy.VehicleEntityID == 0 && !openCopy.IsEnderChest {
		s.syncChestMapSlots(session, openCopy.ContainerID, openCopy.BlockX, openCopy.BlockY, openCopy.BlockZ)
	}
	if isCrafter {
		return s.sendCrafterDataProperties(session, openCopy.ContainerID, session.Dimension(), openCopy.BlockX, openCopy.BlockY, openCopy.BlockZ)
	}
	if isFurnace {
		return s.sendFurnaceDataProperties(session, openCopy.ContainerID, openCopy.BlockX, openCopy.BlockY, openCopy.BlockZ)
	}
	if isEnchant {
		return s.sendEnchantingDataProperties(
			session,
			openCopy.ContainerID,
			openCopy.EnchantCosts,
			int16(session.EnchantmentSeed()),
			openCopy.EnchantClues,
			openCopy.LevelClues,
		)
	}
	if isAnvil {
		return session.send(protocol.PlayContainerSetData, EncodeContainerSetData(openCopy.ContainerID, AnvilDataLevelCost, openCopy.AnvilCost))
	}
	if isBrewingStand {
		return s.sendBrewingStandDataProperties(session, openCopy.ContainerID, openCopy.BlockX, openCopy.BlockY, openCopy.BlockZ)
	}
	if isLoom {
		if err := session.send(protocol.PlayContainerSetData, EncodeContainerSetData(openCopy.ContainerID, 0, openCopy.AnvilCost)); err != nil {
			return err
		}
		if !openCopy.Result.IsEmpty() {
			btr := getBannerMetaTracker(session)
			btr.mu.Lock()
			resPats := btr.loomResult.Clone()
			btr.mu.Unlock()
			return session.send(protocol.PlayContainerSetSlot, encodeContainerSetSlotWithBanner(openCopy.ContainerID, session.NextContainerStateID(), LoomSlotResult, openCopy.Result, resPats, ""))
		}
	}
	return nil
}

func (s *Server) syncPlayerInventoryContainerLocked(session *PlayerSession, tr *playerContainerTracker) error {
	if s == nil || session == nil || tr == nil {
		return nil
	}
	tr.mu.Lock()
	slots := s.buildPlayerMenuSlotData(session, tr)
	carried := getCarriedSlotDataLocked(session, tr)
	tr.mu.Unlock()
	setTrackerRemoteSnapshot(tr, 0, slots, carried)
	if session.send == nil {
		return nil
	}
	stateID := session.NextContainerStateID()
	return session.send(protocol.PlayContainerSetContent, encodeContainerContentFromSlotData(0, stateID, slots, carried))
}

// SyncPlayerInventory pushes session's current player inventory contents and
// cursor item over TCP and updates its remote container snapshot.
func (s *Server) SyncPlayerInventory(session *PlayerSession) error {
	return s.syncPlayerInventoryContainer(session)
}

func (s *Server) syncPlayerInventoryContainer(session *PlayerSession) error {
	if session == nil {
		return nil
	}
	tr := getContainerTracker(session)
	tr.syncMu.Lock()
	defer tr.syncMu.Unlock()
	return s.syncPlayerInventoryContainerLocked(session, tr)
}

func (s *Server) syncOtherStorageViewers(actor *PlayerSession, dim string, bx, by, bz int) error {
	if s == nil {
		return nil
	}
	normDim := world.NormalizeDimension(dim)
	s.playersMu.RLock()
	sessions := make([]*PlayerSession, 0, len(s.players))
	for _, sess := range s.players {
		if sess != nil && sess != actor && sess.send != nil {
			sessions = append(sessions, sess)
		}
	}
	s.playersMu.RUnlock()

	var firstErr error
	for _, sess := range sessions {
		otherTr := getContainerTracker(sess)
		otherTr.syncMu.Lock()
		otherTr.mu.Lock()
		open := otherTr.open
		matches := open != nil && !open.IsEnderChest && open.VehicleEntityID == 0 &&
			(open.MenuType == MenuTypeGeneric9x3 || open.MenuType == MenuTypeGeneric9x6 || open.MenuType == MenuTypeShulkerBox) &&
			((open.BlockX == bx && open.BlockY == by && open.BlockZ == bz) ||
				(open.HasPartner && open.PartnerX == bx && open.PartnerY == by && open.PartnerZ == bz))
		if matches && normDim != "" {
			otherDim := open.Dimension
			if otherDim == "" {
				otherDim = sess.Dimension()
			}
			if world.NormalizeDimension(otherDim) != normDim {
				matches = false
			}
		}
		if !matches {
			otherTr.mu.Unlock()
			otherTr.syncMu.Unlock()
			continue
		}
		cid, slots, carried, supported := s.buildActiveMenuSlotDataLocked(sess, otherTr)
		storageCount := ChestSlotCount
		if open.MenuType == MenuTypeGeneric9x6 {
			storageCount = DoubleChestSlotCount
		}
		otherTr.mu.Unlock()

		if supported {
			if otherTr.remoteContainerID != cid || len(otherTr.remoteSlots) != len(slots) {
				setTrackerRemoteSnapshot(otherTr, cid, slots, carried)
				stateID := sess.NextContainerStateID()
				if err := sess.send(protocol.PlayContainerSetContent, encodeContainerContentFromSlotData(cid, stateID, slots, carried)); err != nil && firstErr == nil {
					firstErr = err
				}
			} else {
				for i := 0; i < storageCount && i < len(slots); i++ {
					actualHS := slots[i].ToHashedStack()
					if !actualHS.Matches(otherTr.remoteSlots[i]) {
						stateID := sess.NextContainerStateID()
						if err := sess.send(protocol.PlayContainerSetSlot, encodeContainerSlotFromSlotData(cid, stateID, i, slots[i])); err != nil && firstErr == nil {
							firstErr = err
						}
						otherTr.remoteSlots[i] = actualHS
					}
				}
			}
		}
		otherTr.syncMu.Unlock()
	}
	return firstErr
}

func (s *Server) syncChestViewers(actor *PlayerSession, bx, by, bz int) error {
	var firstErr error
	dim := ""
	if actor != nil {
		if open := actor.OpenContainer(); open != nil && !open.IsEnderChest {
			dim = open.Dimension
		}
		if dim == "" {
			dim = actor.Dimension()
		}
		if err := s.syncOpenContainer(actor); err != nil {
			firstErr = err
		}
	}
	if err := s.syncOtherStorageViewers(actor, dim, bx, by, bz); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

func (s *Server) applyDoubleChestClickDataLocked(
	session *PlayerSession,
	tr *playerContainerTracker,
	x1, y1, z1, x2, y2, z2, slot int,
	button int8,
	mode int32,
) (pendingDrop containerSlotData, mutated bool) {
	if slot < 0 || slot >= DoubleChestMenuSlotCount {
		return containerSlotData{}, false
	}
	openDim := world.DimensionOverworld
	if tr.open != nil && tr.open.Dimension != "" {
		openDim = tr.open.Dimension
	} else if session != nil && session.Dimension() != "" {
		openDim = session.Dimension()
	}
	slots := s.buildDoubleChestMenuSlotDataInDim(session, openDim, x1, y1, z1, x2, y2, z2)
	orig := append([]containerSlotData(nil), slots...)
	carried := getCarriedSlotDataLocked(session, tr)

	switch mode {
	case ContainerInputPickup:
		slots[slot], carried = applyPickupSlotData(slots[slot], carried, button, MaxItemStackSize)
		mutated = true
	case ContainerInputQuickMove:
		if slot < DoubleChestSlotCount {
			slots[slot], mutated = moveSlotDataSlice(slots, slots[slot], DoubleChestSlotCount, DoubleChestMenuSlotCount, true, nil)
		} else {
			slots[slot], mutated = moveSlotDataSlice(slots, slots[slot], 0, DoubleChestSlotCount, false, nil)
		}
	case ContainerInputSwap:
		if button >= 0 && button < 9 {
			hotbarIdx := 81 + int(button)
			slots[slot], slots[hotbarIdx] = slots[hotbarIdx], slots[slot]
			mutated = true
		} else if button == 40 {
			offData := getPlayerSlotData(session, OffHandContainerSlot)
			slots[slot], offData = offData, slots[slot]
			setPlayerSlotData(session, OffHandContainerSlot, offData)
			mutated = true
		}
	case ContainerInputClone:
		if session.Snapshot().GameMode == GameModeCreative && carried.IsEmpty() && !slots[slot].IsEmpty() {
			carried = slots[slot].WithCount(slots[slot].MaxStackSize())
			mutated = true
		}
	case ContainerInputThrow:
		if carried.IsEmpty() && !slots[slot].IsEmpty() {
			dropCount := int32(1)
			if button == 1 {
				dropCount = slots[slot].Stack.Count
			}
			pendingDrop = slots[slot].WithCount(dropCount)
			slots[slot] = slots[slot].WithCount(slots[slot].Stack.Count - dropCount)
			mutated = true
		}
	case ContainerInputPickupAll:
		if !carried.IsEmpty() {
			applyPickupAllSlotData(slots, &carried, 0, nil)
			mutated = true
		}
	}
	for i := 0; i < ChestSlotCount; i++ {
		if !slots[i].SameItemSameComponents(orig[i]) || slots[i].Stack.Count != orig[i].Stack.Count {
			s.setStorageSlotData(openDim, x1, y1, z1, i, slots[i])
		}
		hi := ChestSlotCount + i
		if !slots[hi].SameItemSameComponents(orig[hi]) || slots[hi].Stack.Count != orig[hi].Stack.Count {
			s.setStorageSlotData(openDim, x2, y2, z2, i, slots[hi])
		}
	}
	for i := 0; i < 27; i++ {
		idx := 54 + i
		if !slots[idx].SameItemSameComponents(orig[idx]) || slots[idx].Stack.Count != orig[idx].Stack.Count {
			setPlayerSlotData(session, 9+i, slots[idx])
		}
	}
	for i := 0; i < 9; i++ {
		idx := 81 + i
		if !slots[idx].SameItemSameComponents(orig[idx]) || slots[idx].Stack.Count != orig[idx].Stack.Count {
			setPlayerSlotData(session, 36+i, slots[idx])
		}
	}
	setCarriedSlotDataLocked(session, tr, carried)
	return pendingDrop, mutated
}

func (s *Server) applyDoubleChestClickLocked(
	session *PlayerSession,
	tr *playerContainerTracker,
	x1, y1, z1, x2, y2, z2, slot int,
	button int8,
	mode int32,
) {
	pendingDrop, _ := s.applyDoubleChestClickDataLocked(session, tr, x1, y1, z1, x2, y2, z2, slot, button, mode)
	if !pendingDrop.IsEmpty() {
		_, _ = s.SpawnPlayerDroppedItem(session, pendingDrop)
		s.RecordItemDropped(session, pendingDrop.Stack.ResolvedID(), pendingDrop.Stack.Count)
	}
}

func (s *Server) applyEnderChestClickDataLocked(
	session *PlayerSession,
	tr *playerContainerTracker,
	slot int,
	button int8,
	mode int32,
) (pendingDrop containerSlotData, mutated bool) {
	if slot < 0 || slot >= ChestMenuSlotCount {
		return containerSlotData{}, false
	}
	slots := s.buildEnderChestMenuSlotData(session, tr)
	orig := append([]containerSlotData(nil), slots...)
	carried := getCarriedSlotDataLocked(session, tr)

	switch mode {
	case ContainerInputPickup:
		slots[slot], carried = applyPickupSlotData(slots[slot], carried, button, MaxItemStackSize)
		mutated = true
	case ContainerInputQuickMove:
		if slot < ChestSlotCount {
			slots[slot], mutated = moveSlotDataSlice(slots, slots[slot], ChestSlotCount, ChestMenuSlotCount, true, nil)
		} else {
			slots[slot], mutated = moveSlotDataSlice(slots, slots[slot], 0, ChestSlotCount, false, nil)
		}
	case ContainerInputSwap:
		if button >= 0 && button < 9 {
			hotbarIdx := 54 + int(button)
			slots[slot], slots[hotbarIdx] = slots[hotbarIdx], slots[slot]
			mutated = true
		} else if button == 40 {
			offData := getPlayerSlotData(session, OffHandContainerSlot)
			slots[slot], offData = offData, slots[slot]
			setPlayerSlotData(session, OffHandContainerSlot, offData)
			mutated = true
		}
	case ContainerInputClone:
		if session.Snapshot().GameMode == GameModeCreative && carried.IsEmpty() && !slots[slot].IsEmpty() {
			carried = slots[slot].WithCount(slots[slot].MaxStackSize())
			mutated = true
		}
	case ContainerInputThrow:
		if carried.IsEmpty() && !slots[slot].IsEmpty() {
			dropCount := int32(1)
			if button == 1 {
				dropCount = slots[slot].Stack.Count
			}
			pendingDrop = slots[slot].WithCount(dropCount)
			slots[slot] = slots[slot].WithCount(slots[slot].Stack.Count - dropCount)
			mutated = true
		}
	case ContainerInputPickupAll:
		if !carried.IsEmpty() {
			applyPickupAllSlotData(slots, &carried, 0, nil)
			mutated = true
		}
	}
	for i := 0; i < ChestSlotCount; i++ {
		if slots[i].IsEmpty() {
			tr.enderChest[i] = world.ItemStack{}
		} else {
			tr.enderChest[i] = world.NewItemStack(slots[i].Stack.ResolvedItemID(), slots[i].Stack.Count)
		}
	}
	for i := 0; i < 27; i++ {
		idx := 27 + i
		if !slots[idx].SameItemSameComponents(orig[idx]) || slots[idx].Stack.Count != orig[idx].Stack.Count {
			setPlayerSlotData(session, 9+i, slots[idx])
		}
	}
	for i := 0; i < 9; i++ {
		idx := 54 + i
		if !slots[idx].SameItemSameComponents(orig[idx]) || slots[idx].Stack.Count != orig[idx].Stack.Count {
			setPlayerSlotData(session, 36+i, slots[idx])
		}
	}
	setCarriedSlotDataLocked(session, tr, carried)
	return pendingDrop, mutated
}

func (s *Server) applyEnderChestClickLocked(
	session *PlayerSession,
	tr *playerContainerTracker,
	slot int,
	button int8,
	mode int32,
) {
	pendingDrop, _ := s.applyEnderChestClickDataLocked(session, tr, slot, button, mode)
	if !pendingDrop.IsEmpty() {
		_, _ = s.SpawnPlayerDroppedItem(session, pendingDrop)
		s.RecordItemDropped(session, pendingDrop.Stack.ResolvedID(), pendingDrop.Stack.Count)
	}
}

func (s *Server) applyBrewingStandContainerClickLocked(
	session *PlayerSession,
	tr *playerContainerTracker,
	bx, by, bz, slot int,
	button int8,
	mode int32,
) {
	if slot < 0 || slot >= BrewingStandMenuSlotCount {
		return
	}

	bs := s.brewingState()
	bs.mu.Lock()
	stand := s.getOrCreateBrewingStandLocked(bs, bx, by, bz)
	stand.lastBrewer = session
	var slotPotions [BrewingStandMenuSlotCount]string
	for i := 0; i < 3; i++ {
		slotPotions[i] = stand.PotionTypes[i]
	}
	bs.mu.Unlock()

	slots := s.buildBrewingStandMenuSlots(session, bx, by, bz)
	for i := 0; i < 27; i++ {
		slotPotions[5+i] = GetSlotPotionType(session, 9+i)
	}
	for i := 0; i < 9; i++ {
		slotPotions[32+i] = GetSlotPotionType(session, 36+i)
	}
	carriedPotion := getCarriedPotionTypeForStack(session, tr.carried)

	switch mode {
	case ContainerInputPickup:
		switch {
		case slot >= 0 && slot < 3:
			if tr.carried.IsEmpty() {
				if !slots[slot].IsEmpty() {
					takenPotion := slotPotions[slot]
					tr.carried = slots[slot]
					carriedPotion = takenPotion
					slots[slot] = world.ItemStack{}
					slotPotions[slot] = ""
					if IsPotionItem(tr.carried) && takenPotion != "" && takenPotion != PotionWater {
						s.TriggerBrewedPotion(session, takenPotion)
					}
				}
			} else if CanPlaceInBrewingBottleSlot(tr.carried) {
				if slots[slot].IsEmpty() {
					slots[slot] = world.NewItemStack(tr.carried.ResolvedItemID(), 1)
					slotPotions[slot] = carriedPotion
					rem := tr.carried.Count - 1
					if rem <= 0 {
						tr.carried = world.ItemStack{}
						carriedPotion = ""
					} else {
						tr.carried = world.NewItemStack(tr.carried.ResolvedItemID(), rem)
					}
				} else if tr.carried.Count == 1 {
					slots[slot], tr.carried = tr.carried, slots[slot]
					slotPotions[slot], carriedPotion = carriedPotion, slotPotions[slot]
				}
			}

		case slot == BrewingStandSlotIngredient:
			if tr.carried.IsEmpty() || IsBrewingIngredient(tr.carried) {
				slots[slot], tr.carried = applyPickupSlot(slots[slot], tr.carried, button)
			}

		case slot == BrewingStandSlotFuel:
			if tr.carried.IsEmpty() || IsBrewingFuel(tr.carried) {
				slots[slot], tr.carried = applyPickupSlot(slots[slot], tr.carried, button)
			}

		default:
			prevSlotEmpty := slots[slot].IsEmpty()
			prevCarriedEmpty := tr.carried.IsEmpty()
			slots[slot], tr.carried = applyPickupSlot(slots[slot], tr.carried, button)
			if prevSlotEmpty && !slots[slot].IsEmpty() {
				slotPotions[slot] = carriedPotion
				if tr.carried.IsEmpty() {
					carriedPotion = ""
				}
			} else if prevCarriedEmpty && !tr.carried.IsEmpty() {
				carriedPotion = slotPotions[slot]
				if slots[slot].IsEmpty() {
					slotPotions[slot] = ""
				}
			} else if !slots[slot].IsEmpty() && !tr.carried.IsEmpty() {
				slotPotions[slot], carriedPotion = carriedPotion, slotPotions[slot]
			}
		}

	case ContainerInputQuickMove:
		if slot < BrewingStandSlotCount {
			movedPotion := slotPotions[slot]
			wasBottleSlot := slot < 3 && IsPotionItem(slots[slot])
			for dst := 5; dst < BrewingStandMenuSlotCount && !slots[slot].IsEmpty(); dst++ {
				if slots[dst].IsEmpty() {
					slots[dst] = slots[slot]
					slotPotions[dst] = movedPotion
					slots[slot] = world.ItemStack{}
					slotPotions[slot] = ""
					if wasBottleSlot && movedPotion != "" && movedPotion != PotionWater {
						s.TriggerBrewedPotion(session, movedPotion)
					}
					break
				}
			}
		} else {
			clicked := slots[slot]
			clickedPotion := slotPotions[slot]
			if !clicked.IsEmpty() {
				switch {
				case IsBrewingFuel(clicked):
					slots[slot] = mergeIntoSlotSlice(slots, clicked, BrewingStandSlotFuel, BrewingStandSlotFuel+1)
				case IsBrewingIngredient(clicked):
					slots[slot] = mergeIntoSlotSlice(slots, clicked, BrewingStandSlotIngredient, BrewingStandSlotIngredient+1)
				case CanPlaceInBrewingBottleSlot(clicked):
					for bSlot := 0; bSlot < 3 && !slots[slot].IsEmpty(); bSlot++ {
						if slots[bSlot].IsEmpty() {
							slots[bSlot] = world.NewItemStack(slots[slot].ResolvedItemID(), 1)
							slotPotions[bSlot] = clickedPotion
							rem := slots[slot].Count - 1
							if rem <= 0 {
								slots[slot] = world.ItemStack{}
								slotPotions[slot] = ""
							} else {
								slots[slot] = world.NewItemStack(slots[slot].ResolvedItemID(), rem)
							}
						}
					}
				}
			}
		}
	}

	for i := 0; i < BrewingStandSlotCount; i++ {
		pType := ""
		if i < 3 {
			pType = slotPotions[i]
		}
		s.SetBrewingSlot(bx, by, bz, i, slots[i], pType)
	}
	for i := 0; i < 27; i++ {
		session.SetInventorySlot(9+i, slots[5+i])
		SetSlotPotionType(session, 9+i, slotPotions[5+i])
	}
	for i := 0; i < 9; i++ {
		session.SetInventorySlot(36+i, slots[32+i])
		SetSlotPotionType(session, 36+i, slotPotions[32+i])
	}
	SetCarriedPotionType(session, carriedPotion)
}

// OpenVehicleContainer opens the container menu for a chest minecart (27 slots),
// hopper minecart (5 slots), or chest boat/raft (27 slots).
func (s *Server) OpenVehicleContainer(session *PlayerSession, entityID int32) (int32, error) {
	if s == nil || s.entities == nil || session == nil {
		return 0, errors.New("server: nil session")
	}
	ent := s.entities.Get(entityID)
	if ent == nil || !IsContainerVehicleEntity(ent.TypeName) {
		return 0, errors.New("server: entity is not a vehicle container")
	}

	_ = s.CloseContainer(session, 0, false)

	menuType := MenuTypeGeneric9x3
	title := "Minecart with Chest"
	switch {
	case ent.TypeName == EntityTypeHopperMinecart:
		menuType = MenuTypeHopper
		title = "Minecart with Hopper"
	case ent.TypeName == "minecraft:bamboo_chest_raft":
		title = "Raft with Chest"
	case IsChestBoatEntity(ent.TypeName):
		title = "Boat with Chest"
	}

	tr := getContainerTracker(session)
	tr.mu.Lock()
	tr.nextWindow = (tr.nextWindow % 100) + 1
	cid := tr.nextWindow
	tr.open = &OpenContainerState{
		ContainerID:     cid,
		MenuType:        menuType,
		BlockX:          int(ent.X),
		BlockY:          int(ent.Y),
		BlockZ:          int(ent.Z),
		VehicleEntityID: entityID,
	}
	carried := tr.carried
	tr.mu.Unlock()

	if session.send != nil {
		if err := session.send(protocol.PlayOpenScreen, EncodeOpenScreen(cid, menuType, title)); err != nil {
			return cid, err
		}
		var slots []world.ItemStack
		if menuType == MenuTypeHopper {
			slots = s.buildVehicleHopperMenuSlots(session, entityID)
		} else {
			slots = s.buildVehicleChestMenuSlots(session, entityID)
		}
		stateID := session.NextContainerStateID()
		if err := session.send(protocol.PlayContainerSetContent, EncodeContainerContent(cid, stateID, slots, carried)); err != nil {
			return cid, err
		}
	}
	return cid, nil
}

func (s *Server) buildVehicleChestMenuSlots(session *PlayerSession, entityID int32) []world.ItemStack {
	vehSlots := s.VehicleContainerSlots(entityID)
	inv := session.Inventory()
	slots := make([]world.ItemStack, ChestMenuSlotCount)
	copy(slots[0:ChestSlotCount], vehSlots[:])
	copy(slots[27:54], inv[9:36])
	copy(slots[54:63], inv[36:45])
	return slots
}

func (s *Server) buildVehicleHopperMenuSlots(session *PlayerSession, entityID int32) []world.ItemStack {
	vehSlots := s.VehicleContainerSlots(entityID)
	inv := session.Inventory()
	slots := make([]world.ItemStack, HopperMenuSlotCount)
	copy(slots[0:HopperSlotCount], vehSlots[:HopperSlotCount])
	copy(slots[5:32], inv[9:36])
	copy(slots[32:41], inv[36:45])
	return slots
}

func (s *Server) applyVehicleChestClickLocked(session *PlayerSession, tr *playerContainerTracker, entityID int32, slot int, button int8, mode int32) {
	if slot < 0 || slot >= ChestMenuSlotCount {
		return
	}
	slots := s.buildVehicleChestMenuSlots(session, entityID)

	switch mode {
	case ContainerInputPickup:
		slots[slot], tr.carried = applyPickupSlot(slots[slot], tr.carried, button)
	case ContainerInputQuickMove:
		if slot < ChestSlotCount {
			slots[slot] = mergeIntoSlotSlice(slots, slots[slot], 54, 63)
			slots[slot] = mergeIntoSlotSlice(slots, slots[slot], 27, 54)
		} else {
			slots[slot] = mergeIntoSlotSlice(slots, slots[slot], 0, ChestSlotCount)
		}
	case ContainerInputSwap:
		if button >= 0 && button < 9 {
			hotbarIdx := 54 + int(button)
			slots[slot], slots[hotbarIdx] = slots[hotbarIdx], slots[slot]
		} else if button == 40 {
			offStack := session.InventorySlot(OffHandContainerSlot)
			slots[slot], offStack = offStack, slots[slot]
			session.SetInventorySlot(OffHandContainerSlot, offStack)
		}
	case ContainerInputClone:
		if session.Snapshot().GameMode == GameModeCreative && tr.carried.IsEmpty() && !slots[slot].IsEmpty() {
			tr.carried = world.NewItemStack(slots[slot].ResolvedItemID(), MaxItemStackSize)
		}
	case ContainerInputThrow:
		if !slots[slot].IsEmpty() {
			dropCount := int32(1)
			if button == 1 {
				dropCount = slots[slot].Count
			}
			dropStack := world.NewItemStack(slots[slot].ResolvedItemID(), dropCount)
			rem := slots[slot].Count - dropCount
			if rem <= 0 {
				slots[slot] = world.ItemStack{}
			} else {
				slots[slot] = world.NewItemStack(slots[slot].ResolvedItemID(), rem)
			}
			snap := session.Snapshot()
			s.SpawnItemEntity(snap.X, snap.Y+0.5, snap.Z, dropStack)
			s.RecordItemDropped(session, dropStack.ResolvedID(), dropStack.Count)
		}
	}

	for i := 0; i < ChestSlotCount; i++ {
		s.SetVehicleContainerSlot(entityID, i, slots[i])
	}
	for i := 0; i < 27; i++ {
		session.SetInventorySlot(9+i, slots[27+i])
	}
	for i := 0; i < 9; i++ {
		session.SetInventorySlot(36+i, slots[54+i])
	}
}

func (s *Server) applyVehicleHopperClickLocked(session *PlayerSession, tr *playerContainerTracker, entityID int32, slot int, button int8, mode int32) {
	if slot < 0 || slot >= HopperMenuSlotCount {
		return
	}
	slots := s.buildVehicleHopperMenuSlots(session, entityID)

	switch mode {
	case ContainerInputPickup:
		slots[slot], tr.carried = applyPickupSlot(slots[slot], tr.carried, button)
	case ContainerInputQuickMove:
		if slot < HopperSlotCount {
			slots[slot] = mergeIntoSlotSlice(slots, slots[slot], 32, 41)
			slots[slot] = mergeIntoSlotSlice(slots, slots[slot], 5, 32)
		} else {
			slots[slot] = mergeIntoSlotSlice(slots, slots[slot], 0, HopperSlotCount)
		}
	case ContainerInputSwap:
		if button >= 0 && button < 9 {
			hotbarIdx := 32 + int(button)
			slots[slot], slots[hotbarIdx] = slots[hotbarIdx], slots[slot]
		} else if button == 40 {
			offStack := session.InventorySlot(OffHandContainerSlot)
			slots[slot], offStack = offStack, slots[slot]
			session.SetInventorySlot(OffHandContainerSlot, offStack)
		}
	case ContainerInputClone:
		if session.Snapshot().GameMode == GameModeCreative && tr.carried.IsEmpty() && !slots[slot].IsEmpty() {
			tr.carried = world.NewItemStack(slots[slot].ResolvedItemID(), MaxItemStackSize)
		}
	case ContainerInputThrow:
		if !slots[slot].IsEmpty() {
			dropCount := int32(1)
			if button == 1 {
				dropCount = slots[slot].Count
			}
			dropStack := world.NewItemStack(slots[slot].ResolvedItemID(), dropCount)
			rem := slots[slot].Count - dropCount
			if rem <= 0 {
				slots[slot] = world.ItemStack{}
			} else {
				slots[slot] = world.NewItemStack(slots[slot].ResolvedItemID(), rem)
			}
			snap := session.Snapshot()
			s.SpawnItemEntity(snap.X, snap.Y+0.5, snap.Z, dropStack)
			s.RecordItemDropped(session, dropStack.ResolvedID(), dropStack.Count)
		}
	}

	for i := 0; i < HopperSlotCount; i++ {
		s.SetVehicleContainerSlot(entityID, i, slots[i])
	}
	for i := 0; i < 27; i++ {
		session.SetInventorySlot(9+i, slots[5+i])
	}
	for i := 0; i < 9; i++ {
		session.SetInventorySlot(36+i, slots[32+i])
	}
}

func (s *Server) syncVehicleContainerViewers(actor *PlayerSession, entityID int32) error {
	s.playersMu.RLock()
	sessions := make([]*PlayerSession, 0, len(s.players))
	for _, sess := range s.players {
		sessions = append(sessions, sess)
	}
	s.playersMu.RUnlock()

	var firstErr error
	for _, sess := range sessions {
		open := sess.OpenContainer()
		if open != nil && open.VehicleEntityID == entityID {
			if err := s.syncOpenContainer(sess); err != nil && firstErr == nil {
				firstErr = err
			}
		} else if sess == actor {
			if err := s.syncOpenContainer(sess); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// OpenCrafter opens the 46-slot `minecraft:crafter_3x3` screen at (x, y, z)
// for session, sends `open_screen` + `container_set_content` + `container_set_data`,
// and emits `GameEventContainerOpen`.
func (s *Server) OpenCrafter(session *PlayerSession, x, y, z int) (int32, error) {
	if s == nil || session == nil {
		return 0, errors.New("server: nil session")
	}
	_ = s.CloseContainer(session, 0, false)

	dim := normalizeHopperDim(session.Dimension())
	s.EmitPlayerBlockVibration(session, GameEventContainerOpen, dim, x, y, z, "minecraft:crafter")

	tr := getContainerTracker(session)
	tr.mu.Lock()
	tr.nextWindow = (tr.nextWindow % 100) + 1
	cid := tr.nextWindow
	tr.open = &OpenContainerState{
		ContainerID: cid,
		MenuType:    CrafterMenuType,
		BlockX:      x,
		BlockY:      y,
		BlockZ:      z,
	}
	carried := tr.carried
	tr.mu.Unlock()

	if session.send != nil {
		if err := session.send(protocol.PlayOpenScreen, EncodeOpenScreen(cid, CrafterMenuType, "Crafter")); err != nil {
			return cid, err
		}
		slots := s.BuildCrafterMenuSlots(session, dim, x, y, z)
		stateID := session.NextContainerStateID()
		if err := session.send(protocol.PlayContainerSetContent, EncodeContainerContent(cid, stateID, slots, carried)); err != nil {
			return cid, err
		}
		if err := s.sendCrafterDataProperties(session, cid, dim, x, y, z); err != nil {
			return cid, err
		}
	}
	return cid, nil
}

func (s *Server) sendCrafterDataProperties(session *PlayerSession, cid int32, dim string, x, y, z int) error {
	if session == nil || session.send == nil {
		return nil
	}
	props := s.CrafterContainerData(dim, x, y, z)
	for i := int16(0); i < int16(len(props)); i++ {
		if err := session.send(protocol.PlayContainerSetData, EncodeContainerSetData(cid, i, props[i])); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) applyCrafterClickLocked(session *PlayerSession, tr *playerContainerTracker, bx, by, bz, slot int, button int8, mode int32) {
	if slot < 0 || slot >= CrafterMenuSlotCount || slot == CrafterMenuResultSlot {
		return
	}
	dim := normalizeHopperDim(session.Dimension())
	slots := s.BuildCrafterMenuSlots(session, dim, bx, by, bz)

	if slot >= 0 && slot < CrafterSlotCount {
		if mode == ContainerInputPickup && tr.carried.IsEmpty() && slots[slot].IsEmpty() {
			s.ToggleCrafterSlotDisabled(dim, bx, by, bz, slot)
			return
		}
		if !tr.carried.IsEmpty() && s.IsCrafterSlotDisabled(dim, bx, by, bz, slot) {
			s.SetCrafterSlotDisabled(dim, bx, by, bz, slot, false)
		}
	}

	switch mode {
	case ContainerInputPickup:
		slots[slot], tr.carried = applyPickupSlot(slots[slot], tr.carried, button)
	case ContainerInputQuickMove:
		if slot < CrafterSlotCount {
			slots[slot] = mergeIntoSlotSlice(slots, slots[slot], 36, 45)
			slots[slot] = mergeIntoSlotSlice(slots, slots[slot], 9, 36)
		} else if slot >= 9 && slot < 45 {
			for dst := 0; dst < CrafterSlotCount && !slots[slot].IsEmpty(); dst++ {
				if s.IsCrafterSlotDisabled(dim, bx, by, bz, dst) {
					continue
				}
				slots[slot] = mergeIntoSlotSlice(slots, slots[slot], dst, dst+1)
			}
		}
	case ContainerInputSwap:
		if button >= 0 && button < 9 {
			hotbarIdx := 36 + int(button)
			if slot < CrafterSlotCount && !slots[hotbarIdx].IsEmpty() && s.IsCrafterSlotDisabled(dim, bx, by, bz, slot) {
				s.SetCrafterSlotDisabled(dim, bx, by, bz, slot, false)
			}
			slots[slot], slots[hotbarIdx] = slots[hotbarIdx], slots[slot]
		} else if button == 40 {
			offStack := session.InventorySlot(OffHandContainerSlot)
			if slot < CrafterSlotCount && !offStack.IsEmpty() && s.IsCrafterSlotDisabled(dim, bx, by, bz, slot) {
				s.SetCrafterSlotDisabled(dim, bx, by, bz, slot, false)
			}
			slots[slot], offStack = offStack, slots[slot]
			session.SetInventorySlot(OffHandContainerSlot, offStack)
		}
	case ContainerInputClone:
		if session.Snapshot().GameMode == GameModeCreative && tr.carried.IsEmpty() && !slots[slot].IsEmpty() {
			tr.carried = world.NewItemStack(slots[slot].ResolvedItemID(), MaxItemStackSize)
		}
	case ContainerInputThrow:
		if !slots[slot].IsEmpty() {
			dropCount := int32(1)
			if button == 1 {
				dropCount = slots[slot].Count
			}
			dropStack := world.NewItemStack(slots[slot].ResolvedItemID(), dropCount)
			rem := slots[slot].Count - dropCount
			if rem <= 0 {
				slots[slot] = world.ItemStack{}
			} else {
				slots[slot] = world.NewItemStack(slots[slot].ResolvedItemID(), rem)
			}
			snap := session.Snapshot()
			s.SpawnItemEntityInDimension(dim, snap.X, snap.Y+0.5, snap.Z, dropStack)
			s.RecordItemDropped(session, dropStack.ResolvedID(), dropStack.Count)
		}
	}

	for i := 0; i < CrafterSlotCount; i++ {
		s.SetCrafterSlot(dim, bx, by, bz, i, slots[i], ItemMeta{})
	}
	for i := 0; i < 27; i++ {
		session.SetInventorySlot(9+i, slots[9+i])
	}
	for i := 0; i < 9; i++ {
		session.SetInventorySlot(36+i, slots[36+i])
	}
}

// SyncCrafterViewers synchronizes the open crafter container screen for all
// players currently viewing the crafter at (bx, by, bz).
func (s *Server) SyncCrafterViewers(actor *PlayerSession, bx, by, bz int) error {
	if s == nil {
		return nil
	}
	s.playersMu.RLock()
	sessions := make([]*PlayerSession, 0, len(s.players))
	for _, sess := range s.players {
		sessions = append(sessions, sess)
	}
	s.playersMu.RUnlock()

	var firstErr error
	for _, sess := range sessions {
		if sess == nil || sess.send == nil {
			continue
		}
		open := sess.OpenContainer()
		if open != nil && open.MenuType == CrafterMenuType &&
			open.BlockX == bx && open.BlockY == by && open.BlockZ == bz {
			if err := s.syncOpenContainer(sess); err != nil && firstErr == nil {
				firstErr = err
			}
		} else if sess == actor {
			if err := s.syncOpenContainer(sess); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// EncodeHorseScreenOpen encodes a ClientboundHorseScreenOpenPacket (0x29 in protocol 775):
// VarInt(ContainerID), VarInt(InventoryColumns), Int32(EntityID).
func EncodeHorseScreenOpen(containerID, inventoryColumns, entityID int32) []byte {
	w := protocol.NewWriter(16)
	w.VarInt(containerID)
	w.VarInt(inventoryColumns)
	w.Int32(entityID)
	return w.Bytes()
}

// DecodeHorseScreenOpen decodes a ClientboundHorseScreenOpenPacket (0x29).
func DecodeHorseScreenOpen(r *protocol.Reader) (containerID, inventoryColumns, entityID int32, err error) {
	containerID, err = r.VarInt()
	if err != nil {
		return 0, 0, 0, err
	}
	inventoryColumns, err = r.VarInt()
	if err != nil {
		return 0, 0, 0, err
	}
	entityID, err = r.Int32()
	if err != nil {
		return 0, 0, 0, err
	}
	return containerID, inventoryColumns, entityID, nil
}

// OpenEquineInventory opens a ClientboundHorseScreenOpenPacket (0x29) menu for
// a tamed horse, donkey, mule, skeleton_horse, zombie_horse, llama, or trader_llama.
func (s *Server) OpenEquineInventory(session *PlayerSession, entityID int32) (int32, error) {
	if s == nil || s.entities == nil || session == nil || session.IsDead() {
		return 0, nil
	}
	ent := s.entities.Get(entityID)
	if ent == nil || !IsHorseLikeMob(ent.TypeName) || !s.IsMobTamed(entityID) {
		return 0, nil
	}
	if open := session.OpenContainer(); open != nil {
		_ = s.CloseContainer(session, open.ContainerID, true)
	}
	tr := getContainerTracker(session)
	tr.mu.Lock()
	tr.nextWindow = (tr.nextWindow % 100) + 1
	cid := tr.nextWindow
	tr.open = &OpenContainerState{
		ContainerID:     cid,
		MenuType:        MenuTypeHorse,
		VehicleEntityID: entityID,
	}
	tr.mu.Unlock()

	chestSlots := s.EquineChestSlotCountFor(entityID)
	columns := int32(chestSlots / 3)
	if session.send != nil {
		if err := session.send(protocol.PlayHorseScreenOpen, EncodeHorseScreenOpen(cid, columns, entityID)); err != nil {
			return cid, err
		}
	}
	return cid, s.syncOpenContainer(session)
}

// BuildEquineMenuSlots builds the container slot slice for a horse/llama screen:
//   - Slot 0: Saddle (`IsEquineSaddled`)
//   - Slot 1: Horse armor (`HorseArmorItem`) or Llama carpet (`LlamaCarpetItem`)
//   - Slots 2 .. 2+chestSlots-1: Chest slots when `HasEquineChest` (`EquineInventorySlot`)
//   - Remaining 36 slots: Player main inventory (9..35) + hotbar (36..44)
func (s *Server) BuildEquineMenuSlots(session *PlayerSession, entityID int32) []world.ItemStack {
	chestSlots := 0
	if s != nil && s.HasEquineChest(entityID) {
		chestSlots = s.EquineChestSlotCountFor(entityID)
	}
	equipAndChest := 2 + chestSlots
	slots := make([]world.ItemStack, equipAndChest+36)
	if s != nil && entityID > 0 {
		if s.IsEquineSaddled(entityID) {
			slots[0] = world.NewNamedItemStack("minecraft:saddle", 1)
		}
		if armorItem := s.HorseArmorItem(entityID); armorItem != "" {
			slots[1] = world.NewNamedItemStack(armorItem, 1)
		} else if carpetItem := s.LlamaCarpetItem(entityID); carpetItem != "" {
			slots[1] = world.NewNamedItemStack(carpetItem, 1)
		}
		for i := 0; i < chestSlots; i++ {
			slots[2+i] = s.EquineInventorySlot(entityID, i)
		}
	}
	if session != nil {
		for i := 0; i < 27; i++ {
			slots[equipAndChest+i] = session.InventorySlot(9 + i)
		}
		for i := 0; i < 9; i++ {
			slots[equipAndChest+27+i] = session.InventorySlot(36 + i)
		}
	}
	return slots
}

func (s *Server) canAcceptEquineSlot(entityID int32, slot int, stack world.ItemStack) bool {
	if stack.IsEmpty() || s == nil || s.entities == nil {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil {
		return false
	}
	norm := canonicalResourceName(ent.TypeName)
	itemName := canonicalResourceName(stack.ResolvedID())
	switch slot {
	case 0:
		if norm == MobTypeLlama || norm == MobTypeTraderLlama {
			return false
		}
		return itemName == "minecraft:saddle"
	case 1:
		if norm == MobTypeHorse {
			return IsHorseArmorItem(itemName)
		}
		if norm == MobTypeLlama || norm == MobTypeTraderLlama {
			_, ok := CarpetColorByItemName(itemName)
			return ok
		}
		return false
	default:
		return true
	}
}

func (s *Server) applyEquineContainerClickLocked(session *PlayerSession, tr *playerContainerTracker, entityID int32, slot int, button int8, mode int32) {
	if s == nil || session == nil || entityID <= 0 {
		return
	}
	slots := s.BuildEquineMenuSlots(session, entityID)
	chestSlots := 0
	if s.HasEquineChest(entityID) {
		chestSlots = s.EquineChestSlotCountFor(entityID)
	}
	equipAndChest := 2 + chestSlots
	totalSlots := equipAndChest + 36
	if slot < 0 || slot >= totalSlots {
		return
	}

	switch mode {
	case ContainerInputPickup:
		if (slot == 0 || slot == 1) && !tr.carried.IsEmpty() {
			if !s.canAcceptEquineSlot(entityID, slot, tr.carried) {
				return
			}
			if slots[slot].IsEmpty() {
				slots[slot] = world.NewItemStack(tr.carried.ResolvedItemID(), 1)
				if tr.carried.Count <= 1 {
					tr.carried = world.ItemStack{}
				} else {
					tr.carried.Count--
				}
			} else if tr.carried.Count == 1 {
				slots[slot], tr.carried = tr.carried, slots[slot]
			}
		} else {
			slots[slot], tr.carried = applyPickupSlot(slots[slot], tr.carried, button)
		}
	case ContainerInputQuickMove:
		if slot < equipAndChest {
			slots[slot] = mergeIntoSlotSlice(slots, slots[slot], equipAndChest+27, totalSlots)
			slots[slot] = mergeIntoSlotSlice(slots, slots[slot], equipAndChest, equipAndChest+27)
		} else {
			cur := slots[slot]
			if !cur.IsEmpty() {
				if slots[0].IsEmpty() && s.canAcceptEquineSlot(entityID, 0, cur) {
					slots[0] = world.NewItemStack(cur.ResolvedItemID(), 1)
					if cur.Count <= 1 {
						slots[slot] = world.ItemStack{}
					} else {
						slots[slot].Count--
					}
				} else if slots[1].IsEmpty() && s.canAcceptEquineSlot(entityID, 1, cur) {
					slots[1] = world.NewItemStack(cur.ResolvedItemID(), 1)
					if cur.Count <= 1 {
						slots[slot] = world.ItemStack{}
					} else {
						slots[slot].Count--
					}
				} else if chestSlots > 0 {
					slots[slot] = mergeIntoSlotSlice(slots, cur, 2, equipAndChest)
				}
			}
		}
	case ContainerInputSwap:
		if button >= 0 && button < 9 {
			hotbarIdx := equipAndChest + 27 + int(button)
			if slot == 0 || slot == 1 {
				if !slots[hotbarIdx].IsEmpty() && (!s.canAcceptEquineSlot(entityID, slot, slots[hotbarIdx]) || slots[hotbarIdx].Count > 1) {
					return
				}
			}
			slots[slot], slots[hotbarIdx] = slots[hotbarIdx], slots[slot]
		}
	case ContainerInputClone:
		if session.Snapshot().GameMode == GameModeCreative && tr.carried.IsEmpty() && !slots[slot].IsEmpty() {
			tr.carried = world.NewItemStack(slots[slot].ResolvedItemID(), MaxItemStackSize)
		}
	}

	// Sync slot 0: Saddle
	wantSaddled := !slots[0].IsEmpty() && canonicalResourceName(slots[0].ResolvedID()) == "minecraft:saddle"
	if wantSaddled != s.IsEquineSaddled(entityID) {
		s.EquipEquineSaddle(entityID, wantSaddled)
	}

	// Sync slot 1: Horse armor or Llama carpet
	if ent := s.entities.Get(entityID); ent != nil {
		norm := canonicalResourceName(ent.TypeName)
		switch norm {
		case MobTypeHorse:
			curArmor := s.HorseArmorItem(entityID)
			newItem := ""
			if !slots[1].IsEmpty() {
				newItem = canonicalResourceName(slots[1].ResolvedID())
			}
			if newItem != curArmor {
				if curArmor != "" {
					_, _ = s.RemoveHorseArmor(entityID)
				}
				if newItem != "" && IsHorseArmorItem(newItem) {
					s.EquipHorseArmor(entityID, slots[1])
				}
			}
		case MobTypeLlama, MobTypeTraderLlama:
			curCarpet := s.LlamaCarpetItem(entityID)
			newItem := ""
			if !slots[1].IsEmpty() {
				newItem = canonicalResourceName(slots[1].ResolvedID())
			}
			if newItem != curCarpet {
				if newItem == "" {
					mgr := s.equineManager()
					mgr.mu.Lock()
					if st := mgr.equines[entityID]; st != nil {
						st.CarpetColor = -1
					}
					mgr.mu.Unlock()
					cp := ent.ChunkPos()
					s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, protocol.PlaySetEquipment,
						EncodeBodyEquipmentPacket(entityID, world.ItemStack{}))
				} else if color, ok := CarpetColorByItemName(newItem); ok {
					s.EquipLlamaCarpet(entityID, color)
				}
			}
		}
	}

	// Sync chest slots 2 .. 2+chestSlots-1
	for i := 0; i < chestSlots; i++ {
		s.SetEquineInventorySlot(entityID, i, slots[2+i])
	}

	// Sync player inventory
	for i := 0; i < 27; i++ {
		session.SetInventorySlot(9+i, slots[equipAndChest+i])
	}
	for i := 0; i < 9; i++ {
		session.SetInventorySlot(36+i, slots[equipAndChest+27+i])
	}
}

func swapPlayerSlotExtendedComponents(session *PlayerSession, slotA, slotB int) {
	if session == nil || slotA < 0 || slotA >= world.PlayerInventorySlots || slotB < 0 || slotB >= world.PlayerInventorySlots || slotA == slotB {
		return
	}
	bundleA := GetSlotBundleContents(session, slotA)
	bundleB := GetSlotBundleContents(session, slotB)
	SetSlotBundleContents(session, slotA, bundleB)
	SetSlotBundleContents(session, slotB, bundleA)

	hornA := GetSlotGoatHornInstrument(session, slotA)
	hornB := GetSlotGoatHornInstrument(session, slotB)
	SetSlotGoatHornInstrument(session, slotA, hornB)
	SetSlotGoatHornInstrument(session, slotB, hornA)

	potA := GetSlotPotionType(session, slotA)
	potB := GetSlotPotionType(session, slotB)
	SetSlotPotionType(session, slotA, potB)
	SetSlotPotionType(session, slotB, potA)

	mapA, hasMapA := GetSlotMapID(session, slotA)
	mapB, hasMapB := GetSlotMapID(session, slotB)
	if hasMapB {
		SetSlotMapID(session, slotA, mapB)
	} else {
		ClearSlotMapID(session, slotA)
	}
	if hasMapA {
		SetSlotMapID(session, slotB, mapA)
	} else {
		ClearSlotMapID(session, slotB)
	}

	wbA, hasWBA := GetSlotWritableBook(session, slotA)
	wbB, hasWBB := GetSlotWritableBook(session, slotB)
	if hasWBB {
		SetSlotWritableBook(session, slotA, wbB)
	} else {
		ClearSlotWritableBook(session, slotA)
	}
	if hasWBA {
		SetSlotWritableBook(session, slotB, wbA)
	} else {
		ClearSlotWritableBook(session, slotB)
	}

	wrbA, hasWRBA := GetSlotWrittenBook(session, slotA)
	wrbB, hasWRBB := GetSlotWrittenBook(session, slotB)
	if hasWRBB {
		SetSlotWrittenBook(session, slotA, wrbB)
	} else {
		ClearSlotWrittenBook(session, slotA)
	}
	if hasWRBA {
		SetSlotWrittenBook(session, slotB, wrbA)
	} else {
		ClearSlotWrittenBook(session, slotB)
	}
}

func (s *Server) syncCartographyContainer(session *PlayerSession) error {
	if s == nil || session == nil || session.send == nil {
		return nil
	}
	tr := getContainerTracker(session)
	tr.mu.Lock()
	if tr.open == nil || tr.open.MenuType != MenuTypeCartographyTable {
		tr.mu.Unlock()
		return nil
	}
	cid := tr.open.ContainerID
	mapSlot := tr.open.Crafting[0]
	addSlot := tr.open.Crafting[1]
	resSlot := tr.open.Result
	carried := tr.carried
	tr.mu.Unlock()

	slots := s.buildCartographyMenuSlots(session, mapSlot, addSlot, resSlot)
	stateID := session.NextContainerStateID()
	if err := session.send(protocol.PlayContainerSetContent, EncodeContainerContent(cid, stateID, slots, carried)); err != nil {
		return err
	}
	s.syncCartographyMapSlots(session, cid)
	return nil
}

func (s *Server) syncCartographyMapSlots(session *PlayerSession, containerID int32) {
	if s == nil || session == nil || session.send == nil {
		return
	}
	tr := getContainerTracker(session)
	tr.mu.Lock()
	if tr.open == nil || tr.open.ContainerID != containerID || tr.open.MenuType != MenuTypeCartographyTable {
		tr.mu.Unlock()
		return
	}
	mapSlot := tr.open.Crafting[0]
	resSlot := tr.open.Result
	carried := tr.carried
	tr.mu.Unlock()

	mtr := getMapMetaTracker(session)
	mtr.mu.Lock()
	cartoMapID, hasCartoMap := mtr.cartoMapID, mtr.hasCartoMap
	resMapID, hasResMap := mtr.resMapID, mtr.hasResMap
	resPostProc, hasResPostProc := mtr.resPostProc, mtr.hasResPostProc
	carriedMapID, hasCarried := mtr.carriedMapID, mtr.hasCarried
	mtr.mu.Unlock()

	if !mapSlot.IsEmpty() && mapSlot.ResolvedID() == FilledMapItemID && hasCartoMap {
		_ = session.send(protocol.PlayContainerSetSlot, encodeContainerSetSlotWithMapID(containerID, session.NextContainerStateID(), CartographySlotMap, mapSlot, cartoMapID, true))
	}
	if !resSlot.IsEmpty() && resSlot.ResolvedID() == FilledMapItemID && hasResMap {
		_ = session.send(protocol.PlayContainerSetSlot, encodeContainerSetSlotWithMapIDAndPostProcessing(containerID, session.NextContainerStateID(), CartographySlotResult, resSlot, resMapID, true, resPostProc, hasResPostProc))
	}
	if !carried.IsEmpty() && carried.ResolvedID() == FilledMapItemID && hasCarried {
		_ = session.send(protocol.PlayContainerSetSlot, encodeContainerSetSlotWithMapID(-1, session.NextContainerStateID(), -1, carried, carriedMapID, true))
	}
	inv := session.Inventory()
	for menuSlot := 3; menuSlot < CartographyMenuSlotCount; menuSlot++ {
		invSlot := 9 + (menuSlot - 3)
		st := inv[invSlot]
		if !st.IsEmpty() && st.ResolvedID() == FilledMapItemID {
			if id, ok := GetSlotMapID(session, invSlot); ok {
				_ = session.send(protocol.PlayContainerSetSlot, encodeContainerSetSlotWithMapID(containerID, session.NextContainerStateID(), menuSlot, st, id, true))
			}
		}
	}
}

func (s *Server) syncChestMapSlots(session *PlayerSession, containerID int32, bx, by, bz int) {
	if s == nil || session == nil || session.send == nil {
		return
	}
	chest := s.ChestSlots(bx, by, bz)
	for i := 0; i < ChestSlotCount; i++ {
		if !chest[i].IsEmpty() && chest[i].ResolvedID() == FilledMapItemID {
			if id, ok := s.GetChestSlotMapID(bx, by, bz, i); ok {
				_ = session.send(protocol.PlayContainerSetSlot, encodeContainerSetSlotWithMapID(containerID, session.NextContainerStateID(), i, chest[i], id, true))
				if data, exists := s.GetMapData(id); exists && data != nil {
					s.refreshMapDecorations(data)
					_ = session.send(PlayMapItemDataCB, EncodeMapItemDataPacket(id, data.Scale, data.Locked, data.DecorationList(), data.FullMapColorPatch()))
				}
			}
		}
	}
	inv := session.Inventory()
	for i := 0; i < 27; i++ {
		st := inv[9+i]
		if !st.IsEmpty() && st.ResolvedID() == FilledMapItemID {
			if id, ok := GetSlotMapID(session, 9+i); ok {
				_ = session.send(protocol.PlayContainerSetSlot, encodeContainerSetSlotWithMapID(containerID, session.NextContainerStateID(), 27+i, st, id, true))
			}
		}
	}
	for i := 0; i < 9; i++ {
		st := inv[36+i]
		if !st.IsEmpty() && st.ResolvedID() == FilledMapItemID {
			if id, ok := GetSlotMapID(session, 36+i); ok {
				_ = session.send(protocol.PlayContainerSetSlot, encodeContainerSetSlotWithMapID(containerID, session.NextContainerStateID(), 54+i, st, id, true))
			}
		}
	}
	carried := session.CarriedItem()
	if !carried.IsEmpty() && carried.ResolvedID() == FilledMapItemID {
		if id, ok := GetCarriedMapID(session); ok {
			_ = session.send(protocol.PlayContainerSetSlot, encodeContainerSetSlotWithMapID(-1, session.NextContainerStateID(), -1, carried, id, true))
		}
	}
}

// --- Bundles (minecraft:bundle & 16 colored bundles, vanilla 26.1.2) ---

const (
	// DataComponentBundleContents is BuiltInRegistries.DATA_COMPONENT_TYPE index 50 ("minecraft:bundle_contents").
	DataComponentBundleContents int32 = 50

	// MaxBundleWeight is the maximum total weight a bundle can hold (64 weight units).
	MaxBundleWeight int32 = 64

	// BundleNestingBaseWeight is the base weight overhead of an empty nested bundle inside another bundle (4 units).
	BundleNestingBaseWeight int32 = 4

	// BundleItemID is the canonical item ID for the uncolored bundle.
	BundleItemID = "minecraft:bundle"
)

// IsBundleItem reports whether itemID is `minecraft:bundle` or one of the 16 colored `minecraft:*_bundle` items.
func IsBundleItem(itemID string) bool {
	norm := canonicalResourceName(itemID)
	switch norm {
	case "minecraft:bundle",
		"minecraft:white_bundle",
		"minecraft:orange_bundle",
		"minecraft:magenta_bundle",
		"minecraft:light_blue_bundle",
		"minecraft:yellow_bundle",
		"minecraft:lime_bundle",
		"minecraft:pink_bundle",
		"minecraft:gray_bundle",
		"minecraft:light_gray_bundle",
		"minecraft:cyan_bundle",
		"minecraft:purple_bundle",
		"minecraft:blue_bundle",
		"minecraft:brown_bundle",
		"minecraft:green_bundle",
		"minecraft:red_bundle",
		"minecraft:black_bundle":
		return true
	default:
		return false
	}
}

// CanInsertIntoBundle reports whether an item of type itemID can be placed inside a bundle.
// In vanilla 26.1.2, shulker boxes cannot be placed inside bundles, whereas nested bundles are allowed.
func CanInsertIntoBundle(itemID string) bool {
	norm := canonicalResourceName(itemID)
	if norm == "" || norm == "minecraft:air" {
		return false
	}
	if norm == "minecraft:shulker_box" || strings.HasSuffix(norm, "_shulker_box") {
		return false
	}
	return true
}

// MaxStackSizeForBundleWeight returns the vanilla maximum stack size (1, 16, or 64)
// of itemID for bundle weight calculation (`64 / maxStackSize`).
func MaxStackSizeForBundleWeight(itemID string) int32 {
	norm := canonicalResourceName(itemID)
	if norm == "" {
		return 64
	}
	if IsBundleItem(norm) || IsShulkerBoxItemOrBlock(norm) || MaxItemDurability(norm) > 0 {
		return 1
	}
	switch norm {
	case "minecraft:enchanted_book",
		"minecraft:writable_book",
		"minecraft:knowledge_book",
		"minecraft:potion",
		"minecraft:splash_potion",
		"minecraft:lingering_potion",
		"minecraft:milk_bucket",
		"minecraft:water_bucket",
		"minecraft:lava_bucket",
		"minecraft:powder_snow_bucket",
		"minecraft:axolotl_bucket",
		"minecraft:cod_bucket",
		"minecraft:salmon_bucket",
		"minecraft:tropical_fish_bucket",
		"minecraft:pufferfish_bucket",
		"minecraft:tadpole_bucket",
		"minecraft:totem_of_undying",
		"minecraft:spyglass",
		"minecraft:goat_horn",
		"minecraft:brush",
		"minecraft:saddle",
		"minecraft:mushroom_stew",
		"minecraft:rabbit_stew",
		"minecraft:beetroot_soup",
		"minecraft:suspicious_stew",
		"minecraft:cake",
		"minecraft:Minecart",
		"minecraft:minecart",
		"minecraft:chest_minecart",
		"minecraft:furnace_minecart",
		"minecraft:tnt_minecart",
		"minecraft:hopper_minecart":
		return 1
	case "minecraft:ender_pearl",
		"minecraft:egg",
		"minecraft:snowball",
		"minecraft:honey_bottle",
		"minecraft:bucket",
		"minecraft:armor_stand",
		"minecraft:written_book":
		return 16
	}
	if strings.HasPrefix(norm, "minecraft:music_disc_") ||
		strings.HasSuffix(norm, "_boat") ||
		strings.HasSuffix(norm, "_raft") ||
		strings.HasSuffix(norm, "_bed") ||
		strings.HasSuffix(norm, "_horse_armor") ||
		strings.HasSuffix(norm, "_banner_pattern") {
		return 1
	}
	if strings.HasSuffix(norm, "_banner") || strings.HasSuffix(norm, "_sign") {
		return 16
	}
	return 64
}

// BundleSingleItemWeight returns the weight (1..64) of a single item of type itemID.
// For a nested bundle, weight is `4 + BundleTotalWeight(nestedContents)`.
func BundleSingleItemWeight(itemID string, nestedContents []world.ItemStack) int32 {
	if IsBundleItem(itemID) {
		w := BundleNestingBaseWeight + BundleTotalWeight(nestedContents)
		if w > MaxBundleWeight {
			w = MaxBundleWeight
		}
		return w
	}
	maxStack := MaxStackSizeForBundleWeight(itemID)
	if maxStack <= 1 {
		return MaxBundleWeight
	}
	return MaxBundleWeight / maxStack
}

// BundleTotalWeight computes the total weight of all stacks stored inside a bundle.
func BundleTotalWeight(items []world.ItemStack) int32 {
	var total int32
	for _, st := range items {
		if st.IsEmpty() || st.Count <= 0 {
			continue
		}
		unit := BundleSingleItemWeight(st.ResolvedID(), nil)
		total += unit * st.Count
	}
	return total
}

type sessionBundleTracker struct {
	mu          sync.Mutex
	slotItems   [world.PlayerInventorySlots][]world.ItemStack
	selectedIdx [world.PlayerInventorySlots]int
	carried     []world.ItemStack
}

type serverDroppedBundleStore struct {
	mu    sync.Mutex
	items map[int32][]world.ItemStack
}

var (
	playerBundles        sync.Map // map[*PlayerSession]*sessionBundleTracker
	serverDroppedBundles sync.Map // map[*Server]*serverDroppedBundleStore
)

func getBundleTracker(session *PlayerSession) *sessionBundleTracker {
	if session == nil {
		return nil
	}
	if v, ok := playerBundles.Load(session); ok {
		return v.(*sessionBundleTracker)
	}
	tr := &sessionBundleTracker{}
	for i := range tr.selectedIdx {
		tr.selectedIdx[i] = -1
	}
	actual, _ := playerBundles.LoadOrStore(session, tr)
	return actual.(*sessionBundleTracker)
}

func (s *Server) droppedBundleStore() *serverDroppedBundleStore {
	if s == nil {
		return nil
	}
	if v, ok := serverDroppedBundles.Load(s); ok {
		return v.(*serverDroppedBundleStore)
	}
	actual, _ := serverDroppedBundles.LoadOrStore(s, &serverDroppedBundleStore{
		items: make(map[int32][]world.ItemStack),
	})
	return actual.(*serverDroppedBundleStore)
}

func cloneBundleItems(items []world.ItemStack) []world.ItemStack {
	if len(items) == 0 {
		return nil
	}
	out := make([]world.ItemStack, 0, len(items))
	for _, st := range items {
		if !st.IsEmpty() && st.Count > 0 {
			out = append(out, world.NewItemStack(st.ResolvedItemID(), st.Count))
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// GetSlotBundleContents returns a copy of the items stored inside the bundle in slot (0..45).
func GetSlotBundleContents(session *PlayerSession, slot int) []world.ItemStack {
	if session == nil || slot < 0 || slot >= world.PlayerInventorySlots {
		return nil
	}
	tr := getBundleTracker(session)
	tr.mu.Lock()
	cp := cloneBundleItems(tr.slotItems[slot])
	tr.mu.Unlock()
	return cp
}

// SetSlotBundleContents sets the items stored inside the bundle in slot (0..45).
func SetSlotBundleContents(session *PlayerSession, slot int, items []world.ItemStack) {
	if session == nil || slot < 0 || slot >= world.PlayerInventorySlots {
		return
	}
	tr := getBundleTracker(session)
	tr.mu.Lock()
	tr.slotItems[slot] = cloneBundleItems(items)
	if len(tr.slotItems[slot]) == 0 {
		tr.selectedIdx[slot] = -1
	} else if tr.selectedIdx[slot] >= len(tr.slotItems[slot]) {
		tr.selectedIdx[slot] = len(tr.slotItems[slot]) - 1
	}
	tr.mu.Unlock()
}

// ClearSlotBundleContents clears any bundle contents and selected index on slot (0..45).
func ClearSlotBundleContents(session *PlayerSession, slot int) {
	SetSlotBundleContents(session, slot, nil)
}

// GetSlotBundleSelectedIndex returns the currently selected item index inside the bundle at slot (-1 if none).
func GetSlotBundleSelectedIndex(session *PlayerSession, slot int) int {
	if session == nil || slot < 0 || slot >= world.PlayerInventorySlots {
		return -1
	}
	tr := getBundleTracker(session)
	tr.mu.Lock()
	idx := tr.selectedIdx[slot]
	tr.mu.Unlock()
	return idx
}

// SetSlotBundleSelectedIndex sets the selected item index inside the bundle at slot.
func SetSlotBundleSelectedIndex(session *PlayerSession, slot int, selectedIndex int) {
	if session == nil || slot < 0 || slot >= world.PlayerInventorySlots {
		return
	}
	tr := getBundleTracker(session)
	tr.mu.Lock()
	if selectedIndex < 0 || selectedIndex >= len(tr.slotItems[slot]) {
		tr.selectedIdx[slot] = -1
	} else {
		tr.selectedIdx[slot] = selectedIndex
	}
	tr.mu.Unlock()
}

// GetCarriedBundleContents returns a copy of the bundle contents on the player's cursor.
func GetCarriedBundleContents(session *PlayerSession) []world.ItemStack {
	if session == nil {
		return nil
	}
	tr := getBundleTracker(session)
	tr.mu.Lock()
	cp := cloneBundleItems(tr.carried)
	tr.mu.Unlock()
	return cp
}

// SetCarriedBundleContents sets the bundle contents on the player's cursor.
func SetCarriedBundleContents(session *PlayerSession, items []world.ItemStack) {
	if session == nil {
		return
	}
	tr := getBundleTracker(session)
	tr.mu.Lock()
	tr.carried = cloneBundleItems(items)
	tr.mu.Unlock()
}

// AssociateDroppedBundleEntity records the bundle contents for a dropped bundle item entity.
func (s *Server) AssociateDroppedBundleEntity(entityID int32, items []world.ItemStack) {
	if s == nil || entityID <= 0 || len(items) == 0 {
		return
	}
	st := s.droppedBundleStore()
	st.mu.Lock()
	st.items[entityID] = cloneBundleItems(items)
	st.mu.Unlock()
}

// DroppedBundleContents returns the bundle contents associated with a dropped item entity.
func (s *Server) DroppedBundleContents(entityID int32) ([]world.ItemStack, bool) {
	if s == nil || entityID <= 0 {
		return nil, false
	}
	st := s.droppedBundleStore()
	st.mu.Lock()
	items, ok := st.items[entityID]
	cp := cloneBundleItems(items)
	st.mu.Unlock()
	return cp, ok && len(cp) > 0
}

// HandleBundleItemSelectedPacket decodes ServerboundBundleItemSelectedPacket (0x03 in protocol 775):
// VarInt(slotId), VarInt(selectedItemIndex).
func (s *Server) HandleBundleItemSelectedPacket(session *PlayerSession, r *protocol.Reader) error {
	if s == nil || session == nil || r == nil {
		return nil
	}
	slotID, err := r.VarInt()
	if err != nil {
		return err
	}
	selectedIdx, err := r.VarInt()
	if err != nil {
		return err
	}
	SetSlotBundleSelectedIndex(session, int(slotID), int(selectedIdx))
	return nil
}

// EncodeBundleContentsComponent writes the payload of `DataComponentBundleContents` (50):
// VarInt(len(items)), followed by each ItemStack.
func EncodeBundleContentsComponent(w *protocol.Writer, items []world.ItemStack) {
	w.VarInt(int32(len(items)))
	for _, it := range items {
		writeContainerItemStack(w, it)
	}
}

// EncodeSlotWithBundleContents writes a protocol 775 ItemStack carrying a
// `minecraft:bundle_contents` (50) component.
func EncodeSlotWithBundleContents(w *protocol.Writer, stack world.ItemStack, items []world.ItemStack) {
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
	w.VarInt(DataComponentBundleContents)
	EncodeBundleContentsComponent(w, items)
}

func insertStackIntoBundleList(bundleItems []world.ItemStack, incoming world.ItemStack, incomingNested []world.ItemStack) (updated []world.ItemStack, inserted int32) {
	if incoming.IsEmpty() || incoming.Count <= 0 || !CanInsertIntoBundle(incoming.ResolvedID()) {
		return bundleItems, 0
	}
	curWeight := BundleTotalWeight(bundleItems)
	unitWeight := BundleSingleItemWeight(incoming.ResolvedID(), incomingNested)
	if unitWeight <= 0 || curWeight+unitWeight > MaxBundleWeight {
		return bundleItems, 0
	}
	maxInsert := (MaxBundleWeight - curWeight) / unitWeight
	if maxInsert <= 0 {
		return bundleItems, 0
	}
	toInsert := incoming.Count
	if toInsert > maxInsert {
		toInsert = maxInsert
	}
	itemID := incoming.ResolvedItemID()
	maxStack := MaxStackSizeForBundleWeight(incoming.ResolvedID())

	out := cloneBundleItems(bundleItems)
	rem := toInsert
	if maxStack > 1 {
		for i := range out {
			if out[i].ResolvedItemID() == itemID && out[i].Count < maxStack {
				space := maxStack - out[i].Count
				add := rem
				if add > space {
					add = space
				}
				out[i] = world.NewItemStack(itemID, out[i].Count+add)
				rem -= add
				if rem <= 0 {
					break
				}
			}
		}
	}
	for rem > 0 {
		add := rem
		if add > maxStack {
			add = maxStack
		}
		// Prepend newly inserted stack at index 0 (most-recently-added item is extracted first in vanilla).
		out = append([]world.ItemStack{world.NewItemStack(itemID, add)}, out...)
		rem -= add
	}
	return out, toInsert
}

func extractOneStackFromBundleList(bundleItems []world.ItemStack, selectedIdx int) (extracted world.ItemStack, remaining []world.ItemStack, ok bool) {
	if len(bundleItems) == 0 {
		return world.ItemStack{}, nil, false
	}
	idx := 0
	if selectedIdx >= 0 && selectedIdx < len(bundleItems) {
		idx = selectedIdx
	}
	extracted = bundleItems[idx]
	remaining = make([]world.ItemStack, 0, len(bundleItems)-1)
	remaining = append(remaining, bundleItems[:idx]...)
	remaining = append(remaining, bundleItems[idx+1:]...)
	return extracted, remaining, true
}

func (s *Server) tryApplyBundlePickupClick(
	session *PlayerSession,
	slot int,
	slots []world.ItemStack,
	metas []ItemMeta,
	carried *world.ItemStack,
) bool {
	if session == nil || slot < 0 || slot >= len(slots) || carried == nil {
		return false
	}
	slotStack := slots[slot]
	curCarried := *carried
	snap := session.Snapshot()
	dim := world.NormalizeDimension(snap.Dimension)
	cx := int32(snap.X) >> 4
	cz := int32(snap.Z) >> 4

	// 1. Bundle in slot: right-clicking with carried item inserts into slot bundle;
	//    right-clicking with empty cursor extracts selected/first item onto cursor.
	if !slotStack.IsEmpty() && IsBundleItem(slotStack.ResolvedID()) && slotStack.Count == 1 {
		bundleItems := GetSlotBundleContents(session, slot)
		if !curCarried.IsEmpty() {
			var nested []world.ItemStack
			if IsBundleItem(curCarried.ResolvedID()) {
				nested = GetCarriedBundleContents(session)
			}
			updated, inserted := insertStackIntoBundleList(bundleItems, curCarried, nested)
			if inserted <= 0 {
				return true
			}
			SetSlotBundleContents(session, slot, updated)
			rem := curCarried.Count - inserted
			if rem <= 0 {
				*carried = world.ItemStack{}
				SetCarriedItemMeta(session, ItemMeta{})
				SetCarriedBundleContents(session, nil)
			} else {
				*carried = world.NewItemStack(curCarried.ResolvedItemID(), rem)
			}
			if s != nil {
				s.BroadcastDimensionChunk(dim, cx, cz, PlaySoundPacketID,
					EncodeSoundPacket("minecraft:item.bundle.insert", SoundSourcePlayers, snap.X, snap.Y, snap.Z, 1.0, 1.0, 0))
			}
			return true
		}
		extracted, remItems, ok := extractOneStackFromBundleList(bundleItems, GetSlotBundleSelectedIndex(session, slot))
		if !ok {
			return false
		}
		SetSlotBundleContents(session, slot, remItems)
		SetSlotBundleSelectedIndex(session, slot, -1)
		*carried = extracted
		SetCarriedItemMeta(session, ItemMeta{})
		if s != nil {
			s.BroadcastDimensionChunk(dim, cx, cz, PlaySoundPacketID,
				EncodeSoundPacket("minecraft:item.bundle.remove_one", SoundSourcePlayers, snap.X, snap.Y, snap.Z, 1.0, 1.0, 0))
		}
		return true
	}

	// 2. Bundle on cursor: right-clicking a non-empty slot inserts from slot into cursor bundle;
	//    right-clicking an empty slot extracts first item from cursor bundle into slot.
	if !curCarried.IsEmpty() && IsBundleItem(curCarried.ResolvedID()) && curCarried.Count == 1 {
		bundleItems := GetCarriedBundleContents(session)
		if !slotStack.IsEmpty() {
			var nested []world.ItemStack
			if IsBundleItem(slotStack.ResolvedID()) {
				nested = GetSlotBundleContents(session, slot)
			}
			updated, inserted := insertStackIntoBundleList(bundleItems, slotStack, nested)
			if inserted <= 0 {
				return true
			}
			SetCarriedBundleContents(session, updated)
			rem := slotStack.Count - inserted
			if rem <= 0 {
				slots[slot] = world.ItemStack{}
				metas[slot] = ItemMeta{}
				ClearSlotBundleContents(session, slot)
			} else {
				slots[slot] = world.NewItemStack(slotStack.ResolvedItemID(), rem)
			}
			if s != nil {
				s.BroadcastDimensionChunk(dim, cx, cz, PlaySoundPacketID,
					EncodeSoundPacket("minecraft:item.bundle.remove_one", SoundSourcePlayers, snap.X, snap.Y, snap.Z, 1.0, 1.0, 0))
			}
			return true
		}
		extracted, remItems, ok := extractOneStackFromBundleList(bundleItems, 0)
		if !ok {
			return false
		}
		SetCarriedBundleContents(session, remItems)
		slots[slot] = extracted
		metas[slot] = ItemMeta{}
		if s != nil {
			s.BroadcastDimensionChunk(dim, cx, cz, PlaySoundPacketID,
				EncodeSoundPacket("minecraft:item.bundle.remove_one", SoundSourcePlayers, snap.X, snap.Y, snap.Z, 1.0, 1.0, 0))
		}
		return true
	}
	return false
}

type sessionCarriedBookTracker struct {
	mu       sync.Mutex
	writable *WritableBookContent
	written  *WrittenBookContent
}

var playerCarriedBooks sync.Map // map[*PlayerSession]*sessionCarriedBookTracker

func getCarriedBookTracker(session *PlayerSession) *sessionCarriedBookTracker {
	if session == nil {
		return nil
	}
	if v, ok := playerCarriedBooks.Load(session); ok {
		return v.(*sessionCarriedBookTracker)
	}
	actual, _ := playerCarriedBooks.LoadOrStore(session, &sessionCarriedBookTracker{})
	return actual.(*sessionCarriedBookTracker)
}

// ClearSlotWritableBook clears any WritableBookContent on player inventory slot (0..45).
func ClearSlotWritableBook(session *PlayerSession, slot int) {
	if session == nil || slot < 0 || slot >= world.PlayerInventorySlots {
		return
	}
	tr := getBookMetaTracker(session)
	tr.mu.Lock()
	tr.writable[slot] = nil
	tr.mu.Unlock()
}

// ClearSlotWrittenBook clears any WrittenBookContent on player inventory slot (0..45).
func ClearSlotWrittenBook(session *PlayerSession, slot int) {
	if session == nil || slot < 0 || slot >= world.PlayerInventorySlots {
		return
	}
	tr := getBookMetaTracker(session)
	tr.mu.Lock()
	tr.written[slot] = nil
	tr.mu.Unlock()
}

// GetCarriedWritableBook returns the WritableBookContent on the player's cursor, if any.
func GetCarriedWritableBook(session *PlayerSession) (WritableBookContent, bool) {
	if session == nil {
		return WritableBookContent{}, false
	}
	tr := getCarriedBookTracker(session)
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if tr.writable == nil {
		return WritableBookContent{}, false
	}
	return tr.writable.Clone(), true
}

// SetCarriedWritableBook sets or clears the WritableBookContent on the player's cursor.
func SetCarriedWritableBook(session *PlayerSession, content WritableBookContent, has bool) {
	if session == nil {
		return
	}
	tr := getCarriedBookTracker(session)
	tr.mu.Lock()
	if !has {
		tr.writable = nil
	} else {
		cp := content.Clone()
		tr.writable = &cp
	}
	tr.mu.Unlock()
}

// GetCarriedWrittenBook returns the WrittenBookContent on the player's cursor, if any.
func GetCarriedWrittenBook(session *PlayerSession) (WrittenBookContent, bool) {
	if session == nil {
		return WrittenBookContent{}, false
	}
	tr := getCarriedBookTracker(session)
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if tr.written == nil {
		return WrittenBookContent{}, false
	}
	return tr.written.Clone(), true
}

// SetCarriedWrittenBook sets or clears the WrittenBookContent on the player's cursor.
func SetCarriedWrittenBook(session *PlayerSession, content WrittenBookContent, has bool) {
	if session == nil {
		return
	}
	tr := getCarriedBookTracker(session)
	tr.mu.Lock()
	if !has {
		tr.written = nil
	} else {
		cp := content.Clone()
		tr.written = &cp
	}
	tr.mu.Unlock()
}

// QuickcraftType extracts the drag type ((button >> 2) & 3) from a QuickCraft click button.
func QuickcraftType(button int8) int8 {
	return (button >> 2) & 3
}

// QuickcraftHeader extracts the drag phase header (button & 3) from a QuickCraft click button.
func QuickcraftHeader(button int8) int8 {
	return button & 3
}

// QuickcraftMask packs a QuickCraft header (0..2) and drag type (0..2) into a button byte.
func QuickcraftMask(header, dragType int8) int8 {
	return (header & 3) | ((dragType & 3) << 2)
}

// IsValidQuickcraftType reports whether dragType is allowed for gameMode
// (matching AbstractContainerMenu.isValidQuickcraftType in 26.1.2).
func IsValidQuickcraftType(dragType int8, gameMode uint8) bool {
	if dragType == QuickCraftTypeClone {
		return gameMode == GameModeCreative
	}
	return dragType == QuickCraftTypeCharitable || dragType == QuickCraftTypeGreedy
}

// QuickCraftPlaceCount computes the number of items placed into each dragged slot
// (matching AbstractContainerMenu.getQuickCraftPlaceCount in 26.1.2).
func QuickCraftPlaceCount(numSlots int, dragType int8, carried world.ItemStack) int32 {
	if carried.IsEmpty() || numSlots <= 0 {
		return 0
	}
	maxStack := MaxStackSizeForBundleWeight(carried.ResolvedID())
	switch dragType {
	case QuickCraftTypeCharitable:
		return carried.Count / int32(numSlots)
	case QuickCraftTypeGreedy:
		return 1
	case QuickCraftTypeClone:
		return maxStack
	default:
		return carried.Count
	}
}

// ResetContainerQuickCraft clears any in-progress QuickCraft drag on session.
func ResetContainerQuickCraft(session *PlayerSession) {
	if session == nil {
		return
	}
	tr := getContainerTracker(session)
	tr.mu.Lock()
	tr.dragStatus = 0
	tr.dragType = 0
	tr.dragSlots = nil
	tr.mu.Unlock()
}

func isQuickCraftResultOrInvalidSlot(session *PlayerSession, tr *playerContainerTracker, containerID int32, slot int, carried world.ItemStack) bool {
	if slot < 0 || carried.IsEmpty() {
		return true
	}
	if containerID == 0 {
		if slot == 0 || slot >= world.PlayerInventorySlots {
			return true
		}
		if slot >= ArmorSlotHead && slot <= ArmorSlotFeet {
			if HasBindingCurseOnSlot(session, slot) {
				return true
			}
			if ws, ok := WearableArmorSlotForItem(carried.ResolvedID()); !ok || ws != slot {
				return true
			}
		}
		return false
	}
	if tr.open == nil || tr.open.ContainerID != containerID {
		return true
	}
	switch tr.open.MenuType {
	case MenuTypeGeneric9x3:
		return slot >= ChestMenuSlotCount
	case MenuTypeShulkerBox:
		if slot >= ChestMenuSlotCount {
			return true
		}
		if slot < ChestSlotCount && IsShulkerBoxItemOrBlock(carried.ResolvedID()) {
			return true
		}
		return false
	case MenuTypeGeneric9x6:
		return slot >= DoubleChestMenuSlotCount
	case MenuTypeHopper:
		return slot >= HopperMenuSlotCount
	case MenuTypeGeneric3x3:
		return slot >= 45
	case CrafterMenuType:
		return slot == CrafterMenuResultSlot || slot >= CrafterMenuSlotCount
	case MenuTypeCrafting:
		return slot == 0 || slot >= CraftingMenuSlotCount
	case MenuTypeFurnace, MenuTypeBlastFurnace, MenuTypeSmoker:
		if slot == world.FurnaceSlotOutput || slot >= FurnaceMenuSlotCount {
			return true
		}
		if slot == world.FurnaceSlotFuel && !CanPlaceInFurnaceFuelSlot(carried) {
			return true
		}
		return false
	default:
		return true
	}
}

func (s *Server) handleQuickCraftMutateLocked(
	session *PlayerSession,
	tr *playerContainerTracker,
	containerID int32,
	slot int,
	button int8,
) (mutated bool, sharedDim string, sharedX, sharedY, sharedZ int, hasShared bool) {
	oldStatus := tr.dragStatus
	header := QuickcraftHeader(button)
	dragType := QuickcraftType(button)
	gm := session.Snapshot().GameMode

	if oldStatus == 0 {
		if header == QuickCraftHeaderStart && IsValidQuickcraftType(dragType, gm) && !tr.carried.IsEmpty() {
			tr.dragStatus = 1
			tr.dragType = dragType
			tr.dragSlots = tr.dragSlots[:0]
		} else {
			tr.dragStatus = 0
			tr.dragType = 0
			tr.dragSlots = nil
		}
		return false, "", 0, 0, 0, false
	}

	if header == QuickCraftHeaderContinue {
		if oldStatus == 1 && dragType == tr.dragType && !tr.carried.IsEmpty() && !isQuickCraftResultOrInvalidSlot(session, tr, containerID, slot, tr.carried) {
			already := false
			for _, existing := range tr.dragSlots {
				if existing == slot {
					already = true
					break
				}
			}
			maxStack := MaxStackSizeForBundleWeight(tr.carried.ResolvedID())
			if !already && (tr.dragType == QuickCraftTypeClone || int32(len(tr.dragSlots)+1) <= tr.carried.Count) && maxStack > 1 {
				tr.dragSlots = append(tr.dragSlots, slot)
			} else if !already && len(tr.dragSlots) == 0 && maxStack == 1 {
				tr.dragSlots = append(tr.dragSlots, slot)
			}
		} else if header != QuickCraftHeaderContinue || oldStatus != 1 || dragType != tr.dragType {
			tr.dragStatus = 0
			tr.dragType = 0
			tr.dragSlots = nil
		}
		return false, "", 0, 0, 0, false
	}

	if header == QuickCraftHeaderEnd {
		dragSlots := append([]int(nil), tr.dragSlots...)
		finalType := tr.dragType
		tr.dragStatus = 0
		tr.dragType = 0
		tr.dragSlots = nil

		if oldStatus != 1 || tr.carried.IsEmpty() || len(dragSlots) == 0 {
			return false, "", 0, 0, 0, false
		}
		if len(dragSlots) == 1 && finalType != QuickCraftTypeClone {
			singleSlot := dragSlots[0]
			if containerID == 0 {
				_, mutated = s.applyPlayerInventoryClickDataLocked(session, tr, singleSlot, finalType, ContainerInputPickup)
				return mutated, "", 0, 0, 0, false
			}
			if tr.open != nil && tr.open.ContainerID == containerID {
				switch tr.open.MenuType {
				case MenuTypeGeneric9x3, MenuTypeShulkerBox:
					if tr.open.IsEnderChest {
						_, mutated = s.applyEnderChestClickDataLocked(session, tr, singleSlot, finalType, ContainerInputPickup)
					} else if tr.open.VehicleEntityID == 0 {
						bx, by, bz := tr.open.BlockX, tr.open.BlockY, tr.open.BlockZ
						dim := tr.open.Dimension
						if dim == "" {
							dim = session.Dimension()
						}
						_, mutated = s.applyChestClickDataLocked(session, tr, bx, by, bz, singleSlot, finalType, ContainerInputPickup)
						return mutated, dim, bx, by, bz, mutated
					}
					return mutated, "", 0, 0, 0, false
				case MenuTypeGeneric9x6:
					bx, by, bz := tr.open.BlockX, tr.open.BlockY, tr.open.BlockZ
					px, py, pz := tr.open.PartnerX, tr.open.PartnerY, tr.open.PartnerZ
					dim := tr.open.Dimension
					if dim == "" {
						dim = session.Dimension()
					}
					_, mutated = s.applyDoubleChestClickDataLocked(session, tr, bx, by, bz, px, py, pz, singleSlot, finalType, ContainerInputPickup)
					return mutated, dim, bx, by, bz, mutated
				case MenuTypeCrafting:
					_, mutated = s.applyCraftingTableClickDataLocked(session, tr, singleSlot, finalType, ContainerInputPickup)
					return mutated, "", 0, 0, 0, false
				}
			}
		}
		if finalType == QuickCraftTypeClone && gm != GameModeCreative {
			return false, "", 0, 0, 0, false
		}
		s.applyQuickCraftDistributeLocked(session, tr, containerID, dragSlots, finalType)
		if containerID != 0 && tr.open != nil && tr.open.ContainerID == containerID &&
			tr.open.VehicleEntityID == 0 && !tr.open.IsEnderChest &&
			(tr.open.MenuType == MenuTypeGeneric9x3 || tr.open.MenuType == MenuTypeGeneric9x6 || tr.open.MenuType == MenuTypeShulkerBox) {
			dim := tr.open.Dimension
			if dim == "" {
				dim = session.Dimension()
			}
			return true, dim, tr.open.BlockX, tr.open.BlockY, tr.open.BlockZ, true
		}
		return true, "", 0, 0, 0, false
	}

	tr.dragStatus = 0
	tr.dragType = 0
	tr.dragSlots = nil
	return false, "", 0, 0, 0, false
}

func (s *Server) handleQuickCraftClickLocked(session *PlayerSession, tr *playerContainerTracker, containerID int32, slot int, button int8) error {
	header := QuickcraftHeader(button)
	oldStatus := tr.dragStatus
	if header == QuickCraftHeaderEnd && oldStatus == 1 && !tr.carried.IsEmpty() && len(tr.dragSlots) == 1 && tr.dragType != QuickCraftTypeClone && containerID != 0 {
		// Delegate single-slot non-supported menus (furnace/etc.) via HandleContainerClick.
		singleSlot := tr.dragSlots[0]
		finalType := tr.dragType
		isSupported := tr.open != nil && tr.open.ContainerID == containerID && tr.open.VehicleEntityID == 0 &&
			(tr.open.MenuType == MenuTypeGeneric9x3 || tr.open.MenuType == MenuTypeShulkerBox || tr.open.MenuType == MenuTypeGeneric9x6 || tr.open.MenuType == MenuTypeCrafting)
		if !isSupported {
			tr.dragStatus = 0
			tr.dragType = 0
			tr.dragSlots = nil
			tr.mu.Unlock()
			return s.HandleContainerClick(session, containerID, singleSlot, finalType, ContainerInputPickup)
		}
	}

	mutated, _, sharedX, sharedY, sharedZ, hasShared := s.handleQuickCraftMutateLocked(session, tr, containerID, slot, button)
	tr.mu.Unlock()
	if !mutated {
		if header == QuickCraftHeaderEnd {
			return s.syncOpenContainer(session)
		}
		return nil
	}
	s.TriggerInventoryChanged(session)
	s.UnlockRecipesForInventory(session)
	if containerID == 0 {
		return s.syncPlayerInventoryContainer(session)
	}
	if hasShared {
		return s.syncChestViewers(session, sharedX, sharedY, sharedZ)
	}
	return s.syncOpenContainer(session)
}

func distributeQuickCraftOnSlice(slots []world.ItemStack, carried *world.ItemStack, dragSlots []int, dragType int8, slotMaxStack func(slot int, defaultMax int32) int32) {
	if carried == nil || carried.IsEmpty() || len(dragSlots) == 0 {
		return
	}
	origCarried := *carried
	itemID := origCarried.ResolvedItemID()
	defaultMax := MaxStackSizeForBundleWeight(origCarried.ResolvedID())
	placePerSlot := QuickCraftPlaceCount(len(dragSlots), dragType, origCarried)
	if placePerSlot <= 0 {
		return
	}
	remaining := origCarried.Count
	for _, sl := range dragSlots {
		if sl < 0 || sl >= len(slots) {
			continue
		}
		cur := slots[sl]
		if !cur.IsEmpty() && cur.ResolvedItemID() != itemID {
			continue
		}
		maxForSlot := defaultMax
		if slotMaxStack != nil {
			maxForSlot = slotMaxStack(sl, defaultMax)
		}
		curCount := int32(0)
		if !cur.IsEmpty() {
			curCount = cur.Count
		}
		if curCount >= maxForSlot {
			continue
		}
		add := placePerSlot
		if curCount+add > maxForSlot {
			add = maxForSlot - curCount
		}
		if dragType != QuickCraftTypeClone {
			if add > remaining {
				add = remaining
			}
			remaining -= add
		}
		if add > 0 {
			slots[sl] = world.NewItemStack(itemID, curCount+add)
		}
	}
	if dragType != QuickCraftTypeClone {
		if remaining <= 0 {
			*carried = world.ItemStack{}
		} else {
			*carried = world.NewItemStack(itemID, remaining)
		}
	}
}

func (s *Server) applyQuickCraftDistributeLocked(session *PlayerSession, tr *playerContainerTracker, containerID int32, dragSlots []int, dragType int8) {
	if containerID == 0 {
		for i := 0; i < 4; i++ {
			if tr.invCraft[i].IsEmpty() {
				tr.invCraft[i] = session.InventorySlot(1 + i)
			}
		}
		slots := s.buildPlayerMenuSlotData(session, tr)
		orig := append([]containerSlotData(nil), slots...)
		carried := getCarriedSlotDataLocked(session, tr)
		distributeQuickCraftOnSlotData(slots, &carried, dragSlots, dragType, func(sl int, item containerSlotData) int32 {
			if sl >= ArmorSlotHead && sl <= ArmorSlotFeet {
				return 1
			}
			return item.MaxStackSize()
		})
		for i := 0; i < 4; i++ {
			tr.invCraft[i] = slots[1+i].Stack
			if slots[1+i].Star != nil {
				tr.invCraftStars[i] = slots[1+i].Star.Clone()
			} else if slots[1+i].IsEmpty() {
				tr.invCraftStars[i] = FireworkExplosion{}
			}
		}
		if res, starExp, fwComp, ok := matchCraftingWithFireworks(tr.invCraft[:], 2, 2, tr.invCraftStars[:]); ok {
			tr.invResult = res
			if starExp != nil {
				SetSlotFireworkStar(session, 0, *starExp)
				ClearSlotFireworks(session, 0)
			} else if fwComp != nil {
				SetSlotFireworks(session, 0, *fwComp)
				ClearSlotFireworkStar(session, 0)
			} else {
				ClearSlotFireworkStar(session, 0)
				ClearSlotFireworks(session, 0)
			}
		} else {
			tr.invResult = world.ItemStack{}
			ClearSlotFireworkStar(session, 0)
			ClearSlotFireworks(session, 0)
		}
		slots[0] = containerSlotData{Stack: tr.invResult}
		for i := 0; i < world.PlayerInventorySlots; i++ {
			if i == 0 {
				session.SetInventorySlot(0, tr.invResult)
				continue
			}
			if !slots[i].SameItemSameComponents(orig[i]) || slots[i].Stack.Count != orig[i].Stack.Count {
				setPlayerSlotData(session, i, slots[i])
			}
		}
		setCarriedSlotDataLocked(session, tr, carried)
		return
	}

	if tr.open == nil || tr.open.ContainerID != containerID {
		return
	}
	switch tr.open.MenuType {
	case MenuTypeGeneric9x3, MenuTypeShulkerBox:
		if tr.open.VehicleEntityID != 0 {
			vehID := tr.open.VehicleEntityID
			slots := s.buildVehicleChestMenuSlots(session, vehID)
			distributeQuickCraftOnSlice(slots, &tr.carried, dragSlots, dragType, nil)
			for i := 0; i < ChestSlotCount; i++ {
				s.SetVehicleContainerSlot(vehID, i, slots[i])
			}
			for i := 0; i < 27; i++ {
				session.SetInventorySlot(9+i, slots[27+i])
			}
			for i := 0; i < 9; i++ {
				session.SetInventorySlot(36+i, slots[54+i])
			}
			return
		}
		if tr.open.IsEnderChest {
			slots := s.buildEnderChestMenuSlotData(session, tr)
			orig := append([]containerSlotData(nil), slots...)
			carried := getCarriedSlotDataLocked(session, tr)
			distributeQuickCraftOnSlotData(slots, &carried, dragSlots, dragType, nil)
			for i := 0; i < ChestSlotCount; i++ {
				tr.enderChest[i] = slots[i].Stack
			}
			for i := 0; i < 27; i++ {
				idx := 27 + i
				if !slots[idx].SameItemSameComponents(orig[idx]) || slots[idx].Stack.Count != orig[idx].Stack.Count {
					setPlayerSlotData(session, 9+i, slots[idx])
				}
			}
			for i := 0; i < 9; i++ {
				idx := 54 + i
				if !slots[idx].SameItemSameComponents(orig[idx]) || slots[idx].Stack.Count != orig[idx].Stack.Count {
					setPlayerSlotData(session, 36+i, slots[idx])
				}
			}
			setCarriedSlotDataLocked(session, tr, carried)
			return
		}
		bx, by, bz := tr.open.BlockX, tr.open.BlockY, tr.open.BlockZ
		openDim := tr.open.Dimension
		if openDim == "" && session != nil {
			openDim = session.Dimension()
		}
		slots := s.buildChestMenuSlotDataInDim(session, openDim, bx, by, bz)
		orig := append([]containerSlotData(nil), slots...)
		carried := getCarriedSlotDataLocked(session, tr)
		distributeQuickCraftOnSlotData(slots, &carried, dragSlots, dragType, nil)
		for i := 0; i < ChestSlotCount; i++ {
			if !slots[i].SameItemSameComponents(orig[i]) || slots[i].Stack.Count != orig[i].Stack.Count {
				s.setStorageSlotData(openDim, bx, by, bz, i, slots[i])
			}
		}
		for i := 0; i < 27; i++ {
			idx := 27 + i
			if !slots[idx].SameItemSameComponents(orig[idx]) || slots[idx].Stack.Count != orig[idx].Stack.Count {
				setPlayerSlotData(session, 9+i, slots[idx])
			}
		}
		for i := 0; i < 9; i++ {
			idx := 54 + i
			if !slots[idx].SameItemSameComponents(orig[idx]) || slots[idx].Stack.Count != orig[idx].Stack.Count {
				setPlayerSlotData(session, 36+i, slots[idx])
			}
		}
		setCarriedSlotDataLocked(session, tr, carried)
	case MenuTypeGeneric9x6:
		bx, by, bz := tr.open.BlockX, tr.open.BlockY, tr.open.BlockZ
		px, py, pz := tr.open.PartnerX, tr.open.PartnerY, tr.open.PartnerZ
		openDim := tr.open.Dimension
		if openDim == "" && session != nil {
			openDim = session.Dimension()
		}
		slots := s.buildDoubleChestMenuSlotDataInDim(session, openDim, bx, by, bz, px, py, pz)
		orig := append([]containerSlotData(nil), slots...)
		carried := getCarriedSlotDataLocked(session, tr)
		distributeQuickCraftOnSlotData(slots, &carried, dragSlots, dragType, nil)
		for i := 0; i < ChestSlotCount; i++ {
			if !slots[i].SameItemSameComponents(orig[i]) || slots[i].Stack.Count != orig[i].Stack.Count {
				s.setStorageSlotData(openDim, bx, by, bz, i, slots[i])
			}
			hi := ChestSlotCount + i
			if !slots[hi].SameItemSameComponents(orig[hi]) || slots[hi].Stack.Count != orig[hi].Stack.Count {
				s.setStorageSlotData(openDim, px, py, pz, i, slots[hi])
			}
		}
		for i := 0; i < 27; i++ {
			idx := 54 + i
			if !slots[idx].SameItemSameComponents(orig[idx]) || slots[idx].Stack.Count != orig[idx].Stack.Count {
				setPlayerSlotData(session, 9+i, slots[idx])
			}
		}
		for i := 0; i < 9; i++ {
			idx := 81 + i
			if !slots[idx].SameItemSameComponents(orig[idx]) || slots[idx].Stack.Count != orig[idx].Stack.Count {
				setPlayerSlotData(session, 36+i, slots[idx])
			}
		}
		setCarriedSlotDataLocked(session, tr, carried)
	case MenuTypeCrafting:
		slots := s.buildCraftingMenuSlotData(session, tr.open)
		orig := append([]containerSlotData(nil), slots...)
		carried := getCarriedSlotDataLocked(session, tr)
		distributeQuickCraftOnSlotData(slots, &carried, dragSlots, dragType, nil)
		newGrid := make([]world.ItemStack, CraftingGridSlotCount)
		newStars := make([]FireworkExplosion, CraftingGridSlotCount)
		for i := 0; i < CraftingGridSlotCount; i++ {
			d := slots[1+i]
			tr.open.CraftingSlots[i] = d.Clone()
			tr.open.Crafting[i] = d.Stack
			newGrid[i] = d.Stack
			if d.IsEmpty() || !IsPotionOrTippedArrowItem(d.Stack) {
				tr.open.CraftingPotions[i] = ""
			} else {
				tr.open.CraftingPotions[i] = d.Potion
			}
			if d.Star != nil && d.Stack.ResolvedID() == FireworkStarItemID {
				cp := d.Star.Clone()
				tr.open.CraftingStars[i] = cp
				newStars[i] = cp
			} else {
				tr.open.CraftingStars[i] = FireworkExplosion{}
			}
		}
		if res, starExp, fwComp, ok := matchCraftingWithFireworks(newGrid, 3, 3, newStars); ok {
			tr.open.Result = res
			if starExp != nil {
				SetSlotFireworkStar(session, 0, *starExp)
				ClearSlotFireworks(session, 0)
			} else if fwComp != nil {
				SetSlotFireworks(session, 0, *fwComp)
				ClearSlotFireworkStar(session, 0)
			} else {
				ClearSlotFireworkStar(session, 0)
				ClearSlotFireworks(session, 0)
			}
		} else {
			tr.open.Result = world.ItemStack{}
			ClearSlotFireworkStar(session, 0)
			ClearSlotFireworks(session, 0)
		}
		for i := 0; i < 27; i++ {
			idx := 10 + i
			if !slots[idx].SameItemSameComponents(orig[idx]) || slots[idx].Stack.Count != orig[idx].Stack.Count {
				setPlayerSlotData(session, 9+i, slots[idx])
			}
		}
		for i := 0; i < 9; i++ {
			idx := 37 + i
			if !slots[idx].SameItemSameComponents(orig[idx]) || slots[idx].Stack.Count != orig[idx].Stack.Count {
				setPlayerSlotData(session, 36+i, slots[idx])
			}
		}
		setCarriedSlotDataLocked(session, tr, carried)
	case MenuTypeFurnace, MenuTypeBlastFurnace, MenuTypeSmoker:
		bx, by, bz := tr.open.BlockX, tr.open.BlockY, tr.open.BlockZ
		slots := s.buildFurnaceMenuSlots(session, bx, by, bz)
		distributeQuickCraftOnSlice(slots, &tr.carried, dragSlots, dragType, nil)
		for i := 0; i < FurnaceSlotCount; i++ {
			s.SetFurnaceSlot(bx, by, bz, i, slots[i])
		}
		for i := 0; i < 27; i++ {
			session.SetInventorySlot(9+i, slots[3+i])
		}
		for i := 0; i < 9; i++ {
			session.SetInventorySlot(36+i, slots[30+i])
		}
	}
}

// EncodeSetCursorItemPacket encodes ClientboundSetCursorItemPacket (0x60 in protocol 775):
// ItemStack.OPTIONAL_STREAM_CODEC.
func EncodeSetCursorItemPacket(stack world.ItemStack) []byte {
	w := protocol.NewWriter(16)
	writeContainerItemStack(w, stack)
	return w.Bytes()
}

// DecodeSetCursorItemPacket decodes ClientboundSetCursorItemPacket (0x60 in protocol 775).
func DecodeSetCursorItemPacket(r *protocol.Reader) (world.ItemStack, error) {
	return readContainerItemStack(r)
}

// EncodeSetPlayerInventoryPacket encodes ClientboundSetPlayerInventoryPacket (0x6c in protocol 775):
// VarInt(slot), ItemStack.OPTIONAL_STREAM_CODEC(contents).
func EncodeSetPlayerInventoryPacket(slot int32, stack world.ItemStack) []byte {
	w := protocol.NewWriter(20)
	w.VarInt(slot)
	writeContainerItemStack(w, stack)
	return w.Bytes()
}

// DecodeSetPlayerInventoryPacket decodes ClientboundSetPlayerInventoryPacket (0x6c in protocol 775).
func DecodeSetPlayerInventoryPacket(r *protocol.Reader) (int32, world.ItemStack, error) {
	if r == nil {
		return 0, world.ItemStack{}, errors.New("server: nil reader")
	}
	slot, err := r.VarInt()
	if err != nil {
		return 0, world.ItemStack{}, err
	}
	st, err := readContainerItemStack(r)
	if err != nil {
		return 0, world.ItemStack{}, err
	}
	return slot, st, nil
}

// SendSetCursorItem sends ClientboundSetCursorItemPacket (0x60) for session's carried cursor item.
func (s *Server) SendSetCursorItem(session *PlayerSession) error {
	if session == nil || session.send == nil {
		return nil
	}
	tr := getContainerTracker(session)
	tr.mu.Lock()
	carriedData := getCarriedSlotDataLocked(session, tr)
	tr.mu.Unlock()
	return session.send(protocol.PlaySetCursorItem, encodeSetCursorItemFromSlotData(carriedData))
}

// SendSetPlayerInventorySlot sends ClientboundSetPlayerInventoryPacket (0x6c) for a player inventory slot.
func (s *Server) SendSetPlayerInventorySlot(session *PlayerSession, slot int32, stack world.ItemStack) error {
	if session == nil || session.send == nil {
		return nil
	}
	return session.send(protocol.PlaySetPlayerInventory, EncodeSetPlayerInventoryPacket(slot, stack))
}






