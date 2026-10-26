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

// Vanilla 26.1.2 sound events and level events for suspicious sand / suspicious gravel brushing.
const (
	SoundBrushSand           = "minecraft:item.brush.brushing.sand"
	SoundBrushGravel         = "minecraft:item.brush.brushing.gravel"
	SoundBrushSandComplete   = "minecraft:item.brush.brushing.sand.complete"
	SoundBrushGravelComplete = "minecraft:item.brush.brushing.gravel.complete"
	SoundBrushGeneric        = "minecraft:item.brush.brushing.generic"
	SoundSuspiciousSandBreak = "minecraft:block.suspicious_sand.break"
	SoundSuspiciousGravBreak = "minecraft:block.suspicious_gravel.break"

	LevelEventParticlesDestroyBlock int32 = 2001
)

// BrushableBlockEntity represents the server-side state of a
// `minecraft:suspicious_sand` or `minecraft:suspicious_gravel` block entity
// (`minecraft:brushable_block`) at (Dimension, X, Y, Z).
type BrushableBlockEntity struct {
	Dimension     string
	X, Y, Z       int
	BlockName     string
	Item          world.ItemStack
	Meta          ItemMeta
	LootTable     string
	LootTableSeed int64
	HitDirection  string
	BrushCount    int
	Dusted        int
	IdleTicks     int
}

// Clone returns a deep copy of the BrushableBlockEntity.
func (be *BrushableBlockEntity) Clone() *BrushableBlockEntity {
	if be == nil {
		return nil
	}
	cp := *be
	cp.Meta = be.Meta.Clone()
	return &cp
}

type serverBrushableStore struct {
	mu     sync.Mutex
	blocks map[hopperBlockKey]*BrushableBlockEntity
}

var serverBrushables sync.Map // map[*Server]*serverBrushableStore

func (s *Server) brushableStore() *serverBrushableStore {
	if v, ok := serverBrushables.Load(s); ok {
		return v.(*serverBrushableStore)
	}
	st := &serverBrushableStore{
		blocks: make(map[hopperBlockKey]*BrushableBlockEntity),
	}
	actual, _ := serverBrushables.LoadOrStore(s, st)
	return actual.(*serverBrushableStore)
}

var archaeologyAdvancementsOnce sync.Once

func ensureArchaeologyAdvancementsRegistered() {
	ensureItemUseAdvancementsRegistered()
	archaeologyAdvancementsOnce.Do(func() {
		reg := DefaultAdvancementRegistry()
		if reg == nil {
			return
		}
		register := func(id, parent, title, desc, icon string, frame int32, crit string) {
			if _, exists := reg.byID[id]; exists {
				return
			}
			def := &AdvancementDef{
				ID:       id,
				ParentID: parent,
				Display: &AdvancementDisplay{
					Title:          title,
					Description:    desc,
					IconItem:       icon,
					Frame:          frame,
					ShowToast:      true,
					AnnounceToChat: true,
				},
				Criteria:            []string{crit},
				Requirements:        [][]string{{crit}},
				SendsTelemetryEvent: true,
			}
			reg.defs = append(reg.defs, def)
			reg.byID[id] = def
			if parent != "" {
				reg.children[parent] = append(reg.children[parent], def)
			}
		}
		register("minecraft:adventure/salvage_sherd", "minecraft:adventure/root",
			"Respecting the Remnants", "Brush a Suspicious block to obtain a Pottery Sherd", "minecraft:brush", AdvancementFrameTask, "has_sherd")
		register("minecraft:adventure/craft_decorated_pot_using_only_sherds", "minecraft:adventure/salvage_sherd",
			"Careful Restoration", "Make a Decorated Pot out of 4 Pottery Sherds", "minecraft:decorated_pot", AdvancementFrameTask, "pot_crafted_using_only_sherds")
		register("minecraft:husbandry/obtain_sniffer_egg", "minecraft:husbandry/root",
			"Smells Interesting", "Obtain a Sniffer Egg", "minecraft:sniffer_egg", AdvancementFrameTask, "obtain_sniffer_egg")
	})
}

func normalizeBrushDirection(dir string) string {
	switch strings.ToLower(strings.TrimSpace(dir)) {
	case "down", "up", "north", "south", "west", "east":
		return strings.ToLower(strings.TrimSpace(dir))
	default:
		return "up"
	}
}

