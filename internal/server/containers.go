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
	MenuTypeHorse        int32 = -2 // Equine/llama inventory screen (opened via PlayHorseScreenOpen 0x29)
	MenuTypeGeneric9x3   int32 = 2  // Single chest (27 chest slots + 36 player slots)
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
	ChestSlotCount        = 27
	ChestMenuSlotCount    = 63 // 27 chest + 27 main inventory + 9 hotbar
	HopperSlotCount       = 5
	HopperMenuSlotCount   = 41 // 5 hopper + 27 main inventory + 9 hotbar
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
	ContainerID     int32
	MenuType        int32
	BlockX          int
	BlockY          int
	BlockZ          int
	VehicleEntityID int32
	Crafting        [CraftingGridSlotCount]world.ItemStack
	CraftingStars   [CraftingGridSlotCount]FireworkExplosion
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

type playerContainerTracker struct {
	mu            sync.Mutex
	nextWindow    int32
	carried       world.ItemStack
	open          *OpenContainerState
	invCraft      [4]world.ItemStack
	invCraftStars [4]FireworkExplosion
	invResult     world.ItemStack
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
		SetCarriedArmorTrim(session, ArmorTrim{})
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
	blockName := world.BlockName(state)
	switch {
	case IsChestBlock(state):
		s.RecordContainerOpenStat(session, blockName)
		cid, err := s.OpenChest(session, x, y, z)
		if err == nil {
			s.EmitPlayerBlockVibration(session, GameEventContainerOpen, session.Dimension(), x, y, z, blockName)
		}
		return err == nil, cid, err
	case IsCraftingTableBlock(state):
		s.RecordContainerOpenStat(session, blockName)
		cid, err := s.OpenCraftingTable(session, x, y, z)
		if err == nil {
			s.EmitPlayerBlockVibration(session, GameEventContainerOpen, session.Dimension(), x, y, z, blockName)
		}
		return err == nil, cid, err
	case IsFurnaceBlock(state):
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
		if dt, _ := world.LookupDimensionType(dim); !dt.BedWorks {
			headPos, footPos, hasPartner := ResolveBedPositions(cache, x, y, z)
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
		s.InteractBed(session, x, y, z)
		return true, 0, nil
	case IsRespawnAnchorBlock(state):
		dim := world.NormalizeDimension(session.Dimension())
		charges := RespawnAnchorCharges(state)
		if !(IsGlowstoneItem(session.HeldItem()) && charges < MaxRespawnAnchorCharges) && charges > 0 {
			if dt, _ := world.LookupDimensionType(dim); !dt.RespawnAnchorWorks {
				if broken, upd := cache.BreakBlockWithUpdates(x, y, z); broken {
					s.broadcastDimensionBlockChanges(dim, cache, upd.Changes, upd.LightChanged)
				}
				s.ExplodeBlock(dim, x, y, z, 5.0, true)
				return true, 0, nil
			}
		}
		return s.InteractRespawnAnchor(session, x, y, z), 0, nil
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
	s.CloseLecternIfOpen(session)
	type returnEntry struct {
		stack  world.ItemStack
		meta   ItemMeta
		potion string
		trim   ArmorTrim
	}
	tr := getContainerTracker(session)
	tr.mu.Lock()
	var returnItems []returnEntry
	if !tr.carried.IsEmpty() {
		carriedTrim, _ := GetCarriedArmorTrim(session)
		returnItems = append(returnItems, returnEntry{
			stack:  tr.carried,
			meta:   GetCarriedItemMeta(session),
			potion: GetCarriedPotionType(session),
			trim:   carriedTrim,
		})
		tr.carried = world.ItemStack{}
		SetCarriedItemMeta(session, ItemMeta{})
		SetCarriedPotionType(session, "")
		SetCarriedArmorTrim(session, ArmorTrim{})
	}
	closedID := containerID
	hadOpen := false
	var closedX, closedY, closedZ int
	if tr.open != nil && (containerID == 0 || containerID == tr.open.ContainerID) {
		hadOpen = true
		closedID = tr.open.ContainerID
		closedX, closedY, closedZ = tr.open.BlockX, tr.open.BlockY, tr.open.BlockZ
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

	if hadOpen {
		s.EmitPlayerBlockVibration(session, GameEventContainerClose, session.Dimension(), closedX, closedY, closedZ, "")
	}

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
		if entry.trim.IsValid() && len(changedSlots) > 0 {
			SetSlotArmorTrim(session, changedSlots[0], entry.trim)
		}
		if rem := st.Count - added; rem > 0 {
			s.SpawnItemEntity(snap.X, snap.Y+0.25, snap.Z, world.NewItemStack(st.ResolvedItemID(), rem))
		}
	}

	if session.send != nil && (notifyClient || hadOpen || len(returnItems) > 0) {
		if notifyClient && closedID > 0 {
			_ = session.send(protocol.PlayContainerClose, EncodeContainerClose(closedID))
		}
		_ = s.syncPlayerInventoryContainer(session)
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
				SetCarriedArmorTrim(session, ArmorTrim{})
			} else {
				tr.carried = world.NewItemStack(tr.carried.ResolvedItemID(), rem)
			}
			tr.mu.Unlock()
			snap := session.Snapshot()
			s.SpawnItemEntity(snap.X, snap.Y+0.5, snap.Z, dropStack)
			s.RecordItemDropped(session, dropStack.ResolvedID(), dropStack.Count)
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
	case MenuTypeGeneric9x3:
		if tr.open.VehicleEntityID != 0 {
			vehID := tr.open.VehicleEntityID
			s.applyVehicleChestClickLocked(session, tr, vehID, slot, button, mode)
			tr.mu.Unlock()
			s.TriggerInventoryChanged(session)
			s.UnlockRecipesForInventory(session)
			return s.syncVehicleContainerViewers(session, vehID)
		}
		bx, by, bz := tr.open.BlockX, tr.open.BlockY, tr.open.BlockZ
		s.applyChestClickLocked(session, tr, bx, by, bz, slot, button, mode)
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
		s.applyBrewingStandContainerClickLocked(session, tr, bx, by, bz, slot, button, mode)
		tr.mu.Unlock()
		s.TriggerInventoryChanged(session)
		s.UnlockRecipesForInventory(session)
		return s.syncBrewingStandViewers(session, bx, by, bz)
	case MenuTypeCartographyTable:
		tr.mu.Unlock()
		return s.HandleCartographyContainerClick(session, containerID, slot, button, mode)
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
			s.RecordItemDropped(session, dropStack.ResolvedID(), dropStack.Count)
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

func (s *Server) applyCraftingTableClickLocked(session *PlayerSession, tr *playerContainerTracker, slot int, button int8, mode int32) {
	if slot < 0 || slot >= CraftingMenuSlotCount || tr.open == nil {
		return
	}
	slots := s.buildCraftingMenuSlots(session, tr.open.Crafting, tr.open.Result)
	gridStars := make([]FireworkExplosion, CraftingGridSlotCount)
	for i := 0; i < CraftingGridSlotCount; i++ {
		if len(tr.open.CraftingStars[i].Colors) > 0 || len(tr.open.CraftingStars[i].FadeColors) > 0 {
			gridStars[i] = tr.open.CraftingStars[i].Clone()
		} else if exp, ok := GetSlotFireworkStar(session, 1+i); ok {
			gridStars[i] = exp
		}
	}

	if slot == 0 {
		// Crafting output slot.
		_, matchedRec, _ := MatchCraftingGrid(slots[1:10], 3, 3)
		if crafted := s.clickCraftingOutput(session, slots, 1, 9, 3, 3, 10, 46, gridStars, func(menuSlot int) int {
			return menuSlot - 1
		}, &tr.carried, mode); !crafted.IsEmpty() {
			s.RecordItemCrafted(session, crafted.ResolvedID(), crafted.Count)
			s.TriggerRecipeCrafted(session, crafted)
			recName := ""
			if matchedRec != nil {
				recName = matchedRec.Name
			}
			s.UnlockRecipeOnCraft(session, recName, crafted)
		}
	} else {
		switch mode {
		case ContainerInputPickup:
			prevSlotEmpty := slots[slot].IsEmpty()
			prevCarriedEmpty := tr.carried.IsEmpty()
			var slotStar *FireworkExplosion
			var slotFW *FireworksComponent
			if slot >= 1 && slot <= 9 {
				if len(gridStars[slot-1].Colors) > 0 || len(gridStars[slot-1].FadeColors) > 0 {
					cp := gridStars[slot-1].Clone()
					slotStar = &cp
				}
			} else if slot >= 10 && slot < 46 {
				sessSlot := slot - 1
				if exp, ok := GetSlotFireworkStar(session, sessSlot); ok {
					slotStar = &exp
				}
				if fw, ok := GetSlotFireworks(session, sessSlot); ok && slots[slot].ResolvedID() == FireworkRocketItemID {
					slotFW = &fw
				}
			}
			var cStar *FireworkExplosion
			if exp, ok := getCarriedFireworkStar(session); ok {
				cStar = &exp
			}
			var cFW *FireworksComponent
			if fw, ok := getCarriedFireworks(session); ok {
				cFW = &fw
			}

			slots[slot], tr.carried = applyPickupSlot(slots[slot], tr.carried, button)

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
			if slot >= 1 && slot <= 9 {
				if newSlotStar != nil {
					tr.open.CraftingStars[slot-1] = newSlotStar.Clone()
				} else {
					tr.open.CraftingStars[slot-1] = FireworkExplosion{}
				}
			} else if slot >= 10 && slot < 46 {
				sessSlot := slot - 1
				if newSlotStar != nil && slots[slot].ResolvedID() == FireworkStarItemID {
					SetSlotFireworkStar(session, sessSlot, *newSlotStar)
				} else if slots[slot].IsEmpty() || slots[slot].ResolvedID() != FireworkStarItemID {
					ClearSlotFireworkStar(session, sessSlot)
				}
				if newSlotFW != nil && slots[slot].ResolvedID() == FireworkRocketItemID {
					SetSlotFireworks(session, sessSlot, *newSlotFW)
				} else if slots[slot].IsEmpty() || slots[slot].ResolvedID() != FireworkRocketItemID {
					ClearSlotFireworks(session, sessSlot)
				}
			}
			setCarriedFireworkStar(session, newCStar)
			setCarriedFireworks(session, newCFW)

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
		if tr.open.Crafting[i].IsEmpty() {
			tr.open.CraftingStars[i] = FireworkExplosion{}
		} else if len(tr.open.CraftingStars[i].Colors) == 0 && len(tr.open.CraftingStars[i].FadeColors) == 0 {
			if exp, ok := GetSlotFireworkStar(session, 1+i); ok {
				tr.open.CraftingStars[i] = exp
			}
		}
	}
	if res, starExp, fwComp, ok := matchCraftingWithFireworks(tr.open.Crafting[:], 3, 3, tr.open.CraftingStars[:]); ok {
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

	if slot == 0 {
		_, matchedRec, _ := MatchCraftingGrid(slots[1:5], 2, 2)
		if crafted := s.clickCraftingOutput(session, slots, 1, 4, 2, 2, 9, 45, tr.invCraftStars[:], func(menuSlot int) int {
			return menuSlot
		}, &tr.carried, mode); !crafted.IsEmpty() {
			s.RecordItemCrafted(session, crafted.ResolvedID(), crafted.Count)
			s.TriggerRecipeCrafted(session, crafted)
			recName := ""
			if matchedRec != nil {
				recName = matchedRec.Name
			}
			s.UnlockRecipeOnCraft(session, recName, crafted)
		}
	} else {
		switch mode {
		case ContainerInputPickup:
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

			slots[slot], tr.carried = applyPickupSlot(slots[slot], tr.carried, button)

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
	}
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
		} else if carried.ResolvedItemID() == resID && carried.Count+res.Count <= MaxItemStackSize {
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
			// Prefer hotbar (last 9 slots of invStart..invEnd) then main inventory.
			hotbarStart := invEnd - 9
			rem := mergeIntoSlotSlice(slots, curRes, hotbarStart, invEnd)
			rem = mergeIntoSlotSlice(slots, rem, invStart, hotbarStart)
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
	isLoom := false
	isCrafter := false
	switch openCopy.MenuType {
	case MenuTypeGeneric9x3:
		if openCopy.VehicleEntityID != 0 {
			slots = s.buildVehicleChestMenuSlots(session, openCopy.VehicleEntityID)
		} else {
			slots = s.buildChestMenuSlots(session, openCopy.BlockX, openCopy.BlockY, openCopy.BlockZ)
		}
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
		isEnchant = true
	case MenuTypeAnvil:
		slots = s.buildCombinerMenuSlots(session, openCopy.CombinerInputs, openCopy.Result)
		isAnvil = true
	case MenuTypeGrindstone:
		slots = s.buildCombinerMenuSlots(session, openCopy.CombinerInputs, openCopy.Result)
	case MenuTypeBrewingStand:
		slots = s.buildBrewingStandMenuSlots(session, openCopy.BlockX, openCopy.BlockY, openCopy.BlockZ)
		isBrewingStand = true
	case MenuTypeLoom:
		slots = s.buildLoomMenuSlots(
			session,
			openCopy.Crafting[LoomSlotBanner],
			openCopy.Crafting[LoomSlotDye],
			openCopy.Crafting[LoomSlotPattern],
			openCopy.Result,
		)
		isLoom = true
	case MenuTypeStonecutter:
		return s.syncStonecutterContainer(session)
	case MenuTypeSmithing:
		return s.syncSmithingContainer(session)
	case MenuTypeBeacon:
		return s.syncOpenBeacon(session)
	default:
		return nil
	}
	stateID := session.NextContainerStateID()
	if err := session.send(protocol.PlayContainerSetContent, EncodeContainerContent(openCopy.ContainerID, stateID, slots, carried)); err != nil {
		return err
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
	w := protocol.NewWriter(32 + len(slots)*12)
	w.VarInt(0)
	w.VarInt(stateID)
	w.VarInt(int32(len(slots)))
	for i, st := range slots {
		switch st.ResolvedID() {
		case FireworkRocketItemID:
			if fw, ok := GetSlotFireworks(session, i); ok {
				EncodeSlotWithFireworks(w, st, fw)
				continue
			}
		case FireworkStarItemID:
			if exp, ok := GetSlotFireworkStar(session, i); ok {
				EncodeSlotWithFireworkStar(w, st, exp)
				continue
			}
		}
		meta := GetSlotItemMeta(session, i)
		trim, hasTrim := GetSlotArmorTrim(session, i)
		if !meta.IsEmpty() || hasTrim {
			WriteSlotWithArmorTrim(w, st, meta, trim)
		} else {
			writeContainerItemStack(w, st)
		}
	}
	encodedCarried := false
	switch carried.ResolvedID() {
	case FireworkRocketItemID:
		if fw, ok := getCarriedFireworks(session); ok {
			EncodeSlotWithFireworks(w, carried, fw)
			encodedCarried = true
		}
	case FireworkStarItemID:
		if exp, ok := getCarriedFireworkStar(session); ok {
			EncodeSlotWithFireworkStar(w, carried, exp)
			encodedCarried = true
		}
	}
	if !encodedCarried {
		carriedMeta := GetCarriedItemMeta(session)
		carriedTrim, hasCarriedTrim := GetCarriedArmorTrim(session)
		if !carriedMeta.IsEmpty() || hasCarriedTrim {
			WriteSlotWithArmorTrim(w, carried, carriedMeta, carriedTrim)
		} else {
			writeContainerItemStack(w, carried)
		}
	}
	return session.send(protocol.PlayContainerSetContent, w.Bytes())
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




