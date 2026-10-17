package server

import (
	"errors"
	"sync"

	"regionio/internal/nbt"
	"regionio/internal/protocol"
	"regionio/internal/world"
)

// MenuType registry IDs in BuiltInRegistries.MENU (protocol 775 / 26.1.2).
const (
	MenuTypeGeneric9x3   int32 = 2  // Single chest (27 chest slots + 36 player slots)
	MenuTypeAnvil        int32 = 8  // Anvil (2 input + 1 result + 36 player slots)
	MenuTypeBlastFurnace int32 = 10 // Blast furnace (3 furnace slots + 36 player slots)
	MenuTypeCrafting     int32 = 12 // Crafting table (1 result + 9 grid + 36 player slots)
	MenuTypeEnchantment  int32 = 13 // Enchanting table (2 input + 36 player slots)
	MenuTypeFurnace      int32 = 14 // Furnace (3 furnace slots + 36 player slots)
	MenuTypeGrindstone   int32 = 15 // Grindstone (2 input + 1 result + 36 player slots)
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
	ChestSlotCount        = 27
	ChestMenuSlotCount    = 63 // 27 chest + 27 main inventory + 9 hotbar
	CraftingGridSlotCount = 9
	CraftingMenuSlotCount = 46 // 1 result + 9 grid + 27 main inventory + 9 hotbar
	FurnaceSlotCount      = world.FurnaceSlotCount
	FurnaceMenuSlotCount  = 39 // 3 furnace + 27 main inventory + 9 hotbar
)

// ContainerClick holds the decoded fields of a serverbound container_click packet.
type ContainerClick struct {
	ContainerID int32
	StateID     int32
	Slot        int16
	Button      int8
	Mode        int32
}

// OpenContainerState represents the currently open screen on a PlayerSession.
type OpenContainerState struct {
	ContainerID    int32
	MenuType       int32
	BlockX         int
	BlockY         int
	BlockZ         int
	Crafting       [CraftingGridSlotCount]world.ItemStack
	Result         world.ItemStack
	EnchantSlots   [2]world.ItemStack
	CombinerInputs [2]world.ItemStack
	AnvilRename    string
	EnchantCosts   [3]int16
	EnchantClues   [3]int16
	LevelClues     [3]int16
	AnvilCost      int16
}

type chestKey struct {
	x, y, z int
}

type serverChestState struct {
	mu     sync.Mutex
	chests map[chestKey]*[ChestSlotCount]world.ItemStack
}

type playerContainerTracker struct {
	mu          sync.Mutex
	nextWindow  int32
	carried     world.ItemStack
	open        *OpenContainerState
	invCraft    [4]world.ItemStack
	invResult   world.ItemStack
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
	if tr.mu.TryLock() {
		defer tr.mu.Unlock()
	}
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
	if tr.mu.TryLock() {
		defer tr.mu.Unlock()
	}
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
	}
}

// ChestSlots returns a copy of the 27 slots stored in the chest at (x, y, z).
func (s *Server) ChestSlots(x, y, z int) [ChestSlotCount]world.ItemStack {
	if s == nil {
		return [ChestSlotCount]world.ItemStack{}
	}
	cs := s.chestState()
	cs.mu.Lock()
	defer cs.mu.Unlock()
	arr := cs.chests[chestKey{x, y, z}]
	if arr == nil {
		return [ChestSlotCount]world.ItemStack{}
	}
	return *arr
}

// SetChestSlot updates a single slot (0..26) in the chest at (x, y, z).
func (s *Server) SetChestSlot(x, y, z, slot int, stack world.ItemStack) bool {
	if s == nil || slot < 0 || slot >= ChestSlotCount {
		return false
	}
	cs := s.chestState()
	cs.mu.Lock()
	key := chestKey{x, y, z}
	arr := cs.chests[key]
	if arr == nil {
		arr = &[ChestSlotCount]world.ItemStack{}
		cs.chests[key] = arr
	}
	if stack.IsEmpty() {
		arr[slot] = world.ItemStack{}
	} else {
		arr[slot] = world.NewItemStack(stack.ResolvedItemID(), stack.Count)
	}
	cs.mu.Unlock()
	return true
}