func (s *Server) getOrCreateBrushableLocked(st *serverBrushableStore, dim string, x, y, z int, cache *world.Cache) *BrushableBlockEntity {
	normDim := normalizeHopperDim(dim)
	key := hopperBlockKey{dim: normDim, x: x, y: y, z: z}
	if existing := st.blocks[key]; existing != nil {
		return existing
	}
	blockName := "minecraft:suspicious_sand"
	dusted := 0
	if cache != nil {
		curState := cache.GetBlock(x, y, z)
		if world.IsBrushableBlock(curState) {
			blockName = world.BlockName(curState)
			dusted, _ = world.BrushableBlockDustedStage(curState)
		}
	}
	be := &BrushableBlockEntity{
		Dimension: normDim,
		X:         x,
		Y:         y,
		Z:         z,
		BlockName: blockName,
		Dusted:    dusted,
	}
	// Hydrate from worldgen Cache.StructureBlockEntityAt if present.
	if cache != nil {
		if sbe, ok := cache.StructureBlockEntityAt(x, y, z); ok &&
			(sbe.ID == "minecraft:brushable_block" || sbe.ID == "minecraft:suspicious_sand" || sbe.ID == "minecraft:suspicious_gravel") {
			be.LootTable = sbe.LootTable
			be.LootTableSeed = sbe.LootTableSeed
			if len(sbe.Items) > 0 && sbe.Items[0].ItemID != "" {
				cnt := int32(sbe.Items[0].Count)
				if cnt <= 0 {
					cnt = 1
				}
				be.Item = world.NewNamedItemStack(sbe.Items[0].ItemID, cnt)
				be.LootTable = ""
			}
		}
	}
	st.blocks[key] = be
	return be
}

// PlaceBrushableBlockAt places a `minecraft:suspicious_sand` or
// `minecraft:suspicious_gravel` block at (dim, x, y, z) and initializes its
// `BrushableBlockEntity` with the given archaeology `lootTable` and `lootSeed`.
func (s *Server) PlaceBrushableBlockAt(
	dim string,
	x, y, z int,
	blockName string,
	lootTable string,
	lootSeed int64,
) (*BrushableBlockEntity, world.BlockUpdateResult) {
	return s.PlaceBrushableBlockWithItemAt(dim, x, y, z, blockName, world.ItemStack{}, ItemMeta{}, lootTable, lootSeed)
}

// PlaceBrushableBlockWithItemAt places a `minecraft:suspicious_sand` or
// `minecraft:suspicious_gravel` block at (dim, x, y, z) and initializes its
// `BrushableBlockEntity` with the given buried `item` (or `lootTable` + `lootSeed`).
func (s *Server) PlaceBrushableBlockWithItemAt(
	dim string,
	x, y, z int,
	blockName string,
	item world.ItemStack,
	meta ItemMeta,
	lootTable string,
	lootSeed int64,
) (*BrushableBlockEntity, world.BlockUpdateResult) {
	var res world.BlockUpdateResult
	if s == nil {
		return nil, res
	}
	normDim := normalizeHopperDim(dim)
	normBlock := canonicalResourceName(blockName)
	if normBlock != "minecraft:suspicious_gravel" {
		normBlock = "minecraft:suspicious_sand"
	}
	cache := s.DimensionChunks(normDim)
	state := world.BrushableBlockState(normBlock, 0)
	if cache != nil && state != world.StateAir {
		_, res = cache.ApplyBlockWithUpdates(x, y, z, state)
		s.broadcastDimensionBlockChanges(normDim, cache, res.Changes, res.LightChanged)
	}
	// If the block immediately fell and shattered on placement, do not register a block entity.
	if cache != nil && !world.IsBrushableBlock(cache.GetBlock(x, y, z)) {
		return nil, res
	}

	st := s.brushableStore()
	st.mu.Lock()
	key := hopperBlockKey{dim: normDim, x: x, y: y, z: z}
	be := &BrushableBlockEntity{
		Dimension:     normDim,
		X:             x,
		Y:             y,
		Z:             z,
		BlockName:     normBlock,
		Item:          item,
		Meta:          meta.Clone(),
		LootTable:     strings.TrimSpace(lootTable),
		LootTableSeed: lootSeed,
	}
	st.blocks[key] = be
	cp := be.Clone()
	st.mu.Unlock()
	return cp, res
}

