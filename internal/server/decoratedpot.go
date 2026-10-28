package server

import (
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"regionio/internal/nbt"
	"regionio/internal/world"
)

// DecoratedPotWobbleStyle identifies the wobble animation style sent via
// ClientboundBlockEventPacket (actionID=1, actionParam=0 for POSITIVE or 1 for NEGATIVE).
type DecoratedPotWobbleStyle byte

const (
	DecoratedPotWobbleNone     DecoratedPotWobbleStyle = 0
	DecoratedPotWobblePositive DecoratedPotWobbleStyle = 1 // BlockEvent param 0
	DecoratedPotWobbleNegative DecoratedPotWobbleStyle = 2 // BlockEvent param 1

	// BlockEntityTypeDecoratedPot is BuiltInRegistries.BLOCK_ENTITY_TYPE index 42 ("minecraft:decorated_pot").
	BlockEntityTypeDecoratedPot int32 = 42
)

// All 23 vanilla pottery sherds in Minecraft 26.1.2 (`#minecraft:decorated_pot_sherds`).
var vanillaPotterySherdSet = map[string]bool{
	"minecraft:angler_pottery_sherd":     true,
	"minecraft:archer_pottery_sherd":     true,
	"minecraft:arms_up_pottery_sherd":    true,
	"minecraft:blade_pottery_sherd":      true,
	"minecraft:brewer_pottery_sherd":     true,
	"minecraft:burn_pottery_sherd":       true,
	"minecraft:danger_pottery_sherd":     true,
	"minecraft:explorer_pottery_sherd":   true,
	"minecraft:flow_pottery_sherd":       true,
	"minecraft:friend_pottery_sherd":     true,
	"minecraft:guster_pottery_sherd":     true,
	"minecraft:heart_pottery_sherd":      true,
	"minecraft:heartbreak_pottery_sherd": true,
	"minecraft:howl_pottery_sherd":       true,
	"minecraft:miner_pottery_sherd":      true,
	"minecraft:mourner_pottery_sherd":    true,
	"minecraft:plenty_pottery_sherd":     true,
	"minecraft:prize_pottery_sherd":      true,
	"minecraft:scrape_pottery_sherd":     true,
	"minecraft:sheaf_pottery_sherd":      true,
	"minecraft:shelter_pottery_sherd":    true,
	"minecraft:skull_pottery_sherd":      true,
	"minecraft:snort_pottery_sherd":      true,
}