// DropChestContents removes any stored chest inventory at (x, y, z) and spawns
// its non-empty stacks as item entities in the world.
func (s *Server) DropChestContents(x, y, z int) {
	if s == nil {
		return
	}
	cs := s.chestState()
	cs.mu.Lock()
	key := chestKey{x, y, z}
	arr := cs.chests[key]
	delete(cs.chests, key)
	cs.mu.Unlock()
	if arr == nil {
		return
	}
	for _, st := range arr {
		if !st.IsEmpty() {
			s.SpawnItemEntity(float64(x)+0.5, float64(y)+0.25, float64(z)+0.5, st)
		}
	}
}

// IsChestBlock reports whether state is a chest block.
func IsChestBlock(state uint16) bool {
	switch world.BlockName(state) {
	case "minecraft:chest", "minecraft:trapped_chest":
		return true
	default:
		return false
	}
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

	// Drain trailing changedSlots and carriedItem HashedStacks if present.
	if r.Remaining() > 0 {
		changedCount, err := r.VarInt()
		if err != nil {
			return click, err
		}
		for i := int32(0); i < changedCount && r.Remaining() > 0; i++ {
			if _, err := r.Uint16(); err != nil {
				return click, err
			}
			if err := skipHashedStack(r); err != nil {
				return click, err
			}
		}
		if r.Remaining() > 0 {
			if err := skipHashedStack(r); err != nil {
				return click, err
			}
		}
	}
	return click, nil
}

func skipHashedStack(r *protocol.Reader) error {
	present, err := r.Bool()
	if err != nil || !present {
		return err
	}
	if _, err := r.VarInt(); err != nil { // item ID
		return err
	}
	if _, err := r.VarInt(); err != nil { // count
		return err
	}
	addedCount, err := r.VarInt()
	if err != nil {
		return err
	}
	for i := int32(0); i < addedCount; i++ {
		if _, err := r.VarInt(); err != nil { // component type ID
			return err
		}
		if _, err := r.Int32(); err != nil { // component CRC32C hash
			return err
		}
	}
	removedCount, err := r.VarInt()
	if err != nil {
		return err
	}
	for i := int32(0); i < removedCount; i++ {
		if _, err := r.VarInt(); err != nil {
			return err
		}
	}
	return nil
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
	switch {
	case IsChestBlock(state):
		cid, err := s.OpenChest(session, x, y, z)
		return err == nil, cid, err
	case IsCraftingTableBlock(state):
		cid, err := s.OpenCraftingTable(session, x, y, z)
		return err == nil, cid, err
	case IsFurnaceBlock(state):
		cid, err := s.OpenFurnace(session, x, y, z)
		return err == nil, cid, err
	case IsEnchantingTableBlock(state):
		cid, err := s.OpenEnchantingTable(session, x, y, z)
		return err == nil, cid, err
	case IsAnvilBlock(state):
		cid, err := s.OpenAnvil(session, x, y, z)
		return err == nil, cid, err
	case IsGrindstoneBlock(state):
		cid, err := s.OpenGrindstone(session, x, y, z)
		return err == nil, cid, err
	case IsBrewingStandBlock(state):
		cid, err := s.OpenBrewingStand(session, x, y, z)
		return err == nil, cid, err
	case IsBedBlock(state):
		s.InteractBed(session, x, y, z)
		return true, 0, nil
	case IsRespawnAnchorBlock(state):
		return s.InteractRespawnAnchor(session, x, y, z), 0, nil
	default:
		return false, 0, nil
	}
}