// GetBrushableBlockAt returns a snapshot of the `BrushableBlockEntity` at
// (dim, x, y, z), hydrating from worldgen structure data if needed.
func (s *Server) GetBrushableBlockAt(dim string, x, y, z int) (*BrushableBlockEntity, bool) {
	if s == nil {
		return nil, false
	}
	normDim := normalizeHopperDim(dim)
	cache := s.DimensionChunks(normDim)
	st := s.brushableStore()
	st.mu.Lock()
	key := hopperBlockKey{dim: normDim, x: x, y: y, z: z}
	be, ok := st.blocks[key]
	if !ok && cache != nil && world.IsBrushableBlock(cache.GetBlock(x, y, z)) {
		be = s.getOrCreateBrushableLocked(st, normDim, x, y, z, cache)
		ok = be != nil
	}
	var cp *BrushableBlockEntity
	if ok && be != nil {
		cp = be.Clone()
	}
	st.mu.Unlock()
	return cp, cp != nil
}

// SetBrushableBlockItem sets the buried item stack and metadata inside the
// brushable block at (dim, x, y, z), clearing any pending LootTable.
func (s *Server) SetBrushableBlockItem(dim string, x, y, z int, item world.ItemStack, meta ItemMeta) bool {
	if s == nil {
		return false
	}
	normDim := normalizeHopperDim(dim)
	cache := s.DimensionChunks(normDim)
	st := s.brushableStore()
	st.mu.Lock()
	be := s.getOrCreateBrushableLocked(st, normDim, x, y, z, cache)
	be.Item = item
	be.Meta = meta.Clone()
	be.LootTable = ""
	be.LootTableSeed = 0
	st.mu.Unlock()
	return true
}

// SetBrushableBlockLootTable sets the archaeology loot table and seed on the
// brushable block at (dim, x, y, z).
func (s *Server) SetBrushableBlockLootTable(dim string, x, y, z int, lootTable string, lootSeed int64) bool {
	if s == nil {
		return false
	}
	normDim := normalizeHopperDim(dim)
	cache := s.DimensionChunks(normDim)
	st := s.brushableStore()
	st.mu.Lock()
	be := s.getOrCreateBrushableLocked(st, normDim, x, y, z, cache)
	be.LootTable = canonicalResourceName(lootTable)
	be.LootTableSeed = lootSeed
	be.Item = world.ItemStack{}
	be.Meta = ItemMeta{}
	st.mu.Unlock()
	return true
}

func unpackBrushableLootLocked(be *BrushableBlockEntity) {
	if be == nil || be.LootTable == "" {
		return
	}
	seed := be.LootTableSeed
	if seed == 0 {
		seed = int64(be.X*3129871) ^ int64(be.Z*116129781) ^ int64(be.Y)
	}
	if be.Item.IsEmpty() {
		be.Item = world.EvaluateArchaeologyLoot(be.LootTable, seed)
	}
	be.LootTable = ""
	be.LootTableSeed = 0
}

// UnpackBrushableBlockLoot resolves the archaeology `LootTable` on the brushable
// block at (dim, x, y, z) if not yet unpacked and returns the buried item.
func (s *Server) UnpackBrushableBlockLoot(dim string, x, y, z int) (world.ItemStack, bool) {
	if s == nil {
		return world.ItemStack{}, false
	}
	normDim := normalizeHopperDim(dim)
	cache := s.DimensionChunks(normDim)
	st := s.brushableStore()
	st.mu.Lock()
	be := s.getOrCreateBrushableLocked(st, normDim, x, y, z, cache)
	unpackBrushableLootLocked(be)
	item := be.Item
	st.mu.Unlock()
	return item, !item.IsEmpty()
}

// BrushBlockAt advances brushing on a `minecraft:suspicious_sand` or
// `minecraft:suspicious_gravel` block at (dim, x, y, z) from face `hitDirection`
// by 1 stage (`0 -> 1 -> 2 -> 3 -> completed`):
//   - Unpacks any archaeology `LootTable` on first brush.
//   - Broadcasts block dust particles (`LevelEventParticlesDestroyBlock`) and
//     brushing sound (`item.brush.brushing.sand` / `item.brush.brushing.gravel`).
//   - On the 4th brush step (`completed == true`), transforms the block into
//     `minecraft:sand` or `minecraft:gravel`, pops out the buried item toward
//     `hitDirection`, damages the player's held `minecraft:brush` by 1 in
//     Survival, plays the completion sound, and grants `salvage_sherd` /
//     `obtain_sniffer_egg` advancements when applicable.
func (s *Server) BrushBlockAt(
	session *PlayerSession,
	dim string,
	x, y, z int,
	hitDirection string,
) (dustedStage int, completed bool, poppedItem world.ItemStack, ok bool) {
	st, done, item, _, ok := s.stepBrushBlockInternal(session, dim, x, y, z, hitDirection, true)
	return st, done, item, ok
}

