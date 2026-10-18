package server

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"regionio/internal/nbt"
	"regionio/internal/world"
)

// Crafter constants for Minecraft 26.1.2 (protocol 775).
const (
	// CrafterMenuType is the BuiltInRegistries.MENU numeric ID for "minecraft:crafter_3x3".
	CrafterMenuType int32 = 7
	// CrafterMenuSlotCount is the total slot count in CrafterMenu (9 grid + 27 inv + 9 hotbar + 1 result).
	CrafterMenuSlotCount = 46
	// CrafterMenuResultSlot is the non-interactive result preview slot index in CrafterMenu.
	CrafterMenuResultSlot = 45

	// CrafterCraftingVisualTicks is the vanilla 6-tick duration that `crafting=true`
	// remains active on a crafter block after dispensing a crafted item.
	CrafterCraftingVisualTicks = 6

	// Vanilla LevelEvent IDs for Crafter (ClientboundLevelEventPacket 0x2e).
	LevelEventCrafterCraft          int32 = 1049
	LevelEventCrafterFail           int32 = 1050
	LevelEventCrafterShootParticles int32 = 2010
)

var crafterVisualTimers sync.Map // map[*Server]*crafterTimerMap

type crafterTimerMap struct {
	mu     sync.Mutex
	timers map[hopperBlockKey]int
}

func (s *Server) crafterTimers() *crafterTimerMap {
	if v, ok := crafterVisualTimers.Load(s); ok {
		return v.(*crafterTimerMap)
	}
	m := &crafterTimerMap{
		timers: make(map[hopperBlockKey]int),
	}
	actual, _ := crafterVisualTimers.LoadOrStore(s, m)
	return actual.(*crafterTimerMap)
}

// --- Crafter Block State & Orientation Helpers ---

// CrafterBlockState returns the block state ID for `minecraft:crafter` with the
// given orientation (e.g. "north_up", "down_north", "up_north"), triggered, and
// crafting flags.
func CrafterBlockState(orientation string, triggered, crafting bool) uint16 {
	orient := normalizeCrafterOrientation(orientation)
	trigStr := "false"
	if triggered {
		trigStr = "true"
	}
	craftStr := "false"
	if crafting {
		craftStr = "true"
	}
	if st, ok := world.BlockStateIDByProperties("minecraft:crafter", map[string]string{
		"orientation": orient,
		"triggered":   trigStr,
		"crafting":    craftStr,
	}); ok {
		return st
	}
	return 0
}

func normalizeCrafterOrientation(orientation string) string {
	o := strings.ToLower(strings.TrimSpace(orientation))
	switch o {
	case "down_east", "down_north", "down_south", "down_west",
		"up_east", "up_north", "up_south", "up_west",
		"west_up", "east_up", "north_up", "south_up":
		return o
	case "north":
		return "north_up"
	case "south":
		return "south_up"
	case "west":
		return "west_up"
	case "east":
		return "east_up"
	case "up":
		return "up_north"
	case "down":
		return "down_north"
	default:
		return "north_up"
	}
}

// CrafterOrientation returns the `orientation` property of a `minecraft:crafter`
// block state (defaulting to "north_up").
func CrafterOrientation(state uint16) string {
	props := world.BlockProperties(state)
	return normalizeCrafterOrientation(props["orientation"])
}

// CrafterFrontDirection returns the front ejection direction ("north", "south",
// "west", "east", "up", or "down") of a `minecraft:crafter` block state,
// corresponding to `FrontAndTop.front()` in vanilla.
func CrafterFrontDirection(state uint16) string {
	orient := CrafterOrientation(state)
	if idx := strings.IndexByte(orient, '_'); idx > 0 {
		return orient[:idx]
	}
	return "north"
}

// CrafterTopDirection returns the top alignment direction ("up", "north",
// "south", "west", or "east") of a `minecraft:crafter` block state,
// corresponding to `FrontAndTop.top()` in vanilla.
func CrafterTopDirection(state uint16) string {
	orient := CrafterOrientation(state)
	if idx := strings.IndexByte(orient, '_'); idx > 0 && idx+1 < len(orient) {
		return orient[idx+1:]
	}
	return "up"
}

// CrafterFrontOffset returns the 3D unit offset (dx, dy, dz) for the front
// output face of a `minecraft:crafter` block state.
func CrafterFrontOffset(state uint16) (dx, dy, dz int) {
	return DispenserFacingVector(CrafterFrontDirection(state))
}