// VanillaPotterySherds returns a sorted slice of all 23 vanilla pottery sherd item IDs.
func VanillaPotterySherds() []string {
	out := make([]string, 0, len(vanillaPotterySherdSet))
	for id := range vanillaPotterySherdSet {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// IsPotterySherd reports whether itemID is one of the 23 vanilla pottery sherds.
func IsPotterySherd(itemID string) bool {
	norm := canonicalResourceName(itemID)
	if vanillaPotterySherdSet[norm] {
		return true
	}
	return strings.HasSuffix(norm, "_pottery_sherd")
}

// IsDecoratedPotIngredient reports whether itemID can be used as one of the 4
// diamond-shaped side ingredients when crafting a `minecraft:decorated_pot`
// (`minecraft:brick` or any `*_pottery_sherd`).
func IsDecoratedPotIngredient(itemID string) bool {
	norm := canonicalResourceName(itemID)
	return norm == "minecraft:brick" || IsPotterySherd(norm)
}

// DefaultDecoratedPotSherds returns the default 4-side sherd array
// (`[back, left, right, front]`), where all 4 sides are `"minecraft:brick"`.
func DefaultDecoratedPotSherds() [4]string {
	return [4]string{
		"minecraft:brick",
		"minecraft:brick",
		"minecraft:brick",
		"minecraft:brick",
	}
}

func normalizePotSherds(sherds [4]string) [4]string {
	out := DefaultDecoratedPotSherds()
	for i := 0; i < 4; i++ {
		if IsDecoratedPotIngredient(sherds[i]) {
			out[i] = canonicalResourceName(sherds[i])
		}
	}
	return out
}

// MatchDecoratedPotRecipe matches the special vanilla `minecraft:decorated_pot`
// 3x3 crafting recipe:
//   - Slots 1 (top = back), 3 (left = left), 5 (right = right), 7 (bottom = front)
//     must each contain a `minecraft:brick` or `*_pottery_sherd`.
//   - Slots 0, 2, 4, 6, 8 must be empty.
//
// Returns `1x minecraft:decorated_pot`, `[4]string{back, left, right, front}`, and `true` on match.
func MatchDecoratedPotRecipe(grid []world.ItemStack) (result world.ItemStack, sherds [4]string, ok bool) {
	if len(grid) < 9 {
		return world.ItemStack{}, sherds, false
	}
	for _, emptyIdx := range []int{0, 2, 4, 6, 8} {
		if !grid[emptyIdx].IsEmpty() && grid[emptyIdx].Count > 0 {
			return world.ItemStack{}, sherds, false
		}
	}
	sideIndices := [4]int{1, 3, 5, 7} // [back, left, right, front]
	for i, slotIdx := range sideIndices {
		st := grid[slotIdx]
		if st.IsEmpty() || st.Count <= 0 {
			return world.ItemStack{}, sherds, false
		}
		id := canonicalResourceName(st.ResolvedID())
		if !IsDecoratedPotIngredient(id) {
			return world.ItemStack{}, sherds, false
		}
		sherds[i] = id
	}
	return world.NewNamedItemStack("minecraft:decorated_pot", 1), sherds, true
}

// --- Decorated Pot Block State Helpers ---

// IsDecoratedPotBlock reports whether state is a `minecraft:decorated_pot`.
func IsDecoratedPotBlock(state uint16) bool {
	return world.BlockName(state) == "minecraft:decorated_pot"
}

// DecoratedPotBlockState returns the block state ID for `minecraft:decorated_pot`
// with the given horizontal facing ("north", "south", "west", "east"), cracked,
// and waterlogged flags.
func DecoratedPotBlockState(facing string, cracked, waterlogged bool) uint16 {
	f := strings.ToLower(strings.TrimSpace(facing))
	switch f {
	case "north", "south", "west", "east":
	default:
		f = "north"
	}
	crackedStr := "false"
	if cracked {
		crackedStr = "true"
	}
	waterStr := "false"
	if waterlogged {
		waterStr = "true"
	}
	if st, ok := world.BlockStateIDByProperties("minecraft:decorated_pot", map[string]string{
		"facing":      f,
		"cracked":     crackedStr,
		"waterlogged": waterStr,
	}); ok {
		return st
	}
	return 0
}

// DecoratedPotFacing returns the `facing` property of a `minecraft:decorated_pot` state.
func DecoratedPotFacing(state uint16) string {
	f := strings.ToLower(world.BlockProperties(state)["facing"])
	switch f {
	case "north", "south", "west", "east":
		return f
	default:
		return "north"
	}
}

// DecoratedPotCracked reports whether the `cracked` property of a
// `minecraft:decorated_pot` state is true.
func DecoratedPotCracked(state uint16) bool {
	return world.BlockProperties(state)["cracked"] == "true"
}

// DecoratedPotWaterlogged reports whether the `waterlogged` property of a
// `minecraft:decorated_pot` state is true.
func DecoratedPotWaterlogged(state uint16) bool {
	return world.BlockProperties(state)["waterlogged"] == "true"
}

// DecoratedPotRegistryID returns the BuiltInRegistries.BLOCK numeric ID for
// `minecraft:decorated_pot` (used in ClientboundBlockEventPacket).
func DecoratedPotRegistryID() int32 {
	if id, ok := world.BlockRegistryID("minecraft:decorated_pot"); ok {
		return id
	}
	return 0
}

// --- DecoratedPotBlockEntity & Server Store ---

// DecoratedPotBlockEntity represents the server-side state of a
// `minecraft:decorated_pot` at (Dimension, X, Y, Z), storing its 4 side sherds
// (`[back, left, right, front]`), its single stored ItemStack (up to maxStackSize),
// and its last wobble animation style.
type DecoratedPotBlockEntity struct {
	Dimension       string
	X, Y, Z         int
	Sherds          [4]string // [0]=Back, [1]=Left, [2]=Right, [3]=Front
	Item            world.ItemStack
	Meta            ItemMeta
	LastWobbleStyle DecoratedPotWobbleStyle
}

// Clone returns a deep copy of the DecoratedPotBlockEntity.
func (dp *DecoratedPotBlockEntity) Clone() *DecoratedPotBlockEntity {
	if dp == nil {
		return nil
	}
	cp := *dp
	cp.Meta = dp.Meta.Clone()
	return &cp
}

type serverDecoratedPotStore struct {
	mu                sync.Mutex
	pots              map[hopperBlockKey]*DecoratedPotBlockEntity
	lastCraftedSherds [4]string
	slotSherds        map[*PlayerSession]map[int][4]string
}

var serverDecoratedPots sync.Map // map[*Server]*serverDecoratedPotStore

func (s *Server) decoratedPotStore() *serverDecoratedPotStore {
	if v, ok := serverDecoratedPots.Load(s); ok {
		return v.(*serverDecoratedPotStore)
	}
	st := &serverDecoratedPotStore{
		pots:              make(map[hopperBlockKey]*DecoratedPotBlockEntity),
		lastCraftedSherds: DefaultDecoratedPotSherds(),
		slotSherds:        make(map[*PlayerSession]map[int][4]string),
	}
	actual, _ := serverDecoratedPots.LoadOrStore(s, st)
	return actual.(*serverDecoratedPotStore)
}

func (s *Server) getOrCreateDecoratedPotLocked(st *serverDecoratedPotStore, dim string, x, y, z int) *DecoratedPotBlockEntity {
	normDim := normalizeHopperDim(dim)
	key := hopperBlockKey{dim: normDim, x: x, y: y, z: z}
	dp := st.pots[key]
	if dp == nil {
		dp = &DecoratedPotBlockEntity{
			Dimension: normDim,
			X:         x,
			Y:         y,
			Z:         z,
			Sherds:    DefaultDecoratedPotSherds(),
		}
		if s != nil {
			if cache := s.DimensionChunks(normDim); cache != nil {
				if sbe, ok := cache.StructureBlockEntityAt(x, y, z); ok && canonicalResourceName(sbe.ID) == "minecraft:decorated_pot" {
					var shArr [4]string = DefaultDecoratedPotSherds()
					for i := 0; i < 4 && i < len(sbe.Sherds); i++ {
						shArr[i] = sbe.Sherds[i]
					}
					dp.Sherds = normalizePotSherds(shArr)
					for _, slot := range sbe.Items {
						if slot.ItemID != "" && slot.Count > 0 {
							dp.Item = world.NewNamedItemStack(slot.ItemID, int32(slot.Count))
							break
						}
					}
				}
			}
		}
		st.pots[key] = dp
	}
	return dp
}

// IsAllSherdsDecoratedPot reports whether all 4 sides of sherds are pottery
// sherds (`*_pottery_sherd`) rather than `minecraft:brick`.
func IsAllSherdsDecoratedPot(sherds [4]string) bool {
	for i := 0; i < 4; i++ {
		if !IsPotterySherd(sherds[i]) {
			return false
		}
	}
	return true
}

// OnDecoratedPotCrafted records the 4 crafted sherds on the server and grants
// `"minecraft:adventure/craft_decorated_pot_using_only_sherds"` ("Careful Restoration")
// when all 4 sides are pottery sherds.
func (s *Server) OnDecoratedPotCrafted(session *PlayerSession, sherds [4]string) bool {
	if s != nil {
		s.SetLastCraftedDecoratedPotSherds(sherds)
	}
	if s != nil && session != nil && IsAllSherdsDecoratedPot(sherds) {
		ensureArchaeologyAdvancementsRegistered()
		return s.GrantAdvancement(session, "minecraft:adventure/craft_decorated_pot_using_only_sherds")
	}
	return false
}

// SetLastCraftedDecoratedPotSherds records the 4 sherds of the most recently
// crafted `minecraft:decorated_pot`.
func (s *Server) SetLastCraftedDecoratedPotSherds(sherds [4]string) {
	if s == nil {
		return
	}
	st := s.decoratedPotStore()
	st.mu.Lock()
	st.lastCraftedSherds = normalizePotSherds(sherds)
	st.mu.Unlock()
}

// LastCraftedDecoratedPotSherds returns the 4 sherds of the most recently
// crafted `minecraft:decorated_pot`.
func (s *Server) LastCraftedDecoratedPotSherds() [4]string {
	if s == nil {
		return DefaultDecoratedPotSherds()
	}
	st := s.decoratedPotStore()
	st.mu.Lock()
	sh := st.lastCraftedSherds
	st.mu.Unlock()
	return sh
}

// SetSlotDecoratedPotSherds associates 4 side sherds with a `minecraft:decorated_pot`
// item in `session`'s inventory slot `slot`.
func SetSlotDecoratedPotSherds(s *Server, session *PlayerSession, slot int, sherds [4]string) {
	if s == nil || session == nil {
		return
	}
	st := s.decoratedPotStore()
	st.mu.Lock()
	m := st.slotSherds[session]
	if m == nil {
		m = make(map[int][4]string)
		st.slotSherds[session] = m
	}
	m[slot] = normalizePotSherds(sherds)
	st.mu.Unlock()
}

// GetSlotDecoratedPotSherds returns the 4 side sherds associated with a
// `minecraft:decorated_pot` item in `session`'s inventory slot `slot`.
func GetSlotDecoratedPotSherds(s *Server, session *PlayerSession, slot int) ([4]string, bool) {
	if s == nil || session == nil {
		return DefaultDecoratedPotSherds(), false
	}
	st := s.decoratedPotStore()
	st.mu.Lock()
	defer st.mu.Unlock()
	if m := st.slotSherds[session]; m != nil {
		if sh, ok := m[slot]; ok {
			return sh, true
		}
	}
	return DefaultDecoratedPotSherds(), false
}

// PlaceDecoratedPotAt places a `minecraft:decorated_pot` block at (dim, x, y, z)
// with the given horizontal `facing` and 4 side `sherds`, initializing its
// `DecoratedPotBlockEntity`.
func (s *Server) PlaceDecoratedPotAt(dim string, x, y, z int, facing string, sherds [4]string) (*DecoratedPotBlockEntity, world.BlockUpdateResult) {
	var res world.BlockUpdateResult
	if s == nil {
		return nil, res
	}
	normDim := normalizeHopperDim(dim)
	cache := s.BindNoteBlockAndJukeboxRedstone(normDim)
	waterlogged := false
	if cache != nil && cache.GetBlock(x, y, z) == world.StateWater {
		waterlogged = true
	}
	potState := DecoratedPotBlockState(facing, false, waterlogged)
	if cache != nil && potState != 0 {
		_, res = cache.ApplyBlockWithUpdates(x, y, z, potState)
		s.broadcastBlockUpdateInDim(normDim, x, y, z, potState)
		for _, ch := range res.Changes {
			s.broadcastBlockUpdateInDim(normDim, ch.Pos.X, ch.Pos.Y, ch.Pos.Z, ch.NewState)
		}
	}

	st := s.decoratedPotStore()
	st.mu.Lock()
	dp := s.getOrCreateDecoratedPotLocked(st, normDim, x, y, z)
	dp.Sherds = normalizePotSherds(sherds)
	dp.Item = world.ItemStack{}
	dp.Meta = ItemMeta{}
	dp.LastWobbleStyle = DecoratedPotWobbleNone
	cp := dp.Clone()
	st.mu.Unlock()

	s.BroadcastDecoratedPotBlockEntityData(normDim, x, y, z)
	s.updateHopperComparatorsAndBroadcast(normDim, x, y, z)
	return cp, res
}

// GetDecoratedPotAt returns a snapshot of the DecoratedPotBlockEntity at (dim, x, y, z).
func (s *Server) GetDecoratedPotAt(dim string, x, y, z int) (*DecoratedPotBlockEntity, bool) {
	if s == nil {
		return nil, false
	}
	normDim := normalizeHopperDim(dim)
	cache := s.DimensionChunks(normDim)
	st := s.decoratedPotStore()
	st.mu.Lock()
	dp, ok := st.pots[hopperBlockKey{dim: normDim, x: x, y: y, z: z}]
	if !ok && cache != nil && IsDecoratedPotBlock(cache.GetBlock(x, y, z)) {
		dp = s.getOrCreateDecoratedPotLocked(st, normDim, x, y, z)
		ok = true
	}
	var cp *DecoratedPotBlockEntity
	if ok && dp != nil {
		cp = dp.Clone()
	}
	st.mu.Unlock()
	return cp, cp != nil
}

// SetDecoratedPotSherds sets the 4 side sherds (`[back, left, right, front]`)
// of the `minecraft:decorated_pot` at (dim, x, y, z).
func (s *Server) SetDecoratedPotSherds(dim string, x, y, z int, sherds [4]string) bool {
	if s == nil {
		return false
	}
	normDim := normalizeHopperDim(dim)
	st := s.decoratedPotStore()
	st.mu.Lock()
	dp := s.getOrCreateDecoratedPotLocked(st, normDim, x, y, z)
	dp.Sherds = normalizePotSherds(sherds)
	st.mu.Unlock()
	s.BroadcastDecoratedPotBlockEntityData(normDim, x, y, z)
	return true
}

// SetDecoratedPotItem sets the stored ItemStack and ItemMeta inside the
// `minecraft:decorated_pot` at (dim, x, y, z) and updates adjacent comparators.
func (s *Server) SetDecoratedPotItem(dim string, x, y, z int, stack world.ItemStack, meta ItemMeta) bool {
	if s == nil {
		return false
	}
	normDim := normalizeHopperDim(dim)
	s.BindNoteBlockAndJukeboxRedstone(normDim)
	st := s.decoratedPotStore()
	st.mu.Lock()
	dp := s.getOrCreateDecoratedPotLocked(st, normDim, x, y, z)
	if stack.IsEmpty() || stack.Count <= 0 {
		dp.Item = world.ItemStack{}
		dp.Meta = ItemMeta{}
	} else {
		maxStack := DispenserItemMaxStackSize(stack)
		cnt := stack.Count
		if cnt > maxStack {
			cnt = maxStack
		}
		if stack.ItemID <= 0 && stack.ID != "" {
			dp.Item = world.NewNamedItemStack(stack.ID, cnt)
		} else {
			dp.Item = world.NewItemStack(stack.ResolvedItemID(), cnt)
		}
		dp.Meta = meta.Clone()
	}
	st.mu.Unlock()
	s.BroadcastDecoratedPotBlockEntityData(normDim, x, y, z)
	s.updateHopperComparatorsAndBroadcast(normDim, x, y, z)
	return true
}

// --- Wobble Animation & Player Item Insertion ---

// WobbleDecoratedPot triggers a wobble animation (`DecoratedPotWobblePositive`
// or `DecoratedPotWobbleNegative`) on the `minecraft:decorated_pot` at
// (dim, x, y, z) and broadcasts ClientboundBlockEventPacket (0x07).
func (s *Server) WobbleDecoratedPot(dim string, x, y, z int, style DecoratedPotWobbleStyle) {
	if s == nil || style == DecoratedPotWobbleNone {
		return
	}
	normDim := normalizeHopperDim(dim)
	st := s.decoratedPotStore()
	st.mu.Lock()
	dp := s.getOrCreateDecoratedPotLocked(st, normDim, x, y, z)
	dp.LastWobbleStyle = style
	st.mu.Unlock()

	param := byte(0)
	if style == DecoratedPotWobbleNegative {
		param = 1
	}
	cx, cz := int32(x>>4), int32(z>>4)
	s.BroadcastDimensionChunk(normDim, cx, cz, PlayBlockEventCB,
		EncodeBlockEventPacket(x, y, z, DecoratedPotRegistryID(), 1, param))
}

// InteractDecoratedPot handles a player right-clicking a `minecraft:decorated_pot`
// at (dim, x, y, z):
//   - If the player holds an item that can be inserted into the pot's single
//     slot (empty pot, or matching item & metadata with count < maxStackSize),
//     inserts 1 item, triggers `DecoratedPotWobblePositive`, plays
//     `minecraft:block.decorated_pot.insert` with fullness pitch, updates
//     comparators, and emits a `block_change` vibration.
//   - Otherwise (empty hand, different item, or full pot), triggers
//     `DecoratedPotWobbleNegative`, plays `minecraft:block.decorated_pot.insert_fail`,
//     and emits a `block_change` vibration.
func (s *Server) InteractDecoratedPot(session *PlayerSession, dim string, x, y, z int) (inserted bool, wobbled bool) {
	if s == nil {
		return false, false
	}
	normDim := normalizeHopperDim(dim)
	if session != nil && dim == "" {
		if d := session.Dimension(); d != "" {
			normDim = normalizeHopperDim(d)
		}
	}
	cache := s.BindNoteBlockAndJukeboxRedstone(normDim)
	if cache != nil {
		state := cache.GetBlock(x, y, z)
		if state != world.StateAir && !IsDecoratedPotBlock(state) {
			return false, false
		}
	}

	var held world.ItemStack
	var heldMeta ItemMeta
	if session != nil {
		held = session.HeldItem()
		heldMeta = session.HeldItemMeta()
	}

	cx, cz := int32(x>>4), int32(z>>4)

	if !held.IsEmpty() && held.Count > 0 {
		maxStack := DispenserItemMaxStackSize(held)
		st := s.decoratedPotStore()
		st.mu.Lock()
		dp := s.getOrCreateDecoratedPotLocked(st, normDim, x, y, z)
		canInsert := false
		var newCount int32
		if dp.Item.IsEmpty() || dp.Item.Count <= 0 {
			canInsert = true
			newCount = 1
			dp.Item = world.NewNamedItemStack(held.ResolvedID(), 1)
			dp.Meta = heldMeta.Clone()
		} else if canonicalResourceName(dp.Item.ResolvedID()) == canonicalResourceName(held.ResolvedID()) && itemMetaEqual(dp.Meta, heldMeta) && dp.Item.Count < maxStack {
			canInsert = true
			newCount = dp.Item.Count + 1
			dp.Item = world.NewNamedItemStack(held.ResolvedID(), newCount)
		}
		if canInsert {
			dp.LastWobbleStyle = DecoratedPotWobblePositive
		}
		st.mu.Unlock()

		if canInsert {
			if session != nil {
				s.ConsumeHeldItem(session, 1)
			}

			s.WobbleDecoratedPot(normDim, x, y, z, DecoratedPotWobblePositive)
			pitch := float32(0.7) + float32(0.5)*(float32(newCount)/float32(maxStack))
			s.BroadcastDimensionChunk(normDim, cx, cz, PlaySoundPacketID,
				EncodeSoundPacket("minecraft:block.decorated_pot.insert", SoundSourceBlocks, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, 1.0, pitch, 0))
			s.BroadcastDecoratedPotBlockEntityData(normDim, x, y, z)
			s.updateHopperComparatorsAndBroadcast(normDim, x, y, z)
			s.EmitVibration(VibrationEvent{
				Dimension: normDim,
				Type:      GameEventBlockChange,
				X:         float64(x) + 0.5,
				Y:         float64(y) + 0.5,
				Z:         float64(z) + 0.5,
				Player:    session,
			})
			return true, true
		}
	}

	// Failed insert (empty hand, mismatched item, or pot already full) -> NEGATIVE wobble.
	s.WobbleDecoratedPot(normDim, x, y, z, DecoratedPotWobbleNegative)
	s.BroadcastDimensionChunk(normDim, cx, cz, PlaySoundPacketID,
		EncodeSoundPacket("minecraft:block.decorated_pot.insert_fail", SoundSourceBlocks, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, 1.0, 1.0, 0))
	s.EmitVibration(VibrationEvent{
		Dimension: normDim,
		Type:      GameEventBlockChange,
		X:         float64(x) + 0.5,
		Y:         float64(y) + 0.5,
		Z:         float64(z) + 0.5,
		Player:    session,
	})
	return false, true
}

// --- Comparator Signal & Hopper Transfer for Decorated Pot ---

// DecoratedPotComparatorSignalAt returns the redstone comparator signal (0..15)
// for a `minecraft:decorated_pot` at (dim, x, y, z) based on its single slot's
// fullness (`1 + floor(count / maxStack * 14)` when non-empty).
func (s *Server) DecoratedPotComparatorSignalAt(dim string, x, y, z int) int {
	if s == nil {
		return 0
	}
	normDim := normalizeHopperDim(dim)
	cache := s.DimensionChunks(normDim)
	isPotBlock := false
	if cache != nil {
		st := cache.GetBlock(x, y, z)
		if st != world.StateAir && !IsDecoratedPotBlock(st) {
			return 0
		}
		isPotBlock = IsDecoratedPotBlock(st)
	}
	st := s.decoratedPotStore()
	st.mu.Lock()
	dp := st.pots[hopperBlockKey{dim: normDim, x: x, y: y, z: z}]
	if dp == nil && isPotBlock {
		dp = s.getOrCreateDecoratedPotLocked(st, normDim, x, y, z)
	}
	if dp == nil || dp.Item.IsEmpty() || dp.Item.Count <= 0 {
		st.mu.Unlock()
		return 0
	}
	cnt := dp.Item.Count
	maxStack := DispenserItemMaxStackSize(dp.Item)
	st.mu.Unlock()
	if maxStack <= 0 {
		maxStack = 64
	}
	frac := float64(cnt) / float64(maxStack)
	if frac > 1.0 {
		frac = 1.0
	}
	return int(math.Floor(frac*14.0)) + 1
}

// HopperInsertIntoDecoratedPot inserts 1 item into the `minecraft:decorated_pot`
// at (dim, x, y, z) if the pot is empty or holds the same item with count < maxStack.
func (s *Server) HopperInsertIntoDecoratedPot(dim string, x, y, z int, stack world.ItemStack, meta ItemMeta) bool {
	if s == nil || stack.IsEmpty() || stack.Count <= 0 {
		return false
	}
	normDim := normalizeHopperDim(dim)
	s.BindNoteBlockAndJukeboxRedstone(normDim)
	maxStack := DispenserItemMaxStackSize(stack)
	single := world.NewNamedItemStack(stack.ResolvedID(), 1)

	st := s.decoratedPotStore()
	st.mu.Lock()
	dp := s.getOrCreateDecoratedPotLocked(st, normDim, x, y, z)
	if dp.Item.IsEmpty() || dp.Item.Count <= 0 {
		dp.Item = single
		dp.Meta = meta.Clone()
		st.mu.Unlock()
		s.BroadcastDecoratedPotBlockEntityData(normDim, x, y, z)
		s.updateHopperComparatorsAndBroadcast(normDim, x, y, z)
		return true
	}
	if canonicalResourceName(dp.Item.ResolvedID()) == canonicalResourceName(single.ResolvedID()) && itemMetaEqual(dp.Meta, meta) && dp.Item.Count < maxStack {
		dp.Item = world.NewNamedItemStack(stack.ResolvedID(), dp.Item.Count+1)
		st.mu.Unlock()
		s.BroadcastDecoratedPotBlockEntityData(normDim, x, y, z)
		s.updateHopperComparatorsAndBroadcast(normDim, x, y, z)
		return true
	}
	st.mu.Unlock()
	return false
}

// HopperExtractFromDecoratedPot extracts 1 item from the `minecraft:decorated_pot`
// at (dim, x, y, z).
func (s *Server) HopperExtractFromDecoratedPot(dim string, x, y, z int) (world.ItemStack, ItemMeta, bool) {
	if s == nil {
		return world.ItemStack{}, ItemMeta{}, false
	}
	normDim := normalizeHopperDim(dim)
	cache := s.BindNoteBlockAndJukeboxRedstone(normDim)
	isPotBlock := cache != nil && IsDecoratedPotBlock(cache.GetBlock(x, y, z))
	st := s.decoratedPotStore()
	st.mu.Lock()
	dp := st.pots[hopperBlockKey{dim: normDim, x: x, y: y, z: z}]
	if dp == nil && isPotBlock {
		dp = s.getOrCreateDecoratedPotLocked(st, normDim, x, y, z)
	}
	if dp == nil || dp.Item.IsEmpty() || dp.Item.Count <= 0 {
		st.mu.Unlock()
		return world.ItemStack{}, ItemMeta{}, false
	}
	out := world.NewNamedItemStack(dp.Item.ResolvedID(), 1)
	meta := dp.Meta.Clone()
	if dp.Item.Count <= 1 {
		dp.Item = world.ItemStack{}
		dp.Meta = ItemMeta{}
	} else {
		dp.Item = world.NewNamedItemStack(dp.Item.ResolvedID(), dp.Item.Count-1)
	}
	st.mu.Unlock()
	s.updateHopperComparatorsAndBroadcast(normDim, x, y, z)
	return out, meta, true
}

// --- Projectile Shattering & Block Breaking (Sherd & Stored Item Drops) ---

// ShouldToolShatterDecoratedPot reports whether breaking a `minecraft:decorated_pot`
// with `toolStack` (and `toolMeta`) shatters it into its 4 sherds/bricks rather
// than dropping the intact `minecraft:decorated_pot` item.
// In vanilla 26.1.2, swords, pickaxes, axes, shovels, and hoes WITHOUT Silk Touch
// shatter the pot; breaking by hand or with Silk Touch drops the intact pot.
func ShouldToolShatterDecoratedPot(toolStack world.ItemStack, toolMeta ItemMeta) bool {
	if toolStack.IsEmpty() || toolStack.Count <= 0 {
		return false
	}
	if toolMeta.EnchantmentLevel("minecraft:silk_touch") > 0 {
		return false
	}
	id := canonicalResourceName(toolStack.ResolvedID())
	return strings.HasSuffix(id, "_sword") ||
		strings.HasSuffix(id, "_pickaxe") ||
		strings.HasSuffix(id, "_axe") ||
		strings.HasSuffix(id, "_shovel") ||
		strings.HasSuffix(id, "_hoe") ||
		id == "minecraft:trident" ||
		id == "minecraft:mace"
}

// BreakDecoratedPotAt removes the `DecoratedPotBlockEntity` at (dim, x, y, z),
// updates the world block (`cracked=true` before breaking when `shatter` is true),
// spawns item drops when `spawnDrops` and `DoTileDrops()` are true, and returns
// the dropped items, stored item stack, and 4 side sherds.
func (s *Server) BreakDecoratedPotAt(
	dim string,
	x, y, z int,
	shatter bool,
	spawnDrops bool,
) (drops []world.ItemStack, storedItem world.ItemStack, storedMeta ItemMeta, sherds [4]string, existed bool) {
	if s == nil {
		return nil, world.ItemStack{}, ItemMeta{}, DefaultDecoratedPotSherds(), false
	}
	normDim := normalizeHopperDim(dim)
	cache := s.BindNoteBlockAndJukeboxRedstone(normDim)
	key := hopperBlockKey{dim: normDim, x: x, y: y, z: z}

	blockIsPot := false
	if cache != nil && IsDecoratedPotBlock(cache.GetBlock(x, y, z)) {
		blockIsPot = true
	}

	st := s.decoratedPotStore()
	st.mu.Lock()
	dp, ok := st.pots[key]
	if !ok && blockIsPot {
		dp = s.getOrCreateDecoratedPotLocked(st, normDim, x, y, z)
		ok = dp != nil
	}
	if ok {
		delete(st.pots, key)
	}
	st.mu.Unlock()

	if cache != nil {
		cache.RemoveStructureBlockEntity(x, y, z)
		curState := cache.GetBlock(x, y, z)
		if IsDecoratedPotBlock(curState) {
			blockIsPot = true
			if shatter {
				if crackedSt, okSt := world.WithBlockProperty(curState, "cracked", "true"); okSt && crackedSt != curState {
					s.setDispenserWorldBlock(normDim, cache, x, y, z, crackedSt)
				}
			}
			if broken, upd := cache.BreakBlockWithUpdates(x, y, z); broken {
				s.broadcastDimensionBlockChanges(normDim, cache, upd.Changes, upd.LightChanged)
			}
		}
	}

	if !ok && !blockIsPot {
		return nil, world.ItemStack{}, ItemMeta{}, DefaultDecoratedPotSherds(), false
	}

	sherds = DefaultDecoratedPotSherds()
	if dp != nil {
		sherds = normalizePotSherds(dp.Sherds)
		storedItem = dp.Item
		storedMeta = dp.Meta.Clone()
	}

	if shatter {
		for _, sh := range sherds {
			drops = append(drops, world.NewNamedItemStack(sh, 1))
		}
	} else {
		drops = append(drops, world.NewNamedItemStack("minecraft:decorated_pot", 1))
	}
	if !storedItem.IsEmpty() && storedItem.Count > 0 {
		drops = append(drops, storedItem)
	}

	if spawnDrops && s.DoTileDrops() {
		for _, d := range drops {
			if !d.IsEmpty() && d.Count > 0 {
				s.SpawnItemEntityInDimension(normDim, float64(x)+0.5, float64(y)+0.35, float64(z)+0.5, d)
			}
		}
	}

	cx, cz := int32(x>>4), int32(z>>4)
	soundName := "minecraft:block.decorated_pot.break"
	if shatter {
		soundName = "minecraft:block.decorated_pot.shatter"
	}
	s.BroadcastDimensionChunk(normDim, cx, cz, PlaySoundPacketID,
		EncodeSoundPacket(soundName, SoundSourceBlocks, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, 1.0, 1.0, 0))
	s.updateHopperComparatorsAndBroadcast(normDim, x, y, z)
	return drops, storedItem, storedMeta, sherds, true
}

// HitDecoratedPotWithProjectile handles any projectile (`arrow`, `spectral_arrow`,
// `trident`, `snowball`, `egg`, `ender_pearl`, `wind_charge`, `breeze_wind_charge`,
// `fireball`, `small_fireball`) striking a `minecraft:decorated_pot` at (dim, x, y, z):
// sets `cracked=true`, shatters the block, drops all 4 sherds/bricks and any
// stored item stack into the world, plays `minecraft:block.decorated_pot.shatter`,
// and emits a `block_destroy` vibration.
func (s *Server) HitDecoratedPotWithProjectile(dim string, x, y, z int, projectileEntityID int32) (shattered bool, drops []world.ItemStack) {
	if s == nil {
		return false, nil
	}
	normDim := normalizeHopperDim(dim)
	cache := s.DimensionChunks(normDim)
	if cache != nil && !IsDecoratedPotBlock(cache.GetBlock(x, y, z)) {
		return false, nil
	}
	drops, _, _, _, ok := s.BreakDecoratedPotAt(normDim, x, y, z, true, true)
	if !ok {
		return false, nil
	}
	s.EmitVibration(VibrationEvent{
		Dimension:      normDim,
		Type:           GameEventBlockDestroy,
		X:              float64(x) + 0.5,
		Y:              float64(y) + 0.5,
		Z:              float64(z) + 0.5,
		SourceEntityID: projectileEntityID,
	})
	return true, drops
}

// --- NBT Serialization & Persistence (`decorated_pots.dat`) ---

// ToNBT serializes the DecoratedPotBlockEntity into a vanilla-compatible NBT compound.
func (dp *DecoratedPotBlockEntity) ToNBT() *nbt.Compound {
	if dp == nil {
		return nbt.NewCompound()
	}
	comp := nbt.NewCompound()
	comp.Set("id", nbt.String("minecraft:decorated_pot"))
	comp.Set("Dimension", nbt.String(normalizeHopperDim(dp.Dimension)))
	comp.Set("x", nbt.Int(int32(dp.X)))
	comp.Set("y", nbt.Int(int32(dp.Y)))
	comp.Set("z", nbt.Int(int32(dp.Z)))

	sh := normalizePotSherds(dp.Sherds)
	sherdTags := []nbt.Tag{
		nbt.String(sh[0]),
		nbt.String(sh[1]),
		nbt.String(sh[2]),
		nbt.String(sh[3]),
	}
	comp.Set("sherds", nbt.List{ElemID: nbt.TagString, Elems: sherdTags})

	if !dp.Item.IsEmpty() && dp.Item.Count > 0 {
		itemComp := nbt.NewCompound()
		itemComp.Set("id", nbt.String(dp.Item.ResolvedID()))
		itemComp.Set("count", nbt.Int(dp.Item.Count))
		if !dp.Meta.IsEmpty() {
			metaComp := nbt.NewCompound()
			encodeItemMetaToNBT(metaComp, dp.Meta)
			itemComp.Set("Meta", metaComp)
		}
		comp.Set("item", itemComp)
	}
	return comp
}

// DecoratedPotBlockEntityFromNBT deserializes a DecoratedPotBlockEntity from an NBT compound.
func DecoratedPotBlockEntityFromNBT(comp *nbt.Compound) (*DecoratedPotBlockEntity, bool) {
	if comp == nil {
		return nil, false
	}
	if idStr, hasID := nbtGetString(comp, "id"); hasID && idStr != "" && canonicalResourceName(idStr) != "minecraft:decorated_pot" {
		return nil, false
	}
	dim, _ := nbtGetString(comp, "Dimension")
	x, okX := nbtGetInt(comp, "x")
	y, okY := nbtGetInt(comp, "y")
	z, okZ := nbtGetInt(comp, "z")
	if !okX || !okY || !okZ {
		return nil, false
	}
	dp := &DecoratedPotBlockEntity{
		Dimension: normalizeHopperDim(dim),
		X:         int(x),
		Y:         int(y),
		Z:         int(z),
		Sherds:    DefaultDecoratedPotSherds(),
	}
	shList, _ := nbtGetListElems(comp, "sherds")
	for i := 0; i < 4 && i < len(shList); i++ {
		if st, ok := shList[i].(nbt.String); ok && IsDecoratedPotIngredient(string(st)) {
			dp.Sherds[i] = canonicalResourceName(string(st))
		}
	}
	if rawItem, ok := comp.Get("item"); ok {
		if itemComp, ok := rawItem.(*nbt.Compound); ok && itemComp != nil {
			itemID, _ := nbtGetString(itemComp, "id")
			cnt, okCnt := nbtGetInt(itemComp, "count")
			if !okCnt || cnt <= 0 {
				cnt = 1
			}
			if itemID != "" {
				dp.Item = world.NewNamedItemStack(itemID, cnt)
				if rawMeta, ok := itemComp.Get("Meta"); ok {
					if metaComp, ok := rawMeta.(*nbt.Compound); ok && metaComp != nil {
						dp.Meta = decodeItemMetaFromNBT(metaComp)
					}
				}
			}
		}
	}
	return dp, true
}

func (s *Server) decoratedPotsFilePath() string {
	if s == nil {
		return ""
	}
	dir := strings.TrimSpace(s.cfg.WorldDir)
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "decorated_pots.dat")
}

// SaveDecoratedPots persists all DecoratedPotBlockEntities to `<WorldDir>/decorated_pots.dat`.
func (s *Server) SaveDecoratedPots() error {
	path := s.decoratedPotsFilePath()
	if path == "" {
		return nil
	}
	st := s.decoratedPotStore()
	st.mu.Lock()
	keys := make([]hopperBlockKey, 0, len(st.pots))
	for k := range st.pots {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].dim != keys[j].dim {
			return keys[i].dim < keys[j].dim
		}
		if keys[i].x != keys[j].x {
			return keys[i].x < keys[j].x
		}
		if keys[i].y != keys[j].y {
			return keys[i].y < keys[j].y
		}
		return keys[i].z < keys[j].z
	})
	list := make([]nbt.Tag, 0, len(keys))
	for _, k := range keys {
		if dp := st.pots[k]; dp != nil {
			list = append(list, dp.ToNBT())
		}
	}
	st.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	root := nbt.NewCompound()
	root.Set("DecoratedPots", nbt.List{ElemID: nbt.TagCompound, Elems: list})
	data := nbt.MarshalNamed("", root)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// LoadDecoratedPots loads persisted DecoratedPotBlockEntities from `<WorldDir>/decorated_pots.dat`.
