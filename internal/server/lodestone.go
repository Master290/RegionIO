package server

import (
	"sync"

	"regionio/internal/nbt"
	"regionio/internal/protocol"
	"regionio/internal/world"
)

// Lodestone & Compass constants for Minecraft 26.1.2 (protocol 775).
const (
	LodestoneBlockID = "minecraft:lodestone"

	// DataComponentLodestoneTracker is BuiltInRegistries.DATA_COMPONENT_TYPE
	// index 67 ("minecraft:lodestone_tracker") in vanilla 26.1.2.
	DataComponentLodestoneTracker int32 = 67
)

// LodestoneTracker aliases world.LodestoneTracker (`minecraft:lodestone_tracker`).
type LodestoneTracker = world.LodestoneTracker

// NewLodestoneTracker constructs a LodestoneTracker pointing to (dim, x, y, z)
// with the given tracked flag.
func NewLodestoneTracker(dim string, x, y, z int, tracked bool) LodestoneTracker {
	return world.NewLodestoneTracker(dim, x, y, z, tracked)
}

// IsLodestoneBlock reports whether state is a `minecraft:lodestone` block.
func IsLodestoneBlock(state uint16) bool {
	return world.IsLodestoneBlock(state)
}

// IsCompassItem reports whether stack is a `minecraft:compass` item.
func IsCompassItem(stack world.ItemStack) bool {
	return !stack.IsEmpty() && canonicalResourceName(stack.ResolvedID()) == CompassItemID
}

// WriteLodestoneTrackerNBT serializes a LodestoneTracker into its vanilla 26.1.2
// NBT compound representation (`target: {dimension, pos}, tracked`).
func WriteLodestoneTrackerNBT(lt LodestoneTracker) *nbt.Compound {
	return lt.ToNBT()
}

// ReadLodestoneTrackerNBT decodes a LodestoneTracker from its vanilla 26.1.2
// NBT compound representation.
func ReadLodestoneTrackerNBT(c *nbt.Compound) (LodestoneTracker, bool) {
	return world.LodestoneTrackerFromNBT(c)
}

// EncodeLodestoneTrackerComponent writes the wire payload of
// `DataComponentLodestoneTracker` (67) matching `LodestoneTracker.STREAM_CODEC`:
//   - Bool(HasTarget)
//   - if HasTarget: ResourceKey/Identifier(Dimension) + BlockPos(X, Y, Z)
//   - Bool(Tracked)
func EncodeLodestoneTrackerComponent(w *protocol.Writer, lt LodestoneTracker) {
	if w == nil {
		return
	}
	w.Bool(lt.HasTarget)
	if lt.HasTarget {
		w.String(world.NormalizeDimension(lt.Dimension))
		w.Position(int(lt.X), int(lt.Y), int(lt.Z))
	}
	w.Bool(lt.Tracked)
}

// DecodeLodestoneTrackerComponent reads the wire payload of
// `DataComponentLodestoneTracker` (67) matching `LodestoneTracker.STREAM_CODEC`.
func DecodeLodestoneTrackerComponent(r *protocol.Reader) (LodestoneTracker, error) {
	hasTarget, err := r.Bool()
	if err != nil {
		return LodestoneTracker{}, err
	}
	var lt LodestoneTracker
	lt.HasTarget = hasTarget
	if hasTarget {
		dim, err := r.String()
		if err != nil {
			return LodestoneTracker{}, err
		}
		x, y, z, err := r.Position()
		if err != nil {
			return LodestoneTracker{}, err
		}
		lt.Dimension = world.NormalizeDimension(dim)
		lt.X = int32(x)
		lt.Y = int32(y)
		lt.Z = int32(z)
	}
	tracked, err := r.Bool()
	if err != nil {
		return LodestoneTracker{}, err
	}
	lt.Tracked = tracked
	return lt, nil
}

// EncodeSlotWithLodestoneTracker writes a protocol 775 Slot carrying
// `DataComponentLodestoneTracker` (67) when `lt != nil`.
func EncodeSlotWithLodestoneTracker(w *protocol.Writer, stack world.ItemStack, lt *LodestoneTracker) {
	if w == nil {
		return
	}
	itemID := stack.ResolvedItemID()
	if stack.IsEmpty() || itemID <= 0 {
		w.VarInt(0)
		return
	}
	w.VarInt(stack.Count)
	w.VarInt(itemID)
	if lt == nil {
		w.VarInt(0)
		w.VarInt(0)
		return
	}
	w.VarInt(1) // 1 added component
	w.VarInt(0) // 0 removed components
	w.VarInt(DataComponentLodestoneTracker)
	EncodeLodestoneTrackerComponent(w, *lt)
}