// CrafterOrientationForPlacement computes the `orientation` property when a
// player places a `minecraft:crafter`, given the player's 3D look direction
// (`lookDir`: "north", "south", "west", "east", "up", "down") and horizontal
// facing (`horizFacing`: "north", "south", "west", "east").
// The crafter's front faces opposite to `lookDir`.
func CrafterOrientationForPlacement(lookDir, horizFacing string) string {
	front := oppositeDropperDirection(strings.ToLower(strings.TrimSpace(lookDir)))
	horiz := strings.ToLower(strings.TrimSpace(horizFacing))
	switch horiz {
	case "north", "south", "west", "east":
	default:
		horiz = "north"
	}
	switch front {
	case "down":
		return "down_" + oppositeDropperDirection(horiz)
	case "up":
		return "up_" + horiz
	case "north", "south", "west", "east":
		return front + "_up"
	default:
		return "north_up"
	}
}

// CrafterTriggered reports whether the `triggered` property of a
// `minecraft:crafter` block state is true.
func CrafterTriggered(state uint16) bool {
	return world.BlockProperties(state)["triggered"] == "true"
}

// CrafterCrafting reports whether the `crafting` property of a
// `minecraft:crafter` block state is true.
func CrafterCrafting(state uint16) bool {
	return world.BlockProperties(state)["crafting"] == "true"
}

// --- Crafter Slot Disabling, Masks & Removal ---

// IsCrafterSlotDisabled reports whether slot (0..8) in the `minecraft:crafter`
// at (dim, x, y, z) is currently disabled.
func (s *Server) IsCrafterSlotDisabled(dim string, x, y, z, slot int) bool {
	if s == nil || slot < 0 || slot >= CrafterSlotCount {
		return false
	}
	cr, ok := s.GetCrafterAt(dim, x, y, z)
	if !ok || cr == nil {
		return false
	}
	return cr.Disabled[slot]
}

// ToggleCrafterSlotDisabled toggles the disabled state of slot (0..8) in the
// `minecraft:crafter` at (dim, x, y, z). Following vanilla CrafterMenu, a slot
// that currently holds items cannot be disabled until emptied.
func (s *Server) ToggleCrafterSlotDisabled(dim string, x, y, z, slot int) (disabled bool, ok bool) {
	if s == nil || slot < 0 || slot >= CrafterSlotCount {
		return false, false
	}
	normDim := normalizeHopperDim(dim)
	s.BindNoteBlockAndJukeboxRedstone(normDim)
	hs := s.hopperState()
	hs.mu.Lock()
	cr := s.getOrCreateCrafterLocked(hs, normDim, x, y, z)
	if !cr.Disabled[slot] && !cr.Slots[slot].IsEmpty() && cr.Slots[slot].Count > 0 {
		hs.mu.Unlock()
		return false, false
	}
	cr.Disabled[slot] = !cr.Disabled[slot]
	nowDisabled := cr.Disabled[slot]
	hs.mu.Unlock()

	s.updateHopperComparatorsAndBroadcast(normDim, x, y, z)
	return nowDisabled, true
}

// CrafterDisabledSlotMask returns a 9-bit bitmask (bits 0..8) of disabled slots
// for the `minecraft:crafter` at (dim, x, y, z).
func (s *Server) CrafterDisabledSlotMask(dim string, x, y, z int) uint16 {
	cr, ok := s.GetCrafterAt(dim, x, y, z)
	if !ok || cr == nil {
		return 0
	}
	var mask uint16
	for i := 0; i < CrafterSlotCount; i++ {
		if cr.Disabled[i] {
			mask |= 1 << uint(i)
		}
	}
	return mask
}

// SetCrafterDisabledSlotMask sets the disabled state of all 9 slots (bits 0..8)
// for the `minecraft:crafter` at (dim, x, y, z) and updates comparators.
func (s *Server) SetCrafterDisabledSlotMask(dim string, x, y, z int, mask uint16) {
	if s == nil {
		return
	}
	normDim := normalizeHopperDim(dim)
	s.BindNoteBlockAndJukeboxRedstone(normDim)
	hs := s.hopperState()
	hs.mu.Lock()
	cr := s.getOrCreateCrafterLocked(hs, normDim, x, y, z)
	for i := 0; i < CrafterSlotCount; i++ {
		cr.Disabled[i] = (mask & (1 << uint(i))) != 0
	}
	hs.mu.Unlock()
	s.updateHopperComparatorsAndBroadcast(normDim, x, y, z)
}

