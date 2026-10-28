package server

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
	"strings"
	"sync"

	"regionio/internal/nbt"
	"regionio/internal/protocol"
	"regionio/internal/registry"
	"regionio/internal/world"
)

// Serverbound player_action status values (protocol 775).
const (
	PlayerActionStartDestroyBlock   = 0
	PlayerActionAbortDestroyBlock   = 1
	PlayerActionStopDestroyBlock    = 2
	PlayerActionDropAllItems        = 3
	PlayerActionDropItem            = 4
	PlayerActionSwapItemWithOffHand = 6
)

// Serverbound client_command action values (protocol 775).
const (
	ClientCommandPerformRespawn = 0
	ClientCommandRequestStats   = 1
)

// Survival gameplay constants.
const (
	MaxItemStackSize           int32   = 64
	ItemPickupHorizontalRadius float64 = 1.5
	ItemPickupVerticalRadius   float64 = 1.5
	ItemMaxAgeTicks            int     = 6000
	FallDamageThreshold        float64 = 3.0
)

type playerSurvivalState struct {
	mu                sync.Mutex
	fallDistance      float64
	dead              bool
	drawingWeapon     string
	drawingHand       int32
	usingSpyglass     bool
	spyglassHand      int32
	ticksFrozen       int
	freezeTickCounter int
	bubbleVX          float64
	bubbleVY          float64
	bubbleVZ          float64
	itemCooldowns     map[string]int
}

var playerSurvivalStates sync.Map // map[*PlayerSession]*playerSurvivalState

func getSurvivalState(session *PlayerSession) *playerSurvivalState {
	if session == nil {
		return nil
	}
	if v, ok := playerSurvivalStates.Load(session); ok {
		return v.(*playerSurvivalState)
	}
	actual, _ := playerSurvivalStates.LoadOrStore(session, &playerSurvivalState{})
	return actual.(*playerSurvivalState)
}

// IsDead reports whether session is currently dead and awaiting respawn.
func (session *PlayerSession) IsDead() bool {
	if session == nil {
		return false
	}
	session.mu.RLock()
	hp := session.health
	session.mu.RUnlock()
	st := getSurvivalState(session)
	st.mu.Lock()
	defer st.mu.Unlock()
	if hp > 0 {
		st.dead = false
		return false
	}
	return true
}

// FallDistance returns the distance accumulated during the player's current fall.
func (session *PlayerSession) FallDistance() float64 {
	if session == nil {
		return 0
	}
	st := getSurvivalState(session)
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.fallDistance
}

// ResetFallDistance resets the player's accumulated fall distance to 0.
func (session *PlayerSession) ResetFallDistance() {
	if session == nil {
		return
	}
	st := getSurvivalState(session)
	st.mu.Lock()
	st.fallDistance = 0
	st.mu.Unlock()
}

// SetFallDistance sets the player's accumulated fall distance.
func (session *PlayerSession) SetFallDistance(dist float64) {
	if session == nil {
		return
	}
	if dist < 0 {
		dist = 0
	}
	st := getSurvivalState(session)
	st.mu.Lock()
	st.fallDistance = dist
	st.mu.Unlock()
}

// ResetPlayerFallDistance resets session's accumulated fall distance to 0.
func (s *Server) ResetPlayerFallDistance(session *PlayerSession) {
	if session != nil {
		session.ResetFallDistance()
	}
}

// IsItemOnCooldown reports whether itemID (or its canonical cooldown group) is currently on cooldown for session.
func (session *PlayerSession) IsItemOnCooldown(itemID string) bool {
	if session == nil {
		return false
	}
	key := canonicalResourceName(itemID)
	st := getSurvivalState(session)
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.itemCooldowns == nil {
		return false
	}
	return st.itemCooldowns[key] > 0
}

// SetItemCooldown sets a cooldown in ticks for itemID on session.
func (session *PlayerSession) SetItemCooldown(itemID string, ticks int) {
	if session == nil {
		return
	}
	key := canonicalResourceName(itemID)
	st := getSurvivalState(session)
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.itemCooldowns == nil {
		st.itemCooldowns = make(map[string]int)
	}
	if ticks <= 0 {
		delete(st.itemCooldowns, key)
	} else {
		st.itemCooldowns[key] = ticks
	}
}

// TickItemCooldowns decrements active item cooldown timers by 1 tick.
func (session *PlayerSession) TickItemCooldowns() {
	if session == nil {
		return
	}
	st := getSurvivalState(session)
	st.mu.Lock()
	defer st.mu.Unlock()
	for k, v := range st.itemCooldowns {
		if v <= 1 {
			delete(st.itemCooldowns, k)
		} else {
			st.itemCooldowns[k] = v - 1
		}
	}
}

// Health returns the player's current health points.
func (session *PlayerSession) Health() float32 {
	if session == nil {
		return 0
	}
	session.mu.RLock()
	defer session.mu.RUnlock()
	return session.health
}

// AddItemToInventory merges stack into the player's inventory (preferring hotbar
// slots 36..44, then main inventory slots 9..35) and returns the number of items
// added along with the container slot indices that changed.
func (session *PlayerSession) AddItemToInventory(stack world.ItemStack) (added int32, changedSlots []int) {
	if session == nil || stack.IsEmpty() {
		return 0, nil
	}
	itemID := stack.ResolvedItemID()
	if itemID <= 0 {
		return 0, nil
	}
	remaining := stack.Count

	session.mu.Lock()
	defer session.mu.Unlock()

	// 1. Merge into existing non-full stacks of the same item: hotbar first, then main inventory.
	mergeSlotRange := func(start, end int) {
		for slot := start; slot < end && remaining > 0; slot++ {
			cur := session.inventory[slot]
			if cur.IsEmpty() || cur.ResolvedItemID() != itemID || cur.Count >= MaxItemStackSize {
				continue
			}
			space := MaxItemStackSize - cur.Count
			take := remaining
			if take > space {
				take = space
			}
			cur.Count += take
			session.inventory[slot] = world.NewItemStack(itemID, cur.Count)
			remaining -= take
			added += take
			changedSlots = append(changedSlots, slot)
		}
	}
	mergeSlotRange(world.HotbarContainerOffset, world.HotbarContainerOffset+world.HotbarSlotCount)
	mergeSlotRange(9, world.HotbarContainerOffset)

	// 2. Fill empty slots: hotbar first, then main inventory.
	fillEmptyRange := func(start, end int) {
		for slot := start; slot < end && remaining > 0; slot++ {
			if !session.inventory[slot].IsEmpty() {
				continue
			}
			take := remaining
			if take > MaxItemStackSize {
				take = MaxItemStackSize
			}
			session.inventory[slot] = world.NewItemStack(itemID, take)
			remaining -= take
			added += take
			changedSlots = append(changedSlots, slot)
		}
	}
	fillEmptyRange(world.HotbarContainerOffset, world.HotbarContainerOffset+world.HotbarSlotCount)
	fillEmptyRange(9, world.HotbarContainerOffset)

	return added, changedSlots
}

// SpawnItemEntity spawns a minecraft:item entity carrying stack at (x, y, z)
// in the Overworld and broadcasts its creation and item metadata to nearby players.
func (s *Server) SpawnItemEntity(x, y, z float64, stack world.ItemStack) (int32, *world.Entity) {
	return s.SpawnItemEntityInDimension(world.DimensionOverworld, x, y, z, stack)
}

// SpawnItemEntityInDimension spawns a minecraft:item entity carrying stack at (x, y, z)
// in dim and broadcasts its creation and item metadata to nearby players in dim.
func (s *Server) SpawnItemEntityInDimension(dim string, x, y, z float64, stack world.ItemStack) (int32, *world.Entity) {
	if s == nil || s.entities == nil || stack.IsEmpty() {
		return 0, nil
	}
	itemID := stack.ResolvedItemID()
	if itemID <= 0 {
		return 0, nil
	}
	dim = world.NormalizeDimension(dim)
	normalized := world.NewItemStack(itemID, stack.Count)
	typeID := registry.EntityTypeIndex(world.ItemEntityTypeName)
	if typeID < 0 {
		typeID = 0
	}
	ent := world.NewItemEntity(x, y, z, normalized, typeID)
	ent.Dimension = dim
	id := s.entities.Add(ent)
	saved := s.entities.Get(id)
	if saved != nil {
		pos := saved.ChunkPos()
		s.BroadcastDimensionChunk(dim, pos.X, pos.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, saved))
		s.BroadcastDimensionChunk(dim, pos.X, pos.Z, protocol.PlaySetEntityData, EncodeItemEntityData(id, normalized))
	}
	return id, saved
}

// EncodeItemEntityData encodes a clientbound set_entity_data packet body for a
// minecraft:item entity's ItemStack (synched entity data index 8, serializer 7).
func EncodeItemEntityData(entityID int32, stack world.ItemStack) []byte {
	w := protocol.NewWriter(24)
	w.VarInt(entityID)
	w.Byte(8)   // ItemEntity.DATA_ITEM accessor ID
	w.VarInt(7) // EntityDataSerializers.ITEM_STACK
	itemID := stack.ResolvedItemID()
	if stack.IsEmpty() || itemID <= 0 {
		w.VarInt(0)
	} else {
		w.VarInt(stack.Count)
		w.VarInt(itemID)
		w.VarInt(0) // added components
		w.VarInt(0) // removed components
	}
	w.Byte(0xFF) // end of synched entity data list
	return w.Bytes()
}

// EncodeTakeItemEntity encodes a clientbound take_item_entity (0x7c) packet body.
func EncodeTakeItemEntity(itemEntityID, collectorEntityID, amount int32) []byte {
	w := protocol.NewWriter(16)
	w.VarInt(itemEntityID)
	w.VarInt(collectorEntityID)
	w.VarInt(amount)
	return w.Bytes()
}

// EncodeHurtAnimation encodes a clientbound hurt_animation (0x2a) packet body.
func EncodeHurtAnimation(entityID int32, yaw float32) []byte {
	w := protocol.NewWriter(12)
	w.VarInt(entityID)
	w.Float32(yaw)
	return w.Bytes()
}

// EncodeSetHealth encodes a clientbound set_health (0x68) packet body.
func EncodeSetHealth(health float32, food int32, saturation float32) []byte {
	w := protocol.NewWriter(16)
	w.Float32(health)
	w.VarInt(food)
	w.Float32(saturation)
	return w.Bytes()
}

// EncodePlayerCombatKill encodes a clientbound player_combat_kill (0x44) packet body.
func EncodePlayerCombatKill(playerEntityID int32, message string) []byte {
	w := protocol.NewWriter(len(message) + 16)
	w.VarInt(playerEntityID)
	w.Raw(nbt.Marshal(nbt.String(message)))
	return w.Bytes()
}

// EncodeRespawnPacket encodes a clientbound respawn (0x52) packet body for
// protocol 775 (CommonPlayerSpawnInfo + dataToKeep).
func EncodeRespawnPacket(gameMode uint8) []byte {
	return EncodeDimensionRespawnPacket(world.DimensionOverworld, gameMode, 0, 0)
}

func encodeContainerSetSlotPacket(stateID int32, slot int, stack world.ItemStack) []byte {
	w := protocol.NewWriter(32)
	w.VarInt(0) // containerId 0 = player inventory
	w.VarInt(stateID)
	w.Uint16(uint16(int16(slot)))
	itemID := stack.ResolvedItemID()
	if stack.IsEmpty() || itemID <= 0 {
		w.VarInt(0)
	} else {
		w.VarInt(stack.Count)
		w.VarInt(itemID)
		w.VarInt(0)
		w.VarInt(0)
	}
	return w.Bytes()
}

func encodeContainerSetContentPacket(stateID int32, inv [world.PlayerInventorySlots]world.ItemStack) []byte {
	w := protocol.NewWriter(256)
	w.VarInt(0) // containerId 0 = player inventory
	w.VarInt(stateID)
	w.VarInt(world.PlayerInventorySlots)
	for i := 0; i < world.PlayerInventorySlots; i++ {
		stack := inv[i]
		itemID := stack.ResolvedItemID()
		if stack.IsEmpty() || itemID <= 0 {
			w.VarInt(0)
		} else {
			w.VarInt(stack.Count)
			w.VarInt(itemID)
			w.VarInt(0)
			w.VarInt(0)
		}
	}
	w.VarInt(0) // carried item (empty)
	return w.Bytes()
}

// BreakBlock handles block digging for a player in creative or survival mode.
// In creative mode, blocks break on START_DESTROY_BLOCK (0) or STOP_DESTROY_BLOCK (2)
// without spawning drops. In survival mode, blocks break on STOP_DESTROY_BLOCK (2)
// (or START_DESTROY_BLOCK for zero-hardness plants/torches) and spawn a dropped
// item entity at the block center.
func (s *Server) BreakBlock(session *PlayerSession, x, y, z int, status int32) (broken bool, dropEntityID int32) {
	if s == nil {
		return false, 0
	}
	mode := GameModeCreative
	dim := world.DimensionOverworld
	if session != nil {
		if session.IsDead() {
			return false, 0
		}
		snap := session.Snapshot()
		mode = snap.GameMode
		dim = world.NormalizeDimension(snap.Dimension)
	}
	if mode == GameModeAdventure || mode == GameModeSpectator {
		return false, 0
	}
	cache := s.DimensionChunks(dim)
	if cache == nil {
		return false, 0
	}

	prevState := cache.GetBlock(x, y, z)
	if prevState == world.StateAir || prevState == world.StateWater {
		return false, 0
	}

	switch mode {
	case GameModeSurvival:
		if prevState == world.StateBedrock || world.BlockName(prevState) == "minecraft:end_portal_frame" || world.BlockName(prevState) == "minecraft:end_portal" || world.BlockName(prevState) == "minecraft:end_gateway" {
			return false, 0
		}
		if status == PlayerActionStartDestroyBlock && world.IsNoteBlock(prevState) {
			if _, nbRes := s.AttackNoteBlock(session, x, y, z); len(nbRes.Changes) > 0 || len(nbRes.LightChanged) > 0 {
				s.broadcastDimensionBlockChanges(dim, cache, nbRes.Changes, nbRes.LightChanged)
			}
			return false, 0
		}
		if status != PlayerActionStopDestroyBlock &&
			!(status == PlayerActionStartDestroyBlock && world.IsInstantBreakBlock(prevState)) {
			return false, 0
		}
	case GameModeCreative:
		if status != PlayerActionStartDestroyBlock && status != PlayerActionStopDestroyBlock {
			return false, 0
		}
	default:
		return false, 0
	}

	var heldStack world.ItemStack
	var toolMeta ItemMeta
	heldSlot := -1
	if session != nil {
		heldSlot = session.HeldInventorySlot()
		if heldSlot >= 0 {
			heldStack = session.InventorySlot(heldSlot)
			toolMeta = GetSlotItemMeta(session, heldSlot)
		}
	}

	if world.BlockName(prevState) == "minecraft:tripwire" {
		if mode == GameModeSurvival && s.DoTileDrops() {
			s.BreakTripwireBlock(dim, x, y, z, heldStack.ResolvedID())
		} else {
			withShears := canonicalResourceName(heldStack.ResolvedID()) == "minecraft:shears"
			twRes := cache.BreakTripwireAt(x, y, z, withShears)
			s.broadcastDimensionBlockChanges(dim, cache, twRes.Changes, twRes.LightChanged)
		}
		s.CheckHangingEntitiesAroundBlock(dim, x, y, z)
		s.OnSculkBlockBroken(dim, x, y, z, prevState)
		s.EmitPlayerBlockVibration(session, GameEventBlockDestroy, dim, x, y, z, "minecraft:tripwire")
		if mode == GameModeSurvival {
			s.RecordBlockBreakExhaustion(session)
			s.RecordBlockMined(session, "minecraft:tripwire")
			if session != nil && heldSlot >= 0 && !heldStack.IsEmpty() {
				s.RecordItemUsed(session, heldStack.ResolvedID(), 1)
			}
		}
		return true, 0
	}

	if IsDecoratedPotBlock(prevState) {
		shatter := mode == GameModeSurvival && ShouldToolShatterDecoratedPot(heldStack, toolMeta)
		s.BreakDecoratedPotAt(dim, x, y, z, shatter, mode == GameModeSurvival)
		s.CheckHangingEntitiesAroundBlock(dim, x, y, z)
		s.OnSculkBlockBroken(dim, x, y, z, prevState)
		s.EmitPlayerBlockVibration(session, GameEventBlockDestroy, dim, x, y, z, "minecraft:decorated_pot")
		if mode == GameModeSurvival {
			s.RecordBlockBreakExhaustion(session)
			s.RecordBlockMined(session, "minecraft:decorated_pot")
			if session != nil && heldSlot >= 0 && !heldStack.IsEmpty() {
				s.RecordItemUsed(session, heldStack.ResolvedID(), 1)
				if s.DamagePlayerItem(session, heldSlot, 1) {
					s.RecordItemBroken(session, heldStack.ResolvedID())
				}
			}
		}
		return true, 0
	}

	if world.IsFireBlock(prevState) {
		s.ExtinguishFireAt(dim, x, y, z)
		s.CheckHangingEntitiesAroundBlock(dim, x, y, z)
		s.OnSculkBlockBroken(dim, x, y, z, prevState)
		s.EmitPlayerBlockVibration(session, GameEventBlockDestroy, dim, x, y, z, world.BlockName(prevState))
		return true, 0
	}

	if world.IsIceBlock(prevState) {
		s.BreakIceBlock(dim, x, y, z, heldStack, toolMeta, mode == GameModeCreative)
		s.CheckHangingEntitiesAroundBlock(dim, x, y, z)
		s.OnSculkBlockBroken(dim, x, y, z, prevState)
		s.EmitPlayerBlockVibration(session, GameEventBlockDestroy, dim, x, y, z, world.BlockName(prevState))
		if mode == GameModeSurvival {
			s.RecordBlockBreakExhaustion(session)
			s.RecordBlockMined(session, world.BlockName(prevState))
			if session != nil && heldSlot >= 0 && !heldStack.IsEmpty() {
				s.RecordItemUsed(session, heldStack.ResolvedID(), 1)
				if s.DamagePlayerItem(session, heldSlot, 1) {
					s.RecordItemBroken(session, heldStack.ResolvedID())
				}
			}
		}
		return true, 0
	}

	if IsCampfireBlock(prevState) {
		if mode == GameModeSurvival && s.DoTileDrops() {
			s.BreakCampfireAt(dim, x, y, z, HasSilkTouch(toolMeta))
		} else {
			s.dropAndRemoveCampfireEntity(dim, x, y, z)
		}
	}

	broken, upd := cache.BreakBlockWithUpdates(x, y, z)
	if !broken {
		return false, 0
	}
	s.InvalidatePortalPOIs(dim, upd.Changes)
	if world.IsEndGatewayState(prevState) {
		s.RemoveEndGatewayAt(dim, x, y, z)
	}
	s.broadcastDimensionBlockChanges(dim, cache, upd.Changes, upd.LightChanged)
	if world.IsTNTBlock(prevState) {
		usedShears := canonicalResourceName(heldStack.ResolvedID()) == "minecraft:shears"
		if mode != GameModeCreative && s.HandleTNTBlockBreak(dim, x, y, z, prevState, usedShears) {
			s.CheckHangingEntitiesAroundBlock(dim, x, y, z)
			s.OnSculkBlockBroken(dim, x, y, z, prevState)
			s.EmitPlayerBlockVibration(session, GameEventBlockDestroy, dim, x, y, z, "minecraft:tnt")
			if mode == GameModeSurvival {
				s.RecordBlockBreakExhaustion(session)
				s.RecordBlockMined(session, "minecraft:tnt")
			}
			return true, 0
		}
	}
	s.CheckHangingEntitiesAroundBlock(dim, x, y, z)
	if y+1 < world.MinY+world.WorldHeight && IsCampfireBlock(cache.GetBlock(x, y+1, z)) {
		s.UpdateCampfireSignalFire(dim, x, y+1, z)
	}
	if IsBedBlock(prevState) {
		s.BreakBedPartner(dim, cache, x, y, z, prevState)
	}
	if IsChestBlock(prevState) || world.IsCopperChestBlock(prevState) {
		s.DropChestContents(x, y, z)
	}
	if IsDispenserOrDropperBlock(prevState) {
		s.BreakDispenserOrDropper(dim, x, y, z)
	}
	if IsHopperBlock(prevState) {
		s.BreakHopperAndDropItems(dim, x, y, z)
	}
	if IsCrafterBlock(prevState) {
		if drops, _, ok := s.RemoveCrafterAt(dim, x, y, z); ok {
			for _, d := range drops {
				if !d.IsEmpty() && d.Count > 0 {
					_, _ = s.SpawnItemEntityInDimension(dim, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, d)
				}
			}
		}
	}
	if IsChiseledBookshelfBlock(prevState) {
		if drops, _, ok := s.RemoveChiseledBookshelfAt(dim, x, y, z); ok {
			for _, d := range drops {
				if !d.IsEmpty() && d.Count > 0 {
					_, _ = s.SpawnItemEntityInDimension(dim, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, d)
				}
			}
		}
	}
	if IsShulkerBoxItemOrBlock(world.BlockName(prevState)) {
		s.BreakShulkerBox(dim, x, y, z)
	}
	if IsFurnaceBlock(prevState) {
		s.DropFurnaceContents(x, y, z)
	}
	if IsBrewingStandBlock(prevState) {
		s.DropBrewingStandContentsInDim(dim, x, y, z)
	}
	if IsLecternBlock(prevState) {
		if book, _, _, ok := s.RemoveLecternAt(dim, x, y, z); ok && !book.IsEmpty() {
			_, _ = s.SpawnItemEntityInDimension(dim, float64(x)+0.5, float64(y)+0.25, float64(z)+0.5, book)
		}
	}
	if IsSignBlock(prevState) {
		s.RemoveSignAt(dim, x, y, z)
	}
	if IsJukeboxBlock(prevState) {
		_, jbRes := s.BreakJukeboxAt(dim, x, y, z)
		if len(jbRes.Changes) > 0 || len(jbRes.LightChanged) > 0 {
			s.broadcastDimensionBlockChanges(dim, cache, jbRes.Changes, jbRes.LightChanged)
		}
	}
	if IsBeaconBlock(prevState) {
		s.RemoveBeaconInDim(dim, x, y, z)
	}
	if IsConduitBlock(prevState) {
		s.RemoveConduitInDim(dim, x, y, z)
	}
	if IsCauldronBlock(prevState) {
		if compRes := s.UpdateComparatorsAt(dim, x, y, z); len(compRes.Changes) > 0 || len(compRes.LightChanged) > 0 {
			s.broadcastDimensionBlockChanges(dim, cache, compRes.Changes, compRes.LightChanged)
		}
	}
	s.OnSculkBlockBroken(dim, x, y, z, prevState)
	s.OnWorkstationBlockBroken(dim, x, y, z)
	s.EmitPlayerBlockVibration(session, GameEventBlockDestroy, dim, x, y, z, world.BlockName(prevState))
	var bannerDrop world.ItemStack
	var hasBannerDrop bool
	if IsBannerBlock(prevState) {
		bannerDrop, _, hasBannerDrop = s.RemoveBannerAt(dim, x, y, z)
	}

	if mode == GameModeSurvival {
		s.RecordBlockBreakExhaustion(session)
		s.RecordBlockMined(session, world.BlockName(prevState))
		if session != nil && heldSlot >= 0 {
			if !heldStack.IsEmpty() {
				s.RecordItemUsed(session, heldStack.ResolvedID(), 1)
			}
			if !world.IsInstantBreakBlock(prevState) {
				if s.DamagePlayerItem(session, heldSlot, 1) && !heldStack.IsEmpty() {
					s.RecordItemBroken(session, heldStack.ResolvedID())
				}
			}
		}
		if !s.DoTileDrops() {
			return true, 0
		}
		if IsCampfireBlock(prevState) {
			return true, 0
		}
		if IsChiseledBookshelfBlock(prevState) {
			if HasSilkTouch(toolMeta) {
				bsDrop := world.NewNamedItemStack("minecraft:chiseled_bookshelf", 1)
				if !bsDrop.IsEmpty() {
					dropID, _ := s.SpawnItemEntityInDimension(dim, float64(x)+0.5, float64(y)+0.25, float64(z)+0.5, bsDrop)
					return true, dropID
				}
			}
			return true, 0
		}
		if hasBannerDrop && !bannerDrop.IsEmpty() {
			dropID, _ := s.SpawnItemEntityInDimension(dim, float64(x)+0.5, float64(y)+0.25, float64(z)+0.5, bannerDrop)
			return true, dropID
		}
		if IsCauldronBlock(prevState) {
			cauldDrop := world.NewNamedItemStack("minecraft:cauldron", 1)
			if !cauldDrop.IsEmpty() {
				dropID, _ := s.SpawnItemEntityInDimension(dim, float64(x)+0.5, float64(y)+0.25, float64(z)+0.5, cauldDrop)
				return true, dropID
			}
		}
		blockName := world.BlockName(prevState)
		fortune := EnchantmentFortuneLevel(toolMeta)
		if isFarmingCropBlockName(blockName) && !(HasSilkTouch(toolMeta) && blockName == "minecraft:melon") {
			var firstDropID int32
			for _, d := range HarvestCropDrops(prevState, fortune, nil) {
				if id, _ := s.SpawnItemEntityInDimension(dim, float64(x)+0.5, float64(y)+0.25, float64(z)+0.5, d); firstDropID == 0 {
					firstDropID = id
				}
			}
			return true, firstDropID
		}
		if strings.HasSuffix(blockName, "_leaves") && !HasSilkTouch(toolMeta) && heldStack.ResolvedID() != "minecraft:shears" {
			var firstDropID int32
			for _, d := range HarvestLeafDecayDrops(prevState, fortune, nil) {
				if id, _ := s.SpawnItemEntityInDimension(dim, float64(x)+0.5, float64(y)+0.25, float64(z)+0.5, d); firstDropID == 0 {
					firstDropID = id
				}
			}
			return true, firstDropID
		}
		if drop, ok := ApplyBlockDropEnchantments(prevState, toolMeta, nil); ok {
			dropID, _ := s.SpawnItemEntityInDimension(dim, float64(x)+0.5, float64(y)+0.25, float64(z)+0.5, drop)
			return true, dropID
		}
	}
	return true, 0
}

func isFarmingCropBlockName(blockName string) bool {
	switch blockName {
	case "minecraft:wheat", "minecraft:carrots", "minecraft:potatoes", "minecraft:beetroots",
		"minecraft:melon_stem", "minecraft:attached_melon_stem",
		"minecraft:pumpkin_stem", "minecraft:attached_pumpkin_stem",
		"minecraft:melon", "minecraft:nether_wart", "minecraft:cocoa", "minecraft:sweet_berry_bush":
		return true
	default:
		return false
	}
}