// DecodeSlotWithLodestoneTracker decodes a protocol 775 Slot and extracts an
// optional `DataComponentLodestoneTracker` (67) component if present.
func DecodeSlotWithLodestoneTracker(r *protocol.Reader) (world.ItemStack, *LodestoneTracker, error) {
	count, err := r.VarInt()
	if err != nil {
		return world.ItemStack{}, nil, err
	}
	if count <= 0 {
		return world.ItemStack{}, nil, nil
	}
	itemID, err := r.VarInt()
	if err != nil {
		return world.ItemStack{}, nil, err
	}
	addedCount, err := r.VarInt()
	if err != nil {
		return world.ItemStack{}, nil, err
	}
	removedCount, err := r.VarInt()
	if err != nil {
		return world.ItemStack{}, nil, err
	}
	var tracker *LodestoneTracker
	for i := int32(0); i < addedCount; i++ {
		compType, err := r.VarInt()
		if err != nil {
			return world.ItemStack{}, nil, err
		}
		if compType == DataComponentLodestoneTracker {
			lt, err := DecodeLodestoneTrackerComponent(r)
			if err != nil {
				return world.ItemStack{}, nil, err
			}
			tracker = &lt
		}
	}
	for i := int32(0); i < removedCount; i++ {
		if _, err := r.VarInt(); err != nil {
			return world.ItemStack{}, nil, err
		}
	}
	return world.NewItemStack(itemID, count), tracker, nil
}

type playerLodestoneSlotState struct {
	mu       sync.Mutex
	trackers map[int]LodestoneTracker
	carried  *LodestoneTracker
}

var playerLodestoneSlots sync.Map // map[*PlayerSession]*playerLodestoneSlotState

func getLodestoneSlotState(session *PlayerSession) *playerLodestoneSlotState {
	if session == nil {
		return nil
	}
	if v, ok := playerLodestoneSlots.Load(session); ok {
		return v.(*playerLodestoneSlotState)
	}
	initSt := &playerLodestoneSlotState{
		trackers: make(map[int]LodestoneTracker),
	}
	actual, _ := playerLodestoneSlots.LoadOrStore(session, initSt)
	return actual.(*playerLodestoneSlotState)
}

// GetSlotLodestoneTracker returns the LodestoneTracker stored in session's
// inventory slot (0..45) if the slot holds a compass with a lodestone_tracker component.
func GetSlotLodestoneTracker(session *PlayerSession, slot int) (LodestoneTracker, bool) {
	if session == nil || slot < 0 || slot >= world.PlayerInventorySlots {
		return LodestoneTracker{}, false
	}
	if !IsCompassItem(session.InventorySlot(slot)) {
		return LodestoneTracker{}, false
	}
	st := getLodestoneSlotState(session)
	st.mu.Lock()
	lt, ok := st.trackers[slot]
	st.mu.Unlock()
	return lt, ok
}

// SetSlotLodestoneTracker attaches a LodestoneTracker component to session's
// inventory slot (0..45).
func SetSlotLodestoneTracker(session *PlayerSession, slot int, lt LodestoneTracker) {
	if session == nil || slot < 0 || slot >= world.PlayerInventorySlots {
		return
	}
	if lt.Dimension != "" {
		lt.Dimension = world.NormalizeDimension(lt.Dimension)
	}
	st := getLodestoneSlotState(session)
	st.mu.Lock()
	st.trackers[slot] = lt
	st.mu.Unlock()
}

// ClearSlotLodestoneTracker removes any LodestoneTracker component from
// session's inventory slot (0..45).
func ClearSlotLodestoneTracker(session *PlayerSession, slot int) {
	if session == nil || slot < 0 || slot >= world.PlayerInventorySlots {
		return
	}
	st := getLodestoneSlotState(session)
	st.mu.Lock()
	delete(st.trackers, slot)
	st.mu.Unlock()
}

// GetCarriedLodestoneTracker returns the LodestoneTracker on session's cursor item, if any.
func GetCarriedLodestoneTracker(session *PlayerSession) (LodestoneTracker, bool) {
	if session == nil {
		return LodestoneTracker{}, false
	}
	st := getLodestoneSlotState(session)
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.carried == nil {
		return LodestoneTracker{}, false
	}
	return *st.carried, true
}

// SetCarriedLodestoneTracker sets or clears the LodestoneTracker on session's cursor item.
func SetCarriedLodestoneTracker(session *PlayerSession, lt LodestoneTracker, present bool) {
	if session == nil {
		return
	}
	st := getLodestoneSlotState(session)
	st.mu.Lock()
	if !present {
		st.carried = nil
	} else {
		cp := lt
		if cp.Dimension != "" {
			cp.Dimension = world.NormalizeDimension(cp.Dimension)
		}
		st.carried = &cp
	}
	st.mu.Unlock()
}