// BrushBlockTickAt advances brushing on a brushable block using vanilla's 10-stroke
// counter (`BrushCount` 1..10, mapping to `dusted` stages 0, 0, 1, 1, 1, 2, 2, 3, 3, complete).
func (s *Server) BrushBlockTickAt(
	session *PlayerSession,
	dim string,
	x, y, z int,
	hitDirection string,
) (dustedStage int, completed bool, poppedItem world.ItemStack, ok bool) {
	st, done, item, _, ok := s.stepBrushBlockInternal(session, dim, x, y, z, hitDirection, false)
	return st, done, item, ok
}

func (s *Server) stepBrushBlockInternal(
	session *PlayerSession,
	dim string,
	x, y, z int,
	hitDirection string,
	advanceStageDirectly bool,
) (dustedStage int, completed bool, poppedItem world.ItemStack, poppedMeta ItemMeta, ok bool) {
	if s == nil || y < world.MinY || y >= world.MinY+world.WorldHeight {
		return 0, false, world.ItemStack{}, ItemMeta{}, false
	}
	normDim := normalizeHopperDim(dim)
	if session != nil && dim == "" {
		if d := session.Dimension(); d != "" {
			normDim = normalizeHopperDim(d)
		}
	}
	cache := s.DimensionChunks(normDim)
	if cache == nil {
		return 0, false, world.ItemStack{}, ItemMeta{}, false
	}
	curState := cache.GetBlock(x, y, z)
	if !world.IsBrushableBlock(curState) {
		return 0, false, world.ItemStack{}, ItemMeta{}, false
	}
	blockName := world.BlockName(curState)
	dir := normalizeBrushDirection(hitDirection)

	st := s.brushableStore()
	st.mu.Lock()
	be := s.getOrCreateBrushableLocked(st, normDim, x, y, z, cache)
	be.BlockName = blockName
	be.HitDirection = dir
	be.IdleTicks = 0
	unpackBrushableLootLocked(be)

	if advanceStageDirectly {
		if be.Dusted < world.BrushableMaxDustedStage {
			be.Dusted++
			switch be.Dusted {
			case 1:
				be.BrushCount = 1
			case 2:
				be.BrushCount = 3
			case 3:
				be.BrushCount = 6
			}
			dustedStage = be.Dusted
		} else {
			completed = true
			be.BrushCount = world.BrushableTotalBrushesToComplete
			dustedStage = world.BrushableMaxDustedStage
		}
	} else {
		be.BrushCount++
		if be.BrushCount >= world.BrushableTotalBrushesToComplete {
			completed = true
			dustedStage = world.BrushableMaxDustedStage
		} else {
			be.Dusted = world.BrushCompletionStageForCount(be.BrushCount)
			dustedStage = be.Dusted
		}
	}

	if completed {
		poppedItem = be.Item
		poppedMeta = be.Meta.Clone()
		delete(st.blocks, hopperBlockKey{dim: normDim, x: x, y: y, z: z})
	}
	st.mu.Unlock()

	cx, cz := int32(x>>4), int32(z>>4)
	// Broadcast dust particles for the current block state.
	s.BroadcastDimensionChunk(normDim, cx, cz, PlayLevelEventCB,
		EncodeLevelEventPacket(LevelEventParticlesDestroyBlock, x, y, z, int32(curState), false))

	if !completed {
		nextState := world.BrushableBlockState(blockName, dustedStage)
		if nextState != world.StateAir && nextState != curState {
			_, upd := cache.ApplyBlockWithUpdates(x, y, z, nextState)
			s.broadcastDimensionBlockChanges(normDim, cache, upd.Changes, upd.LightChanged)
		}
		brushSound := SoundBrushSand
		if blockName == "minecraft:suspicious_gravel" {
			brushSound = SoundBrushGravel
		}
		s.BroadcastDimensionChunk(normDim, cx, cz, PlaySoundPacketID,
			EncodeSoundPacket(brushSound, SoundSourceBlocks, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, 1.0, 1.0, 0))
		s.EmitVibration(VibrationEvent{
			Dimension: normDim,
			Type:      GameEventItemInteractFinish,
			X:         float64(x) + 0.5,
			Y:         float64(y) + 0.5,
			Z:         float64(z) + 0.5,
			Player:    session,
		})
		return dustedStage, false, world.ItemStack{}, ItemMeta{}, true
	}

	// Completion: replace suspicious_sand -> sand or suspicious_gravel -> gravel.
	turnsInto := world.BrushableBlockTurnsInto(curState)
	cache.RemoveStructureBlockEntity(x, y, z)
	_, upd := cache.ApplyBlockWithUpdates(x, y, z, turnsInto)
	s.broadcastDimensionBlockChanges(normDim, cache, upd.Changes, upd.LightChanged)

	// Spawn the excavated item offset toward hitDirection.
	if !poppedItem.IsEmpty() && poppedItem.Count > 0 && s.DoTileDrops() {
		dx, dy, dz := DispenserFacingVector(dir)
		ix := float64(x) + 0.5 + float64(dx)*0.65
		iy := float64(y) + 0.35 + float64(dy)*0.65
		iz := float64(z) + 0.5 + float64(dz)*0.65
		s.SpawnItemEntityInDimension(normDim, ix, iy, iz, poppedItem)
	}

	completeSound := SoundBrushSandComplete
	if blockName == "minecraft:suspicious_gravel" {
		completeSound = SoundBrushGravelComplete
	}
	s.BroadcastDimensionChunk(normDim, cx, cz, PlaySoundPacketID,
		EncodeSoundPacket(completeSound, SoundSourceBlocks, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, 1.0, 1.0, 0))

	if session != nil {
		// Damage held brush by 1 durability in Survival/Adventure.
		if canonicalResourceName(session.HeldItem().ResolvedID()) == BrushItemID {
			s.DamageHeldItem(session, 1)
		}
		itemID := canonicalResourceName(poppedItem.ResolvedID())
		if IsPotterySherd(itemID) {
			ensureArchaeologyAdvancementsRegistered()
			s.GrantAdvancement(session, "minecraft:adventure/salvage_sherd")
		} else if itemID == "minecraft:sniffer_egg" {
			ensureArchaeologyAdvancementsRegistered()
			s.GrantAdvancement(session, "minecraft:husbandry/obtain_sniffer_egg")
		}
	}

	s.EmitVibration(VibrationEvent{
		Dimension: normDim,
		Type:      GameEventBlockChange,
		X:         float64(x) + 0.5,
		Y:         float64(y) + 0.5,
		Z:         float64(z) + 0.5,
		Player:    session,
	})
	return dustedStage, true, poppedItem, poppedMeta, true
}