// HeldInventorySlot returns the container slot index (36..44, or fallback 0..8)
// of the player's currently selected hotbar slot, or -1 if session is nil.
func (session *PlayerSession) HeldInventorySlot() int {
	if session == nil {
		return -1
	}
	session.mu.RLock()
	defer session.mu.RUnlock()
	sel := int(session.selectedSlot)
	if sel < 0 || sel >= world.HotbarSlotCount {
		sel = 0
	}
	slot := world.HotbarContainerOffset + sel
	if session.inventory[slot].IsEmpty() && !session.inventory[sel].IsEmpty() {
		return sel
	}
	return slot
}

// HeldItemMeta returns the ItemMeta (enchantments, durability damage, custom name)
// associated with the player's currently held hotbar slot.
func (session *PlayerSession) HeldItemMeta() ItemMeta {
	slot := session.HeldInventorySlot()
	if slot < 0 {
		return ItemMeta{}
	}
	return GetSlotItemMeta(session, slot)
}

// ArmorEnchantmentProtection returns the fraction of damage (in [0, 0.80]) absorbed
// by enchantments on the player's equipped armor pieces (slots 5..8) for cause.
func (session *PlayerSession) ArmorEnchantmentProtection(cause string) float32 {
	if session == nil {
		return 0
	}
	var armorMetas []ItemMeta
	for slot := ArmorSlotHead; slot <= ArmorSlotFeet; slot++ {
		if !session.InventorySlot(slot).IsEmpty() {
			armorMetas = append(armorMetas, GetSlotItemMeta(session, slot))
		}
	}
	if len(armorMetas) == 0 {
		return 0
	}
	return EnchantmentArmorProtection(armorMetas, cause)
}

// PlayerWeaponAttackBonus returns the extra melee damage granted by enchantments
// (Sharpness, Smite, Bane of Arthropods) on session's held weapon against targetEntityType.
func (s *Server) PlayerWeaponAttackBonus(session *PlayerSession, targetEntityType string) float32 {
	if session == nil {
		return 0
	}
	return EnchantmentAttackBonus(session.HeldItemMeta(), targetEntityType)
}

// DamageHeldItem applies durability wear to session's currently held item when in
// survival mode (respecting Unbreaking and removing the item if it breaks).
func (s *Server) DamageHeldItem(session *PlayerSession, amount int32) bool {
	if s == nil || session == nil || amount <= 0 {
		return false
	}
	if session.Snapshot().GameMode != GameModeSurvival {
		return false
	}
	slot := session.HeldInventorySlot()
	if slot < 0 {
		return false
	}
	heldID := session.InventorySlot(slot).ResolvedID()
	broke := s.DamagePlayerItem(session, slot, amount)
	if broke && heldID != "" {
		s.RecordItemBroken(session, heldID)
	}
	return broke
}

// AttackEntityWithEnchantments performs a melee attack against targetEntityID,
// applying weapon enchantment bonus damage (Sharpness/Smite/Bane of Arthropods),
// wearing the held weapon's durability in survival mode, and spawning XP orbs on kill.
func (s *Server) AttackEntityWithEnchantments(session *PlayerSession, targetEntityID int32) (dealtDamage float32, killed bool) {
	if s == nil || s.entities == nil || session == nil || session.IsDead() {
		return 0, false
	}
	if session.Snapshot().GameMode == GameModeSpectator {
		s.SetSpectatorCamera(session, targetEntityID)
		return 0, false
	}
	if victim := s.playerByEntityID(targetEntityID); victim != nil {
		if !s.PvP() {
			return 0, false
		}
		dealt, killed := s.AttackEntity(session, targetEntityID)
		if dealt > 0 {
			s.RecordPlayerDamageDealt(session, dealt)
			s.DamageHeldItem(session, 1)
		}
		if killed {
			s.RecordEntityKilled(session, "minecraft:player")
			s.RecordKilledByEntity(victim, "minecraft:player")
		}
		return dealt, killed
	}
	target := s.entities.Get(targetEntityID)
	if target == nil || target.IsItem() {
		return 0, false
	}
	if IsVehicleEntity(target.TypeName) {
		scale := session.CurrentAttackStrengthScale(0.5)
		weaponDmg := BasePlayerAttackDamage
		if ws, ok := vanillaWeapons[canonicalItemName(session.HeldItem().ResolvedID())]; ok {
			weaponDmg = ws.AttackDamage
		}
		baseDmg := weaponDmg * (0.2 + scale*scale*0.8)
		session.ResetAttackCooldown()
		if baseDmg < 1.0 {
			baseDmg = 1.0
		}
		destroyed := s.DamageVehicle(target.ID, baseDmg, session)
		return baseDmg, destroyed
	}
	scale := session.CurrentAttackStrengthScale(0.5)
	bonus := s.PlayerWeaponAttackBonus(session, target.TypeName) * scale
	frameSt, hadFrame := s.ItemFrameState(targetEntityID)

	dealt, killed := s.AttackEntity(session, targetEntityID)
	if hadFrame {
		if _, stillThere := s.ItemFrameState(targetEntityID); stillThere {
			s.RefreshItemFrameComparators(targetEntityID)
		} else {
			s.refreshRemovedItemFrameComparators(frameSt)
		}
	}
	if dealt <= 0 {
		return 0, false
	}
	s.DamageHeldItem(session, 1)

	if bonus > 0 {
		dealt += bonus
		chunkPos := target.ChunkPos()
		s.BroadcastDimensionChunk(target.EffectiveDimension(), chunkPos.X, chunkPos.Z, PlayAnimateCB,
			EncodeAnimatePacket(target.ID, AnimateMagicCriticalHit))
		if !killed {
			rem := s.MobHealth(targetEntityID) - bonus
			if rem > 0 {
				s.SetMobHealth(targetEntityID, rem)
			} else {
				killed = true
				s.SetMobHealth(targetEntityID, 0)
				deathSnd, deathSrc := mobDeathSound(target.TypeName)
				s.BroadcastDimensionChunk(target.EffectiveDimension(), chunkPos.X, chunkPos.Z, PlaySoundPacketID,
					EncodeSoundPacket(deathSnd, deathSrc, target.X, target.Y, target.Z, 1.0, 1.0, 0))
				s.BroadcastDimensionChunk(target.EffectiveDimension(), chunkPos.X, chunkPos.Z, protocol.PlayEntityEvent,
					EncodeEntityEventPacket(target.ID, EntityEventDeath))
				s.entities.Remove(target.ID)
				s.BroadcastDimensionChunk(target.EffectiveDimension(), chunkPos.X, chunkPos.Z, protocol.PlayRemoveEntities,
					encodeRemoveEntitiesPacket(nil, []int32{target.ID}))
				if s.DoMobLoot() {
					for _, drop := range MobLootDrops(target.TypeName, nil) {
						s.SpawnItemEntityInDimension(target.EffectiveDimension(), target.X, target.Y+0.25, target.Z, drop)
					}
				}
			}
		}
	}
	s.RecordPlayerDamageDealt(session, dealt)
	if killed {
		s.RecordEntityKilled(session, target.TypeName)
		s.TriggerEntityKilled(session, target.TypeName)
		if s.DoMobLoot() {
			if xp := mobKillXPPoints(target.TypeName); xp > 0 {
				s.SpawnExperienceOrbsInDimension(target.EffectiveDimension(), target.X, target.Y+0.25, target.Z, xp)
			}
		}
	}
	return dealt, killed
}

func mobKillXPPoints(typeName string) int32 {
	switch normalizeFoodItemID(typeName) {
	case MobTypeZombie, "minecraft:skeleton", "minecraft:spider", "minecraft:creeper", "minecraft:enderman":
		return 5
	case MobTypePig, "minecraft:cow", "minecraft:sheep", "minecraft:chicken":
		return 2
	default:
		return 0
	}
}

// HandlePlayerAttackPacket decodes a 26.1.2 serverbound attack (0x01) packet and
// executes an enchantment-aware melee attack against the target entity (or
// attaches the spectator camera in Spectator mode).
func (s *Server) HandlePlayerAttackPacket(session *PlayerSession, r *protocol.Reader) error {
	entityID, err := r.VarInt()
	if err != nil {
		return err
	}
	if s != nil && session != nil {
		if session.Snapshot().GameMode == GameModeSpectator {
			s.SetSpectatorCamera(session, entityID)
			return nil
		}
		s.AttackEntityWithEnchantments(session, entityID)
	}
	return nil
}

// HandlePlayerInteractPacket decodes a serverbound interact (0x1a) packet and
// executes an enchantment-aware melee attack when Action == InteractActionAttack,
// or right-click entity interaction (e.g. opening villager trading UI) otherwise.
func (s *Server) HandlePlayerInteractPacket(session *PlayerSession, r *protocol.Reader) error {
	pkt, err := DecodeInteractPacket(r)
	if err != nil {
		return err
	}
	if s != nil && session != nil {
		isSpectator := session.Snapshot().GameMode == GameModeSpectator
		switch pkt.Action {
		case InteractActionAttack:
			if isSpectator {
				s.SetSpectatorCamera(session, pkt.EntityID)
				return nil
			}
			s.AttackEntityWithEnchantments(session, pkt.EntityID)
		case InteractActionInteractAt:
			if isSpectator {
				return nil
			}
			if s.InteractWithDecorationAt(session, pkt.EntityID, pkt.Hand, float64(pkt.TargetX), float64(pkt.TargetY), float64(pkt.TargetZ), true) {
				s.RefreshItemFrameComparators(pkt.EntityID)
				return nil
			}
			if pkt.Hand == 0 {
				if handled, _, err := s.InteractWithVehicle(session, pkt.EntityID); handled || err != nil {
					return err
				}
				if handled, err := s.InteractWithEquineEntity(session, pkt.EntityID, pkt.UsingSecondaryAction || session.IsSneaking()); handled || err != nil {
					return err
				}
			}
		default:
			if isSpectator {
				return nil
			}
			if s.InteractWithZombieVillager(session, pkt.EntityID, nil) {
				return nil
			}
			if pkt.Hand == 0 {
				if handled, _, err := s.InteractWithVehicle(session, pkt.EntityID); handled || err != nil {
					return err
				}
				sneaking := pkt.UsingSecondaryAction || session.IsSneaking()
				if handled, err := s.InteractWithEquineEntity(session, pkt.EntityID, sneaking); handled || err != nil {
					return err
				}
				_, isVil := s.VillagerState(pkt.EntityID)
				if sneaking && isVil && s.TryFeedVillager(session, pkt.EntityID) {
					return nil
				}
				opened, _, _ := s.InteractWithEntity(session, pkt.EntityID)
				if opened {
					snap := session.Snapshot()
					s.FaceVillagerTowardPlayer(pkt.EntityID, snap.X, snap.Z)
					s.RecordTalkedToVillager(session)
				} else if isVil {
					s.TryFeedVillager(session, pkt.EntityID)
				}
				s.RefreshItemFrameComparators(pkt.EntityID)
			}
		}
	}
	return nil
}

// TryFeedVillager attempts to give 1 held villager food item (bread, carrot,
// potato, beetroot) from session's main hand to a non-sleeping villager entity,
// advancing its FoodLevel via AddVillagerFood and consuming 1 item in Survival.
func (s *Server) TryFeedVillager(session *PlayerSession, entityID int32) bool {
	if s == nil || session == nil || session.IsDead() || s.IsVillagerSleeping(entityID) {
		return false
	}
	if _, ok := s.VillagerState(entityID); !ok {
		return false
	}
	slot, stack := session.handSlotAndStack(0)
	if stack.IsEmpty() {
		return false
	}
	itemID := canonicalItemName(stack.ResolvedID())
	if VillagerFoodValue(itemID) <= 0 {
		return false
	}
	s.AddVillagerFood(entityID, itemID, 1)
	if session.Snapshot().GameMode != GameModeCreative {
		stack.Count--
		if stack.Count <= 0 {
			stack = world.ItemStack{}
		}
		_ = session.SetInventorySlot(slot, stack)
		if session.send != nil {
			_ = session.send(protocol.PlayContainerSetSlot, EncodeContainerSlot(0, session.NextContainerStateID(), slot, stack))
		}
		s.TriggerInventoryChanged(session)
	}
	return true
}

// InteractWithEquineEntity handles right-clicking an equine or llama entity:
//   - If sneaking with an empty hand on a tamed mob, opens ClientboundHorseScreenOpenPacket.
//   - If sneaking or holding equipment/food/chest/shears, delegates to InteractWithTameableMob.
//   - Otherwise (not sneaking, adult, unridden), mounts the player onto the equine/llama.
func (s *Server) InteractWithEquineEntity(session *PlayerSession, entityID int32, sneaking bool) (bool, error) {
	if s == nil || s.entities == nil || session == nil || session.IsDead() {
		return false, nil
	}
	ent := s.entities.Get(entityID)
	if ent == nil || !IsHorseLikeMob(ent.TypeName) {
		return false, nil
	}
	held := session.HeldItem()
	if sneaking && held.IsEmpty() && s.IsMobTamed(entityID) {
		cid, err := s.OpenEquineInventory(session, entityID)
		return cid > 0 || err != nil, err
	}
	if sneaking || !held.IsEmpty() {
		if handled, _ := s.InteractWithTameableMob(session, entityID, nil); handled {
			return true, nil
		}
		if sneaking {
			return false, nil
		}
	}
	if !s.IsMobBaby(entityID) && s.EquineRider(entityID) == 0 {
		if !s.IsMobTamed(entityID) {
			_, _ = s.InteractWithTameableMob(session, entityID, nil)
		}
		if s.MountEquine(session.EntityID, entityID) {
			return true, nil
		}
	}
	return false, nil
}

// StrikeLightningInThunderstorm spawns a lightning bolt at (dim, x, y, z) and,
// when the server weather is thundering, rolls TrySpawnSkeletonTrapOnLightning.
func (s *Server) StrikeLightningInThunderstorm(dim string, x, y, z float64, rng *rand.Rand) (boltID, trapHorseID int32, trapSpawned bool) {
	if s == nil {
		return 0, 0, false
	}
	boltID, sx, sy, sz, _ := s.SpawnLightningBolt(dim, x, y, z, rng)
	if s.IsThundering() {
		trapHorseID, trapSpawned = s.TrySpawnSkeletonTrapOnLightning(dim, sx, sy, sz, false, rng)
	}
	return boltID, trapHorseID, trapSpawned
}

func (s *Server) broadcastBlockAndLightUpdate(x, y, z int, state uint16, lightChunks []world.ChunkPos) {
	s.broadcastDimensionBlockChanges(world.DimensionOverworld, s.chunks, []world.BlockChange{{Pos: world.BlockPos{X: x, Y: y, Z: z}, NewState: state}}, lightChunks)
}

func (s *Server) broadcastDimensionBlockChanges(dim string, cache *world.Cache, changes []world.BlockChange, lightChunks []world.ChunkPos) {
	dim = world.NormalizeDimension(dim)
	for _, ch := range changes {
		w := protocol.NewWriter(12)
		w.Position(ch.Pos.X, ch.Pos.Y, ch.Pos.Z)
		w.VarInt(int32(ch.NewState))
		cx := int32(ch.Pos.X >> 4)
		cz := int32(ch.Pos.Z >> 4)
		s.BroadcastDimensionChunk(dim, cx, cz, protocol.PlayBlockUpdate, w.Bytes())
	}
	if cache != nil {
		for _, chunk := range lightChunks {
			if light, err := cache.LightUpdate(chunk.X, chunk.Z); err == nil {
				s.BroadcastDimensionChunk(dim, chunk.X, chunk.Z, protocol.PlayLightUpdate, light)
			}
		}
	}
}