// SyncPlayerCompassSlot sends a PlayContainerSetSlot packet for player inventory
// slot (0..45), including DataComponentLodestoneTracker (67) when present.
func (s *Server) SyncPlayerCompassSlot(session *PlayerSession, slot int) error {
	if session == nil || slot < 0 || slot >= world.PlayerInventorySlots || session.send == nil {
		return nil
	}
	stack := session.InventorySlot(slot)
	stateID := session.NextContainerStateID()
	var trackerPtr *LodestoneTracker
	if lt, ok := GetSlotLodestoneTracker(session, slot); ok {
		trackerPtr = &lt
	}
	w := protocol.NewWriter(48)
	w.VarInt(0) // containerId 0 = player inventory
	w.VarInt(stateID)
	w.Uint16(uint16(slot))
	EncodeSlotWithLodestoneTracker(w, stack, trackerPtr)
	return session.send(protocol.PlayContainerSetSlot, w.Bytes())
}

// addLodestoneCompassToInventory places a single lodestone compass (with `lt`
// and `meta`) into the first available empty slot of `session`'s inventory
// (preferring hotbar 36..44, then main inventory 9..35), or merges into an
// existing stack with the exact same `LodestoneTracker` and `ItemMeta`.
func (s *Server) addLodestoneCompassToInventory(session *PlayerSession, lt LodestoneTracker, meta ItemMeta) (slot int, ok bool) {
	if session == nil {
		return -1, false
	}
	single := world.NewNamedItemStack(CompassItemID, 1)
	if single.IsEmpty() {
		return -1, false
	}

	// 1. Try merging into an existing compass stack that has the identical LodestoneTracker and ItemMeta.
	tryMerge := func(start, end int) (int, bool) {
		for idx := start; idx < end; idx++ {
			cur := session.InventorySlot(idx)
			if !IsCompassItem(cur) || cur.Count >= MaxItemStackSize {
				continue
			}
			curLT, hasLT := GetSlotLodestoneTracker(session, idx)
			if !hasLT || curLT != lt {
				continue
			}
			if !dispenserItemMetaEqual(GetSlotItemMeta(session, idx), meta) {
				continue
			}
			cur.Count++
			session.SetInventorySlot(idx, cur)
			return idx, true
		}
		return -1, false
	}
	if idx, merged := tryMerge(world.HotbarContainerOffset, world.HotbarContainerOffset+world.HotbarSlotCount); merged {
		_ = s.SyncPlayerCompassSlot(session, idx)
		return idx, true
	}
	if idx, merged := tryMerge(9, world.HotbarContainerOffset); merged {
		_ = s.SyncPlayerCompassSlot(session, idx)
		return idx, true
	}

	// 2. Place into the first empty hotbar or main inventory slot.
	tryEmpty := func(start, end int) (int, bool) {
		for idx := start; idx < end; idx++ {
			if !session.InventorySlot(idx).IsEmpty() {
				continue
			}
			session.SetInventorySlot(idx, single)
			SetSlotLodestoneTracker(session, idx, lt)
			SetSlotItemMeta(session, idx, meta)
			return idx, true
		}
		return -1, false
	}
	if idx, placed := tryEmpty(world.HotbarContainerOffset, world.HotbarContainerOffset+world.HotbarSlotCount); placed {
		_ = s.SyncPlayerCompassSlot(session, idx)
		return idx, true
	}
	if idx, placed := tryEmpty(9, world.HotbarContainerOffset); placed {
		_ = s.SyncPlayerCompassSlot(session, idx)
		return idx, true
	}
	return -1, false
}