// OpenChest opens the 27-slot chest screen at (x, y, z) for session and sends
// open_screen + container_set_content.
func (s *Server) OpenChest(session *PlayerSession, x, y, z int) (int32, error) {
	if s == nil || session == nil {
		return 0, errors.New("server: nil session")
	}
	_ = s.CloseContainer(session, 0, false)

	tr := getContainerTracker(session)
	tr.mu.Lock()
	tr.nextWindow = (tr.nextWindow % 100) + 1
	cid := tr.nextWindow
	tr.open = &OpenContainerState{
		ContainerID: cid,
		MenuType:    MenuTypeGeneric9x3,
		BlockX:      x,
		BlockY:      y,
		BlockZ:      z,
	}
	carried := tr.carried
	tr.mu.Unlock()

	if session.send != nil {
		if err := session.send(protocol.PlayOpenScreen, EncodeOpenScreen(cid, MenuTypeGeneric9x3, "Chest")); err != nil {
			return cid, err
		}
		slots := s.buildChestMenuSlots(session, x, y, z)
		stateID := session.NextContainerStateID()
		if err := session.send(protocol.PlayContainerSetContent, EncodeContainerContent(cid, stateID, slots, carried)); err != nil {
			return cid, err
		}
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

	tr := getContainerTracker(session)
	tr.mu.Lock()
	tr.nextWindow = (tr.nextWindow % 100) + 1
	cid := tr.nextWindow
	tr.open = &OpenContainerState{
		ContainerID: cid,
		MenuType:    MenuTypeCrafting,
		BlockX:      x,
		BlockY:      y,
		BlockZ:      z,
	}
	carried := tr.carried
	tr.mu.Unlock()

	if session.send != nil {
		if err := session.send(protocol.PlayOpenScreen, EncodeOpenScreen(cid, MenuTypeCrafting, "Crafting")); err != nil {
			return cid, err
		}
		slots := s.buildCraftingMenuSlots(session, [CraftingGridSlotCount]world.ItemStack{}, world.ItemStack{})
		stateID := session.NextContainerStateID()
		if err := session.send(protocol.PlayContainerSetContent, EncodeContainerContent(cid, stateID, slots, carried)); err != nil {
			return cid, err
		}
	}
	return cid, nil
}

func (s *Server) buildChestMenuSlots(session *PlayerSession, x, y, z int) []world.ItemStack {
	chest := s.ChestSlots(x, y, z)
	inv := session.Inventory()
	slots := make([]world.ItemStack, ChestMenuSlotCount)
	copy(slots[0:27], chest[:])
	copy(slots[27:54], inv[9:36])
	copy(slots[54:63], inv[36:45])
	return slots
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
	if s == nil || session == nil {
		return nil
	}
	type returnEntry struct {
		stack  world.ItemStack
		meta   ItemMeta
		potion string
	}
	tr := getContainerTracker(session)
	tr.mu.Lock()
	var returnItems []returnEntry
	if !tr.carried.IsEmpty() {
		returnItems = append(returnItems, returnEntry{
			stack:  tr.carried,
			meta:   GetCarriedItemMeta(session),
			potion: GetCarriedPotionType(session),
		})
		tr.carried = world.ItemStack{}
		SetCarriedItemMeta(session, ItemMeta{})
		SetCarriedPotionType(session, "")
	}
	closedID := containerID
	hadOpen := false
	if tr.open != nil && (containerID == 0 || containerID == tr.open.ContainerID) {
		hadOpen = true
		closedID = tr.open.ContainerID
		for i := range tr.open.Crafting {
			if !tr.open.Crafting[i].IsEmpty() {
				returnItems = append(returnItems, returnEntry{stack: tr.open.Crafting[i]})
			}
		}
		metaTr := getItemMetaTracker(session)
		metaTr.mu.Lock()
		for i := range tr.open.EnchantSlots {
			if !tr.open.EnchantSlots[i].IsEmpty() {
				returnItems = append(returnItems, returnEntry{
					stack: tr.open.EnchantSlots[i],
					meta:  metaTr.enchantMeta[i].Clone(),
				})
				metaTr.enchantMeta[i] = ItemMeta{}
			}
		}
		for i := range tr.open.CombinerInputs {
			if !tr.open.CombinerInputs[i].IsEmpty() {
				returnItems = append(returnItems, returnEntry{
					stack: tr.open.CombinerInputs[i],
					meta:  metaTr.combinerMeta[i].Clone(),
				})
				metaTr.combinerMeta[i] = ItemMeta{}
			}
		}
		metaTr.resultMeta = ItemMeta{}
		metaTr.mu.Unlock()
		tr.open = nil
	}
	for i := range tr.invCraft {
		if !tr.invCraft[i].IsEmpty() {
			returnItems = append(returnItems, returnEntry{stack: tr.invCraft[i]})
			tr.invCraft[i] = world.ItemStack{}
		}
	}
	tr.invResult = world.ItemStack{}
	session.SetInventorySlot(0, world.ItemStack{})
	for i := 1; i <= 4; i++ {
		session.SetInventorySlot(i, world.ItemStack{})
	}
	tr.mu.Unlock()

	snap := session.Snapshot()
	for _, entry := range returnItems {
		st := entry.stack
		added, changedSlots := session.AddItemToInventory(st)
		if !entry.meta.IsEmpty() && len(changedSlots) > 0 {
			SetSlotItemMeta(session, changedSlots[0], entry.meta)
		}
		if entry.potion != "" && len(changedSlots) > 0 {
			SetSlotPotionType(session, changedSlots[0], entry.potion)
		}
		if rem := st.Count - added; rem > 0 {
			s.SpawnItemEntity(snap.X, snap.Y+0.25, snap.Z, world.NewItemStack(st.ResolvedItemID(), rem))
		}
	}

	if session.send != nil && (notifyClient || hadOpen || len(returnItems) > 0) {
		if notifyClient && closedID > 0 {
			_ = session.send(protocol.PlayContainerClose, EncodeContainerClose(closedID))
		}
		stateID := session.NextContainerStateID()
		inv := session.Inventory()
		_ = session.send(protocol.PlayContainerSetContent, EncodeContainerContent(0, stateID, inv[:], world.ItemStack{}))
	}
	return nil
}

// HandleContainerClick processes a slot click on either the player's inventory
// container (containerID == 0) or the currently open chest / crafting table
// container, updating authoritative slot contents and synchronizing the client.
func (s *Server) HandleContainerClick(session *PlayerSession, containerID int32, slot int, button int8, mode int32) error {
	if s == nil || session == nil || session.IsDead() {
		return nil
	}
	tr := getContainerTracker(session)
	tr.mu.Lock()

	// Clicking outside the window (-999) with Pickup drops carried item(s).
	if slot == -999 && mode == ContainerInputPickup {
		if !tr.carried.IsEmpty() {
			dropCount := tr.carried.Count
			if button == 1 {
				dropCount = 1
			}
			dropStack := world.NewItemStack(tr.carried.ResolvedItemID(), dropCount)
			rem := tr.carried.Count - dropCount
			if rem <= 0 {
				tr.carried = world.ItemStack{}
				SetCarriedItemMeta(session, ItemMeta{})
				SetCarriedPotionType(session, "")
			} else {
				tr.carried = world.NewItemStack(tr.carried.ResolvedItemID(), rem)
			}
			tr.mu.Unlock()
			snap := session.Snapshot()
			s.SpawnItemEntity(snap.X, snap.Y+0.5, snap.Z, dropStack)
			return s.syncOpenContainer(session)
		}
		tr.mu.Unlock()
		return s.syncOpenContainer(session)
	}

	if containerID == 0 {
		s.applyPlayerInventoryClickLocked(session, tr, slot, button, mode)
		tr.mu.Unlock()
		s.TriggerInventoryChanged(session)
		return s.syncPlayerInventoryContainer(session)
	}

	if tr.open == nil || tr.open.ContainerID != containerID {
		tr.mu.Unlock()
		return nil
	}

	switch tr.open.MenuType {
	case MenuTypeGeneric9x3:
		bx, by, bz := tr.open.BlockX, tr.open.BlockY, tr.open.BlockZ
		s.applyChestClickLocked(session, tr, bx, by, bz, slot, button, mode)
		tr.mu.Unlock()
		s.TriggerInventoryChanged(session)
		return s.syncChestViewers(session, bx, by, bz)
	case MenuTypeCrafting:
		s.applyCraftingTableClickLocked(session, tr, slot, button, mode)
		tr.mu.Unlock()
		s.TriggerInventoryChanged(session)
		return s.syncOpenContainer(session)
	case MenuTypeFurnace, MenuTypeBlastFurnace, MenuTypeSmoker:
		menuType := tr.open.MenuType
		bx, by, bz := tr.open.BlockX, tr.open.BlockY, tr.open.BlockZ
		s.applyFurnaceClickLocked(session, tr, menuType, bx, by, bz, slot, button, mode)
		tr.mu.Unlock()
		s.TriggerInventoryChanged(session)
		return s.syncFurnaceViewers(session, bx, by, bz)
	case MenuTypeEnchantment:
		s.applyEnchantingTableClickLocked(session, tr, slot, button, mode)
		tr.mu.Unlock()
		s.TriggerInventoryChanged(session)
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
			if s.DamageAnvilBlockOnUse(session, bx, by, bz, nil) {
				return nil
			}
		} else {
			s.TriggerInventoryChanged(session)
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
		return s.syncOpenContainer(session)
	case MenuTypeBrewingStand:
		bx, by, bz := tr.open.BlockX, tr.open.BlockY, tr.open.BlockZ
		s.applyBrewingStandContainerClickLocked(session, tr, bx, by, bz, slot, button, mode)
		tr.mu.Unlock()
		s.TriggerInventoryChanged(session)
		return s.syncBrewingStandViewers(session, bx, by, bz)
	default:
		tr.mu.Unlock()
		return nil
	}
}

func (s *Server) applyChestClickLocked(session *PlayerSession, tr *playerContainerTracker, bx, by, bz, slot int, button int8, mode int32) {
	if slot < 0 || slot >= ChestMenuSlotCount {
		return
	}
	slots := s.buildChestMenuSlots(session, bx, by, bz)

	switch mode {
	case ContainerInputPickup:
		slots[slot], tr.carried = applyPickupSlot(slots[slot], tr.carried, button)
	case ContainerInputQuickMove:
		if slot < ChestSlotCount {
			// Chest -> Player inventory (prefer hotbar 54..62, then main 27..53)
			slots[slot] = mergeIntoSlotSlice(slots, slots[slot], 54, 63)
			slots[slot] = mergeIntoSlotSlice(slots, slots[slot], 27, 54)
		} else {
			// Player inventory -> Chest (0..26)
			slots[slot] = mergeIntoSlotSlice(slots, slots[slot], 0, ChestSlotCount)
		}
	case ContainerInputSwap:
		if button >= 0 && button < 9 {
			hotbarIdx := 54 + int(button)
			slots[slot], slots[hotbarIdx] = slots[hotbarIdx], slots[slot]
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
		}
	}

	// Write back chest slots (0..26) and player inventory slots (27..62).
	for i := 0; i < ChestSlotCount; i++ {
		s.SetChestSlot(bx, by, bz, i, slots[i])
	}
	for i := 0; i < 27; i++ {
		session.SetInventorySlot(9+i, slots[27+i])
	}
	for i := 0; i < 9; i++ {
		session.SetInventorySlot(36+i, slots[54+i])
	}
}

func (s *Server) applyCraftingTableClickLocked(session *PlayerSession, tr *playerContainerTracker, slot int, button int8, mode int32) {
	if slot < 0 || slot >= CraftingMenuSlotCount || tr.open == nil {
		return
	}
	slots := s.buildCraftingMenuSlots(session, tr.open.Crafting, tr.open.Result)

	if slot == 0 {
		// Crafting output slot.
		if crafted := s.clickCraftingOutput(slots, 1, 9, 3, 3, 10, 46, &tr.carried, mode); !crafted.IsEmpty() {
			s.TriggerRecipeCrafted(session, crafted)
		}
	} else {
		switch mode {
		case ContainerInputPickup:
			slots[slot], tr.carried = applyPickupSlot(slots[slot], tr.carried, button)
		case ContainerInputQuickMove:
			if slot >= 1 && slot <= 9 {
				// Crafting grid -> player inventory
				slots[slot] = mergeIntoSlotSlice(slots, slots[slot], 37, 46)
				slots[slot] = mergeIntoSlotSlice(slots, slots[slot], 10, 37)
			} else if slot >= 10 && slot < 37 {
				// Main inventory -> hotbar
				slots[slot] = mergeIntoSlotSlice(slots, slots[slot], 37, 46)
			} else if slot >= 37 && slot < 46 {
				// Hotbar -> main inventory
				slots[slot] = mergeIntoSlotSlice(slots, slots[slot], 10, 37)
			}
		case ContainerInputSwap:
			if button >= 0 && button < 9 {
				hotbarIdx := 37 + int(button)
				slots[slot], slots[hotbarIdx] = slots[hotbarIdx], slots[slot]
			}
		case ContainerInputClone:
			if session.Snapshot().GameMode == GameModeCreative && tr.carried.IsEmpty() && !slots[slot].IsEmpty() {
				tr.carried = world.NewItemStack(slots[slot].ResolvedItemID(), MaxItemStackSize)
			}
		}
	}

	// Write back crafting grid (1..9), recompute result (0), and write back player inventory (10..45).
	for i := 0; i < CraftingGridSlotCount; i++ {
		tr.open.Crafting[i] = slots[1+i]
	}
	if res, _, ok := MatchCraftingGrid(tr.open.Crafting[:], 3, 3); ok {
		tr.open.Result = res
	} else {
		tr.open.Result = world.ItemStack{}
	}
	for i := 0; i < 27; i++ {
		session.SetInventorySlot(9+i, slots[10+i])
	}
	for i := 0; i < 9; i++ {
		session.SetInventorySlot(36+i, slots[37+i])
	}
}

func (s *Server) applyPlayerInventoryClickLocked(session *PlayerSession, tr *playerContainerTracker, slot int, button int8, mode int32) {
	if slot < 0 || slot >= world.PlayerInventorySlots {
		return
	}
	for i := 0; i < 4; i++ {
		if tr.invCraft[i].IsEmpty() {
			tr.invCraft[i] = session.InventorySlot(1 + i)
		}
	}
	if res, _, ok := MatchCraftingGrid(tr.invCraft[:], 2, 2); ok {
		tr.invResult = res
	} else {
		tr.invResult = world.ItemStack{}
	}
	slots := s.buildPlayerMenuSlots(session, tr.invCraft, tr.invResult)

	if slot == 0 {
		if crafted := s.clickCraftingOutput(slots, 1, 4, 2, 2, 9, 45, &tr.carried, mode); !crafted.IsEmpty() {
			s.TriggerRecipeCrafted(session, crafted)
		}
	} else {
		switch mode {
		case ContainerInputPickup:
			slots[slot], tr.carried = applyPickupSlot(slots[slot], tr.carried, button)
		case ContainerInputQuickMove:
			if slot >= 1 && slot <= 4 {
				slots[slot] = mergeIntoSlotSlice(slots, slots[slot], 36, 45)
				slots[slot] = mergeIntoSlotSlice(slots, slots[slot], 9, 36)
			} else if slot >= 9 && slot < 36 {
				slots[slot] = mergeIntoSlotSlice(slots, slots[slot], 36, 45)
			} else if slot >= 36 && slot < 45 {
				slots[slot] = mergeIntoSlotSlice(slots, slots[slot], 9, 36)
			}
		case ContainerInputSwap:
			if button >= 0 && button < 9 {
				hotbarIdx := 36 + int(button)
				slots[slot], slots[hotbarIdx] = slots[hotbarIdx], slots[slot]
			}
		case ContainerInputClone:
			if session.Snapshot().GameMode == GameModeCreative && tr.carried.IsEmpty() && !slots[slot].IsEmpty() {
				tr.carried = world.NewItemStack(slots[slot].ResolvedItemID(), MaxItemStackSize)
			}
		}
	}

	for i := 0; i < 4; i++ {
		tr.invCraft[i] = slots[1+i]
	}
	if res, _, ok := MatchCraftingGrid(tr.invCraft[:], 2, 2); ok {
		tr.invResult = res
	} else {
		tr.invResult = world.ItemStack{}
	}
	slots[0] = tr.invResult
	for i := 0; i < world.PlayerInventorySlots; i++ {
		session.SetInventorySlot(i, slots[i])
	}
}

func (s *Server) clickCraftingOutput(
	slots []world.ItemStack,
	gridStart, gridCount, width, height int,
	invStart, invEnd int,
	carried *world.ItemStack,
	mode int32,
) world.ItemStack {
	grid := slots[gridStart : gridStart+gridCount]
	res, _, ok := MatchCraftingGrid(grid, width, height)
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
			if cur.Count <= 1 {
				slots[gridStart+i] = world.ItemStack{}
			} else {
				slots[gridStart+i] = world.NewItemStack(cur.ResolvedItemID(), cur.Count-1)
			}
		}
	}

	var crafted world.ItemStack
	switch mode {
	case ContainerInputPickup:
		if carried.IsEmpty() {
			*carried = world.NewItemStack(resID, res.Count)
			consumeGridOnce()
			crafted = res
		} else if carried.ResolvedItemID() == resID && carried.Count+res.Count <= MaxItemStackSize {
			*carried = world.NewItemStack(resID, carried.Count+res.Count)
			consumeGridOnce()
			crafted = res
		}
	case ContainerInputQuickMove:
		for iter := 0; iter < 64; iter++ {
			curRes, _, match := MatchCraftingGrid(slots[gridStart:gridStart+gridCount], width, height)
			if !match || curRes.IsEmpty() || curRes.ResolvedItemID() != resID {
				break
			}
			if !canFitInSlice(slots, curRes, invStart, invEnd) {
				break
			}
			// Prefer hotbar (last 9 slots of invStart..invEnd) then main inventory.
			hotbarStart := invEnd - 9
			rem := mergeIntoSlotSlice(slots, curRes, hotbarStart, invEnd)
			rem = mergeIntoSlotSlice(slots, rem, invStart, hotbarStart)
			consumeGridOnce()
			crafted = res
			if !rem.IsEmpty() {
				break
			}
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

func (s *Server) syncOpenContainer(session *PlayerSession) error {
	if session == nil || session.send == nil {
		return nil
	}
	tr := getContainerTracker(session)
	tr.mu.Lock()
	carried := tr.carried
	var openCopy *OpenContainerState
	if tr.open != nil {
		cp := *tr.open
		openCopy = &cp
	}
	tr.mu.Unlock()

	if openCopy == nil {
		return s.syncPlayerInventoryContainer(session)
	}
	var slots []world.ItemStack
	isFurnace := false
	isEnchant := false
	isAnvil := false
	isBrewingStand := false
	switch openCopy.MenuType {
	case MenuTypeGeneric9x3:
		slots = s.buildChestMenuSlots(session, openCopy.BlockX, openCopy.BlockY, openCopy.BlockZ)
	case MenuTypeCrafting:
		slots = s.buildCraftingMenuSlots(session, openCopy.Crafting, openCopy.Result)
	case MenuTypeFurnace, MenuTypeBlastFurnace, MenuTypeSmoker:
		slots = s.buildFurnaceMenuSlots(session, openCopy.BlockX, openCopy.BlockY, openCopy.BlockZ)
		isFurnace = true
	case MenuTypeEnchantment:
		slots = s.buildEnchantingMenuSlots(session, openCopy.EnchantSlots)
		isEnchant = true
	case MenuTypeAnvil:
		slots = s.buildCombinerMenuSlots(session, openCopy.CombinerInputs, openCopy.Result)
		isAnvil = true
	case MenuTypeGrindstone:
		slots = s.buildCombinerMenuSlots(session, openCopy.CombinerInputs, openCopy.Result)
	case MenuTypeBrewingStand:
		slots = s.buildBrewingStandMenuSlots(session, openCopy.BlockX, openCopy.BlockY, openCopy.BlockZ)
		isBrewingStand = true
	default:
		return nil
	}
	stateID := session.NextContainerStateID()
	if err := session.send(protocol.PlayContainerSetContent, EncodeContainerContent(openCopy.ContainerID, stateID, slots, carried)); err != nil {
		return err
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
	return nil
}

func (s *Server) syncPlayerInventoryContainer(session *PlayerSession) error {
	if session == nil || session.send == nil {
		return nil
	}
	tr := getContainerTracker(session)
	tr.mu.Lock()
	carried := tr.carried
	grid := tr.invCraft
	res := tr.invResult
	tr.mu.Unlock()

	slots := s.buildPlayerMenuSlots(session, grid, res)
	stateID := session.NextContainerStateID()
	return session.send(protocol.PlayContainerSetContent, EncodeContainerContent(0, stateID, slots, carried))
}

func (s *Server) syncChestViewers(actor *PlayerSession, bx, by, bz int) error {
	s.playersMu.RLock()
	sessions := make([]*PlayerSession, 0, len(s.players))
	for _, sess := range s.players {
		sessions = append(sessions, sess)
	}
	s.playersMu.RUnlock()

	var firstErr error
	for _, sess := range sessions {
		open := sess.OpenContainer()
		if open != nil && open.MenuType == MenuTypeGeneric9x3 &&
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
	carriedPotion := GetCarriedPotionType(session)

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