func (s *Server) LoadDecoratedPots() error {
	path := s.decoratedPotsFilePath()
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	_, parsedTag, err := nbt.UnmarshalNamed(data)
	if err != nil {
		return err
	}
	root, ok := parsedTag.(*nbt.Compound)
	if !ok || root == nil {
		return nil
	}
	entries, _ := nbtGetListElems(root, "DecoratedPots")
	var loaded []*DecoratedPotBlockEntity
	st := s.decoratedPotStore()
	st.mu.Lock()
	for _, raw := range entries {
		comp, ok := raw.(*nbt.Compound)
		if !ok {
			continue
		}
		if dp, ok := DecoratedPotBlockEntityFromNBT(comp); ok && dp != nil {
			st.pots[hopperBlockKey{dim: dp.Dimension, x: dp.X, y: dp.Y, z: dp.Z}] = dp
			loaded = append(loaded, dp.Clone())
		}
	}
	st.mu.Unlock()

	for _, dp := range loaded {
		s.BindNoteBlockAndJukeboxRedstone(dp.Dimension)
		s.updateHopperComparatorsAndBroadcast(dp.Dimension, dp.X, dp.Y, dp.Z)
	}
	return nil
}

// ChunkDecoratedPotBlockEntitiesNBT returns vanilla `block_entities` NBT
// compounds for all decorated pots in chunk (cx, cz) of dim.
func (s *Server) ChunkDecoratedPotBlockEntitiesNBT(dim string, cx, cz int32) []*nbt.Compound {
	if s == nil {
		return nil
	}
	normDim := normalizeHopperDim(dim)
	st := s.decoratedPotStore()
	st.mu.Lock()
	defer st.mu.Unlock()
	var out []*nbt.Compound
	for k, dp := range st.pots {
		if k.dim != normDim || dp == nil {
			continue
		}
		if int32(k.x>>4) == cx && int32(k.z>>4) == cz {
			out = append(out, dp.ToNBT())
		}
	}
	return out
}