// UseCompassOnLodestone handles right-clicking a `minecraft:lodestone` block at
// (x, y, z) while holding a `minecraft:compass` in `hand`, matching vanilla
// 26.1.2 `CompassItem.useOn`:
//   - Plays `minecraft:item.lodestone_compass.lock` (`SoundSourcePlayers`, vol 1.0, pitch 1.0).
//   - Grants advancement `minecraft:nether/use_lodestone` ("Country Lode, Take Me Home").
//   - Sets `DataComponents.LODESTONE_TRACKER` (`target={dimension, pos}`, `tracked=true`):
//     if `!creative && stack.Count == 1`, mutates the held slot in place;
//     otherwise decrements the held stack by 1 (when `!creative`) and adds a
//     1-count lodestone compass to the player's inventory (or drops it if full).
func (s *Server) UseCompassOnLodestone(session *PlayerSession, hand int32, x, y, z int) bool {
	if s == nil || session == nil || session.IsDead() {
		return false
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator {
		return false
	}
	dim := world.NormalizeDimension(snap.Dimension)
	cache := s.DimensionChunks(dim)
	if cache == nil || !world.IsLodestoneBlock(cache.GetBlock(x, y, z)) {
		return false
	}

	invSlot := session.HeldInventorySlot()
	if hand == HandOffHand {
		invSlot = OffHandContainerSlot
	}
	if invSlot < 0 || invSlot >= world.PlayerInventorySlots {
		return false
	}
	heldStack := session.InventorySlot(invSlot)
	if !IsCompassItem(heldStack) {
		return false
	}
	heldMeta := GetSlotItemMeta(session, invSlot)

	cx := int32(x >> 4)
	cz := int32(z >> 4)
	s.BroadcastDimensionChunk(dim, cx, cz, PlaySoundPacketID,
		EncodeSoundPacket("minecraft:item.lodestone_compass.lock", SoundSourcePlayers, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, 1.0, 1.0, 0))
	s.TriggerUseLodestone(session)
	s.RecordItemUsed(session, CompassItemID, 1)

	tracker := NewLodestoneTracker(dim, x, y, z, true)
	creative := snap.GameMode == GameModeCreative
	replaceExistingStack := !creative && heldStack.Count == 1

	if replaceExistingStack {
		SetSlotLodestoneTracker(session, invSlot, tracker)
		_ = s.SyncPlayerCompassSlot(session, invSlot)
		return true
	}

	if !creative {
		heldStack.Count--
		if heldStack.Count <= 0 {
			session.SetInventorySlot(invSlot, world.ItemStack{})
			ClearSlotLodestoneTracker(session, invSlot)
			SetSlotItemMeta(session, invSlot, ItemMeta{})
		} else {
			session.SetInventorySlot(invSlot, heldStack)
		}
		_ = s.SyncPlayerCompassSlot(session, invSlot)
	}

	if _, added := s.addLodestoneCompassToInventory(session, tracker, heldMeta); !added {
		single := world.NewNamedItemStack(CompassItemID, 1)
		s.SpawnItemEntityInDimension(dim, snap.X, snap.Y, snap.Z, single)
	}
	return true
}

// TickPlayerLodestoneCompasses runs vanilla `CompassItem.inventoryTick` ->
// `LodestoneTracker.tick(ServerLevel)` across all inventory slots of `session`,
// clearing `HasTarget` (while preserving `Tracked: true`) on any tracked
// lodestone compass whose target is in `session`'s current dimension and no
// longer has a `minecraft:lodestone` block at `target.pos`.
func (s *Server) TickPlayerLodestoneCompasses(session *PlayerSession) int {
	if s == nil || session == nil {
		return 0
	}
	dim := world.NormalizeDimension(session.Dimension())
	cache := s.DimensionChunks(dim)
	if cache == nil {
		return 0
	}
	updatedCount := 0
	for slot := 0; slot < world.PlayerInventorySlots; slot++ {
		lt, ok := GetSlotLodestoneTracker(session, slot)
		if !ok {
			continue
		}
		if next, changed := lt.Tick(dim, cache); changed {
			SetSlotLodestoneTracker(session, slot, next)
			_ = s.SyncPlayerCompassSlot(session, slot)
			updatedCount++
		}
	}
	return updatedCount
}

// OnLodestoneBlockBroken notifies all online players when a `minecraft:lodestone`
// block at (dim, x, y, z) is broken, immediately clearing `HasTarget` (while
// preserving `Tracked: true`) on any tracked lodestone compass pointing to that
// block. Compasses with `Tracked == false` are left unmodified per vanilla.
func (s *Server) OnLodestoneBlockBroken(dim string, x, y, z int) int {
	if s == nil {
		return 0
	}
	normDim := world.NormalizeDimension(dim)
	cache := s.DimensionChunks(normDim)
	var sessions []*PlayerSession
	s.playersMu.RLock()
	for _, sess := range s.players {
		if sess != nil {
			sessions = append(sessions, sess)
		}
	}
	s.playersMu.RUnlock()

	invalidated := 0
	for _, sess := range sessions {
		for slot := 0; slot < world.PlayerInventorySlots; slot++ {
			lt, ok := GetSlotLodestoneTracker(sess, slot)
			if !ok || !lt.Tracked || !lt.HasTarget {
				continue
			}
			if world.NormalizeDimension(lt.Dimension) != normDim {
				continue
			}
			if int(lt.X) == x && int(lt.Y) == y && int(lt.Z) == z {
				if cache != nil && world.IsLodestoneBlock(cache.GetBlock(x, y, z)) {
					continue
				}
				next := LodestoneTracker{
					HasTarget: false,
					Tracked:   true,
				}
				SetSlotLodestoneTracker(sess, slot, next)
				_ = s.SyncPlayerCompassSlot(sess, slot)
				invalidated++
			}
		}
	}
	return invalidated
}