// TickBrushableBlocks advances the idle cooldown on all partially-brushed
// `BrushableBlockEntity` instances by 1 game tick, stepping `dusted` down
// (`3 -> 2 -> 1 -> 0`) every 4 ticks once 40 idle ticks have elapsed.
func (s *Server) TickBrushableBlocks() int {
	if s == nil {
		return 0
	}
	type decayUpdate struct {
		dim        string
		x, y, z    int
		blockName  string
		nextDusted int
	}
	var updates []decayUpdate

	st := s.brushableStore()
	st.mu.Lock()
	for _, be := range st.blocks {
		if be == nil || (be.Dusted <= 0 && be.BrushCount <= 0) {
			continue
		}
		be.IdleTicks++
		if be.IdleTicks < world.BrushableCooldownTicks {
			continue
		}
		if (be.IdleTicks-world.BrushableCooldownTicks)%world.BrushableResetTickDelay != 0 {
			continue
		}
		if be.Dusted > 0 {
			be.Dusted--
		}
		switch be.Dusted {
		case 0:
			be.BrushCount = 0
			be.HitDirection = ""
		case 1:
			be.BrushCount = 1
		case 2:
			be.BrushCount = 3
		}
		updates = append(updates, decayUpdate{
			dim:        be.Dimension,
			x:          be.X,
			y:          be.Y,
			z:          be.Z,
			blockName:  be.BlockName,
			nextDusted: be.Dusted,
		})
	}
	st.mu.Unlock()

	for _, u := range updates {
		cache := s.DimensionChunks(u.dim)
		if cache == nil {
			continue
		}
		cur := cache.GetBlock(u.x, u.y, u.z)
		if !world.IsBrushableBlock(cur) {
			continue
		}
		nextState := world.BrushableBlockState(world.BlockName(cur), u.nextDusted)
		if nextState != world.StateAir && nextState != cur {
			_, upd := cache.ApplyBlockWithUpdates(u.x, u.y, u.z, nextState)
			s.broadcastDimensionBlockChanges(u.dim, cache, upd.Changes, upd.LightChanged)
		}
	}
	return len(updates)
}