// ConsumeHeldItem decrements the player's currently selected hotbar stack by
// count when in survival mode and sends container_set_slot to the client.
func (s *Server) ConsumeHeldItem(session *PlayerSession, count int32) (slot int, updated world.ItemStack, ok bool) {
	if session == nil || count <= 0 {
		return -1, world.ItemStack{}, false
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeCreative {
		return int(world.HotbarContainerOffset + snap.SelectedSlot), session.HeldItem(), true
	}
	if snap.GameMode != GameModeSurvival {
		return -1, world.ItemStack{}, false
	}

	session.mu.Lock()
	sel := int(session.selectedSlot)
	if sel < 0 || sel >= world.HotbarSlotCount {
		sel = 0
	}
	slot = world.HotbarContainerOffset + sel
	cur := session.inventory[slot]
	if cur.IsEmpty() {
		// Fallback to legacy slot index 0..8 if used.
		if !session.inventory[sel].IsEmpty() {
			slot = sel
			cur = session.inventory[sel]
		} else {
			session.mu.Unlock()
			return -1, world.ItemStack{}, false
		}
	}
	if cur.Count < count {
		session.mu.Unlock()
		return -1, world.ItemStack{}, false
	}
	remaining := cur.Count - count
	if remaining <= 0 {
		updated = world.ItemStack{}
	} else {
		updated = world.NewItemStack(cur.ResolvedItemID(), remaining)
	}
	session.inventory[slot] = updated
	session.containerStateID++
	if session.containerStateID <= 0 {
		session.containerStateID = 1
	}
	stateID := session.containerStateID
	send := session.send
	session.mu.Unlock()

	if send != nil {
		_ = send(protocol.PlayContainerSetSlot, encodeContainerSetSlotPacket(stateID, slot, updated))
	}
	return slot, updated, true
}

// TryPickupItems checks for nearby dropped item entities around session, adds
// them to the player's inventory, sends take_item_entity + container_set_slot,
// and removes or decrements the picked-up item entities. Returns total items picked up.
func (s *Server) TryPickupItems(session *PlayerSession) int32 {
	if s == nil || s.entities == nil || session == nil || session.IsDead() {
		return 0
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator || snap.Health <= 0 {
		return 0
	}
	playerDim := world.NormalizeDimension(snap.Dimension)

	cx := int32(int64(math.Floor(snap.X)) >> 4)
	cz := int32(int64(math.Floor(snap.Z)) >> 4)
	nearby := s.entities.AppendInChunkRange(nil, cx, cz, 1)

	var totalPickedUp int32
	for i := range nearby {
		ent := &nearby[i]
		if !ent.IsItem() || ent.IsExperienceOrb() || ent.PickupDelay > 0 || ent.Item.IsEmpty() {
			continue
		}
		if ent.EffectiveDimension() != playerDim {
			continue
		}
		if math.Abs(ent.X-snap.X) > ItemPickupHorizontalRadius ||
			math.Abs(ent.Z-snap.Z) > ItemPickupHorizontalRadius ||
			math.Abs(ent.Y-snap.Y) > ItemPickupVerticalRadius {
			continue
		}
		// Re-fetch under lock to ensure the entity wasn't already picked up.
		live := s.entities.Get(ent.ID)
		if live == nil || !live.IsItem() || live.IsExperienceOrb() || live.PickupDelay > 0 || live.Item.IsEmpty() || live.EffectiveDimension() != playerDim {
			continue
		}

		added, changedSlots := session.AddItemToInventory(live.Item)
		if added <= 0 {
			continue
		}
		if fishMeta, ok := s.DroppedFishingItemMeta(live.ID); ok && !fishMeta.IsEmpty() {
			for _, slot := range changedSlots {
				SetSlotItemMeta(session, slot, fishMeta)
			}
		}
		if hornInst, ok := s.DroppedGoatHornInstrument(live.ID); ok && hornInst != "" {
			for _, slot := range changedSlots {
				SetSlotGoatHornInstrument(session, slot, hornInst)
			}
		}
		if mapID, ok := s.DroppedMapEntityID(live.ID); ok {
			for _, slot := range changedSlots {
				SetSlotMapID(session, slot, mapID)
			}
		}
		hasBundleContents := false
		if bundleItems, ok := s.DroppedBundleContents(live.ID); ok && len(bundleItems) > 0 {
			hasBundleContents = true
			for _, slot := range changedSlots {
				SetSlotBundleContents(session, slot, bundleItems)
			}
		}
		totalPickedUp += added
		pickedStack := world.NewItemStack(live.Item.ResolvedItemID(), added)
		if pickedStack.ID == "" {
			pickedStack.ID = live.Item.ResolvedID()
		}
		s.RecordItemPickedUp(session, pickedStack.ResolvedID(), added)
		s.TriggerItemAcquired(session, pickedStack)
		s.UnlockRecipesForItem(session, pickedStack)

		chunkPos := live.ChunkPos()
		s.BroadcastDimensionChunk(playerDim, chunkPos.X, chunkPos.Z, protocol.PlayTakeItemEntity,
			EncodeTakeItemEntity(live.ID, session.EntityID, added))

		if session.send != nil {
			if hasBundleContents {
				s.syncPlayerInventoryContainer(session)
			} else {
				for _, slot := range changedSlots {
					stateID := session.NextContainerStateID()
					slotStack := session.InventorySlot(slot)
					_ = session.send(protocol.PlayContainerSetSlot, encodeContainerSetSlotPacket(stateID, slot, slotStack))
				}
			}
		}

		if added >= live.Item.Count {
			s.entities.Remove(live.ID)
			s.BroadcastDimensionChunk(playerDim, chunkPos.X, chunkPos.Z, protocol.PlayRemoveEntities,
				encodeRemoveEntitiesPacket(nil, []int32{live.ID}))
		} else {
			remaining := world.NewItemStack(live.Item.ResolvedItemID(), live.Item.Count-added)
			s.entities.Update(live.ID, func(e *world.Entity) {
				e.Item = remaining
			})
			s.BroadcastDimensionChunk(playerDim, chunkPos.X, chunkPos.Z, protocol.PlaySetEntityData,
				EncodeItemEntityData(live.ID, remaining))
		}
	}
	totalPickedUp += s.TryPickupGroundedProjectiles(session)
	if totalPickedUp > 0 {
		s.TriggerInventoryChanged(session)
		s.UnlockRecipesForInventory(session)
	}
	return totalPickedUp
}

// DamagePlayer applies amount damage to session in survival or adventure mode,
// sends hurt_animation and set_health, and handles player death when health
// drops to 0 or below. Returns true if the damage killed the player.
func (s *Server) DamagePlayer(session *PlayerSession, amount float32, cause string) bool {
	if s == nil || session == nil || amount <= 0 || session.IsDead() {
		return false
	}

	switch cause {
	case "fall":
		if !s.FallDamage() {
			return false
		}
	case "fire", "in_fire", "on_fire", "lava", "hot_floor", "fireball":
		if !s.FireDamage() {
			return false
		}
	case "drown", "drowning":
		if !s.DrowningDamage() {
			return false
		}
	case "freeze", "freezing":
		if !s.FreezeDamage() {
			return false
		}
	case "player":
		if !s.PvP() {
			return false
		}
	}

	isKillCommand := cause == "kill" || cause == "generic_kill"
	isOutOfBorder := cause == "out_of_border" || cause == "outside_border" || cause == "world_border"
	if !isOutOfBorder && !isKillCommand {
		if prot := session.ArmorEnchantmentProtection(cause); prot > 0 {
			amount *= 1.0 - prot
			if amount <= 0 {
				return false
			}
		}
	}

	dim := session.Dimension()
	session.mu.Lock()
	if !isKillCommand && (session.gameMode == GameModeCreative || session.gameMode == GameModeSpectator) {
		session.mu.Unlock()
		return false
	}
	if session.health <= 0 {
		session.mu.Unlock()
		return false
	}
	newHealth := session.health - amount
	died := false
	if isKillCommand || newHealth <= 0 {
		newHealth = 0
		died = true
	}
	session.health = newHealth
	food := session.foodLevel
	saturation := session.foodSaturation
	entityID := session.EntityID
	name := session.Profile.Name
	pos := session.position
	send := session.send
	session.mu.Unlock()

	s.RecordPlayerDamageTaken(session, amount)
	s.DismountShoulderParrots(session)
	s.OnPlayerDamagedWithEffects(session, false)

	if cause != "starve" && cause != "void" && cause != "out_of_world" && !isOutOfBorder {
		for slot := ArmorSlotHead; slot <= ArmorSlotFeet; slot++ {
			st := session.InventorySlot(slot)
			if !st.IsEmpty() {
				if s.DamagePlayerItem(session, slot, 1) {
					s.RecordItemBroken(session, st.ResolvedID())
				}
			}
		}
	}

	if s.IsPlayerSleeping(session) {
		s.WakePlayerFromBed(session)
	}

	if died && s.TryConsumeTotemOfUndying(session, cause) {
		died = false
		newHealth = 1.0
	}

	if died {
		s.StopUsingSpyglass(session)
		s.RecordPlayerDeath(session)
		s.OnPlayerDiedWithEffects(session)
		s.DespawnPlayerFishingBobber(session)
		s.DismountPlayerVehicle(session)
		_ = s.CloseContainer(session, 0, true)
		st := getSurvivalState(session)
		st.mu.Lock()
		st.dead = true
		st.fallDistance = 0
		st.mu.Unlock()
		if !s.KeepInventory() {
			s.DropPlayerDeathInventoryAndXP(session)
		}
		if s.IsHardcore() {
			session.mu.Lock()
			session.gameMode = GameModeSpectator
			session.mu.Unlock()
			s.BroadcastPlayerGameModeUpdate(session)
		}
	}

	cx := int32(int64(math.Floor(pos[0])) >> 4)
	cz := int32(int64(math.Floor(pos[2])) >> 4)
	hurtPayload := EncodeHurtAnimation(entityID, 0)
	s.BroadcastDimensionChunk(dim, cx, cz, protocol.PlayHurtAnimation, hurtPayload)

	if send != nil {
		_ = send(protocol.PlaySetHealth, EncodeSetHealth(newHealth, food, saturation))
	}
	s.SyncHealthObjectives(session)

	if died {
		if name == "" {
			name = "Player"
		}
		var deathMsg string
		switch cause {
		case "fall":
			deathMsg = fmt.Sprintf("%s fell from a high place", name)
		case "bad_respawn_point":
			deathMsg = fmt.Sprintf("%s was killed by [Intentional Game Design]", name)
		case "out_of_border", "outside_border", "world_border":
			deathMsg = fmt.Sprintf("%s left the confines of this world", name)
		default:
			if comp := s.FormatDeathMessage(session, cause, ""); comp.PlainText() != "" {
				deathMsg = comp.PlainText()
			} else {
				deathMsg = fmt.Sprintf("%s died", name)
			}
		}
		if send != nil && !s.DoImmediateRespawn() {
			_ = send(protocol.PlayPlayerCombatKill, EncodePlayerCombatKill(entityID, deathMsg))
		}
		if !isCombatDeathCause(cause) {
			s.IncrementCriteriaObjectives(session.Profile.Name, "deathCount", 1)
			if s.ShowDeathMessages() {
				if cause == "bad_respawn_point" || isOutOfBorder {
					chatW := protocol.NewWriter(len(deathMsg) + 8)
					chatW.Raw(nbt.Marshal(nbt.String(deathMsg)))
					chatW.Bool(false)
					s.Broadcast(protocol.PlaySystemChat, chatW.Bytes())
				} else {
					s.BroadcastDeathMessage(session, cause, "")
				}
			}
		}
		if s.DoImmediateRespawn() && !s.IsHardcore() {
			_ = s.RespawnPlayer(session)
		}
	}
	return died
}

func isCombatDeathCause(cause string) bool {
	switch cause {
	case "player", "mob", "zombie", "skeleton", "spider", "enderman", "creeper", "arrow", "projectile":
		return true
	default:
		return false
	}
}

// HandlePlayerFall updates session's accumulated fall distance from vertical
// movement (oldY -> newY) and applies fall damage (ceil(fallDistance - 3.0))
// upon landing on the ground. Returns the damage applied (0 if none).
func (s *Server) HandlePlayerFall(session *PlayerSession, oldY, newY float64, wasOnGround, onGround bool) float32 {
	if s == nil || session == nil || session.IsDead() {
		return 0
	}
	snap := session.Snapshot()
	st := getSurvivalState(session)

	if snap.GameMode == GameModeCreative || snap.GameMode == GameModeSpectator || snap.Flying || session.IsFlying() || s.PlayerMountedVehicle(session) != 0 {
		st.mu.Lock()
		st.fallDistance = 0
		st.mu.Unlock()
		return 0
	}

	if cache := s.DimensionChunks(world.NormalizeDimension(snap.Dimension)); cache != nil {
		bx, by, bz := int(math.Floor(snap.X)), int(math.Floor(newY)), int(math.Floor(snap.Z))
		if isWaterOrBubbleOrWaterloggedAt(cache, bx, by, bz) || isWaterOrBubbleOrWaterloggedAt(cache, bx, by+1, bz) {
			st.mu.Lock()
			st.fallDistance = 0
			st.mu.Unlock()
			return 0
		}
	}

	dy := newY - oldY
	jumped := false
	fellFar := false
	st.mu.Lock()
	if dy < 0 {
		st.fallDistance += -dy
		if st.fallDistance > 0.5 {
			fellFar = true
		}
	} else if !onGround && wasOnGround {
		// Jumping upward from ground starts a fresh airborne trajectory.
		st.fallDistance = 0
		if dy > 0 {
			jumped = true
		}
	}
	if !onGround {
		st.mu.Unlock()
		if jumped || fellFar {
			s.DismountShoulderParrots(session)
		}
		return 0
	}
	totalFall := st.fallDistance
	st.fallDistance = 0
	st.mu.Unlock()

	if totalFall > 0.5 {
		s.DismountShoulderParrots(session)
		if trampled, res := s.CheckPlayerFarmlandTrample(session, totalFall, nil); trampled && len(res.Changes) > 0 {
			dim := world.NormalizeDimension(snap.Dimension)
			s.broadcastDimensionBlockChanges(dim, s.DimensionChunks(dim), res.Changes, res.LightChanged)
		}
	}

	if !s.FallDamage() || session.HasSlowFalling() {
		return 0
	}
	if totalFall <= FallDamageThreshold {
		return 0
	}
	rawDamage := float32(math.Ceil(totalFall - FallDamageThreshold))
	effectiveBase := rawDamage
	if jb := session.JumpBoostLevel(); jb > 0 {
		effectiveBase -= float32(jb)
	}
	if effectiveBase <= 0 {
		return 0
	}
	prot := session.ArmorEnchantmentProtection("fall")
	resMult := session.ResistanceDamageMultiplier("fall")
	s.DamagePlayerWithEffects(session, rawDamage, "fall")
	return effectiveBase * resMult * (1.0 - prot)
}

// RespawnPlayer restores a dead (or respawning) player's health and hunger to
// full, sends the clientbound respawn packet, teleports the player to their
// spawn point (or world default spawn), and re-synchronizes abilities, health,
// held slot, and inventory.
func (s *Server) RespawnPlayer(session *PlayerSession) error {
	if s == nil || session == nil {
		return errors.New("server: nil player session")
	}
	if !session.IsDead() {
		if session.Dimension() == world.DimensionEnd {
			_, err := s.TravelThroughEndPortal(session)
			return err
		}
		if s.CompleteEndCreditsExit(session) {
			toDim, destX, destY, destZ, yaw, pitch := s.ResolveEndReturnSpawn(session)
			return s.ChangePlayerDimension(session, toDim, destX, destY, destZ, yaw, pitch)
		}
	}

	st := getSurvivalState(session)
	st.mu.Lock()
	st.dead = false
	st.fallDistance = 0
	st.mu.Unlock()

	session.SetFoodExhaustion(0)
	session.SetFoodTickTimer(0)
	session.SetBlockingWithShield(false)
	s.StopUsingSpyglass(session)
	s.StopPlayerFallFlying(session)
	s.DismountPlayerVehicle(session)
	s.CancelEating(session)
	s.DespawnPlayerFishingBobber(session)
	s.WakePlayerFromBed(session)
	s.ClearPlayerEffects(session)

	prevDim := session.Dimension()
	isHC := s.IsHardcore()
	session.mu.Lock()
	session.health = world.DefaultPlayerHealth
	session.foodLevel = world.DefaultPlayerFoodLevel
	session.foodSaturation = world.DefaultPlayerSaturation
	if isHC {
		session.gameMode = GameModeSpectator
	}
	mode := session.gameMode
	selectedSlot := session.selectedSlot
	inv := session.inventory
	session.containerStateID++
	if session.containerStateID <= 0 {
		session.containerStateID = 1
	}
	stateID := session.containerStateID
	send := session.send
	session.mu.Unlock()

	if isHC {
		s.BroadcastPlayerGameModeUpdate(session)
	}

	targetDim, targetX, targetY, targetZ, targetYaw, targetPitch, _, _ := s.ResolvePlayerRespawn(session)
	session.SetDimension(targetDim)
	targetCache := s.DimensionChunks(targetDim)

	if send != nil {
		if err := send(protocol.PlayRespawn, EncodeDimensionRespawnPacket(targetDim, mode, s.cfg.WorldSeed, 0)); err != nil {
			return err
		}
	}
	targetCX := int32(int64(math.Floor(targetX)) >> 4)
	targetCZ := int32(int64(math.Floor(targetZ)) >> 4)
	if prevDim != targetDim {
		if switchFn := getPlayerDimSwitchFunc(session); switchFn != nil && targetCache != nil {
			switchFn(targetCache, targetCX, targetCZ)
		}
	}
	if err := s.TeleportPlayer(session, targetX, targetY, targetZ, targetYaw, targetPitch); err != nil {
		return err
	}
	if send != nil {
		var abilityFlags byte
		switch mode {
		case GameModeCreative:
			abilityFlags = 0x0F
		case GameModeSpectator:
			abilityFlags = 0x07
		default:
			abilityFlags = 0x00
		}
		ab := protocol.NewWriter(9)
		ab.Byte(abilityFlags)
		ab.Float32(0.05)
		ab.Float32(0.1)
		_ = send(protocol.PlayAbilities, ab.Bytes())
		_ = send(protocol.PlaySetHealth, EncodeSetHealth(world.DefaultPlayerHealth, world.DefaultPlayerFoodLevel, world.DefaultPlayerSaturation))

		heldW := protocol.NewWriter(4)
		heldW.VarInt(selectedSlot)
		_ = send(protocol.PlaySetHeldSlot, heldW.Bytes())

		_ = send(protocol.PlayContainerSetContent, encodeContainerSetContentPacket(stateID, inv))
		if s.KeepInventory() {
			_ = s.SendPlayerExperience(session)
		}
		s.SendWorldBorderToPlayer(session)
		s.SendDifficultyToPlayer(session)
	}
	return nil
}

// UseProjectileItem handles starting a bow/trident draw or immediately throwing
// a throwable projectile (snowball, egg, ender pearl, wind charge) on serverbound
// use_item. It also emits projectile vibrations for charged crossbows and
// aerial firework rockets before HandleUseItemPacket processes them.
func (s *Server) UseProjectileItem(session *PlayerSession, hand int32) bool {
	if s == nil || session == nil || session.IsDead() {
		return false
	}
	slot, stack := session.handSlotAndStack(hand)
	if stack.IsEmpty() {
		return false
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator {
		return false
	}
	itemID := canonicalItemName(stack.ResolvedID())

	if IsBoatItem(itemID) {
		if _, placed := s.UseBoatItemInAir(session, hand); placed {
			return true
		}
	}

	switch itemID {
	case "minecraft:bucket":
		if _, ok := s.UseEmptyBucketInAir(session, hand); ok {
			return true
		}
		return false

	case GoatHornItemID:
		if s.UseGoatHornItem(session, hand, "") {
			s.RecordItemUsed(session, itemID, 1)
			return true
		}
		return false

	case WindChargeItemID:
		if chargeID, used := s.UseWindChargeItem(session, hand); used {
			s.RecordItemUsed(session, itemID, 1)
			s.EmitVibration(VibrationEvent{
				Dimension:          snap.Dimension,
				Type:               GameEventProjectileShoot,
				X:                  snap.X,
				Y:                  snap.Y + 1.5,
				Z:                  snap.Z,
				SourceEntityID:     snap.EntityID,
				ProjectileEntityID: chargeID,
				Player:             session,
				Sneaking:           session.IsSneaking(),
				BlockOrItemID:      WindChargeItemID,
			})
			return true
		}
		return false

	case "minecraft:snowball", "minecraft:egg", "minecraft:ender_pearl":
		if itemID == "minecraft:ender_pearl" && session.IsItemOnCooldown("minecraft:ender_pearl") {
			return false
		}
		if !s.consumeHandItemOne(session, hand) {
			return false
		}
		if itemID == "minecraft:ender_pearl" {
			session.SetItemCooldown("minecraft:ender_pearl", 20)
			if session.send != nil {
				_ = session.send(protocol.PlayCooldown, EncodeCooldownPacket("minecraft:ender_pearl", 20))
			}
		}
		s.RecordItemUsed(session, itemID, 1)
		cause := "thrown"
		if itemID == "minecraft:Snowball" || itemID == "minecraft:snowball" {
			cause = "snowball"
		}
		s.traceAndResolveProjectileShot(session, -1, itemID, 0.0, cause, 24.0)
		return true

	case "minecraft:bow", "minecraft:trident":
		s.StartDrawingRangedWeapon(session, hand)
		return false

	case CrossbowItemID:
		charged := GetSlotChargedProjectiles(session, slot)
		if len(charged) > 0 {
			projItemID := canonicalItemName(charged[0].Stack.ResolvedID())
			if projItemID == FireworkRocketItemID {
				s.EmitVibration(VibrationEvent{
					Dimension:          snap.Dimension,
					Type:               GameEventProjectileShoot,
					X:                  snap.X,
					Y:                  snap.Y + 1.5,
					Z:                  snap.Z,
					SourceEntityID:     snap.EntityID,
					ProjectileEntityID: -1,
					Player:             session,
					Sneaking:           session.IsSneaking(),
					BlockOrItemID:      FireworkRocketItemID,
				})
			}
		} else {
			s.StartDrawingRangedWeapon(session, hand)
		}
		return false

	case FireworkRocketItemID:
		if session.IsFallFlying() {
			s.EmitVibration(VibrationEvent{
				Dimension:          snap.Dimension,
				Type:               GameEventProjectileShoot,
				X:                  snap.X,
				Y:                  snap.Y,
				Z:                  snap.Z,
				SourceEntityID:     snap.EntityID,
				ProjectileEntityID: -1,
				Player:             session,
				Sneaking:           session.IsSneaking(),
				BlockOrItemID:      FireworkRocketItemID,
			})
		}
		return false
	}
	return false
}

// ReleaseProjectileWeapon handles PlayerActionReleaseUseItem for a drawn bow,
// trident, or progressively charged crossbow, consuming ammo/durability as
// appropriate, resolving projectile trajectories, and emitting vibrations.
func (s *Server) ReleaseProjectileWeapon(session *PlayerSession) bool {
	if s == nil || session == nil || session.IsDead() {
		return false
	}
	st := getSurvivalState(session)
	var weapon string
	var hand int32
	if st != nil {
		st.mu.Lock()
		weapon = st.drawingWeapon
		hand = st.drawingHand
		st.drawingWeapon = ""
		st.mu.Unlock()
	}

	var (
		drawTicks       int
		explicitlyTimed bool
	)
	if ds := s.playerDrawState(session); ds != nil {
		ds.mu.Lock()
		if ds.active {
			if weapon == "" {
				weapon = ds.weapon
				hand = ds.hand
			}
			drawTicks = ds.drawTicks
			explicitlyTimed = ds.explicitlyTimed
		} else if ds.explicitlyTimed {
			drawTicks = ds.drawTicks
			explicitlyTimed = true
		}
		ds.active = false
		ds.weapon = ""
		ds.drawTicks = 0
		ds.explicitlyTimed = false
		ds.mu.Unlock()
	}

	_, stack := session.handSlotAndStack(hand)
	itemID := canonicalItemName(stack.ResolvedID())
	if itemID != BowItemID && itemID != TridentItemID && itemID != CrossbowItemID {
		_, stack = session.handSlotAndStack(HandMain)
		itemID = canonicalItemName(stack.ResolvedID())
		if itemID == BowItemID || itemID == TridentItemID || itemID == CrossbowItemID {
			hand = HandMain
		} else {
			_, stack = session.handSlotAndStack(HandOffHand)
			itemID = canonicalItemName(stack.ResolvedID())
			if itemID == BowItemID || itemID == TridentItemID || itemID == CrossbowItemID {
				hand = HandOffHand
			}
		}
	}
	if weapon == "" && (itemID == BowItemID || itemID == TridentItemID || itemID == CrossbowItemID) {
		weapon = itemID
	}
	if stack.IsEmpty() || canonicalItemName(stack.ResolvedID()) != weapon {
		return false
	}

	switch weapon {
	case CrossbowItemID:
		if !explicitlyTimed && drawTicks <= 0 {
			drawTicks = CrossbowBaseChargeTicks
		}
		return s.ReleaseCrossbowDraw(session, hand, drawTicks)

	case BowItemID:
		if !explicitlyTimed && drawTicks <= 0 {
			drawTicks = BowFullChargeTicks
		}
		_, ok := s.ShootBow(session, hand, drawTicks, nil)
		return ok

	case TridentItemID:
		if !explicitlyTimed && drawTicks <= 0 {
			drawTicks = TridentMinChargeTicks
		}
		_, ok := s.ThrowTrident(session, hand, drawTicks)
		return ok
	}
	return false
}

func (s *Server) traceAndResolveProjectileShot(
	session *PlayerSession,
	projID int32,
	itemID string,
	damage float32,
	cause string,
	maxRange float64,
) {
	if s == nil || session == nil {
		return
	}
	snap := session.Snapshot()
	dim := world.NormalizeDimension(snap.Dimension)
	startX, startY, startZ := snap.X, snap.Y+1.5, snap.Z
	if projID == 0 {
		projID = -1
	}

	s.EmitVibration(VibrationEvent{
		Dimension:          dim,
		Type:               GameEventProjectileShoot,
		X:                  startX,
		Y:                  startY,
		Z:                  startZ,
		SourceEntityID:     snap.EntityID,
		ProjectileEntityID: projID,
		Player:             session,
		Sneaking:           session.IsSneaking(),
		BlockOrItemID:      itemID,
	})

	lx, ly, lz := ElytraLookVector(snap.Yaw, snap.Pitch)
	landX := startX + lx*maxRange
	landY := startY + ly*maxRange
	landZ := startZ + lz*maxRange

	cache := s.DimensionChunks(dim)
	steps := int(maxRange * 2.0)
	if steps < 1 {
		steps = 1
	}
	for step := 1; step <= steps; step++ {
		dist := float64(step) * 0.5
		rx := startX + lx*dist
		ry := startY + ly*dist
		rz := startZ + lz*dist

		if s.entities != nil {
			cx := int32(int64(math.Floor(rx)) >> 4)
			cz := int32(int64(math.Floor(rz)) >> 4)
			nearby := s.entities.AppendInChunkRange(nil, cx, cz, 1)
			hitEntity := false
			for i := range nearby {
				ent := &nearby[i]
				if ent.ID == snap.EntityID || ent.ID == projID || ent.IsItem() || ent.IsExperienceOrb() ||
					ent.TypeName == EntityTypeFireworkRocket || IsHangingEntity(ent.TypeName) {
					continue
				}
				if ent.EffectiveDimension() != dim {
					continue
				}
				if math.Abs(ent.X-rx) <= 0.8 && ry >= ent.Y-0.2 && ry <= ent.Y+2.0 && math.Abs(ent.Z-rz) <= 0.8 {
					if ent.TypeName == EntityTypeArmorStand {
						dmg := damage
						if dmg <= 0 {
							dmg = 1.0
						}
						s.DamageArmorStand(ent.ID, dmg, cause, session)
						hitEntity = true
						break
					}
					if IsVehicleEntity(ent.TypeName) {
						dmg := damage
						if dmg <= 0 {
							dmg = 1.0
						}
						s.DamageVehicle(ent.ID, dmg, session)
						hitEntity = true
						break
					}
					if damage > 0 {
						switch ent.TypeName {
						case MobTypeWither:
							s.DamageWither(ent.ID, damage, session, true)
						case MobTypeEnderDragon:
							s.DamageEnderDragon(ent.ID, damage, session, true)
						default:
							s.applyDamageToMob(ent, damage, session, 0.3, false, false)
						}
					}
					hitEntity = true
					break
				}
			}
			if hitEntity {
				landX, landY, landZ = rx, ry, rz
				break
			}
		}

		if cache != nil {
			bx := int(math.Floor(rx))
			by := int(math.Floor(ry))
			bz := int(math.Floor(rz))
			hitState := cache.GetBlock(bx, by, bz)
			if world.IsBellBlock(hitState) {
				if s.RingBellAt(dim, bx, by, bz, "", projID, session) {
					s.OnVillageBellRung(dim, bx, by, bz)
				}
			}
			if IsCampfireBlock(hitState) && itemID == "minecraft:arrow" && s.PlayerFireTicks(session) > 0 {
				s.TryIgniteCampfireWithProjectile(dim, bx, by, bz, true)
			}
			if IsDecoratedPotBlock(hitState) && s.ProjectilesCanBreakBlocks() {
				s.HitDecoratedPotWithProjectile(dim, bx, by, bz, projID)
				landX, landY, landZ = rx, ry, rz
				break
			}
			if world.BlockName(hitState) == "minecraft:chorus_flower" && s.ProjectilesCanBreakBlocks() {
				if valid, lc := cache.SetBlockWithLight(bx, by, bz, world.StateAir); valid {
					s.BroadcastDimensionBlocks(dim, []world.BlockChange{
						{Pos: world.BlockPos{X: bx, Y: by, Z: bz}, OldState: hitState, NewState: world.StateAir},
					}, lc)
					if s.DoTileDrops() {
						if drop := world.NewNamedItemStack("minecraft:chorus_flower", 1); !drop.IsEmpty() {
							s.SpawnItemEntityInDimension(dim, float64(bx)+0.5, float64(by)+0.5, float64(bz)+0.5, drop)
						}
					}
				}
				landX, landY, landZ = rx, ry, rz
				break
			}
			if !isPassableBlock(hitState) {
				landX, landY, landZ = rx, ry, rz
				break
			}
		}
	}

	if itemID == "minecraft:ender_pearl" && !(session.IsDead() && s.EnderPearlsVanishOnDeath()) {
		if _, travelled := s.CheckEnderPearlGateway(session, dim, landX, landY, landZ); !travelled {
			_ = s.TeleportPlayer(session, landX, landY, landZ, snap.Yaw, snap.Pitch)
			if snap.GameMode != GameModeCreative && snap.GameMode != GameModeSpectator {
				s.DamagePlayerWithEffects(session, 5.0, "fall")
			}
		}
	}

	s.EmitVibration(VibrationEvent{
		Dimension:          dim,
		Type:               GameEventProjectileLand,
		X:                  landX,
		Y:                  landY,
		Z:                  landZ,
		SourceEntityID:     snap.EntityID,
		ProjectileEntityID: projID,
		Player:             session,
		Sneaking:           session.IsSneaking(),
		BlockOrItemID:      itemID,
	})
}

// DispenserPlacementFacing computes the 6-direction `facing` property ("north",
// "south", "west", "east", "up", "down") opposite to a player's look direction
// (matching vanilla DispenserBlock.getStateForPlacement -> getNearestLookingDirection().getOpposite()).
func DispenserPlacementFacing(yaw, pitch float32) string {
	lx, ly, lz := ElytraLookVector(yaw, pitch)
	ax, ay, az := math.Abs(lx), math.Abs(ly), math.Abs(lz)
	if ay > ax && ay > az {
		if ly > 0 {
			return "down"
		}
		return "up"
	}
	if ax > az {
		if lx > 0 {
			return "west"
		}
		return "east"
	}
	if lz > 0 {
		return "north"
	}
	return "south"
}

// ResolveDispenserOrDropperPlacementState resolves the placed block state for a
// `minecraft:dispenser` or `minecraft:dropper` with `facing` opposite to
// session's look direction and `triggered=false`.
func (s *Server) ResolveDispenserOrDropperPlacementState(session *PlayerSession, baseState uint16) uint16 {
	if !IsDispenserOrDropperBlock(baseState) {
		return baseState
	}
	facing := "north"
	if session != nil {
		snap := session.Snapshot()
		facing = DispenserPlacementFacing(snap.Yaw, snap.Pitch)
	}
	blockName := world.BlockName(baseState)
	if st, ok := world.BlockStateByName(blockName, map[string]string{
		"facing":    facing,
		"triggered": "false",
	}); ok {
		return st
	}
	return baseState
}

// BlockFaceDirectionName converts a protocol Face index (0..5: DOWN, UP, NORTH,
// SOUTH, WEST, EAST) into its lowercase direction name.
func BlockFaceDirectionName(face int32) string {
	switch face {
	case 0:
		return "down"
	case 1:
		return "up"
	case 2:
		return "north"
	case 3:
		return "south"
	case 4:
		return "west"
	case 5:
		return "east"
	default:
		return "up"
	}
}

// ResolvePlacedBlockState resolves orientation/facing properties for directional
// blocks placed by a player.
func (s *Server) ResolvePlacedBlockState(session *PlayerSession, baseState uint16, clickedFace int32) uint16 {
	if IsDispenserOrDropperBlock(baseState) {
		return s.ResolveDispenserOrDropperPlacementState(session, baseState)
	}
	var yaw, pitch float32
	if session != nil {
		snap := session.Snapshot()
		yaw, pitch = snap.Yaw, snap.Pitch
	}
	oppLookDir := DispenserPlacementFacing(yaw, pitch)
	lookDir := oppositeDropperDirection(oppLookDir)
	oppHorizFacing := DispenserPlacementFacing(yaw, 0)
	horizLookDir := oppositeDropperDirection(oppHorizFacing)
	faceName := BlockFaceDirectionName(clickedFace)

	if IsCrafterBlock(baseState) {
		orient := CrafterOrientationForPlacement(lookDir, horizLookDir)
		if st := CrafterBlockState(orient, false, false); st != 0 {
			return st
		}
		return baseState
	}
	if IsHopperBlock(baseState) {
		if st := HopperBlockState(HopperPlacementFacing(faceName), true); st != 0 {
			return st
		}
		return baseState
	}
	if IsChiseledBookshelfBlock(baseState) {
		if st := ChiseledBookshelfBlockState(oppHorizFacing, [ChiseledBookshelfSlotCount]bool{}); st != 0 {
			return st
		}
		return baseState
	}
	if IsDecoratedPotBlock(baseState) {
		if st := DecoratedPotBlockState(oppHorizFacing, false, false); st != 0 {
			return st
		}
		return baseState
	}
	if IsCampfireBlock(baseState) {
		if st, ok := world.WithBlockProperty(baseState, "facing", oppHorizFacing); ok {
			return st
		}
		return baseState
	}
	if IsAnvilBlock(baseState) {
		anvilFacing := clockwiseHorizontalDirection(horizLookDir)
		if st := AnvilBlockState(world.BlockName(baseState), anvilFacing); st != 0 {
			return st
		}
		return baseState
	}
	if IsGrindstoneBlock(baseState) {
		gsFace := "wall"
		gsFacing := faceName
		switch faceName {
		case "up":
			gsFace = "floor"
			gsFacing = horizLookDir
		case "down":
			gsFace = "ceiling"
			gsFacing = horizLookDir
		}
		if st := GrindstoneBlockState(gsFace, gsFacing); st != 0 {
			return st
		}
		return baseState
	}
	switch world.BlockName(baseState) {
	case "minecraft:observer":
		if st, ok := world.WithBlockProperty(baseState, "facing", world.ObserverFacingForPlacement(lookDir)); ok {
			return st
		}
	case "minecraft:piston", "minecraft:sticky_piston":
		if st, ok := world.WithBlockProperty(baseState, "facing", oppLookDir); ok {
			return st
		}
	case "minecraft:lightning_rod":
		if st, ok := world.WithBlockProperty(baseState, "facing", faceName); ok {
			return st
		}
	case "minecraft:tripwire_hook":
		hookFacing := oppHorizFacing
		switch faceName {
		case "north", "south", "west", "east":
			hookFacing = faceName
		}
		if st, ok := world.WithBlockProperty(baseState, "facing", hookFacing); ok {
			return st
		}
	}
	return baseState
}

func clockwiseHorizontalDirection(dir string) string {
	switch dir {
	case "north":
		return "east"
	case "east":
		return "south"
	case "south":
		return "west"
	case "west":
		return "north"
	default:
		return "north"
	}
}

// BreakDispenserOrDropper drops all stored items from a broken dispenser or
// dropper at (dim, x, y, z), removes its block entity state, and updates comparators.
func (s *Server) BreakDispenserOrDropper(dim string, x, y, z int) {
	if s == nil {
		return
	}
	s.DropDispenserContentsInDimension(dim, x, y, z)
}

// BreakShulkerBox clears the stored shulker box block entity state at (dim, x, y, z)
// and updates any adjacent redstone comparators.
func (s *Server) BreakShulkerBox(dim string, x, y, z int) {
	if s == nil {
		return
	}
	normDim := normalizeDispenserDim(dim)
	ds := s.dispenserState()
	ds.mu.Lock()
	delete(ds.shulkerBoxes, dispenserBlockKey{dim: normDim, x: x, y: y, z: z})
	ds.mu.Unlock()
	cs := s.chestState()
	cs.mu.Lock()
	delete(cs.chests, chestKey{x, y, z})
	cs.mu.Unlock()
	s.updateDispenserComparatorsAndBroadcast(normDim, x, y, z)
}

func (s *Server) refreshRemovedItemFrameComparators(st ItemFrameState) {
	if s == nil {
		return
	}
	normDim := normalizeDispenserDim(st.Dimension)
	cache := s.BindNoteBlockAndJukeboxRedstone(normDim)
	if cache == nil {
		return
	}
	res1 := cache.UpdateComparatorsAround(st.BlockX, st.BlockY, st.BlockZ)
	dx, dy, dz := DirectionOffset(st.Direction)
	res2 := cache.UpdateComparatorsAround(st.BlockX-dx, st.BlockY-dy, st.BlockZ-dz)
	changes := append(res1.Changes, res2.Changes...)
	lightChanged := append(res1.LightChanged, res2.LightChanged...)
	if len(changes) > 0 || len(lightChanged) > 0 {
		s.broadcastDimensionBlockChanges(normDim, cache, changes, lightChanged)
	}
}

// TickDispenserProjectiles advances all active projectiles launched by dispensers
// by one game tick and cleans up expired or impacted projectiles.
func (s *Server) TickDispenserProjectiles() int {
	if s == nil {
		return 0
	}
	ds := s.dispenserState()
	ds.mu.Lock()
	if len(ds.projectiles) == 0 {
		ds.mu.Unlock()
		return 0
	}
	var toRemove []int32
	type bellHit struct {
		dim        string
		x, y, z    int
		projEntity int32
	}
	var bellHits []bellHit
	ticked := 0
	for id, proj := range ds.projectiles {
		if proj == nil {
			toRemove = append(toRemove, id)
			continue
		}
		ticked++
		proj.TicksLived++
		if proj.Impacted || (proj.MaxTicks > 0 && proj.TicksLived >= proj.MaxTicks) {
			toRemove = append(toRemove, id)
			continue
		}
		proj.X += proj.VX
		proj.Y += proj.VY
		proj.Z += proj.VZ
		if proj.TypeName != EntityTypeSmallFireball && proj.TypeName != EntityTypeWindCharge {
			proj.VY -= 0.05
			proj.VX *= 0.99
			proj.VY *= 0.99
			proj.VZ *= 0.99
		}
		if cache := s.DimensionChunks(proj.Dimension); cache != nil {
			bx, by, bz := int(math.Floor(proj.X)), int(math.Floor(proj.Y)), int(math.Floor(proj.Z))
			if by >= world.MinY && by < world.MinY+world.WorldHeight && world.IsBellBlock(cache.GetBlock(bx, by, bz)) {
				proj.Impacted = true
				bellHits = append(bellHits, bellHit{dim: proj.Dimension, x: bx, y: by, z: bz, projEntity: id})
			}
		}
		if s.entities != nil {
			nx, ny, nz := proj.X, proj.Y, proj.Z
			vx, vy, vz := proj.VX, proj.VY, proj.VZ
			s.entities.Update(id, func(e *world.Entity) {
				e.X = nx
				e.Y = ny
				e.Z = nz
				e.VelocityX = int16(clampDispenserFloat(vx*8000, -32767, 32767))
				e.VelocityY = int16(clampDispenserFloat(vy*8000, -32767, 32767))
				e.VelocityZ = int16(clampDispenserFloat(vz*8000, -32767, 32767))
			})
		}
	}
	for _, id := range toRemove {
		delete(ds.projectiles, id)
		if s.entities != nil {
			s.entities.Remove(id)
		}
	}
	ds.mu.Unlock()
	for _, bh := range bellHits {
		if s.RingBellAt(bh.dim, bh.x, bh.y, bh.z, "", bh.projEntity, nil) {
			s.OnVillageBellRung(bh.dim, bh.x, bh.y, bh.z)
		}
	}
	return ticked
}

// TickAllPlayersCampfireContactDamage applies lit campfire contact damage and
// powder snow freezing ticks to all connected players.
func (s *Server) TickAllPlayersCampfireContactDamage() {
	if s == nil {
		return
	}
	s.playersMu.RLock()
	sessions := make([]*PlayerSession, 0, len(s.players))
	for _, sess := range s.players {
		sessions = append(sessions, sess)
	}
	s.playersMu.RUnlock()
	for _, sess := range sessions {
		s.ApplyCampfireContactDamageToPlayer(sess)
		s.StepPlayerPowderSnow(sess)
		s.StepPlayerFluidAndBubbleColumn(sess)
	}
}

// ExplodeBlock triggers a block-category explosion (bed in Nether/End, respawn
// anchor outside Nether) at (x+0.5, y+0.5, z+0.5) in dimension with radius and causesFire.
func (s *Server) ExplodeBlock(dimension string, x, y, z int, radius float64, causesFire bool) ExplosionResult {
	if s == nil {
		return ExplosionResult{}
	}
	return s.DetonateExplosion(ExplosionOptions{
		Dimension:   dimension,
		X:           float64(x) + 0.5,
		Y:           float64(y) + 0.5,
		Z:           float64(z) + 0.5,
		Radius:      radius,
		Category:    ExplosionSourceBlock,
		DamageCause: "bad_respawn_point",
		CausesFire:  causesFire,
	})
}

// TryIgniteBlockOrTNTWithItem handles right-clicking block (x, y, z) on face
// (0..5) with flint_and_steel or fire_charge, priming TNT, lighting campfires/
// candles/portals, or placing fire.
func (s *Server) TryIgniteBlockOrTNTWithItem(dimension string, x, y, z int, face int32, heldStack world.ItemStack, creative bool) (world.ItemStack, bool) {
	if s == nil || heldStack.IsEmpty() {
		return heldStack, false
	}
	faceName := BlockFaceDirectionName(face)
	var dummySess *PlayerSession
	if creative {
		dummySess = &PlayerSession{gameMode: GameModeCreative, health: 20}
	}
	nextStack, _, ok := s.IgniteBlockWithItem(dummySess, dimension, x, y, z, faceName, heldStack, ItemMeta{})
	return nextStack, ok
}

// HandleTNTBlockBreak checks whether breaking a minecraft:tnt block with state
// at (x, y, z) in dimension should prime the TNT instead of dropping an item
// (i.e. when unstable=true and not broken with shears).
func (s *Server) HandleTNTBlockBreak(dimension string, x, y, z int, state uint16, usedShears bool) bool {
	if s == nil || !world.IsTNTBlock(state) {
		return false
	}
	if world.IsUnstableTNTState(state) && !usedShears {
		if s.TNTExplodes() {
			s.SpawnPrimedTNTWithFuse(dimension, float64(x)+0.5, float64(y), float64(z)+0.5, DefaultPrimedTNTFuseTicks, nil)
		}
		return true
	}
	return false
}

// FireContactDamage records fire contact damage applied to a player standing in fire or soul_fire.
type FireContactDamage struct {
	Player       *PlayerSession
	Damage       float32
	SetFireTicks int
}

// CheckPlayersInFire checks all connected players for contact with fire (1.0 damage,
// 160 fire ticks) or soul_fire (2.0 damage, 160 fire ticks) and applies contact damage.
func (s *Server) CheckPlayersInFire() []FireContactDamage {
	if s == nil {
		return nil
	}
	s.playersMu.RLock()
	sessions := make([]*PlayerSession, 0, len(s.players))
	for _, sess := range s.players {
		sessions = append(sessions, sess)
	}
	s.playersMu.RUnlock()

	var out []FireContactDamage
	for _, sess := range sessions {
		if sess == nil || sess.IsDead() {
			continue
		}
		snap := sess.Snapshot()
		if snap.GameMode == GameModeCreative || snap.GameMode == GameModeSpectator || snap.Health <= 0 {
			continue
		}
		dim := world.NormalizeDimension(snap.Dimension)
		cache := s.DimensionChunks(dim)
		if cache == nil {
			continue
		}
		bx, by, bz := int(math.Floor(snap.X)), int(math.Floor(snap.Y)), int(math.Floor(snap.Z))
		if by < world.MinY || by >= world.MinY+world.WorldHeight {
			continue
		}
		st := cache.GetBlock(bx, by, bz)
		if !world.IsFireBlock(st) {
			continue
		}
		rawDmg := world.FireDamageForBlock(st)
		s.ApplyPlayerFireBlockContactDamage(sess)
		if s.PlayerFireTicks(sess) < 160 {
			s.SetPlayerFireTicks(sess, 160)
		}
		out = append(out, FireContactDamage{
			Player:       sess,
			Damage:       rawDmg,
			SetFireTicks: 160,
		})
	}
	return out
}

// TickFire synchronizes fire/TNT/fluid gamerules to dimension caches and processes
// player fire contact damage on each server tick.
func (s *Server) TickFire() {
	if s == nil {
		return
	}
	s.syncTNTAndFireRulesToCaches()
	s.syncFluidConversionRulesToCaches()
	_ = s.CheckPlayersInFire()
}

// BroadcastDimensionBlocks invalidates any affected Portal POIs and broadcasts
// block and lighting updates in dim.
func (s *Server) BroadcastDimensionBlocks(dim string, changes []world.BlockChange, lightChunks []world.ChunkPos) {
	if s == nil || len(changes) == 0 {
		return
	}
	normDim := world.NormalizeDimension(dim)
	cache := s.DimensionChunks(normDim)
	s.InvalidatePortalPOIs(normDim, changes)
	s.broadcastDimensionBlockChanges(normDim, cache, changes, lightChunks)
}

// KillPlayerCommand kills session via /kill (generic_kill), bypassing Creative
// and Spectator invulnerability.
func (s *Server) KillPlayerCommand(session *PlayerSession) bool {
	if s == nil || session == nil || session.IsDead() {
		return false
	}
	session.mu.Lock()
	prevMode := session.gameMode
	if prevMode == GameModeCreative || prevMode == GameModeSpectator {
		session.gameMode = GameModeSurvival
	}
	hp := session.health
	if hp <= 0 {
		hp = 20
		session.health = 20
	}
	session.mu.Unlock()

	killed := s.DamagePlayer(session, hp+1000, "generic_kill")
	if prevMode == GameModeCreative || prevMode == GameModeSpectator {
		session.mu.Lock()
		if !s.IsHardcore() {
			session.gameMode = prevMode
		}
		session.mu.Unlock()
	}
	return killed
}

// KillEntityCommand removes a non-player entity via /kill and broadcasts its removal.
func (s *Server) KillEntityCommand(entityID int32) bool {
	if s == nil || s.entities == nil || entityID <= 0 {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil {
		return false
	}
	dim := ent.EffectiveDimension()
	cp := ent.ChunkPos()
	s.SetMobHealth(entityID, 0)
	s.entities.Remove(entityID)
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayRemoveEntities,
		encodeRemoveEntitiesPacket(nil, []int32{entityID}))
	return true
}

// DamageEntityCommand applies amount damage to a non-player entity via /damage.
func (s *Server) DamageEntityCommand(entityID int32, amount float32) bool {
	if s == nil || s.entities == nil || entityID <= 0 || amount < 0 {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil {
		return false
	}
	dim := ent.EffectiveDimension()
	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayHurtAnimation, EncodeHurtAnimation(entityID, 0))
	curHP := s.MobHealth(entityID)
	if curHP > 0 {
		rem := curHP - amount
		if rem <= 0 {
			s.SetMobHealth(entityID, 0)
			s.entities.Remove(entityID)
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayRemoveEntities,
				encodeRemoveEntitiesPacket(nil, []int32{entityID}))
		} else {
			s.SetMobHealth(entityID, rem)
		}
		return true
	}
	if amount > 0 && ent.IsItem() {
		s.entities.Remove(entityID)
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayRemoveEntities,
			encodeRemoveEntitiesPacket(nil, []int32{entityID}))
	}
	return true
}

// SummonEntityCommand spawns an entity of typeName at (x, y, z) in dim for /summon.
func (s *Server) SummonEntityCommand(dim, typeName string, x, y, z float64) (*world.Entity, error) {
	if s == nil || s.entities == nil {
		return nil, errors.New("entity manager unavailable")
	}
	normDim := world.NormalizeDimension(dim)
	normType := strings.ToLower(strings.TrimSpace(typeName))
	if !strings.Contains(normType, ":") {
		normType = "minecraft:" + normType
	}
	if IsVehicleEntity(normType) {
		_, vEnt := s.SpawnVehicle(normDim, normType, x, y, z, 0)
		if vEnt != nil {
			return vEnt, nil
		}
	}
	if normType == "minecraft:lightning_bolt" {
		boltID, _, _, _, _ := s.SpawnLightningBolt(normDim, x, y, z, nil)
		if ent := s.entities.Get(boltID); ent != nil {
			return ent, nil
		}
	}
	if normType == "minecraft:tnt" {
		tntID := s.SpawnPrimedTNTWithFuse(normDim, x, y, z, DefaultPrimedTNTFuseTicks, nil)
		if ent := s.entities.Get(tntID); ent != nil {
			return ent, nil
		}
	}
	if normType == "minecraft:player" || normType == "minecraft:fishing_bobber" {
		return nil, fmt.Errorf("Unable to summon %s", typeName)
	}
	if mobID, err := s.SpawnMobInDimension(normDim, x, y, z, normType); err == nil && mobID > 0 {
		if ent := s.entities.Get(mobID); ent != nil {
			return ent, nil
		}
	}
	typeID := ResolveEntityTypeID(normType)
	if typeID < 0 {
		return nil, fmt.Errorf("Unknown entity type %q", typeName)
	}
	ent := &world.Entity{
		TypeID:    typeID,
		TypeName:  normType,
		Dimension: normDim,
		X:         x,
		Y:         y,
		Z:         z,
		OnGround:  true,
	}
	id := s.entities.Add(ent)
	s.SetMobHealth(id, 20.0)
	saved := s.entities.Get(id)
	if saved != nil {
		cp := saved.ChunkPos()
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, saved))
	}
	return saved, nil
}