// RemoveCrafterAt removes the `CrafterBlockEntity` at (dim, x, y, z) when the
// block is broken and returns all non-empty slot stacks so they can be dropped.
func (s *Server) RemoveCrafterAt(dim string, x, y, z int) (drops []world.ItemStack, metas []ItemMeta, existed bool) {
	if s == nil {
		return nil, nil, false
	}
	normDim := normalizeHopperDim(dim)
	key := hopperBlockKey{dim: normDim, x: x, y: y, z: z}
	hs := s.hopperState()
	hs.mu.Lock()
	cr, ok := hs.crafters[key]
	if ok {
		delete(hs.crafters, key)
	}
	hs.mu.Unlock()

	tm := s.crafterTimers()
	tm.mu.Lock()
	delete(tm.timers, key)
	tm.mu.Unlock()

	if !ok || cr == nil {
		return nil, nil, false
	}
	for i := 0; i < CrafterSlotCount; i++ {
		if !cr.Slots[i].IsEmpty() && cr.Slots[i].Count > 0 {
			drops = append(drops, cr.Slots[i])
			metas = append(metas, cr.SlotMeta[i].Clone())
		}
	}
	s.updateHopperComparatorsAndBroadcast(normDim, x, y, z)
	return drops, metas, true
}

// --- Crafting Remainder & 3x3 Recipe Evaluation ---

// CraftingRemainderForItem returns the container remainder item left behind
// when 1 unit of itemID is consumed in a crafting recipe (e.g. `minecraft:bucket`
// for `minecraft:milk_bucket` or `minecraft:water_bucket`, and
// `minecraft:glass_bottle` for `minecraft:honey_bottle` or `minecraft:dragon_breath`).
func CraftingRemainderForItem(itemID string) world.ItemStack {
	switch canonicalResourceName(itemID) {
	case "minecraft:milk_bucket",
		"minecraft:water_bucket",
		"minecraft:lava_bucket",
		"minecraft:powder_snow_bucket",
		"minecraft:cod_bucket",
		"minecraft:salmon_bucket",
		"minecraft:tropical_fish_bucket",
		"minecraft:pufferfish_bucket",
		"minecraft:axolotl_bucket",
		"minecraft:tadpole_bucket":
		return world.NewNamedItemStack("minecraft:bucket", 1)
	case "minecraft:honey_bottle",
		"minecraft:dragon_breath":
		return world.NewNamedItemStack("minecraft:glass_bottle", 1)
	default:
		return world.ItemStack{}
	}
}

// MatchCrafterGrid evaluates a 3x3 crafter grid against special recipes (such
// as `minecraft:decorated_pot` from 4 bricks/sherds in a diamond) and all
// shaped/shapeless recipes in DefaultRecipeBook().
func MatchCrafterGrid(slots [CrafterSlotCount]world.ItemStack) (result world.ItemStack, potSherds *[4]string, recipeName string, ok bool) {
	if potStack, sherds, matched := MatchDecoratedPotRecipe(slots[:]); matched {
		shCopy := sherds
		return potStack, &shCopy, "minecraft:decorated_pot", true
	}
	resStack, rec, matched := MatchCraftingGrid(slots[:], 3, 3)
	if !matched || resStack.IsEmpty() {
		return world.ItemStack{}, nil, "", false
	}
	name := ""
	if rec != nil {
		name = rec.Name
	}
	return resStack, nil, name, true
}

// PreviewCrafterResult returns the item stack that would be crafted by the
// current 3x3 grid of the `minecraft:crafter` at (dim, x, y, z), without
// consuming any ingredients (used for slot 45 in CrafterMenu).
func (s *Server) PreviewCrafterResult(dim string, x, y, z int) (world.ItemStack, bool) {
	cr, ok := s.GetCrafterAt(dim, x, y, z)
	if !ok || cr == nil {
		return world.ItemStack{}, false
	}
	res, _, _, matched := MatchCrafterGrid(cr.Slots)
	return res, matched
}

// --- Auto-Crafting Execution & Front Container/World Ejection ---