// BreakBrushableBlockAt breaks a `suspicious_sand` or `suspicious_gravel` block
// at (dim, x, y, z): in vanilla 26.1.2, breaking a brushable block (even with
// Silk Touch) drops nothing and destroys the buried item.
func (s *Server) BreakBrushableBlockAt(dim string, x, y, z int) bool {
	if s == nil {
		return false
	}
	normDim := normalizeHopperDim(dim)
	cache := s.DimensionChunks(normDim)
	key := hopperBlockKey{dim: normDim, x: x, y: y, z: z}

	st := s.brushableStore()
	st.mu.Lock()
	_, hadBE := st.blocks[key]
	delete(st.blocks, key)
	st.mu.Unlock()

	blockName := "minecraft:suspicious_sand"
	wasBlock := false
	if cache != nil {
		cur := cache.GetBlock(x, y, z)
		if world.IsBrushableBlock(cur) {
			wasBlock = true
			blockName = world.BlockName(cur)
			cache.RemoveStructureBlockEntity(x, y, z)
			if broken, upd := cache.BreakBlockWithUpdates(x, y, z); broken {
				s.broadcastDimensionBlockChanges(normDim, cache, upd.Changes, upd.LightChanged)
			}
		}
	}
	if !hadBE && !wasBlock {
		return false
	}
	sound := SoundSuspiciousSandBreak
	if blockName == "minecraft:suspicious_gravel" {
		sound = SoundSuspiciousGravBreak
	}
	cx, cz := int32(x>>4), int32(z>>4)
	s.BroadcastDimensionChunk(normDim, cx, cz, PlaySoundPacketID,
		EncodeSoundPacket(sound, SoundSourceBlocks, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, 1.0, 1.0, 0))
	return true
}

// ConvertDirtToMudWithWaterBottle handles a player (or dispenser when `session == nil`)
// using a water potion bottle (`minecraft:potion` with `PotionWater`) on `minecraft:dirt`,
// `minecraft:coarse_dirt`, or `minecraft:rooted_dirt` at (dim, x, y, z),
// converting the block into `minecraft:mud` and replacing the water bottle with
// `minecraft:glass_bottle` in Survival mode.
func (s *Server) ConvertDirtToMudWithWaterBottle(
	session *PlayerSession,
	dim string,
	x, y, z int,
) bool {
	if s == nil || y < world.MinY || y >= world.MinY+world.WorldHeight {
		return false
	}
	normDim := normalizeHopperDim(dim)
	if session != nil && dim == "" {
		if d := session.Dimension(); d != "" {
			normDim = normalizeHopperDim(d)
		}
	}
	cache := s.DimensionChunks(normDim)
	if cache == nil {
		return false
	}
	cur := cache.GetBlock(x, y, z)
	if !world.CanConvertToMudWithWaterBottle(cur) {
		return false
	}
	var inventorySlot int = -1
	if session != nil {
		inventorySlot = session.HeldInventorySlot()
		if inventorySlot >= 0 && inventorySlot < world.PlayerInventorySlots {
			held := session.InventorySlot(inventorySlot)
			if canonicalResourceName(held.ResolvedID()) != "minecraft:potion" {
				return false
			}
			pot := GetSlotPotionType(session, inventorySlot)
			if pot != "" && canonicalResourceName(pot) != PotionWater {
				return false
			}
		}
	}
	ok, upd := cache.ConvertDirtToMudAt(x, y, z)
	if !ok {
		return false
	}
	s.broadcastDimensionBlockChanges(normDim, cache, upd.Changes, upd.LightChanged)

	if session != nil && inventorySlot >= 0 && inventorySlot < world.PlayerInventorySlots {
		if session.Snapshot().GameMode != GameModeCreative {
			held := session.InventorySlot(inventorySlot)
			SetSlotPotionType(session, inventorySlot, "")
			replaceOrGiveBucketItem(session, inventorySlot, held, world.NewNamedItemStack("minecraft:glass_bottle", 1))
			s.syncPlayerInventorySlot(session, inventorySlot)
		}
	}
	cx, cz := int32(x>>4), int32(z>>4)
	s.BroadcastDimensionChunk(normDim, cx, cz, PlaySoundPacketID,
		EncodeSoundPacket("minecraft:item.bottle.empty", SoundSourceBlocks, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, 1.0, 1.0, 0))
	s.EmitVibration(VibrationEvent{
		Dimension: normDim,
		Type:      GameEventBlockChange,
		X:         float64(x) + 0.5,
		Y:         float64(y) + 0.5,
		Z:         float64(z) + 0.5,
		Player:    session,
	})
	return true
}

// --- NBT Serialization & Persistence (`brushable_blocks.dat`) ---