// UnloadChunkDecoratedPots removes all decorated pots in chunk (cx, cz) of dim
// from runtime memory and returns their serialized NBT compounds.
func (s *Server) UnloadChunkDecoratedPots(dim string, cx, cz int32) []*nbt.Compound {
	if s == nil {
		return nil
	}
	normDim := normalizeHopperDim(dim)
	st := s.decoratedPotStore()
	st.mu.Lock()
	defer st.mu.Unlock()
	var out []*nbt.Compound
	for k, dp := range st.pots {
		if k.dim != normDim {
			continue
		}
		if int32(k.x>>4) == cx && int32(k.z>>4) == cz {
			if dp != nil {
				out = append(out, dp.ToNBT())
			}
			delete(st.pots, k)
		}
	}
	return out
}

// LoadChunkDecoratedPotBlockEntitiesNBT loads decorated pot block entities
// from a chunk's `block_entities` NBT list and updates comparators.
func (s *Server) LoadChunkDecoratedPotBlockEntitiesNBT(dim string, list []*nbt.Compound) int {
	if s == nil || len(list) == 0 {
		return 0
	}
	normDim := normalizeHopperDim(dim)
	var loaded []*DecoratedPotBlockEntity
	st := s.decoratedPotStore()
	st.mu.Lock()
	for _, comp := range list {
		dp, ok := DecoratedPotBlockEntityFromNBT(comp)
		if !ok || dp == nil {
			continue
		}
		if dp.Dimension == "" {
			dp.Dimension = normDim
		}
		st.pots[hopperBlockKey{dim: dp.Dimension, x: dp.X, y: dp.Y, z: dp.Z}] = dp
		loaded = append(loaded, dp.Clone())
	}
	st.mu.Unlock()

	for _, dp := range loaded {
		s.BindNoteBlockAndJukeboxRedstone(dp.Dimension)
		s.updateHopperComparatorsAndBroadcast(dp.Dimension, dp.X, dp.Y, dp.Z)
	}
	return len(loaded)
}