// PlayStopSoundCB is ClientboundStopSoundPacket (0x76 in protocol 775).
const PlayStopSoundCB int32 = 0x76

// PlayTransferCB is ClientboundTransferPacket (0x7f in protocol 775).
const PlayTransferCB int32 = 0x7f

// PlayUpdateAttributesCB is ClientboundUpdateAttributesPacket (0x83 in protocol 775).
const PlayUpdateAttributesCB int32 = 0x83

// ErrPlayerBanned is returned when a banned player attempts to join.
var ErrPlayerBanned = errors.New("server: player is banned from this server")

// ParseSoundSource parses a vanilla sound source name into its SoundSource enum index.
func ParseSoundSource(name string) (int32, bool) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "master":
		return SoundSourceMaster, true
	case "music":
		return SoundSourceMusic, true
	case "record", "records":
		return SoundSourceRecords, true
	case "weather":
		return SoundSourceWeather, true
	case "block", "blocks":
		return SoundSourceBlocks, true
	case "hostile":
		return SoundSourceHostile, true
	case "neutral":
		return SoundSourceNeutral, true
	case "player", "players":
		return SoundSourcePlayers, true
	case "ambient":
		return SoundSourceAmbient, true
	case "voice":
		return SoundSourceVoice, true
	case "ui":
		return SoundSourceUI, true
	default:
		return SoundSourceMaster, false
	}
}

// SendPlaySoundToPlayer sends a ClientboundSoundPacket directly to session.
func (s *Server) SendPlaySoundToPlayer(session *PlayerSession, soundName string, source int32, x, y, z float64, volume, pitch float32, seed int64) {
	if session == nil || session.send == nil {
		return
	}
	name := strings.ToLower(strings.TrimSpace(soundName))
	if !strings.Contains(name, ":") {
		name = "minecraft:" + name
	}
	_ = session.send(PlaySoundPacketID, EncodeSoundPacket(name, source, x, y, z, volume, pitch, seed))
}

// EncodeStopSoundPacket encodes a ClientboundStopSoundPacket (0x76 in protocol 775).
func EncodeStopSoundPacket(hasSource bool, source int32, soundName string) []byte {
	var flags byte
	if hasSource {
		flags |= 0x01
	}
	if soundName != "" {
		flags |= 0x02
	}
	w := protocol.NewWriter(32)
	w.Byte(flags)
	if hasSource {
		w.VarInt(source)
	}
	if soundName != "" {
		name := strings.ToLower(strings.TrimSpace(soundName))
		if !strings.Contains(name, ":") {
			name = "minecraft:" + name
		}
		w.String(name)
	}
	return w.Bytes()
}

// SendStopSoundToPlayer sends a ClientboundStopSoundPacket directly to session.
func (s *Server) SendStopSoundToPlayer(session *PlayerSession, hasSource bool, source int32, soundName string) {
	if session == nil || session.send == nil {
		return
	}
	_ = session.send(PlayStopSoundCB, EncodeStopSoundPacket(hasSource, source, soundName))
}

var vanillaParticleIDs = map[string]int32{
	"minecraft:angry_villager": 0,
	"minecraft:Bubble":         4,
	"minecraft:bubble":         4,
	"minecraft:cloud":          7,
	"minecraft:crit":           8,
	"minecraft:damage_indicator": 9,
	"minecraft:dragon_breath":  10,
	"minecraft:enchant":        17,
	"minecraft:end_rod":        18,
	"minecraft:explosion":      20,
	"minecraft:explosion_emitter": 19,
	"minecraft:firework":       24,
	"minecraft:flame":          26,
	"minecraft:happy_villager": 34,
	"minecraft:heart":          35,
	"minecraft:instant_effect": 36,
	"minecraft:large_smoke":    41,
	"minecraft:lava":           42,
	"minecraft:mycelium":       43,
	"minecraft:nautilus":       44,
	"minecraft:note":           ParticleTypeNoteID,
	"minecraft:poof":           46,
	"minecraft:portal":         47,
	"minecraft:rain":           48,
	"minecraft:smoke":          50,
	"minecraft:white_smoke":    51,
	"minecraft:sneeze":         52,
	"minecraft:spit":           53,
	"minecraft:squid_ink":      54,
	"minecraft:sweep_attack":   55,
	"minecraft:totem_of_undying": 56,
	"minecraft:witch":          60,
	"minecraft:soul_fire_flame": 30,
	"minecraft:soul":           31,
	"minecraft:campfire_cosy_smoke": 64,
	"minecraft:campfire_signal_smoke": 65,
	"minecraft:Ash":            70,
	"minecraft:ash":            70,
	"minecraft:crimson_spore":  71,
	"minecraft:warped_spore":   72,
	"minecraft:reverse_portal": 77,
	"minecraft:white_ash":      78,
	"minecraft:snowflake":      87,
	"minecraft:sonic_boom":     91,
	"minecraft:sculk_soul":     92,
	"minecraft:trial_spawner_detection": 102,
}

// EncodeGenericParticlePacket encodes a ClientboundLevelParticlesPacket (0x2f) for a simple particle.
func EncodeGenericParticlePacket(particleName string, x, y, z float64, dx, dy, dz, speed float32, count int32, force bool) []byte {
	norm := strings.ToLower(strings.TrimSpace(particleName))
	if !strings.Contains(norm, ":") {
		norm = "minecraft:" + norm
	}
	pid, ok := vanillaParticleIDs[norm]
	if !ok {
		pid = 50 // default to minecraft:smoke for unknown simple particle names
	}
	w := protocol.NewWriter(64)
	w.Bool(force)
	w.Bool(force)
	w.Float64(x)
	w.Float64(y)
	w.Float64(z)
	w.Float32(dx)
	w.Float32(dy)
	w.Float32(dz)
	w.Float32(speed)
	w.Int32(count)
	w.VarInt(pid)
	return w.Bytes()
}

// SendParticleToPlayer sends a ClientboundLevelParticlesPacket to session.
func (s *Server) SendParticleToPlayer(session *PlayerSession, particleName string, x, y, z float64, dx, dy, dz, speed float32, count int32, force bool) {
	if session == nil || session.send == nil {
		return
	}
	_ = session.send(PlayLevelParticlesCB, EncodeGenericParticlePacket(particleName, x, y, z, dx, dy, dz, speed, count, force))
}

// BanEntry represents a player or IP ban entry.
type BanEntry struct {
	Target string
	Reason string
}

// AttributeModifier represents an attribute modifier applied via /attribute.
type AttributeModifier struct {
	ID        string
	Amount    float64
	Operation int // 0 = add_value, 1 = add_multiplied_base, 2 = add_multiplied_total
}

type attributeInstance struct {
	Base      float64
	Modifiers map[string]AttributeModifier
}

type serverCommandAuxState struct {
	mu                sync.RWMutex
	playerTags        map[[16]byte]map[string]bool
	entityTags        map[int32]map[string]bool
	playerAttrs       map[[16]byte]map[string]*attributeInstance
	entityAttrs       map[int32]map[string]*attributeInstance
	entityMount       map[int32]int32 // riderEntityID -> vehicleEntityID
	forceLoadedChunks map[string]map[[2]int32]bool
	tickRate          float32
	tickFrozen        bool
	tickStepTicks     int
	tickSprintTicks   int
	bannedPlayers     map[string]BanEntry
	bannedIPs         map[string]BanEntry
	commandStorage    map[string]*nbt.Compound
}

var serverAuxStates sync.Map // map[*Server]*serverCommandAuxState

func (s *Server) cmdAux() *serverCommandAuxState {
	if s == nil {
		return nil
	}
	if v, ok := serverAuxStates.Load(s); ok {
		return v.(*serverCommandAuxState)
	}
	initSt := &serverCommandAuxState{
		playerTags:        make(map[[16]byte]map[string]bool),
		entityTags:        make(map[int32]map[string]bool),
		playerAttrs:       make(map[[16]byte]map[string]*attributeInstance),
		entityAttrs:       make(map[int32]map[string]*attributeInstance),
		entityMount:       make(map[int32]int32),
		forceLoadedChunks: make(map[string]map[[2]int32]bool),
		tickRate:          20.0,
		bannedPlayers:     make(map[string]BanEntry),
		bannedIPs:         make(map[string]BanEntry),
		commandStorage:    make(map[string]*nbt.Compound),
	}
	actual, _ := serverAuxStates.LoadOrStore(s, initSt)
	return actual.(*serverCommandAuxState)
}

// MountEntityCommand mounts a rider (player or entity) onto vehicleEntityID for /ride.
func (s *Server) MountEntityCommand(riderPlayer *PlayerSession, riderEntityID, vehicleEntityID int32) error {
	if s == nil {
		return errors.New("server unavailable")
	}
	if riderEntityID == vehicleEntityID || (riderPlayer != nil && riderPlayer.EntityID == vehicleEntityID) {
		return errors.New("Entity cannot ride itself")
	}
	if riderPlayer != nil {
		riderEntityID = riderPlayer.EntityID
		if _, ok := s.MountPlayerOnVehicle(riderPlayer, vehicleEntityID); ok {
			return nil
		}
	}
	aux := s.cmdAux()
	aux.mu.Lock()
	// Check cycle
	cur := vehicleEntityID
	for cur != 0 {
		if cur == riderEntityID {
			aux.mu.Unlock()
			return errors.New("Cannot mount entity in a riding loop")
		}
		cur = aux.entityMount[cur]
	}
	aux.entityMount[riderEntityID] = vehicleEntityID
	var passengers []int32
	for r, v := range aux.entityMount {
		if v == vehicleEntityID {
			passengers = append(passengers, r)
		}
	}
	aux.mu.Unlock()

	dim := world.DimensionOverworld
	var cx, cz int32
	if ent := s.entities.Get(vehicleEntityID); ent != nil {
		dim = ent.EffectiveDimension()
		cp := ent.ChunkPos()
		cx, cz = cp.X, cp.Z
	} else if riderPlayer != nil {
		snap := riderPlayer.Snapshot()
		dim = snap.Dimension
		cx = int32(int64(math.Floor(snap.X)) >> 4)
		cz = int32(int64(math.Floor(snap.Z)) >> 4)
	}
	s.BroadcastDimensionChunk(dim, cx, cz, protocol.PlaySetPassengers, EncodeSetPassengersPacket(vehicleEntityID, passengers))
	return nil
}

// DismountEntityCommand dismounts a rider (player or entity) from its current vehicle for /ride.
func (s *Server) DismountEntityCommand(riderPlayer *PlayerSession, riderEntityID int32) bool {
	if s == nil {
		return false
	}
	if riderPlayer != nil {
		riderEntityID = riderPlayer.EntityID
		if s.DismountPlayerVehicle(riderPlayer) {
			return true
		}
	}
	aux := s.cmdAux()
	aux.mu.Lock()
	vehicleID, ok := aux.entityMount[riderEntityID]
	if ok {
		delete(aux.entityMount, riderEntityID)
	}
	var passengers []int32
	if ok {
		for r, v := range aux.entityMount {
			if v == vehicleID {
				passengers = append(passengers, r)
			}
		}
	}
	aux.mu.Unlock()
	if !ok {
		return false
	}
	dim := world.DimensionOverworld
	var cx, cz int32
	if ent := s.entities.Get(vehicleID); ent != nil {
		dim = ent.EffectiveDimension()
		cp := ent.ChunkPos()
		cx, cz = cp.X, cp.Z
	} else if riderPlayer != nil {
		snap := riderPlayer.Snapshot()
		dim = snap.Dimension
		cx = int32(int64(math.Floor(snap.X)) >> 4)
		cz = int32(int64(math.Floor(snap.Z)) >> 4)
	}
	s.BroadcastDimensionChunk(dim, cx, cz, protocol.PlaySetPassengers, EncodeSetPassengersPacket(vehicleID, passengers))
	return true
}

// MountedVehicleOfEntity returns the vehicle entity ID currently ridden by riderPlayer / riderEntityID.
func (s *Server) MountedVehicleOfEntity(riderPlayer *PlayerSession, riderEntityID int32) int32 {
	if s == nil {
		return 0
	}
	if riderPlayer != nil {
		riderEntityID = riderPlayer.EntityID
		if v := s.PlayerMountedVehicle(riderPlayer); v > 0 {
			return v
		}
		if v := s.PlayerMountedEquine(riderPlayer); v > 0 {
			return v
		}
		if v := s.PlayerMountedCamel(riderPlayer); v > 0 {
			return v
		}
	}
	aux := s.cmdAux()
	aux.mu.RLock()
	v := aux.entityMount[riderEntityID]
	aux.mu.RUnlock()
	return v
}