// ToNBT serializes the BrushableBlockEntity into a vanilla-compatible NBT compound.
func (be *BrushableBlockEntity) ToNBT() *nbt.Compound {
	if be == nil {
		return nbt.NewCompound()
	}
	comp := nbt.NewCompound()
	comp.Set("id", nbt.String("minecraft:brushable_block"))
	comp.Set("Dimension", nbt.String(normalizeHopperDim(be.Dimension)))
	comp.Set("x", nbt.Int(int32(be.X)))
	comp.Set("y", nbt.Int(int32(be.Y)))
	comp.Set("z", nbt.Int(int32(be.Z)))
	if be.BlockName != "" {
		comp.Set("BlockName", nbt.String(be.BlockName))
	}
	if be.LootTable != "" {
		comp.Set("LootTable", nbt.String(be.LootTable))
		if be.LootTableSeed != 0 {
			comp.Set("LootTableSeed", nbt.Long(be.LootTableSeed))
		}
	}
	if !be.Item.IsEmpty() && be.Item.Count > 0 {
		itemComp := nbt.NewCompound()
		itemComp.Set("id", nbt.String(be.Item.ResolvedID()))
		itemComp.Set("count", nbt.Int(be.Item.Count))
		if !be.Meta.IsEmpty() {
			metaComp := nbt.NewCompound()
			encodeItemMetaToNBT(metaComp, be.Meta)
			itemComp.Set("Meta", metaComp)
		}
		comp.Set("item", itemComp)
	}
	if be.HitDirection != "" {
		comp.Set("hit_direction", nbt.String(be.HitDirection))
	}
	if be.BrushCount > 0 {
		comp.Set("brush_count", nbt.Int(int32(be.BrushCount)))
	}
	if be.Dusted > 0 {
		comp.Set("dusted", nbt.Int(int32(be.Dusted)))
	}
	return comp
}

// BrushableBlockEntityFromNBT deserializes a BrushableBlockEntity from an NBT compound.
func BrushableBlockEntityFromNBT(comp *nbt.Compound) (*BrushableBlockEntity, bool) {
	if comp == nil {
		return nil, false
	}
	if idStr, hasID := nbtGetString(comp, "id"); hasID && idStr != "" {
		normID := canonicalResourceName(idStr)
		if normID != "minecraft:brushable_block" && normID != "minecraft:suspicious_sand" && normID != "minecraft:suspicious_gravel" {
			return nil, false
		}
	}
	dim, _ := nbtGetString(comp, "Dimension")
	x, okX := nbtGetInt(comp, "x")
	y, okY := nbtGetInt(comp, "y")
	z, okZ := nbtGetInt(comp, "z")
	if !okX || !okY || !okZ {
		return nil, false
	}
	blockName, _ := nbtGetString(comp, "BlockName")
	if blockName == "" {
		blockName = "minecraft:suspicious_sand"
	}
	lootTable, _ := nbtGetString(comp, "LootTable")
	var lootSeed int64
	if rawSeed, ok := comp.Get("LootTableSeed"); ok {
		if l, isLong := rawSeed.(nbt.Long); isLong {
			lootSeed = int64(l)
		}
	}
	hitDir, _ := nbtGetString(comp, "hit_direction")
	brushCnt, _ := nbtGetInt(comp, "brush_count")
	dusted, _ := nbtGetInt(comp, "dusted")

	be := &BrushableBlockEntity{
		Dimension:     normalizeHopperDim(dim),
		X:             int(x),
		Y:             int(y),
		Z:             int(z),
		BlockName:     canonicalResourceName(blockName),
		LootTable:     lootTable,
		LootTableSeed: lootSeed,
		HitDirection:  hitDir,
		BrushCount:    int(brushCnt),
		Dusted:        int(dusted),
	}
	if rawItem, ok := comp.Get("item"); ok {
		if itemComp, ok := rawItem.(*nbt.Compound); ok && itemComp != nil {
			itemID, _ := nbtGetString(itemComp, "id")
			cnt, okCnt := nbtGetInt(itemComp, "count")
			if !okCnt || cnt <= 0 {
				cnt = 1
			}
			if itemID != "" {
				be.Item = world.NewNamedItemStack(itemID, cnt)
				if rawMeta, ok := itemComp.Get("Meta"); ok {
					if metaComp, ok := rawMeta.(*nbt.Compound); ok && metaComp != nil {
						be.Meta = decodeItemMetaFromNBT(metaComp)
					}
				}
			}
		}
	}
	return be, true
}

func (s *Server) brushableBlocksFilePath() string {
	if s == nil {
		return ""
	}
	dir := strings.TrimSpace(s.cfg.WorldDir)
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "brushable_blocks.dat")
}