// DispenseFromCrafter executes one crafting cycle on the `minecraft:crafter` at
// (dim, x, y, z), invoked when its 4-tick scheduled redstone pulse fires:
//   - If no recipe matches the 3x3 grid, broadcasts LevelEvent 1050 (fail) and
//     returns (empty, false).
//   - If a recipe matches, consumes 1 item from each non-empty slot, sets
//     `crafting=true` on the block state, updates comparators, broadcasts
//     LevelEvent 1049 & 2010, and ejects the crafted result (plus any crafting
//     remainder items like empty buckets/bottles) into the container directly
//     in front of the crafter or as ItemEntities into the world.
func (s *Server) DispenseFromCrafter(dim string, x, y, z int) (world.ItemStack, bool) {
	if s == nil {
		return world.ItemStack{}, false
	}
	normDim := normalizeHopperDim(dim)
	cache := s.BindNoteBlockAndJukeboxRedstone(normDim)
	if cache == nil {
		return world.ItemStack{}, false
	}
	state := cache.GetBlock(x, y, z)
	if !IsCrafterBlock(state) {
		return world.ItemStack{}, false
	}

	hs := s.hopperState()
	hs.mu.Lock()
	cr := s.getOrCreateCrafterLocked(hs, normDim, x, y, z)
	result, potSherds, _, matched := MatchCrafterGrid(cr.Slots)
	if !matched || result.IsEmpty() {
		hs.mu.Unlock()
		s.BroadcastDimensionChunk(normDim, int32(x>>4), int32(z>>4), PlayLevelEventCB,
			EncodeLevelEventPacket(LevelEventCrafterFail, x, y, z, 0, false))
		return world.ItemStack{}, false
	}

	var remainders []world.ItemStack
	for i := 0; i < CrafterSlotCount; i++ {
		cur := cr.Slots[i]
		if cur.IsEmpty() || cur.Count <= 0 {
			continue
		}
		if rem := CraftingRemainderForItem(cur.ResolvedID()); !rem.IsEmpty() {
			remainders = append(remainders, rem)
		}
		if cur.Count <= 1 {
			cr.Slots[i] = world.ItemStack{}
			cr.SlotMeta[i] = ItemMeta{}
		} else {
			next := world.NewItemStack(cur.ResolvedItemID(), cur.Count-1)
			if next.IsEmpty() {
				next = world.NewNamedItemStack(cur.ResolvedID(), cur.Count-1)
			}
			cr.Slots[i] = next
		}
	}
	hs.mu.Unlock()

	// Set crafting=true on the crafter block state and start the 6-tick visual timer.
	if nextSt, ok := world.WithBlockProperty(state, "crafting", "true"); ok && nextSt != state {
		s.setDispenserWorldBlock(normDim, cache, x, y, z, nextSt)
		state = nextSt
	}
	tm := s.crafterTimers()
	tm.mu.Lock()
	tm.timers[hopperBlockKey{dim: normDim, x: x, y: y, z: z}] = CrafterCraftingVisualTicks
	tm.mu.Unlock()

	s.updateHopperComparatorsAndBroadcast(normDim, x, y, z)
	s.BroadcastDimensionChunk(normDim, int32(x>>4), int32(z>>4), PlayLevelEventCB,
		EncodeLevelEventPacket(LevelEventCrafterCraft, x, y, z, 0, false))
	s.BroadcastDimensionChunk(normDim, int32(x>>4), int32(z>>4), PlayLevelEventCB,
		EncodeLevelEventPacket(LevelEventCrafterShootParticles, x, y, z, 0, false))
	s.EmitGameEvent(normDim, GameEventBlockActivate, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, VibrationSource{})

	frontDir := CrafterFrontDirection(state)
	var resultMeta ItemMeta
	if potSherds != nil {
		// Record the crafted sherds so if the pot item is inspected or placed it carries them.
		s.SetLastCraftedDecoratedPotSherds(*potSherds)
	}
	s.dispenseCrafterStackOutFront(normDim, x, y, z, frontDir, result, resultMeta)
	for _, rem := range remainders {
		s.dispenseCrafterStackOutFront(normDim, x, y, z, frontDir, rem, ItemMeta{})
	}
	return result, true
}

func (s *Server) dispenseCrafterStackOutFront(dim string, x, y, z int, frontDir string, stack world.ItemStack, meta ItemMeta) {
	if stack.IsEmpty() || stack.Count <= 0 {
		return
	}
	dx, dy, dz := DispenserFacingVector(frontDir)
	tx, ty, tz := x+dx, y+dy, z+dz

	remaining := stack.Count
	cache := s.DimensionChunks(dim)
	hasFrontContainer := false
	if cache != nil && ty >= world.MinY && ty < world.MinY+world.WorldHeight {
		targetState := cache.GetBlock(tx, ty, tz)
		if IsHopperContainerBlock(targetState) || IsDecoratedPotBlock(targetState) {
			hasFrontContainer = true
		}
	}
	if !hasFrontContainer {
		if len(s.FindVehiclesInBox(dim, float64(tx), float64(ty), float64(tz), float64(tx+1), float64(ty+1), float64(tz+1))) > 0 {
			hasFrontContainer = true
		}
	}

	if hasFrontContainer {
		single := world.NewItemStack(stack.ResolvedItemID(), 1)
		if single.IsEmpty() {
			single = world.NewNamedItemStack(stack.ResolvedID(), 1)
		}
		for remaining > 0 {
			if !s.TryHopperInsertIntoBlock(dim, tx, ty, tz, frontDir, single, meta, "") {
				break
			}
			remaining--
		}
	}

	if remaining > 0 {
		ejectStack := world.NewItemStack(stack.ResolvedItemID(), remaining)
		if ejectStack.IsEmpty() {
			ejectStack = world.NewNamedItemStack(stack.ResolvedID(), remaining)
		}
		px := float64(x) + 0.5 + 0.7*float64(dx)
		py := float64(y) + 0.5 + 0.7*float64(dy) - 0.15
		pz := float64(z) + 0.5 + 0.7*float64(dz)
		if dy == 0 {
			py = float64(y) + 0.35
		}
		entID, ent := s.SpawnItemEntityInDimension(dim, px, py, pz, ejectStack)
		if ent != nil && entID != 0 && s.entities != nil {
			vx := int16(dx * 640)
			vy := int16(dy*640 + 320)
			vz := int16(dz * 640)
			s.entities.Update(entID, func(e *world.Entity) {
				e.VelocityX = vx
				e.VelocityY = vy
				e.VelocityZ = vz
			})
		}
	}
}