// PassengersOfEntity returns all passenger entity IDs currently riding vehicleEntityID.
func (s *Server) PassengersOfEntity(vehicleEntityID int32) []int32 {
	if s == nil || vehicleEntityID <= 0 {
		return nil
	}
	seen := make(map[int32]bool)
	var out []int32
	for _, id := range s.VehiclePassengers(vehicleEntityID) {
		if id > 0 && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	aux := s.cmdAux()
	aux.mu.RLock()
	for r, v := range aux.entityMount {
		if v == vehicleEntityID && r > 0 && !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	aux.mu.RUnlock()
	return out
}

// AddPlayerTag adds a scoreboard tag to session, returning false if already present.
func (s *Server) AddPlayerTag(session *PlayerSession, tag string) bool {
	if s == nil || session == nil || tag == "" {
		return false
	}
	aux := s.cmdAux()
	aux.mu.Lock()
	defer aux.mu.Unlock()
	m := aux.playerTags[session.Profile.UUID]
	if m == nil {
		m = make(map[string]bool)
		aux.playerTags[session.Profile.UUID] = m
	}
	if m[tag] {
		return false
	}
	m[tag] = true
	return true
}

// RemovePlayerTag removes a scoreboard tag from session.
func (s *Server) RemovePlayerTag(session *PlayerSession, tag string) bool {
	if s == nil || session == nil || tag == "" {
		return false
	}
	aux := s.cmdAux()
	aux.mu.Lock()
	defer aux.mu.Unlock()
	m := aux.playerTags[session.Profile.UUID]
	if m == nil || !m[tag] {
		return false
	}
	delete(m, tag)
	return true
}

// PlayerTags returns a sorted list of tags on session.
func (s *Server) PlayerTags(session *PlayerSession) []string {
	if s == nil || session == nil {
		return nil
	}
	aux := s.cmdAux()
	aux.mu.RLock()
	defer aux.mu.RUnlock()
	m := aux.playerTags[session.Profile.UUID]
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// HasPlayerTag reports whether session has the specified scoreboard tag.
func (s *Server) HasPlayerTag(session *PlayerSession, tag string) bool {
	if s == nil || session == nil {
		return false
	}
	aux := s.cmdAux()
	aux.mu.RLock()
	defer aux.mu.RUnlock()
	m := aux.playerTags[session.Profile.UUID]
	if tag == "" {
		return len(m) == 0
	}
	return m != nil && m[tag]
}

// AddEntityTag adds a scoreboard tag to entityID.
func (s *Server) AddEntityTag(entityID int32, tag string) bool {
	if s == nil || entityID <= 0 || tag == "" {
		return false
	}
	aux := s.cmdAux()
	aux.mu.Lock()
	defer aux.mu.Unlock()
	m := aux.entityTags[entityID]
	if m == nil {
		m = make(map[string]bool)
		aux.entityTags[entityID] = m
	}
	if m[tag] {
		return false
	}
	m[tag] = true
	return true
}

// RemoveEntityTag removes a scoreboard tag from entityID.
func (s *Server) RemoveEntityTag(entityID int32, tag string) bool {
	if s == nil || entityID <= 0 || tag == "" {
		return false
	}
	aux := s.cmdAux()
	aux.mu.Lock()
	defer aux.mu.Unlock()
	m := aux.entityTags[entityID]
	if m == nil || !m[tag] {
		return false
	}
	delete(m, tag)
	return true
}

// EntityTags returns all scoreboard tags on entityID.
func (s *Server) EntityTags(entityID int32) []string {
	if s == nil || entityID <= 0 {
		return nil
	}
	aux := s.cmdAux()
	aux.mu.RLock()
	defer aux.mu.RUnlock()
	m := aux.entityTags[entityID]
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// HasEntityTag reports whether entityID has the specified scoreboard tag.
func (s *Server) HasEntityTag(entityID int32, tag string) bool {
	if s == nil || entityID <= 0 {
		return false
	}
	aux := s.cmdAux()
	aux.mu.RLock()
	defer aux.mu.RUnlock()
	m := aux.entityTags[entityID]
	if tag == "" {
		return len(m) == 0
	}
	return m != nil && m[tag]
}

var defaultAttributeBases = map[string]float64{
	"minecraft:max_health":               20.0,
	"minecraft:follow_range":             32.0,
	"minecraft:knockback_resistance":     0.0,
	"minecraft:movement_speed":           0.1,
	"minecraft:flying_speed":             0.4,
	"minecraft:attack_damage":            1.0,
	"minecraft:attack_knockback":         0.0,
	"minecraft:attack_speed":             4.0,
	"minecraft:armor":                    0.0,
	"minecraft:armor_toughness":          0.0,
	"minecraft:fall_damage_multiplier":   1.0,
	"minecraft:luck":                     0.0,
	"minecraft:max_absorption":           0.0,
	"minecraft:safe_fall_distance":       3.0,
	"minecraft:scale":                    1.0,
	"minecraft:step_height":              0.6,
	"minecraft:gravity":                  0.08,
	"minecraft:jump_strength":            0.42,
	"minecraft:burning_time":             1.0,
	"minecraft:explosion_knockback_resistance": 0.0,
	"minecraft:movement_efficiency":      0.0,
	"minecraft:oxygen_bonus":             0.0,
	"minecraft:water_movement_efficiency": 0.0,
	"minecraft:block_interaction_range":  4.5,
	"minecraft:entity_interaction_range": 3.0,
	"minecraft:block_break_speed":        1.0,
	"minecraft:mining_efficiency":        0.0,
	"minecraft:sneaking_speed":           0.3,
	"minecraft:submerged_mining_speed":   0.2,
	"minecraft:sweeping_damage_ratio":    0.0,
}

// CanonicalAttributeName normalizes an attribute identifier (supporting both
// "minecraft:generic.max_health" and 26.1.2 "minecraft:max_health").
func CanonicalAttributeName(attr string) (string, float64, bool) {
	a := strings.ToLower(strings.TrimSpace(attr))
	if !strings.Contains(a, ":") {
		a = "minecraft:" + a
	}
	a = strings.Replace(a, "minecraft:generic.", "minecraft:", 1)
	a = strings.Replace(a, "minecraft:player.", "minecraft:", 1)
	a = strings.Replace(a, "minecraft:horse.", "minecraft:", 1)
	base, ok := defaultAttributeBases[a]
	return a, base, ok
}

func (s *Server) getOrCreateAttrLocked(aux *serverCommandAuxState, session *PlayerSession, entityID int32, canonAttr string, defaultBase float64) *attributeInstance {
	if session != nil {
		m := aux.playerAttrs[session.Profile.UUID]
		if m == nil {
			m = make(map[string]*attributeInstance)
			aux.playerAttrs[session.Profile.UUID] = m
		}
		inst := m[canonAttr]
		if inst == nil {
			inst = &attributeInstance{Base: defaultBase, Modifiers: make(map[string]AttributeModifier)}
			m[canonAttr] = inst
		}
		return inst
	}
	m := aux.entityAttrs[entityID]
	if m == nil {
		m = make(map[string]*attributeInstance)
		aux.entityAttrs[entityID] = m
	}
	inst := m[canonAttr]
	if inst == nil {
		if canonAttr == "minecraft:max_health" {
			if hp := s.MobHealth(entityID); hp > 0 {
				defaultBase = float64(hp)
			}
		}
		inst = &attributeInstance{Base: defaultBase, Modifiers: make(map[string]AttributeModifier)}
		m[canonAttr] = inst
	}
	return inst
}

func computeAttributeTotal(inst *attributeInstance) float64 {
	if inst == nil {
		return 0
	}
	v := inst.Base
	for _, mod := range inst.Modifiers {
		if mod.Operation == 0 {
			v += mod.Amount
		}
	}
	multBase := 0.0
	for _, mod := range inst.Modifiers {
		if mod.Operation == 1 {
			multBase += mod.Amount
		}
	}
	v *= (1.0 + multBase)
	for _, mod := range inst.Modifiers {
		if mod.Operation == 2 {
			v *= (1.0 + mod.Amount)
		}
	}
	return v
}

// GetTargetAttributeBase returns the base value of attr on session or entityID.
func (s *Server) GetTargetAttributeBase(session *PlayerSession, entityID int32, attr string) (float64, error) {
	canon, defBase, ok := CanonicalAttributeName(attr)
	if !ok {
		return 0, fmt.Errorf("Unknown attribute %q", attr)
	}
	aux := s.cmdAux()
	aux.mu.Lock()
	defer aux.mu.Unlock()
	inst := s.getOrCreateAttrLocked(aux, session, entityID, canon, defBase)
	return inst.Base, nil
}

// SetTargetAttributeBase sets the base value of attr on session or entityID.
func (s *Server) SetTargetAttributeBase(session *PlayerSession, entityID int32, attr string, base float64) error {
	canon, defBase, ok := CanonicalAttributeName(attr)
	if !ok {
		return fmt.Errorf("Unknown attribute %q", attr)
	}
	aux := s.cmdAux()
	aux.mu.Lock()
	inst := s.getOrCreateAttrLocked(aux, session, entityID, canon, defBase)
	inst.Base = base
	total := computeAttributeTotal(inst)
	aux.mu.Unlock()
	s.syncAttributeChange(session, entityID, canon, total)
	return nil
}

// ResetTargetAttributeBase resets the base value of attr to its vanilla default.
func (s *Server) ResetTargetAttributeBase(session *PlayerSession, entityID int32, attr string) (float64, error) {
	canon, defBase, ok := CanonicalAttributeName(attr)
	if !ok {
		return 0, fmt.Errorf("Unknown attribute %q", attr)
	}
	_ = s.SetTargetAttributeBase(session, entityID, canon, defBase)
	return defBase, nil
}

// GetTargetAttributeTotal returns the computed total value of attr (including modifiers).
func (s *Server) GetTargetAttributeTotal(session *PlayerSession, entityID int32, attr string) (float64, error) {
	canon, defBase, ok := CanonicalAttributeName(attr)
	if !ok {
		return 0, fmt.Errorf("Unknown attribute %q", attr)
	}
	aux := s.cmdAux()
	aux.mu.Lock()
	defer aux.mu.Unlock()
	inst := s.getOrCreateAttrLocked(aux, session, entityID, canon, defBase)
	return computeAttributeTotal(inst), nil
}

// AddTargetAttributeModifier adds a modifier to attr on session or entityID.
func (s *Server) AddTargetAttributeModifier(session *PlayerSession, entityID int32, attr, modID string, amount float64, op int) error {
	canon, defBase, ok := CanonicalAttributeName(attr)
	if !ok {
		return fmt.Errorf("Unknown attribute %q", attr)
	}
	normID := strings.ToLower(strings.TrimSpace(modID))
	if !strings.Contains(normID, ":") {
		normID = "minecraft:" + normID
	}
	aux := s.cmdAux()
	aux.mu.Lock()
	inst := s.getOrCreateAttrLocked(aux, session, entityID, canon, defBase)
	if _, exists := inst.Modifiers[normID]; exists {
		aux.mu.Unlock()
		return fmt.Errorf("Modifier %s is already present on attribute %s", normID, canon)
	}
	inst.Modifiers[normID] = AttributeModifier{ID: normID, Amount: amount, Operation: op}
	total := computeAttributeTotal(inst)
	aux.mu.Unlock()
	s.syncAttributeChange(session, entityID, canon, total)
	return nil
}

// RemoveTargetAttributeModifier removes a modifier from attr on session or entityID.
func (s *Server) RemoveTargetAttributeModifier(session *PlayerSession, entityID int32, attr, modID string) (bool, error) {
	canon, defBase, ok := CanonicalAttributeName(attr)
	if !ok {
		return false, fmt.Errorf("Unknown attribute %q", attr)
	}
	normID := strings.ToLower(strings.TrimSpace(modID))
	if !strings.Contains(normID, ":") {
		normID = "minecraft:" + normID
	}
	aux := s.cmdAux()
	aux.mu.Lock()
	inst := s.getOrCreateAttrLocked(aux, session, entityID, canon, defBase)
	if _, exists := inst.Modifiers[normID]; !exists {
		aux.mu.Unlock()
		return false, nil
	}
	delete(inst.Modifiers, normID)
	total := computeAttributeTotal(inst)
	aux.mu.Unlock()
	s.syncAttributeChange(session, entityID, canon, total)
	return true, nil
}

// GetTargetAttributeModifierValue returns the amount of a modifier on attr.
func (s *Server) GetTargetAttributeModifierValue(session *PlayerSession, entityID int32, attr, modID string) (float64, bool, error) {
	canon, defBase, ok := CanonicalAttributeName(attr)
	if !ok {
		return 0, false, fmt.Errorf("Unknown attribute %q", attr)
	}
	normID := strings.ToLower(strings.TrimSpace(modID))
	if !strings.Contains(normID, ":") {
		normID = "minecraft:" + normID
	}
	aux := s.cmdAux()
	aux.mu.Lock()
	defer aux.mu.Unlock()
	inst := s.getOrCreateAttrLocked(aux, session, entityID, canon, defBase)
	mod, exists := inst.Modifiers[normID]
	if !exists {
		return 0, false, nil
	}
	return mod.Amount, true, nil
}

func (s *Server) syncAttributeChange(session *PlayerSession, entityID int32, canonAttr string, total float64) {
	if canonAttr == "minecraft:max_health" {
		if session != nil {
			snap := session.Snapshot()
			if float64(snap.Health) > total && total > 0 {
				s.SetPlayerHealth(session, float32(total), snap.FoodLevel, snap.FoodSaturation)
				if session.send != nil {
					w := protocol.NewWriter(16)
					w.Float32(float32(total))
					w.VarInt(snap.FoodLevel)
					w.Float32(snap.FoodSaturation)
					_ = session.send(protocol.PlaySetHealth, w.Bytes())
				}
			}
		} else if entityID > 0 && total > 0 {
			if cur := s.MobHealth(entityID); float64(cur) > total {
				s.SetMobHealth(entityID, float32(total))
			}
		}
	}
}

// SetChunkForceLoaded sets or clears the force-loaded status of chunk (cx, cz) in dim.
func (s *Server) SetChunkForceLoaded(dim string, cx, cz int32, forced bool) bool {
	if s == nil {
		return false
	}
	normDim := world.NormalizeDimension(dim)
	aux := s.cmdAux()
	aux.mu.Lock()
	m := aux.forceLoadedChunks[normDim]
	if m == nil {
		m = make(map[[2]int32]bool)
		aux.forceLoadedChunks[normDim] = m
	}
	key := [2]int32{cx, cz}
	changed := false
	if forced {
		if !m[key] {
			m[key] = true
			changed = true
		}
	} else {
		if m[key] {
			delete(m, key)
			changed = true
		}
	}
	aux.mu.Unlock()
	if forced {
		if cache := s.DimensionChunks(normDim); cache != nil {
			_ = cache.GetBlock(int(cx)*16, 64, int(cz)*16)
		}
	}
	return changed
}

// IsChunkForceLoaded reports whether chunk (cx, cz) in dim is force-loaded.
func (s *Server) IsChunkForceLoaded(dim string, cx, cz int32) bool {
	if s == nil {
		return false
	}
	normDim := world.NormalizeDimension(dim)
	aux := s.cmdAux()
	aux.mu.RLock()
	defer aux.mu.RUnlock()
	m := aux.forceLoadedChunks[normDim]
	return m != nil && m[[2]int32{cx, cz}]
}

// ForceLoadedChunks returns all force-loaded chunk coordinates in dim.
func (s *Server) ForceLoadedChunks(dim string) [][2]int32 {
	if s == nil {
		return nil
	}
	normDim := world.NormalizeDimension(dim)
	aux := s.cmdAux()
	aux.mu.RLock()
	defer aux.mu.RUnlock()
	m := aux.forceLoadedChunks[normDim]
	out := make([][2]int32, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// ClearForceLoadedChunks removes all force-loaded chunks in dim.
func (s *Server) ClearForceLoadedChunks(dim string) int {
	if s == nil {
		return 0
	}
	normDim := world.NormalizeDimension(dim)
	aux := s.cmdAux()
	aux.mu.Lock()
	defer aux.mu.Unlock()
	n := len(aux.forceLoadedChunks[normDim])
	delete(aux.forceLoadedChunks, normDim)
	return n
}

// TickState returns the current tick rate, frozen status, step ticks, and sprint ticks.
func (s *Server) TickState() (rate float32, frozen bool, stepTicks, sprintTicks int) {
	if s == nil {
		return 20.0, false, 0, 0
	}
	aux := s.cmdAux()
	aux.mu.RLock()
	defer aux.mu.RUnlock()
	return aux.tickRate, aux.tickFrozen, aux.tickStepTicks, aux.tickSprintTicks
}

// SetTickRate updates the target server tick rate (1..10000 TPS).
func (s *Server) SetTickRate(rate float32) {
	if s == nil {
		return
	}
	if rate < 1.0 {
		rate = 1.0
	}
	if rate > 10000.0 {
		rate = 10000.0
	}
	aux := s.cmdAux()
	aux.mu.Lock()
	aux.tickRate = rate
	aux.mu.Unlock()
}

// SetTickFrozen sets whether the server tick loop is frozen.
func (s *Server) SetTickFrozen(frozen bool) {
	if s == nil {
		return
	}
	aux := s.cmdAux()
	aux.mu.Lock()
	aux.tickFrozen = frozen
	if !frozen {
		aux.tickStepTicks = 0
	}
	aux.mu.Unlock()
}

// StepServerTicks schedules ticks to advance while frozen (or stops stepping when ticks == 0).
func (s *Server) StepServerTicks(ticks int) bool {
	if s == nil {
		return false
	}
	aux := s.cmdAux()
	aux.mu.Lock()
	defer aux.mu.Unlock()
	if ticks > 0 && !aux.tickFrozen {
		return false
	}
	aux.tickStepTicks = ticks
	return true
}

// SprintServerTicks schedules ticks to sprint at maximum speed (or stops sprinting when ticks == 0).
func (s *Server) SprintServerTicks(ticks int) (wasActive bool) {
	if s == nil {
		return false
	}
	aux := s.cmdAux()
	aux.mu.Lock()
	defer aux.mu.Unlock()
	wasActive = aux.tickSprintTicks > 0
	aux.tickSprintTicks = ticks
	return wasActive
}

// KickPlayer disconnects session with aPlayDisconnect packet and unregisters them.
func (s *Server) KickPlayer(session *PlayerSession, reason string) {
	if s == nil || session == nil {
		return
	}
	if reason == "" {
		reason = "Kicked by an operator"
	}
	if session.send != nil {
		w := protocol.NewWriter(len(reason) + 8)
		w.Raw(nbt.Marshal(nbt.String(reason)))
		_ = session.send(protocol.PlayDisconnect, w.Bytes())
	}
	s.UnregisterPlayer(session)
}

// BanPlayer adds name to the banned players list and kicks them if online.
func (s *Server) BanPlayer(name, reason string) bool {
	if s == nil {
		return false
	}
	key := strings.ToLower(strings.TrimSpace(name))
	if key == "" {
		return false
	}
	if reason == "" {
		reason = "Banned by an operator."
	}
	aux := s.cmdAux()
	aux.mu.Lock()
	if _, exists := aux.bannedPlayers[key]; exists {
		aux.mu.Unlock()
		return false
	}
	aux.bannedPlayers[key] = BanEntry{Target: strings.TrimSpace(name), Reason: reason}
	aux.mu.Unlock()
	if online := s.PlayerByName(name); online != nil {
		s.KickPlayer(online, "You are banned from this server. Reason: "+reason)
	}
	return true
}

// PardonPlayer removes name from the banned players list.
func (s *Server) PardonPlayer(name string) bool {
	if s == nil {
		return false
	}
	key := strings.ToLower(strings.TrimSpace(name))
	aux := s.cmdAux()
	aux.mu.Lock()
	defer aux.mu.Unlock()
	if _, exists := aux.bannedPlayers[key]; !exists {
		return false
	}
	delete(aux.bannedPlayers, key)
	return true
}

// BannedPlayers returns all active player bans.
func (s *Server) BannedPlayers() []BanEntry {
	if s == nil {
		return nil
	}
	aux := s.cmdAux()
	aux.mu.RLock()
	defer aux.mu.RUnlock()
	out := make([]BanEntry, 0, len(aux.bannedPlayers))
	for _, b := range aux.bannedPlayers {
		out = append(out, b)
	}
	return out
}

// BanIP adds ip to the banned IP list.
func (s *Server) BanIP(ip, reason string) bool {
	if s == nil {
		return false
	}
	key := strings.ToLower(strings.TrimSpace(ip))
	if key == "" {
		return false
	}
	if reason == "" {
		reason = "Banned by an operator."
	}
	aux := s.cmdAux()
	aux.mu.Lock()
	defer aux.mu.Unlock()
	if _, exists := aux.bannedIPs[key]; exists {
		return false
	}
	aux.bannedIPs[key] = BanEntry{Target: strings.TrimSpace(ip), Reason: reason}
	return true
}

// PardonIP removes ip from the banned IP list.
func (s *Server) PardonIP(ip string) bool {
	if s == nil {
		return false
	}
	key := strings.ToLower(strings.TrimSpace(ip))
	aux := s.cmdAux()
	aux.mu.Lock()
	defer aux.mu.Unlock()
	if _, exists := aux.bannedIPs[key]; !exists {
		return false
	}
	delete(aux.bannedIPs, key)
	return true
}

// BannedIPs returns all active IP bans.
func (s *Server) BannedIPs() []BanEntry {
	if s == nil {
		return nil
	}
	aux := s.cmdAux()
	aux.mu.RLock()
	defer aux.mu.RUnlock()
	out := make([]BanEntry, 0, len(aux.bannedIPs))
	for _, b := range aux.bannedIPs {
		out = append(out, b)
	}
	return out
}

// TransferPlayer sends a ClientboundTransferPacket (0x7f) to session.
func (s *Server) TransferPlayer(session *PlayerSession, host string, port int) {
	if session == nil || session.send == nil {
		return
	}
	w := protocol.NewWriter(len(host) + 8)
	w.String(host)
	w.VarInt(int32(port))
	_ = session.send(PlayTransferCB, w.Bytes())
}

// CommandStorage returns or initializes the NBT compound for command storage id.
func (s *Server) CommandStorage(id string) *nbt.Compound {
	if s == nil {
		return nbt.NewCompound()
	}
	norm := strings.ToLower(strings.TrimSpace(id))
	if !strings.Contains(norm, ":") {
		norm = "minecraft:" + norm
	}
	aux := s.cmdAux()
	aux.mu.Lock()
	defer aux.mu.Unlock()
	c := aux.commandStorage[norm]
	if c == nil {
		c = nbt.NewCompound()
		aux.commandStorage[norm] = c
	}
	return c
}

// DeleteCommandStorageKey removes key from the command storage compound id.
func (s *Server) DeleteCommandStorageKey(id, key string) bool {
	if s == nil {
		return false
	}
	norm := strings.ToLower(strings.TrimSpace(id))
	if !strings.Contains(norm, ":") {
		norm = "minecraft:" + norm
	}
	aux := s.cmdAux()
	aux.mu.Lock()
	defer aux.mu.Unlock()
	c := aux.commandStorage[norm]
	if c == nil {
		return false
	}
	if _, ok := c.Get(key); !ok {
		return false
	}
	next := nbt.NewCompound()
	for _, k := range c.Keys() {
		if k == key {
			continue
		}
		if v, ok := c.Get(k); ok {
			next.Set(k, v)
		}
	}
	aux.commandStorage[norm] = next
	return true
}

func (s *Server) broadcastBlockUpdate(x, y, z int, state uint16) {
	s.broadcastBlockUpdateInDim(world.DimensionOverworld, x, y, z, state)
}

// ApplyPlayerFrostWalker freezes exposed source water around (blockX, blockY, blockZ)
// into frosted_ice[age=0] when session is wearing boots enchanted with frost_walker.
func (s *Server) ApplyPlayerFrostWalker(session *PlayerSession, blockX, blockY, blockZ int) int {
	if s == nil || session == nil || session.IsDead() {
		return 0
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator || !snap.OnGround {
		return 0
	}
	boots := session.InventorySlot(ArmorSlotFeet)
	if boots.IsEmpty() {
		return 0
	}
	lvl := int(GetSlotItemMeta(session, ArmorSlotFeet).EnchantmentLevel("minecraft:frost_walker"))
	if lvl <= 0 {
		return 0
	}
	dim := world.NormalizeDimension(snap.Dimension)
	cache := s.DimensionChunks(dim)
	if cache == nil {
		return 0
	}
	placed, upd := cache.ApplyFrostWalkerAround(blockX, blockY, blockZ, lvl)
	if placed > 0 {
		s.broadcastDimensionBlockChanges(dim, cache, upd.Changes, upd.LightChanged)
	}
	return placed
}

// CanPlayerWalkOnPowderSnow reports whether session is wearing leather_boots
// and can walk on top of powder_snow without sinking.
func (s *Server) CanPlayerWalkOnPowderSnow(session *PlayerSession) bool {
	if session == nil {
		return false
	}
	return world.CanEntityWalkOnPowderSnow(session.InventorySlot(ArmorSlotFeet).ResolvedID())
}

// PlayerTicksFrozen returns session's current powder_snow freeze ticks (0..140).
func (s *Server) PlayerTicksFrozen(session *PlayerSession) int {
	if session == nil {
		return 0
	}
	st := getSurvivalState(session)
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.ticksFrozen
}

// SetPlayerTicksFrozen sets session's powder_snow freeze ticks (clamped to 0..140).
func (s *Server) SetPlayerTicksFrozen(session *PlayerSession, ticks int) {
	if session == nil {
		return
	}
	if ticks < 0 {
		ticks = 0
	} else if ticks > world.PowderSnowMinFreezeTicks {
		ticks = world.PowderSnowMinFreezeTicks
	}
	st := getSurvivalState(session)
	st.mu.Lock()
	st.ticksFrozen = ticks
	st.mu.Unlock()
}

// StepPlayerPowderSnow advances session's powder_snow freezing counter by 1 tick
// (or cools down by 2 ticks outside powder_snow / with leather armor), melts
// powder_snow when the player is burning, and deals 1.0 freeze damage every 40 ticks when fully frozen.
func (s *Server) StepPlayerPowderSnow(session *PlayerSession) (ticksFrozen int, fullyFrozen bool) {
	if s == nil || session == nil || session.IsDead() {
		return 0, false
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator {
		return 0, false
	}
	dim := world.NormalizeDimension(snap.Dimension)
	cache := s.DimensionChunks(dim)
	if cache == nil {
		return 0, false
	}
	bx, by, bz := int(math.Floor(snap.X)), int(math.Floor(snap.Y)), int(math.Floor(snap.Z))
	inPowderSnow := false
	if by >= world.MinY && by < world.MinY+world.WorldHeight {
		inPowderSnow = world.IsPowderSnowBlock(cache.GetBlock(bx, by, bz))
		if !inPowderSnow && by+1 < world.MinY+world.WorldHeight {
			inPowderSnow = world.IsPowderSnowBlock(cache.GetBlock(bx, by+1, bz))
		}
	}
	if inPowderSnow && s.PlayerFireTicks(session) > 0 {
		if melted, upd := cache.ExtinguishBurningEntityInPowderSnowAt(bx, by, bz, true, true); melted {
			s.SetPlayerFireTicks(session, 0)
			s.broadcastDimensionBlockChanges(dim, cache, upd.Changes, upd.LightChanged)
			inPowderSnow = false
		}
	}

	bootsID := session.InventorySlot(ArmorSlotFeet).ResolvedID()
	_ = world.CanEntityWalkOnPowderSnow(bootsID)
	hasLeatherArmor := world.IsEntityFreezeImmuneByArmor(
		session.InventorySlot(ArmorSlotHead).ResolvedID(),
		session.InventorySlot(ArmorSlotHead+1).ResolvedID(),
		session.InventorySlot(ArmorSlotHead+2).ResolvedID(),
		bootsID,
	)

	st := getSurvivalState(session)
	st.mu.Lock()
	nextFrozen, frozen := world.StepPowderSnowFreezing(inPowderSnow, hasLeatherArmor, st.ticksFrozen)
	st.ticksFrozen = nextFrozen
	shouldDamage := false
	if frozen && snap.GameMode != GameModeCreative {
		st.freezeTickCounter++
		if st.freezeTickCounter >= world.PowderSnowFreezeDamageIntervalTicks {
			st.freezeTickCounter = 0
			shouldDamage = true
		}
	} else {
		st.freezeTickCounter = 0
	}
	st.mu.Unlock()

	if shouldDamage {
		s.DamagePlayer(session, world.PowderSnowFreezeDamage, "freeze")
	}
	return nextFrozen, frozen
}

// DropSelectedItem drops 1 item (dropAll == false, Q) or the entire held stack
// (dropAll == true, Ctrl+Q) from session's selected hotbar slot into the world
// in front of the player, preserving metadata and recording drop statistics.
func (s *Server) DropSelectedItem(session *PlayerSession, dropAll bool) (int32, world.ItemStack) {
	if s == nil || session == nil || session.IsDead() {
		return 0, world.ItemStack{}
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator {
		return 0, world.ItemStack{}
	}
	invSlot := session.HeldInventorySlot()
	if invSlot < 0 || invSlot >= world.PlayerInventorySlots {
		return 0, world.ItemStack{}
	}
	cur := session.InventorySlot(invSlot)
	if cur.IsEmpty() {
		return 0, world.ItemStack{}
	}
	dropCount := int32(1)
	if dropAll || cur.Count <= 1 {
		dropCount = cur.Count
	}
	dropped := world.NewItemStack(cur.ResolvedItemID(), dropCount)
	slotHornInst := GetSlotGoatHornInstrument(session, invSlot)
	slotMapID, hasSlotMapID := GetSlotMapID(session, invSlot)
	slotBundle := GetSlotBundleContents(session, invSlot)
	slotMeta := GetSlotItemMeta(session, invSlot)
	s.StopUsingSpyglass(session)

	rem := cur.Count - dropCount
	if rem <= 0 {
		session.SetInventorySlot(invSlot, world.ItemStack{})
		SetSlotItemMeta(session, invSlot, ItemMeta{})
		SetSlotPotionType(session, invSlot, "")
		SetSlotArmorTrim(session, invSlot, ArmorTrim{})
		ClearSlotBannerMeta(session, invSlot)
		ClearSlotGoatHornInstrument(session, invSlot)
		ClearSlotMapID(session, invSlot)
		ClearSlotBundleContents(session, invSlot)
		ClearSlotWritableBook(session, invSlot)
		ClearSlotWrittenBook(session, invSlot)
	} else {
		session.SetInventorySlot(invSlot, world.NewItemStack(cur.ResolvedItemID(), rem))
	}

	if session.send != nil {
		_ = session.send(protocol.PlayContainerSetSlot, EncodeContainerSlot(0, session.NextContainerStateID(), invSlot, session.InventorySlot(invSlot)))
	}
	s.TriggerInventoryChanged(session)

	yawRad := float64(snap.Yaw) * (math.Pi / 180.0)
	pitchRad := float64(snap.Pitch) * (math.Pi / 180.0)
	dirX := -math.Sin(yawRad) * math.Cos(pitchRad)
	dirZ := math.Cos(yawRad) * math.Cos(pitchRad)
	spawnX := snap.X + dirX*0.3
	spawnY := snap.Y + 1.3
	spawnZ := snap.Z + dirZ*0.3

	dim := world.NormalizeDimension(snap.Dimension)
	entID, _ := s.SpawnItemEntityInDimension(dim, spawnX, spawnY, spawnZ, dropped)
	if entID > 0 {
		if !slotMeta.IsEmpty() {
			ft := s.fishingTracker()
			ft.mu.Lock()
			ft.droppedMeta[entID] = slotMeta.Clone()
			ft.mu.Unlock()
		}
		if slotHornInst != "" && canonicalItemName(dropped.ResolvedID()) == GoatHornItemID {
			s.AssociateDroppedGoatHornEntity(entID, slotHornInst)
		}
		if hasSlotMapID && canonicalItemName(dropped.ResolvedID()) == FilledMapItemID {
			s.AssociateDroppedMapEntity(entID, slotMapID)
		}
		if len(slotBundle) > 0 && IsBundleItem(dropped.ResolvedID()) {
			s.AssociateDroppedBundleEntity(entID, slotBundle)
		}
	}
	s.RecordItemDropped(session, dropped.ResolvedID(), dropped.Count)
	return entID, dropped
}

// SwapHeldWithOffhand swaps the player's currently selected hotbar slot with
// OffHandContainerSlot (slot 45), preserving all item metadata/components and
// broadcasting updated equipment to nearby players.
func (s *Server) SwapHeldWithOffhand(session *PlayerSession) bool {
	if s == nil || session == nil || session.IsDead() {
		return false
	}
	if session.Snapshot().GameMode == GameModeSpectator {
		return false
	}
	mainSlot := session.HeldInventorySlot()
	if mainSlot < 0 || mainSlot >= world.PlayerInventorySlots {
		return false
	}
	offSlot := OffHandContainerSlot
	mainStack := session.InventorySlot(mainSlot)
	offStack := session.InventorySlot(offSlot)
	if mainStack.IsEmpty() && offStack.IsEmpty() {
		return false
	}

	mainMeta := GetSlotItemMeta(session, mainSlot)
	offMeta := GetSlotItemMeta(session, offSlot)
	mainPotion := GetSlotPotionType(session, mainSlot)
	offPotion := GetSlotPotionType(session, offSlot)
	mainTrim, _ := GetSlotArmorTrim(session, mainSlot)
	offTrim, _ := GetSlotArmorTrim(session, offSlot)
	mainStar, hasMainStar := GetSlotFireworkStar(session, mainSlot)
	offStar, hasOffStar := GetSlotFireworkStar(session, offSlot)
	mainFW, hasMainFW := GetSlotFireworks(session, mainSlot)
	offFW, hasOffFW := GetSlotFireworks(session, offSlot)
	mainBanner := GetSlotBannerPatterns(session, mainSlot)
	offBanner := GetSlotBannerPatterns(session, offSlot)
	mainHorn := GetSlotGoatHornInstrument(session, mainSlot)
	offHorn := GetSlotGoatHornInstrument(session, offSlot)
	mainMapID, hasMainMap := GetSlotMapID(session, mainSlot)
	offMapID, hasOffMap := GetSlotMapID(session, offSlot)
	mainBundle := GetSlotBundleContents(session, mainSlot)
	offBundle := GetSlotBundleContents(session, offSlot)
	mainBundleSel := GetSlotBundleSelectedIndex(session, mainSlot)
	offBundleSel := GetSlotBundleSelectedIndex(session, offSlot)
	mainWB, hasMainWB := GetSlotWritableBook(session, mainSlot)
	offWB, hasOffWB := GetSlotWritableBook(session, offSlot)
	mainWrB, hasMainWrB := GetSlotWrittenBook(session, mainSlot)
	offWrB, hasOffWrB := GetSlotWrittenBook(session, offSlot)
	s.StopUsingSpyglass(session)

	session.SetInventorySlot(mainSlot, offStack)
	session.SetInventorySlot(offSlot, mainStack)
	SetSlotItemMeta(session, mainSlot, offMeta)
	SetSlotItemMeta(session, offSlot, mainMeta)
	SetSlotPotionType(session, mainSlot, offPotion)
	SetSlotPotionType(session, offSlot, mainPotion)
	SetSlotArmorTrim(session, mainSlot, offTrim)
	SetSlotArmorTrim(session, offSlot, mainTrim)
	SetSlotGoatHornInstrument(session, mainSlot, offHorn)
	SetSlotGoatHornInstrument(session, offSlot, mainHorn)
	if hasOffMap {
		SetSlotMapID(session, mainSlot, offMapID)
	} else {
		ClearSlotMapID(session, mainSlot)
	}
	if hasMainMap {
		SetSlotMapID(session, offSlot, mainMapID)
	} else {
		ClearSlotMapID(session, offSlot)
	}
	SetSlotBundleContents(session, mainSlot, offBundle)
	SetSlotBundleContents(session, offSlot, mainBundle)
	SetSlotBundleSelectedIndex(session, mainSlot, offBundleSel)
	SetSlotBundleSelectedIndex(session, offSlot, mainBundleSel)
	if hasOffWB {
		SetSlotWritableBook(session, mainSlot, offWB)
	} else {
		ClearSlotWritableBook(session, mainSlot)
	}
	if hasMainWB {
		SetSlotWritableBook(session, offSlot, mainWB)
	} else {
		ClearSlotWritableBook(session, offSlot)
	}
	if hasOffWrB {
		SetSlotWrittenBook(session, mainSlot, offWrB)
	} else {
		ClearSlotWrittenBook(session, mainSlot)
	}
	if hasMainWrB {
		SetSlotWrittenBook(session, offSlot, mainWrB)
	} else {
		ClearSlotWrittenBook(session, offSlot)
	}
	if hasOffStar {
		SetSlotFireworkStar(session, mainSlot, offStar)
	} else {
		ClearSlotFireworkStar(session, mainSlot)
	}
	if hasMainStar {
		SetSlotFireworkStar(session, offSlot, mainStar)
	} else {
		ClearSlotFireworkStar(session, offSlot)
	}
	if hasOffFW {
		SetSlotFireworks(session, mainSlot, offFW)
	} else {
		ClearSlotFireworks(session, mainSlot)
	}
	if hasMainFW {
		SetSlotFireworks(session, offSlot, mainFW)
	} else {
		ClearSlotFireworks(session, offSlot)
	}
	SetSlotBannerPatterns(session, mainSlot, offBanner)
	SetSlotBannerPatterns(session, offSlot, mainBanner)

	if session.send != nil {
		stateID := session.NextContainerStateID()
		_ = session.send(protocol.PlayContainerSetSlot, EncodeContainerSlot(0, stateID, mainSlot, offStack))
		_ = session.send(protocol.PlayContainerSetSlot, EncodeContainerSlot(0, stateID, offSlot, mainStack))
	}
	s.syncPlayerInventoryContainer(session)
	s.TriggerInventoryChanged(session)
	return true
}

func pickBlockItemName(state uint16) string {
	bname := world.BlockName(state)
	if bname == "" || bname == "minecraft:air" || bname == "minecraft:cave_air" || bname == "minecraft:void_air" {
		return ""
	}
	switch bname {
	case "minecraft:redstone_wire":
		return "minecraft:redstone"
	case "minecraft:wheat":
		return "minecraft:wheat_seeds"
	case "minecraft:carrots":
		return "minecraft:carrot"
	case "minecraft:potatoes":
		return "minecraft:potato"
	case "minecraft:beetroots":
		return "minecraft:beetroot_seeds"
	case "minecraft:melon_stem", "minecraft:attached_melon_stem":
		return "minecraft:melon_seeds"
	case "minecraft:pumpkin_stem", "minecraft:attached_pumpkin_stem":
		return "minecraft:pumpkin_seeds"
	case "minecraft:sweet_berry_bush":
		return "minecraft:sweet_berries"
	case "minecraft:cave_vines", "minecraft:cave_vines_plant":
		return "minecraft:glow_berries"
	case "minecraft:kelp_plant":
		return "minecraft:kelp"
	case "minecraft:tall_seagrass":
		return "minecraft:seagrass"
	case "minecraft:bamboo_sapling":
		return "minecraft:bamboo"
	case "minecraft:cocoa":
		return "minecraft:cocoa_beans"
	case "minecraft:tripwire":
		return "minecraft:string"
	case "minecraft:powder_snow":
		return "minecraft:powder_snow_bucket"
	case "minecraft:wall_torch":
		return "minecraft:torch"
	case "minecraft:soul_wall_torch":
		return "minecraft:soul_torch"
	case "minecraft:redstone_wall_torch":
		return "minecraft:redstone_torch"
	case "minecraft:copper_wall_torch":
		return "minecraft:copper_torch"
	case "minecraft:water_cauldron", "minecraft:lava_cauldron", "minecraft:powder_snow_cauldron":
		return "minecraft:cauldron"
	case "minecraft:piston_head", "minecraft:moving_piston":
		return "minecraft:piston"
	}
	switch {
	case strings.HasSuffix(bname, "_wall_sign"):
		bname = strings.TrimSuffix(bname, "_wall_sign") + "_sign"
	case strings.HasSuffix(bname, "_wall_hanging_sign"):
		bname = strings.TrimSuffix(bname, "_wall_hanging_sign") + "_hanging_sign"
	case strings.HasSuffix(bname, "_wall_banner"):
		bname = strings.TrimSuffix(bname, "_wall_banner") + "_banner"
	case strings.HasSuffix(bname, "_wall_head"):
		bname = strings.TrimSuffix(bname, "_wall_head") + "_head"
	case strings.HasSuffix(bname, "_wall_skull"):
		bname = strings.TrimSuffix(bname, "_wall_skull") + "_skull"
	}
	if id, ok := world.ItemNameToID(bname); ok && id > 0 {
		return bname
	}
	return ""
}

func pickEntityItemName(ent *world.Entity) string {
	if ent == nil {
		return ""
	}
	if ent.IsItem() && !ent.Item.IsEmpty() {
		return ent.Item.ResolvedID()
	}
	typeName := canonicalResourceName(ent.TypeName)
	if typeName == "" {
		return ""
	}
	switch typeName {
	case "minecraft:leash_knot":
		return "minecraft:lead"
	case "minecraft:armor_stand",
		"minecraft:end_crystal",
		"minecraft:item_frame",
		"minecraft:glow_item_frame",
		"minecraft:painting",
		"minecraft:minecart",
		"minecraft:chest_minecart",
		"minecraft:furnace_minecart",
		"minecraft:tnt_minecart",
		"minecraft:hopper_minecart":
		if id, ok := world.ItemNameToID(typeName); ok && id > 0 {
			return typeName
		}
	}
	if strings.HasSuffix(typeName, "_boat") || strings.HasSuffix(typeName, "_raft") {
		if id, ok := world.ItemNameToID(typeName); ok && id > 0 {
			return typeName
		}
	}
	eggName := typeName + "_spawn_egg"
	if id, ok := world.ItemNameToID(eggName); ok && id > 0 {
		return eggName
	}
	return ""
}

// SelectOrPickItem implements vanilla middle-click item selection:
// 1. If the item is already on the hotbar (slots 36..44), switch SelectedSlot to that hotbar slot.
// 2. If the item is in the main inventory (slots 9..35), swap it into an empty hotbar slot (or the current SelectedSlot).
// 3. In Creative mode, if the item is not in the inventory, place a 1-count stack into an empty hotbar slot (or SelectedSlot).
func (s *Server) SelectOrPickItem(session *PlayerSession, itemName string) bool {
	if s == nil || session == nil || session.IsDead() {
		return false
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator {
		return false
	}
	itemID, ok := world.ItemNameToID(itemName)
	if !ok || itemID <= 0 {
		return false
	}

	inv := session.Inventory()
	// 1. Check hotbar (slots 36..44)
	for hb := 0; hb < world.HotbarSlotCount; hb++ {
		slot := world.HotbarContainerOffset + hb
		if !inv[slot].IsEmpty() && inv[slot].ResolvedItemID() == itemID {
			session.SetSelectedSlot(int32(hb))
			if session.send != nil {
				w := protocol.NewWriter(2)
				w.VarInt(int32(hb))
				_ = session.send(protocol.PlaySetHeldSlot, w.Bytes())
			}
			return true
		}
	}

	targetHB := int(session.SelectedSlot())
	if targetHB < 0 || targetHB >= world.HotbarSlotCount {
		targetHB = 0
	}
	targetSlot := world.HotbarContainerOffset + targetHB
	if !inv[targetSlot].IsEmpty() {
		for hb := 0; hb < world.HotbarSlotCount; hb++ {
			sSlot := world.HotbarContainerOffset + hb
			if inv[sSlot].IsEmpty() {
				targetHB = hb
				targetSlot = sSlot
				break
			}
		}
	}

	// 2. Check main inventory (slots 9..35)
	for srcSlot := 9; srcSlot < world.HotbarContainerOffset; srcSlot++ {
		if !inv[srcSlot].IsEmpty() && inv[srcSlot].ResolvedItemID() == itemID {
			srcStack := inv[srcSlot]
			dstStack := inv[targetSlot]
			srcMeta := GetSlotItemMeta(session, srcSlot)
			dstMeta := GetSlotItemMeta(session, targetSlot)
			srcTrim, _ := GetSlotArmorTrim(session, srcSlot)
			dstTrim, _ := GetSlotArmorTrim(session, targetSlot)
			srcPotion := GetSlotPotionType(session, srcSlot)
			dstPotion := GetSlotPotionType(session, targetSlot)
			srcHorn := GetSlotGoatHornInstrument(session, srcSlot)
			dstHorn := GetSlotGoatHornInstrument(session, targetSlot)
			srcMapID, hasSrcMap := GetSlotMapID(session, srcSlot)
			dstMapID, hasDstMap := GetSlotMapID(session, targetSlot)
			srcBundle := GetSlotBundleContents(session, srcSlot)
			dstBundle := GetSlotBundleContents(session, targetSlot)
			srcBanner := GetSlotBannerPatterns(session, srcSlot)
			dstBanner := GetSlotBannerPatterns(session, targetSlot)
			srcWB, hasSrcWB := GetSlotWritableBook(session, srcSlot)
			dstWB, hasDstWB := GetSlotWritableBook(session, targetSlot)
			srcWrB, hasSrcWrB := GetSlotWrittenBook(session, srcSlot)
			dstWrB, hasDstWrB := GetSlotWrittenBook(session, targetSlot)

			session.SetInventorySlot(targetSlot, srcStack)
			session.SetInventorySlot(srcSlot, dstStack)
			SetSlotItemMeta(session, targetSlot, srcMeta)
			SetSlotItemMeta(session, srcSlot, dstMeta)
			SetSlotArmorTrim(session, targetSlot, srcTrim)
			SetSlotArmorTrim(session, srcSlot, dstTrim)
			SetSlotPotionType(session, targetSlot, srcPotion)
			SetSlotPotionType(session, srcSlot, dstPotion)
			SetSlotGoatHornInstrument(session, targetSlot, srcHorn)
			SetSlotGoatHornInstrument(session, srcSlot, dstHorn)
			if hasSrcMap {
				SetSlotMapID(session, targetSlot, srcMapID)
			} else {
				ClearSlotMapID(session, targetSlot)
			}
			if hasDstMap {
				SetSlotMapID(session, srcSlot, dstMapID)
			} else {
				ClearSlotMapID(session, srcSlot)
			}
			SetSlotBundleContents(session, targetSlot, srcBundle)
			SetSlotBundleContents(session, srcSlot, dstBundle)
			SetSlotBannerPatterns(session, targetSlot, srcBanner)
			SetSlotBannerPatterns(session, srcSlot, dstBanner)
			if hasSrcWB {
				SetSlotWritableBook(session, targetSlot, srcWB)
			} else {
				ClearSlotWritableBook(session, targetSlot)
			}
			if hasDstWB {
				SetSlotWritableBook(session, srcSlot, dstWB)
			} else {
				ClearSlotWritableBook(session, srcSlot)
			}
			if hasSrcWrB {
				SetSlotWrittenBook(session, targetSlot, srcWrB)
			} else {
				ClearSlotWrittenBook(session, targetSlot)
			}
			if hasDstWrB {
				SetSlotWrittenBook(session, srcSlot, dstWrB)
			} else {
				ClearSlotWrittenBook(session, srcSlot)
			}

			session.SetSelectedSlot(int32(targetHB))
			if session.send != nil {
				stateID := session.NextContainerStateID()
				_ = session.send(protocol.PlayContainerSetSlot, EncodeContainerSlot(0, stateID, targetSlot, srcStack))
				_ = session.send(protocol.PlayContainerSetSlot, EncodeContainerSlot(0, stateID, srcSlot, dstStack))
				w := protocol.NewWriter(2)
				w.VarInt(int32(targetHB))
				_ = session.send(protocol.PlaySetHeldSlot, w.Bytes())
			}
			s.syncPlayerInventoryContainer(session)
			return true
		}
	}

	// 3. Creative mode spawns the item into targetSlot.
	if snap.GameMode == GameModeCreative {
		newStack := world.NewItemStack(itemID, 1)
		session.SetInventorySlot(targetSlot, newStack)
		clearAllPickedSlotComponents(s, session, targetSlot)
		session.SetSelectedSlot(int32(targetHB))
		if session.send != nil {
			stateID := session.NextContainerStateID()
			_ = session.send(protocol.PlayContainerSetSlot, EncodeContainerSlot(0, stateID, targetSlot, newStack))
			w := protocol.NewWriter(2)
			w.VarInt(int32(targetHB))
			_ = session.send(protocol.PlaySetHeldSlot, w.Bytes())
		}
		return true
	}
	return false
}

func clearAllPickedSlotComponents(s *Server, session *PlayerSession, slot int) {
	SetSlotItemMeta(session, slot, ItemMeta{})
	SetSlotArmorTrim(session, slot, ArmorTrim{})
	SetSlotPotionType(session, slot, "")
	ClearSlotGoatHornInstrument(session, slot)
	ClearSlotMapID(session, slot)
	ClearSlotBundleContents(session, slot)
	ClearSlotBannerMeta(session, slot)
	ClearSlotWritableBook(session, slot)
	ClearSlotWrittenBook(session, slot)
	if s != nil {
		SetSlotDecoratedPotSherds(s, session, slot, DefaultDecoratedPotSherds())
	}
}

func allocateCreativePickHotbarSlot(s *Server, session *PlayerSession, itemID int32) int {
	inv := session.Inventory()
	targetHB := int(session.SelectedSlot())
	if targetHB < 0 || targetHB >= world.HotbarSlotCount {
		targetHB = 0
	}
	targetSlot := world.HotbarContainerOffset + targetHB
	if !inv[targetSlot].IsEmpty() {
		for hb := 0; hb < world.HotbarSlotCount; hb++ {
			sSlot := world.HotbarContainerOffset + hb
			if inv[sSlot].IsEmpty() {
				targetHB = hb
				targetSlot = sSlot
				break
			}
		}
	}
	newStack := world.NewItemStack(itemID, 1)
	session.SetInventorySlot(targetSlot, newStack)
	clearAllPickedSlotComponents(s, session, targetSlot)
	session.SetSelectedSlot(int32(targetHB))
	if session.send != nil {
		w := protocol.NewWriter(2)
		w.VarInt(int32(targetHB))
		_ = session.send(protocol.PlaySetHeldSlot, w.Bytes())
	}
	return targetSlot
}

// PickItemFromBlock handles middle-click block picking at (x, y, z).
// When includeData == true in Creative mode (Ctrl + Pick Block), block entity
// data (Banner patterns & CustomName, DecoratedPot sherds, Furnace/Hopper/BrewingStand
// CustomName) is copied onto the picked hotbar item.
func (s *Server) PickItemFromBlock(session *PlayerSession, x, y, z int, includeData bool) bool {
	if s == nil || session == nil {
		return false
	}
	cache := s.PlayerChunks(session)
	if cache == nil {
		return false
	}
	state := cache.GetBlock(x, y, z)
	itemName := pickBlockItemName(state)
	if itemName == "" {
		return false
	}
	snap := session.Snapshot()
	if !includeData || snap.GameMode != GameModeCreative {
		return s.SelectOrPickItem(session, itemName)
	}
	itemID, ok := world.ItemNameToID(itemName)
	if !ok || itemID <= 0 {
		return false
	}
	targetSlot := allocateCreativePickHotbarSlot(s, session, itemID)
	dim := world.NormalizeDimension(snap.Dimension)

	if IsBannerBlock(state) {
		if b, exists := s.GetBannerAt(dim, x, y, z); exists && b != nil {
			SetSlotBannerPatterns(session, targetSlot, b.Patterns)
			if b.CustomName != nil && b.CustomName.PlainText() != "" {
				meta := GetSlotItemMeta(session, targetSlot)
				meta.CustomName = b.CustomName.PlainText()
				SetSlotItemMeta(session, targetSlot, meta)
			}
		}
	}
	if IsDecoratedPotBlock(state) {
		if dp, exists := s.GetDecoratedPotAt(dim, x, y, z); exists && dp != nil {
			SetSlotDecoratedPotSherds(s, session, targetSlot, dp.Sherds)
		}
	}
	if fbe, exists := s.FurnaceAt(x, y, z); exists && fbe.CustomName != "" {
		meta := GetSlotItemMeta(session, targetSlot)
		meta.CustomName = fbe.CustomName
		SetSlotItemMeta(session, targetSlot, meta)
	}
	if hbe, exists := s.HopperAt(dim, x, y, z); exists && hbe != nil && hbe.CustomName != "" {
		meta := GetSlotItemMeta(session, targetSlot)
		meta.CustomName = hbe.CustomName
		SetSlotItemMeta(session, targetSlot, meta)
	}
	if bs, exists := s.BrewingStandAtInDim(dim, x, y, z); exists && bs.CustomName != "" {
		meta := GetSlotItemMeta(session, targetSlot)
		meta.CustomName = bs.CustomName
		SetSlotItemMeta(session, targetSlot, meta)
	}

	if session.send != nil {
		stateID := session.NextContainerStateID()
		_ = session.send(protocol.PlayContainerSetSlot, EncodeContainerSlot(0, stateID, targetSlot, session.InventorySlot(targetSlot)))
	}
	s.syncPlayerInventoryContainer(session)
	return true
}

// PickItemFromEntity handles middle-click entity picking for entityID.
// For non-empty item frames (`minecraft:item_frame` / `minecraft:glow_item_frame`),
// picks the framed item first; when includeData == true in Creative mode, also
// copies entity/item-frame custom name, metadata, map ID, goat horn instrument,
// or bundle contents.
func (s *Server) PickItemFromEntity(session *PlayerSession, entityID int32, includeData bool) bool {
	if s == nil || session == nil || s.entities == nil {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil {
		return false
	}
	frameSt, isFrame := s.ItemFrameState(entityID)
	hasFramedItem := isFrame && !frameSt.Item.IsEmpty()

	var itemName string
	if hasFramedItem {
		itemName = frameSt.Item.ResolvedID()
	} else {
		itemName = pickEntityItemName(ent)
	}
	if itemName == "" {
		return false
	}
	snap := session.Snapshot()
	if !includeData || snap.GameMode != GameModeCreative {
		return s.SelectOrPickItem(session, itemName)
	}
	itemID, ok := world.ItemNameToID(itemName)
	if !ok || itemID <= 0 {
		return false
	}
	targetSlot := allocateCreativePickHotbarSlot(s, session, itemID)

	if hasFramedItem {
		if !frameSt.ItemMeta.IsEmpty() {
			SetSlotItemMeta(session, targetSlot, frameSt.ItemMeta)
		}
		if frameSt.HasMapID {
			SetSlotMapID(session, targetSlot, frameSt.MapID)
		}
	} else if ent.IsItem() {
		if fishMeta, exists := s.DroppedFishingItemMeta(ent.ID); exists && !fishMeta.IsEmpty() {
			SetSlotItemMeta(session, targetSlot, fishMeta)
		}
		if hornInst, exists := s.DroppedGoatHornInstrument(ent.ID); exists && hornInst != "" {
			SetSlotGoatHornInstrument(session, targetSlot, hornInst)
		}
		if mapID, exists := s.DroppedMapEntityID(ent.ID); exists {
			SetSlotMapID(session, targetSlot, mapID)
		}
		if bundleItems, exists := s.DroppedBundleContents(ent.ID); exists && len(bundleItems) > 0 {
			SetSlotBundleContents(session, targetSlot, bundleItems)
		}
	} else if vst, exists := s.VehicleStateByID(ent.ID); exists && vst.CustomName != "" {
		meta := GetSlotItemMeta(session, targetSlot)
		meta.CustomName = vst.CustomName
		SetSlotItemMeta(session, targetSlot, meta)
	}

	if session.send != nil {
		stateID := session.NextContainerStateID()
		_ = session.send(protocol.PlayContainerSetSlot, EncodeContainerSlot(0, stateID, targetSlot, session.InventorySlot(targetSlot)))
	}
	s.syncPlayerInventoryContainer(session)
	return true
}

// HandlePickItemFromBlockPacket decodes ServerboundPickItemFromBlockPacket (0x24):
// Position(pos), Bool(includeData).
func (s *Server) HandlePickItemFromBlockPacket(session *PlayerSession, r *protocol.Reader) error {
	if s == nil || session == nil || r == nil {
		return nil
	}
	x, y, z, err := r.Position()
	if err != nil {
		return err
	}
	includeData, err := r.Bool()
	if err != nil {
		return err
	}
	s.PickItemFromBlock(session, x, y, z, includeData)
	return nil
}

// HandlePickItemFromEntityPacket decodes ServerboundPickItemFromEntityPacket (0x25):
// VarInt(entityID), Bool(includeData).
func (s *Server) HandlePickItemFromEntityPacket(session *PlayerSession, r *protocol.Reader) error {
	if s == nil || session == nil || r == nil {
		return nil
	}
	entityID, err := r.VarInt()
	if err != nil {
		return err
	}
	includeData, err := r.Bool()
	if err != nil {
		return err
	}
	s.PickItemFromEntity(session, entityID, includeData)
	return nil
}

var bucketFaceOffsets = [6][3]int{
	{0, -1, 0}, {0, 1, 0}, {0, 0, -1}, {0, 0, 1}, {-1, 0, 0}, {1, 0, 0},
}

func isWaterOrBubbleOrWaterloggedAt(cache *world.Cache, x, y, z int) bool {
	if cache == nil || y < world.MinY || y >= world.MinY+world.WorldHeight {
		return false
	}
	st := cache.GetBlock(x, y, z)
	if st == world.StateWater || world.IsBubbleColumnBlock(st) || world.IsWaterlogged(st) {
		return true
	}
	if kind, _, _, _, _, ok := world.ClassifyFluidState(st); ok && kind == world.FluidWater {
		return true
	}
	return false
}

// StepPlayerFluidAndBubbleColumn checks whether session is inside a
// `minecraft:bubble_column`, `minecraft:water`, or a `waterlogged="true"` block:
//   - In `minecraft:bubble_column`: computes updated velocity via
//     `cache.BubbleColumnVelocityAt`, resets `FallDistance` to `0`, extinguishes
//     player fire ticks, and sends `PlaySetEntityMotion` to session.
//   - In `water` or `waterlogged="true"` block: resets `FallDistance` to `0`
//     and extinguishes player fire ticks (or evaporates waterlogged water in
//     ultrawarm dimensions such as the Nether).
func (s *Server) StepPlayerFluidAndBubbleColumn(session *PlayerSession) (vx, vy, vz float64, inBubbleColumn, inWater bool) {
	if s == nil || session == nil || session.IsDead() {
		return 0, 0, 0, false, false
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator {
		return 0, 0, 0, false, false
	}
	dim := world.NormalizeDimension(snap.Dimension)
	cache := s.DimensionChunks(dim)
	if cache == nil {
		return 0, 0, 0, false, false
	}
	bx, by, bz := int(math.Floor(snap.X)), int(math.Floor(snap.Y)), int(math.Floor(snap.Z))
	st := getSurvivalState(session)

	st.mu.Lock()
	curVX, curVY, curVZ := st.bubbleVX, st.bubbleVY, st.bubbleVZ
	st.mu.Unlock()

	nx, ny, nz, ok := cache.BubbleColumnVelocityAt(bx, by, bz, curVX, curVY, curVZ)
	if !ok && by+1 < world.MinY+world.WorldHeight {
		nx, ny, nz, ok = cache.BubbleColumnVelocityAt(bx, by+1, bz, curVX, curVY, curVZ)
	}
	if ok {
		st.mu.Lock()
		st.fallDistance = 0
		st.bubbleVX = nx
		st.bubbleVY = ny
		st.bubbleVZ = nz
		st.mu.Unlock()
		if s.PlayerFireTicks(session) > 0 {
			s.SetPlayerFireTicks(session, 0)
		}
		if session.send != nil {
			_ = session.send(protocol.PlaySetEntityMotion, EncodeSetEntityMotionPacket(snap.EntityID, nx, ny, nz))
		}
		return nx, ny, nz, true, true
	}

	st.mu.Lock()
	st.bubbleVX = 0
	st.bubbleVY = 0
	st.bubbleVZ = 0
	st.mu.Unlock()

	evaporates := cache.IsWaterEvaporatingDimension()
	for _, cy := range [2]int{by, by + 1} {
		if cy < world.MinY || cy >= world.MinY+world.WorldHeight {
			continue
		}
		blk := cache.GetBlock(bx, cy, bz)
		if world.IsWaterlogged(blk) && evaporates {
			if dry, changed := world.SetWaterlogged(blk, false); changed {
				if applied, upd := cache.ApplyBlockWithUpdates(bx, cy, bz, dry); applied {
					s.broadcastDimensionBlockChanges(dim, cache, upd.Changes, upd.LightChanged)
					s.BroadcastDimensionChunk(dim, int32(bx>>4), int32(bz>>4), PlaySoundPacketID,
						EncodeSoundPacket("minecraft:block.fire.extinguish", SoundSourceBlocks, float64(bx)+0.5, float64(cy)+0.5, float64(bz)+0.5, 0.5, 2.6, 0))
				}
			}
			continue
		}
		if isWaterOrBubbleOrWaterloggedAt(cache, bx, cy, bz) {
			inWater = true
		}
	}

	if inWater {
		st.mu.Lock()
		st.fallDistance = 0
		st.mu.Unlock()
		if s.PlayerFireTicks(session) > 0 {
			s.SetPlayerFireTicks(session, 0)
		}
	}
	return 0, 0, 0, false, inWater
}

func (s *Server) giveOrReplaceFilledBucket(session *PlayerSession, invSlot int, heldStack world.ItemStack, bucketItem string) world.ItemStack {
	filledStack := world.NewNamedItemStack(bucketItem, 1)
	if session == nil || invSlot < 0 || invSlot >= world.PlayerInventorySlots {
		return filledStack
	}
	snap := session.Snapshot()
	dim := world.NormalizeDimension(snap.Dimension)
	if snap.GameMode != GameModeCreative {
		if heldStack.Count <= 1 {
			session.SetInventorySlot(invSlot, filledStack)
			if session.send != nil {
				_ = session.send(protocol.PlayContainerSetSlot, EncodeContainerSlot(0, session.NextContainerStateID(), invSlot, filledStack))
			}
		} else {
			remStack := world.NewItemStack(heldStack.ResolvedItemID(), heldStack.Count-1)
			session.SetInventorySlot(invSlot, remStack)
			added, changedSlots := session.AddItemToInventory(filledStack)
			if added <= 0 && s != nil {
				_, _ = s.SpawnItemEntityInDimension(dim, snap.X, snap.Y+0.5, snap.Z, filledStack)
			}
			if session.send != nil {
				_ = session.send(protocol.PlayContainerSetSlot, EncodeContainerSlot(0, session.NextContainerStateID(), invSlot, session.InventorySlot(invSlot)))
				for _, cs := range changedSlots {
					_ = session.send(protocol.PlayContainerSetSlot, EncodeContainerSlot(0, session.NextContainerStateID(), cs, session.InventorySlot(cs)))
				}
			}
		}
	}
	if s != nil {
		s.RecordItemUsed(session, "minecraft:bucket", 1)
		s.TriggerItemAcquired(session, filledStack)
		s.TriggerInventoryChanged(session)
	}
	return filledStack
}

// UseFluidBucketOnBlock handles right-clicking block (x, y, z) on face (0..5)
// with an empty bucket ("minecraft:bucket"), water bucket ("minecraft:water_bucket"),
// or lava bucket ("minecraft:lava_bucket"):
//   - "minecraft:bucket": calls cache.PickupFluidAt at (x, y, z) (and if not
//     found, at the adjacent cell along face), broadcasts res.Changes and
//     res.LightChanged, and gives the filled bucket to session.
//   - "minecraft:water_bucket" / "minecraft:lava_bucket": resolves the target
//     cell (waterlogging (x, y, z) when CanBlockBeWaterlogged and not sneaking,
//     or placing at the adjacent cell along face), calls cache.PlaceFluidAt
//     (handling water evaporation in the Nether and extinguishing fire/campfires),
//     broadcasts res.Changes and res.LightChanged, and replaces the held bucket
//     with "minecraft:bucket" in survival mode.
func (s *Server) UseFluidBucketOnBlock(session *PlayerSession, hand int32, x, y, z int, face int32) (res world.BlockUpdateResult, evaporated bool, handled bool) {
	if s == nil || session == nil || session.IsDead() {
		return res, false, false
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator || snap.GameMode == GameModeAdventure {
		return res, false, false
	}
	invSlot, heldStack := session.handSlotAndStack(hand)
	if heldStack.IsEmpty() {
		return res, false, false
	}
	itemID := canonicalItemName(heldStack.ResolvedID())
	if itemID != "minecraft:bucket" && itemID != "minecraft:water_bucket" && itemID != "minecraft:lava_bucket" {
		return res, false, false
	}
	dim := world.NormalizeDimension(snap.Dimension)
	cache := s.DimensionChunks(dim)
	if cache == nil {
		return res, false, false
	}

	if itemID == "minecraft:bucket" {
		tx, ty, tz := x, y, z
		bucketItem, ok, pickupRes := cache.PickupFluidAt(tx, ty, tz)
		if !ok && face >= 0 && int(face) < len(bucketFaceOffsets) {
			off := bucketFaceOffsets[face]
			tx, ty, tz = x+off[0], y+off[1], z+off[2]
			bucketItem, ok, pickupRes = cache.PickupFluidAt(tx, ty, tz)
		}
		if !ok {
			return res, false, false
		}
		res = pickupRes
		s.InvalidatePortalPOIs(dim, res.Changes)
		if len(res.Changes) > 0 || len(res.LightChanged) > 0 {
			s.broadcastDimensionBlockChanges(dim, cache, res.Changes, res.LightChanged)
		}
		s.giveOrReplaceFilledBucket(session, invSlot, heldStack, bucketItem)
		s.EmitPlayerBlockVibration(session, GameEventFluidPickup, dim, tx, ty, tz, bucketItem)
		return res, false, true
	}

	kind := world.FluidWater
	if itemID == "minecraft:lava_bucket" {
		kind = world.FluidLava
	}
	clickedState := cache.GetBlock(x, y, z)
	tx, ty, tz := x, y, z
	if kind == world.FluidWater && !session.IsSneaking() && world.CanBlockBeWaterlogged(clickedState) && !world.IsWaterlogged(clickedState) {
		tx, ty, tz = x, y, z
	} else if face >= 0 && int(face) < len(bucketFaceOffsets) {
		off := bucketFaceOffsets[face]
		tx, ty, tz = x+off[0], y+off[1], z+off[2]
	}

	prevTargetState := cache.GetBlock(tx, ty, tz)
	if kind == world.FluidWater && !cache.IsWaterEvaporatingDimension() && IsCampfireBlock(prevTargetState) && CampfireStateLit(prevTargetState) {
		s.ExtinguishCampfire(dim, tx, ty, tz, false)
	}

	placed, evap, placeRes := cache.PlaceFluidAt(tx, ty, tz, kind)
	if !placed {
		return res, false, false
	}
	res = placeRes
	evaporated = evap
	s.InvalidatePortalPOIs(dim, res.Changes)
	if len(res.Changes) > 0 || len(res.LightChanged) > 0 {
		s.broadcastDimensionBlockChanges(dim, cache, res.Changes, res.LightChanged)
	}
	if evaporated {
		s.BroadcastDimensionChunk(dim, int32(tx>>4), int32(tz>>4), PlaySoundPacketID,
			EncodeSoundPacket("minecraft:block.fire.extinguish", SoundSourceBlocks, float64(tx)+0.5, float64(ty)+0.5, float64(tz)+0.5, 0.5, 2.6, 0))
	}
	if snap.GameMode != GameModeCreative && invSlot >= 0 && invSlot < world.PlayerInventorySlots {
		emptyBucket := world.NewNamedItemStack("minecraft:bucket", 1)
		session.SetInventorySlot(invSlot, emptyBucket)
		if session.send != nil {
			_ = session.send(protocol.PlayContainerSetSlot, EncodeContainerSlot(0, session.NextContainerStateID(), invSlot, emptyBucket))
		}
	}
	s.RecordItemUsed(session, itemID, 1)
	s.TriggerInventoryChanged(session)
	s.EmitPlayerBlockVibration(session, GameEventFluidPlace, dim, tx, ty, tz, itemID)
	return res, evaporated, true
}

// UseEmptyBucketInAir raycasts up to 5 blocks from session's eye along their
// look direction and picks up the first fluid source, bubble_column, or
// waterlogged block via cache.PickupFluidAt.
func (s *Server) UseEmptyBucketInAir(session *PlayerSession, hand int32) (res world.BlockUpdateResult, handled bool) {
	if s == nil || session == nil || session.IsDead() {
		return res, false
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator || snap.GameMode == GameModeAdventure {
		return res, false
	}
	invSlot, heldStack := session.handSlotAndStack(hand)
	if heldStack.IsEmpty() || canonicalItemName(heldStack.ResolvedID()) != "minecraft:bucket" {
		return res, false
	}
	dim := world.NormalizeDimension(snap.Dimension)
	cache := s.DimensionChunks(dim)
	if cache == nil {
		return res, false
	}
	lx, ly, lz := ElytraLookVector(snap.Yaw, snap.Pitch)
	eyeX, eyeY, eyeZ := snap.X, snap.Y+1.62, snap.Z
	var lastBx, lastBy, lastBz int
	hasLast := false
	for step := 1; step <= 20; step++ {
		dist := float64(step) * 0.25
		bx := int(math.Floor(eyeX + lx*dist))
		by := int(math.Floor(eyeY + ly*dist))
		bz := int(math.Floor(eyeZ + lz*dist))
		if hasLast && bx == lastBx && by == lastBy && bz == lastBz {
			continue
		}
		lastBx, lastBy, lastBz = bx, by, bz
		hasLast = true
		if by < world.MinY || by >= world.MinY+world.WorldHeight {
			continue
		}
		if bucketItem, ok, pickupRes := cache.PickupFluidAt(bx, by, bz); ok {
			res = pickupRes
			s.InvalidatePortalPOIs(dim, res.Changes)
			if len(res.Changes) > 0 || len(res.LightChanged) > 0 {
				s.broadcastDimensionBlockChanges(dim, cache, res.Changes, res.LightChanged)
			}
			s.giveOrReplaceFilledBucket(session, invSlot, heldStack, bucketItem)
			s.EmitPlayerBlockVibration(session, GameEventFluidPickup, dim, bx, by, bz, bucketItem)
			return res, true
		}
		cur := cache.GetBlock(bx, by, bz)
		if !isPassableBlock(cur) {
			break
		}
	}
	return res, false
}

// --- Totem of Undying (minecraft:totem_of_undying) ---

const (
	// TotemOfUndyingItemID is the canonical item ID for Totem of Undying.
	TotemOfUndyingItemID = "minecraft:totem_of_undying"

	// EntityEventTotemActivated is ClientboundEntityEventPacket status 35 (totem activation animation & sound).
	EntityEventTotemActivated byte = 35

	// Vanilla Totem of Undying status effect durations (ticks) and amplifiers.
	TotemRegenerationDuration    int32 = 900 // 45s Regeneration II
	TotemRegenerationAmplifier   int32 = 1
	TotemAbsorptionDuration      int32 = 100 // 5s Absorption II
	TotemAbsorptionAmplifier     int32 = 1
	TotemFireResistanceDuration  int32 = 800 // 40s Fire Resistance I
	TotemFireResistanceAmplifier int32 = 0
)

var itemUseAdvancementsOnce sync.Once

func ensureItemUseAdvancementsRegistered() {
	itemUseAdvancementsOnce.Do(func() {
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
		register("minecraft:adventure/totem_of_undying", "minecraft:adventure/kill_a_mob",
			"Postmortal", "Use a Totem of Undying to cheat death", TotemOfUndyingItemID, AdvancementFrameGoal, "used_totem")
		register("minecraft:adventure/spyglass_at_parrot", "minecraft:adventure/root",
			"Is It a Bird?", "Look at a Parrot through a Spyglass", SpyglassItemID, AdvancementFrameTask, "look_at_parrot")
		register("minecraft:adventure/spyglass_at_ghast", "minecraft:adventure/spyglass_at_parrot",
			"Is It a Balloon?", "Look at a Ghast through a Spyglass", SpyglassItemID, AdvancementFrameTask, "look_at_ghast")
		register("minecraft:adventure/spyglass_at_dragon", "minecraft:adventure/spyglass_at_ghast",
			"Is It a Plane?", "Look at the Ender Dragon through a Spyglass", SpyglassItemID, AdvancementFrameTask, "look_at_dragon")
		register("minecraft:adventure/salvage_sherd", "minecraft:adventure/root",
			"Respecting the Remnants", "Brush a Suspicious block to obtain a Pottery Sherd", "minecraft:brush", AdvancementFrameTask, "has_sherd")
	})
}

// HasCriterion reports whether session has obtained criterion on advancementID.
func (s *Server) HasCriterion(session *PlayerSession, advancementID, criterion string) bool {
	if s == nil || session == nil {
		return false
	}
	ensureItemUseAdvancementsRegistered()
	advID := canonicalAdvancementID(advancementID)
	tr := getAdvancementTracker(session)
	tr.mu.Lock()
	defer tr.mu.Unlock()
	prog, ok := tr.progress[advID]
	if !ok || prog == nil {
		return false
	}
	cp, exists := prog.Criteria[criterion]
	return exists && cp.IsDone()
}

// TriggerUsedTotem fires the "Postmortal" advancement
// (`minecraft:adventure/totem_of_undying`, criterion `"used_totem"`).
func (s *Server) TriggerUsedTotem(session *PlayerSession) {
	if s == nil || session == nil {
		return
	}
	ensureItemUseAdvancementsRegistered()
	s.GrantCriterion(session, "minecraft:adventure/totem_of_undying", "used_totem")
}

// TryConsumeTotemOfUndying checks whether fatal damage from cause can be
// prevented by a `minecraft:totem_of_undying` in session's main hand or off-hand.
// In vanilla 26.1.2, void (`out_of_world`), `/kill` (`generic_kill`), and world
// border bypass Totem of Undying. When activated:
//   - Consumes 1 totem from main hand (checked first) or off-hand in non-Creative mode
//   - Sets player health to 1.0
//   - Clears all existing status effects (`ClearPlayerEffects`)
//   - Applies Regeneration II (900t), Absorption II (100t), Fire Resistance I (800t)
//   - Broadcasts `PlayEntityEvent` (status 35) and records `used:totem_of_undying` + `minecraft:adventure/totem_of_undying`
func (s *Server) TryConsumeTotemOfUndying(session *PlayerSession, cause string) bool {
	if s == nil || session == nil {
		return false
	}
	switch cause {
	case "kill", "generic_kill", "void", "out_of_world", "out_of_border", "outside_border", "world_border":
		return false
	}

	mainSlot := session.HeldInventorySlot()
	checkSlots := [2]int{mainSlot, OffHandContainerSlot}
	totemSlot := -1
	var totemStack world.ItemStack
	for _, sl := range checkSlots {
		if sl < 0 || sl >= world.PlayerInventorySlots {
			continue
		}
		st := session.InventorySlot(sl)
		if !st.IsEmpty() && canonicalItemName(st.ResolvedID()) == TotemOfUndyingItemID {
			totemSlot = sl
			totemStack = st
			break
		}
	}
	if totemSlot < 0 {
		return false
	}

	dim := world.NormalizeDimension(session.Dimension())
	session.mu.Lock()
	session.health = 1.0
	gm := session.gameMode
	entityID := session.EntityID
	pos := session.position
	send := session.send
	session.mu.Unlock()

	st := getSurvivalState(session)
	st.mu.Lock()
	st.dead = false
	st.mu.Unlock()

	if gm != GameModeCreative {
		var updated world.ItemStack
		if totemStack.Count > 1 {
			updated = world.NewItemStack(totemStack.ResolvedItemID(), totemStack.Count-1)
		}
		session.SetInventorySlot(totemSlot, updated)
		if updated.IsEmpty() {
			SetSlotItemMeta(session, totemSlot, ItemMeta{})
		}
		if send != nil {
			_ = send(protocol.PlayContainerSetSlot, EncodeContainerSlot(0, session.NextContainerStateID(), totemSlot, updated))
		}
	}

	s.ClearPlayerEffects(session)
	s.ApplyPlayerEffect(session, NewStatusEffectInstance(EffectRegeneration, TotemRegenerationDuration, TotemRegenerationAmplifier))
	s.ApplyPlayerEffect(session, NewStatusEffectInstance(EffectAbsorption, TotemAbsorptionDuration, TotemAbsorptionAmplifier))
	s.ApplyPlayerEffect(session, NewStatusEffectInstance(EffectFireResistance, TotemFireResistanceDuration, TotemFireResistanceAmplifier))

	evPkt := EncodeEntityEventPacket(entityID, EntityEventTotemActivated)
	if send != nil {
		_ = send(protocol.PlayEntityEvent, evPkt)
	}
	cx := int32(int64(math.Floor(pos[0])) >> 4)
	cz := int32(int64(math.Floor(pos[2])) >> 4)
	s.BroadcastDimensionChunk(dim, cx, cz, protocol.PlayEntityEvent, evPkt)

	s.RecordItemUsed(session, TotemOfUndyingItemID, 1)
	s.TriggerUsedTotem(session)
	s.TriggerInventoryChanged(session)
	return true
}

// --- Spyglass (minecraft:spyglass) ---

const (
	// SpyglassItemID is the canonical item ID for Spyglass.
	SpyglassItemID = "minecraft:spyglass"

	// SpyglassMaxLookRange is the maximum distance (blocks) for spyglass look advancements.
	SpyglassMaxLookRange = 64.0
)

// EncodeLivingEntityUsingFlagsPacket encodes a ClientboundSetEntityDataPacket (0x61)
// updating LivingEntity `DATA_LIVING_ENTITY_FLAGS` (index 8, Byte serializer 0):
// bit 0x01 = isUsingItem, bit 0x02 = off-hand.
func EncodeLivingEntityUsingFlagsPacket(entityID int32, usingItem bool, hand int32) []byte {
	var flags byte
	if usingItem {
		flags |= 0x01
		if hand == HandOffHand {
			flags |= 0x02
		}
	}
	w := protocol.NewWriter(12)
	w.VarInt(entityID)
	w.Byte(8) // LivingEntity DATA_LIVING_ENTITY_FLAGS index
	w.VarInt(0)
	w.Byte(flags)
	w.Byte(0xff)
	return w.Bytes()
}

// IsUsingSpyglass reports whether session is currently looking through a spyglass.
func (s *Server) IsUsingSpyglass(session *PlayerSession) bool {
	if session == nil {
		return false
	}
	st := getSurvivalState(session)
	st.mu.Lock()
	using := st.usingSpyglass
	st.mu.Unlock()
	return using
}

// StartUsingSpyglass starts using a `minecraft:spyglass` in `hand`, sets the
// player's LivingEntity using-item metadata flag (for third-person animation and
// client FOV zoom), plays `minecraft:item.spyglass.use`, records item-used stats,
// and checks line-of-sight up to 64 blocks for "Is It a Bird?" (`parrot`),
// "Is It a Balloon?" (`ghast`), and "Is It a Plane?" (`ender_dragon`).
func (s *Server) StartUsingSpyglass(session *PlayerSession, hand int32) (lookedAtType string, started bool) {
	if s == nil || session == nil || session.IsDead() {
		return "", false
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator {
		return "", false
	}
	_, held := session.handSlotAndStack(hand)
	if held.IsEmpty() || canonicalItemName(held.ResolvedID()) != SpyglassItemID {
		return "", false
	}

	st := getSurvivalState(session)
	st.mu.Lock()
	st.usingSpyglass = true
	st.spyglassHand = hand
	st.mu.Unlock()

	dim := world.NormalizeDimension(snap.Dimension)
	cx := int32(int64(math.Floor(snap.X)) >> 4)
	cz := int32(int64(math.Floor(snap.Z)) >> 4)
	metaPkt := EncodeLivingEntityUsingFlagsPacket(snap.EntityID, true, hand)
	if session.send != nil {
		_ = session.send(protocol.PlaySetEntityData, metaPkt)
	}
	s.BroadcastDimensionChunk(dim, cx, cz, protocol.PlaySetEntityData, metaPkt)
	s.BroadcastDimensionChunk(dim, cx, cz, PlaySoundPacketID,
		EncodeSoundPacket("minecraft:item.spyglass.use", SoundSourcePlayers, snap.X, snap.Y+1.62, snap.Z, 1.0, 1.0, 0))
	s.RecordItemUsed(session, SpyglassItemID, 1)

	lookedAtType = s.SpyglassLookAtEntity(session)
	switch lookedAtType {
	case "minecraft:parrot":
		ensureItemUseAdvancementsRegistered()
		s.GrantCriterion(session, "minecraft:adventure/spyglass_at_parrot", "look_at_parrot")
	case "minecraft:ghast":
		ensureItemUseAdvancementsRegistered()
		s.GrantCriterion(session, "minecraft:adventure/spyglass_at_ghast", "look_at_ghast")
	case "minecraft:ender_dragon":
		ensureItemUseAdvancementsRegistered()
		s.GrantCriterion(session, "minecraft:adventure/spyglass_at_dragon", "look_at_dragon")
	}
	return lookedAtType, true
}

// StopUsingSpyglass stops active spyglass usage for session, clearing the
// LivingEntity using-item flag and playing `minecraft:item.spyglass.stop_using`.
func (s *Server) StopUsingSpyglass(session *PlayerSession) bool {
	if s == nil || session == nil {
		return false
	}
	st := getSurvivalState(session)
	st.mu.Lock()
	if !st.usingSpyglass {
		st.mu.Unlock()
		return false
	}
	st.usingSpyglass = false
	st.mu.Unlock()

	snap := session.Snapshot()
	dim := world.NormalizeDimension(snap.Dimension)
	cx := int32(int64(math.Floor(snap.X)) >> 4)
	cz := int32(int64(math.Floor(snap.Z)) >> 4)
	metaPkt := EncodeLivingEntityUsingFlagsPacket(snap.EntityID, false, HandMain)
	if session.send != nil {
		_ = session.send(protocol.PlaySetEntityData, metaPkt)
	}
	s.BroadcastDimensionChunk(dim, cx, cz, protocol.PlaySetEntityData, metaPkt)
	s.BroadcastDimensionChunk(dim, cx, cz, PlaySoundPacketID,
		EncodeSoundPacket("minecraft:item.spyglass.stop_using", SoundSourcePlayers, snap.X, snap.Y+1.62, snap.Z, 1.0, 1.0, 0))
	return true
}

// SpyglassLookAtEntity raycasts up to SpyglassMaxLookRange (64 blocks) along
// session's look vector and returns the canonical TypeName of the closest entity
// within the look cone (or "" if none).
func (s *Server) SpyglassLookAtEntity(session *PlayerSession) string {
	if s == nil || s.entities == nil || session == nil {
		return ""
	}
	snap := session.Snapshot()
	dim := world.NormalizeDimension(snap.Dimension)
	lx, ly, lz := ElytraLookVector(snap.Yaw, snap.Pitch)
	eyeX, eyeY, eyeZ := snap.X, snap.Y+1.62, snap.Z

	cx := int32(int64(math.Floor(snap.X)) >> 4)
	cz := int32(int64(math.Floor(snap.Z)) >> 4)
	ents := s.entities.AppendInChunkRange(nil, cx, cz, 4)

	bestDist := SpyglassMaxLookRange + 1.0
	bestType := ""
	for i := range ents {
		ent := &ents[i]
		if ent.EffectiveDimension() != dim || ent.IsItem() || ent.IsExperienceOrb() {
			continue
		}
		targetY := ent.Y + 0.5
		dx := ent.X - eyeX
		dy := targetY - eyeY
		dz := ent.Z - eyeZ
		proj := dx*lx + dy*ly + dz*lz
		if proj <= 0 || proj > SpyglassMaxLookRange || proj >= bestDist {
			continue
		}
		perpX := dx - proj*lx
		perpY := dy - proj*ly
		perpZ := dz - proj*lz
		perpDistSq := perpX*perpX + perpY*perpY + perpZ*perpZ
		hitRadius := 1.25
		if canonicalResourceName(ent.TypeName) == "minecraft:ghast" || canonicalResourceName(ent.TypeName) == "minecraft:ender_dragon" {
			hitRadius = 3.0
		}
		if perpDistSq <= hitRadius*hitRadius {
			bestDist = proj
			bestType = canonicalResourceName(ent.TypeName)
		}
	}
	return bestType
}

// --- Goat Horn Per-Slot Instrument & Protocol 775 Cooldown ---

type sessionGoatHornTracker struct {
	mu          sync.Mutex
	slotInst    [world.PlayerInventorySlots]string
	carriedInst string
}

var playerGoatHorns sync.Map // map[*PlayerSession]*sessionGoatHornTracker

func getGoatHornTracker(session *PlayerSession) *sessionGoatHornTracker {
	if session == nil {
		return nil
	}
	if v, ok := playerGoatHorns.Load(session); ok {
		return v.(*sessionGoatHornTracker)
	}
	actual, _ := playerGoatHorns.LoadOrStore(session, &sessionGoatHornTracker{})
	return actual.(*sessionGoatHornTracker)
}

// GetSlotGoatHornInstrument returns the goat horn instrument ID stored in
// session's inventory slot (0..45), or "" if none is set.
func GetSlotGoatHornInstrument(session *PlayerSession, slot int) string {
	if session == nil || slot < 0 || slot >= world.PlayerInventorySlots {
		return ""
	}
	tr := getGoatHornTracker(session)
	tr.mu.Lock()
	inst := tr.slotInst[slot]
	tr.mu.Unlock()
	return inst
}

// SetSlotGoatHornInstrument associates a goat horn instrument ID with session's
// inventory slot (0..45).
func SetSlotGoatHornInstrument(session *PlayerSession, slot int, instrument string) {
	if session == nil || slot < 0 || slot >= world.PlayerInventorySlots {
		return
	}
	tr := getGoatHornTracker(session)
	tr.mu.Lock()
	if instrument == "" {
		tr.slotInst[slot] = ""
	} else {
		tr.slotInst[slot] = canonicalResourceName(instrument)
	}
	tr.mu.Unlock()
}

// ClearSlotGoatHornInstrument clears any goat horn instrument on slot (0..45).
func ClearSlotGoatHornInstrument(session *PlayerSession, slot int) {
	SetSlotGoatHornInstrument(session, slot, "")
}

// GetCarriedGoatHornInstrument returns the goat horn instrument on the cursor.
func GetCarriedGoatHornInstrument(session *PlayerSession) string {
	if session == nil {
		return ""
	}
	tr := getGoatHornTracker(session)
	tr.mu.Lock()
	inst := tr.carriedInst
	tr.mu.Unlock()
	return inst
}

// SetCarriedGoatHornInstrument sets or clears the goat horn instrument on the cursor.
func SetCarriedGoatHornInstrument(session *PlayerSession, instrument string) {
	if session == nil {
		return
	}
	tr := getGoatHornTracker(session)
	tr.mu.Lock()
	if instrument == "" {
		tr.carriedInst = ""
	} else {
		tr.carriedInst = canonicalResourceName(instrument)
	}
	tr.mu.Unlock()
}

// AssociateDroppedGoatHornEntity records the goat horn instrument for a dropped item entity.
func (s *Server) AssociateDroppedGoatHornEntity(entityID int32, instrument string) {
	if s == nil || entityID <= 0 || instrument == "" {
		return
	}
	mgr := s.goatManager()
	mgr.mu.Lock()
	mgr.hornInstrument[entityID] = canonicalResourceName(instrument)
	mgr.mu.Unlock()
}

// DroppedGoatHornInstrument returns the goat horn instrument associated with a dropped item entity.
func (s *Server) DroppedGoatHornInstrument(entityID int32) (string, bool) {
	if s == nil || entityID <= 0 {
		return "", false
	}
	mgr := s.goatManager()
	mgr.mu.Lock()
	inst, ok := mgr.hornInstrument[entityID]
	mgr.mu.Unlock()
	return inst, ok && inst != ""
}

// UsePlayerGoatHorn plays the `minecraft:goat_horn` held in session's `hand`
// using the slot's stored instrument (or `ponder_goat_horn` by default),
// enforces the 140-tick cooldown, sends `protocol.PlayCooldown` (0x11 in protocol 775),
// and records `used:goat_horn`.
func (s *Server) UsePlayerGoatHorn(session *PlayerSession, hand int32) bool {
	if s == nil || session == nil || session.IsDead() {
		return false
	}
	slot, held := session.handSlotAndStack(hand)
	if held.IsEmpty() || canonicalItemName(held.ResolvedID()) != GoatHornItemID {
		return false
	}
	inst := GetSlotGoatHornInstrument(session, slot)
	if !s.UseGoatHornItem(session, hand, inst) {
		return false
	}
	if session.send != nil {
		_ = session.send(protocol.PlayCooldown, EncodeCooldownPacket(GoatHornItemID, GoatHornCooldownTicks))
	}
	s.RecordItemUsed(session, GoatHornItemID, 1)
	return true
}

// --- Milk Bucket & Thrown Potion Trajectory / Water Splash Extinguish ---

// RaycastThrownPotionImpact traces along session's look vector up to 12 blocks
// to determine the impact point `(hitX, hitY, hitZ)` and any directly hit entity
// for a thrown splash or lingering potion.
func (s *Server) RaycastThrownPotionImpact(session *PlayerSession) (hitX, hitY, hitZ float64, directEntityID int32) {
	if s == nil || session == nil {
		return 0, 0, 0, 0
	}
	snap := session.Snapshot()
	dim := world.NormalizeDimension(snap.Dimension)
	cache := s.DimensionChunks(dim)
	lx, ly, lz := ElytraLookVector(snap.Yaw, snap.Pitch)
	eyeX, eyeY, eyeZ := snap.X, snap.Y+1.5, snap.Z

	var ents []world.Entity
	if s.entities != nil {
		cx := int32(int64(math.Floor(snap.X)) >> 4)
		cz := int32(int64(math.Floor(snap.Z)) >> 4)
		ents = s.entities.AppendInChunkRange(nil, cx, cz, 2)
	}

	curX, curY, curZ := eyeX, eyeY, eyeZ
	for step := 1; step <= 24; step++ {
		dist := float64(step) * 0.5
		px := eyeX + lx*dist
		py := eyeY + ly*dist - 0.015*dist*dist // slight gravity arc
		pz := eyeZ + lz*dist

		for i := range ents {
			ent := &ents[i]
			if ent.EffectiveDimension() != dim || ent.IsItem() || ent.IsExperienceOrb() {
				continue
			}
			dx := ent.X - px
			dy := (ent.Y + 0.5) - py
			dz := ent.Z - pz
			if dx*dx+dy*dy+dz*dz <= 0.8*0.8 {
				return ent.X, ent.Y, ent.Z, ent.ID
			}
		}

		if cache != nil {
			bx, by, bz := int(math.Floor(px)), int(math.Floor(py)), int(math.Floor(pz))
			if by >= world.MinY && by < world.MinY+world.WorldHeight {
				st := cache.GetBlock(bx, by, bz)
				if !isPassableBlock(st) && st != world.StateWater {
					return curX, curY, curZ, 0
				}
			}
		}
		curX, curY, curZ = px, py, pz
	}
	return curX, curY, curZ, 0
}

// ExtinguishBlocksWithWaterSplashAt extinguishes `minecraft:fire`, `minecraft:soul_fire`,
// lit campfires, and lit candles within 2 blocks of `(x, y, z)` in `dim`.
func (s *Server) ExtinguishBlocksWithWaterSplashAt(dim string, x, y, z float64) int {
	if s == nil {
		return 0
	}
	dim = world.NormalizeDimension(dim)
	cache := s.DimensionChunks(dim)
	if cache == nil {
		return 0
	}
	cx, cy, cz := int(math.Floor(x)), int(math.Floor(y)), int(math.Floor(z))
	extinguished := 0
	for dx := -2; dx <= 2; dx++ {
		for dy := -1; dy <= 1; dy++ {
			for dz := -2; dz <= 2; dz++ {
				bx, by, bz := cx+dx, cy+dy, cz+dz
				if by < world.MinY || by >= world.MinY+world.WorldHeight {
					continue
				}
				st := cache.GetBlock(bx, by, bz)
				if st == world.StateAir {
					continue
				}
				bname := world.BlockName(st)
				if bname == "minecraft:fire" || bname == "minecraft:soul_fire" {
					if s.ExtinguishFireAt(dim, bx, by, bz) {
						extinguished++
					}
					continue
				}
				if IsCampfireBlock(st) && CampfireStateLit(st) {
					if s.ExtinguishCampfire(dim, bx, by, bz, true) {
						extinguished++
					}
					continue
				}
				if strings.HasSuffix(bname, "candle") || strings.Contains(bname, "candle_cake") {
					props := world.BlockProperties(st)
					if props["lit"] == "true" {
						newProps := make(map[string]string, len(props))
						for k, v := range props {
							newProps[k] = v
						}
						newProps["lit"] = "false"
						if unlit, ok := world.BlockStateByName(bname, newProps); ok && unlit != st {
							if applied, upd := cache.ApplyBlockWithUpdates(bx, by, bz, unlit); applied {
								s.broadcastDimensionBlockChanges(dim, cache, upd.Changes, upd.LightChanged)
								extinguished++
							}
						}
					}
				}
			}
		}
	}
	return extinguished
}

// UseBeverageOrThrownPotion handles right-clicking (`use_item`) with a potion,
// milk bucket, ominous bottle, splash potion, or lingering potion, including
// look-vector trajectory raycasting and water splash block extinguishing.
func (s *Server) UseBeverageOrThrownPotion(session *PlayerSession, hand int32) bool {
	if s == nil || session == nil || session.IsDead() {
		return false
	}
	slot, stack := session.handSlotAndStack(hand)
	if stack.IsEmpty() {
		return false
	}
	itemID := canonicalItemName(stack.ResolvedID())
	snap := session.Snapshot()
	dim := world.NormalizeDimension(snap.Dimension)

	switch itemID {
	case "minecraft:potion":
		if s.DrinkPotion(session, hand) {
			s.RecordItemUsed(session, itemID, 1)
			return true
		}
		return false
	case "minecraft:milk_bucket":
		if _, ok := s.DrinkMilkBucket(session, hand); ok {
			s.RecordItemUsed(session, itemID, 1)
			return true
		}
		return false
	case "minecraft:ominous_bottle":
		if s.DrinkOminousBottle(session, hand) {
			s.RecordItemUsed(session, itemID, 1)
			return true
		}
		return false
	case "minecraft:splash_potion":
		potionID := GetSlotPotionType(session, slot)
		if potionID == "" {
			potionID = PotionWater
		}
		hitX, hitY, hitZ, directEntID := s.RaycastThrownPotionImpact(session)
		s.consumeThrownPotionSlot(session, slot, stack)
		s.ApplySplashPotionAt(dim, hitX, hitY, hitZ, potionID, directEntID)
		normPot := canonicalPotionID(potionID)
		if normPot == PotionWater || normPot == PotionMundane || normPot == PotionThick || normPot == PotionAwkward {
			s.ExtinguishBlocksWithWaterSplashAt(dim, hitX, hitY, hitZ)
		}
		s.RecordItemUsed(session, itemID, 1)
		return true
	case "minecraft:lingering_potion":
		potionID := GetSlotPotionType(session, slot)
		if potionID == "" {
			potionID = PotionWater
		}
		hitX, hitY, hitZ, _ := s.RaycastThrownPotionImpact(session)
		s.consumeThrownPotionSlot(session, slot, stack)
		s.broadcastPotionBreakEffect(dim, hitX, hitY, hitZ, potionID)
		s.SpawnLingeringPotionCloud(dim, hitX, hitY, hitZ, potionID)
		normPot := canonicalPotionID(potionID)
		if normPot == PotionWater || normPot == PotionMundane || normPot == PotionThick || normPot == PotionAwkward {
			s.ExtinguishBlocksWithWaterSplashAt(dim, hitX, hitY, hitZ)
		}
		s.RecordItemUsed(session, itemID, 1)
		return true
	default:
		return false
	}
}

// --- Bundle Use in Air (minecraft:bundle & colored *_bundle) ---

// UseBundleInAir handles right-clicking (`use_item`) with a `minecraft:bundle`
// (or any colored `*_bundle`) in `hand`: ejects the selected item stack (or the
// first item stack if SelectedIndex < 0) from the bundle into the world in front
// of the player, updates `SetSlotBundleContents`, plays `minecraft:item.bundle.drop_contents`,
// syncs the player's inventory container (`DataComponentBundleContents = 50`),
// and records `used:<bundle>`.
func (s *Server) UseBundleInAir(session *PlayerSession, hand int32) (dropped world.ItemStack, handled bool) {
	if s == nil || session == nil || session.IsDead() {
		return world.ItemStack{}, false
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator {
		return world.ItemStack{}, false
	}
	invSlot, held := session.handSlotAndStack(hand)
	if held.IsEmpty() || !IsBundleItem(held.ResolvedID()) {
		return world.ItemStack{}, false
	}
	contents := GetSlotBundleContents(session, invSlot)
	if len(contents) == 0 {
		return world.ItemStack{}, false
	}
	selIdx := GetSlotBundleSelectedIndex(session, invSlot)
	if selIdx < 0 || selIdx >= len(contents) {
		selIdx = 0
	}
	dropped = contents[selIdx]
	nextContents := make([]world.ItemStack, 0, len(contents)-1)
	nextContents = append(nextContents, contents[:selIdx]...)
	nextContents = append(nextContents, contents[selIdx+1:]...)
	SetSlotBundleContents(session, invSlot, nextContents)
	if selIdx >= len(nextContents) {
		SetSlotBundleSelectedIndex(session, invSlot, -1)
	}

	dim := world.NormalizeDimension(snap.Dimension)
	yawRad := float64(snap.Yaw) * (math.Pi / 180.0)
	pitchRad := float64(snap.Pitch) * (math.Pi / 180.0)
	dirX := -math.Sin(yawRad) * math.Cos(pitchRad)
	dirZ := math.Cos(yawRad) * math.Cos(pitchRad)
	spawnX := snap.X + dirX*0.3
	spawnY := snap.Y + 1.3
	spawnZ := snap.Z + dirZ*0.3

	_, _ = s.SpawnItemEntityInDimension(dim, spawnX, spawnY, spawnZ, dropped)

	cx := int32(int64(math.Floor(snap.X)) >> 4)
	cz := int32(int64(math.Floor(snap.Z)) >> 4)
	s.BroadcastDimensionChunk(dim, cx, cz, PlaySoundPacketID,
		EncodeSoundPacket("minecraft:item.bundle.drop_contents", SoundSourcePlayers, snap.X, snap.Y+1.0, snap.Z, 0.8, 0.8, 0))
	s.syncPlayerInventoryContainer(session)
	s.RecordItemUsed(session, canonicalItemName(held.ResolvedID()), 1)
	s.TriggerInventoryChanged(session)
	return dropped, true
}

// --- Brush on Suspicious Sand / Suspicious Gravel (minecraft:brush) ---

// UsePlayerBrushOnBlock handles using a `minecraft:brush` in `hand` on a
// `minecraft:suspicious_sand` or `minecraft:suspicious_gravel` block at `(x, y, z)`
// on `face` (0..5):
//   - Steps `dusted` (`0 -> 1 -> 2 -> 3`) and schedules a 40-tick `UpdateFalling`
//     decay timer (`world.BrushableCooldownTicks`) so idle blocks reset back toward 0.
//   - On the 4th brush step (`dusted == 3` -> completion), extracts the buried
//     item from `cache.StructureBlockEntityAt(x, y, z)` (or evaluates its
//     archaeology loot table via `world.EvaluateArchaeologyLoot`), removes the
//     structure block entity, converts the block into `minecraft:sand` or
//     `minecraft:gravel` (`world.BrushableBlockTurnsInto`), spawns the excavated
//     item entity outside `face`, damages the brush by 1 durability in non-Creative
//     mode, records `used:minecraft:brush`, and grants `minecraft:adventure/salvage_sherd`
//     (`"has_sherd"`) when a pottery sherd (`*_pottery_sherd`) is excavated.
func (s *Server) UsePlayerBrushOnBlock(session *PlayerSession, hand int32, x, y, z int, face int32) (dustedStage int, completed bool, poppedItem world.ItemStack, handled bool) {
	if s == nil || session == nil || session.IsDead() {
		return 0, false, world.ItemStack{}, false
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator || snap.GameMode == GameModeAdventure {
		return 0, false, world.ItemStack{}, false
	}
	invSlot, held := session.handSlotAndStack(hand)
	if held.IsEmpty() || canonicalItemName(held.ResolvedID()) != BrushItemID {
		return 0, false, world.ItemStack{}, false
	}
	dim := world.NormalizeDimension(snap.Dimension)
	cache := s.DimensionChunks(dim)
	if cache == nil || y < world.MinY || y >= world.MinY+world.WorldHeight {
		return 0, false, world.ItemStack{}, false
	}
	curState := cache.GetBlock(x, y, z)
	if !world.IsBrushableBlock(curState) {
		return 0, false, world.ItemStack{}, false
	}
	curDusted, ok := world.BrushableBlockDustedStage(curState)
	if !ok {
		return 0, false, world.ItemStack{}, false
	}
	bname := world.BlockName(curState)
	cx := int32(x >> 4)
	cz := int32(z >> 4)

	if curDusted < world.BrushableMaxDustedStage {
		nextDusted := curDusted + 1
		nextState := world.BrushableBlockState(bname, nextDusted)
		if applied, upd := cache.ApplyBlockWithUpdates(x, y, z, nextState); applied {
			s.broadcastDimensionBlockChanges(dim, cache, upd.Changes, upd.LightChanged)
		}
		cache.ScheduleBlockUpdate(x, y, z, world.UpdateFalling, world.BrushableCooldownTicks)
		snd := "minecraft:item.brush.brushing.sand"
		if bname == "minecraft:suspicious_gravel" {
			snd = "minecraft:item.brush.brushing.gravel"
		}
		s.BroadcastDimensionChunk(dim, cx, cz, PlaySoundPacketID,
			EncodeSoundPacket(snd, SoundSourceBlocks, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, 1.0, 1.0, 0))
		return nextDusted, false, world.ItemStack{}, true
	}

	// Completion step (curDusted == 3 -> turns into sand/gravel and pops item).
	if sbe, exists := cache.StructureBlockEntityAt(x, y, z); exists {
		for _, slot := range sbe.Items {
			if slot.ItemID != "" && slot.Count > 0 {
				poppedItem = world.NewNamedItemStack(slot.ItemID, int32(slot.Count))
				break
			}
		}
		if poppedItem.IsEmpty() && sbe.LootTable != "" {
			seed := sbe.LootTableSeed
			if seed == 0 {
				seed = int64(x)*3129871 ^ int64(z)*116129781 ^ int64(y)
			}
			poppedItem = world.EvaluateArchaeologyLoot(sbe.LootTable, seed)
		}
		cache.RemoveStructureBlockEntity(x, y, z)
	}

	turnsInto := world.BrushableBlockTurnsInto(curState)
	if applied, upd := cache.ApplyBlockWithUpdates(x, y, z, turnsInto); applied {
		s.broadcastDimensionBlockChanges(dim, cache, upd.Changes, upd.LightChanged)
	}

	if !poppedItem.IsEmpty() {
		var off [3]int = [3]int{0, 1, 0}
		if face >= 0 && int(face) < len(bucketFaceOffsets) {
			off = bucketFaceOffsets[face]
		}
		spawnX := float64(x) + 0.5 + float64(off[0])*0.65
		spawnY := float64(y) + 0.35 + float64(off[1])*0.65
		spawnZ := float64(z) + 0.5 + float64(off[2])*0.65
		s.SpawnItemEntityInDimension(dim, spawnX, spawnY, spawnZ, poppedItem)
		if strings.HasSuffix(canonicalItemName(poppedItem.ResolvedID()), "_pottery_sherd") {
			ensureItemUseAdvancementsRegistered()
			s.GrantCriterion(session, "minecraft:adventure/salvage_sherd", "has_sherd")
		}
	}

	completeSnd := "minecraft:item.brush.brushing.sand.complete"
	if bname == "minecraft:suspicious_gravel" {
		completeSnd = "minecraft:item.brush.brushing.gravel.complete"
	}
	s.BroadcastDimensionChunk(dim, cx, cz, PlaySoundPacketID,
		EncodeSoundPacket(completeSnd, SoundSourceBlocks, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, 1.0, 1.0, 0))

	if snap.GameMode != GameModeCreative && invSlot >= 0 && invSlot < world.PlayerInventorySlots {
		if s.DamagePlayerItem(session, invSlot, 1) {
			s.RecordItemBroken(session, BrushItemID)
		}
	}
	s.RecordItemUsed(session, BrushItemID, 1)
	return 0, true, poppedItem, true
}

// --- Written Book & Writable Book Right-Click Use ---

// OpenPlayerWrittenBook handles right-clicking (`use_item`) with a
// `minecraft:written_book` or `minecraft:writable_book` in `hand`:
//   - For `minecraft:written_book`: resolves the slot's `WrittenBookContent`,
//     syncs the slot (`DataComponentWrittenBookContent = 61`), sends
//     `protocol.PlayOpenBook` (`0x37` in protocol 775) as well as `UseBookItem`,
//     and records `used:minecraft:written_book`.
//   - For `minecraft:writable_book`: syncs the slot (`DataComponentWritableBookContent = 60`)
//     and records `used:minecraft:writable_book`.
func (s *Server) OpenPlayerWrittenBook(session *PlayerSession, hand int32) bool {
	if s == nil || session == nil || session.IsDead() {
		return false
	}
	_, held := session.handSlotAndStack(hand)
	if held.IsEmpty() {
		return false
	}
	switch canonicalItemName(held.ResolvedID()) {
	case WrittenBookItemID:
		_ = s.UseBookItem(session, hand)
		if session.send != nil {
			_ = session.send(protocol.PlayOpenBook, EncodeOpenBookPacket(hand))
		}
		s.RecordItemUsed(session, WrittenBookItemID, 1)
		return true
	case WritableBookItemID:
		s.syncPlayerInventoryContainer(session)
		s.RecordItemUsed(session, WritableBookItemID, 1)
		return true
	default:
		return false
	}
}