// SaveBrushableBlocks persists all BrushableBlockEntities to `<WorldDir>/brushable_blocks.dat`.
func (s *Server) SaveBrushableBlocks() error {
	path := s.brushableBlocksFilePath()
	if path == "" {
		return nil
	}
	st := s.brushableStore()
	st.mu.Lock()
	keys := make([]hopperBlockKey, 0, len(st.blocks))
	for k := range st.blocks {
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
		if be := st.blocks[k]; be != nil {
			list = append(list, be.ToNBT())
		}
	}
	st.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	root := nbt.NewCompound()
	root.Set("BrushableBlocks", nbt.List{ElemID: nbt.TagCompound, Elems: list})
	data := nbt.MarshalNamed("", root)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// LoadBrushableBlocks loads persisted BrushableBlockEntities from `<WorldDir>/brushable_blocks.dat`.
func (s *Server) LoadBrushableBlocks() error {
	path := s.brushableBlocksFilePath()
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
	entries, _ := nbtGetListElems(root, "BrushableBlocks")
	var loaded []*BrushableBlockEntity
	st := s.brushableStore()
	st.mu.Lock()
	for _, raw := range entries {
		comp, ok := raw.(*nbt.Compound)
		if !ok {
			continue
		}
		if be, ok := BrushableBlockEntityFromNBT(comp); ok && be != nil {
			st.blocks[hopperBlockKey{dim: be.Dimension, x: be.X, y: be.Y, z: be.Z}] = be
			loaded = append(loaded, be.Clone())
		}
	}
	st.mu.Unlock()
	for _, be := range loaded {
		if cache := s.DimensionChunks(be.Dimension); cache != nil && world.IsBrushableBlock(cache.GetBlock(be.X, be.Y, be.Z)) {
			if stID := world.BrushableBlockState(be.BlockName, be.Dusted); stID != world.StateAir {
				cache.SetBlock(be.X, be.Y, be.Z, stID)
			}
		}
	}
	return nil
}

// ChunkBrushableBlockEntitiesNBT returns vanilla `block_entities` NBT compounds
// for all brushable blocks in chunk (cx, cz) of dim.
func (s *Server) ChunkBrushableBlockEntitiesNBT(dim string, cx, cz int32) []*nbt.Compound {
	if s == nil {
		return nil
	}
	normDim := normalizeHopperDim(dim)
	st := s.brushableStore()
	st.mu.Lock()
	defer st.mu.Unlock()
	var out []*nbt.Compound
	for k, be := range st.blocks {
		if k.dim != normDim || be == nil {
			continue
		}
		if int32(k.x>>4) == cx && int32(k.z>>4) == cz {
			out = append(out, be.ToNBT())
		}
	}
	return out
}

// UnloadChunkBrushableBlocks removes all brushable blocks in chunk (cx, cz) of
// dim from runtime memory and returns their serialized NBT compounds.
func (s *Server) UnloadChunkBrushableBlocks(dim string, cx, cz int32) []*nbt.Compound {
	if s == nil {
		return nil
	}
	normDim := normalizeHopperDim(dim)
	st := s.brushableStore()
	st.mu.Lock()
	defer st.mu.Unlock()
	var out []*nbt.Compound
	for k, be := range st.blocks {
		if k.dim != normDim {
			continue
		}
		if int32(k.x>>4) == cx && int32(k.z>>4) == cz {
			if be != nil {
				out = append(out, be.ToNBT())
			}
			delete(st.blocks, k)
		}
	}
	return out
}

// LoadChunkBrushableBlockEntitiesNBT loads brushable block entities from a
// chunk's `block_entities` NBT list.
func (s *Server) LoadChunkBrushableBlockEntitiesNBT(dim string, list []*nbt.Compound) int {
	if s == nil || len(list) == 0 {
		return 0
	}
	normDim := normalizeHopperDim(dim)
	var loaded []*BrushableBlockEntity
	st := s.brushableStore()
	st.mu.Lock()
	for _, comp := range list {
		be, ok := BrushableBlockEntityFromNBT(comp)
		if !ok || be == nil {
			continue
		}
		if be.Dimension == "" {
			be.Dimension = normDim
		}
		st.blocks[hopperBlockKey{dim: be.Dimension, x: be.X, y: be.Y, z: be.Z}] = be
		loaded = append(loaded, be.Clone())
	}
	st.mu.Unlock()
	for _, be := range loaded {
		if cache := s.DimensionChunks(be.Dimension); cache != nil && world.IsBrushableBlock(cache.GetBlock(be.X, be.Y, be.Z)) {
			if stID := world.BrushableBlockState(be.BlockName, be.Dusted); stID != world.StateAir {
				cache.SetBlock(be.X, be.Y, be.Z, stID)
			}
		}
	}
	return len(loaded)
}