// TickCrafters advances the 6-tick `crafting=true` visual cooldown on active
// crafters and resets `crafting=false` once elapsed.
func (s *Server) TickCrafters() {
	if s == nil {
		return
	}
	tm := s.crafterTimers()
	tm.mu.Lock()
	if len(tm.timers) == 0 {
		tm.mu.Unlock()
		return
	}
	var expired []hopperBlockKey
	for k, rem := range tm.timers {
		rem--
		if rem <= 0 {
			expired = append(expired, k)
			delete(tm.timers, k)
		} else {
			tm.timers[k] = rem
		}
	}
	tm.mu.Unlock()

	for _, key := range expired {
		cache := s.DimensionChunks(key.dim)
		if cache == nil {
			continue
		}
		st := cache.GetBlock(key.x, key.y, key.z)
		if IsCrafterBlock(st) && CrafterCrafting(st) {
			if nextSt, ok := world.WithBlockProperty(st, "crafting", "false"); ok && nextSt != st {
				s.setDispenserWorldBlock(key.dim, cache, key.x, key.y, key.z, nextSt)
			}
		}
	}
}

// --- Crafter Container-UI & NBT Persistence Helpers ---

// BuildCrafterMenuSlots constructs the 46-slot CrafterMenu slice for a player:
//   - Slots 0..8:   3x3 crafter grid
//   - Slots 9..35:  player main inventory (inv[9:36])
//   - Slots 36..44: player hotbar (inv[36:45])
//   - Slot 45:      non-interactive crafting result preview
func (s *Server) BuildCrafterMenuSlots(session *PlayerSession, dim string, x, y, z int) []world.ItemStack {
	slots := make([]world.ItemStack, CrafterMenuSlotCount)
	if cr, ok := s.GetCrafterAt(dim, x, y, z); ok && cr != nil {
		copy(slots[0:CrafterSlotCount], cr.Slots[:])
		if preview, _, _, matched := MatchCrafterGrid(cr.Slots); matched {
			slots[CrafterMenuResultSlot] = preview
		}
	}
	if session != nil {
		inv := session.Inventory()
		copy(slots[9:36], inv[9:36])
		copy(slots[36:45], inv[36:45])
	}
	return slots
}

// CrafterContainerData returns the 10 ContainerData properties for CrafterMenu:
//   - Indices 0..8: 1 if slot i is disabled, 0 if enabled
//   - Index 9:      1 if the crafter block is currently `triggered=true`, 0 otherwise
func (s *Server) CrafterContainerData(dim string, x, y, z int) [10]int16 {
	var data [10]int16
	if s == nil {
		return data
	}
	normDim := normalizeHopperDim(dim)
	if cr, ok := s.GetCrafterAt(normDim, x, y, z); ok && cr != nil {
		for i := 0; i < CrafterSlotCount; i++ {
			if cr.Disabled[i] {
				data[i] = 1
			}
		}
	}
	if cache := s.DimensionChunks(normDim); cache != nil {
		st := cache.GetBlock(x, y, z)
		if IsCrafterBlock(st) && CrafterTriggered(st) {
			data[9] = 1
		}
	}
	return data
}