// BroadcastDecoratedPotBlockEntityData broadcasts ClientboundBlockEntityDataPacket
// (`0x06`, `BlockEntityTypeDecoratedPot = 42`) for the decorated pot at (dim, x, y, z).
func (s *Server) BroadcastDecoratedPotBlockEntityData(dim string, x, y, z int) {
	if s == nil {
		return
	}
	normDim := normalizeHopperDim(dim)
	dp, ok := s.GetDecoratedPotAt(normDim, x, y, z)
	if !ok || dp == nil {
		return
	}
	pkt := EncodeBlockEntityDataPacket(x, y, z, BlockEntityTypeDecoratedPot, dp.ToNBT())
	s.BroadcastDimensionChunk(normDim, int32(x>>4), int32(z>>4), PlayBlockEntityDataCB, pkt)
}

// HydrateChunkDecoratedPots inspects all `world.StructureBlockEntity` entries on
// the loaded chunk (dim, cx, cz) and registers any `minecraft:decorated_pot` block entities.
func (s *Server) HydrateChunkDecoratedPots(dim string, cx, cz int32) int {
	if s == nil {
		return 0
	}
	normDim := normalizeHopperDim(dim)
	cache := s.DimensionChunks(normDim)
	if cache == nil {
		return 0
	}
	sbes := cache.LoadedChunkStructureBlockEntities(cx, cz)
	if len(sbes) == 0 {
		return 0
	}
	st := s.decoratedPotStore()
	st.mu.Lock()
	var hydrated [][3]int
	for _, sbe := range sbes {
		if canonicalResourceName(sbe.ID) != "minecraft:decorated_pot" {
			continue
		}
		key := hopperBlockKey{dim: normDim, x: sbe.X, y: sbe.Y, z: sbe.Z}
		if _, exists := st.pots[key]; exists {
			continue
		}
		_ = s.getOrCreateDecoratedPotLocked(st, normDim, sbe.X, sbe.Y, sbe.Z)
		hydrated = append(hydrated, [3]int{sbe.X, sbe.Y, sbe.Z})
	}
	st.mu.Unlock()

	for _, pos := range hydrated {
		s.BindNoteBlockAndJukeboxRedstone(normDim)
		s.updateHopperComparatorsAndBroadcast(normDim, pos[0], pos[1], pos[2])
	}
	return len(hydrated)
}


