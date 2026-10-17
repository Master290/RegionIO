package server

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"

	"regionio/internal/nbt"
	"regionio/internal/protocol"
	"regionio/internal/registry"
	"regionio/internal/world"
)

// Serverbound player_action status values (protocol 775).
const (
	PlayerActionStartDestroyBlock = 0
	PlayerActionAbortDestroyBlock = 1
	PlayerActionStopDestroyBlock  = 2
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
	mu           sync.Mutex
	fallDistance float64
	dead         bool
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
		if prevState == world.StateBedrock || world.BlockName(prevState) == "minecraft:end_portal_frame" || world.BlockName(prevState) == "minecraft:end_portal" {
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

	broken, upd := cache.BreakBlockWithUpdates(x, y, z)
	if !broken {
		return false, 0
	}
	s.broadcastDimensionBlockChanges(dim, cache, upd.Changes, upd.LightChanged)
	s.CheckHangingEntitiesAroundBlock(dim, x, y, z)
	if IsBedBlock(prevState) {
		s.BreakBedPartner(dim, cache, x, y, z, prevState)
	}
	if IsChestBlock(prevState) {
		s.DropChestContents(x, y, z)
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
	var bannerDrop world.ItemStack
	var hasBannerDrop bool
	if IsBannerBlock(prevState) {
		bannerDrop, _, hasBannerDrop = s.RemoveBannerAt(dim, x, y, z)
	}

	if mode == GameModeSurvival {
		s.RecordBlockBreakExhaustion(session)
		s.RecordBlockMined(session, world.BlockName(prevState))
		var toolMeta ItemMeta
		var heldStack world.ItemStack
		heldSlot := session.HeldInventorySlot()
		if session != nil && heldSlot >= 0 {
			heldStack = session.InventorySlot(heldSlot)
			toolMeta = GetSlotItemMeta(session, heldSlot)
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
	scale := session.CurrentAttackStrengthScale(0.5)
	bonus := s.PlayerWeaponAttackBonus(session, target.TypeName) * scale

	dealt, killed := s.AttackEntity(session, targetEntityID)
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
// executes an enchantment-aware melee attack against the target entity.
func (s *Server) HandlePlayerAttackPacket(session *PlayerSession, r *protocol.Reader) error {
	entityID, err := r.VarInt()
	if err != nil {
		return err
	}
	if s != nil && session != nil {
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
		switch pkt.Action {
		case InteractActionAttack:
			s.AttackEntityWithEnchantments(session, pkt.EntityID)
		case InteractActionInteractAt:
			s.InteractWithDecorationAt(session, pkt.EntityID, pkt.Hand, float64(pkt.TargetX), float64(pkt.TargetY), float64(pkt.TargetZ), true)
		default:
			if pkt.Hand == 0 {
				_, _, _ = s.InteractWithEntity(session, pkt.EntityID)
			}
		}
	}
	return nil
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
			for _, slot := range changedSlots {
				stateID := session.NextContainerStateID()
				slotStack := session.InventorySlot(slot)
				_ = session.send(protocol.PlayContainerSetSlot, encodeContainerSetSlotPacket(stateID, slot, slotStack))
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

	isOutOfBorder := cause == "out_of_border" || cause == "outside_border" || cause == "world_border"
	if !isOutOfBorder {
		if prot := session.ArmorEnchantmentProtection(cause); prot > 0 {
			amount *= 1.0 - prot
			if amount <= 0 {
				return false
			}
		}
	}

	dim := session.Dimension()
	session.mu.Lock()
	if session.gameMode == GameModeCreative || session.gameMode == GameModeSpectator {
		session.mu.Unlock()
		return false
	}
	if session.health <= 0 {
		session.mu.Unlock()
		return false
	}
	newHealth := session.health - amount
	died := false
	if newHealth <= 0 {
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

	if died {
		s.RecordPlayerDeath(session)
		s.OnPlayerDiedWithEffects(session)
		s.DespawnPlayerFishingBobber(session)
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

	if snap.GameMode == GameModeCreative || snap.GameMode == GameModeSpectator {
		st.mu.Lock()
		st.fallDistance = 0
		st.mu.Unlock()
		return 0
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

	st := getSurvivalState(session)
	st.mu.Lock()
	st.dead = false
	st.fallDistance = 0
	st.mu.Unlock()

	session.SetFoodExhaustion(0)
	session.SetFoodTickTimer(0)
	session.SetBlockingWithShield(false)
	s.StopPlayerFallFlying(session)
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