// encodeItemMetaToNBT writes an ItemMeta into an NBT compound.
func encodeItemMetaToNBT(c *nbt.Compound, m ItemMeta) {
	if c == nil || m.IsEmpty() {
		return
	}
	if m.Damage > 0 {
		c.Set("Damage", nbt.Int(m.Damage))
	}
	if m.CustomName != "" {
		c.Set("CustomName", nbt.String(m.CustomName))
	}
	if m.RepairCost > 0 {
		c.Set("RepairCost", nbt.Int(m.RepairCost))
	}
	if len(m.Enchantments) > 0 {
		keys := make([]string, 0, len(m.Enchantments))
		for id, lvl := range m.Enchantments {
			if lvl > 0 {
				keys = append(keys, id)
			}
		}
		sort.Strings(keys)
		elems := make([]nbt.Tag, 0, len(keys))
		for _, id := range keys {
			ec := nbt.NewCompound()
			ec.Set("id", nbt.String(id))
			ec.Set("lvl", nbt.Int(int32(m.Enchantments[id])))
			elems = append(elems, ec)
		}
		c.Set("Enchantments", nbt.List{ElemID: nbt.TagCompound, Elems: elems})
	}
}

// decodeItemMetaFromNBT reads an ItemMeta from an NBT compound.
func decodeItemMetaFromNBT(c *nbt.Compound) ItemMeta {
	if c == nil {
		return ItemMeta{}
	}
	dmg, _ := nbtGetInt(c, "Damage")
	name, _ := nbtGetString(c, "CustomName")
	rc, _ := nbtGetInt(c, "RepairCost")
	m := ItemMeta{
		Damage:     dmg,
		CustomName: name,
		RepairCost: rc,
	}
	elems, _ := nbtGetListElems(c, "Enchantments")
	for _, raw := range elems {
		ec, ok := raw.(*nbt.Compound)
		if !ok || ec == nil {
			continue
		}
		id, _ := nbtGetString(ec, "id")
		lvl32, _ := nbtGetInt(ec, "lvl")
		lvl := int(lvl32)
		if id != "" && lvl > 0 {
			if m.Enchantments == nil {
				m.Enchantments = make(map[string]int)
			}
			m.Enchantments[id] = lvl
		}
	}
	return m
}

// itemMetaEqual reports whether two ItemMeta values have identical damage,
// custom name, repair cost, and enchantments.
func itemMetaEqual(a, b ItemMeta) bool {
	if a.Damage != b.Damage || a.CustomName != b.CustomName || a.RepairCost != b.RepairCost {
		return false
	}
	if len(a.Enchantments) != len(b.Enchantments) {
		return false
	}
	for id, lvl := range a.Enchantments {
		if b.Enchantments[id] != lvl {
			return false
		}
	}
	return true
}

// ToNBT serializes the CrafterBlockEntity into a vanilla-compatible NBT compound.
func (cr *CrafterBlockEntity) ToNBT() *nbt.Compound {
	if cr == nil {
		return nbt.NewCompound()
	}
	comp := nbt.NewCompound()
	comp.Set("id", nbt.String("minecraft:crafter"))
	comp.Set("Dimension", nbt.String(normalizeHopperDim(cr.Dimension)))
	comp.Set("x", nbt.Int(int32(cr.X)))
	comp.Set("y", nbt.Int(int32(cr.Y)))
	comp.Set("z", nbt.Int(int32(cr.Z)))
	comp.Set("crafting_ticks_remaining", nbt.Int(int32(cr.CraftingTicksRemaining)))

	var disabledIndices []int32
	var itemEntries []nbt.Tag
	for i := 0; i < CrafterSlotCount; i++ {
		if cr.Disabled[i] {
			disabledIndices = append(disabledIndices, int32(i))
		}
		st := cr.Slots[i]
		if st.IsEmpty() || st.Count <= 0 {
			continue
		}
		entry := nbt.NewCompound()
		entry.Set("Slot", nbt.Byte(int8(i)))
		entry.Set("id", nbt.String(st.ResolvedID()))
		entry.Set("count", nbt.Int(st.Count))
		if !cr.SlotMeta[i].IsEmpty() {
			metaComp := nbt.NewCompound()
			encodeItemMetaToNBT(metaComp, cr.SlotMeta[i])
			entry.Set("Meta", metaComp)
		}
		itemEntries = append(itemEntries, entry)
	}
	comp.Set("disabled_slots", nbt.IntArray(disabledIndices))
	comp.Set("Items", nbt.List{ElemID: nbt.TagCompound, Elems: itemEntries})
	return comp
}

// CrafterBlockEntityFromNBT deserializes a CrafterBlockEntity from an NBT compound.
func CrafterBlockEntityFromNBT(comp *nbt.Compound) (*CrafterBlockEntity, bool) {
	if comp == nil {
		return nil, false
	}
	if idStr, hasID := nbtGetString(comp, "id"); hasID && idStr != "" && canonicalResourceName(idStr) != "minecraft:crafter" {
		return nil, false
	}
	dim, _ := nbtGetString(comp, "Dimension")
	x, okX := nbtGetInt(comp, "x")
	y, okY := nbtGetInt(comp, "y")
	z, okZ := nbtGetInt(comp, "z")
	if !okX || !okY || !okZ {
		return nil, false
	}
	craftingTicks, _ := nbtGetInt(comp, "crafting_ticks_remaining")
	if craftingTicks < 0 {
		craftingTicks = 0
	}
	cr := &CrafterBlockEntity{
		Dimension:              normalizeHopperDim(dim),
		X:                      int(x),
		Y:                      int(y),
		Z:                      int(z),
		CraftingTicksRemaining: int(craftingTicks),
	}
	if rawDis, ok := comp.Get("disabled_slots"); ok {
		if dis, ok := rawDis.(nbt.IntArray); ok {
			for _, idx := range dis {
				if idx >= 0 && int(idx) < CrafterSlotCount {
					cr.Disabled[idx] = true
				}
			}
		}
	}
	items, _ := nbtGetListElems(comp, "Items")
	for _, raw := range items {
		entry, ok := raw.(*nbt.Compound)
		if !ok || entry == nil {
			continue
		}
		slotB, okSlot := nbtGetByte(entry, "Slot")
		if !okSlot || slotB < 0 || int(slotB) >= CrafterSlotCount {
			continue
		}
		slot := int(slotB)
		itemID, _ := nbtGetString(entry, "id")
		count, _ := nbtGetInt(entry, "count")
		if count <= 0 {
			if cB, okB := nbtGetByte(entry, "Count"); okB && cB > 0 {
				count = int32(cB)
			}
		}
		if itemID == "" || count <= 0 {
			continue
		}
		cr.Slots[slot] = world.NewNamedItemStack(itemID, count)
		if rawMeta, ok := entry.Get("Meta"); ok {
			if metaComp, ok := rawMeta.(*nbt.Compound); ok && metaComp != nil {
				cr.SlotMeta[slot] = decodeItemMetaFromNBT(metaComp)
			}
		}
	}
	return cr, true
}

func (s *Server) craftersFilePath() string {
	if s == nil {
		return ""
	}
	dir := strings.TrimSpace(s.cfg.WorldDir)
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "crafters.dat")
}

// SaveCrafters persists all CrafterBlockEntities to `<WorldDir>/crafters.dat`.
func (s *Server) SaveCrafters() error {
	path := s.craftersFilePath()
	if path == "" {
		return nil
	}
	tm := s.crafterTimers()
	tm.mu.Lock()
	timerSnap := make(map[hopperBlockKey]int, len(tm.timers))
	for k, v := range tm.timers {
		timerSnap[k] = v
	}
	tm.mu.Unlock()

	hs := s.hopperState()
	hs.mu.Lock()
	keys := make([]hopperBlockKey, 0, len(hs.crafters))
	for k := range hs.crafters {
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
		if cr := hs.crafters[k]; cr != nil {
			cp := cr.Clone()
			if rem, ok := timerSnap[k]; ok && rem > 0 {
				cp.CraftingTicksRemaining = rem
			}
			list = append(list, cp.ToNBT())
		}
	}
	hs.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	root := nbt.NewCompound()
	root.Set("Crafters", nbt.List{ElemID: nbt.TagCompound, Elems: list})
	data := nbt.MarshalNamed("", root)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// LoadCrafters loads persisted CrafterBlockEntities from `<WorldDir>/crafters.dat`.
func (s *Server) LoadCrafters() error {
	path := s.craftersFilePath()
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
	entries, _ := nbtGetListElems(root, "Crafters")
	var loaded []*CrafterBlockEntity
	hs := s.hopperState()
	hs.mu.Lock()
	for _, raw := range entries {
		comp, ok := raw.(*nbt.Compound)
		if !ok {
			continue
		}
		if cr, ok := CrafterBlockEntityFromNBT(comp); ok && cr != nil {
			hs.crafters[hopperBlockKey{dim: cr.Dimension, x: cr.X, y: cr.Y, z: cr.Z}] = cr
			loaded = append(loaded, cr.Clone())
		}
	}
	hs.mu.Unlock()

	tm := s.crafterTimers()
	for _, cr := range loaded {
		s.BindNoteBlockAndJukeboxRedstone(cr.Dimension)
		key := hopperBlockKey{dim: cr.Dimension, x: cr.X, y: cr.Y, z: cr.Z}
		rem := cr.CraftingTicksRemaining
		if rem <= 0 {
			if cache := s.DimensionChunks(cr.Dimension); cache != nil {
				if CrafterCrafting(cache.GetBlock(cr.X, cr.Y, cr.Z)) {
					rem = CrafterCraftingVisualTicks
				}
			}
		}
		if rem > 0 {
			tm.mu.Lock()
			tm.timers[key] = rem
			tm.mu.Unlock()
		}
		s.updateHopperComparatorsAndBroadcast(cr.Dimension, cr.X, cr.Y, cr.Z)
	}
	return nil
}

// ChunkCrafterBlockEntitiesNBT returns vanilla `block_entities` NBT compounds
// for all crafters in chunk (cx, cz) of dim.
func (s *Server) ChunkCrafterBlockEntitiesNBT(dim string, cx, cz int32) []*nbt.Compound {
	if s == nil {
		return nil
	}
	normDim := normalizeHopperDim(dim)
	tm := s.crafterTimers()
	tm.mu.Lock()
	timerSnap := make(map[hopperBlockKey]int, len(tm.timers))
	for k, v := range tm.timers {
		timerSnap[k] = v
	}
	tm.mu.Unlock()

	hs := s.hopperState()
	hs.mu.Lock()
	defer hs.mu.Unlock()
	var out []*nbt.Compound
	for k, cr := range hs.crafters {
		if k.dim != normDim || cr == nil {
			continue
		}
		if int32(k.x>>4) == cx && int32(k.z>>4) == cz {
			cp := cr.Clone()
			if rem, ok := timerSnap[k]; ok && rem > 0 {
				cp.CraftingTicksRemaining = rem
			}
			out = append(out, cp.ToNBT())
		}
	}
	return out
}

// UnloadChunkCrafters removes all crafters in chunk (cx, cz) of dim from runtime
// memory and returns their serialized NBT compounds.
func (s *Server) UnloadChunkCrafters(dim string, cx, cz int32) []*nbt.Compound {
	if s == nil {
		return nil
	}
	normDim := normalizeHopperDim(dim)
	tm := s.crafterTimers()
	tm.mu.Lock()
	timerSnap := make(map[hopperBlockKey]int)
	for k, v := range tm.timers {
		if k.dim == normDim && int32(k.x>>4) == cx && int32(k.z>>4) == cz {
			timerSnap[k] = v
			delete(tm.timers, k)
		}
	}
	tm.mu.Unlock()

	hs := s.hopperState()
	hs.mu.Lock()
	defer hs.mu.Unlock()
	var out []*nbt.Compound
	for k, cr := range hs.crafters {
		if k.dim != normDim {
			continue
		}
		if int32(k.x>>4) == cx && int32(k.z>>4) == cz {
			if cr != nil {
				cp := cr.Clone()
				if rem, ok := timerSnap[k]; ok && rem > 0 {
					cp.CraftingTicksRemaining = rem
				}
				out = append(out, cp.ToNBT())
			}
			delete(hs.crafters, k)
		}
	}
	return out
}

// LoadChunkCrafterBlockEntitiesNBT loads crafter block entities from a chunk's
// `block_entities` NBT list and restores any active `crafting_ticks_remaining` timers.
func (s *Server) LoadChunkCrafterBlockEntitiesNBT(dim string, list []*nbt.Compound) int {
	if s == nil || len(list) == 0 {
		return 0
	}
	normDim := normalizeHopperDim(dim)
	var loaded []*CrafterBlockEntity
	hs := s.hopperState()
	hs.mu.Lock()
	for _, comp := range list {
		cr, ok := CrafterBlockEntityFromNBT(comp)
		if !ok || cr == nil {
			continue
		}
		if cr.Dimension == "" {
			cr.Dimension = normDim
		}
		hs.crafters[hopperBlockKey{dim: cr.Dimension, x: cr.X, y: cr.Y, z: cr.Z}] = cr
		loaded = append(loaded, cr.Clone())
	}
	hs.mu.Unlock()

	tm := s.crafterTimers()
	for _, cr := range loaded {
		s.BindNoteBlockAndJukeboxRedstone(cr.Dimension)
		key := hopperBlockKey{dim: cr.Dimension, x: cr.X, y: cr.Y, z: cr.Z}
		rem := cr.CraftingTicksRemaining
		if rem <= 0 {
			if cache := s.DimensionChunks(cr.Dimension); cache != nil {
				if CrafterCrafting(cache.GetBlock(cr.X, cr.Y, cr.Z)) {
					rem = CrafterCraftingVisualTicks
				}
			}
		}
		if rem > 0 {
			tm.mu.Lock()
			tm.timers[key] = rem
			tm.mu.Unlock()
		}
		s.updateHopperComparatorsAndBroadcast(cr.Dimension, cr.X, cr.Y, cr.Z)
	}
	return len(loaded)
}

